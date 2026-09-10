package ebitenapp

import (
	"testing"
	"time"
)

// These tests lock the window half of the record/submit pipeline
// (docs/DESIGN_GPU_RENDERER.md §13.10): where an update body runs, how wide the
// window's fraction tolerance is, and that a prediction at the end of an update
// period lands on the same side of the client's clamp as the measured value.

// The ledger's whole job is arithmetic: one body per Update call, wherever the
// body runs. The simulation may not step twice for one update period and may
// not skip one, so a run of frames at four Draws per update must answer every
// counted call exactly once and never owe more than one at a time.
func TestUpdateLedgerRunsOneBodyPerUpdateCall(t *testing.T) {
	var l updateLedger
	calls, bodies := 0, 0
	for frame := range 400 {
		if frame%4 == 0 {
			calls++
			for range l.call(true) {
				bodies++
			}
		}
		if l.tail() {
			bodies++
		}
		if l.owed > 1 {
			t.Fatalf("frame %d: %d update bodies owed at once; one call may only ever owe one", frame, l.owed)
		}
	}
	if bodies != calls {
		t.Fatalf("%d bodies for %d Update calls; the simulation stepped a different number of times than the host asked for", bodies, calls)
	}
	// In the steady state every body belongs to a Draw tail, which is the whole
	// point: the tail runs it before the launch rather than after it.
	if calls < 90 {
		t.Fatalf("only %d Update calls simulated; the run is too short to mean anything", calls)
	}
}

// A window that stopped presenting — the classic executor, an occluded Draw —
// must not defer bodies to a tail that never comes. The ledger runs them in the
// Update call instead, and catches up rather than losing a step.
func TestUpdateLedgerRunsInlineWhenTheDrawTailStops(t *testing.T) {
	var l updateLedger
	// Warm up into the deferring steady state.
	l.tail()
	if n := l.call(true); n != 0 {
		t.Fatalf("a call with a live tail ran %d bodies inline; it should have deferred", n)
	}
	if !l.tail() {
		t.Fatal("the Draw tail did not take the body it was owed")
	}
	// Now the tail stops. The first call still defers — it cannot know yet —
	// and the second must run both.
	if n := l.call(true); n != 0 {
		t.Fatalf("the first call after the tail stopped ran %d bodies; it cannot know the tail is gone", n)
	}
	if n := l.call(true); n != 2 {
		t.Fatalf("the second call ran %d bodies, want the 2 that were owed", n)
	}
	// With no tail since, every later call runs its own body immediately.
	for i := range 3 {
		if n := l.call(true); n != 1 {
			t.Fatalf("call %d after the tail stopped ran %d bodies, want 1", i, n)
		}
	}
	// The classic executor never defers at all.
	l.tail()
	if n := l.call(false); n != 1 {
		t.Fatalf("a non-deferrable call ran %d bodies, want 1", n)
	}
}

// The tolerance is one present interval expressed in the blend's units: the
// fractions advance by period × 30 of a whole across one present, and the
// window accepts a prediction that far out because that is the bound on present
// jitter it can still call the same frame.
func TestFractionToleranceIsOnePresentInterval(t *testing.T) {
	cases := []struct {
		period time.Duration
		want   int32
	}{
		{0, 0},
		{-time.Millisecond, 0},
		// 120 Hz: a quarter of a 30 Hz update.
		{time.Second / 120, fractionOne / 4},
		// 60 Hz: half of one.
		{time.Second / 60, fractionOne / 2},
		// A present as long as a whole update saturates just under one, which
		// is the client's own domain.
		{time.Second / 30, fractionOne - 1},
		{time.Second, fractionOne - 1},
	}
	for _, c := range cases {
		got := fractionTolerance(c.period)
		// The floating conversion may land one quantum either side.
		if got < c.want-1 || got > c.want+1 {
			t.Fatalf("fractionTolerance(%v) = %d quanta, want about %d", c.period, got, c.want)
		}
	}
	// One millisecond of draw jitter — the tick fraction producer's own
	// resolution — has to fit inside a 120 Hz tolerance, or the window would
	// miss on a single step of the number it is comparing.
	if q := fractionTolerance(time.Second / 120); q < fractionOne*30/1000 {
		t.Fatalf("a 120 Hz tolerance of %d quanta is under the producer's own millisecond step; every frame would miss", q)
	}
}

// The tolerance is one NOMINAL present interval — the cap, the display's rate,
// or the wider of the two — and only falls back to the last launch's own
// measurement. A frame that hitched must not widen its own tolerance by the
// lateness the cap exists to catch.
func TestTolerancePeriodIsTheNominalPresentInterval(t *testing.T) {
	var p pipeline
	if d := p.tolerancePeriod(0, 0); d != 0 {
		t.Fatalf("a window with no rate at all = %v, want 0 (the exact path)", d)
	}
	if d := p.tolerancePeriod(0, 120); d != time.Second/120 {
		t.Fatalf("display rate = %v, want %v", d, time.Second/120)
	}
	// A cap wider than the display's interval is what the window presents at.
	if d := p.tolerancePeriod(time.Second/30, 120); d != time.Second/30 {
		t.Fatalf("capped window = %v, want the cap %v", d, time.Second/30)
	}
	// A cap the display cannot reach does not shorten the interval.
	if d := p.tolerancePeriod(time.Second/240, 120); d != time.Second/120 {
		t.Fatalf("cap under the display's rate = %v, want %v", d, time.Second/120)
	}
	// A hitched frame's own measurement never widens the tolerance while a
	// nominal rate is known.
	p.launchPeriod = 31 * time.Millisecond
	if d := p.tolerancePeriod(0, 60); d != time.Second/60 {
		t.Fatalf("hitched launch period = %v, want the nominal %v; a hitch would be presented most of a tick out", d, time.Second/60)
	}
	// With no nominal rate the last launch's measurement is the last resort.
	if d := p.tolerancePeriod(0, 0); d != 31*time.Millisecond {
		t.Fatalf("last-resort period = %v, want %v", d, 31*time.Millisecond)
	}
	p.launchPeriod = time.Second
	if d := p.tolerancePeriod(0, 0); d != presentPeriodCeiling {
		t.Fatalf("a stalled measurement = %v, want the ceiling %v", d, presentPeriodCeiling)
	}
}

// The last presented frame of an update period saturates both fractions at the
// client's clamp. The prediction must saturate there too, or it could never
// compare equal to what that frame measures — and declining instead is what
// used to cost the window every crossing frame, before the update body moved
// into the Draw tail.
func TestPredictNextClampsAtTheEndOfAnUpdatePeriod(t *testing.T) {
	var p pipeline
	now := time.Now()
	updatedAt := now.Add(-30 * time.Millisecond)
	// The next Draw lands 8.3 ms later, past the end of this 33.3 ms update.
	tick16, camera16, ok := p.predictNext(now.Add(time.Second/120), now, updatedAt, fractionOne-fractionOne/8)
	if !ok {
		t.Fatal("a prediction at the end of an update period declined; every crossing frame would record synchronously")
	}
	if camera16 != fractionOne-1 {
		t.Fatalf("camera fraction = %d, want the clamp %d", camera16, fractionOne-1)
	}
	if tick16 != fractionOne-1 {
		t.Fatalf("tick fraction = %d, want the clamp %d", tick16, fractionOne-1)
	}
	// Mid-period the prediction is the extrapolation itself, not the clamp.
	updatedAt = now.Add(-8 * time.Millisecond)
	tick16, camera16, ok = p.predictNext(now.Add(time.Second/120), now, updatedAt, fractionOne/4)
	if !ok {
		t.Fatal("a mid-period prediction declined")
	}
	if camera16 <= fractionOne/4 || camera16 >= fractionOne-1 {
		t.Fatalf("camera fraction = %d, want a value inside the period", camera16)
	}
	if want := fractionOne / 4; tick16 <= want || tick16 >= want+fractionOne/2 {
		t.Fatalf("tick fraction = %d, want about a quarter of a tick past %d", tick16, want)
	}
	// Without an update to measure from there is nothing to predict against.
	if _, _, ok := p.predictNext(now.Add(time.Second/120), now, time.Time{}, 0); ok {
		t.Fatal("a prediction was launched before the first update")
	}
}
