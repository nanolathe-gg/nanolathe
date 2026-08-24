package clock

import "testing"

// The renderer's frame delta must reach the budget as scaled time, not as a
// second accumulator that reimplements the clamp. Driving the pair together is
// the only thing that proves the viewer inherits pause, speed and carry.

func TestFrameClockScalesMillisecondsToTicks(t *testing.T) {
	var f FrameClock
	// One second of 60 Hz frames is 30 scaled units [01 §4.2]. The floor lands
	// on 29 or 30 depending on where the accumulated float seconds fall
	// relative to the boundary — 1/60 is not exact in binary — which is the
	// same one-unit jitter retail's integer millisecond counter has.
	for i := 0; i < 60; i++ {
		f.Scaled(1.0 / 60.0)
	}
	if got := f.Scaled(0); got < 29 || got > 30 {
		t.Fatalf("one second of frames scaled to %d, want 29 or 30", got)
	}
	if ms := f.Millis(); ms < 999.9 || ms > 1000.1 {
		t.Fatalf("accumulated %v ms over one second", ms)
	}
}

// The sub-millisecond remainder is retained rather than truncated each frame.
// A per-frame integer truncation loses every one of these frames entirely.
func TestFrameClockKeepsSubMillisecondFrames(t *testing.T) {
	var f FrameClock
	for i := 0; i < 1500; i++ {
		f.Scaled(0.0001) // 0.1 ms per frame, 150 ms in total -> 4.5 scaled
	}
	if got := f.Scaled(0); got != 4 {
		t.Fatalf("1500 sub-millisecond frames scaled to %d, want 4", got)
	}
}

func TestViewerBudgetInheritsPauseAndSpeed(t *testing.T) {
	var f FrameClock
	s := &State{Requested: 10, Active: 10}

	// A normal 1/30 s frame yields one tick.
	if got := s.AdvanceSP(f.Scaled(1.0 / 30.0)); got != 1 {
		t.Fatalf("one frame budget %d, want 1", got)
	}

	// Pause stalls the budget entirely [01 §4.3] C4. A private float64
	// accumulator in the viewer would keep handing out ticks here.
	s.Paused = true
	for i := 0; i < 30; i++ {
		if got := s.AdvanceSP(f.Scaled(1.0 / 30.0)); got != 0 {
			t.Fatalf("paused budget %d, want 0", got)
		}
	}

	// Unpause yields at most one capped burst of five, not the whole second.
	s.Paused = false
	if got := s.AdvanceSP(f.Scaled(1.0 / 30.0)); got != 5 {
		t.Fatalf("unpause burst %d, want the 0..5 clamp to cap it at 5", got)
	}
}

func TestFrameClockIgnoresNonPositiveDeltas(t *testing.T) {
	var f FrameClock
	f.Scaled(1.0)
	before := f.Millis()
	f.Scaled(-1.0)
	f.Scaled(0)
	if f.Millis() != before {
		t.Fatalf("a negative or zero frame advanced the clock: %v then %v", before, f.Millis())
	}
}
