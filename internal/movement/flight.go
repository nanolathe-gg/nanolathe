// Package movement — flight integrator [04 §10.1] C26–C30.
//
// FlightState is the explicit integration surface that retail scatters across
// the unit record. The orchestrator will unify this with units.Unit once that
// type grows velocity/heading/speed fields. Retail offsets are noted where
// established so the unification is mechanical.
//
// Mapping to retail [04 §10.1] (I13: offsets are identity, not layout):
//
//	Mode                 low 2 bits of mover mode word, mirrored into unit [04 §9.1]
//	                     2 == active locomotion, 1 == stopped/parked, 0/3 preserved but no producer
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//	VX,VY,VZ             velocity components, 32-bit 16.16 words [04 §10.1] C27
//	Speed                scalar speed word, 32-bit (full 3-D magnitude) [04 §10.1]
//	Heading              uint16 heading 0..65535 per circle at +? [04 §5.1]
//	TargetHeading        command heading (desired heading)
//	TurnResidual         16-bit turn residual word [04 §10.1] C30
//	MaxVelocity          definition MaxVelocity, fixed 16.16 [02 "Unit record"]
//	Acceleration         definition Acceleration, fixed 16.16 [02 "Unit record"]
//	BrakeRate            definition BrakeRate, fixed 16.16 [02 "Unit record"]
//	TurnRate             definition TurnRate, integer [02 "Unit record"]
//	TargetY              command altitude, 16.16 (targetY) [04 §10.1] C29
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//	                     TODO(question): sentinel semantic name unknown, called verticalHoldSentinel per PLAN_07 Explicit unknowns.
//	TargetX/Z            command XZ 16.16 [04 §10.1] horizontal accel
//	TargetVX/VZ          command VXZ 16.16 [04 §10.1]
//	Dirty                transform-dirty bit set by heading integration when err != 0 [04 §10.1] C30
package movement

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// FlightState holds the mutable flight integrator state. See package comment
// for retail offset mapping. All fixed values are raw 16.16 int32 words unless
// noted. Angles are uint16 0..65535 per circle.
type FlightState struct {
	Mode uint8 // low 2 bits; 2 == active [04 §9.1] C26

	X, Y, Z    int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	VX, VY, VZ int32 // velocity 16.16 [04 §10.1] C27
	Speed      int32 // scalar speed word [04 §10.1] C29

	Heading       uint16 // current heading [04 §5.1]
	TargetHeading uint16 // command heading [04 §10.1] C30
	TurnResidual  int16  // 16-bit turn residual [04 §10.1] C26 C30

	MaxVelocity  int32 // fixed 16.16 [02 "Unit record"]
	Acceleration int32 // fixed 16.16 [02 "Unit record"]
	BrakeRate    int32 // fixed 16.16 [02 "Unit record"]
	TurnRate     int32 // integer [02 "Unit record"]

	TargetY int32 // target altitude 16.16 [04 §10.1] C29
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// Semantic name unknown; called verticalHoldSentinel per PLAN_07 Explicit unknowns.
	VerticalHoldSentinel bool

	TargetX  int32 // command X 16.16 [04 §10.1] horizontal accel
	TargetZ  int32 // command Z 16.16 [04 §10.1]
	TargetVX int32 // command VX 16.16 [04 §10.1]
	TargetVZ int32 // command VZ 16.16 [04 §10.1]

	Dirty bool // transform-dirty set when heading delta non-zero [04 §10.1] C30
}

// IntegrateFlight runs the can-fly integrator [04 §10.1] C26–C30.
//
// Order and truncation match retail exactly; the mixed fixed/float instruction
// sequence for brake shaping is preserved (I2 allowlist row: Flight brake
// integration temporaries (hypot, h, b, ratio) float64, narrowed at the named
// fixed-point stores). No algebraic rearrangement is performed [04 §10.1] C28.
func IntegrateFlight(s *FlightState) {
	if s == nil {
		return
	}
	// C26 — runs only when mover low mode bits == 2 [04 §10.1].
	if s.Mode&0x3 != 2 {
		s.VX = 0
		s.VY = 0
		s.VZ = 0
		s.Speed = 0
		s.TurnResidual = 0
		return
	}

	// C27 — per-component decay BEFORE command input [04 §10.1].
	// decay = 0x10000 − trunc((Acceleration<<16)/MaxVelocity)  // signed idiv, trunc toward zero [04 §10.1]
	// v = (v·decay)>>16  literal arithmetic shift floors [04 §10.1]; I3 shift semantics block.
	// Division unguarded — zero MaxVelocity faults (I11).
	decay := int32(0x10000 - int(int64(s.Acceleration)<<16/int64(s.MaxVelocity)))
	s.VX = int32((int64(s.VX) * int64(decay)) >> 16)
	s.VY = int32((int64(s.VY) * int64(decay)) >> 16)
	s.VZ = int32((int64(s.VZ) * int64(decay)) >> 16)

	// C28 — brake shaping with strict h > b [04 §10.1].
	// h = hypot(vx,vz)/65536, b = BrakeRate/65536  (float64 temporaries per I2)
	// only under STRICT h > b (equality skips whole block) both horizontal
	// components scale by trunc((b/h)·65536) via >>16, then excess
	// q = trunc((h−b)·65536) subtracted per-axis through fixed-point
	// sine/cosine of heading (numeric.Sin/Cos tables) [04 §5.1].
	// Keep exact mixed fixed/float instruction ORDER — no rearrangement [04 §10.1] C28.
	h := math.Hypot(float64(s.VX), float64(s.VZ)) / 65536.0 // float64 per I2
	b := float64(s.BrakeRate) / 65536.0                     // float64 per I2
	if h > b {                                              // STRICT [04 §10.1] C28
		ratio := int32((b / h) * 65536.0)                // trunc((b/h)·65536) [04 §10.1] C28
		s.VX = int32((int64(s.VX) * int64(ratio)) >> 16) // shift floors [04 §10.1]
		s.VZ = int32((int64(s.VZ) * int64(ratio)) >> 16)
		q := int32((h - b) * 65536.0) // trunc((h−b)·65536) [04 §10.1] C28
		// Fixed-point heading trig: Sin/Cos tables scaled 8192 [04 §5.1].
		// Heading 0 == north (+Z), so X via Sin, Z via Cos.
		// TODO(question): sin→VX / cos→VZ axis mapping [04 §10.1] C28
		sin := int32(numeric.Sin(numeric.Angle(s.Heading)))
		cos := int32(numeric.Cos(numeric.Angle(s.Heading)))
		s.VX -= int32((int64(q)*int64(sin) + 4096) >> 13) // round to nearest [04 §5.1] via [04 §10.1] C28
		s.VZ -= int32((int64(q)*int64(cos) + 4096) >> 13)
	}

	// C29 — vertical control sentinel-gated [04 §10.1].
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// sentinel by full 32-bit compare, skip assignment and vy keeps damped
	// value. Otherwise limit = 0x10000 when (speed & ~3) < 0x40000 else speed>>2,
	// then dy<=-limit ⇒ vy=+limit; dy<limit ⇒ vy=-dy; else vy=-limit.
	if !s.VerticalHoldSentinel {
		dy := int64(s.Y) - int64(s.TargetY)
		limit := int32(0x10000)
		if (s.Speed & ^int32(3)) >= int32(0x40000) {
			limit = s.Speed >> 2
		}
		if dy <= int64(-limit) {
			s.VY = limit
		} else if dy < int64(limit) {
			s.VY = int32(-dy)
		} else {
			s.VY = -limit
		}
	}

	// C30 — heading integration independent of vertical block [04 §10.1].
	// err = int16(targetHeading − heading); zero error zeroes residual
	// without setting dirty; otherwise clamp to TurnRate.
	err := int16(s.TargetHeading - s.Heading)
	if err == 0 {
		s.TurnResidual = 0
	} else {
		e := int32(err)
		tr := s.TurnRate
		if e > tr {
			e = tr
		} else if e < -tr {
			e = -tr
		}
		s.TurnResidual = int16(e)
		s.Heading += uint16(e)
		s.Dirty = true
	}

	// Horizontal acceleration [04 §10.1] (Established, I2 flight-temporaries float).
	// Uses post-decay/post-brake velocities against command targets.
	dxRaw := float64(int64(s.X) - int64(s.TargetX))
	dzRaw := float64(int64(s.Z) - int64(s.TargetZ))
	dvxRaw := float64(int64(s.VX) - int64(s.TargetVX))
	dvzRaw := float64(int64(s.VZ) - int64(s.TargetVZ))
	d := math.Hypot(dxRaw, dzRaw) / 65536.0
	if d < 8.0 {
		d = 8.0
	}
	a := float64(s.Acceleration) / 65536.0
	var ax, az float64
	if d != 0 && a != 0 {
		k := -math.Sqrt((2 * a) / d)
		ax = (dxRaw*k - dvxRaw) / 65536.0
		az = (dzRaw*k - dvzRaw) / 65536.0
		if hypot := math.Hypot(ax, az); hypot > a && hypot != 0 {
			scale := a / hypot
			ax *= scale
			az *= scale
		}
	} else if a == 0 {
		ax = -dvxRaw / 65536.0
		az = -dvzRaw / 65536.0
		// cap at 0 when a==0 => scale to 0 if hypot>0
		if hypot := math.Hypot(ax, az); hypot > 0 {
			ax = 0
			az = 0
		}
	}
	s.VX += int32(ax * 65536.0) // trunc toward zero [01 §8] via int32(float64)
	s.VZ += int32(az * 65536.0)

	// Scalar speed recomputed as FULL 3-D magnitude trunc(sqrt(vx²+vy²+vz²)) [04 §10.1].
	s.Speed = int32(math.Sqrt(float64(s.VX)*float64(s.VX) + float64(s.VY)*float64(s.VY) + float64(s.VZ)*float64(s.VZ)))
}
