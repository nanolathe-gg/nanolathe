package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

// resetUnitInfoState clears the process-wide screen singleton so one test
// cannot leak an open screen into the next.
func resetUnitInfoState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { unitInfoUI = nil })
	unitInfoUI = nil
}

func unitInfoTestCatalog() *content.Catalog {
	tank := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armstump"},
		UnitName:         "armstump",
		Name:             "Stumpy",
		BuildCostEnergy:  1000,
		BuildCostMetal:   200,
		BuildTime:        3000,
		BMCode:           1,
		MaxVelocity:      1 << 16, // 1.0 world units per tick
		Acceleration:     1 << 14, // 0.25
		TurnRate:         600,
	}
	mill := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armmex"},
		UnitName:         "armmex",
		Name:             "Metal Extractor",
		BuildCostEnergy:  10,
		BuildCostMetal:   40,
		BuildTime:        900,
	}
	return &content.Catalog{Units: map[string]*content.UnitDef{
		tank.CanonicalKey: tank,
		mill.CanonicalKey: mill,
	}}
}

// The value column of [07 R-HUD-03 §8]: the two header rows hold "\n", the
// three costs are the authored integers, and a building's three statistic
// rows are N/A while a mobile unit's are the scaled figures.
func TestUnitInfoValuesFollowTheAuthoredRows(t *testing.T) {
	cat := unitInfoTestCatalog()
	tank, _ := cat.Unit("armstump")
	got := unitInfoValues(tank)
	want := [8]string{
		unitInfoHeaderValue,
		"1000",
		"200",
		"3000",
		unitInfoHeaderValue,
		// 1.0 * 30 * 0.4
		"12.0 m/s",
		// 0.25 * 30 * 0.4
		"3.00 m/s/s",
		// 600 * 30 * (360/65536)
		"99 deg/s",
	}
	if got != want {
		t.Fatalf("mobile values = %q, want %q", got, want)
	}

	mill, _ := cat.Unit("armmex")
	got = unitInfoValues(mill)
	if got[1] != "10" || got[2] != "40" || got[3] != "900" {
		t.Fatalf("building cost rows = %q", got)
	}
	for _, i := range []int{5, 6, 7} {
		if got[i] != unitInfoNotApplicable {
			t.Fatalf("building statistic row %d = %q, want %q", i, got[i], unitInfoNotApplicable)
		}
	}
	if got[0] != unitInfoHeaderValue || got[4] != unitInfoHeaderValue {
		t.Fatalf("header rows = %q/%q, want the newline string", got[0], got[4])
	}
}

// The subject is the hovered build button's product when a gadget is hovered,
// otherwise the hovered world unit [07 R-HUD-03 §8].
func TestUnitInfoSubjectPrefersTheHoveredBuildButtonsProduct(t *testing.T) {
	resetUnitInfoState(t)
	cat := unitInfoTestCatalog()
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: "armstump"})
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	h := &retailBattleHUD{cat: cat, fs: vfs.New()}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: cat, hud: h,
		footerHoverUnit: 1}

	// No gadget hovered: the hovered world unit is the subject.
	if def, ok := b.unitInfoSubject(); !ok || def == nil || def.UnitName != "armstump" {
		t.Fatalf("hovered world unit subject = %v/%v", def, ok)
	}

	// A hovered build button's product wins outright, exactly as the footer's
	// first source does.
	h.hoveredGadget, h.hoveredGadgetName, h.hoveredGadgetOK = 4, "ARMMEX", true
	if def, ok := b.unitInfoSubject(); !ok || def == nil || def.UnitName != "armmex" {
		t.Fatalf("hovered gadget subject = %v/%v", def, ok)
	}

	// A gadget name that does not resolve opens nothing rather than falling
	// back to the world unit.
	h.hoveredGadgetName = "NOTAUNIT"
	if def, ok := b.unitInfoSubject(); ok || def != nil {
		t.Fatalf("unresolved gadget name produced subject %v", def)
	}

	// With neither source the screen opens nothing.
	h.hoveredGadget, h.hoveredGadgetName, h.hoveredGadgetOK = hud.NoGadget, "", false
	b.footerHoverUnit = 0
	if _, ok := b.unitInfoSubject(); ok {
		t.Fatal("a subjectless F1 resolved a definition")
	}
}

// F1 opens nothing while the options window is open [07 R-HUD-03 §8].
func TestUnitInfoRefusesToOpenUnderTheOptionsWindow(t *testing.T) {
	resetUnitInfoState(t)
	cat := unitInfoTestCatalog()
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: "armstump"})
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	b := &battleSession{sess: &session.Session{Snapshot: buf, LocalOwner: 0}, cat: cat,
		hud: &retailBattleHUD{cat: cat, fs: vfs.New()}, fs: vfs.New(), footerHoverUnit: 1}
	b.battleState().OpenOptions()
	if b.battleState().Modal() == ui.BattleModalClosed {
		t.Fatal("the options window did not open")
	}
	b.openUnitInfo()
	if unitInfoOpen() {
		t.Fatal("F1 opened the unit information screen under the options window")
	}
}

// A release on `DONE` closes the screen, and every other release inside the
// window is consumed by it [07 §3][07 R-WGT-01 §1].
func TestUnitInfoDoneClosesAndTheWindowOwnsItsClicks(t *testing.T) {
	resetUnitInfoState(t)
	window := &gui.Window{
		Rect:    gui.Rect{X: 200, Y: 100, W: 100, H: 100},
		OriginX: 200, OriginY: 100,
		Gadgets: []gui.Gadget{
			{Name: "HEADER", Kind: gui.KindPanel, Active: 1, Rect: gui.Rect{X: 200, Y: 100, W: 100, H: 100}},
			{Name: "DONE", Kind: gui.KindButton, Active: 1, Rect: gui.Rect{X: 10, Y: 70, W: 40, H: 20}},
		},
	}
	unitInfoUI = &unitInfoScreen{window: window, def: &content.UnitDef{}}
	h := &retailBattleHUD{}

	if !unitInfoCovers(250, 150) || unitInfoCovers(150, 150) {
		t.Fatal("the open window's cover test does not follow its rectangle")
	}
	// A release inside the window but off DONE is consumed and leaves it open.
	if !h.unitInfoConsumeClick(250, 120) || !unitInfoOpen() {
		t.Fatal("a release inside the window was not consumed, or closed it")
	}
	// A release outside the window is not this window's.
	if h.unitInfoConsumeClick(10, 10) {
		t.Fatal("a release outside the window was consumed")
	}
	// DONE closes.
	if !h.unitInfoConsumeClick(215, 175) || unitInfoOpen() {
		t.Fatal("a release on DONE did not close the screen")
	}
	if h.unitInfoConsumeClick(250, 120) {
		t.Fatal("a closed screen still consumed a release")
	}
}

// The screen is built from the authored `UNITINFOx.GUI`; its rectangles are
// the layout and the eight appended label rows sit inside the window
// [07 R-HUD-03 §8].
func TestUnitInfoAuthoredWindowCarriesTheGadgetsTheSectionNames(t *testing.T) {
	resetUnitInfoState(t)
	root := testsupport.RetailRoot(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	defer cs.Close()
	window, err := gui.Load(cs.fs, retailUnitInfoGUI)
	if err != nil {
		t.Fatalf("load %s: %v", retailUnitInfoGUI, err)
	}
	names := map[string]gui.Rect{}
	for i, gad := range window.Gadgets {
		names[gad.Name] = window.PlacedRect(i)
	}
	for _, want := range []string{"HEADER", "DONE", "HOTR", "NAME"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("%s carries no %s gadget", retailUnitInfoGUI, want)
		}
	}
	// The runtime appends its eight labels at window-local positions; every
	// one of them, and the value column, must land inside the authored window.
	for _, label := range unitInfoLabels {
		if int32(label.X) >= window.Rect.W || int32(label.Y) >= window.Rect.H {
			t.Fatalf("appended label %q at (%d,%d) falls outside the %dx%d window",
				label.Text, label.X, label.Y, window.Rect.W, window.Rect.H)
		}
	}
	if int32(unitInfoValueColumn) >= window.Rect.W {
		t.Fatalf("value column x=%d falls outside the %d-wide window", unitInfoValueColumn, window.Rect.W)
	}
	// The picture surface is the 96x96 HOTR rectangle, and the appended label
	// column starts to its right.
	hotr := names["HOTR"]
	if int32(unitInfoLabels[0].X) < hotr.X-window.Rect.X+hotr.W {
		t.Fatalf("the Cost label at x=%d overlaps the HOTR surface ending at %d",
			unitInfoLabels[0].X, hotr.X-window.Rect.X+hotr.W)
	}
	// `DONE` is the authored escape, enter and focus default, which is why it
	// is the screen's only close [07 §3].
	if window.Header.EscDefault != "DONE" || window.Header.CrDefault != "DONE" {
		t.Fatalf("authored defaults = esc %q cr %q, want DONE/DONE",
			window.Header.EscDefault, window.Header.CrDefault)
	}
}

// The single-float definition value is truncated for the %d rows
// [02 R-KEYS-01 §5][07 R-HUD-03 §8][01 R-DET-01 §1].
func TestUnitInfoCostsUseStoredSingles(t *testing.T) {
	source := int32(16777217)
	def := &content.UnitDef{BuildCostEnergy: float32(source), BuildCostMetal: 2147483648}
	got := unitInfoValues(def)
	if got[1] != "16777216" || got[2] != "-2147483648" {
		t.Fatalf("cost rows = %q/%q, want stored value and truncation low word", got[1], got[2])
	}
}
