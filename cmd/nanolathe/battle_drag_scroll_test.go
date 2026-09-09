package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func TestCtrlRightDragUsesEventModifierAndSpendsReleaseMotion(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.cam = &camera.Camera{X: 100, Z: 100, ViewW: 640, ViewH: 480, MapW: 4000, MapH: 4000}
	c, err := client.New(client.Options{Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	in := c.Input()
	step := func(x, y int32, down, ctrl bool, kind input.PointerEventKind) {
		e := input.PointerEvent{X: x, Y: y, Kind: kind, Buttons: input.MouseButtons{Right: down}, Modifiers: input.Modifiers{Ctrl: ctrl}}
		in.UpdatePointerMotion(e)
		if kind != 0 {
			in.EnqueuePointer(e)
		}
		in.PublishPointer()
		b.handleInput(in, c)
	}
	// Ctrl has already been released in the live keyboard state. Admission
	// uses the down event's modifier, not that newer state [07 R-CAM-01 §11].
	step(300, 200, true, true, input.RightDown)
	if !b.dragScrollActive || !c.PointerCaptured() || b.cam.X != 100 || b.cam.Z != 100 {
		t.Fatal("Ctrl-right did not capture without moving on entry")
	}
	step(307, 193, true, false, 0)
	if x, z := b.cam.BattleViewOrigin(); x != 240 || z != 112 {
		t.Fatalf("drag beam origin = %d,%d, want 240,112", x, z)
	}
	step(311, 197, false, false, input.RightUp)
	if x, z := b.cam.BattleViewOrigin(); x != 256 || z != 128 {
		t.Fatalf("release beam origin = %d,%d, want 256,128", x, z)
	}
	if b.dragScrollActive || c.PointerCaptured() || !b.dragScrollStepped {
		t.Fatal("release did not finish capture after spending motion")
	}
	if len(b.sess.PendingHumanCommands()) != 0 {
		t.Fatal("drag emitted an order or deselection")
	}
}

func TestCtrlRightViewerEntryCancelsPendingTrackedFollow(t *testing.T) {
	b, cam, buf := followTestFixture(pool.Handle(7))
	cam.X, cam.Z = 100, 100
	publishFollowedUnit(t, buf, 1, 7, 1000, 0, 500)
	c, err := client.New(client.Options{Buffer: buf, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	b.cl = c
	c.SetFocused(true)
	in := c.Input()
	in.EnqueuePointer(input.PointerEvent{Kind: input.RightDown, X: 300, Y: 200, Buttons: input.MouseButtons{Right: true}, Modifiers: input.Modifiers{Ctrl: true}})
	in.PublishPointer()
	b.viewerStep(0, c)
	if !b.dragScrollActive || cam.X != 100 || cam.Z != 100 || cam.LatchedTracked() != 0 {
		t.Fatalf("entry retained follow: camera=%d,%d latched=%d", cam.X, cam.Z, cam.LatchedTracked())
	}
}

func TestCtrlRightReleasesOnTheFirstModalFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	c := b.cl
	c.SetFocused(true)
	b.beginDragScroll(300, 200, c)
	c.Input().Kbd.SetKey(input.KeyTab, true)
	b.viewerStep(0, c)
	if b.dragScrollActive || c.PointerCaptured() {
		t.Fatal("new options window retained relative pointer capture")
	}
}

func TestCtrlRightDoesNotCaptureArmedOrAlternateInterface(t *testing.T) {
	for _, alternate := range []bool{false, true} {
		b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
		if alternate {
			b.applyInterfaceTypeSetting(settings.Settings{InterfaceType: settings.InterfaceTypeRightClick})
		} else {
			b.battleState().Input.Latch = input.LatchMove
		}
		in := input.NewState()
		in.Mouse.SetPosition(300, 200)
		in.Mouse.SetButton(input.MouseButtonRight, true)
		in.Kbd.SetKey(input.KeyCtrl, true)
		b.handleInput(in, nil)
		if b.dragScrollActive {
			t.Fatalf("captured disallowed gesture: alternate=%v", alternate)
		}
	}
}
