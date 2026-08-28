package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestShakeRequestAndActiveTick locks the phase-10 driver arithmetic
// [R-CORE-01 §4.4.1] DET-04: exactly two CRT draws per active tick, linear
// decay sx = amp*remaining/duration, offsetX = rand()*sx/0x8000 − sx/2 with
// SIGNED truncating division, and the no-draw expiry tick.
func TestShakeRequestAndActiveTick(t *testing.T) {
	s := &Session{}
	s.SeedSessionRNG(1, 1)
	crt := s.CrtRNG()

	// Reference stream reproduces the expected draw values.
	ref := rng.NewCRT(1)

	s.RequestShake(-3, 4)
	if !s.shakeActive || s.shakeDuration != 2 || s.shakeRemaining != 2 {
		t.Fatalf("request: active=%v duration=%d remaining=%d, want true/2/2 (blend trunc((0+4)/2))", s.shakeActive, s.shakeDuration, s.shakeRemaining)
	}
	// Amplitudes accumulate on both axes from the single magnitude [R-CORE-01 §4.4.1].
	if s.shakeAmpX != -3 || s.shakeAmpY != -3 {
		t.Fatalf("amplitudes = (%d,%d), want (-3,-3)", s.shakeAmpX, s.shakeAmpY)
	}

	// Active tick: exactly two CRT draws; signed truncating arithmetic at an
	// ODD NEGATIVE sum (sx = -3*2/2 = -3; rand()*-3/0x8000 truncates toward
	// zero, − sx/2 = −(−1) = +1 — the shift-of-abs form would differ).
	draws0 := crt.Draws()
	ref0 := ref.Draws()
	s.tickShake(1)
	if crt.Draws()-draws0 != 2 {
		t.Fatalf("active tick consumed %d CRT draws, want exactly 2", crt.Draws()-draws0)
	}
	rx := int64(ref.Rand())
	_ = ref0
	sx := int64(-3)
	wantDx := int32(rx*int64(sx)/0x8000 - int64(sx)/2)
	ry := int64(ref.Rand())
	wantDy := int32(ry*int64(sx)/0x8000 - int64(sx)/2)
	if s.shakeOffsetX != wantDx || s.shakeOffsetY != wantDy {
		t.Fatalf("offset = (%d,%d), want signed-truncating (%d,%d)", s.shakeOffsetX, s.shakeOffsetY, wantDx, wantDy)
	}
	oddNegative := wantDx != int32(rx*int64(sx)/0x8000-int64(absForTest(sx)>>1))
	if !oddNegative {
		// The reference draw happened to make both forms agree; force the
		// discriminating case directly on the arithmetic.
		shiftForm := int32(rx*int64(sx)/0x8000 - int64(absForTest(sx)>>1))
		divForm := int32(rx*int64(sx)/0x8000 - int64(sx)/2)
		if shiftForm == divForm {
			t.Fatalf("test lost its discriminating case: shift %d == div %d for sx=%d", shiftForm, divForm, sx)
		}
		t.Fatalf("unexpected: shift and division agree for sx=%d", sx)
	}
	if s.shakeRemaining != 1 {
		t.Fatalf("remaining = %d, want 1 (linear decay)", s.shakeRemaining)
	}

	// Counter reaches zero: the next tick still draws (remaining 1 -> step),
	// then the tick AFTER the counter reaches zero clears the flag with NO draws.
	draws1 := crt.Draws()
	s.tickShake(2)
	if crt.Draws()-draws1 != 2 {
		t.Fatalf("final countdown tick consumed %d draws, want 2", crt.Draws()-draws1)
	}
	if s.shakeRemaining != 0 {
		t.Fatalf("remaining = %d, want 0", s.shakeRemaining)
	}
	draws2 := crt.Draws()
	s.tickShake(3)
	if crt.Draws() != draws2 {
		t.Fatal("expiry tick consumed CRT draws; it must clear the flag with none")
	}
	if s.shakeActive {
		t.Fatal("expiry tick must clear the active flag")
	}
}

func absForTest(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// TestShakeInactiveRequestClearsAmplitudes locks the request rule: when no
// shake is active the two amplitude accumulators are cleared and the duration
// blends with the current accumulator; the active flag is set only when the
// blended duration is positive [R-CORE-01 §4.4.1].
func TestShakeInactiveRequestClearsAmplitudes(t *testing.T) {
	s := &Session{}
	s.SeedSessionRNG(2, 2)
	s.shakeAmpX, s.shakeAmpY = 17, -9 // stale accumulators from a finished shake
	s.RequestShake(5, 6)
	if s.shakeAmpX != 5 || s.shakeAmpY != 5 {
		t.Fatalf("inactive request must clear then accumulate: (%d,%d), want (5,5)", s.shakeAmpX, s.shakeAmpY)
	}
	if s.shakeDuration != 3 { // trunc((6+0)/2)
		t.Fatalf("duration = %d, want 3", s.shakeDuration)
	}

	// Zero/negative blended duration leaves the shake inactive.
	s2 := &Session{}
	s2.RequestShake(5, 0)
	if s2.shakeActive {
		t.Fatal("zero duration must not activate shake")
	}
	_ = s2.CrtRNG()
}

// TestShakeConsumesNoSimDraws: the driver never touches the simulation
// stream [R-CORE-01 §4.4.1] (I4).
func TestShakeConsumesNoSimDraws(t *testing.T) {
	s := &Session{}
	s.SeedSessionRNG(3, 3)
	s.RequestShake(10, 6)
	sim0 := s.SimRNG().Draws()
	for i := 0; i < 10; i++ {
		s.tickShake(uint32(i))
	}
	if s.SimRNG().Draws() != sim0 {
		t.Fatal("shake consumed simulation draws")
	}
}
