package combat

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

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
	h2 := h * h
	// Retail delta is muzzle - target for Y [orchestration-research-combat-effects §2.1 notes
	// "x,y,z be signed 32-bit source/target deltas" without ordering; the
	// +2*g*y term matches the physical -2*g*dy only when y = -(target-muzzle).
	// The caller supplies dy = target - muzzle, so we negate to match retail's
	// internal ordering.
	y := -float64(dy32)
	v := float64(v32)
	g := float64(g32)

	// [06 §6.4] s = y*y + h2
	s := y*y + h2

	// [GAP T5] g2 = i32(g*g) wraps as signed 32-bit integer multiply before double conversion.
	g2raw := int32(int64(g32) * int64(g32))
	g2 := float64(g2raw)

	v2 := v * v

	// [06 §6.4] discriminant construction transcribed verbatim from [GAP T5] / orchestration-research-combat-effects §2.1:
	// disc = ( y*y*g2 + (v*v - g*y*(-2.0))*v*v ) * h2*h2 - h2*h2*g2*s
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// the negative makes the term additive: v*v + 2*g*y.
	disc := (y*y*g2+(v2-g*y*(-2.0))*v2)*h2*h2 - h2*h2*g2*s

	// [06 §6.4] discriminant is tested against exactly 0.0 with no positive epsilon guard.
	// Negative or unordered (NaN) returns the no-solution sentinel 0x8000.
	// [INVARIANTS I2] discriminant, acos, sqrt are float64.
	if math.IsNaN(disc) || disc < 0.0 {
		return 0, false
	}

	sqrtDisc := math.Sqrt(disc)

	// [06 §6.4] base = (v*v + g*y) * h2 ; den = 2.0 * s
	base := (v2 + g*y) * h2
	den := 2.0 * s
	if den == 0.0 || math.IsNaN(den) || math.IsInf(den, 0) {
		return 0, false
	}

	rPlus := (base + sqrtDisc) / den
	rMinus := (base - sqrtDisc) / den

	// [06 §6.4] aPlus = pi/2 if rPlus <= 0 else acos(sqrt(rPlus)/v)
	// aMinus similarly. This is the acos(sqrt(r)/v) construction.
	var aPlus, aMinus float64
	if rPlus <= 0.0 || math.IsNaN(rPlus) {
		aPlus = math.Pi / 2
	} else {
		// sqrt(rPlus)/v may be >1 or NaN; Acos returns NaN which will be rejected by gate.
		if v == 0 {
			aPlus = math.NaN()
		} else {
			aPlus = math.Acos(math.Sqrt(rPlus) / v)
		}
	}
	if rMinus <= 0.0 || math.IsNaN(rMinus) {
		aMinus = math.Pi / 2
	} else {
		if v == 0 {
			aMinus = math.NaN()
		} else {
			aMinus = math.Acos(math.Sqrt(rMinus) / v)
		}
	}

	const pi4 = math.Pi / 4

	// [06 §6.4] candidate selection in strict order — plus root first, then minus —
	// each accepted only when minBarrel < a <= pi/4; over-45-degree arcs rejected.
	// [GAP T5] same.
	if !math.IsNaN(aPlus) && aPlus > minBarrel && aPlus <= pi4 {
		// [06 §6.4] accepted angle serializes as trunc(angle*32768/pi) into 16-bit domain.
		pitch := uint16(int32(aPlus * 32768 / math.Pi))
		return pitch, true
	}
	if !math.IsNaN(aMinus) && aMinus > minBarrel && aMinus <= pi4 {
		pitch := uint16(int32(aMinus * 32768 / math.Pi))
		return pitch, true
	}
	return 0, false
}
