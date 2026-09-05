package combat

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestMuzzleWorldPointIsUnitPlusLocatorOffset locks the muzzle case of
// [03 R-RAST-01 §8], which closed [06 §4.1]'s Unknown on the sense of "added
// to the unit's world position": the muzzle for every slot is the unit's
// position PLUS the piece locator's triple, with no further sign change.
//
// The fixture mounts a flare at authored `(2, 1, 30)`. [03 §2.4] C20's
// load-time half-turn stores that as `(−2, 1, −30)`, and the locator negates
// the composed Z once on output, so on a unit at heading 0 — facing world −Z
// [04 R-MOV-01 §4] — the muzzle is `unit + (−2, 1, +30)`: the authored X
// mirrored, the authored Z kept.
//
// A muzzle on the wrong side of the unit is not only a wrong spawn point: the
// same point is the origin of the aim delta the yaw and pitch solvers are
// handed [06 §3.3], and an inverted ballistic distance word feeds an UNSIGNED
// `T0` divide [06 §6.4].
func TestMuzzleWorldPointIsUnitPlusLocatorOffset(t *testing.T) {
	const (
		authoredX = 2
		authoredY = 1
		authoredZ = 30
	)
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Children: []int{1}},
			{Name: "flare", Parent: 0, Translate: [3]numeric.Fixed{
				numeric.FixedFromInt(-authoredX),
				numeric.FixedFromInt(authoredY),
				numeric.FixedFromInt(-authoredZ),
			}},
		},
	}
	vm := &cob.VM{Pieces: make([]model.PieceState, 2)}
	// COB index 0 is the flare, which is model index 1: the two orders differ
	// so the test cannot pass by reading the wrong piece.
	binding := &cob.Binding{Model: mdl, VM: vm, PieceMap: []int{1, 0}}
	u := &units.Unit{
		X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(7), Z: numeric.FixedFromInt(-20),
		Script: vm, ScriptState: &units.ScriptState{VM: vm, Binding: binding},
	}
	u.Move.Heading, u.Move.Pitch, u.Move.Bank = 0, 0, 0

	got, ok := muzzleWorldPosResolved(u, 0)
	if !ok {
		t.Fatal("strict binding did not resolve the muzzle piece")
	}
	want := Vec3{
		X: u.X.Add(numeric.FixedFromInt(-authoredX)),
		Y: u.Y.Add(numeric.FixedFromInt(authoredY)),
		Z: u.Z.Add(numeric.FixedFromInt(authoredZ)),
	}
	if got != want {
		t.Fatalf("muzzle world point = %#v, want %#v: unit + (−ax, ay, +az) for an authored (%d,%d,%d) at heading 0 [03 R-RAST-01 §8]",
			got, want, authoredX, authoredY, authoredZ)
	}
	// The failure this pins is a second negation at the call site, which would
	// put the flare the same distance behind the unit instead of ahead of it.
	if mirrored := (Vec3{X: want.X, Y: want.Y, Z: u.Z.Sub(numeric.FixedFromInt(authoredZ))}); got == mirrored {
		t.Fatalf("muzzle Z was mirrored a second time: the locator already returns (x, y, −z) [03 R-RAST-01 §8]")
	}
}
