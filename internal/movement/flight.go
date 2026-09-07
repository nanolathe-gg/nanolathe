// Flight integrator [04 §10.1] C26–C30.
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
//	X,Y,Z                world position 16.16; the Y high word is the signed height [04 §8.1]
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
//	OffMap               the unit's air-sector link is the off-map sector record [04 R-AIR-01 §5][04 R-AIR-01 §15]
//	TargetX/Z            command XZ 16.16 [04 §10.1] horizontal accel
//	TargetVX/VZ          command VXZ 16.16 [04 §10.1]
//	Dirty                transform-dirty bit set by heading integration when err != 0 [04 §10.1] C30

package movement

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// FlightState holds the mutable flight integrator state. See package comment
// for retail offset mapping. All fixed values are raw 16.16 int32 words unless
// noted. Angles are uint16 0..65535 per circle.
type FlightState struct {
	Mode uint8 // low 2 bits; 2 == active [04 §9.1] C26

	X, Y, Z    int32 // position 16.16 [04 §2.3]; the Y high word is the signed height
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
	// OffMap is the flight integrator's vertical bypass: the word [04 §10.1]
	// C29 compares by full 32-bit equality against a global is the unit's
	// AIR-SECTOR LIST LINK, and the global is the OFF-MAP SECTOR RECORD — one
	// extra 10-byte record allocated beside the coarse sector grid at map load,
	// never in the grid array, that the occupancy re-stamp links a unit into
	// when its footprint anchor lies outside the attribute grid
	// (anchorX < 0 || anchorZ < 0 || width <= anchorX + footX ||
	// height <= anchorZ + footZ) [04 R-AIR-01 §5]. While the link is that
	// record the vertical velocity assignment is skipped entirely and vertical
	// velocity keeps its damped value: an aircraft that leaves the map has no
	// vertical control at all until it re-enters. The earlier placeholder name
	// "vertical-hold sentinel" described the effect; [04 R-AIR-01 §15] gives
	// the role, which is off-map.
	//
	// syncStampedAirSector updates this mirror after footprint reconciliation.
	// CollisionState owns the canonical sector identity for both airborne and
	// grounded consumers; an out-of-bounds stamp selects the sentinel, and an
	// in-bounds stamp selects a grid sector [04 R-COLL-01 §4][04 R-AIR-01 §5].
	OffMap bool

	TargetX  int32 // command X 16.16 [04 §10.1] horizontal accel
	TargetZ  int32 // command Z 16.16 [04 §10.1]
	TargetVX int32 // command VX 16.16 [04 §10.1]
	TargetVZ int32 // command VZ 16.16 [04 §10.1]

	Dirty bool // transform-dirty set when heading delta non-zero [04 §10.1] C30

	// ModeMirror is the unit-side copy of the committed mover mode that the
	// position commit compares against the mover's own mode word and rewrites
	// on a full commit [04 R-MOV-01 §8]. The commit enters when any velocity
	// component is non-zero OR the two disagree, and a landed aircraft has a
	// zero velocity triple, so the mismatch is the one thing that makes the
	// touchdown tick commit at all — and that commit's transform-dirty bit is
	// what lets the post-move correction write the resting Y once
	// [04 R-AIR-01 §6 "Touchdown"]. Initialised to the allocation mode.
	ModeMirror uint8

	// Command is the mover's one motion controller: the flight command block a
	// can-fly mover allocates instead of the ground route follower
	// [04 R-AIR-01 §1]. The integrator reads the command words copied out of it,
	// never the block or an order record directly.
	Command *FlightCommand

	// Unit is the mover's back-reference to the unit whose two visual angle
	// words the lean accumulator writes [04 R-AIR-01 §2]. Nil in the isolated
	// integrator fixtures, which read Bank and Pitch here instead.
	Unit *units.Unit

	// LeanX/Y/Z are the mover's three-component lean accumulator, zeroed at
	// mover construction and persistent across ticks [04 R-AIR-01 §1]. It is
	// simulation state, not presentation: the bank and pitch it produces feed
	// the piece-angle triple the occupancy commit builds [04 R-AIR-01 §2].
	LeanX, LeanY, LeanZ int32

	BankScale  int32 // definition bankscale, 16.16, default 1.0 [04 R-AIR-01 §2][02 "Unit record"]
	PitchScale int32 // definition pitchscale, 16.16, default 0.0 [04 R-AIR-01 §2][02 "Unit record"]
	Gravity    int32 // map gravity runtime word [04 R-AIR-01 §2][03 R-TERR-01 §6]

	Bank  uint16 // bank angle word written by the lean accumulator [04 R-AIR-01 §2]
	Pitch uint16 // pitch angle word written by the lean accumulator [04 R-AIR-01 §2]
}

// flightGoalDistance is the horizontal goal distance of the per-tick command
// producer [04 R-AIR-01 §1] step 3: the double-precision hypot of the two raw
// fixed-point differences, truncated toward zero. The result is a raw 16.16
// quantity, compared against the producer's world-unit thresholds.
func flightGoalDistance(dx, dz int64) int64 {
	return int64(math.Hypot(float64(dx), float64(dz)))
}

// bearing is the movement package's air-order adapter. It delegates the
// self-minus-target operand order and exact round-to-nearest-even conversion
// to the shared numeric helper [04 R-AIR-01 §1][04 R-MOV-01 §2].
func bearing(ax, az, bx, bz numeric.Fixed) uint16 {
	return numeric.AngleFromAtan2(int64(ax)-int64(bx), int64(az)-int64(bz)).Raw()
}

// rotateLeanPair rotates the lean accumulator's horizontal pair by the unit's
// heading, leaving it unchanged when the heading word is exactly zero, and
// stores both results under round-to-nearest, ties to even [04 R-AIR-01 §2].
//
// The sign convention this site used to leave open is settled
// [04 R-AIR-01 §15]: the shared coordinate-pair rotation stores
// x' = x·cos θ − z·sin θ first, then z' = x·sin θ + z·cos θ, which is
// BODY-TO-WORLD against retail's heading convention; the transpose
// (x·cos + z·sin, −x·sin + z·cos) is not what retail computes. The form below
// is already that one. Retail converts the heading as a SIGNED 16-bit integer,
// which differs from the unsigned conversion here by a whole turn and so gives
// the same sine and cosine.
func rotateLeanPair(x, z int32, heading uint16) (int32, int32) {
	if heading == 0 {
		return x, z
	}
	sin, cos := math.Sincos(float64(heading) * 2 * math.Pi / 65536.0)
	px := int32(math.RoundToEven(float64(x)*cos - float64(z)*sin))
	pz := int32(math.RoundToEven(float64(x)*sin + float64(z)*cos))
	return px, pz
}

// ApplyLean advances the lean accumulator by one tick and rewrites the bank and
// pitch angle words [04 R-AIR-01 §2]. It is called from the end of the flight
// integrator with the tick's velocity delta, and from the mover-mode setter with
// a zero delta.
//
// Each component decays by 62259/65536 as a 64-bit signed product shifted down,
// this tick's velocity delta is added, the horizontal pair is rotated by the
// unit's heading, and each scaled term is divided against the gravity term
// L = (gravity << 16) / 3277 through atan2, scaled to the 16-bit angle circle
// and rounded to nearest. BOTH bank and pitch take the negated FIRST rotated
// component: retail feeds the same rotated X into the two angle calls, and the
// second rotated component is computed by the shared rotation but never read
// before the routine returns [04 R-AIR-01 §2]. An earlier version of this
// routine derived the pitch term from the negated rotated Z; that was wrong,
// and latent on stock content only because `pitchscale` defaults to 0. There is
// no zero guard on L: a map whose gravity word is zero puts the whole quarter
// turn on the angle, which is what retail computes.
func (s *FlightState) ApplyLean(dvx, dvy, dvz int32) {
	if s == nil {
		return
	}
	s.LeanX = int32((int64(s.LeanX) * leanDecay) >> 16)
	s.LeanY = int32((int64(s.LeanY) * leanDecay) >> 16)
	s.LeanZ = int32((int64(s.LeanZ) * leanDecay) >> 16)
	s.LeanX += dvx
	s.LeanY += dvy
	s.LeanZ += dvz

	// The second rotated component is computed by the shared rotation and
	// discarded unread, exactly as retail does [04 R-AIR-01 §2].
	px, _ := rotateLeanPair(s.LeanX, s.LeanZ, s.Heading)
	l := (int64(s.Gravity) << 16) / leanGravityDivisor // 64-bit signed divide, truncating [I3]
	bankTerm := (int64(s.BankScale) * int64(-px)) >> 16
	pitchTerm := (int64(s.PitchScale) * int64(-px)) >> 16
	s.Bank = numeric.AngleFromAtan2(bankTerm, l).Raw()
	s.Pitch = numeric.AngleFromAtan2(pitchTerm, l).Raw()
	if s.Unit != nil {
		s.Unit.Move.Bank = s.Bank
		s.Unit.Move.Pitch = s.Pitch
	}
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

	// The lean accumulator is driven by the new-minus-old velocity vector
	// captured after the position commit, so the old vector is taken here, before
	// the decay [04 §10.1][04 R-AIR-01 §2].
	oldVX, oldVY, oldVZ := s.VX, s.VY, s.VZ

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
		// Sine feeds VX, cosine feeds VZ, both subtractions, each product
		// rounded by adding 0x1000 before the 13-bit shift [04 R-AIR-01 §15]
		// [04 §10.1] C28. The mapping is confirmed, not assumed.
		sin := int32(numeric.Sin(numeric.Angle(s.Heading)))
		cos := int32(numeric.Cos(numeric.Angle(s.Heading)))
		s.VX -= int32((int64(q)*int64(sin) + 4096) >> 13) // round to nearest [04 §5.1] via [04 §10.1] C28
		s.VZ -= int32((int64(q)*int64(cos) + 4096) >> 13)
	}

	// C29 — vertical control gated on the off-map sector link [04 §10.1]
	// [04 R-AIR-01 §5][04 R-AIR-01 §15].
	// dy = unitY − targetY (both 16.16); while the unit's air-sector link is the
	// off-map sector record the assignment is skipped and vy keeps its damped
	// value. Otherwise limit = 0x10000 when (speed & ~3) < 0x40000 else speed>>2,
	// then dy<=-limit ⇒ vy=+limit; dy<limit ⇒ vy=-dy; else vy=-limit.
	if !s.OffMap {
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

	// Commit position via velocity [04 §10.1] shared mover position commit; flight branch shares final position commit with ground [04 §10.1].
	s.X += s.VX
	s.Y += s.VY
	s.Z += s.VZ

	// Bank and pitch, from this tick's velocity delta [04 R-AIR-01 §2].
	s.ApplyLean(s.VX-oldVX, s.VY-oldVY, s.VZ-oldVZ)
}
