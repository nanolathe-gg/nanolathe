package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/vfs"
)

// Front-end shell. The main menu is the retail MAINMENU.GUI panel drawn with
// its retail art: BackTile background and BUTTONS0 staged buttons from
// commongui.gaf, Credits art from mainmenu.gaf [fmt gui]. The skirmish map
// picker is a Nanolathe stand-in for retail's battle-setup flow (documented
// divergence until that .gui flow is implemented).

// shellMode selects the front-end screen.
type shellMode uint8

const (
	modeMenuMain     shellMode = iota // retail MAINMENU.GUI
	modeMenuSkirmish                  // Nanolathe stand-in map picker
	modeBattle                        // live battle view (client morphs)
)

// menuAssets holds the retail front-end art for the main menu.
type menuAssets struct {
	gui      *gui.Window
	commong  *formats.GAF
	mainmenu *formats.GAF
	fnt      *formats.FNT
	pal      *palette.Tables
}

// gameShell owns the window, the front-end flow, and the battle hand-off.
type gameShell struct {
	opts Options
	cs   *contentSet

	mode    shellMode
	assets  *menuAssets
	maps    []string // skirmish-capable map names (OTA with Network schema)
	mapIdx  int
	pressed int // gadget index currently pressed, -1 none

	cam    *camera.Camera
	battle *battleSession
	fnt    *formats.FNT

	clickX, clickY int32
	clickEdge      bool
	pickerHot      int // picker row under cursor, -1 none
	pickerPressed  int
}

// runGameShell is the windowed entry: menus by default; straight into the
// battle view when --map was supplied (dev/testing path).
func runGameShell(opts Options, cs *contentSet) error {
	shell := &gameShell{opts: opts, cs: cs}
	if opts.Map != "" {
		// startBattle only morphs an existing shell client; the --map path
		// has no menu, so launch the battle view with its own client.
		return runBattleView(opts, cs)
	}
	fmt.Fprintln(os.Stderr, "nanolathe: shell: enumerating skirmish maps")
	maps, err := enumerateSkirmishMaps(cs.fs)
	if err != nil {
		return err
	}
	if len(maps) == 0 {
		return fmt.Errorf("nanolathe: no skirmish-capable maps found under the mounted install")
	}
	shell.maps = maps
	shell.mode = modeMenuMain
	shell.pressed = -1
	shell.pickerHot = -1
	shell.pickerPressed = -1
	fmt.Fprintf(os.Stderr, "nanolathe: shell: %d skirmish maps, loading front-end art\n", len(maps))

	shell.assets = loadMenuAssets(cs)
	if shell.assets != nil && shell.assets.fnt != nil {
		shell.fnt = shell.assets.fnt
	}

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
	cl.SetCamera(shell.cam)
	if shell.assets != nil && shell.assets.pal != nil {
		cl.SetPalette(shell.assets.pal)
	}
	if shell.fnt != nil {
		cl.SetFNT(shell.fnt)
	}
	cl.Overlay = func(c *client.Client) { shell.draw(c) }
	fmt.Fprintln(os.Stderr, "nanolathe: shell: opening window")
	return client.RunGame(cl)
}

// loadMenuAssets loads the retail front-end art; any piece may fail nil and
// the shell falls back to flat panels so the menu still works.
func loadMenuAssets(cs *contentSet) *menuAssets {
	a := &menuAssets{}
	if w, err := gui.Load(cs.fs, "guis/mainmenu.gui"); err == nil {
		a.gui = w
	}
	if g, err := formats.LoadGAFFile(cs.fs, "anims/commongui.gaf"); err == nil {
		a.commong = g
	}
	if g, err := formats.LoadGAFFile(cs.fs, "anims/mainmenu.gaf"); err == nil {
		a.mainmenu = g
	}
	a.fnt = loadFNT(cs)
	if p, err := palette.Load(cs.fs); err == nil {
		// Front-end draws through GUIPAL [03 §4.3]; identity logical table.
		t := &palette.Tables{Base: p.GUI}
		for i := 0; i < 256; i++ {
			t.Logical[i] = byte(i)
		}
		a.pal = t
	}
	return a
}

// step dispatches by mode; menus tick nothing, battle drives its session.
func (g *gameShell) step(delta float64, cl *client.Client) {
	switch g.mode {
	case modeBattle:
		if g.battle != nil {
			g.battle.viewerStep(delta, cl)
		}
	default:
		g.menuInput(cl)
	}
}

// startBattle transitions the shell into the live battle view in-process.
func (g *gameShell) startBattle(mapName string) error {
	opts := g.opts
	opts.Map = mapName
	sess, cat, err := newBattleSession(opts, g.cs)
	if err != nil {
		return err
	}
	terrain := sess.World
	const winW, winH = 640, 480
	g.cam = &camera.Camera{
		X: 0, Z: 0,
		ViewW: winW, ViewH: winH,
		MapW: int32(terrain.CellW * 16), MapH: int32(terrain.CellH * 16),
	}
	g.cam.Pan(0, 0)
	centerOnCommander(sess.Units, g.cam, winW, winH)
	g.battle = &battleSession{sess: sess, cat: cat, cam: g.cam}
	g.mode = modeBattle
	if clPtr != nil {
		clPtr.SetSnapshot(sess.Snapshot)
		clPtr.SetTerrain(terrain)
		clPtr.SetCamera(g.cam)
		if pal := loadPalette(g.cs); pal != nil {
			clPtr.SetPalette(pal) // battle draws through PALETTE.PAL [03 §4.3]
		}
	}
	return nil
}

// enumerateSkirmishMaps lists map names whose OTA carries a Network schema —
// the only maps retail skirmish can select [08 "Schema choice"]. Names are the
// lowercase OTA basenames, sorted [I1].
func enumerateSkirmishMaps(fs *vfs.FS) ([]string, error) {
	var paths []string
	for _, e := range fs.Entries() {
		p := strings.ToLower(e.Path)
		if strings.HasPrefix(p, "maps/") && strings.HasSuffix(p, ".ota") {
			paths = append(paths, e.Path)
		}
	}
	sort.Strings(paths)
	seen := make(map[string]bool, len(paths))
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		ota, err := formats.LoadOTAFile(fs, p)
		if err != nil || !ota.HasNetworkSchema() {
			continue // mission-only map: retail skirmish cannot select it
		}
		base := p
		if i := strings.LastIndexByte(base, '/'); i >= 0 {
			base = base[i+1:]
		}
		base = strings.TrimSuffix(base, ".ota")
		base = strings.TrimSuffix(base, ".OTA")
		key := strings.ToLower(base)
		if seen[key] {
			continue
		}
		seen[key] = true
		names = append(names, key)
	}
	sort.Strings(names)
	return names, nil
}

// menuInput polls menu-mode input; no simulation ticks.
func (g *gameShell) menuInput(cl *client.Client) {
	in := cl.Input()
	if in == nil {
		return
	}
	mouse := in.Mouse
	kbd := in.Kbd
	if mouse.Pressed(input.MouseButtonLeft) {
		g.clickX, g.clickY = int32(mouse.X), int32(mouse.Y)
		g.clickEdge = true
		if g.mode == modeMenuMain {
			g.dispatchMenuClick()
		}
	}
	if g.mode == modeMenuSkirmish && len(g.maps) > 0 {
		if kbd.KeyDown(input.KeyUp) {
			g.mapIdx = (g.mapIdx - 1 + len(g.maps)) % len(g.maps)
		}
		if kbd.KeyDown(input.KeyDown) {
			g.mapIdx = (g.mapIdx + 1) % len(g.maps)
		}
	}
}

// fntTextWidth measures a string in the loaded font (0 when unavailable).
func fntTextWidth(fnt *formats.FNT, s string) int {
	if fnt == nil {
		return 0
	}
	w := 0
	for i := 0; i < len(s); i++ {
		if gl := fnt.Glyphs[s[i]]; gl != nil {
			w += int(gl.Width)
		}
	}
	return w
}

// draw renders the active front-end screen.
func (g *gameShell) draw(c *client.Client) {
	if g.mode == modeBattle {
		if g.battle != nil {
			g.battle.drawOverlay(c, g.fnt)
		}
		return
	}
	a := g.assets
	switch g.mode {
	case modeMenuMain:
		g.drawRetailMenu(c, a)
	case modeMenuSkirmish:
		g.drawRetailMenu(c, a) // retail panel stays under the stand-in picker
		g.drawPicker(c, a)
	}
	g.clickEdge = false
}

// drawRetailMenu renders MAINMENU.GUI with retail art: BackTile background,
// BUTTONS0 96×20 staged frames (12 rest, 13 pressed), Credits art [fmt gui].
func (g *gameShell) drawRetailMenu(c *client.Client, a *menuAssets) {
	c.UIFillRect(0, 0, 640, 480, 0)
	if a == nil {
		return
	}
	// Background: tiled BackTile from commongui.gaf.
	if a.commong != nil {
		if e, ok := a.commong.Find("BackTile"); ok && len(e.Frames) > 0 && e.Frames[0].Frame != nil {
			f := e.Frames[0].Frame
			for y := 0; y < 480; y += int(f.Height) {
				for x := 0; x < 640; x += int(f.Width) {
					c.UIBlit(f, x, y)
				}
			}
		}
	}
	if a.gui == nil {
		return
	}
	for i, gad := range a.gui.Gadgets {
		if i == 0 || gad.Active == 0 {
			continue
		}
		switch gad.Kind {
		case gui.KindButton:
			pressed := g.pressed == i
			g.drawButton(c, a, gad, pressed)
		case gui.KindLabel:
			// "Debug Build" label ships inactive in retail data; skip active=0
			// above, and draw any active label centered in its rect.
			w := fntTextWidth(g.fnt, gad.Text)
			x := int(gad.Rect.X) + int(gad.Rect.W)/2 - w/2
			y := int(gad.Rect.Y)
			c.UIText(g.fnt, gad.Text, x, y, 250)
		}
	}
}

// drawButton stamps the retail button frame and its centered label.
func (g *gameShell) drawButton(c *client.Client, a *menuAssets, gad gui.Gadget, pressed bool) {
	frame := -1
	if a.commong != nil {
		if e, ok := a.commong.Find("BUTTONS0"); ok {
			// Size-matched family: frames come in groups of four
			// (rest, pressed, disabled, spare) [fmt gui "Button"].
			for base := 0; base+3 < len(e.Frames); base += 4 {
				f0 := e.Frames[base].Frame
				if f0 == nil {
					continue
				}
				if int(f0.Width) == int(gad.Rect.W) && int(f0.Height) == int(gad.Rect.H) {
					if pressed {
						frame = base + 1
					} else {
						frame = base
					}
					break
				}
			}
			if frame >= 0 {
				c.UIBlit(e.Frames[frame].Frame, int(gad.Rect.X), int(gad.Rect.Y))
			}
		}
	}
	// Credits gadget uses its own art entry from mainmenu.gaf.
	if gad.Name == "Credits" && a.mainmenu != nil {
		if e, ok := a.mainmenu.Find("Credits"); ok && len(e.Frames) > 0 {
			c.UIBlit(e.Frames[0].Frame, int(gad.Rect.X), int(gad.Rect.Y))
		}
	}
	if gad.Text == "" || g.fnt == nil {
		return
	}
	w := fntTextWidth(g.fnt, gad.Text)
	x := int(gad.Rect.X) + int(gad.Rect.W)/2 - w/2
	y := int(gad.Rect.Y) + int(gad.Rect.H)/2 - int(g.fnt.Height)/2
	c.UIText(g.fnt, gad.Text, x, y, 250)
}

// pickerRows is how many map rows the stand-in picker shows at once.
const pickerRows = 14

// drawPicker renders the Nanolathe stand-in skirmish map picker (divergence:
// retail's battle-setup .gui flow is not implemented yet).
func (g *gameShell) drawPicker(c *client.Client, a *menuAssets) {
	c.UIFrameRect(96, 60, 448, 300, 250)
	c.UIText(g.fnt, "SKIRMISH — choose map (up/down or click, then START)", 104, 66, 250)
	if len(g.maps) == 0 {
		return
	}
	g.pickerHot = -1
	start := g.mapIdx - pickerRows/2
	if start < 0 {
		start = 0
	}
	if end := start + pickerRows; end > len(g.maps) {
		start = len(g.maps) - pickerRows
		if start < 0 {
			start = 0
		}
	}
	for row := 0; row < pickerRows; row++ {
		idx := start + row
		if idx >= len(g.maps) {
			break
		}
		y := int32(84 + row*14)
		hot := g.clickEdge && g.clickY >= y && g.clickY < y+14 &&
			g.clickX >= 104 && g.clickX < 536
		if idx == g.mapIdx {
			c.UIFillRect(100, int(y), 440, 13, 30)
		} else if hot {
			g.pickerHot = idx
			c.UIFillRect(100, int(y), 440, 13, 12)
		}
		c.UIText(g.fnt, g.maps[idx], 104, int(y), 250)
		if hot {
			g.mapIdx = idx
			g.clickEdge = false
		}
	}
	// START / BACK reuse the retail 96×20 button art.
	startRect := gui.Rect{X: 148, Y: 380, W: 96, H: 20}
	backRect := gui.Rect{X: 396, Y: 380, W: 96, H: 20}
	g.drawPickerButton(c, a, "START", startRect, g.clickEdge && inRect(g.clickX, g.clickY, startRect))
	g.drawPickerButton(c, a, "BACK", backRect, g.clickEdge && inRect(g.clickX, g.clickY, backRect))
	if g.clickEdge {
		if inRect(g.clickX, g.clickY, startRect) {
			g.clickEdge = false
			name := g.maps[g.mapIdx]
			fmt.Fprintf(os.Stderr, "nanolathe: starting skirmish on %q\n", name)
			if err := g.startBattle(name); err != nil {
				fmt.Fprintf(os.Stderr, "nanolathe: skirmish start: %v\n", err)
			}
			return
		}
		if inRect(g.clickX, g.clickY, backRect) {
			g.clickEdge = false
			g.mode = modeMenuMain
		}
	}
}

func (g *gameShell) drawPickerButton(c *client.Client, a *menuAssets, label string, r gui.Rect, pressed bool) {
	if a != nil && a.commong != nil {
		if e, ok := a.commong.Find("BUTTONS0"); ok {
			for base := 0; base+3 < len(e.Frames); base += 4 {
				f0 := e.Frames[base].Frame
				if f0 == nil {
					continue
				}
				if int(f0.Width) == int(r.W) && int(f0.Height) == int(r.H) {
					idx := base
					if pressed {
						idx = base + 1
					}
					c.UIBlit(e.Frames[idx].Frame, int(r.X), int(r.Y))
					break
				}
			}
		}
	}
	w := fntTextWidth(g.fnt, label)
	c.UIText(g.fnt, label, int(r.X)+int(r.W)/2-w/2, int(r.Y)+int(r.H)/2-int(g.fnt.Height)/2, 250)
}

func inRect(x, y int32, r gui.Rect) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// dispatchMenuClick hit-tests the retail main menu gadgets and runs the
// Nanolathe bindings (SINGLE → stand-in skirmish picker; EXIT → quit;
// MULTI/INTRO/Credits are present but inert until their retail flows land).
func (g *gameShell) dispatchMenuClick() {
	if g.assets == nil || g.assets.gui == nil {
		return
	}
	for i, gad := range g.assets.gui.Gadgets {
		if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 {
			continue
		}
		r := gad.Rect
		if g.clickX >= int32(r.X) && g.clickX < int32(r.X)+int32(r.W) &&
			g.clickY >= int32(r.Y) && g.clickY < int32(r.Y)+int32(r.H) {
			switch gad.Name {
			case "SINGLE":
				g.mode = modeMenuSkirmish
			case "EXIT":
				os.Exit(0)
			default:
				fmt.Fprintf(os.Stderr, "nanolathe: menu %q not wired yet\n", gad.Name)
			}
			return
		}
	}
}
