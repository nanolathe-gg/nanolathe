package main

// ARMOPT's two read-only children and their shared plumbing.
//
// `MISSION` opens `BRIEFING.GUI` in a campaign and `GAMEOPTIONS.GUI`
// otherwise; `HELP` opens `HELP.GUI`. All three open over the surviving
// options root, which keeps the pause bit `ARMOPT` set, and each one's `OK`
// returns to that root [07 R-FE-01 §7][07 R-WGT-01 §1].
//
// The windows print their content as run-time labels: a kind-5 record at the
// given position with height 15, `colorf` 15, the given attribute word and the
// given text. `GAMEOPTIONS` rows and `HELP` lines are both made this way
// [07 R-FE-02 §5 "synthesised gadgets"]. Because the rows are appended to the
// parsed record, every refill first truncates the gadget list back to the
// authored count the window was opened with, exactly as the `HELP` page filler
// does.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// battleInfoLabelHeight and battleInfoLabelColor are the appended label
// record's fixed fields [07 R-FE-02 §5].
const (
	battleInfoLabelHeight = 15
	battleInfoLabelColor  = 15
	// battleInfoLabelAttribs is the attribute word the rows are drawn with.
	// Both openers pass 2 to the append helper and then immediately rewrite
	// every appended record's attribute word to 1, so the value that ever
	// reaches a draw is the LEFT bit: the rows are left-aligned at their
	// column x, not centred. Storing the surviving value keeps the record the
	// painter sees identical to retail's without modelling a rewrite whose
	// only observable is this word [07 R-FE-01 §7][03 R-FONT-01 §6].
	battleInfoLabelAttribs = 1
)

// battleInfoWindows holds the three parsed records, their retained widget
// state and the row content each one prints.
type battleInfoWindows struct {
	briefingWin    *gui.Window
	gameOptionsWin *gui.Window
	helpWin        *gui.Window

	briefingPanel    *ui.Panel
	gameOptionsPanel *ui.Panel
	helpPanel        *ui.Panel

	// authored counts captured before any row is appended, so a refill can
	// restore the window to the record the loader produced.
	gameOptionsAuthored int
	helpAuthored        int

	// briefingBuilt records that the one-time attribute rewrite the in-battle
	// briefing performs on `MOREBAR` and `TextRegion` has run.
	briefingBuilt bool

	// helpPage is the stage `Page` last selected; briefing is the shared
	// wrap/blink/pager the campaign briefing screen uses.
	helpPage     int
	helpLines    []battleHelpLine
	briefing     *campaignBriefingController
	briefingFont *formats.FNT

	// backdrops caches each authored background bitmap. A nil value records a
	// decode that failed, so a missing optional image is looked up once.
	backdrops map[string]*formats.PCX
}

func loadBattleInfoWindows(fs vfs.FSOps, captions gui.CaptionTranslator) battleInfoWindows {
	info := battleInfoWindows{
		briefingWin:    loadGUIOptional(fs, "guis/briefing.gui", "briefing.gui [07 R-FE-01 §7]", captions),
		gameOptionsWin: loadGUIOptional(fs, "guis/gameoptions.gui", "gameoptions.gui [07 R-FE-01 §7][08 R-SKIR-01 §11]", captions),
		helpWin:        loadGUIOptional(fs, "guis/help.gui", "help.gui [07 R-FE-01 §7]", captions),
	}
	if info.gameOptionsWin != nil {
		info.gameOptionsAuthored = len(info.gameOptionsWin.Gadgets)
	}
	if info.helpWin != nil {
		info.helpAuthored = len(info.helpWin.Gadgets)
	}
	return info
}

// infoBackdrop decodes one authored background bitmap. The request carries no
// apply-palette flag, so the image is drawn in the battle's own palette and no
// display palette is installed [07 R-FE-02 §2].
func (h *retailBattleHUD) infoBackdrop(name string) *formats.PCX {
	if h == nil || h.fs == nil || name == "" {
		return nil
	}
	if h.info.backdrops == nil {
		h.info.backdrops = make(map[string]*formats.PCX)
	}
	if image, ok := h.info.backdrops[name]; ok {
		return image
	}
	logical := "bitmaps/" + name + ".pcx"
	image, err := formats.LoadPCXFile(h.fs, logical)
	if err != nil {
		hudAssetWarning(h.fs, logical, "in-battle window background [07 R-FE-01 §7]", err)
		image = nil
	}
	h.info.backdrops[name] = image
	return image
}

// truncateBattleInfoWindow restores the window to its authored record set.
// The `HELP` page filler writes the saved gadget count back before it appends
// the next page's labels, and the `GAMEOPTIONS` opener appends onto a freshly
// opened window; both are expressed here as the same restore [07 R-FE-01 §7].
func truncateBattleInfoWindow(window *gui.Window, authored int) {
	if window == nil || authored <= 0 || len(window.Gadgets) <= authored {
		return
	}
	window.Gadgets = window.Gadgets[:authored]
}

// appendBattleInfoLabel is the append-label helper both windows use. A width
// of -1 takes `panelWidth - x - 5` [07 R-FE-02 §5].
func appendBattleInfoLabel(window *gui.Window, text string, x, y, width int) {
	if window == nil {
		return
	}
	if width < 0 {
		width = int(window.Rect.W) - x - 5
	}
	window.Gadgets = append(window.Gadgets, gui.Gadget{
		Kind:    gui.KindLabel,
		Rect:    gui.Rect{X: int32(x), Y: int32(y), W: int32(width), H: battleInfoLabelHeight},
		Attribs: battleInfoLabelAttribs,
		ColorF:  battleInfoLabelColor,
		Active:  1,
		Text:    text,
	})
}

// battleInfoWindow selects the open child's record, retained widget state and
// authored background name.
func (b *battleSession) battleInfoWindow() (*gui.Window, *ui.Panel, string) {
	if b == nil || b.hud == nil || b.battleState() == nil {
		return nil, nil, ""
	}
	h := b.hud
	switch b.battleState().Modal() {
	case ui.BattleModalBriefing:
		return h.info.briefingWin, h.info.briefingPanel, "igmbrief"
	case ui.BattleModalGameOptions:
		return h.info.gameOptionsWin, h.info.gameOptionsPanel, "GameSettings"
	case ui.BattleModalHelp:
		return h.info.helpWin, h.info.helpPanel, "dhelp"
	}
	return nil, nil, ""
}

// battleInfoWindowActive reports whether one of ARMOPT's read-only children
// owns the top of the modal chain.
func (b *battleSession) battleInfoWindowActive() bool {
	if b == nil || b.battleState() == nil {
		return false
	}
	switch b.battleState().Modal() {
	case ui.BattleModalBriefing, ui.BattleModalGameOptions, ui.BattleModalHelp:
		return true
	}
	return false
}

// openBattleInfoWindow completes the transition ARMOPT's `MISSION` or `HELP`
// already selected: it builds the window's rows and its retained widget state.
func (b *battleSession) openBattleInfoWindow() {
	if b == nil || b.hud == nil || b.battleState() == nil {
		return
	}
	switch b.battleState().Modal() {
	case ui.BattleModalBriefing:
		b.openBattleBriefingWindow()
	case ui.BattleModalGameOptions:
		b.openBattleGameOptionsWindow()
	case ui.BattleModalHelp:
		b.openBattleHelpWindow(0)
	}
}

// handleBattleInfoWindowInput drives the open child through the same indexed
// widget service the other ordinary battle children use. It reports whether
// the pass claimed the event; an unclaimed one falls through to the battle
// caller's own modal rows [07 R-WGT-01 §§1-3].
func (b *battleSession) handleBattleInfoWindowInput(in *input.State, cl *client.Client) bool {
	window, panel, _ := b.battleInfoWindow()
	if window == nil || panel == nil || in == nil || in.Mouse == nil {
		return false
	}
	result := b.serviceBattleChildPanel(panel, window, nil, in)
	if result.Fired && result.FiredIndex >= 0 && result.FiredIndex < len(window.Gadgets) {
		b.activateBattleMenuButton(window.Gadgets[result.FiredIndex].Name, cl)
		return true
	}
	return result.ConsumedTokens != 0
}

// drawBattleInfoWindow paints the open child over the surviving options root.
//
// It is not the shared modal painter for one reason: these three windows are
// the only battle children that install an authored background bitmap, which
// replaces the window's panel fill [07 R-FE-01 §7][07 R-FE-02 §2]. Everything
// else — the art chain, the greyed shade and the two text pens — is the
// battle modal family's.
func (h *retailBattleHUD) drawBattleInfoWindow(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil {
		return
	}
	window, panel, backdrop := b.battleInfoWindow()
	// A child with no retained widget state was never opened; the draw path
	// never builds one [07 R-WGT-01 §1][07 R-WGT-02 §2].
	if window == nil || panel == nil || window.Rect.W <= 0 || window.Rect.H <= 0 {
		return
	}
	clip := window.Rect
	if image := h.infoBackdrop(backdrop); image != nil {
		// The background bitmap lands in the window's own surface at its
		// origin; anything past the rectangle is not part of the window
		// [07 R-FE-02 §2][07 §4].
		c.UIBlitPCXClipped(image, int(clip.X), int(clip.Y), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
	} else {
		drawWindowPanel(c, window, nil, h.common, h.guiColor)
	}
	for i, gad := range window.Gadgets {
		if i == 0 || gad.Kind == gui.KindFont || gad.Kind == gui.KindPanel {
			continue
		}
		if panel != nil && !panel.ActiveAt(i) {
			continue
		}
		r := window.PlacedRect(i)
		down, stage := int(gad.Status), 0
		if panel != nil {
			down, stage = panel.DownAt(i), panel.StageAt(i)
		}
		grey := gad.GrayedOut&1 != 0
		if frame := h.modalGadgetFrameState(gad, nil, down, stage, grey); frame != nil {
			c.UIBlitClipped(frame, int(r.X), int(r.Y), int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
		} else if gad.Kind == gui.KindButton {
			v := retailButtonVerdict(gad, 0, int(gad.ArtFrame), down, stage, grey)
			drawGUIBevelClipped(c, r, h.guiColor(v.top), h.guiColor(v.bot), h.guiColor(v.fill), clip)
		}
		text := gad.Text
		if gad.Kind == gui.KindButton {
			text = retailBattleButtonText(gad, panel, i, stage)
		}
		if text == "" {
			continue
		}
		if gad.Kind != gui.KindButton && gad.Kind != gui.KindLabel {
			continue
		}
		// The gadget's `fontnumber` selects one of the window's own kind-7
		// records. Which family draws is decided by whether a record MATCHED,
		// not by whether its file loaded: a matched record whose file is
		// unreadable still takes the FNT branch and simply leaves the common
		// font active [07 R-WGT-01 §12][03 R-FONT-01 §5][03 R-FONT-01 §6].
		selected := window.Font(h.fs, gad.FontNumber)
		if gad.Kind == gui.KindLabel {
			h.drawBattleInfoLabel(c, window, gad, r, text, selected, clip)
			continue
		}
		fallback := selected
		if fallback == nil {
			fallback = h.guiFont
		}
		var textWidth, metric int
		switch {
		case h.modalFont != nil:
			textWidth, metric = retailGAFTextWidth(h.modalFont, text), retailGAFTextHeight(h.modalFont)
		case fallback != nil:
			textWidth, metric = client.MeasureText(fallback, text), int(fallback.Height)
		default:
			continue
		}
		flash := uint16(0)
		if panel != nil {
			flash = panel.FlashRow(i)
		}
		h.drawBattleButtonCaption(c, clip, gad, r, text, selected, textWidth, metric, flash)
	}
	// The briefing's laid text region is not a label the window owns; the
	// pager writes it directly [07 R-HUD-03 §10].
	h.drawBattleBriefingText(c, b, window)
}

// drawBattleInfoLabel is the kind-5 label pen of [03 R-FONT-01 §6]. It is not
// the button pen the shared battle-modal painter also uses for labels: a label
// has no 3-pixel inset and no vertical centring — its pen y IS the gadget's y
// — and its width limit depends on which family the painter's font walk
// selected.
//
//   - a `fontnumber` that matched one of the window's kind-7 records draws
//     through the FNT drawer with the width limit DROPPED (a matched record
//     whose file did not load leaves the common font active, which is what a
//     nil `selected` means here);
//   - a `fontnumber` that matched none draws through the GAF pen with the
//     limit set to the gadget width, so a row wider than its column is
//     truncated at the column edge with nothing appended. Neither
//     `GAMEOPTIONS.GUI` nor `HELP.GUI` authors a font record, so that is the
//     branch every printed row takes [07 R-FE-01 §7].
func (h *retailBattleHUD) drawBattleInfoLabel(c *client.Client, window *gui.Window, gad gui.Gadget, r gui.Rect, text string, selected *formats.FNT, clip gui.Rect) {
	if h == nil || c == nil || window == nil || text == "" {
		return
	}
	if window.FontRecord(gad.FontNumber) != gui.NoFontRecord {
		font := selected
		if font == nil {
			font = h.guiFont
		}
		if font != nil {
			h.drawModalLabelFNT(c, window, gad, r, text, font)
		}
		return
	}
	if h.modalFont == nil {
		// The GAF pen's null-slot fallback calls the FNT drawer and drops the
		// caller's width limit, which is exactly the FNT branch above
		// [03 R-FONT-01 §6].
		if h.guiFont != nil {
			h.drawModalLabelFNT(c, window, gad, r, text, h.guiFont)
		}
		return
	}
	textWidth := retailGAFTextWidth(h.modalFont, text)
	gx, gy, w := int(r.X), int(r.Y), int(r.W)
	if gad.Rect.RawX == -1 {
		gx = int(window.Rect.X) + (int(window.Rect.W)-textWidth)/2
	}
	penX := gx
	switch {
	case gad.Attribs&4 != 0:
		penX = gx + w - textWidth
	case gad.Attribs&2 != 0:
		penX = gx + w/2 - textWidth/2
	}
	// TODO(T23): the painter wraps instead when twice the line metric is less
	// than the label height minus one, and no GAF wrapper exists here yet. No
	// row these three windows print can reach it — the appended height is 15
	// and the GAF metric is never below 7 — so the single-line pen stands.
	drawRetailGAFTextClipped(c, h.modalFont, text, penX, gy, w, int(clip.X), int(clip.Y), int(clip.W), int(clip.H))
}
