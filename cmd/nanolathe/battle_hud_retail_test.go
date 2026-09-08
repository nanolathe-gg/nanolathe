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
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
)

func TestRetailCommanderPageDrawsAndArmsAuthoredProduct(t *testing.T) {
	root := testsupport.RetailRoot(t)
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
		u.Flags &^= hud.SelectionFlag
		if commanderName == "" && u.Def != nil && u.Def.Builder && u.Def.CanMove {
			u.Flags |= hud.SelectionFlag
			commanderName = u.Def.UnitName
		}
	}
	if commanderName == "" {
		t.Fatal("local mobile builder not found")
	}
	var curReady bool
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
		if cur := sess.Snapshot.Current(); cur != nil {
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
	centerBattleStartCamera(sess, cam)
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	if b.hud.radar == nil {
		t.Fatal("production HUD did not install minimap service")
	}
	layout, dst, ok := b.minimapLayout()
	if !ok {
		t.Fatal("production HUD did not publish minimap layout")
	}
	if got := b.hud.radar.Picture(); got == nil || got.W != int(layout.W) || got.H != int(layout.H) {
		t.Fatalf("production radar picture = %+v, want layout %dx%d", got, layout.W, layout.H)
	}
	// The first completed frame exercises the same load → published frame →
	// rebuild path used by the draw loop. Radar callbacks and contacts are
	// consumed from the committed payload, never rebound to visibility state
	// or rebuilt from frame units [03 §3.4][03 §3.9].
	cur := sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("production HUD has no committed frame")
	}
	if len(cur.Radar.Contacts) == 0 {
		t.Fatal("production frame did not publish radar contacts")
	}
	if got := b.hud.rebuildRadar(b, cur, layout); got == nil {
		t.Fatal("production radar rebuild returned nil")
	}
	if !dst.Contains(dst.X1, dst.Y1) || !b.isOverMinimap(dst.X1, dst.Y1) {
		t.Fatalf("production minimap input rectangle is not the drawn destination: %+v", dst)
	}
	// Lens input uses that exact destination/layout pair. Check the center
	// maps through the canonical camera adapter and remains bounded.
	mx := dst.X1 + (dst.X2-dst.X1)/2
	my := dst.Y1 + (dst.Y2-dst.Y1)/2
	intent, ok := client.MinimapCameraIntent(layout, dst, sess.World.PlayRight, sess.World.PlayBottom, mx, my)
	if !ok {
		t.Fatal("production minimap center input was not consumed")
	}
	// The clicked map point becomes the view centre [07 R-CAM-01 §11].
	b.cam.JumpToBattleViewCenter(intent.X, intent.Z)
	// The same destination and layout drive the viewport rectangle the player
	// sees, so a click and the marker cannot disagree [03 R-MM-01 §1].
	if _, ok := hud.MinimapViewportRect(b.cam, layout, sess.World.PlayRight, sess.World.PlayBottom, dst); !ok {
		t.Fatal("production minimap published no viewport rectangle")
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
	cur = sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("selected commander snapshot disappeared")
	}
	// The command window is the page-shown bit's, not the selection's — and a
	// builder that has never been paged is already on its first build page:
	// unit creation seeds page field 1 with the paged bit set for every
	// definition whose page-count byte is at least 2, and no click ever writes
	// the field [07 R-HUD-04 §4 "First build page"]. So a freshly selected
	// commander opens "%s%d.GUI" composed from its own internal name, and the
	// side's "%sGEN.GUI" orders window is what page 0 — the paged bit clear —
	// opens, one ORDERS click away [07 R-HUD-03 §6].
	//
	// This assertion wanted the orders window until 2026-09-04. That was the
	// behavior of the three days before the creation seed gained a producer
	// (RWU-19-42), not the contract: [07 R-HUD-03 §6] "The state a builder is
	// first selected in" establishes the first build page from manual retail
	// observation, and [07 R-HUD-04 §4] names unit creation as its writer.
	wantWindow := strings.ToLower(commanderName) + "1.gui"
	ordersWindow := strings.ToLower(b.hud.side.NamePrefix) + "gen.gui"
	w, _, err := b.hud.windowForRequired(b, cur)
	if err != nil {
		t.Fatalf("command window: %v", err)
	}
	if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), wantWindow) {
		if w == nil {
			t.Fatalf("freshly selected commander window is nil; want suffix %q", wantWindow)
		}
		t.Fatalf("freshly selected commander window = %q; want suffix %q", w.Name, wantWindow)
	}
	if cur.CommandPage.Page != 1 || !commandPageIsPaged(cur) {
		t.Fatalf("freshly selected commander is on page %d (paged=%v), want build page 1 [07 R-HUD-04 §4]", cur.CommandPage.Page, commandPageIsPaged(cur))
	}
	// BUILD sets the page-shown bit and ORDERS clears it: the two halves of the
	// pair select the state each one stages from [07 R-HUD-03 §6]. Both plates
	// are authored into both windows, so the round trip is a click each way.
	step := int32(31)
	clickCommandButton := func(suffix string) {
		t.Helper()
		window, _, err := b.hud.windowForRequired(b, sess.Snapshot.Current())
		if err != nil {
			t.Fatalf("command window: %v", err)
		}
		if window == nil {
			t.Fatalf("no command window to click %s in", suffix)
		}
		for i, gad := range window.Gadgets {
			if i == 0 || gad.Kind != gui.KindButton || gad.Active == 0 {
				continue
			}
			if !strings.HasSuffix(strings.ToUpper(gad.Name), suffix) {
				continue
			}
			r := window.PlacedRect(i)
			if !b.hud.consumeClick(b, r.X+r.W/2, r.Y+r.H/2) {
				t.Fatalf("%s click was not consumed", suffix)
			}
			for end := step + 4; step <= end; step++ {
				sess.Step(step)
			}
			return
		}
		t.Fatalf("window %q authors no %s gadget", window.Name, suffix)
	}
	// ORDERS goes to page 0 and opens the side's general window; BUILD brings
	// the same page back, because page 0 leaves the page field alone and so the
	// builder remembers where it was [07 R-HUD-03 §6][07 R-HUD-04 §4].
	clickCommandButton("ORDERS")
	cur = sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("orders-state commander snapshot disappeared")
	}
	if cur.CommandPage.Page != 0 || commandPageIsPaged(cur) || len(cur.CommandPage.ProductKeys) != 0 {
		t.Fatalf("ORDERS left page %d (paged=%v) with products %v, want the orders page", cur.CommandPage.Page, commandPageIsPaged(cur), cur.CommandPage.ProductKeys)
	}
	w, _, err = b.hud.windowForRequired(b, cur)
	if err != nil {
		t.Fatalf("command window: %v", err)
	}
	if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), ordersWindow) {
		if w == nil {
			t.Fatalf("commander orders window is nil; want suffix %q", ordersWindow)
		}
		t.Fatalf("commander orders window = %q; want suffix %q", w.Name, ordersWindow)
	}
	clickCommandButton("BUILD")
	cur = sess.Snapshot.Current()
	if cur.CommandPage.Page != 1 || !commandPageIsPaged(cur) {
		t.Fatalf("BUILD returned to page %d (paged=%v), want the remembered page 1", cur.CommandPage.Page, commandPageIsPaged(cur))
	}
	w, _, err = b.hud.windowForRequired(b, cur)
	if err != nil {
		t.Fatalf("command window: %v", err)
	}
	if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), wantWindow) {
		t.Fatalf("commander window after the round trip = %v; want suffix %q", w, wantWindow)
	}
	menu := cat.BuildMenus[content.CanonicalKey(commanderName)]
	// The published page-count byte counts the orders page too, so the authored
	// <name>N.GUI windows on disk are one fewer [07 R-HUD-03 §6]. The compiled
	// byte is the probe of those windows [02 R-CAT-01 §5 step 5]; the HUD's own
	// probe of the same files must agree with it, which is what pins the
	// compiled value to the install rather than to itself. The reference
	// install settles the convention: armcom1..4.GUI are the commander's four
	// authored pages.
	commanderDef, ok := cat.Unit(commanderName)
	if !ok || commanderDef == nil {
		t.Fatalf("commander definition %q missing from catalog", commanderName)
	}
	wantPages := authoredBuildPageCount(b.hud, commanderDef)
	if wantPages < 1 {
		t.Fatalf("%s authored no build page window", commanderName)
	}
	if got := hud.BuilderPageCount(commanderDef); got != wantPages+1 {
		t.Fatalf("%s compiled page count = %d; want %d authored windows plus the orders page", commanderName, got, wantPages)
	}
	if got := int(cur.CommandPage.PageCount); got != wantPages+1 {
		t.Fatalf("published page count = %d; want %d authored pages plus the orders page", got, wantPages)
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
	if b.battleState().Input.BuildDef != content.CanonicalKey(clicked) || b.battleState().Input.Latch != input.LatchMobileBuild {
		t.Fatalf("product %q armed buildDef=%q latch=%d; want %q MOBILEBUILD", clicked, b.battleState().Input.BuildDef, b.battleState().Input.Latch, content.CanonicalKey(clicked))
	}

	if shot := os.Getenv("NANOLATHE_HUD_SHOT"); shot != "" {
		cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetTerrain(sess.World)
		cl.SetCamera(cam)
		cl.SetPalette(pal)
		cl.SetFNT(b.hud.console)
		cl.SetModelFS(cs.fs)
		cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
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

// TestRetailNoSelectionClosesCommandWindows was TestRetailNoSelectionUsesSideGeneralWindow,
// which asserted that an empty selection opens <prefix>GEN.GUI. That is the
// playtest defect, not the contract: [07 §6] "Command-window switch is closed"
// establishes that when the selected-unit count becomes zero the switch closes
// the command windows down to the root <prefix>MAIN2.GUI and opens nothing.
// The general page belongs to a multiple selection or a single non-builder
// selection.
func TestRetailNoSelectionClosesCommandWindows(t *testing.T) {
	root := testsupport.RetailRoot(t)
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
			u.Flags &^= hud.SelectionFlag
		}
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
		if cur := sess.Snapshot.Current(); cur != nil {
			break
		}
	}

	const winW, winH = 640, 480
	cam := &camera.Camera{
		ViewW: winW, ViewH: winH,
		MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16),
	}
	centerBattleStartCamera(sess, cam)
	b := &battleSession{sess: sess, cat: cat, cam: cam}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	cur := sess.Snapshot.Current()
	if cur == nil {
		t.Fatal("no-selection snapshot not published")
	}
	if window, _, _ := b.hud.windowForRequired(b, cur); window != nil {
		t.Fatalf("no-selection window = %q; the command windows are closed to the root [07 §6]", window.Name)
	}

	if shot := os.Getenv("NANOLATHE_HUD_EMPTY_SHOT"); shot != "" {
		writeRetailHUDShot(t, b, cs, cam, pal, winW, winH, shot)
	}

	if shot := os.Getenv("NANOLATHE_HUD_MENU_SHOT"); shot != "" {
		b.openBattleMenu()
		switch os.Getenv("NANOLATHE_HUD_MENU_STATE") {
		case "exit":
			b.battleState().ShowExit()
		case "confirm":
			b.battleState().ShowConfirmation(false)
		case "confirm-main":
			b.battleState().ShowConfirmation(true)
		}
		cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: winW, Height: winH})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetTerrain(sess.World)
		cl.SetCamera(cam)
		cl.SetPalette(pal)
		cl.SetFNT(b.hud.console)
		cl.SetModelFS(cs.fs)
		cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
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

func TestRetailEnergyProductionAnchorFits640Viewport(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
		if sess.Snapshot.Current() != nil {
			break
		}
	}
	pal := retailPaletteForTest(t, cs)
	h, err := loadRetailBattleHUD(cs.fs, sess, cat, pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	anchor, ok := h.anchors.ByIndex(hud.AnchorEnergyProduced)
	if !ok {
		t.Fatal("retail ENERGYPRODUCED anchor is missing")
	}
	text := hud.FormatEnergyProduced(99999) // widest normal-range energy form [07 §6]
	textWidth := client.MeasureText(h.console, text)
	if anchor.X1 < 0 || anchor.Y1 < 0 || anchor.X1+int32(textWidth) > 640 || anchor.Y1 >= 480 {
		t.Fatalf("ENERGYPRODUCED text %q at (%d,%d), width %d, escapes 640x480 viewport", text, anchor.X1, anchor.Y1, textWidth)
	}
}

// writeRetailHUDShot composes one headless frame through the production
// client and writes it as a PNG. It is the visual-evidence path for the HUD
// units; the composition is exactly the draw loop's [I6].
func writeRetailHUDShot(t *testing.T, b *battleSession, cs *contentSet, cam *camera.Camera, pal *palette.Tables, winW, winH int, path string) {
	t.Helper()
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: winW, Height: winH})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.fs)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	file, err := os.Create(path)
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
