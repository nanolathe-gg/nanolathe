package main

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// drawResultOverlay renders the retail victory/defeat overlay [07 §11][RS-05][08][P1-01]
// using authored igtitles GAF frames via the same intgaf/gui machinery as HUD panels [07 §6][07 §11].
// It replaces the custom text chrome with the retail art while keeping scores where retail shows them.
// Kills/Losses remain behind the snapshot view field with TODO(question) where retail establishes counters [P1-01 §2.3].
func (h *retailBattleHUD) drawResultOverlay(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || !b.isResultVisible() {
		return
	}
	view := b.resultView()
	// Dim the scene — retail copies last frame beneath; we fill with palette 1 [07 §11] "Copy of last game frame"
	c.UIFillRect(0, 0, 640, 480, 1)
	// Centered message-box-style surface [07 §11] "Copy of last game frame" / "Click to continue."
	bx, by, bw, bh := 80, 80, 480, 320
	c.UIFillRect(bx, by, bw, bh, 8)
	c.UIFillRect(bx, by, bw, 2, 15)
	c.UIFillRect(bx, by+bh-2, bw, 2, 15)
	c.UIFillRect(bx, by, 2, bh, 15)
	c.UIFillRect(bx+bw-2, by, 2, bh, 15)

	// Header strings [07 §11] - last game frame copy indication
	if h.console != nil {
		hdr := "Copy of last game frame"
		tx := bx + (bw-client.MeasureText(h.console, hdr))/2
		c.UIText(h.console, hdr, tx, by+6, 7)
	}

	// Title frame from igtitles: igvictory / igdefeat / draw fallback [07 §11] gated by mode-word bits 5/6 and pause bit
	title := "VICTORY"
	if view.Draw {
		title = "DRAW"
	} else if view.Kind == "defeat" {
		title = "DEFEAT"
	} else if view.Kind == "victory" {
		title = "VICTORY"
	} else if view.WinnerTeam != b.sess.TeamForOwner(int(b.sess.LocalOwner)) && view.WinnerTeam != -1 {
		title = "DEFEAT"
	}
	var titleFrame = h.victoryFrame
	if title == "DEFEAT" {
		titleFrame = h.defeatFrame
	} else if title == "DRAW" {
		titleFrame = nil
	}
	if titleFrame != nil {
		x := (640 - int(titleFrame.Width)) / 2
		y := by + 18
		c.UIBlit(titleFrame, x, y)
		// Keep text fallback for accessibility even when art is present? Retail shows only GAF; we keep text small beneath
		if h.console != nil {
			tx := bx + (bw-client.MeasureText(h.console, title))/2
			c.UIText(h.console, title, tx, y+int(titleFrame.Height)+4, 15)
		}
	} else if h.console != nil {
		tx := bx + (bw-client.MeasureText(h.console, title))/2
		c.UIText(h.console, title, tx, by+22, 15)
	}

	yBase := by + 50
	if titleFrame != nil {
		yBase = by + 18 + int(titleFrame.Height) + 18
	}
	if h.console != nil {
		reason := view.Reason
		if reason == "" {
			reason = "commander_death"
		}
		c.UIText(h.console, reason, bx+10, yBase, 7)
		c.UIText(h.console, fmt.Sprintf("Tick %d Armed %d Countdown %d", view.Tick, view.ArmedTick, view.Countdown), bx+10, yBase+12, 7)
		wStr := fmt.Sprintf("Winners: %v", view.Winners)
		if view.Draw {
			wStr = "Winners: none (draw)"
		}
		c.UIText(h.console, wStr, bx+10, yBase+24, 7)
		c.UIText(h.console, fmt.Sprintf("Losers: %v", view.Losers), bx+10, yBase+36, 7)
	}

	// End-mission statistics screen per [07 §11]/P1-01: 58-byte-style stat rows up to ten players.
	// Populate rows ONLY from data sim already publishes; anything retail-establishes-but-we-don't-track is TODO(question) placeholder [07 §11].
	h.drawResultStatistics(c, b, view, bx, yBase+52, bw, bh-(yBase+52-by))

	// Footer "Click to continue." after delay gate [07 §11]; we show immediately (delay not established)
	if h.console != nil {
		footer := "Click to continue."
		tx := bx + (bw-client.MeasureText(h.console, footer))/2
		c.UIText(h.console, footer, tx, by+bh-14, 7)
	}

	// Buttons [RS-05] 6→7→2 graph
	b.ensureResultButtons()
	for _, btn := range b.resultButtons {
		c.UIFillRect(int(btn.X), int(btn.Y), panelButtonW, panelButtonH, 4)
		c.UIFillRect(int(btn.X), int(btn.Y), panelButtonW, 2, 15)
		c.UIFillRect(int(btn.X), int(btn.Y+panelButtonH-2), panelButtonW, 2, 15)
		c.UIFillRect(int(btn.X), int(btn.Y), 2, panelButtonH, 15)
		c.UIFillRect(int(btn.X+panelButtonW-2), int(btn.Y), 2, panelButtonH, 15)
		if h.console != nil {
			tx := int(btn.X) + (panelButtonW-client.MeasureText(h.console, btn.Name))/2
			ty := int(btn.Y) + 6
			c.UIText(h.console, btn.Name, tx, ty, 15)
		}
	}
}

// drawResultStatistics renders per-player statistics rows [07 §11] P1-01.
// Categories: Kills, Losses, EProduced, MProduced, EWasted, MWasted, Score gated per player by enable-byte table.
// Retail animates seven categories one at a time behind +10-tick gate substate 0→6 with EndGameStatBar/EndGameScore cues; we render all rows statically as TODO(question): animation cadence not wired.
// Data sources: ResultView.Scores for Kills/Losses/Score (kills/losses currently 0 placeholder TODO(question) [P1-01 §2.3]); snapshot Economy/Resources for E/M Produced/Consumed/Wasted where available.
func (h *retailBattleHUD) drawResultStatistics(c *client.Client, b *battleSession, view snapshot.ResultView, bx, y, bw, bh int) {
	if h == nil || c == nil || b == nil || h.console == nil {
		return
	}
	// Build lookup for economy per player if available via snapshot
	curEconomy := map[int]snapshot.EconomyView{}
	curResources := map[int]snapshot.ResourceView{}
	if _, cur, ok := c.Buffer().Read(); ok && cur != nil {
		for _, e := range cur.Economy {
			curEconomy[int(e.Player)] = e
		}
		for _, r := range cur.Resources {
			curResources[int(r.Player)] = r
		}
	} else if b.sess != nil && b.sess.Snapshot != nil {
		if _, cur2, ok2 := b.sess.Snapshot.Read(); ok2 && cur2 != nil {
			for _, e := range cur2.Economy {
				curEconomy[int(e.Player)] = e
			}
			for _, r := range cur2.Resources {
				curResources[int(r.Player)] = r
			}
		}
	}
	// Enable-byte gating: retail uses enable-byte table per player; we gate on Scores existence or Economy existence [07 §11]
	yPos := y
	lineH := 12
	// Header for statistics
	if yPos+lineH < y+bh-30 {
		c.UIText(h.console, "Statistics: Kills Losses EProd MProd EWaste MWaste Score", bx+10, yPos, 10)
		yPos += lineH
	}
	for _, sc := range view.Scores {
		if yPos+lineH > y+bh-30 {
			break
		}
		// EProduced/MProduced from economy if present else placeholder
		eProd := 0
		mProd := 0
		eWaste := 0
		mWaste := 0
		// TODO(question): retail establishes EProduced/MProduced/EWasted/MWasted counters [07 §11] but sim does not yet track them authoritatively; we map EnergyProduced/MetalProduced and use 0 placeholders for waste until ledger wired [P1-01 §2.3] [05 "Player slot"].
		if ev, ok := curEconomy[sc.Player]; ok {
			eProd = int(ev.EnergyProduced)
			mProd = int(ev.MetalProduced)
			// EWasted/MWasted not tracked; use placeholder 0 with TODO(question): wasted counters not established in sim [07 §11]
			_ = ev.EnergyConsumed
			_ = ev.MetalConsumed
		} else if rv, ok := curResources[sc.Player]; ok {
			eProd = int(rv.EnergyProduced)
			mProd = int(rv.MetalProduced)
		}
		// Kills/Losses currently hardcoded 0 behind snapshot view field [P1-01 §2.3] TODO(question): wire ledger kill counter
		line := fmt.Sprintf("P%d T%d K:%d L:%d E:%d M:%d EW:%d MW:%d S:%d %s", sc.Player, sc.Team, sc.Kills, sc.Losses, eProd, mProd, eWaste, mWaste, sc.Score, sc.Kind)
		c.UIText(h.console, line, bx+10, yPos, 7)
		yPos += lineH
	}
	// If no scores but still have economy, show economy-only rows gated by enable byte
	if len(view.Scores) == 0 && len(curEconomy) > 0 {
		for pid, ev := range curEconomy {
			if yPos+lineH > y+bh-30 {
				break
			}
			line := fmt.Sprintf("P%d EProd:%d MProd:%d EWaste:0 MWaste:0", pid, int(ev.EnergyProduced), int(ev.MetalProduced))
			c.UIText(h.console, line, bx+10, yPos, 7)
			yPos += lineH
		}
		_ = curResources
	}
}

// drawStatusMessage draws transient game-speed and pause messages [07 §11][07 §2] presentation-only (I6).
func (b *battleSession) drawStatusMessage(c *client.Client) {
	if b == nil || c == nil || !b.statusVisible() {
		return
	}
	// Draw at top center below resource bars; use console font via HUD palette
	var fnt = b.hud.console
	if fnt == nil {
		return
	}
	// Centered
	txt := b.statusMessage
	w := client.MeasureText(fnt, txt)
	x := (640 - w) / 2
	y := 30 // below top strip
	c.UIText(fnt, txt, x, y, 15)
}
