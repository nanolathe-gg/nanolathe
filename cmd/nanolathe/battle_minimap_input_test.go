package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// withMinimap gives a fixture battle the rail's fixed 126-pixel radar canvas,
// which the production HUD installs at the framebuffer origin [07 §6][07 §10].
func withMinimap(b *battleSession) hud.Rect {
	dst := hud.Rect{X1: 0, Y1: 0, X2: camera.MinimapLongSide - 1, Y2: camera.MinimapLongSide - 1}
	b.hud = &retailBattleHUD{minimapAnchor: dst, minimapAnchorOK: true}
	return dst
}

// TestMinimapLeftClickIssuesAnOrderAtTheLensPoint locks the default
// `Interface Type 0` polarity: left down over the minimap issues the armed
// order at the minimap's world point [07 R-CAM-01 §5]. The point is the lens
// conversion with no half-viewport term [07 R-CAM-01 §11], and the order goes
// through orderSelected — the one order producer — not a second path.
func TestMinimapLeftClickIssuesAnOrderAtTheLensPoint(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	dst := withMinimap(b)
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)
	b.battleState().Input.Latch = input.LatchMove

	mx, my := int32(70), int32(50)
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		t.Fatal("fixture world published no play area")
	}
	layout, _, ok := b.minimapLayout()
	if !ok {
		t.Fatal("fixture HUD published no minimap layout")
	}
	wantX, wantZ, ok := client.MinimapPointerWorld(layout, dst, playW, playH, mx, my)
	if !ok {
		t.Fatal("fixture pointer did not classify as minimap")
	}

	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = float32(mx), float32(my)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)

	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder {
		t.Fatalf("minimap left click queued %+v, want exactly one HumanOrder", pending)
	}
	got := pending[0].Order
	if got.Code != hud.LatchToCode(input.LatchMove) {
		t.Fatalf("minimap order code = %d, want the armed Move code %d", got.Code, hud.LatchToCode(input.LatchMove))
	}
	if int32(got.Position.X>>16) != wantX || int32(got.Position.Z>>16) != wantZ {
		t.Fatalf("minimap order landed at %d,%d, want the lens point %d,%d",
			got.Position.X>>16, got.Position.Z>>16, wantX, wantZ)
	}
	// The armed latch retires after dispatch, as it does for a world click.
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("latch = %v after an unshifted minimap dispatch, want Normal", b.battleState().Input.Latch)
	}
	// And the camera did not move: the left button is the order button here.
	if b.cam.X != 0 || b.cam.Z != 0 {
		t.Fatalf("minimap left click moved the camera to %d,%d", b.cam.X, b.cam.Z)
	}
}

// A radar contact has a minimap blip even when the viewport cannot draw its
// unit. The minimap's own hover list still resolves that unit for an armed
// attack [03 §3.9][07 R-SEL-02B2][07 R-CAM-01 §14].
func TestMinimapAttackTargetsRadarOnlyContact(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	dst := withMinimap(b)
	const mx, my int32 = 70, 50
	layout, _, _ := b.minimapLayout()
	playW, playH, _ := b.sess.PlayArea()
	wx, wz, ok := client.MinimapPointerWorld(layout, dst, playW, playH, mx, my)
	if !ok {
		t.Fatal("fixture pointer did not classify as minimap")
	}
	target := placeUnit(b, "armcons", numeric.Fixed(wx)<<16, numeric.Fixed(wz)<<16)
	target.Owner = 1
	actor := placeUnit(b, "armcons", numeric.Fixed(8<<16), numeric.Fixed(8<<16))
	actor.Def.CanAttack = true
	replaceSelectionForTest(t, b, actor)
	cur, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("fixture did not publish a snapshot")
	}
	written := b.sess.Snapshot.BeginWrite()
	*written = *cur
	written.Units = append([]frame.UnitView(nil), cur.Units...)
	written.Radar.Contacts = append([]frame.RadarContactView(nil), cur.Radar.Contacts...)
	written.Radar.MappingLOS = 1
	written.Radar.BlinkPhase = 1
	unitFound, contactFound := false, false
	for i := range written.Units {
		if written.Units[i].Slot == target.Handle {
			written.Units[i].DirectVisibilityKnown = true
			written.Units[i].DirectlyVisible = false
			unitFound = true
		}
	}
	for i := range written.Radar.Contacts {
		if written.Radar.Contacts[i].Handle == target.Handle {
			written.Radar.Contacts[i].Status |= visibility.SeenBit
			written.Radar.Contacts[i].Seen = true
			written.Radar.Contacts[i].Visible = true
			contactFound = true
		}
	}
	if !unitFound || !contactFound {
		t.Fatal("fixture did not publish the target unit and radar contact")
	}
	if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
		t.Fatal(err)
	}
	cur, _ = b.currentSnapshot()
	view, _ := snapshotUnitByHandle(cur, target.Handle)
	if client.SnapshotVisible(cur, view, cur.ViewingPlayer) {
		t.Fatal("radar-only target became directly visible in the viewport")
	}
	if got := b.minimapHoverUnit(cur, mx, my); got != target.Handle {
		t.Fatalf("radar-only minimap hover = %d, want target %d", got, target.Handle)
	}
	b.battleState().Input.Latch = input.LatchAttack
	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = float32(mx), float32(my)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != hud.LatchToCode(input.LatchAttack) || pending[0].Order.Target != target.Handle {
		t.Fatalf("radar-only minimap attack queued %+v, want target %d", pending, target.Handle)
	}
}

// TestMinimapShiftLeftClickQueues locks the Shift modifier reaching the queued
// flag through the same producer [07 §9][P0-I14].
func TestMinimapShiftLeftClickQueues(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	withMinimap(b)
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)
	b.battleState().Input.Latch = input.LatchMove

	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = 70, 50
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	in.Kbd.SetKey(input.KeyShift, true)
	b.handleInput(in, nil)

	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || !pending[0].Order.Queued {
		t.Fatalf("shifted minimap click queued %+v, want one queued order", pending)
	}
}

// TestMinimapCameraCaptureBeginsOnTheNextHostFrame locks the minimap latch:
// a qualifying down edge captures the camera gesture, but service happens
// before fresh clicks on the following frame [07 R-CAM-01 §5][07 R-CAM-01 §11].
func TestMinimapCameraCaptureBeginsOnTheNextHostFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	dst := withMinimap(b)
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)

	mx, my := int32(70), int32(50)
	playW, playH, _ := b.sess.PlayArea()
	layout, _, _ := b.minimapLayout()
	wx, wz, ok := client.MinimapPointerWorld(layout, dst, playW, playH, mx, my)
	if !ok {
		t.Fatal("fixture pointer did not classify as minimap")
	}
	want := &camera.Camera{ViewW: b.cam.ViewW, ViewH: b.cam.ViewH, MapW: b.cam.MapW, MapH: b.cam.MapH}
	want.JumpToBattleViewCenter(wx, wz)

	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = float32(mx), float32(my)
	in.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(in, nil)
	if !b.minimapCameraCaptured {
		t.Fatal("qualifying right down did not capture the minimap camera")
	}
	if b.cam.X != 0 || b.cam.Z != 0 {
		t.Fatalf("press frame moved camera to %d,%d; latch must first service next frame", b.cam.X, b.cam.Z)
	}
	b.cam.SetTracked(u.Handle)
	in.Mouse.ResetEdges()
	b.handleInput(in, nil)

	if b.cam.X != want.X || b.cam.Z != want.Z {
		t.Fatalf("captured minimap service put the camera at %d,%d, want the recentred %d,%d (world point %d,%d)",
			b.cam.X, b.cam.Z, want.X, want.Z, wx, wz)
	}
	if b.cam.X == wx && b.cam.Z == wz {
		t.Fatal("camera origin equals the clicked point: the half-viewport recenter is missing")
	}
	if b.cam.Tracked() != 0 {
		t.Fatal("minimap camera jump did not cancel follow")
	}
	if q := orders.QueueForUnit(u); q != nil && q.LenPrimary() != 0 {
		t.Fatal("the minimap latch button issued an order")
	}
}

func TestMinimapCameraCaptureIsAdmittedOnlyOnDownAndSurvivesTheRadarEdge(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	withMinimap(b)
	// A held button that entered the radar with no down edge cannot acquire.
	held := input.NewState()
	held.Mouse.X, held.Mouse.Y = 70, 50
	held.Mouse.SetButton(input.MouseButtonRight, true)
	held.Mouse.ResetEdges()
	b.handleInput(held, nil)
	if b.minimapCameraCaptured || b.cam.X != 0 || b.cam.Z != 0 {
		t.Fatalf("held-only radar entry acquired capture=%v camera=%d,%d", b.minimapCameraCaptured, b.cam.X, b.cam.Z)
	}

	press := input.NewState()
	press.Mouse.X, press.Mouse.Y = 70, 50
	press.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(press, nil)
	press.Mouse.ResetEdges()
	// The saved capture keeps servicing after the pointer leaves the canvas.
	press.Mouse.SetPosition(400, 400)
	b.handleInput(press, nil)
	if !b.minimapCameraCaptured {
		t.Fatal("captured drag released merely by leaving the radar")
	}
	if b.cam.X == 0 && b.cam.Z == 0 {
		t.Fatal("captured outside-radar pointer did not run the camera jump")
	}
	// An unrelated release does not clear a right-button capture.
	press.Mouse.SetButton(input.MouseButtonLeft, true)
	press.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleInput(press, nil)
	if !b.minimapCameraCaptured {
		t.Fatal("left release cleared a right-button minimap capture")
	}
	press.Mouse.ResetEdges()
	press.Mouse.SetButton(input.MouseButtonRight, false)
	b.handleInput(press, nil)
	if b.minimapCameraCaptured {
		t.Fatal("matching right release did not clear minimap capture")
	}
}

func TestMinimapCameraCaptureNeedsMatchingUpAndServicesThatReleaseFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	withMinimap(b)
	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = 70, 50
	in.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(in, nil) // capture only

	// I02's polling boundary can provide no held bit without a matching queued
	// up. That is not permission to release this captured gesture.
	gap := input.NewState()
	gap.Mouse.X, gap.Mouse.Y = 70, 50
	b.handleInput(gap, nil)
	if !b.minimapCameraCaptured || (b.cam.X == 0 && b.cam.Z == 0) {
		t.Fatalf("missing-held sample ended or skipped captured service: capture=%v camera=%d,%d", b.minimapCameraCaptured, b.cam.X, b.cam.Z)
	}
	priorX, priorZ := b.cam.X, b.cam.Z
	layout, dst, ok := b.minimapLayout()
	if !ok {
		t.Fatal("fixture HUD published no minimap layout")
	}
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		t.Fatal("fixture world published no play area")
	}
	releaseIntent, ok := client.MinimapCameraCaptureIntent(layout, dst, playW, playH, 110, 90)
	if !ok {
		t.Fatal("release pointer did not yield a minimap camera intent")
	}
	want := *b.cam
	want.JumpToBattleViewCenter(releaseIntent.X, releaseIntent.Z)
	if priorX == want.X && priorZ == want.Z {
		t.Fatalf("fixture release point leaves camera at prior %d,%d", priorX, priorZ)
	}

	// A matching up edge is handled after the already-set latch's service, so
	// its release frame still applies the pointer record's camera jump.
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(110, 90)
	in.Mouse.SetButton(input.MouseButtonRight, false)
	b.handleInput(in, nil)
	if b.minimapCameraCaptured {
		t.Fatal("matching up did not clear captured latch")
	}
	if b.cam.X != want.X || b.cam.Z != want.Z {
		t.Fatalf("release frame camera = %d,%d, want release-point jump %d,%d (prior %d,%d)", b.cam.X, b.cam.Z, want.X, want.Z, priorX, priorZ)
	}
}

func TestMinimapUsesPersistedAlternatePolarity(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	withMinimap(b)
	b.shell = &gameShell{interfaceType: settings.InterfaceTypeRightClick}
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)

	// Type 1 swaps the *idle* minimap roles: left down captures the camera and
	// does not move it until the following host frame. An armed order still
	// owns left, as verified separately below [07 R-CAM-01 §5].
	left := input.NewState()
	left.Mouse.X, left.Mouse.Y = 70, 50
	left.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(left, nil)
	if !b.minimapCameraCaptured || b.minimapCameraCaptureButton != input.MouseButtonLeft {
		t.Fatal("Interface Type 1 left down did not acquire the minimap camera")
	}
	left.Mouse.ResetEdges()
	b.handleInput(left, nil)
	if b.cam.X == 0 && b.cam.Z == 0 {
		t.Fatal("Interface Type 1 minimap capture did not service on its next frame")
	}
	left.Mouse.ResetEdges()
	left.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleInput(left, nil)

	// Once armed, Type 1's left button continues to reach the order producer;
	// its idle camera capture cannot steal this action.
	b.battleState().Input.Latch = input.LatchMove
	armedLeft := input.NewState()
	armedLeft.Mouse.X, armedLeft.Mouse.Y = 70, 50
	armedLeft.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(armedLeft, nil)
	if pending := b.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanOrder {
		t.Fatalf("Interface Type 1 armed left minimap click queued %+v, want one HumanOrder", pending)
	}
}

func TestMinimapCursorUsesTheSameUsableRegionAsOrders(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	dst := withMinimap(b)
	playW, playH, _ := b.sess.PlayArea()
	layout, _, _ := b.minimapLayout()
	mx, my := int32(70), int32(50)
	wx, wz, ok := client.MinimapPointerWorld(layout, dst, playW, playH, mx, my)
	if !ok {
		t.Fatal("fixture radar point did not classify")
	}
	// An own finished unit at the radar dot is the same target the click path
	// sees, and an empty selection exposes the select cursor through the
	// production chooser [07 R-HUD-03 §1][07 §8].
	placeUnit(b, "armcons", numeric.Fixed(wx<<16), numeric.Fixed(wz<<16))
	applyPendingBattleCommands(b) // publish the committed minimap dot
	if got := b.classifyPointer(mx, my); got != battlePointerMinimap {
		t.Fatalf("radar point classification = %v, want minimap", got)
	}
	_, target, _ := b.pickTarget(mx, my)
	if target == nil {
		t.Fatal("minimap cursor path did not receive the minimap hover target")
	}
	if got := hud.ChooseCursor(input.LatchNormal, hud.CursorSelection{Viewer: b.sess.LocalOwner, Hostile: b.hostile}, hud.CursorHover{OverWorld: true, Target: target}); got != render.CursorSelect {
		t.Fatalf("minimap own hover cursor = %d, want cursorselect %d", got, render.CursorSelect)
	}
	// A build preview is not recomputed over the minimap: its retained in-view
	// verdict feeds the cursor just as it feeds the click branch.
	b.battleState().Input.BuildDef = "armcons"
	b.battleState().Input.BuildOK = true
	if got := hud.ChooseCursor(input.LatchMobileBuild, hud.CursorSelection{}, hud.CursorHover{OverWorld: b.classifyPointer(mx, my) != battlePointerChrome, Placing: true, PlacementValid: b.battleState().Input.BuildOK}); got != render.CursorFindSite {
		t.Fatalf("valid retained minimap build verdict cursor = %d, want %d", got, render.CursorFindSite)
	}
	// The letterbox bar remains inert chrome for both order and cursor paths.
	tall := newTestBattle(testCatalogON05(), testWorldON05(20, 60))
	withMinimap(tall)
	if got := tall.classifyPointer(0, 60); got != battlePointerChrome {
		t.Fatalf("letterbox classification = %v, want chrome", got)
	}
}

func minimapCursorClient(t *testing.T, b *battleSession) (*client.Client, func()) {
	t.Helper()
	cs, err := openContent(Options{Root: probeRetail(t)})
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := client.LoadCursors(cs.fs)
	if err != nil {
		cs.Close()
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		cs.Close()
		t.Fatal(err)
	}
	cl.SetCursors(cursors)
	return cl, func() { cs.Close() }
}

func TestMinimapCursorProductionConsumerAndCoveredModal(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	dst := withMinimap(b)
	playW, playH, _ := b.sess.PlayArea()
	layout, _, _ := b.minimapLayout()
	mx, my := int32(70), int32(50)
	wx, wz, ok := client.MinimapPointerWorld(layout, dst, playW, playH, mx, my)
	if !ok {
		t.Fatal("fixture radar point did not classify")
	}
	targetUnit := placeUnit(b, "armcons", numeric.Fixed(wx<<16), numeric.Fixed(wz<<16))
	actor := placeUnit(b, "armcons", numeric.Fixed(8<<16), numeric.Fixed(8<<16))
	actor.Def.CanAttack = true
	applyPendingBattleCommands(b)
	cl, closeContent := minimapCursorClient(t, b)
	defer closeContent()
	setPointer := func(x, y int32) {
		cl.Input().Mouse.SetPosition(float32(x), float32(y))
		b.updateCursor(cl)
	}

	// This calls updateCursor and the loaded software-cursor setter, not the
	// chooser directly. An own eligible minimap dot is inspectable.
	setPointer(mx, my)
	if got := cl.Cursors().Index(); got != render.CursorSelect {
		t.Fatalf("own minimap dot installed cursor %d, want cursorselect %d", got, render.CursorSelect)
	}

	// With an acting mover selected, the same minimap target drives the move
	// shape through updateCursor and the loaded cursor setter.
	replaceSelectionForTest(t, b, actor)
	b.battleState().Input.Latch = input.LatchMove
	setPointer(mx, my)
	if got := cl.Cursors().Index(); got != render.CursorMove {
		t.Fatalf("minimap move cursor = %d, want %d", got, render.CursorMove)
	}

	// A hostile dot reaches the production hover consumer. With the latch idle,
	// an attack-capable selection must produce the contextual attack answer,
	// which distinguishes a target-dependent verdict from inert HUD chrome.
	cur, ok := b.currentSnapshot()
	if !ok || len(cur.Units) == 0 {
		t.Fatal("fixture did not publish a minimap unit")
	}
	written := b.sess.Snapshot.BeginWrite()
	*written = *cur
	written.Units = append([]frame.UnitView(nil), cur.Units...)
	// The foreign target remains visible through the committed presentation
	// mask. The minimap consumer must not treat the ownership change itself as
	// a fog result before it reaches the shared cursor/order predicate.
	written.Visibility = frame.VisibilityView{W: 32, H: 32, Valid: true, CoverageBytes: true, Visible: make([]uint8, 32*32)}
	for i := range written.Visibility.Visible {
		written.Visibility.Visible[i] = 1
	}
	for i := range written.Units {
		if written.Units[i].Slot == targetUnit.Handle {
			written.Units[i].Owner = 1
			break
		}
	}
	if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
		t.Fatal(err)
	}
	updated, ok := b.currentSnapshot()
	if !ok {
		t.Fatal("hostile fixture did not publish")
	}
	view, found := snapshotUnitByHandle(updated, targetUnit.Handle)
	if !found || view.Owner != 1 {
		t.Fatalf("hostile fixture target = %+v, want owner 1", view)
	}
	_, hoverTarget, _ := b.pickTarget(mx, my)
	if hoverTarget == nil || hoverTarget.Owner != 1 {
		t.Fatalf("hostile fixture hover target = %+v, want owner 1", hoverTarget)
	}
	b.battleState().Input.Latch = input.LatchNormal
	setPointer(mx, my)
	if got := cl.Cursors().Index(); got != render.CursorAttack {
		t.Fatalf("hostile minimap dot installed cursor %d, want attack %d", got, render.CursorAttack)
	}
	b.minimapClickOrder(cl, mx, my, false)
	if pending := b.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 1 || pending[0].Order.Target != targetUnit.Handle {
		t.Fatalf("hostile minimap click queued %+v, want contextual attack at target %d", pending, targetUnit.Handle)
	}

	// Placement over the minimap reads the existing in-view verdict and does
	// not run updatePlacement there. Both cursor outcomes reach the real setter.
	b.battleState().Input.BuildDef = "armcons"
	b.battleState().Input.Latch = input.LatchMobileBuild
	b.battleState().Input.BuildOK = true
	setPointer(mx, my)
	if got := cl.Cursors().Index(); got != render.CursorFindSite {
		t.Fatalf("valid retained minimap build verdict installed cursor %d, want %d", got, render.CursorFindSite)
	}
	b.battleState().Input.BuildOK = false
	setPointer(mx, my)
	if got := cl.Cursors().Index(); got != render.CursorTooFar {
		t.Fatalf("invalid retained minimap build verdict installed cursor %d, want %d", got, render.CursorTooFar)
	}

	b.disarmPlacement()
	setPointer(128, 200) // rail chrome, outside the radar canvas
	if got := cl.Cursors().Index(); got != render.CursorNormal {
		t.Fatalf("inert chrome installed cursor %d, want normal %d", got, render.CursorNormal)
	}

	oldInfo := unitInfoUI
	unitInfoUI = &unitInfoScreen{window: &gui.Window{Rect: gui.Rect{X: 0, Y: 0, W: camera.MinimapLongSide, H: camera.MinimapLongSide}}}
	defer func() { unitInfoUI = oldInfo }()
	setPointer(mx, my)
	if got := cl.Cursors().Index(); got != render.CursorNormal {
		t.Fatalf("modal-covered minimap dot installed cursor %d, want normal %d", got, render.CursorNormal)
	}
}

// TestMinimapClickOnTheLetterboxBarDoesNothing keeps the interaction region
// and the fitted radar rectangle distinct: the canvas captures the pointer,
// but only the fitted rectangle selects the lens [07 §10].
func TestMinimapClickOnTheLetterboxBarDoesNothing(t *testing.T) {
	// A tall map letterboxes horizontally, so the canvas's left column is bar.
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 60))
	withMinimap(b)
	u := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, u)
	layout, _, ok := b.minimapLayout()
	if !ok || layout.PadX <= 0 {
		t.Skipf("fixture layout is not letterboxed horizontally: %+v", layout)
	}
	b.battleState().Input.Latch = input.LatchMove

	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = 0, 60
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(in, nil)

	if pending := b.sess.PendingHumanCommands(); len(pending) != 0 {
		t.Fatalf("a click on the letterbox bar queued %+v", pending)
	}
	if b.cam.X != 0 || b.cam.Z != 0 {
		t.Fatalf("a click on the letterbox bar moved the camera to %d,%d", b.cam.X, b.cam.Z)
	}
}
