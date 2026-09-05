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

// A wholly unexplored map must reach the solid unexplored value on every cell
// the border touches, not partial cloud art: the four conditional border fixups
// propagate each in-map edge tile's fogged state into the void line beside it,
// and the passes compound at the corners [03 §3.3] "Map-edge propagation".
func TestRebuildFogBorderReachesSolidUnexploredOnEveryEdge(t *testing.T) {
	const cells = 8 // visibility tiles; the terrain grid is twice that
	s := New(&world.Terrain{CellW: cells * 2, CellH: cells * 2}, ModeHistoryEnabled|ModeCurrentEnabled)
	if s == nil {
		t.Fatal("nil visibility service")
	}
	// Nothing explored, nothing currently lit: every tile is fogged in both
	// channels, so every cell the map touches must resolve to the solid value.
	for i := range s.WordMask() {
		s.WordMask()[i] = 0
	}
	grid := s.ByteGrid(0)
	for i := range grid {
		grid[i] = 0
	}
	// A window wide enough to cross all four edges at once.
	s.RebuildFogWindow(0, 0, cells*32, cells*32)
	cache := s.Fog()
	for gz := int32(-1); gz < cells; gz++ {
		for gx := int32(-1); gx < cells; gx++ {
			ox, oz := cache.Origin()
			c0, c1 := cache.Channel(gx-ox, gz-oz)
			if c0 != 15 || c1 != 15 {
				t.Fatalf("cell (%d,%d) resolved to ch0=%d ch1=%d, want the solid unexplored 15 on both channels [03 §3.3]", gx, gz, c0, c1)
			}
		}
	}
}

// An explored map border leaves the void line beside it untouched: the fixups
// are conditional ORs on the in-map tile's own state, never unconditional
// stores [03 §3.3] "Map-edge propagation".
func TestRebuildFogBorderLeavesExploredEdgeTransparent(t *testing.T) {
	const cells = 8 // visibility tiles; the terrain grid is twice that
	s := New(&world.Terrain{CellW: cells * 2, CellH: cells * 2}, ModeHistoryEnabled|ModeCurrentEnabled)
	if s == nil {
		t.Fatal("nil visibility service")
	}
	word := s.WordMask()
	grid := s.ByteGrid(0)
	for i := range word {
		word[i] = cellBit(0)
		grid[i] = 1
	}
	s.RebuildFogWindow(0, 0, cells*32, cells*32)
	cache := s.Fog()
	ox, oz := cache.Origin()
	for gz := int32(-1); gz < cells; gz++ {
		for gx := int32(-1); gx < cells; gx++ {
			if c0, c1 := cache.Channel(gx-ox, gz-oz); c0 != 0 || c1 != 0 {
				t.Fatalf("explored cell (%d,%d) got ch0=%d ch1=%d, want transparent", gx, gz, c0, c1)
			}
		}
	}
}
