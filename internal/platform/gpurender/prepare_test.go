package gpurender

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func checkPreparedTerrainDevicePixels() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	defer r.ResetSources()
	terrain := terrainFixtureTerrain()
	for _, detail := range [][][drawlist.DetailTilePixels]byte{nil, terrainFixtureDetail()} {
		cam := &camera.Camera{X: 13, Z: 7, Scale: camera.ViewScaleDetail}
		list := terrainFixtureList(terrain, detail, cam, 80, 48, camera.ViewScaleDetail)
		cold := make([]byte, 80*48*4)
		r.Execute(&list, 80, 48).ReadPixels(cold)
		r.ResetSources()
		r.PrepareTerrain(drawlist.Terrain{Terrain: terrain, Detail: detail})
		atlas := r.atlasFor(terrain, detail, camera.ViewScaleDetail)
		prepared := make([]byte, len(cold))
		r.Execute(&list, 80, 48).ReadPixels(prepared)
		if !bytes.Equal(cold, prepared) || len(r.tileAtlases) != 2 || r.atlasFor(terrain, detail, camera.ViewScaleDetail) != atlas {
			return fmt.Errorf("prepared terrain changed device pixels or replaced its atlas")
		}
		r.ResetSources()
	}
	return nil
}

func TestPrepareTerrainReusesDrawKeysAndReleasesBothScales(t *testing.T) {
	skipAfterDeviceLoop(t)
	for _, detail := range [][][drawlist.DetailTilePixels]byte{nil, terrainFixtureDetail()} {
		r := &Renderer{tileAtlases: make(map[tileAtlasKey]*tileAtlas)}
		terrain := terrainFixtureTerrain()
		r.PrepareTerrain(drawlist.Terrain{Terrain: terrain, Detail: detail})
		native := r.atlasFor(terrain, nil, camera.ViewScaleNative)
		zoom := r.atlasFor(terrain, detail, camera.ViewScaleDetail)
		if native == nil || zoom == nil || native == zoom || len(r.tileAtlases) != 2 {
			t.Fatal("preparation did not create precisely the native and detail draw keys")
		}
		r.PrepareTerrain(drawlist.Terrain{Terrain: terrain, Detail: detail})
		if r.atlasFor(terrain, nil, camera.ViewScaleNative) != native || r.atlasFor(terrain, detail, camera.ViewScaleDetail) != zoom {
			t.Fatal("repeated preparation replaced cached pages")
		}
		if allocations := testing.AllocsPerRun(10, func() { r.atlasFor(terrain, detail, camera.ViewScaleDetail) }); allocations != 0 {
			t.Fatalf("warm detail lookup allocated %v times", allocations)
		}
		released := make(map[*ebiten.Image]int)
		r.resetSources(func(img *ebiten.Image) { released[img]++ })
		for _, atlas := range []*tileAtlas{native, zoom} {
			for _, img := range atlas.pages {
				if released[img] != 1 {
					t.Fatal("prepared source page did not retire exactly once")
				}
			}
		}
	}
}
