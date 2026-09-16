//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestRetailSiloPageCarriesTheStockpileToy is the presentation half of the
// Retaliator play-test report: a selected ARM Retaliator must open ARMSILO1.GUI,
// that page must expose the MAKENUKE toy, and clicking it must reach the
// session's BUILDWEAPON alias.
//
// The page was invisible because both the publication boundary and this
// window switch asked the FBI `Builder` word whether the unit had a page.
// ARMSILO authors `Builder=0` and no CANBUILD list, so it failed both, yet it
// ships one page window whose single toy is `ARMMAKENUKE` with `commonattribs`
// bit 0x08 — the stockpile format of the count-label writer [07 R-P0-11 §2].
// Which window opens is the unit's page-shown bit and page field over the
// definition's page-count byte, and nothing else [07 R-HUD-03 §6]
// [02 R-CAT-01 §5 step 5].
func TestRetailSiloPageCarriesTheStockpileToy(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	def, ok := cat.Unit("ARMSILO")
	if !ok || def == nil {
		t.Skip("retail fixture unit ARMSILO is absent")
	}
	if def.Builder {
		t.Skip("ARMSILO now authors the builder word; the gate this test guards is unreachable")
	}
	if got := hud.BuilderPageCount(def); got < 2 {
		t.Fatalf("ARMSILO page-count byte = %d, want the authored ARMSILO1.GUI counted as page 1 [07 R-HUD-03 §6]", got)
	}

	// The committed frame a selected, complete Retaliator produces: unit
	// creation seeds the page-shown bit and page field 1 for any definition
	// whose page count is 2 or more [07 R-HUD-03 §6].
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: def.UnitName, Flags: hud.EncodePageBits(0, 1)})
	w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
	w.CommandPage = frame.CommandPageView{Builder: 1, Page: 1, PageCount: uint16(hud.BuilderPageCount(def))}
	if err := buf.Publish(1); err != nil {
		t.Fatalf("publish frame: %v", err)
	}

	root := testsupport.RetailRoot(t)
	pageFS := vfs.New()
	if err := pageFS.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	defer pageFS.Close()

	var side *content.SideDef
	if len(cat.Sides) > 0 {
		side = cat.Sides[0]
	}
	battleHUD := &retailBattleHUD{cat: cat, fs: pageFS, side: side, owner: 0}
	sess := &session.Session{Snapshot: buf, LocalOwner: 0}
	b := &battleSession{sess: sess, cat: cat, hud: battleHUD, battleUI: ui.NewProductionBattleState()}

	window, _, err := battleHUD.windowForRequired(b, buf.Current())
	if err != nil {
		t.Fatalf("resolve the Retaliator's page: %v", err)
	}
	if window == nil {
		t.Fatal("a selected Retaliator resolved no command window; ARMSILO1.GUI is authored [07 R-HUD-03 §6]")
	}
	toy := -1
	for i, gad := range window.Gadgets {
		if i == 0 {
			continue // gadget 0 is the window record itself
		}
		if StockpileGadget(gad) {
			toy = i
			break
		}
	}
	if toy < 0 {
		t.Fatalf("the Retaliator's page exposes no stockpile toy; ARMSILO1.GUI authors ARMMAKENUKE with commonattribs 0x08 [07 R-P0-11 §2]")
	}

	// A real pointer press on the toy's authored rectangle: the click must be
	// consumed by the stockpile arm and reach the session as one BUILDWEAPON
	// round against the unit the committed page names [06 §11.1].
	r := window.PlacedRect(toy)
	if !hudConsumeClick(battleHUD, b, r.X+r.W/2, r.Y+r.H/2) {
		t.Fatalf("a click on %s was not consumed", window.Gadgets[toy].Name)
	}
	pending := sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanStockpile {
		t.Fatalf("the toy enqueued %#v, want one HumanStockpile command", pending)
	}
	if pending[0].Stockpile.Unit != 1 {
		t.Fatalf("the toy named unit %v, want the committed page's Retaliator", pending[0].Stockpile.Unit)
	}
}
