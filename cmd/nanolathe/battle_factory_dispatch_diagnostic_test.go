package main

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
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
	w.Units = append(w.Units, frame.UnitView{Slot: 1, Owner: 0, DefName: factory.UnitName})
	w.CommandPage = frame.CommandPageView{Builder: 1, PageCount: 1, ProductKeys: []string{product.UnitName}}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	sess := &session.Session{Snapshot: buf, LocalOwner: 0}
	hud := &retailBattleHUD{
		cat: cat,
		fs:  vfs.New(),
		windows: map[string]*gui.Window{
			"armlab1": {
				Gadgets: []gui.Gadget{{}, {Kind: gui.KindButton, Active: 1, Name: product.UnitName, Rect: gui.Rect{W: 20, H: 20}}},
			},
		},
	}
	want := errors.New("queue boundary rejected product")
	hud.factoryDispatch = func(_ *battleSession, _ string, _ int) error { return want }
	b := &battleSession{sess: sess, cat: cat, hud: hud, battleUI: ui.NewProductionBattleState()}
	if !hud.consumeClick(b, 10, 10) {
		t.Fatal("factory product click was not consumed")
	}
	if !errors.Is(hud.LastDispatchError(), want) {
		t.Fatalf("HUD dispatch diagnostic=%v, want %v", hud.LastDispatchError(), want)
	}
}
