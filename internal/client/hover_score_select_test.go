package client

// PT3-06: a click on an aircraft flying over a building selected the building.
// These tests lock the retail overlap rule that decides it — the hover
// reduction's size score [07 R-REV-01 §7-§10] — not an altitude or draw-order rule.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// boxModel is a single root piece whose vertices span the given half-extents,
// with y running 0..height. Three vertices are the minimum the hull bounds
// helper accepts [07 R-REV-01 §1].
func boxModel(name string, halfX, height, halfZ float64) *compiledmodel.Model {
	return &compiledmodel.Model{
		Root: 0,
		Name: name,
		Pieces: []compiledmodel.Piece{{
			Name:   "base",
			Parent: -1,
			Vertices: [][3]numeric.Fixed{
				{fixedUnits(-halfX), fixedUnits(0), fixedUnits(-halfZ)},
				{fixedUnits(halfX), fixedUnits(height), fixedUnits(halfZ)},
				{fixedUnits(0), fixedUnits(height), fixedUnits(0)},
			},
		}},
	}
}

// TestModelTotalHeightIsTheZeroSeededVerticalWalk locks [07 R-REV-01 §7]: the
// height accumulator is seeded at zero at every level, each node's own
// translation is added to its vertices and to whatever its child chain
// returned, and a subtree that lies entirely below its parent contributes
// nothing rather than lowering the result.
func TestModelTotalHeightIsTheZeroSeededVerticalWalk(t *testing.T) {
	if got := modelTotalHeight(nil); got != 0 {
		t.Fatalf("nil model height = %d, want 0", got)
	}

	// Root vertices reach 20; a child translated up by 30 carrying a vertex at
	// 5 reaches 35 and wins.
	m := &compiledmodel.Model{
		Root: 0,
		Pieces: []compiledmodel.Piece{
			{
				Name:     "base",
				Parent:   -1,
				Vertices: [][3]numeric.Fixed{{0, fixedUnits(20), 0}, {0, fixedUnits(-4), 0}},
				Children: []int{1, 2},
			},
			{
				Name:      "mast",
				Parent:    0,
				Translate: [3]numeric.Fixed{0, fixedUnits(30), 0},
				Vertices:  [][3]numeric.Fixed{{0, fixedUnits(5), 0}},
			},
			{
				// Entirely below the origin: contributes zero, not -50.
				Name:      "keel",
				Parent:    0,
				Translate: [3]numeric.Fixed{0, fixedUnits(-40), 0},
				Vertices:  [][3]numeric.Fixed{{0, fixedUnits(-10), 0}},
			},
		},
	}
	if got, want := modelTotalHeight(m), fixedUnits(35); got != want {
		t.Fatalf("model total height = %d, want %d", got, want)
	}

	// The root's own translation is part of the height.
	m.Pieces[0].Translate[1] = fixedUnits(7)
	if got, want := modelTotalHeight(m), fixedUnits(42); got != want {
		t.Fatalf("model total height with root translation = %d, want %d", got, want)
	}

	// A model whose every vertex is below the origin has height zero.
	low := boxModel("low", 4, -6, 4)
	low.Pieces[0].Vertices = [][3]numeric.Fixed{{0, fixedUnits(-6), 0}, {0, fixedUnits(-9), 0}, {0, fixedUnits(-1), 0}}
	if got := modelTotalHeight(low); got != 0 {
		t.Fatalf("all-negative model height = %d, want 0 (zero-seeded)", got)
	}
}

// TestHoverScoreIsTheDefinitionSizeReduction locks the reduction arithmetic and
// its term order [07 R-REV-01 §8]:
//
//	score = ((((yExtent * 32768) >> 16) + zExtent) * xExtent) >> 16
//
// with the X and Z extents being the authored footprint scaled by sixteen world
// units in 16.16 and the Y extent the model total height.
func TestHoverScoreIsTheDefinitionSizeReduction(t *testing.T) {
	m := boxModel("plant", 64, 40, 64)
	// yExtent = 40 world units in 16.16; footprint 8x8.
	x := int32(8) << 20
	z := int32(8) << 20
	y := int32(fixedUnits(40))
	want := int32((int64(int32((int64(y)*32768)>>16)+z) * int64(x)) >> 16)
	if got := hoverScore(m, 8, 8); got != want {
		t.Fatalf("hoverScore = %d, want %d", got, want)
	}
	// A zero X footprint annihilates the product, exactly as retail's does.
	if got := hoverScore(m, 0, 8); got != 0 {
		t.Fatalf("hoverScore with zero X footprint = %d, want 0", got)
	}
	// The score is monotone in size, which is the property the pick depends on.
	small := boxModel("fighter", 8, 6, 10)
	if hoverScore(small, 2, 2) >= hoverScore(m, 8, 8) {
		t.Fatal("a 2x2 fighter must score below an 8x8 plant")
	}
}

// TestPickPrefersAircraftOverBuildingBeneathIt is PT3-06. A plane hovering over
// an aircraft plant is inside both hulls; retail's reduction gives the smaller
// definition the pick, so the click resolves to the plane even though the plant
// occupies the lower pool slot [07 R-REV-01 §8-§9][07 R-REV-01 §5].
//
// Before the score reduction landed this test failed: with every admitted
// candidate scoring equally, the lowest-slot rule handed the click to the
// plant.
func TestPickPrefersAircraftOverBuildingBeneathIt(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	plant := boxModel("plant", 64, 40, 64)
	plane := boxModel("plane", 8, 6, 10)
	src := UnitHullModelFunc(func(name string) *compiledmodel.Model {
		switch name {
		case "plant":
			return plant
		case "plane":
			return plane
		}
		return nil
	})

	plantView := frame.UnitView{
		Slot: 1, Owner: 0, Model: "plant",
		X: fixedUnits(500), Y: fixedUnits(0), Z: fixedUnits(500),
		FootX: 8, FootZ: 8, IsBuilding: true,
	}
	planeView := frame.UnitView{
		Slot: 2, Owner: 0, Model: "plane",
		X: fixedUnits(500), Y: fixedUnits(60), Z: fixedUnits(500),
		FootX: 2, FootZ: 2, MoverMode: 2,
	}
	f := &frame.Frame{Units: []frame.UnitView{plantView, planeView}}

	// Click the centre of the plane's projected hull.
	hull, ok := hoverHullCorners(plane, planeView, cam)
	if !ok {
		t.Fatal("plane hull did not project")
	}
	cx := (hull[0][0] + hull[2][0]) / 2
	cy := (hull[0][1] + hull[2][1]) / 2
	if !containsStrictPolygon(cx, cy, hull[:]) {
		t.Fatalf("probe %d,%d is not inside the plane hull %v", cx, cy, hull)
	}
	// The point must also be inside the plant's hull, or the test would prove
	// nothing about overlap resolution.
	plantHull, ok := hoverHullCorners(plant, plantView, cam)
	if !ok {
		t.Fatal("plant hull did not project")
	}
	if !containsStrictPolygon(cx, cy, plantHull[:]) {
		t.Fatalf("probe %d,%d is not inside the plant hull %v; the fixture does not overlap", cx, cy, plantHull)
	}
	// The plant holds the lower slot, so an equal-score reduction would pick it.
	if planeView.Slot <= plantView.Slot {
		t.Fatal("fixture must give the building the lower pool slot")
	}

	h, got, ok := PickSnapshotUnit(f, cx, cy, cam, 0, src)
	if !ok || h != planeView.Slot || got.Model != "plane" {
		t.Fatalf("click over the plane picked handle %d (%q, ok=%v), want the plane at slot %d",
			h, got.Model, ok, planeView.Slot)
	}

	// Off the plane but still over the plant, the plant is picked: the rule is
	// the size score, not an unconditional aircraft precedence.
	ex := plantHull[0][0] + 4
	ey := (plantHull[0][1] + plantHull[2][1]) / 2
	if containsStrictPolygon(ex, ey, hull[:]) {
		t.Fatalf("probe %d,%d should be outside the plane hull", ex, ey)
	}
	if !containsStrictPolygon(ex, ey, plantHull[:]) {
		t.Fatalf("probe %d,%d should be inside the plant hull", ex, ey)
	}
	if h, got, ok := PickSnapshotUnit(f, ex, ey, cam, 0, src); !ok || h != plantView.Slot {
		t.Fatalf("click clear of the plane picked handle %d (%q, ok=%v), want the plant at slot %d",
			h, got.Model, ok, plantView.Slot)
	}
}
