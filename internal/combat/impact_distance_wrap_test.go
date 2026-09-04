package combat

import "testing"

// The blast separation is reduced to a SIGNED 16-BIT whole-world-unit value, and
// the narrowing is truncating: "a distance at or above 32,768 world units wraps
// negative and passes the acceptance test" [06 §9.3]. Acceptance is `d < R` on
// that reduced value, so a wrapped negative is accepted at every radius while a
// saturated 32,767 is rejected at every radius — the opposite decision.
//
// This site saturated until WU-19-154. Stock content cannot reach the boundary
// (no shipped weapon authors a radius near 32,767 and the separation has to be
// larger still), so the test drives DistanceToBox directly rather than a blast.
func TestDistanceToBoxWrapsPastTheSignedSixteenBitBoundary(t *testing.T) {
	const maxRaw = int32(1<<31 - 1) // 32,767.99 world units, the widest 16.16 delta

	// A box whose nearest corner sits maxRaw away on all three axes: the
	// separation is sqrt(3) x maxRaw, well past 32,768 whole world units.
	far := UnitForArea{
		Pos: Vec3{},
		Min: Vec3{X: fixRaw(maxRaw), Y: fixRaw(maxRaw), Z: fixRaw(maxRaw)},
		Max: Vec3{X: fixRaw(maxRaw), Y: fixRaw(maxRaw), Z: fixRaw(maxRaw)},
	}
	d := DistanceToBox(Vec3{}, far)
	if d >= 0 {
		t.Fatalf("separation reduced to %d, want a negative value: the sixteen-bit narrowing wraps [06 §9.3]", d)
	}
	if d < -32768 || d > 32767 {
		t.Fatalf("separation %d is outside the signed sixteen-bit range [06 §9.3]", d)
	}

	// An ordinary separation is unaffected: 100 world units away on one axis.
	near := UnitForArea{
		Pos: Vec3{},
		Min: Vec3{X: fixRaw(100 * 65536)},
		Max: Vec3{X: fixRaw(100 * 65536)},
	}
	if got := DistanceToBox(Vec3{}, near); got != 100 {
		t.Fatalf("separation %d for a 100-world-unit gap, want 100 [06 §9.3]", got)
	}

	// On or inside the box is exactly zero [06 §9.3].
	inside := UnitForArea{
		Pos: Vec3{},
		Min: Vec3{X: fixRaw(-65536), Y: fixRaw(-65536), Z: fixRaw(-65536)},
		Max: Vec3{X: fixRaw(65536), Y: fixRaw(65536), Z: fixRaw(65536)},
	}
	if got := DistanceToBox(Vec3{}, inside); got != 0 {
		t.Fatalf("separation %d inside the box, want 0 [06 §9.3]", got)
	}
}
