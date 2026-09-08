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
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/ui"
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

// handleBattleOptionsInput routes the active options root through the shared
// widget service. Ordinary battle child windows do not call this consuming
// path [07 R-WGT-01 §2].
func (b *battleSession) handleBattleOptionsInput(cl *client.Client) {
	if !b.battlePrefsActive() || cl == nil || cl.Input() == nil {
		return
	}
	p := optionsPanel
	in := cl.Input()
	if in.Mouse == nil || in.Kbd == nil || p == nil || p.Window == nil {
		return
	}
	b.serviceBattleOptionsWidgets(p, in)
}

// serviceBattleOptionsWidgets adapts the shared service to the in-battle
// options window. Preference writes and screen transitions remain here.
func (b *battleSession) serviceBattleOptionsWidgets(p *ui.Panel, in *input.State) bool {
	if b == nil || b.shell == nil || p == nil || in == nil || in.Mouse == nil {
		return true
	}
	g := b.shell
	for i, gad := range p.Window.Gadgets {
		if gad.Kind == gui.KindScrollBar {
			if s := g.retailOptionsSliderAt(i); s != nil {
				p.SetSliderKnobAt(i, s.knob)
			}
		}
	}
	frame := pointerFrame(in, widgetTokens(in), false)
	frame.TokenMode = true
	// TODO(question): options-close/page transition coverage for this word is
	// unfinished; the open root explicitly enables it [07 R-WGT-01 §2].
	frame.KeyNavigation = true
	if in.Kbd != nil {
		frame.ShiftHeld, frame.AltHeld = in.Kbd.HasShift(), in.Kbd.KeyHeld(input.KeyAlt)
	}
	if b.millisSource != nil {
		frame.TimerAdvanced = p.TimerAdvanced(clock.ScaledNow(b.millisSource.Millis32()))
	}
	result := p.ServiceFrame(frame, ui.WidgetHooks{Metric: func(int) int { return g.retailTextHeight() }, ArtFrames: func(index int) int {
		gad, ok := battleOptionsGadget(index)
		if !ok {
			return 0
		}
		return g.retailButtonArtFrames(gad)
	}, Change: func(index int) {
		if s := g.retailOptionsSliderAt(index); s != nil {
			g.moveRetailSliderAt(index, s, p.SliderKnobAt(index))
		}
	}})
	in.DiscardTokens(result.ConsumedTokens)
	if result.Fired {
		if _, ok := battleOptionsGadget(result.FiredIndex); ok {
			g.activateWidgetGadget(p, result)
		}
		return true
	}
	return true
}

// battleOptionsGadget reads one gadget of the open in-battle window.
func battleOptionsGadget(index int) (gui.Gadget, bool) {
	if optionsPanel == nil || optionsPanel.Window == nil || index < 1 || index >= len(optionsPanel.Window.Gadgets) {
		return gui.Gadget{}, false
	}
	return optionsPanel.Window.Gadgets[index], true
}
