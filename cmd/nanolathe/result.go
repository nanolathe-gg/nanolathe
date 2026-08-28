package main

import (
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

// drawResultOverlay composes the authored ENDMSN window and the established
// outcome title art. Layout, controls, labels, and background art all come
// from the retail GUI/GAF records; there is intentionally no generated panel,
// dim layer, text, or button geometry here [07 §11][08 "Session end and reporting"].
func (h *retailBattleHUD) drawResultOverlay(c *client.Client, b *battleSession, view frame.ResultView) {
	if h == nil || c == nil || b == nil || !view.Ended || b.battleState().Input.ResultDismissed {
		return
	}
	if h.resultWin != nil {
		h.drawGUIWindow(c, h.resultWin, h.resultGAF, "")
	}

	// ENDMSN's outcome copies are authored with center-anchor offsets. Missing
	// optional art remains a diagnostic from HUD loading and does not acquire a
	// synthetic text substitute [07 §11][fmt gaf].
	title := h.resultTitleFrame(view)
	if title == nil {
		return
	}
	w, height := c.Size()
	c.UIBlitAnchor(title, w/2, height/2)
}

// resultTitleFrame selects only the two authored terminal outcomes. Draw and
// any result kind not established by the retail result contract have no title;
// in particular, they must not inherit the victory art by default [07 §11].
func (h *retailBattleHUD) resultTitleFrame(view frame.ResultView) *formats.GAFFrame {
	if h == nil || view.Draw {
		return nil
	}
	switch strings.ToLower(view.Kind) {
	case "victory":
		return h.resultVictoryFrame
	case "defeat":
		return h.resultDefeatFrame
	default:
		return nil
	}
}

// resultStartAvailable mirrors the retail ENDMSN initializer's outcome choice:
// Start is enabled only when a campaign has a discovered next mission. A
// missing provenance or discovery error leaves the result non-continuable; it
// does not invent a replacement route [07 §11][08 "Progression"].
func resultStartAvailable(fs vfs.FSOps, sess *session.Session) bool {
	if fs == nil || sess == nil || sess.Mission == nil || sess.Mission.Type != mission.TypeCampaign {
		return false
	}
	path := sess.Mission.CampaignPath
	index := sess.Mission.CampaignIndex
	if path == "" {
		return false
	}
	if index < 0 {
		index = sess.CampaignSlot
	}
	if index < 0 {
		return false
	}
	_, hasNext, err := mission.NextCampaignMission(fs, path, index)
	return err == nil && hasNext
}

// configureResultPanel applies the established ENDMSN outcome activation.
// Retail files carry these controls inactive and the end-mission initializer
// enables the single route selected by campaign progression; unrelated
// controls remain inactive rather than being force-enabled [07 §11].
func configureResultPanel(fs vfs.FSOps, sess *session.Session, panel *ui.Panel) {
	if panel == nil {
		return
	}
	start := resultStartAvailable(fs, sess)
	panel.SetActive("Start", start)
	panel.SetActive("MainMenu", !start)
}

// resultActionForControl is a compatibility-shaped adapter for the canonical
// UI result model. The command layer receives a semantic action but keeps the
// battle transition string used by the existing dispatch path [07 §11].
func resultActionForControl(name string) string {
	switch ui.ResultActionForControl(name) {
	case ui.ResultActionContinue:
		return "result_continue"
	case ui.ResultActionMainMenu:
		return "result_main"
	}
	return ""
}

// handleResultInput routes release-inside gestures through the same authored
// Panel state used by the frontend. It does not own a second result-specific
// pressed/button state [07 §3][07 §11].
func (h *retailBattleHUD) handleResultInput(in *input.State) string {
	if h == nil || h.resultPanel == nil || in == nil || in.Mouse == nil {
		return ""
	}
	mx, my := int32(in.Mouse.X), int32(in.Mouse.Y)
	if in.Mouse.Pressed(input.MouseButtonLeft) {
		h.resultPanel.Press(mx, my)
	}
	if !in.Mouse.Released(input.MouseButtonLeft) {
		return ""
	}
	action := h.resultPanel.ReleaseAction(mx, my)
	if action.Kind != ui.ActionActivate {
		return ""
	}
	return resultActionForControl(action.Gadget)
}

// editorFocused reports the authored type-3 focus owner available to the
// battle HUD. Battle side/modal windows have no runtime ui.Panel focus owner
// or text-input dispatcher; the shared authored result panel is the only
// battle-owned panel that can carry such focus. Returning false here is
// therefore an evidence-backed absence, not a second focus model [07 §4][07
// §6].
func (h *retailBattleHUD) editorFocused() bool {
	if h == nil || h.resultPanel == nil || h.resultPanel.Window == nil {
		return false
	}
	index := h.resultPanel.Focused()
	if index < 0 || index >= len(h.resultPanel.Window.Gadgets) {
		return false
	}
	gadget := h.resultPanel.Window.Gadgets[index]
	return gadget.Kind == gui.KindTextBox && gadget.Active != 0 && gadget.GrayedOut == 0 && h.resultPanel.ActiveOf(gadget.Name)
}

// drawStatusMessage draws transient game-speed and pause messages [07 §11][07 §2].
// It remains separate from result presentation; status text is produced by the
// established battle-speed/pause path and is not an endgame label.
func (b *battleSession) drawStatusMessage(c *client.Client, presented *frame.Frame) {
	if b == nil || c == nil || !b.statusVisible(presented) || b.hud == nil || b.hud.console == nil {
		return
	}
	// BattleState is the canonical owner of transient status text. The legacy
	// battleSession mirror is intentionally not read here [07 §11].
	txt := b.battleState().Input.StatusMessage
	w := client.MeasureText(b.hud.console, txt)
	c.UIText(b.hud.console, txt, (640-w)/2, 30, 15)
}
