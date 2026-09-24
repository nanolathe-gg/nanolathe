package main

// The Survival wave line: a Nanolathe HUD overlay, right-aligned under the
// resource strip so it clears the wind and clock overlays at the left, that
// shows the committed director state (docs/DESIGN_SURVIVAL.md §9).

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

const (
	survivalStatusY = 36
	// resultRowsTop and resultRowPitch are ENDMSN's authored row geometry,
	// as resultPlayerColorRect places each row.
	resultRowsTop  = 93
	resultRowPitch = 20
)

// survivalStatusText formats one tick's Survival state.
func survivalStatusText(s frame.SurvivalStatus) string {
	clock := fmt.Sprintf("%d:%02d", s.SecondsLeft/60, s.SecondsLeft%60)
	switch s.Phase {
	case frame.SurvivalGrace:
		return "Survival - first wave in " + clock
	case frame.SurvivalWarning:
		return fmt.Sprintf("Wave %d incoming in %s", s.Wave, clock)
	case frame.SurvivalActive:
		return fmt.Sprintf("Wave %d - %d attackers", s.Wave, s.Attackers)
	default:
		return fmt.Sprintf("Wave %d in %s - %d attackers left", s.Wave, clock, s.Attackers)
	}
}

func (h *retailBattleHUD) drawSurvivalStatus(c *client.Client, cur *frame.Frame) {
	if h == nil || c == nil || cur == nil || !cur.Survival.Active || h.console == nil {
		return
	}
	text := survivalStatusText(cur.Survival)
	width, _ := c.Size()
	w := client.MeasureText(h.console, text)
	x := width - w - 8
	c.UIFillRect(x-4, survivalStatusY-2, w+8, int(h.console.Height)+4, h.guiColor(0))
	color := h.guiColor(15)
	if cur.Survival.Phase == frame.SurvivalWarning {
		color = h.guiColor(12)
	}
	c.UIText(h.console, text, x, survivalStatusY, color)
}

// drawSurvivalResultLine is the one Nanolathe line under ENDMSN's authored
// rows (DESIGN_SURVIVAL §8): the seven columns keep their layout.
func (h *retailBattleHUD) drawSurvivalResultLine(c *client.Client, view frame.ResultView, rows int) {
	r := view.Survival
	if h == nil || c == nil || r == nil || h.console == nil {
		return
	}
	secs := r.TicksAlive / 30
	text := fmt.Sprintf("Survived %d:%02d:%02d - waves survived %d - value destroyed %d - Survival score %d",
		secs/3600, secs/60%60, secs%60, r.Waves, r.Destroyed, r.Score)
	width, _ := c.Size()
	w := client.MeasureText(h.console, text)
	y := resultRowsTop + resultRowPitch*rows + 8
	c.UIText(h.console, text, (width-w)/2, y, h.guiColor(15))
}
