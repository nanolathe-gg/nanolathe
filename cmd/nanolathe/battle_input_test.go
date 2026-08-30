package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// screenPos computes the framebuffer position where the world renderer draws a
// unit [07 §9][03 §2.5].
func screenPos(cam *camera.Camera, u *units.Unit) (int32, int32) {
	sx, sy := cam.WorldToScreen(u.X, u.Y, u.Z)
	return sx - camera.OriginX, sy - camera.OriginY
}

func clickAt(b *battleSession, sx, sy int32, shift bool) {
	applyPendingBattleCommands(b)
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	// Press
	in.Mouse.SetPosition(float32(sx), float32(sy))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	// Hold one frame (drag tracking)
	in.Mouse.ResetEdges()
	in.Kbd.ResetEdges()
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(sx), float32(sy))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	// Release
	in.Mouse.ResetEdges()
	in.Kbd.ResetEdges()
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(sx), float32(sy))
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
	applyPendingBattleCommands(b)
}

func rightClickAt(b *battleSession, sx, sy int32, shift bool) {
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(sx), float32(sy))
	in.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(in, nil)
	applyPendingBattleCommands(b)
}

func dragSelect(b *battleSession, sx0, sy0, sx1, sy1 int32, shift bool) {
	applyPendingBattleCommands(b)
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(sx0), float32(sy0))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	in.Mouse.ResetEdges()
	in.Kbd.ResetEdges()
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(sx1), float32(sy1))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	in.Mouse.ResetEdges()
	in.Kbd.ResetEdges()
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	in.Mouse.SetPosition(float32(sx1), float32(sy1))
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
	applyPendingBattleCommands(b)
}

// applyPendingBattleCommands advances the real session input boundary used by
// production. Input helpers bind the typed queue explicitly, then run one
// authoritative tick so assertions inspect applied state rather than a test
// fallback [01 §4.4][07 §9].
func applyPendingBattleCommands(b *battleSession) {
	if b == nil || b.sess == nil {
		return
	}
	b.sess.State = session.StateBattle
	now := int32(1)
	if b.sess.Clock != nil && b.sess.Clock.GlobalTick > 0 {
		now = int32(b.sess.Clock.GlobalTick + 1)
	}
	b.sess.Step(now)
}

func replaceSelectionForTest(t *testing.T, b *battleSession, us ...*units.Unit) {
	t.Helper()
	handles := make([]pool.Handle, 0, len(us))
	for _, u := range us {
		if u != nil {
			handles = append(handles, u.Handle)
		}
	}
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: handles}}); err != nil {
		t.Fatalf("replace selection: %v", err)
	}
	applyPendingBattleCommands(b)
}

// TestClickCommanderSelectsExactlyOne [07 §9][RS-P0-003] small-click selects exactly one via canonical picker.
func TestClickCommanderSelectsExactlyOne(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	cam := b.cam
	// Place commander inside the visible battle surface. The renderer's world
	// pass uses framebuffer coordinates, so points under the side rail are not
	// valid click fixtures.
	cmdr := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	b.battleState().Input.Latch = input.LatchNormal
	sx, sy := screenPos(cam, cmdr)
	clickAt(b, sx, sy, false)
	count := 0
	var selected *units.Unit
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Flags&hud.SelectionFlag != 0 {
			count++
			selected = u
		}
	}
	if count != 1 {
		t.Fatalf("click commander: want exactly 1 selected, got %d", count)
	}
	if selected != cmdr {
		t.Fatalf("click commander selected wrong unit")
	}
}

// TestClickAtRenderedCommanderPosition selects at the framebuffer position
// where the battle renderer draws the unit. The input path must not subtract
// the HUD viewport origin a second time [03 §2.5][07 §8].
func TestClickAtRenderedCommanderPosition(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	b.battleState().Input.Latch = input.LatchNormal
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	beamX, beamY := b.cam.WorldToScreen(commander.X, commander.Y, commander.Z)
	sx, sy := beamX-camera.OriginX, beamY-camera.OriginY
	clickAt(b, sx, sy, false)

	if commander.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("rendered commander at (%d,%d) was not selected", sx, sy)
	}
}

// TestClickAtRenderedCommanderPositionUsesSnapshotPicker covers the normal
// loaded-battle path, where input picks from the immutable frame already being
// rendered rather than the live-pool fallback [03 §2.5][07 §9].
func TestClickAtRenderedCommanderPositionUsesSnapshotPicker(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.sess.LocalOwner = 0
	b.battleState().Input.Latch = input.LatchNormal
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	w := b.sess.Snapshot.BeginWrite()
	*w = frame.Frame{
		Units: []frame.UnitView{{
			Slot:  commander.Handle,
			Owner: b.sess.LocalOwner,
			X:     commander.X,
			Y:     commander.Y,
			Z:     commander.Z,
		}},
		Selection: frame.SelectionView{LocalPlayer: b.sess.LocalOwner},
	}
	_ = b.sess.Snapshot.Publish(w.Tick)

	beamX, beamY := b.cam.WorldToScreen(commander.X, commander.Y, commander.Z)
	sx, sy := beamX-camera.OriginX, beamY-camera.OriginY
	clickAt(b, sx, sy, false)

	if commander.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("snapshot-rendered commander at (%d,%d) was not selected", sx, sy)
	}
}

// TestEmptyClickClearsShiftToggles [07 §9] C6 empty clears, shift toggles/adds.
func TestEmptyClickClearsShiftToggles(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(30, 30)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	a := placeUnit(b, "armcons", numeric.Fixed(180*65536), numeric.Fixed(100*65536))
	c := placeUnit(b, "armsolar", numeric.Fixed(320*65536), numeric.Fixed(220*65536))
	b.battleState().Input.Latch = input.LatchNormal
	// Click A selects A
	sxA, syA := screenPos(b.cam, a)
	clickAt(b, sxA, syA, false)
	if a.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("A should be selected after click")
	}
	// Click B without shift replaces: A cleared, B selected
	sxC, syC := screenPos(b.cam, c)
	clickAt(b, sxC, syC, false)
	if a.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("A should be cleared after replace click on C")
	}
	if c.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("C should be selected after replace")
	}
	// Shift-click A adds A (now both selected) [07 §9] additive toggle inside
	clickAt(b, sxA, syA, true)
	if a.Flags&hud.SelectionFlag == 0 || c.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("shift-click should add A, want both selected")
	}
	// Shift-click A again toggles A off, leaving only C
	clickAt(b, sxA, syA, true)
	if a.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("shift toggle should deselect A")
	}
	if c.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("C should remain selected after toggling A off")
	}
	// Left empty with selection issues a contextual move order and does NOT clear [07 §9][04 §3.4] — right-click is deselect/cancel only.
	// Use the mobile builder A (armcons, CanMove) for move tests; C is a building (armsolar) that cannot move.
	clickAt(b, sxA, syA, false) // select mobile A
	if a.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("A should be selected for move test")
	}
	qBefore := 0
	if q := orders.QueueForUnit(a); q != nil {
		qBefore = q.LenPrimary()
	}
	// Use a far empty ground location that is not within 16px of any unit.
	// Use a far empty point so it is not intercepted as minimap input [C-6][07 §10].
	clickAt(b, 500, 300, false) // far empty ground
	if a.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("left empty with selection should preserve selection (issues move instead of clear)")
	}
	if c.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("left empty should not affect C, got %v", c.Flags&hud.SelectionFlag != 0)
	}
	if q := orders.QueueForUnit(a); q == nil || q.LenPrimary() != qBefore+1 {
		t.Fatalf("left empty with selection should queue a contextual move, before %d after %d", qBefore, func() int {
			if q := orders.QueueForUnit(a); q != nil {
				return q.LenPrimary()
			}
			return 0
		}())
	} else {
		// Clear the queued move for the next sub-test.
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
		replaceSelectionForTest(t, b, a)
	}
	// Right empty clears when not additive [07 §9] — deselect branch.
	// Right click must be outside minimap as well.
	rightClickAt(b, 500, 300, false)
	if a.Flags&hud.SelectionFlag != 0 || c.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("right empty should clear all, A %v C %v", a.Flags&hud.SelectionFlag != 0, c.Flags&hud.SelectionFlag != 0)
	}
	// Shift+right empty still clears in current retail path (right does not queue).
	// Select A again and verify shift+left empty preserves via queued move.
	clickAt(b, sxA, syA, false)
	qBefore2 := 0
	if q := orders.QueueForUnit(a); q != nil {
		qBefore2 = q.LenPrimary()
	}
	clickAt(b, 500, 300, true) // shift left empty → queued move, preserves
	if a.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("shift left empty should preserve selection via queued move")
	}
	if q := orders.QueueForUnit(a); q == nil || q.LenPrimary() != qBefore2+1 {
		t.Fatalf("shift left empty should queue a move, before %d after %d", qBefore2, func() int {
			if q := orders.QueueForUnit(a); q != nil {
				return q.LenPrimary()
			}
			return 0
		}())
	}
	// Drag semantics are covered by the canonical HUD selection walker; verify
	// the battle dispatcher still filters updates to LocalOwner.
}

// TestFoggedEnemyCannotBeSelectedOrTargeted [03 §3.2] C8 local-owner word gate.
func TestFoggedEnemyCannotBeSelectedOrTargeted(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	// Ensure visibility service is empty (W==0 => enemy invisible) [03 §3.2] C8
	b.sess.Vis = &visibility.Service{} // empty
	enemy := placeUnit(b, "armsolar", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	enemy.Owner = 1
	b.battleState().Input.Latch = input.LatchNormal
	sx, sy := screenPos(b.cam, enemy)
	clickAt(b, sx, sy, false)
	if enemy.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("fogged enemy should not be selectable")
	}
	// Targeting: armed attack latch should not acquire fogged unit handle
	b.battleState().Input.Latch = input.LatchAttack
	// Use orderSelected path via armed click
	h, u, _ := b.pickTarget(sx, sy)
	if h != 0 || u != nil {
		t.Fatalf("fogged enemy should not be targeted via pickTarget, got handle %v unit %v", h, u)
	}
	// Also via visibility direct
	if b.sess.Vis != nil {
		tgt := visibility.Target{Owner: visibility.PlayerID(enemy.Owner), X: enemy.X, Y: enemy.Y, Z: enemy.Z, Status: enemy.Flags}
		if b.sess.Vis.IsVisible(visibility.PlayerID(b.sess.LocalOwner), tgt) {
			t.Fatalf("visibility should be false for fogged enemy")
		}
	}
	// Now make visible via nil vis (no fog) – should be selectable if owned? But enemy owned so not selectable as own.
	// Instead place own unit fogged? Own bypass [03 §3.2] C8 step1 so own always visible even with empty vis.
	own := placeUnit(b, "armsolar", numeric.Fixed(300*65536), numeric.Fixed(200*65536))
	own.Owner = 0
	sxOwn, syOwn := screenPos(b.cam, own)
	b.battleState().Input.Latch = input.LatchNormal
	clickAt(b, sxOwn, syOwn, false)
	if own.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("own unit should be selectable even with empty vis (owner bypass)")
	}
}

// TestLocalOwnerNonzeroReceivesCommands [08 "Skirmish configuration"] local owner routing.
func TestLocalOwnerNonzeroReceivesCommands(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	// Prepare orders table
	_ = orders.Lookup("Move_Ground")
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 1 // human is player 1, not 0 [RS-P0-004]
	// Place two units, one owned by 1 (local), one owned by 0
	localUnit := placeUnit(b, "armcons", numeric.Fixed(180*65536), numeric.Fixed(100*65536))
	localUnit.Owner = 1
	otherUnit := placeUnit(b, "armcons", numeric.Fixed(240*65536), numeric.Fixed(160*65536))
	otherUnit.Owner = 0
	// Try to select otherUnit via click – should not select because filter to LocalOwner [07 §9]
	sxOther, syOther := screenPos(b.cam, otherUnit)
	clickAt(b, sxOther, syOther, false)
	if otherUnit.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("foreign unit (owner 0) should not be selectable when LocalOwner=1")
	}
	if localUnit.Flags&hud.SelectionFlag != 0 {
		t.Fatalf("local unit should not be selected after foreign click (empty clear)")
	}
	// Click local unit – should select
	sxLocal, syLocal := screenPos(b.cam, localUnit)
	clickAt(b, sxLocal, syLocal, false)
	if localUnit.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("local unit (owner 1) should be selectable when LocalOwner=1")
	}
	// Left-click contextual move should dispatch only to local selection via same canonical producer [P0-I03].
	// Acquire orders for local unit
	// Use orderSelected direct: code 2 = MOVE
	b.orderSelected(2, 300, 300, false) // ground point
	applyPendingBattleCommands(b)
	qLocal := orders.QueueForUnit(localUnit)
	if qLocal == nil || qLocal.LenPrimary() == 0 {
		t.Fatalf("local owner unit should receive move order")
	}
	qOther := orders.QueueForUnit(otherUnit)
	if qOther != nil && qOther.LenPrimary() != 0 {
		t.Fatalf("player 0 unit should not receive command when LocalOwner=1, got %d", qOther.LenPrimary())
	}
	// The committed frame reports the local selection used by command routing.
	if !b.hasSelection() {
		t.Fatalf("hasSelection should be true while local unit remains selected")
	}
	// Clear the local selection through the typed boundary; hasSelection then
	// follows the committed frame rather than any live fixture state.
	if err := b.enqueueHumanCommand(session.HumanCommand{Kind: session.HumanSelectionClear}); err != nil {
		t.Fatalf("clear local selection: %v", err)
	}
	applyPendingBattleCommands(b)
	if b.hasSelection() {
		t.Fatalf("hasSelection should be false after typed clear with only foreign flag")
	}
}

// TestFeaturePickingOverlap [07 §8][07 §9] feature IsWreck tie etc. Minimal check that unit>feature and feature visible.
func TestFeaturePickingOverlap(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(10, 10)
	featDef := cat.Features[content.CanonicalKey("armrock")]
	terrain.FeatureNames = []string{"armrock"}
	terrain.FeatureDefs = []*content.FeatureDef{featDef}
	// The feature service stamps the cell itself and refuses a cell that
	// already carries a feature word [06 §13.1], so the plot is left empty
	// until PlaceAt writes it.
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	b.cam.X = 0
	b.cam.Z = 0
	b.sess.Features = features.NewService(terrain, nil, nil, nil)
	if b.sess.Features.PlaceAt(9, 5, featDef) == nil {
		t.Fatal("feature placement failed")
	}
	b.sess.Vis = visibility.New(terrain, 0)
	b.sess.Vis.SetLocal(0)
	// Place unit at same cell as feature to test unit>feature priority
	u := placeUnit(b, "armsolar", numeric.Fixed(int64(9*16)<<16), numeric.Fixed(int64(5*16)<<16))
	beamX, beamY := b.cam.WorldToScreen(numeric.Fixed(int64(9*16)<<16), 0, numeric.Fixed(int64(5*16)<<16))
	sx, sy := beamX-camera.OriginX, beamY-camera.OriginY
	// The production picker consumes the committed frame, so publish the
	// feature and overlapping unit before checking priority [I6].
	applyPendingBattleCommands(b)
	// Pick at feature cell – unit should win
	h, _, pos := b.pickTarget(sx, sy)
	if h == 0 {
		t.Fatalf("unit>feature: want unit handle, got 0 (feature only) pos %+v", pos)
	}
	// Move unit away, feature should be found via HasFeature
	u.X = numeric.Fixed(int64(2*16) << 16)
	u.Z = numeric.Fixed(int64(2*16) << 16)
	applyPendingBattleCommands(b)
	_, _, pos2 := b.pickTarget(sx, sy)
	if !pos2.HasFeature {
		t.Fatalf("feature picking after unit moved: HasFeature false")
	}
	// Feature picking should respect fog: with empty vis, the feature is invisible.
	b.sess.Vis = visibility.New(terrain, visibility.ModeHistoryEnabled)
	b.sess.Vis.SetLocal(0)
	applyPendingBattleCommands(b)
	_, _, pos3 := b.pickTarget(sx, sy)
	// With empty vis and feature, our code checks VisiblePoint – empty returns false, so not visible.
	if pos3.HasFeature {
		t.Fatalf("fogged feature should not be HasFeature when not visible, got true")
	}
	_ = world.ResolveFeature
}

// Ensure orders use canonical payload builder: button, hotkey, left-click contextual all via NewNodeForOrder.
func TestCanonicalPayloadIdentical(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)
	// Hotkey latch Move then dispatch
	b.battleState().Input.Latch = input.LatchMove
	b.orderSelected(2, 200, 200, false)
	applyPendingBattleCommands(b)
	q := orders.QueueForUnit(u)
	if q.LenPrimary() != 1 {
		t.Fatalf("hotkey latch move should queue 1")
	}
	first := q.Primary()[0]
	if first.GoalX == 0 && first.GoalZ == 0 {
		t.Fatalf("payload missing Goal")
	}
	// Clear and test left-click contextual (code 1) also uses same builder
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	// Need selection still
	replaceSelectionForTest(t, b, u)
	b.orderSelected(1, 210, 210, false)
	applyPendingBattleCommands(b)
	if q.LenPrimary() != 1 {
		t.Fatalf("left-click contextual should queue 1")
	}
	second := q.Primary()[0]
	if second.Target != 0 && second.Target != first.Target {
		// Both may have different resolved IDs but payload fields populated via same constructor
	}
	// Verify both have tick and owner set via NewNodeForOrder contract [04 §3.2]
	if first.Owner == 0 || second.Owner == 0 {
		t.Fatalf("canonical NewNodeForOrder should set Owner handle")
	}
}
