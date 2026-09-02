package main

import (
	"strings"

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
	b.applyBattleSchedule(b.battleState().OpenOptions())
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
		return b.hud.optionsWin
	case ui.BattleModalExit:
		return b.hud.exitWin
	case ui.BattleModalConfirmMain, ui.BattleModalConfirmExit:
		return b.hud.confirmWin
	default:
		return nil
	}
}

// handleBattleMenuInput owns all input while a retail modal is open. Buttons
// activate once on release-inside the same authored gadget [07 §3].
func (b *battleSession) handleBattleMenuInput(in *input.State, cl *client.Client) {
	if b != nil && b.shell != nil && b.shell.saveLoadPanelActive() {
		// The dialog is a child window of the frontend panel stack, so the
		// frontend's own pump owns it while it is up [07 R-FE-01 §8].
		b.shell.menuInput(cl)
		return
	}
	state := b.battleState()
	if b == nil || state == nil || in == nil || in.Kbd == nil || in.Mouse == nil || state.Modal() == ui.BattleModalClosed {
		return
	}
	if in.Kbd.KeyDown(input.KeyEscape) {
		b.applyBattleSchedule(state.Back())
		return
	}

	mx, my := int32(in.Mouse.X), int32(in.Mouse.Y)
	if b.hud != nil && in.Mouse.Pressed(input.MouseButtonLeft) {
		state.PressModal(b.hud.modalButtonAt(b.menuWindow(), mx, my))
	}
	if !in.Mouse.Released(input.MouseButtonLeft) {
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
	b.activateBattleMenuButton(strings.ToUpper(window.Gadgets[pressed].Name), cl)
}

func (b *battleSession) activateBattleMenuButton(name string, cl *client.Client) {
	state := b.battleState()
	if state == nil {
		return
	}
	before := state.Modal()
	action := state.Activate(name)
	// Only root close emits the resume intent. All child transitions keep the
	// single-player pause anchor unchanged [07 §11].
	if before == ui.BattleModalOptions && state.Modal() == ui.BattleModalClosed {
		b.applyBattleSchedule(ui.BattleScheduleIntent{PauseSet: true, Pause: false})
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
