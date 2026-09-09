package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func cachedLiveRegressionSubject(t *testing.T) (*Client, frame.UnitView) {
	t.Helper()
	c := newPieceFixtureClient(t)
	c.models["split"] = syntheticModel([]pieceInfo{{name: "base", parent: -1}, {name: "live", parent: 0}}, []syntheticTri{
		makeTriangle(0, "base", [3][3]float64{{0, 0, 0}, {16, 0, 0}, {0, 0, 16}}, 31, 0),
		makeTriangle(1, "live", [3][3]float64{{0, 0, 0}, {16, 0, 0}, {0, 0, 16}}, 99, 0),
	}, 0)
	v := frame.UnitView{InstanceID: 71, Slot: 1, Model: "split", X: numeric.Fixed(100 << 16), Z: numeric.Fixed(100 << 16), ZBuffer: true, CacheRevision: 1, CacheValidityRevision: 1,
		Pieces: []frame.PieceView{{Index: 0}, {Index: 1, DontCache: true}}}
	return c, v
}

func TestCachedLiveMotionExpandsRetainedBounds(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	cachedLiveReplay(t, c, v)
	before := bytes.Count(c.indexed, []byte{99})
	v.Pieces[1].Tx = numeric.Fixed(48 << 16)
	cachedLiveReplay(t, c, v)
	if after := bytes.Count(c.indexed, []byte{99}); before == 0 || after != before {
		t.Fatalf("translated live pixels=%d, want preserved %d", after, before)
	}
}

func TestCachedLiveChildPassWinsEqualCachedKeys(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	m, ok := c.composeChildModel(v)
	if !ok || m.image == nil || bytes.Count(m.image.color, []byte{99}) == 0 {
		t.Fatal("child live face lost the later equal-key pass")
	}
	if bytes.Count(c.cachedModelBodies[v.InstanceID].image.color, []byte{99}) != 0 {
		t.Fatal("live face leaked into retained child body")
	}
}

func TestCachedLiveStructureLaneIsNeverSupersampled(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.antiAlias = true
	c.pal = &palette.Tables{}
	draw, ok := c.unitDrawFor(v)
	if !ok {
		t.Fatal("draw missing")
	}
	live, ok := c.composeModelLane(draw, v.Owner, unitTeamColor(v), v.InstanceID, modelCursorUnit, nil, 0, presentationrender.PieceLaneLive, false)
	if !ok || live.raster.scale != 1 {
		t.Fatal("live structure lane was supersampled")
	}
	cached, ok := c.composeModelLane(draw, v.Owner, unitTeamColor(v), v.InstanceID, modelCursorUnit, nil, 0, presentationrender.PieceLaneCached, false)
	if !ok || cached.raster.scale != 2 {
		t.Fatal("cached structure lane lost its authored anti-alias pass")
	}
}

func TestCachedLiveMissingImageCarrierUsesDirectRoute(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	v.BMCode = true
	v.ZBuffer = false
	cachedLiveReplay(t, c, v)
	v.CacheRevision++ // image discard without validity clear [03 R-COMP-01 §4]
	child := v
	child.InstanceID++
	child.X += numeric.Fixed(32 << 16)
	clearIndexed(c)
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	if !c.composeCarrier(v, 0, 0, []frame.UnitView{child}) {
		t.Fatal("image-less carrier did not use direct present")
	}
	c.replayForTest()
	if bytes.Count(c.indexed, []byte{99}) == 0 {
		t.Fatal("image-less carrier and child lost live geometry")
	}
}

func TestCachedLiveRevealDoesNotMutateRetainedBody(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	v.BuildRemaining = 1
	cachedLiveReplay(t, c, v)
	if bytes.Count(c.indexed, []byte{31}) != 0 || bytes.Count(c.indexed, []byte{99}) != 0 {
		t.Fatal("new nanoframe retained unrevealed face colors")
	}
	body := c.cachedModelBodies[v.InstanceID].image
	if bytes.Count(body.color, []byte{31}) == 0 {
		t.Fatal("reveal destroyed the retained raw composition")
	}
}

func TestCachedLiveCarrierDiggerEraseIncludesAttachedChild(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	v.Digger = true
	v.Y = numeric.Fixed(20 << 16)
	child := v
	child.InstanceID++
	child.Digger = false
	child.X += numeric.Fixed(40 << 16)
	// The child is outside the carrier body but below its final key-125 erase.
	// It survives its own ordinary waterline, then disappears in group finalization.
	clearIndexed(c)
	c.resetListForTest()
	c.modelScratch.reset()
	c.modelScratch.active = true
	defer func() { c.modelScratch.active = false }()
	if !c.composeCarrier(v, 0, 0, []frame.UnitView{child}) {
		t.Fatal("carrier missing")
	}
	c.replayForTest()
	if bytes.Count(c.indexed, []byte{99}) != 0 {
		t.Fatal("attached child escaped the carrier's final Digger erase")
	}
}

func TestCachedLiveTransparentIndexStillErasesCachedColor(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.models["split"].compiled.Pieces[1].Primitives[0].ColorIndex = uint32(transparentModelIndex)
	cachedLiveReplay(t, c, v)
	if bytes.Count(c.indexed, []byte{31}) != 0 {
		t.Fatal("live key-colored face failed to erase cached pixels")
	}
}

// The live renderer is an unshaded second invocation even for a shaded
// BMcode=0 body. Give every SHD row a deliberately different result so the
// production lane cannot accidentally pass by choosing row zero.
func TestCachedLiveStructurePieceBypassesSHD(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	previousShading := presentationrender.Shading
	presentationrender.Shading = true
	t.Cleanup(func() { presentationrender.Shading = previousShading })
	c.pal = &palette.Tables{}
	c.antiAlias = false
	for row := range c.pal.Shade {
		c.pal.Shade[row][99] = 7
		c.pal.Shade[row][31] = 7
	}
	draw, ok := c.unitDrawFor(v)
	if !ok {
		t.Fatal("draw missing")
	}
	live, ok := c.composeModelLane(draw, v.Owner, unitTeamColor(v), v.InstanceID, modelCursorUnit, nil, 0, presentationrender.PieceLaneLive, false)
	if !ok || bytes.Count(live.image.color, []byte{99}) == 0 || bytes.Count(live.image.color, []byte{7}) != 0 {
		t.Fatal("structure live lane did not retain its raw authored colour")
	}
	cached, ok := c.composeModelLane(draw, v.Owner, unitTeamColor(v), v.InstanceID, modelCursorUnit, nil, 0, presentationrender.PieceLaneCached, false)
	if !ok || bytes.Count(cached.image.color, []byte{7}) == 0 {
		t.Fatalf("structure cached lane did not retain its shaded renderer: colors 31=%d 7=%d rows=%#v", bytes.Count(cached.image.color, []byte{31}), bytes.Count(cached.image.color, []byte{7}), draw.Pieces[0].Primitives[0].ShadeRows)
	}
}

func TestModelCommitAndChildCompositeKeyOnTransparentIndex(t *testing.T) {
	// A live raster write may mark coverage while placing the image background
	// over a cached colour. The final blit still keys on color 1, leaving the
	// earlier framebuffer pixel alone [03 R-REN-03A §1].
	final := newModelImage(1, 1, 0, 0, 0, 0, true, 1)
	final.color[0], final.covered[0] = transparentModelIndex, true
	framebuffer := []byte{73}
	final.commit(framebuffer, 1, 1)
	if framebuffer[0] != 73 {
		t.Fatalf("final transparent write painted %d, want prior framebuffer pixel 73", framebuffer[0])
	}

	// Attached-child composition has the same boundary: its image background
	// cannot erase the carrier before the final keyed blit [03 R-REN-03A §4].
	carrier := newModelImage(1, 1, 0, 0, 0, 0, true, 1)
	carrier.color[0], carrier.covered[0] = 55, true
	child := newModelImage(1, 1, 0, 0, 0, 0, true, 1)
	child.color[0], child.covered[0] = transparentModelIndex, true
	carrier.compositeChild(child, 0)
	framebuffer[0] = 73
	carrier.commit(framebuffer, 1, 1)
	if framebuffer[0] != 55 {
		t.Fatalf("transparent child replaced carrier colour with %d, want 55", framebuffer[0])
	}
}

func TestCachedBodyMemoizationTracksPresentationInputs(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	previousShading := presentationrender.Shading
	t.Cleanup(func() { presentationrender.Shading = previousShading })
	p1, p2 := &palette.Tables{}, &palette.Tables{}
	c.SetPalette(p1)
	c.SetAntiAlias(false)
	c.SetShadowOptions(false, false, true)
	cachedLiveReplay(t, c, v)
	first := c.cachedModelBodies[v.InstanceID]
	if first == nil || first.supersampled || first.scale != 1 || first.palette != p1 || !first.shaded {
		t.Fatalf("initial body memo key=%+v", first)
	}

	c.SetShadowOptions(false, false, false)
	cachedLiveReplay(t, c, v)
	shadingOff := c.cachedModelBodies[v.InstanceID]
	if shadingOff == first || shadingOff.shaded {
		t.Fatal("shading change reused the shaded cached body")
	}

	c.SetAntiAlias(true)
	cachedLiveReplay(t, c, v)
	aa := c.cachedModelBodies[v.InstanceID]
	if aa == shadingOff || !aa.supersampled {
		t.Fatal("effective anti-alias change reused the native cached body")
	}

	c.cam.Scale = 2
	cachedLiveReplay(t, c, v)
	zoom := c.cachedModelBodies[v.InstanceID]
	if zoom == aa || zoom.scale != 2 {
		t.Fatal("effective camera scale change reused the prior cached body")
	}

	c.SetPalette(p2)
	cachedLiveReplay(t, c, v)
	paletteChanged := c.cachedModelBodies[v.InstanceID]
	if paletteChanged == zoom || paletteChanged.palette != p2 {
		t.Fatal("palette installation reused the prior cached body")
	}
}

// Rebuilding the image for a separate input must not consume sub-threshold
// orientation deltas: the reference describes the rasterized pose [03 §5.2].
func TestCachedBodyMemoRebuildPreservesOrientationReference(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	cachedLiveReplay(t, c, v)
	v.Heading = 7
	c.cam.Scale = 2
	cachedLiveReplay(t, c, v)
	if got := c.orientationCache(v.InstanceID).Heading; got != 0 {
		t.Fatalf("scale rebuild advanced retained heading to %d", got)
	}
	v.Heading = 14
	cachedLiveReplay(t, c, v)
	if got := c.orientationCache(v.InstanceID).Heading; got != 14 {
		t.Fatalf("accumulated turn did not refresh heading: %d", got)
	}
	draw, ok := c.unitDrawFor(v)
	if !ok || draw.PieceStates[0].RotY != 14 {
		t.Fatalf("live transform disagrees with refreshed cached pose")
	}
}

// A key-colored live face erases the body but must leave its ground shadow.
// Coverage inside staging is not opacity at the shadow punch [R-REN-03D §5].
func TestCachedLiveTransparentBodyPreservesGroundShadow(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	oldShading := presentationrender.Shading
	t.Cleanup(func() { presentationrender.Shading = oldShading })
	c.pal = &palette.Tables{}
	for i := range c.pal.Alpha {
		c.pal.Alpha[i] = 42
	}
	c.SetShadowOptions(true, true, false)
	cachedLiveReplay(t, c, v)
	before := append([]byte(nil), c.indexed...)
	c.models["split"].compiled.Pieces[1].Primitives[0].ColorIndex = uint32(transparentModelIndex)
	cachedLiveReplay(t, c, v)
	exposed := 0
	for i, color := range before {
		if color == 99 && c.indexed[i] == 42 {
			exposed++
		}
	}
	if exposed == 0 {
		t.Fatal("transparent live face incorrectly removed the shadow below the body")
	}
}
