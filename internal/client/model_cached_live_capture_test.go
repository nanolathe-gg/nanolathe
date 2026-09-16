package client

import (
	"bytes"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestCachedLiveClassicCaptures writes opt-in, replayed classic evidence. It
// uses the same recorded List and classic sink as normal presentation; no test
// writes model pixels directly. Set NANOLATHE_CAPTURE_DIR to retain PNGs.
func TestCachedLiveClassicCaptures(t *testing.T) {
	dir := os.Getenv("NANOLATHE_CAPTURE_DIR")
	if dir == "" {
		t.Skip("set NANOLATHE_CAPTURE_DIR to write cached/live classic captures")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	previousShading := presentationrender.Shading
	t.Cleanup(func() { presentationrender.Shading = previousShading })

	// A cached base and a translated live child overlap at fractional X. The
	// keyed staging pass, not painter order, selects their shared pixels.
	c := newPieceFixtureClient(t)
	c.models["capture-overlap"] = syntheticModel(
		[]pieceInfo{{name: "base", parent: -1}, {name: "door", parent: 0}},
		[]syntheticTri{
			makeTriangle(0, "base", [3][3]float64{{0, 0, 0}, {14, 0, 0}, {0, 0, 14}}, 44, 0),
			makeTriangle(1, "door", [3][3]float64{{0, 1, 0}, {14, 1, 0}, {0, 1, 14}}, 99, 0),
		}, 0)
	overlap := frame.UnitView{InstanceID: 41, Slot: 1, Model: "capture-overlap", X: numeric.Fixed(100 << 16), Z: numeric.Fixed(100 << 16), ZBuffer: true, CacheRevision: 1, CacheValidityRevision: 1,
		Pieces: []frame.PieceView{{Index: 0}, {Index: 1, DontCache: true, Tx: numeric.Fixed(1 << 15)}}}
	cachedLiveReplay(t, c, overlap)
	writeCachedLivePNG(t, c, filepath.Join(dir, "cached-live-fractional-keyed.png"))
	writeCachedLiveContentZoom(t, c, filepath.Join(dir, "cached-live-fractional-keyed-zoom.png"))

	// The cached orientation reference advances only when the body rebuilds:
	// seven is clean, eight rebuilds.
	c2 := newPieceFixtureClient(t)
	c2.models["capture-turn"] = syntheticModel([]pieceInfo{{name: "base", parent: -1}}, []syntheticTri{
		makeTriangle(0, "base", [3][3]float64{{4, 0, 0}, {20, 0, 0}, {4, 0, 10}}, 140, 0),
	}, 0)
	turn := frame.UnitView{InstanceID: 42, Slot: 2, Model: "capture-turn", X: numeric.Fixed(100 << 16), Z: numeric.Fixed(100 << 16), ZBuffer: true, CacheRevision: 1, CacheValidityRevision: 1, Pieces: []frame.PieceView{{Index: 0, DontCache: true}}}
	cachedLiveReplay(t, c2, turn)
	zero := append([]uint8(nil), c2.indexed...)
	turn.Heading = 7
	cachedLiveReplay(t, c2, turn)
	seven := append([]uint8(nil), c2.indexed...)
	if string(zero) != string(seven) {
		t.Fatal("live piece rotated below the strict orientation threshold")
	}
	writeCachedLivePNG(t, c2, filepath.Join(dir, "cached-live-orientation-7.png"))
	writeCachedLiveContentZoom(t, c2, filepath.Join(dir, "cached-live-orientation-7-zoom.png"))
	turn.Heading = 8
	cachedLiveReplay(t, c2, turn)
	eight := append([]uint8(nil), c2.indexed...)
	writeCachedLivePNG(t, c2, filepath.Join(dir, "cached-live-orientation-8.png"))
	writeCachedLiveContentZoom(t, c2, filepath.Join(dir, "cached-live-orientation-8-zoom.png"))
	if string(seven) == string(eight) {
		t.Fatal("strict orientation threshold produced identical seven and eight captures")
	}

	// The direct live fill preserves the exclusive framebuffer border.
	c3 := newTestClient(t)
	c3.models["capture-edge"] = syntheticModel([]pieceInfo{{name: "live", parent: -1}}, []syntheticTri{
		// The face crosses both bounds; clipping must still leave the last
		// row and column unused [03 R-RAST-01 §1].
		makeTriangle(0, "live", [3][3]float64{{0, 0, 0}, {8, 0, 0}, {0, 0, -8}}, 201, 0),
	}, 0)
	edge := frame.UnitView{InstanceID: 43, Slot: 3, Model: "capture-edge", X: numeric.Fixed(636 << 16), Z: numeric.Fixed(476 << 16), CacheRevision: 1, CacheValidityRevision: 1,
		Pieces: []frame.PieceView{{Index: 0, DontCache: true}}}
	cachedLiveReplay(t, c3, edge)
	writeCachedLivePNG(t, c3, filepath.Join(dir, "cached-live-no-key-edge.png"))
	writeCachedLiveEdgeZoom(t, c3, filepath.Join(dir, "cached-live-no-key-edge-zoom.png"))
	if c3.indexed[477*640+637] != 201 {
		t.Fatal("clipped live fixture failed to draw its in-bounds interior")
	}
	if got := c3.indexed[479*640+639]; got != 0 {
		t.Fatalf("direct live edge pixel = %d, want untouched zero", got)
	}

	// The live lane can write color 1 into staging to erase its cached base,
	// but the completed composition image still keys that byte at its final
	// blit. Seed the previous framebuffer so the capture exposes that boundary.
	c4, transparent := cachedLiveRegressionSubject(t)
	c4.models["split"].compiled.Pieces[1].Primitives[0].ColorIndex = uint32(transparentModelIndex)
	for i := range c4.indexed {
		c4.indexed[i] = 73
	}
	c4.resetListForTest()
	c4.modelScratch.reset()
	c4.modelScratch.active = true
	sx, sy := c4.cam.WorldToScreen(transparent.X, transparent.Y, transparent.Z)
	if !c4.drawUnitModel(transparent, sx, sy) {
		t.Fatal("transparent live fixture was not recorded")
	}
	c4.replayForTest()
	c4.modelScratch.active = false
	writeCachedLivePNG(t, c4, filepath.Join(dir, "cached-live-transparent-prior.png"))
	if bytes.Count(c4.indexed, []byte{31}) != 0 || bytes.Count(c4.indexed, []byte{73}) != len(c4.indexed) {
		t.Fatal("transparent live capture did not retain the previous framebuffer")
	}

	// A shaded structure body and its unshaded live piece are composed through
	// different renderer entries. The non-identity table makes that split
	// visible in the replayed PNG.
	c5, shaded := cachedLiveRegressionSubject(t)
	shaded.Pieces[1].Tx = numeric.Fixed(4 << 16)
	c5.SetPalette(&palette.Tables{})
	c5.SetAntiAlias(false)
	c5.SetShadowOptions(false, false, true)
	for row := range c5.pal.Shade {
		c5.pal.Shade[row][99] = 7
		c5.pal.Shade[row][31] = 7
	}
	cachedLiveReplay(t, c5, shaded)
	writeCachedLivePNG(t, c5, filepath.Join(dir, "cached-live-shaded-body-live-raw.png"))
	writeCachedLiveContentZoom(t, c5, filepath.Join(dir, "cached-live-shaded-body-live-raw-zoom.png"))
	if bytes.Count(c5.indexed, []byte{99}) == 0 || bytes.Count(c5.indexed, []byte{7}) == 0 {
		t.Fatal("shaded/live capture did not expose both renderer entries")
	}

	// The following three replayed frames keep the published validity words
	// unchanged. Their different retained-image memo inputs must nevertheless
	// rebuild a classic raster before it is recorded again.
	c6, settings := cachedLiveRegressionSubject(t)
	p1, p2 := &palette.Tables{}, &palette.Tables{}
	for i := 0; i < 256; i++ {
		p1.Alpha[i*256+i], p2.Alpha[i*256+i] = byte(i), byte(i)
	}
	c6.SetPalette(p1)
	c6.SetAntiAlias(false)
	c6.SetShadowOptions(false, false, true)
	cachedLiveReplay(t, c6, settings)
	base := c6.cachedModelBodies[settings.InstanceID]
	writeCachedLivePNG(t, c6, filepath.Join(dir, "cached-live-settings-base.png"))
	c6.SetAntiAlias(true)
	cachedLiveReplay(t, c6, settings)
	aa := c6.cachedModelBodies[settings.InstanceID]
	writeCachedLivePNG(t, c6, filepath.Join(dir, "cached-live-settings-aa.png"))
	c6.cam.Scale = camera.ViewScaleDetail
	cachedLiveReplay(t, c6, settings)
	zoom := c6.cachedModelBodies[settings.InstanceID]
	writeCachedLivePNG(t, c6, filepath.Join(dir, "cached-live-settings-zoom.png"))
	c6.SetPalette(p2)
	cachedLiveReplay(t, c6, settings)
	if base == aa || aa == zoom || c6.cachedModelBodies[settings.InstanceID] == zoom {
		t.Fatal("settings/zoom capture reused a retained raster")
	}
}

// TestCachedModelRetentionPrunesAtTheCompositionBoundary keeps retention
// bounded to the units in the frame being composed. It also clears the
// orientation reference, since it is meaningful only beside the same body.
func TestCachedModelRetentionPrunesAtTheCompositionBoundary(t *testing.T) {
	c := newTestClient(t)
	c.cachedModelBodies[41] = &cachedModelBody{}
	c.cachedModelBodies[42] = &cachedModelBody{}
	c.orientationCache(41)
	c.orientationCache(42)
	c.composeIndexed(&frame.Frame{Units: []frame.UnitView{{InstanceID: 41}}}, true)
	if _, ok := c.cachedModelBodies[41]; !ok {
		t.Fatal("live unit body was pruned")
	}
	if _, ok := c.modelOrientation[41]; !ok {
		t.Fatal("live unit orientation was pruned")
	}
	if _, ok := c.cachedModelBodies[42]; ok {
		t.Fatal("departed unit body was retained")
	}
	if _, ok := c.modelOrientation[42]; ok {
		t.Fatal("departed unit orientation was retained")
	}
	c.composeIndexed(nil, false)
	if len(c.cachedModelBodies) != 0 || len(c.modelOrientation) != 0 {
		t.Fatal("empty publication retained model state")
	}
}

// TestKeylessCarrierCommitsItsLiveLaneBeforeAttachedChildren exercises the
// actual classic recording API. A live carrier piece must survive the
// keyless painter route even when an attached child follows it.
func TestKeylessCarrierCommitsItsLiveLaneBeforeAttachedChildren(t *testing.T) {
	c := newPieceFixtureClient(t)
	c.models["carrier"] = syntheticModel(
		[]pieceInfo{{name: "body", parent: -1}, {name: "live", parent: 0}},
		[]syntheticTri{
			makeTriangle(0, "body", [3][3]float64{{0, 0, 0}, {12, 0, 0}, {0, 0, 12}}, 31, 0),
			makeTriangle(1, "live", [3][3]float64{{1, 1, 0}, {13, 1, 0}, {1, 1, 12}}, 99, 0),
		}, 0)
	c.models["cargo"] = syntheticModel([]pieceInfo{{name: "body", parent: -1}}, []syntheticTri{
		makeTriangle(0, "body", [3][3]float64{{0, 0, 0}, {8, 0, 0}, {0, 0, 8}}, 77, 0),
	}, 0)
	carrier := frame.UnitView{InstanceID: 51, Model: "carrier", X: numeric.Fixed(100 << 16), Z: numeric.Fixed(100 << 16), CacheRevision: 1, CacheValidityRevision: 1, Pieces: []frame.PieceView{{Index: 0}, {Index: 1, DontCache: true}}}
	child := frame.UnitView{InstanceID: 52, Model: "cargo", X: numeric.Fixed(124 << 16), Z: numeric.Fixed(100 << 16), CacheRevision: 1, CacheValidityRevision: 1}
	clearIndexed(c)
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	if !c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
		t.Fatal("carrier was not recorded")
	}
	c.replayForTest()
	for _, color := range c.indexed {
		if color == 99 {
			return
		}
	}
	t.Fatal("keyless carrier direct live piece was omitted before attached child")
}

func cachedLiveReplay(t *testing.T, c *Client, v frame.UnitView) {
	t.Helper()
	clearIndexed(c)
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	sx, sy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	if !c.drawUnitModel(v, sx, sy) {
		t.Fatal("cached/live draw was not recorded")
	}
	c.replayForTest()
}

func writeCachedLivePNG(t *testing.T, c *Client, path string) {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, c.width, c.height))
	copy(img.Pix, c.indexed)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// writeCachedLiveEdgeZoom makes the replayed final-pixel proof visible during
// human review without changing the underlying capture or its pixels.
func writeCachedLiveEdgeZoom(t *testing.T, c *Client, path string) {
	t.Helper()
	const source, scale = 16, 12
	img := image.NewGray(image.Rect(0, 0, source*scale, source*scale))
	for y := 0; y < source*scale; y++ {
		for x := 0; x < source*scale; x++ {
			sx := c.width - source + x/scale
			sy := c.height - source + y/scale
			img.Pix[y*img.Stride+x] = c.indexed[sy*c.width+sx]
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// writeCachedLiveContentZoom scales the replayed non-background bounds for
// visual inspection while keeping the full-size PNG as the pixel evidence.
func writeCachedLiveContentZoom(t *testing.T, c *Client, path string) {
	t.Helper()
	minX, minY, maxX, maxY := c.width, c.height, -1, -1
	for y := 0; y < c.height; y++ {
		for x := 0; x < c.width; x++ {
			if c.indexed[y*c.width+x] == 0 {
				continue
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < minX || maxY < minY {
		t.Fatal("capture had no model pixels to zoom")
	}
	const padding, scale = 8, 8
	minX, minY = max(0, minX-padding), max(0, minY-padding)
	maxX, maxY = min(c.width-1, maxX+padding), min(c.height-1, maxY+padding)
	w, h := maxX-minX+1, maxY-minY+1
	img := image.NewGray(image.Rect(0, 0, w*scale, h*scale))
	for y := 0; y < h*scale; y++ {
		for x := 0; x < w*scale; x++ {
			img.Pix[y*img.Stride+x] = c.indexed[(minY+y/scale)*c.width+minX+x/scale]
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

// The modern executor keeps a model slot until the retained cached lane behind
// it changes, and it recognises that lane by the pair the recorder stamps on
// every rebased packet (docs/DESIGN_GPU_RENDERER.md §13.12). A body that stores
// new geometry must therefore present a new pair, and a body dropped and
// recreated under the same presentation identity must never present a pair an
// earlier body already used.
func TestRetainedGeometryIdentityChangesOnEveryStore(t *testing.T) {
	c := newTestClient(t)
	draw := &presentationrender.UnitDraw{Model: &model.Model{Name: "fixture"}}
	v := frame.UnitView{InstanceID: 41}
	store := func() drawlist.ModelCacheKey {
		g := &drawlist.ModelGeometry{Eligible: true, Scale: 1, Width: 4, Height: 4,
			Faces: []drawlist.ModelFace{{Vertices: []drawlist.ModelVertex{{}, {X: 4}, {X: 4, Y: 4}}}}}
		c.replaceCachedGeometry(41, v, draw, g)
		return c.cachedModelBodies[41].cacheKey(0, 0)
	}
	first := store()
	if !first.Reusable() {
		t.Fatal("a stored cached lane presented no identity")
	}
	second := store()
	if second == first || second.Body != first.Body || second.Revision == first.Revision {
		t.Fatalf("storing new geometry gave identity %+v, want the same body at a new revision after %+v", second, first)
	}
	// A body with no stored geometry has no raster to identify.
	empty := (&cachedModelBody{}).cacheKey(0, 0)
	if empty.Reusable() {
		t.Fatalf("an empty body presented identity %+v", empty)
	}
	// InvalidateModelImages drops every body; the replacement must not inherit
	// the identity the executor still holds a raster for.
	c.InvalidateModelImages()
	c.cachedModelBodies[41] = &cachedModelBody{}
	if again := store(); again.Body == first.Body {
		t.Fatalf("a recreated body reused body serial %d", again.Body)
	}
}
