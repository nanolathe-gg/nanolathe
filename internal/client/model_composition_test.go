package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// compositionClient is a headless client with a framebuffer big enough to hold
// a small model and a palette whose ALP table is deliberately non-identity off
// the diagonal, so a blended pixel is distinguishable from either source.
func compositionClient(t *testing.T) *Client {
	t.Helper()
	pal := &palette.Tables{}
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			if a == b {
				// ALP's diagonal is exact identity in the retail table, and the
				// anti-alias resolve depends on it [R-REN-03A §7].
				pal.Alpha[a*256+b] = uint8(a)
				continue
			}
			// A marker blend that is never equal to either operand.
			pal.Alpha[a*256+b] = uint8((a + b) / 2 & 0x7f)
		}
	}
	return &Client{
		width: 64, height: 64, indexed: make([]uint8, 64*64), pal: pal,
		cam: &camera.Camera{X: 0, Z: 0, ViewW: 64, ViewH: 64, MapW: 4096, MapH: 4096},
	}
}

// TestCompositionImageMeasuresExtentWithMargin locks retail's image sizing:
// the extrema are seeded at the model origin and a two-pixel margin is added on
// every side [R-REN-03A §1].
func TestCompositionImageMeasuresExtentWithMargin(t *testing.T) {
	polys := []screenPoly{walkPoly([][2]int32{{3, 4}, {9, 4}, {3, 10}}, nil)}
	w, h, ox, oy := modelExtent(polys)
	// min is 0 on both axes because the extrema start at the model origin.
	if w != 9+2*int(modelTargetMargin) || h != 10+2*int(modelTargetMargin) {
		t.Fatalf("extent = %dx%d, want %dx%d", w, h, 9+2*int(modelTargetMargin), 10+2*int(modelTargetMargin))
	}
	if ox != modelTargetMargin || oy != modelTargetMargin {
		t.Fatalf("origin = (%d,%d), want (%d,%d)", ox, oy, modelTargetMargin, modelTargetMargin)
	}

	// A model reaching left of its own origin pushes the origin right by
	// exactly that overhang plus the margin.
	polys = []screenPoly{walkPoly([][2]int32{{-5, 0}, {2, 0}, {-5, 3}}, nil)}
	w, _, ox, _ = modelExtent(polys)
	if w != 7+2*int(modelTargetMargin) || ox != 5+modelTargetMargin {
		t.Fatalf("negative overhang: width=%d origin=%d, want %d and %d", w, ox, 7+2*int(modelTargetMargin), 5+modelTargetMargin)
	}
}

// TestCompositionImageBackgroundIsPaletteIndexOne locks the background the
// anti-alias downscale blends against [R-REN-03A §1][R-REN-03A §7].
func TestCompositionImageBackgroundIsPaletteIndexOne(t *testing.T) {
	img := newModelImage(3, 2, 0, 0, 0, 0, true, 1)
	if transparentModelIndex != 1 {
		t.Fatalf("background index = %d, want 1", transparentModelIndex)
	}
	for i, v := range img.color {
		if v != 1 {
			t.Fatalf("colour plane pixel %d prefilled with %d, want 1", i, v)
		}
	}
	for i, v := range img.height {
		if v != 0 {
			t.Fatalf("height plane pixel %d prefilled with %d, want 0", i, v)
		}
	}
}

// TestAbsentKeyPlaneIsPainterOrder locks that a unit whose definition authors
// no ZBuffer composes without a depth test, exactly as retail's span writers do
// with a null key plane [R-REN-03A §2].
func TestAbsentKeyPlaneIsPainterOrder(t *testing.T) {
	c := compositionClient(t)
	high := heightPlaneFace(200)
	low := heightPlaneFace(10)

	withKey := newModelImage(8, 8, 0, 0, 0, 0, true, 1)
	c.fillPolyTarget(withKey, &high, 40)
	c.fillPolyTarget(withKey, &low, 41)
	if got := withKey.color[1*8+1]; got != 40 {
		t.Fatalf("with a key plane the lower face won: %d, want 40", got)
	}

	noKey := newModelImage(8, 8, 0, 0, 0, 0, false, 1)
	c.fillPolyTarget(noKey, &high, 40)
	c.fillPolyTarget(noKey, &low, 41)
	if got := noKey.color[1*8+1]; got != 41 {
		t.Fatalf("without a key plane the later face must win: %d, want 41", got)
	}
}

// TestResolveSupersampleIsThreeALPLookups locks the exact filter shape: the two
// horizontal pairs first, then the two results, with the left/top operand
// first, and the key nearest-sampled from the block's top-left [R-REN-03A §6].
func TestResolveSupersampleIsThreeALPLookups(t *testing.T) {
	c := compositionClient(t)
	src := newModelImage(2, 2, 0, 0, 0, 0, true, 2)
	dst := newModelImage(1, 1, 0, 0, 0, 0, true, 1)
	src.color[0], src.color[1] = 10, 20 // top-left, top-right
	src.color[2], src.color[3] = 30, 40 // bottom-left, bottom-right
	src.height[0], src.height[1] = 77, 5
	src.height[2], src.height[3] = 6, 7

	src.resolveSupersample(dst, &c.pal.Alpha)

	top := c.pal.Alpha[10*256+20]
	bottom := c.pal.Alpha[30*256+40]
	want := c.pal.Alpha[int(top)*256+int(bottom)]
	if got := dst.color[0]; got != want {
		t.Fatalf("resolved colour = %d, want ALP[ALP[10,20], ALP[30,40]] = %d", got, want)
	}
	if got := dst.height[0]; got != 77 {
		t.Fatalf("resolved key = %d, want the top-left sample 77", got)
	}
}

// TestSupersampleKeepsAWhollyBackgroundBlockTransparent is the property that
// stops anti-aliased buildings acquiring an opaque box: ALP's diagonal is exact
// identity, so four background samples resolve back to the background
// [R-REN-03A §7].
func TestSupersampleKeepsAWhollyBackgroundBlockTransparent(t *testing.T) {
	c := compositionClient(t)
	src := newModelImage(2, 2, 0, 0, 0, 0, false, 2)
	dst := newModelImage(1, 1, 0, 0, 0, 0, false, 1)
	src.resolveSupersample(dst, &c.pal.Alpha)
	if got := dst.color[0]; got != transparentModelIndex {
		t.Fatalf("background block resolved to %d, want the background index %d", got, transparentModelIndex)
	}
	if dst.covered[0] {
		t.Fatal("background block was marked covered and would be blitted")
	}
}

// TestSupersampleBlendsTheSilhouetteAgainstTheBackground is retail's red/purple
// fringe: a block straddling the edge blends model colour with the background
// index rather than excluding it, producing a pixel that is neither the model
// colour nor transparent [R-REN-03A §7]. We reproduce the defect deliberately.
func TestSupersampleBlendsTheSilhouetteAgainstTheBackground(t *testing.T) {
	c := compositionClient(t)
	src := newModelImage(2, 2, 0, 0, 0, 0, false, 2)
	dst := newModelImage(1, 1, 0, 0, 0, 0, false, 1)
	// Three background samples, one model pixel: the classic edge block.
	src.color[0] = 200
	src.resolveSupersample(dst, &c.pal.Alpha)
	got := dst.color[0]
	if got == transparentModelIndex {
		t.Fatal("edge block stayed transparent; the background must participate in the blend")
	}
	if got == 200 {
		t.Fatal("edge block kept the model colour; the background must participate in the blend")
	}
	top := c.pal.Alpha[200*256+int(transparentModelIndex)]
	bottom := c.pal.Alpha[int(transparentModelIndex)*256+int(transparentModelIndex)]
	if want := c.pal.Alpha[int(top)*256+int(bottom)]; got != want {
		t.Fatalf("edge blend = %d, want %d", got, want)
	}
	if !dst.covered[0] {
		t.Fatal("edge blend must be blitted; that is what makes the fringe visible")
	}
}

// TestSupersampleGateIsStructureAndOption locks that only BMcode=0 subjects
// anti-alias, and only while the option is on [R-REN-03A §6].
func TestSupersampleGateIsStructureAndOption(t *testing.T) {
	c := compositionClient(t)
	for _, tc := range []struct {
		name      string
		option    bool
		structure bool
		want      bool
	}{
		{"structure with the option on", true, true, true},
		{"structure with the option off", false, true, false},
		{"mobile unit with the option on", true, false, false},
		{"mobile unit with the option off", false, false, false},
	} {
		c.antiAlias = tc.option
		if got := c.supersampleModel(tc.structure); got != tc.want {
			t.Fatalf("%s: supersample = %v, want %v", tc.name, got, tc.want)
		}
	}
	// No palette means no ALP table, so the resolve cannot run.
	c.antiAlias, c.pal = true, nil
	if c.supersampleModel(true) {
		t.Fatal("supersample must not engage without a palette")
	}
}

// TestPlacePolysDoublesAroundTheImageOrigin locks the supersampled placement,
// including the one-pixel shear term for an odd model-relative height
// [R-REN-03A §6].
func TestPlacePolysDoublesAroundTheImageOrigin(t *testing.T) {
	base := func() []screenPoly {
		p := walkPoly([][2]int32{{1, 4}, {2, 5}, {3, 6}}, nil)
		p.oddHeight[1] = true
		return []screenPoly{p}
	}

	plain := base()
	placeFaces(plain, 10, 20, 1)
	if got := plain[0].x; got[0] != 11 || got[1] != 12 || got[2] != 13 {
		t.Fatalf("1x x = %v", got)
	}
	if got := plain[0].y; got[0] != 24 || got[1] != 25 || got[2] != 26 {
		t.Fatalf("1x y = %v", got)
	}

	doubled := base()
	placeFaces(doubled, 10, 20, 2)
	if got := doubled[0].x; got[0] != 22 || got[1] != 24 || got[2] != 26 {
		t.Fatalf("2x x = %v, want twice (local+origin)", got)
	}
	// Corner 1 has an odd height, so its shear is one pixel above 2*(local+origin).
	if got := doubled[0].y; got[0] != 48 || got[1] != 49 || got[2] != 52 {
		t.Fatalf("2x y = %v, want {48,49,52}", got)
	}
}

// TestCommitSkipsTheBackgroundIndexAndPlacesAtTheAnchor locks the final blit
// [R-REN-03A §1].
func TestCommitSkipsTheBackgroundIndexAndPlacesAtTheAnchor(t *testing.T) {
	c := compositionClient(t)
	img := newModelImage(3, 3, 1, 1, 20, 30, false, 1)
	img.write(1*3+1, 99, true) // the model's own (0,0)
	img.write(0, 55, true)     // one pixel up-left of it
	img.commit(c.indexed, c.width, c.height)

	if got := c.indexed[30*c.width+20]; got != 99 {
		t.Fatalf("origin pixel landed as %d at the anchor, want 99", got)
	}
	if got := c.indexed[29*c.width+19]; got != 55 {
		t.Fatalf("up-left pixel landed as %d, want 55", got)
	}
	// Everything else stayed untouched: the background index is never blitted.
	painted := 0
	for _, v := range c.indexed {
		if v != 0 {
			painted++
		}
	}
	if painted != 2 {
		t.Fatalf("blit painted %d pixels, want exactly the 2 covered ones", painted)
	}
}

// TestModelLocalVertexFloorsAndShears locks the model-relative narrowing: one
// arithmetic shift per component, then the half-height shear [R-REN-03A §1].
func TestModelLocalVertexFloorsAndShears(t *testing.T) {
	origin := [3]numeric.Fixed{0, 0, 0}
	// -1.5 units floors to -2 on every axis.
	lx, ly, ry := modelLocalVertex([3]numeric.Fixed{-98304, -98304, -98304}, origin)
	if lx != -2 || ry != -2 {
		t.Fatalf("narrowing gave lx=%d ry=%d, want -2 and -2", lx, ry)
	}
	// Z carries the handedness flip: Zn = hi16(-vz) = floor(1.5) = 1, and the
	// shear is ly = Zn - (ry>>1) = 1 - (-1) = 2 [R-RAST-01 §2].
	if ly != 2 {
		t.Fatalf("shear gave ly=%d, want 2", ly)
	}
	// The negation precedes the narrowing, so a fractional +Z floors after the
	// sign change: -ceil(0.25) = -1, not -floor(0.25) = 0 [R-RAST-01 §2].
	_, ly, _ = modelLocalVertex([3]numeric.Fixed{0, 0, 1 << 14}, origin)
	if ly != -1 {
		t.Fatalf("negate-then-floor gave ly=%d, want -1", ly)
	}
	// The origin is subtracted before narrowing, not after.
	lx, _, _ = modelLocalVertex([3]numeric.Fixed{3 << 16, 0, 0}, [3]numeric.Fixed{1 << 15, 0, 0})
	if lx != 2 {
		t.Fatalf("relative narrowing gave %d, want floor(3 - 0.5) = 2", lx)
	}
}
