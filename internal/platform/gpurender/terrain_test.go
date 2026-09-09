package gpurender

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/world"
)

// The terrain pass at the detail view scale — contract D4
// (docs/DESIGN_GPU_RENDERER.md §14.5), verified against the classic blitter it
// has to agree with pixel for pixel (§14.7 item 4).

// terrainFixtureTerrain builds a two-tile map whose tiles are distinguishable
// per pixel: tile 0's byte is 1 + (x&7) + 8*(y&7) and tile 1's is that plus 64,
// so a mis-sampled remainder, a transposed axis and a swapped tile all show up
// as a different index rather than as the same flat colour.
func terrainFixtureTerrain() *world.Terrain {
	t := &world.Terrain{
		CellW: 4, CellH: 2, // tile map 2x1 [03 §2.2]
		TileIndices: []uint16{0, 1},
		TileSet:     make([][1024]byte, 2),
	}
	for id := 0; id < 2; id++ {
		for y := 0; y < terrainTileSize; y++ {
			for x := 0; x < terrainTileSize; x++ {
				t.TileSet[id][y*terrainTileSize+x] = byte(1 + (x & 7) + 8*(y&7) + 64*id)
			}
		}
	}
	return t
}

// terrainFixtureDetail builds the 2x tile set of §14.3 for that map, with its
// own pattern at the 64-pixel period so a detail draw cannot be mistaken for a
// doubled native draw.
func terrainFixtureDetail() [][drawlist.DetailTilePixels]byte {
	detail := make([][drawlist.DetailTilePixels]byte, 2)
	for id := 0; id < 2; id++ {
		for y := 0; y < terrainDetailSize; y++ {
			for x := 0; x < terrainDetailSize; x++ {
				detail[id][y*terrainDetailSize+x] = byte(129 + (x & 3) + 4*(y&3) + 16*id)
			}
		}
	}
	return detail
}

// TestTerrainAtlasLayoutFitsDevice checks the page grid the detail scale needs
// for the largest retail tile set. Measured over the 276 map tile sets of a
// retail install, that is Lava & Two Hills at 11,561 tiles: 108x108 squares of
// 64 pixels is 6,912 on a side, which one page holds on a device reporting
// 8,192 or more and which bands into three pages at 4,096 (§14.5). The design
// section's example figure (Painted Desert, 6,875 tiles) is not the largest;
// the check uses the measured maximum instead.
func TestTerrainAtlasLayoutFitsDevice(t *testing.T) {
	const largestRetailTileSet = 11561
	for _, tc := range []struct {
		tiles     int
		maxSide   int
		side      int
		wantCols  int
		wantPages int
	}{
		{tiles: largestRetailTileSet, maxSide: 16384, side: 32, wantCols: 108, wantPages: 1},
		{tiles: largestRetailTileSet, maxSide: 16384, side: 64, wantCols: 108, wantPages: 1},
		{tiles: largestRetailTileSet, maxSide: 8192, side: 64, wantCols: 108, wantPages: 1},
		{tiles: largestRetailTileSet, maxSide: 4096, side: 64, wantCols: 64, wantPages: 3},
		{tiles: 6875, maxSide: 16384, side: 64, wantCols: 83, wantPages: 1},
		{tiles: 6875, maxSide: 4096, side: 64, wantCols: 64, wantPages: 2},
	} {
		cols, rowsPer, pages := terrainAtlasLayout(tc.tiles, tc.side, tc.maxSide)
		if cols != tc.wantCols || pages != tc.wantPages {
			t.Errorf("layout(%d tiles, side %d, max %d) = cols %d, pages %d; want cols %d, pages %d",
				tc.tiles, tc.side, tc.maxSide, cols, pages, tc.wantCols, tc.wantPages)
		}
		if cols*tc.side > tc.maxSide || rowsPer*tc.side > tc.maxSide {
			t.Errorf("layout(%d tiles, side %d, max %d) exceeds the device maximum: %dx%d",
				tc.tiles, tc.side, tc.maxSide, cols*tc.side, rowsPer*tc.side)
		}
		if cols*rowsPer*pages < tc.tiles {
			t.Errorf("layout(%d tiles, side %d, max %d) holds only %d tiles",
				tc.tiles, tc.side, tc.maxSide, cols*rowsPer*pages)
		}
	}
}

// terrainFixtureList records one terrain command at one view scale, with or
// without detail tiles, over a full-window viewport.
func terrainFixtureList(t *world.Terrain, detail [][drawlist.DetailTilePixels]byte,
	cam *camera.Camera, w, h int, scale int32) drawlist.List {
	var list drawlist.List
	list.RecordClear()
	list.RecordTerrain(drawlist.Terrain{
		Terrain: t, Cam: cam,
		OriginX: cam.X, OriginY: cam.Z,
		DstW: int32(w), DstH: int32(h),
		Scale: scale, Detail: detail,
	})
	list.RecordExpand()
	return list
}

// checkTerrainDeviceScale draws the two-tile map at one view scale, with and
// without detail tiles, and compares the device result against the classic
// blitter's bytes for the same record (§14.7 item 4). The classic bytes come
// from client.BlitTerrainDetail, which is the byte writer the modern pass has to
// agree with: same camera, same viewport, same detail tiles.
//
// The camera sits at a position that is not a multiple of the tile size, so the
// intra-tile remainder and the clipped left/top edge are exercised rather than
// only whole tiles.
func checkTerrainDeviceScale(scale int32) error {
	pal := fixturePalette()
	terrain := terrainFixtureTerrain()
	detail := terrainFixtureDetail()
	cam := &camera.Camera{X: 13, Z: 7, Scale: scale}
	// The map is 64x32 world pixels and the camera is inside it, so the window is
	// sized so that both tiles are on screen at either scale and the map's right
	// and bottom edges are too: the void beyond them, the clipped left and top
	// edges and the whole tiles between them all appear in one comparison.
	w, h := 48*int(scale), 24*int(scale)

	for _, withDetail := range []bool{false, true} {
		var tiles [][drawlist.DetailTilePixels]byte
		name := "doubled tiles"
		if withDetail {
			tiles = detail
			name = "detail tiles"
		}
		r, err := NewChecked(&pal, w, h)
		if err != nil {
			return err
		}
		list := terrainFixtureList(terrain, tiles, cam, w, h, scale)
		img := r.Execute(&list, w, h)
		if img == nil {
			return fmt.Errorf("terrain device fixture (%s, scale %d) returned no image", name, scale)
		}
		want := make([]byte, w*h)
		client.BlitTerrainDetail(want, w, h, terrain, cam, tiles)
		// The reference must actually carry both tiles' art, or the comparison
		// below would pass on two empty surfaces. Tile 0's indices are 1..64,
		// tile 1's are 65..128, and the detail tiles' are 129..160.
		var low, high, detailed int
		for _, b := range want {
			switch {
			case b >= 1 && b <= 64:
				low++
			case b >= 65 && b <= 128:
				high++
			case b >= 129:
				detailed++
			}
		}
		if withDetail && scale != 1 {
			if detailed == 0 {
				return fmt.Errorf("terrain scale %d %s: reference has no detail-tile pixels", scale, name)
			}
		} else if low == 0 || high == 0 {
			return fmt.Errorf("terrain scale %d %s: reference has %d pixels of tile 0 and %d of tile 1",
				scale, name, low, high)
		}
		pixels := make([]byte, w*h*4)
		img.ReadPixels(pixels)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				at := (y*w + x) * 4
				if err := checkExactIndex(
					fmt.Sprintf("terrain scale %d %s at (%d,%d)", scale, name, x, y),
					pixels, at, &pal, want[y*w+x]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkTerrainDevicePixels is the device-loop entry point: the native scale
// (which must be unchanged by §14.5) and the detail scale.
func checkTerrainDevicePixels() error {
	if err := checkTerrainDeviceScale(1); err != nil {
		return err
	}
	return checkTerrainDeviceScale(2)
}

// TestTerrainDeviceFixture is opt-in because ordinary tests must not require a
// graphics device (C-G10). The hidden Ebitengine loop in TestMain runs the
// check; this test reports its result.
func TestTerrainDeviceFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device terrain fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}
