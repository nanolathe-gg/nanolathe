package world

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestCoordsNegative locks the sign-corrected floor conversions [03 §2.1]
// (PLAN_04 WU-04-1, C1): -1 world unit must land in cell -1, not 0, and the
// round trip through CellToWorld must land at the cell origin.
func TestCoordsNegative(t *testing.T) {
	one := numeric.Fixed(-1)
	if got := WorldToCell(one); got != -1 {
		t.Fatalf("WorldToCell(-1) = %d, want -1", got)
	}
	if got := WorldToCell(0); got != 0 {
		t.Fatalf("WorldToCell(0) = %d, want 0", got)
	}
	// One cell covers 16 map pixels; one pixel is 65,536 world units [03 §2.1].
	const pixel = 65536
	const cellWU = 16 * pixel
	if got := WorldToCell(cellWU); got != 1 {
		t.Fatalf("WorldToCell(one cell) = %d, want 1", got)
	}
	if got := WorldToCell(cellWU - 1); got != 0 {
		t.Fatalf("WorldToCell just under one cell = %d, want 0", got)
	}
	if got := WorldToCell(-cellWU); got != -1 {
		t.Fatalf("WorldToCell(-one cell) = %d, want -1", got)
	}
	if got := WorldToCell(-cellWU - 1); got != -2 {
		t.Fatalf("WorldToCell just past -one cell = %d, want -2", got)
	}
	// Tile conversion: one tile covers 32 map pixels.
	if got := WorldToTile(numeric.Fixed(-1)); got != -1 {
		t.Fatalf("WorldToTile(-1) = %d, want -1", got)
	}
	if got := WorldToTile(-32 * pixel); got != -1 {
		t.Fatalf("WorldToTile(-32 pixels) = %d, want -1", got)
	}
	if got := WorldToTile(-32*pixel - 1); got != -2 {
		t.Fatalf("WorldToTile just past -32 pixels = %d, want -2", got)
	}
	// Round trips: cell origins.
	for _, c := range []int32{-3, -1, 0, 1, 7} {
		if back := WorldToCell(CellToWorld(c)); back != c {
			t.Fatalf("cell round trip %d became %d", c, back)
		}
		if back := WorldToTile(TileToWorld(c)); back != c {
			t.Fatalf("tile round trip %d became %d", c, back)
		}
	}
	// A point half a cell into cell -1 stays in cell -1.
	halfCell := CellToWorld(-1) + numeric.Fixed(8*65536)
	if got := WorldToCell(halfCell); got != -1 {
		t.Fatalf("half-cell point landed in cell %d, want -1", got)
	}
}

// TestHeightAtSentinelAtEdges locks HeightAt's retail guard: no neighbour
// clamping; the last row/column and out-of-bounds queries return the raw −1
// sentinel (notes/terrain/01_attribute_cells.md:434).
func TestHeightAtSentinelAtEdges(t *testing.T) {
	ter := &Terrain{CellW: 4, CellH: 4, Plot: make([]PlotCell, 16)}
	for i := range ter.Plot {
		ter.Plot[i][4] = 10
	}
	sentinel := numeric.Fixed(-1)
	// Interior query interpolates normally.
	if got := ter.HeightAt(CellToWorld(1), CellToWorld(1)); got == sentinel || got < 0 {
		t.Fatalf("interior HeightAt returned sentinel %d", got.Raw())
	}
	// Last row/column return the sentinel.
	if got := ter.HeightAt(CellToWorld(3), CellToWorld(1)); got != sentinel {
		t.Fatalf("east-edge HeightAt = %d, want -1 sentinel", got.Raw())
	}
	if got := ter.HeightAt(CellToWorld(1), CellToWorld(3)); got != sentinel {
		t.Fatalf("south-edge HeightAt = %d, want -1 sentinel", got.Raw())
	}
	// Out of bounds on either side returns the sentinel too.
	if got := ter.HeightAt(CellToWorld(-1), CellToWorld(1)); got != sentinel {
		t.Fatalf("west-out HeightAt = %d, want -1 sentinel", got.Raw())
	}
	if got := ter.HeightAt(CellToWorld(1), CellToWorld(9)); got != sentinel {
		t.Fatalf("north-out HeightAt = %d, want -1 sentinel", got.Raw())
	}
	// Interior bilinear with all-equal heights is that height.
	v := ter.HeightAt(CellToWorld(1)+numeric.Fixed(8*65536), CellToWorld(2))
	if math.Abs(float64(v.Raw())/65536.0-10) > 0.01 {
		t.Fatalf("flat interior height = %f, want 10", float64(v.Raw())/65536.0)
	}
}
