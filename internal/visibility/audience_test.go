package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// pixelWorld returns a 16.16 world coordinate for a map-pixel value [03 §2.1].
func pixelWorld(p int64) numeric.Fixed { return numeric.Fixed(p << 16) }

// TestAudiblePointProjectsWithTheHeightShear locks the audience gate of
// [03 §8.3] to the projected point of [03 §3.2] step 4 in both coverage modes.
//
// Map pixel (32, 64, 64) projects to coverage cell (1, 1): u = 32>>5 = 1 and
// v = (64 - (64>>1))>>5 = 1. Dropping the height term lands on (1, 2)
// instead, which is the cell under an aircraft rather than the cell it
// occupies on screen, so both cells are asserted in both directions.
func TestAudiblePointProjectsWithTheHeightShear(t *testing.T) {
	x, y, z := pixelWorld(32), pixelWorld(64), pixelWorld(64)

	t.Run("explored byte grid", func(t *testing.T) {
		s := New(&world.Terrain{CellW: 8, CellH: 8}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.byteGrids[0][1*4+1] = 1
		if !s.AudiblePoint(0, x, y, z) {
			t.Fatal("projected cell (1,1) explored: want audible")
		}
		s.byteGrids[0][1*4+1] = 0
		s.byteGrids[0][2*4+1] = 1
		if s.AudiblePoint(0, x, y, z) {
			t.Fatal("only the unsheared cell (1,2) explored: want silent")
		}
	})

	t.Run("LOS word mask", func(t *testing.T) {
		s := New(&world.Terrain{CellW: 8, CellH: 8}, ModeHistoryEnabled)
		s.wordMask[1*4+1] = cellBit(0)
		if !s.AudiblePoint(0, x, y, z) {
			t.Fatal("projected cell (1,1) lit: want audible")
		}
		s.wordMask[1*4+1] = 0
		s.wordMask[2*4+1] = cellBit(0)
		if s.AudiblePoint(0, x, y, z) {
			t.Fatal("only the unsheared cell (1,2) lit: want silent")
		}
	})
}

// TestAudiblePointRejectsOffGridAndInvalidSlot locks the unsigned bounds test
// and the ten-slot player range [03 §3.2] step 4, [03 §3.1].
func TestAudiblePointRejectsOffGridAndInvalidSlot(t *testing.T) {
	s := New(&world.Terrain{CellW: 8, CellH: 8}, ModeHistoryEnabled|ModeCurrentEnabled)
	for i := range s.byteGrids[0] {
		s.byteGrids[0][i] = 1
	}
	x, y, z := pixelWorld(32), pixelWorld(64), pixelWorld(64)
	if !s.AudiblePoint(0, x, y, z) {
		t.Fatal("precondition: the fully explored grid must admit the projected point")
	}
	if s.AudiblePoint(0, pixelWorld(-1), 0, z) {
		t.Fatal("a negative projected column must be rejected, not wrapped into the grid")
	}
	if s.AudiblePoint(0, x, pixelWorld(64), 0) {
		t.Fatal("the shear can carry an in-bounds Z off the north edge; that must be rejected")
	}
	if s.AudiblePoint(0, pixelWorld(128), 0, z) {
		t.Fatal("a column past the east edge must be rejected")
	}
	if s.AudiblePoint(10, x, y, z) {
		t.Fatal("a slot outside the ten player bits must not alias slot zero")
	}
}
