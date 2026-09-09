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
// controller's session step does, then reads the producer the way the client
// reads it at Draw time. It reports the ticks the budget released.
func (b *battleSession) fractionStep(millis *scriptedMillis, ms uint32) (int, float32) {
	millis.ms = ms
	ticks := b.sess.Clock.AdvanceSP(clock.ScaledNow(millis.Millis32()))
	return ticks, b.tickFraction()
}

func nearlyFraction(got, want float32) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 0.002
}

// At the nominal speed the effective multiplier is one, so the fraction is the
// phase alone and sweeps the tick instead of sitting at the carry's zero.
func TestTickFractionSweepsTheTickAtNominalSpeed(t *testing.T) {
	b, millis := fractionHarness(10)
	// (ms × 30 mod 1000) / 1000: 0, 0.24, 0.48, 0.75, 0.99. The scaled unit has
	// not advanced yet at 33 ms, because floor(33 × 30 / 1000) is still 0.
	for _, want := range []struct {
		ms       uint32
		fraction float32
	}{{0, 0}, {8, 0.24}, {16, 0.48}, {25, 0.75}, {33, 0.99}} {
		ticks, got := b.fractionStep(millis, want.ms)
		if ticks != 0 {
			t.Fatalf("ms=%d released %d ticks before the scaled unit advanced", want.ms, ticks)
		}
		if !nearlyFraction(got, want.fraction) {
			t.Fatalf("ms=%d fraction = %.4f, want %.4f", want.ms, got, want.fraction)
		}
	}
	// The scaled unit advances at 41 ms: the tick fires and the fraction wraps
	// back near the start of the next tick instead of continuing to climb.
	ticks, got := b.fractionStep(millis, 41)
	if ticks != 1 {
		t.Fatalf("ms=41 released %d ticks, want 1", ticks)
	}
	if !nearlyFraction(got, 0.23) {
		t.Fatalf("fraction after the tick fired = %.4f, want %.4f", got, float32(0.23))
	}
}

// At a non-nominal speed the carry is nonzero and the phase is scaled by the
// effective speed. The sum stays inside [0, 1) — the clamp is part of the
// formula, and above a large carry the sum does reach past one — and it never
// moves backwards inside one tick, which is what makes it usable as a position
// within that tick.
func TestTickFractionAtSevenTenthsSpeed(t *testing.T) {
	b, millis := fractionHarness(7)
	last := float32(-1)
	for ms := uint32(0); ms <= 60; ms += 4 {
		ticks, got := b.fractionStep(millis, ms)
		if got < 0 || got >= 1 {
			t.Fatalf("ms=%d fraction = %.4f, want [0, 1)", ms, got)
		}
		phase := float32((uint64(ms)*30)%1000) / 1000
		want := client.ClampTickFraction(b.sess.Clock.Carry + phase*0.7)
		if !nearlyFraction(got, want) {
			t.Fatalf("ms=%d fraction = %.4f, want clamp(carry + phase × 0.7) = %.4f", ms, got, want)
		}
		if ticks == 0 && got < last {
			t.Fatalf("ms=%d fraction %.4f moved back from %.4f inside one tick", ms, got, last)
		}
		last = got
	}
}

// A paused battle runs no budget, so the producer returns the value it last
// returned unpaused and the blend freezes (§13.5).
func TestTickFractionHeldWhilePaused(t *testing.T) {
	b, millis := fractionHarness(10)
	_, held := b.fractionStep(millis, 16)
	b.sess.Clock.Paused = true
	millis.ms = 25
	if got := b.tickFraction(); got != held {
		t.Fatalf("paused fraction = %.4f, want the held %.4f", got, held)
	}
}
