package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// publishSeaLevel republishes the committed frame with one sea level, so the
// waterline input can be moved while the model itself stays put. Raising the
// water rather than sinking the hull is what keeps the two captures framed
// identically: the model's own height also shears its screen placement, so
// moving the unit would change the composition as well as the split.
func publishSeaLevel(t *testing.T, c *Client, tick uint32, sea int32, viewer uint8) {
	t.Helper()
	w := c.buffer.BeginWrite()
	w.Selection = frame.SelectionView{LocalPlayer: viewer}
	w.Visibility.SeaLevel = numeric.Fixed(sea) << 16
	if err := c.buffer.Publish(tick); err != nil {
		t.Fatal(err)
	}
}

// composeSub composes one stock model at the world origin with the waterline
// pass live and returns the composed pixels' mean blue-minus-red in palette RGB
// together with the covered count. The tint's whole visible effect is that
// number going up.
func composeSub(t *testing.T, c *Client, fs *vfs.FS, name string, owner uint8) (float64, int) {
	t.Helper()
	m, err := compiledmodel.Load(fs, "objects3d/"+name+".3do")
	if err != nil {
		t.Skipf("%s is not in this install: %v", name, err)
	}
	for i := range c.indexed {
		c.indexed[i] = 0
	}
	states := make([]compiledmodel.PieceState, len(m.Pieces))
	draw := presentationrender.BuildUnitDrawInto(m, states, 0, 0, 0, frame.UnitView{}, nil, &presentationrender.DrawScratch{})
	draw.KeyPlane = true
	composed, ok := c.composeModel(draw, owner, teamColor{index: owner, known: true}, 1, modelCursorUnit, nil, 0)
	if !ok {
		t.Fatalf("%s composed no geometry", name)
	}
	// Coverage is read off the composition image, not off the framebuffer: a
	// hull pixel that lands on palette index 0 is a drawn pixel and would be
	// indistinguishable from the cleared background there.
	var sum float64
	n := 0
	for i, covered := range composed.image.covered {
		if !covered {
			continue
		}
		r, _, b, _ := c.pal.RGBA(composed.image.color[i])
		sum += float64(b) - float64(r)
		n++
	}
	c.finishModel(composed, nil)
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// TestSubmergedHullIsTintedBlue is the visual half of [03 R-REN-03A §8] and
// [03 R-WATER-01 §2] on real geometry: the same submarine, composed once with
// the water plane below it and once with the water plane over it, must come out
// bluer under water and must keep every pixel it had.
//
// The measure is the mean blue-minus-red over the composed pixels rather than a
// pixel census, because which palette entry each hull byte lands on is the blue
// table's business and that table is locked in the palette package. What this
// test owns is that the pass runs at all, that it runs on the right side of the
// water plane, and that ownership picks the arm — a regression that dropped the
// tint would show as the two numbers converging, and one that picked the wrong
// arm as an owned hull losing its pixels.
//
// Set NANOLATHE_SHOT_DIR to also write the captures for a human to look at.
func TestSubmergedHullIsTintedBlue(t *testing.T) {
	c, fs := captureClient(t, 96, 96)
	const viewer uint8 = 0
	c.buffer = frame.NewBuffer()

	// Water plane well below the model origin: nothing is submerged.
	publishSeaLevel(t, c, 1, -200, viewer)
	dry, dryPixels := composeSub(t, c, fs, "ARMSUB", viewer)
	writeCapture(t, c, "armsub-above-water")

	// Water plane well above it: the whole hull is under the surface.
	publishSeaLevel(t, c, 2, 200, viewer)
	wet, wetPixels := composeSub(t, c, fs, "ARMSUB", viewer)
	writeCapture(t, c, "armsub-submerged")

	if dryPixels == 0 || wetPixels == 0 {
		t.Fatalf("composed %d dry and %d submerged pixels", dryPixels, wetPixels)
	}
	// The recolour keeps coverage, unlike the erase arm: a tinted hull is the
	// same silhouette in a different colour.
	if wetPixels != dryPixels {
		t.Fatalf("submerged composition covered %d pixels against %d dry; the tint must not remove geometry", wetPixels, dryPixels)
	}
	if wet <= dry {
		t.Fatalf("submerged hull mean blue-minus-red = %.1f, dry = %.1f; the waterline tint is not reaching the image", wet, dry)
	}

	// The other arm, on the same geometry: an enemy hull with no sonar contact
	// is cut off below the surface instead [R-RAST-01 §4].
	_, enemyPixels := composeSub(t, c, fs, "ARMSUB", viewer+1)
	writeCapture(t, c, "armsub-submerged-enemy")
	if enemyPixels != 0 {
		t.Fatalf("a fully submerged enemy with no sonar contact composed %d pixels; it must be erased below the surface", enemyPixels)
	}
}

// TestMobileShadowCopiesTheComposedSubmarine is a real-geometry capture for
// the mobile shadow branch. The dry shadow must have the final body's complete
// silhouette, flattened to black, while a water plane above every possible
// byte key clips every shadow pixel [R-REN-03D §1, §6][R-RAST-01 §4].
//
// Set NANOLATHE_SHOT_DIR to retain dry and submerged captures for visual
// inspection alongside TestSubmergedHullIsTintedBlue.
func TestMobileShadowCopiesTheComposedSubmarine(t *testing.T) {
	c, fs := captureClient(t, 96, 96)
	const viewer uint8 = 0
	c.buffer = frame.NewBuffer()
	m, err := compiledmodel.Load(fs, "objects3d/ARMSUB.3do")
	if err != nil {
		t.Skipf("ARMSUB is not in this install: %v", err)
	}
	states := make([]compiledmodel.PieceState, len(m.Pieces))
	draw := presentationrender.BuildUnitDrawInto(m, states, 0, 0, 0, frame.UnitView{}, nil, &presentationrender.DrawScratch{})
	draw.KeyPlane, draw.CastsShadow = true, true

	compose := func(tick uint32, sea int32, capture string) (bodyPixels, shadowPixels int) {
		t.Helper()
		publishSeaLevel(t, c, tick, sea, viewer)
		for i := range c.indexed {
			c.indexed[i] = 200
		}
		c.resetListForTest()
		body, ok := c.composeModel(draw, viewer, teamColor{index: viewer, known: true}, 1, modelCursorUnit, nil, 0)
		if !ok {
			t.Fatal("ARMSUB composed no geometry")
		}
		shadow := c.buildModelShadow(draw, body.image)
		if shadow == nil {
			t.Fatal("ARMSUB mobile shadow was not built")
		}
		for _, covered := range body.image.covered {
			if covered {
				bodyPixels++
			}
		}
		for i, covered := range shadow.covered {
			if !covered {
				continue
			}
			if shadow.color[i] != shadowColorIndex {
				t.Fatalf("ARMSUB shadow pixel %d has palette index %d, want %d", i, shadow.color[i], shadowColorIndex)
			}
			shadowPixels++
		}
		c.finishModel(body, nil)
		c.replayForTest()
		writeCapture(t, c, capture)
		return bodyPixels, shadowPixels
	}

	dryBody, dryShadow := compose(1, -200, "armsub-shadow-above-water")
	if dryBody == 0 || dryShadow != dryBody {
		t.Fatalf("dry ARMSUB has %d body and %d shadow pixels; the shadow must copy its final body silhouette", dryBody, dryShadow)
	}
	// The threshold is clamped to 255, so this positive depth clips every
	// possible key byte inclusively. The body is still present: waterline
	// ownership tints it, whereas mobile-shadow clipping removes only shadow.
	wetBody, wetShadow := compose(2, 1000, "armsub-shadow-submerged")
	if wetBody != dryBody {
		t.Fatalf("submerged ARMSUB body has %d pixels, want dry body's %d", wetBody, dryBody)
	}
	if wetShadow != 0 {
		t.Fatalf("fully submerged ARMSUB shadow has %d pixels, want none", wetShadow)
	}
}
