package main

// `HELP.GUI`, the keyboard-command reference ARMOPT's `HELP` opens
// [07 R-FE-01 §7].
//
// The window authors `OK` and a three-stage `Page` button; its rows come from
// `gamedata\help.tdf` section `[Help]`. Page `p` reads keys `Line<n>` for
// `n = 17p … 17p+16`. Each value splits at its first `|` into a key column at
// x 40 of width 78 and a description column at x 125 of width 300, with the
// first row at y 50 and each next row 18 lower; both columns are localised.
// A key the section does not hold consumes no row, and a value that opens
// with `|` prints a single space in the key column [fmt tdf "HELP.TDF"].

import (
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The page filler's constants [07 R-FE-01 §7].
const (
	helpLinesPerPage = 17
	helpKeyX         = 40
	helpKeyW         = 78
	helpDescX        = 125
	helpDescW        = 300
	helpFirstY       = 50
	helpStepY        = 18
	// helpBlankKey is what a value opening with `|` prints in the key column:
	// the buffer is replaced by a single space before the split.
	helpBlankKey = " "
)

// battleHelpLine is one resolved row of the page.
type battleHelpLine struct {
	Key         string
	Description string
}

// battleHelpPage reads one page of `[Help]` rows. Absent keys are skipped and
// consume no row slot [07 R-FE-01 §7].
func battleHelpPage(fs vfs.FSOps, page int) []battleHelpLine {
	if fs == nil || page < 0 {
		return nil
	}
	doc, err := formats.LoadTDF(fs, "gamedata/help.tdf")
	if err != nil || doc == nil || doc.Root == nil {
		return nil
	}
	section := doc.Root.Section("Help")
	if section == nil {
		return nil
	}
	lines := make([]battleHelpLine, 0, helpLinesPerPage)
	for n := page * helpLinesPerPage; n < page*helpLinesPerPage+helpLinesPerPage; n++ {
		value, ok := section.FirstValue("Line" + strconv.Itoa(n))
		if !ok {
			continue
		}
		lines = append(lines, splitBattleHelpLine(value))
	}
	return lines
}

// splitBattleHelpLine applies the filler's two cases: a value whose first byte
// is `|` becomes a single-space key column with the rest as the description,
// and any other value is cut at its first `|` past that byte
// [07 R-FE-01 §7][fmt tdf "HELP.TDF"].
func splitBattleHelpLine(value string) battleHelpLine {
	if value == "" {
		return battleHelpLine{}
	}
	if strings.HasPrefix(value, "|") {
		return battleHelpLine{Key: helpBlankKey, Description: value[1:]}
	}
	if cut := strings.Index(value[1:], "|"); cut >= 0 {
		return battleHelpLine{Key: value[:cut+1], Description: value[cut+2:]}
	}
	return battleHelpLine{Key: value}
}

// openBattleHelpWindow fills the window with one page. Every fill first
// restores the authored record set, which is what the page filler's saved
// gadget count does [07 R-FE-01 §7].
func (b *battleSession) openBattleHelpWindow(page int) {
	if b == nil || b.hud == nil {
		return
	}
	h := b.hud
	window := h.info.helpWin
	if window == nil {
		return
	}
	if page < 0 {
		page = 0
	}
	truncateBattleInfoWindow(window, h.info.helpAuthored)
	captions := hudCaptionTranslator(h)
	translate := func(text string) string {
		if captions == nil {
			return text
		}
		return captions.Translate(text)
	}
	lines := battleHelpPage(h.fs, page)
	y := helpFirstY
	for _, line := range lines {
		appendBattleInfoLabel(window, translate(line.Key), helpKeyX, y, helpKeyW)
		appendBattleInfoLabel(window, translate(line.Description), helpDescX, y, helpDescW)
		y += helpStepY
	}
	h.info.helpPage, h.info.helpLines = page, lines
	h.installWindow(window, nil)
	h.applyBattleInfoPlacement(window, true)
	h.info.helpPanel = ui.NewPanel(window)
	// The window is rebuilt around the authored record set, so the selected
	// stage of `Page` is restored onto the fresh widget state — the filler
	// itself never moves it [07 R-WGT-01 §3].
	if index := h.info.helpPanel.Index("Page"); index >= 0 {
		h.info.helpPanel.SetStageAt(index, page)
	}
}

// refillBattleHelpPage is `Page`'s action: the button has already advanced its
// own stage, and the page it now selects is the page the window loads
// [07 R-FE-01 §7].
func (b *battleSession) refillBattleHelpPage() {
	if b == nil || b.hud == nil || b.hud.info.helpPanel == nil {
		return
	}
	page := 0
	if index := b.hud.info.helpPanel.Index("Page"); index >= 0 {
		page = b.hud.info.helpPanel.StageAt(index)
	}
	b.openBattleHelpWindow(page)
}
