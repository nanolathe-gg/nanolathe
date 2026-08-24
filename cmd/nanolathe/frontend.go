package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// Game modes for the single-window shell: the front-end menu morphs into the
// battle view in-process.
type shellMode uint8

const (
	modeMenuMain shellMode = iota
	modeMenuSkirmish
	modeBattle
)

type menuItem struct {
	Label  string
	X, Y   int32
	W, H   int32
	Action func(*gameShell)
}

// gameShell owns the window, the front-end menu flow, and hands off to the
// battle view when a skirmish starts.
type gameShell struct {
	opts Options
	cs   *contentSet
	cam  *camera.Camera

	mode      shellMode
	maps      []string // sorted catalog map names [02 "Map files"]
	mapIdx    int
	items     []menuItem
	battle    *battleSession
	fnt       *formats.FNT
	clickX    int32
	clickY    int32
	clickEdge bool
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
	fmt.Fprintln(os.Stderr, "nanolathe: shell: compiling catalog")
	cat, err := content.Compile(cs.fs)
	if err != nil {
		return fmt.Errorf("nanolathe: catalog: %w", err)
	}
	maps := listMapNames(cat)
	if len(maps) == 0 {
		return fmt.Errorf("nanolathe: no maps found under the mounted install")
	}
	shell.maps = maps
	shell.mode = modeMenuMain
	shell.buildMenuItems()
	fmt.Fprintf(os.Stderr, "nanolathe: shell: %d maps, opening window\n", len(maps))

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
	pal := loadPalette(cs)
	fnt := loadFNT(cs)
	if pal != nil {
		cl.SetPalette(pal)
	}
	if fnt != nil {
		cl.SetFNT(fnt)
		shell.fnt = fnt
	}
	cl.Overlay = func(c *client.Client) { shell.draw(c) }
	return client.RunGame(cl)
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
	}
	return nil
}

// listMapNames extracts sorted map names from a compiled catalog [I1].
func listMapNames(cat *content.Catalog) []string {
	names := make([]string, 0, len(cat.Maps))
	for name := range cat.Maps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// menuInput polls menu-mode input (click edge + arrows); no simulation ticks.
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

// buildMenuItems lays out clickable items per mode.
func (g *gameShell) buildMenuItems() {
	g.items = g.items[:0]
	x, y := int32(240), int32(200)
	add := func(label string, action func(*gameShell)) {
		g.items = append(g.items, menuItem{Label: label, X: x, Y: y, W: 160, H: 24, Action: action})
		y += 32
	}
	switch g.mode {
	case modeMenuMain:
		add("Single Player", func(s *gameShell) { s.mode = modeMenuSkirmish; s.buildMenuItems() })
		add("Quit", func(s *gameShell) { os.Exit(0) })
	case modeMenuSkirmish:
		add("Start Skirmish", func(s *gameShell) {
			if len(s.maps) == 0 {
				return
			}
			name := s.maps[s.mapIdx]
			fmt.Fprintf(os.Stderr, "nanolathe: starting skirmish on %q\n", name)
			if err := s.startBattle(name); err != nil {
				fmt.Fprintf(os.Stderr, "nanolathe: skirmish start: %v\n", err)
			}
		})
		add("Back", func(s *gameShell) { s.mode = modeMenuMain; s.buildMenuItems() })
	}
}

// draw renders the active mode's chrome and dispatches menu clicks.
func (g *gameShell) draw(c *client.Client) {
	fnt := g.fnt
	if g.mode == modeBattle {
		if g.battle != nil && fnt != nil {
			g.battle.drawOverlay(c, fnt)
		}
		return
	}
	c.UIFillRect(0, 0, 640, 480, 0)
	c.UIText(fnt, "N A N O L A T H E", 232, 120, 250)
	// Rebuild layout when the mode changed since the last frame.
	wantFirst := "Single Player"
	if g.mode == modeMenuSkirmish {
		wantFirst = "Start Skirmish"
	}
	if len(g.items) == 0 || g.items[0].Label != wantFirst {
		g.buildMenuItems()
	}
	if g.mode == modeMenuSkirmish && len(g.maps) > 0 {
		c.UIText(fnt, fmt.Sprintf("Map: < %s >  (up/down)", g.maps[g.mapIdx]), 216, 168, 250)
	}
	for _, it := range g.items {
		hover := g.clickEdge && g.clickX >= it.X && g.clickX < it.X+it.W &&
			g.clickY >= it.Y && g.clickY < it.Y+it.H
		idx := byte(12)
		if hover {
			idx = 30
		}
		c.UIFillRect(int(it.X), int(it.Y), int(it.W), int(it.H), idx)
		c.UIFrameRect(int(it.X), int(it.Y), int(it.W), int(it.H), 250)
		c.UIText(fnt, it.Label, int(it.X)+8, int(it.Y)+8, 250)
		if hover && it.Action != nil {
			it.Action(g)
		}
	}
	g.clickEdge = false
}
