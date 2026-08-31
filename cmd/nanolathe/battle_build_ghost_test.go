package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// mapPx is one map pixel in 16.16 world units [03 §2.1].
func mapPx(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }

// TestBuildSiteSnapRoundsToNearestCell locks the round-to-nearest term of the
// site snap [07 R-P0-11 §1 "The site."]:
//
//	cell = (picked - (foot << 19) + (1 << 19)) >> 20
//
// The `+ (1 << 19)` is not decoration. An even footprint straddles a cell
// boundary, so the site follows whichever boundary the pointer is nearer to;
// dropping the term (a plain floor of `picked - foot*8`) biases every even
// footprint one cell north-west. The expected values below are written out
// rather than recomputed from the formula so a re-derived implementation has
// something to disagree with: a floor-only snap answers -1 for pickedPx 8 and
// 0 for pickedPx 24 in the two-cell rows.
func TestBuildSiteSnapRoundsToNearestCell(t *testing.T) {
	cases := []struct {
		name     string
		foot     int32
		pickedPx int32
		wantCell int32
	}{
		// Even footprint: the boundary nearest the pointer wins, with the tie
		// at the exact cell centre resolving to the southern/eastern cell.
		{"even, one pixel north of the tie", 2, 7, -1},
		{"even, tie at the cell centre", 2, 8, 0},
		{"even, just south of the tie", 2, 9, 0},
		{"even, one pixel before the next tie", 2, 23, 0},
		{"even, at the next tie", 2, 24, 1},
		{"even, four cells", 4, 40, 1},
		// Odd footprint: centred on a cell, so the rounding term is absorbed by
		// the half-extent and the result is the pointer's own cell, less the
		// whole half-extent.
		{"odd, start of cell 0", 3, 0, -1},
		{"odd, end of cell 0", 3, 15, -1},
		{"odd, start of cell 1", 3, 16, 0},
		{"odd, five cells in cell 2", 5, 40, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cx, cz := world.PlacementAnchor(mapPx(tc.pickedPx), mapPx(tc.pickedPx), tc.foot, tc.foot)
			if cx != tc.wantCell || cz != tc.wantCell {
				t.Fatalf("anchor for foot %d at map pixel %d = (%d,%d), want (%d,%d)",
					tc.foot, tc.pickedPx, cx, cz, tc.wantCell, tc.wantCell)
			}
		})
	}
}

// TestBuildSiteCentreRoundTripsThroughTheSnap locks the pairing of the two
// halves of the placement contract: the click stores the footprint's centre,
// `((foot + 2*cell) << 19)` per axis, not the raw cursor point, and the ghost
// snap applied to that centre must return the very cell the ghost was drawn on
// [07 R-P0-11 §1 "The site.", "The click."]. The queued build-site marker
// re-derives its rectangle that way, so a change to either half that breaks
// the round trip moves every queued marker off its site.
func TestBuildSiteCentreRoundTripsThroughTheSnap(t *testing.T) {
	for _, foot := range []int32{1, 2, 3, 4, 5, 6, 7, 8} {
		for _, cell := range []int32{0, 1, 7, 13, 128, 255} {
			wx, wz := world.PlacementCenter(cell, cell+3, foot, foot)
			cx, cz := world.PlacementAnchor(wx, wz, foot, foot)
			if cx != cell || cz != cell+3 {
				t.Fatalf("foot %d anchor (%d,%d) round-tripped through centre (%d,%d) to (%d,%d)",
					foot, cell, cell+3, wx.Raw(), wz.Raw(), cx, cz)
			}
		}
	}
}

// TestBuildGhostProjectsBothCornersAtTheSiteHeight locks the projection of the
// ghost rectangle [07 R-P0-11 §1 "The drawing."]:
//
//	left  = cellX*16 - cameraX + 128     top    = cellZ*16          - (h>>1) - cameraZ + 32
//	right = (cellX+footX)*16 - cameraX + 128
//	bottom = (cellZ+footZ)*16 - (h>>1) - cameraZ + 32
//
// Both corners carry the same site height, which is what keeps the ghost flat
// on the ground the building will stand on instead of tilting with the terrain
// under the cursor. Projecting the far corner at a second height would make the
// rectangle's height disagree with the footprint's.
func TestBuildGhostProjectsBothCornersAtTheSiteHeight(t *testing.T) {
	b := &battleSession{cam: &camera.Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}}
	b.cam.X, b.cam.Z = 100, 60

	const cellX, cellZ, footX, footZ int32 = 10, 20, 3, 5
	l0, t0 := cellX*16, cellZ*16
	r0, b0 := (cellX+footX)*16, (cellZ+footZ)*16

	for _, h := range []int32{0, 1, 86, 87, 255} {
		left, top, right, bottom := b.siteRectToScreen(l0, t0, r0, b0, h)
		// The projection subtracts the camera and the baked view origin; the
		// composed world surface is viewport-relative, so both are removed.
		wantLeft := l0 - b.cam.X
		wantRight := r0 - b.cam.X
		wantTop := t0 - (h >> 1) - b.cam.Z
		wantBottom := b0 - (h >> 1) - b.cam.Z
		if left != wantLeft || right != wantRight || top != wantTop || bottom != wantBottom {
			t.Fatalf("h=%d rect (%d,%d)-(%d,%d), want (%d,%d)-(%d,%d)",
				h, left, top, right, bottom, wantLeft, wantTop, wantRight, wantBottom)
		}
		// The shear is the same on both corners, so the drawn rectangle is
		// exactly the footprint however tall the site is.
		if got := bottom - top; got != footZ*16 {
			t.Fatalf("h=%d rectangle height %d, want the footprint's %d — the two corners took different heights",
				h, got, footZ*16)
		}
		if got := right - left; got != footX*16 {
			t.Fatalf("h=%d rectangle width %d, want the footprint's %d", h, got, footX*16)
		}
	}
}
