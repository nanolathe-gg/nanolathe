package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestPickSnapshotUnitTieAndImmutable(t *testing.T) {
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	frame := &frame.Frame{Units: []frame.UnitView{
		{Slot: 2, Owner: 0, X: numeric.Fixed(10 << 16), Z: numeric.Fixed(10 << 16)},
		{Slot: 1, Owner: 0, X: numeric.Fixed(10 << 16), Z: numeric.Fixed(10 << 16)},
	}}
	x, y := cam.WorldToScreen(frame.Units[0].X, 0, frame.Units[0].Z)
	h, _, ok := PickSnapshotUnit(frame, x-camera.OriginX, y-camera.OriginY, cam, 0)
	if !ok || h != 1 {
		t.Fatalf("tie winner=%d ok=%v, want lower slot 1", h, ok)
	}
	_, picked, _ := PickSnapshotUnit(frame, x-camera.OriginX, y-camera.OriginY, cam, 0)
	pickedX := picked.X
	frame.Units[0].X += numeric.Fixed(100 << 16)
	if h2, _, _ := PickSnapshotUnit(frame, x-camera.OriginX, y-camera.OriginY, cam, 0); h2 != 1 {
		t.Fatalf("picker changed frame result unexpectedly: %d", h2)
	}
	if picked.X != pickedX {
		t.Fatalf("picked value aliased frame mutation: got %v want %v", picked.X, pickedX)
	}
}

func TestSnapshotUnitHandlesInRectVisibility(t *testing.T) {
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

func TestSnapshotVisibilityRejectsInvalidViewerAndMasks(t *testing.T) {
	frame := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 1, H: 1, Visible: []uint8{1}}, Units: []frame.UnitView{{Slot: 1, Owner: 1, X: 0, Z: 0}}}
	if SnapshotVisible(frame, frame.Units[0], 10) {
		t.Fatal("viewer outside 0..9 must not see foreign unit")
	}
	frame.Visibility.Visible[0] = 0
	if SnapshotVisible(frame, frame.Units[0], 0) {
		t.Fatal("masked foreign unit was visible")
	}
}
