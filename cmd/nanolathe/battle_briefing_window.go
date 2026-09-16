package main

// `BRIEFING.GUI`, the in-battle briefing ARMOPT's `MISSION` opens in a
// campaign [07 R-FE-01 §7].
//
// It is the same mission text and the same pager as `MSNBRIEF.GUI` over the
// background `igmbrief`, with `OK` closing it [07 R-FE-01 §4]. Two things
// separate it from the front-end screen: the opener clears the inert-label
// attribute bit on `MOREBAR` and `TextRegion`, so both are ordinary fired
// gadgets here rather than rectangles the screen hit-tests itself; and the
// narration the shared text installer starts is skipped in a battle, because
// that request is gated on the host mode word the in-battle briefing runs
// under [07 R-FE-02 §2][07 R-FE-01 §4].

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// battleBriefingInertBit is the label attribute bit the opener clears on
// `MOREBAR` and `TextRegion` [07 R-FE-01 §7][07 R-WGT-01 §7].
const battleBriefingInertBit = 0x10

// battleLocalSide is the local player's authored side ordinal — the record
// `SINGLE.GUI` writes the registry `side` word into, and the one the briefing
// text region's font number is derived from [07 R-FE-01 §4][08 R-CAMP-01 §2].
func battleLocalSide(b *battleSession) int {
	if b == nil || b.sess == nil {
		return 0
	}
	slot := int(b.sess.LocalOwner)
	if slot < 0 || slot >= len(b.sess.Skirmish.Players) {
		return 0
	}
	side := b.sess.Skirmish.Players[slot].Side
	if side < 0 {
		return 0
	}
	return side
}

// openBattleBriefingWindow builds the window's retained widget state and
// installs the mission text into the shared pager.
func (b *battleSession) openBattleBriefingWindow() {
	if b == nil || b.hud == nil || b.sess == nil {
		return
	}
	h := b.hud
	window := h.info.briefingWin
	if window == nil {
		return
	}
	if !h.info.briefingBuilt {
		for i := range window.Gadgets {
			switch gui.CallbackName(window.Gadgets[i].Name) {
			case "MOREBAR", "TextRegion":
				window.Gadgets[i].Attribs &^= battleBriefingInertBit
			}
		}
		h.installWindow(window, nil)
		h.info.briefingBuilt = true
	}
	h.info.briefingPanel = ui.NewPanel(window)

	side := battleLocalSide(b)
	// The controller is the campaign briefing screen's own pager. It is
	// constructed without a CRT source: the wind draws belong to `MSNBRIEF`'s
	// entry, and nothing in this window prints wind [08 R-CAMP-01 §2].
	controller := NewCampaignBriefingController(b.sess.Mission, side, nil, nil)
	h.info.briefing = controller
	h.info.briefingFont = window.Font(h.fs, uint8(side+1))
	if b.sess.Mission != nil && b.sess.Mission.OTA != nil {
		globals := mission.DecodeMissionGlobals(b.sess.Mission.OTA.Global)
		controller.text = battleBriefingText(b, globals.Brief)
	}
	index := window.GadgetIndex("TextRegion")
	if index < 0 {
		return
	}
	rect := window.PlacedRect(index)
	controller.SetTextRegion(controller.text, int(rect.W), int(rect.H), h.battleBriefingTextHeight(), h.battleBriefingTextWidth)
}

// battleBriefingText reads the mission's authored slot-2 briefing file through
// the same `camps\briefs` path the campaign screen uses [08 R-CAMP-01 §2].
func battleBriefingText(b *battleSession, name string) string {
	if b == nil || b.hud == nil || b.hud.fs == nil {
		return ""
	}
	logical := briefingMediaPath(name, "txt")
	if logical == "" {
		return ""
	}
	data, err := b.hud.fs.ReadFileLimit(logical, int64(formats.DefaultTDFLimits().MaxBytes))
	if err != nil {
		return ""
	}
	return string(data)
}

// battleBriefingTextWidth and battleBriefingTextHeight are the text region
// font's metrics. The wrapper, the lines-per-page divide and the run pen all
// measure through the same font [07 R-FE-02 §6][07 R-HUD-03 §10].
func (h *retailBattleHUD) battleBriefingTextWidth(text string) int {
	if h == nil {
		return len(text)
	}
	if h.info.briefingFont != nil {
		return client.MeasureText(h.info.briefingFont, text)
	}
	if h.modalFont != nil {
		return retailGAFTextWidth(h.modalFont, text)
	}
	if h.guiFont != nil {
		return client.MeasureText(h.guiFont, text)
	}
	return len(text)
}

func (h *retailBattleHUD) battleBriefingTextHeight() int {
	if h == nil {
		return 1
	}
	if h.info.briefingFont != nil {
		return int(h.info.briefingFont.Height)
	}
	if h.modalFont != nil {
		return retailGAFTextHeight(h.modalFont)
	}
	if h.guiFont != nil {
		return int(h.guiFont.Height)
	}
	return 1
}

// pageBattleBriefing advances the pager. Either `TextRegion` or `MOREBAR`
// reaches it [07 R-FE-01 §7][07 R-HUD-03 §10].
func (b *battleSession) pageBattleBriefing() {
	if b == nil || b.hud == nil || b.hud.info.briefing == nil {
		return
	}
	_, _ = b.hud.info.briefing.Dispatch(BriefingActionMore)
}

// drawBattleBriefingText emits the current page's labels and the `MOREBAR`
// caption. The pager's labels are laid at
// `(regionX + 5, regionY + (fontHeight+2)/2 + i × (fontHeight+2))`, in the
// side's plain text colour, with each `&X…&` run drawn over the label at the
// pen the pager measured [07 R-HUD-03 §10][07 R-FE-02 §7].
func (h *retailBattleHUD) drawBattleBriefingText(c *client.Client, b *battleSession, window *gui.Window) {
	if h == nil || c == nil || b == nil || window == nil || window != h.info.briefingWin {
		return
	}
	controller := h.info.briefing
	if controller == nil {
		return
	}
	now := int64(0)
	if b.millisSource != nil {
		now = int64(b.millisSource.Millis32())
	}
	controller.Update(now, briefingPresentationTick(now))
	if index := window.GadgetIndex("MOREBAR"); index >= 0 {
		r := window.PlacedRect(index)
		h.drawBattleBriefingLine(c, controller.MoreCaption(), int(r.X)+5, int(r.Y), controller.CaptionColor())
	}
	index := window.GadgetIndex("TextRegion")
	if index < 0 {
		return
	}
	r := window.PlacedRect(index)
	step := h.battleBriefingTextHeight() + 2
	x := int(r.X) + 5
	plain := controller.PlainColor()
	for i, line := range controller.Lines() {
		y := int(r.Y) + step/2 + i*step
		h.drawBattleBriefingLine(c, line.Text, x, y, plain)
		for _, run := range line.Runs {
			h.drawBattleBriefingLine(c, run.Text, x+run.X, y, run.Color(controller.localSide))
		}
	}
}

// drawBattleBriefingLine draws one laid label. The pager gives its labels no
// width, so nothing here truncates: the wrapper is what keeps a line inside
// the region [07 R-HUD-03 §10].
func (h *retailBattleHUD) drawBattleBriefingLine(c *client.Client, text string, x, y int, color byte) {
	if c == nil || text == "" {
		return
	}
	if h.info.briefingFont != nil {
		width, _ := c.Size()
		c.UITextWidth(h.info.briefingFont, text, x, y, width-x, color)
		return
	}
	if h.modalFont != nil {
		width, _ := c.Size()
		drawRetailGAFTextClipped(c, h.modalFont, text, x, y, width-x, 0, 0, width, width)
		return
	}
	if h.guiFont != nil {
		c.UITextWidth(h.guiFont, text, x, y, -1, color)
	}
}
