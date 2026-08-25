package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/session"
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
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}, menuPressed: -1}
	b.openBattleMenu()
	if b.menu != battleMenuOptions || !b.sess.Clock.Paused {
		t.Fatalf("open menu = state %d paused %v; want options and paused", b.menu, b.sess.Clock.Paused)
	}
	b.closeBattleMenu()
	if b.menu != battleMenuClosed || b.sess.Clock.Paused {
		t.Fatalf("close menu = state %d paused %v; want closed and running", b.menu, b.sess.Clock.Paused)
	}
}

func TestBattleMenuExitGameConfirmationRequestsTermination(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}, menuPressed: -1}
	cl, err := client.New(client.Options{Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", cl)
	if b.menu != battleMenuExit {
		t.Fatalf("EXIT state = %d; want exit menu", b.menu)
	}
	b.activateBattleMenuButton("EXITGAME", cl)
	if b.menu != battleMenuConfirmExit {
		t.Fatalf("EXITGAME state = %d; want confirmation", b.menu)
	}
	b.activateBattleMenuButton("CHOICE1", cl)
	if !cl.ExitRequested() {
		t.Fatal("Yes did not request client termination")
	}
}

func TestBattleMenuMainMenuConfirmationInvokesReturn(t *testing.T) {
	b := &battleSession{sess: &session.Session{Clock: &clock.State{}}, menuPressed: -1}
	returned := false
	b.returnToMenu = func(*client.Client) { returned = true }
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", nil)
	b.activateBattleMenuButton("MAINMENU", nil)
	if b.menu != battleMenuConfirmMain {
		t.Fatalf("MAINMENU state = %d; want main-menu confirmation", b.menu)
	}
	b.activateBattleMenuButton("CHOICE1", nil)
	if !returned {
		t.Fatal("Yes did not invoke frontend return")
	}
}
