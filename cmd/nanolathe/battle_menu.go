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
		if b.cl != nil {
			b.cl.SetPresentationPaused(paused)
		}
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
	// `MISSION`'s branch is the session kind, read where the opener reads the
	// same fact to relabel the button [07 R-FE-01 §7].
	b.battleState().SetCampaign(battleSessionKind(b) == 1)
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
	b.developer.quickkeysDisabled = false
	if state := b.battleState(); state != nil {
		b.applyBattleSchedule(state.CloseOptions())
	}
}

// handleBattleMenuInput routes each open battle modal to its input owner.
// Preferences uses the shared attribute-driven widget service [07 R-WGT-01 §3].
func (b *battleSession) handleBattleMenuInput(in *input.State, cl *client.Client) {
	if b != nil {
		before := b.battleState().Modal()
		prefs := b.battlePrefsActive()
		save := b.shell != nil && b.shell.saveLoadPanelActive()
		var modal *ui.Panel
		if b.shell != nil {
			modal = b.shell.frontend.Panels.Modal()
		}
		defer func() {
			after := b.battleState().Modal()
			closed := before != after && (after == ui.BattleModalClosed || after == ui.BattleModalOptions)
			closed = closed || prefs && !b.battlePrefsActive() || save && !b.shell.saveLoadPanelActive()
			if modal != nil && b.shell.frontend.Panels.Modal() != modal {
				closed = true
			}
			if closed {
				b.developer.quickkeysDisabled = false
			}
		}()
	}

	if b != nil && b.shell != nil && (b.shell.saveLoadPanelActive() || b.shell.frontend.Panels.Modal() != nil) {
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
	// ARMOPT's read-only children own the pass while they are open. An event
	// their indexed service does not claim falls through to the battle
	// caller's modal rows below, which is where Escape closes them back to the
	// surviving options root [07 R-FE-01 §7][07 R-WGT-01 §1].
	if b != nil && b.battleInfoWindowActive() && b.handleBattleInfoWindowInput(in, cl) {
		return
	}
	state := b.battleState()
	if b == nil || state == nil || in == nil || state.Modal() == ui.BattleModalClosed {
		return
	}
	panel, window, page := b.battleModalPanel()
	result := b.serviceBattleChildPanel(panel, window, page, in)
	if result.Fired && panel != nil && result.FiredIndex >= 0 && result.FiredIndex < len(panel.Window.Gadgets) {
		b.activateBattleMenuButton(panel.Window.Gadgets[result.FiredIndex].Name, cl)
		return
	}
	// The child gets its indexed quickkey peek first; only an unclaimed
	// event can enter the battle caller's modal rows [07 §3].
	if result.ConsumedTokens != 0 {
		return
	}
	modalInput := *in
	modalInput.ShortcutTokenMode = in.ShortcutTokenMode || in.PendingTokens() != 0
	if tokens := in.PeekTokens(); len(tokens) != 0 {
		modalInput.ShortcutToken = tokens[0]
	}
	kbd := battleShortcutKeyboard(&modalInput)
	// This is the modal-chain back transition, not the ordinary child window's
	// Escape default.  It remains a battle caller key and therefore does not
	// enable the zero-token child's key matrix [07 R-FE-01 §7][07 R-WGT-01 §2].
	if kbd != nil && kbd.KeyDown(input.KeyEscape) {
		in.DiscardTokens(1)
		b.applyBattleSchedule(state.Back())
		return
	}
	// YESORNO additionally has an explicit battle caller row: raw Enter routes
	// to CHOICE2.  It is not the child key matrix or a header default, which
	// stays excluded by zero token mode [07 R-FE-01 §7][07 R-CAM-01 §2].
	if kbd != nil && kbd.KeyDown(input.KeyEnter) &&
		(state.Modal() == ui.BattleModalConfirmMain || state.Modal() == ui.BattleModalConfirmExit) {
		in.DiscardTokens(1)
		b.activateBattleMenuButton("CHOICE2", cl)
		return
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
	if before == ui.BattleModalOptions && b.battleInfoWindowActive() {
		b.openBattleInfoWindow()
		flushWindowTokens(cl)
	}
	if state.Modal() != before && (state.Modal() == ui.BattleModalExit || state.Modal() == ui.BattleModalRestart || state.Modal() == ui.BattleModalConfirmMain || state.Modal() == ui.BattleModalConfirmExit) {
		flushWindowTokens(cl)
	}
	// Only root close emits the resume intent. All child transitions keep the
	// single-player pause anchor unchanged [07 §11].
	if before == ui.BattleModalOptions && state.Modal() == ui.BattleModalClosed {
		b.developer.quickkeysDisabled = false
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
	case ui.BattleModalActionHelpPage:
		b.refillBattleHelpPage()
	case ui.BattleModalActionBriefingPage:
		b.pageBattleBriefing()
	}
}

// openBattleSaveLoadScreen opens the one LOADGAME.GUI surface over the battle,
// in the direction the ARMOPT button selected [07 R-FE-01 §7] [07 R-FE-01 §8].
func (b *battleSession) openBattleSaveLoadScreen(mode saveLoadMode) {
	if b == nil || b.shell == nil {
		return
	}
	b.shell.openSaveLoadScreenReporting(mode, saveLoadFromBattle)
}
