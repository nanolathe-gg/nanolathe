package main

// The bottom slide strip held up by Space: its geometry and the three
// translated readouts [07 R-HUD-04 §4].

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// The unit count and the game clock were drawn here, at the TOTALUNITS and
// TOTALTIME anchors. That was our own defect, not authored data: the side
// anchor block has exactly two consumers, the top-strip painter and the
// footer, and TOTALUNITS/TOTALTIME are "loaded, never read"
// [07 R-HUD-03 §5]. Stock ARM authors TOTALTIME at (605,7) and
// ENERGYPRODUCED at (609,5), so painting the clock there overlapped the
// energy production reading and partly occluded it. The running display
// belongs to the Space-held slide strip, whose three translated lines are
// `Game Time : hh:mm:ss`, `Total Units : %d  (Max %d)` and `Game Speed %s%s`
// [07 R-HUD-04 §4].
//
// The slide strip's placement is established [07 R-HUD-04 §4]: with `x` the
// composer clip rectangle's left edge (the view's, 128), `yBottom` its bottom
// edge (H - 33) and `off` the slide offset (-31..0, drawn only while
// non-zero), the band is blitted at `(x, yBottom + off)` and the three
// strings are written on one line at `yBottom + off + 10` — `Game Time` at
// `x + 25`, `Total Units` at `x + 190`, `Game Speed` at `x + 380` — in GAF
// slot 1 (hattfont11). drawSlideStrip below is that draw.

// Slide-strip geometry [07 R-HUD-04 §4]. `x` is the composer surface
// rectangle's left edge and `yBottom` its bottom edge. In battle that
// rectangle is the composer's clip rectangle, which battle entry sets to the
// view — left 128, top 32, right W-1, bottom H-33 — so the strip's origin is
// the view's bottom-left corner, not the screen's.
const (
	slideStripViewLeft     = 128
	slideStripViewBottomUp = 33
	slideStripTimeX        = 25
	slideStripUnitsX       = 190
	slideStripSpeedX       = 380
	slideStripTextY        = 10
	// slideStripNormalSpeed is the value at which the adapted current speed
	// word prints the localized normal word instead of an offset, and the
	// value both `%+d` arguments are measured from [07 §6][07 R-CAM-01 §3].
	slideStripNormalSpeed = 10
)

// drawSlideStrip draws the §6 strip: the band, then its three readouts. The
// strip is drawn only while the slide offset is non-zero — at 0 it is off
// screen — and every string sits on one line at `yBottom + off + 10`
// [07 R-HUD-04 §4][07 §6].
//
// The composer steps this strip in every session kind, unlike the Space-held
// score panel of [07 R-HUD-04 §1], and its show test is Space unless a text
// editor has the focus. That test is the rail state this build already owns, so
// the offset is read rather than recomputed.
//
// This is the offset's ONE consumer. It is not a side rail: "it is not a side
// rail but the strip that slides up from the bottom edge of the view when Space
// is held" [07 R-HUD-03 §1 "the panel-slide gate"], and neither PANELSIDE nor
// any rail window or gadget rectangle moves with it [07 R-HUD-05].
//
// Band. The art is frame index 1 of the common GUI GAF's `LIGHTBAR` entry
// (507x32 in the stock file), cached with its hotspot zeroed and blitted by
// the plain frame blitter at `(x, yBottom + off)` [07 R-HUD-04 §4]. This used
// to be a TODO(question): the band was not drawn and PANELBOT showed through.
//
// Text. The composer selects the window's GAF-font slot 1 (hattfont11) for the
// three strings and restores slot 0 afterwards; each goes through the GAF pen
// with no width limit and mode 0 — glyph bytes copied, no light-table remap
// [03 R-FONT-01 §6][07 R-HUD-04 §4]. The formats are literal: `%s : %02d:%02d:%02d`,
// `%s : %d  (Max %d)` (two spaces) and `%s %s`, each `%s` the translated key
// `Game Time` / `Total Units` / `Game Speed`; no colon follows the key. This
// used to be drawn with the side FNT in the GUI colour map's entry 0 at
// screen-relative offsets, which is what made it look unlike retail.
func (h *retailBattleHUD) drawSlideStrip(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil || cur == nil {
		return
	}
	off := int(b.battleState().PanelOffset)
	if off == 0 {
		return
	}
	_, height := c.Size()
	x := slideStripViewLeft
	yBottom := height - slideStripViewBottomUp
	if h.stripArt != nil {
		c.UIBlit(h.stripArt, x, yBottom+off)
	}
	font := h.modalFontSmall
	y := yBottom + off + slideStripTextY
	write := func(dx int, text string) {
		if font != nil {
			drawRetailGAFText(c, font, text, x+dx, y, -1)
		} else if h.console != nil {
			// Null slot: the pen falls back to the active FNT with the width
			// limit dropped [03 R-FONT-01 §6].
			c.UIText(h.console, text, x+dx, y, h.guiColor(0))
		}
	}
	live := 0
	if slot := int(cur.Selection.LocalPlayer); slot >= 0 && slot < len(cur.Players) {
		live = cur.Players[slot].LiveUnits
	}
	write(slideStripTimeX, slideStripTimeText(int32(cur.Tick)))
	write(slideStripUnitsX, slideStripUnitsText(live, cur.Strip.UnitLimit))
	write(slideStripSpeedX, "Game Speed "+slideStripSpeedText(cur.Strip))
}

// slideStripTimeText is the strip's clock line, `%s : %02d:%02d:%02d` of the
// translated `Game Time` key and the tick count as h:m:s at 30 Hz
// [07 R-HUD-04 §4].
func slideStripTimeText(tick int32) string {
	return "Game Time : " + retailSummaryTime(tick)
}

// slideStripUnitsText is the strip's unit line, `%s : %d  (Max %d)` — note the
// two spaces before the parenthesis — of the translated `Total Units` key
// [07 R-HUD-04 §4].
func slideStripUnitsText(live int, limit int32) string {
	return fmt.Sprintf("Total Units : %d  (Max %d)", live, limit)
}

// slideStripSpeedText is the composer's own speed formatter, which is separate
// from the message-ring announcement of [07 R-CAM-01 §3]: `Normal` when the
// ADAPTED CURRENT speed word is 10, otherwise `%+d` of that same word's offset
// from normal, with ` (%+d)` of the TARGET word's offset appended while the two
// words differ [07 §6][07 R-CAM-01 §3].
//
// Which word goes where is the part that is easy to get backwards, and this
// code had it backwards. Both the `Normal` test and the leading `%+d` read the
// adapted current word — the one the tick-budget adaptation of [01 §4.3] steps
// toward the target — and only the parenthesised suffix carries the target word
// the speed keys set. So an adaptation of 11 under a target of 13 reads
// `Game Speed +1 (+3)`, and `Game Speed Normal (+2)` means the adaptation has
// settled at 10 under a target of 12.
//
// Both `%+d` arguments are the **offset from normal**, not the raw speed word:
// each speed word is widened from 16 bits without sign extension and 10 is
// subtracted before it is formatted [07 R-CAM-01 §3]. The suffix test is a
// plain inequality of the two words and runs after the `Normal` branch has
// joined, so `Normal (+2)` is reachable.
func slideStripSpeedText(strip frame.StripReadout) string {
	// ActiveSpeed is the adapted current word, RequestedSpeed the target
	// [07 R-CAM-01 §3].
	text := fmt.Sprintf("%+d", strip.ActiveSpeed-slideStripNormalSpeed)
	if strip.ActiveSpeed == slideStripNormalSpeed {
		text = "Normal"
	}
	if strip.ActiveSpeed != strip.RequestedSpeed {
		text += fmt.Sprintf(" (%+d)", strip.RequestedSpeed-slideStripNormalSpeed)
	}
	return text
}
