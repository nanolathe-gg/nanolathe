package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestBattleTerrainSourcesAdmitsDetailWithoutChangingZoom(t *testing.T) {
	c, err := New(Options{Width: 32, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	terrain := &world.Terrain{TileSet: make([][1024]byte, 2)}
	c.SetTerrain(terrain)
	c.SetCamera(&camera.Camera{Scale: camera.ViewScaleNative})
	art := &DetailArt{Tiles: make([][detailTilePixels]byte, 2)}
	c.SetDetailArt(art)
	c.SetEnhanced(true)
	sources := c.BattleTerrainSources()
	if sources.Terrain != terrain || &sources.Detail[0] != &art.Tiles[0] || c.viewScale() != camera.ViewScaleNative {
		t.Fatal("loading preparation lost source identity or changed the live view")
	}
	if c.detailTiles() != nil {
		t.Fatal("native recording must still omit detail art")
	}
	c.SetEnhanced(false)
	if c.BattleTerrainSources().Detail != nil {
		t.Fatal("classic preparation admitted enhanced art")
	}
	c.SetEnhanced(true)
	c.SetDetailArt(&DetailArt{Tiles: art.Tiles[:1]})
	if c.BattleTerrainSources().Detail != nil {
		t.Fatal("short provider must use the recording path's nearest fallback")
	}
	c.SetTerrain(nil)
	if got := c.BattleTerrainSources(); got.Terrain != nil || got.Detail != nil {
		t.Fatal("teardown retained battle sources")
	}
}
