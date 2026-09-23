package gpurender

import (
	"fmt"
	"image"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The Enhanced glow layer (docs/DESIGN_GPU_RENDERER.md §19).
//
// Retail's composite has no light: a laser is a one-pixel line in a palette
// colour and an explosion is art plus a brightening of the pixels under it.
// The glow layer gives those sources a halo. While the frame's world region is
// being replayed, every emissive command — a beam or lightning stroke, an
// effect, projectile or strip sprite, an explosion flash disc or ground halo —
// appends a second quad to a batch this file owns. When the fog composite is
// about to compile, or the world region closes without one, the batch is drawn
// into a full-frame source plane, that plane is shrunk and blurred at a quarter
// and an eighth of the frame, and the two blurred octaves are added back onto
// the composite. Everything after that — the fog, the chrome, the cursor — is
// drawn over the glow as it always was, so the glow never reaches the
// interface and the black fog still hides it.
//
// What a source emits:
//
//   - A stroke emits its palette colour over a quad glowLineWidth screen pixels
//     wide with rounded-off ends, so a one-pixel beam has enough energy to show
//     after the blur.
//   - A sprite emits its own resolved colour where that colour is bright:
//     the emission is the texel's colour scaled by a smooth step from
//     glowThreshold to full brightness, so the fire of an explosion glows and
//     its smoke does not. A tinted (half-blended) strip sprite emits at half
//     strength, as it is composited.
//   - A flash disc or halo emits the light it ADDED: the composite colour under
//     the fragment times the row family's high lane, max(k−1, 0), which is the
//     brightening the disc applied (points.go, flash.go). That is the energy
//     the clamped composite could not show, and it needs no tint of its own:
//     the ground's colour under the light is the glow's colour.
//
// The batch is additive, so its order does not matter and it needs no phase
// placement; the world transform of §16.3 is applied to its vertices exactly
// as the scheduler applies it to the commands it shadows. Nothing here is
// simulation state and nothing is retail behaviour: the classic executor never
// sees the emissive flags, and every device call happens after the frame's
// own replay has committed what the glow reads.

// The layer's look, in one place. These are presentation knobs, not retail
// constants; they were tuned by eye against the battle benchmark and the M2/M5
// captures.
const (
	// glowLineWidth is the width of a stroke's emissive quad in WORLD pixels;
	// it is scaled to screen pixels by the frame's view scale.
	glowLineWidth = 4.0
	// glowGain multiplies every emission before the blur.
	glowGain = 1.0
	// glowThreshold is the brightness (largest channel) below which a sprite
	// texel emits nothing; the emission rises smoothly to full above it.
	glowThreshold = 0.65
	// glowSpriteGain scales sprite emission: art is already bright where it
	// emits, so its halo is kept below a stroke's.
	glowSpriteGain = 0.6
	// glowLightGain scales the flash and halo emission, whose (k−1) lane reaches
	// 1.0 for the brightest LHT rows.
	glowLightGain = 0.35
	// glowNearWeight and glowFarWeight are the two blurred octaves' shares of
	// the composite at the default strength: the quarter-resolution one is the
	// tight core, the eighth the wide falloff. They were 0.65 and 0.5 until a
	// play-test found the halo washing out bright hulls and the shipyard's
	// nanolathe emitters; halving both halves the halo's energy and keeps its
	// shape, so a strength of 200 percent reproduces the earlier look exactly.
	glowNearWeight = 0.325
	glowFarWeight  = 0.25
	// GlowStrengthDefault and GlowStrengthMax are the strength percentages of
	// SetGlowStrength: 100 is the tuned look above and 200 the most a
	// preference or a content pack may ask for. The percentage scales both
	// octaves' weights, and 0 turns the layer off.
	GlowStrengthDefault = 100
	GlowStrengthMax     = 200
	// glowTapCount is the number of taps on each side of the centre of the
	// separable blur, and glowSigma its standard deviation in steps of the tap
	// spacing the blur is drawn with.
	glowTapCount = 4
	glowSigma    = 2.0
	// glowOctaveNear and glowOctaveFar are the two blurred octaves' shrink
	// factors from the frame: a near texel is four framebuffer pixels across and
	// a far texel eight.
	glowOctaveNear = 4.0
	glowOctaveFar  = 8.0
	// glowNearSigmaWorld is the near octave's blur radius in WORLD pixels. The
	// halo is sized in world pixels, not screen pixels, because everything it
	// surrounds — the beam, the sprite, the flash disc — is drawn at the frame's
	// view scale (§16.3): a halo fixed in screen pixels is half as wide, relative
	// to the units it comes from, at the 2x step as at 1x, and changes size under
	// the wheel. The far octave is twice this, as it is twice the near octave's
	// texel. At the native view scale this is the eight framebuffer pixels the
	// layer was tuned at, so a 1x frame composes exactly as it did.
	glowNearSigmaWorld = glowSigma * glowOctaveNear
	// glowRunVertexLimit bounds one device draw, as the scheduler's runs are.
	glowRunVertexLimit = schedRunVertexLimit
)

// The glow source shader's op selector, in Custom3.
const (
	// glowOpSolid emits PAL[ColorR] × ColorG: the stroke quad.
	glowOpSolid = 0
	// glowOpKeyed emits a keyed scene-atlas texel's resolved colour, scaled by
	// the smooth step from ColorB to 1 of its brightness, times ColorG.
	glowOpKeyed = 1
	// glowOpLight emits the composite colour under the fragment times the lane
	// read out of source 0 (the flash atlas) times ColorG.
	glowOpLight = 2
	// glowOpHalo emits the composite colour under the fragment times ColorR
	// (the row's high lane) times ColorG, inside the disc the custom lanes
	// describe, as destOpHalo tests it.
	glowOpHalo = 3
)

// glowRun is one device draw of the batch: the image bindings it needs and its
// vertex and index span.
type glowRun struct {
	imgs             [4]*ebiten.Image
	vOff, vLen, iOff int32
	iLen             int32
}

// glowLayer is the renderer's glow state: the switch, the batch, the planes and
// the compiled passes. Storage is retained across frames so a steady-state
// frame allocates nothing here.
type glowLayer struct {
	// on is the display switch (Renderer.SetGlow); off, no source appends and
	// the resolve is a no-op.
	on bool
	// strength is the halo's scale as a fraction of the default look
	// (Renderer.SetGlowStrength): 1 at 100 percent. Zero is off exactly as the
	// switch is, so a strength of 0 costs nothing.
	strength float32
	// resolved is set once this frame's resolve has run, so a frame with both
	// a fog composite and a world-region close resolves once.
	resolved bool
	// viewScale is the frame's screen pixels per world pixel, recorded as the
	// sources append so the resolve sizes the blur the same way the sources
	// sized themselves. Zero means no source appended and is read as the native
	// view.
	viewScale float32

	runs  []glowRun
	verts []ebiten.Vertex
	idx   []uint32
	quads int

	// source is the full-frame emission plane; half, quarter and eighth its
	// shrunk octaves, quarterB and eighthB the blur ping-pong partners. The
	// source is unmanaged so its texels never depend on an atlas placement
	// (§13.12 "The defect this round exposed").
	source                                   *ebiten.Image
	half, quarter, quarterB, eighth, eighthB *ebiten.Image
	w, h                                     int

	sourceShader, blurShader *ebiten.Shader
	shaderErr                error
	compiled                 bool

	shaderOpts ebiten.DrawTrianglesShaderOptions
	imageOpts  ebiten.DrawImageOptions
	quadVerts  [4]ebiten.Vertex
	quadIdx    [6]uint32
}

// SetGlow switches the Enhanced glow layer (docs/DESIGN_GPU_RENDERER.md §19).
// The switch is read as the frame replays, so it takes effect on the next
// Execute.
func (r *Renderer) SetGlow(on bool) {
	if r == nil {
		return
	}
	r.glow.on = on
}

// SetGlowStrength sets the glow layer's strength as a percentage of the default
// look (docs/DESIGN_GPU_RENDERER.md §19.4): GlowStrengthDefault is the tuned
// halo, 0 is off exactly as SetGlow(false) is, and the value is clamped to
// 0..GlowStrengthMax. It scales the two octaves' weights and nothing else, so
// the halo keeps its size and colour and only its energy changes. Like the
// switch it is read as the frame replays and takes effect on the next Execute.
func (r *Renderer) SetGlowStrength(percent int) {
	if r == nil {
		return
	}
	r.glow.strength = glowStrengthScale(percent)
}

// glowStrengthScale is a strength percentage as the fraction of the default look
// the octave weights are scaled by.
func glowStrengthScale(percent int) float32 {
	return float32(min(max(percent, 0), GlowStrengthMax)) / GlowStrengthDefault
}

// octaveWeights are the two blurred octaves' shares of the composite at the
// layer's strength: the only values the strength reaches.
func (g *glowLayer) octaveWeights() (near, far float32) {
	return glowNearWeight * g.strength, glowFarWeight * g.strength
}

// glowActive reports whether sources should append to the batch: the switch is
// on, the strength is above zero, and a palette (the table atlas) is installed
// to resolve colours through.
func (r *Renderer) glowActive() bool {
	return r.glow.on && r.glow.strength > 0 && r.tables.atlas != nil && r.surfaces[0] != nil
}

// glowViewScale is the frame's screen pixels per world pixel: the record step
// the recorder projected the world at, times the live zoom factor the scheduler
// applies to world geometry (§16.2, §16.3). The terrain record carries the step
// — the aircraft shadow layer reads it from the same place — and a frame with no
// terrain is the native view. Every glow source is appended inside the world
// region, so the transform is armed whenever this is read.
func (r *Renderer) glowViewScale() float32 {
	s := float32(r.water.record.Scale.Float()) * r.sched.txf(1)
	if s <= 0 {
		return 1
	}
	return s
}

// noteGlowViewScale records the scale a source was appended at, and returns it.
func (r *Renderer) noteGlowViewScale() float32 {
	s := r.glowViewScale()
	r.glow.viewScale = s
	return s
}

// resetFrame drops the batch for a new Execute; storage is kept.
func (g *glowLayer) resetFrame() {
	g.resolved = false
	g.viewScale = 0
	g.runs = g.runs[:0]
	g.verts = g.verts[:0]
	g.idx = g.idx[:0]
	g.quads = 0
}

// selectRun opens a run for imgs unless the open run already binds them and has
// room for one more quad.
func (g *glowLayer) selectRun(imgs [4]*ebiten.Image) *glowRun {
	if n := len(g.runs); n > 0 {
		run := &g.runs[n-1]
		if run.imgs == imgs && int(run.vLen)+quadVertices <= glowRunVertexLimit {
			return run
		}
	}
	g.runs = append(g.runs, glowRun{imgs: imgs, vOff: int32(len(g.verts)), iOff: int32(len(g.idx))})
	return &g.runs[len(g.runs)-1]
}

// quad appends one quad with explicit corners to the batch, in the scheduler's
// vertex order. Corner positions are already in screen pixels.
func (g *glowLayer) quad(imgs [4]*ebiten.Image, xs, ys, sxs, sys [4]float32, col [4]float32, custom [4][4]float32) {
	run := g.selectRun(imgs)
	// Indices are relative to the run's own vertex span, which is the slice
	// its draw hands the device.
	base := uint32(len(g.verts)) - uint32(run.vOff)
	for i := 0; i < 4; i++ {
		g.verts = append(g.verts, ebiten.Vertex{
			DstX: xs[i], DstY: ys[i], SrcX: sxs[i], SrcY: sys[i],
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[i][0], Custom1: custom[i][1], Custom2: custom[i][2], Custom3: custom[i][3],
		})
	}
	g.idx = append(g.idx, base, base+1, base+2, base+1, base+2, base+3)
	run.vLen += quadVertices
	run.iLen += 6
	g.quads++
}

// rect appends one axis-aligned quad whose custom lanes are the same at every
// corner.
func (g *glowLayer) rect(imgs [4]*ebiten.Image, dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1 float32, col, custom [4]float32) {
	g.quad(imgs,
		[4]float32{dx0, dx1, dx0, dx1}, [4]float32{dy0, dy0, dy1, dy1},
		[4]float32{sx0, sx1, sx0, sx1}, [4]float32{sy0, sy0, sy1, sy1},
		col, [4][4]float32{custom, custom, custom, custom})
}

// glowLine appends a beam or lightning stroke: a quad glowLineWidth world
// pixels wide along the stroke, extended by half its width at both ends so a
// short stroke keeps its energy.
func (r *Renderer) glowLine(l drawlist.Line) {
	if !r.glowActive() {
		return
	}
	s := &r.sched
	x0, y0 := s.txx(float32(l.X0)+0.5), s.txy(float32(l.Y0)+0.5)
	x1, y1 := s.txx(float32(l.X1)+0.5), s.txy(float32(l.Y1)+0.5)
	// The stroke itself is one record pixel wide, so its halo has to take its
	// width from the view scale rather than from the stroke: at the 2x step the
	// free-zoom factor alone is half the screen pixels a world pixel covers.
	hw := float32(glowLineWidth) * 0.5 * r.noteGlowViewScale()
	if hw < 1 {
		hw = 1
	}
	xs, ys := glowStrokeCorners(x0, y0, x1, y1, hw)
	custom := [4]float32{0, 0, 0, glowOpSolid}
	r.glow.quad([4]*ebiten.Image{1: r.tables.atlas}, xs, ys,
		[4]float32{}, [4]float32{},
		[4]float32{float32(l.Index), glowGain, 0, 0},
		[4][4]float32{custom, custom, custom, custom})
}

// glowSprite appends a keyed GAF sprite whose top-left is (x, y) in record
// space, over the same clip-intersected rectangle the sprite itself covered.
// gain is the emission scale: 1 for an opaque sprite, ½ for a tinted one.
func (r *Renderer) glowSprite(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int, gain float32) {
	if f == nil || !r.glowActive() {
		return
	}
	e := r.sceneFrameFor(f)
	if !e.ok {
		return
	}
	r.noteGlowViewScale()
	fw, fh := int(f.Width), int(f.Height)
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, r.clipW()), min(clipY+clipH, r.clipH())
	col0, col1 := max(0, minX-x), min(fw, maxX-x)
	row0, row1 := max(0, minY-y), min(fh, maxY-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	s := &r.sched
	imgs := r.sceneImages(e)
	imgs[1] = r.tables.atlas
	r.glow.rect(imgs,
		s.txx(float32(x+col0)), s.txy(float32(y+row0)), s.txx(float32(x+col1)), s.txy(float32(y+row1)),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{0, gain * glowSpriteGain * glowGain, glowThreshold, 0}, [4]float32{0, 0, 0, glowOpKeyed})
}

// glowFlash appends one explosion disc: the same magnified atlas quad Flash
// compiled, reading the composite under it. The rectangle and source span are
// the ones Flash computed, in record space.
func (r *Renderer) glowFlash(cx0, cy0, cx1, cy1 int, sx0, sy0, sx1, sy1 float32) {
	if !r.glowActive() || r.sched.flash.img == nil {
		return
	}
	r.noteGlowViewScale()
	s := &r.sched
	r.glow.rect([4]*ebiten.Image{0: s.flash.img, 1: r.tables.atlas, 2: r.surfaces[0]},
		s.txx(float32(cx0)), s.txy(float32(cy0)), s.txx(float32(cx1)), s.txy(float32(cy1)),
		sx0, sy0, sx1, sy1,
		[4]float32{0, glowLightGain * glowGain, 0, 0}, [4]float32{0, 0, 0, glowOpLight})
}

// glowHalo appends one flat ground halo: the disc test of destOpHalo over the
// composite under it, emitting the row's high lane. Arguments are the ones
// Halo computed, in record space.
func (r *Renderer) glowHalo(cx0, cy0, cx1, cy1 int, high, lx0, ly0, lx1, ly1, r2 float32) {
	if !r.glowActive() || high <= 0 {
		return
	}
	r.noteGlowViewScale()
	s := &r.sched
	r.glow.quad([4]*ebiten.Image{1: r.tables.atlas, 2: r.surfaces[0]},
		[4]float32{s.txx(float32(cx0)), s.txx(float32(cx1)), s.txx(float32(cx0)), s.txx(float32(cx1))},
		[4]float32{s.txy(float32(cy0)), s.txy(float32(cy0)), s.txy(float32(cy1)), s.txy(float32(cy1))},
		[4]float32{}, [4]float32{},
		[4]float32{high, glowLightGain * glowGain, 0, 0},
		[4][4]float32{
			{lx0, ly0, r2, glowOpHalo},
			{lx1, ly0, r2, glowOpHalo},
			{lx0, ly1, r2, glowOpHalo},
			{lx1, ly1, r2, glowOpHalo},
		})
}

// ensureGlowPlanes sizes the planes to the frame. The source plane is the
// frame's size; each octave halves the one above, rounding up.
func (g *glowLayer) ensurePlanes(w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	if g.source != nil && g.w == w && g.h == h {
		return true
	}
	g.w, g.h = w, h
	g.source = ebiten.NewImageWithOptions(image.Rect(0, 0, w, h), &ebiten.NewImageOptions{Unmanaged: true})
	g.half = ebiten.NewImage((w+1)/2, (h+1)/2)
	g.quarter = ebiten.NewImage((w+3)/4, (h+3)/4)
	g.quarterB = ebiten.NewImage((w+3)/4, (h+3)/4)
	g.eighth = ebiten.NewImage((w+7)/8, (h+7)/8)
	g.eighthB = ebiten.NewImage((w+7)/8, (h+7)/8)
	return true
}

// resolveGlow draws the batch, blurs it and adds it onto the composite
// (§19). It is called before the fog composite compiles and when the world
// region closes, and runs at most once per frame. The scheduler is submitted
// first, so the composite the light sources read and the surface the glow is
// added to are the frame as replayed so far.
func (r *Renderer) resolveGlow() {
	if r == nil || r.glow.resolved {
		return
	}
	g := &r.glow
	g.resolved = true
	if !g.on || g.strength <= 0 || g.quads == 0 || r.surfaces[0] == nil {
		return
	}
	if !g.compiled {
		g.compiled = true
		g.sourceShader, g.shaderErr = ebiten.NewShader([]byte(glowSourceShaderSource()))
		if g.shaderErr == nil {
			g.blurShader, g.shaderErr = ebiten.NewShader([]byte(glowBlurShaderSource()))
		}
	}
	if g.sourceShader == nil || g.blurShader == nil {
		return
	}
	r.submitSchedule()
	if !g.ensurePlanes(r.w, r.h) {
		return
	}
	r.modelStats.GlowQuads += g.quads

	// 1. The emission plane: every source quad, added together.
	g.source.Clear()
	r.beginPass(g.source)
	fill := r.placeholderImage()
	for i := range g.runs {
		run := &g.runs[i]
		if run.iLen == 0 {
			continue
		}
		for j := 0; j < 4; j++ {
			img := run.imgs[j]
			if img == nil {
				img = fill
			}
			g.shaderOpts.Images[j] = img
		}
		g.shaderOpts.Blend = ebiten.BlendLighter
		r.recordSubmission(int(run.vLen), int(run.iLen))
		g.source.DrawTrianglesShader32(
			g.verts[run.vOff:run.vOff+run.vLen],
			g.idx[run.iOff:run.iOff+run.iLen],
			g.sourceShader, &g.shaderOpts)
		r.frameDraws++
	}

	// 2. Shrink to the two octaves and blur each separably. The tap spacing is
	// what carries the halo's world-pixel size onto the octaves: the near
	// octave's kernel has to reach glowNearSigmaWorld world pixels, which is
	// that many framebuffer pixels times the view scale, and one near texel is
	// glowOctaveNear framebuffer pixels. The far octave takes the same spacing
	// on a texel twice as wide, so it stays twice the near halo as it was. At
	// the native view scale the spacing is exactly one texel, which is the
	// kernel the layer was tuned with.
	step := glowBlurStep(g.viewScale)
	r.glowShrink(g.half, g.source)
	r.glowShrink(g.quarter, g.half)
	r.glowBlur(g.quarterB, g.quarter, step, 0)
	r.glowBlur(g.quarter, g.quarterB, 0, step)
	r.glowShrink(g.eighth, g.quarter)
	r.glowBlur(g.eighthB, g.eighth, step, 0)
	r.glowBlur(g.eighth, g.eighthB, 0, step)

	// 3. Add the octaves back, magnified with linear filtering so the blur's
	// texels do not show as blocks.
	near, far := g.octaveWeights()
	r.glowAdd(r.surfaces[0], g.quarter, glowOctaveNear, near)
	r.glowAdd(r.surfaces[0], g.eighth, glowOctaveFar, far)
	r.modelStats.GlowPasses += 9
	g.runs = g.runs[:0]
	g.verts = g.verts[:0]
	g.idx = g.idx[:0]
	g.quads = 0
}

// glowBlurStep is the separable blur's tap spacing, in texels of the octave
// being blurred, for a frame drawn at viewScale screen pixels per world pixel.
// It is the one place the halo's world-pixel size becomes screen pixels:
// glowSigma taps of spacing t cover t × glowOctaveNear × glowSigma framebuffer
// pixels of the near octave, and that has to be glowNearSigmaWorld × viewScale.
// A zero or negative scale is the native view.
//
// Fetches are nearest, so a spacing below one texel folds taps onto the same
// texel — the kernel narrows toward the octave's own resolution rather than
// aliasing, which is the right failure at a zoomed-out view where the halo is
// already finer than the octave can hold.
func glowBlurStep(viewScale float32) float32 {
	if viewScale <= 0 {
		viewScale = 1
	}
	return viewScale * glowNearSigmaWorld / (glowSigma * glowOctaveNear)
}

// glowShrink halves src into dst with linear filtering: each destination
// texel is the mean of the four source texels under it.
func (r *Renderer) glowShrink(dst, src *ebiten.Image) {
	op := &r.glow.imageOpts
	op.GeoM.Reset()
	op.GeoM.Scale(0.5, 0.5)
	op.ColorScale.Reset()
	op.Filter = ebiten.FilterLinear
	op.Blend = ebiten.BlendCopy
	r.beginPass(dst)
	dst.Clear()
	dst.DrawImage(src, op)
	r.frameDraws++
}

// glowBlur draws src into dst through the separable Gaussian along (stepX,
// stepY), in texels. dst and src are the same size.
func (r *Renderer) glowBlur(dst, src *ebiten.Image, stepX, stepY float32) {
	g := &r.glow
	w, h := float32(dst.Bounds().Dx()), float32(dst.Bounds().Dy())
	corners := [4][2]float32{{0, 0}, {w, 0}, {0, h}, {w, h}}
	for i, c := range corners {
		g.quadVerts[i] = ebiten.Vertex{DstX: c[0], DstY: c[1], SrcX: c[0], SrcY: c[1],
			ColorR: 1, ColorG: 1, ColorB: 1, ColorA: 1, Custom0: stepX, Custom1: stepY}
	}
	g.quadIdx = [6]uint32{0, 1, 2, 1, 2, 3}
	fill := r.placeholderImage()
	g.shaderOpts.Images = [4]*ebiten.Image{src, fill, fill, fill}
	g.shaderOpts.Blend = ebiten.BlendCopy
	r.beginPass(dst)
	r.recordSubmission(4, 6)
	dst.DrawTrianglesShader32(g.quadVerts[:], g.quadIdx[:], g.blurShader, &g.shaderOpts)
	r.frameDraws++
}

// blendScreen is the composite's blend: out = src + dst × (1 − src), which adds
// the glow where the composite is dark and leaves an already-white pixel
// white, so a fireball's core keeps its own art instead of clipping.
var blendScreen = ebiten.Blend{
	BlendFactorSourceRGB:        ebiten.BlendFactorOne,
	BlendFactorSourceAlpha:      ebiten.BlendFactorZero,
	BlendFactorDestinationRGB:   ebiten.BlendFactorOneMinusSourceColor,
	BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
	BlendOperationRGB:           ebiten.BlendOperationAdd,
	BlendOperationAlpha:         ebiten.BlendOperationAdd,
}

// glowAdd adds src, magnified by scale with linear filtering and weighted by
// weight, onto dst.
func (r *Renderer) glowAdd(dst, src *ebiten.Image, scale float64, weight float32) {
	op := &r.glow.imageOpts
	op.GeoM.Reset()
	op.GeoM.Scale(scale, scale)
	op.ColorScale.Reset()
	op.ColorScale.Scale(weight, weight, weight, 1)
	op.Filter = ebiten.FilterLinear
	op.Blend = blendScreen
	r.beginPass(dst)
	dst.DrawImage(src, op)
	r.frameDraws++
}

// glowSourceShaderSource is the emission pass. Source 0 is the scene atlas
// page or the flash atlas, source 1 the table atlas and source 2 the composite
// as replayed so far; the destination is the emission plane, which is the
// composite's size, so the composite texel under a fragment is at the
// fragment's own offset from the destination origin.
func glowSourceShaderSource() string {
	return `//kage:unit pixels

package main

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0

func palAt(idx float) vec3 {
	return imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb
}

// under is the composite colour already composed at this fragment's pixel.
func under(dstPos vec4) vec3 {
	return imageSrc2AtFromSrc0Pos(imageSrc0Origin() + (dstPos.xy - imageDstOrigin())).rgb
}

func emit(c vec3) vec4 {
	return vec4(c, max(c.r, max(c.g, c.b)))
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	op := int(custom.w + 0.5)
	if op == ` + fmt.Sprint(glowOpSolid) + ` {
		return emit(palAt(floor(color.r+0.5)) * color.g)
	}
	if op == ` + fmt.Sprint(glowOpKeyed) + ` {
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		c := palAt(floor(tex.r*255.0 + 0.5))
		bright := max(c.r, max(c.g, c.b))
		return emit(c * smoothstep(color.b, 1.0, bright) * color.g)
	}
	if op == ` + fmt.Sprint(glowOpLight) + ` {
		// The disc atlas texel is the row family's high lane, max(k−1, 0), as a
		// 24-bit fixed-point triple (points.go); zero where the disc is not.
		t := imageSrc0At(srcPos)
		n := floor(t.r*255.0+0.5)*65536.0 + floor(t.g*255.0+0.5)*256.0 + floor(t.b*255.0+0.5)
		return emit(under(dstPos) * (n / 8388608.0) * color.g)
	}
	// glowOpHalo: the flat disc test of destOpHalo, emitting the row's high lane
	// (ColorR) over the composite under it.
	d := floor(custom.xy)
	if d.x*d.x+d.y*d.y > custom.z {
		return vec4(0.0)
	}
	return emit(under(dstPos) * color.r * color.g)
}
`
}

// glowBlurShaderSource is one direction of the separable Gaussian. The step
// vector rides Custom0/1 in texels; taps outside the source read transparent
// black, which is the plane's own border.
func glowBlurShaderSource() string {
	weights := glowWeights()
	src := `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	step := custom.xy
	sum := imageSrc0At(srcPos) * ` + fmt.Sprintf("%.6f", weights[0]) + `
`
	for i := 1; i <= glowTapCount; i++ {
		src += fmt.Sprintf("\tsum += (imageSrc0At(srcPos+step*%d.0) + imageSrc0At(srcPos-step*%d.0)) * %.6f\n", i, i, weights[i])
	}
	src += `	return sum
}
`
	return src
}

// glowWeights is the normalized one-sided Gaussian: index 0 the centre, then
// one weight per tap outward, summing to one over the full kernel.
func glowWeights() [glowTapCount + 1]float64 {
	var w [glowTapCount + 1]float64
	total := 0.0
	for i := 0; i <= glowTapCount; i++ {
		w[i] = math.Exp(-float64(i*i) / (2 * glowSigma * glowSigma))
		if i == 0 {
			total += w[i]
		} else {
			total += 2 * w[i]
		}
	}
	for i := range w {
		w[i] /= total
	}
	return w
}

// glowStrokeCorners is the quad of a stroke from (x0, y0) to (x1, y1) with
// half-width hw: the segment extended by hw at both ends and widened by hw on
// each side, in the scheduler's corner order: the two corners on the side of
// the stroke's left-hand normal first (start, end), then the two on the other
// side. A degenerate stroke runs along +x so it still covers a square of side
// 2·hw.
func glowStrokeCorners(x0, y0, x1, y1, hw float32) (xs, ys [4]float32) {
	dx, dy := x1-x0, y1-y0
	length := float32(math.Sqrt(float64(dx*dx + dy*dy)))
	if length < 1e-3 {
		dx, dy, length = 1, 0, 1
	}
	ux, uy := dx/length, dy/length
	x0, y0 = x0-ux*hw, y0-uy*hw
	x1, y1 = x1+ux*hw, y1+uy*hw
	nx, ny := -uy*hw, ux*hw
	xs = [4]float32{x0 + nx, x1 + nx, x0 - nx, x1 - nx}
	ys = [4]float32{y0 + ny, y1 + ny, y0 - ny, y1 - ny}
	return xs, ys
}
