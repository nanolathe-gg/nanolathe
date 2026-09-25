package main

// The Survival wave and score lines: a Nanolathe HUD overlay, right-aligned
// under the resource strip so it clears the wind and clock overlays at the
// left, that shows the committed director state and the team's score
// (docs/DESIGN_SURVIVAL.md §8, §9).

import (
	"fmt"
	"strconv"
	"strings"

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

// groupDigits writes n with a comma between each group of three digits.
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (h *retailBattleHUD) drawSurvivalStatus(c *client.Client, cur *frame.Frame) {
	if h == nil || c == nil || cur == nil || !cur.Survival.Active || h.console == nil {
		return
	}
	color := h.guiColor(15)
	if cur.Survival.Phase == frame.SurvivalWarning {
		color = h.guiColor(12)
	}
	y := h.drawSurvivalLine(c, survivalStatusText(cur.Survival), survivalStatusY, color)
	h.drawSurvivalLine(c, "Score "+groupDigits(cur.Survival.Score), y, h.guiColor(15))
}

// drawSurvivalLine draws one right-aligned line on its backing and returns
// the next line's top.
func (h *retailBattleHUD) drawSurvivalLine(c *client.Client, text string, y int, color uint8) int {
	width, _ := c.Size()
	w := client.MeasureText(h.console, text)
	x := width - w - 8
	lineH := int(h.console.Height) + 4
	c.UIFillRect(x-4, y-2, w+8, lineH, h.guiColor(0))
	c.UIText(h.console, text, x, y, color)
	return y + lineH
}

// survivalResultLines are the Nanolathe lines under ENDMSN's authored rows
// (DESIGN_SURVIVAL §8): the team's time, waves and score, then how the score
// divides into each survivor's damage and the waves' points.
func survivalResultLines(view frame.ResultView) []string {
	r := view.Survival
	if r == nil {
		return nil
	}
	secs := r.TicksAlive / 30
	first := fmt.Sprintf("Survived %d:%02d:%02d - waves survived %d - team score %s",
		secs/3600, secs/60%60, secs%60, r.Waves, groupDigits(r.Score))
	var parts []string
	for _, row := range view.Scores {
		if row.Player >= 0 && row.Player < len(r.SlotDamage) && r.SlotDamage[row.Player] > 0 {
			parts = append(parts, fmt.Sprintf("%s %s", row.Name, groupDigits(r.SlotDamage[row.Player])))
		}
	}
	second := "Damage " + groupDigits(r.Damage)
	if len(parts) > 1 {
		second += " (" + strings.Join(parts, ", ") + ")"
	}
	second += " - waves " + groupDigits(r.WavePoints)
	return []string{first, second}
}

func (h *retailBattleHUD) drawSurvivalResultLine(c *client.Client, view frame.ResultView, rows int) {
	if h == nil || c == nil || h.console == nil {
		return
	}
	width, _ := c.Size()
	y := resultRowsTop + resultRowPitch*rows + 8
	for _, text := range survivalResultLines(view) {
		w := client.MeasureText(h.console, text)
		c.UIText(h.console, text, (width-w)/2, y, h.guiColor(15))
		y += int(h.console.Height) + 4
	}
}
