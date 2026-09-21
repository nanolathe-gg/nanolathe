package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestDragBandIsTakenFromTheWorldEndpointsNotTheCamera locks [07 §9]
// "Drag-rectangle conversion is closed": both endpoints are recorded as whole
// three-component world points and converted at the moment the rectangle is
// tested, so both sides of the comparison are in the world frame. A drag whose
// camera moved between press and release therefore admits exactly the handles a
// stationary drag over the same world span admits.
//
// The band used to be built from the pointer pixels stored at press and at the
// last motion. Those stay in the press-time screen frame while the unit points
// move with the camera, so a drag that reached the edge-scroll strip — or that
// took a wheel notch — shifted the accepted set by the accumulated camera
// travel, and the drawn box drifted off the terrain with it.
func TestDragBandIsTakenFromTheWorldEndpointsNotTheCamera(t *testing.T) {
	cam := &camera.Camera{}
	b := &battleSession{
		sess: &session.Session{LocalOwner: 0},
		cat:  &content.Catalog{Units: map[string]*content.UnitDef{}},
		cam:  cam,
	}
	at := func(slot pool.Handle, x, y, z int32) frame.UnitView {
		return frame.UnitView{
			Slot: slot, Owner: 0, DefName: "u",
			Flags: units.ClassifierEligibleStatus,
			X:     numeric.FixedFromInt(int64(x)),
			Y:     numeric.FixedFromInt(int64(y)),
			Z:     numeric.FixedFromInt(int64(z)),
		}
	}
	f := &frame.Frame{Units: []frame.UnitView{
		at(1, 100, 0, 100),  // inside the dragged span
		at(2, 140, 64, 152), // inside only because its own height shears it up
		at(3, 300, 0, 300),  // outside on x
		at(4, 120, 0, 172),  // outside on z
	}}
	in := ui.BattleInputState{
		DragActive:      true,
		DragStartWorldX: 88, DragStartWorldZ: 88,
		DragEndWorldX: 162, DragEndWorldZ: 162,
	}

	stationary := b.eligibleHandlesInBand(f, b.selectionBand(in))
	want := []pool.Handle{1, 2}
	if len(stationary) != len(want) || stationary[0] != want[0] || stationary[1] != want[1] {
		t.Fatalf("stationary drag admitted %v, want %v [07 §9]", stationary, want)
	}

	// The camera scrolls while the button is still down. Only the moving
	// endpoint follows the pointer; the recorded world points are untouched.
	pressBand := b.selectionBand(in)
	cam.X, cam.Z = 37, 11
	movedBand := b.selectionBand(in)
	if movedBand.Record == pressBand.Record {
		t.Fatal("camera move did not change the projected band; the test proves nothing")
	}
	moved := b.eligibleHandlesInBand(f, movedBand)
	if len(moved) != len(want) || moved[0] != want[0] || moved[1] != want[1] {
		t.Fatalf("drag with a moving camera admitted %v, want %v [07 §9]", moved, want)
	}

	// The drawn box is the same band, so it moves with the camera too.
	if movedBand.Surface == pressBand.Surface {
		t.Fatal("the published box did not follow the camera")
	}
	if movedBand.Surface != movedBand.Record {
		t.Fatalf("at the native factor the box %+v must equal the tested band %+v", movedBand.Surface, movedBand.Record)
	}
}
