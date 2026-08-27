package visibility

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRebuildFogWindowUsesViewportOriginAndModeBit(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	if s == nil {
		t.Fatal("nil visibility service")
	}
	// Seed one unexplored/fogged tile in each authoritative source.
	s.WordMask()[0] = 0
	grid := s.ByteGrid(0)
	grid[0] = 0
	s.RebuildFogWindow(0, 0, 64, 64)
	cache := s.Fog()
	ox, oz := cache.Origin()
	if ox != -2 || oz != -2 {
		t.Fatalf("window origin got %d,%d want -2,-2", ox, oz)
	}
	w, h := cache.Dimensions()
	if w != 5 || h != 5 {
		t.Fatalf("window dimensions got %d,%d want 5,5", w, h)
	}
	if !s.FogCacheValid() {
		t.Fatal("window rebuild must set the single mode validity bit")
	}
	// The cache is presentation-only and must not alter the authoritative grids.
	if s.WordMask()[0] != 0 || grid[0] != 0 {
		t.Fatal("fog rebuild mutated authoritative visibility stores")
	}
}
