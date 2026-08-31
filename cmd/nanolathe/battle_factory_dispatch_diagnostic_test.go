package main

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestFactoryClickRetainsDispatchFailureDiagnostic(t *testing.T) {
	factory := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armlab"},
		UnitName:         "armlab",
		Builder:          true,
	}
	product := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "broken"},
		UnitName:         "broken",
		BMCode:           true,
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
	// A builder command page is only ever published for a single selected
	// builder; the command windows are closed to the root while the selected-unit
	// count is zero [07 §6], so the fixture carries the selection that the
	// authoritative publisher would have committed alongside the page.
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
	want := errors.New("queue boundary rejected product")
	battleHUD.factoryDispatch = func(_ *battleSession, _ string, _ int) error { return want }
	b := &battleSession{sess: sess, cat: cat, hud: battleHUD, battleUI: ui.NewProductionBattleState()}
	if !battleHUD.consumeClick(b, 10, 10) {
		t.Fatal("factory product click was not consumed")
	}
	if !errors.Is(battleHUD.LastDispatchError(), want) {
		t.Fatalf("HUD dispatch diagnostic=%v, want %v", battleHUD.LastDispatchError(), want)
	}
}
