package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func TestPlaceBattleModalCentersOverRetailPlayfield(t *testing.T) {
	tests := []struct {
		name       string
		w, h, x, y int32
	}{
		{name: "exit menu", w: 150, h: 155, x: 309, y: 162},
		{name: "yes or no", w: 400, h: 100, x: 184, y: 190},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window := &gui.Window{
				Rect:    gui.Rect{X: 7, Y: 9, W: test.w, H: test.h, RawX: 7, RawY: 9},
				OriginX: 7,
				OriginY: 9,
				Gadgets: []gui.Gadget{{Rect: gui.Rect{X: 7, Y: 9, W: test.w, H: test.h}}},
			}
			placeBattleModal(window, 640, 480)
			if window.Rect.X != test.x || window.Rect.Y != test.y {
				t.Fatalf("origin = (%d,%d), want (%d,%d)", window.Rect.X, window.Rect.Y, test.x, test.y)
			}
			if window.Rect.RawX != 7 || window.Rect.RawY != 9 {
				t.Fatalf("authored origin was discarded: raw=(%d,%d)", window.Rect.RawX, window.Rect.RawY)
			}
		})
	}
}

func TestBattleMenuPauseAndResume(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	b.openBattleMenu()
	if b.battleState().Modal() != ui.BattleModalOptions || !b.sess.Clock.Paused {
		t.Fatalf("open menu = state %d paused %v; want options and paused", b.battleState().Modal(), b.sess.Clock.Paused)
	}
	b.closeBattleMenu()
	if b.battleState().Modal() != ui.BattleModalClosed || b.sess.Clock.Paused {
		t.Fatalf("close menu = state %d paused %v; want closed and running", b.battleState().Modal(), b.sess.Clock.Paused)
	}
}

func TestBattleMenuExitGameConfirmationRequestsTermination(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	cl, err := client.New(client.Options{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", cl)
	if b.battleState().Modal() != ui.BattleModalExit {
		t.Fatalf("EXIT state = %d; want exit menu", b.battleState().Modal())
	}
	b.activateBattleMenuButton("EXITGAME", cl)
	if b.battleState().Modal() != ui.BattleModalConfirmExit {
		t.Fatalf("EXITGAME state = %d; want confirmation", b.battleState().Modal())
	}
	b.activateBattleMenuButton("CHOICE1", cl)
	if !cl.ExitRequested() {
		t.Fatal("Yes did not request client termination")
	}
}

func TestBattleMenuMainMenuConfirmationInvokesReturn(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}}
	returned := false
	b.returnToMenu = func(*client.Client) { returned = true }
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", nil)
	b.activateBattleMenuButton("MAINMENU", nil)
	if b.battleState().Modal() != ui.BattleModalConfirmMain {
		t.Fatalf("MAINMENU state = %d; want main-menu confirmation", b.battleState().Modal())
	}
	b.activateBattleMenuButton("CHOICE1", nil)
	if !returned {
		t.Fatal("Yes did not invoke frontend return")
	}
}

func TestBattleMenuTabCloseConsumesClosingFrame(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.openBattleMenu()
	if b.battleState().Modal() != ui.BattleModalOptions {
		t.Fatal("test setup did not open options modal")
	}
	cl, err := client.New(client.Options{
		Buffer:   b.sess.Snapshot,
		Width:    640,
		Height:   480,
		Headless: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	in := cl.Input()
	in.Kbd.SetKey(input.KeyTab, true)
	in.Kbd.SetKey(input.KeyM, true)
	in.Mouse.SetPosition(200, 200)
	in.Mouse.SetButton(input.MouseButtonLeft, true)

	// The frame starts modal, so Tab closes ARMOPT but the simultaneous M and
	// mouse edges must not arm an order or enqueue a world action [07 §2][07 §3].
	b.viewerStep(0, cl)
	if b.battleState().Modal() != ui.BattleModalClosed {
		t.Fatalf("Tab did not close options modal: %d", b.battleState().Modal())
	}
	if b.battleState().Input.Latch != input.LatchNormal {
		t.Fatalf("closing modal frame leaked M hotkey and armed latch %d", b.battleState().Input.Latch)
	}
	if got := b.sess.PendingHumanCommands(); len(got) != 0 {
		t.Fatalf("closing modal frame leaked %d human commands", len(got))
	}
}
