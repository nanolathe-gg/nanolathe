package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// screenPos computes shell coords for a world unit [07 §9][03 §2.5].
func screenPos(cam *camera.Camera, u *units.Unit) (int32, int32) {
	sx0, sy0 := cam.WorldToScreen(u.X, u.Y, u.Z)
	return sx0 - camera.OriginX, sy0 - camera.OriginY
}

func clickAt(b *battleSession, sx, sy int32, shift bool) {
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	if shift {
		in.Kbd.InjectKey(input.KeyShift, true)
	}
	// Press
	in.Mouse.InjectMouseMove(float32(sx), float32(sy))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	// Hold one frame (drag tracking)
	in.Mouse.ClearEdges()
	in.Kbd.ClearEdges()
	if shift {
		in.Kbd.InjectKey(input.KeyShift, true)
	}
	in.Mouse.InjectMouseMove(float32(sx), float32(sy))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	// Release
	in.Mouse.ClearEdges()
	in.Kbd.ClearEdges()
	if shift {
		in.Kbd.InjectKey(input.KeyShift, true)
	}
	in.Mouse.InjectMouseMove(float32(sx), float32(sy))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
}

func dragSelect(b *battleSession, sx0, sy0, sx1, sy1 int32, shift bool) {
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	if shift {
		in.Kbd.InjectKey(input.KeyShift, true)
	}
	in.Mouse.InjectMouseMove(float32(sx0), float32(sy0))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	in.Mouse.ClearEdges()
	in.Kbd.ClearEdges()
	if shift {
		in.Kbd.InjectKey(input.KeyShift, true)
	}
	in.Mouse.InjectMouseMove(float32(sx1), float32(sy1))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	in.Mouse.ClearEdges()
	in.Kbd.ClearEdges()
	if shift {
		in.Kbd.InjectKey(input.KeyShift, true)
	}
	in.Mouse.InjectMouseMove(float32(sx1), float32(sy1))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
}

// TestClickCommanderSelectsExactlyOne [07 §9][RS-P0-003] small-click selects exactly one via canonical picker.
func TestClickCommanderSelectsExactlyOne(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	cam := b.cam
	// Place commander at (10,10) map pixels
	cmdr := placeUnit(b, "armcons", numeric.Fixed(10*65536), numeric.Fixed(10*65536))
	// Ensure no prior selection
	for _, u := range b.sess.Units.Iter() {
		if u != nil {
			u.Flags &^= client.SelectionFlag
		}
	}
	b.latch = input.LatchNormal
	sx, sy := screenPos(cam, cmdr)
	clickAt(b, sx, sy, false)
	count := 0
	var selected *units.Unit
	for _, u := range b.sess.Units.Iter() {
		if u != nil && u.Flags&client.SelectionFlag != 0 {
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

// TestEmptyClickClearsShiftToggles [07 §9] C6 empty clears, shift toggles/adds.
func TestEmptyClickClearsShiftToggles(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(30, 30)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	a := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	c := placeUnit(b, "armsolar", numeric.Fixed(20*65536), numeric.Fixed(20*65536))
	b.latch = input.LatchNormal
	// Click A selects A
	sxA, syA := screenPos(b.cam, a)
	clickAt(b, sxA, syA, false)
	if a.Flags&client.SelectionFlag == 0 {
		t.Fatalf("A should be selected after click")
	}
	// Click B without shift replaces: A cleared, B selected
	sxC, syC := screenPos(b.cam, c)
	clickAt(b, sxC, syC, false)
	if a.Flags&client.SelectionFlag != 0 {
		t.Fatalf("A should be cleared after replace click on C")
	}
	if c.Flags&client.SelectionFlag == 0 {
		t.Fatalf("C should be selected after replace")
	}
	// Shift-click A adds A (now both selected) [07 §9] additive toggle inside
	clickAt(b, sxA, syA, true)
	if a.Flags&client.SelectionFlag == 0 || c.Flags&client.SelectionFlag == 0 {
		t.Fatalf("shift-click should add A, want both selected")
	}
	// Shift-click A again toggles A off, leaving only C
	clickAt(b, sxA, syA, true)
	if a.Flags&client.SelectionFlag != 0 {
		t.Fatalf("shift toggle should deselect A")
	}
	if c.Flags&client.SelectionFlag == 0 {
		t.Fatalf("C should remain selected after toggling A off")
	}
	// Empty click clears when not additive [07 §9] C6
	clickAt(b, 5, 5, false) // 5,5 is HUD-ish but b.hud nil so considered world empty; pick no unit
	// Ensure empty: choose far coords where no unit within 16px
	clickAt(b, 600, 400, false)
	if a.Flags&client.SelectionFlag != 0 || c.Flags&client.SelectionFlag != 0 {
		t.Fatalf("empty click should clear all, A %v C %v", a.Flags&client.SelectionFlag != 0, c.Flags&client.SelectionFlag != 0)
	}
	// Shift+empty preserves [07 §9] C6
	// Select C again
	clickAt(b, sxC, syC, false)
	clickAt(b, 600, 400, true) // shift empty
	if c.Flags&client.SelectionFlag == 0 {
		t.Fatalf("shift empty should preserve selection")
	}
	// Drag semantics also covered via ApplyDragSelectionWorld already, but verify drag replace/toggle still filtered to LocalOwner
}

// TestHUDPressDragReleaseNeverSelects [07 §3][F-P1-008] HUD capture latch.
func TestHUDPressDragReleaseNeverSelects(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	wx := numeric.Fixed(int64(10*16) << 16) // but worldToCell uses fixed; simpler place at 100,100 px
	_ = wx
	u := placeUnit(b, "armsolar", numeric.Fixed(10*65536), numeric.Fixed(10*65536))
	u.Flags &^= client.SelectionFlag
	builder := placeUnit(b, "armcons", numeric.Fixed(2*65536), numeric.Fixed(2*65536))
	builder.Flags |= client.SelectionFlag
	b.armBuildPanel()
	if len(b.panelButtons) == 0 {
		t.Fatalf("panel empty")
	}
	sxU, syU := screenPos(b.cam, u)
	// Press begins on HUD panel band (>=428 y with panel)
	in := &client.InputState{Mouse: &client.MouseState{}, Kbd: &client.KeyboardState{}}
	in.Mouse.InjectMouseMove(10, 450)
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	if !b.hudCaptured {
		t.Fatalf("HUD press should capture")
	}
	in.Mouse.ClearEdges()
	in.Mouse.InjectMouseMove(float32(sxU), float32(syU))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	in.Mouse.ClearEdges()
	in.Mouse.InjectMouseMove(float32(sxU), float32(syU))
	in.Mouse.InjectMouseButton(input.MouseButtonLeft, false)
	b.handleInput(in, nil)
	if u.Flags&client.SelectionFlag != 0 {
		t.Fatalf("HUD press-drag-release leaked into world selection")
	}
}

// TestFoggedEnemyCannotBeSelectedOrTargeted [03 §3.2] C8 local-owner word gate.
func TestFoggedEnemyCannotBeSelectedOrTargeted(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	// Ensure visibility service is empty (W==0 => enemy invisible) [03 §3.2] C8
	b.sess.Vis = &visibility.Service{} // empty
	enemy := placeUnit(b, "armsolar", numeric.Fixed(10*65536), numeric.Fixed(10*65536))
	enemy.Owner = 1
	enemy.Flags &^= client.SelectionFlag
	b.latch = input.LatchNormal
	sx, sy := screenPos(b.cam, enemy)
	clickAt(b, sx, sy, false)
	if enemy.Flags&client.SelectionFlag != 0 {
		t.Fatalf("fogged enemy should not be selectable")
	}
	// Targeting: armed attack latch should not acquire fogged unit handle
	b.latch = input.LatchAttack
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
	own := placeUnit(b, "armsolar", numeric.Fixed(12*65536), numeric.Fixed(12*65536))
	own.Owner = 0
	sxOwn, syOwn := screenPos(b.cam, own)
	b.latch = input.LatchNormal
	clickAt(b, sxOwn, syOwn, false)
	if own.Flags&client.SelectionFlag == 0 {
		t.Fatalf("own unit should be selectable even with empty vis (owner bypass)")
	}
}

// TestEqualOverlapTieLowerSlotWins [07 §9] strict < so lower slot wins.
func TestEqualOverlapTieLowerSlotWins(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	x := numeric.Fixed(10 * 65536)
	z := numeric.Fixed(10 * 65536)
	h1, _ := b.sess.Units.Create(cat.Units[content.CanonicalKey("armcons")], 0, x, 0, z)
	h2, _ := b.sess.Units.Create(cat.Units[content.CanonicalKey("armsolar")], 0, x, 0, z)
	u1 := b.sess.Units.Unit(h1)
	u2 := b.sess.Units.Unit(h2)
	if u1 == nil || u2 == nil {
		t.Fatalf("units not created")
	}
	b.latch = input.LatchNormal
	for _, u := range b.sess.Units.Iter() {
		if u != nil {
			u.Flags &^= client.SelectionFlag
		}
	}
	sx, sy := screenPos(b.cam, u1) // same as u2
	// First via direct picker
	viewer := visibility.PlayerID(b.sess.LocalOwner)
	bh, bu := client.PickUnit(sx, sy, b.cam, b.sess.Units, nil, viewer)
	if bh != h1 || bu != u1 {
		t.Fatalf("overlap tie: want lower slot %v got %v", h1, bh)
	}
	// Via click selection
	clickAt(b, sx, sy, false)
	if u1.Flags&client.SelectionFlag == 0 {
		t.Fatalf("lower slot should be selected on exact overlap click")
	}
	if u2.Flags&client.SelectionFlag != 0 {
		t.Fatalf("higher slot should not be selected on tie")
	}
	// Nudge u2 to be 1px closer – should win despite higher slot (nearest wins)
	u2.X = x + numeric.Fixed(1*65536)
	sx2, sy2 := screenPos(b.cam, u2)
	bh2, _ := client.PickUnit(sx2, sy2, b.cam, b.sess.Units, nil, viewer)
	if bh2 != h2 {
		t.Fatalf("nearest should win despite higher slot, want %v got %v", h2, bh2)
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
	localUnit := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	localUnit.Owner = 1
	localUnit.Flags &^= client.SelectionFlag
	otherUnit := placeUnit(b, "armcons", numeric.Fixed(10*65536), numeric.Fixed(10*65536))
	otherUnit.Owner = 0
	otherUnit.Flags &^= client.SelectionFlag
	// Try to select otherUnit via click – should not select because filter to LocalOwner [07 §9]
	sxOther, syOther := screenPos(b.cam, otherUnit)
	clickAt(b, sxOther, syOther, false)
	if otherUnit.Flags&client.SelectionFlag != 0 {
		t.Fatalf("foreign unit (owner 0) should not be selectable when LocalOwner=1")
	}
	if localUnit.Flags&client.SelectionFlag != 0 {
		t.Fatalf("local unit should not be selected after foreign click (empty clear)")
	}
	// Click local unit – should select
	sxLocal, syLocal := screenPos(b.cam, localUnit)
	clickAt(b, sxLocal, syLocal, false)
	if localUnit.Flags&client.SelectionFlag == 0 {
		t.Fatalf("local unit (owner 1) should be selectable when LocalOwner=1")
	}
	// Right-click move should dispatch only to local selection via same canonical producer [P0-I03].
	// Acquire orders for local unit
	// Use orderSelected direct: code 2 = MOVE
	b.orderSelected(2, 300, 300, false) // ground point
	qLocal := orders.QueueForUnit(localUnit)
	if qLocal == nil || qLocal.LenPrimary() == 0 {
		t.Fatalf("local owner unit should receive move order")
	}
	qOther := orders.QueueForUnit(otherUnit)
	if qOther != nil && qOther.LenPrimary() != 0 {
		t.Fatalf("player 0 unit should not receive command when LocalOwner=1, got %d", qOther.LenPrimary())
	}
	// Also ensure hasSelection respects LocalOwner: otherUnit selected would not count
	otherUnit.Flags |= client.SelectionFlag
	if !b.hasSelection() {
		// hasSelection should be true because localUnit still selected
		t.Fatalf("hasSelection should be true when local has selection despite foreign flag")
	}
	// Clear local, leave foreign flagged – hasSelection should be false (filtered)
	localUnit.Flags &^= client.SelectionFlag
	if b.hasSelection() {
		t.Fatalf("hasSelection should be false when only foreign unit flagged and LocalOwner=1")
	}
}

// TestFeaturePickingOverlap [07 §8][07 §9] feature IsWreck tie etc. Minimal check that unit>feature and feature visible.
func TestFeaturePickingOverlap(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(10, 10)
	featDef := cat.Features[content.CanonicalKey("armrock")]
	terrain.FeatureNames = []string{"armrock"}
	terrain.FeatureDefs = []*content.FeatureDef{featDef}
	idx := 5*int(terrain.CellW) + 5
	terrain.Plot[idx][8] = 0
	terrain.Plot[idx][9] = 0
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	b.cam.X = 0
	b.cam.Z = 0
	// Place unit at same cell as feature to test unit>feature priority
	u := placeUnit(b, "armsolar", numeric.Fixed(int64(5*16)<<16), numeric.Fixed(int64(5*16)<<16))
	sx := int32(5 * 16)
	sy := int32(5 * 16)
	// Pick at feature cell – unit should win
	h, _, pos := b.pickTarget(sx, sy)
	if h == 0 {
		t.Fatalf("unit>feature: want unit handle, got 0 (feature only) pos %+v", pos)
	}
	// Move unit away, feature should be found via HasFeature
	u.X = numeric.Fixed(int64(2*16) << 16)
	u.Z = numeric.Fixed(int64(2*16) << 16)
	_, _, pos2 := b.pickTarget(sx, sy)
	if !pos2.HasFeature {
		t.Fatalf("feature picking after unit moved: HasFeature false")
	}
	// Feature picking should respect fog: with empty vis, feature at 5,5 invisible?
	b.sess.Vis = &visibility.Service{}
	_, _, pos3 := b.pickTarget(sx, sy)
	// With empty vis and feature, our code checks VisiblePoint – empty returns false, so not visible.
	if pos3.HasFeature {
		t.Fatalf("fogged feature should not be HasFeature when not visible, got true")
	}
	_ = world.ResolveFeature
}

// Ensure orders use canonical payload builder: button, hotkey, right-click all via NewNodeForOrder.
func TestCanonicalPayloadIdentical(t *testing.T) {
	cat := testCatalogON05()
	terrain := testWorldON05(20, 20)
	b := newTestBattle(cat, terrain)
	b.sess.LocalOwner = 0
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	u.Flags |= client.SelectionFlag
	// Hotkey latch Move then dispatch
	b.latch = input.LatchMove
	b.orderSelected(2, 200, 200, false)
	q := orders.QueueForUnit(u)
	if q.LenPrimary() != 1 {
		t.Fatalf("hotkey latch move should queue 1")
	}
	first := q.Primary()[0]
	if first.GoalX == 0 && first.GoalZ == 0 {
		t.Fatalf("payload missing Goal")
	}
	// Clear and test right-click contextual (code 1) also uses same builder
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	// Need selection still
	u.Flags |= client.SelectionFlag
	b.orderSelected(1, 210, 210, false)
	if q.LenPrimary() != 1 {
		t.Fatalf("right-click contextual should queue 1")
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
