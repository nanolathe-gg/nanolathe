package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"testing"
)

func TestInputIdleClickStrictWorldAndLiveClock(t *testing.T) {
	s := ui.BattleInputState{DragPressClock: 100, DragEndWorldX: 31, DragEndWorldZ: -31}
	if !idleDragIsClick(s, 124) || idleDragIsClick(s, 125) {
		t.Fatal("strict deadline boundary")
	}
	s.DragEndWorldX = 32
	if idleDragIsClick(s, 124) {
		t.Fatal("32 whole world units admitted")
	}
	s.DragEndWorldX = 0
	s.DragEndWorldZ = -32
	if idleDragIsClick(s, 124) {
		t.Fatal("negative Z boundary admitted")
	}
	s.DragEndWorldZ = 0
	s.DragPressClock = 4294967
	if !idleDragIsClick(s, 0) {
		t.Fatal("absolute comparison lost multiplication wrap")
	}
	source := &fakeMillisSource{ms: 143165576}
	b := &battleSession{millisSource: source}
	if got := b.inputScaledClock(); got != 4294967 {
		t.Fatalf("prewrap scaled=%d", got)
	}
	source.ms++
	if got := b.inputScaledClock(); got != 0 {
		t.Fatalf("multiply must wrap before divide, got %d", got)
	}
}

func TestInputReleaseUsesStoredWorldEndpointAndCurrentPointer(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	u := placeUnit(b, "armcons", 200<<16, 120<<16)
	replaceSelectionForTest(t, b, u)
	source := &fakeMillisSource{ms: 1000}
	b.millisSource = source
	in := input.NewState()
	in.Mouse.SetPosition(300, 200)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(310, 210)
	b.handleInput(in, nil)
	endX, endZ := b.battleState().Input.DragEndWorldX, b.battleState().Input.DragEndWorldZ
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(500, 350)
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	source.ms = 1100
	b.handleInput(in, nil)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder {
		t.Fatalf("release classified from current pointer: %+v", pending)
	}
	wx, _, wz := b.cursorWorld(500, 350)
	if pending[0].Order.Position.X != wx || pending[0].Order.Position.Z != wz {
		t.Fatal("accepted click did not act at release point")
	}
	if b.battleState().Input.DragEndWorldX != endX || b.battleState().Input.DragEndWorldZ != endZ {
		t.Fatal("release refreshed stored endpoint")
	}
}

func TestInputArmedPressAndCoherentPlacement(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	u := placeUnit(b, "armcons", 200<<16, 120<<16)
	replaceSelectionForTest(t, b, u)
	b.battleState().ArmPlacement("armsolar", 2, 2)
	b.handleHudOrderButton("MOVE")
	if b.battleState().Input.BuildDef != "" || b.battleState().PlacementArmed() {
		t.Fatal("MOVE retained placement")
	}
	in := input.NewState()
	in.Mouse.SetPosition(300, 200)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Order.Code != 2 || b.battleState().Input.DragActive {
		t.Fatalf("armed left press did not dispatch immediately: %+v", pending)
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
	if len(b.sess.PendingHumanCommands()) != 1 {
		t.Fatal("armed release duplicated dispatch")
	}
	b.battleState().ArmPlacement("armsolar", 2, 2)
	b.handleHudOrderButton("STOP")
	if b.battleState().Input.BuildDef != "" {
		t.Fatal("STOP retained product")
	}
}

func TestInputMappedFeatureGateAndAdjacentGadgetEdge(t *testing.T) {
	f := &frame.Frame{Visibility: frame.VisibilityView{Valid: true, W: 2, H: 2, CoverageBytes: true, Visible: []uint8{0, 0, 0, 0}, WordVisible: []uint16{0, 1, 0, 0}}}
	if !snapshotFeatureMappedAt(f, 32<<16, 32<<16, 16<<16, 0) {
		t.Fatal("mapped memory rejected under current fog")
	}
	if !snapshotFeatureMappedAt(f, (65536+32)<<16, (65536+32)<<16, (65536+16)<<16, 0) {
		t.Fatal("projection did not narrow signed whole coordinate words")
	}
	if snapshotFeatureMappedAt(f, 0, 0, 0, 0) || snapshotFeatureMappedAt(f, 32<<16, 32<<16, 16<<16, 1) {
		t.Fatal("unmapped/viewer gate bypass")
	}
	left := gui.Rect{X: 10, Y: 20, W: 10, H: 10}
	right := gui.Rect{X: 20, Y: 20, W: 10, H: 10}
	if guiRectContains(left, 20, 20) || !guiRectContains(right, 20, 20) || guiRectContains(left, 19, 30) {
		t.Fatal("adjacent gadget last-pixel boundary")
	}
}

func TestInputPaletteDisabledProductsAndPageArrows(t *testing.T) {
	f := &frame.Frame{}
	f.CommandPage.Page = 1
	f.CommandPage.PageCount = 1
	for _, name := range []string{"ARMNEXT", "ARMPREV"} {
		v, ok := paletteGadgetVerdict(gui.Gadget{Name: name}, f, true, nil)
		if !ok || !v.hidden {
			t.Fatal("singleton arrow visible")
		}
	}
	gad := gui.Gadget{Name: "missing", CommonAttribs: 4}
	v, _ := paletteGadgetVerdict(gad, f, true, &content.Catalog{})
	if !v.grey {
		t.Fatal("unresolved product not grey")
	}
}

func TestInputInterfaceOptionCrossesCommandBoundary(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	u := placeUnit(b, "armcons", 200<<16, 120<<16)
	replaceSelectionForTest(t, b, u)
	b.interfaceType = orders.InterfaceTypeRightClick
	if err := b.DispatchOrderCommand(session.HumanOrderCommand{Code: 1}); err != nil {
		t.Fatal(err)
	}
	if got := b.sess.PendingHumanCommands()[0].Order.Position.InterfaceType; got != orders.InterfaceTypeRightClick {
		t.Fatalf("option at command boundary=%d", got)
	}
}

func TestInputPagingWrapAndPendingCueIdentity(t *testing.T) {
	b, cl, _ := paletteCallbackFactory(t, []gui.Gadget{{Kind: gui.KindButton, Name: "ARMNEXT", Active: 1, QuickKey: 'n'}}, nil, 0, 3)
	spy := paletteCallbackCues(t, b, cueNextBuildMenu)
	_ = cl
	b.nextBuildPage()
	b.nextBuildPage()
	b.nextBuildPage()
	b.prevBuildPage()
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 4 {
		t.Fatalf("pending pages=%+v", pending)
	}
	for i, want := range []int{1, 2, 0, 2} {
		if pending[i].BuildPage.Page != want {
			t.Fatalf("page %d=%d want %d", i, pending[i].BuildPage.Page, want)
		}
	}
	before := len(spy.aliases)
	b.switchBuildPage(9)
	b.switchBuildPage(3)
	if len(b.sess.PendingHumanCommands()) != 4 || len(spy.aliases) != before {
		t.Fatal("invalid or unchanged pending page emitted command/cue")
	}
}

func TestInputWorldDragSurvivesMinimapHeldSample(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	applyPendingBattleCommands(b)
	b.battleState().Input.DragActive = true
	b.battleState().Input.DragStartX = 300
	b.battleState().Input.DragStartY = 200
	b.battleState().Input.DragEndX = 300
	b.battleState().Input.DragEndY = 200
	in := input.NewState()
	in.Mouse.SetPosition(50, 50)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	in.Mouse.ResetEdges()
	b.handleInput(in, nil)
	if !b.battleState().Input.DragActive || b.battleState().Input.DragEndX != 50 || b.minimapCameraCaptured {
		t.Fatal("minimap stole active world drag")
	}
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
	if b.battleState().Input.DragActive {
		t.Fatal("release failed to retire world drag")
	}
}

func TestInputRecordTimestampsCannotExtendIdleClickDeadline(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		releaseMillis, recordTime uint32
		kind                      session.HumanCommandKind
	}{
		{"old event with live short hold", 1100, 4000000, session.HumanOrder},
		{"fresh event with expired hold", 2000, 1, session.HumanSelectionReplace},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
			u := placeUnit(b, "armcons", 200<<16, 120<<16)
			replaceSelectionForTest(t, b, u)
			source := &fakeMillisSource{ms: 1000}
			b.millisSource = source
			in := input.NewState()
			in.EnqueuePointer(input.PointerEvent{Kind: input.LeftDown, X: 300, Y: 200, Buttons: input.MouseButtons{Left: true}})
			in.PublishPointer()
			b.handleInput(in, nil)
			source.ms = tc.releaseMillis
			in.EnqueuePointer(input.PointerEvent{Kind: input.LeftUp, X: 300, Y: 200, Timestamp: tc.recordTime})
			in.PublishPointer()
			b.handleInput(in, nil)
			pending := b.sess.PendingHumanCommands()
			if len(pending) != 1 || pending[0].Kind != tc.kind {
				t.Fatalf("processing clock classified %+v", pending)
			}
		})
	}
}
