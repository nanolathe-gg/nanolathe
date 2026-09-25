package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// pieceLinkLanes are the committed lanes internal/session publishes for its
// authored piece-link fixture (TestPublishedPieceLanesCarryTheScriptLink):
// script pieces [base, barrel, gun, extra] linked to the model
// [base, turret, barrel] as [0, 2, 1, -1] [04 R-COB-01 §4]. gun is the alias:
// the model has no piece of that name, so it animates turret, the piece its
// slot took. extra is beyond the model. Lanes carry no names.
func pieceLinkLanes() []frame.PieceView {
	return []frame.PieceView{
		{Index: 0},
		{Index: 2, Tz: numeric.FixedFromInt(3)},
		{Index: 1, Tx: numeric.FixedFromInt(10)},
		{Index: -1, Ty: numeric.FixedFromInt(7)},
	}
}

// TestModelStatesPoseThroughTheScriptLink composes the session fixture's own
// model with the lanes its script publishes. The alias lane must move turret,
// and the lane beyond the model must reach no piece [04 R-COB-01 §4] [03 §2.4].
func TestModelStatesPoseThroughTheScriptLink(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountDirectory("../session/testdata/piece_link", 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	m, err := expandModelFromFSStrict(fs, "pieceslot")
	if err != nil {
		t.Fatal(err)
	}
	c := newTestClient(t)
	states := c.modelStates(m, pieceLinkLanes())
	f := numeric.FixedFromInt
	want := []compiledmodel.PieceState{
		{},                                     // base
		{Trans: [3]numeric.Fixed{f(10), 0, 0}}, // turret, posed by the gun alias
		{Trans: [3]numeric.Fixed{0, 0, f(3)}},  // barrel
	}
	if len(states) != len(want) {
		t.Fatalf("states = %d pieces, want %d", len(states), len(want))
	}
	for i := range want {
		if states[i] != want[i] {
			t.Fatalf("model piece %d state = %+v, want %+v", i, states[i], want[i])
		}
	}
	rest := make([]compiledmodel.PieceState, len(states))
	moved := compiledmodel.Compose(m.compiled, states, 1).Apply([3]numeric.Fixed{})
	still := compiledmodel.Compose(m.compiled, rest, 1).Apply([3]numeric.Fixed{})
	if moved[0]-still[0] != f(10) || moved[1] != still[1] || moved[2] != still[2] {
		t.Fatalf("turret origin moved %v -> %v, want +10 on X only", still, moved)
	}
}

// TestAliasLaneMovesTheSlotPieceOnScreen draws the same lanes through the
// production model path. The committed lanes must paint exactly what posing
// turret and barrel directly paints, which holds only if the alias lane moved
// turret and the beyond-the-model lane moved nothing; without the alias lane
// the picture differs [04 R-COB-01 §4].
func TestAliasLaneMovesTheSlotPieceOnScreen(t *testing.T) {
	c := newPieceFixtureClient(t)
	pieces := []pieceInfo{
		{name: "base", parent: -1},
		{name: "turret", parent: 0, translate: [3]float64{2, 0, 0}},
		{name: "barrel", parent: 0, translate: [3]float64{3, 0, 0}},
	}
	tris := []syntheticTri{
		makeTriangle(0, "base", [3][3]float64{{-30, 0, 0}, {-24, 0, 0}, {-30, 0, 6}}, 11, 0),
		makeTriangle(1, "turret", [3][3]float64{{0, 0, 20}, {6, 0, 20}, {0, 0, 26}}, 22, 1),
		makeTriangle(2, "barrel", [3][3]float64{{20, 0, -20}, {26, 0, -20}, {20, 0, -14}}, 33, 2),
	}
	c.models["syn_piece_link"] = syntheticModel(pieces, tris, 0)
	draw := func(lanes []frame.PieceView) uint32 {
		t.Helper()
		view := frame.UnitView{Slot: 1, X: numeric.FixedFromInt(100), Z: numeric.FixedFromInt(100), Model: "syn_piece_link", Pieces: lanes}
		clearIndexed(c)
		sx, sy := c.cam.WorldToScreen(view.X, view.Y, view.Z)
		if !c.drawUnitModelReplay(view, sx, sy) {
			t.Fatal("drawUnitModel failed")
		}
		return hashIndexed(c)
	}
	committed := draw(pieceLinkLanes())
	direct := draw([]frame.PieceView{
		{Index: 1, Tx: numeric.FixedFromInt(10)},
		{Index: 2, Tz: numeric.FixedFromInt(3)},
	})
	if committed != direct {
		t.Fatalf("committed lanes painted %08x, posing turret and barrel directly painted %08x", committed, direct)
	}
	withoutAlias := pieceLinkLanes()
	withoutAlias = append(withoutAlias[:2], withoutAlias[3])
	if draw(withoutAlias) == committed {
		t.Fatal("the gun alias lane did not move turret on screen")
	}
}
