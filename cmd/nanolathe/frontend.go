package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/vfs"
)

// shellMode is the retail frontend state. The menu screens deliberately map
// one-to-one to the retail GUI files; there is no Nanolathe-owned layout.
type shellMode uint8

const (
	modeMenuMain shellMode = iota
	modeMenuSingle
	modeMenuMission
	modeMenuMap
	modeMenuSkirmish
	// modeLoading is the retail loading screen. It owns no .GUI file: retail
	// closes the frontend window, forces 640x480, and paints the screen from
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	modeLoading
	modeBattle
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
}

// menuAssets is the mounted retail frontend resource set. All menu pixels,
// widgets and text font come from the same files TotalA.exe selects.
type menuAssets struct {
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
}

// gameShell owns only frontend state and the battle hand-off. Menu state is
// kept in retailPanelState in retail_menu.go and is reset whenever retail
// opens a new .GUI panel.
type gameShell struct {
	opts Options
	cs   *contentSet

	mode   shellMode
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

	campaigns              []mission.Campaign
	campaignOptions        []mission.Campaign
	campaignIdx            int
	missionIdx             int
	missionAny             bool
	missionSide            int
	missionDifficultyValue int

	// loading is live only while mode is modeLoading. The loader runs on its
	// own goroutine, so the shell reads its progress and adopts its result
	// from the render goroutine only.
	loading *loadingState
	// loadingReturn is the screen a failed load falls back to.
	loadingReturn shellMode

	panel        *retailPanelState
	modal        *retailPanelState
	modalPressed bool

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// window in front of the one already open and keeps a SAVE UNDER surface
	// for it, so a panel window with no background bitmap — SELMAP.GUI is the
	// single-player case — is drawn over the screen beneath it rather than
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	under     *retailPanelState
	underMode shellMode

	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// left button is down on an associated list scrollbar.  It is presentation
	// state only; the list's top item remains the model used by the authored
	// MAPNAMES/Campaign/Missions controls.
	scrollDrag         retailScrollDrag
	retailPressed      int
	retailRightPressed int

	cam    *camera.Camera
	battle *battleSession
}

// newGameShell builds the frontend state: the skirmish map list, the retail
// resource set, and the opening panel. The windowed entry and the headless
// menu screenshot path share it so a captured frame is the same composition
// the window shows.
func newGameShell(opts Options, cs *contentSet) (*gameShell, error) {
	shell := &gameShell{opts: opts, cs: cs, mode: modeMenuMain}
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
	shell.assets = loadMenuAssets(cs)
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	shell.attachSettings()
	maps := shell.maps

	const winW, winH = 640, 480
	shell.cam = &camera.Camera{X: 0, Z: 0, ViewW: winW, ViewH: winH, MapW: winW, MapH: winH}
	buf := &snapshot.Buffer{}
	var cl *client.Client
	cl, err = client.New(client.Options{
		Buffer:   buf,
		Width:    winW,
		Height:   winH,
		Title:    "Nanolathe",
		Headless: false,
		Step:     func(delta float64) { shell.step(delta, cl) },
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
	// Software cursor [07 §8]. A missing cursor GAF is not fatal: the shell
	// falls back to the window system's own pointer.
	if cursors, cerr := client.LoadCursors(cs.fs); cerr == nil {
		cl.SetCursors(cursors)
	} else {
		fmt.Fprintf(os.Stderr, "nanolathe: %v\n", cerr)
	}
	cl.Overlay = func(c *client.Client) { shell.draw(c) }
	fmt.Fprintf(os.Stderr, "nanolathe: retail frontend: %d skirmish maps\n", len(maps))
	return client.RunGame(cl)
}

func loadMenuAssets(cs *contentSet) *menuAssets {
	a := &menuAssets{panel: make(map[shellMode]*retailPanelAssets)}
	if cs == nil || cs.fs == nil {
		return a
	}
	if g, err := formats.LoadGAFFile(cs.fs, "anims/commongui.gaf"); err == nil {
		a.common = g
	}
	if g, err := formats.LoadGAFFile(cs.fs, "textures/logos.gaf"); err == nil {
		a.logos = g
	}
	if f, err := formats.LoadFNTFile(cs.fs, "fonts/comix.fnt"); err == nil {
		a.font = f
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// hattfont11 as secondary slot 1. Generic frontend controls prefer the
	// primary GAF font over the active COMIX FNT [07 §4].
	if f, err := formats.LoadGAFFile(cs.fs, "anims/hattfont12.gaf"); err == nil {
		a.gafFont = f
	}
	if f, err := formats.LoadGAFFile(cs.fs, "anims/hattfont11.gaf"); err == nil {
		a.gafFontSmall = f
	}
	if p, err := palette.Load(cs.fs); err == nil {
		a.pal = p
	}
	a.panel[modeMenuMain] = loadRetailPanel(cs, "guis/mainmenu.gui", "bitmaps/frontendx.pcx", "anims/mainmenu.gaf")
	a.panel[modeMenuSingle] = loadRetailPanel(cs, "guis/single.gui", "bitmaps/singlebg.pcx", "anims/single.gaf")
	a.panel[modeMenuMission] = loadRetailPanel(cs, "guis/newgame.gui", "bitmaps/newcampaign4x.pcx", "anims/newgame.gaf")
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// The file is 640x480 but its panel art occupies only the top-left
	// 494x420, matching the window's own 494x420 record at (84,12), so it is
	// drawn at the window origin and clipped to the window [07 §4].
	// bitmaps/selectgame2x.pcx belongs to the multiplayer SELGAME.GUI lobby
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	a.panel[modeMenuMap] = loadRetailPanel(cs, "guis/selmap.gui", "bitmaps/dselectmap2.pcx", "")
	a.panel[modeMenuSkirmish] = loadRetailPanel(cs, "guis/skirmish.gui", "bitmaps/skirmsetup4x.pcx", "anims/skirmish.gaf")
	a.message = loadRetailPanel(cs, "guis/msgbox.gui", "", "")
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// open, so it becomes the global background the loading screen repaints
	// from [07 §4].
	if p, err := formats.LoadPCXFile(cs.fs, "bitmaps/loadgame2bg.pcx"); err == nil {
		a.loading = p
	}
	if p, err := formats.LoadPCXFile(cs.fs, "bitmaps/newcampaign4.pcx"); err == nil {
		a.missionCampaign = p
	}
	if p, err := formats.LoadPCXFile(cs.fs, "bitmaps/newcampaign4x.pcx"); err == nil {
		a.missionSmall = p
	}
	if p, err := formats.LoadPCXFile(cs.fs, "bitmaps/playanygame4.pcx"); err == nil {
		a.missionAny = p
	}
	return a
}

func loadRetailPanel(cs *contentSet, guiName, pcxName, gafName string) *retailPanelAssets {
	if cs == nil || cs.fs == nil {
		return nil
	}
	p := &retailPanelAssets{}
	if w, err := gui.Load(cs.fs, guiName); err == nil {
		p.window = w
	}
	if pcxName != "" {
		if bg, err := formats.LoadPCXFile(cs.fs, pcxName); err == nil {
			p.background = bg
		}
	}
	if gafName != "" {
		if g, err := formats.LoadGAFFile(cs.fs, gafName); err == nil {
			p.art = g
		}
	}
	return p
}

func (g *gameShell) openMenu(mode shellMode) {
	g.under, g.underMode = nil, mode
	if g.panel != nil && mode != g.mode && g.panelWindowNeedsUnder(mode) {
		g.under, g.underMode = g.panel, g.mode
	}
	g.mode = mode
	g.panel = nil
	g.modal = nil
	g.modalPressed = false
	g.scrollDrag = retailScrollDrag{}
	g.retailPressed = -1
	g.retailRightPressed = -1
	if g.assets != nil {
		if mode == modeMenuMission {
			// NEWGAME.GUI is reused for both New Campaign and Play Any Game.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// Any branch, so restore/apply that runtime mutation before the
			// panel state takes its snapshot.
			g.applyRetailMissionLayout()
		}
		if p := g.assets.panel[mode]; p != nil && p.window != nil {
			g.panel = newRetailPanelState(p.window)
		}
	}
	if mode == modeMenuSkirmish {
		g.installSkirmishDynamicGadgets()
		if g.assets != nil {
			if p := g.assets.panel[mode]; p != nil && p.window != nil {
				g.panel = newRetailPanelState(p.window)
			}
		}
	}
	g.refreshRetailPanel()
	g.resolveRetailButtonGeometry()
}

// panelWindowNeedsUnder reports whether opening mode pushes a panel window
// onto the chain rather than replacing the screen. The test is the retail one:
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// and height, positioned at the window origin, and copies the screen into it
// before anything is painted. A window smaller than the display therefore
// never erases what is under it, and retail keeps a SAVE UNDER copy so it can
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
	switch g.mode {
	case modeBattle:
		if g.battle != nil {
			g.battle.viewerStep(delta, cl)
		}
	case modeLoading:
		// The transition that blocks on catalog and map loading installs the
		// hourglass shape [07 §8]; the frontend's own idle shape returns with
		// the next menu.
		if cursors := cl.Cursors(); cursors != nil {
			cursors.SetIndex(render.CursorHourglass)
			g.cursorAccum += delta * 30
			if n := int(g.cursorAccum); n > 0 {
				cursors.Step(n)
				g.cursorAccum -= float64(n)
			}
		}
		g.stepLoading(delta)
	default:
		// The front end uses the idle shape throughout; the loading shape is
		// installed by the transition that blocks on catalog and map loading
		// [07 §8]. Menu animation still advances at the renderer's cadence so a
		// visible hourglass keeps turning.
		if cursors := cl.Cursors(); cursors != nil {
			cursors.SetIndex(render.CursorNormal)
			g.cursorAccum += delta * 30
			if n := int(g.cursorAccum); n > 0 {
				cursors.Step(n)
				g.cursorAccum -= float64(n)
			}
		}
		g.menuInput(cl)
	}
}

func (g *gameShell) missionDifficulty() int {
	return g.missionDifficultyValue
}

func (g *gameShell) enterBattle(sess *session.Session, cat *content.Catalog) error {
	if sess == nil {
		return fmt.Errorf("nil session")
	}
	terrain := sess.World
	if terrain == nil {
		return fmt.Errorf("selected mission has no terrain data")
	}
	const winW, winH = 640, 480
	g.cam = &camera.Camera{
		X: 0, Z: 0, ViewW: winW, ViewH: winH,
		MapW: int32(terrain.CellW * 16), MapH: int32(terrain.CellH * 16),
	}
	g.cam.Pan(0, 0)
	centerOnCommander(sess.Units, g.cam, winW, winH)
	pal := loadPalette(g.cs)
	battleHUD, err := loadRetailBattleHUD(g.cs.fs, sess, cat, pal)
	if err != nil {
		return err
	}
	g.battle = &battleSession{sess: sess, cat: cat, cam: g.cam, hud: battleHUD, latch: input.LatchNormal}
	g.mode = modeBattle
	if clPtr != nil {
		clPtr.SetSnapshot(sess.Snapshot)
		clPtr.SetTerrain(terrain)
		clPtr.SetCamera(g.cam)
		if pal != nil {
			clPtr.SetPalette(pal)
		}
		clPtr.SetFNT(battleHUD.console)
		clPtr.Overlay = func(c *client.Client) { battleHUD.draw(c, g.battle) }
	}
	return nil
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
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// records, not a folded copy: the localized-string lookup is only
		// consulted when it returns something different from the stem.
		names = append(names, base)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// comparison is _stricmp, so the authored MAPNAMES order is ascending and
	// case-insensitive, not archive order [07 §4].
	sort.SliceStable(names, func(i, j int) bool { return retailStricmp(names[i], names[j]) < 0 })
	return names, nil
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

func (g *gameShell) draw(c *client.Client) {
	if g.mode == modeLoading {
		g.drawLoadingScreen(c)
		return
	}
	if g.mode == modeBattle {
		if g.battle != nil && g.battle.hud != nil {
			g.battle.hud.draw(c, g.battle)
		}
		return
	}
	g.drawRetailPanel(c)
	g.drawRetailModal(c)
}
