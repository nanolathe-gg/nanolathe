// Ground steering [04 §8.1] C20, C21.
//
// SteerState is the explicit integration surface that retail scatters across
// the unit record. The orchestrator will unify this with units.Unit once that
// type grows velocity/heading/speed fields. Retail offsets are noted where
// established so the unification is mechanical.
//
// Mapping to retail [04 §8.1][04 §5.1][02 "Unit record"]. Per I13 a field
// shared by two clean-room contracts resolves to one Go field through its
// logical name and citations, never through the executable's record layout:
//
//	X,Z                  world position, 16.16 (X/Z pair, Fixed) [04 §8.1]
//	Heading              current heading, uint16 0..65535 per circle [04 §5.1][04 §8.1] C20
//	PendingHeading       pending heading, updated before integration [04 §8.1] C20
//	Dirty                dirty movement flag, set before integration [04 §8.1] C20
//	Speed                scalar speed word, fixed 16.16, capped by pitch table [04 §8.1] C21
//	MaxVelocity          definition MaxVelocity fixed 16.16 [02 "Unit record"] [04 §8.1] C21
//	TurnRate             definition TurnRate integer [02 "Unit record"] [04 §8.1] C20 clamp
//	HeightWord           the unit's signed integer height — the signed high
//	                     word of its 16.16 Y [04 §8.1][04 R-MOV-01 §4] C21
//	SeaLevel             the map's sea-level byte [fmt tnt][04 §8.1] C21 — value 0..255
//	DefFlags             definition flags word [04 §8.1][04 R-MOV-01 §8a] C21;
//	                     bit 0x1000 canhover, 0x80000 floater, mask 0x81000
//	Pitch                the flight lean accumulator's derived pitch word;
//	                     unused by ground steering [04 R-AIR-01 §2] C21
//	Bank                 the flight lean accumulator's derived bank word;
//	                     unused by ground steering [04 R-AIR-01 §2]
//	PitchScale/BankScale definition lean-scale factors, flight only
//	                     [04 R-AIR-01 §2][02 "Unit record"]
//	Acceleration/BrakeRate definition acceleration/braking rates, fixed 16.16
//	                     [02 "Unit record"][04 R-MOV-01 §1][04 R-MOV-01 §4]
//	ResidualX/Y/Z        the flight lean accumulator's three components,
//	                     flight only [04 R-AIR-01 §2]

package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Flag bits for the below-water halving gate [04 §8.1] C21.
const (
	flagCanHover = 0x1000                     // canhover [04 §8.1] C21
	flagFloater  = 0x80000                    // floater [04 §8.1] C21
	flagNoHalve  = flagCanHover | flagFloater // 0x81000 [04 §8.1] C21 neither set => halve
)

// pitchTable is the exact pitch speed percent table [04 §8.1] C21.
// Index -5..+5 maps to offset +5 = 0..10; values are signed bytes
// 25,55,70,85,100,100,75,50,25,20,15 in index order -5 through +5.
// Note the asymmetric middle: both -1 and 0 are 100 [04 §8.1] C21.
var pitchTable = [11]int32{25, 55, 70, 85, 100, 100, 75, 50, 25, 20, 15} // [04 §8.1] C21

// SteerState holds the mutable ground steering state. See package comment
// for retail offset mapping. All fixed values are raw 16.16 int32 words unless
// noted. Angles are uint16 0..65535 per circle [04 §5.1].
type SteerState struct {
	X, Z int32 // position 16.16 [04 §8.1]

	Heading        uint16 // current heading [04 §5.1][04 §8.1] C20
	PendingHeading uint16 // pending heading, updated before integration [04 §8.1] C20
	Dirty          bool   // dirty movement flag, set before integration [04 §8.1] C20

	Speed       int32 // scalar speed word fixed 16.16 [04 §8.1] C20 C21
	MaxVelocity int32 // definition MaxVelocity fixed 16.16 [02 "Unit record"] [04 §8.1] C21
	TurnRate    int32 // definition TurnRate integer [02 "Unit record"] [04 §8.1] C20

	HeightWord int16  // signed height high word (Y>>16) [04 §8.1][04 R-MOV-01 §4] C21
	SeaLevel   uint8  // sea-level byte [04 §8.1] C21 0..255
	DefFlags   uint32 // definition flags word [04 §8.1] C21; gate 0x81000

	Acceleration int32 // definition Acceleration, fixed 16.16 [02 "Unit record"][04 R-MOV-01 §1][04 R-MOV-01 §4] M3
	BrakeRate    int32 // definition BrakeRate, fixed 16.16 [02 "Unit record"][04 R-MOV-01 §1][04 R-MOV-01 §4] M3
}

// PitchIndex maps a signed height delta to the clamped table index [04 §8.1] C21.
// pitch = arithmeticShift(delta, 11) clamped to [-5,+5].
// The shift is a signed arithmetic shift (sign-extending), not a logical shift [04 §8.1] C21, I3.
func PitchIndex(delta int32) int { // [04 §8.1] C21
	idx := int(delta >> 11) // arithmetic shift preserves sign [04 §8.1] C21
	if idx < -5 {
		idx = -5
	} else if idx > 5 {
		idx = 5
	}
	return idx
}

// pitchPercent returns the table percentage for a clamped index [04 §8.1] C21.
func pitchPercent(idx int) int32 { // [04 §8.1] C21
	if idx < -5 {
		idx = -5
	} else if idx > 5 {
		idx = 5
	}
	return pitchTable[idx+5]
}

// PitchCap returns the pitch-capped speed without the below-water halving [04 §8.1] C21.
// cap = table[clamped(delta>>11)] * MaxVelocity / 100 with signed truncation toward zero [I3][01 §8].
func PitchCap(delta int32, maxVelocity int32) int32 { // [04 §8.1] C21
	pct := pitchPercent(PitchIndex(delta))
	// signed truncation toward zero is Go's / on signed integers [I3][01 §8]
	return int32(int64(pct) * int64(maxVelocity) / 100)
}

// SpeedCap returns the final capped speed for the given pitch delta,
// including the below-water halving gate [04 §8.1] C21.
// If HeightWord < SeaLevel (signed) and DefFlags & 0x81000 == 0 (neither
// canhover 0x1000 nor floater 0x80000) the cap halves [04 §8.1] C21.
func (s *SteerState) SpeedCap(delta int32) int32 { // [04 §8.1] C21
	if s == nil {
		return 0
	}
	cap := PitchCap(delta, s.MaxVelocity)
	if int16(s.HeightWord) < int16(int16(s.SeaLevel)) && s.DefFlags&flagNoHalve == 0 { // [04 §8.1] C21
		// Halve with signed truncation toward zero [I3]; positive cap => /2
		cap = cap / 2
	}
	return cap
}

// SpeedCapForPitch returns the pitch-capped speed using the authoritative
// unit pitch word [04 R-MOV-01 §4].
func (s *SteerState) SpeedCapForPitch(pitch int16) int32 {
	if s == nil {
		return 0
	}
	idx := int(pitch >> 11)
	if idx < -5 {
		idx = -5
	} else if idx > 5 {
		idx = 5
	}
	pct := pitchTable[idx+5]
	cap := int32(int64(pct) * int64(s.MaxVelocity) / 100) // trunc toward zero [I3]
	if int16(s.HeightWord) < int16(s.SeaLevel) && s.DefFlags&flagNoHalve == 0 {
		cap /= 2
	}
	return cap
}

// headingDelta wraps the heading difference on the 16-bit circle [04 §8.1] C20.
// It is int16(desired - current) so wrap is automatic via uint16 subtraction.
func headingDelta(current, desired uint16) int16 { // [04 §8.1] C20
	return int16(desired - current)
}

// clampDelta clamps a signed heading delta to the definition's turn rate [04 §8.1] C20.
func clampDelta(delta int16, turnRate int32) int16 { // [04 §8.1] C20
	if turnRate < 0 {
		turnRate = 0
	}
	if int32(delta) > turnRate {
		// turnRate may exceed int16 range; saturate to int16 max in that case,
		// but spec's TurnRate values are small (typical < 2000) so direct cast is safe.
		if turnRate > 32767 {
			return 32767
		}
		return int16(turnRate)
	}
	if int32(delta) < -turnRate {
		if turnRate > 32768 {
			return -32768
		}
		return int16(-turnRate)
	}
	return delta
}

// UpdateHeading implements C20: desired heading wraps on the 16-bit circle
// and heading change clamps to TurnRate; pending heading and the dirty flag
// update BEFORE integration [04 §8.1] C20.
// There is no reverse-speed branch — this function does not touch Speed at all,
// reproducing the absence [04 §8.1] C20.
func (s *SteerState) UpdateHeading(desired uint16) { // [04 §8.1] C20
	if s == nil {
		return
	}
	delta := headingDelta(s.Heading, desired)    // wrap [04 §8.1] C20
	delta = clampDelta(delta, s.TurnRate)        // clamp to TurnRate [04 §8.1] C20
	s.PendingHeading = s.Heading + uint16(delta) // wraps on 16-bit circle [04 §8.1] C20
	// pending heading and dirty flag update BEFORE integration [04 §8.1] C20
	if delta != 0 {
		s.Dirty = true
	}
	// No reverse-speed branch: Speed is left untouched and never made negative here [04 §8.1] C20
}

// M-5 of the 2026-09-05 audit removed `UpdateSpeedWithBraking` from this file.
// It was a second, NON-RETAIL speed integrator: it braked when
// `distToGoal <= speed*speed/(2*brake)` and snapped straight to the cap on a
// zero acceleration, neither of which is [04 R-MOV-01 §4]. The route follower's
// own decision — the per-axis squares, the turn-distance term, the strict `>`
// on both tests — lives in the follower, and the integrator it drives is
// UpdateFollowerSpeed below. Nothing outside this package's tests ever called
// the removed function, so its only effect was to offer a plausible wrong
// answer to a later reader.

// UpdateFollowerSpeed applies the route follower's already-selected signed
// acceleration or braking delta, then its pitch/water ceiling. The caller owns
// the two strict turn/stopping-distance tests [04 R-MOV-01 §4].
func (s *SteerState) UpdateFollowerSpeed(cap int32, hasWaypoint, accelerate bool) {
	if s == nil {
		return
	}
	if hasWaypoint && accelerate {
		s.Speed += s.Acceleration
	} else {
		s.Speed -= s.BrakeRate
	}
	if s.Speed < 0 {
		s.Speed = 0
	}
	if cap < s.Speed {
		s.Speed = cap
	}
}

// ClampSpeed is a helper that returns the capped speed without mutating state.
// It is useful for tests that want to check the asymmetric table and halving
// without constructing a full SteerState tick.
func ClampSpeed(target int32, pitchDelta int32, maxVelocity int32, heightWord int16, seaLevel uint8, defFlags uint32) int32 { // [04 §8.1] C20 C21
	s := &SteerState{
		MaxVelocity: maxVelocity,
		HeightWord:  heightWord,
		SeaLevel:    seaLevel,
		DefFlags:    defFlags,
	}
	cap := s.SpeedCap(pitchDelta)
	if target < 0 {
		target = 0 // no reverse [04 §8.1] C20
	}
	if target > cap {
		target = cap
	}
	return target
}

// Integrate commits the pending heading and advances position [04 §8.1] C20.
// It must be called AFTER UpdateHeading so that pending/Dirty are set before
// position integration, satisfying the BEFORE ordering [04 §8.1] C20.
// Position integration uses fixed-point trig tables [04 §5.1] and integer
// arithmetic only (no float) [I2][I3].
func (s *SteerState) Integrate() { // [04 §8.1] C20
	if s == nil {
		return
	}
	// Commit heading: pending becomes current if dirty [04 §8.1] C20
	if s.Dirty {
		s.Heading = s.PendingHeading
	}
	// Position step, exactly as retail writes it [04 R-MOV-01 §4]:
	//
	//	vx = -((sin[heading] * speed + 0x1000) >> 13)
	//	vy = 0
	//	vz = -((cos[heading] * speed + 0x1000) >> 13)
	//
	// The negation is applied AFTER the rounding shift, not folded into the
	// product, so the two forms are not interchangeable on odd remainders. The
	// table is the shared 512-entry round(8192*sin) family [04 §5.1]; the
	// 0x1000 addend is round-to-nearest at that scale, and the shift is
	// arithmetic.
	// Speed is 16.16 and the table is 8192-scaled, so product>>13 is a 16.16
	// delta.
	//
	// Consequence of the sign, and the whole point of it: heading 0 steps
	// toward -Z (up-screen), 16384 toward -X, 32768 toward +Z, 49152 toward +X
	// [04 R-MOV-01 §4]. A unit's rendered facing therefore agrees with the
	// direction it travels.
	//
	// No reverse branch: Speed is non-negative, so step is forward only [04 §8.1] C20.
	if s.Speed != 0 {
		sin := numeric.Sin(numeric.Angle(s.Heading)) // scaled 8192 [04 §5.1]
		cos := numeric.Cos(numeric.Angle(s.Heading)) // scaled 8192 [04 §5.1]
		// int64 intermediate against overflow; round-to-nearest then negate.
		s.X += -int32((int64(sin)*int64(s.Speed) + 0x1000) >> 13)
		s.Z += -int32((int64(cos)*int64(s.Speed) + 0x1000) >> 13)
	}
	// Dirty remains set; caller may clear if desired. Retail keeps dirty set for the tick.
}
