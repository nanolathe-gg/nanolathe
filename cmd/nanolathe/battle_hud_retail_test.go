//go:build retail

package main

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func TestRetailCommanderPageDrawsAndArmsAuthoredProduct(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("retail assets unavailable: %v", err)
		}
		root = home + "/TotalAnnihilation"
	}
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}

	var commanderName string
	for _, u := range sess.Units.Iter() {
		if u == nil || u.Owner != sess.LocalOwner {
			continue
		}
		u.Flags &^= client.SelectionFlag
		if commanderName == "" && u.Def != nil && u.Def.Builder && u.Def.CanMove {
			u.Flags |= client.SelectionFlag
			commanderName = u.Def.UnitName
		}
	}
	if commanderName == "" {
		t.Fatal("local mobile builder not found")
	}
	var curReady bool
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
		if _, cur, ok := sess.Snapshot.Read(); ok && cur != nil {
			curReady = true
			break
		}
	}
	if !curReady {
		t.Fatal("selected commander snapshot not published after 30 host steps")
	}

	const winW, winH = 640, 480
	cam := &camera.Camera{
		ViewW: winW, ViewH: winH,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	centerOnCommander(sess.Units, cam, winW, winH)
	b := &battleSession{sess: sess, cat: cat, cam: cam, latch: input.LatchNormal}
	pal := loadPalette(cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.hud.exitWin.Rect; got.X != 309 || got.Y != 162 || got.W != 150 || got.H != 155 {
		t.Fatalf("retail EXITMENU runtime rect = %+v, want (309,162,150,155)", got)
	}
	if got := b.hud.confirmWin.Rect; got.X != 184 || got.Y != 190 || got.W != 400 || got.H != 100 {
		t.Fatalf("retail YESORNO runtime rect = %+v, want (184,190,400,100)", got)
	}
	choice1 := -1
	for i, gad := range b.hud.confirmWin.Gadgets {
		if strings.EqualFold(gad.Name, "CHOICE1") {
			choice1 = i
			break
		}
	}
	if choice1 < 0 {
		t.Fatal("YESORNO has no CHOICE1 gadget")
	}
	if got := b.hud.modalGadgetRect(b.hud.confirmWin, choice1, nil); got.W != 96 || got.H != 20 {
		t.Fatalf("YESORNO CHOICE1 runtime size = %dx%d, want stock frame 96x20", got.W, got.H)
	}
	_, cur, ok := sess.Snapshot.Read()
	if !ok || cur == nil {
		t.Fatal("selected commander snapshot disappeared")
	}
	w, _ := b.hud.windowFor(b, cur)
	wantWindow := strings.ToLower(commanderName) + "1.gui"
	if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), wantWindow) {
		if w == nil {
			t.Fatalf("commander window is nil; want %s", wantWindow)
		}
		t.Fatalf("commander window = %q; want suffix %q", w.Name, wantWindow)
	}
	menu := cat.BuildMenus[content.CanonicalKey(commanderName)]
	wantPages := hud.PageCountFromButtons(len(menu.Buttons), hud.RetailBuildButtonsPerPage)
	if got := b.hud.buildPageCount(b.selectedBuilder().Def); got != wantPages {
		t.Fatalf("%s authored page count = %d; want %d from CANBUILD", commanderName, got, wantPages)
	}

	clicked := ""
	var clickX, clickY int32
	var buttonNames []string
	for i, gad := range w.Gadgets {
		if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 || gad.GrayedOut != 0 {
			continue
		}
		candidates := append([]string{gad.Name, gad.Text}, gad.Labels...)
		buttonNames = append(buttonNames, candidates...)
		for _, candidate := range candidates {
			if !hud.ValidateBuildProduct(cat, content.CanonicalKey(commanderName), candidate) {
				continue
			}
			r := w.PlacedRect(i)
			clickX, clickY = r.X+r.W/2, r.Y+r.H/2
			clicked = candidate
			break
		}
		if clicked != "" {
			break
		}
	}
	if clicked == "" {
		t.Fatalf("commander page has no authored product button: builder=%s menu=%v gadgets=%v", commanderName, menu.Buttons, buttonNames)
	}
	if !b.hud.sameButton(b, clickX, clickY, clickX, clickY) || !b.hud.consumeClick(b, clickX, clickY) {
		t.Fatalf("authored product %q did not activate on release-inside", clicked)
	}
	if b.buildDef != content.CanonicalKey(clicked) || b.latch != input.LatchMobileBuild {
		t.Fatalf("product %q armed buildDef=%q latch=%d; want %q MOBILEBUILD", clicked, b.buildDef, b.latch, content.CanonicalKey(clicked))
	}

	if shot := os.Getenv("NANOLATHE_HUD_SHOT"); shot != "" {
		cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH, Headless: true})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetTerrain(sess.World)
		cl.SetCamera(cam)
		cl.SetPalette(pal)
		cl.SetFNT(b.hud.console)
		cl.SetModelFS(cs.fs)
		cl.Overlay = func(c *client.Client) { b.hud.draw(c, b) }
		file, err := os.Create(shot)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, cl.ComposeFrame()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRetailNoSelectionUsesSideGeneralWindow(t *testing.T) {
	root := os.Getenv("NANOLATHE_TA_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("retail assets unavailable: %v", err)
		}
		root = home + "/TotalAnnihilation"
	}
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range sess.Units.Iter() {
		if u != nil {
			u.Flags &^= client.SelectionFlag
		}
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
		if _, cur, ok := sess.Snapshot.Read(); ok && cur != nil {
			break
		}
	}

	const winW, winH = 640, 480
	cam := &camera.Camera{
		ViewW: winW, ViewH: winH,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	centerOnCommander(sess.Units, cam, winW, winH)
	b := &battleSession{sess: sess, cat: cat, cam: cam, latch: input.LatchNormal, menuPressed: -1}
	pal := loadPalette(cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal)
	if err != nil {
		t.Fatal(err)
	}
	_, cur, ok := sess.Snapshot.Read()
	if !ok || cur == nil {
		t.Fatal("no-selection snapshot not published")
	}
	window, _ := b.hud.windowFor(b, cur)
	want := strings.ToLower(b.hud.side.NamePrefix) + "gen.gui"
	if window == nil || !strings.HasSuffix(strings.ToLower(window.Name), want) {
		if window == nil {
			t.Fatalf("no-selection window is nil; want %s", want)
		}
		t.Fatalf("no-selection window = %q; want suffix %q", window.Name, want)
	}

	if shot := os.Getenv("NANOLATHE_HUD_MENU_SHOT"); shot != "" {
		b.openBattleMenu()
		switch os.Getenv("NANOLATHE_HUD_MENU_STATE") {
		case "exit":
			b.menu = battleMenuExit
		case "confirm":
			b.menu = battleMenuConfirmExit
		case "confirm-main":
			b.menu = battleMenuConfirmMain
		}
		cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH, Headless: true})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetTerrain(sess.World)
		cl.SetCamera(cam)
		cl.SetPalette(pal)
		cl.SetFNT(b.hud.console)
		cl.SetModelFS(cs.fs)
		cl.Overlay = func(c *client.Client) { b.hud.draw(c, b) }
		file, err := os.Create(shot)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, cl.ComposeFrame()); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
