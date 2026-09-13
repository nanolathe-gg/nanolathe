package client

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestLensCapturesBeforeWritingAndSkipsSampledKey(t *testing.T) {
	c := &Client{width: 40, height: 40, indexed: make([]byte, 1600)}
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			c.indexed[y*40+x] = byte(1 + (x+3*y)%254)
		}
	}
	clip := drawlist.Rect{W: 40, H: 40}
	l := drawlist.Lens{X: 20, Y: 20, Clip: clip, Key: 0}
	before := append([]byte(nil), c.indexed...)
	c.classicSink().(drawlist.LensSink).Lens(l)
	if c.indexed[19*40+19] != before[20*40+20] || c.indexed[18*40+18] != before[19*40+19] {
		t.Fatal("lens did not snapshot inward samples before stores")
	}
	if c.indexed[15*40+20] != before[15*40+20] {
		t.Fatal("sentinel rim changed")
	}
	copy(c.indexed, before)
	c.indexed[20*40+20] = 0
	c.classicSink().(drawlist.LensSink).Lens(l)
	if c.indexed[19*40+19] != before[19*40+19] {
		t.Fatal("sampled transparent key overwrote destination")
	}
}

func TestLensAdmissionUsesInclusiveViewportEdgesAndPreservesPriorDraws(t *testing.T) {
	c := &Client{width: 640, height: 480, indexed: make([]byte, 640*480), cam: &camera.Camera{}}
	for _, tc := range []struct {
		x, y int
		want bool
	}{{128, 32, true}, {639, 447, true}, {127, 32, false}, {640, 447, false}, {128, 31, false}, {639, 448, false}} {
		v := frame.ProjectileView{X: numeric.FixedFromInt(int64(tc.x)), Z: numeric.FixedFromInt(int64(tc.y))}
		if got := c.admitProjectileLens(v); got != tc.want {
			t.Fatalf("center(%d,%d)=%v", tc.x, tc.y, got)
		}
	}
	views := []frame.ProjectileView{
		{Handle: 1, RenderType: render.RenderTypeBeam, X: numeric.FixedFromInt(180), Z: numeric.FixedFromInt(60), TailX: numeric.FixedFromInt(185), TailZ: numeric.FixedFromInt(60), PrimaryColor: 7, HasPrimaryColor: true},
		{Handle: 2, RenderType: render.RenderTypeGlobalGAF, X: numeric.FixedFromInt(127), Z: numeric.FixedFromInt(60)},
		{Handle: 3, RenderType: render.RenderTypeBeam, X: numeric.FixedFromInt(200), Z: numeric.FixedFromInt(60), TailX: numeric.FixedFromInt(205), TailZ: numeric.FixedFromInt(60), PrimaryColor: 9, HasPrimaryColor: true},
	}
	stats := c.DrawProjectileViews(views, 1, func(frame.ProjectileView) bool { return true }, nil, render.ProjectileDispatchOptions{})
	c.replayForTest()
	if !stats.Aborted || stats.Dispatched != 1 || c.indexed[60*640+180] != 7 || c.indexed[60*640+200] != 0 {
		t.Fatalf("abort lost preceding output or painted following output: %+v", stats)
	}
}

// This explicitly authored background and lens sequence has no stock unit
// claim. The device fixture uses the same small scene for paired captures.
func TestLensClassicAuthoredCaptures(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		n, _ := scale.Whole()
		w, h := 128*int(n), 80*int(n)
		c := &Client{width: w, height: h, indexed: make([]byte, w*h)}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c.indexed[y*w+x] = byte(1 + (x/int(n)+17*(y/int(n)))%254)
			}
		}
		// Source-key equality and a preceding beam distinguish skips from copies.
		for y := 30 * int(n); y < 31*int(n); y++ {
			for x := 75 * int(n); x < 76*int(n); x++ {
				c.indexed[y*w+x] = 0
			}
		}
		c.list.RecordLine(drawlist.Line{X0: 24 * n, Y0: 30 * n, X1: 31 * n, Y1: 30 * n, Index: 210})
		clip := drawlist.Rect{W: int32(w), H: int32(h)}
		for _, xy := range [][2]int32{{30, 30}, {31, 30}, {75, 30}, {0, 60}, {127, 60}} {
			c.list.RecordLens(drawlist.Lens{X: xy[0] * n, Y: xy[1] * n, Scale: scale, Clip: clip})
		}
		c.list.RecordLine(drawlist.Line{X0: 26 * n, Y0: 33 * n, X1: 35 * n, Y1: 33 * n, Index: 220})
		c.replayForTest()
		if c.indexed[33*int(n)*w+30*int(n)] != 220 {
			t.Fatal("later command did not overlay lens")
		}
		if dir := os.Getenv("NANOLATHE_LENS_SHOTS"); dir != "" {
			out := image.NewRGBA(image.Rect(0, 0, w, h))
			for i, v := range c.indexed {
				out.Pix[i*4] = v
				out.Pix[i*4+1] = v * 3
				out.Pix[i*4+2] = v * 7
				out.Pix[i*4+3] = 255
			}
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "lens-classic-"+scale.String()+".png")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, out)
			closeErr := f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
		}
	}
}
