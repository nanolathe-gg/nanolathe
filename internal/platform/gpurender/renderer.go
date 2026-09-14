package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"os"
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
	// effectEnv holds the construction-time environment overrides and
	// effects/effectsSet the last player selection SetEffects applied (§30).
	effectEnv
	effects          drawlist.Effects
	effectsSet       bool
	materialsEnabled bool
	scorchEnabled    bool
	scorchShader     *ebiten.Shader
	metalGlint       bool
	modelPrep        modelPrepScratch
	tables           tables
	displayPalette   [256][4]byte
	// scene2D is the one opaque pass and sceneDest the one destination-compositing
	// pass (§11.2 "One scene shader for the 2D families").
	scene2D                   *ebiten.Shader
	sceneDest                 *ebiten.Shader
	markerShader              *ebiten.Shader
	markerAtlases             [4]markerAtlasUpload
	markerClock, markerWrites uint64 // resource reuse diagnostics only

	// surfaces[0] is the true-colour composite the whole frame is drawn into and
	// the image Execute returns: every source index is resolved through PAL as it
	// is written, so there is no expansion pass (C-G8 as amended, §13.3).
	// surfaces[1] holds the read copy for fog, Enhanced water and blast distortion. Each run's
	// destination region is copied here just before the run reads it.
	surfaces [2]*ebiten.Image
	// placeholder backs an image slot no op in a run requested, for the case
	// where no palette (and so no table atlas) has been installed.
	placeholder *ebiten.Image
	// textureAtlas packs every resolved 3DO texture frame the model lane samples
	// (model_atlas.go).
	textureAtlas modelTextureAtlas
	w, h         int
	// worldW, worldH are the extent every family clips a world command against
	// while the recorded world region is open: the RECORD extent, which is wider
	// than the framebuffer whenever the live zoom factor is below the record
	// step (docs/DESIGN_GPU_RENDERER.md §16.3). Outside the region they are w
	// and h, so an interface family clips exactly as it always did.
	worldW, worldH int

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

	// pointRows is the LHT row of every pixel of the lit point batch being
	// compiled and pointRuns its placed runs, both retained across frames so the
	// point layer allocates nothing (§11.2 "Allocation policy"). pointPlane is the
	// per-frame atlas a dense group of points commits through instead of one quad
	// per pixel (points.go, §13.8).
	pointRows  []uint8
	pointRuns  []litPointRun
	pointPlane pointPlane
	// pointGroups is the batch's runs collected by phase and pointGroupIdx the
	// index from a phase number to its group, both retained.
	pointGroups   []litPointGroup
	pointGroupIdx []int32

	// frameDraws counts device draws issued since the last Execute began, and
	// lastDest the destination of the most recent device call this package
	// instruments, which is how ModelStats.Passes counts destination switches.
	// Both are diagnostic only.
	frameDraws int
	lastDest   *ebiten.Image

	modelStats          ModelStats
	submissionFrame     uint64
	peakSubmissionFrame uint64
	peakSubmissionStats ModelStats

	// fog is the fog pass state of docs/DESIGN_GPU_RENDERER.md §11.2, owned by
	// fog.go so the fog unit and the model unit never edit the same file.
	fog  fogPass
	lens lensPass

	// glow is the Enhanced glow layer of docs/DESIGN_GPU_RENDERER.md §19: the
	// emissive batch, its planes and passes (glow.go).
	glow     glowLayer
	lighting battleLighting
	// ground is the Enhanced terrain illumination pass of §31: one clipped
	// disc per battle light, drawn at the end of the terrain pass.
	ground         groundLighting
	aircraftShadow aircraftShadowLayer
	water          waterLayer
	reflections    waterReflections
	distortion     worldDistortion
	heat           treeHeat

	// modelDirect is the PROTOTYPE direct model lane (model_direct.go): faces
	// drawn straight onto the composite instead of through the slot stage.
	modelDirect modelDirectLane
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

// SetDisplayPalette changes only final colour resolution. All index remapping
// tables retain their authored values [07 R-FE-01 §11].
func (r *Renderer) SetDisplayPalette(p [256][4]byte) {
	if r.tables.atlas == nil || r.displayPalette == p {
		return
	}
	r.tables.setDisplayPalette(p)
	r.displayPalette = p
	clear(r.lighting.colors)
}

// NewChecked is New but also returns the first shader compilation error. Each
// drawing family guards its own resources; callers report the initialization
// error before attempting a frame.
func NewChecked(pal *palette.Tables, w, h int) (*Renderer, error) {
	// The three environment overrides are read once, here, and then AND-ed with
	// the player's switches on every SetEffects (§29.2, §30). Reading them at
	// construction is what lets a developer's `…=0` keep a family off no matter
	// what the options page later selects.
	env := effectEnv{
		materials:  os.Getenv("NANOLATHE_MODEL_MATERIALS") != "0",
		scorch:     os.Getenv("NANOLATHE_SCORCH") != "0",
		metalGlint: os.Getenv("NANOLATHE_METAL_GLINT") != "0",
	}
	r := &Renderer{
		effectEnv:        env,
		materialsEnabled: env.materials,
		scorchEnabled:    env.scorch,
		metalGlint:       env.metalGlint,
		tables:           uploadTables(pal),
		tileAtlases:      make(map[tileAtlasKey]*tileAtlas),
		gafImages:        make(map[*formats.GAFFrame]*ebiten.Image),
		copyIdx:          [6]uint32{0, 1, 2, 1, 2, 3},
	}
	// Temporary prototype comparison; no saved setting (GPU design §25.2).
	r.SetDynamicBlastDistortion(os.Getenv("NANOLATHE_DYNAMIC_BLAST") != "0")
	r.SetExplosionGroundFlash(os.Getenv("NANOLATHE_EXPLOSION_GROUND_FLASH") != "0")
	if pal != nil {
		r.displayPalette = pal.Base
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
	compile(&r.lens.shader, newLensShader)
	compile(&r.markerShader, newMarkerShader)
	compile(&r.scorchShader, newScorchShader)
	compile(&r.aircraftShadow.shader, newAircraftShadowShader)
	compile(&r.water.shader, newWaterShader)
	compile(&r.water.wakeShader, newSurfaceWakeShader)
	compile(&r.reflections.sourceShader, newReflectionSourceShader)
	compile(&r.reflections.resolveShader, newReflectionResolveShader)
	compile(&r.reflections.softResolveShader, newSoftReflectionResolveShader)
	compile(&r.distortion.shader, newDistortionShader)
	compile(&r.ground.shader, newGroundLightShader)
	if err := r.initModelDirect(); err != nil && firstErr == nil {
		firstErr = err
	}

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
	r.modelStats = ModelStats{}
	r.submissionFrame++
	defer func() {
		if r.modelStats.SubmittedVertices > r.peakSubmissionStats.SubmittedVertices {
			r.peakSubmissionFrame = r.submissionFrame
			r.peakSubmissionStats = r.modelStats
		}
	}()
	r.frameDraws = 0
	r.lastDest = nil
	r.sched.resetFrame(r.w, r.h)
	r.pointPlane.resetFrame()
	r.glow.resetFrame()
	r.worldW, r.worldH = r.w, r.h
	r.modelPrep.reset()
	defer r.modelPrep.reset()
	// Every eligible subject of the frame is rasterized into the slot atlas
	// before Replay commits any of them, so the model stages cost a fixed
	// number of draws instead of one set per subject (§11.2). The frame's
	// attached-unit groups then compose over the shared staging atlas, ordered by
	// destination, so their cost is a fixed handful of passes rather than three
	// per group (model_stage.go).
	r.prepareBattleLighting(list)
	r.reflections.resetFrame()
	r.prepareBlastDistortion(list)
	r.prepareTreeHeat(list)
	r.prepareModelDirect(list)
	r.prepareProjectileReflections(list)
	list.Replay(r)
	// A list without an Expand marker still leaves no compiled work behind.
	r.submitSchedule()
	r.modelStats.DeviceDraws = r.frameDraws
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
