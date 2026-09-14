package gpurender

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The blur kernel is a normalized Gaussian: the centre plus twice each
// one-sided tap sums to one, so a flat field blurs to itself and the glow's
// energy is neither gained nor lost by the blur (§19).
func TestGlowWeightsAreNormalized(t *testing.T) {
	w := glowWeights()
	sum := w[0]
	for i := 1; i < len(w); i++ {
		sum += 2 * w[i]
		if w[i] >= w[i-1] {
			t.Fatalf("tap %d weight %g is not below tap %d weight %g", i, w[i], i-1, w[i-1])
		}
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Fatalf("kernel sums to %g, want 1", sum)
	}
}

// A run's indices are relative to the vertex slice its draw hands the device,
// and a change of image bindings opens a new run; the same bindings continue
// the open one (§19).
func TestGlowBatchIndicesAreRunRelative(t *testing.T) {
	var g glowLayer
	a := [4]*ebiten.Image{}
	b := [4]*ebiten.Image{0: &ebiten.Image{}}
	custom := [4]float32{0, 0, 0, glowOpSolid}
	g.rect(a, 0, 0, 1, 1, 0, 0, 0, 0, [4]float32{}, custom)
	g.rect(a, 2, 0, 3, 1, 0, 0, 0, 0, [4]float32{}, custom)
	g.rect(b, 4, 0, 5, 1, 0, 0, 0, 0, [4]float32{}, custom)
	g.rect(a, 6, 0, 7, 1, 0, 0, 0, 0, [4]float32{}, custom)
	if g.quads != 4 || len(g.verts) != 16 || len(g.idx) != 24 {
		t.Fatalf("batch holds %d quads, %d vertices, %d indices; want 4, 16, 24", g.quads, len(g.verts), len(g.idx))
	}
	if len(g.runs) != 3 {
		t.Fatalf("batch opened %d runs, want 3 (a, b, a)", len(g.runs))
	}
	for i := range g.runs {
		run := &g.runs[i]
		for _, ix := range g.idx[run.iOff : run.iOff+run.iLen] {
			if int32(ix) >= run.vLen {
				t.Fatalf("run %d index %d is not below its own vertex count %d", i, ix, run.vLen)
			}
		}
	}
	g.resetFrame()
	if g.quads != 0 || len(g.runs) != 0 || len(g.verts) != 0 || g.resolved {
		t.Fatalf("resetFrame left the batch non-empty: %+v", g)
	}
}

// The halo is sized in world pixels, so the blur's tap spacing rides the view
// scale: one texel at the native view, which is the kernel the layer was tuned
// with, and proportionally wider or narrower at any other scale (§19, §16.3).
func TestGlowBlurStepFollowsViewScale(t *testing.T) {
	if got := glowBlurStep(1); got != 1 {
		t.Fatalf("native view tap spacing %v, want exactly 1 texel", got)
	}
	for _, tc := range []struct{ scale, want float32 }{{2, 2}, {1.5, 1.5}, {0.5, 0.5}, {0.25, 0.25}} {
		if got := glowBlurStep(tc.scale); got != tc.want {
			t.Fatalf("view scale %v tap spacing %v, want %v", tc.scale, got, tc.want)
		}
	}
	if got := glowBlurStep(0); got != 1 {
		t.Fatalf("a frame with no source read %v, want the native view's 1", got)
	}
}

// The frame's screen pixels per world pixel is the recorder's step times the
// live zoom factor the scheduler applies. Reading only one of the two is what
// made a beam's halo half its width at the 2x step (§16.2).
func TestGlowViewScaleCombinesStepAndZoom(t *testing.T) {
	r := &Renderer{}
	if got := r.glowViewScale(); got != 1 {
		t.Fatalf("a frame with no terrain read %v, want the native view's 1", got)
	}
	r.water.record.Scale = camera.ViewScaleDetail
	if got := r.glowViewScale(); got != 2 {
		t.Fatalf("the 2x step at rest read %v, want 2", got)
	}
	// The 2x step with the factor eased to 1.5x: the recorder projected at two
	// screen pixels per world pixel and the executor shrinks by three quarters.
	r.sched.setWorldTransform(0.75, 0, 0)
	if got := r.glowViewScale(); got != 1.5 {
		t.Fatalf("the 2x step under a 0.75 transform read %v, want 1.5", got)
	}
	r.water.record.Scale = camera.ViewScaleNative
	if got := r.glowViewScale(); got != 0.75 {
		t.Fatalf("the native step under a 0.75 transform read %v, want 0.75", got)
	}
}

// A stroke's quad is the segment extended by its half-width at both ends and
// widened by it on both sides, so a one-pixel beam has energy to spread and a
// zero-length stroke still covers a square (§19).
func TestGlowStrokeCorners(t *testing.T) {
	xs, ys := glowStrokeCorners(10, 5, 50, 5, 2)
	if xs != [4]float32{8, 52, 8, 52} || ys != [4]float32{7, 7, 3, 3} {
		t.Fatalf("horizontal stroke corners xs=%v ys=%v, want xs=[8 52 8 52] ys=[7 7 3 3]", xs, ys)
	}
	xs, ys = glowStrokeCorners(5, 10, 5, 50, 2)
	if xs != [4]float32{3, 3, 7, 7} || ys != [4]float32{8, 52, 8, 52} {
		t.Fatalf("vertical stroke corners xs=%v ys=%v, want xs=[3 3 7 7] ys=[8 52 8 52]", xs, ys)
	}
	xs, ys = glowStrokeCorners(9, 9, 9, 9, 1.5)
	if xs != [4]float32{7.5, 10.5, 7.5, 10.5} || ys != [4]float32{10.5, 10.5, 7.5, 7.5} {
		t.Fatalf("degenerate stroke corners xs=%v ys=%v, want a 3-px square about (9,9)", xs, ys)
	}
}

// Without a palette or a surface nothing is batched, whatever the switch says,
// so a renderer that cannot resolve a colour never accumulates work (§19).
func TestGlowNeedsPaletteAndSurface(t *testing.T) {
	r := &Renderer{}
	r.SetGlow(true)
	r.glowLine(drawlist.Line{X0: 1, Y0: 1, X1: 9, Y1: 1, Index: 255, Emissive: true})
	if r.glow.quads != 0 {
		t.Fatalf("a renderer without a palette batched %d glow quads", r.glow.quads)
	}
	r.resolveGlow()
	if !r.glow.resolved {
		t.Fatal("resolveGlow did not mark the frame resolved")
	}
}

// glowFixtureList is one frame: a flat dark field with one bright emissive
// stroke across its middle, inside a world region at the rest factor.
func glowFixtureList(w, h int32, field, bright uint8) drawlist.List {
	var list drawlist.List
	list.RecordClear()
	list.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative,
		Viewport: drawlist.Rect{W: w, H: h}, RecordW: w, RecordH: h})
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: field, Style: drawlist.FillSolid})
	list.RecordLine(drawlist.Line{X0: 40, Y0: h / 2, X1: 88, Y1: h / 2, Index: bright, Emissive: true})
	list.RecordWorld(drawlist.WorldSpace{Begin: false, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative,
		Viewport: drawlist.Rect{W: w, H: h}, RecordW: w, RecordH: h})
	list.RecordExpand()
	return list
}

// checkGlowDevicePixels draws the fixture with the layer on and off and reads
// both back: off, the frame is the exact classic expansion; on, the stroke's
// own pixels are unchanged (a screen blend leaves white white), the field
// beside the stroke is brighter than the field, the brightening falls off with
// distance, and the far corner is the field to within the blur's last tap
// (§19).
func checkGlowDevicePixels() error {
	pal := fixturePalette()
	const w, h, field, bright = 128, 64, 20, 255
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	read := func(on bool) ([]byte, error) {
		r.SetGlow(on)
		list := glowFixtureList(w, h, field, bright)
		img := r.Execute(&list, w, h)
		if img == nil {
			return nil, fmt.Errorf("glow device fixture returned no image")
		}
		pixels := make([]byte, w*h*4)
		img.ReadPixels(pixels)
		return pixels, nil
	}
	off, err := read(false)
	if err != nil {
		return err
	}
	if r.modelStats.GlowQuads != 0 || r.modelStats.GlowPasses != 0 {
		return fmt.Errorf("glow off still counted %d quads and %d passes", r.modelStats.GlowQuads, r.modelStats.GlowPasses)
	}
	for _, p := range [][2]int{{64, 38}, {64, 44}, {2, 2}, {64, 32}} {
		want := byte(field)
		if p[1] == 32 {
			want = bright
		}
		if err := checkExactIndex(fmt.Sprintf("glow off at (%d,%d)", p[0], p[1]), off, (p[1]*w+p[0])*4, &pal, want); err != nil {
			return err
		}
	}
	on, err := read(true)
	if err != nil {
		return err
	}
	if r.modelStats.GlowQuads != 1 || r.modelStats.GlowPasses == 0 {
		return fmt.Errorf("glow on counted %d quads and %d passes, want 1 quad and a resolve", r.modelStats.GlowQuads, r.modelStats.GlowPasses)
	}
	at := func(x, y int) int { return int(on[(y*w+x)*4]) }
	if err := checkExactIndex("the stroke itself with glow on", on, (32*w+64)*4, &pal, bright); err != nil {
		return err
	}
	near, far, corner := at(64, 38), at(64, 44), at(2, 2)
	if near <= field+8 {
		return fmt.Errorf("beside the stroke reads %d, want clearly above the field %d", near, field)
	}
	if far >= near {
		return fmt.Errorf("further from the stroke reads %d, want below the nearer %d", far, near)
	}
	if corner > field+2 {
		return fmt.Errorf("the far corner reads %d, want the field %d to within the blur's reach", corner, field)
	}
	// The same recorded stroke on a frame the recorder projected at the 2x step:
	// a world pixel is two screen pixels, so the halo is twice as many screen
	// pixels across and the reading twelve pixels out rises (§19, §16.3). The
	// step reaches the layer through the terrain record, as it reaches the
	// aircraft shadows; this fixture records no terrain, so it is set directly.
	r.water.record.Scale = camera.ViewScaleDetail
	defer func() { r.water.record.Scale = 0 }()
	detail, err := read(true)
	if err != nil {
		return err
	}
	atDetail := func(x, y int) int { return int(detail[(y*w+x)*4]) }
	if err := checkExactIndex("the stroke itself at the 2x step", detail, (32*w+64)*4, &pal, bright); err != nil {
		return err
	}
	if d := atDetail(64, 44); d <= far {
		return fmt.Errorf("twelve pixels from the stroke the 2x-step halo reads %d, want above the 1x halo's %d", d, far)
	}
	if d := atDetail(64, 38); d <= field+8 {
		return fmt.Errorf("beside the stroke the 2x-step halo reads %d, want clearly above the field %d", d, field)
	}
	if dir := os.Getenv("NANOLATHE_GLOW_SHOTS"); dir != "" {
		return writeGlowScaleCaptures(dir)
	}
	return nil
}

// writeGlowScaleCaptures writes the same world scene at the native step and at
// the 2x step, for a review of the halo's size against the thing it comes from.
// The 2x frame is the same world drawn from twice as many pixels — twice the
// framebuffer, twice the stroke — so halving it should land on the 1x frame.
// It is the review the battle benchmark cannot give cheaply: a capture route
// frame at the player's start position has nothing emissive in it.
func writeGlowScaleCaptures(dir string) error {
	const field, bright = 20, 255
	pal := fixturePalette()
	for _, step := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		s := int32(step.Norm()) / 2
		w, h := 192*s, 128*s
		r, err := NewChecked(&pal, int(w), int(h))
		if err != nil {
			return err
		}
		r.SetGlow(true)
		// The step reaches the layer through the terrain record; this fixture
		// records no terrain, so it is set the way a terrain command would.
		r.water.record.Scale = step
		var list drawlist.List
		list.RecordClear()
		list.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomOf(step), Step: step,
			Viewport: drawlist.Rect{W: w, H: h}, RecordW: w, RecordH: h})
		list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 0, Y: 0, W: w, H: h}, Index: field, Style: drawlist.FillSolid})
		// One horizontal and one diagonal beam, in world pixels times the step.
		list.RecordLine(drawlist.Line{X0: 24 * s, Y0: 40 * s, X1: 168 * s, Y1: 40 * s, Index: bright, Emissive: true})
		list.RecordLine(drawlist.Line{X0: 48 * s, Y0: 108 * s, X1: 144 * s, Y1: 76 * s, Index: bright, Emissive: true})
		list.RecordWorld(drawlist.WorldSpace{Begin: false, Zoom: camera.ZoomOf(step), Step: step,
			Viewport: drawlist.Rect{W: w, H: h}, RecordW: w, RecordH: h})
		list.RecordExpand()
		img := r.Execute(&list, int(w), int(h))
		if img == nil {
			return fmt.Errorf("glow capture at step %v returned no image", step)
		}
		pixels := make([]byte, int(w)*int(h)*4)
		img.ReadPixels(pixels)
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("glow-step-%dx.png", s)))
		if err != nil {
			return err
		}
		err = png.Encode(f, &image.RGBA{Pix: pixels, Stride: int(w) * 4, Rect: image.Rect(0, 0, int(w), int(h))})
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// TestGlowDeviceFixture is opt-in because ordinary tests must not require a
// graphics device (C-G10); the hidden loop in TestMain runs the check.
func TestGlowDeviceFixture(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device glow fixtures")
	}
	if deviceFixtureResult != nil {
		t.Fatalf("device fixture loop: %v", deviceFixtureResult)
	}
}
