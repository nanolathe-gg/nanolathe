package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

// shellMode is the retail frontend state. The menu screens deliberately map
// one-to-one to the retail GUI files; there is no Nanolathe-owned layout.
type shellMode = ui.Mode

const (
	modeMenuMain     shellMode = ui.ModeMain
	modeMenuSingle   shellMode = ui.ModeSingle
	modeMenuMission  shellMode = ui.ModeMission
	modeMenuMap      shellMode = ui.ModeMap
	modeMenuSkirmish shellMode = ui.ModeSkirmish
	// modeLoading is the retail loading screen. It owns no .GUI file: retail
	// closes the frontend window, forces 640x480, and paints the authored
	// loading background while the loader thread works [07 §4].
	modeLoading shellMode = ui.ModeLoading
	modeBattle  shellMode = ui.ModeBattle
)

// retailScreenW and retailScreenH are the frontend display the .GUI files are
// authored against; every full-screen shell window is (0,0,640,480) [07 §4].
const (
	retailScreenW = 640
	retailScreenH = 480
)

type retailPanelAssets struct {
	window     *gui.Window
	background *formats.PCX
	art        *formats.GAF
	// unavailable records an unresolved authored GUI at the construction seam.
	// The caller keeps this value explicit and never treats it as a valid empty
	// panel [07 §5].
	unavailable error
}

// menuAssets is the mounted retail frontend resource set. All menu pixels,
// widgets and text font come from the same files TotalA.exe selects.
type menuAssets struct {
	// err is retained on the compatibility-shaped loader below so existing
	// asset-inspection tests can still inspect a partially built value. The
	// frontend constructor always checks it before installing a panel [07 §5
	// "Frontend asset failure boundaries"].
	err             error
	common          *formats.GAF
	logos           *formats.GAF
	font            *formats.FNT
	gafFont         *formats.GAF
	gafFontSmall    *formats.GAF
	pal             *palette.Tables
	panel           map[shellMode]*retailPanelAssets
	message         *retailPanelAssets
	loading         *formats.PCX
	missionCampaign *formats.PCX
	missionSmall    *formats.PCX
	missionAny      *formats.PCX
	// briefing is loaded lazily after a campaign mission resolves its planet;
	// MSNBRIEF is not needed by the shell's skirmish path.
	briefing *retailPanelAssets
}

// gameShell owns only frontend state and the battle hand-off. Menu state is
// kept in ui.Panel in retail_menu.go and is reset whenever retail
// opens a new .GUI panel.
type gameShell struct {
	opts Options
	cs   *contentSet

	assets *menuAssets
	font   *formats.FNT

	// cursorAccum converts renderer seconds into whole cursor animation ticks
	// for the menu screens, which have no simulation clock [03 §4.4].
	cursorAccum float64

	maps         []string
	mapLabels    []string
	mapIdx       int
	mapReturn    shellMode
	mapData      map[string]*retailMapData
	setup        session.SkirmishConfig
	selectedSlot int
	// retailControllers preserves the numeric Controller field that TotalA.exe
	// puts in each Player%d row: 0=open, 1=human, 2=computer. The session
	// package has a separate compatibility representation, so the conversion
	// happens only at battle entry.
	retailControllers    [session.SkirmishMaxPlayers]int
	retailControllersSet bool

	// settingsWritable is set by the windowed entry point once it has read the
	// persisted preferences. Only that path writes them back, so the
	// screenshot path and the tests never touch the user's settings file.
	settingsWritable bool
	scrollSpeed      int // persisted scrollspeed [02 "Settings"] [07 §10] C2 presentation-only

	campaigns              []mission.Campaign
	campaignOptions        []mission.Campaign
	campaignIdx            int
	missionIdx             int
	missionAny             bool
	missionSide            int
	missionDifficultyValue int
	// briefing owns the explicit campaign presentation state between mission
	// selection and the shared battle loading request [08 R-CAMP-01 §2].
	briefing      *campaignBriefingController
	briefingPanel *ui.Panel
	briefingNowMS int64

	// audioOwner is shared by the frontend briefing and the subsequently
	// composed battle. It is the one semantic audio owner for both seams;
	// briefing effects are never sent to a second frontend-only service
	// [03 R-AUD-02 §1][I6].
	audioOwner *audio.Service

	// campaignProgress is copied from the frozen result session when Start
	// selects a successor or retry. The next battle receives the same bank
	// value through its loading adoption callback; no UI path writes W/L.
	campaignProgress    session.BankProgress
	campaignProgressSet bool

	// loading is live only while mode is modeLoading. The loader runs on its
	// own goroutine, so the shell reads its progress and adopts its result
	// from the render goroutine only.
	loading *loadingState
	// loadingReturn is the screen a failed load falls back to.
	loadingReturn shellMode

	// frontend owns mode, active authored window, save-under predecessor, modal
	// message, focus/press latches, and list thumb capture [07 §3][07 §4].
	frontend *ui.Frontend

	cam    *camera.Camera
	battle *battleSession
}

// gameShellUIStage adapts the canonical frontend state to the client's single
// typed UI slot. It does not participate in world ordering; the client invokes
// it only after the committed frame has completed its world passes [03 §1].
type gameShellUIStage struct{ shell *gameShell }

func (s gameShellUIStage) DrawUI(c *client.Client, presented client.UIFrame) {
	if s.shell != nil {
		s.shell.draw(c, presented)
	}
}

// battleHUDUIStage is the one battle-surface adapter. The HUD consumes the
// committed frame supplied by the client rather than acquiring another frame
// from the session [I6].
type battleHUDUIStage struct {
	hud    *retailBattleHUD
	battle *battleSession
}

func (s battleHUDUIStage) DrawUI(c *client.Client, presented client.UIFrame) {
	if s.hud != nil {
		s.hud.draw(c, s.battle, presented)
	}
}

// newGameShell builds the frontend state: the skirmish map list, the retail
// resource set, and the opening panel used by the windowed entry.
func newGameShell(opts Options, cs *contentSet) (*gameShell, error) {
	shell := &gameShell{opts: opts, cs: cs, frontend: ui.NewFrontend(modeMenuMain)}
	maps, err := enumerateSkirmishMaps(cs.fs)
	if err != nil {
		return nil, err
	}
	shell.maps = maps
	shell.mapLabels = make([]string, len(maps))
	for i, name := range maps {
		shell.mapLabels[i] = name
	}
	mapName := ""
	if len(maps) != 0 {
		mapName = maps[0]
	}
	shell.setup = newSkirmishMenuConfig(mapName)
	shell.missionDifficultyValue = session.SkirmishDefaultDifficulty
	shell.scrollSpeed = settings.DefaultScrollSpeed // [02 "Settings"] [07 §10]
	shell.assets = loadMenuAssets(cs)
	if shell.assets == nil {
		return nil, fmt.Errorf("nanolathe: retail frontend assets: construction returned no asset set")
	}
	if shell.assets.err != nil {
		return nil, shell.assets.err
	}
	if shell.assets != nil {
		shell.font = shell.assets.font
	}
	shell.openMenu(modeMenuMain)
	return shell, nil
}

// runGameShell is the windowed entry: retail menus by default; straight into
// the battle view when --map was supplied (the established development path).
func runGameShell(opts Options, cs *contentSet) error {
	if opts.Map != "" {
		return runBattleView(opts, cs)
	}

	shell, err := newGameShell(opts, cs)
	if err != nil {
		return err
	}
	// The persisted frontend preferences are read once here, before the first
	// panel is drawn, the way retail reads its registry block during startup
	// [07 §4].
	shell.attachSettings()
	maps := shell.maps

	const winW, winH = 640, 480
	shell.cam = &camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: winW, MapH: winH}
	buf := &frame.Buffer{}
	var cl *client.Client
	cl, err = client.New(client.Options{
		Buffer: buf,
		Width:  winW,
		Height: winH,
		Title:  "Nanolathe",
		Step:   func(delta float64) { shell.step(delta, cl) },
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	clPtr = cl // entering a battle morphs THIS client
	cl.SetModelFS(cs.fs)
	cl.SetCamera(shell.cam)
	if shell.assets != nil && shell.assets.pal != nil {
		// Retail keeps one indexed display palette for frontend and battle. GAF,
		// PCX, and FNT raster bytes all address PALETTE.PAL directly; GUIPAL is
		// consulted only when a GUI semantic color field is resolved.
		cl.SetPalette(shell.assets.pal)
	}
	if shell.font != nil {
		cl.SetFNT(shell.font)
	}
	// Software cursor [07 §8]. The cursor GAF is mandatory for the windowed
	// frontend, and installation happens before entering Ebitengine's loop.
	cursors, cerr := client.LoadCursors(cs.fs)
	if cerr != nil {
		return cerr
	}
	cl.SetCursors(cursors)
	cl.SetUIStage(gameShellUIStage{shell: shell})
	if opts.LoadSave != "" {
		// The host supplied an explicit path; loading is performed before the
		// client loop starts, on the same thread that owns render-thread state.
		// No file-picker or alternate save format is introduced here.
		if err := shell.loadRetailSavePath(opts.LoadSave); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "nanolathe: retail frontend: %d skirmish maps\n", len(maps))
	return client.RunGame(cl)
}

func loadMenuAssets(cs *contentSet) *menuAssets {
	a := &menuAssets{panel: make(map[shellMode]*retailPanelAssets)}
	if cs == nil || cs.fs == nil {
		a.err = fmt.Errorf("nanolathe: retail frontend assets: missing VFS: logical path <install>, providers searched [], expected mounted retail content")
		return a
	}
	// PALETTE.PAL (or its authored PCX fallback inside palette.Load) and COMIX
	// are installed before any frontend window. A missing or malformed input is
	// a construction error; there is no authored replacement [03 §4.3][07 §5].
	p, err := palette.Load(cs.fs)
	if err != nil {
		a.err = retailFrontendAssetError(cs, "retail frontend palette", "palettes/PALETTE.PAL", "the shared retail palette tables", err)
		return a
	}
	a.pal = p
	f, err := formats.LoadFNTFile(cs.fs, "fonts/comix.fnt")
	if err != nil {
		a.err = retailFrontendAssetError(cs, "retail frontend font", "fonts/comix.fnt", "the authored COMIX FNT", err)
		return a
	}
	a.font = f

	// These windows and their bitmap backgrounds are the implemented
	// single-player frontend. The bitmap path is established fatal; the GUI
	// opener's caller-level outcome for a missing or malformed window remains
	// unknown, so loadRetailPanelStrict records that panel as explicitly
	// unavailable instead of treating it as a valid empty layout [07 §5
	// "Frontend asset failure boundaries"].
	panels := []struct {
		mode     shellMode
		guiName  string
		pcxName  string
		gafName  string
		expected string
	}{
		{modeMenuMain, "guis/mainmenu.gui", "bitmaps/frontendx.pcx", "anims/mainmenu.gaf", "MAINMENU authored GUI and background"},
		{modeMenuSingle, "guis/single.gui", "bitmaps/singlebg.pcx", "anims/single.gaf", "SINGLE authored GUI and background"},
		{modeMenuMission, "guis/newgame.gui", "bitmaps/newcampaign4x.pcx", "anims/newgame.gaf", "NEWGAME authored GUI and background"},
		{modeMenuMap, "guis/selmap.gui", "bitmaps/dselectmap2.pcx", "", "SELMAP authored GUI and background"},
		{modeMenuSkirmish, "guis/skirmish.gui", "bitmaps/skirmsetup4x.pcx", "anims/skirmish.gaf", "SKIRMISH authored GUI and background"},
	}
	for _, spec := range panels {
		panel, loadErr := loadRetailPanelStrict(cs, spec.guiName, spec.pcxName, spec.gafName, spec.expected)
		if loadErr != nil {
			// The common GUI opener does not establish the process-level outcome
			// for a missing/malformed GUI. Keep that panel explicitly unavailable;
			// never retain a partially loaded panel or synthesize a replacement
			// window [07 §5 "Frontend asset failure boundaries"].
			if panel != nil && panel.unavailable != nil {
				a.panel[spec.mode] = panel
				continue
			}
			a.err = loadErr
			return a
		}
		a.panel[spec.mode] = panel
	}
	message, err := loadRetailPanelStrict(cs, "guis/msgbox.gui", "", "", "MSGBOX authored GUI")
	if err != nil {
		if message != nil && message.unavailable != nil {
			a.message = message
		} else {
			a.err = err
			return a
		}
	}
	a.message = message

	// Loading and both NEWGAME background variants are selected by later
	// callbacks, but they all enter through the same fatal bitmap loader when
	// selected. Preload them so no reachable callback can land on a nil PCX.
	for _, spec := range []struct {
		logical string
		dst     **formats.PCX
		expect  string
	}{
		{"bitmaps/loadgame2bg.pcx", &a.loading, "the authored loading background"},
		{"bitmaps/newcampaign4.pcx", &a.missionCampaign, "the authored campaign background"},
		{"bitmaps/newcampaign4x.pcx", &a.missionSmall, "the authored compressed campaign background"},
		{"bitmaps/playanygame4.pcx", &a.missionAny, "the authored Play Any background"},
	} {
		pcx, loadErr := formats.LoadPCXFile(cs.fs, spec.logical)
		if loadErr != nil {
			a.err = retailFrontendAssetError(cs, "retail frontend bitmap", spec.logical, spec.expect, loadErr)
			return a
		}
		*spec.dst = pcx
	}

	// GUI-attached roots and HATTFONT slots are separately optional in retail:
	// a missing root leaves its handle empty and controls continue through the
	// authored support/fallback lookup [07 §5]. They are intentionally loaded
	// without converting a null handle into a fabricated panel or widget.
	if g, err := formats.LoadGAFFile(cs.fs, "anims/commongui.gaf"); err == nil {
		a.common = g
	}
	if g, err := formats.LoadGAFFile(cs.fs, "textures/logos.gaf"); err == nil {
		a.logos = g
	}
	// The frontend installs hattfont12 as primary GAF font slot 0 and
	// hattfont11 as secondary slot 1. Generic frontend controls prefer the
	// primary GAF font over the active COMIX FNT [07 §4].
	if f, err := formats.LoadGAFFile(cs.fs, "anims/hattfont12.gaf"); err == nil {
		a.gafFont = f
	}
	if f, err := formats.LoadGAFFile(cs.fs, "anims/hattfont11.gaf"); err == nil {
		a.gafFontSmall = f
	}
	/* panels loaded above */
	// SELMAP.GUI's background is DSELECTMAP2. Its screen opener hands that
	// logical bitmap to the shared cache, which stores it on the open window.
	// The file is 640x480 but its panel art occupies only the top-left
	// 494x420, matching the window's own 494x420 record at (84,12), so it is
	// drawn at the window origin and clipped to the window [07 §4].
	// bitmaps/selectgame2x.pcx belongs to the multiplayer SELGAME.GUI lobby,
	// which is out of scope, and is not loaded here.
	return a
}

func loadRetailPanel(cs *contentSet, guiName, pcxName, gafName string) *retailPanelAssets {
	p, _ := loadRetailPanelStrict(cs, guiName, pcxName, gafName, "authored frontend panel")
	return p
}

func loadRetailPanelStrict(cs *contentSet, guiName, pcxName, gafName, expected string) (*retailPanelAssets, error) {
	if cs == nil || cs.fs == nil {
		return nil, fmt.Errorf("nanolathe: retail frontend panel: logical path %s, providers searched [], expected %s", guiName, expected)
	}
	p := &retailPanelAssets{}
	if w, err := gui.Load(cs.fs, guiName); err != nil {
		// Missing/malformed GUI outcome is not established by the retail caller.
		// TODO(question): settle the retail process-level outcome for this
		// missing/malformed GUI with an executable trace. Preserve an explicit
		// unavailable panel, never a partial one.
		p.unavailable = retailFrontendAssetError(cs, "retail frontend GUI unavailable", guiName, expected, err)
		return p, p.unavailable
	} else {
		p.window = w
	}
	if pcxName != "" {
		if bg, err := formats.LoadPCXFile(cs.fs, pcxName); err != nil {
			return nil, retailFrontendAssetError(cs, "retail frontend bitmap", pcxName, expected, err)
		} else {
			p.background = bg
		}
	}
	if gafName != "" {
		if g, err := formats.LoadGAFFile(cs.fs, gafName); err == nil {
			p.art = g
		}
	}
	return p, nil
}

func retailFrontendAssetError(cs *contentSet, what, logical, expected string, cause error) error {
	providers := []string(nil)
	if cs != nil && cs.fs != nil {
		providers = providerNames(cs.fs)
	}
	base := &missingProductError{what: what, logical: logical, providers: providers, expected: expected}
	if cause == nil {
		return base
	}
	return fmt.Errorf("%w: %v", base, cause)
}

func (g *gameShell) openMenu(mode shellMode) {
	if g == nil {
		return
	}
	if g.frontend == nil {
		g.frontend = ui.NewFrontend(mode)
	}
	oldPanel, oldMode := g.activePanel(), g.frontend.Mode
	if oldPanel != nil {
		// Clear a gesture before replacing the active window. This used to sit
		// after panel=nil and was unreachable, allowing a held press to leak
		// into the next authored screen [07 §3].
		oldPanel.ResetPress()
		oldPanel.CancelScrollDrag()
	}
	var panel *ui.Panel
	if g.assets != nil {
		if mode == modeMenuMission {
			// NEWGAME.GUI is reused for both New Campaign and Play Any Game.
			// The Play Any branch changes these authored list rectangles, so
			// restore/apply that runtime mutation before the
			// panel state takes its frame.
			g.applyRetailMissionLayout()
		}
		if p := g.assets.panel[mode]; p != nil && p.window != nil {
			panel = ui.NewPanel(p.window)
		}
	}
	if mode == modeMenuSkirmish {
		g.installSkirmishDynamicGadgets()
		if g.assets != nil {
			if p := g.assets.panel[mode]; p != nil && p.window != nil {
				panel = ui.NewPanel(p.window)
			}
		}
	}
	saveUnder := oldPanel != nil && mode != oldMode && g.panelWindowNeedsUnder(mode)
	g.frontend.Open(mode, panel, saveUnder)
	g.refreshRetailPanel()
	g.resolveRetailButtonGeometry()
}

func (g *gameShell) activePanel() *ui.Panel {
	if g == nil || g.frontend == nil {
		return nil
	}
	return g.frontend.ActivePanel()
}

func reportRetailMessageError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

// panelWindowNeedsUnder reports whether opening mode pushes a panel window
// onto the chain rather than replacing the screen. The test is the retail one:
// The window initializer gives every window a drawing surface of exactly its
// own width and height, positioned at the window origin, and copies the screen
// into it before anything is painted. A window smaller than the display
// therefore never erases what is under it, and retail keeps a SAVE UNDER copy
// so it can put those pixels back on close. SELMAP.GUI (84,12,494,420)
// is the single-player case; the four frontend screens are all authored at
// (0,0,640,480) and cover everything [07 §4].
func (g *gameShell) panelWindowNeedsUnder(mode shellMode) bool {
	if g.assets == nil {
		return false
	}
	p := g.assets.panel[mode]
	if p == nil || p.window == nil {
		return false
	}
	r := p.window.Rect
	return r.X > 0 || r.Y > 0 || int(r.X+r.W) < retailScreenW || int(r.Y+r.H) < retailScreenH
}

func (g *gameShell) step(delta float64, cl *client.Client) {
	switch g.frontend.Mode {
	case modeBattle:
		if g.battle != nil {
			g.battle.viewerStep(delta, cl)
		}
	case modeLoading:
		// The transition that blocks on catalog and map loading installs the
		// hourglass shape [07 §8]; the frontend's own idle shape returns with
		// the next menu.
		cursors := cl.Cursors()
		cursors.SetIndex(render.CursorHourglass)
		g.cursorAccum += delta * 30
		if n := int(g.cursorAccum); n > 0 {
			cursors.Step(n)
			g.cursorAccum -= float64(n)
		}
		g.stepLoading(delta)
	default:
		// The front end uses the idle shape throughout; the loading shape is
		// installed by the transition that blocks on catalog and map loading
		// [07 §8]. Menu animation still advances at the renderer's cadence so a
		// visible hourglass keeps turning.
		cursors := cl.Cursors()
		if g.briefing != nil && g.briefing.State() == BriefingOpen {
			// Renderer delta is the shell's monotonic presentation clock; the
			// briefing callbacks consume its scaled units [07 R-CAM-01 §1].
			g.briefingNowMS += int64(delta * 1000)
			if g.audioOwner != nil {
				g.audioOwner.TickStream(uint32(briefingPresentationTick(g.briefingNowMS)))
			}
		}
		cursors.SetIndex(render.CursorNormal)
		g.cursorAccum += delta * 30
		if n := int(g.cursorAccum); n > 0 {
			cursors.Step(n)
			g.cursorAccum -= float64(n)
		}
		g.menuInput(cl)
	}
}

func (g *gameShell) missionDifficulty() int {
	return g.missionDifficultyValue
}

func (g *gameShell) enterBattle(sess *session.Session, cat *content.Catalog) error {
	return g.enterBattleAtCamera(sess, cat, nil)
}

// enterBattleAtCamera prepares the complete battle presentation before
// retiring the current battle/frontend state. This ordering is the atomic
// save-load boundary: every fallible operation happens on the detached
// candidate, while the grouped adoption below runs only on the render thread
// [08 R-SAVE-02 §11–§12].
func (g *gameShell) enterBattleAtCamera(sess *session.Session, cat *content.Catalog, savedCamera *save.Camera) error {
	if g == nil || sess == nil {
		return fmt.Errorf("nil battle session")
	}
	if g.audioOwner != nil && sess != nil {
		// The frontend briefing and battle share one semantic audio owner. This
		// only changes the detached candidate until adoption below.
		sess.Audio = g.audioOwner
	}
	g.adoptCampaignProgress(sess)
	battle, err := composeBattleEntryDetached(sess, cat, g.cs, g, savedCamera)
	if err != nil {
		return err
	}
	g.commitBattleCandidate(battle)
	return nil
}

// commitBattleCandidate is the sole render-thread installation point for a
// prepared battle. It intentionally contains no fallible work: the previous
// battle and its client bindings are retired only after candidate preparation
// has completed successfully [I6].
func (g *gameShell) commitBattleCandidate(battle *battleSession) {
	if g == nil || battle == nil {
		return
	}
	if g.battle != nil {
		g.battle.teardown(clPtr)
	}
	g.battle = nil
	g.cam = nil
	installBattleClient(clPtr, battle)
	g.cam = battle.cam
	g.battle = battle
	g.battle.returnToMenu = g.returnFromBattle
	g.battle.returnToSkirmish = func(cl *client.Client) {
		if g != nil {
			g.teardownBattle(cl)
			g.openMenu(modeMenuSkirmish)
			g.bindFrontendClient(cl)
		}
	}
	if g.frontend != nil {
		g.frontend.SetMode(modeBattle)
	}
}

// returnFromBattle is the retail MAINMENU confirmation outcome: discard the
// live battle presentation and restore the main frontend window in the same
// client. The abandoned session has no background goroutine and becomes
// unreachable after this hand-off.
func (g *gameShell) returnFromBattle(cl *client.Client) {
	if g == nil {
		return
	}
	g.teardownBattle(cl)
	g.openMenu(modeMenuMain)
	g.bindFrontendClient(cl)
}

// bindFrontendClient restores the established inert frontend/loading
// presentation after battle teardown. The loading screen is a game-shell UI
// mode over an empty snapshot and owns no terrain or battle presentation
// references [07 §4][08 R-ENTRY-01 §1][I6].
func (g *gameShell) bindFrontendClient(cl *client.Client) {
	if g == nil || cl == nil {
		return
	}
	if g.cam == nil {
		g.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	}
	cl.SetSnapshot(&frame.Buffer{})
	cl.SetTerrain(nil)
	cl.SetCamera(g.cam)
	if g.audioOwner != nil {
		cl.SetAudioService(g.audioOwner)
	}
	if g.assets != nil {
		if g.assets.pal != nil {
			cl.SetPalette(g.assets.pal)
		}
		cl.SetFNT(g.assets.font)
	}
	cl.SetUIStage(gameShellUIStage{shell: g})
}

// teardownBattle clears the shell side of the idempotent battle-exit seam.
// Loading ownership is also dropped here so a continuation cannot retain an
// old progress callback while the next request is being built [08 "Session
// states"][08 R-ENTRY-01 §1][I6].
func (g *gameShell) teardownBattle(cl *client.Client) {
	if g == nil {
		return
	}
	if g.battle != nil {
		g.battle.teardown(cl)
		g.cam = nil
	}
	g.battle = nil
	g.loading = nil
	g.loadingReturn = modeMenuMain
}

// enumerateSkirmishMaps is the retail map census: only OTA files with a
// Network schema are put into the SELMAP MAPNAMES list [08 "Schema choice"].
func enumerateSkirmishMaps(fs *vfs.FS) ([]string, error) {
	if fs == nil {
		return nil, fmt.Errorf("nil VFS")
	}
	entries, err := fs.RetailReadDir("maps")
	if err != nil {
		if errors.Is(err, vfs.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	seen := make(map[string]bool, len(entries))
	var names []string
	for _, entry := range entries {
		if entry.IsDir || !strings.HasSuffix(strings.ToLower(entry.Name), ".ota") {
			continue
		}
		p := entry.Path
		ota, err := formats.LoadOTAFile(fs, p)
		if err != nil || !ota.HasNetworkSchema() {
			continue
		}
		base := entry.Name
		base = strings.TrimSuffix(strings.TrimSuffix(base, ".ota"), ".OTA")
		key := strings.ToLower(base)
		if seen[key] {
			continue
		}
		seen[key] = true
		// The map loader stores the OTA file stem with the case the archive
		// records, not a folded copy: the localized-string lookup is only
		// consulted when it returns something different from the stem.
		names = append(names, base)
	}
	// The map loader hands the packed name list to the retail string sorter
	// ("SORTED LIST1") before installing it in MAPNAMES. That is a bubble sort whose
	// comparison is _stricmp, so the authored MAPNAMES order is ascending and
	// case-insensitive, not archive order [07 §4].
	sort.SliceStable(names, func(i, j int) bool { return retailStricmp(names[i], names[j]) < 0 })
	return names, nil
}

// retailStricmp is the comparison the retail string sorter uses. The helper folds
// only the ASCII range A-Z and compares the folded bytes, so it is neither
// locale-aware nor Unicode-aware; map names outside ASCII order by raw byte.
func retailStricmp(a, b string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		ca, cb := retailFold(a[i]), retailFold(b[i])
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

func retailFold(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

func (g *gameShell) draw(c *client.Client, _ client.UIFrame) {
	if g.frontend.Mode == modeLoading {
		g.drawLoadingScreen(c)
		return
	}
	if g.frontend.Mode == modeBattle {
		// Battle UI is installed as the client's typed stage at the hand-off.
		// Keeping this adapter out of the shell prevents two renderer owners.
		return
	}
	g.drawRetailPanel(c)
	g.drawRetailModal(c)
}
