package session

// Campaign progression: MISSION%d contiguous to first gap, MissionList allocation
// tag only, VFS first-provider-wins, language-prefixed missionname [P0-05].
// This file implements latch, scoring, and registry vs bank split [P0-05]
// and the P1-01 end-of-mission countdown/teardown [P1-01].

import "math"

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
	LatchInitial             = 4
	LatchBitEnding    uint16 = 0x04 // [P0-05][P1-01] ending bit 0x04
	LatchBitEndingAlt uint16 = 0x04 // deprecated alias; retail uses 0x04 only [P0-05]
	LatchBitWin1      uint16 = 0x10 // [P1-01] win variant
	LatchBitWin2      uint16 = 0x20 // [P1-01] extra win bit
	LatchBitLose      uint16 = 0x40 // [P1-01] lose variant, clears win
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

// Tick decrements roughly once per second (~1/s) via the local deadline
// block path. Retail decrements via wall-clock GetTickCount scaled (~60
// scaled units per second) checked post-loop per-tick [P0-05]; for the
// normal human path we approximate with tick%30==0 (30 ticks==1s at 30Hz)
// for determinism [P1-01 §3]. The no-human path uses TickNoHuman which
// decrements every tick [P1-01 §7.2].
func (l *EndLatch) Tick(tick uint32) bool {
	if l.Countdown <= 0 {
		return false
	}
	if tick%30 != 0 {
		return false
	}
	l.Countdown--
	return l.Countdown == 0
}

// TickNoHuman decrements the countdown every tick for the
// humanCount==0 post-loop site
// [P1-01 §7.2]. On no-human saves the latch fires in 5 ticks, not 150.
// When Countdown crosses below zero it writes the latch word with ending
// plus pending win/lose bits [P1-01 §2.2][08 "Evaluation"].
func (l *EndLatch) TickNoHuman() bool {
	if l.Countdown < 0 {
		return false
	}
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		if l.Pending == 1 {
			l.Win()
		} else if l.Pending == 2 {
			l.Lose()
		} else {
			// No pending set via Advance path guard; default win not used.
		}
		l.Pending = 0
		return true
	}
	return false
}

// AdvanceWin advances the countdown for a victory predicate. If countdown
// is negative (unarmed) it arms to 4 with pending win; otherwise it
// decrements once per eligible invocation. When the decrement crosses below
// zero it latches ending plus pending win bits [P1-01 §3][08 "Evaluation"].
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
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		if l.Pending == 1 {
			l.Win()
		} else if l.Pending == 2 {
			l.Lose()
		} else {
			l.Win()
		}
		l.Pending = 0
		return true
	}
	return false
}

// AdvanceLose advances for defeat (only when victory not candidate).
// Lose sets 0x40 and clears win bit 0x10 via AND ~0x10
// [P1-01 §2.2][08 "Evaluation"]. Pending lose is armed at 4 and not written until crossing.
func (l *EndLatch) AdvanceLose(isDeadlineDue bool) bool {
	if !isDeadlineDue {
		return false
	}
	if l.Countdown < 0 {
		l.Arm()
		l.Pending = 2
		return false
	}
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		if l.Pending == 2 {
			l.Lose()
		} else if l.Pending == 1 {
			l.Win()
		} else {
			l.Lose()
		}
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

// Score computes retail score: int(kills*killmul) + int(ticks/1800.0*timemul) clamp>=0 [P0-05][P1-01 §2.3][P1-01 §4].
// killmul and timemul are authored score multipliers; ticks is GlobalTick,
// incremented per subtick [P1-01 §4].
// Retail does float multiplies via FLD then FTOL trunc toward zero, sum then clamp via TEST/JGE; XOR.
// Kills and losses are player counters incremented by the combat/death path
// [P1-01 §2.3].
// TODO(question): default killmul/timemul when absent (0 vs 1.0) not proven [P1-01 §8].
func Score(kills int, killmul float32, ticks uint32, timemul float32) int {
	killPart := float64(kills) * float64(killmul)
	timePart := float64(ticks) / 1800.0 * float64(timemul)
	s := int(math.Trunc(killPart)) + int(math.Trunc(timePart))
	if s < 0 {
		s = 0
	}
	return s
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
// TODO(P0-05): HAPIBANK persistence for Summary/BetweenMissions etc not yet wired; registry vs bank split noted here.
// DisplaymodeDepth guard: Games bit1 only when DisplaymodeDepth==0x100 [P0-05].
type Registry struct {
	Difficulty         int // 0/1/2 else fail [P0-05]
	Games              int // bit1 under DisplaymodeDepth==0x100 guard [P0-05]
	AllMissions        int // bit0 [P0-05]
	NumSkirmishPlayers int // no-op validation: both branches store raw [P0-05]
}

// ValidateNumSkirmishPlayers is retail no-op: both branches store raw
// val unchanged [P0-05] SPEC_CONFLICTS.
func (r *Registry) ValidateNumSkirmishPlayers(val int) int {
	// No clamping, store raw even if out of 2..10
	r.NumSkirmishPlayers = val
	return val
}

// BankProgress holds BetweenMissions and allied persistence via HAPIBANK
// Summary/BetweenMissions=1 and Players/Alliances/W/L boxes [P0-05][P1-01].
// BetweenMissions 1 selects campaign continuation while the other route
// restores battle state [P1-13]; Summary timing is post-battle after latch,
// before returning to the router [P1-01 §2.4].
type BankProgress struct {
	BetweenMissions int // 1 outside live battle [P0-05][P1-01] — Summary/BetweenMissions
	Alliances       [11]byte
	WL              [10]byte // 'W'/'L' at the mission slot [P0-05][P1-01 §2.3]
}

// TeardownOrder documents the final-tick order per [P1-01 §2.4] and
// [01 §4.4] via the 12-phase tree with globalTick incremented before phase 1.
// Order: network→units→projectiles→player/economy/triggers→sharing→features etc
// [P1-01 §2.4]. Projectile phase captures count at entry; trigger poll sits
// inside player phase after settlement gate [P1-01 §3]. Latch freezes
// settlement same tick; network drain still runs at next tick top [P1-01 §3].
// TODO(question): exact WinLoseTime/DisplayTimer UI consumers outside save [P1-01 §8].
const TeardownOrder = "network→units→projectiles→player/economy/triggers→sharing→features→visibility→wind→cleanup→barrier→cadence"

// ApplyCampaignResult writes 'W'/'L' at the mission slot and updates
// BetweenMissions persistence via the post-battle handler [P1-01 §2.3].
// slot is the mission list slot; win true writes 'W' else 'L'.
func (b *BankProgress) ApplyCampaignResult(slot int, win bool) {
	if slot < 0 || slot >= len(b.WL) {
		return
	}
	if win {
		b.WL[slot] = 'W'
	} else {
		b.WL[slot] = 'L'
	}
}

// Retry semantics: RETRY path reloads same mission via state 5 directly
// without rewriting campaign progress beyond current slot, while CONTINUE
// or RETURN routes via post-battle handler that writes W/L and unlocks next
// mission before returning to the front-end router [P1-01 §7.5].
// TODO(question): campaign progress registry vs file persistence location
// and write timing relative to report ticker remains TODO(question) [P1-01 §8].
type CampaignTransition int

const (
	TransitionRetry    CampaignTransition = iota // reload same mission via state 5 directly [P1-01 §7.5]
	TransitionContinue                           // next mission via post-battle W/L [P1-01 §7.5]
	TransitionReturn                             // return to front-end [P1-01 §7.5]
)
