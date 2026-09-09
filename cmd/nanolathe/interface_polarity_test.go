package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestInterfaceTypePreferenceSources locks the two in-memory owners of
// Interface Type. A shell stage is live, while a direct battle gets its
// persisted stage at entry and never needs a settings read from input handling
// [07 R-CAM-01 §5][07 R-CAM-01 §7].
func TestInterfaceTypePreferenceSources(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.shell = &gameShell{interfaceType: settings.InterfaceTypeLeftClick}
	if got := b.minimapCameraButton(); got != input.MouseButtonRight {
		t.Fatalf("shell Type 0 camera button = %v, want right", got)
	}
	b.shell.interfaceType = settings.InterfaceTypeRightClick
	if got := b.minimapCameraButton(); got != input.MouseButtonLeft {
		t.Fatalf("live shell Type 1 camera button = %v, want left", got)
	}

	direct := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	direct.applyInterfaceTypeSetting(settings.Settings{InterfaceType: settings.InterfaceTypeRightClick})
	if got := direct.minimapCameraButton(); got != input.MouseButtonLeft {
		t.Fatalf("direct persisted Type 1 camera button = %v, want left", got)
	}
	// Re-applying a freshly loaded block models a new direct entry; the pointer
	// path reads only this stored value afterwards.
	direct.applyInterfaceTypeSetting(settings.Settings{InterfaceType: settings.InterfaceTypeLeftClick})
	if got := direct.minimapCameraButton(); got != input.MouseButtonRight {
		t.Fatalf("direct reloaded Type 0 camera button = %v, want right", got)
	}
}

// TestInterfaceTypeOneMinimapAndWorldPolarity keeps the production click paths
// together: Type 1's idle minimap left captures camera, right orders, but an
// armed left remains an order. Its viewport idle order moves to right; left
// remains selection/drag and clears an empty click [07 R-CAM-01 §5].
func TestInterfaceTypeOneMinimapAndWorldPolarity(t *testing.T) {
	newTypeOne := func() *battleSession {
		b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
		b.applyInterfaceTypeSetting(settings.Settings{InterfaceType: settings.InterfaceTypeRightClick})
		return b
	}

	// Idle minimap left owns the camera latch.
	b := newTypeOne()
	withMinimap(b)
	left := input.NewState()
	left.Mouse.X, left.Mouse.Y = 70, 50
	left.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(left, nil)
	if !b.minimapCameraCaptured || b.minimapCameraCaptureButton != input.MouseButtonLeft {
		t.Fatal("Type 1 idle minimap left did not capture camera")
	}

	// Armed minimap left is never stolen by that idle capture path.
	b = newTypeOne()
	withMinimap(b)
	actor := placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, actor)
	b.battleState().Input.Latch = input.LatchMove
	armedLeft := input.NewState()
	armedLeft.Mouse.X, armedLeft.Mouse.Y = 70, 50
	armedLeft.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleInput(armedLeft, nil)
	if b.minimapCameraCaptured {
		t.Fatal("Type 1 armed minimap left incorrectly captured camera")
	}
	if pending := b.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != hud.LatchToCode(input.LatchMove) {
		t.Fatalf("Type 1 armed minimap left queued %+v, want one Move order", pending)
	}

	// Type 1's right button is never an armed minimap order. It reaches the
	// normal cancellation path after the minimap classifier declines it.
	b = newTypeOne()
	withMinimap(b)
	actor = placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, actor)
	b.battleState().Input.Latch = input.LatchMove
	armedRight := input.NewState()
	armedRight.Mouse.X, armedRight.Mouse.Y = 70, 50
	armedRight.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(armedRight, nil)
	if b.battleState().Input.Latch != input.LatchNormal || len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("Type 1 armed minimap right did not cancel without ordering")
	}

	// The complementary idle minimap right reaches the same contextual-order
	// boundary and retains the event's Shift queue modifier.
	b = newTypeOne()
	withMinimap(b)
	actor = placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, actor)
	rightMinimap := input.NewState()
	rightMinimap.Mouse.X, rightMinimap.Mouse.Y = 70, 50
	rightMinimap.Mouse.SetButton(input.MouseButtonRight, true)
	rightMinimap.Kbd.SetKey(input.KeyShift, true)
	b.handleInput(rightMinimap, nil)
	if pending := b.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 1 || !pending[0].Order.Queued {
		t.Fatalf("Type 1 shifted right minimap click queued %+v, want one queued contextual order", pending)
	}

	// In the viewport, idle right now issues contextual code 1, while right on
	// an armed latch still cancels. A left click remains the armed order button.
	b = newTypeOne()
	actor = placeUnit(b, "armcons", numeric.Fixed(8*65536), numeric.Fixed(8*65536))
	replaceSelectionForTest(t, b, actor)
	rightWorld := input.NewState()
	rightWorld.Mouse.X, rightWorld.Mouse.Y = 300, 300
	rightWorld.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(rightWorld, nil)
	if pending := b.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 1 {
		t.Fatalf("Type 1 right world click queued %+v, want contextual order", pending)
	}
	applyPendingBattleCommands(b)
	b.battleState().Input.Latch = input.LatchMove
	rightArmed := input.NewState()
	rightArmed.Mouse.X, rightArmed.Mouse.Y = 300, 300
	rightArmed.Mouse.SetButton(input.MouseButtonRight, true)
	b.handleInput(rightArmed, nil)
	if b.battleState().Input.Latch != input.LatchNormal || len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("Type 1 right click did not cancel the armed latch cleanly")
	}
	b.battleState().Input.Latch = input.LatchMove
	clickAt(b, 300, 300, false)
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatal("Type 1 armed left did not retire after dispatch")
	}

	// The idle left path is selection/drag only. A small empty click clears an
	// existing selection rather than issuing a contextual order.
	replaceSelectionForTest(t, b, actor)
	clickAt(b, 300, 300, false)
	if b.hasSelection() {
		t.Fatal("Type 1 idle empty left click did not deselect")
	}
	replaceSelectionForTest(t, b, actor)
	clickAt(b, 300, 300, true)
	if b.hasSelection() {
		t.Fatal("Type 1 shifted idle empty left click preserved selection")
	}
}
