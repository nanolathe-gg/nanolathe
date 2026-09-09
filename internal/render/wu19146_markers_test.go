package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// A model projectile's angle block is {roll, yaw, pitch} onto the {Z, Y, X}
// slots, and the `propeller` flag substitutes the spinning angle for word 0 —
// the roll word — with no half-circle offset [03 §5.2][06 R-WFX-01 §4]. The
// slot is the part that is easy to regress silently: feeding Y instead of Z
// still spins something, just the wrong axis, and the site carried exactly that
// mistake behind an open-question marker until this test was written.
func TestPropellerSpinUsesRollSlotWithoutOffset(t *testing.T) {
	st := make([]model.PieceState, 2)
	FoldPropellerSpin(st, 1, 4096)
	if st[1].RotZ != 4096 {
		t.Fatalf("propeller spin Z = %d, want 4096 (block word 0 is the roll slot)", st[1].RotZ)
	}
	if st[1].RotY != 0 || st[1].RotX != 0 {
		t.Fatalf("propeller spin leaked into Y=%d X=%d; only the roll slot is substituted", st[1].RotY, st[1].RotX)
	}

	// No -32768 offset: only the yaw and pitch words carry one, which is what
	// FoldProjectileAngles applies. A zero spin must stay zero.
	zero := make([]model.PieceState, 1)
	FoldPropellerSpin(zero, 0, 0)
	if zero[0].RotZ != 0 {
		t.Fatalf("zero spin folded to %d, want 0 (word 0 takes no half-circle offset)", zero[0].RotZ)
	}

	// The other two words keep their offsets when both folds run on one piece:
	// the propeller substitution touches word 0 only.
	both := make([]model.PieceState, 1)
	FoldProjectileAngles(both, 0, 1000, 2000)
	FoldPropellerSpin(both, 0, 300)
	if both[0].RotY != 1000+halfCircle || both[0].RotX != 2000+halfCircle || both[0].RotZ != 300 {
		t.Fatalf("combined fold Y=%d X=%d Z=%d", both[0].RotY, both[0].RotX, both[0].RotZ)
	}

	// Out-of-range piece indices are a no-op, not a panic.
	FoldPropellerSpin(st, -1, 1)
	FoldPropellerSpin(st, len(st), 1)
}

// A model shadow sits five pixels right of the body and is sheared by the
// terrain height beneath the subject, never by the subject's own height
// [03 §5.3][03 R-REN-03D §3]. Both halves matter: the offset was missing here,
// and the absence of a unit-height term is what makes an aircraft's shadow stay
// on the ground.
func TestShadowScreenVertexOffsetAndGroundShear(t *testing.T) {
	world := [3]numeric.Fixed{numeric.FixedFromInt(100), numeric.FixedFromInt(400), numeric.FixedFromInt(200)}
	ground := numeric.FixedFromInt(60)

	x, y := ShadowScreenVertex(world, ground, 10, 20)
	if x != 100-10+133 {
		t.Fatalf("shadow screen X = %d, want %d (body's +128 plus the shadow's five pixels)", x, 100-10+133)
	}
	if y != 200-(60>>1)-20+32 {
		t.Fatalf("shadow screen Y = %d, want %d", y, 200-(60>>1)-20+32)
	}

	// Raising the subject without moving the ground under it must not move the
	// shadow: no altitude term exists in the placement.
	high := world
	high[1] = numeric.FixedFromInt(4000)
	hx, hy := ShadowScreenVertex(high, ground, 10, 20)
	if hx != x || hy != y {
		t.Fatalf("shadow moved with subject height: (%d,%d) then (%d,%d)", x, y, hx, hy)
	}

	// Raising the ground under it must move it, by half the height.
	_, uy := ShadowScreenVertex(world, numeric.FixedFromInt(80), 10, 20)
	if uy != y-((80-60)>>1) {
		t.Fatalf("ground shear = %d, want %d", uy, y-((80-60)>>1))
	}
}
