package main

import (
	"errors"
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/platform/ebitenapp"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
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
	intro               *introPlayback
	startupMoviePending bool
	movieQueue          []string

	opts Options
	cs   *contentSet

	// pendingDetail is the load-time remaster's art for the battle the loading
	// screen has just finished, handed to the candidate at the one render-thread
	// adoption point and cleared there (DESIGN_GPU_RENDERER §14.4). A battle
	// adopted by another route — a restored save — leaves it nil and gets the
	// client's nearest doubling.
	pendingDetail *client.DetailArt

	assets *menuAssets
	font   *formats.FNT

	// cursorAccum converts renderer seconds into whole cursor animation ticks
	// for the menu screens, which have no simulation clock [03 §4.4].
	cursorAccum float64

	// widgetMillis is the presentation timer used by the common GUI pass. It
	// shares the established wrapping millisecond source and 30 Hz conversion
	// with the battle controller, without borrowing simulation time [07 R-WGT-01 §1].
	widgetMillis clock.MillisSource

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
	windowSize       retailDisplayMode // committed host size; options edits apply on OK
	fullscreen       bool              // Nanolathe host preference (DESIGN_PRESENTATION_CLIENT §2.1)
	scrollSpeed      int               // persisted scrollspeed [02 "Settings"] [07 §10] C2 presentation-only
	// display is the live `DisplaymodeWidth`/`Height` pair and the `VISUALS`
	// page's option values. The load transition reads the pair for the logical
	// battle canvas [07 R-FE-01 §6][07 R-FE-01 §11]; windowOptions also uses it
	// for Nanolathe's stable host window (DESIGN_PRESENTATION_CLIENT §2.1).
	display      settings.Display
	presentation settings.Presentation
	gameplay     gameplay.Mode
	// messages is the message-column ring configuration (`textlines`,
	// `textscroll`, `screenchat`, `unitchattext`). The options family's
	// interface page writes `textscroll`, `textlines` and `unitchattext`;
	// `screenchat` rides through unchanged [02 §3][07 R-CAM-01 §7].
	messages settings.Messages
	// audioPrefs is the audio half of the preference block the options family
	// snapshots on entry: the sound and music pages' stores
	// [03 R-AUD-01 §2][03 R-AUD-01 §4].
	audioPrefs settings.Audio
	// gameSpeed is the stored game-speed word the interface page's `GAME`
	// slider writes. In the front end it only persists; in battle the same
	// write also goes through the session's speed setter
	// [07 R-CAM-01 §7][07 R-CAM-01 §3].
	gameSpeed int
	// interfaceType is the `Interface Type` word the interface page's
	// `LEFTCLICK` two-stage button writes [07 R-CAM-01 §5].
	interfaceType int
	// switchAlt is the persisted digit-key mux bit [07 R-CAM-01 §4]. It has
	// no authored options-page gadget; the shell carries it into each battle.
	switchAlt bool
	// clockVisible is the persisted stand-alone battle-clock bit. It has no
	// options-page gadget; `+Clock` changes it during battle and the shell
	// carries the result into later battles [07 R-CAM-01 §6].
	clockVisible bool
	showRanges   bool // process-only Shift overlay detail switch [07 R-CAM-01 §6]

	campaigns       []mission.Campaign
	campaignOptions []mission.Campaign
	campaignIdx     int
	missionIdx      int
	// missionAny records which SINGLE.GUI button opened NEWGAME.GUI
	// (`NewCamp` or `AnyMsn`). Retail's only reachable NEWGAME.GUI layout
	// shows and fills both the campaign and mission lists regardless of
	// which button was pressed — the flag that would instead hide the
	// mission list has no live caller [07 §4 (R-FE-01 §4)] — so this no
	// longer selects a layout; it is kept only in case a future cue/
	// substate distinction needs it.
	missionAny             bool
	missionSide            int
	importedRetailBattle   bool
	missionDifficultyValue int
	// briefing owns the explicit campaign presentation state between mission
	// selection and the shared battle loading request [08 R-CAMP-01 §2].
	briefing      *campaignBriefingController
	briefingPanel *ui.Panel
	briefingNowMS int64
	// briefingFont is the FNT the TextRegion's font index selects — `armfont`
	// or `corefont` from MSNBRIEF's own kind-7 records. The wrapper, the pager
	// and the label pen all read it [08 R-CAMP-01 §2][07 R-WGT-01 §12].
	briefingFont *formats.FNT

	// audioOwner is shared by the frontend briefing and the subsequently
	// composed battle. It is the one semantic audio owner for both seams;
	// briefing effects are never sent to a second frontend-only service
	// [03 R-AUD-02 §1][I6].
	audioOwner *audio.Service
	// frontendAliasesBound records that the authored alias table has been
	// registered on audioOwner so the front end's own interface cues resolve
	// [02 "Sound aliases"][07 R-FE-01 §2].
	frontendAliasesBound bool
	// menuBGMPending defers the MAINMENU loop until the common presentation
	// step, after the process output has been installed [03 R-AUD-01 §5].
	menuBGMPending bool

	// campaignProgress is copied from the frozen result session when Start
	// selects a successor or retry. The next battle receives the same bank
	// value through its loading adoption callback; no UI path writes W/L.
	campaignProgress    session.BankProgress
	campaignProgressSet bool

	// resultBackground is `ENDMSN`'s authored background bitmap — `outcome1`
	// when the results route to another mission, `outcome0` otherwise. The
	// population step hands it to the bitmap cache with the window open, which
	// both makes it the window's background and installs its own 256-entry
	// palette [08 R-CAMP-01 §8].
	resultBackground    *formats.PCX
	resultBackgroundPal palette.Tables

	// loading is live only while mode is modeLoading. The loader runs on its
	// own goroutine, so the shell reads its progress and adopts its result
	// from the render goroutine only.
	loading *loadingState
	// loadingReturn is the screen a failed load falls back to.
	loadingReturn shellMode
	// quickKeyPreclearDisabled starts false, so every window build clears
	// button keys until the first successful loading-to-battle hand-off.
	// Nothing restores it when a battle returns to the frontend
	// [07 R-WGT-01 §3].
	quickKeyPreclearDisabled bool

	// frontend owns mode, active authored window, save-under predecessor, modal
	// message, focus/press latches, and list thumb capture [07 §3][07 §4].
	frontend *ui.Frontend

	cam    *camera.Camera
	battle *battleSession
}

func (g *gameShell) widgetTimerAdvanced(p *ui.Panel) bool {
	if g == nil || p == nil {
		return false
	}
	if g.widgetMillis == nil {
		g.widgetMillis = newMonotonicMillisSource()
	}
	return p.TimerAdvanced(clock.ScaledNow(g.widgetMillis.Millis32()))
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
	copy(shell.mapLabels, maps)
	mapName := ""
	if len(maps) != 0 {
		mapName = maps[0]
	}
	shell.setup = newSkirmishMenuConfig(mapName)
	shell.missionDifficultyValue = session.SkirmishDefaultDifficulty
	shell.scrollSpeed = settings.DefaultScrollSpeed // [02 "Settings"] [07 §10]
	shell.presentation = startupPresentation(opts, settings.DefaultPresentation())
	shell.setGameplay(startupGameplay(opts, gameplay.Modern))
	shell.display = settings.DefaultDisplay()   // [02 R-KEYS-01 §5]
	shell.messages = settings.DefaultMessages() // [02 §3]
	// The audio block, the game-speed word and the `Interface Type` word are
	// the options family's remaining stores [03 R-AUD-01 §2][07 R-CAM-01 §7]
	// [07 R-CAM-01 §5].
	shell.audioPrefs = settings.DefaultAudio()
	shell.gameSpeed = settings.DefaultGameSpeed
	shell.interfaceType = settings.DefaultInterfaceType
	shell.switchAlt = settings.DefaultSwitchAlt != 0
	shell.clockVisible = settings.DefaultClock != 0
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
	// The interface alias table is a startup preload, not a first-click lazy
	// path: mode 0 (alias registration) probe-decodes every allsound.tdf
	// alias once, at construction, into a session-lifetime table
	// [03 R-AUD-01 §1 mode 0][03 §8.3 "Alias registration"] — the same
	// bring-up timing as the two FNT loads just above (`shell.font` /
	// `shell.assets.font`), which retail also finishes "before the shell
	// opens" [03 R-FONT-01 §5]. Calling ensureFrontendAudio here (idempotent;
	// playMenuCue still calls it lazily for any caller that skips this
	// constructor) moves that fixed cost off the first interface click, where
	// a play-test reported it as visible latency (WU-19-224; measured well
	// under retail's own cost budget — see the commit message for numbers).
	shell.ensureFrontendAudio()
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
	if err := validatePresentationZoom(shell.opts); err != nil {
		return err
	}
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
		// The shell's client morphs into the battle's, so the Enhanced blend's
		// fraction producer follows whichever battle is live; outside a battle
		// there is no tick to be part-way through
		// (docs/DESIGN_GPU_RENDERER.md §13.5).
		TickFraction: func() float32 {
			if shell.battle == nil {
				return 0
			}
			return shell.battle.tickFraction()
		},
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	clPtr = cl // entering a battle morphs THIS client
	cl.SetModelFS(cs.fs)
	cl.SetCamera(shell.cam)
	// The preferences were read before the client existed, so the three
	// display-option bits reach it here [07 R-FE-01 §6].
	shell.applyRetailVisualOptions(cl)
	applyGammaOption(cl, shell.display.Gamma)
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
	shell.queueStartupMovie()
	defer shell.closeIntro(cl)
	return ebitenapp.Run(cl, rendererMode(shell.opts), shell.windowOptions())
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
	// single-player frontend. The bitmap path is fatal; the GUI opener has no
	// recovery branch. Nanolathe reports a failed initial screen at startup and
	// retains unavailable children for refusal at their open boundary [07 §5
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
			// MAINMENU is needed before any usable parent exists. A missing child
			// can be refused when selected, retaining that parent [07 §5
			// "Frontend asset failure boundaries"].
			if spec.mode != modeMenuMain && panel != nil && panel.unavailable != nil {
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

func loadRetailPanelStrict(cs *contentSet, guiName, pcxName, gafName, expected string) (*retailPanelAssets, error) {
	if cs == nil || cs.fs == nil {
		return nil, fmt.Errorf("nanolathe: retail frontend panel: logical path %s, providers searched [], expected %s", guiName, expected)
	}
	p := &retailPanelAssets{}
	if w, err := cs.loadGUI(guiName); err != nil {
		// Deliberate divergence, and the divergence is the point. Retail's
		// shared window opener does not test its own result: when the file
		// cannot be opened it skips the whole allocate-and-parse block and
		// still runs its tail, which copies the window's name through a window
		// pointer it never assigned. That pointer is zero, so a front-end
		// screen whose `.GUI` is missing faults the process — no diagnostic,
		// no fallback, and in particular no empty layout
		// [07 §5 "Frontend asset failure boundaries"]. Nanolathe reports
		// instead: an explicit unavailable panel, never a partial one.
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
	// Refuse an unavailable child before changing the parent or its gesture
	// state. MSGBOX may also be absent, so preserve the original diagnostic
	// through the host error channel [07 §5 "Frontend asset failure boundaries"].
	if g.assets != nil && mode <= modeMenuSkirmish {
		p := g.assets.panel[mode]
		if p == nil || p.window == nil {
			err := fmt.Errorf("nanolathe: frontend screen unavailable: logical path screen/%d, providers searched [vfs], expected authored GUI", mode)
			if p != nil && p.unavailable != nil {
				err = p.unavailable
			}
			if g.frontend != nil {
				if messageErr := g.showRetailMessage(err.Error()); messageErr != nil {
					reportRetailMessageError(errors.Join(err, messageErr))
				}
			} else {
				reportRetailMessageError(err)
			}
			return
		}
	}
	if g.frontend == nil {
		g.frontend = ui.NewFrontend(mode)
	}
	if clPtr != nil && clPtr.Input() != nil {
		clPtr.Input().DrainTokens()
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
		if p := g.assets.panel[mode]; p != nil && p.window != nil {
			// The cached window is the parsed definition. Each open begins from
			// a fresh record set so startup preclear never erases the authored
			// keys needed after the first battle [07 R-WGT-01 §3].
			window := gui.CloneWindow(p.window)
			if mode == modeMenuMission {
				g.applyRetailMissionLayout(window)
			}
			if mode == modeMenuSkirmish {
				// Screen entry copies the lobby selector into the shared session
				// word before building its controls [08 "Skirmish configuration"].
				g.missionDifficultyValue = g.setup.Difficulty
				g.installSkirmishDynamicGadgets(window)
			}
			// The builder sees runtime-appended controls and resolves all
			// records before Panel copies instance state. Repaints only use the
			// installed record and never reassign keys [07 R-WGT-01 §3].
			g.installRetailWindowButtonArt(window, p.art)
			g.installRetailListScrollbars(window, p.art)
			panel = ui.NewPanel(window)
			if mode == modeMenuMain {
				// Retail supplies this literal, reveals the authored label, and
				// shifts its fresh rectangle by half the primary-font width
				// (integer division) [07 R-FE-01 §3].
				const menuVersion = "v3.1"
				panel.SetActive("DebugString", true)
				panel.SetText("DebugString", menuVersion)
				if i := window.GadgetIndex("DebugString"); i >= 0 {
					window.Gadgets[i].Rect.X -= int32(g.retailTextWidth(menuVersion) / 2)
				}
			}
		}
	}
	saveUnder := oldPanel != nil && mode != oldMode && g.panelWindowNeedsUnder(mode)
	g.frontend.Open(mode, panel, saveUnder)
	if mode == modeMenuMain {
		g.armMenuBGM()
	} else {
		// The request is deferred only for the main screen's common step. A
		// transition before that step must not start it from loading or battle.
		g.menuBGMPending = false
	}
	g.refreshRetailPanel()
	if mode <= modeMenuSkirmish {
		// The shell loader and the post-battle controller both call the one
		// routine that forces the logical display size back to 640x480 and,
		// only when the window differs, re-creates the presentation surface
		// and the offscreen: the front end always runs at 640x480 whatever
		// `DisplaymodeWidth`/`Height` hold [07 R-FE-02 §2]. Every authored
		// menu screen is opened through here, so this is that routine's site.
		// The battle is excluded: its size is the other direction, applied by
		// the load transition's second half [07 "The loading screen"]. The
		// loading screen is excluded here only because it sets 640x480 itself,
		// at the transition that opens it.
		g.applyDisplaySize(clPtr, retailScreenW, retailScreenH)
	}
}

// applyDisplaySize moves the presentation surface to one logical size. It is
// retail's "compare to the current window size and, when different, resize the
// window, re-select the mode and re-create the offscreen" step, in both of its
// directions [07 R-FE-02 §2][07 "The loading screen"].
//
// The client owns the size and the offscreen; the platform adapter follows it
// for the window and the uploaded image, so nothing here reaches a device.
// The shell's own camera viewport follows too, because the front end's camera
// is the surface [I6].
func (g *gameShell) applyDisplaySize(cl *client.Client, width, height int) {
	if g == nil || cl == nil {
		return
	}
	if width < settings.MinDisplaymodeWidth || height < settings.MinDisplaymodeHeight {
		width, height = retailScreenW, retailScreenH
	}
	if w, h := cl.Size(); w == width && h == height {
		return
	}
	cl.Resize(width, height)
	if g.cam != nil {
		g.cam.ViewW, g.cam.ViewH = int32(width), int32(height)
		g.cam.Clamp()
		// Refit a retained overview to the new floor, including one that had
		// already settled. Its old factor may no longer be tactical (§16.7).
		if g.battle != nil && g.battle.cam == g.cam && cl.Enhanced() && g.cam.RequestedZoom() < camera.ZoomUnit {
			mx, my := battleViewCentre(g.cam)
			jumpBattleZoom(g.battle, mx, my, g.cam.RequestedZoom(), true)
		}
	}
}

// applyDisplayMode is the load transition's second half: the stored
// `DisplaymodeWidth`/`Height` pair is compared to the current window size and
// the surface follows it when they differ. It runs once the load has completed
// and before the in-game HUD opens, so the loading screen itself stays at
// 640x480 and the battle composes at the chosen size
// [07 "The loading screen"].
//
// The battle chrome is not scaled to the new size; it extends by rule
// [07 R-HUD-05]: the world viewport takes `(128,32)..(W-1,H-33)` [03 §4.1],
// the bottom strip moves to `H-32` and both horizontal strips are stamped
// rightward to the surface edge, the left rail keeps its authored 129x480 art
// with palette index 0 below it, the footer's anchors shift by `H - baseheight`
// and the modal windows re-centre in the live view. The authored `.GUI` rail
// pages keep their coordinates. The battle composer reads the surface size at
// draw time, so nothing here has to reach into it.
func (g *gameShell) applyDisplayMode(cl *client.Client) {
	if g == nil {
		return
	}
	g.applyDisplaySize(cl, g.display.Width, g.display.Height)
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
	if g.startupMoviePending {
		g.startupMoviePending = false
		reportRetailMessageError(g.startMovie(cl, startupMoviePath, false))
		return
	}
	if g.intro != nil {
		g.stepIntro(delta, cl)
		return
	}
	pumpAudio(time.Now())
	if cl != nil && cl.IsFocused() && g.audioOwner != nil && g.audioOwner.Music != nil {
		serviceMusic(g.audioOwner)
	}
	g.playPendingMenuBGM()
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
		return fmt.Errorf("nanolathe: battle entry failed: no shell or session")
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
	battle.beginBattleArrival(g.opts, clPtr, savedCamera != nil)
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
	// Save restoration reaches this point only after both detached candidates
	// are ready. Keep frontend cues alive on preflight failure, then stop the
	// ordinary table at this successful commitment [03 R-AUD-01 §1].
	// Direct entry can reach battle before the initial menu has had a frame
	// to start its music. Retire that pending cue with the successful menu
	// transition, so the next shell step cannot start it over the battle.
	g.menuBGMPending = false
	g.stopOrdinaryAudio()
	if g.battle != nil {
		g.battle.teardown(clPtr)
	}
	g.battle = nil
	g.cam = nil
	// The art the loading goroutine synthesized for this world, if this
	// candidate is the one that load produced (DESIGN_GPU_RENDERER §14.3).
	battle.detail = g.pendingDetail
	g.pendingDetail = nil
	installBattleClient(clPtr, battle)
	g.cam = battle.cam
	// The battle viewport is the presentation surface. Battle composition
	// builds the camera at the authored 640x480 design size; the surface may
	// already be at the chosen display mode, so the viewport is squared with
	// it here, at the one render-thread installation point that sees both
	// [07 "The loading screen"].
	if clPtr != nil && g.cam != nil {
		if w, h := clPtr.Size(); w > 0 && h > 0 {
			g.cam.ViewW, g.cam.ViewH = int32(w), int32(h)
			g.cam.Clamp()
		}
	}
	// `--zoom` is a start-up view scale, so it is applied once the viewport is
	// the surface's, about the battle viewport's centre (§14.6).
	applyEntryZoom(g.opts, battle)
	g.applyRetailVisualOptions(clPtr)
	g.battle = battle
	g.battle.returnToMenu = g.returnFromBattle
	g.battle.restartBattle = g.restartBattle
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
	// This is the successful end of loading-to-battle. The candidate's loading
	// windows were built before this point; failed loads never reach it
	// [07 R-WGT-01 §3].
	g.quickKeyPreclearDisabled = true
}

// returnFromBattle is the retail MAINMENU confirmation outcome: discard the
// live battle presentation and restore the main frontend window in the same
// client. The abandoned session has no background goroutine and becomes
// unreachable after this hand-off.
func (g *gameShell) returnFromBattle(cl *client.Client) {
	if g == nil {
		return
	}
	var retired *session.Session
	if g.battle != nil {
		retired = g.battle.sess
	}
	g.teardownBattle(cl)
	// Teardown records the current mark before the battle loses its session.
	// Retain that bank for the returning frontend; a fresh campaign selection
	// remains the separate reset owner [08 R-CAMP-01 §7][08 R-CAMP-01 §8].
	if retired != nil && retired.Mission != nil && retired.Mission.Type == mission.TypeCampaign {
		g.campaignProgress = retired.Progress
		g.campaignProgressSet = true
	}
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
		return nil, fmt.Errorf("nanolathe: skirmish map census failed: no mounted content")
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
	if g.startupMoviePending {
		w, h := c.Size()
		c.UIFillRect(0, 0, w, h, 0)
		return
	}
	if g.intro != nil {
		g.drawIntro(c)
		return
	}
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
