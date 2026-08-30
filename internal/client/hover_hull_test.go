package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// hullTestModel is the same synthetic root piece TestSelectionQuadProjectsRootBounds
// uses: vertices spanning x -8..16, y 0..20, z -12..24 with a one-vertex `wake`
// child that must contribute nothing [07 R-REV-01 §1][03 R-WATER-01 §1].
func hullTestModel() *compiledmodel.Model {
	m := &compiledmodel.Model{
		Root: 0,
		Name: "synthetic-hull",
		Pieces: []compiledmodel.Piece{
			{
				Name:   "base",
				Parent: -1,
				Vertices: [][3]numeric.Fixed{
					{fixedUnits(-8), fixedUnits(0), fixedUnits(-12)},
					{fixedUnits(16), fixedUnits(20), fixedUnits(24)},
					{fixedUnits(4), fixedUnits(5), fixedUnits(6)},
				},
			},
			{
				Name:     "wake",
				Parent:   0,
				Vertices: [][3]numeric.Fixed{{fixedUnits(400), fixedUnits(400), fixedUnits(400)}},
			},
		},
	}
	m.Pieces[0].Children = []int{1}
	return m
}

func hullTestView() frame.UnitView {
	return frame.UnitView{
		Slot:  1,
		Owner: 0,
		Model: "synthetic-hull",
		X:     fixedUnits(100),
		Y:     fixedUnits(0),
		Z:     fixedUnits(200),
	}
}

// TestHoverHullIsTheDrawnSelectionQuad locks the claim the whole picker rests
// on: the hover hull and the selected-unit footprint outline are one rectangle
// [07 R-REV-01 §1-§3][03 R-WATER-01 §1]. If these ever diverge, the clickable
// area stops matching the drawn area.
func TestHoverHullIsTheDrawnSelectionQuad(t *testing.T) {
	c := newTestClient(t)
	m := hullTestModel()
	v := hullTestView()

	drawn, ok := c.selectionQuadScreen(m, v)
	if !ok {
		t.Fatal("selectionQuadScreen reported no quad")
	}
	hull, ok := hoverHullCorners(m, v, c.cam)
	if !ok {
		t.Fatal("hoverHullCorners reported no hull")
	}
	if hull != drawn {
		t.Fatalf("hover hull %v is not the drawn quad %v", hull, drawn)
	}
	// The measured rectangle: a 24x36 ground quad, whose corners are far more
	// than sixteen pixels from the projected unit origin.
	want := [4][2]int32{{92, 212}, {116, 212}, {116, 176}, {92, 176}}
	if hull != want {
		t.Fatalf("hull corners = %v, want %v", hull, want)
	}
}

// TestStrictPolygonRejectsEdgeAndVertexEquality locks [07 R-REV-01 §4]: the
// comparison is strict, so a point exactly on an edge — interior or vertex — is
// outside. There is no tolerance and no widening.
func TestStrictPolygonRejectsEdgeAndVertexEquality(t *testing.T) {
	quad := [][2]int32{{92, 212}, {116, 212}, {116, 176}, {92, 176}}
	if !containsStrictPolygon(104, 194, quad) {
		t.Fatal("interior point was rejected; the edge operand order is inverted")
	}
	for _, p := range [][2]int32{
		{92, 200},  // left edge interior
		{116, 200}, // right edge interior
		{104, 212}, // bottom edge interior
		{104, 176}, // top edge interior
		{92, 212},  // vertex
		{116, 176}, // vertex
	} {
		if containsStrictPolygon(p[0], p[1], quad) {
			t.Fatalf("collinear point %v was admitted; equality must reject", p)
		}
	}
	// One pixel in from each of those edges is inside.
	for _, p := range [][2]int32{{93, 200}, {115, 200}, {104, 211}, {104, 177}} {
		if !containsStrictPolygon(p[0], p[1], quad) {
			t.Fatalf("interior point %v was rejected", p)
		}
	}
	if containsStrictPolygon(104, 194, quad[:2]) {
		t.Fatal("fewer than three vertices must reject")
	}
}

// TestPickSnapshotUnitAdmitsWholeDrawnRectangle is the maintainer's report: a
// click well beyond sixteen pixels from a unit's centre, but inside its drawn
// selection rectangle, selects it; a click just outside that rectangle does not
// [07 R-REV-01 §2-§4].
func TestPickSnapshotUnitAdmitsWholeDrawnRectangle(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	m := hullTestModel()
	src := UnitHullModelFunc(func(string) *compiledmodel.Model { return m })
	f := &frame.Frame{Units: []frame.UnitView{hullTestView()}}

	// The projected origin, which the old radius picker measured from.
	ox, oy := cam.WorldToScreen(f.Units[0].X, f.Units[0].Y, f.Units[0].Z)
	ox, oy = ox-camera.OriginX, oy-camera.OriginY

	inside := [2]int32{115, 177} // inside the rectangle, corner-most pixel but one
	dx, dy := int64(inside[0]-ox), int64(inside[1]-oy)
	if d := dx*dx + dy*dy; d <= 16*16 {
		t.Fatalf("probe point is only %d px^2 from the origin; it must exceed the old 16px radius", d)
	}
	if h, _, ok := PickSnapshotUnit(f, inside[0], inside[1], cam, 0, src); !ok || h != 1 {
		t.Fatalf("click inside the drawn rectangle at %v did not select: handle=%d ok=%v", inside, h, ok)
	}
	for _, p := range [][2]int32{{117, 177}, {91, 200}, {104, 213}, {104, 175}} {
		if _, _, ok := PickSnapshotUnit(f, p[0], p[1], cam, 0, src); ok {
			t.Fatalf("click outside the drawn rectangle at %v selected a unit", p)
		}
	}
	// The rectangle's own edge is outside, exactly as the predicate requires.
	if _, _, ok := PickSnapshotUnit(f, 92, 200, cam, 0, src); ok {
		t.Fatal("click on the rectangle edge selected a unit; equality must reject")
	}
	// A candidate whose model cannot be resolved has no hull and is never picked.
	none := UnitHullModelFunc(func(string) *compiledmodel.Model { return nil })
	if _, _, ok := PickSnapshotUnit(f, inside[0], inside[1], cam, 0, none); ok {
		t.Fatal("unresolvable model produced a hull")
	}
}

// TestPickSnapshotUnitRetainsLowestSlotOnEqualScore locks the reduction's
// Established outcome: candidates are walked in ascending pool order and the
// winner is replaced only on a strictly smaller score, so co-located units
// resolve to the lowest stable slot [07 R-REV-01 §5].
func TestPickSnapshotUnitRetainsLowestSlotOnEqualScore(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	m := hullTestModel()
	src := UnitHullModelFunc(func(string) *compiledmodel.Model { return m })
	high, low := hullTestView(), hullTestView()
	high.Slot, low.Slot = 7, 3
	f := &frame.Frame{Units: []frame.UnitView{high, low}}
	if h, _, ok := PickSnapshotUnit(f, 104, 194, cam, 0, src); !ok || h != 3 {
		t.Fatalf("equal-score winner = %d (ok=%v), want the lowest slot 3", h, ok)
	}
}
