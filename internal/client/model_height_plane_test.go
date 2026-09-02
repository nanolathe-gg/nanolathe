package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

func heightPlaneClient() *Client {
	return &Client{width: 8, height: 8, indexed: make([]uint8, 64)}
}

// heightPlaneTri is the triangle the shadow rasterization's filler still
// consumes; heightPlaneFace is the same geometry as the n-corner face the body
// raster takes through the two-chain walk [R-RAST-01 §1].
func heightPlaneTri(key uint8) screenTri {
	return screenTri{
		x:   [3]int32{0, 6, 0},
		y:   [3]int32{0, 0, 6},
		key: [3]float64{float64(key), float64(key), float64(key)},
	}
}

func heightPlaneFace(key uint8) screenPoly {
	k := int32(key)
	return walkPoly([][2]int32{{0, 0}, {6, 0}, {0, 6}}, []int32{k, k, k})
}

func TestModelHeightPlaneHigherFaceWinsRegardlessOfOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		c := heightPlaneClient()
		low, high := heightPlaneFace(50), heightPlaneFace(70)
		target := newModelTarget(c.width, c.height)
		if reverse {
			c.fillPolyTarget(target, &high, 22, nil)
			c.fillPolyTarget(target, &low, 11, nil)
		} else {
			c.fillPolyTarget(target, &low, 11, nil)
			c.fillPolyTarget(target, &high, 22, nil)
		}
		target.commit(c.indexed, c.width, c.height)
		if got := c.indexed[1*c.width+1]; got != 22 {
			t.Fatalf("reverse=%v: pixel=%d, want higher face 22", reverse, got)
		}
	}
}

func TestModelHeightPlaneCrossingFacesUsePerPixelKey(t *testing.T) {
	c := heightPlaneClient()
	a := walkPoly([][2]int32{{0, 0}, {6, 0}, {0, 6}}, []int32{50, 100, 50})
	b := walkPoly([][2]int32{{0, 0}, {6, 0}, {0, 6}}, []int32{80, 60, 80})
	target := newModelTarget(c.width, c.height)
	c.fillPolyTarget(target, &a, 11, nil)
	c.fillPolyTarget(target, &b, 22, nil)
	target.commit(c.indexed, c.width, c.height)
	if got := c.indexed[1*c.width+1]; got != 22 {
		t.Fatalf("crossing faces pixel=%d, want per-pixel higher face 22", got)
	}
}

func TestModelHeightPlaneEqualKeyLaterFaceWins(t *testing.T) {
	c := heightPlaneClient()
	a, b := heightPlaneFace(64), heightPlaneFace(64)
	target := newModelTarget(c.width, c.height)
	c.fillPolyTarget(target, &a, 11, nil)
	c.fillPolyTarget(target, &b, 22, nil)
	target.commit(c.indexed, c.width, c.height)
	if got := c.indexed[1*c.width+1]; got != 22 {
		t.Fatalf("equal-key pixel=%d, want later face 22", got)
	}
}

func TestModelHeightPlaneSharedByFlatAndTexturedFaces(t *testing.T) {
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{33}, Transparent: []bool{false}}
	for _, texturedFirst := range []bool{false, true} {
		c := heightPlaneClient()
		low, high := heightPlaneFace(50), heightPlaneFace(70)
		target := newModelTarget(c.width, c.height)
		if texturedFirst {
			low.frame = texture
			c.blitTexturedPolyTarget(target, &low, texture, nil)
			c.fillPolyTarget(target, &high, 44, nil)
		} else {
			c.fillPolyTarget(target, &high, 44, nil)
			low.frame = texture
			c.blitTexturedPolyTarget(target, &low, texture, nil)
		}
		target.commit(c.indexed, c.width, c.height)
		if got := c.indexed[1*c.width+1]; got != 44 {
			t.Fatalf("texturedFirst=%v: pixel=%d, want admitted flat face 44", texturedFirst, got)
		}
	}
}

func TestModelHeightPlaneTransparentTextureDoesNotAdmit(t *testing.T) {
	c := heightPlaneClient()
	face := heightPlaneFace(70)
	frame := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{9}, Transparent: []bool{true}}
	target := newModelTarget(c.width, c.height)
	c.blitTexturedPolyTarget(target, &face, frame, nil)
	if got := target.height[1*c.width+1]; got != 0 {
		t.Fatalf("transparent texel changed height key to %d", got)
	}
	if got := target.color[1*c.width+1]; got != transparentModelIndex {
		t.Fatalf("transparent texel changed composition colour to %d, want the background index %d", got, transparentModelIndex)
	}

	frame.Transparent[0] = false
	c.blitTexturedPolyTarget(target, &face, frame, nil)
	if got := target.height[1*c.width+1]; got != 70 {
		t.Fatalf("opaque texel key=%d, want 70", got)
	}
	target.commit(c.indexed, c.width, c.height)
	if got := c.indexed[1*c.width+1]; got != 9 {
		t.Fatalf("opaque texel committed color=%d, want 9", got)
	}
}

func TestModelHeightKeyFloorsNegativeWholeUnits(t *testing.T) {
	// Retail narrows the model-relative 16.16 height by extracting its high
	// word, which is an arithmetic shift, so -1.5 world units floors to -2 and
	// biases to 48. It is not an __ftol conversion and I3's truncate-toward-
	// zero rule does not reach it. The key is also the whole height, not half
	// of it [R-REN-03A §2].
	if got := modelHeightKey(-98304, false); got != 48 {
		t.Fatalf("key for -1.5 world units=%d, want 48", got)
	}
	if got := modelHeightKey(11<<16, false); got != 61 {
		t.Fatalf("key for +11 world units=%d, want 61 (unhalved)", got)
	}
}

// TestDiggerKeyRaisesBySeventyFiveAndErasesAtOrBelowOneTwentyFive locks both
// halves of [R-REN-03A §8]'s digger clip together, because either half alone is
// invisible: the +75 only shifts every key uniformly, and the erase only
// matters once the base has moved.
//
// The boundary is the whole contract. 125 is 50 + 75, so a vertex exactly at
// the model origin keys 125 and is erased, and the first whole unit above it
// keys 126 and survives. The fixture uses keys 50 and 51 for the same boundary
// on an undigger'd image, which is what an ordinary unit's origin looks like.
func TestDiggerKeyRaisesBySeventyFiveAndErasesAtOrBelowOneTwentyFive(t *testing.T) {
	// A definition that authors Digger raises every key by exactly 75.
	if got, want := modelHeightKey(0, true), int32(125); got != want {
		t.Fatalf("digger key at the model origin = %d, want %d", got, want)
	}
	if got, want := modelHeightKey(1<<16, true), int32(126); got != want {
		t.Fatalf("digger key one unit above the origin = %d, want %d", got, want)
	}
	if got, want := modelHeightKey(-1<<16, true), int32(124); got != want {
		t.Fatalf("digger key one unit below the origin = %d, want %d", got, want)
	}
	if diggerEraseThreshold != 125 {
		t.Fatalf("erase threshold = %d, want 50 + 75", diggerEraseThreshold)
	}

	// The erase itself, on a fixture image: at the threshold the pixel goes, one
	// above it stays. Run at the ordinary base so the arithmetic is legible.
	img := newModelImage(2, 1, 0, 0, 0, 0, true, 1)
	img.height[0], img.color[0], img.covered[0] = 50, 40, true
	img.height[1], img.color[1], img.covered[1] = 51, 41, true
	img.eraseAtOrBelow(50)
	if img.covered[0] || img.color[0] != transparentModelIndex {
		t.Fatalf("key 50 survived the erase as colour %d covered=%v", img.color[0], img.covered[0])
	}
	if !img.covered[1] || img.color[1] != 41 {
		t.Fatalf("key 51 was erased: colour %d covered=%v", img.color[1], img.covered[1])
	}
}

func TestConstructionUsesTheSameHeightPlaneAdmission(t *testing.T) {
	c := heightPlaneClient()
	low, high := heightPlaneFace(50), heightPlaneFace(70)
	reveal := presentationRevealKeep()
	target := newModelTarget(c.width, c.height)
	c.fillPolyTarget(target, &low, 11, &reveal)
	c.fillPolyTarget(target, &high, 22, &reveal)
	target.commit(c.indexed, c.width, c.height)
	if got := c.indexed[1*c.width+1]; got != 22 {
		t.Fatalf("construction pixel=%d, want higher face 22", got)
	}
}

func TestScanlineHeightKeyUsesFixedPointStageTruncation(t *testing.T) {
	// This interior sample is a rounding boundary: direct float barycentrics
	// yield 232, while the two 16.16 scanline stages yield 231.
	tri := screenTri{
		x:   [3]int32{0, 35, 35},
		y:   [3]int32{0, 16, 12},
		key: [3]float64{290, -12, 186},
	}
	if got := scanlineHeightKey(&tri, 10, 4); got != 231 {
		t.Fatalf("fixed scanline key=%d, want 231", got)
	}
}

func TestNanoframeEraseClearsPreviouslyComposedLowerFace(t *testing.T) {
	c := heightPlaneClient()
	c.indexed[1*c.width+1] = 77 // pre-existing terrain/background pixel
	low, high := heightPlaneFace(50), heightPlaneFace(70)
	reveal := presentationrender.NanoframeReveal{
		Line: 60, Floor: 56,
		Below: presentationrender.NanoframeKeep,
		Band:  presentationrender.NanoframeKeep,
		Above: presentationrender.NanoframeErase,
	}
	target := newModelTarget(c.width, c.height)
	c.fillPolyTarget(target, &low, 11, &reveal)
	c.fillPolyTarget(target, &high, 22, &reveal)
	target.commit(c.indexed, c.width, c.height)
	if got := c.indexed[1*c.width+1]; got != 77 {
		t.Fatalf("erased winning face changed destination to %d, want existing background 77", got)
	}
}

// Each subject composes into a freshly allocated image, so no state from an
// earlier model can reach a later one [R-REN-03A §1].
func TestEachModelComposesIntoItsOwnImage(t *testing.T) {
	c := heightPlaneClient()
	first := heightPlaneFace(70)
	target := newModelTarget(c.width, c.height)
	c.fillPolyTarget(target, &first, 11, nil)
	target.commit(c.indexed, c.width, c.height)
	if c.indexed[1*c.width+1] != 11 {
		t.Fatal("first model did not populate its composition image")
	}

	// The second model has the same bounds but covers the opposite half. A
	// stale colour or coverage bit at (1,1) would leak into this composition.
	c.indexed[1*c.width+1] = 77
	second := walkPoly([][2]int32{{6, 0}, {6, 6}, {0, 6}}, []int32{70, 70, 70})
	target = newModelTarget(c.width, c.height)
	c.fillPolyTarget(target, &second, 22, nil)
	target.commit(c.indexed, c.width, c.height)
	if got := c.indexed[1*c.width+1]; got != 77 {
		t.Fatalf("stale model pixel leaked as %d, want destination 77", got)
	}
}

func presentationRevealKeep() presentationrender.NanoframeReveal {
	return presentationrender.NanoframeReveal{
		Line: 255, Floor: 251,
		Below: presentationrender.NanoframeKeep,
		Band:  presentationrender.NanoframeKeep,
		Above: presentationrender.NanoframeKeep,
	}
}
