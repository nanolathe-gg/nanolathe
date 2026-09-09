package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/session"
)

// The Enhanced blend fraction is the budget's own input read un-floored:
// carry + phase × effective speed, with phase the elapsed part of the current
// scaled unit (docs/DESIGN_GPU_RENDERER.md §13.5) [01 §4.1][01 §4.2]. The
// scheduler's own timebase is floor(milliseconds × 30 / 1000), so the carry
// alone is identically zero at the nominal speed and would freeze the blend on
// the previous committed pose; these tests lock the finer value the client
// reads at Draw time.

// scriptedMillis is a host clock the test moves by hand, standing in for the
// monotonic source the budget and the scroll pass sample.
type scriptedMillis struct{ ms uint32 }

func (s *scriptedMillis) Millis32() uint32 { return s.ms }

func fractionHarness(active int32) (*battleSession, *scriptedMillis) {
	millis := &scriptedMillis{}
	return &battleSession{
		sess:         &session.Session{Clock: &clock.State{Requested: active, Active: active}},
		millisSource: millis,
	}, millis
}

// fractionStep advances the host clock, runs the budget exactly as the
// controller's session step does — the released ticks move the global tick the
// way the session's phases would, and the timing note follows — then reads the
// producer the way the client reads it at Draw time. It reports the ticks the
// budget released.
func (b *battleSession) fractionStep(millis *scriptedMillis, ms uint32) (int, float32) {
	millis.ms = ms
	ticks := b.sess.Clock.AdvanceSP(clock.ScaledNow(millis.Millis32()))
	b.sess.Clock.GlobalTick += uint32(ticks)
	b.noteTickTiming()
	return ticks, b.tickFraction()
}

func nearlyFraction(got, want float32) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 0.002
}

// The fraction measures from the moment the last tick fired, not from the wall
// clock's phase: ticks are released only inside the 30 Hz Update, whose timing
// drifts against that phase (§13.5). At the nominal speed one tick is 33.3 ms,
// so the fraction is the elapsed milliseconds since the fire times 0.03. These
// tests lock a Nanolathe presentation rule, not retail behaviour.
func TestTickFractionSweepsFromTheLastFire(t *testing.T) {
	b, millis := fractionHarness(10)
	// Before any tick has fired there is nothing to blend from.
	if ticks, got := b.fractionStep(millis, 33); ticks != 0 || got != 0 {
		t.Fatalf("ms=33 released %d ticks, fraction %.4f; want 0 and 0", ticks, got)
	}
	// The scaled unit advances at 41 ms: the tick fires and the fraction
	// starts from zero there, then climbs with the milliseconds since.
	if ticks, got := b.fractionStep(millis, 41); ticks != 1 || got != 0 {
		t.Fatalf("ms=41 released %d ticks, fraction %.4f; want 1 and 0", ticks, got)
	}
	for _, want := range []struct {
		ms       uint32
		fraction float32
	}{{49, 0.24}, {57, 0.48}, {66, 0.75}} {
		millis.ms = want.ms
		if got := b.tickFraction(); !nearlyFraction(got, want.fraction) {
			t.Fatalf("ms=%d fraction = %.4f, want %.4f", want.ms, got, want.fraction)
		}
	}
}

// An Update that releases no tick — the wall phase wrapped between two Updates
// — must not move the blend backwards: the fraction saturates at one and holds
// the current pose until the next fire, which restarts it from zero.
func TestTickFractionNeverMovesBackWithinOneTick(t *testing.T) {
	b, millis := fractionHarness(10)
	b.fractionStep(millis, 41) // first fire
	last := float32(0)
	for ms := uint32(42); ms < 100; ms++ {
		millis.ms = ms
		got := b.tickFraction()
		if got < last {
			t.Fatalf("ms=%d fraction %.4f moved back from %.4f with no tick fired", ms, got, last)
		}
		last = got
	}
	if last < 0.99 {
		t.Fatalf("fraction after 58 ms without a fire = %.4f, want saturated near 1", last)
	}
	// The next fire (scaled unit 3 at 100 ms) restarts the sweep.
	if ticks, got := b.fractionStep(millis, 100); ticks == 0 || got != 0 {
		t.Fatalf("ms=100 released %d ticks, fraction %.4f; want a fire and 0", ticks, got)
	}
}

// At a non-nominal speed the fire leaves a carry and the sweep runs at the
// effective rate; inside one tick the fraction still never moves backwards.
func TestTickFractionAtSevenTenthsSpeed(t *testing.T) {
	b, millis := fractionHarness(7)
	last := float32(-1)
	lastTick := uint32(0)
	fired := 0
	for ms := uint32(0); ms <= 400; ms += 4 {
		ticks, got := b.fractionStep(millis, ms)
		if got < 0 || got > 1 {
			t.Fatalf("ms=%d fraction = %.4f, want [0, 1]", ms, got)
		}
		if ticks == 0 && b.sess.Clock.GlobalTick == lastTick && got < last {
			t.Fatalf("ms=%d fraction %.4f moved back from %.4f inside one tick", ms, got, last)
		}
		if ticks != 0 {
			fired++
			if want := client.ClampTickFraction(b.sess.Clock.Carry); !nearlyFraction(got, want) {
				t.Fatalf("ms=%d fraction right after a fire = %.4f, want the carry %.4f", ms, got, want)
			}
		}
		last, lastTick = got, b.sess.Clock.GlobalTick
	}
	if fired == 0 {
		t.Fatal("no tick fired in 400 ms at seven tenths speed")
	}
}

// A paused battle runs no budget, so the producer returns the value it last
// returned unpaused and the blend freezes (§13.5).
func TestTickFractionHeldWhilePaused(t *testing.T) {
	b, millis := fractionHarness(10)
	b.fractionStep(millis, 41)
	millis.ms = 50
	held := b.tickFraction()
	b.sess.Clock.Paused = true
	millis.ms = 58
	if got := b.tickFraction(); got != held {
		t.Fatalf("paused fraction = %.4f, want the held %.4f", got, held)
	}
}
