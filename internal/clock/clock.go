// Package clock implements the retail 30 Hz fixed-step budget.
//
// This is the single multiplayer-aware timebase that decides how many
// simulation sub-ticks run each pump iteration. Rendering interpolates between
// ticks; simulation never reads wall-clock state [01 §4.2], [01 §4.3], [01 §4.4].
package clock

import (
	"encoding/binary"
	"math"
)

// State holds the scheduler block that the budget mutates.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Only the fields required by PLAN_03 are exported; pending, slew and flags
// round-trip through SaveBox/LoadBox to preserve the 28-byte contract (C14).
type State struct {
	ScaledAnchor int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Delta        int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Carry        float32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	GlobalTick   uint32  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Requested    int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Active       int32   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Paused       bool    // TODO(question): Historical analysis omitted; independently worded behavior is needed.

	// Internal scheduler state that round-trips through the 28-byte save box
	// but is not part of the PLAN_03 public struct literal.
	pending int32  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	slew    int16  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	flags   uint16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
}

func clampSpeed(v int32) int32 {
	if v < 1 {
		return 1
	}
	if v > 20 {
		return 20
	}
	return v
}

func (s *State) effectiveSpeedLocked() float64 {
	// Active is already clamped to 1..20 by callers; clamp defensively.
	a := clampSpeed(s.Active)
	return float64(a) * 0.1 // [01 §4.2] nominal 10 => 1.0
}

// lagThrottleFactor implements the MP remote-progress throttling expression
// [01 §4.2] max(0.01, (3600 - min(lag,3600)) / 2700) for T24.
// Retail applies it only when lag >= 900; otherwise factor is 1.0.
// In single-player there is no remote lag, so factor stays 1.0.
func lagThrottleFactor(lag uint32) float64 {
	if lag < 900 {
		return 1.0
	}
	if lag > 3600 {
		lag = 3600
	}
	f := float64(3600-lag) / 2700.0 // [01 §4.2]
	if f < 0.01 {
		f = 0.01
	}
	return f
}

// updateFlags recomputes low flag bits from current state and preserves
// upper bits from the stored flags. Bit 0 pause, bit 1 lag throttle, bit 2
// pending-speed mismatch [08 "Scheduler and random state in saves"].
func (s *State) updateFlags() {
	preserved := s.flags &^ uint16(0x07)
	var f uint16
	if s.Paused {
		f |= 1
	}
	// Lag throttle flag (bit 1) — no network presently, so lag=0 => flag 0.
	// Keep the branch so the expression is not lost for T24.
	var lag uint32 = 0 // TODO(T24): replace with real oldest remote progress lag
	if lag >= 900 {
		f |= 2
	}
	if clampSpeed(s.Requested) != clampSpeed(s.Active) {
		f |= 4
	}
	s.flags = preserved | f
}

// applyHysteresis updates the speed-slew counter and may step Active
// toward Requested or down under sustained capped load [01 §4.3].
// Called only when not paused.
func (s *State) applyHysteresis(trunc int32) {
	isCapped := trunc >= 6 // raw trunc before clamp [01 §4.3]
	if isCapped {
		if s.slew < 32767 {
			s.slew++
		}
		if s.slew > 10 {
			if s.Active > 1 {
				s.Active--
				if s.Active < 1 {
					s.Active = 1
				}
			}
			s.slew = 0
		}
	} else {
		if s.slew > -32768 {
			s.slew--
		}
		if s.slew < -100 { // >100 normal observations [01 §4.3]
			if s.Active < s.Requested {
				s.Active++
			} else if s.Active > s.Requested {
				s.Active--
			}
			if s.Active < 1 {
				s.Active = 1
			} else if s.Active > 20 {
				s.Active = 20
			}
			s.slew = 0
		}
	}
}

// budget computes raw = double(delta)*effectiveSpeed + double(carry) [01 §4.2],
// truncates toward zero (__ftol) [01 §8], stores remainder as float32 carry,
// and clamps the integer to 0..5. It returns the pre-clamp trunc and clamped
// ticks. Caller updates anchor/delta and hysteresis.
func (s *State) budget(delta int32, eff float64) (int32, int) {
	// I2 allowlist: clock budget product is float64, carry is float32.
	raw := float64(delta)*eff + float64(s.Carry) // [01 §4.2]
	trunc := int32(raw)                          // trunc toward zero [01 §8], I3
	rem := raw - float64(trunc)
	s.Carry = float32(rem) // [01 §4.2] store raw - trunc as float32
	ticks := int(trunc)
	if ticks < 0 {
		ticks = 0 // [01 §4.2] negative wrap delta clamps to 0, C2
	} else if ticks > 5 {
		ticks = 5 // [01 §4.2] clamp 0..5, excess dropped C1
	}
	return trunc, ticks
}

// AdvanceSP is the single-player budget path [01 §4.3], [GAP T12].
// It short-circuits behind the pause test, stalling anchor, delta and carry,
// so unpause yields at most one capped burst of 5 ticks (C4).
func (s *State) AdvanceSP(scaledNow int32) int {
	if s.Paused {
		// SP pause: do not evaluate budget at all; wall-clock seen on next
		// unpaused call becomes the burst. Return 0 and keep carry/anchor.
		s.updateFlags()
		s.pending = 0
		return 0
	}
	// Clamp speeds 1..20 [01 §4.3] C3. Preserve caller-visible values clamped.
	s.Requested = clampSpeed(s.Requested)
	s.Active = clampSpeed(s.Active)

	delta := scaledNow - s.ScaledAnchor // signed delta; wrap becomes negative C2
	eff := s.effectiveSpeedLocked()     // active *0.1 [01 §4.2]
	// SP has no lag throttle.

	trunc, ticks := s.budget(delta, eff)

	// Hysteresis when not paused [01 §4.3].
	s.applyHysteresis(trunc)

	// Commit scheduler state.
	s.ScaledAnchor = scaledNow
	s.Delta = delta
	s.pending = int32(ticks)
	s.updateFlags()
	return ticks
}

// AdvanceMP is the multiplayer budget path [01 §4.3], [GAP T12].
// It evaluates the budget every iteration even while paused, so the anchor
// tracks wall-clock and only the sub-1.0 carry survives. The integer is
// discarded while paused, so no burst occurs on unpause (C4). MP lag
// throttling expression is kept for T24 [01 §4.2] C3.
func (s *State) AdvanceMP(scaledNow int32) int {
	// Clamp speeds defensively.
	s.Requested = clampSpeed(s.Requested)
	s.Active = clampSpeed(s.Active)

	delta := scaledNow - s.ScaledAnchor
	eff := s.effectiveSpeedLocked()
	// MP lag throttling — keep expression for T24, currently lag=0 => factor 1.
	eff *= lagThrottleFactor(0)

	trunc, ticks := s.budget(delta, eff)

	if !s.Paused {
		s.applyHysteresis(trunc)
	}

	// Anchor, delta and carry advance even while paused [01 §4.3].
	s.ScaledAnchor = scaledNow
	s.Delta = delta
	s.pending = int32(ticks)

	s.updateFlags()

	if s.Paused {
		// Discard integer while keeping carry [01 §4.3].
		s.pending = 0
		return 0
	}
	return ticks
}

// SaveBox serializes the scheduler block to the exact 28-byte little-endian
// layout used by the Players/GameTime box [08 "Scheduler and random state in saves"],
// [01 §7.3]. RNG state is not saved (C14).
func (s *State) SaveBox() [28]byte {
	// Ensure flags reflect current paused/mismatch bits before snapshot.
	s.updateFlags()
	// Ensure speeds are within saveable i16 range.
	req := int16(clampSpeed(s.Requested))
	act := int16(clampSpeed(s.Active))

	var b [28]byte
	binary.LittleEndian.PutUint32(b[0:4], uint32(s.ScaledAnchor))
	binary.LittleEndian.PutUint32(b[4:8], uint32(s.pending))
	binary.LittleEndian.PutUint32(b[8:12], uint32(s.Delta))
	binary.LittleEndian.PutUint32(b[12:16], math.Float32bits(s.Carry))
	binary.LittleEndian.PutUint32(b[16:20], uint32(s.GlobalTick))
	binary.LittleEndian.PutUint16(b[20:22], uint16(req))
	binary.LittleEndian.PutUint16(b[22:24], uint16(act))
	binary.LittleEndian.PutUint16(b[24:26], uint16(s.slew))
	binary.LittleEndian.PutUint16(b[26:28], s.flags)
	return b
}

// LoadBox restores the scheduler block from a 28-byte box [08 "Scheduler and random state in saves"].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// If the on-disk box is larger, the caller must truncate; if shorter, the budget
// restores nothing — we require exactly 28 bytes (caller validates).
func (s *State) LoadBox(b [28]byte) {
	s.ScaledAnchor = int32(binary.LittleEndian.Uint32(b[0:4]))
	s.pending = int32(binary.LittleEndian.Uint32(b[4:8]))
	s.Delta = int32(binary.LittleEndian.Uint32(b[8:12]))
	s.Carry = math.Float32frombits(binary.LittleEndian.Uint32(b[12:16]))
	s.GlobalTick = binary.LittleEndian.Uint32(b[16:20])
	s.Requested = int32(int16(binary.LittleEndian.Uint16(b[20:22])))
	s.Active = int32(int16(binary.LittleEndian.Uint16(b[22:24])))
	s.slew = int16(binary.LittleEndian.Uint16(b[24:26]))
	s.flags = binary.LittleEndian.Uint16(b[26:28])
	s.Paused = s.flags&1 != 0

	// Clamp speeds after load [01 §4.3] C3.
	s.Requested = clampSpeed(s.Requested)
	s.Active = clampSpeed(s.Active)
}

// BeginSubTick increments the global simulation tick and returns its new
// value. It is the single writer of GlobalTick.
//
// [01 §4.4]: "each sub-tick increments the global tick before any phase runs"
// (C6). The kernel calls this once per sub-tick, before phase 1; nothing else
// may advance the counter. Keeping it here rather than in the kernel means the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [08 "Scheduler and random state in saves"] (C14).
func (s *State) BeginSubTick() uint32 {
	s.GlobalTick++
	return s.GlobalTick
}

// ScaledNow converts a GetTickCount millisecond value to the engine's
// scaled timebase floor(tickCount *30/1000) [01 §4.1]. Kept here so the
// conversion stays in one place and no other package invents its own.
func ScaledNow(tickCount uint32) int32 {
	return int32((uint64(tickCount) * 30) / 1000)
}
