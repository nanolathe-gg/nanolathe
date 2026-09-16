package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// composeStockModel renders one stock 3DO into a small framebuffer with the
// real retail palette and returns the indexed result. No texture index is
// bound, so every face takes the authored-texture-miss flat colour and the
// silhouette is a single index — which is exactly what makes the fringe
// countable.
func composeStockModel(t *testing.T, pal *palette.Tables, fs *vfs.FS, name string, structure, antiAlias bool) ([]uint8, int, int) {
	t.Helper()
	m, err := compiledmodel.Load(fs, "objects3d/"+name+".3do")
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	const w, h = 96, 72
	c := &Client{
		width: w, height: h, indexed: make([]uint8, w*h), pal: pal,
		cam:       &camera.Camera{X: -48, Z: -50, ViewW: w, ViewH: h, MapW: 4096, MapH: 4096},
		antiAlias: antiAlias,
	}
	draw := presentationrender.BuildUnitDrawInto(m, nil, 0, 0, 0, frame.UnitView{}, nil, &presentationrender.DrawScratch{})
	draw.Structure, draw.KeyPlane = structure, true
	// Compose through the UNSHADED renderer. A BMcode=0 pose with the Shading
	// display option on emits
	// per-corner SHD rows, and the shaded flat writer of [R-REN-03A §5] would
	// then resolve this fixture's one authored colour into a spread of tones —
	// correct for a structure, but it is the downscale this test is measuring,
	// not the shading. Clearing the rows selects the unshaded pair of writers,
	// which is what a mobile subject gets, and keeps the silhouette one index
	// so the fringe stays countable.
	for pi := range draw.Pieces {
		for pri := range draw.Pieces[pi].Primitives {
			draw.Pieces[pi].Primitives[pri].ShadeRow = presentationrender.NoShadeRow
			draw.Pieces[pi].Primitives[pri].ShadeRows = nil
		}
	}
	if !c.drawModel(draw, 0, teamColor{index: 0, known: true}, 1, modelCursorUnit, nil, 0) {
		t.Fatalf("%s composed no geometry", name)
	}
	c.replayForTest()
	return c.indexed, w, h
}

func indexHistogram(px []uint8) map[uint8]int {
	h := map[uint8]int{}
	for _, v := range px {
		if v != 0 {
			h[v]++
		}
	}
	return h
}

// TestStructureAntiAliasProducesTheRedFringe is the end-to-end lock on
// [R-REN-03A §6] and [R-REN-03A §7], against the real PALETTE.PAL and
// PALETTE.ALP and real stock geometry.
//
// With anti-aliasing off a single-colour model composes to exactly one palette
// index. With it on, the silhouette gains a border of *different* indices,
// produced by blending the body colour against the composition background —
// palette index 1, (128,0,0) — and those border indices must be red-shifted
// relative to the body. That is retail's building fringe, reproduced
// deliberately.
func TestStructureAntiAliasProducesTheRedFringe(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets not mountable: %v", err)
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		t.Skipf("retail palette not loadable: %v", err)
	}
	// The mechanism only works because the background is maroon.
	if r, g, b, _ := pal.RGBA(transparentModelIndex); r != 128 || g != 0 || b != 0 {
		t.Fatalf("PALETTE.PAL[%d] = (%d,%d,%d), want the maroon (128,0,0) the fringe blends toward", transparentModelIndex, r, g, b)
	}

	for _, name := range []string{"ARMSOLAR", "ARMLAB"} {
		plain, _, _ := composeStockModel(t, pal, fs, name, true, false)
		aliased := indexHistogram(plain)
		if len(aliased) != 1 {
			t.Fatalf("%s without anti-aliasing composed %d distinct indices, want 1", name, len(aliased))
		}
		var body uint8
		for k := range aliased {
			body = k
		}
		bodyR, bodyG, bodyB, _ := pal.RGBA(body)
		bodyDistance := distanceToBackground(bodyR, bodyG, bodyB)

		smoothed, _, _ := composeStockModel(t, pal, fs, name, true, true)
		hist := indexHistogram(smoothed)
		if len(hist) < 2 {
			t.Fatalf("%s with anti-aliasing still composed one index; no fringe was produced", name)
		}
		fringe := 0
		for idx, n := range hist {
			if idx == body {
				continue
			}
			fringe += n
			// Every fringe index is the body blended toward the background, so
			// it must sit strictly closer to that maroon than the body does.
			// That is the whole mechanism, stated as a measurable property
			// rather than as a hue guess [R-REN-03A §7].
			r, g, b, _ := pal.RGBA(idx)
			if distanceToBackground(r, g, b) >= bodyDistance {
				t.Fatalf("%s fringe index %d rgb(%d,%d,%d) is no closer to the background than the body %d rgb(%d,%d,%d)",
					name, idx, r, g, b, body, bodyR, bodyG, bodyB)
			}
		}
		if fringe == 0 {
			t.Fatalf("%s produced no fringe pixels", name)
		}
		// The fringe is a border, not a wash: it must be a small minority.
		if total := fringe + hist[body]; fringe*4 > total {
			t.Fatalf("%s fringe is %d of %d pixels; a one-pixel border should be far smaller", name, fringe, total)
		}
	}
}

// TestMobileUnitsGetNoFringe is the other half of the gate: the same model
// composed as a BMcode=1 subject must be pixel-identical with the option on
// and off, because retail never anti-aliases mobile units [R-REN-03A §6].
func TestMobileUnitsGetNoFringe(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets not mountable: %v", err)
	}
	defer fs.Close()
	pal, err := palette.Load(fs)
	if err != nil {
		t.Skipf("retail palette not loadable: %v", err)
	}
	off, _, _ := composeStockModel(t, pal, fs, "ARMSOLAR", false, false)
	on, _, _ := composeStockModel(t, pal, fs, "ARMSOLAR", false, true)
	for i := range off {
		if off[i] != on[i] {
			t.Fatalf("mobile subject differed at pixel %d with the option on: %d vs %d", i, off[i], on[i])
		}
	}
}

// distanceToBackground is the squared RGB distance to the composition image's
// background colour, PALETTE.PAL entry 1 (128,0,0) [R-REN-03A §7].
func distanceToBackground(r, g, b uint8) int {
	dr, dg, db := int(r)-128, int(g), int(b)
	return dr*dr + dg*dg + db*db
}
