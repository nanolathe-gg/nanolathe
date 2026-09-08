package gpurender

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Renderer is the modern (GPU) executor (docs/DESIGN_GPU_RENDERER.md §2.3,
// §11.2). It owns the palette table atlas, the scene atlas, the indexed
// offscreen the frame composes into (index in the red channel), the expanded RGBA
// surface presented at the end, and the compiled passes. It implements
// drawlist.Sink, so Execute replays a recorded List straight through it. Every
// device resource lives here, never on the client [I6].
//
// The Sink methods compile rather than draw: each appends its command's clipped
// rectangle, class and vertices to the phase scheduler (schedule.go), and Execute
// submits the phases after Replay returns. A barrier — the fog pass, a
// carrier/child group, a model subject the slot atlas could not fit, the clear
// and the expansion — submits everything pending first, so those keep their
// places in record order (C-G3).
type Renderer struct {
	modelPrep modelPrepScratch
	tables    tables
	expand    *ebiten.Shader
	// scene2D is the one opaque pass and sceneDest the one destination-reading
	// pass (§11.2 "One scene shader for the 2D families").
	scene2D   *ebiten.Shader
	sceneDest *ebiten.Shader
	// The model slot atlas rasterization passes. modelCommit and
	// modelShadowCommit are compiled because the slot allocator gates a
	// subject's shadow and body on them; the commits themselves ride the scene
	// and destination shaders above.
	modelKey          *ebiten.Shader
	modelBody         *ebiten.Shader
	modelCommit       *ebiten.Shader
	modelShadowCommit *ebiten.Shader
	modelClip         *ebiten.Shader
	modelReveal       *ebiten.Shader
	modelCopy         *ebiten.Shader
	modelChild        *ebiten.Shader
	modelResolve      *ebiten.Shader

	// offscreen is the indexed frame surface, RGBA8 with the palette index in the
	// red channel (C-G4). output is the expanded RGBA surface Execute returns.
	offscreen *ebiten.Image
	output    *ebiten.Image
	// destScratch is the phase snapshot: before a phase's destination-reading
	// batch writes the offscreen, the union rectangle of that batch is copied
	// here, and the pass reads it while writing the offscreen — so a
	// destination-reading write never samples a pixel the same batch changed
	// (§11.2 "The scheduler", C-G7). It is full-surface and aligned 1:1 with the
	// offscreen, recreated when the frame size changes.
	destScratch *ebiten.Image
	// placeholder backs an image slot no op in a run requested, for the case
	// where no palette (and so no table atlas) has been installed.
	placeholder *ebiten.Image
	// modelAtlas is the per-frame slot atlas every model subject rasterizes
	// into before any of them commits (docs/DESIGN_GPU_RENDERER.md §11.2).
	// modelStage/modelStageScratch are the attached-unit group's staging pair,
	// grown to the largest group seen and reused. modelOpts and modelStageOp
	// are reused draw options, so a steady-state frame's model draws allocate
	// no options value and no uniform map.
	modelAtlas                    modelSlotAtlas
	modelPageLimit                int
	modelStage, modelStageScratch *ebiten.Image
	modelStageW, modelStageH      int
	modelOpts                     ebiten.DrawTrianglesShaderOptions
	modelStageOp                  ebiten.DrawImageOptions
	textureAtlas                  modelTextureAtlas
	w, h                          int

	// tileAtlases caches one tile-index atlas per *world.Terrain identity, built
	// on first Terrain draw and reused for the map's lifetime (C-G4,
	// docs/DESIGN_GPU_RENDERER.md §2.3). Keyed by pointer, never ranged in a way
	// that reaches output, so it introduces no ordering [I1].
	tileAtlases map[*world.Terrain]*tileAtlas

	// gafImages caches one index texture per *formats.GAFFrame identity for the
	// model material passes, which sample a frame directly rather than through
	// the scene atlas (C-G4). The 2D families read the scene atlas instead. Keyed
	// by pointer and never ranged in a way that reaches output [I1].
	gafImages map[*formats.GAFFrame]*ebiten.Image

	// scene is the packed source atlas the scene shader samples: GAF frames, FNT
	// glyph strips, PCX backgrounds and the per-frame indexed surface, so a whole
	// phase's opaque commands can share one device draw (§11.2).
	scene sceneAtlas

	// sceneOpts is the reused draw options value every batched submission fills,
	// so a steady-state frame allocates no options and no uniform map
	// (§11.2 "Allocation policy").
	sceneOpts ebiten.DrawTrianglesShaderOptions
	// snapshotOpt is the reused options value of the phase snapshot copy.
	snapshotOpt ebiten.DrawImageOptions

	// surfaceDynamic serves zero-identity commands. surfaceCache is a bounded
	// presentation cache for durable Surface identities; replay order never
	// depends on its lookup or eviction order [I1].
	surfaceDynamic surfaceUpload
	surfaceCache   [4]surfaceUpload
	surfaceClock   uint64
	surfaceWrites  uint64 // focused device-fixture diagnostic; never output state

	// sched is the compiled frame: phases, runs and reusable vertex/index
	// scratch (schedule.go).
	sched scheduler

	// frameDraws counts device draws issued since the last Execute began. It is
	// diagnostic only.
	frameDraws int

	// verts and idx are the geometry scratch the fog pass, the expansion and the
	// model rasterization passes share; the batched 2D families use the
	// scheduler's own buffers instead.
	verts []ebiten.Vertex
	idx   []uint16

	modelStats ModelStats

	// fog is the fog pass state of docs/DESIGN_GPU_RENDERER.md §11.2, owned by
	// fog.go so the fog unit and the model unit never edit the same file.
	fog fogPass
}

// surfaceUpload is one indexed-surface upload slot: the scene atlas region its
// bytes live in, and the identity/revision that decides whether they have to be
// written again.
type surfaceUpload struct {
	identity uint64
	revision uint64
	used     uint64
	entry    sceneEntry
	w, h     int
	pixels   []byte
	// sent is the source bytes last uploaded. A surface whose revision changed
	// but whose bytes did not is not re-uploaded, because Ebitengine's Metal
	// driver builds a staging texture per WritePixels
	// (docs/DESIGN_GPU_RENDERER.md §11.2 "Allocation policy").
	sent []byte
}

// New builds a renderer from the installed palette tables, uploading every table
// texture once (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4, C-G8). w and h are the
// initial frame size; pass 0 for either to defer surface allocation until the
// first Execute (the size is re-checked every Execute regardless).
//
// The passes are compiled here. If one fails to compile the renderer is still
// returned with a nil shader; the families guarding on it then no-op rather than
// panicking, and the compile error is reported by NewChecked for callers that
// want it.
func New(pal *palette.Tables, w, h int) *Renderer {
	r, _ := NewChecked(pal, w, h)
	return r
}

// NewChecked is New but also returns the first shader compilation error. Each
// drawing family guards its own resources; callers report the initialization
// error before attempting a frame.
func NewChecked(pal *palette.Tables, w, h int) (*Renderer, error) {
	r := &Renderer{
		tables:      uploadTables(pal),
		tileAtlases: make(map[*world.Terrain]*tileAtlas),
		gafImages:   make(map[*formats.GAFFrame]*ebiten.Image),
	}
	r.scene.frames = make(map[*formats.GAFFrame]sceneEntry)
	r.scene.pcx = make(map[*formats.PCX]sceneEntry)
	r.scene.fonts = make(map[*formats.FNT]*fntAtlas)

	// Report the first compile error so a caller that wants it (NewChecked) can
	// surface it; each drawing family guards on its own nil shader and no-ops
	// rather than panicking, exactly as Expand does.
	var firstErr error
	compile := func(dst **ebiten.Shader, build func() (*ebiten.Shader, error)) {
		s, err := build()
		*dst = s
		if firstErr == nil {
			firstErr = err
		}
	}
	compile(&r.expand, newExpandShader)
	compile(&r.scene2D, newScene2DShader)
	compile(&r.sceneDest, newSceneDestShader)
	compile(&r.modelKey, newModelKeyShader)
	compile(&r.modelBody, newModelBodyShader)
	compile(&r.modelCommit, newModelCommitShader)
	compile(&r.modelShadowCommit, newModelShadowCommitShader)
	compile(&r.modelClip, newModelClipShader)
	compile(&r.modelReveal, newModelRevealShader)
	compile(&r.modelCopy, newModelCopyShader)
	compile(&r.modelChild, newModelChildShader)
	compile(&r.modelResolve, newModelResolveShader)

	if w > 0 && h > 0 {
		r.ensureSize(w, h)
	}
	return r, firstErr
}

// ensureSize allocates or reallocates the indexed offscreen and the expanded
// output when the frame size changes.
func (r *Renderer) ensureSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	if r.offscreen != nil && r.w == w && r.h == h {
		return
	}
	r.offscreen = ebiten.NewImage(w, h)
	r.output = ebiten.NewImage(w, h)
	r.destScratch = ebiten.NewImage(w, h)
	r.w, r.h = w, h
	r.sched.resetFrame(w, h)
}

// Execute replays the recorded frame list through this renderer and returns the
// expanded RGBA surface for the adapter to present (docs/DESIGN_GPU_RENDERER.md
// §2.3). The list is visited in exact record order (C-G3); the Sink methods
// compile the commands into phases and this submits them (§11.2), ending with the
// palette expansion after every indexed composite.
//
// It returns nil when the surface cannot be sized (a degenerate w/h) or when the
// list is nil, so the adapter can fall back to leaving the screen untouched.
func (r *Renderer) Execute(list *drawlist.List, w, h int) *ebiten.Image {
	if r == nil || list == nil {
		return nil
	}
	r.ensureSize(w, h)
	if r.offscreen == nil {
		return nil
	}
	r.modelStats = ModelStats{UnsupportedFace: -1}
	r.frameDraws = 0
	r.sched.resetFrame(r.w, r.h)
	r.modelPrep.reset()
	defer r.modelPrep.reset()
	// Every eligible subject of the frame is rasterized into the slot atlas
	// before Replay commits any of them, so the model stages cost a fixed
	// number of draws instead of one set per subject (§11.2).
	r.prepareModelSlots(list)
	list.Replay(r)
	// A list without an Expand marker still leaves no compiled work behind.
	r.submitSchedule()
	return r.output
}

// DeviceDraws reports the device draws the most recent Execute issued. It is
// diagnostic only and never reaches simulation state.
func (r *Renderer) DeviceDraws() int {
	if r == nil {
		return 0
	}
	return r.frameDraws
}

// index0Color is the offscreen's cleared value: index 0 in the red channel with
// opaque alpha, so premultiplied sampling recovers red exactly (C-G4).
var index0Color = color.RGBA{R: 0, G: 0, B: 0, A: 255}

// Clear fills the indexed offscreen with palette index 0 — the first command of
// every committed frame (C-G1). Index 0 rides the red channel; alpha is opaque so
// the stored red survives premultiplication and decodes back to 0 (C-G4). It is a
// barrier: anything already compiled is submitted first, so the clear keeps its
// place in record order.
func (r *Renderer) Clear() {
	if r.offscreen == nil {
		return
	}
	r.submitSchedule()
	r.offscreen.Fill(index0Color)
	r.frameDraws++
}

// Expand runs the index→RGBA expansion pass: the indexed offscreen through
// PALETTE.PAL into the output surface, the single colour pass of the composite
// (docs/DESIGN_GPU_RENDERER.md C-G8). It is a barrier — every compiled phase is
// submitted before the surface is read.
//
// The quad maps the output 1:1 to the offscreen, so each output pixel samples its
// own offscreen texel with nearest filtering (C-G4). BlendCopy overwrites the
// output outright — no blend arithmetic on the result (C-G4).
func (r *Renderer) Expand() {
	r.submitSchedule()
	if r.expand == nil || r.offscreen == nil || r.output == nil || r.tables.atlas == nil {
		// Without a compiled shader or an installed palette there is nothing to
		// expand; leave the output as-is rather than guessing a colour (I9).
		return
	}
	fw, fh := float32(r.w), float32(r.h)
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(0, 0, fw, fh, 0, 0, fw, fh)
	r.sceneOpts.Blend = ebiten.BlendCopy
	r.sceneOpts.Images[0] = r.offscreen // the indexed frame, index in red
	r.sceneOpts.Images[1] = r.tables.atlas
	r.sceneOpts.Images[2] = nil
	r.sceneOpts.Images[3] = nil
	r.output.DrawTrianglesShader(r.verts, r.idx, r.expand, &r.sceneOpts)
	r.sceneOpts.Images[1] = nil
	r.frameDraws++
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
}

// The drawing families live beside this file: Terrain in terrain.go, the fills,
// line and point batches in draw.go, the keyed sprite/PCX/surface/cursor blits in
// sprites.go, the destination-reading blits and rects in deststage.go, the FNT
// text run in text.go, the fog composite in fog.go and the model commits in
// models.go. Every one of them compiles into the scheduler rather than drawing
// (§11.2).

// staticSinkCheck fails to compile if *Renderer stops satisfying drawlist.Sink.
var _ drawlist.Sink = (*Renderer)(nil)
