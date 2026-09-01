package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// withMinimap gives a fixture battle the rail's fixed 126-pixel radar canvas,
// which the production HUD installs at the framebuffer origin [07 §6][07 §10].
func withMinimap(b *battleSession) hud.Rect {
	dst := hud.Rect{X1: 0, Y1: 0, X2: camera.MinimapLongSide - 1, Y2: camera.MinimapLongSide - 1}
	b.hud = &retailBattleHUD{minimapAnchor: dst, minimapAnchorOK: true}
	return dst
}

func minimapPress(b *battleSession, button input.MouseButton, x, y int32, shift bool) {
	in := input.NewState()
	in.Mouse.X, in.Mouse.Y = float32(x), float32(y)
	in.Mouse.SetButton(button, true)
	if shift {
		in.Kbd.SetKey(input.KeyShift, true)
	}
	b.handleInput(in, nil)
	applyPendingBattleCommands(b)
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

// TestMinimapRightClickJumpsTheCameraToTheCentre locks the minimap latch:
// right down over the minimap sets it and the clicked map point becomes the
// view *centre* [07 R-CAM-01 §5][07 R-CAM-01 §11]. A left click must not do
// this, and the right click must not issue an order.
func TestMinimapRightClickJumpsTheCameraToTheCentre(t *testing.T) {
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

	minimapPress(b, input.MouseButtonRight, mx, my, false)

	if b.cam.X != want.X || b.cam.Z != want.Z {
		t.Fatalf("minimap right click put the camera at %d,%d, want the recentred %d,%d (world point %d,%d)",
			b.cam.X, b.cam.Z, want.X, want.Z, wx, wz)
	}
	if b.cam.X == wx && b.cam.Z == wz {
		t.Fatal("camera origin equals the clicked point: the half-viewport recenter is missing")
	}
	if q := orders.QueueForUnit(u); q != nil && q.LenPrimary() != 0 {
		t.Fatal("the minimap latch button issued an order")
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
