package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

// TestFogWindowMatchesClippedFullBuild locks BuildFogOpsWindowInto against the
// map-wide builder it replaces in the composer: for every camera position, the
// windowed operations must be exactly the map-wide operations that survive the
// composer's clip, in the same order.
//
// The clip is reproduced here because that is the contract. The composer
// rebases each operation off the retail viewport origin and then drops it if
// the clipped rectangle is empty; the windowed builder claims to reach the same
// set without building the rest. A phase error of one tile in either direction
// would show up as a missing or extra fog cell at a screen edge, which is
// exactly the defect a full-screen capture of a mostly-explored map hides.
func TestFogWindowMatchesClippedFullBuild(t *testing.T) {
	const gw, gh = 24, 24
	cache := testFogCache(t, gw, gh)
	// A mix of never-seen, fogged-but-explored and GAF-valued cells, so the
	// builder emits every operation kind including the cells that emit two.
	for row := int32(0); row < gh; row++ {
		for col := int32(0); col < gw; col++ {
			c0 := uint8((col*3 + row*5) % 16)
			c1 := uint8((col*7 + row*11) % 16)
			cache.SetChannel(col, row, c0, c1)
		}
	}

	surfW, surfH := int32(320), int32(200)
	// Cameras inside the grid, off each edge, and at negative and non-tile
	// -aligned positions: the window's row and column tests are the part that
	// can be off by a tile.
	for _, cam := range []*camera.Camera{
		{X: 0, Z: 0, ViewW: surfW, ViewH: surfH},
		{X: 17, Z: 3, ViewW: surfW, ViewH: surfH},
		{X: 200, Z: 150, ViewW: surfW, ViewH: surfH},
		{X: -64, Z: -48, ViewW: surfW, ViewH: surfH},
		{X: -1, Z: -1, ViewW: surfW, ViewH: surfH},
		{X: 511, Z: 511, ViewW: surfW, ViewH: surfH},
		{X: 5000, Z: 5000, ViewW: surfW, ViewH: surfH},
	} {
		for _, dither := range []bool{false, true} {
			full := BuildFogOpsInto(nil, cache, cam, cam.ViewW, cam.ViewH, gw, gh, nil, dither)
			var want []FogOp
			for _, op := range full {
				x0 := op.ScreenX0 - camera.OriginX
				y0 := op.ScreenY0 - camera.OriginY
				x1 := op.ScreenX1 - camera.OriginX
				y1 := op.ScreenY1 - camera.OriginY
				if x0 < 0 {
					x0 = 0
				}
				if y0 < 0 {
					y0 = 0
				}
				if x1 > surfW {
					x1 = surfW
				}
				if y1 > surfH {
					y1 = surfH
				}
				if x0 >= x1 || y0 >= y1 {
					continue
				}
				want = append(want, op)
			}
			got := BuildFogOpsWindowInto(nil, cache, cam, surfW, surfH, nil, dither)
			if len(got) != len(want) {
				t.Fatalf("camera (%d,%d) dither=%v: windowed build has %d ops, the clipped map-wide build has %d",
					cam.X, cam.Z, dither, len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("camera (%d,%d) dither=%v op %d: windowed %+v, clipped map-wide %+v",
						cam.X, cam.Z, dither, i, got[i], want[i])
				}
			}
		}
	}
}

// TestFogWindowRejectsEmptySurface checks the degenerate surface produces no
// operations rather than the whole map: a zero-sized surface clips everything
// away, and the windowed builder must agree.
func TestFogWindowRejectsEmptySurface(t *testing.T) {
	cache := testFogCache(t, 4, 4)
	cache.SetChannel(0, 0, 15, 0)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 320, ViewH: 200}
	for _, size := range [][2]int32{{0, 200}, {320, 0}, {-1, -1}} {
		if ops := BuildFogOpsWindowInto(nil, cache, cam, size[0], size[1], nil, false); len(ops) != 0 {
			t.Fatalf("surface %dx%d produced %d ops, want none", size[0], size[1], len(ops))
		}
	}
}

// TestFogWindowReusesScratch checks the scratch slice is reused rather than
// reallocated: the composer hands the same slice back every frame and relies on
// it keeping its capacity.
func TestFogWindowReusesScratch(t *testing.T) {
	cache := testFogCache(t, 8, 8)
	for row := int32(0); row < 8; row++ {
		for col := int32(0); col < 8; col++ {
			cache.SetChannel(col, row, 15, 0)
		}
	}
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 320, ViewH: 200}
	scratch := BuildFogOpsWindowInto(nil, cache, cam, 320, 200, nil, false)
	if len(scratch) == 0 {
		t.Fatal("scene produces no fog ops; the reuse check would prove nothing")
	}
	grown := scratch[:cap(scratch)]
	again := BuildFogOpsWindowInto(scratch, cache, cam, 320, 200, nil, false)
	if cap(again) != cap(grown) || &again[:1][0] != &grown[:1][0] {
		t.Fatal("windowed build reallocated the scratch slice instead of refilling it")
	}
}
