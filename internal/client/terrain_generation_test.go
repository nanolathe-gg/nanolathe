package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestTerrainGenerationSeparatesBattleLifetimes(t *testing.T) {
	c := newTestClient(t)
	first := &world.Terrain{CellW: 2, CellH: 2}
	c.SetTerrain(first)
	installed := c.TerrainGeneration()
	c.SetTerrain(first)
	if c.TerrainGeneration() != installed {
		t.Fatal("reinstalling the current world invalidated its source caches")
	}
	c.SetTerrain(nil)
	retired := c.TerrainGeneration()
	if retired == installed {
		t.Fatal("battle teardown did not retire its source generation")
	}
	c.SetTerrain(nil)
	if c.TerrainGeneration() != retired {
		t.Fatal("remaining in the frontend invalidated its source caches")
	}
	// A restart reloads the same map into a distinct world object.
	c.SetTerrain(&world.Terrain{CellW: first.CellW, CellH: first.CellH})
	if c.TerrainGeneration() == retired || c.TerrainGeneration() == installed {
		t.Fatal("restarting the map reused a retired source generation")
	}
}
