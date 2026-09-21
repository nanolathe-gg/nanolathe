package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// bandOfRecordRect wraps a rectangle that is already in the record space unit
// points project into. The walk tests build one directly instead of converting
// world endpoints; on a rest-factor camera the drawn box is the same rectangle.
func bandOfRecordRect(r Rect) SelectionBand { return SelectionBand{Record: r, Surface: r} }

func bandUnit(slot pool.Handle, x, y, z int32) frame.UnitView {
	return frame.UnitView{
		Slot:  slot,
		Owner: 0,
		X:     numeric.FixedFromInt(int64(x)),
		Y:     numeric.FixedFromInt(int64(y)),
		Z:     numeric.FixedFromInt(int64(z)),
	}
}

// TestSelectionBandCarriesEachEndpointsOwnShear locks the conversion half of
// [07 §9] "Drag-rectangle conversion is closed": each endpoint is projected by
// the ordinary projection with its OWN recorded height, so the vertical
// coordinate is `z − (y >> 1) − cameraZ`, and the two projected corners are
// then sorted independently per axis.
func TestSelectionBandCarriesEachEndpointsOwnShear(t *testing.T) {
	cam := &camera.Camera{X: 10, Z: 20}
	w := func(v int32) numeric.Fixed { return numeric.FixedFromInt(int64(v)) }

	// Endpoints given back to front on both axes, with different heights.
	band := WorldSelectionBand(cam, w(200), w(64), w(300), w(100), w(0), w(150))
	want := Rect{
		MinX: 100 - 10,
		MinY: 150 - 20,
		MaxX: 200 - 10,
		MaxY: 300 - 32 - 20, // the 64-high endpoint sheared by y>>1
	}
	if band.Record != want {
		t.Fatalf("band = %+v, want %+v [07 §9]", band.Record, want)
	}
	if band.Surface != band.Record {
		t.Fatalf("at the native rest factor the drawn box %+v must equal the tested band %+v", band.Surface, band.Record)
	}
	// Dropping the shear would put the high endpoint 32 rows lower.
	if flat := WorldSelectionBand(cam, w(200), 0, w(300), w(100), 0, w(150)); flat.Record.MaxY != want.MaxY+32 {
		t.Fatalf("height is not carried into the endpoint conversion: flat MaxY = %d", flat.Record.MaxY)
	}
}

// TestSelectionBandAgreesWithTheDrawnBoxAtEveryFactor locks the other half of
// the same contract: the rectangle the membership walk tests and the box the
// overlay draws are one band, so they agree at the detail record step and at a
// free zoom factor as well as at native. The band is projected in the record
// space unit points are built in, and mirrored into the surface pixels the
// overlay writes in (DESIGN_GPU_RENDERER §16.3, §16.4).
func TestSelectionBandAgreesWithTheDrawnBoxAtEveryFactor(t *testing.T) {
	// Slots 1 and 2 are inside the dragged world span, 2 only because its own
	// height shears it up into the band; 3 and 4 are outside it.
	f := &frame.Frame{Units: []frame.UnitView{
		bandUnit(1, 100, 0, 100),
		bandUnit(2, 140, 64, 152),
		bandUnit(3, 300, 0, 300),
		bandUnit(4, 120, 0, 172),
	}}
	want := []pool.Handle{1, 2}
	w := func(v int32) numeric.Fixed { return numeric.FixedFromInt(int64(v)) }

	for _, tc := range []struct {
		name string
		cam  camera.Camera
	}{
		{"native", camera.Camera{X: 12, Z: 6}},
		{"detail 2x", camera.Camera{X: 12, Z: 6, Scale: camera.ViewScaleDetail}},
		{"free 1.5x", camera.Camera{X: 12, Z: 6, Zoom: camera.ZoomUnit * 3 / 2}},
	} {
		cam := tc.cam
		band := WorldSelectionBand(&cam, w(88), 0, w(88), w(162), 0, w(162))
		got := SnapshotUnitHandlesInBand(f, &cam, band, 0)
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("%s: band admitted %v, want %v [07 §9]", tc.name, got, want)
		}
		if cam.AtRestStep() && band.Surface != band.Record {
			t.Fatalf("%s: drawn box %+v differs from the tested band %+v at a rest factor", tc.name, band.Surface, band.Record)
		}
		// Every unit the band admits is drawn inside the box, and every unit it
		// rejects is drawn outside it.
		for _, v := range f.Units {
			p := NewViewportTransform(&cam, 0, 0).WorldToSurface(v.X, v.Y, v.Z)
			sx, sy := surfacePoint(&cam, p.X, p.Y)
			admitted := v.Slot == 1 || v.Slot == 2
			if band.Surface.Contains(sx, sy) != admitted {
				t.Fatalf("%s: slot %d drawn at (%d,%d) disagrees with the box %+v", tc.name, v.Slot, sx, sy, band.Surface)
			}
		}
	}
}
