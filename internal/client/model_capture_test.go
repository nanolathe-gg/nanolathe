package client

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// captureClient is a headless client with real assets: the retail palette, the
// real texture index, and a framebuffer sized for one close-up model.
func captureClient(t *testing.T, w, h int) (*Client, *vfs.FS) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("retail assets not mountable: %v", err)
	}
	t.Cleanup(func() { fs.Close() })
	pal, err := palette.Load(fs)
	if err != nil {
		t.Skipf("retail palette not loadable: %v", err)
	}
	c := &Client{
		width: w, height: h, indexed: make([]uint8, w*h), pal: pal,
		modelFS: fs, texIndex: map[string]texRef{}, models: map[string]*unitModel{},
		// The camera is offset so the model's own origin lands mid-image: a
		// model-space +Z vertex projects up-screen, so a subject anchored at the
		// framebuffer corner composes off it [R-RAST-01 §2].
		cam:     &camera.Camera{X: int32(-w / 2), Z: int32(-h / 2), ViewW: int32(w), ViewH: int32(h), MapW: 4096, MapH: 4096},
		shading: true,
	}
	c.buildTextureIndex()
	return c, fs
}

// writeCapture dumps the composed indexed frame through the retail palette. It
// writes only when NANOLATHE_SHOT_DIR names a directory, so the assertions below
// are the test and the pictures are evidence for a human reviewer.
func writeCapture(t *testing.T, c *Client, name string) {
	t.Helper()
	dir := os.Getenv("NANOLATHE_SHOT_DIR")
	if dir == "" {
		return
	}
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	for i, idx := range c.indexed {
		r, g, b, a := c.pal.RGBA(idx)
		img.Set(i%c.width, i/c.width, color.RGBA{R: r, G: g, B: b, A: a})
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatalf("create capture: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode capture: %v", err)
	}
}

// composeAtHeading composes one stock model at one heading, clearing the frame
// first, and returns the number of covered pixels.
func composeAtHeading(t *testing.T, c *Client, fs *vfs.FS, name string, heading uint16, digger bool) int {
	t.Helper()
	m, err := compiledmodel.Load(fs, "objects3d/"+name+".3do")
	if err != nil {
		t.Skipf("%s is not in this install: %v", name, err)
	}
	for i := range c.indexed {
		c.indexed[i] = 0
	}
	states := make([]compiledmodel.PieceState, len(m.Pieces))
	draw := presentationrender.BuildUnitDrawSimple(m, states, heading, 0, 0, [3]numeric.Fixed{})
	draw.KeyPlane, draw.DiggerClip = true, digger
	c.resetListForTest()
	if !c.drawModel(draw, 0, 1, modelCursorUnit, nil, 0) {
		t.Fatalf("%s at heading %d composed no geometry", name, heading)
	}
	c.replayForTest()
	n := 0
	for _, v := range c.indexed {
		if v != 0 {
			n++
		}
	}
	return n
}

// TestCommanderComposesAtEveryHeading is the close-up composition capture for
// the two-chain edge walk [R-RAST-01 §1]. A turning unit is where a folded
// projection actually occurs: as the model rotates, faces pass edge-on and
// their projected rings degenerate and re-form.
//
// The assertion is the relationship — every heading composes a substantial,
// distinct silhouette, and none of them collapses — rather than a pixel census,
// so it survives palette and texture-cache changes and fails if the walk starts
// dropping faces at particular headings.
func TestCommanderComposesAtEveryHeading(t *testing.T) {
	c, fs := captureClient(t, 96, 96)
	headings := []uint16{0, 8192, 16384, 24576, 32768, 40960, 49152, 57344}
	counts := make([]int, len(headings))
	for i, h := range headings {
		counts[i] = composeAtHeading(t, c, fs, "ARMCOM", h, false)
		writeCapture(t, c, "armcom-heading-"+string(rune('0'+i)))
		if counts[i] < 200 {
			t.Fatalf("ARMCOM at heading %d composed only %d pixels; a face-dropping walk looks like this", h, counts[i])
		}
	}
	// A commander seen from eight headings must not compose the same silhouette
	// area every time; identical counts would mean the rotation never reached
	// the raster.
	same := true
	for i := 1; i < len(counts); i++ {
		if counts[i] != counts[0] {
			same = false
			break
		}
	}
	if same {
		t.Fatalf("every heading composed %d pixels; the orientation is not reaching the projection", counts[0])
	}
}

// TestDiggerClipRemovesTheBuriedHalf is the visual half of [R-REN-03A §8]: the
// erase can only ever remove pixels, and on a model that actually carries
// geometry below its own origin it removes some.
//
// It is stated as "at least one of the three stock Diggers shrinks" rather than
// "each one does" because how much of a pop-up defence sits below its origin is
// a property of the pose, not of the clip. `ARMAMB` at rest has nothing below
// the origin plane and loses nothing; `CORTOAST` has its whole base there. The
// exact boundary — key 125 erased, 126 kept — is locked separately on the key
// itself, so this test is about the direction of the effect on real geometry.
func TestDiggerClipRemovesTheBuriedHalf(t *testing.T) {
	c, fs := captureClient(t, 96, 96)
	shrank := 0
	for _, name := range []string{"CORTOAST", "ARMAMB", "CORVIPE"} {
		plain := composeAtHeading(t, c, fs, name, 0, false)
		writeCapture(t, c, name+"-plain")
		dug := composeAtHeading(t, c, fs, name, 0, true)
		writeCapture(t, c, name+"-digger")
		if dug > plain {
			t.Fatalf("%s: digger clip composed %d pixels against %d without it; the erase can only remove", name, dug, plain)
		}
		if dug == 0 {
			t.Fatalf("%s: digger clip erased the whole model", name)
		}
		if dug < plain {
			shrank++
		}
	}
	if shrank == 0 {
		t.Fatal("no stock Digger lost a pixel to the clip; the +75 and the erase at 125 are not on the same scale")
	}
}
