package clock

import (
	"encoding/binary"
	"errors"
	"math"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// ErrMalformedBox reports a scheduler image that cannot represent the defined
// scheduler state. Checked loading validates into a temporary state first, so
// callers never observe a partially applied image [08 "Scheduler and random
// state in saves"].
var ErrMalformedBox = errors.New("clock: malformed scheduler box")

// MillisSource is the platform-neutral boundary for the wrapping host
// millisecond counter. Presentation or platform code supplies the source;
// the simulation only consumes the scaled integer returned by ScaledNow
// [01 §4.1][01 §4.2].
type MillisSource interface {
	Millis32() uint32
}

// State holds the scheduler block that the budget mutates.
// Only the fields required by PLAN_03 are exported; pending, slew and flags
// round-trip through SaveBox/LoadBox to preserve the 28-byte contract (C14).
type State struct {
	ScaledAnchor int32   // last scaledNow [01 §4.2]
	Delta        int32   // last scaled delta [01 §4.2]
	Carry        float32 // fractional carry, float32 per [01 §4.2] (I2 allowlist)
	GlobalTick   uint32  // authoritative global tick, incremented before phase 1 [01 §4.4]
	Requested    int32   // requested speed 1..20 [01 §4.3]
	Active       int32   // active speed 1..20 [01 §4.3]
	Paused       bool    // pause gate, bit 0 of scheduler flags [01 §4.3]

	// Internal scheduler state that round-trips through the 28-byte save box
	// but is not part of the PLAN_03 public struct literal.
	pending int32  // clamped ticks to run 0..5
	slew    int16  // speed-slew hysteresis counter [01 §4.3]
	flags   uint16 // scheduler flags bits 0..15 (bit0 pause, bit1 lag, bit2 pending-speed)
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

// updateFlags recomputes the locally-owned pause and pending-speed bits while
// preserving the lag bit and all other flags. Lag is owned by the multiplayer
// dispatcher, so the clock must not erase a valid serialized value it does not
// model [08 "Scheduler and random state in saves"].
func (s *State) updateFlags() {
	preserved := s.flags &^ uint16(0x05)
	var f uint16
	if s.Paused {
		f |= 1
	}
	// Bit 1 is the lag-throttle flag. It is not clock-owned while network
	// progress is supplied by the multiplayer dispatcher; preserve it.
	f |= s.flags & 2
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
			// Normal observations only raise active speed toward the request.
			// The retail branch is intentionally not symmetric: an active
			// value above the request is left alone [01 §4.3].
			if s.Active < s.Requested {
				s.Active++
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
// floors the saved binary64 before signed-64/low-word narrowing, stores the
// remainder against that floating floor as float32, and clamps the signed
// word to 0..5 [01 R-DET-01 §3]. Caller updates anchor/delta and hysteresis.
func (s *State) budget(delta int32, eff float64) (int32, int) {
	// I2 allowlist: clock budget product is float64, carry is float32.
	// Explicit stores also prevent a host fused multiply-add from skipping
	// the working-precision product rounding [01 §4.2][01 R-DET-01 §3].
	product := float64(float64(delta) * eff)
	raw := float64(product + float64(s.Carry))
	floored := math.Floor(raw)
	trunc := numeric.TruncateFloat64ToLow32(floored)
	// Carry uses the floating result, before the integer word can wrap.
	s.Carry = float32(raw - floored) // [01 §4.2]
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

// LoadBox restores a valid scheduler block from a 28-byte box [08
// "Scheduler and random state in saves"]. Invalid values are rejected without
// mutation; callers that need the error should use LoadBoxChecked. The fixed
// array preserves the existing API and makes short input impossible here.
func (s *State) LoadBox(b [28]byte) {
	_ = s.LoadBoxChecked(b)
}

// LoadBoxChecked validates and restores a scheduler block transactionally.
// Valid fields are copied bit-for-bit, including the float32 carry and opaque
// scheduler flag bits. The save reader owns any larger-box prefix handling;
// this API accepts exactly the defined 28-byte value [08 "Scheduler and random
// state in saves"].
func (s *State) LoadBoxChecked(b [28]byte) error {
	decoded, err := decodeBox(b)
	if err != nil {
		return err
	}
	*s = decoded
	return nil
}

// LoadBoxBytes is the slice form of LoadBoxChecked. It accepts the defined
// 28-byte prefix from an oversized save box and rejects short input [08
// "Scheduler and random state in saves"].
func (s *State) LoadBoxBytes(data []byte) error {
	if len(data) < 28 {
		return ErrMalformedBox
	}
	var box [28]byte
	copy(box[:], data[:28])
	return s.LoadBoxChecked(box)
}

func decodeBox(b [28]byte) (State, error) {
	decoded := State{
		ScaledAnchor: int32(binary.LittleEndian.Uint32(b[0:4])),
		pending:      int32(binary.LittleEndian.Uint32(b[4:8])),
		Delta:        int32(binary.LittleEndian.Uint32(b[8:12])),
		Carry:        math.Float32frombits(binary.LittleEndian.Uint32(b[12:16])),
		GlobalTick:   binary.LittleEndian.Uint32(b[16:20]),
		Requested:    int32(int16(binary.LittleEndian.Uint16(b[20:22]))),
		Active:       int32(int16(binary.LittleEndian.Uint16(b[22:24]))),
		slew:         int16(binary.LittleEndian.Uint16(b[24:26])),
		flags:        binary.LittleEndian.Uint16(b[26:28]),
	}
	// The pending count is the already-clamped work for the next pump. Speed
	// values are the common setter's inclusive 1..20 range. Carry is the
	// floating floor remainder narrowed to float32; a value just below one
	// can round to exactly one. Keep accepting the previously supported
	// negative fractional images too [01 §4.2][01 §4.3].
	if decoded.pending < 0 || decoded.pending > 5 ||
		decoded.Requested < 1 || decoded.Requested > 20 ||
		decoded.Active < 1 || decoded.Active > 20 ||
		math.IsNaN(float64(decoded.Carry)) || math.IsInf(float64(decoded.Carry), 0) ||
		decoded.Carry <= -1 || decoded.Carry > 1 {
		return State{}, ErrMalformedBox
	}
	decoded.Paused = decoded.flags&1 != 0
	return decoded, nil
}

// BeginSubTick increments the global simulation tick and returns its new
// value. It is the single writer of GlobalTick.
//
// [01 §4.4]: "each sub-tick increments the global tick before any phase runs"
// (C6). The kernel calls this once per sub-tick, before phase 1; nothing else
// may advance the counter. Keeping it here rather than in the kernel means the
// value SaveBox persists is the same value the phases observed
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
