package combat

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The two zero-tolerance drift gates [06 R-WPN-03 §2]. A weapon that authors
// no tolerance is gated on the shooter's movement tier instead: 150 angle
// units (about 0.8 degrees) while the shooter is stationary, 2000 (about 11
// degrees) while it is moving. Both are read from the image, not chosen.
const (
	DriftGateStationary int32 = 150
	DriftGateMoving     int32 = 2000
)

// DriftGates returns the yaw and pitch halves of the angular-drift gate
// [06 R-WPN-03 §2].
//
// The zero test is on `tolerance` alone. When it is nonzero the yaw gate is
// `tolerance` and the pitch gate is `pitchtolerance` when that is nonzero,
// otherwise `tolerance` again — the only place the two keys interact. When
// `tolerance` is zero both gates take the movement-tier fallback and an
// authored `pitchtolerance` is never consulted.
//
// Both keys are stored 16-bit and read **zero-extended** [06 R-WPN-03 §1], so
// a negatively authored tolerance reads back as its two's complement: -1
// becomes a gate of 65,535 that every error passes. Stock content never
// authors one.
func DriftGates(w *content.WeaponDef, stationary bool) (yawGate, pitchGate int32) {
	var tol, pitchTol int32
	if w != nil {
		tol = int32(uint16(w.Tolerance))           // zero-extended [06 R-WPN-03 §1]
		pitchTol = int32(uint16(w.PitchTolerance)) // zero-extended [06 R-WPN-03 §1]
	}
	if tol != 0 {
		if pitchTol != 0 {
			return tol, pitchTol
		}
		return tol, tol
	}
	if stationary {
		return DriftGateStationary, DriftGateStationary
	}
	return DriftGateMoving, DriftGateMoving
}

// AngleError is the drift gate's error term: the absolute value of the signed
// 16-bit difference of two angles [06 R-WPN-03 §2]. The difference is the
// 16-bit wrap of stored minus wanted, sign-extended, so the largest possible
// error is 32,768 — the wrap of 0x8000.
func AngleError(stored, wanted uint16) int32 {
	d := int16(stored - wanted)
	if d < 0 {
		return -int32(d)
	}
	return int32(d)
}

// DriftGatePass reports whether a slot's stored angles are close enough to the
// wanted pair to fire [06 R-WPN-03 §2].
//
// Yaw is tested first and the test short-circuits: the pitch difference is not
// computed when yaw fails. Both comparisons are inclusive, so an error of
// exactly the gate passes.
func DriftGatePass(w *content.WeaponDef, stationary bool, storedYaw, wantYaw, storedPitch, wantPitch uint16) bool {
	yawGate, pitchGate := DriftGates(w, stationary)
	if AngleError(storedYaw, wantYaw) > yawGate {
		return false
	}
	return AngleError(storedPitch, wantPitch) <= pitchGate
}

// accuracySpreadBoundWithDivisor computes the turret executor's spread bound
// [06 §4.4] [06 R-WPN-03 §4]. The bound is computed, never authored:
//
//	healthTerm = uint32(int32(int16(health)) << 11) / uint32(maxHealth)  ; unsigned divide, 2048 at full health
//	raw        = uint16(accuracy) - uint16(healthTerm)                   ; 16-bit subtraction, wraps
//	bound      = uint16(raw + 0x800)                                     ; carry out of bit 15 discarded
//	div        = selected veterancy divisor                              ; Strict is uint16(kills) / 12
//	if div > 1 { bound = uint16(int32(bound) / int32(div)) }             ; signed 32-bit divide
//
// So the bound is `accuracy + 2048*(1 - health/maxHealth)` modulo 65,536: a
// full-health shooter with `accuracy = 0` has bound 0 and fires exactly on its
// solved angles, and twenty-four credited kills halve whatever bound it has.
// The angle units are the uint16 angle-per-circle of the slot angles, so 182
// units is about half a degree — `accuracy` is neither degrees nor a percent.
//
// Zero or negative maxHealth raises the processor divide fault in retail, the
// same malformed-only case the reload computation reproduces; stock
// MaxDamage is always positive.
func accuracySpreadBoundWithDivisor(accuracy, health, maxHealth int32, div uint32) uint16 {
	if maxHealth <= 0 {
		panic("combat: zero or negative maxHealth divide fault in accuracy spread [06 R-WPN-03 §4]")
	}
	healthTerm := uint32(int32(int16(health))<<11) / uint32(maxHealth)
	bound := uint16(accuracy) - uint16(healthTerm) + 0x800
	if div > 1 {
		bound = uint16(int32(bound) / int32(div))
	}
	return bound
}

// BallisticSolve computes the launch pitch for a ballistic projectile.
//
// It transcribes the retail solver at [06 §3.3] / [06 §6.4] and its malformed
// arithmetic from [GAP T5]. The discriminant is float64 per [INVARIANTS I2] and
// is compared against exactly 0.0 with no epsilon. Candidate order is aPlus
// then aMinus accepting minBarrel < a <= pi/4, converting as trunc(a*32768/pi).
func BallisticSolve(dx, dy, dz numeric.Fixed, vel, grav numeric.Fixed, minBarrel float64) (uint16, bool) {
	// [GAP T5] X/Z delta components wrap as signed 32-bit before double conversion.
	// [06 §6.4] g2 = i32(g*g) wraps before double conversion.
	dx32 := int32(dx.Raw())
	dy32 := int32(dy.Raw())
	dz32 := int32(dz.Raw())
	v32 := int32(vel.Raw())
	g32 := int32(grav.Raw())

	// [06 §6.4] h = hypot(double(dx), double(dz))
	h := math.Hypot(float64(dx32), float64(dz32))
	h2 := float64(h * h)
	// Retail's solver operands are `source - target` on all three axes, not
	// `target - source` [06 §6.4] (ordering corrected there 2026-09-16 after a
	// call-site census; the section previously said the other way round). The
	// planar pair reaches the discriminant only through a hypotenuse, so only
	// the vertical sign is observable — and it is very observable: the
	// discriminant's +2*g*y term is the physical -2*g*(target-source), so the
	// wrong sign mirrors every sloped firing solution, aiming high at a target
	// below and low at one above. Callers here supply target-minus-muzzle
	// deltas, so negate the vertical one.
	y := -float64(dy32)
	v := float64(v32)
	g := float64(g32)

	// [06 §6.4] s = y*y + h2
	//
	// Every explicit float64 conversion of a product in this solver is a
	// rounding barrier, not a width change: retail's x87 rounds each product
	// to working precision before it enters a sum or a difference, and a
	// backend with a fused multiply-add would round the two together and
	// produce a different discriminant [06 §6.4] (I2's no-fusion rule).
	s := float64(y*y) + h2

	// [GAP T5] g2 = i32(g*g) wraps as signed 32-bit integer multiply before double conversion.
	g2raw := int32(int64(g32) * int64(g32))
	g2 := float64(g2raw)

	v2 := float64(v * v)

	// [06 §6.4] discriminant construction transcribed verbatim from [GAP T5] / orchestration-research-combat-effects §2.1:
	// disc = ( y*y*g2 + (v*v - g*y*(-2.0))*v*v ) * h2*h2 - h2*h2*g2*s
	// The -2.0 is a literal in the discriminant expression; subtracting the
	// negative makes the term additive: v*v + 2*g*y.
	disc := float64((float64(y*y*g2)+float64((v2-float64(g*y*(-2.0)))*v2))*h2*h2) - float64(h2*h2*g2*s)

	// [06 §6.4] discriminant is tested against exactly 0.0 with no positive epsilon guard.
	// Negative or unordered (NaN) returns the no-solution sentinel 0x8000.
	// [INVARIANTS I2] discriminant, acos, sqrt are float64.
	if math.IsNaN(disc) || disc < 0.0 {
		return 0, false
	}

	sqrtDisc := math.Sqrt(disc)

	// [06 §6.4] base = (v*v + g*y) * h2 ; den = 2.0 * s
	// The outer conversion also keeps the `* h2` out of the two divisions
	// below, which the compiler would otherwise fuse into their numerators.
	base := float64((v2 + float64(g*y)) * h2)
	den := 2.0 * s
	if den == 0.0 || math.IsNaN(den) || math.IsInf(den, 0) {
		return 0, false
	}

	rPlus := (base + sqrtDisc) / den
	rMinus := (base - sqrtDisc) / den

	// [06 §6.4] aPlus = pi/2 if rPlus <= 0 else acos(sqrt(rPlus)/v)
	// aMinus similarly. This is the acos(sqrt(r)/v) construction.
	//
	// r is V*V*cos(theta)^2 and carries no sign, so the recovered angle is
	// never negative and a depressed-barrel solution is unreachable. A target
	// below the aim origin by more than g*h*h/(2*V*V) is therefore engaged
	// with the mirror-image UPWARD arc and overshot, and a steeper drop is
	// rejected by the quarter-turn gate below. That is retail's, which is also
	// why the lower gate against a negative authored minbarrelangle is vacuous
	// [06 §6.4] [I11]. Do not "fix" it by restoring the sign.
	// The researched constants lie slightly below computed pi/2 and pi/4;
	// deriving them from math.Pi admits a different upper boundary [06 §3.3].
	const substituteAngle = 1.570796326794895
	const maxAngle = 0.7853981633974475
	var aPlus, aMinus float64
	if rPlus <= 0.0 || math.IsNaN(rPlus) {
		aPlus = substituteAngle
	} else {
		// sqrt(rPlus)/v may be >1 or NaN; Acos returns NaN which will be rejected by gate.
		if v == 0 {
			aPlus = math.NaN()
		} else {
			aPlus = math.Acos(math.Sqrt(rPlus) / v)
		}
	}
	if rMinus <= 0.0 || math.IsNaN(rMinus) {
		aMinus = substituteAngle
	} else {
		if v == 0 {
			aMinus = math.NaN()
		} else {
			aMinus = math.Acos(math.Sqrt(rMinus) / v)
		}
	}

	// [06 §6.4] candidate selection in strict order — plus root first, then minus —
	// each accepted only when minBarrel < a <= pi/4; over-45-degree arcs rejected.
	// [GAP T5] same.
	if !math.IsNaN(aPlus) && aPlus > minBarrel && aPlus <= maxAngle {
		// [06 §6.4] accepted angle serializes as trunc(angle*32768/pi) into 16-bit domain.
		pitch := uint16(numeric.TruncateFloat64ToLow32(aPlus * 32768 / math.Pi))
		return pitch, true
	}
	if !math.IsNaN(aMinus) && aMinus > minBarrel && aMinus <= maxAngle {
		pitch := uint16(numeric.TruncateFloat64ToLow32(aMinus * 32768 / math.Pi))
		return pitch, true
	}
	return 0, false
}

// distance3DRaw is the three-dimensional distance both [06 §3.3]'s pre-fire
// lead and [06 §6.8]'s cruise helper form, and they form it identically:
//
//	trunc(sqrt((dX*dX + dY*dY) + dZ*dZ))
//
// over RAW 16.16 deltas that have already wrapped as signed 32-bit, evaluated
// at working precision in that association order and truncated toward zero
// [01 §8] I3. The answer stays a raw 16.16 distance — nothing here shifts it
// down to whole world units, and both callers depend on that: the lead divides
// it by a 16.16 `weaponvelocity`, and the cruise helper does its own shift
// before a signed-short compare.
//
// This is deliberately NOT the planar range distance of [06 §3.3], which
// squares and shifts per axis without a square root at all; the two are
// different quantities and neither may stand in for the other.
//
// float64 here is the two I2 rows citing [06 §3.3] and [06 §6.8]; the value is
// a transient and is never stored.
func distance3DRaw(dx, dy, dz int32) int64 {
	fx, fy, fz := float64(dx), float64(dy), float64(dz)
	// Full-range raw deltas reach 2³¹, so each square exceeds 53 bits and is
	// inexact. The explicit conversions round each square before it is summed,
	// which is what retail's x87 does and what a fused multiply-add would not;
	// the truncated distance below was measured to differ otherwise.
	x2, y2, z2 := float64(fx*fx), float64(fy*fy), float64(fz*fz)
	return int64(numeric.TruncateFloat64ToLow32(math.Sqrt((x2 + y2) + z2)))
}
