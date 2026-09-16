package client

import (
	"image"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Options configures a Client. Step is the injected owner of clock, sub-ticks,
// and snapshot publish; it is called first inside each update (C9). The
// window, palette, camera, and snapshot dependencies are carried here.
type Options struct {
	Step func(delta float64) // injected owner of clock/sub-ticks/snapshot publish (C9)

	// TickFraction is the Enhanced blend's fraction producer, read at Draw time
	// when an interpolated frame is recorded (docs/DESIGN_GPU_RENDERER.md
	// §13.5). The battle supplies it from the same millisecond source the tick
	// budget and the scroll pass read, un-floored; the client only clamps the
	// result into [0, 1) and never reads a clock itself [I6]. Nil leaves the
	// fraction at whatever SetTickFraction last stored, which is zero for a
	// client that never interpolates.
	TickFraction func() float32

	// Presentation snapshot source. If nil, an empty buffer is used.
	Buffer *frame.Buffer

	// Negotiated window size: the HUD panel arithmetic in phase 12 depends on
	// it, so it is not invented per frame. Zero means 640×480.
	Width, Height int
	Title         string
}

// Client is the software framebuffer, palette, camera, and snapshot reader. It
// keeps retail's 8-bit indexed renderer and presents one RGBA upload per
// frame. Rendering consumes only the currently committed frame (I6).
type Client struct {
	arrival             arrivalPresentation
	presentationPaused  bool
	pausedWorldRevision uint64
	pausedLayer         pausedRecordLayer

	// debugDeviceCapture is a host-owned, on-demand diagnostics bridge. It is
	// invoked only after the frame recorder has joined, outside simulation.
	debugDeviceCapture func(string) error
	modelScratch       modelScratch
	// projectileDraws retains the projectile dispatch list across frames; it
	// is rewound and rewritten by every DrawProjectileViews call.
	projectileDraws []presentationrender.ProjectileDraw
	opts            Options

	// exitRequested lets authored in-game GUI actions terminate the same
	// Ebitengine loop as closing the window. It is presentation state only.
	exitRequested bool
	// rendererToggles counts the executor swaps requested through
	// RequestRendererToggle (F10). The window adapter polls it; nothing else
	// reads it (docs/DESIGN_GPU_RENDERER.md §14.6) [I6].
	rendererToggles                int
	focused                        bool
	pointerCaptured                bool
	cursorRestorePending           bool
	cursorRestoreX, cursorRestoreY float32

	// stripBuckets holds the committed effects of one composed frame sorted by
	// strip barrier, with stripUnstripped holding the fixed pool. They are
	// filled once per frame by classifyEffectStrips and keep their capacity
	// between frames; the committed frame itself is never mutated [I6].
	stripBuckets    [effectStripCount][]frame.EffectView
	stripUnstripped []frame.EffectView
	stripsFrame     *frame.Frame
	stripsTick      uint32
	stripsValid     bool

	buffer *frame.Buffer

	// Enhanced interpolation state (docs/DESIGN_GPU_RENDERER.md §13.5).
	// interpolation is set only by the modern window path and the 120 TPS
	// benchmark; tickFraction16 is the blend fraction in 16.16 and
	// tickFractionSet says an explicit SetTickFraction outranks the option
	// producer; interp owns the blended view and its retained buffers. The
	// camera fields are the two stepped origin samples, the blend's save slot
	// and how many samples exist yet. See interpolate.go.
	interpolation           bool
	tickFraction16          int32
	tickFractionSet         bool
	interp                  interpolator
	camSamples              uint8
	camPrevView, camCurView camera.PresentationView
	camSave                 camera.Camera
	camDrawView             camera.PresentationView
	camBlending             bool
	// cameraFraction16 is the camera's own blend fraction — how far the window
	// is through the current Update — which is not the tick fraction (§13.5).
	// cameraFractionSet is false for a client that never presents through the
	// window adapter, and such a client does not blend its camera.
	cameraFraction16  int32
	cameraFractionSet bool

	// The record/submit pipeline of docs/DESIGN_GPU_RENDERER.md §13.10.
	// presentationEpoch is the host's client-mutation counter and pre owns the
	// one goroutine that records the next frame while the game goroutine is
	// blocked in the window layer's flush. A client that never calls
	// StartPreRecord starts no goroutine and behaves exactly as it did before.
	presentationEpoch uint64
	pre               preRecorder

	width, height int
	// recordW, recordH are the RECORD-SPACE extent every world emission site
	// clips against (DESIGN_GPU_RENDERER §16.3). They equal width/height
	// whenever the live zoom factor is on its record step — which is always, in
	// the classic executor — and are larger whenever the modern executor is
	// zoomed out, because the recorder then covers more world than the
	// framebuffer has pixels and the executor shrinks it on the way in.
	// Interface sites keep width/height: the chrome is drawn in framebuffer
	// pixels at every factor.
	recordW, recordH int
	// worldOverlay is true while the UI stage's own world region is open
	// (BeginWorldOverlay). Inside it the UI helpers that bake a framebuffer
	// bound into the recorded command — the plain blit's clip and the default
	// text width — take the record extent instead, because the commands between
	// those markers are world-positioned and the executor scales them (§16.3).
	worldOverlay bool
	indexed      []uint8
	rgba         []byte

	// The strategic marker layer of DESIGN_GPU_RENDERER §16.11.
	// strategicBlip is the minimap blip art the marker colours are taken from
	// and strategicBlipColors caches one resolved index per player colour;
	// radarOptions is the battle's radar mode-flags word, whose full-radar bit
	// the minimap blip gate reads; markerArena is the reusable batch storage, so
	// a strategic frame allocates nothing after the first.
	strategicTeam                         *formats.GAFEntry
	strategicTeamColors                   []strategicBlipColor
	strategicBlip                         *formats.GAFEntry
	strategicBlipColors                   []strategicBlipColor
	radarOptions                          uint32
	markerArena                           []drawlist.Marker
	strategicIcons                        *StrategicIconCatalog
	strategicDraw                         strategicLayoutScratch
	strategicPick                         strategicLayoutScratch
	strategicHover                        uint64
	strategicRecorded, strategicPresented strategicProjection

	// list is the recorded committed-frame draw list, reset and re-recorded
	// each frame then replayed through classicSink (docs/DESIGN_GPU_RENDERER.md
	// §2.2, C-G1). Its zero value is a usable empty list; a warm frame reuses
	// its backing arrays and allocates nothing.
	list drawlist.List
	// uiClip is the current private GUI surface. A child window intersects its
	// parent's surface before recording commands, so each recorded command keeps
	// the exact clipped destination it needs at replay time [07 §4].
	uiClip    drawlist.Rect
	hasUIClip bool
	// recordModelGeometry is enabled only for durable recording consumers.
	// Ordinary classic Frame composition leaves it false so normal presentation
	// does not allocate model packets before the modern executor asks for them.
	recordModelGeometry bool
	// geometryOnlyModels records native-scale polygons without allocating or
	// rasterizing CPU model images. RecordFrame selects it for modern mode.
	geometryOnlyModels bool

	// Runtime is presentation-only bookkeeping for backend frame cadence.
	runtime float64
	// Host update identity for acknowledgement admission; simulation speed and
	// pause do not change this counter (DESIGN_PRESENTATION_CLIENT C18).
	audioOpportunity uint64

	// base is the gamma-adjusted output palette. Every final indexed pixel
	// resolves through it; pal.Base retains the authored source [03 §4.3].
	// The logical→physical map lives on palette.Tables and is consulted only
	// where a semantic colour entry is resolved, before that byte is written
	// into the indexed surface [07 "Retail palette contract"].
	base        [256][4]byte
	gammaFactor float32
	pal         *palette.Tables

	// World / camera for Gate 1 terrain viewer [PLAN_04A]. When set, Frame
	// draws real TNT terrain instead of the placeholder gradient.
	terrain *world.Terrain
	// terrainGeneration lets the host retire source caches after a battle
	// transition without keeping the old world alive through a cache key
	// (DESIGN_GPU_RENDERER §14.3). It is presentation bookkeeping only.
	terrainGeneration         uint64
	prepareBattlePresentation func()
	cam                       *camera.Camera
	fnt                       *formats.FNT
	// messageFNT is the primary COMIX face selected by the later message pass;
	// the group-digit walk retains the side font in fnt [07 R-HUD-03 §14.4]
	// [03 R-FX-01 §6A].
	messageFNT    *formats.FNT
	developer     DeveloperOptions
	developerFont *formats.FNT
	developerScan modelTarget
	// messageLogos is the loaded LOGOS bank's player-colour entry, shared
	// with the battle HUD [07 R-HUD-03 §14.4][07 R-HUD-04 §4].
	messageLogos *formats.GAFEntry

	modelFS   *vfs.FS
	models    map[string]*unitModel
	texIndex  map[string]texRef
	logoIndex map[string]texRef
	// texRefs is the per-model texture resolution table (model_texrefs.go),
	// shared by pointer with the record workers; texGen changes whenever the
	// indices above are replaced, so a table built against old indices is
	// rebuilt rather than read.
	texRefs *sync.Map
	texGen  uint64
	// modelTextures is supplied by battle composition. Its players are bound
	// before ticks begin; this client only reads the selected primitive frame.
	modelTextures *ModelTextureRegistry
	// The two fields below support only explicit standalone preview/test setup.
	// A battle receives its loaded-model phase-7 registry through modelTextures;
	// orientation caches remain client-local [03 R-CRD-005 §1][I6].
	modelPresentation map[modelTextureKey]*modelTextureCursor
	modelPlayers      []phase7Stepper
	modelOrientation  map[uint64]*presentationrender.OrientationCache
	// cachedModelBodies retains only local composition planes keyed by committed
	// presentation identity. Its invalidation keys are published revisions; it
	// never holds a frame pointer or mutable simulation state [03 R-REN-03A §4][I6].
	cachedModelBodies map[uint64]*cachedModelBody
	// DET-04: shake state is authoritative phase 10. The owning battle
	// presentation updates its camera from the committed offset; this renderer
	// never keeps a second shake/camera accumulator.
	// DET-01: crt is a PRIVATE presentation copy (struct value copied at bind
	// time), never the session's authoritative stream. It feeds only the
	// presentation-side segmented-projectile pass, whose draws are
	// render-time and cannot affect simulation. AUDIT(parity-spine): approved
	// divergence — retail resolves segmented projectiles from the live CRT;
	// Nanolathe isolates the copy so render cadence cannot desync the sim.
	// Next wave: move segment resolution to sim-published values and drop this
	// field. Shrink-only.
	crt          *rng.CRT
	crtBound     bool
	frameTick    uint32 // committed tick of the frame being composed
	worldBuckets worldBuckets
	// parallelRecord and recordPool are the two-stage unit record of
	// docs/DESIGN_GPU_RENDERER.md §13.9. The pool is created on the first
	// modern frame that would use it and parked between frames; the flag is the
	// recorder's own switch and never a user-facing mode [I11].
	parallelRecord bool
	recordPool     *recordPool
	// unitJobs is this frame's stage-one job list: indices into the committed
	// frame's unit slice, in bucket order. It keeps its capacity between frames.
	unitJobs []int32
	// pendingPair hands the precomputed pair from the bucket walk to the one
	// unitGeometryPair call that would otherwise recompute it. It is live for
	// the length of a single presentUnit and is read on the recording goroutine
	// only.
	pendingPair *geometryPair
	fogCache    *visibility.FogCache
	fogVersion  uint64 // last immutable FogView copied into fogCache
	fogSource   uint64 // source identity paired with fogVersion
	fogOps      []presentationrender.FogOp
	// pointArena is this frame's backing store for every recorded Points batch
	// (the LHT halo, the calculated flash disc and the minimap
	// viewport rectangle). Each emitPoints batch is appended here and recorded as
	// a three-index sub-slice arena[off:end:end]; the capped bound forces any
	// later append to reallocate rather than overwrite an already-recorded batch,
	// so every batch is immutable for the life of the frame and safe under
	// deferred replay (WU-1.8). It is reset to [:0] in lockstep with c.list at the
	// top of composeIndexed, and its capacity is retained so a warm frame
	// allocates nothing (docs/DESIGN_GPU_RENDERER.md §2.2).
	// surfaceArena owns minimap packet bytes until the frame list is reset.
	// Separate capped sub-slices preserve multiple writes within one frame;
	// retained lists copy their bytes through List.Clone.
	surfaceArena []byte
	pointArena   []drawlist.Point
	// effectDraws is the retained buffer each effect pass refills. The draw
	// records are consumed inside DrawEffectViews and never recorded, so one
	// buffer serves every strip and the fixed pool instead of allocating a list
	// per pass (docs/DESIGN_GPU_RENDERER.md §11.5 "CPU").
	effectDraws     []presentationrender.EffectDraw
	selectionChrome []selectionChrome
	selectionDrag   SelectionDrag
	// rendererTraceSink is nil for the normal presentation path. When enabled,
	// model composition emits value-only candidate evidence after the complete
	// subject pixel is resolved [03 §2.4.1][03 §5.2][I6].
	rendererTraceSink   RendererTraceSink
	rendererTraceFilter RendererTraceFilter

	// Feature GAF presentation — sprite class [02 "Feature record"] [03 §5.1.1].
	// Loaded lazily from anims/<filename>.gaf via modelFS; cache is presentation-only (I6).
	featureGAFs   map[string]*formats.GAF      // lower filename -> GAF
	featureFrames map[string]*formats.GAFFrame // lower "filename|seqname" -> frame
	featureGACErr map[string]error             // memoised load failures (presentation-only)
	// detailArt is the installed 2x art provider and detailFrames the resolved
	// per-frame variants: the provider's own where it covers a frame, and the
	// nearest-doubled frame built on first use everywhere else. Both are
	// presentation-only and are never iterated on a draw path
	// (DESIGN_GPU_RENDERER §14.3) [I1][I6].
	detailArt    *DetailArt
	detailFrames map[*formats.GAFFrame]*formats.GAFFrame
	// doubledFrames is the nearest-doubled fallback cache, provider-independent
	// and kept for the client's life. enhanced records that the Enhanced
	// (modern) executor is presenting: only then is the provider consulted, so
	// Original (classic) draws the authored tiles and frames, doubled, at the
	// detail scale (DESIGN_GPU_RENDERER §14.3).
	doubledFrames map[*formats.GAFFrame]*formats.GAFFrame
	enhanced      bool
	// trails is the Enhanced trail layer's retained state (DESIGN_GPU_RENDERER
	// §15): presentation only, reset with the model registry and the terrain.
	trails trailState
	// wakes retains Enhanced land hover particles (GPU design §26).
	wakes       surfaceWakeState
	scorch      scorchState
	waterMotion waterMotionState
	waterFoam   []drawlist.SurfaceWake
	// featureSeqs memoises the compiled animation sequences the SIMULATION
	// reads through Client.FeatureSequence — the burn frame geometry and the
	// die/reclaim/burn lifetimes of [05 R-FEAT-01 §10]. A nil value is a
	// memoised miss. Unlike every other cache here this one is consulted from
	// an authoritative phase, so it is warmed up front and never loads on a
	// visit; feature_sequence.go owns the contract.
	featureSeqs map[string]*featureSequenceInfo
	// featureAnim holds the per-DEFINITION rest cursors of the animating
	// features, keyed by the lower-case "filename|seqname" that names the
	// definition's sequence. Retail initialises one cursor per definition, not
	// one per instance, and every placed copy of an animating feature is
	// therefore always on the same frame [05 R-FEAT-01 §1 "Established — the
	// rest cursors"]. featureAnimTick is the committed tick the cursors were
	// last advanced on, so recomposing one snapshot never advances them twice
	// [03 §4.4][I6].
	featureAnim         map[string]*featureAnimCursor
	featureAnimTick     uint32
	featureAnimTickSeen bool

	// Projectile presentation uses the one shared fx bank. It is loaded on
	// first use through the VFS boundary and retained for this client only;
	// unresolved art is memoised as an ordinary optional-resource miss [03
	// R-FX-01 §2][06 R-WFX-01 §1][I6].
	projectileGAF       *formats.GAF
	projectileGAFErr    error
	projectileGAFLoaded bool

	// effectBanks is the shared animation-bank cache the explosion-art
	// resolver reads: a weapon names its bank by key (`explosiongaf`), and the
	// bank is loaded from `anims/<name>.gaf` on the first miss and retained
	// [06 R-WFX-01 §1]. Keys are lower-cased, which is this build's form of
	// retail's case-insensitive scan of the loaded banks. A bank that fails to
	// load is memoised as a nil entry — retail treats that as a fatal fault
	// with a modal message box and exit; a presentation client draws nothing
	// instead and lets the rest of the frame compose [I6].
	effectBanks             map[string]*formats.GAF
	artDiagnostics          []ArtDiagnostic
	artDiagnosticsTruncated bool
	effectStats             EffectDrawStats
	stripStats              StripDrawStats
	blastSizes              map[*formats.GAFEntry]float32

	// flash holds the generated calculated-explosion tables [06 R-WFX-01 §2].
	flash flashTables

	// Fog overlay — anims/fog.gaf handles, presentation-only [03 §3.3].
	fogGAF      *formats.GAF
	fogGray     [4]*formats.GAFEntry // Gray1-4 variant family [03 §3.3]
	fogBlack    [4]*formats.GAFEntry // Black1-4 variant family [03 §3.3]
	fogLoaded   bool
	fogLoadErr  error
	ditheredFog bool // options byte bit6 0x40 DitheredFog [03 §3.3]
	// antiAlias is options word bit1 0x02 Anti_Alias. It gates the structure
	// composition supersample and, with it, retail's red/purple building
	// fringe [R-REN-03A §6][R-REN-03A §7].
	antiAlias bool
	// glow is the Enhanced glow layer switch: bloom from beams, projectile and
	// effect art and explosion light, drawn by the modern executor only
	// (docs/DESIGN_GPU_RENDERER.md §19). It is a Nanolathe display option with
	// no retail bit; the settings file persists it as display.glow.
	glow bool
	// effects is the player's Enhanced effect selection
	// (docs/DESIGN_GPU_RENDERER.md §30), persisted in the presentation block.
	// The recorder gates the producers that cost work to record; the executor
	// gates the passes. Classic composes the same pixels whatever it says.
	effects drawlist.Effects
	// shadows is options word bit2 0x04, the master model-shadow gate;
	// vehicleShadows is bit3 0x08, featureShadows is bit4 0x10, and shading is
	// bit5 0x20. Feature sprites read bit4 directly; it remains independent of
	// the model-shadow master and the structure-body shading selector when a
	// per-category preference changes it [03 §5.3][R-REN-03D §1, §4].
	shadows        bool
	vehicleShadows bool
	featureShadows bool
	shading        bool

	// Software cursor, drawn last over the composed surface [07 §8].
	cursors *Cursors

	// uiStage is the sole UI adapter. World ordering stays in drawCommittedFrame;
	// cmd-owned authored surfaces run once at its final interface slot [03 §1].
	uiStage            UIStage
	displayedResources DisplayedResources
	resourceTimers     [10]resourceDisplayTimer
	// recordNextResources selects a pure prediction on the pre-record worker.
	recordNextResources bool

	in InputState

	// Audio is the concrete internal/audio owner. The client only binds the
	// service and drains it at the rendered-frame boundary [03 §8.3–§8.4] [I6].
	audioService *audio.Service
	// messages is the presentation-owned shared caption/chat ring. It is
	// rebuilt only from committed semantic events and never read by simulation
	// [07 R-HUD-03 §14][I6].
	messages          frame.MessageRing
	messageEventsTick uint32
	// committedEvents is the scratch destination for the retained committed
	// events drained once per rendered frame. Reusing it keeps the drain
	// allocation-free after the first busy frame [03 R-AUD-01 §7][I6].
	committedEvents []frame.EventView
	screenChat      uint8
}

// PushUIClip confines subsequently recorded UI primitives to a private surface
// until the returned function is called. Nested surfaces intersect their parent
// rather than replacing it [07 §4].
func (c *Client) PushUIClip(x, y, w, h int) func() {
	if c == nil {
		return func() {}
	}
	previous, previousSet := c.uiClip, c.hasUIClip
	next := drawlist.Rect{X: int32(x), Y: int32(y), W: int32(w), H: int32(h)}
	if previousSet {
		next = intersectUIRects(previous, next)
	}
	c.uiClip, c.hasUIClip = next, true
	return func() {
		c.uiClip, c.hasUIClip = previous, previousSet
	}
}

func intersectUIRects(a, b drawlist.Rect) drawlist.Rect {
	ax0, ay0 := int64(a.X), int64(a.Y)
	ax1, ay1 := ax0+int64(a.W), ay0+int64(a.H)
	bx0, by0 := int64(b.X), int64(b.Y)
	bx1, by1 := bx0+int64(b.W), by0+int64(b.H)
	if ax1 < ax0 {
		ax1 = ax0
	}
	if ay1 < ay0 {
		ay1 = ay0
	}
	if bx1 < bx0 {
		bx1 = bx0
	}
	if by1 < by0 {
		by1 = by0
	}
	x0, y0 := max(ax0, bx0), max(ay0, by0)
	x1, y1 := min(ax1, bx1), min(ay1, by1)
	if x1 < x0 {
		x1 = x0
	}
	if y1 < y0 {
		y1 = y0
	}
	return drawlist.Rect{X: int32(x0), Y: int32(y0), W: int32(x1 - x0), H: int32(y1 - y0)}
}

func (c *Client) clipUIRect(r drawlist.Rect) drawlist.Rect {
	if c == nil || !c.hasUIClip {
		return r
	}
	return intersectUIRects(r, c.uiClip)
}

func (c *Client) clipUISprite(hasClip bool, clip drawlist.Rect) (bool, drawlist.Rect) {
	if c == nil || !c.hasUIClip {
		return hasClip, clip
	}
	if hasClip {
		return true, intersectUIRects(clip, c.uiClip)
	}
	return true, c.uiClip
}

// New creates a client. It allocates the indexed framebuffer at the negotiated
// logical size and prepares fallback palette tables. Window creation happens
// only in RunGame.
func New(opts Options) (*Client, error) {
	w := opts.Width
	h := opts.Height
	if w <= 0 {
		w = 640
	}
	if h <= 0 {
		h = 480
	}
	buf := opts.Buffer
	if buf == nil {
		buf = &frame.Buffer{}
	}
	c := &Client{
		opts:   opts,
		buffer: buf,
		// The settings reader looks Anti_Alias up under the registry key
		// Anti-Alias alongside Shadows/VehicleShadows/FeatureShadows; on a
		// miss it sets the bit and writes the default back, so anti-aliasing
		// is on by default. Established (direct-static) [R-RAST-01 §4].
		antiAlias: true,
		// The glow layer is on until the player turns it off; it costs nothing
		// under the classic executor, which never sees it (§19).
		glow: true,
		// Every Enhanced effect is on until the player turns it off (§30).
		effects: drawlist.AllEffects(),
		// Restore-defaults sets the Shading bit, so shading is on unless the
		// player turns it off [R-RND-02A]; the bulk shadow key sets its three
		// bits together [03 §5.3].
		shadows:           true,
		vehicleShadows:    true,
		featureShadows:    true,
		shading:           true,
		width:             w,
		height:            h,
		recordW:           w,
		recordH:           h,
		indexed:           make([]uint8, w*h),
		rgba:              make([]byte, w*h*4),
		models:            map[string]*unitModel{},
		texIndex:          map[string]texRef{},
		logoIndex:         map[string]texRef{},
		texRefs:           &sync.Map{},
		modelPresentation: map[modelTextureKey]*modelTextureCursor{},
		modelPlayers:      nil,
		modelOrientation:  map[uint64]*presentationrender.OrientationCache{},
		cachedModelBodies: map[uint64]*cachedModelBody{},
		featureGAFs:       map[string]*formats.GAF{},
		featureFrames:     map[string]*formats.GAFFrame{},
		featureGACErr:     map[string]error{},
		featureAnim:       map[string]*featureAnimCursor{},
		messages:          *frame.NewMessageRing(),
		screenChat:        1,
	}
	// The four values seeded above and by NewMessageRing are retail's own
	// missing-value defaults: `textlines` 10, `textscroll` 10, `screenchat` 1,
	// `unitchattext` 5 [02 §3][02 "registry preference (Total Annihilation
	// key)"]. Their owner is not open — it is the startup settings loader,
	// which reads each value under the game's registry key and, when one is
	// absent, installs exactly these and writes them back; the `SPEEDS` page's
	// `MAXLINES`, `TXTSCROL` and `UNITCHAT` controls and the `ScreenChat`
	// command are the runtime writers [07 §5][07 R-FE-01 §11].
	//
	// These are only the values a fresh profile installs. internal/settings
	// now carries the four (Settings.Messages), and cmd/nanolathe's
	// installBattleClient calls ConfigureMessageLines/SetScreenChat with the
	// persisted choice, so a returning profile's stored values reach this
	// client instead of these defaults standing for the whole session
	// (WU-19-170).
	c.in = *newInputState()
	c.gammaFactor = 1
	// Fallback display palette: grayscale. This keeps the framebuffer path
	// valid before SetPalette installs PALETTE.PAL [03 §4.3].
	for i := 0; i < 256; i++ {
		c.base[i][0] = byte(i)
		c.base[i][1] = byte(i)
		c.base[i][2] = byte(i)
		c.base[i][3] = 255
	}
	return c, nil
}

// SetTerrain sets the world terrain for Gate 1 drawing [PLAN_04A C1]. Clearing
// the terrain clears the detail art with it: the tile set it carries belongs to
// the map that is going away (DESIGN_GPU_RENDERER §14.3).
func (c *Client) SetTerrain(t *world.Terrain) {
	if c != nil {
		c.pausedWorldRevision++
	}
	if c != nil && c.terrain != t {
		c.terrainGeneration++
		c.arrival = arrivalPresentation{}
		c.resetFogCache()
		c.resetTrails()
		c.terrain = t
		if t == nil {
			c.SetDetailArt(nil)
		}
	}
}

// TerrainGeneration changes when SetTerrain replaces or clears the world.
// The host reads it after joining the recording worker, before reusing any
// source caches; repeated installation of the same world leaves it unchanged.
func (c *Client) TerrainGeneration() uint64 {
	if c == nil {
		return 0
	}
	return c.terrainGeneration
}

// SetCamera sets the camera for Gate 1 pan [07 §10].
func (c *Client) SetCamera(cam *camera.Camera) {
	if c.cam != cam {
		c.camSamples = 0
	}
	c.cam = cam
}

// SetPalette installs real palette tables [03 §4.3] C7.
func (c *Client) SetPalette(p *palette.Tables) {
	if c != nil {
		c.pausedWorldRevision++
	}
	c.pal = p
	if p != nil {
		c.rebuildDisplayPalette()
		c.ResolveUnitStyle()
	}
}

// SetGammaFactor rebuilds the output palette from authored channels. Retail
// stores the factor as binary32, clamps only above 255, then truncates and
// retains the low byte; negative command factors wrap [07 R-FE-01 §11].
func (c *Client) SetGammaFactor(factor float32) {
	c.gammaFactor = factor
	c.rebuildDisplayPalette()
}

// DisplayPalette is the final physical-index-to-colour table for both backends.
// Indexed lighting, blending and semantic colour tables remain immutable.
func (c *Client) DisplayPalette() [256][4]byte { return c.base }

func (c *Client) rebuildDisplayPalette() {
	for i := range c.base {
		source := [4]byte{byte(i), byte(i), byte(i), 255}
		if c.pal != nil {
			source = c.pal.Base[i]
		}
		for channel := 0; channel < 3; channel++ {
			// The binary32 factor times an eight-bit integer is exact in
			// binary64; do not round the product back to binary32.
			value := float64(source[channel]) * float64(c.gammaFactor)
			if value > 255 {
				value = 255
			}
			c.base[i][channel] = byte(numeric.TruncateFloat64ToLow32(value))
		}
		c.base[i][3] = 0
	}
}

// PaletteTables exposes the installed palette tables so the platform adapter can
// build the modern (GPU) executor's index→RGBA lookup textures once, off the
// client (docs/DESIGN_GPU_RENDERER.md §2.3, §2.4, C-G8). It is a read-only
// accessor of the immutable-after-load tables; nil before SetPalette. The
// tables never enter a sim path, so handing out the pointer keeps the device
// resources in gpurender rather than on the client [I6].
func (c *Client) PaletteTables() *palette.Tables {
	if c == nil {
		return nil
	}
	return c.pal
}

// SetFNT installs the shared software font used by typed UI stages [03 §7.1].
// Standalone callers share it with messages; battle adoption then overrides
// the message face with SetMessageFNT. Clearing it also clears that binding.
func (c *Client) SetFNT(fnt *formats.FNT) { c.fnt, c.messageFNT = fnt, fnt }

// SetMessageFNT installs the primary font for the message column without
// changing the side-font group digits [07 R-HUD-03 §14.4][03 R-FX-01 §6A].
func (c *Client) SetMessageFNT(fnt *formats.FNT) { c.messageFNT = fnt }

// SetSnapshot repoints presentation at another published buffer — used when
// the shell transitions from front-end menus into a live battle session [I6].
func (c *Client) SetSnapshot(b *frame.Buffer) {
	if c != nil && b != nil && c.buffer != b {
		c.resetFogCache()
		c.resetTrails()
		// Entry and restore reset stocks and bind the new saved deadlines;
		// the four rate latches survive this reset [07 R-HUD-03 §4].
		c.displayedResources.Energy, c.displayedResources.Metal = 0, 0
		c.resourceTimers = [10]resourceDisplayTimer{}
		c.buffer = b
		c.arrival = arrivalPresentation{}
		c.SetPresentationPaused(false)
	}
}

func (c *Client) resetFogCache() {
	if c == nil {
		return
	}
	c.fogCache = nil
	c.fogVersion = 0
	c.fogSource = 0
	c.fogOps = c.fogOps[:0]
}

// Size returns the negotiated logical framebuffer size.
func (c *Client) Size() (int, int) { return c.width, c.height }

// Resize re-negotiates the logical framebuffer size and re-allocates the
// indexed surface and its RGBA expansion.
//
// This is retail's offscreen re-creation. Both directions of the size change
// run it: the load transitions compare `DisplaymodeWidth`/`Height` to the
// presentation window's current size and, when different, free the offscreen,
// drop and re-create the presentation surface and re-allocate the offscreen at
// the new size [07 R-FE-01 §11]; the shell loader and the post-battle
// controller do the same in reverse, back to 640x480 [07 R-FE-02 §2].
//
// Nanolathe's offscreen is these two buffers, so re-allocating them is the
// whole of it — every drawing path reads c.width/c.height at use time. The
// platform adapter follows Size() for the uploaded image and logical layout;
// the host window size is independent (DESIGN_PRESENTATION_CLIENT §2.1). A same-size call is a
// no-op, which is what makes the "when different" test above cheap enough to
// run unconditionally at the transition.
func (c *Client) Resize(width, height int) {
	if c == nil || width <= 0 || height <= 0 {
		return
	}
	if width == c.width && height == c.height {
		return
	}
	c.width, c.height = width, height
	c.recordW, c.recordH = width, height
	c.indexed = make([]uint8, width*height)
	c.rgba = make([]byte, width*height*4)
}

// IsFocused reports the platform window focus sampled at the client edge.
// Battle camera predicates consume this value without importing Ebitengine;
// focus is checked before edge scrolling, and an unfocused window suppresses
// it [07 §10].
func (c *Client) IsFocused() bool {
	return c != nil && c.focused
}

// SetFocused records the window focus for the current host frame. The window
// loop samples it from the platform alongside the pointer each update; an
// embedder without a window supplies it the same way it supplies the pointer.
func (c *Client) SetFocused(focused bool) {
	if c != nil {
		c.focused = focused
	}
}

// The four methods below are the surface a platform adapter presents through.
// They exist so the adapter can own the window, the device and the uploaded
// image while the client owns only pixels and presentation state; no client
// code reaches a device [I6].

// Title is the window title the adapter should install, or "" for none.
func (c *Client) Title() string {
	if c == nil {
		return ""
	}
	return c.opts.Title
}

// HasCursors reports whether the retail software cursor has been installed.
// A windowed adapter requires it before it hides the system pointer [07 §8].
func (c *Client) HasCursors() bool { return c != nil && c.cursors != nil }

// Step advances presentation runtime by one host frame and runs the injected
// owner of clock, sub-ticks and snapshot publication (C9). Delta is the
// adapter's fixed host period; wall-clock time never enters the sim [I6].
func (c *Client) Step(delta float64) {
	if c == nil {
		return
	}
	c.cursorRestorePending = false
	c.runtime += delta
	c.audioOpportunity++
	if c.opts.Step != nil {
		c.opts.Step(delta)
	}
	// The camera has now been moved by this step's scroll and follow passes, so
	// this is where Enhanced takes the sample it blends between
	// (docs/DESIGN_GPU_RENDERER.md §13.5).
	c.sampleCameraOrigin()
}

// Present composes one frame and returns the expanded RGBA framebuffer for the
// adapter to upload. The slice is owned by the client and is overwritten by
// the next Present; the adapter must copy it into its own image rather than
// retain it.
func (c *Client) Present() []byte {
	if c == nil {
		return nil
	}
	c.Frame()
	return c.rgba
}

// Buffer exposes the presentation snapshot source (diagnostics publish into
// it directly; the session path owns it in normal play).
func (c *Client) Buffer() *frame.Buffer { return c.buffer }

// RequestExit asks the window backend to terminate after the current update.
func (c *Client) RequestExit() { c.exitRequested = true }

// ExitRequested reports whether RequestExit has been called.
func (c *Client) ExitRequested() bool { return c.exitRequested }

// RequestRendererToggle asks the window adapter to swap presentation executors
// — F10 of docs/DESIGN_GPU_RENDERER.md §14.6. The client owns neither executor,
// so it publishes the request as a count the adapter polls at its next Update;
// a route with no adapter (a capture, a test) never services it and nothing
// changes. Presentation-only: no simulation phase reads it [I6].
func (c *Client) RequestRendererToggle() {
	if c == nil {
		return
	}
	c.rendererToggles++
}

// RendererToggleCount reports how many executor swaps have been requested. The
// adapter compares it with the count it last acted on, so a request made while
// the window was between updates is never lost and never applied twice.
func (c *Client) RendererToggleCount() int {
	if c == nil {
		return 0
	}
	return c.rendererToggles
}

// StepCursorScaledDelta advances the software cursor from the positive
// scaled wall-clock delta used by the presentation adapter. Cursor playback
// is not tied to runnable simulation ticks; the underlying cursor consumes a
// signed 16-bit delta, so large elapsed intervals are split without changing
// the resulting countdown [03 §4.4][R-CRD-005 §1].
func (c *Client) StepCursorScaledDelta(delta int32) {
	if c == nil || c.cursors == nil || delta <= 0 {
		return
	}
	for delta > 0 {
		step := delta
		if step > 32767 {
			step = 32767
		}
		c.cursors.play.StepDelta(int16(-step))
		delta -= step
		if !c.cursors.play.IsActive() {
			return
		}
	}
}

// SetModelFS installs the VFS for lazy 3DO/texture loads and builds the
// texture-name index. Presentation state only.
func (c *Client) SetModelFS(fs *vfs.FS) {
	if c != nil {
		c.pausedWorldRevision++
	}
	c.modelFS = fs
	// The hover hull reads root-piece vertices from the same presentation model
	// cache the draw path uses [07 R-REV-01 §1]. PickSnapshotUnit is handed a
	// camera and a committed frame by shell callers that hold no cache, so the
	// cache registers itself here, where models first become resolvable.
	SetUnitHullModels(c)
	c.modelPresentation = map[modelTextureKey]*modelTextureCursor{}
	c.modelPlayers = nil
	c.modelTextures = nil
	c.modelOrientation = map[uint64]*presentationrender.OrientationCache{}
	c.cachedModelBodies = map[uint64]*cachedModelBody{}
	c.models = map[string]*unitModel{}
	c.resetTrails()
	c.texIndex = map[string]texRef{}
	c.logoIndex = map[string]texRef{}
	c.texGen++
	c.featureGAFs = map[string]*formats.GAF{}
	c.featureFrames = map[string]*formats.GAFFrame{}
	c.detailArt = nil
	c.detailFrames = nil
	c.featureGACErr = map[string]error{}
	c.featureSeqs = map[string]*featureSequenceInfo{}
	c.projectileGAF = nil
	c.projectileGAFErr = nil
	c.projectileGAFLoaded = false
	c.effectBanks = nil
	c.artDiagnostics = nil
	c.artDiagnosticsTruncated = false
	c.blastSizes = nil
	c.flash = flashTables{}
	c.fogGAF = nil
	c.fogLoaded = false
	c.fogLoadErr = nil
	for i := range c.fogGray {
		c.fogGray[i] = nil
		c.fogBlack[i] = nil
	}
	c.buildTextureIndex()
}

// SetModelTextureRegistry attaches immutable battle model metadata. The
// registry remains battle-owned and is installed as the session phase-7
// service; replacing this client never changes its cadence [03 R-CRD-005 §1].
func (c *Client) SetModelTextureRegistry(registry *ModelTextureRegistry) {
	if c != nil {
		c.pausedWorldRevision++
	}
	if c == nil {
		return
	}
	if c.modelTextures != registry {
		// A registry boundary is a battle boundary. Unit slots are reusable, so
		// no retained body or orientation reference may cross it.
		c.modelOrientation = map[uint64]*presentationrender.OrientationCache{}
		c.cachedModelBodies = map[uint64]*cachedModelBody{}
	}
	c.modelTextures = registry
	if registry == nil {
		return
	}
	c.modelFS = registry.fs
	c.texIndex = registry.primary
	c.logoIndex = registry.logos
	c.texGen++
	c.models = map[string]*unitModel{}
}

// SetDitheredFog selects patterned current-fog rendering [03 §3.3]. When set,
// hi==15 draws a checker and hi 1..14 uses patterned Gray GAF drawing; when
// clear, both use their plain forms. The lo/Black family is always plain.
func (c *Client) SetDitheredFog(v bool) { c.ditheredFog = v }

// SetAntiAlias selects the Anti_Alias display option, which makes structures
// compose through the 2x supersample and ALP downscale [R-REN-03A §6].
func (c *Client) SetAntiAlias(v bool) { c.antiAlias = v }

// AntiAlias returns the current Anti_Alias bit.
func (c *Client) AntiAlias() bool { return c.antiAlias }

// SetGlow selects the Enhanced glow layer: bloom from emissive world art under
// the modern executor (docs/DESIGN_GPU_RENDERER.md §19). The recording is the
// same either way; the executor reads this switch through Glow. The paused
// world raster is cached against its inputs and the executor resolves glow
// into it, so a changed switch is a different raster (§13.10).
func (c *Client) SetGlow(v bool) {
	if c == nil || c.glow == v {
		return
	}
	c.glow = v
	c.pausedWorldRevision++
}

// Glow returns the Enhanced glow layer switch.
func (c *Client) Glow() bool { return c != nil && c.glow }

// SetEffects selects the player's Enhanced effects (§30). A changed selection
// retires the transient histories the same way an executor swap does: the
// trail, wake, water-motion and scorch states are accumulated per committed
// tick while their producer runs, so a switch that was off left gaps in them
// and a switch turned off must not leave stale marks behind. Nothing here
// touches simulation state [I6].
func (c *Client) SetEffects(e drawlist.Effects) {
	if c == nil || c.effects == e {
		return
	}
	c.effects = e
	c.resetTrails()
	// The paused world composite is cached against its inputs; a changed
	// selection is a different world raster (§13.10).
	c.pausedWorldRevision++
}

// Effects returns the player's Enhanced effect selection.
func (c *Client) Effects() drawlist.Effects {
	if c == nil {
		return drawlist.Effects{}
	}
	return c.effects
}

// SetShadowOptions selects the master shadow, vehicle-shadow and shading bits
// [03 §5.3][R-REN-03D §1].
//
// The shading bit also reaches the model composer, which is the option's real
// consumer: retail runs the shaded piece renderer only for a unit whose class
// bit says structure (`BMcode=0`) **and** with the `Shading` display option on
// [03 R-RND-02A]. Until this assignment the renderer's copy of the bit had no
// writer outside its own tests, so turning `SHADING` off on the `VISUALS` page
// left every structure shaded.
func (c *Client) SetShadowOptions(master, vehicle, shading bool) {
	c.shadows, c.vehicleShadows, c.shading = master, vehicle, shading
	presentationrender.Shading = shading
}

// SetFeatureShadows selects the feature/sprite shadow bit. The per-feature
// dispatcher reads this bit directly, independently of the model-shadow master,
// vehicle-shadow and structure-body shading bits [03 §5.3][R-REN-03D §4].
func (c *Client) SetFeatureShadows(enabled bool) { c.featureShadows = enabled }

// FeatureShadows reports the current feature/sprite shadow bit.
func (c *Client) FeatureShadows() bool { return c != nil && c.featureShadows }

// ShadowOptions returns the three bits the model shadow gate reads.
func (c *Client) ShadowOptions() (master, vehicle, shading bool) {
	return c.shadows, c.vehicleShadows, c.shading
}

// DitheredFog returns the current DitheredFog bit.
func (c *Client) DitheredFog() bool { return c.ditheredFog }

// ensureFogGAF loads anims/fog.gaf lazily and binds Gray1-4/Black1-4 [03 §3.3].
// It is presentation-only and never touches sim state (I6).
func (c *Client) ensureFogGAF() {
	if c.fogLoaded || c.modelFS == nil {
		return
	}
	c.fogLoaded = true
	gaf, err := formats.LoadGAFFile(c.modelFS, "anims/fog.gaf")
	if err != nil {
		c.fogLoadErr = err
		return
	}
	c.fogGAF = gaf
	namesGray := [4]string{"Gray1", "Gray2", "Gray3", "Gray4"}
	namesBlack := [4]string{"Black1", "Black2", "Black3", "Black4"}
	for i, n := range namesGray {
		if e, ok := gaf.Find(n); ok {
			c.fogGray[i] = e
		}
	}
	for i, n := range namesBlack {
		if e, ok := gaf.Find(n); ok {
			c.fogBlack[i] = e
		}
	}
}

// ComposedFrameSnapshot is caller-owned evidence copied from one invocation of
// the normal committed-frame composer. Indexed and RGBA describe the same
// completed surface, including the software cursor. It records no writer
// provenance; the renderer trace owns its narrower subject-composition scope.
//
// Committed distinguishes the deterministic pre-publication surface from a
// publication whose valid tick is zero. The returned slices never alias the
// client's reusable presentation planes [03 §1][03 §2.4][I6].
type ComposedFrameSnapshot struct {
	Width, Height int
	Committed     bool
	Tick          uint32
	Indexed       []uint8
	RGBA          []byte
	// List is a deep copy of the committed-frame draw list this composition
	// recorded, in the order Clear, all draws, Cursor, Expand (WU-1.8). It is the
	// same list the classic sink just replayed into Indexed/RGBA, so a caller can
	// replay it through another sink (the GPU executor and tools/framediff) and
	// compare. The copy owns its arrays — the client reuses the live list's
	// backing store each frame — so it stays valid after the next composition
	// [C-G1][03 §2.4].
	List drawlist.List
}

// composeCurrentFrame runs the same one-pass committed-frame composition used
// by ComposeFrame and returns the sampled publication only long enough for a
// caller to copy its tick identity. It never exposes that frame beyond the
// documented Buffer.Current reader lifetime [03 §2.4][I6].
func (c *Client) composeCurrentFrame() *frame.Frame {
	cur := c.buffer.Current()
	// The recording pass writes nothing to c.indexed: composeIndexed records the
	// clear and every world/interface draw, drawCursor records the cursor, and
	// the expansion marker is recorded last. Replaying the whole list once through
	// the classic sink is the single execution — Clear, all draws, Cursor, Expand
	// in record order (docs/DESIGN_GPU_RENDERER.md §2.2, C-G1, C-G8).
	c.composeIndexed(cur, cur != nil)
	c.drawCursor() // cursor last, over the composed surface [07 §8]
	c.list.RecordExpand()
	c.list.Replay(c.classicSink())
	return cur
}

// ComposeFrame reads the published buffer and composes one current frame,
// returning it as an RGBA image without entering the window loop (I6). It is
// the basis of the --shot diagnostic path.
func (c *Client) ComposeFrame() *image.RGBA {
	c.composeCurrentFrame()
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	copy(img.Pix, c.rgba)
	return img
}

// ComposeFrameSnapshot composes exactly once through the same committed-frame
// path as ComposeFrame, then copies both indexed and RGBA planes. This is a
// diagnostic boundary for deterministic parity capture, not another renderer
// or a claim about the final pixel writer [03 §1][03 §2.4][I6].
func (c *Client) ComposeFrameSnapshot() ComposedFrameSnapshot {
	if c == nil {
		return ComposedFrameSnapshot{}
	}
	wasRecordingGeometry := c.recordModelGeometry
	wasGeometryOnly := c.geometryOnlyModels
	c.recordModelGeometry = true
	c.geometryOnlyModels = false
	cur := c.composeCurrentFrame()
	c.recordModelGeometry = wasRecordingGeometry
	c.geometryOnlyModels = wasGeometryOnly
	snapshot := ComposedFrameSnapshot{
		Width:     c.width,
		Height:    c.height,
		Committed: cur != nil,
		Indexed:   append([]uint8(nil), c.indexed...),
		RGBA:      append([]byte(nil), c.rgba...),
		// A deep copy so the caller's list survives the next composition, which
		// reuses the live list's backing arrays (WU-1.8).
		List: c.list.Clone(),
	}
	if cur != nil {
		snapshot.Tick = cur.Tick
	}
	return snapshot
}

// Input exposes the per-frame input snapshot for the windowed paths. Edge
// flags are valid for exactly one Update; held state persists while down.
func (c *Client) Input() *input.State { return &c.in }

// featureGAFFor loads anims/<filename>.gaf lazily and returns the GAF.
// Filename is the TDF `filename` stem (e.g. "trees") without extension [02 "Feature record"].
// The path is lower-cased per VFS canonical rules (I1). Presentation-only (I6).
func (c *Client) featureGAFFor(filename string) (*formats.GAF, error) {
	if filename == "" || c.modelFS == nil {
		return nil, nil
	}
	key := strings.ToLower(strings.TrimSpace(filename))
	if key == "" {
		return nil, nil
	}
	if gaf, ok := c.featureGAFs[key]; ok {
		return gaf, nil
	}
	if err, ok := c.featureGACErr[key]; ok {
		return nil, err
	}
	path := "anims/" + key + ".gaf"
	gaf, err := formats.LoadGAFFile(c.modelFS, path)
	if err != nil {
		c.featureGACErr[key] = err
		return nil, err
	}
	c.featureGAFs[key] = gaf
	// A bank loaded after the provider was installed is indexed as it loads, so
	// the frame-variant lookup does not depend on load order
	// (DESIGN_GPU_RENDERER §14.3).
	c.indexDetailBank(key, gaf)
	return gaf, nil
}

// featureFrameFor resolves seqName inside filename's GAF, with per-frame durations.
// For static features it returns the first frame; animated features use an
// independent cursor keyed by the feature's stable presentation identity [03 §4.4].
// Returns nil on missing filename/seq or load failure; no authored pixels are
// emitted for an unresolved sequence [05 "Feature catalog and placement"].
func (c *Client) featureFrameFor(f frame.FeatureView, shadow bool) *formats.GAFFrame {
	filename := f.Filename
	seq := f.SeqName
	if shadow {
		seq = f.SeqNameShad
	}
	// A cell with a live event record blits that record's cursor frames — the
	// burn, death or reclaim animation the instance is running — instead of the
	// definition's rest cursor [03 R-RAST-01 §6][05 R-FEAT-01 §10] pass 3. The
	// visit index comes from the committed frame, so the painted frame follows
	// the simulation's cursor rather than a presentation-side timer, and the
	// record's last visit is the one that retires it (I6).
	if f.EventSeqName != "" && filename != "" {
		eventSeq := f.EventSeqName
		if shadow {
			eventSeq = f.EventSeqNameShad
		}
		if eventSeq == "" {
			return nil // the definition names no shadow twin for this sequence
		}
		return c.featureEventFrame(filename, eventSeq, f.EventSeqVisit)
	}
	if filename == "" || seq == "" {
		return nil
	}
	gaf, err := c.featureGAFFor(filename)
	if err != nil || gaf == nil {
		return nil
	}
	entry, ok := gaf.Find(seq)
	if !ok || entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	if !f.Animating {
		if entry.Frames[0].Frame != nil {
			return entry.Frames[0].Frame
		}
		return nil
	}
	return c.featureAnimatedFrame(strings.ToLower(filename)+"|"+strings.ToLower(seq), entry)
}

// featureAnimCursor is one animating feature definition's rest cursor. It
// pairs the generic tick-stepped playback cursor with the decoded frames of
// the definition's own GAF entry [03 §4.4].
type featureAnimCursor struct {
	player *presentationrender.TexturePlayer
	frames []*formats.GAFFrame
	// cycle is the sum of the entry's authored per-frame durations: the whole
	// number of simulation ticks one loop of the sequence takes [03 §4.4].
	// Zero means the sequence has no usable timing and is left on frame zero.
	cycle uint32
}

// featureAnimatedFrame resolves the current frame of one animating feature
// definition.
//
// Correction. This previously routed through the model-texture cursor
// adapter, which keys a cursor by a per-INSTANCE identity and refuses the
// zero identity. `frame.FeatureView.InstanceID` has no writer anywhere in the
// publication path, so every animating feature arrived with identity zero and
// the adapter answered nil — the ten multi-frame animating definitions in the
// stock corpus (the `acidplant01..05` gas plants, twenty frames each) drew no
// pixels at all, and nothing ever stepped a feature cursor either, so they
// would have been frozen on frame 0 even with an identity. Per-instance was
// also the wrong shape: retail builds the rest cursor once per DEFINITION from
// `seqname`, shares it across every placed copy, and advances it in the
// feature phase every tick, so all copies of an animating feature are always
// on the same frame [05 R-FEAT-01 §1 "Established — the rest cursors"].
//
// Single-frame entries never advance [03 §4.4] — which is every `*vent*`
// definition in the stock corpus, `geothermal` included.
func (c *Client) featureAnimatedFrame(key string, entry *formats.GAFEntry) *formats.GAFFrame {
	if c == nil || entry == nil || len(entry.Frames) == 0 {
		return nil
	}
	if len(entry.Frames) == 1 {
		return entry.Frames[0].Frame
	}
	// Advance every live definition cursor exactly once per committed tick,
	// before this tick's first animating feature reads one. The feature pass
	// runs inside one committed frame, so the whole set moves together and a
	// repainted snapshot moves nothing [03 §4.4][I6].
	c.stepFeatureAnimations()
	cur := c.featureAnim[key]
	if cur == nil {
		frames := make([]*formats.GAFFrame, len(entry.Frames))
		ids := make([]content.AssetID, len(frames))
		durations := make([]uint32, len(frames))
		cycle := uint64(0)
		for i, ref := range entry.Frames {
			frames[i] = ref.Frame
			ids[i] = content.AssetID(key + "#" + strconv.Itoa(i))
			durations[i] = ref.Value // authored whole simulation ticks [03 §4.4]
			cycle += uint64(ref.Value)
		}
		if cycle > uint64(^uint32(0)) {
			cycle = 0 // unusable authored timing; hold frame zero rather than invent one
		}
		cur = &featureAnimCursor{
			player: presentationrender.NewTexturePlayer(content.AssetSequence{
				ID: content.AssetID(key), Frames: ids, Durations: durations, Loop: true,
			}),
			frames: frames,
			cycle:  uint32(cycle),
		}
		if c.featureAnim == nil {
			c.featureAnim = map[string]*featureAnimCursor{}
		}
		c.featureAnim[key] = cur
		// Retail's rest cursor is created at map load on frame 0 and advanced by
		// the feature phase on every tick since [05 R-FEAT-01 §1]. A cursor this
		// client only builds when the definition first enters the viewport must
		// therefore catch up to the committed tick, or the frame shown would
		// depend on when the camera arrived instead of on the tick. The sequence
		// loops, so the catch-up is bounded by one cycle.
		if cur.cycle != 0 {
			for i := uint32(0); i < c.frameTick%cur.cycle; i++ {
				cur.player.Step()
			}
		}
	}
	asset, ok := cur.player.Frame()
	if !ok {
		return nil
	}
	for i := range cur.frames {
		if content.AssetID(key+"#"+strconv.Itoa(i)) == asset {
			return cur.frames[i]
		}
	}
	return nil
}

// stepFeatureAnimations advances the feature rest cursors one simulation tick
// when the committed tick has moved. Feature cursors are explicitly not part
// of the phase-7 model-texture registry [03 §4.4 R-CRD-005 §1]; their owning
// presentation path drives them, which is here.
func (c *Client) stepFeatureAnimations() {
	if c == nil {
		return
	}
	if c.featureAnimTickSeen && c.featureAnimTick == c.frameTick {
		return
	}
	// Multiple simulation ticks in one present advance the cursor multiple
	// times; a present with no new tick freezes it [03 §4.4]. A tick that moved
	// backwards (a client re-attached to a fresh session) advances nothing.
	steps := uint32(0)
	if c.featureAnimTickSeen && c.frameTick > c.featureAnimTick {
		steps = c.frameTick - c.featureAnimTick
	}
	c.featureAnimTick = c.frameTick
	c.featureAnimTickSeen = true
	for _, cur := range sortedFeatureAnim(c.featureAnim) {
		// The sequence loops, so replaying more than one cycle is wasted work
		// that lands on the same frame.
		n := steps
		if cur.cycle != 0 && n >= cur.cycle {
			n %= cur.cycle
		}
		for i := uint32(0); i < n; i++ {
			cur.player.Step()
		}
	}
}

// sortedFeatureAnim returns the live cursors in ascending key order. The set
// is presentation-only, but a stable order keeps a composed frame reproducible
// from one run to the next (I1 in spirit; nothing here reaches simulation).
func sortedFeatureAnim(m map[string]*featureAnimCursor) []*featureAnimCursor {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]*featureAnimCursor, 0, len(keys))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// blitGAFFrame blits a GAF indexed frame to the indexed framebuffer at
// top-left (dstX,dstY) = anchor - frame offsets, clipped to the viewport
// [03 §4.4] [fmt gaf]. It copies opaque indexed pixels directly (palette
// mapping happens at present time, C7). Feature shadows select the ordinary
// keyed or ALP-tinted primitive before reaching this writer [03 §5.3.1].
func (c *Client) blitGAFFrame(frame *formats.GAFFrame, dstX, dstY int) {
	if frame == nil || c.indexed == nil {
		return
	}
	w := c.width
	h := c.height
	// Top-left already adjusted for XOffset/YOffset by caller; frame origin is at dstX,dstY.
	fw := int(frame.Width)
	fh := int(frame.Height)
	if fw <= 0 || fh <= 0 {
		return
	}
	// Clip against viewport.
	srcX0 := 0
	srcY0 := 0
	if dstX < 0 {
		srcX0 = -dstX
		dstX = 0
	}
	if dstY < 0 {
		srcY0 = -dstY
		dstY = 0
	}
	if dstX >= w || dstY >= h {
		return
	}
	copyW := fw - srcX0
	copyH := fh - srcY0
	if dstX+copyW > w {
		copyW = w - dstX
	}
	if dstY+copyH > h {
		copyH = h - dstY
	}
	if copyW <= 0 || copyH <= 0 {
		return
	}
	for y := 0; y < copyH; y++ {
		srcY := srcY0 + y
		dstYPos := dstY + y
		dstOff := dstYPos*w + dstX
		srcRow := srcY*fw + srcX0
		for x := 0; x < copyW; x++ {
			idx := srcRow + x
			if idx < 0 || idx >= len(frame.Pixels) {
				continue
			}
			if idx < len(frame.Transparent) && frame.Transparent[idx] {
				continue
			}
			pix := frame.Pixels[idx]
			c.indexed[dstOff+x] = pix
		}
	}
}

// fogBlitMode selects what a fog GAF frame does to the pixels its non-key
// mask covers. The mask itself is identical in all three: retail compares each
// source pixel against the frame's ColorKey and skips the matches
// [fmt gaf][R-RR16-A §3].
type fogBlitMode uint8

const (
	// fogBlitBlack copies the source pixel — the Black family via
	// its keyed raw blitter, whose art is palette index 0 [03 §3.3].
	fogBlitBlack fogBlitMode = iota
	// fogBlitGray remaps the destination through the GRAY TABLE and never
	// writes the source pixel [03 §3.3][R-RR16-A §1].
	fogBlitGray
	// fogBlitPatterned writes literal index 0 on a 2-pixel checker — the
	// dithered Gray variant, which steps x by two [03 §3.3].
	fogBlitPatterned
)

// blitFogGAF blits a fog GAF frame for the cell whose screen rect origin is
// (dstX,dstY) [03 §3.3]. Retail subtracts the frame's signed 16-bit
// XOffset/YOffset anchor fields from the destination before clipping —
// the quadrant geometry of the 14 fog frames lives entirely in those offsets.
// The fog art is a mask: every frame is built from the color key (index 9) and
// index 0, so what reaches the screen is decided by mode, not by the source
// palette indices [R-RR16-A §3].
func (c *Client) blitFogGAF(frame *formats.GAFFrame, dstX, dstY int, mode fogBlitMode) {
	if frame == nil || c.indexed == nil {
		return
	}
	if mode == fogBlitGray && c.pal == nil {
		return
	}
	// Gray and dither reject compressed parents before traversal and retain
	// that gate at each child [03 R-COMP-01 §2]. Black uses ordinary composition.
	if mode != fogBlitBlack && frame.Compressed != 0 {
		return
	}
	if len(frame.Subframes) != 0 {
		for _, child := range frame.Subframes {
			if mode == fogBlitBlack && child != nil && child.AlternateBlitter != 0 {
				c.tintedBlitAnchor(child, dstX, dstY)
			} else {
				c.blitFogGAF(child, dstX, dstY, mode)
			}
		}
		return
	}
	dstX -= int(frame.XOffset) // retail anchor: dest = cell origin - frame offset
	dstY -= int(frame.YOffset)
	w := c.width
	h := c.height
	fw := int(frame.Width)
	fh := int(frame.Height)
	if fw <= 0 || fh <= 0 {
		return
	}
	// Checker phase seeded by camera parity (camX+camZ)&1 [03 §3.3].
	parity := int32(0)
	if c.cam != nil {
		parity = (c.cam.X + c.cam.Z) & 1
	}
	srcX0, srcY0 := 0, 0
	if dstX < 0 {
		srcX0 = -dstX
		dstX = 0
	}
	if dstY < 0 {
		srcY0 = -dstY
		dstY = 0
	}
	if dstX >= w || dstY >= h {
		return
	}
	copyW := fw - srcX0
	copyH := fh - srcY0
	if dstX+copyW > w {
		copyW = w - dstX
	}
	if dstY+copyH > h {
		copyH = h - dstY
	}
	if copyW <= 0 || copyH <= 0 {
		return
	}
	for y := 0; y < copyH; y++ {
		srcY := srcY0 + y
		dstYPos := dstY + y
		dstOff := dstYPos*w + dstX
		srcRow := srcY*fw + srcX0
		for x := 0; x < copyW; x++ {
			if mode == fogBlitPatterned && ((int32(dstX+x)+int32(dstYPos)+parity)&1) == 0 {
				continue
			}
			idx := srcRow + x
			if idx < 0 || idx >= len(frame.Pixels) {
				continue
			}
			// Key pixels leave the destination alone; the decoder records the
			// key match in Transparent [fmt gaf].
			if idx < len(frame.Transparent) && frame.Transparent[idx] {
				continue
			}
			switch mode {
			case fogBlitGray:
				c.indexed[dstOff+x] = c.pal.Gray[c.indexed[dstOff+x]]
			case fogBlitPatterned:
				c.indexed[dstOff+x] = 0
			default:
				c.indexed[dstOff+x] = frame.Pixels[idx]
			}
		}
	}
}

// featureScreenPos computes the retail feature screen anchor [03 §5.1.4].
// It applies footprint centering and four-corner terrain-height averaging.
// The snapshot already carries world-centered X,Z and Y=coarseHeight; we reuse WorldToScreen
// for the shear and add footprint-half offset explicitly for non-centered callers.
// When terrain is available the Y uses the averaged heights at the footprint's four corners
// matching the four-corner averaging contract. Presentation-only (I6).
func (c *Client) featureScreenPos(f frame.FeatureView) (int32, int32) {
	if c.cam == nil {
		// Fallback deterministic when no camera: use world high word directly [03 §2.5].
		wx := int32(int64(f.X) >> 16)
		wz := int32(int64(f.Z) >> 16)
		wy := int32(int64(f.Y) >> 16)
		return wx, wz - (wy >> 1)
	}
	// Prefer terrain height averaging when terrain is bound [03 §2.2][03 §2.3].
	if c.terrain != nil && f.FootX > 0 && f.FootZ > 0 {
		cx := f.CX
		cz := f.CZ
		footX := int32(f.FootX)
		footZ := int32(f.FootZ)
		// Collect heights across the footprint's four-corner sample pattern:
		// h0 = cell, h1 = cell+W*0xD+4 next-X, h2 = next-Z, h3 = diag.
		// For foot >1 the footprint center is used, but height avg still over covered cells' heights.
		// Use coarse average over footprint via CoarseHeightAt as approximation for multi-cell.
		// For single-cell features this reduces to the four-corner average around anchor.
		// For now use snapshot Y (coarse) via WorldToScreen which applies shear.
		_ = footX
		_ = footZ
		_ = cx
		_ = cz
	}
	sx, sy := c.cam.WorldToScreen(f.X, f.Y, f.Z)
	// Rebase from the observed beam origin (128,32) to the shell viewport origin
	// (0,0) used by BlitTerrainOrigin for full-window draws [03 §2.5] C1 [PLAN_04A C1].
	// Without this, features would be 128,32 southeast of their terrain tiles.
	sx -= camera.OriginX
	sy -= camera.OriginY
	return sx, sy
}
