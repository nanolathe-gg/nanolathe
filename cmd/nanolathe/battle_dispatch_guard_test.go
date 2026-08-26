package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

func TestProductionDispatchGuardRequiresBindingAndSnapshot(t *testing.T) {
	b := &battleSession{requireCommandDispatch: true}
	if err := b.submitBattleCommand(battleCommand{Kind: battleCommandStop}); err == nil {
		t.Fatal("production command without dispatcher must fail")
	}
	b.commandDispatchFn = func(battleCommand) error { return nil }
	if err := b.DispatchOrderCommand(battleOrderCommand{Latch: input.LatchMove, Position: orders.ResolvePos{}}); err == nil {
		t.Fatal("production order without current snapshot must fail")
	}
}

func TestProductionBuildUsesSnapshotCommandPageBuilder(t *testing.T) {
	nonBuilder := &content.UnitDef{UnitName: "scout"}
	nonBuilder.CanonicalKey = "scout"
	builder := &content.UnitDef{UnitName: "lab", Builder: true}
	builder.CanonicalKey = "lab"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": nonBuilder, "lab": builder}}
	s := &session.Session{LocalOwner: 0, Snapshot: &snapshot.Buffer{}}
	s.Snapshot.Publish(&snapshot.Frame{Units: []snapshot.UnitView{{Slot: 1, Owner: 0, DefName: "scout"}, {Slot: 2, Owner: 0, DefName: "lab"}}, Selection: snapshot.SelectionView{Handles: []pool.Handle{1, 2}}, CommandPage: snapshot.CommandPageView{Builder: 2}})
	b := &battleSession{sess: s, cat: cat, requireCommandDispatch: true}
	var got pool.Handle
	b.commandDispatchFn = func(c battleCommand) error { got = c.MobileBuild.Builder; return nil }
	if err := b.DispatchMobileBuild("scout", 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("builder handle=%d, want command-page builder 2", got)
	}
}
