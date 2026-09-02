// External test package: internal/movement imports internal/combat, so the
// mover can only be exercised from here.
package combat_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestProjectileLaunchAgreesWithMoverAndTarget is the RWU-19-41 probe: a unit
// steered at a target, and an ordinary projectile created toward the same
// target, both move toward it on their first tick, and along the same line.
//
// The contract under test is the sign convention of [06 R-WPN-05 §11]: retail
// builds every velocity as (-sin a, -cos a) of an absolute angle solved over
// muzzle-minus-target deltas; this build solves target-minus-muzzle and builds
// +sin/+cos, and the two flips cancel. If either half were applied without the
// other, every shot would leave half a turn from its target — the failure this
// probe exists to make loud.
func TestProjectileLaunchAgreesWithMoverAndTarget(t *testing.T) {
	const reach = 200 << 16
	w := &content.WeaponDef{ID: 1, WeaponVelocity: 6 << 16, Range: 400, WeaponTimer: 30}
	muzzle := combat.Vec3{X: 1000 << 16, Y: 10 << 16, Z: 1000 << 16}

	for _, d := range []struct{ dx, dz int64 }{
		{0, -reach}, {0, reach}, {-reach, 0}, {reach, 0},
		{reach, -reach / 3}, {-reach / 4, reach}, {-reach, -reach}, {reach / 2, reach / 5},
	} {
		target := combat.Vec3{X: muzzle.X + numeric.Fixed(d.dx), Y: muzzle.Y, Z: muzzle.Z + numeric.Fixed(d.dz)}

		// The ordinary creator [06 §6.3].
		var p combat.Projectile
		combat.InitOrdinary(&p, w, 0, muzzle, target, 0)
		vx, vz := p.Velocity.X.Raw(), p.Velocity.Z.Raw()

		// The mover, steered at the same delta [04 R-MOV-01 §4].
		s := movement.SteerState{Speed: 6 << 16, Dirty: true}
		s.PendingHeading = movement.HeadingFromDelta(d.dx, d.dz)
		s.Integrate()
		mx, mz := int64(s.X), int64(s.Z)

		if !sameSign(vx, d.dx) || !sameSign(vz, d.dz) {
			t.Fatalf("delta (%d,%d): projectile first step (%d,%d) does not point at the target", d.dx, d.dz, vx, vz)
		}
		if !sameSign(mx, d.dx) || !sameSign(mz, d.dz) {
			t.Fatalf("delta (%d,%d): mover first step (%d,%d) does not point at the goal", d.dx, d.dz, mx, mz)
		}
		// Same line: the cross product of the two steps is zero up to the
		// 512-entry table's quantization (one entry is 1/512 of a turn).
		cross := vx*mz - vz*mx
		norm := (absI(vx) + absI(vz)) * (absI(mx) + absI(mz))
		if norm == 0 || absI(cross)*64 > norm {
			t.Fatalf("delta (%d,%d): projectile (%d,%d) and mover (%d,%d) do not travel the same line (cross %d, norm %d)", d.dx, d.dz, vx, vz, mx, mz, cross, norm)
		}
		// Units and projectiles share one direction, expressed in two
		// numberings exactly half a turn apart: a projectile flying the way a
		// unit at heading h travels carries retail's yaw h, and this build's
		// stored yaw h + 0x8000 [06 R-WPN-05 §11].
		if got := combat.RetailYaw(p.Yaw); got != s.Heading {
			t.Fatalf("delta (%d,%d): projectile retail yaw %#04x, mover heading %#04x", d.dx, d.dz, got, s.Heading)
		}
	}
}

func sameSign(v, want int64) bool {
	switch {
	case want > 0:
		return v > 0
	case want < 0:
		return v < 0
	default:
		return v == 0
	}
}

func absI(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
