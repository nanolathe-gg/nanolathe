package main

import (
	"errors"
	"fmt"
	"os"
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
	modeBattle
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

	campaigns              []mission.Campaign
	campaignOptions        []mission.Campaign
	campaignIdx            int
	missionIdx             int
	missionAny             bool
	missionSide            int
	missionDifficultyValue int

	panel        *retailPanelState
	modal        *retailPanelState
	modalPressed bool

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

// runGameShell is the windowed entry: retail menus by default; straight into
// the battle view when --map was supplied (the established development path).
func runGameShell(opts Options, cs *contentSet) error {
	if opts.Map != "" {
		return runBattleView(opts, cs)
	}

	shell := &gameShell{opts: opts, cs: cs, mode: modeMenuMain}
	maps, err := enumerateSkirmishMaps(cs.fs)
	if err != nil {
		return err
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
	clPtr = cl // startBattle morphs THIS client when a skirmish starts
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
	a.panel[modeMenuMap] = loadRetailPanel(cs, "guis/selmap.gui", "bitmaps/selectgame2x.pcx", "")
	a.panel[modeMenuSkirmish] = loadRetailPanel(cs, "guis/skirmish.gui", "bitmaps/skirmsetup4x.pcx", "anims/skirmish.gaf")
	a.message = loadRetailPanel(cs, "guis/msgbox.gui", "", "")
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

func (g *gameShell) step(delta float64, cl *client.Client) {
	switch g.mode {
	case modeBattle:
		if g.battle != nil {
			g.battle.viewerStep(delta, cl)
		}
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

// startBattle transitions the shell into the live skirmish view in-process.
func (g *gameShell) startBattle(mapName string) error {
	opts := g.opts
	opts.Map = mapName
	cfg := g.skirmishConfigForStart(mapName)
	sess, cat, err := newBattleSessionWithConfig(opts, g.cs, cfg)
	if err != nil {
		return err
	}
	return g.enterBattle(sess, cat)
}

func (g *gameShell) startMission() error {
	if g.campaignIdx < 0 || g.campaignIdx >= len(g.campaignOptions) {
		return fmt.Errorf("no campaign selected")
	}
	c := g.campaignOptions[g.campaignIdx]
	if g.missionIdx < 0 || g.missionIdx >= len(c.Missions) {
		return fmt.Errorf("no mission selected")
	}
	cat, err := content.Compile(g.cs.fs)
	if err != nil {
		return fmt.Errorf("catalog: %w", err)
	}
	stub := c.Missions[g.missionIdx]
	path := fmt.Sprintf("%s:MISSION%d", c.Path, stub.Index)
	sess, err := session.NewMissionWithFS(g.cs.fs, cat, path, g.missionDifficulty())
	if err != nil {
		return err
	}
	return g.enterBattle(sess, cat)
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
		names = append(names, key)
	}
	return names, nil
}

func (g *gameShell) draw(c *client.Client) {
	if g.mode == modeBattle {
		if g.battle != nil && g.battle.hud != nil {
			g.battle.hud.draw(c, g.battle)
		}
		return
	}
	g.drawRetailPanel(c)
	g.drawRetailModal(c)
}
