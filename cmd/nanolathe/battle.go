package main

import (
	"fmt"
	"os"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

// battleSession is the composition root for the windowed battle view. It owns
// the integrated session (all twelve kernel phases) and the interaction state:
// selection, order latch, and build placement.
type battleSession struct {
	sess  *session.Session
	cat   *content.Catalog
	cam   *camera.Camera
	hud   *retailBattleHUD
	fs    vfs.FSOps
	shell *gameShell

	millisSource clock.MillisSource // host millisecond source for Session.Step [01 §4.1]

	// surfaceW/surfaceH is the negotiated presentation surface the pointer and
	// the world viewport are measured against. The interface art is authored in
	// the logical 640×480 design space, but a larger display mode is neither
	// scaled nor letterboxed: the chrome extends by rule and the world viewport
	// takes `(128,32)..(W−1,H−33)` [07 R-HUD-05][03 §4.1]. The host frame
	// refreshes both words from the client surface before any input is read;
	// zero means "not negotiated yet" and reads back as the authored size.
	surfaceW, surfaceH int32

	battleUI         *ui.BattleState
	returnToMenu     func(*client.Client)
	returnToSkirmish func(*client.Client)
	ended            bool

	controller *BattleController

	// postBattle is created once, at the first committed terminal ResultView.
	// It owns the frozen result presentation sequence; the live Session is not
	// consulted after this boundary [03 §2.4][08 R-CAMP-01 §6].
	postBattle           *session.PostBattleController
	postBattleClock      float64
	postBattleLastUnit   int64
	postBattleEffectPos  int
	postBattleGlamour    *formats.PCX
	postBattleNormalPal  *palette.Tables
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
	// through the composition root (tests) still resolves a real value. Valid
	// bytes are 1..255, so 0 doubles as "not primed yet".
	scrollSpeedByte byte

	// switchAlt is captured once when the battle installs its settings. It is
	// presentation input state only; routeDigit reads this cached bit rather
	// than opening the settings file on a keypress [07 R-CAM-01 §4][I6].
	switchAlt bool

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

	// watcherSlot latches the world-rebuild tail's watcher branch for the local
	// slot. Besides the camera jump, that tail clears render-flags bits 0 and 1
	// — the mapping and LOS masks — so a watcher's minimap is unmasked from its
	// first frame [07 R-CAM-01 §14][03 R-MM-01 §3]. Presentation-only [I6].
	watcherSlot bool

	// visitedUnits and currentUnit are the `n` unit cycle's state
	// [07 R-CAM-01 §2]. Retail keeps the visited bits in each unit's status
	// word; presentation may not write simulation state, so the cycle keeps its
	// own set here [I6]. The set is only looked up, never ranged, so it takes
	// part in no order-producing iteration [I1].
	visitedUnits map[pool.Handle]bool
	currentUnit  pool.Handle
}

// factoryBuildDelta applies the retail signed button count: left click adds
// one, Shift-left adds five; right-click variants pass negative values.
func factoryBuildDelta(shiftHeld, rightClick bool) int {
	count := 1
	if shiftHeld {
		count = 5
	}
	if rightClick {
		count = -count
	}
	return count
}

var clPtr *client.Client

// runBattleView launches the windowed battle view over the real session.
func runBattleView(opts Options, cs *contentSet) error {
	request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return err
	}
	sess, cat := authoritative.Session, authoritative.Session.Catalog
	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  retailScreenW,
		Height: retailScreenH,
		Title:  "Nanolathe — " + opts.Map,
		Step: func(delta float64) {
			b.viewerStep(delta, cl)
		},
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.fs)
	b, err = composeBattleEntry(sess, cat, cs, cl, nil)
	if err != nil {
		return err
	}
	clPtr = cl
	b.returnToMenu = func(cl *client.Client) {
		// The battle view has no menu shell callback; mark it ended and exit.
		b.ended = true
		if cl != nil {
			cl.RequestExit()
		}
	}
	defer b.teardown(cl)
	// Software cursor [07 §8]. The cursor GAF is mandatory for a windowed
	// battle, and installation happens before entering Ebitengine's loop.
	cursors, cerr := client.LoadCursors(cs.fs)
	if cerr != nil {
		return cerr
	}
	cl.SetCursors(cursors)
	fmt.Fprintln(os.Stderr, "nanolathe: battle view — drag=select left-click=action right-click=deselect/cancel M=move A=attack P=patrol R=repair E=reclaim C=capture G=guard D=blast B=build X=cancel O=on/off N=stockpile Esc=cancel 1..9/Alt+1..9=pages/groups (SwitchAlt swaps) Shift=queue")
	return ebitenapp.Run(cl, rendererMode(opts))
}

// rendererMode maps the --renderer flag to the platform executor selection. Any
// value other than "modern" — including the empty string and any typo — selects
// the classic executor, the safe default (docs/DESIGN_GPU_RENDERER.md §2.4,
// §2.5).
func rendererMode(opts Options) ebitenapp.RendererMode {
	if opts.Renderer == "modern" {
		return ebitenapp.RendererModern
	}
	return ebitenapp.RendererClassic
}

// composeBattleEntry is the single presentation composition for every
// constructed battle session. Session construction has already completed the
// authoritative entry tail's tick-zero per-player priming and second resource
// grant [08 R-ENTRY-01 §8]. This helper does not change which authored HUD
// surfaces the existing battle loader provides.
func composeBattleEntry(sess *session.Session, cat *content.Catalog, cs *contentSet, cl *client.Client, shell *gameShell) (*battleSession, error) {
	b, err := composeBattleEntryDetached(sess, cat, cs, shell, nil)
	if err != nil {
		return nil, err
	}
	if cl != nil {
		installBattleClient(cl, b)
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
	hud, err := loadRetailBattleHUD(cs.fs, sess, cat, pal, shell)
	if err != nil {
		return nil, err
	}
	b := &battleSession{
		sess: sess, cat: cat, cam: cam, hud: hud, fs: cs.fs, shell: shell,
		millisSource: newMonotonicMillisSource(), battleUI: ui.NewProductionBattleState(),
		watcherSlot: sessionLocalIsWatcher(sess),
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
	// The session executor owns one message-ring retirement per host pump;
	// the client remains the sole presentation owner of the ring itself
	// [01 R-PLAT-02 §§7,8][I6]. Binding this callback leaves unrelated optional
	// post-loop diagnostics installed by a caller intact.
	bindBattleMessageRetirement(b.sess, cl)
	cl.SetSnapshot(b.sess.Snapshot)
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)
	cl.SetPalette(b.hud.pal)
	cl.SetFNT(b.hud.console)
	s := loadedSettings()
	applyBattleAudioOptions(b, s)
	applyDamageBarsSetting(s)
	// A shell already holds the startup settings block and may carry its live
	// value into a new battle. A direct --map battle has no shell, so its one
	// install-time settings read supplies the same bit.
	b.applySwitchAltSetting(s)
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
	// The warm pass below still belongs here: it compiles the catalog's event
	// sequences into the client's PIXEL cache off the draw path, so the first
	// frame of a feature's death animation never waits on a load.
	if b.sess.Catalog != nil {
		cl.WarmFeatureSequences(b.sess.Catalog.Features)
	}
	attachBattleAudio(cl, b.sess, b.fs)
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
	if b.battleUI != nil {
		b.closeBattleMenu()
		b.battleUI.SetPanelCue(nil)
		b.battleUI.ResetInteraction()
	}
	if cl != nil {
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
	}
	detachBattleAudio(cl, b.sess)
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
	b.returnToMenu = nil
	b.returnToSkirmish = nil
}

// newBattleSession builds the integrated skirmish session for the window.
// It uses the canonical DirectSkirmishConfig normalization [08 "Skirmish configuration"] [GAP T14].
func newBattleSession(opts Options, cs *contentSet) (*session.Session, *content.Catalog, error) {
	request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return nil, nil, err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return nil, nil, err
	}
	return authoritative.Session, authoritative.Session.Catalog, nil
}

// newBattleSessionWithConfig is the windowed composition path used by the
// skirmish lobby. The menu's per-slot and round settings reach the canonical
// session constructor [08 "Skirmish configuration"].
func newBattleSessionWithConfig(opts Options, cs *contentSet, cfg session.SkirmishConfig) (*session.Session, *content.Catalog, error) {
	return newBattleSessionWithConfigAndSource(opts, cs, cfg, newBattleSeedSource(opts))
}

// newBattleSessionWithConfigAndSource is the injectable composition seam for
// battle entry. The selected pair is copied into the session configuration
// before NewSkirmishWithFS performs any setup-owned draw [R-CORE-02].
func newBattleSessionWithConfigAndSource(opts Options, cs *contentSet, cfg session.SkirmishConfig, source BattleSeedSource) (*session.Session, *content.Catalog, error) {
	request, err := skirmishBattleRequest(opts, cs, cfg, headlessScenarioSkirmish, nil, source)
	if err != nil {
		return nil, nil, err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return nil, nil, err
	}
	return authoritative.Session, authoritative.Session.Catalog, nil
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

// viewerStep runs one rendered frame: input → session ticks → camera pan.
func (b *battleSession) viewerStep(delta float64, cl *client.Client) {
	if b == nil || cl == nil {
		return
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
	// Advance the canonical panel state during the host-frame update. Drawing
	// must remain a pure read of this state so hit testing and raster placement
	// use the same offset [07 §6][I6].
	// The rail slide's test is Space alone, unless a text editor has the focus
	// [07 §6]. Interface-flags bit 0x80 is *not* a term of it: that bit has
	// exactly two readers, the score panel's showing test and the kill-credit
	// finalize's flash arm [07 R-HUD-04 §1][07 R-CAM-01 §14]. It used to be
	// ORed in here, which pinned the rail open as well as the panel.
	spaceHeld := in != nil && in.Kbd != nil && in.Kbd.KeyHeld(input.KeySpace)
	editorFocused := b.hud != nil && b.hud.editorFocused()
	b.battleState().AdvancePanelNow(spaceHeld, editorFocused)
	// End-mission presentation takes ownership of the frame once the
	// authoritative result is latched. The authored result panel owns any
	// release-inside gesture; no battle hotkey or world command leaks through
	// [07 §3][07 §11].
	if b.isResultVisible() {
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
	keyDown := func(key input.Key) bool {
		return in != nil && in.Kbd != nil && in.Kbd.KeyDown(key)
	}
	shiftHeld := in != nil && in.Kbd != nil && in.Kbd.HasShift()
	// F2 is the options window's own key, and Tab toggles it in battle mode;
	// Escape only ever closes it. The "ESC bit" name survives because Escape is
	// the bit's clearer, but token 0xE3 is F2, not Escape
	// [07 R-CAM-01 §2 "Escape versus F2"].
	if modalAtFrameStart {
		// Tab is the authored root-modal toggle. Handle it before the modal
		// dispatcher so the same edge cannot also activate a newly closed/opened
		// window. Escape remains owned by the modal handler (which applies its
		// back transition), but the whole frame is consumed either way.
		//
		// A child window over `ARMOPT` owns the pass while it is up — the
		// options root and the save/load dialog both do — so the toggle is
		// suppressed for as long as one is open and the modal dispatcher
		// routes the frame to it instead [07 R-WGT-01 §1][07 R-FE-01 §6].
		if (keyDown(input.KeyTab) || keyDown(input.KeyF2)) && state.Modal() == ui.BattleModalOptions &&
			!b.battlePrefsActive() && !(b.shell != nil && b.shell.saveLoadPanelActive()) {
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
	// There is deliberately no keyboard-ownership seam for the unit-information
	// screen here. A battle window leaves its token-mode word zero, so the GUI
	// pass *peeks* the token instead of popping it and every battle hotkey runs
	// underneath the open window [07 R-WGT-01 §1 step 3][07 R-WGT-01 §2]; see
	// the correction note in unitinfo.go, which this block used to contradict.
	if keyDown(input.KeyTab) || (keyDown(input.KeyF2) && !shiftHeld) {
		b.openBattleMenu()
		cl.Cursors().SetIndex(render.CursorNormal)
		return
	}
	// Escape with the options window closed: an armed latch or placement
	// returns to idle, and an idle latch deselects everything. It does not open
	// the options window — that is F2's and Tab's row [07 R-CAM-01 §2].
	if keyDown(input.KeyEscape) {
		if b.battleState().Input.Latch == input.LatchNormal && b.battleState().Input.BuildDef == "" {
			_ = b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
		}
		b.disarmPlacement()
		b.battleState().Input.Latch = input.LatchNormal
		b.battleState().Input.ShiftLatchSticky = false
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
		b.stepFollowCamera()
		b.controller.Step(b.pointerSample(in, delta), cl)
		b.applyCommittedShake()
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
	// - direction predicates: exact-edge bands plus less-than-100px beyond-edge forced strip with focus [07 §10]
	// - held-arrow gated on TALK.GUI suppression, edge never suppressed by TALK [07 §10]
	// - minimap interaction region suppresses edge [07 §10]
	// - modal GUI suppresses edge [07 §10]
	// - focus gating before edge [07 §10]
	// - scroll setting from persisted settings byte [02 "Settings"] default 32 [C-5]
	// W/A/S/D remain unbound per ON-05 (do not pan) [F-P1-008].
	if b.cam != nil && state.Modal() == ui.BattleModalClosed {
		kbd := cl.Input().Kbd
		mouse := cl.Input().Mouse
		rawDelta := b.scrollDelta // refreshed at the budget step above [07 §1]
		scrollSetting := b.scrollSetting()
		focused := cl.IsFocused()
		w, h := cl.Size()
		wi, hi := int32(w), int32(h)
		mx, my := int32(mouse.X), int32(mouse.Y)
		// Beyond-edge forced strip: pointer outside right/bottom <100px beyond with focus is forced to edge [07 §10].
		effX, effY := mx, my
		if focused {
			if mx >= wi && mx < wi+100 {
				effX = wi - 1
			}
			if my >= hi && my < hi+100 {
				effY = hi - 1
			}
		}
		talkActive := b.isTalkGUIActive()
		overMinimap := b.isOverMinimap(effX, effY)
		modalActive := state.Modal() != ui.BattleModalClosed
		// Every scroll-pass write is a jump by delta, and a jump by the scroll
		// pass cancels the follow triple [07 R-CAM-01 §12].
		scroll := func(dir camera.Direction) {
			b.cam.Scroll(scrollSetting, rawDelta, dir)
			b.cam.ClearFollow()
		}
		// Held-arrow branches gated on TALK absence [07 §10]; edge branches gated on focus, modal, and minimap.
		// Left: (Left held && !talk) OR (x==0 && y<H) [07 §10]
		if kbd.KeyHeld(input.KeyLeft) && !talkActive {
			scroll(camera.DirLeft)
		} else if focused && !modalActive && !overMinimap && effX == 0 && effY < hi {
			scroll(camera.DirLeft)
		}
		// Right: (Right held && !talk) OR x==W-1 [07 §10]
		if kbd.KeyHeld(input.KeyRight) && !talkActive {
			scroll(camera.DirRight)
		} else if focused && !modalActive && !overMinimap && effX == wi-1 {
			scroll(camera.DirRight)
		}
		// Up: (Up held && !talk) OR (y==0 && x<W) [07 §10]
		if kbd.KeyHeld(input.KeyUp) && !talkActive {
			scroll(camera.DirUp)
		} else if focused && !modalActive && !overMinimap && effY == 0 && effX < wi {
			scroll(camera.DirUp)
		}
		// Down: (Down held && !talk) OR y==H-1 [07 §10]
		if kbd.KeyHeld(input.KeyDown) && !talkActive {
			scroll(camera.DirDown)
		} else if focused && !modalActive && !overMinimap && effY == hi-1 {
			scroll(camera.DirDown)
		}
		// Middle-drag camera pan [F-P1-008]: presentation-only, uses mouse delta / scale.
		if mouse.Held(input.MouseButtonMiddle) && mouse.Moved() {
			dx := int32(mouse.X - b.battleState().Input.PrevMouseX)
			dy := int32(mouse.Y - b.battleState().Input.PrevMouseY)
			if dx != 0 || dy != 0 {
				b.cam.Drag(dx, dy)
			}
		}
		// Wheel belongs to the active GUI list under the pointer. It is not a
		// battle-camera control [07 §2][07 §10]. The UI boundary consumes it
		// before this camera pass.
		b.battleState().Input.PrevMouseX = mouse.X
		b.battleState().Input.PrevMouseY = mouse.Y
	}
}
