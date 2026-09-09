package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func (b *battleSession) battleState() *ui.BattleState {
	if b == nil {
		return nil
	}
	if b.battleUI == nil {
		b.battleUI = ui.NewProductionBattleState()
	}
	return b.battleUI
}

func (b *battleSession) applyBattleSchedule(intent ui.BattleScheduleIntent) {
	if b == nil || b.sess == nil {
		return
	}
	if intent.PauseSet {
		paused := b.sess.SetPaused(intent.Pause)
		b.battleState().SetPauseTruth(paused)
	}
	if intent.SpeedDelta != 0 {
		b.setGameSpeed(intent.SpeedDelta)
	}
}

func (b *battleSession) openBattleMenu() {
	if b == nil || b.sess == nil {
		return
	}
	b.endDragScroll(b.cl)
	if b.hud != nil {
		b.hud.openOptionsWindow()
	}
	before := b.battleState().Modal()
	b.applyBattleSchedule(b.battleState().OpenOptions())
	if b.battleState().Modal() != before {
		flushWindowTokens(b.cl)
	}
	b.battleState().Input.DragActive = false
	b.battleState().Input.HUDCaptured = false
}

func (b *battleSession) closeBattleMenu() {
	if b == nil {
		return
	}
	if state := b.battleState(); state != nil {
		b.applyBattleSchedule(state.CloseOptions())
	}
}

// handleBattleMenuInput routes each open battle modal to its input owner.
// Preferences uses the shared attribute-driven widget service [07 R-WGT-01 §3].
func (b *battleSession) handleBattleMenuInput(in *input.State, cl *client.Client) {
	if b != nil && b.shell != nil && b.shell.saveLoadPanelActive() {
		// The dialog is a child window of the frontend panel stack, so the
		// frontend's own pump owns it while it is up [07 R-FE-01 §8].
		b.shell.menuInput(cl)
		return
	}
	if b.battlePrefsActive() {
		// `PREFS` opens the options root over `ARMOPT`, and the top window owns
		// the pass [07 R-FE-01 §6][07 R-WGT-01 §1].
		b.handleBattleOptionsInput(cl)
		return
	}
	if b != nil && b.battleState() != nil && b.battleState().Modal() == ui.BattleModalRestart {
		b.handleBattleRestartInput(in, cl)
		return
	}
	state := b.battleState()
	if b == nil || state == nil || in == nil || state.Modal() == ui.BattleModalClosed {
		return
	}
	// This is the modal-chain back transition, not the ordinary child window's
	// Escape default.  It remains a battle caller key and therefore does not
	// enable the zero-token child's key matrix [07 R-FE-01 §7][07 R-WGT-01 §2].
	if in.Kbd != nil && in.Kbd.KeyDown(input.KeyEscape) {
		b.applyBattleSchedule(state.Back())
		return
	}
	// YESORNO additionally has an explicit battle caller row: raw Enter routes
	// to CHOICE2.  It is not the child key matrix or a header default, which
	// stays excluded by zero token mode [07 R-FE-01 §7][07 R-CAM-01 §2].
	if in.Kbd != nil && in.Kbd.KeyDown(input.KeyEnter) &&
		(state.Modal() == ui.BattleModalConfirmMain || state.Modal() == ui.BattleModalConfirmExit) {
		b.activateBattleMenuButton("CHOICE2", cl)
		return
	}
	panel, window, page := b.battleModalPanel()
	result := b.serviceBattleChildPanel(panel, window, page, in)
	if result.Fired && panel != nil && result.FiredIndex >= 0 && result.FiredIndex < len(panel.Window.Gadgets) {
		b.activateBattleMenuButton(panel.Window.Gadgets[result.FiredIndex].Name, cl)
	}
}

func (b *battleSession) activateBattleMenuButton(name string, cl *client.Client) {
	state := b.battleState()
	if state == nil {
		return
	}
	before := state.Modal()
	action := state.Activate(name)
	// Child construction belongs to the transition that exposed it.  The draw
	// path only consumes the retained panel, so it cannot replace a capture or
	// flush input while a child remains open [07 R-WGT-01 §1][07 R-WGT-02 §2].
	if b.hud != nil {
		switch state.Modal() {
		case ui.BattleModalExit:
			b.hud.openExitWindow()
		case ui.BattleModalConfirmMain, ui.BattleModalConfirmExit:
			b.hud.openConfirmWindow()
		}
	}
	if state.Modal() != before && (state.Modal() == ui.BattleModalExit || state.Modal() == ui.BattleModalRestart || state.Modal() == ui.BattleModalConfirmMain || state.Modal() == ui.BattleModalConfirmExit) {
		flushWindowTokens(cl)
	}
	// Only root close emits the resume intent. All child transitions keep the
	// single-player pause anchor unchanged [07 §11].
	if before == ui.BattleModalOptions && state.Modal() == ui.BattleModalClosed {
		b.applyBattleSchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: false})
	}
	if before == ui.BattleModalExit && state.Modal() == ui.BattleModalRestart {
		if b.hud != nil {
			b.hud.openRestartWindow()
		}
		b.openBattleRestartDialog()
	}
	switch action {
	case ui.BattleModalActionMainMenu:
		if b.returnToMenu != nil {
			b.ended = true
			b.returnToMenu(cl)
		}
	case ui.BattleModalActionExitGame:
		if cl != nil {
			cl.RequestExit()
		}
	case ui.BattleModalActionSaveGame:
		b.openBattleSaveLoadScreen(saveScreenMode)
	case ui.BattleModalActionLoadGame:
		b.openBattleSaveLoadScreen(loadScreenMode)
	case ui.BattleModalActionPrefs:
		b.openBattlePrefs()
	case ui.BattleModalActionRestart:
		b.acceptBattleRestart(cl)
	}
}

// openBattleSaveLoadScreen opens the one LOADGAME.GUI surface over the battle,
// in the direction the ARMOPT button selected [07 R-FE-01 §7] [07 R-FE-01 §8].
//
// Presentation note: the shell paints frontend panels only while it is not in
// a battle, so this dialog is currently driven but not painted over the battle
// surface. The two lines that paint it belong in cmd/nanolathe/battle_hud.go's
// drawBattleMenu, which this unit does not own.
func (b *battleSession) openBattleSaveLoadScreen(mode saveLoadMode) {
	if b == nil || b.shell == nil {
		return
	}
	b.shell.openSaveLoadScreenReporting(mode, saveLoadFromBattle)
}
