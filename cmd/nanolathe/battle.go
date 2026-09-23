package main

import (
	"fmt"
	"os"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// battleSession is the composition root for the windowed battle view. It owns
// the integrated session (all twelve kernel phases) and the interaction state:
// selection, order latch, and build placement.
type battleSession struct {
	hostPresentation                       *settings.Presentation
	incomeMinimized, incomePointerCaptured bool
	developer                              battleDeveloperState
	// Host diagnostic request/result state; synchronous writes serialize captures.
	debugCaptureBusy  bool
	debugCaptureBase  string // empty uses the per-user diagnostics directory
	debugCapturePath  string
	debugCaptureError error

	sess  *session.Session
	cat   *content.Catalog
	cam   *camera.Camera
	hud   *retailBattleHUD
	fs    vfs.FSOps
	shell *gameShell

	// Retain the original saved origin until installation knows the battle
	// surface size; clamping a provisional viewport must not lose it.
	entrySavedCamera *save.Camera

	millisSource clock.MillisSource // host millisecond source for Session.Step [01 §4.1]
	// lastTickFraction is the Enhanced blend fraction last produced while the
	// battle was running. A paused battle runs no budget, so tickFraction
	// returns this value again and the blend freezes
	// (docs/DESIGN_GPU_RENDERER.md §13.5).
	lastTickFraction float32
	// tickFiredAt is the host millisecond at which the most recent session
	// tick was released, tickFiredCarry the budget's carry just after it, and
	// tickFiredTick the global tick that was current then. The blend fraction
	// is measured from that moment (docs/DESIGN_GPU_RENDERER.md §13.5).
	tickFiredAt    uint32
	tickFiredCarry float32
	tickFiredTick  uint32
	tickFiredValid bool

	// surfaceW/surfaceH is the negotiated presentation surface the pointer and
	// the world viewport are measured against. The interface art is authored in
	// the logical 640×480 design space, but a larger display mode is neither
	// scaled nor letterboxed: the chrome extends by rule and the world viewport
	// takes `(128,32)..(W−1,H−33)` [07 R-HUD-05][03 §4.1]. The host frame
	// refreshes both words from the client surface before any input is read;
	// zero means "not negotiated yet" and reads back as the authored size.
	surfaceW, surfaceH int32

	// detail is the load-time remaster's 2x art for this battle, synthesized on
	// the loader goroutine and installed with the terrain at adoption
	// (DESIGN_GPU_RENDERER §14.3, §14.4). Nil means the client doubles every 1x
	// asset by nearest sampling, which is also what --auto-remaster=false gives.
	// Presentation-only [I6].
	detail *client.DetailArt
	// radarOptions is the battle-local minimap options word. Bit 9 admits every
	// unit contact and starts clear on each fresh battle [03 R-MM-01 §3]
	// [07 R-CAM-01 §6].
	radarOptions uint32

	// zoom is smooth zoom's state machine (DESIGN_GPU_RENDERER §16.6): the
	// factor the wheel and F9 aim at, and the ease that carries the camera
	// there on the host Update grid. It is presentation-only [I6] and is idle
	// in the classic executor, which has no free zoom.
	zoom     camera.ZoomController
	gestures battleGestures
	// communityPlacement owns the host-only CP-CON-5/6 cursor choice. The
	// selected facing deliberately survives disarming and product changes;
	// each definition is clamped only when previewed or issued.
	communityPlacement    communityPlacementState
	communityRotationMenu communityRotationMenuState
	communityOrderDrag    communityOrderDragState

	battleUI         *ui.BattleState
	returnToMenu     func(*client.Client)
	returnToSkirmish func(*client.Client)
	// restart is RESTART.GUI's presentation-only widget state. The callback is
	// installed by the frontend lifecycle owner; it tears down and starts a
	// fresh entry rather than reusing this session [08 R-CAMP-01 §8].
	restart       battleRestartState
	restartBattle func(*client.Client, battleRestartRequest)
	ended         bool

	controller *BattleController
	// modelTextures is built once at battle entry from the catalog and VFS. It
	// owns loaded-model primitive cursors and remains the session's phase-7
	// service even when no client is attached [03 R-CRD-005 §1].
	modelTextures *client.ModelTextureRegistry

	// postBattle is created once, at the first committed terminal ResultView.
	// It owns the frozen result presentation sequence; the live Session is not
	// consulted after this boundary [03 §2.4][08 R-CAMP-01 §6].
	postBattle           *session.PostBattleController
	postBattleClock      float64
	postBattleLastUnit   int64
	postBattleEffectPos  int
	postBattleGlamour    *formats.PCX
	postBattleNormalPal  *palette.Tables
	postBattleGamma      float32
	postBattleGammaSaved bool
	postBattleFadePal    palette.Tables
	postBattleFadeCur    [1024]byte
	postBattleFadeDst    [1024]byte
	postBattleFadeStep   [1024]int16
	postBattleFadeReady  bool
	postBattleFadeLevels []int

	// AppliedShake records only the last committed camera offset consumed by
	// this battle owner. The client renderer remains a pure frame reader; the
	// battle camera applies the authoritative phase-10 random walk once after
	// Session.Step, before the following draw [03 §5.6][I6].
	appliedShakeX int32
	appliedShakeY int32

	// The scroll pass's own clock [07 §10]. scrollAnchor is the previous host
	// frame's scaled reading (floor(hostMillis*30/1000), the timebase of
	// [01 §4.1]) and scrollDelta is this frame's reading minus it — the same
	// raw delta retail's tick-budget step stores and its scroll pass consumes.
	// Presentation-only [I6].
	scrollAnchor    int32
	scrollDelta     int32
	scrollAnchorSet bool

	// scrollSpeedByte caches the persisted scrollspeed byte [02 "Settings"]
	// [07 §10] C2. It is read once — at battle entry, by primeScrollSetting —
	// rather than from disk on every host frame [WU-19-114]; scrollSetting
	// falls back to loading it lazily so a battleSession built without going
	// through the composition root (tests) still resolves a real value. Zero
	// is a valid typed-command setting, so a separate bit records priming.
	scrollSpeedByte   byte
	scrollSpeedPrimed bool

	chat battleChatState

	dragScroll        camera.DragScroll
	dragScrollActive  bool
	dragScrollStepped bool
	// The active GUI is serviced once before residual battle input [07 §3].
	paletteFrameServiced bool
	palettePointerOwned  bool
	dragScrollLastX      int32
	dragScrollLastY      int32

	// switchAlt is captured once when the battle installs its settings. It is
	// presentation input state only; routeDigit reads this cached bit rather
	// than opening the settings file on a keypress [07 R-CAM-01 §4][I6].
	switchAlt bool
	// clockVisible is the presentation-only stand-alone clock switch. A shell
	// mirrors the persisted process setting; a direct battle keeps its loaded
	// copy here [07 R-CAM-01 §6][I6].
	clockVisible bool
	bpsVisible   bool
	// fpsVisible is the direct-battle fallback for the host-only +fps display.
	fpsVisible bool
	// clockUsePrimaryFont records the stateful FNT selection at the retail
	// clock draw site for a direct battle. A shell-backed battle reads its live
	// text-line setting because MAXLINES may change it while battle is running.
	clockUsePrimaryFont bool
	// showRanges is a process-lifetime presentation toggle, retained by the
	// shell across battles and never written to settings [07 R-CAM-01 §6].
	showRanges bool
	// Placement guides are presentation preferences only (GPU design §20).
	rangePreferences contentprofiles.Presentation

	// interfaceType is the persisted LEFTCLICK stage for a direct battle. A
	// frontend-backed battle reads the shell's live copy instead, so an
	// in-battle options change reaches the next pointer event without disk I/O
	// [07 R-CAM-01 §5][07 R-CAM-01 §7].
	interfaceType         int
	resourceQueueFeedback *resourceQueueFeedback // transient modern queue overlay; DESIGN_INTERFACE_HUD_INPUT §3.10
	modernDrag            *battleCommandDrag     // modern command capture; DESIGN_INTERFACE_HUD_INPUT §3.11
	resourceClick         *resourceClick         // modern resource gesture receipt; DESIGN_INTERFACE_HUD_INPUT §3.10
	// gammaSetting retains the direct battle's write-all value independently
	// of the command's immediate display factor [07 R-CAM-01 §6].
	gammaSetting int

	// The footer's pointer record [07 R-HUD-03 §1]. Both words are
	// presentation-only: the simulation neither writes nor reads them [I6].
	// footerHoverUnit is rewritten only while the pointer is inside the view
	// with no drag rectangle armed, or over the minimap; anywhere else — the
	// side rail, the top or bottom strip — it keeps its previous value, so a
	// unit hovered on the way to the panel stays in the footer.
	// footerHoverFeature is recomputed every frame wherever the pointer is.
	footerHoverUnit    pool.Handle
	footerHoverFeature string

	// minimapCameraCaptured is the presentation-only minimap camera gesture.
	// It is set by the admitted minimap down edge, served from the following
	// host frame's pointer record, and cleared only by that button's up edge
	// [07 R-CAM-01 §5][07 R-CAM-01 §11]. Keeping it here rather than in the
	// transient mouse sample means a drag remains captured after it leaves the
	// radar rectangle.
	minimapCameraCaptured      bool
	minimapCameraCaptureButton input.MouseButton

	// cl is the presentation client installed by installBattleClient. Retail
	// has one message ring, and the client owns the instance the composer
	// draws and the options page configures (ConfigureMessageLines); this
	// reference is how the battle shell's hotkeys (F3, F12) and the
	// game-speed announcement reach that same ring instead of holding a
	// second one [07 R-HUD-03 §14.3].
	cl *client.Client

	// panelHoldFlag is interface-flags bit 0x80. It has exactly two readers:
	// the score panel's showing test, and the kill-credit finalize's flash arm
	// [07 R-HUD-04 §1][07 R-CAM-01 §14]. Presentation-only [I6].
	panelHoldFlag bool

	// visitedUnits and currentUnit are the `n` unit cycle's state
	// [07 R-CAM-01 §2]. Retail keeps the visited bits in each unit's status
	// word; presentation may not write simulation state, so the cycle keeps its
	// own set here [I6]. The set is only looked up, never ranged, so it takes
	// part in no order-producing iteration [I1].
	visitedUnits       map[pool.Handle]bool
	deferFollowInput   bool
	pendingFollowInput func()
	currentUnit        pool.Handle
	// communityBuilderCursor and communityFactoryCursor are the last selected
	// slots for the optional host-side idle cycles. Zero means no previous hit;
	// the committed-frame walk advances past the cursor and wraps once.
	communityBuilderCursor pool.Handle
	communityFactoryCursor pool.Handle
}

func (b *battleSession) fpsShown() bool {
	if b == nil {
		return false
	}
	if b.shell != nil {
		return b.shell.fpsVisible
	}
	return b.fpsVisible
}

// countedClickDelta is the signed count the counted build-page producer takes
// from a click: +1 for a plain left click, +5 for Shift+left, -1 for a plain
// right click and -5 for Shift+right [07 R-P0-11 §1]. Shift scales the count —
// it is not a queue mode — and the producer never purges.
func countedClickDelta(shift, rightClick bool) int {
	count := 1
	if shift {
		count = 5
	}
	if rightClick {
		count = -count
	}
	return count
}

// factoryBuildDelta retains the signed count [07 R-P0-11 §1] and adds the
// requested Alt batch of twenty (DESIGN_INTERFACE_HUD_INPUT §5).
func factoryBuildDelta(modifiers input.Modifiers, rightClick bool) int {
	if !modifiers.Alt {
		return countedClickDelta(modifiers.Shift, rightClick)
	}
	count := 20
	if rightClick {
		count = -count
	}
	return count
}

// stockpileClickDelta is the same counted producer for a MAKENUKE/MAKEANTI toy.
// The Alt batch of twenty is deliberately NOT applied here: the divergence in
// DESIGN_INTERFACE_HUD_INPUT §5 scopes itself out of the stockpile toys, so a
// stockpile click keeps retail's ±1/±5 [07 R-P0-11 §1].
func stockpileClickDelta(modifiers input.Modifiers, rightClick bool) int {
	return countedClickDelta(modifiers.Shift, rightClick)
}

var clPtr *client.Client

// runBattleView launches the windowed battle view over the real session.
func runBattleView(opts Options, cs *contentSet) error {
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		return err
	}
	shell.settingsWritable = true
	defer shell.teardownBattle(cl)
	return ebitenapp.Run(cl, rendererMode(shell.opts), shell.windowOptions())
}

// newDirectBattleView skips menu navigation, but retains the same shell owner
// for dialogs and battle replacement as menu entry. Both the update callback
// and interpolation producer follow the current battle after loading a save.
func newDirectBattleView(opts Options, cs *contentSet) (*gameShell, *client.Client, error) {
	saved := loadedSettings()
	opts.Gameplay = startupGameplay(opts, saved.Gameplay)
	request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return nil, nil, err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return nil, nil, err
	}
	shell, err := newGameShell(opts, cs)
	if err != nil {
		return nil, nil, err
	}
	shell.applySettings(saved)
	if err := validatePresentationZoom(shell.opts); err != nil {
		return nil, nil, err
	}
	// Direct entry retains its own skirmish setup, including the pool limit
	// the save Summary writes, rather than the last menu game's preferences.
	shell.setup = authoritative.Session.Skirmish
	var cl *client.Client
	cl, err = client.New(client.Options{
		Buffer: authoritative.Session.Snapshot,
		Width:  shell.display.Width,
		Height: shell.display.Height,
		Title:  "Nanolathe — " + opts.Map,
		Step:   func(delta float64) { shell.step(delta, cl) },
		TickFraction: func() float32 {
			if shell.battle == nil {
				return 0
			}
			return shell.battle.tickFraction()
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("nanolathe: client: %w", err)
	}
	// The presentation model cache is typed on the concrete overlay, so it
	// reads objects3d, textures and anims unmapped. That is correct only
	// while no content profile renames those three, which a test in this
	// package asserts (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	cl.SetModelFS(cs.unmappedMount)
	cursors, err := client.LoadCursors(cs.fs)
	if err != nil {
		return nil, nil, err
	}
	cl.SetCursors(cursors)
	clPtr = cl
	// There is no loading screen on direct entry. Prepare the same optional
	// detail art before the shell adopts the completed battle.
	sess := authoritative.Session
	shell.pendingDetail = detailArtFor(shell.opts, cs, sess.World, nil)
	if err := shell.enterBattle(sess, sess.Catalog); err != nil {
		return nil, nil, err
	}
	return shell, cl, nil
}

// rendererMode maps the --renderer flag to the platform executor selection. Any
// value other than "modern" — including the empty string and any typo — selects
// the classic executor (docs/DESIGN_GPU_RENDERER.md §2.4,
// §2.5).
func rendererMode(opts Options) ebitenapp.RendererMode {
	if opts.Renderer == "modern" {
		return ebitenapp.RendererModern
	}
	return ebitenapp.RendererClassic
}

// windowRunOptions carries the host-side window settings to the adapter. The cap is
// a presentation setting only: the simulation never observes how often the
// window presents [I6].
func windowRunOptions(opts Options) ebitenapp.RunOptions {
	fps := opts.FPS
	if fps < 0 {
		fps = 0
	}
	return ebitenapp.RunOptions{MaxFPS: fps, Stats: opts.Stats}
}

// composeBattleEntryWithDetail is the single presentation composition for
// every constructed battle session. Session construction has already completed
// the authoritative entry tail's tick-zero per-player priming and second
// resource grant [08 R-ENTRY-01 §8]; this helper does not change which authored
// HUD surfaces the existing battle loader provides.
//
// `detail` is the load-time remaster's art, which the route that owns the load
// has already synthesized (DESIGN_GPU_RENDERER §14.4 "When"). It reaches the
// client at the same grouped adoption that installs the terrain, and is
// cleared with it.
func composeBattleEntryWithDetail(sess *session.Session, cat *content.Catalog, cs *contentSet, cl *client.Client, shell *gameShell, detail *client.DetailArt) (*battleSession, error) {
	b, err := composeBattleEntryDetached(sess, cat, cs, shell, nil)
	if err != nil {
		return nil, err
	}
	b.detail = detail
	if cl != nil {
		installBattleClient(cl, b)
	}
	if b.hud != nil && b.hud.windowContext != nil {
		b.hud.windowContext.completeTransition()
	}
	return b, nil
}

// composeBattleEntryDetached performs every fallible presentation
// construction without touching the active client or frontend. A saved
// camera is applied only to this candidate and is installed at the same
// render-thread commit as the rest of the battle presentation [08
// R-SAVE-02 §11–§12].
func composeBattleEntryDetached(sess *session.Session, cat *content.Catalog, cs *contentSet, shell *gameShell, savedCamera *save.Camera) (*battleSession, error) {
	if sess == nil {
		return nil, fmt.Errorf("nanolathe: battle composition failed: no session")
	}
	terrain := sess.World
	if terrain == nil {
		return nil, fmt.Errorf("nanolathe: battle composition failed: the session carries no terrain")
	}
	pal, err := loadPaletteStrict(cs)
	if err != nil {
		return nil, err
	}

	terrainW := int32(terrain.CellW * 16)
	terrainH := int32(terrain.CellH * 16)
	// Camera clamp uses the same playable insets consumed by minimap input and
	// marker projection; raw terrain extents include the void margins [07 §10].
	cam := camera.NewFromTerrain(terrainW, terrainH, terrain.PlayRight, terrain.PlayBottom, retailScreenW, retailScreenH)
	// The world rebuild resets the camera block before any battle-start writer
	// runs [08 R-ENTRY-01 §3 step 12]. The reset zeroes twenty-three
	// consecutive words of that block and writes the scroll-setting byte back;
	// the block spans the **current origin** and the **desired origin** as well
	// as the tracked object, follow target, bookmarks and hold state, so after
	// it `current = desired = (0, 0)` [07 R-CAM-01 §14 "the camera-block reset
	// leaves both origins at (0, 0)"]. §10's earlier list omitted the two
	// origins, which is why this used to be a bare pan that left the desired
	// origin alone. A campaign without a start-position special keeps (0, 0)
	// as both origins [08 "Campaign camera"].
	cam.JumpTo(0, 0)
	if savedCamera != nil {
		applyRetailSavedCamera(cam, savedCamera)
	} else {
		centerBattleStartCamera(sess, cam)
	}

	// The battle HUD is mandatory retail content: side-selected PANELTOP,
	// PANELSIDE, PANELBOT, the 30 SIDEDATA anchors, side fonts, and the authored
	// general command page [07 §6][07 §9].
	hud, err := loadRetailBattleHUD(cs.fs, sess, cat, pal, shell, newBattleWindowContext(cs, shell))
	if err != nil {
		return nil, err
	}
	b := &battleSession{
		sess: sess, cat: cat, cam: cam, hud: hud, fs: cs.fs, shell: shell,
		showRanges: cs.presentation.ShowRanges, rangePreferences: cs.presentation,
		millisSource: newMonotonicMillisSource(), battleUI: ui.NewProductionBattleState(),
	}
	if savedCamera != nil {
		saved := *savedCamera
		b.entrySavedCamera = &saved
	}
	restoreStart := len(sess.World.FeatureDefs)
	if sess.Features != nil {
		restoreStart = sess.Features.DefinitionRestoreStart()
	}
	b.modelTextures, err = client.NewModelTextureRegistry(cs.unmappedMount, cat, sess.World, restoreStart)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: battle composition failed: model textures: %w", err)
	}
	sess.SetPhase7Service(b.modelTextures)
	sess.SetFragmentMaterialResolver(b.modelTextures.FreezeFragmentMaterial)
	if sess.Features != nil {
		sess.Features.SetDefinitionAdmissionObserver(b.modelTextures.AdmitFeatureDefinition)
	}
	// Prime the scroll-speed cache once here, at battle entry, instead of
	// leaving the first camera-pan frame to fault it in lazily [WU-19-114].
	b.primeScrollSetting()
	// Rail detent cues are emitted by canonical UI state; this callback only
	// adapts the authored cue to the session audio sink [07 §6][I6].
	b.battleUI.SetPanelCue(func(name string) {
		if sess.Audio != nil {
			_ = sess.Audio.PlayUICue(name)
		}
	})
	return b, nil
}

// applyRetailSavedCamera loads the `Camera` account's origin. The load is a
// *jump*, not a glide: retail reads `X Position` / `Z Position` with the
// current origin as each default, writes them to the current origin, sets the
// view-dirty bit, clamps, copies current into desired, and clears the terrain
// cache-valid bit [07 R-CAM-01 §14 "a saved-camera load is a jump"]. Camera.
// JumpTo is exactly that write-clamp-copy, so the load no longer leaves a
// stale desired origin behind the restored one.
//
// The two "state bits" every camera writer touches are the view-dirty bit and
// the terrain view-cache-valid bit; neither is authored or saved, and a build
// that rebuilds the view every frame — this one — needs nothing beyond
// `desired := current`. That is what closes the old marker here.
func applyRetailSavedCamera(cam *camera.Camera, saved *save.Camera) {
	if cam == nil || saved == nil {
		return
	}
	cam.JumpTo(saved.XPosition, saved.ZPosition)
}

// installBattleClient is the non-fallible render-thread half of battle
// adoption. Resource construction and authoritative restoration have already
// succeeded before this grouped setter sequence runs [I6].
func installBattleClient(cl *client.Client, b *battleSession) {
	if cl == nil || b == nil || b.sess == nil {
		return
	}
	b.cl = cl
	p := loadedSettings().Presentation
	b.hostPresentation = &p
	applyCommunityHUDOptions(cl, b.hostPreferences())
	cl.SetTrailStrength(b.hostPreferences().TrailStrength)
	b.placeEntryCamera(cl.Size())
	// Every successful battle rebuild, including a load, empties the visible
	// message span before old source handles can be reused [08 R-ENTRY-01 §3].
	cl.MessageRing().Clear()
	cl.SetModelTextureRegistry(b.modelTextures)
	// The session executor owns one message-ring retirement per host pump;
	// the client remains the sole presentation owner of the ring itself
	// [01 R-PLAT-02 §§7,8][I6]. Binding this callback leaves unrelated optional
	// post-loop diagnostics installed by a caller intact.
	bindBattleMessageRetirement(b.sess, cl)
	cl.SetSnapshot(b.sess.Snapshot)
	// Restore pause truth before input can run; the first draw is not an
	// initialization boundary [08 "Scheduler and random state in saves"][I6].
	paused := b.sess.Clock != nil && b.sess.Clock.Paused
	b.battleState().SetPauseTruth(paused)
	cl.SetPresentationPaused(paused)
	cl.SetTerrain(b.sess.World)
	// The detail-art provider is installed with the terrain it belongs to and
	// cleared by the SetTerrain(nil) of teardown (DESIGN_GPU_RENDERER §14.3).
	cl.SetDetailArt(b.detail)
	cl.SetCamera(b.cam)
	cl.SetPalette(b.hud.pal)
	cl.SetFNT(b.hud.console)
	// The later message column selects COMIX; group digits retain the side
	// console face [07 R-HUD-03 §14.4][03 R-FX-01 §6A].
	cl.SetMessageFNT(b.hud.primaryFont)
	cl.SetDeveloperFont(b.hud.developerFont)
	b.syncDeveloperView()
	cl.SetMessageLogos(b.hud.logos)
	// Strategic icons use the HUD team logos; generic contacts retain the radar
	// art/options bindings (DESIGN_GPU_RENDERER §18.4).
	icons, iconErr := configuredStrategicIcons(b.cat, b.hostPreferences().StrategicIconConfig)
	if iconErr != nil {
		fmt.Fprintln(os.Stderr, iconErr)
	}
	cl.SetStrategicIconCatalog(icons)
	cl.SetHoverScripts(b.cat)
	cl.SetStrategicBlipArt(b.hud.radarBlipGAF)
	if b.hud.logos != nil {
		teamArt, _ := b.hud.logos.Find(sideLogoEntry)
		cl.SetStrategicTeamArt(teamArt)
	}
	cl.SetRadarOptions(b.radarOptions)
	s := loadedSettings()
	gamma := s.Display.Gamma
	if b.shell != nil {
		gamma = b.shell.display.Gamma
	}
	b.gammaSetting = gamma
	applyGammaOption(cl, gamma)
	applyBattleAudioOptions(b, s)
	applyDamageBarsSetting(s)
	// A shell already holds the startup settings block and may carry its live
	// value into a new battle. A direct --map battle has no shell, so its one
	// install-time settings read supplies the same bit.
	b.applySwitchAltSetting(s)
	b.applyClockSetting(s)
	b.applyInterfaceTypeSetting(s)
	// `textlines`/`textscroll` configure the message ring and `screenchat`
	// sets its class filter; retail's startup loader installs these the same
	// way it installs damagebars [02 §3][07 R-HUD-03 §14.3][07 R-HUD-03 §14.4].
	applyMessageLineSettings(cl, s)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	// The presentation effect pool needs each admitted effect's authored
	// per-frame holds, which live in the GAF entry the event names — an asset
	// the client owns and the session does not [06 R-WFX-01 §1][03 §1]. This
	// is the shell filling that seam with the same bank cache the draw pass
	// resolves frames through, so the cursor the pool advances and the frame
	// the composer blits can never come from two different readings of one
	// entry.
	b.sess.SetEffectTimingResolver(func(e render.Event) (render.FrameTiming, bool) {
		return cl.EffectFrameTiming(e.AssetID, e.Graphic)
	})
	// The two AUTHORITATIVE readings of that same art — a smoke puff's last
	// frame against its bound entry's frame count [03 R-STRIP-01 §2], and a
	// burning feature's frame geometry and its die/reclaim/burn lifetime in
	// visits [05 R-FEAT-01 §10] — are deliberately NOT bound here. Content
	// compiles them from the battle's VFS and the session's own composition
	// installs them, so a headless battle and this one are the same simulation.
	// The shell binds presentation and nothing else.
	//
	// The warm pass below prepares every battle-reachable event sequence in
	// the client's PIXEL cache off the draw path. The terrain table already
	// includes mission and restored features; the client closes it over all
	// catalog unit corpses and feature successors before any draw can use it.
	if b.sess.Catalog != nil {
		cl.WarmBattleFeatureSequences(b.sess.Catalog, b.sess.World.FeatureDefs)
	}
	attachBattleAudio(cl, b.sess, b.fs)
	prefs := s.Audio
	unitChatText := s.Messages.UnitChatText
	if b.shell != nil {
		prefs = b.shell.audioPrefs
		unitChatText = b.shell.messages.UnitChatText
	}
	// The eight-entry voice queue arbitrates with the persisted `speechfx` bit
	// and the two acknowledgement levels, so battle setup installs them on the
	// session's own service — the direct map/capture path has no shell to have
	// done it already [03 §8.3][03 R-AUD-01 §2][07 R-CAM-01 §7].
	applyRetailVoiceGates(b.sess.Audio, prefs, unitChatText)
	music := b.sess.Audio.Music
	music.SetVolume(prefs.MusicVol)
	music.SetEnabled(prefs.MusicMode != 0)
	music.Configure(audio.PlayMode(prefs.CDMode), music.DesiredCategory())
	// Battle entry requests Building music [03 R-AUD-01 §5]. Device
	// creation can follow installation, so playback starts on the host pump.
	b.sess.Audio.StartBattleMusic()
}

// applyBattleAudioOptions supplies the presentation output configuration at
// battle installation. A frontend carries its live selection into battle; a
// direct map/capture path reads the persisted block here, before the platform
// creates its PCM output [03 R-AUD-01 §2][I6].
func applyBattleAudioOptions(b *battleSession, s settings.Settings) {
	if b != nil && b.shell != nil {
		applyRetailAudioOptions(b.shell.audioPrefs)
		return
	}
	applyRetailAudioOptions(s.Audio)
}

func bindBattleMessageRetirement(sess *session.Session, cl *client.Client) {
	if sess == nil || cl == nil {
		return
	}
	sess.BindMessageRetirement(cl.MessageRing().RetireOne)
}

// teardown is the one idempotent battle-exit boundary. Presentation joins are
// detached before authoritative references are dropped, and every interaction
// latch is reset so a later load cannot reach the old battle [08 "Session
// states"][08 R-ENTRY-01 §8][I6].
func (b *battleSession) teardown(cl *client.Client) {
	if b == nil {
		return
	}
	// LoadGame can leave ENDMSN through replacement rather than its Start or
	// MainMenu routes. Retire the same temporary display state on every exit.
	if b.postBattle != nil {
		b.restorePostBattlePalette(cl)
	}
	b.endDragScroll(cl)
	// The unit information screen is a battle child window held in one package
	// global, so a battle left through RESTART, a campaign continuation or a
	// load would otherwise hand the next battle a window built from the old
	// battle's definition and art — one that keeps swallowing pointer input
	// under its rectangle until `DONE` is clicked [07 §3][07 R-HUD-04 §3].
	closeUnitInfo()
	if b.sess != nil {
		// The score teardown also runs for manual exits [08 R-CAMP-01 §7].
		b.sess.CommitCampaignTeardown()
		b.sess.SetPhase7Service(nil)
		b.sess.SetFragmentMaterialResolver(nil)
		b.sess.SetPublicationObserver(nil)
		if b.sess.Features != nil {
			b.sess.Features.SetDefinitionAdmissionObserver(nil)
		}
	}
	if b.battleUI != nil {
		b.closeBattleMenu()
		b.battleUI.SetPanelCue(nil)
		b.battleUI.ResetInteraction()
	}
	if cl != nil {
		cl.SetStrategicIconCatalog(nil)
		cl.SetHoverScripts(nil)
		cl.SetStrategicBlipArt(nil)
		cl.SetStrategicTeamArt(nil)
		cl.SetModelTextureRegistry(nil)
		cl.SetMessageLogos(nil)
		cl.SetUIStage(nil)
		cl.SetAudioService(nil)
		cl.SetTerrain(nil)
		cl.SetCamera(nil)
		cl.SetPalette(nil)
		cl.SetFNT(nil)
		cl.SetSnapshot(&frame.Buffer{})
		if in := cl.Input(); in != nil {
			*in = *input.NewState()
		}
		// The parallel record pool is the one thing in the client that owns
		// goroutines, and each worker holds a clone of the whole client with
		// its grown scratch arenas. Retiring it with the rest of the battle
		// state releases those between battles; the next recorded frame starts
		// a fresh pool. Safe here because teardown never runs beside a frame.
		cl.Close()
	}
	detachBattleAudio(cl, b.sess)
	if b.shell == nil && b.sess != nil && b.sess.Audio != nil {
		// A direct process exit has no continuing shell to service the fade.
		b.sess.Audio.Close()
	}
	if b.controller != nil {
		b.controller.battle = nil
	}
	b.ended = true
	b.sess = nil
	b.cat = nil
	b.cam = nil
	b.hud = nil
	b.fs = nil
	b.cl = nil
	b.shell = nil
	b.battleUI = nil
	b.controller = nil
	b.modelTextures = nil
	b.returnToMenu = nil
	b.returnToSkirmish = nil
}

// configWithBattleSeeds is the composition boundary for skirmish setup. It
// asks the source exactly once and copies that pair into the config consumed
// by the session constructor [R-CORE-02].
func configWithBattleSeeds(cfg session.SkirmishConfig, source BattleSeedSource) session.SkirmishConfig {
	if source == nil {
		return cfg
	}
	seeds := source.NextBattleSeeds()
	cfg.RNGSimSeed = uint32(seeds.Simulation)
	cfg.RNGCrtSeed = seeds.CRT
	return cfg
}

// setSurfaceSize records the negotiated presentation surface for this host
// frame. The battle chrome is not scaled to a larger display mode — it extends
// by rule and the world viewport takes what is left — so every pointer test
// below reads the live size rather than the authored one [07 R-HUD-05].
func (b *battleSession) setSurfaceSize(w, h int32) {
	if b == nil || w <= 0 || h <= 0 {
		return
	}
	b.surfaceW, b.surfaceH = w, h
	// The chrome's own layout is derived at draw time, but the world-region
	// test the click path runs reads it too; laying it out here keeps the
	// first host frame's pointer on the same viewport the composer will paint
	// [07 R-HUD-05]. Repeated calls at an unchanged size are a no-op.
	b.hud.applyDisplaySize(int(w), int(h))
}

// surfaceSize returns the negotiated surface, falling back to the authored
// design space before the first host frame has reported one [07 §1].
func (b *battleSession) surfaceSize() (w, h int32) {
	if b == nil || b.surfaceW <= 0 || b.surfaceH <= 0 {
		return retailScreenW, retailScreenH
	}
	return b.surfaceW, b.surfaceH
}

// pointerSample is the one production expression that turns the client edge's
// state into a host-frame sample. It exists so the pointer clamp and the world
// mapping below cannot disagree about which surface they are on.
func (b *battleSession) pointerSample(in *input.State, delta float64) input.Sample {
	w, h := b.surfaceSize()
	return input.SampleFromState(in, delta, w, h)
}

// tickFraction is the Enhanced blend's fraction producer: how much of the next
// authoritative tick has already elapsed, read at Draw time by the client
// (docs/DESIGN_GPU_RENDERER.md §13.5).
//
// The scheduler's own time source is the scaled timebase
// floor(milliseconds × 30 / 1000) [01 §4.1], so its delta is a whole number of
// thirtieths and the budget's carry never resolves a position inside a tick: at
// the nominal speed it is identically zero after every step. The fraction is
// therefore that same millisecond source read un-floored — the source the tick
// budget and the scroll pass already sample [07 §10], not a second clock. With
// `phase = (milliseconds × 30 mod 1000) / 1000` the elapsed part of the current
// scaled unit and `eff` the clock's effective speed (active × 0.1 [01 §4.2]),
// the fraction is `carry + phase × eff`; the client clamps it into [0, 1),
// which is what bounds the doubled speeds where the phase alone can pass one.
//
// While paused the budget does not run, so the value last returned unpaused is
// returned again and the blend is frozen.
func (b *battleSession) tickFraction() float32 {
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return 0
	}
	if b.sess.Clock.Paused {
		return b.lastTickFraction
	}
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	if !b.tickFiredValid {
		return 0
	}
	// Ticks are released only inside the window's 30 Hz Update, whose timing
	// drifts against the scaled timebase's own phase, so the fraction is the
	// time since the most recent tick actually fired, not the phase of the
	// wall clock: elapsed × 30 × the effective speed (active × 0.1 [01 §4.2])
	// is how much of the next tick the budget has accrued since, on top of
	// the carry it kept at the fire. Measured from the wall phase instead, an
	// Update landing just before the phase wrapped released no tick while the
	// phase reset, which slid every blended pose back toward the previous
	// tick and then jumped it two ticks forward — the piece jiggle of §13.5.
	// The clamp at one holds the current pose when an Update releases
	// nothing; the blend never moves backwards within one tick.
	active := b.sess.Clock.Active
	if active < 1 {
		active = 1
	} else if active > 20 {
		active = 20
	}
	elapsed := float32(b.millisSource.Millis32()-b.tickFiredAt) / 1000
	f := client.ClampTickFraction(b.tickFiredCarry + elapsed*30*float32(active)*0.1)
	b.lastTickFraction = f
	return f
}

// noteTickTiming records the moment a session step released a tick. It runs
// right after every session step, in the same 30 Hz Update the ticks fire in,
// and stamps the host millisecond only when the global tick moved, so the
// fraction above measures from the last real tick and not from the step.
func (b *battleSession) noteTickTiming() {
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	tick := b.sess.Clock.GlobalTick
	if b.tickFiredValid && tick == b.tickFiredTick {
		return
	}
	if b.millisSource == nil {
		b.millisSource = newMonotonicMillisSource()
	}
	b.tickFiredAt = b.millisSource.Millis32()
	b.tickFiredCarry = b.sess.Clock.Carry
	b.tickFiredTick = tick
	b.tickFiredValid = true
}

// viewerStep runs one rendered frame: input → session ticks → camera pan.
func (b *battleSession) viewerStep(delta float64, cl *client.Client) {
	if b == nil || cl == nil {
		return
	}
	if b.stepArrival(delta, cl) {
		return
	}
	// End input gestures before any modal or accelerator consumes their key
	// edges. Returning to battle must not reinterpret an earlier click.
	if !cl.IsFocused() || b.isResultVisible() || b.battleState().Modal() != ui.BattleModalClosed || b.isTalkGUIActive() || unitInfoOpen() {
		b.modernDrag = nil
		b.resourceClick = nil
		b.resourceQueueFeedback = nil
	}
	resourceInputServiced := false
	defer func() {
		if !resourceInputServiced {
			b.modernDrag = nil
			b.resourceClick = nil
			b.resourceQueueFeedback = nil
		}
	}()
	// Losing the camera pass (modal, result, consumed input) cancels a pinch.
	// Its remaining events must not reactivate it when the viewport returns.
	gesturesServiced := false
	defer func() {
		if !gesturesServiced {
			b.gestures = battleGestures{}
		}
	}()
	if b.developer.host.enabled {
		started := time.Now()
		defer b.sampleDeveloperHost(started)
	}
	defer b.syncDeveloperView()
	if b.handleDebugCapture(cl) {
		return
	}
	if b.shell == nil && cl.IsFocused() && b.sess != nil && b.sess.Audio != nil && b.sess.Audio.Music != nil {
		serviceMusic(b.sess.Audio)
	}
	b.dragScrollStepped = false
	if b.dragScrollActive && (!cl.IsFocused() || b.isResultVisible() || b.battleState().Modal() != ui.BattleModalClosed) {
		// TODO(T25): release native relative capture when another screen takes
		// ownership; the host cannot preserve retail's native warp sequence.
		b.endDragScroll(cl)
	}
	// The client owns the surface size; the battle follows it before reading
	// the pointer, so a mode change reaches the placement path on the same
	// frame it reaches the composer [07 R-FE-01 §11][07 R-HUD-05].
	if w, h := cl.Size(); w > 0 && h > 0 {
		b.setSurfaceSize(int32(w), int32(h))
	}
	// The client composes the drag frame after world/fog and before the HUD;
	// this deferred bridge mirrors the input-owned gesture after every early
	// return as well as the normal controller path [03 §1][R-SEL-02A].
	defer b.syncSelectionDrag(cl)
	in := cl.Input()
	producerIn := in
	// The command palette owns its ordered accelerator peek before the battle
	// controller converts the host state into a sample. Samples deliberately
	// do not carry the token ring, so doing this below controller.Step loses
	// every producer token [07 R-WGT-01 §§1-3][07 R-WGT-02 §5].
	b.cl = cl
	// Advance the canonical panel state during the host-frame update. Drawing
	// must remain a pure read of this state so hit testing and raster placement
	// use the same offset [07 §6][I6].
	// The rail slide's test is Space alone, unless a text editor has the focus
	// [07 §6]. Interface-flags bit 0x80 is *not* a term of it: that bit has
	// exactly two readers, the score panel's showing test and the kill-credit
	// finalize's flash arm [07 R-HUD-04 §1][07 R-CAM-01 §14]. It used to be
	// ORed in here, which pinned the rail open as well as the panel.
	spaceHeld := in != nil && in.Kbd != nil && in.Kbd.KeyHeld(input.KeySpace)
	editorFocused := b.isTalkGUIActive() || b.hud != nil && b.hud.editorFocused()
	b.battleState().AdvancePanelNow(spaceHeld, editorFocused)
	// End-mission presentation takes ownership of the frame once the
	// authoritative result is latched. The authored result panel owns any
	// release-inside gesture; no battle hotkey or world command leaks through
	// [07 §3][07 §11].
	if b.isResultVisible() {
		if b.postBattle == nil && b.sess != nil && b.sess.Audio != nil {
			b.sess.Audio.EndBattleMusic()
		}
		// The first terminal frame freezes the result and installs the one
		// post-battle controller. From here on the controller and its effect
		// cursor own the presentation sequence; no live-world value is read
		// [03 §2.4][08 R-CAMP-01 §6].
		b.ensurePostBattleController()
		b.stepPostBattle(delta, in, cl)
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	state := b.battleState()
	// Modal ownership is decided at frame entry. Closing ARMOPT with Tab or
	// Escape must not hand the same frame's remaining mouse/key edges to the
	// battle controller [07 §2][07 §3].
	modalAtFrameStart := state.Modal() != ui.BattleModalClosed
	shortcutKeyboard := battleShortcutKeyboard(in)
	keyDown := func(key input.Key) bool {
		return shortcutKeyboard != nil && shortcutKeyboard.KeyDown(key)
	}
	// F2 is the options window's own key, and Tab toggles it in battle mode;
	// Escape only ever closes it. The "ESC bit" name survives because Escape is
	// the bit's clearer, but token 0xE3 is F2, not Escape
	// [07 R-CAM-01 §2 "Escape versus F2"].
	if modalAtFrameStart {
		pendingBeforeModal := in.PendingTokens()
		defer func() {
			// A modal's unclaimed event has no battle action, but must still
			// retire before the next queued key can be serviced [07 §2].
			if pendingBeforeModal != 0 && in.PendingTokens() == pendingBeforeModal {
				in.DiscardTokens(1)
			}
		}()
		// Inspect the first queued event without stealing it from a child.
		modalInput := *in
		modalInput.ShortcutTokenMode = in.ShortcutTokenMode || in.PendingTokens() != 0
		if tokens := in.PeekTokens(); len(tokens) != 0 {
			modalInput.ShortcutToken = tokens[0]
		}
		shortcutKeyboard = battleShortcutKeyboard(&modalInput)
		// Tab is the authored root-modal toggle. Handle it before the modal
		// dispatcher so the same edge cannot also activate a newly closed/opened
		// window. Escape remains owned by the modal handler (which applies its
		// back transition), but the whole frame is consumed either way.
		//
		// A child window over `ARMOPT` owns the pass while it is up — the
		// options root and the save/load dialog both do — so the toggle is
		// suppressed for as long as one is open and the modal dispatcher
		// routes the frame to it instead [07 R-WGT-01 §1][07 R-FE-01 §6].
		if (keyDown(input.KeyTab) || keyDown(input.KeyF2) && !shortcutKeyboard.KeyHeld(input.KeyCtrl)) && state.Modal() == ui.BattleModalOptions &&
			!b.battlePrefsActive() && !(b.shell != nil && (b.shell.saveLoadPanelActive() || b.shell.frontend.Panels.Modal() != nil)) {
			in.DiscardTokens(1)
			b.closeBattleMenu()
		} else {
			b.handleBattleMenuInput(in, cl)
		}
		if b.ended {
			return
		}
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	// An already-open TALK window owns its entire frame before ordinary battle
	// children. Otherwise UNITINFO and the command palette receive their usual
	// ordered peek, and only an Enter left unclaimed by both opens TALK
	// [07 §3][07 §5 "Chat"].
	talkOwned := b.chat.active
	tokenClaimed := false
	unitInfoAtFrameStart := false
	b.paletteFrameServiced = true
	b.palettePointerOwned = talkOwned
	defer func() {
		b.paletteFrameServiced, b.palettePointerOwned = false, false
		b.chat.ownsFrame = false
	}()
	if talkOwned {
		b.serviceTalk(in)
	} else {
		unitInfoAtFrameStart = unitInfoOpen()
		tokensBeforeChild := in.PendingTokens()
		b.serviceUnitInfoKeyboard(in)
		tokenClaimed = in.PendingTokens() < tokensBeforeChild
		b.palettePointerOwned = unitInfoAtFrameStart && !unitInfoOpen()
		if b.hud != nil && !unitInfoAtFrameStart {
			result, owned := b.hud.servicePaletteFrame(b, in, true)
			b.palettePointerOwned = owned
			tokenClaimed = result.ConsumedTokens > 0
		}
		if !tokenClaimed && talkOpenToken(in) && b.openTalk(in, cl) {
			talkOwned = true
			b.palettePointerOwned = true
		}
	}
	if talkOwned || tokenClaimed || unitInfoAtFrameStart || b.palettePointerOwned {
		b.modernDrag = nil
		b.resourceClick = nil
		b.resourceQueueFeedback = nil
	}
	if !talkOwned {
		in = battleTokenInput(in, tokenClaimed)
	}
	shortcutKeyboard = battleShortcutKeyboard(in)
	shiftHeld := shortcutKeyboard != nil && shortcutKeyboard.HasShift()
	ctrlHeld := shortcutKeyboard != nil && shortcutKeyboard.KeyHeld(input.KeyCtrl)
	if !talkOwned && (keyDown(input.KeyTab) || keyDown(input.KeyF2) && !ctrlHeld && !shiftHeld) {
		// Host UI policy: a paused save opens without ARMOPT, so Tab resumes
		// it directly instead of requiring an open/close cycle. F2 retains
		// access to options (DESIGN_INTERFACE_HUD_INPUT §3.4).
		if keyDown(input.KeyTab) && state.Paused() {
			b.applyBattleSchedule(ui.PauseIntent(false))
		} else {
			b.openBattleMenu()
		}
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	// Escape with the options window closed: an armed latch or placement
	// returns to idle, and an idle latch deselects everything. It does not open
	// the options window — that is F2's and Tab's row [07 R-CAM-01 §2].
	if !talkOwned && keyDown(input.KeyEscape) {
		if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
			_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		}
		b.disarmPlacement()
		b.resetOrderLatch()
		b.battleState().Input.HUDCaptured = false
		b.battleState().Input.DragActive = false
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	if state.Modal() != ui.BattleModalClosed {
		b.handleBattleMenuInput(in, cl)
	} else {
		if b.controller == nil {
			b.controller = NewBattleController(b)
		}
		// The scroll pass's raw delta is refreshed here, at retail's tick-budget
		// step, rather than down in the camera pass: the budget step runs before
		// hotkey dispatch, so the frame that presses pause still refreshes the
		// delta and it is that frame's value the pause freezes [07 §1][07 §10].
		b.refreshScrollDelta()
		// Phase 10's follow step runs inside the sub-tick loop, before the
		// hotkey dispatch and the scroll pass [07 R-CAM-01 §1 steps 3-5]
		// [07 R-CAM-01 §12]: a `t` or Ctrl+C pressed below therefore begins its
		// glide on the following frame, as retail's does.
		sample := b.pointerSample(in, delta)
		if talkOwned {
			sample = talkOwnedInput(producerIn, delta)
		}
		b.controller.Step(sample, cl)
		resourceInputServiced = true
	}
	if b.ended {
		return
	}
	if state.Modal() != ui.BattleModalClosed {
		cl.Cursors().SetIndex(render.CursorNormal)
	} else {
		b.updateCursor(cl)
	}
	// Camera pan: exact predicates per [07 §10] C2/C3; presentation-only [I6].
	// - delta = scrollSettingByte * rawTimeDelta capped at 128 [07 §10] (C2)
	// - one exclusive test per axis, Left before Right and Up before Down [07 §10][07 R-CRD-006 §1]
	// - exact-edge predicates [07 §10] after host-coordinate adaptation (DESIGN_INTERFACE_HUD_INPUT §3.1)
	// - held-arrow gated on TALK.GUI suppression, edge never suppressed by TALK [07 §10]
	// - the minimap does NOT suppress edge: retail's pass reads the cursor
	//   position and the four arrows and nothing else — no minimap or GUI
	//   hit test appears in its input list [07 R-CAM-01 §10]. Because the
	//   radar canvas occupies the screen's top-left corner, such a test
	//   costs the corner itself plus the first 126 pixels of both the top
	//   and the left edge, which is where a player reaches for them (C2).
	// - modal GUI suppresses edge [07 §10]
	// - focus gating before edge [07 §10]
	// - scroll setting from persisted settings byte [02 "Settings"] default 32 [C-5]
	// W/A/S/D remain unbound per ON-05 (do not pan) [F-P1-008].
	if b.cam != nil && state.Modal() == ui.BattleModalClosed && !b.dragScrollActive && !b.dragScrollStepped {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := b.scrollDelta // refreshed at the budget step above [07 §1]
		scrollSetting := b.scrollSetting()
		focused := cl.IsFocused()
		w, h := cl.Size()
		wi, hi := int32(w), int32(h)
		// Adapt host overshoot and letterboxing without moving the pointer
		// used for picking or HUD clicks (DESIGN_INTERFACE_HUD_INPUT §3.1).
		mx, my := int32(mouse.X), int32(mouse.Y)
		effX, effY := cl.EdgeScrollPosition()
		talkActive := b.isTalkGUIActive()
		overMinimap := b.isOverMinimap(mx, my)
		modalActive := state.Modal() != ui.BattleModalClosed
		// Every scroll-pass write is a jump by delta, and a jump by the scroll
		// pass cancels the follow triple [07 R-CAM-01 §12].
		scroll := func(dir camera.Direction, keyboard bool) {
			if keyboard {
				b.cam.ScrollScreen(scrollSetting, rawDelta, dir)
			} else {
				b.cam.Scroll(scrollSetting, rawDelta, dir)
			}
			b.cam.ClearFollow()
			b.pendingFollowInput = nil
		}
		// One exclusive test per axis, not four independent ones
		// [07 §10][07 R-CRD-006 §1]. The horizontal axis evaluates the Left
		// predicate first and, when it holds, subtracts the magnitude and moves
		// straight to the vertical axis without evaluating the Right predicate
		// at all; only a failed Left predicate reaches Right. The vertical axis
		// has the same shape with Up before Down. So with both directions of a
		// pair satisfied — opposing arrows, or one screen edge plus the
		// opposite arrow — the camera moves once, toward Left/Up, rather than
		// twice or (as a summed vector would give) not at all.
		//
		// Each predicate is its arrow held with TALK.GUI absent, OR its edge
		// band [07 §10]. The held arm wins the magnitude form when both arms of
		// one predicate hold, as the keyboard arm is tested first. The edge arm
		// additionally requires focus and no modal; it deliberately does not
		// test the minimap [07 R-CAM-01 §10]. A captured minimap camera drag
		// that reaches the canvas edge cannot fight this pass: that latch jumps
		// the camera to the lens point of the same screen edge — canvas x 0 is
		// the leftmost column of the map, canvas y 0 its topmost row — so the
		// clamp has already pinned the axis the edge disjunct then pushes
		// further in the same direction [07 R-CAM-01 §11][C3].
		heldLeft := kbd.KeyHeld(input.KeyLeft) && !talkActive
		heldRight := kbd.KeyHeld(input.KeyRight) && !talkActive
		heldUp := kbd.KeyHeld(input.KeyUp) && !talkActive
		heldDown := kbd.KeyHeld(input.KeyDown) && !talkActive
		edgeReady := focused && !modalActive
		switch { // horizontal: Left predicate, then Right only if it failed
		case heldLeft || (edgeReady && effX == 0 && effY < hi):
			scroll(camera.DirLeft, heldLeft)
		case heldRight || (edgeReady && effX == wi-1):
			scroll(camera.DirRight, heldRight)
		}
		switch { // vertical: Up predicate, then Down only if it failed
		case heldUp || (edgeReady && effY == 0 && effX < wi):
			scroll(camera.DirUp, heldUp)
		case heldDown || (edgeReady && effY == hi-1):
			scroll(camera.DirDown, heldDown)
		}
		// Middle-drag camera pan [F-P1-008]: presentation-only, uses mouse delta / scale.
		if !talkActive && mouse.Held(input.MouseButtonMiddle) && mouse.Moved() {
			dx := int32(mouse.X - b.battleState().Input.PrevMouseX)
			dy := int32(mouse.Y - b.battleState().Input.PrevMouseY)
			if dx != 0 || dy != 0 {
				b.cam.Drag(dx, dy)
			}
		}
		// The wheel over the battle viewport is smooth zoom
		// (DESIGN_GPU_RENDERER §16.6). It is a Nanolathe binding, not a retail
		// one: retail leaves the wheel to the active GUI list under the pointer
		// [07 §2][07 §10], and the UI boundary still consumes it first — this
		// pass only ever sees a wheel the chrome did not want, and takes it only
		// over the world, only outside TALK, and only in the executor that can
		// present a free factor.
		if cl.Enhanced() && !talkActive && !modalActive && !overMinimap &&
			mouse.ZoomScrollY != 0 && !b.communityPlacementWheelOwned(in) && b.overBattleViewport(mx, my) {
			b.wheelZoom(mx, my, float64(mouse.ZoomScrollY))
		}
		b.applyTrackpadGestures(mouse, cl.Enhanced() && focused && !talkActive && !talkOwned &&
			!modalActive && !overMinimap && !b.palettePointerOwned && !unitInfoOpen() &&
			b.overBattleViewport(mx, my), mx, my)
		gesturesServiced = true
		// One Update of the ease, whatever produced the target. It runs
		// unconditionally so a target set by F9 or by the wheel of an earlier
		// frame keeps moving; the controller is idle when nothing is in flight.
		b.zoom.Step(b.cam)
		b.battleState().Input.PrevMouseX = mouse.X
		b.battleState().Input.PrevMouseY = mouse.Y
	}
}

// overBattleViewport reports whether a framebuffer point is inside the battle
// viewport — the world region `(129,32)..(W-1,H-33)` the chrome leaves — which
// is where the wheel is smooth zoom rather than a chrome control
// (DESIGN_GPU_RENDERER §16.6)[03 §4.1][07 R-HUD-05].
func (b *battleSession) overBattleViewport(x, y int32) bool {
	if b == nil || b.cam == nil {
		return false
	}
	return x > camera.OriginX && x < b.cam.ViewW &&
		y >= camera.OriginY && y < b.cam.ViewH-camera.OriginY
}
