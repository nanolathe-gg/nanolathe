package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// serviceBattleChildPanel adapts an ordinary battle child to the common
// indexed service.  Its token mode remains zero: only an admitted quickkey
// claims the peeked prefix, while Enter and Escape remain outside the child
// key matrix [07 R-WGT-01 §§1-3][07 R-WGT-02 §2].
func (b *battleSession) serviceBattleChildPanel(panel *ui.Panel, window *gui.Window, page *formats.GAF, in *input.State) ui.ServiceResult {
	result := ui.ServiceResult{FiredIndex: -1, HoverIndex: -1}
	if panel == nil || window == nil || in == nil {
		return result
	}
	frame := pointerFrame(in, in.PeekTokens(), false)
	frame.DisableQuickKeys = b != nil && b.developer.quickkeysDisabled
	if in.Kbd != nil {
		frame.AltHeld = in.Kbd.KeyHeld(input.KeyAlt)
	}
	if b != nil && b.millisSource != nil {
		frame.TimerAdvanced = panel.TimerAdvanced(clock.ScaledNow(b.millisSource.Millis32()))
	}
	result = panel.ServiceFrame(frame, ui.WidgetHooks{ArtFrames: func(index int) int {
		if b == nil || b.hud == nil || index < 0 || index >= len(window.Gadgets) {
			return 0
		}
		return b.hud.modalGadgetArtFrames(window.Gadgets[index], page)
	}})
	in.DiscardTokens(result.ConsumedTokens)
	return result
}

// serviceBattleResultPanel is the ENDMSN form of the same zero-token child
// adapter.  ENDMSN's post-battle owner has no battle timer parameter, so its
// timed-widget arm remains inactive; pointer and accelerator service use the
// same indexed pass as the other battle children [07 R-WGT-01 §§1-3].
func (h *retailBattleHUD) serviceBattleResultPanel(in *input.State) ui.ServiceResult {
	result := ui.ServiceResult{FiredIndex: -1, HoverIndex: -1}
	if h == nil || h.resultPanel == nil || h.resultWin == nil || in == nil {
		return result
	}
	frame := pointerFrame(in, in.PeekTokens(), false)
	if in.Kbd != nil {
		frame.AltHeld = in.Kbd.KeyHeld(input.KeyAlt)
	}
	result = h.resultPanel.ServiceFrame(frame, ui.WidgetHooks{ArtFrames: func(index int) int {
		if index < 0 || index >= len(h.resultWin.Gadgets) {
			return 0
		}
		return h.modalGadgetArtFrames(h.resultWin.Gadgets[index], h.resultGAF)
	}})
	in.DiscardTokens(result.ConsumedTokens)
	return result
}

// battleModalPanel selects the retained runtime panel corresponding to the
// current ordinary modal.  Opening is idempotent and never occurs in drawing;
// it only completes a modal transition which already selected that child.
func (b *battleSession) battleModalPanel() (*ui.Panel, *gui.Window, *formats.GAF) {
	if b == nil || b.hud == nil || b.battleState() == nil {
		return nil, nil, nil
	}
	switch b.battleState().Modal() {
	case ui.BattleModalOptions:
		b.hud.openOptionsWindow()
		return b.hud.optionsPanel, b.hud.optionsWin, b.hud.optionsGAF
	case ui.BattleModalExit:
		b.hud.openExitWindow()
		return b.hud.exitPanel, b.hud.exitWin, nil
	case ui.BattleModalConfirmMain, ui.BattleModalConfirmExit:
		b.hud.openConfirmWindow()
		return b.hud.confirmPanel, b.hud.confirmWin, nil
	}
	return nil, nil, nil
}
