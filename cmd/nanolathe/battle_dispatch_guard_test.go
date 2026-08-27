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
	b := &battleSession{}
	if err := b.submitBattleCommand(battleCommand{Kind: battleCommandStop}); err == nil || err.Error() != "battle: production command dispatch is unbound" {
		t.Fatalf("production command without dispatcher error=%v", err)
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
	b := &battleSession{sess: s, cat: cat}
	var got pool.Handle
	b.commandDispatchFn = func(c battleCommand) error { got = c.MobileBuild.Builder; return nil }
	if err := b.DispatchMobileBuild("scout", 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("builder handle=%d, want command-page builder 2", got)
	}
}

func TestProductionDigitRoutingUsesBattleModeAltGate(t *testing.T) {
	bdef := &content.UnitDef{UnitName: "lab", Builder: true}
	bdef.CanonicalKey = "lab"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"lab": bdef}}
	s := &session.Session{LocalOwner: 0, Snapshot: &snapshot.Buffer{}}
	s.Snapshot.Publish(&snapshot.Frame{
		Units:       []snapshot.UnitView{{Slot: 2, Owner: 0, DefName: "lab"}},
		Selection:   snapshot.SelectionView{Handles: []pool.Handle{2}, Primary: 2, Count: 1},
		CommandPage: snapshot.CommandPageView{Builder: 2, PageCount: 3},
	})

	tests := []struct {
		name       string
		mode       byte
		alt        bool
		shift      bool
		wantKind   battleCommandKind
		wantPage   int
		wantGroup  int
		wantQueued bool
	}{
		{name: "normal page", mode: 0, alt: false, wantKind: battleCommandBuildPage, wantPage: 1},
		{name: "normal alt group", mode: 0, alt: true, shift: true, wantKind: battleCommandGroupRecall, wantGroup: 2, wantQueued: true},
		{name: "battle group", mode: 1, alt: false, wantKind: battleCommandGroupRecall, wantGroup: 2},
		{name: "battle alt page", mode: 1, alt: true, wantKind: battleCommandBuildPage, wantPage: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &battleSession{sess: s, cat: cat, battleMode: tc.mode}
			var got battleCommand
			b.commandDispatchFn = func(c battleCommand) error {
				got = c
				return nil
			}
			b.routeDigit(2, tc.alt, tc.shift)
			if got.Kind != tc.wantKind {
				t.Fatalf("command kind=%d, want %d (%+v)", got.Kind, tc.wantKind, got)
			}
			if got.Kind == battleCommandBuildPage {
				if got.BuildPage.Page != tc.wantPage || got.BuildPage.Builder != 2 {
					t.Fatalf("page command=%+v, want builder=2 page=%d", got.BuildPage, tc.wantPage)
				}
				return
			}
			if got.Group.Group != tc.wantGroup || got.Group.Preserve != tc.wantQueued {
				t.Fatalf("group command=%+v, want group=%d preserve=%v", got.Group, tc.wantGroup, tc.wantQueued)
			}
		})
	}
}

func TestProductionDigitGroupPathDoesNotEmitPageForNonBuilder(t *testing.T) {
	nonBuilder := &content.UnitDef{UnitName: "scout"}
	nonBuilder.CanonicalKey = "scout"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"scout": nonBuilder}}
	s := &session.Session{LocalOwner: 0, Snapshot: &snapshot.Buffer{}}
	s.Snapshot.Publish(&snapshot.Frame{
		Units:       []snapshot.UnitView{{Slot: 1, Owner: 0, DefName: "scout"}},
		Selection:   snapshot.SelectionView{Handles: []pool.Handle{1}, Primary: 1, Count: 1},
		CommandPage: snapshot.CommandPageView{},
	})
	b := &battleSession{sess: s, cat: cat, battleMode: 1}
	var got battleCommand
	b.commandDispatchFn = func(c battleCommand) error {
		got = c
		return nil
	}
	b.routeDigit(3, false, false)
	if got.Kind != battleCommandGroupRecall || got.Group.Group != 3 {
		t.Fatalf("non-builder digit command=%+v, want group recall 3", got)
	}
	if got.Kind == battleCommandBuildPage {
		t.Fatal("non-builder/group route emitted build-page command")
	}
}

func TestProductionCtrlDigitAssignsGroupWithoutPageCommand(t *testing.T) {
	b := &battleSession{}
	var got battleCommand
	b.commandDispatchFn = func(c battleCommand) error {
		got = c
		return nil
	}
	if err := b.DispatchGroupAssign(4); err != nil {
		t.Fatal(err)
	}
	if got.Kind != battleCommandGroupAssign || got.Group.Group != 4 {
		t.Fatalf("ctrl-digit assignment command=%+v, want group assignment 4", got)
	}
}
