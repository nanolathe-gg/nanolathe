package hud

// The Space-held Kills/Losses score panel [07 R-HUD-04 §1].
//
// This file is pure arithmetic: the slide word, the panel and row geometry,
// the rank-ordered row scan with its vacated-rank compaction, the two flash
// byte arrays and their decay, and the counter pair the commander-death
// option word selects. It holds no session, pool or economy pointer and
// touches no simulation state [I6].
//
// The composer in cmd/nanolathe owns the painting: which surface, which font,
// which palette tables. Everything here is integers.

import "github.com/nanolathe/nanolathe/internal/frame"

// Panel constants, all from [07 R-HUD-04 §1].
const (
	// ScorePanelWidth is the open detent: the panel is 125 pixels wide and
	// the slide word counts the pixels of it that are on screen.
	ScorePanelWidth = 125
	// ScorePanelTop is the panel's first scanline and the heading row's y.
	ScorePanelTop = 32
	// ScorePanelRowHeight is the per-row advance.
	ScorePanelRowHeight = 40
	// ScorePanelFirstRow is the first row's top.
	ScorePanelFirstRow = 47
	// ScorePanelHeightPad is the constant added to 40 x playerCount for the
	// panel's bottom edge.
	ScorePanelHeightPad = 46
	// ScorePanelTextLimit is the text width limit every string in the panel
	// is written with.
	ScorePanelTextLimit = 119
	// ScorePanelShadeLevel is the rectangle shader level the panel body is
	// darkened at [03 R-COMP-02 §5].
	ScorePanelShadeLevel = -24
	// ScorePanelLocalLightA and ScorePanelLocalLightB are the two lighten
	// levels the local player's row is passed through, in this order.
	ScorePanelLocalLightA = 31
	ScorePanelLocalLightB = 20
	// ScorePanelLogoWidth and ScorePanelLogoHeight are the destination quad
	// the side logo frame's interior is stretched onto.
	ScorePanelLogoWidth  = 112
	ScorePanelLogoHeight = 36
	// ScoreFlashInitial is the value a credited kill writes into the
	// crediting slot's kill flash and the victim slot's loss flash.
	ScoreFlashInitial uint8 = 30
	// ScoreFlashDecay is subtracted from every nonzero flash entry once per
	// unit of the scaled timer [07 R-CAM-01 §10].
	ScoreFlashDecay uint8 = 2
	// ScorePanelSlots is the number of player slots the row scan walks.
	ScorePanelSlots = 10
	// ScoreCuePanel and ScoreCueOptions are the two sound aliases the slide
	// plays when it leaves and when it reaches a detent.
	ScoreCuePanel   = "Panel"
	ScoreCueOptions = "Options"
	// ScoreDeathmatchOption is the commander-death option word that selects
	// the commander-kill and commander-loss counters instead of the ordinary
	// pair [07 R-FE-01 §7].
	ScoreDeathmatchOption = 2
	// ScoreSideExcluded is the side byte a slot must not carry to qualify.
	ScoreSideExcluded uint8 = 10
)

// ScoreSessionKindDraws reports whether the battle composer draws the panel
// at all: only session kinds 2 and 3 (skirmish and multiplayer in doc 08's
// vocabulary). A campaign mission (kind 1) never calls it [07 R-HUD-04 §1].
func ScoreSessionKindDraws(kind uint8) bool { return kind == 2 || kind == 3 }

// ScoreShowing is the show/hide polarity: the panel is showing when the F4
// interface bit is set, or when Space is held and the focused gadget of the
// top window is not a text editor [07 R-HUD-04 §1][07 R-CAM-01 §2].
func ScoreShowing(interfaceBit, spaceHeld, editorFocused bool) bool {
	return interfaceBit || (spaceHeld && !editorFocused)
}

// ScoreSlide is the signed slide word, 0..125, in panel pixels on screen.
type ScoreSlide int32

// Step advances the slide word one composed frame and reports whether the
// panel draws in this frame and which cue, if any, the step played. Retail
// steps it once per composed frame with no wall-clock throttle, unlike the
// bottom slide strip of [07 §6] [07 R-HUD-04 §1].
//
// Only one cue can fire per step: the first quarter-step out of a detent is
// 125/4 = 31 pixels, so a single step never crosses the whole travel.
func (s *ScoreSlide) Step(showing bool) (visible bool, cue string) {
	if s == nil {
		return false, ""
	}
	if !showing {
		if *s < 1 {
			// Fully retracted: nothing is drawn and the routine returns
			// before the step, so a hidden panel never plays a cue.
			return false, ""
		}
		if *s == ScorePanelWidth {
			cue = ScoreCuePanel
		}
		step := *s / 4 // trunc toward zero, both operands non-negative [I3]
		if step < 1 {
			step = 1
		}
		*s -= step
		if *s < 1 {
			*s = 0
			cue = ScoreCueOptions
		}
		return true, cue
	}
	if *s < ScorePanelWidth {
		if *s == 0 {
			cue = ScoreCuePanel
		}
		step := (ScorePanelWidth - *s) / 4
		if step < 1 {
			step = 1
		}
		*s += step
		if *s > ScorePanelWidth-1 {
			*s = ScorePanelWidth
			cue = ScoreCueOptions
		}
	}
	return true, cue
}

// ScorePanelRect is the panel body rectangle in surface coordinates.
type ScorePanelRect struct {
	X0, Y0, X1, Y1 int32
}

// ScorePanelGeometry places the panel against the right screen edge: x0 is
// the surface width minus the slide word, x1 is x0 plus the panel width, y0
// is 32 and y1 is 40 x playerCount + 46 [07 R-HUD-04 §1].
func ScorePanelGeometry(surfaceWidth int32, slide ScoreSlide, playerCount int) ScorePanelRect {
	x0 := surfaceWidth - int32(slide)
	return ScorePanelRect{
		X0: x0,
		Y0: ScorePanelTop,
		X1: x0 + ScorePanelWidth,
		Y1: int32(ScorePanelRowHeight*playerCount + ScorePanelHeightPad),
	}
}

// ScoreRowTop is the top scanline of the n-th drawn row: rows start at 47 and
// advance by 40 per row drawn [07 R-HUD-04 §1].
func ScoreRowTop(drawn int) int32 {
	return ScorePanelFirstRow + int32(ScorePanelRowHeight*drawn)
}

// ScoreSlot is one player slot's row-filter input [07 R-HUD-04 §1].
type ScoreSlot struct {
	Present    bool
	Controller uint8
	Side       uint8
	LiveUnits  int
	Auxiliary  uint32
	Watcher    bool
	Rank       uint8
}

// Qualifies is the row filter: the record is present; the controller byte is
// 1, 2 or 3; the side byte is not 10; the live-unit count is nonzero or the
// slot's auxiliary word is zero; and the lobby record's watcher bit is clear
// [07 R-HUD-04 §1].
func (s ScoreSlot) Qualifies() bool {
	if !s.Present {
		return false
	}
	if s.Controller != 1 && s.Controller != 2 && s.Controller != 3 {
		return false
	}
	if s.Side == ScoreSideExcluded {
		return false
	}
	if s.LiveUnits == 0 && s.Auxiliary != 0 {
		return false
	}
	return !s.Watcher
}

// ScoreRowOrder returns the slot indices in the order their rows are drawn,
// and the rank bytes after this frame's compaction [07 R-HUD-04 §1].
//
// Ranks r = 0 .. playerCount-1 are tried in turn. For each rank the ten slots
// are scanned in slot order for the first that qualifies and whose rank byte
// equals r; the first match draws the row and the scan stops. If no slot
// holds rank r, every qualifying slot whose rank is greater than r has its
// rank byte decremented by one and the same rank is retried, so a vacated
// rank collapses in the same frame and the row count equals the number of
// qualifying slots. (The section's "the next rank is tried" is read as the
// next attempt at this rank, not r+1: only that reading produces the row
// count the same sentence states, because advancing r past a vacated rank
// would drop the slot that just moved into it and never converge.)
//
// Slots are visited in ascending slot order, never through a map [I1].
func ScoreRowOrder(slots []ScoreSlot, playerCount int) (order []int, compacted []ScoreSlot) {
	compacted = append([]ScoreSlot(nil), slots...)
	if playerCount <= 0 {
		return nil, compacted
	}
	for r := 0; r < playerCount; r++ {
		for {
			found := -1
			for i := range compacted {
				if compacted[i].Qualifies() && int(compacted[i].Rank) == r {
					found = i
					break
				}
			}
			if found >= 0 {
				order = append(order, found)
				break
			}
			// No slot holds this rank. Compact every qualifying slot above it
			// and retry. When nothing sits above r there is nothing left to
			// draw and the scan is finished.
			moved := false
			for i := range compacted {
				if compacted[i].Qualifies() && int(compacted[i].Rank) > r {
					compacted[i].Rank--
					moved = true
				}
			}
			if !moved {
				return order, compacted
			}
		}
	}
	return order, compacted
}

// ScoreFlash holds the two ten-entry per-slot flash byte arrays. The kill
// record finalize sets the crediting slot's kill flash and the victim slot's
// loss flash to 30; the panel routine decays every nonzero entry by 2 once
// per unit of the scaled timer whether or not the panel is showing, and the
// byte is passed as the light-table row of the number's text, so a fresh kill
// draws bright and fades to row 0 over half a second
// [07 R-HUD-04 §1][03 R-FONT-01 §6][08 R-SKIR-01 §3].
type ScoreFlash struct {
	Kills  [ScorePanelSlots]uint8
	Losses [ScorePanelSlots]uint8
}

// Credit arms the crediting slot's kill flash and the victim slot's loss
// flash. A slot index outside 0..9 is ignored, as an unoccupied row is.
func (f *ScoreFlash) Credit(killer, victim int) {
	if f == nil {
		return
	}
	if killer >= 0 && killer < ScorePanelSlots {
		f.Kills[killer] = ScoreFlashInitial
	}
	if victim >= 0 && victim < ScorePanelSlots {
		f.Losses[victim] = ScoreFlashInitial
	}
}

// Decay applies one unit of the scaled timer to both arrays.
func (f *ScoreFlash) Decay() {
	if f == nil {
		return
	}
	for i := 0; i < ScorePanelSlots; i++ {
		f.Kills[i] = decayFlash(f.Kills[i])
		f.Losses[i] = decayFlash(f.Losses[i])
	}
}

func decayFlash(v uint8) uint8 {
	if v == 0 {
		return 0
	}
	if v <= ScoreFlashDecay {
		return 0
	}
	return v - ScoreFlashDecay
}

// Reset zeroes both arrays, which is what battle entry does.
func (f *ScoreFlash) Reset() {
	if f == nil {
		return
	}
	f.Kills = [ScorePanelSlots]uint8{}
	f.Losses = [ScorePanelSlots]uint8{}
}

// ScoreCounters selects the pair the panel prints: the ordinary kill and loss
// counters, or the commander-kill and commander-loss counters when the
// commander-death option word is 2 (Deathmatch) [07 R-HUD-04 §1][07 R-FE-01 §7].
//
// It reads the per-tick player row, not the latched result row: the panel is
// drawn throughout a live battle, while the result rows of [08 R-CAMP-01 §7]
// exist only once the result is collected.
func ScoreCounters(row frame.PlayerRow, commanderDeathOption int) (kills, losses int) {
	if commanderDeathOption == ScoreDeathmatchOption {
		return row.CommandersKilled, row.CommandersLost
	}
	return row.Kills, row.Losses
}
