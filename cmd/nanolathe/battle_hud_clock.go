package main

// The persisted stand-alone battle clock. It is a separate late composer
// layer from the Space-held LIGHTBAR strip [07 R-CAM-01 §6]
// [07 R-HUD-04 §4].

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

const (
	standaloneClockX        = 130
	standaloneClockBottomDY = 34
	clockTicksPerSecond     = 30
	clockTicksPerMinute     = 60 * clockTicksPerSecond
	clockTicksPerHour       = 60 * clockTicksPerMinute
)

// standaloneClockText formats the unsigned global tick. Hours are cumulative,
// not a time-of-day value, and therefore do not wrap at 24 or 100
// [07 R-CAM-01 §6].
func standaloneClockText(label string, tick uint32) string {
	hours := tick / clockTicksPerHour
	remainder := tick % clockTicksPerHour
	minutes := remainder / clockTicksPerMinute
	seconds := (remainder % clockTicksPerMinute) / clockTicksPerSecond
	return fmt.Sprintf("%s : %02d:%02d:%02d", label, hours, minutes, seconds)
}

// standaloneClockFont reproduces the legacy composer's active-FNT state at
// this draw site. With a live message column, its earlier pass has selected
// COMIX. When textlines is zero, that pass returns before selecting a font and
// the side console FNT remains active. MAXLINES can change the shell value
// while battle is running; a direct battle has only its install-time copy
// [07 R-HUD-03 §14.4].
func (h *retailBattleHUD) standaloneClockFont(b *battleSession) *formats.FNT {
	if h == nil || b == nil {
		return nil
	}
	usePrimary := b.clockUsePrimaryFont
	if b.shell != nil {
		usePrimary = b.shell.messages.TextLines != 0
	}
	if usePrimary {
		return h.primaryFont
	}
	return h.console
}

func (h *retailBattleHUD) drawClock(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil || cur == nil || !b.clockShown() {
		return
	}
	font := h.standaloneClockFont(b)
	if font == nil {
		return
	}
	label := "Game Time"
	if captions := hudCaptionTranslator(h); captions != nil {
		label = captions.Translate(label)
	}
	_, height := c.Size()
	y := height - standaloneClockBottomDY - int(font.Height)
	c.UIText(font, standaloneClockText(label, cur.Tick), standaloneClockX, y, h.guiColor(15))
}
