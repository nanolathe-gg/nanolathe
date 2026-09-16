//go:build retail

package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// battleInfoWindowFixture is a battle with only the parts ARMOPT's read-only
// children read: the authored records, the session words the overlay prints
// and the modal chain.
func battleInfoWindowFixture(t *testing.T, campaign bool) *battleSession {
	t.Helper()
	root := testsupport.RetailRoot(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	h := &retailBattleHUD{fs: cs.fs, info: loadBattleInfoWindows(cs.fs, nil)}
	h.applyDisplaySize(retailScreenW, retailScreenH)
	sess := &session.Session{}
	sess.Skirmish.MapName = "Great Divide"
	if campaign {
		sess.Mission = &mission.Mission{Type: mission.TypeCampaign}
	}
	b := &battleSession{hud: h, sess: sess, battleUI: ui.NewProductionBattleState()}
	b.battleState().SetCampaign(battleSessionKind(b) == 1)
	b.battleState().OpenOptions()
	return b
}

func openBattleInfoChild(t *testing.T, b *battleSession, button string) (*gui.Window, *ui.Panel) {
	t.Helper()
	b.activateBattleMenuButton(button, nil)
	window, panel, _ := b.battleInfoWindow()
	if window == nil || panel == nil {
		t.Fatalf("%s left no open child window", button)
	}
	return window, panel
}

// `MISSION` outside a campaign opens GAMEOPTIONS.GUI and prints the nine
// read-only rows as appended labels [07 R-FE-01 §7][08 R-SKIR-01 §11].
func TestBattleMissionOpensGameOptionsOutsideACampaign(t *testing.T) {
	b := battleInfoWindowFixture(t, false)
	window, panel := openBattleInfoChild(t, b, "MISSION")
	if got := b.battleState().Modal(); got != ui.BattleModalGameOptions {
		t.Fatalf("skirmish MISSION opened modal %d", got)
	}
	if !strings.EqualFold(window.Name, "guis/gameoptions.gui") {
		t.Fatalf("the open child is %q", window.Name)
	}
	rows := battleGameOptionsRows(b)
	if len(window.Gadgets) != b.hud.info.gameOptionsAuthored+2*len(rows) {
		t.Fatalf("the overlay holds %d gadgets for %d rows over %d authored",
			len(window.Gadgets), len(rows), b.hud.info.gameOptionsAuthored)
	}
	first := window.Gadgets[b.hud.info.gameOptionsAuthored]
	if first.Text != "Commander Death:" || first.Rect.X != gameOptionsNameX || first.Rect.Y != gameOptionsFirstY {
		t.Fatalf("the first printed row is %q at (%d,%d)", first.Text, first.Rect.X, first.Rect.Y)
	}
	last := window.Gadgets[len(window.Gadgets)-1]
	if wantY := int32(gameOptionsFirstY + (len(rows)-1)*gameOptionsStepY); last.Rect.Y != wantY {
		t.Fatalf("the last printed row sits at y %d, want %d", last.Rect.Y, wantY)
	}
	if panel.Window != window {
		t.Fatal("the retained widget state does not own the open record")
	}
	// `OK` returns to the surviving options root.
	b.activateBattleMenuButton("OK", nil)
	if got := b.battleState().Modal(); got != ui.BattleModalOptions {
		t.Fatalf("the overlay's OK left modal %d", got)
	}
}

// `MISSION` in a campaign opens BRIEFING.GUI, and its opener clears the
// inert-label attribute bit on `MOREBAR` and `TextRegion` so both fire
// [07 R-FE-01 §7][07 R-FE-01 §4].
func TestBattleMissionOpensBriefingInACampaign(t *testing.T) {
	b := battleInfoWindowFixture(t, true)
	window, _ := openBattleInfoChild(t, b, "MISSION")
	if got := b.battleState().Modal(); got != ui.BattleModalBriefing {
		t.Fatalf("campaign MISSION opened modal %d", got)
	}
	if !strings.EqualFold(window.Name, "guis/briefing.gui") {
		t.Fatalf("the open child is %q", window.Name)
	}
	for _, name := range []string{"MOREBAR", "TextRegion"} {
		index := window.GadgetIndex(name)
		if index < 0 {
			t.Fatalf("BRIEFING.GUI has no %s", name)
		}
		if window.Gadgets[index].Attribs&battleBriefingInertBit != 0 {
			t.Fatalf("%s kept the inert-label attribute bit", name)
		}
	}
	if b.hud.info.briefing == nil {
		t.Fatal("the in-battle briefing installed no pager")
	}
	b.activateBattleMenuButton("OK", nil)
	if got := b.battleState().Modal(); got != ui.BattleModalOptions {
		t.Fatalf("the briefing's OK left modal %d", got)
	}
}

// `HELP` opens HELP.GUI and fills page 0 from `gamedata\help.tdf`; `Page`
// reloads the window at the stage its three-stage button selects
// [07 R-FE-01 §7].
func TestBattleHelpOpensHelpWindowAndPages(t *testing.T) {
	b := battleInfoWindowFixture(t, false)
	window, panel := openBattleInfoChild(t, b, "HELP")
	if got := b.battleState().Modal(); got != ui.BattleModalHelp {
		t.Fatalf("HELP opened modal %d", got)
	}
	if !strings.EqualFold(window.Name, "guis/help.gui") {
		t.Fatalf("the open child is %q", window.Name)
	}
	page0 := append([]battleHelpLine(nil), b.hud.info.helpLines...)
	if len(page0) == 0 {
		t.Fatal("page 0 read no rows from gamedata/help.tdf")
	}
	if len(window.Gadgets) != b.hud.info.helpAuthored+2*len(page0) {
		t.Fatalf("the window holds %d gadgets for %d rows over %d authored",
			len(window.Gadgets), len(page0), b.hud.info.helpAuthored)
	}
	first := window.Gadgets[b.hud.info.helpAuthored]
	if first.Rect.X != helpKeyX || first.Rect.Y != helpFirstY || first.Rect.W != helpKeyW {
		t.Fatalf("the key column sits at (%d,%d) width %d", first.Rect.X, first.Rect.Y, first.Rect.W)
	}
	if desc := window.Gadgets[b.hud.info.helpAuthored+1]; desc.Rect.X != helpDescX || desc.Rect.W != helpDescW {
		t.Fatalf("the description column sits at x %d width %d", desc.Rect.X, desc.Rect.W)
	}

	// The three-stage button advances itself, then the window reloads at the
	// page it now names.
	index := panel.Index("Page")
	if index < 0 {
		t.Fatal("HELP.GUI has no Page button")
	}
	panel.SetStageAt(index, 1)
	b.activateBattleMenuButton("Page", nil)
	if b.hud.info.helpPage != 1 {
		t.Fatalf("Page left the window on page %d", b.hud.info.helpPage)
	}
	page1 := b.hud.info.helpLines
	if len(page1) == 0 {
		t.Fatal("page 1 read no rows")
	}
	if page1[0] == page0[0] {
		t.Fatalf("page 1 repeated page 0's first row %v", page0[0])
	}
	if got := len(b.hud.info.helpWin.Gadgets); got != b.hud.info.helpAuthored+2*len(page1) {
		t.Fatalf("the refill left %d gadgets, want the authored %d plus %d rows",
			got, b.hud.info.helpAuthored, len(page1))
	}
	b.activateBattleMenuButton("OK", nil)
	if got := b.battleState().Modal(); got != ui.BattleModalOptions {
		t.Fatalf("HELP's OK left modal %d", got)
	}
}
