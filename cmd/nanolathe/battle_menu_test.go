package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/session"
)

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
