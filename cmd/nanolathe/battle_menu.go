package main

import (
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
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

func (b *battleSession) menuWindow() *gui.Window {
	if b == nil || b.hud == nil || b.battleState() == nil {
		return nil
	}
	switch b.battleState().Modal() {
	case ui.BattleModalOptions:
		b.hud.openOptionsWindow()
		return b.hud.optionsWin
	case ui.BattleModalExit:
		b.hud.openExitWindow()
		return b.hud.exitWin
	case ui.BattleModalConfirmMain, ui.BattleModalConfirmExit:
		b.hud.openConfirmWindow()
		return b.hud.confirmWin
	case ui.BattleModalRestart:
		b.hud.openRestartWindow()
		return b.hud.restartWin
	default:
		return nil
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
	if b == nil || state == nil || in == nil || in.Kbd == nil || in.Mouse == nil || state.Modal() == ui.BattleModalClosed {
		return
	}
	// Both authored confirmation defaults are No [07 R-FE-01 §7]. Keep
	// this local to YESORNO; other windows have their own keyboard matrix.
	if (state.Modal() == ui.BattleModalConfirmMain || state.Modal() == ui.BattleModalConfirmExit) && in.Kbd.KeyDown(input.KeyEnter) {
		b.activateBattleMenuButton("CHOICE2", cl)
		return
	}
	if in.Kbd.KeyDown(input.KeyEscape) {
		before := state.Modal()
		b.applyBattleSchedule(state.Back())
		if state.Modal() != before && state.Modal() != ui.BattleModalClosed {
			flushWindowTokens(cl)
		}
		return
	}

	mouse, _ := publishedPointer(in)
	mx, my := int32(mouse.X), int32(mouse.Y)
	if b.hud != nil && mouse.Pressed(input.MouseButtonLeft) {
		state.PressModal(b.hud.modalButtonAt(b.menuWindow(), mx, my))
	}
	if !mouse.Released(input.MouseButtonLeft) {
		return
	}
	window := b.menuWindow()
	released := -1
	if b.hud != nil {
		released = b.hud.modalButtonAt(window, mx, my)
	}
	pressed, ok := state.ReleaseModal(released)
	if !ok || window == nil || pressed >= len(window.Gadgets) {
		return
	}
	b.activateBattleMenuButton(window.Gadgets[pressed].Name, cl)
}

func (b *battleSession) activateBattleMenuButton(name string, cl *client.Client) {
	state := b.battleState()
	if state == nil {
		return
	}
	before := state.Modal()
	action := state.Activate(name)
	if state.Modal() != before && state.Modal() != ui.BattleModalClosed {
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
