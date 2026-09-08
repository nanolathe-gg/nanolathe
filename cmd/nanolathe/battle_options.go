package main

// The in-battle options window: `ARMOPT`'s `PREFS` button, its pointer pump,
// and the four writes whose consumers are the running battle rather than the
// stored preference block [07 R-FE-01 §6][07 R-FE-01 §7].
//
// Everything about the window itself — the root, the page merge, the sliders,
// the snapshot, `RESTORE` and `UNDO` — is the front end's own machinery in
// retail_menu_options.go with its in-battle arm taken. This file is only the
// seam: who opens it, who drives it, and what a page write reaches inside a
// live session.

import (
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/settings"
)

// openBattlePrefs is `ARMOPT`'s `PREFS`: the options root opens as a child
// window over the battle with the options-open bit set [07 R-FE-01 §6]
// [07 R-FE-01 §7]. `ARMOPT` has already set the pause bit and stays on the
// modal chain underneath, so nothing here touches the schedule.
func (b *battleSession) openBattlePrefs() {
	if b == nil || b.shell == nil {
		return
	}
	b.shell.openRetailOptionsScreenReportingIn(true)
}

// battlePrefsActive reports whether the in-battle options window owns input and
// the last composition layer.
func (b *battleSession) battlePrefsActive() bool {
	return b != nil && b.shell != nil && b.shell.retailOptionsActive() && optionsState != nil && optionsState.inBattle
}

// battleOptionsSession is the running battle the options window was opened
// over, or nil in the front end. The shell keeps one battle at a time, and it
// is torn down before the shell returns to a menu screen.
func (g *gameShell) battleOptionsSession() *battleSession {
	if g == nil || optionsState == nil || !optionsState.inBattle {
		return nil
	}
	if g.battle == nil || g.battle.ended {
		return nil
	}
	return g.battle
}

// applyRetailOptionsToBattle re-applies every page write whose consumer is the
// running battle. `CANCEL` calls it after restoring the entry snapshot, the way
// it re-applies gamma and the volumes [07 R-FE-01 §6].
func (g *gameShell) applyRetailOptionsToBattle() {
	g.applyRetailBattleGameSpeed()
	g.applyRetailBattleScrollSpeed()
	g.applyRetailBattleMessageLines()
}

// applyRetailBattleGameSpeed pushes the stored game-speed word through the
// session's speed setter, which is where `GAME` applies "at once"
// [07 R-FE-01 §6][07 R-CAM-01 §3]. The setter is relative, so the request is
// expressed as the delta from the live requested speed; it owns the 1..20
// clamp and the announcement, and it posts nothing when the clamped target is
// unchanged.
func (g *gameShell) applyRetailBattleGameSpeed() {
	b := g.battleOptionsSession()
	if b == nil || b.sess == nil || b.sess.Clock == nil {
		return
	}
	current := int(b.sess.Clock.Requested)
	if current < settings.MinGameSpeed {
		current = settings.MinGameSpeed
	}
	if current > settings.MaxGameSpeed {
		current = settings.MaxGameSpeed
	}
	if delta := g.gameSpeed - current; delta != 0 {
		b.setGameSpeed(delta)
	}
}

// applyRetailBattleScrollSpeed re-primes the battle's cached scroll byte from
// the shell's live value. The camera's scroll pass reads that cache once per
// host frame [07 §10]; `SCREEN` is the one control that can change it while a
// battle is running.
func (g *gameShell) applyRetailBattleScrollSpeed() {
	if b := g.battleOptionsSession(); b != nil {
		b.primeScrollSetting()
	}
}

// applyRetailBattleMessageLines pushes `textlines` and `textscroll` into the
// live message column. Retail installs them at settings load [02 §3]
// [07 R-HUD-03 §14.3]; the interface page is the writer that can move them
// afterwards [07 R-CAM-01 §7].
func (g *gameShell) applyRetailBattleMessageLines() {
	b := g.battleOptionsSession()
	if b == nil || b.cl == nil {
		return
	}
	// The client owns the one message ring the composer draws and the battle's
	// own hotkeys post to, so the page reconfigures that instance rather than a
	// second one [07 R-HUD-03 §14.3][07 R-HUD-03 §14.4].
	b.cl.ConfigureMessageLines(uint16(g.messages.TextLines), uint16(g.messages.TextScroll))
	b.cl.SetScreenChat(uint8(g.messages.ScreenChat))
}

// handleBattleOptionsInput is the in-battle options window's own pointer and
// keyboard pass.
//
// This pump retains the options window's own pointer capture and page-slider
// state. Both pumps share indexed Enter/Space target selection; ordinary battle
// child windows do not call either consuming matrix path [07 R-WGT-01 §2].
func (b *battleSession) handleBattleOptionsInput(cl *client.Client) {
	if !b.battlePrefsActive() || cl == nil || cl.Input() == nil {
		return
	}
	g := b.shell
	p := optionsPanel
	in := cl.Input()
	mouse, kbd := in.Mouse, in.Kbd
	if mouse == nil || kbd == nil || p == nil || p.Window == nil {
		return
	}

	// A knob drag owns the pointer until the button is released
	// [07 R-WGT-01 §5 "Pointer"].
	g.updateRetailSliderDrag(mouse)

	pressed := mouse.Pressed(input.MouseButtonLeft)
	released := mouse.Released(input.MouseButtonLeft)
	held := mouse.Held(input.MouseButtonLeft)
	x, y := int32(mouse.X), int32(mouse.Y)

	// The captured gadget keeps receiving the pass while the button is held,
	// which is what makes a slider arrow repeat [07 R-WGT-01 §1 "Capture"]
	// [07 R-WGT-01 §5 "Pointer"].
	if held && !pressed && optionsState.pressed >= 0 {
		if gad, ok := battleOptionsGadget(optionsState.pressed); ok && gad.Kind == gui.KindScrollBar {
			if s := g.retailOptionsSlider(gad.Name); s != nil {
				r := p.Window.PlacedRect(optionsState.pressed)
				knobStart := int(r.X) + s.arrowW + 1 + s.knob
				if int(x) < knobStart {
					g.adjustRetailSlider(gad, -1)
				} else if int(x) >= knobStart+s.knobSize {
					g.adjustRetailSlider(gad, 1)
				}
			}
		}
	}

	if pressed {
		optionsState.pressed = battleOptionsPressTest(x, y)
		if optionsState.pressed >= 0 {
			p.SetFocus(optionsState.pressed)
			if gad, ok := battleOptionsGadget(optionsState.pressed); ok && gad.Kind == gui.KindScrollBar {
				g.clickRetailSlider(gad, p.Window.PlacedRect(optionsState.pressed), x, y)
			}
		}
	}
	if released {
		index := optionsState.pressed
		optionsState.pressed = -1
		if index >= 0 && battleOptionsPressTest(x, y) == index {
			if gad, ok := battleOptionsGadget(index); ok {
				if gad.Kind == gui.KindScrollBar {
					g.releaseRetailSlider(gad)
					// A release on the track beyond the knob is the
					// synthesised arrow's step [07 R-WGT-01 §5 "Pointer"].
					if s := g.retailOptionsSlider(gad.Name); s != nil {
						r := p.Window.PlacedRect(index)
						knobStart := int(r.X) + s.arrowW + 1 + s.knob
						if int(x) < knobStart {
							g.adjustRetailSlider(gad, -1)
						} else if int(x) >= knobStart+s.knobSize {
							g.adjustRetailSlider(gad, 1)
						}
					}
				} else {
					// A callback may close the window, so nothing after this
					// may touch the options state.
					g.activateGadget(gad.Name)
					return
				}
			}
		}
	}

	// `PREFS.GUI` authors `escdefault=PREV` and `crdefault=PREV`, so Escape and
	// Enter both leave through "OK" — the tab-close path of [07 R-FE-01 §6],
	// which returns to `ARMOPT` with the battle still paused.
	if kbd.KeyDown(input.KeyEscape) {
		g.activateEscape()
		return
	}
	if (kbd.KeyDown(input.KeyEnter) || kbd.KeyDown(input.KeySpace)) && g.activateDefaultKey(p, kbd.KeyDown(input.KeyEnter)) {
		return
	}
	g.activateButtonQuickKey(p, kbd, optionsState.pressed)
}

// battleOptionsGadget reads one gadget of the open in-battle window.
func battleOptionsGadget(index int) (gui.Gadget, bool) {
	if optionsPanel == nil || optionsPanel.Window == nil || index < 1 || index >= len(optionsPanel.Window.Gadgets) {
		return gui.Gadget{}, false
	}
	return optionsPanel.Window.Gadgets[index], true
}

// battleOptionsFires reports whether a gadget kind has a press handler at all.
// Buttons (§3), lists (§4), text inputs (§6) and sliders (§5) do; labels reach
// their `link` redirection (§7). Panels, fonts, raw files, lines and picture
// boxes do not — a picture box "blits its frame" and returns
// [07 R-WGT-01 §8][07 R-WGT-01 §12]. A blank surface takes a press only while
// its `hotornot` word is 1 [07 R-WGT-01 §8].
func battleOptionsFires(gad gui.Gadget) bool {
	switch gad.Kind {
	case gui.KindButton, gui.KindListBox, gui.KindTextBox, gui.KindScrollBar, gui.KindLabel:
		return true
	case gui.KindSurface:
		return gad.HotOrNot == 1
	}
	return false
}

// battleOptionsPressTest returns the gadget that takes the pointer capture, or
// -1: the first in index order that can accept a press, is not hidden and is
// not greyed [07 R-WGT-01 §1 "Capture"][07 R-WGT-01 §13].
func battleOptionsPressTest(x, y int32) int {
	p := optionsPanel
	if p == nil || p.Window == nil {
		return -1
	}
	for i := 1; i < len(p.Window.Gadgets); i++ {
		gad := p.Window.Gadgets[i]
		if !battleOptionsFires(gad) || !p.ActiveAt(i) || gad.GrayedOut != 0 {
			continue
		}
		r := p.Window.PlacedRect(i)
		if r.W <= 0 || r.H <= 0 || x < r.X || y < r.Y || x > r.X+r.W-1 || y > r.Y+r.H-1 {
			continue
		}
		return i
	}
	return -1
}
