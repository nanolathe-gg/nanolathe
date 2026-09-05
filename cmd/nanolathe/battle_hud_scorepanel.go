package main

// The score panel: its rows, the flash cadence and the side logo frames
// [07 R-HUD-03].

import (
	"fmt"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/input"
)

// drawScorePanel is the Space-held Kills/Losses panel [07 R-HUD-04 §1].
//
// The composer draws it after the world and chrome and only when the session
// kind is 2 or 3; a campaign mission never calls it, so its slide word stays
// inert there. The slide word is stepped once per composed frame with no
// wall-clock throttle — unlike the §6 bottom strip — which is why the step
// lives in the composer rather than in the host-frame update.
func (h *retailBattleHUD) drawScorePanel(c *client.Client, b *battleSession, cur *frame.Frame) {
	if h == nil || c == nil || b == nil {
		return
	}
	// The flash arrays decay whether or not the panel is showing, one step per
	// unit of the scaled timer, and arm only while the F4 interface bit is set
	// [07 R-HUD-04 §1].
	h.stepScoreFlash(cur, b.panelHoldFlag)
	if !hud.ScoreSessionKindDraws(battleSessionKind(b)) {
		return
	}
	spaceHeld := false
	if c.Input() != nil && c.Input().Kbd != nil {
		spaceHeld = c.Input().Kbd.KeyHeld(input.KeySpace)
	}
	showing := hud.ScoreShowing(b.panelHoldFlag, spaceHeld, h.editorFocused())
	visible, cue := h.score.Step(showing)
	if cue != "" {
		b.playUICue(c, cue)
	}
	if !visible || cur == nil {
		return
	}
	width, _ := c.Size()
	// The player-count word is the number of occupied player slots, which the
	// committed frame publishes one economy row per [I6].
	rect := hud.ScorePanelGeometry(int32(width), h.score, len(cur.Economy))
	c.UIShadeRect(h.pal, int(rect.X0), int(rect.Y0), int(rect.X1-rect.X0), int(rect.Y1-rect.Y0), hud.ScorePanelShadeLevel)
	// The localised headings: `Kills` at (x0+2, 32), `Losses` right-aligned at
	// (x1 - textWidth - 2, 32), both at width limit 119 and light row 0.
	h.drawScoreText(c, "Kills", int(rect.X0)+2, hud.ScorePanelTop, 0)
	lossesHeading := "Losses"
	h.drawScoreText(c, lossesHeading, int(rect.X1)-retailGAFTextWidth(h.modalFont, lossesHeading)-2, hud.ScorePanelTop, 0)

	// Rows are emitted in rank order over the ten player slots the committed
	// frame publishes every tick, filtered on the row filter's six terms
	// [07 R-HUD-04 §1]. Both the filter and the rank scan with its vacated-rank
	// compaction live in internal/hud; this loop only paints what they return.
	// The compacted rank bytes are dropped: retail writes them back to the slot
	// records, and presentation may not write simulation state [I6]. With the
	// ranks published in slot order and the kill-lead maintenance of
	// [08 R-CAMP-01 §9] not implemented (the marker is on the publisher), no
	// frame presents a vacated rank to write back.
	slots := make([]hud.ScoreSlot, frame.PlayerRowSlots)
	for i := range cur.Players {
		row := cur.Players[i]
		slots[i] = hud.ScoreSlot{
			Present:    row.Present,
			Controller: row.Controller,
			Side:       row.Side,
			LiveUnits:  row.LiveUnits,
			Auxiliary:  row.Auxiliary,
			Watcher:    row.Watcher,
			Rank:       row.Rank,
		}
	}
	order, _ := hud.ScoreRowOrder(slots, len(cur.Economy))
	for drawn, slot := range order {
		h.drawScoreRow(c, b, cur, rect, drawn, slot, cur.Players[slot])
	}
}

// stepScoreFlash arms and then decays the two flash arrays, at most once per
// committed tick [07 R-HUD-04 §1][07 R-CAM-01 §10].
//
// Arming is the kill-credit finalize's, and it happens **only while the F4
// interface bit is set** — with the bit clear the finalize skips the arm and
// both arrays stay zero, so a Space-held panel shows steady numbers. That gate
// is the F4 bit's second visible effect, and it is why nothing armed these
// arrays before [07 R-HUD-04 §1][07 R-CAM-01 §14].
//
// The finalize's own writes are the per-slot kill and loss counters the
// committed frame carries, so an increment between two committed ticks is one
// or more credited kills at that slot. A commander kill increments the
// ordinary counter too, so the commander pair needs no separate watch even in
// Deathmatch, where the panel prints it. Arming precedes the decay because the
// finalize runs inside the tick and the panel routine decays afterwards.
func (h *retailBattleHUD) stepScoreFlash(cur *frame.Frame, armed bool) {
	if cur == nil {
		return
	}
	if h.scoreFlashTickOK && h.scoreFlashTick == cur.Tick {
		return
	}
	h.scoreFlashTick, h.scoreFlashTickOK = cur.Tick, true
	for i := range cur.Players {
		kills, losses := cur.Players[i].Kills, cur.Players[i].Losses
		if h.scoreCountersOK && armed {
			if kills > h.scorePrevKills[i] {
				h.scoreFlash.Credit(i, -1)
			}
			if losses > h.scorePrevLosses[i] {
				h.scoreFlash.Credit(-1, i)
			}
		}
		h.scorePrevKills[i], h.scorePrevLosses[i] = kills, losses
	}
	h.scoreCountersOK = true
	h.scoreFlash.Decay()
}

// drawScoreRow paints one player's row: the local player's two lightening
// passes, the player name, and the kill and loss counts with their flash
// brightness [07 R-HUD-04 §1].
func (h *retailBattleHUD) drawScoreRow(c *client.Client, b *battleSession, cur *frame.Frame, rect hud.ScorePanelRect, drawn, slot int, row frame.PlayerRow) {
	y := hud.ScoreRowTop(drawn)
	if slot >= 0 && slot < hud.ScorePanelSlots && uint8(slot) == cur.Selection.LocalPlayer {
		// (x0+4, y-1)-(x1-4, y+38), lightened at 31 then 20.
		x, w := int(rect.X0)+4, int(rect.X1-rect.X0)-8
		top, height := int(y)-1, 40
		c.UILightRect(h.pal, x, top, w, height, hud.ScorePanelLocalLightA)
		c.UILightRect(h.pal, x, top, w, height, hud.ScorePanelLocalLightB)
	}
	// The row's side logo: the frame numbered by the lobby record's logo byte,
	// quad mapped from the frame *interior* — source corners (1,1) (w-1,1)
	// (w-1,h-1) (1,h-1) — onto (x0+7, y+1)-(x0+119, y+37), i.e. stretched to
	// 112 x 36 [07 R-HUD-04 §1][03 R-RAST-01 §1]. The entry is `32xlogos` of
	// textures/logos.gaf [07 R-HUD-04 §4], which is the same handle the
	// footer's LOGO2 draw and the result surface read.
	if logo := h.sideLogoFrame(row.Logo); logo != nil {
		width, height := c.Size()
		c.UIBlitFrameSourceRectScaledClipped(logo, 1, 1, int(logo.Width)-1, int(logo.Height)-1,
			int(rect.X0)+7, int(y)+1, hud.ScorePanelLogoWidth, hud.ScorePanelLogoHeight,
			0, 0, width, height)
	}
	h.drawScoreText(c, row.Name, int(rect.X0)+9, int(y)+6, 0)
	kills, losses := hud.ScoreCounters(row, commanderDeathOption(b))
	killText := fmt.Sprintf("%d", kills)
	lossText := fmt.Sprintf("%d", losses)
	killShade, lossShade := 0, 0
	if slot >= 0 && slot < hud.ScorePanelSlots {
		killShade = int(h.scoreFlash.Kills[slot])
		lossShade = int(h.scoreFlash.Losses[slot])
	}
	h.drawScoreText(c, killText, int(rect.X0)+9, int(y)+21, killShade)
	h.drawScoreText(c, lossText, int(rect.X0)+119-retailGAFTextWidth(h.modalFont, lossText)-2, int(y)+21, lossShade)
}

// sideLogoFrame resolves the side-logo frame for a lobby colour byte. Both
// battle logo draws — the footer's LOGO2 and the score panel's row logo — read
// one GAF handle bound during battle-data initialization, and the frame index
// is the owner's lobby colour byte:
//
//	frame = logos.gaf["32xlogos"].Frames[lobbyColour]
//
// [07 R-HUD-04 §4]. An absent file or an out-of-range colour draws nothing;
// the art is retail content, and this seam does not substitute for it.
func (h *retailBattleHUD) sideLogoFrame(colour uint8) *formats.GAFFrame {
	if h == nil || h.logos == nil {
		return nil
	}
	entry, ok := h.logos.Find(sideLogoEntry)
	if !ok || int(colour) >= len(entry.Frames) {
		return nil
	}
	return entry.Frames[colour].Frame
}

// sideLogoEntry is the GAF entry both battle logo draws address
// [07 R-HUD-04 §4].
const sideLogoEntry = "32xlogos"

// drawScoreText is the panel's text writer: the GAF font, the 119-pixel width
// limit, and the light-table row as the brightness argument
// [07 R-HUD-04 §1][03 R-FONT-01 §6].
func (h *retailBattleHUD) drawScoreText(c *client.Client, text string, x, y, shade int) {
	if text == "" || h.modalFont == nil {
		return
	}
	drawRetailGAFTextLit(c, h.modalFont, text, x, y, hud.ScorePanelTextLimit, h.pal, shade)
}
