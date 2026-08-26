package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

const cellW = numeric.Fixed(worldUnitsPerCell)

// TestPlacementAnchorRoundsToNearestCell locks retail's `+ (1 << 19)` rounding
// term [07 §9]. An odd footprint sits on a cell and floors; an even one
// straddles a boundary and follows whichever cell the cursor is nearer.
func TestPlacementAnchorRoundsToNearestCell(t *testing.T) {
	cases := []struct {
		name         string
		px, pz       numeric.Fixed
		footX, footZ int32
		wantX, wantZ int32
	}{
		{"odd footprint on cell center", 10*cellW + cellW/2, 10*cellW + cellW/2, 3, 3, 9, 9},
		{"odd footprint at cell origin", 10 * cellW, 10 * cellW, 3, 3, 9, 9},
		{"even footprint below half cell", 10*cellW + cellW/4, 10*cellW + cellW/4, 2, 2, 9, 9},
		{"even footprint above half cell", 10*cellW + 3*cellW/4, 10*cellW + 3*cellW/4, 2, 2, 10, 10},
		{"single cell floors", 10*cellW + cellW - 1, 10 * cellW, 1, 1, 10, 10},
	}
	for _, tc := range cases {
		gotX, gotZ := PlacementAnchor(tc.px, tc.pz, tc.footX, tc.footZ)
		if gotX != tc.wantX || gotZ != tc.wantZ {
			t.Errorf("%s: PlacementAnchor = %d,%d want %d,%d [07 §9]", tc.name, gotX, gotZ, tc.wantX, tc.wantZ)
		}
	}
}

// TestPlacementCenterIsFootprintMidpoint checks the order position retail
// stores: the middle of the footprint, not its corner and not the cursor.
func TestPlacementCenterIsFootprintMidpoint(t *testing.T) {
	for _, foot := range []int32{1, 2, 3, 5} {
		x, z := PlacementCenter(9, 6, foot, foot)
		wantX := CellToWorld(9) + numeric.Fixed(int64(foot)*worldUnitsPerCell/2)
		wantZ := CellToWorld(6) + numeric.Fixed(int64(foot)*worldUnitsPerCell/2)
		if x != wantX || z != wantZ {
			t.Errorf("foot %d: PlacementCenter = %d,%d want %d,%d [07 §9]", foot, x, z, wantX, wantZ)
		}
	}
}

// TestPlacementAnchorRoundTrip checks that placing at a footprint's own center
// re-derives the same anchor, so a queued site re-drawn from its stored
// position lands on the cells it was validated against.
func TestPlacementAnchorRoundTrip(t *testing.T) {
	for _, foot := range []int32{1, 2, 3, 4, 5} {
		for _, cell := range []int32{0, 1, 7, 63} {
			x, z := PlacementCenter(cell, cell, foot, foot)
			gotX, gotZ := PlacementAnchor(x, z, foot, foot)
			if gotX != cell || gotZ != cell {
				t.Errorf("foot %d cell %d: round trip gave %d,%d [07 §9]", foot, cell, gotX, gotZ)
			}
		}
	}
}
