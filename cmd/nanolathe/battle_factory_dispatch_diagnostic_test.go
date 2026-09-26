package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// factoryClickFixture stands up the smallest committed frame a factory product
// click can be served from: one locally-owned builder on its first build page,
// the selection the authoritative publisher would have committed beside that
// page [07 §6], and an authored window whose second gadget is the product
// button.
func factoryClickFixture(t *testing.T) (*retailBattleHUD, *battleSession, string) {
	t.Helper()
	factory := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armlab"},
		UnitName:         "armlab",
		Builder:          true,
	}
	product := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "peewee"},
		UnitName:         "peewee",
		BMCode:           1,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		factory.CanonicalKey: factory,
		product.CanonicalKey: product,
	}}
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	// Page 1 is the factory's first build page: page 0 is the orders state and
	// carries no products, so the page-shown bit is what puts a product rail on
	// screen at all [07 R-HUD-03 §6].
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: factory.UnitName, Flags: hud.EncodePageBits(0, 1)})
	w.Selection = frame.SelectionView{Handles: append(w.Selection.Handles, 1), Primary: 1, Count: 1}
	w.CommandPage = frame.CommandPageView{Builder: 1, Page: 1, PageCount: 2, ProductKeys: []string{product.UnitName}}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Snapshot: buf, LocalOwner: 0}
	battleHUD := &retailBattleHUD{
		cat: cat,
		fs:  vfs.New(),
		windows: map[string]*gui.Window{
			"armlab1": {
				Gadgets: []gui.Gadget{{}, {Kind: gui.KindButton, Active: 1, Name: product.UnitName, Rect: gui.Rect{W: 20, H: 20}}},
			},
		},
	}
	b := &battleSession{sess: sess, cat: cat, hud: battleHUD, battleUI: ui.NewProductionBattleState()}
	return battleHUD, b, product.UnitName
}

// TestFactoryProductClickDispatchesAndSynthesizesNoStatusText locks the click
// half of [01 §4.4][07 §3]: a hit on a factory product button is consumed, it
// reaches the command boundary through the production dispatcher, and the HUD
// invents no retail status text of its own — LastDispatchError is the only
// surface, and it stays empty when the enqueue succeeds.
//
// The selected actor and authored product page admit counted production
// independently of the actor's Builder flag [07 R-P0-11 §1]. The diagnostic
// refusal is checked separately below, at the dispatch boundary.
func TestFactoryProductClickDispatchesAndSynthesizesNoStatusText(t *testing.T) {
	for _, builder := range []bool{true, false} {
		t.Run(map[bool]string{true: "builder", false: "non-builder"}[builder], func(t *testing.T) {
			battleHUD, b, _ := factoryClickFixture(t)
			b.cat.Units["armlab"].Builder = builder
			if !hudConsumeClick(battleHUD, b, 10, 10) {
				t.Fatal("factory product click was not consumed")
			}
			if err := battleHUD.LastDispatchError(); err != nil {
				t.Fatalf("HUD retained a dispatch diagnostic after a successful enqueue: %v", err)
			}
			pending := b.sess.PendingHumanCommands()
			if len(pending) != 1 || pending[0].Kind != session.HumanFactoryBuild {
				t.Fatalf("click enqueued %#v, want one HumanFactoryBuild command", pending)
			}
			if pending[0].FactoryBuild.Builder != 1 {
				t.Fatalf("enqueued command names builder %v, want the committed page's builder", pending[0].FactoryBuild.Builder)
			}
		})
	}
}

// TestFactoryBuildDispatchErrorIsNotSwallowed locks the other half: the HUD's
// one call into the command boundary returns the dispatcher's own diagnostic
// unaltered rather than absorbing it [01 §4.4]. "No committed frame" is the
// dispatcher's own refusal and needs no injected failure to raise.
func TestFactoryBuildDispatchErrorIsNotSwallowed(t *testing.T) {
	battleHUD, b, product := factoryClickFixture(t)
	// A battle whose session has no frame buffer has no committed frame, which
	// is the first thing DispatchFactoryBuildDelta refuses on.
	starved := &battleSession{sess: &session.Session{LocalOwner: 0}, cat: b.cat, hud: battleHUD}
	err := battleHUD.dispatchFactoryBuild(starved, product, 1)
	if err == nil {
		t.Fatal("dispatchFactoryBuild returned nil for a battle with no committed frame")
	}
	if !strings.Contains(err.Error(), "no committed frame") {
		t.Fatalf("dispatchFactoryBuild returned %v, want the dispatcher's own refusal verbatim", err)
	}
}
