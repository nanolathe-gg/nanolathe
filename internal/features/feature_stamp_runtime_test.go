package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestPlaceAtUsesTerrainFeatureStampRect(t *testing.T) {
	terrain := newEmptyTerrain(6, 6)
	def := featureDef("stamped", 0, 0, 10)
	def.FootprintX = 2
	def.FootprintZ = 2
	svc := NewService(terrain, &rng.Simulation{}, nil, nil)

	if inst := svc.PlaceAt(1, 1, def); inst == nil {
		t.Fatal("PlaceAt returned nil")
	}
	for dz := 0; dz < 2; dz++ {
		for dx := 0; dx < 2; dx++ {
			cx, cz := 1+dx, 1+dz
			if dx == 0 && dz == 0 {
				if got := terrain.PlotAt(int32(cx), int32(cz)).Feature(); got != 0 {
					t.Fatalf("anchor feature = %#x, want 0", got)
				}
				continue
			}
			cell := terrain.PlotAt(int32(cx), int32(cz))
			if !cell.IsFringe() {
				t.Fatalf("cell (%d,%d) = %#x, want fringe", cx, cz, cell.Feature())
			}
			got, ok := world.ResolveFeature(terrain.Plot, int(terrain.CellW), int(terrain.CellH), cx, cz)
			if !ok || got != 0 {
				t.Fatalf("cell (%d,%d) resolved to %d/%v, want feature 0", cx, cz, got, ok)
			}
		}
	}
}
