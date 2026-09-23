package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
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

// The strength percentage reaches the composite through the two octave
// weights alone: 100 percent is the tuned look, the weights scale linearly with
// the percentage up to its cap, and 0 turns the layer off so no source batches
// and the resolve spends no pass (§19.4).
func TestGlowStrengthScalesOctaveWeightsAndZeroDisables(t *testing.T) {
	r := &Renderer{}
	r.tables.atlas = &ebiten.Image{}
	r.surfaces[0] = &ebiten.Image{}
	r.SetGlow(true)
	for _, tc := range []struct {
		percent    int
		near, far  float32
		wantActive bool
	}{
		{GlowStrengthDefault, glowNearWeight, glowFarWeight, true},
		{50, glowNearWeight / 2, glowFarWeight / 2, true},
		{GlowStrengthMax, glowNearWeight * 2, glowFarWeight * 2, true},
		{GlowStrengthMax + 100, glowNearWeight * 2, glowFarWeight * 2, true},
		{0, 0, 0, false},
		{-20, 0, 0, false},
	} {
		r.SetGlowStrength(tc.percent)
		near, far := r.glow.octaveWeights()
		if math.Abs(float64(near-tc.near)) > 1e-6 || math.Abs(float64(far-tc.far)) > 1e-6 {
			t.Fatalf("strength %d weights near %v far %v, want %v and %v", tc.percent, near, far, tc.near, tc.far)
		}
		if got := r.glowActive(); got != tc.wantActive {
			t.Fatalf("strength %d glowActive %v, want %v", tc.percent, got, tc.wantActive)
		}
	}
	// At 0 an emissive stroke batches nothing, and a batch left from before the
	// strength dropped is not resolved: no shader is compiled, no pass counted.
	r.SetGlowStrength(GlowStrengthDefault)
	r.glowLine(drawlist.Line{X0: 1, Y0: 1, X1: 9, Y1: 1, Index: 255, Emissive: true})
	if r.glow.quads != 1 {
		t.Fatalf("at the default strength a stroke batched %d quads, want 1", r.glow.quads)
	}
	r.SetGlowStrength(0)
	r.glowLine(drawlist.Line{X0: 1, Y0: 3, X1: 9, Y1: 3, Index: 255, Emissive: true})
	if r.glow.quads != 1 {
		t.Fatalf("at strength 0 a stroke still batched (%d quads)", r.glow.quads)
	}
	r.resolveGlow()
	if r.glow.compiled || r.modelStats.GlowPasses != 0 {
		t.Fatalf("strength 0 resolved the batch: compiled %v, %d passes", r.glow.compiled, r.modelStats.GlowPasses)
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
	// Strength 0 is off: the frame is the exact frame with the switch off.
	r.SetGlowStrength(0)
	zero, err := read(true)
	r.SetGlowStrength(GlowStrengthDefault)
	if err != nil {
		return err
	}
	if !bytes.Equal(zero, off) {
		return fmt.Errorf("glow at strength 0 differs from glow off")
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

// A stored display.glowStrength of 50 reaches the renderer the way every host
// hands it over: the settings loader repairs it, the client carries it, and the
// executor site copies the client's value before Execute, which halves both
// octave weights (§19.4).
func TestGlowStrengthSettingReachesRendererThroughClient(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"version":` + strconv.Itoa(settings.FileVersion) + `,"display":{"glowStrength":50}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	loaded, err := settings.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	cl := &client.Client{}
	cl.SetGlowStrength(loaded.Display.GlowStrength)
	r := &Renderer{}
	r.SetGlowStrength(cl.GlowStrength())
	near, far := r.glow.octaveWeights()
	if near != glowNearWeight/2 || far != glowFarWeight/2 {
		t.Fatalf("a stored strength of 50 gave octave weights %v and %v, want %v and %v",
			near, far, glowNearWeight/2, glowFarWeight/2)
	}
}

// Each family's strength reaches only its own family, and 0 turns off only that
// family (§19.4): the weapons percentage scales a beam's emission, the
// nanolathe percentage the spray's glow and the light its cluster casts, and
// the ground percentage the terrain receiver's share of every light alone.
func TestGlowFamiliesScaleOnlyTheirOwnFamily(t *testing.T) {
	type reading struct {
		weapon, nanoGlow float32
		weaponQuad       bool
		nanoQuad         bool
		nanoLight        [3]float32
		ground           float32
		lit              bool
	}
	read := func(weapons, nanolathe, ground int) reading {
		r := &Renderer{w: 320, h: 240}
		r.tables.atlas = &ebiten.Image{}
		r.surfaces[0] = &ebiten.Image{}
		r.SetGlow(true)
		r.SetGlowStrength(GlowStrengthDefault)
		for i := 161; i <= 167; i++ {
			r.displayPalette[i] = [4]byte{40, 200, 40, 255}
		}
		r.SetGlowFamilies(weapons, nanolathe, ground)
		var out reading
		r.glowLine(drawlist.Line{X0: 10, Y0: 10, X1: 60, Y1: 10, Index: 255, Emissive: true})
		if n := len(r.glow.verts); n > 0 {
			out.weaponQuad, out.weapon = true, r.glow.verts[n-1].ColorG
		}
		before := len(r.glow.verts)
		spray := drawlist.Fill{Nano: true, Rect: drawlist.Rect{X: 100, Y: 80, W: 2, H: 2}, Index: 163, WorldHeight: 16}
		r.glowNano(spray)
		if n := len(r.glow.verts); n > before {
			out.nanoQuad, out.nanoGlow = true, r.glow.verts[n-1].ColorG
		}
		var list drawlist.List
		for i := 0; i < 10; i++ {
			f := spray
			f.Rect.X += int32(i)
			list.RecordFill(f)
		}
		r.prepareBattleLighting(&list)
		if len(r.lighting.lights) > 0 {
			out.lit = true
			out.nanoLight = r.lighting.lights[0].color
			out.ground = groundScale(&r.lighting.lights[0])
		}
		return out
	}
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-6 }
	base := read(100, 100, 100)
	if !base.weaponQuad || !base.nanoQuad || !base.lit || base.ground <= 0 {
		t.Fatalf("default families left a family dark: %+v", base)
	}
	for _, tc := range []struct {
		name                           string
		weapons, nanolathe, ground     int
		weapon, nanoGlow, light, earth float32
	}{
		{"weapons at half", 50, 100, 100, 0.5, 1, 1, 1},
		{"nanolathe at half", 100, 50, 100, 1, 0.5, 0.5, 1},
		{"ground at half", 100, 100, 50, 1, 1, 1, 0.5},
		{"ground doubled", 100, 100, 200, 1, 1, 1, 2},
	} {
		got := read(tc.weapons, tc.nanolathe, tc.ground)
		if !near(got.weapon, base.weapon*tc.weapon) || !near(got.nanoGlow, base.nanoGlow*tc.nanoGlow) ||
			!near(got.nanoLight[1], base.nanoLight[1]*tc.light) || !near(got.ground, base.ground*tc.earth) {
			t.Fatalf("%s: read %+v against the default %+v", tc.name, got, base)
		}
	}
	if got := read(0, 100, 100); got.weaponQuad || !got.nanoQuad || got.nanoLight != base.nanoLight || got.ground != base.ground {
		t.Fatalf("weapons at 0 reached another family or kept its own: %+v", got)
	}
	if got := read(100, 0, 100); !got.weaponQuad || got.weapon != base.weapon || got.nanoQuad || got.lit {
		t.Fatalf("nanolathe at 0 reached another family or kept its own: %+v", got)
	}
	if got := read(100, 100, 0); got.weapon != base.weapon || got.nanoGlow != base.nanoGlow || got.nanoLight != base.nanoLight || got.ground != 0 {
		t.Fatalf("ground at 0 reached another family or kept its own: %+v", got)
	}
}

// A content pack's [effects] nanolathe=50 reaches the renderer the way every
// host hands it over: the annotation loader installs it, the client reads it,
// and the executor site copies it before Execute, halving only the nanolathe
// family (§19.4).
func TestGlowFamiliesContentReachesRendererThroughClient(t *testing.T) {
	// An [effects]-only file keeps the texture table in force, so restoring
	// the defaults this way leaves nothing else changed.
	t.Cleanup(func() {
		if err := client.SetMaterialTable("test", []byte("[effects]\n\t{\n\t}\n"), "test"); err != nil {
			t.Errorf("restore: %v", err)
		}
	})
	if err := client.SetMaterialTable("test", []byte("[effects]\n\t{\n\tnanolathe=50;\n\t}\n"), "test"); err != nil {
		t.Fatal(err)
	}
	cl := &client.Client{}
	r := &Renderer{}
	r.SetGlowFamilies(cl.GlowFamilies())
	if w, n, g := r.families.scale(glowFamilyWeapons), r.families.scale(glowFamilyNanolathe), r.families.scale(glowFamilyGround); w != 1 || n != 0.5 || g != 1 {
		t.Fatalf("nanolathe=50 gave family scales %v/%v/%v, want 1/0.5/1", w, n, g)
	}
}
