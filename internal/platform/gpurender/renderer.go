package gpurender

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

// Renderer is the modern (GPU) executor (docs/DESIGN_GPU_RENDERER.md §2.3,
// §11.2, §11.5, §13.3). It owns the palette table atlas, the scene atlas, the
// true-colour composite surface Execute returns, the read surface the fog run
// copies into, and the compiled passes. It implements drawlist.Sink, so Execute
// replays a recorded List straight through it. Every device resource lives here,
// never on the client [I6].
//
// The Sink methods compile rather than draw: each appends its command's clipped
// rectangle, class and vertices to the phase scheduler (schedule.go), and Execute
// submits the phases after Replay returns. A barrier — a carrier/child group, a
// model subject the slot atlas could not fit, the clear and the Expand marker —
// submits everything pending first, so those keep their places in record order
// (C-G3). The fog composite is not one of them: it compiles as an ordinary
// destination command over the visible fog region.
type Renderer struct {
	modelPrep modelPrepScratch
	tables    tables
	// scene2D is the one opaque pass and sceneDest the one destination-compositing
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

	// surfaces[0] is the true-colour composite the whole frame is drawn into and
	// the image Execute returns: every source index is resolved through PAL as it
	// is written, so there is no expansion pass (C-G8 as amended, §13.3).
	// surfaces[1] survives only as the fog run's read copy — the fog composite is
	// the one family that still reads the pixels it rewrites, and its region is
	// copied here just before it draws.
	surfaces [2]*ebiten.Image
	// placeholder backs an image slot no op in a run requested, for the case
	// where no palette (and so no table atlas) has been installed.
	placeholder *ebiten.Image
	// modelAtlas is the per-frame slot atlas every model subject rasterizes
	// into before any of them commits (docs/DESIGN_GPU_RENDERER.md §11.2).
	// modelGroups is the attached-unit staging atlas the frame's groups compose
	// over together (model_stage.go); modelStage/modelStageScratch are the
	// fallback pair a group the atlas could not serve composes over one at a
	// time, grown to the largest such group seen and reused. modelOpts and
	// modelStageOp are reused draw options, so a steady-state frame's model draws
	// allocate no options value and no uniform map.
	modelGroups                   modelStageAtlas
	modelAtlas                    modelSlotAtlas
	modelPageLimit                int
	modelStage, modelStageScratch *ebiten.Image
	modelStageW, modelStageH      int
	modelOpts                     ebiten.DrawTrianglesShaderOptions
	modelStageOp                  ebiten.DrawImageOptions
	textureAtlas                  modelTextureAtlas
	w, h                          int

	// tileAtlases caches one tile-index atlas per (tile set identity, detail tile
	// set identity, view scale), built on first Terrain draw at that scale and
	// reused for the map's lifetime (C-G4, docs/DESIGN_GPU_RENDERER.md §2.3,
	// §14.5). Keyed by pointer and scale, never ranged in a way that reaches
	// output, so it introduces no ordering [I1].
	tileAtlases map[tileAtlasKey]*tileAtlas

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
	// copyVerts and copyIdx are the one quad every forward copy between the two
	// surfaces draws, kept as fixed arrays so a pass allocates no geometry and no
	// sub-image (§11.5).
	copyVerts [4]ebiten.Vertex
	copyIdx   [6]uint32

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

	// frameDraws counts device draws issued since the last Execute began, and
	// lastDest the destination of the most recent device call this package
	// instruments, which is how ModelStats.Passes counts destination switches.
	// Both are diagnostic only.
	frameDraws int
	lastDest   *ebiten.Image

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
		tileAtlases: make(map[tileAtlasKey]*tileAtlas),
		gafImages:   make(map[*formats.GAFFrame]*ebiten.Image),
		copyIdx:     [6]uint32{0, 1, 2, 1, 2, 3},
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

// ensureSize allocates or reallocates the composite and the fog read surface
// when the frame size changes.
func (r *Renderer) ensureSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	if r.surfaces[0] != nil && r.w == w && r.h == h {
		return
	}
	r.surfaces[0] = ebiten.NewImage(w, h)
	r.surfaces[1] = ebiten.NewImage(w, h)
	r.w, r.h = w, h
	r.sched.resetFrame(w, h)
}

// Execute replays the recorded frame list through this renderer and returns the
// composite for the adapter to present (docs/DESIGN_GPU_RENDERER.md §2.3). The
// list is visited in exact record order (C-G3); the Sink methods compile the
// commands into phases and this submits them (§11.2). The composite is already
// colour, so there is no expansion after it (§13.3).
//
// It returns nil when the surface cannot be sized (a degenerate w/h) or when the
// list is nil, so the adapter can fall back to leaving the screen untouched.
func (r *Renderer) Execute(list *drawlist.List, w, h int) *ebiten.Image {
	if r == nil || list == nil {
		return nil
	}
	r.ensureSize(w, h)
	if r.surfaces[0] == nil {
		return nil
	}
	r.modelStats = ModelStats{UnsupportedFace: -1}
	r.frameDraws = 0
	r.lastDest = nil
	r.sched.resetFrame(r.w, r.h)
	r.modelPrep.reset()
	defer r.modelPrep.reset()
	// Every eligible subject of the frame is rasterized into the slot atlas
	// before Replay commits any of them, so the model stages cost a fixed
	// number of draws instead of one set per subject (§11.2). The frame's
	// attached-unit groups then compose over the shared staging atlas, ordered by
	// destination, so their cost is a fixed handful of passes rather than three
	// per group (model_stage.go).
	r.prepareModelSlots(list)
	r.prepareModelGroups(list)
	r.composeModelStage()
	list.Replay(r)
	// A list without an Expand marker still leaves no compiled work behind.
	r.submitSchedule()
	return r.surfaces[0]
}

// DeviceDraws reports the device draws the most recent Execute issued. It is
// diagnostic only and never reaches simulation state.
func (r *Renderer) DeviceDraws() int {
	if r == nil {
		return 0
	}
	return r.frameDraws
}

// index0Color is the model slot atlas key plane's cleared value: index 0 in the
// red channel with opaque alpha, so premultiplied sampling recovers red exactly.
// The model stage stays in index space (C-G4); only its commit resolves colour.
var index0Color = color.RGBA{R: 0, G: 0, B: 0, A: 255}

// Clear fills the composite with palette index 0 resolved through PAL — the
// first command of every committed frame (C-G1). It rides the scene shader's
// constant-index op, so the clear resolves its colour the same way every other
// opaque family does (C-G8 as amended, §13.3).
//
// It is a barrier that opens a segment rather than a device call: the clear
// compiles as an opaque full-surface fill at the head of the new segment's first
// phase, so it joins the batch the phase draws first instead of costing a device
// call of its own (docs/DESIGN_GPU_RENDERER.md §11.5).
func (r *Renderer) Clear() {
	if r.surfaces[0] == nil {
		return
	}
	r.submitSchedule()
	if r.scene2D == nil {
		return
	}
	if !r.sched.begin(schedOpaque, 0, 0, r.w, r.h, [4]*ebiten.Image{}) {
		return
	}
	r.sched.quad(schedOpaque, 0, 0, float32(r.w), float32(r.h), 0, 0, 0, 0,
		[4]float32{0, 0, 0, 0}, [4]float32{0, 0, 0, sceneOpSolid})
}

// Expand marks the end of the recorded composite. The Enhanced executor has no
// expansion pass: every fragment already resolved its index through PALETTE.PAL
// as it was written, which is the same lookup the software expansion made, done
// per fragment instead of once per frame (C-G8 as amended,
// docs/DESIGN_GPU_RENDERER.md §13.3).
//
// It remains a barrier, so a caller that reads the composite after the marker —
// a capture, or the window adapter presenting it — sees every compiled phase
// submitted.
func (r *Renderer) Expand() {
	r.submitSchedule()
}

// The drawing families live beside this file: Terrain in terrain.go, the fills,
// line and point batches in draw.go, the keyed sprite/PCX/surface/cursor blits in
// sprites.go, the destination-reading blits and rects in deststage.go, the FNT
// text run in text.go, the fog composite in fog.go and the model commits in
// models.go. Every one of them compiles into the scheduler rather than drawing
// (§11.2).

// staticSinkCheck fails to compile if *Renderer stops satisfying drawlist.Sink.
var _ drawlist.Sink = (*Renderer)(nil)
