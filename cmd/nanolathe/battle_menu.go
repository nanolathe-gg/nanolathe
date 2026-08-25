package main

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
)

// battleMenuState follows retail's modal window chain:
// ARMOPT.GUI -> EXITMENU.GUI -> YESORNO.GUI.
type battleMenuState uint8

const (
	battleMenuClosed battleMenuState = iota
	battleMenuOptions
	battleMenuExit
	battleMenuConfirmMain
	battleMenuConfirmExit
)

func (b *battleSession) openBattleMenu() {
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	b.menu = battleMenuOptions
	b.menuPressed = -1
	b.menuPressedState = battleMenuClosed
	b.sess.Clock.Paused = true
	b.dragActive = false
	b.hudCaptured = false
}

func (b *battleSession) closeBattleMenu() {
	if b == nil {
		return
	}
	b.menu = battleMenuClosed
	b.menuPressed = -1
	b.menuPressedState = battleMenuClosed
	if b.sess != nil && b.sess.Clock != nil {
		b.sess.Clock.Paused = false
	}
}

func (b *battleSession) menuWindow() *gui.Window {
	if b == nil || b.hud == nil {
		return nil
	}
	switch b.menu {
	case battleMenuOptions:
		return b.hud.optionsWin
	case battleMenuExit:
		return b.hud.exitWin
	case battleMenuConfirmMain, battleMenuConfirmExit:
		return b.hud.confirmWin
	default:
		return nil
	}
}

// handleBattleMenuInput owns all input while a retail modal is open. Buttons
// activate once on release-inside the same authored gadget [07 §3].
func (b *battleSession) handleBattleMenuInput(in *client.InputState, cl *client.Client) {
	if b == nil || in == nil || in.Kbd == nil || in.Mouse == nil || b.menu == battleMenuClosed {
		return
	}
	if in.Kbd.KeyDown(input.KeyEscape) {
		switch b.menu {
		case battleMenuOptions:
			b.closeBattleMenu()
		case battleMenuExit:
			b.menu = battleMenuOptions
		case battleMenuConfirmMain, battleMenuConfirmExit:
			b.menu = battleMenuExit
		}
		b.menuPressed = -1
		return
	}

	mx, my := int32(in.Mouse.X), int32(in.Mouse.Y)
	if in.Mouse.Pressed(input.MouseButtonLeft) {
		b.menuPressed = b.hud.modalButtonAt(b.menuWindow(), mx, my)
		b.menuPressedState = b.menu
	}
	if !in.Mouse.Released(input.MouseButtonLeft) {
		return
	}
	window := b.menuWindow()
	released := b.hud.modalButtonAt(window, mx, my)
	pressed := b.menuPressed
	pressedState := b.menuPressedState
	b.menuPressed = -1
	b.menuPressedState = battleMenuClosed
	if pressed < 0 || released != pressed || pressedState != b.menu || window == nil || pressed >= len(window.Gadgets) {
		return
	}
	b.activateBattleMenuButton(strings.ToUpper(window.Gadgets[pressed].Name), cl)
}

func (b *battleSession) activateBattleMenuButton(name string, cl *client.Client) {
	switch b.menu {
	case battleMenuOptions:
		switch name {
		case "OK", "CANCEL":
			b.closeBattleMenu()
		case "EXIT":
			b.menu = battleMenuExit
		}
	case battleMenuExit:
		switch name {
		case "CANCEL":
			b.menu = battleMenuOptions
		case "MAINMENU":
			b.menu = battleMenuConfirmMain
		case "EXITGAME":
			b.menu = battleMenuConfirmExit
		}
	case battleMenuConfirmMain, battleMenuConfirmExit:
		switch name {
		case "CHOICE2", "CANCEL":
			b.menu = battleMenuExit
		case "CHOICE1":
			if b.menu == battleMenuConfirmMain && b.returnToMenu != nil {
				b.ended = true
				b.returnToMenu(cl)
			} else if b.menu == battleMenuConfirmExit && cl != nil {
				cl.RequestExit()
			}
		}
	}
}
