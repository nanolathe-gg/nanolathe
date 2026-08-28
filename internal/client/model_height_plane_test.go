package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

func heightPlaneClient() *Client {
	return &Client{width: 8, height: 8, indexed: make([]uint8, 64)}
}

func heightPlaneTri(key uint8) screenTri {
	return screenTri{
		x:   [3]int32{0, 6, 0},
		y:   [3]int32{0, 0, 6},
		key: [3]float64{float64(key), float64(key), float64(key)},
	}
}

func TestModelHeightPlaneHigherFaceWinsRegardlessOfOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		c := heightPlaneClient()
		low, high := heightPlaneTri(50), heightPlaneTri(70)
		target := newModelTarget(c.width, c.height)
		if reverse {
			c.fillTriTarget(target, &high, 22)
			c.fillTriTarget(target, &low, 11)
		} else {
			c.fillTriTarget(target, &low, 11)
			c.fillTriTarget(target, &high, 22)
		}
		target.commit(c.indexed)
		if got := c.indexed[1*c.width+1]; got != 22 {
			t.Fatalf("reverse=%v: pixel=%d, want higher face 22", reverse, got)
		}
	}
}

func TestModelHeightPlaneCrossingFacesUsePerPixelKey(t *testing.T) {
	c := heightPlaneClient()
	a := heightPlaneTri(0)
	a.key = [3]float64{50, 100, 50}
	b := heightPlaneTri(0)
	b.key = [3]float64{80, 60, 80}
	target := newModelTarget(c.width, c.height)
	c.fillTriTarget(target, &a, 11)
	c.fillTriTarget(target, &b, 22)
	target.commit(c.indexed)
	if got := c.indexed[1*c.width+1]; got != 22 {
		t.Fatalf("crossing faces pixel=%d, want per-pixel higher face 22", got)
	}
}

func TestModelHeightPlaneEqualKeyLaterFaceWins(t *testing.T) {
	c := heightPlaneClient()
	a, b := heightPlaneTri(64), heightPlaneTri(64)
	target := newModelTarget(c.width, c.height)
	c.fillTriTarget(target, &a, 11)
	c.fillTriTarget(target, &b, 22)
	target.commit(c.indexed)
	if got := c.indexed[1*c.width+1]; got != 22 {
		t.Fatalf("equal-key pixel=%d, want later face 22", got)
	}
}

func TestModelHeightPlaneSharedByFlatAndTexturedFaces(t *testing.T) {
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{33}, Transparent: []bool{false}}
	for _, texturedFirst := range []bool{false, true} {
		c := heightPlaneClient()
		low, high := heightPlaneTri(50), heightPlaneTri(70)
		target := newModelTarget(c.width, c.height)
		if texturedFirst {
			low.frame = texture
			c.blitTexturedTriTarget(target, &low, texture)
			c.fillTriTarget(target, &high, 44)
		} else {
			c.fillTriTarget(target, &high, 44)
			low.frame = texture
			c.blitTexturedTriTarget(target, &low, texture)
		}
		target.commit(c.indexed)
		if got := c.indexed[1*c.width+1]; got != 44 {
			t.Fatalf("texturedFirst=%v: pixel=%d, want admitted flat face 44", texturedFirst, got)
		}
	}
}

func TestModelHeightPlaneTransparentTextureDoesNotAdmit(t *testing.T) {
	c := heightPlaneClient()
	tri := heightPlaneTri(70)
	frame := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{9}, Transparent: []bool{true}}
	target := newModelTarget(c.width, c.height)
	c.blitTexturedTriTarget(target, &tri, frame)
	if got := target.height[1*c.width+1]; got != 0 {
		t.Fatalf("transparent texel changed height key to %d", got)
	}
	if got := target.color[1*c.width+1]; got != 0 {
		t.Fatalf("transparent texel changed scratch color to %d", got)
	}

	frame.Transparent[0] = false
	c.blitTexturedTriTarget(target, &tri, frame)
	if got := target.height[1*c.width+1]; got != 70 {
		t.Fatalf("opaque texel key=%d, want 70", got)
	}
	target.commit(c.indexed)
	if got := c.indexed[1*c.width+1]; got != 9 {
		t.Fatalf("opaque texel committed color=%d, want 9", got)
	}
}

func TestModelHeightKeyTruncatesNegativeWholeUnitsTowardZero(t *testing.T) {
	// -1.5 world units narrows to -1, then halves to zero; arithmetic
	// shifting would incorrectly produce -2 before the half-height step.
	if got := modelHeightKey(-98304); got != 50 {
		t.Fatalf("key for -1.5 world units=%d, want 50", got)
	}
}

func TestConstructionUsesTheSameHeightPlaneAdmission(t *testing.T) {
	c := heightPlaneClient()
	low, high := heightPlaneTri(50), heightPlaneTri(70)
	reveal := presentationRevealKeep()
	target := newModelTarget(c.width, c.height)
	c.fillTriNanoframeTarget(target, &low, 11, reveal)
	c.fillTriNanoframeTarget(target, &high, 22, reveal)
	target.commit(c.indexed)
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
	low, high := heightPlaneTri(50), heightPlaneTri(70)
	reveal := presentationrender.NanoframeReveal{
		Line: 60, Floor: 56,
		Below: presentationrender.NanoframeKeep,
		Band:  presentationrender.NanoframeKeep,
		Above: presentationrender.NanoframeErase,
	}
	target := newModelTarget(c.width, c.height)
	c.fillTriNanoframeTarget(target, &low, 11, reveal)
	c.fillTriNanoframeTarget(target, &high, 22, reveal)
	target.commit(c.indexed)
	if got := c.indexed[1*c.width+1]; got != 77 {
		t.Fatalf("erased winning face changed destination to %d, want existing background 77", got)
	}
}

func TestReusableModelTargetClearsPreviousBoundsAndCoverage(t *testing.T) {
	c := heightPlaneClient()
	first := heightPlaneTri(70)
	target := c.reusableModelTarget([]screenTri{first})
	c.fillTriTarget(target, &first, 11)
	target.commit(c.indexed)
	if c.indexed[1*c.width+1] != 11 {
		t.Fatal("first model did not populate scratch target")
	}

	// The second model has the same bounds but covers the opposite half. A
	// stale color or covered bit at (1,1) would leak into this composition.
	c.indexed[1*c.width+1] = 77
	second := screenTri{
		x: [3]int32{6, 6, 0}, y: [3]int32{0, 6, 6},
		key: [3]float64{70, 70, 70},
	}
	target = c.reusableModelTarget([]screenTri{second})
	c.fillTriTarget(target, &second, 22)
	target.commit(c.indexed)
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
