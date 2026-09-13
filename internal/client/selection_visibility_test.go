package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestSelectionPickCommittedUnitTieAndCopy(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	// Both candidates carry the same hull, so their scores tie and the earlier
	// ascending-pool member wins [07 R-REV-01 §5].
	src := UnitHullModelFunc(func(string) *compiledmodel.Model { return hullTestModel() })
	frame := &frame.Frame{Units: []frame.UnitView{
		{Slot: 2, Owner: 0, Model: "synthetic-hull", X: numeric.Fixed(10 << 16), Z: numeric.Fixed(10 << 16)},
		{Slot: 1, Owner: 0, Model: "synthetic-hull", X: numeric.Fixed(10 << 16), Z: numeric.Fixed(10 << 16)},
	}}
	x, y := cam.WorldToScreen(frame.Units[0].X, 0, frame.Units[0].Z)
	h, _, ok := PickSnapshotUnit(frame, x-camera.OriginX, y-camera.OriginY, cam, 0, src)
	if !ok || h != 1 {
		t.Fatalf("tie winner=%d ok=%v, want lower slot 1", h, ok)
	}
	_, picked, _ := PickSnapshotUnit(frame, x-camera.OriginX, y-camera.OriginY, cam, 0, src)
	pickedX := picked.X
	frame.Units[0].X += numeric.Fixed(100 << 16)
	if h2, _, _ := PickSnapshotUnit(frame, x-camera.OriginX, y-camera.OriginY, cam, 0, src); h2 != 1 {
		t.Fatalf("picker changed frame result unexpectedly: %d", h2)
	}
	if picked.X != pickedX {
		t.Fatalf("picked value aliased frame mutation: got %v want %v", picked.X, pickedX)
	}
}

func TestSelectionHandlesInRectVisibility(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	frame := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 4, H: 4, Visible: make([]uint8, 16)}, Units: []frame.UnitView{
		{Slot: 1, Owner: 0, X: numeric.Fixed(4 << 16), Z: numeric.Fixed(4 << 16)},
		{Slot: 2, Owner: 1, X: numeric.Fixed(4 << 16), Z: numeric.Fixed(4 << 16)},
	}}
	x, y := cam.WorldToScreen(frame.Units[0].X, 0, frame.Units[0].Z)
	r := NormalizeRect(x-camera.OriginX, y-camera.OriginY, x-camera.OriginX, y-camera.OriginY)
	h := SnapshotUnitHandlesInRect(frame, cam, r, 0)
	if len(h) != 1 || h[0] != 1 {
		t.Fatalf("visible handles=%v, want own unit only", h)
	}
}

func TestCommittedUnitVisibilityRejectsInvalidViewerAndMasks(t *testing.T) {
	frame := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 1, H: 1, Visible: []uint8{1}}, Units: []frame.UnitView{{Slot: 1, Owner: 1, X: 0, Z: 0}}}
	if SnapshotVisible(frame, frame.Units[0], 10) {
		t.Fatal("viewer outside 0..9 must not see foreign unit")
	}
	frame.Visibility.Visible[0] = 0
	if SnapshotVisible(frame, frame.Units[0], 0) {
		t.Fatal("masked foreign unit was visible")
	}
}

// TestCommittedUnitVisibilityHonorsForeignCloakAndDecloak locks step 2 of the
// gate [03 §3.2][06 §3.1] against the committed hidden-instance input.
//
// Corrected. This test used to set `Flags: 0x4` for "cloaked" and OR in
// `0x1000` for "decloaking", because that is what the gate read. Neither bit
// carries that meaning in the instance flag word: 0x4 is the construction
// layer's start-building edge and 0x1000 is the order pump's active-record
// marker. So the test passed while asserting the opposite of the contract — a
// genuinely cloaked unit was never hidden, and every enemy builder that raised
// the start-building edge disappeared. The gate now reads the published hidden
// input; the timer does not bypass it [06 §3.1].
func TestCommittedUnitVisibilityHonorsForeignCloakAndDecloak(t *testing.T) {
	f := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 1, H: 1, CoverageBytes: true, Visible: []uint8{1}}}
	cloaked := frame.UnitView{Slot: 1, Owner: 1, Cloaked: true, X: 0, Z: 0}
	if SnapshotVisible(f, cloaked, 0) {
		t.Fatal("foreign cloaked unit bypassed the committed visibility gate")
	}
	decloaked := cloaked
	decloaked.Decloaking = true
	if SnapshotVisible(f, decloaked, 0) {
		t.Fatal("decloak timer bypassed the published hidden-instance bit [06 §3.1]")
	}
	// The play-test defect: a foreign factory that has begun building carries
	// the start-building edge, which is instance flag bit 2. It is not a cloak
	// input and must not hide the factory behind its own nanoframe and spray.
	building := frame.UnitView{Slot: 2, Owner: 1, Flags: 1 << 2, X: 0, Z: 0}
	if !SnapshotVisible(f, building, 0) {
		t.Fatal("a foreign builder vanished the moment it raised the start-building edge")
	}
	// The order pump's active-record marker is likewise not a decloak.
	stillCloaked := cloaked
	stillCloaked.Flags |= 0x1000
	if SnapshotVisible(f, stillCloaked, 0) {
		t.Fatal("the order pump's active marker was read as a decloak timer")
	}
}

func TestCommittedUnitVisibilityUsesShearAndWordCoverage(t *testing.T) {
	// X/Z are 16.16 world coordinates. The 32-pixel tile is selected after
	// narrowing to map pixels and applying the half-height shear: (32,64,32)
	// projects to tile (1,1), index 3 in this 2x2 word grid [03 §3.2].
	vis := frame.VisibilityView{
		Valid:       true,
		W:           2,
		H:           2,
		WordVisible: []uint16{0, 0, 0, 1 << 0},
	}
	f := &frame.Frame{Visibility: vis}
	u := frame.UnitView{Slot: 1, Owner: 1, X: numeric.Fixed(32 << 16), Y: numeric.Fixed(32 << 16), Z: numeric.Fixed(64 << 16)}
	if !SnapshotVisible(f, u, 0) {
		t.Fatal("word-grid fallback did not apply sheared 32-pixel projection")
	}
}
