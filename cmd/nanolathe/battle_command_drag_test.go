package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// These tests exercise the requested modern policy, not inferred retail input.
func dragInput(b *battleSession, cl *client.Client, x, y int32, button input.MouseButton, phase string, mods input.Modifiers) {
	in := input.NewState()
	in.Mouse.SetPosition(float32(x), float32(y))
	in.Kbd.SetKey(input.KeyShift, mods.Shift)
	in.Kbd.SetKey(input.KeyAlt, mods.Alt)
	in.Kbd.SetKey(input.KeyCtrl, mods.Ctrl)
	in.Kbd.ResetEdges()
	in.Mouse.SetButton(button, true)
	if phase != "press" {
		in.Mouse.ResetEdges()
	}
	if phase == "release" {
		in.Mouse.SetButton(button, false)
	}
	b.handleInput(in, cl)
}

func TestCommandDragBuildReleaseAndGrid(t *testing.T) {
	for _, grid := range []bool{false, true} {
		for _, shift := range []bool{false, true} {
			t.Run(fmt.Sprintf("grid=%v shift=%v", grid, shift), func(t *testing.T) {
				b, cl, _, _ := resourceFixture(t, false)
				b.armPlacement(b.cat.Units["armsolar"])
				mods := input.Modifiers{Alt: grid, Shift: shift}
				dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", mods)
				dragInput(b, cl, 396, 314, input.MouseButtonLeft, "held", mods)
				if len(resourceBuildCommands(b.sess)) != 0 {
					t.Fatal("committed before release")
				}
				d := b.modernDrag
				if d == nil || !d.dragged || len(d.sites) < 3 {
					t.Fatalf("preview missing: %+v", d)
				}
				sites := slices.Clone(d.sites)
				dragInput(b, cl, 396, 314, input.MouseButtonLeft, "release", mods)
				got := resourceBuildCommands(b.sess)
				if len(got) != len(sites) {
					t.Fatalf("builds %d != preview sites %d: %+v", len(got), len(sites), sites)
				}
				for i, c := range got {
					if !c.AppendOnly || c.Queued != (shift || i > 0) {
						t.Fatalf("order %d intent: %+v", i, c)
					}
					for _, prior := range got[:i] {
						if numeric.Abs(int32((c.WX-prior.WX).Floor())) < 32 && numeric.Abs(int32((c.WZ-prior.WZ).Floor())) < 32 {
							t.Fatal("overlapping footprints")
						}
					}
				}
				if b.modernDrag != nil || b.battleState().PlacementArmed() != shift {
					t.Fatal("capture or latch survived incorrectly")
				}
			})
		}
	}
}

func TestCommandDragReservationsSkipAndFirstAcceptedReplaces(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	b.armPlacement(b.cat.Units["armsolar"])
	b.updatePlacement(300, 250)
	if !b.commitBuild(true) {
		t.Fatal("reserve initial site")
	}
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{})
	dragInput(b, cl, 396, 250, input.MouseButtonLeft, "held", input.Modifiers{})
	if b.modernDrag.sites[0].valid {
		t.Fatal("reserved footprint previewed green")
	}
	dragInput(b, cl, 396, 250, input.MouseButtonLeft, "release", input.Modifiers{})
	got := resourceBuildCommands(b.sess)
	if len(got) < 2 || got[1].Queued {
		t.Fatalf("first accepted site must replace: %+v", got)
	}
}

func TestCommandDragCancellationAndClassic(t *testing.T) {
	for _, reason := range []string{"escape", "right", "focus", "selection", "latch", "lost release", "classic"} {
		t.Run(reason, func(t *testing.T) {
			b, cl, _, _ := resourceFixture(t, false)
			b.armPlacement(b.cat.Units["armsolar"])
			if reason == "classic" {
				cl.SetEnhanced(false)
			}
			dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{})
			if reason == "classic" {
				if b.modernDrag != nil || len(resourceBuildCommands(b.sess)) != 1 {
					t.Fatal("classic press changed")
				}
				return
			}
			in := input.NewState()
			in.Mouse.SetPosition(396, 250)
			switch reason {
			case "escape":
				in.Kbd.SetKey(input.KeyEscape, true)
			case "right":
				in.Mouse.SetButton(input.MouseButtonRight, true)
			case "focus":
				cl.SetFocused(false)
			case "latch":
				b.battleState().SetLatch(input.LatchAttack)
			case "selection":
				f, _ := b.currentSnapshot()
				next := b.sess.Snapshot.BeginWrite()
				*next = *f
				next.Selection.Handles = nil
				if err := b.sess.Snapshot.Publish(f.Tick + 1); err != nil {
					t.Fatal(err)
				}
			}
			b.handleInput(in, cl)
			if b.modernDrag != nil || len(resourceBuildCommands(b.sess)) != 0 {
				t.Fatal("cancelled drag committed")
			}
		})
	}
}

func TestCommandDragAreaFiltersTargets(t *testing.T) {
	b, _, _, builder := resourceFixture(t, false)
	f, _ := b.currentSnapshot()
	next := b.sess.Snapshot.BeginWrite()
	*next = *f
	next.Units = []frame.UnitView{
		{Slot: builder, Owner: 0, X: 200 << 16, Z: 200 << 16, Health: 50, MaxHealth: 100},
		{Slot: 2, Owner: 0, X: 220 << 16, Z: 220 << 16, Health: 100, MaxHealth: 100},
		{Slot: 3, Owner: 1, X: 220 << 16, Z: 220 << 16, Health: 50, MaxHealth: 100},
		{Slot: 4, Owner: 0, X: 900 << 16, Z: 900 << 16, Health: 50, MaxHealth: 100},
	}
	next.Features = []frame.FeatureView{
		{CX: 12, CZ: 12, FootX: 1, FootZ: 1, X: 200 << 16, Z: 200 << 16, Reclaimable: true, Blocking: true},
		{CX: 13, CZ: 13, FootX: 1, FootZ: 1, X: 216 << 16, Z: 216 << 16, Reclaimable: false, Blocking: true},
		{CX: 40, CZ: 40, FootX: 1, FootZ: 1, X: 648 << 16, Z: 648 << 16, Reclaimable: true, Blocking: true},
		// Reclaimable ground cover inside the drag must not become work.
		{CX: 14, CZ: 14, FootX: 1, FootZ: 1, X: 232 << 16, Z: 232 << 16, Reclaimable: true},
	}
	if err := b.sess.Snapshot.Publish(f.Tick + 1); err != nil {
		t.Fatal(err)
	}
	d := &battleCommandDrag{start: dragPoint{180, 180}, end: dragPoint{260, 260}, latch: input.LatchRepair}
	repair := b.dragAreaTargets(d)
	if len(repair) != 1 || repair[0].Target != builder {
		t.Fatalf("repair %+v", repair)
	}
	d.latch = input.LatchReclaim
	reclaim := b.dragAreaTargets(d)
	if len(reclaim) != 1 || reclaim[0].Target != 0 || !reclaim[0].Position.HasFeature || reclaim[0].Position.X != 200<<16 {
		t.Fatalf("reclaim %+v", reclaim)
	}
	f, _ = b.currentSnapshot()
	next = b.sess.Snapshot.BeginWrite()
	*next = *f
	next.Visibility.Valid = false
	if err := b.sess.Snapshot.Publish(f.Tick + 1); err != nil {
		t.Fatal(err)
	}
	if len(b.dragAreaTargets(d)) != 0 {
		t.Fatal("reclaim admitted hidden feature")
	}
}

func TestCommandDragFormationUsesCurveAndIndividualActors(t *testing.T) {
	for _, gesture := range []string{"armed", "right", "alt-left", "alt-left-right-interface"} {
		t.Run(gesture, func(t *testing.T) {
			b, cl, _, builder := resourceFixture(t, false)
			def := b.cat.Units["armcons"]
			handles := []pool.Handle{builder}
			for i := 0; i < 2; i++ {
				h, err := b.sess.Units.Create(def, 0, numeric.FixedFromInt(int64(180+i*40)), 0, 160<<16)
				if err != nil {
					t.Fatal(err)
				}
				handles = append(handles, h)
			}
			if err := b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: handles}}); err != nil {
				t.Fatal(err)
			}
			b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
			button := input.MouseButtonLeft
			b.battleState().SetLatch(input.LatchMove)
			if gesture == "right" {
				b.interfaceType = settings.InterfaceTypeRightClick
				b.battleState().SetLatch(input.LatchNormal)
				button = input.MouseButtonRight
			}
			mods := input.Modifiers{}
			if gesture == "alt-left" || gesture == "alt-left-right-interface" {
				b.battleState().SetLatch(input.LatchNormal)
				mods.Alt = true
				if gesture == "alt-left-right-interface" {
					b.interfaceType = settings.InterfaceTypeRightClick
				}
			}
			dragInput(b, cl, 300, 200, button, "press", mods)
			dragInput(b, cl, 300, 300, button, "held", input.Modifiers{})
			dragInput(b, cl, 400, 300, button, "release", input.Modifiers{Shift: true})
			cmds := b.sess.PendingHumanCommands()
			if len(cmds) != len(handles) {
				t.Fatalf("commands: %+v", cmds)
			}
			seen := map[pool.Handle]bool{}
			goals := map[dragPoint]bool{}
			for _, c := range cmds {
				if c.Kind != session.HumanOrder || c.Order.Code != int(input.LatchMove) || len(c.Order.Handles) != 1 || !c.Order.Queued || !c.Order.AssignedPosition {
					t.Fatalf("command: %+v", c)
				}
				h := c.Order.Handles[0]
				if seen[h] {
					t.Fatal("actor assigned twice")
				}
				seen[h] = true
				goals[dragPoint{int32(c.Order.Position.X.Floor()), int32(c.Order.Position.Z.Floor())}] = true
			}
			if len(goals) != 3 {
				t.Fatalf("coincident goals: %+v", goals)
			}
			// The bend must survive; a straight chord would put the middle point at
			// (350,250) in this flat native-scale fixture.
			if goals[dragPoint{350, 250}] {
				t.Fatal("curve collapsed to chord")
			}
			if b.battleState().Input.DragActive {
				t.Fatal("formation became box selection")
			}
		})
	}
}

func TestCommandDragAreaReleaseProducesOneBatch(t *testing.T) {
	b, cl, _, builder := resourceFixture(t, false)
	f, _ := b.currentSnapshot()
	next := b.sess.Snapshot.BeginWrite()
	*next = *f
	next.Units = slices.Clone(f.Units)
	next.Units = append(next.Units, frame.UnitView{Slot: 12, Owner: 0, X: 340 << 16, Z: 280 << 16, Health: 50, MaxHealth: 100})
	if err := b.sess.Snapshot.Publish(f.Tick + 1); err != nil {
		t.Fatal(err)
	}
	b.battleState().SetLatch(input.LatchRepair)
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{})
	dragInput(b, cl, 400, 320, input.MouseButtonLeft, "release", input.Modifiers{})
	cmds := b.sess.PendingHumanCommands()
	if len(cmds) != 1 || len(cmds[0].Order.Targets) != 1 || cmds[0].Order.Targets[0].Target != 12 || !slices.Equal(cmds[0].Order.Handles, []pool.Handle{builder}) || cmds[0].Order.Code != 8 {
		t.Fatalf("batch: %+v", cmds)
	}
	if cmds[0].Order.Position.InterfaceType != orders.InterfaceTypeLeftClick {
		t.Fatal("interface changed")
	}
}

func TestCommandDragKeyboardCancelsWithoutSwallowingShortcut(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	b.battleState().SetLatch(input.LatchMove)
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{})
	in := input.NewState()
	in.Mouse.SetPosition(400, 300)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	in.Mouse.ResetEdges()
	in.Kbd.SetKey(input.KeyCtrl, true)
	in.Kbd.SetKey(input.KeyA, true)
	b.handleInput(in, cl)
	if b.modernDrag != nil {
		t.Fatal("shortcut retained command capture")
	}
	cmds := b.sess.PendingHumanCommands()
	if len(cmds) != 1 || cmds[0].Kind != session.HumanSelectionReplace {
		t.Fatalf("Ctrl+A was swallowed: %+v", cmds)
	}
	dragInput(b, cl, 400, 300, input.MouseButtonLeft, "release", input.Modifiers{})
	if len(b.sess.PendingHumanCommands()) != 1 {
		t.Fatal("old release issued an order")
	}
}

func TestCommandDragSingleRightUnitReceivesMidpoint(t *testing.T) {
	b, cl, _, builder := resourceFixture(t, false)
	b.interfaceType = settings.InterfaceTypeRightClick
	dragInput(b, cl, 300, 250, input.MouseButtonRight, "press", input.Modifiers{})
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("right press committed before drag classification")
	}
	dragInput(b, cl, 400, 250, input.MouseButtonRight, "release", input.Modifiers{})
	cmds := b.sess.PendingHumanCommands()
	if len(cmds) != 1 || !slices.Equal(cmds[0].Order.Handles, []pool.Handle{builder}) || cmds[0].Order.Position.X != 350<<16 {
		t.Fatalf("single midpoint: %+v", cmds)
	}
}

func TestCommandDragShortReleaseAtRadarKeepsViewport(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	withMinimap(b)
	b.battleState().SetLatch(input.LatchMove)
	dragInput(b, cl, 129, 80, input.MouseButtonLeft, "press", input.Modifiers{})
	if b.modernDrag == nil {
		t.Fatal("viewport press did not capture")
	}
	wx, _, wz := b.cursorWorld(125, 80)
	dragInput(b, cl, 125, 80, input.MouseButtonLeft, "release", input.Modifiers{})
	cmds := b.sess.PendingHumanCommands()
	if len(cmds) != 1 || cmds[0].Order.Position.X != wx || cmds[0].Order.Position.Z != wz {
		t.Fatalf("release jumped to radar: %+v; want %v,%v", cmds, wx, wz)
	}
}

func TestCommandDragAltMoveOverFeature(t *testing.T) {
	for _, drag := range []bool{false, true} {
		for _, queued := range []bool{false, true} {
			t.Run(fmt.Sprintf("drag=%v queued=%v", drag, queued), func(t *testing.T) {
				b, cl, _, builder := resourceFixture(t, true)
				// A compiled definition is immutable once a battle publishes
				// it (publication retains views by definition identity), so
				// the reclaimable variant is a new definition swapped in for
				// the placed instance, not an edit of the old one in place.
				reclaimable := *b.cat.Features["deposit"]
				reclaimable.Reclaimable = true
				b.cat.Features["deposit"] = &reclaimable
				b.sess.Features.InstanceAt(20, 20).Def = &reclaimable
				b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
				x, y := o5ScreenWorld(b.cam, 320<<16, 0, 320<<16)
				_, _, pos := b.pickTarget(x, y)
				if pos == nil || !pos.HasFeature {
					t.Fatal("fixture must start on a reclaimable feature")
				}
				dragInput(b, cl, x, y, input.MouseButtonLeft, "press", input.Modifiers{Alt: true, Shift: queued})
				if b.modernDrag == nil || b.battleState().Input.DragActive || len(b.sess.PendingHumanCommands()) != 0 {
					t.Fatal("Alt press did not exclusively capture movement")
				}
				if drag {
					x += 100
				}
				// The initiating Alt may be released; Shift at release controls queuing.
				dragInput(b, cl, x, y, input.MouseButtonLeft, "release", input.Modifiers{Shift: queued})
				cmds := b.sess.PendingHumanCommands()
				if len(cmds) != 1 || cmds[0].Kind != session.HumanOrder || cmds[0].Order.Code != int(input.LatchMove) || cmds[0].Order.Queued != queued {
					t.Fatalf("Alt gesture emitted contextual work or selection: %+v", cmds)
				}
				if drag && !slices.Equal(cmds[0].Order.Handles, []pool.Handle{builder}) {
					t.Fatal("formation lost its explicit actor")
				}
				f, _ := b.currentSnapshot()
				if !slices.Equal(f.Selection.Handles, []pool.Handle{builder}) {
					t.Fatal("Alt gesture changed the selection")
				}
				if b.modernDrag != nil || b.battleState().Input.Latch != input.LatchNormal || b.battleState().Input.ShiftLatchSticky {
					t.Fatal("Alt gesture left a persistent move mode")
				}
			})
		}
	}
}

func TestCommandDragAltRespectsSelectionAndArmedWork(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mods    input.Modifiers
		classic bool
		latch   input.Latch
		capture bool
	}{
		{name: "normal selection"},
		{name: "additive selection", mods: input.Modifiers{Shift: true}},
		{name: "classic Alt selection", mods: input.Modifiers{Alt: true}, classic: true},
		{name: "Ctrl selection", mods: input.Modifiers{Alt: true, Ctrl: true}},
		{name: "repair precedence", mods: input.Modifiers{Alt: true}, latch: input.LatchRepair, capture: true},
		{name: "reclaim precedence", mods: input.Modifiers{Alt: true}, latch: input.LatchReclaim, capture: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, cl, _, _ := resourceFixture(t, false)
			cl.SetEnhanced(!tc.classic)
			b.battleState().SetLatch(tc.latch)
			dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", tc.mods)
			if tc.capture {
				if b.modernDrag == nil || b.modernDrag.latch != tc.latch {
					t.Fatal("Alt overrode armed work")
				}
			} else if b.modernDrag != nil || !b.battleState().Input.DragActive {
				t.Fatal("formation shortcut stole selection")
			}
		})
	}
}
