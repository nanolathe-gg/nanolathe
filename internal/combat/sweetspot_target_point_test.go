package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// sweetSpotFixture builds a target whose script answers `SweetSpot` with the
// given piece and whose model carries the given per-piece vertex clouds.
func sweetSpotFixture(piece int32, pieces []model.Piece) *units.Unit {
	prog := &cob.Program{
		// SweetSpot: push piece; pop local 0; return [04 §4.3]
		Code:        []uint32{0x10021001, uint32(piece), 0x10023002, 0, 0x10065000},
		Scripts:     map[string]int{"SweetSpot": 0},
		ScriptsByID: []int{0},
		Pieces:      make([]string, len(pieces)),
	}
	pieceMap := make([]int, len(pieces))
	for i := range pieces {
		prog.Pieces[i] = pieces[i].Name
		pieceMap[i] = i
	}
	vm := cob.NewVM(prog)
	u := &units.Unit{
		X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(7), Z: numeric.FixedFromInt(-20),
		Script: vm,
	}
	u.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{
		VM: vm, Callbacks: cob.NewCallbackBridge(vm),
		Model: &model.Model{Root: 0, Pieces: pieces}, PieceMap: pieceMap,
	}}
	return u
}

func fi(v int64) numeric.Fixed { return numeric.FixedFromInt(v) }

// TestUnitTargetPointIsSweetSpotVertexBoxCentre locks the live-unit outcome of
// the target-point resolver [06 R-WPN-04 §1]: the target's position plus the
// centre of the `SweetSpot` piece's own vertex bounding box, seeded at the
// piece origin, with no hierarchy, no piece state and no output Z negation.
func TestUnitTargetPointIsSweetSpotVertexBoxCentre(t *testing.T) {
	body := model.Piece{Name: "body", Parent: 0,
		// A parent offset that must NOT enter the answer [06 R-WPN-04 §1].
		Translate: [3]numeric.Fixed{fi(50), fi(50), fi(50)},
		Vertices:  [][3]numeric.Fixed{{fi(-2), fi(0), fi(-4)}, {fi(6), fi(10), fi(8)}},
	}
	u := sweetSpotFixture(1, []model.Piece{{Name: "base", Parent: -1, Children: []int{1}}, body})
	// The script moved the piece, and that must not enter either.
	u.ScriptState.VM.Pieces[1].Trans = [3]numeric.Fixed{fi(9), fi(9), fi(9)}
	got := UnitTargetPoint(u)
	want := Vec3{X: u.X.Add(fi(2)), Y: u.Y.Add(fi(5)), Z: u.Z.Add(fi(2))}
	if got != want {
		t.Fatalf("target point = %v, want %v: position + (max+min)/2 over the piece's own vertices [06 R-WPN-04 §1]", got, want)
	}
	if got.Z == u.Z.Sub(fi(2)) {
		t.Fatal("the vertex-box centre's Z was negated; the resolver adds the model-space triple as-is [06 R-WPN-04 §1]")
	}
}

// TestSweetSpotBoxIsSeededAtThePieceOrigin locks the seed: a cloud lying
// wholly on one side of its origin gets a centre pulled toward the origin,
// and the halving of the raw 16.16 words truncates toward zero on both signs
// [06 R-WPN-04 §1] [I3].
func TestSweetSpotBoxIsSeededAtThePieceOrigin(t *testing.T) {
	// Y and Z are odd RAW words (3/65536 and −3/65536) so the halving's
	// rounding is observable; X is in whole units.
	oneSided := model.Piece{Name: "body", Parent: 0,
		Vertices: [][3]numeric.Fixed{{fi(4), numeric.Fixed(3), numeric.Fixed(-3)}, {fi(8), numeric.Fixed(3), numeric.Fixed(-3)}},
	}
	u := sweetSpotFixture(1, []model.Piece{{Name: "base", Parent: -1, Children: []int{1}}, oneSided})
	got := UnitTargetPoint(u)
	// X: box 0..8 → 4 (not the cloud's own 6); Y: box 0..3 raw → 1 raw
	// (truncated); Z: box −3..0 raw → −1 raw (toward zero, not −2).
	want := Vec3{X: u.X.Add(fi(4)), Y: u.Y.Add(numeric.Fixed(1)), Z: u.Z.Add(numeric.Fixed(-1))}
	if got != want {
		t.Fatalf("target point = %v, want %v: box seeded at the origin, raw halves truncated toward zero [06 R-WPN-04 §1]", got, want)
	}
}

// TestSweetSpotAbsentOrEmptyPieceAimsAtTheUnitPosition locks the two
// degenerate answers: a script without `SweetSpot` leaves the zero seed, so
// piece 0 is used, and a piece with no vertices yields the unit position
// exactly [06 R-WPN-04 §1][06 R-WPN-03 §6].
func TestSweetSpotAbsentOrEmptyPieceAimsAtTheUnitPosition(t *testing.T) {
	u := sweetSpotFixture(1, []model.Piece{
		{Name: "base", Parent: -1, Children: []int{1}},
		{Name: "body", Parent: 0, Vertices: [][3]numeric.Fixed{{fi(4), fi(4), fi(4)}}},
	})
	// Drop the SweetSpot entry: cell 0 keeps its zero seed → piece 0, which
	// has no vertices.
	u.ScriptState.VM = cob.NewVM(&cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{"Create": 0}, ScriptsByID: []int{0}, Pieces: []string{"base", "body"}})
	u.ScriptState.Binding.VM = u.ScriptState.VM
	u.ScriptState.Binding.Callbacks = cob.NewCallbackBridge(u.ScriptState.VM)
	if got, want := UnitTargetPoint(u), (Vec3{X: u.X, Y: u.Y, Z: u.Z}); got != want {
		t.Fatalf("no SweetSpot entry: target point = %v, want the unit position %v (piece 0, no vertices) [06 R-WPN-04 §1]", got, want)
	}
	bare := &units.Unit{X: fi(1), Y: fi(2), Z: fi(3)}
	if got, want := UnitTargetPoint(bare), (Vec3{X: fi(1), Y: fi(2), Z: fi(3)}); got != want {
		t.Fatalf("unbound target: target point = %v, want the unit position %v", got, want)
	}
}
