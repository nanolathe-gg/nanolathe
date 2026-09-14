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
						if absInt32(int32((c.WX-prior.WX).Floor())) < 32 && absInt32(int32((c.WZ-prior.WZ).Floor())) < 32 {
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
		{CX: 12, CZ: 12, FootX: 1, FootZ: 1, X: 200 << 16, Z: 200 << 16, Reclaimable: true},
		{CX: 13, CZ: 13, FootX: 1, FootZ: 1, X: 216 << 16, Z: 216 << 16, Reclaimable: false},
		{CX: 40, CZ: 40, FootX: 1, FootZ: 1, X: 648 << 16, Z: 648 << 16, Reclaimable: true},
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
	if len(reclaim) != 1 || reclaim[0].Target != 0 || !reclaim[0].Position.HasFeature {
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
	for _, right := range []bool{false, true} {
		t.Run(fmt.Sprint(right), func(t *testing.T) {
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
			if right {
				b.interfaceType = settings.InterfaceTypeRightClick
				b.battleState().SetLatch(input.LatchNormal)
				button = input.MouseButtonRight
			}
			dragInput(b, cl, 300, 200, button, "press", input.Modifiers{})
			dragInput(b, cl, 300, 300, button, "held", input.Modifiers{})
			dragInput(b, cl, 400, 300, button, "release", input.Modifiers{Shift: true})
			cmds := b.sess.PendingHumanCommands()
			if len(cmds) != len(handles) {
				t.Fatalf("commands: %+v", cmds)
			}
			seen := map[pool.Handle]bool{}
			goals := map[dragPoint]bool{}
			for _, c := range cmds {
				if c.Kind != session.HumanOrder || c.Order.Code != int(input.LatchMove) || len(c.Order.Handles) != 1 || !c.Order.Queued {
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
