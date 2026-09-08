package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestDrawFogRefreshesZeroVersionAndReplacementSource(t *testing.T) {
	c := newTestClient(t)
	first := &frame.Frame{Fog: frame.FogView{W: 1, H: 1, Ch0: []uint8{1}, Ch1: []uint8{2}, Valid: true}}
	c.drawFog(first)
	if got, _ := c.fogCache.Channel(0, 0); got != 1 {
		t.Fatalf("first dynamic fog = %d, want 1", got)
	}
	second := &frame.Frame{Fog: frame.FogView{W: 1, H: 1, Ch0: []uint8{3}, Ch1: []uint8{4}, Valid: true}}
	c.drawFog(second)
	if got, _ := c.fogCache.Channel(0, 0); got != 3 {
		t.Fatalf("zero-version fog remained cached: got %d want 3", got)
	}

	first.Fog.Version, first.Fog.Source, first.Fog.Ch0[0] = 1, 10, 5
	c.drawFog(first)
	second.Fog.Version, second.Fog.Source, second.Fog.Ch0[0] = 1, 11, 6
	c.drawFog(second)
	if got, _ := c.fogCache.Channel(0, 0); got != 6 {
		t.Fatalf("replacement fog source remained cached: got %d want 6", got)
	}
}

func TestFogCacheResetsWithTerrainAndSnapshot(t *testing.T) {
	c := newTestClient(t)
	c.fogCache = nil
	c.drawFog(&frame.Frame{Fog: frame.FogView{W: 1, H: 1, Ch0: []uint8{1}, Ch1: []uint8{1}, Valid: true, Version: 1, Source: 1}})
	c.SetTerrain(&world.Terrain{})
	if c.fogCache != nil || c.fogVersion != 0 || c.fogSource != 0 {
		t.Fatal("SetTerrain retained fog cache")
	}
	c.drawFog(&frame.Frame{Fog: frame.FogView{W: 1, H: 1, Ch0: []uint8{1}, Ch1: []uint8{1}, Valid: true, Version: 1, Source: 1}})
	c.SetSnapshot(frame.NewBuffer())
	if c.fogCache != nil || c.fogVersion != 0 || c.fogSource != 0 {
		t.Fatal("SetSnapshot retained fog cache")
	}
}
