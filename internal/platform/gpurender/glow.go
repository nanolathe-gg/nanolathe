package gpurender

import (
	"fmt"
	"image"
	"math"
	"strings"

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
// into a full-frame source plane, that plane is shrunk to a quarter of the
// frame and blurred there into a near octave and, shrinking further as it
// blurs, a far octave at an eighth, and the two octaves are added back onto
// the composite: five render passes in all (resolveGlow). Everything after
// that — the fog, the chrome, the cursor — is drawn over the glow as it always
// was, so the glow never reaches the interface and the black fog still hides
// it.
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
	// tight core, the eighth the wide falloff. They were tuned at 0.65 and 0.5.
	// A play-test found the halo washing out a shipyard's hulls; the cause was
	// the nanolathe family, which now has its own lower gain (nano.go), so the
	// weapon and explosion halo keeps about 85 percent of its first energy.
	glowNearWeight = 0.55
	glowFarWeight  = 0.425
	// GlowStrengthDefault and GlowStrengthMax are the strength percentages of
	// SetGlowStrength: 100 is the tuned look above and 200 the most a
	// preference or a content pack may ask for. The percentage scales both
	// octaves' weights, and 0 turns the layer off.
	GlowStrengthDefault = 100
	GlowStrengthMax     = 200
	// glowTapCount is the number of taps on each side of the centre of the near
	// kernel, and glowSigma its standard deviation in steps of the tap spacing
	// the kernel is drawn with.
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
	// the wheel. At the native view scale this is the eight framebuffer pixels
	// the layer was tuned at, so the near octave composes as it did.
	glowNearSigmaWorld = glowSigma * glowOctaveNear
	// glowFarSigma is the far kernel's standard deviation in quarter-plane
	// texels at the native view, 18.6 world pixels. The far octave was once the
	// near octave shrunk by two and blurred again with the near kernel on its
	// wider texel, which took three passes of its own. It is now a Gaussian over
	// the quarter plane, read at every quarter texel and evaluated at the eighth
	// plane's texels, so it shrinks as it blurs and rides the near octave's two
	// blur passes. The value is fitted to the old chain's response to a point
	// source: at the native view its spread (17.0 pixels) agrees within half a
	// percent and no pixel of the response moves by more than one percent of its
	// peak (§19.3).
	glowFarSigma = 4.65
	// glowFarCut is where the far kernel ends, in multiples of its sigma.
	glowFarCut = 2.5
	// glowFarReachMax is the most far-kernel taps on each side of the centre:
	// the bound of the shader's loop, which Kage requires to be constant. Taps
	// sit half a texel off the centre, so the loop reaches glowFarReachMax − ½
	// texels. The widest kernel the camera asks for, at camera.ZoomMax, takes
	// twenty-three.
	glowFarReachMax = 24
	// glowFarSigmaMin and glowFarSigmaMax bound the far kernel: below the minimum
	// the two taps nearest the centre would lose their weight to the cut, and
	// the maximum is the widest kernel the loop holds.
	glowFarSigmaMin = 0.25
	glowFarSigmaMax = glowFarReachMax / glowFarCut
	// glowRunVertexLimit bounds one device draw, as the scheduler's runs are.
	glowRunVertexLimit = schedRunVertexLimit
)

// The blur shader's op selector, in Custom3: which octave a quad blurs.
const (
	glowBlurNear = 0
	glowBlurFar  = 1
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

// glowFamily names one family of light a content pack can scale on its own
// (docs/DESIGN_GPU_RENDERER.md §19.4). The families are presentation groupings,
// not retail classes.
type glowFamily uint8

const (
	// glowFamilyWeapons is the glow of beams and lightning, effect, projectile
	// and strip sprites, and explosion flash discs and halos.
	glowFamilyWeapons glowFamily = iota
	// glowFamilyNanolathe is the nanolathe spray: its glow quads and the light
	// its clusters cast on models, smoke and terrain (§23.5).
	glowFamilyNanolathe
	// glowFamilyGround is the terrain receiver of every battle light (§31.3):
	// the pools of light on the ground, not the light on models or smoke.
	glowFamilyGround
	glowFamilyCount
)

// glowFamilies holds each family's scale as its difference from 1, so the zero
// value is every family at its default and a renderer that never hears from
// the host draws the tuned look.
type glowFamilies struct {
	offset [glowFamilyCount]float32
}

// scale is family k's multiplier: 1 at 100 percent, 0 when it is off.
func (f *glowFamilies) scale(k glowFamily) float32 { return 1 + f.offset[k] }

// SetGlowFamilies sets the three families' strengths as percentages of their
// tuned look, each clamped to 0..GlowStrengthMax (§19.4). A family at 0 is off
// and no other family changes. The weapons and nanolathe glow are further
// scaled by SetGlowStrength, which weights the whole glow layer. The host
// applies the content pack's values before every Execute, beside the glow
// switch and strength.
func (r *Renderer) SetGlowFamilies(weapons, nanolathe, ground int) {
	if r == nil {
		return
	}
	for k, percent := range [glowFamilyCount]int{weapons, nanolathe, ground} {
		r.families.offset[k] = glowStrengthScale(percent) - 1
	}
}

// glowRun is one device draw of the batch: the image bindings it needs and its
// own vertex and index storage, which is retained with the run across frames.
type glowRun struct {
	imgs  [4]*ebiten.Image
	verts []ebiten.Vertex
	idx   []uint32
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

	// runs are the batch's device draws. The emission plane is a saturating
	// sum, so a quad may join ANY run that can bind what it reads, not only the
	// last one opened (see selectRun). last is the run the most recent quad
	// joined.
	runs  []glowRun
	last  int
	quads int

	// The planes, one per resolve pass (glowRegions has their layout). source
	// is the full-frame emission plane and quarter its 4×4 shrink. across holds
	// both octaves blurred along x: the near octave at quarter resolution, the
	// far octave already shrunk to the eighth plane's columns. octaves holds
	// both blurred along y as well, the far octave shrunk to the eighth plane's
	// rows, inside a border of transparent texels. Each resolve pass draws into
	// one of these four or into the composite, so each is one render pass. All
	// four are unmanaged, so no atlas placement can put two of them on one
	// texture or insert a copy between the passes (§13.12 "Page planes are
	// unmanaged").
	source, quarter, across, octaves *ebiten.Image
	w, h                             int
	regions                          glowRegions

	sourceShader, shrinkShader, blurShader, compositeShader *ebiten.Shader
	shaderErr                                               error
	compiled                                                bool

	shaderOpts ebiten.DrawTrianglesShaderOptions
	// resolveVerts and resolveIdx are the resolve passes' quads: at most two a
	// pass, one per octave.
	resolveVerts [8]ebiten.Vertex
	resolveIdx   [12]uint32
}

// glowRegions is the resolve planes' layout for one frame size. The near
// octave is qw×qh texels, a quarter of the frame, and the far octave ew×eh, an
// eighth, each rounding up.
//
//   - quarter is exactly the near octave's qw×qh.
//   - across is (qw+ew)×qh: the near octave at the origin and the far
//     octave's eighth-resolution columns at (qw, 0), still at quarter rows.
//   - octaves is (qw+ew+3)×(qh+2): the near octave at (1, 1) and the far
//     octave at (qw+2, 1), each inside a border of transparent texels, which
//     is where the composite's linear taps land when they step off an octave.
type glowRegions struct {
	qw, qh, ew, eh int
}

// glowRegionsFor lays out the planes of a w×h frame.
func glowRegionsFor(w, h int) glowRegions {
	return glowRegions{qw: (w + 3) / 4, qh: (h + 3) / 4, ew: (w + 7) / 8, eh: (h + 7) / 8}
}

// farX is the far octave's first column in the octave plane.
func (g glowRegions) farX() int { return g.qw + 2 }

// octaveSize is the octave plane's size.
func (g glowRegions) octaveSize() (w, h int) { return g.farX() + g.ew + 1, g.qh + 2 }

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
	g.dropRuns()
}

// dropRuns empties the batch, keeping every run's storage for the next frame.
func (g *glowLayer) dropRuns() {
	for i := range g.runs {
		g.runs[i].verts, g.runs[i].idx = g.runs[i].verts[:0], g.runs[i].idx[:0]
	}
	g.runs = g.runs[:0]
	g.last = 0
	g.quads = 0
}

// selectRun returns a run that can bind imgs and has room for one more quad,
// opening one when none can.
//
// Every source quad is added onto the emission plane with a saturating add
// (BlendLighter into an 8-bit plane), and each fragment's contribution is
// quantized on its own, so the plane is the same whatever order the quads are
// drawn in: draw order is not a constraint and a quad may join any run. Callers
// bind exactly the slots their op reads and leave the rest nil (a stroke reads
// only the palette, a halo the palette and the composite, a sprite its atlas
// page and the palette), so a nil slot is one nothing in the quad reads: a
// quad joins a run whose bound slots agree with its own non-nil ones, and the
// run adopts whatever slots it newly needs. A frame's strokes, halos, flash
// discs and sprites then leave in a draw per distinct texture rather than one
// per change of source kind in record order (§19).
func (g *glowLayer) selectRun(imgs [4]*ebiten.Image) *glowRun {
	for i := range g.runs {
		run := &g.runs[i]
		if len(run.verts)+quadVertices > glowRunVertexLimit {
			continue
		}
		ok := true
		for j := range imgs {
			if imgs[j] != nil && run.imgs[j] != nil && imgs[j] != run.imgs[j] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for j := range imgs {
			if imgs[j] != nil {
				run.imgs[j] = imgs[j]
			}
		}
		g.last = i
		return run
	}
	n := len(g.runs)
	if n < cap(g.runs) {
		g.runs = g.runs[:n+1]
		g.runs[n].imgs = imgs
	} else {
		g.runs = append(g.runs, glowRun{imgs: imgs})
	}
	g.last = n
	return &g.runs[n]
}

// quad appends one quad with explicit corners to the batch, in the scheduler's
// vertex order. Corner positions are already in screen pixels.
func (g *glowLayer) quad(imgs [4]*ebiten.Image, xs, ys, sxs, sys [4]float32, col [4]float32, custom [4][4]float32) {
	run := g.selectRun(imgs)
	// Indices are relative to the run's own vertex storage, which is the slice
	// its draw hands the device.
	base := uint32(len(run.verts))
	for i := 0; i < 4; i++ {
		run.verts = append(run.verts, ebiten.Vertex{
			DstX: xs[i], DstY: ys[i], SrcX: sxs[i], SrcY: sys[i],
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[i][0], Custom1: custom[i][1], Custom2: custom[i][2], Custom3: custom[i][3],
		})
	}
	run.idx = append(run.idx, base, base+1, base+2, base+1, base+2, base+3)
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
	weapons := r.families.scale(glowFamilyWeapons)
	if weapons <= 0 || !r.glowActive() {
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
		[4]float32{float32(l.Index), glowGain * weapons, 0, 0},
		[4][4]float32{custom, custom, custom, custom})
}

// glowSprite appends a keyed GAF sprite whose top-left is (x, y) in record
// space, over the same clip-intersected rectangle the sprite itself covered.
// gain is the emission scale: 1 for an opaque sprite, ½ for a tinted one.
func (r *Renderer) glowSprite(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int, gain float32) {
	weapons := r.families.scale(glowFamilyWeapons)
	if f == nil || weapons <= 0 || !r.glowActive() {
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
		[4]float32{0, gain * glowSpriteGain * glowGain * weapons, glowThreshold, 0}, [4]float32{0, 0, 0, glowOpKeyed})
}

// glowFlash appends one explosion disc: the same magnified atlas quad Flash
// compiled, reading the composite under it. The rectangle and source span are
// the ones Flash computed, in record space.
func (r *Renderer) glowFlash(cx0, cy0, cx1, cy1 int, sx0, sy0, sx1, sy1 float32) {
	weapons := r.families.scale(glowFamilyWeapons)
	if weapons <= 0 || !r.glowActive() || r.sched.flash.img == nil {
		return
	}
	r.noteGlowViewScale()
	s := &r.sched
	r.glow.rect([4]*ebiten.Image{0: s.flash.img, 1: r.tables.atlas, 2: r.surfaces[0]},
		s.txx(float32(cx0)), s.txy(float32(cy0)), s.txx(float32(cx1)), s.txy(float32(cy1)),
		sx0, sy0, sx1, sy1,
		[4]float32{0, glowLightGain * glowGain * weapons, 0, 0}, [4]float32{0, 0, 0, glowOpLight})
}

// glowHalo appends one flat ground halo: the disc test of destOpHalo over the
// composite under it, emitting the row's high lane. Arguments are the ones
// Halo computed, in record space.
func (r *Renderer) glowHalo(cx0, cy0, cx1, cy1 int, high, lx0, ly0, lx1, ly1, r2 float32) {
	weapons := r.families.scale(glowFamilyWeapons)
	if weapons <= 0 || !r.glowActive() || high <= 0 {
		return
	}
	r.noteGlowViewScale()
	s := &r.sched
	r.glow.quad([4]*ebiten.Image{1: r.tables.atlas, 2: r.surfaces[0]},
		[4]float32{s.txx(float32(cx0)), s.txx(float32(cx1)), s.txx(float32(cx0)), s.txx(float32(cx1))},
		[4]float32{s.txy(float32(cy0)), s.txy(float32(cy0)), s.txy(float32(cy1)), s.txy(float32(cy1))},
		[4]float32{}, [4]float32{},
		[4]float32{high, glowLightGain * glowGain * weapons, 0, 0},
		[4][4]float32{
			{lx0, ly0, r2, glowOpHalo},
			{lx1, ly0, r2, glowOpHalo},
			{lx0, ly1, r2, glowOpHalo},
			{lx1, ly1, r2, glowOpHalo},
		})
}

// ensurePlanes sizes the planes to the frame (glowRegions).
func (g *glowLayer) ensurePlanes(w, h int) bool {
	if w <= 0 || h <= 0 {
		return false
	}
	if g.source != nil && g.w == w && g.h == h {
		return true
	}
	for _, img := range []*ebiten.Image{g.source, g.quarter, g.across, g.octaves} {
		if img != nil {
			img.Deallocate()
		}
	}
	g.w, g.h = w, h
	q := glowRegionsFor(w, h)
	g.regions = q
	ow, oh := q.octaveSize()
	unmanaged := &ebiten.NewImageOptions{Unmanaged: true}
	g.source = ebiten.NewImageWithOptions(image.Rect(0, 0, w, h), unmanaged)
	g.quarter = ebiten.NewImageWithOptions(image.Rect(0, 0, q.qw, q.qh), unmanaged)
	g.across = ebiten.NewImageWithOptions(image.Rect(0, 0, q.qw+q.ew, q.qh), unmanaged)
	g.octaves = ebiten.NewImageWithOptions(image.Rect(0, 0, ow, oh), unmanaged)
	return true
}

// resolveGlow draws the batch, blurs it and adds it onto the composite
// (§19.3). It is called before the fog composite compiles and when the world
// region closes, and runs at most once per frame. The scheduler is submitted
// first, so the composite the light sources read and the surface the glow is
// added to are the frame as replayed so far.
//
// It is five render passes, each drawing into one image: the emission plane,
// its quarter shrink, both octaves blurred along x, both blurred along y, and
// the composite. Every pass after the emission serves both octaves at once —
// the far octave shrinks to the eighth plane inside the two blur passes rather
// than in passes of its own — because a render pass, not its fill, is the
// unit the Metal driver stalls on (§11.5, §22.1). Blurring both axes in one
// pass would save one more, but it takes 81 taps a quarter texel against the
// separable pair's 18, and a four-pass prototype built that way cost 3.3 times
// the old resolve's GPU time on the device, so the blur stays separable
// (§19.3).
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
		g.shaderErr = g.compileShaders()
	}
	if g.shaderErr != nil {
		return
	}
	r.submitSchedule()
	if !g.ensurePlanes(r.w, r.h) {
		return
	}
	r.modelStats.GlowQuads += g.quads
	passes := r.modelStats.Passes

	// 1. The emission plane: every source quad, added together.
	g.source.Clear()
	r.beginPass(g.source)
	fill := r.placeholderImage()
	for i := range g.runs {
		run := &g.runs[i]
		if len(run.idx) == 0 {
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
		r.recordSubmission(len(run.verts), len(run.idx))
		g.source.DrawTrianglesShader32(deviceVertexSpan(run.verts, 0, len(run.verts)), run.idx, g.sourceShader, &g.shaderOpts)
		r.frameDraws++
	}

	// 2. Shrink the emission plane to the quarter plane.
	// 3–4. Blur both octaves along x, then along y. The view scale reaches the
	// halo here and only here, through the near kernel's tap spacing and the
	// far kernel's width (glowBlurStep, glowFarKernelSigma).
	// 5. Add both octaves onto the composite in one draw.
	step, farSigma := glowBlurStep(g.viewScale), glowFarKernelSigma(g.viewScale)
	r.glowShrink()
	r.glowBlurAcross(step, farSigma)
	r.glowBlurDown(step, farSigma)
	near, far := g.octaveWeights()
	r.glowComposite(near, far)
	r.modelStats.GlowPasses += r.modelStats.Passes - passes
	g.dropRuns()
}

// compileShaders compiles the four resolve shaders, once.
func (g *glowLayer) compileShaders() error {
	for _, s := range []struct {
		dst *(*ebiten.Shader)
		src string
	}{
		{&g.sourceShader, glowSourceShaderSource()},
		{&g.shrinkShader, glowShrinkShaderSource()},
		{&g.blurShader, glowBlurShaderSource()},
		{&g.compositeShader, glowCompositeShaderSource()},
	} {
		shader, err := ebiten.NewShader([]byte(s.src))
		if err != nil {
			return err
		}
		*s.dst = shader
	}
	return nil
}

// glowBlurStep is the near kernel's tap spacing, in texels of the quarter
// plane, for a frame drawn at viewScale screen pixels per world pixel. It is
// the one place the halo's world-pixel size becomes screen pixels:
// glowSigma taps of spacing t cover t × glowOctaveNear × glowSigma framebuffer
// pixels of the near octave, and that has to be glowNearSigmaWorld × viewScale.
// The far kernel's width scales by the same factor (glowFarKernelSigma). A
// zero or negative scale is the native view.
//
// Near fetches are nearest, so a spacing below one texel folds taps onto the
// same texel — the kernel narrows toward the octave's own resolution rather
// than aliasing, which is the right failure at a zoomed-out view where the
// halo is already finer than the octave can hold.
func glowBlurStep(viewScale float32) float32 {
	if viewScale <= 0 {
		viewScale = 1
	}
	return viewScale * glowNearSigmaWorld / (glowSigma * glowOctaveNear)
}

// glowFarKernelSigma is the far kernel's standard deviation, in texels of the
// quarter plane, at viewScale: glowFarSigma at the native view, scaled with
// the near kernel's spacing so the far halo keeps its world-pixel size too.
// It is held between glowFarSigmaMin and glowFarSigmaMax, the bounds of the
// shader's loop, and no view scale the camera allows reaches either.
func glowFarKernelSigma(viewScale float32) float32 {
	return min(max(glowFarSigma*glowBlurStep(viewScale), glowFarSigmaMin), glowFarSigmaMax)
}

// glowQuad fills quad i of the resolve's geometry: the destination rectangle
// (x0, y0)–(x1, y1), whose top-left corner samples the source at (sx, sy) and
// which advances sx1 source texels along x, and sy1 along y, per destination
// pixel, carrying col and custom unchanged to every fragment.
func (g *glowLayer) glowQuad(i int, x0, y0, x1, y1, sx, sy, sx1, sy1 float32, col, custom [4]float32) {
	xs := [4]float32{x0, x1, x0, x1}
	ys := [4]float32{y0, y0, y1, y1}
	for c := 0; c < 4; c++ {
		g.resolveVerts[4*i+c] = ebiten.Vertex{
			DstX: xs[c], DstY: ys[c], SrcX: sx + (xs[c]-x0)*sx1, SrcY: sy + (ys[c]-y0)*sy1,
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3],
		}
	}
	b := uint32(4 * i)
	idx := [6]uint32{b, b + 1, b + 2, b + 1, b + 2, b + 3}
	copy(g.resolveIdx[6*i:6*i+6], idx[:])
}

// glowDraw draws the first quads quads of the resolve's geometry from src into
// dst: one device draw, and one render pass when dst is not the destination
// already open.
func (r *Renderer) glowDraw(dst, src *ebiten.Image, shader *ebiten.Shader, quads int, blend ebiten.Blend) {
	g := &r.glow
	fill := r.placeholderImage()
	g.shaderOpts.Images = [4]*ebiten.Image{src, fill, fill, fill}
	g.shaderOpts.Blend = blend
	r.beginPass(dst)
	r.recordSubmission(4*quads, 6*quads)
	dst.DrawTrianglesShader32(g.resolveVerts[:4*quads], g.resolveIdx[:6*quads], shader, &g.shaderOpts)
	r.frameDraws++
}

// glowShrink draws the quarter plane from the emission plane: each texel the
// mean of the 4×4 block under it, which is the two 2×2 halvings the layer was
// tuned with in one pass. A fragment's source position is its block's centre.
func (r *Renderer) glowShrink() {
	g := &r.glow
	q := g.regions
	g.glowQuad(0, 0, 0, float32(q.qw), float32(q.qh), 0, 0, glowOctaveNear, glowOctaveNear,
		[4]float32{}, [4]float32{})
	r.glowDraw(g.quarter, g.source, g.shrinkShader, 1, ebiten.BlendCopy)
}

// glowBlurAcross blurs the quarter plane along x into the across plane. The
// near octave takes the nine-tap kernel of glowWeights at tap spacing step,
// the kernel the layer was tuned with. The far octave's fragments fall on
// every other quarter column — at the corner between the two quarter texels an
// eighth texel covers — and read every quarter texel within reach through a
// Gaussian of farSigma, so the far octave shrinks to the eighth plane's
// columns as it blurs and needs no plane or pass of its own. Reading every
// texel rather than every step-th keeps a thin source from combing the halo
// when the view scale spreads the taps.
func (r *Renderer) glowBlurAcross(step, farSigma float32) {
	g := &r.glow
	q := g.regions
	qw, qh, ew := float32(q.qw), float32(q.qh), float32(q.ew)
	g.glowQuad(0, 0, 0, qw, qh, 0, 0, 1, 1,
		[4]float32{}, [4]float32{step, 0, 0, glowBlurNear})
	g.glowQuad(1, qw, 0, qw+ew, qh, 0, 0, glowOctaveFar/glowOctaveNear, 1,
		[4]float32{}, [4]float32{1, 0, farSigma, glowBlurFar})
	r.glowDraw(g.across, g.quarter, g.blurShader, 2, ebiten.BlendCopy)
}

// glowBlurDown blurs the across plane along y into the octave plane, the far
// octave shrinking to the eighth plane's rows as it did to its columns. The
// octave plane is cleared first, within the same pass, so the border around
// each octave is transparent.
func (r *Renderer) glowBlurDown(step, farSigma float32) {
	g := &r.glow
	q := g.regions
	qw, qh, ew, eh, fx := float32(q.qw), float32(q.qh), float32(q.ew), float32(q.eh), float32(q.farX())
	g.glowQuad(0, 1, 1, 1+qw, 1+qh, 0, 0, 1, 1,
		[4]float32{}, [4]float32{0, step, 0, glowBlurNear})
	g.glowQuad(1, fx, 1, fx+ew, 1+eh, qw, 0, 1, glowOctaveFar/glowOctaveNear,
		[4]float32{}, [4]float32{0, 1, farSigma, glowBlurFar})
	g.octaves.Clear()
	r.glowDraw(g.octaves, g.across, g.blurShader, 2, ebiten.BlendCopy)
}

// glowComposite adds both blurred octaves, magnified with linear filtering and
// weighted by near and far, onto the composite in one draw. A screen blend
// composes associatively — 1 − out = (1 − dst)(1 − n)(1 − f) — so screening
// the two octaves together in the shader and once onto the composite is the
// two screen blends the layer used to draw, without the rounding between them.
// A fragment's source position is its own position in near-octave texels.
func (r *Renderer) glowComposite(near, far float32) {
	g := &r.glow
	w, h := float32(r.w), float32(r.h)
	g.glowQuad(0, 0, 0, w, h, 0, 0, 1/glowOctaveNear, 1/glowOctaveNear,
		[4]float32{near, far, 0, 0}, [4]float32{float32(g.regions.farX()), 0, 0, 0})
	r.glowDraw(r.surfaces[0], g.octaves, g.compositeShader, 1, blendScreen)
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

// glowShrinkShaderSource draws the quarter plane: the mean of the block of
// emission texels around the fragment's source position, which is its
// block's centre. Taps outside the emission plane read transparent black, the
// frame's own border.
func glowShrinkShaderSource() string {
	n := int(glowOctaveNear)
	var b strings.Builder
	b.WriteString(`//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	sum := vec4(0.0)
`)
	half := float64(n-1) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			fmt.Fprintf(&b, "\tsum += imageSrc0At(srcPos + vec2(%.1f, %.1f))\n", float64(x)-half, float64(y)-half)
		}
	}
	fmt.Fprintf(&b, "\treturn sum / %d.0\n}\n", n*n)
	return b.String()
}

// glowBlurShaderSource is one direction of both octaves' blur. Custom3
// selects the octave. The near octave's step vector rides Custom0/1 in texels,
// as the layer's separable blur always took it. The far octave's unit
// direction rides Custom0/1 and its sigma Custom2; its fragment's source
// position is the corner the eighth texel's centre falls on. Taps outside the
// source read transparent black, the plane's own border: each octave's taps
// run along its own row or column of the source, so they meet no other
// octave's texels.
func glowBlurShaderSource() string {
	weights := glowWeights()
	var b strings.Builder
	fmt.Fprintf(&b, `//kage:unit pixels

package main

// farCut is where the far kernel ends, in multiples of its sigma.
const farCut = %.4f

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	if custom.w < %d.5 {
		step := custom.xy
		sum := imageSrc0At(srcPos) * %.6f
`, glowFarCut, glowBlurNear, weights[0])
	for i := 1; i <= glowTapCount; i++ {
		fmt.Fprintf(&b, "\t\tsum += (imageSrc0At(srcPos+step*%d.0) + imageSrc0At(srcPos-step*%d.0)) * %.6f\n", i, i, weights[i])
	}
	fmt.Fprintf(&b, `		return sum
	}
	// The far octave: a Gaussian read at every texel within farCut sigmas, the
	// taps half a texel off the corner. The Gaussian's value at the cut is
	// subtracted, so the weights reach zero there and a kernel whose sigma
	// eases with the zoom changes smoothly rather than gaining taps at once.
	dir := custom.xy
	k := 0.5 / (custom.z * custom.z)
	reach := farCut * custom.z
	tail := exp(-0.5 * farCut * farCut)
	sum := vec4(0.0)
	norm := 0.0
	for i := 0; i < %d; i++ {
		d := float(i) - %.1f
		if abs(d) > reach {
			continue
		}
		w := max(exp(-d*d*k)-tail, 0.0)
		norm += w
		sum += imageSrc0At(srcPos+dir*d) * w
	}
	return sum / norm
}
`, 2*glowFarReachMax, float64(glowFarReachMax)-0.5)
	return b.String()
}

// glowCompositeShaderSource adds both blurred octaves onto the composite. The
// octave plane is source 0, the colour lanes carry the near and far weights
// and Custom0 the far octave's first column. Each octave is magnified with
// linear filtering, weighted and clamped as the premultiplied colour a
// weighted draw hands the blend, and the two are screened together; the
// pass's blendScreen screens the result onto the composite.
func glowCompositeShaderSource() string {
	return fmt.Sprintf(`//kage:unit pixels

package main

// texel is the octave plane's texel centred at c, relative to its origin. The
// octaves sit inside a border of transparent texels, so the linear taps below
// never leave the plane and read no other octave.
func texel(c vec2) vec4 {
	return imageSrc0UnsafeAt(imageSrc0Origin() + c)
}

// bilinear samples the octave plane at p with linear filtering, texel centres
// at half-integers, as a magnified DrawImage samples its source.
func bilinear(p vec2) vec4 {
	q := p - 0.5
	f := fract(q)
	c := floor(q) + 0.5
	top := mix(texel(c), texel(c+vec2(1.0, 0.0)), f.x)
	bottom := mix(texel(c+vec2(0.0, 1.0)), texel(c+vec2(1.0, 1.0)), f.x)
	return mix(top, bottom, f.y)
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	// p is the fragment in near-octave texels; a far texel is %[1]s of them.
	p := srcPos - imageSrc0Origin()
	n := clamp(bilinear(p+1.0).rgb*color.r, 0.0, 1.0)
	f := clamp(bilinear(p/%[1]s+vec2(custom.x, 1.0)).rgb*color.g, 0.0, 1.0)
	return vec4(n+f-n*f, 1.0)
}
`, fmt.Sprintf("%.1f", glowOctaveFar/glowOctaveNear))
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
