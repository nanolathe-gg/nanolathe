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
	repair := b.dragAreaTargets(b.sess.Snapshot.Current(), d)
	if len(repair) != 1 || repair[0].Target != builder {
		t.Fatalf("repair %+v", repair)
	}
	d.latch = input.LatchReclaim
	reclaim := b.dragAreaTargets(b.sess.Snapshot.Current(), d)
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
	if len(b.dragAreaTargets(b.sess.Snapshot.Current(), d)) != 0 {
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
			// The selection is made from a frame that shows the new units.
			b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
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
	b.localState().ClearSelection()
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
	// Ctrl+A selects through the local interface state
	// (DESIGN_MULTIPLAYER §7.3); clear it first to see the shortcut land.
	if b.localState().SelectionCount() == 0 || len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatalf("Ctrl+A was swallowed: selection %v, queued %+v", b.localState().SelectedRefs(), b.sess.PendingHumanCommands())
	}
	dragInput(b, cl, 400, 300, input.MouseButtonLeft, "release", input.Modifiers{})
	if len(b.sess.PendingHumanCommands()) != 0 {
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
				// The swap is a fixture's direct write to a resting record,
				// so it renews the record's publication revision as every
				// production mutation point does (features/publication.go).
				reclaimable := *b.cat.Features["deposit"]
				reclaimable.Reclaimable = true
				b.cat.Features["deposit"] = &reclaimable
				inst := b.sess.Features.InstanceAt(20, 20)
				inst.Def = &reclaimable
				b.sess.Features.MarkPublicationChanged(inst)
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

// dragQueuedSites returns the builder's queued build goals, in queue order.
func dragQueuedSites(b *battleSession, builder pool.Handle) (goals []dragPoint, products []string) {
	for _, n := range orders.QueueForUnit(b.sess.Units.Unit(builder)).Primary() {
		if orders.IsMobileBuild(n.ID) {
			goals = append(goals, dragPoint{int32(n.GoalX.Floor()), int32(n.GoalZ.Floor())})
			products = append(products, n.BuildDefKey)
		}
	}
	return goals, products
}

func dragRow(b *battleSession, cl *client.Client, fromX, toX, z int32, mods input.Modifiers) []dragBuildSite {
	x0, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(int64(fromX)), 0, numeric.FixedFromInt(int64(z)))
	x1, _ := o5ScreenWorld(b.cam, numeric.FixedFromInt(int64(toX)), 0, numeric.FixedFromInt(int64(z)))
	dragInput(b, cl, x0, y, input.MouseButtonLeft, "press", mods)
	dragInput(b, cl, x1, y, input.MouseButtonLeft, "held", mods)
	var sites []dragBuildSite
	if b.modernDrag != nil {
		sites = slices.Clone(b.modernDrag.sites)
	}
	dragInput(b, cl, x1, y, input.MouseButtonLeft, "release", mods)
	return sites
}

// A Shift row over the builder's own queued sites of the dragged product
// sends each such site the ordinary queued toggle, as a Shift-click there
// would [07 R-P0-11 §6]; the rest of the row still appends (§3.11).
func TestCommandDragShiftRowCancelsQueuedSites(t *testing.T) {
	b, cl, _, builder := resourceFixture(t, false)
	b.armPlacement(b.cat.Units["armsolar"])
	shift := input.Modifiers{Shift: true}
	if sites := dragRow(b, cl, 400, 496, 320, shift); len(sites) != 4 {
		t.Fatalf("first row previewed %d sites, want 4", len(sites))
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	queued, _ := dragQueuedSites(b, builder)
	if len(queued) != 4 || !b.battleState().PlacementArmed() {
		t.Fatalf("first row queued %v", queued)
	}

	// Without Shift nothing is toggled: queued footprints stay reserved.
	x0, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(400), 0, numeric.FixedFromInt(320))
	x1, _ := o5ScreenWorld(b.cam, numeric.FixedFromInt(560), 0, numeric.FixedFromInt(320))
	dragInput(b, cl, x0, y, input.MouseButtonLeft, "press", shift)
	dragInput(b, cl, x1, y, input.MouseButtonLeft, "held", input.Modifiers{})
	for i, site := range b.modernDrag.sites {
		if site.cancel || (i < 4) == site.valid {
			t.Fatalf("unshifted site %d = %+v", i, site)
		}
	}
	// Shift at release decides, and the preview follows it.
	dragInput(b, cl, x1, y, input.MouseButtonLeft, "held", shift)
	sites := slices.Clone(b.modernDrag.sites)
	dragInput(b, cl, x1, y, input.MouseButtonLeft, "release", shift)
	if len(sites) != 6 {
		t.Fatalf("second row previewed %d sites, want 6", len(sites))
	}
	for i, site := range sites {
		if site.cancel != (i < 4) || site.valid != (i >= 4) {
			t.Fatalf("site %d = %+v, want the first four cancelled and two added", i, site)
		}
	}
	cmds := resourceBuildCommands(b.sess)
	if len(cmds) != 6 {
		t.Fatalf("commands %+v", cmds)
	}
	for i, c := range cmds {
		if !c.Queued || c.AppendOnly != (i >= 4) || c.Product != "armsolar" {
			t.Fatalf("command %d intent %+v", i, c)
		}
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	remaining, _ := dragQueuedSites(b, builder)
	if len(remaining) != 2 || remaining[0].x <= queued[3].x || remaining[1].x <= remaining[0].x {
		t.Fatalf("queue after the cancelling row %v, want only the two new sites past %v", remaining, queued)
	}
}

// The row cancels only the dragged product and only from a committed queue:
// another product at the site stays reserved (the toggle would remove it),
// and a command awaiting its tick suppresses cancellation until it applies.
func TestCommandDragCancelBoundaries(t *testing.T) {
	b, cl, _, builder := resourceFixture(t, false)
	// A 3x3 extractor centred within one cell of the third solar site.
	if err := b.DispatchMobileBuild("extractor", numeric.FixedFromInt(472), 0, numeric.FixedFromInt(336), true); err != nil {
		t.Fatal(err)
	}
	b.armPlacement(b.cat.Units["armsolar"])
	shift := input.Modifiers{Shift: true}
	dragRow(b, cl, 400, 432, 320, shift)
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	if before, products := dragQueuedSites(b, builder); len(before) != 3 {
		t.Fatalf("queue %v %v", before, products)
	}
	probe := func() dragBuildSite {
		t.Helper()
		x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(472), 0, numeric.FixedFromInt(336))
		dragInput(b, cl, x, y, input.MouseButtonLeft, "press", shift)
		if b.modernDrag == nil || len(b.modernDrag.sites) != 1 {
			t.Fatal("probe press did not capture")
		}
		site := b.modernDrag.sites[0]
		escape := input.NewState()
		escape.Mouse.SetPosition(float32(x), float32(y))
		escape.Kbd.SetKey(input.KeyEscape, true)
		b.handleInput(escape, cl)
		b.armPlacement(b.cat.Units["armsolar"])
		return site
	}
	// Before the gesture is a drag it is a Shift-click, whose toggle matches
	// any product: the preview shows the extractor it would remove.
	if site := probe(); !site.cancel {
		t.Fatalf("undragged press over the extractor = %+v, want the click's cancellation", site)
	}
	// An unshifted build awaiting its tick will purge the queue first.
	if err := b.DispatchMobileBuild("armsolar", numeric.FixedFromInt(640), 0, numeric.FixedFromInt(640), false); err != nil {
		t.Fatal(err)
	}
	if site := probe(); site.cancel {
		t.Fatal("a site was cancelled against a queue that a pending command will change")
	}
	if len(resourceBuildCommands(b.sess)) != 1 {
		t.Fatal("an abandoned probe issued a command")
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	// Rebuild the three-site queue behind the pending purge, then drag.
	if err := b.DispatchMobileBuild("extractor", numeric.FixedFromInt(472), 0, numeric.FixedFromInt(336), false); err != nil {
		t.Fatal(err)
	}
	dragRow(b, cl, 400, 432, 320, shift)
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	sites := dragRow(b, cl, 400, 464, 320, shift)
	if len(sites) != 3 || !sites[0].cancel || !sites[1].cancel || sites[2].cancel || sites[2].valid {
		t.Fatalf("sites %+v, want two solar cancels and a refused extractor site", sites)
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	after, products := dragQueuedSites(b, builder)
	if len(after) != 1 || products[0] != "extractor" {
		t.Fatalf("queue after row %v %v, want only the extractor", after, products)
	}
}

func TestCommandDragAreaReclaimWalksNearestFirstPerWorker(t *testing.T) {
	b, _, _, builder := resourceFixture(t, false)
	// armcons in this fixture cannot reclaim; armrecl can.
	reclaimer, ok := b.cat.Unit("armrecl")
	if !ok {
		t.Fatal("fixture reclaimer missing")
	}
	west, err := b.sess.Units.Create(reclaimer, 0, numeric.Fixed(100<<16), 0, numeric.Fixed(160<<16))
	if err != nil {
		t.Fatal(err)
	}
	east, err := b.sess.Units.Create(reclaimer, 0, numeric.Fixed(900<<16), 0, numeric.Fixed(160<<16))
	if err != nil {
		t.Fatal(err)
	}
	// Selection admits only published units, so publish them first.
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	if err := b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder, west, east}}}); err != nil {
		t.Fatal(err)
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	f, _ := b.currentSnapshot()
	next := b.sess.Snapshot.BeginWrite()
	*next = *f
	// Publication order is deliberately scrambled along the row.
	next.Features = nil
	for _, x := range []int64{600, 200, 800, 400} {
		next.Features = append(next.Features, frame.FeatureView{CX: int32(x / 16), CZ: 10, FootX: 1, FootZ: 1, X: numeric.Fixed(x << 16), Z: 160 << 16, Reclaimable: true, Blocking: true})
	}
	if err := b.sess.Snapshot.Publish(f.Tick + 1); err != nil {
		t.Fatal(err)
	}
	f = b.sess.Snapshot.Current()
	d := &battleCommandDrag{start: dragPoint{150, 100}, end: dragPoint{850, 220}, latch: input.LatchReclaim}
	b.dispatchAreaWork(f, 12, b.dragAreaTargets(f, d), false)
	xs := func(c session.HumanCommand) []int64 {
		var out []int64
		for _, target := range c.Order.Targets {
			out = append(out, target.Position.X.Floor())
		}
		return out
	}
	byActor := map[pool.Handle][]int64{}
	var handles [][]pool.Handle
	for _, c := range b.sess.PendingHumanCommands() {
		if c.Kind != session.HumanOrder || c.Order.Code != 12 {
			continue
		}
		handles = append(handles, c.Order.Handles)
		for _, h := range c.Order.Handles {
			byActor[h] = xs(c)
		}
	}
	if got := byActor[west]; !slices.Equal(got, []int64{200, 400, 600, 800}) {
		t.Fatalf("west worker %v", got)
	}
	if got := byActor[east]; !slices.Equal(got, []int64{800, 600, 400, 200}) {
		t.Fatalf("east worker %v", got)
	}
	// The unit that cannot reclaim queues nothing; it rides in the first
	// worker's command rather than costing a command of its own.
	if want := [][]pool.Handle{{builder, west}, {east}}; !slices.EqualFunc(handles, want, slices.Equal) {
		t.Fatalf("commands name %v, want %v", handles, want)
	}
}

// The wheel during a build row widens the steps between its sites instead of
// zooming, and the gap stays for the next row of the battle.
func TestCommandDragWheelSetsBuildGap(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	solar := b.cat.Units["armsolar"]
	b.armPlacement(solar)
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{})
	if !b.buildDragWheelOwned() {
		t.Fatal("a build drag must own the wheel")
	}
	wheel := input.NewState()
	wheel.Mouse.SetPosition(492, 250)
	wheel.Mouse.SetButton(input.MouseButtonLeft, true)
	wheel.Mouse.ResetEdges()
	wheel.Mouse.SetWheel(0, 1.5) // one notch now, half a notch carried
	b.handleInput(wheel, cl)
	wheel.Mouse.SetWheel(0, 0.5)
	b.handleInput(wheel, cl)
	if b.buildDragGap != 2 {
		t.Fatalf("gap = %d, want 2", b.buildDragGap)
	}
	dragInput(b, cl, 492, 250, input.MouseButtonLeft, "release", input.Modifiers{})
	got := resourceBuildCommands(b.sess)
	if len(got) < 2 {
		t.Fatalf("row too short: %+v", got)
	}
	step := numeric.FixedFromInt(int64((solar.FootprintX + 2) * 16))
	for i := 1; i < len(got); i++ {
		if got[i].WX-got[i-1].WX != step || got[i].WZ != got[0].WZ {
			t.Fatalf("site %d at %v,%v after %v: want an X step of %v", i, got[i].WX, got[i].WZ, got[i-1].WX, step)
		}
	}
	if b.buildDragWheelOwned() || b.buildDragGap != 2 {
		t.Fatal("the wheel stays owned after release, or the gap was lost")
	}
	b.stepBuildDragGap(-100, 0)
	if b.buildDragGap != 0 {
		t.Fatalf("gap below zero: %d", b.buildDragGap)
	}
	b.stepBuildDragGap(100, 0)
	if b.buildDragGap != dragBuildGapMax {
		t.Fatalf("gap above the bound: %d", b.buildDragGap)
	}
}

// Precise scrolling (trackpad, Magic Mouse) has no notch count: its vertical
// travel steps the gap, and the camera does not pan while a build drag owns it.
func TestCommandDragPreciseScrollSetsBuildGapWithoutPanning(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	b.armPlacement(b.cat.Units["armsolar"])
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{})
	wheel := input.NewState()
	wheel.Mouse.SetPosition(492, 250)
	wheel.Mouse.SetButton(input.MouseButtonLeft, true)
	wheel.Mouse.ResetEdges()
	wheel.Mouse.PanY = dragBuildGapPanStep * 1.5 // one notch now, half carried
	b.handleInput(wheel, cl)
	wheel.Mouse.PanY = dragBuildGapPanStep * 0.5
	b.handleInput(wheel, cl)
	if b.buildDragGap != 2 {
		t.Fatalf("gap = %d, want 2", b.buildDragGap)
	}
	x, z := b.cam.X, b.cam.Z
	b.applyTrackpadGestures(&input.MouseState{PanX: 200, PanY: 200}, true, 492, 250)
	if b.cam.X != x || b.cam.Z != z {
		t.Fatalf("camera panned during a build drag: %v,%v -> %v,%v", x, z, b.cam.X, b.cam.Z)
	}
}

// macOS turns a wheel sideways while Shift is held; a Shift row still steps
// its gap from the horizontal notch, and an unmodified sideways wheel does not.
func TestCommandDragShiftWheelSetsBuildGap(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	b.armPlacement(b.cat.Units["armsolar"])
	shift := input.Modifiers{Shift: true}
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", shift)
	wheel := input.NewState()
	wheel.Mouse.SetPosition(492, 250)
	wheel.Mouse.SetButton(input.MouseButtonLeft, true)
	wheel.Mouse.ResetEdges()
	wheel.Mouse.SetWheel(1, 0)
	b.handleInput(wheel, cl)
	if b.buildDragGap != 0 {
		t.Fatalf("unmodified sideways wheel set gap %d", b.buildDragGap)
	}
	wheel.Kbd.SetKey(input.KeyShift, true)
	wheel.Kbd.ResetEdges()
	b.handleInput(wheel, cl)
	if b.buildDragGap != 1 {
		t.Fatalf("Shift wheel gap = %d, want 1", b.buildDragGap)
	}
}

// The gap keys step a build row's gap with Shift held or released, and their
// press does not cancel the row the way a new command's key does; outside a
// build drag they change nothing. The frames run through viewerStep, which
// owns the token queue.
func TestCommandDragKeysSetBuildGap(t *testing.T) {
	b, cl, _, _ := resourceFixture(t, false)
	b.armPlacement(b.cat.Units["armsolar"])
	cl.SetFocused(true)
	in := cl.Input()
	in.Mouse.SetPosition(300, 250)
	frame := func(shift bool, key input.Key, r rune) {
		in.Mouse.ResetEdges()
		in.Kbd.ResetEdges()
		in.Kbd.SetKey(input.KeyShift, shift)
		if key != input.KeyNone {
			in.Kbd.SetKey(key, true)
			in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
		}
		b.viewerStep(0, cl)
		in.Kbd.SetKey(key, false)
	}
	frame(false, input.KeyRightBracket, ']')
	if b.buildDragGap != 0 {
		t.Fatalf("a gap key outside a build drag set gap %d", b.buildDragGap)
	}
	dragInput(b, cl, 300, 250, input.MouseButtonLeft, "press", input.Modifiers{Shift: true})
	in.Mouse.SetPosition(492, 250)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	frame(true, input.KeyNone, 0)
	if !b.buildDragWheelOwned() {
		t.Fatal("no build drag")
	}
	frame(true, input.KeyRightBracket, '}')
	frame(false, input.KeyRightBracket, ']')
	frame(false, input.KeyRightBracket, ']')
	frame(true, input.KeyLeftBracket, '{')
	if b.buildDragGap != 2 || !b.buildDragWheelOwned() {
		t.Fatalf("gap = %d, drag %v; want 2 and the row still held", b.buildDragGap, b.buildDragWheelOwned())
	}
	for range dragBuildGapMax + 2 {
		frame(false, input.KeyLeftBracket, '[')
	}
	if b.buildDragGap != 0 || !b.buildDragWheelOwned() {
		t.Fatalf("gap = %d, drag %v; want 0 and the row still held", b.buildDragGap, b.buildDragWheelOwned())
	}
}
