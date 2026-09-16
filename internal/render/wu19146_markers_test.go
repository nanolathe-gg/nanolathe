package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/model"
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
