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
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/ui"
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
// kept in ui.Panel in retail_menu.go and is reset whenever retail
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
	scrollSpeed      int // persisted scrollspeed [02 "Settings"] [07 §10] C2 presentation-only

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

	// panels owns the active authored window, its save-under predecessor, modal
	// message, focus/press latches, and list thumb capture [07 §3][07 §4].
	panels ui.PanelStack

	cam    *camera.Camera
	battle *battleSession
}

// newGameShell builds the frontend state: the skirmish map list, the retail
// resource set, and the opening panel used by the windowed entry.
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
	shell.scrollSpeed = settings.DefaultScrollSpeed // [02 "Settings"] [07 §10]
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
	buf := &frame.Buffer{}
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
	// Software cursor [07 §8]. The cursor GAF is mandatory for the windowed
	// frontend, and installation happens before entering Ebitengine's loop.
	cursors, cerr := client.LoadCursors(cs.fs)
	if cerr != nil {
		return cerr
	}
	cl.SetCursors(cursors)
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
	oldPanel, oldMode := g.activePanel(), g.mode
	if oldPanel != nil {
		// Clear a gesture before replacing the active window. This used to sit
		// after panel=nil and was unreachable, allowing a held press to leak
		// into the next authored screen [07 §3].
		oldPanel.ResetPress()
		oldPanel.CancelScrollDrag()
	}
	g.mode = mode
	g.panels.CloseModal()
	var panel *ui.Panel
	if g.assets != nil {
		if mode == modeMenuMission {
			// NEWGAME.GUI is reused for both New Campaign and Play Any Game.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// Any branch, so restore/apply that runtime mutation before the
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
	if panel == nil {
		g.panels.Replace(nil)
	} else if oldPanel != nil && mode != oldMode && g.panelWindowNeedsUnder(mode) {
		g.panels.Push(panel)
	} else {
		g.panels.Replace(panel)
	}
	g.refreshRetailPanel()
	g.resolveRetailButtonGeometry()
}

func (g *gameShell) activePanel() *ui.Panel {
	if g == nil {
		return nil
	}
	if modal := g.panels.Modal(); modal != nil {
		return g.panels.Under()
	}
	return g.panels.Top()
}

func reportRetailMessageError(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
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
	centerOnCommanderForSession(sess, g.cam, winW, winH)
	pal := loadPalette(g.cs)
	battleHUD, err := loadRetailBattleHUD(g.cs.fs, sess, cat, pal)
	if err != nil {
		return err
	}
	g.battle = &battleSession{sess: sess, cat: cat, cam: g.cam, hud: battleHUD, fs: g.cs.fs, shell: g, latch: input.LatchNormal}
	g.battle.returnToMenu = g.returnFromBattle
	g.battle.returnToSkirmish = func(cl *client.Client) {
		if g != nil {
			detachBattleAudio(cl, sess)
			g.battle = nil
			g.openMenu(modeMenuSkirmish)
			if cl != nil {
				cl.SetSnapshot(&frame.Buffer{})
				cl.SetTerrain(nil)
				cl.SetCamera(g.cam)
				if g.assets != nil && g.assets.pal != nil {
					cl.SetPalette(g.assets.pal)
				}
				if g.assets != nil {
					cl.SetFNT(g.assets.font)
				}
				cl.Overlay = func(c *client.Client) { g.draw(c) }
			}
		}
	}
	g.battle.retryFunc = func(cl *client.Client) {
		if g == nil || g.battle == nil {
			return
		}
		// RS-05 retry: recreate clean session from same SkirmishConfig via state graph 7→2→5 [08]
		cfg := g.battle.sess.Skirmish
		cat2 := g.battle.sess.Catalog
		if cat2 == nil {
			cat2 = cat
		}
		newSess, err := session.NewSkirmishWithFS(g.cs.fs, cat2, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "nanolathe: retry failed: %v\n", err)
			return
		}
		// Replace battle session with clean one [RS-05] no duplicate callbacks
		g.battle.sess = newSess
		g.battle.cat = newSess.Catalog
		g.battle.resultDismissed = false
		g.battle.resultButtons = nil
		centerOnCommanderForSession(newSess, g.cam, winW, winH)
		if cl != nil {
			cl.SetSnapshot(newSess.Snapshot)
			cl.SetTerrain(newSess.World)
			cl.SetCamera(g.cam)
		}
		newSess.State = session.StateBattle
		// Rebind HUD for new side
		if pal2 := loadPalette(g.cs); pal2 != nil {
			if hud2, err2 := loadRetailBattleHUD(g.cs.fs, newSess, newSess.Catalog, pal2); err2 == nil {
				g.battle.hud = hud2
				if cl != nil {
					cl.SetFNT(hud2.console)
					cl.Overlay = func(c *client.Client) { hud2.draw(c, g.battle) }
				}
			}
		}
	}
	g.battle.continueFunc = func(cl *client.Client) {
		if g == nil || g.battle == nil || g.battle.sess == nil {
			return
		}
		s := g.battle.sess
		if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			// Determine win vs loss for progression. [08 "Progression"] latch bits 0x10 win / 0x40 lose, and Progress.WL slot.
			// Victory is AND across victory triggers, defeat OR, victory first [08 "Evaluation"]; latch arms 4→-1 ~150 ticks [P1-01].
			isWin := false
			if s.CampaignSlot >= 0 && s.CampaignSlot < len(s.Progress.WL) && s.Progress.WL[s.CampaignSlot] == 'W' {
				isWin = true
			} else if s.Latch.IsWin() {
				isWin = true
			} else if s.VictoryDone && !s.DefeatDone {
				isWin = true
			} else {
				if r := s.GetResult(); r.Ended && r.Kind == "victory" {
					isWin = true
				}
			}
			// Also consider Progress.WL at mission's provenance index when CampaignSlot not set correctly for old saves.
			if !isWin && s.Mission != nil && s.Mission.CampaignIndex >= 0 && s.Mission.CampaignIndex < len(s.Progress.WL) && s.Progress.WL[s.Mission.CampaignIndex] == 'W' {
				isWin = true
			}
			if isWin {
				campaignPath := ""
				curIdx := -1
				difficulty := -1
				if s.Mission != nil {
					campaignPath = s.Mission.CampaignPath
					curIdx = s.Mission.CampaignIndex
					difficulty = s.Mission.Difficulty
				}
				// Fallback: CampaignSlot holds mission list slot [P1-01 §2.3]; prefer Mission provenance when present.
				if campaignPath == "" && s.CampaignSlot >= 0 {
					// TODO(question): campaign file provenance not retained for old sessions without Mission.CampaignPath; cannot determine next deterministically. Fallback to menu.
					_ = s.ContinueCampaign()
					g.returnFromBattle(cl)
					return
				}
				if curIdx < 0 {
					curIdx = s.CampaignSlot
				}
				if difficulty < 0 {
					difficulty = g.missionDifficulty()
				}
				// Ensure progress W/L is written exactly once [P1-01 §2.3] before advancing.
				_ = s.ContinueCampaign()
				nextIdx, hasNext, err := mission.NextCampaignMission(g.cs.fs, campaignPath, curIdx)
				if err != nil || !hasNext {
					// Campaign complete or discovery error: return to menu; no next mission to load.
					// TODO(question): retail briefing/report/credits sequence between missions not established [08 "Progression"] [07 §11]; treat as return to main.
					g.returnFromBattle(cl)
					return
				}
				// Load next mission linear advance cur+1 [07 §11] [08 "Progression"] contiguous MISSION0..N until first gap [08 "Campaign discovery"] C1.
				nextPath := fmt.Sprintf("%s:MISSION%d", campaignPath, nextIdx)
				// Copy progress so new session retains W/L history [P0-05][P1-01 §2.3] Summary/BetweenMissions bank split TODO(question) exact persistence.
				prevProgress := s.Progress
				prevSlot := curIdx
				g.beginLoad("", modeMenuMission, func(state *loadingState) (*session.Session, error) {
					sess2, err := session.NewMissionWithProgress(g.cs.fs, nil, nextPath, difficulty, state.report)
					if err != nil {
						return nil, err
					}
					// Retain campaign progression history in new session.
					sess2.Progress = prevProgress
					// Ensure the winning slot is marked 'W' if latch write was missed.
					if prevSlot >= 0 && prevSlot < len(sess2.Progress.WL) && sess2.Progress.WL[prevSlot] == 0 {
						sess2.Progress.WL[prevSlot] = 'W'
					}
					sess2.CampaignSlot = nextIdx
					return sess2, nil
				})
				return
			}
			// Losing path: retail Continue after defeat not established as auto-retry [07 §11]; keep menu return.
			// TODO(question): losing Continue vs Retry distinction not established; current behavior returns to main, Retry button handles same-mission reload [P1-01 §7.5].
			_ = s.ContinueCampaign()
			g.returnFromBattle(cl)
			return
		}
		g.returnFromBattle(cl)
	}
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
		// The menu morphs its own client into the battle rather than building a
		// new one, so it must make the same session joins the direct battle
		// entry makes. Without this the client has no presentation CRT, and
		// every consumer of that stream degrades silently: nano particles all
		// draw the same trajectory, screen shake never jitters, and no cue
		// reaches the audio device [01 §7.2][03 §5.5][03 §5.6][03 §8.3].
		attachBattleAudio(clPtr, sess, g.cs.fs)
	}
	return nil
}

// returnFromBattle is the retail MAINMENU confirmation outcome: discard the
// live battle presentation and restore the main frontend window in the same
// client. The abandoned session has no background goroutine and becomes
// unreachable after this hand-off.
func (g *gameShell) returnFromBattle(cl *client.Client) {
	if g == nil {
		return
	}
	if g.battle != nil {
		g.battle.closeBattleMenu()
		// Release what entering the battle joined, so the abandoned session's
		// music stops and the device is not held across the hand-off.
		detachBattleAudio(cl, g.battle.sess)
	}
	g.battle = nil
	g.cam = &camera.Camera{ViewW: retailScreenW, ViewH: retailScreenH, MapW: retailScreenW, MapH: retailScreenH}
	g.openMenu(modeMenuMain)
	if cl == nil {
		return
	}
	cl.SetSnapshot(&frame.Buffer{})
	cl.SetTerrain(nil)
	cl.SetCamera(g.cam)
	if g.assets != nil {
		if g.assets.pal != nil {
			cl.SetPalette(g.assets.pal)
		}
		cl.SetFNT(g.assets.font)
	}
	cl.Overlay = func(c *client.Client) { g.draw(c) }
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
