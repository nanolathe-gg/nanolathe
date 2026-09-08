package session

// Campaign progression: MISSION%d contiguous to first gap, MissionList allocation
// tag only, VFS first-provider-wins, language-prefixed missionname [P0-05].
// This file implements latch, scoring, and registry vs bank split [P0-05]
// and the P1-01 end-of-mission countdown/teardown [P1-01].

import (
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Latch arms to 4 then decrements ~1/s before latch word bits [P0-05][P1-01].
// Retail stores an int16 countdown starting at -1, then arms to 4 when <0
// inside the local deadline block and at the post-loop per-tick site
// [P1-01 §2.2].
// Decrements on subsequent eligible invocations; latch site decrements
// post-loop once per phase invocation regardless of deadlines when
// humanCount==0 [P1-01 §2.2]. The latch bits are 0x04 ending always on
// latch, 0x10|0x20 win, and 0x40 lose (lose clears win via AND ~0x10)
// [P1-01 §2.2]. Latch never clears 0x04 once set (only OR) [P1-01].
// Settlement freeze gates countdown<0 && NOT latched (countdown<0 && NOT
// (latch&0x04)) plus UpdateTime deadline [P1-01 §2.2].
const (
	LatchInitial          = 4
	LatchBitEnding uint16 = 0x04 // [P0-05][P1-01] ending bit 0x04
	LatchBitWin1   uint16 = 0x10 // [P1-01] win variant
	LatchBitWin2   uint16 = 0x20 // [P1-01] extra win bit
	LatchBitLose   uint16 = 0x40 // [P1-01] lose variant, clears win
)

// EndLatch holds countdown and win/lose bits [P0-05][P1-01].
// Countdown and Bits are session-owned fields [P1-01 §2.2].
// Initial value -1 (0xFFFF) means "not armed" [P1-01].
// Pending stores the outcome armed but not yet latched: 0 none, 1 win, 2 lose.
// It is set at arm time and consumed when Countdown crosses below zero to
// write the latch word. Bits win/lose are not written until that crossing
// [08 "Evaluation"][P1-01 §2.2], while SettlementFrozen already holds via
// Countdown>=0.
type EndLatch struct {
	Countdown int16
	Bits      uint16
	Pending   uint8 // 0 none, 1 win pending, 2 lose pending
}

// NewEndLatch returns a latch in the retail initial state: countdown -1
// (0xFFFF) and no bits set [P1-01 §2.2].
func NewEndLatch() EndLatch { return EndLatch{Countdown: -1} }

// IsEnding reports whether latch bit 0x04 is set (game ended) [P1-01 §2.2].
func (l *EndLatch) IsEnding() bool { return l.Bits&LatchBitEnding != 0 }

// IsLatched is alias for IsEnding.
func (l *EndLatch) IsLatched() bool { return l.IsEnding() }

// Arm sets countdown to 4 without yet setting the latch word bits
// [08 "Evaluation"][P1-01 §2.2]. Retail arms to 4 when Countdown<0 inside
// the local deadline block; Bits 0x04/0x10/0x20/0x40 are written only when the
// countdown later crosses below zero to  -1. Settlement freeze already holds
// via Countdown>=0 even before Bits are written [P1-01 §2.2].
func (l *EndLatch) Arm() {
	l.Countdown = LatchInitial
	// Bits not set until latch transition [08 "Evaluation"][P1-01 §2.2]
}

// AdvanceWin advances the countdown for a victory predicate. If countdown
// is negative (unarmed) it arms to 4; otherwise it records win as the current
// true path and decrements once. When the decrement crosses below zero it
// latches ending plus win bits [08 R-TRIG-01 §6].
// The win bits are not written until the crossing [P1-01 §2.2]; SettlementFrozen
// already holds via Countdown>=0 even before Bits are written.
// Returns true when the latch transition (ending) occurs on this call.
func (l *EndLatch) AdvanceWin(isDeadlineDue bool) bool {
	if !isDeadlineDue {
		return false
	}
	if l.Countdown < 0 {
		l.Arm()
		l.Pending = 1
		return false
	}
	l.Pending = 1
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		l.Win()
		l.Pending = 0
		return true
	}
	return false
}

// AdvanceLose advances for defeat (only when victory is false). Each true due
// records lose as the current path before decrementing, so the sixth true
// due's path selects the terminal bits [08 R-TRIG-01 §6].
func (l *EndLatch) AdvanceLose(isDeadlineDue bool) bool {
	if !isDeadlineDue {
		return false
	}
	if l.Countdown < 0 {
		l.Arm()
		l.Pending = 2
		return false
	}
	l.Pending = 2
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		l.Lose()
		l.Pending = 0
		return true
	}
	return false
}

// Win sets win bits 0x10|0x20 and clears lose [P0-05][P1-01].
func (l *EndLatch) Win() {
	l.Bits |= LatchBitWin1 | LatchBitWin2
	l.Bits &^= LatchBitLose
}

// Lose sets lose bit 0x40 and clears win bit 0x10 [P0-05][P1-01][08 "Evaluation"].
// Retail does OR 0x40 then AND ~0x10 [P1-01 §2.2]; 0x20 is not
// cleared on the lose path — it remains only where a prior Win set it.
func (l *EndLatch) Lose() {
	l.Bits &^= LatchBitWin1 // [08 "Evaluation"] lose clears 0x10, not 0x20
	l.Bits |= LatchBitLose
}

// IsWin reports win [P1-01].
func (l *EndLatch) IsWin() bool { return l.Bits&LatchBitWin1 != 0 }

// IsLose reports lose [P1-01].
func (l *EndLatch) IsLose() bool { return l.Bits&LatchBitLose != 0 }

// Score computes retail's end-of-battle score:
// __ftol(float(int64(globalTick/60 unsigned)) × timemul) +
// __ftol(float(Kills) × killmul), each product truncated toward zero
// SEPARATELY before the two are summed, then clamped at zero
// [08 R-CAMP-01 §7] [08 R-CAMP-01 §11 point 3]. killmul and timemul are the
// mission's [GlobalHeader] floats, default 0.0 when absent (stock missions
// author killmul=50; timemul=0;); ticks is the 32-bit global tick counter of
// [01 §2], divided by 60 as an UNSIGNED integer (not 1800, and not a
// floating-point division) before the timemul multiply. Kills is the
// player's 16-bit kill counter, sign-extended.
//
// Correction (2026-09-01, [08 R-CAMP-01 §11]): the divisor was 1800 here;
// retail divides ticks by 60. [02 R-MAP-01 §3] and [fmt ota] previously
// marked killmul/timemul "inert (reader census: none)" — that census missed
// this helper, which multiplies by both; both docs are corrected in place.
func Score(kills int, killmul float32, ticks uint32, timemul float32) int {
	timePart := int64(numeric.TruncateFloat32ToLow32(float32(ticks/60) * timemul))
	killPart := int64(numeric.TruncateFloat32ToLow32(float32(kills) * killmul))
	total := timePart + killPart
	if total < 0 {
		return 0
	}
	return int(total)
}

// SettlementFrozen reports whether economy settlement is frozen by the
// latch gate: countdown<0 && NOT latched is the narrow gate that allows
// settlement; otherwise settlement is frozen [P1-01 §2.2]. Equivalent to
// checking GameEnded or EndGameCountdown >=0 via economy.Service.
func (l *EndLatch) SettlementFrozen() bool {
	if l.Bits&LatchBitEnding != 0 {
		return true
	}
	if l.Countdown >= 0 {
		return true
	}
	return false
}

// Registry holds difficulty and flags that persist via HKCU registry
// via the installed software registry path [P0-05].
// AllMissions uses bit 0, Games uses bit 1 under the DisplaymodeDepth guard,
// and Difficulty stores the selected difficulty [P0-05].
//
// The registry is *not* where campaign progress lives. The mission index, the
// 25-byte Thumbs mark array and the difficulty word are three in-memory items;
// they persist only through a save bank's Summary account, written by the
// save screen reached from the results panel or from the in-battle options
// menu [08 R-CAMP-01 §8 "Progress write"]. ContinuationSummary below is that
// projection; WriteRetailContinuationSave in retail_save.go is the writer.
// DisplaymodeDepth guard: Games bit1 only when DisplaymodeDepth==0x100 [P0-05].
type Registry struct {
	Difficulty         int // 0/1/2 else fail [P0-05]
	Games              int // bit1 under DisplaymodeDepth==0x100 guard [P0-05]
	AllMissions        int // bit0 [P0-05]
	NumSkirmishPlayers int // no-op validation: both branches store raw [P0-05]
}

// BankProgress holds BetweenMissions and allied persistence via HAPIBANK
// Summary/BetweenMissions=1 and Players/Alliances/W/L boxes [P0-05][P1-01].
// BetweenMissions 1 selects campaign continuation while the other route
// restores battle state [P1-13]; Summary timing is post-battle after latch,
// before returning to the router [P1-01 §2.4].
type BankProgress struct {
	BetweenMissions int // 1 outside live battle [P0-05][P1-01] — Summary/BetweenMissions
	Alliances       [11]byte
	WL              [10]byte // compatibility view of the first ten marks [P0-05][P1-01 §2.3]
	// Thumbs is the 25-slot campaign mark array.  The older WL view is kept for
	// callers that only model the ten-player result table; campaign progression
	// writes both views at the same one-byte site [08 R-CAMP-01 §8].
	Thumbs [25]byte // 'U', 'W', or 'L' by campaign mission slot
}

// TeardownOrder documents the final-tick order per [P1-01 §2.4] and
// [01 §4.4] via the 12-phase tree with globalTick incremented before phase 1.
// Order: network→units→projectiles→player/economy/triggers→sharing→features etc
// [P1-01 §2.4]. Projectile phase captures count at entry; trigger poll sits
// inside player phase after settlement gate [P1-01 §3]. Latch freezes
// settlement same tick; network drain still runs at next tick top [P1-01 §3].
// WinLoseTime and DisplayTimer's UI consumers are closed, not open: the save
// record's `WinLoseTime` has no reader anywhere in the image beyond the save
// writer itself (persisted verbatim for compatibility, otherwise inert), and
// `DisplayTimer` is the HUD resource-rate refresh deadline — a presentation
// consumer, not a sim phase — advanced on a strict compare one tick ahead of
// the settlement deadline [08 "Player records"][05 R-ECO-01 §6]. Neither
// belongs in this teardown order.
const TeardownOrder = "network→units→projectiles→player/economy/triggers→sharing→features→visibility→wind→cleanup→barrier→cadence"

// ApplyCampaignResult writes the single 'W'/'L' mark at the mission slot.
// Session's score-teardown path owns this write before the post-battle handler
// is installed; BetweenMissions is a save-summary projection, not a second
// mark write [08 R-CAMP-01 §7–8]. slot is the mission list slot; win true
// writes 'W' else 'L'.
func (b *BankProgress) ApplyCampaignResult(slot int, win bool) {
	if slot < 0 || slot >= len(b.WL) {
		if slot < 0 || slot >= len(b.Thumbs) {
			return
		}
	}
	mark := byte('L')
	if win {
		mark = 'W'
	}
	if slot < len(b.WL) {
		b.WL[slot] = mark
	}
	if slot < len(b.Thumbs) {
		b.Thumbs[slot] = mark
	}
}

// ContinuationSaveMetadata is the caller-owned half of a between-missions
// save: the typed slot name, the wall-clock game identity and the session
// counters the campaign identity does not carry. Both wall-clock values are
// read in the presentation layer, never inside a session [I6]
// [08 R-SAVE-02 §1].
type ContinuationSaveMetadata struct {
	// Description is the name typed into the save screen's GAMENAME edit;
	// GameID is the wall-clock seconds at the moment of saving
	// [08 R-SAVE-02 §1].
	Description string
	GameID      string
	// Players is the session's live player count. The load screen renders
	// `???` for a zero value, so a campaign save carries a nonzero count
	// [08 R-SAVE-02 §3].
	Players int32
	// MaxUnits and GameTime are the Summary's remaining scalars; GameTime is
	// the global simulation tick, presentation metadata only [08 "Summary"].
	MaxUnits int32
	GameTime int32
}

// ContinuationSummary projects a frozen campaign result into the Summary
// account the retail writer emits outside a live battle.
//
// PostBattleSummary already carries the between-missions quirk: a save taken
// from the results screen calls Advance before writing `Mission`/`Map`, so it
// names the **next** mission whenever one exists — win or loss — and the
// played mission only when it was the last [08 R-CAMP-01 §8
// "Between-missions save quirk"]. `Map` carries the same string as `Mission`
// on a campaign save [08 R-CAMP-01 §8 "Progress write"]. The item set and its
// order are the writer's [08 "Summary"]; `BetweenMissions` = 1 is what routes
// the load into campaign continuation rather than battle reconstruction
// [08 "battle versus campaign continuations and timing"]. No `Radar Image`
// box is written: that box is live-battle only [08 "Summary"].
func ContinuationSummary(p PostBattleSummary, meta ContinuationSaveMetadata) save.Summary {
	return save.Summary{
		MaxUnits:   meta.MaxUnits,
		Campaign:   p.Campaign,
		Mission:    p.Mission,
		MapName:    p.Mission,
		Difficulty: int32(p.Difficulty),
		Side:       int32(p.Side),
		Players:    meta.Players,
		// Load preflight accepts only campaign or multiplayer; a continuation
		// is always the campaign type [08 "Summary"].
		Gametype:        GametypeCampaign,
		Thumbs:          string(p.Thumbs[:]),
		BetweenMissions: 1,
		Description:     meta.Description,
		GameID:          meta.GameID,
		GameTime:        meta.GameTime,
		IsBattle:        false,
	}
}

// Retry semantics: RETRY path reloads same mission via state 5 directly
// without rewriting campaign progress beyond current slot, while CONTINUE
// or RETURN routes via post-battle handler that writes W/L and unlocks next
// mission before returning to the front-end router [P1-01 §7.5].
// Persistence location and write timing are closed, not open [08 R-CAMP-01
// §6–§8]: campaign progress is exactly three in-memory items (the mission
// index, the 25-byte Thumbs mark array, and the difficulty word); they are
// never written to the registry (which holds only the difficulty/games/
// all-missions mirrors), and persist only through the save bank's own
// Summary account. The single W/L mark is written once per battle, by the
// score helper, at the battle-teardown end transition — before the results
// handler is installed. Session.pollMissionTriggers matches that timing: it
// calls CommitCampaignTeardown when the latch crosses into its ending state,
// before the post-battle result is reported. Manual battle teardown uses the
// same mark writer, including an unfinished mission's L [08 R-CAMP-01 §7–8].
type CampaignTransition int

const (
	TransitionRetry    CampaignTransition = iota // reload same mission via state 5 directly [P1-01 §7.5]
	TransitionContinue                           // next mission via post-battle W/L [P1-01 §7.5]
	TransitionReturn                             // return to front-end [P1-01 §7.5]
)
