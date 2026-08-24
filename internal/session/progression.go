package session

// Campaign progression: MISSION%d contiguous to first gap, MissionList allocation
// tag only, VFS first-provider-wins, language-prefixed missionname [P0-05].
// This file implements latch, scoring, and registry vs bank split [P0-05]
// and the P1-01 end-of-mission countdown/teardown [P1-01].

import "math"

// Latch arms to 4 then decrements ~1/s before latch word bits [P0-05][P1-01].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// arms to 4 when <0 inside local deadline block (several branches) and at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Decrements on subsequent eligible invocations; latch site decrements
// post-loop once per phase invocation regardless of deadlines when
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// latch, 0x10|0x20 win, 0x40 lose (lose clears win via AND ~0x10 at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type EndLatch struct {
	Countdown int16
	Bits      uint16
}

// NewEndLatch returns a latch in the retail initial state: countdown -1
// (0xFFFF) and no bits set [P1-01 §2.2].
func NewEndLatch() EndLatch { return EndLatch{Countdown: -1} }

// IsEnding reports whether latch bit 0x04 is set (game ended) [P1-01 §2.2].
func (l *EndLatch) IsEnding() bool { return l.Bits&LatchBitEnding != 0 }

// IsLatched is alias for IsEnding.
func (l *EndLatch) IsLatched() bool { return l.IsEnding() }

// Arm sets countdown to 4 and sets ending bit 0x04 [P0-05][P1-01 §2.2].
// Retail writes 4 when countdown<0 inside local deadline block and at
// post-loop site [P1-01 §3].
func (l *EndLatch) Arm() {
	l.Countdown = LatchInitial
	l.Bits |= LatchBitEnding // 0x04 only [P0-05][P1-01]
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// [P1-01 §7.2]. On no-human saves the latch fires in 5 ticks, not 150.
func (l *EndLatch) TickNoHuman() bool {
	if l.Countdown <= 0 {
		return false
	}
	l.Countdown--
	return l.Countdown == 0
}

// AdvanceWin advances the countdown for a victory predicate. If countdown
// is negative (unarmed) it arms to 4; otherwise it decrements once per
// eligible invocation. When the decrement crosses below zero it latches
// ending+win bits [P1-01 §3].
// Returns true when the latch transition (ending) occurs on this call.
func (l *EndLatch) AdvanceWin(isDeadlineDue bool) bool {
	if !isDeadlineDue {
		return false
	}
	if l.Countdown < 0 {
		l.Arm()
		l.Win()
		return false
	}
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		l.Win()
		return true
	}
	return false
}

// AdvanceLose advances for defeat (only when victory not candidate).
// Lose clears win bits via AND ~0x10 (actually ~0x30) and sets 0x40
// [P1-01 §2.2].
func (l *EndLatch) AdvanceLose(isDeadlineDue bool) bool {
	if !isDeadlineDue {
		return false
	}
	if l.Countdown < 0 {
		l.Arm()
		l.Lose()
		return false
	}
	l.Countdown--
	if l.Countdown < 0 {
		l.Bits |= LatchBitEnding
		l.Lose()
		return true
	}
	return false
}

// Win sets win bits 0x10|0x20 and clears lose [P0-05][P1-01].
func (l *EndLatch) Win() {
	l.Bits |= LatchBitWin1 | LatchBitWin2
	l.Bits &^= LatchBitLose
}

// Lose sets lose bit 0x40 and clears win bits 0x10 and 0x20 [P0-05][P1-01].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (l *EndLatch) Lose() {
	l.Bits &^= LatchBitWin1 | LatchBitWin2
	l.Bits |= LatchBitLose
}

// IsWin reports win [P1-01].
func (l *EndLatch) IsWin() bool { return l.Bits&LatchBitWin1 != 0 }

// IsLose reports lose [P1-01].
func (l *EndLatch) IsLose() bool { return l.Bits&LatchBitLose != 0 }

// Score computes retail score: int(kills*killmul) + int(ticks/1800.0*timemul) clamp>=0 [P0-05][P1-01 §2.3][P1-01 §4].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Retail does float multiplies via FLD then FTOL trunc toward zero, sum then clamp via TEST/JGE; XOR.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// AllMissions at 0x38D7F bit0, Games at 0x37F2F bit1 under DisplaymodeDepth guard,
// Difficulty at 0x37EEE [P0-05].
// TODO(P0-05): HAPIBANK persistence for Summary/BetweenMissions etc not yet wired; registry vs bank split noted here.
// DisplaymodeDepth guard: Games bit1 only when DisplaymodeDepth==0x100 [P0-05].
type Registry struct {
	Difficulty         int // 0/1/2 else fail [P0-05]; stored at 0x37EEE
	Games              int // bit1 at 0x37F2F under DisplaymodeDepth==0x100 guard [P0-05]
	AllMissions        int // bit0 at 0x38D7F [P0-05]
	NumSkirmishPlayers int // no-op validation: both branches store raw [P0-05]
}

// ValidateNumSkirmishPlayers is retail no-op: both branches store raw
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (r *Registry) ValidateNumSkirmishPlayers(val int) int {
	// No clamping, store raw even if out of 2..10
	r.NumSkirmishPlayers = val
	return val
}

// BankProgress holds BetweenMissions and allied persistence via HAPIBANK
// Summary/BetweenMissions=1 and Players/Alliances/W/L boxes [P0-05][P1-01].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
type BankProgress struct {
	BetweenMissions int // 1 outside live battle [P0-05][P1-01] — Summary/BetweenMissions
	Alliances       [11]byte
	WL              [10]byte // 'W'/'L' at 0x391CF+slot [P0-05][P1-01 §2.3]
}

// TeardownOrder documents the final-tick order per [P1-01 §2.4] and
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Order: network→units→projectiles→player/economy/triggers→sharing→features etc
// [P1-01 §2.4]. Projectile phase captures count at entry; trigger poll sits
// inside player phase after settlement gate [P1-01 §3]. Latch freezes
// settlement same tick; network drain still runs at next tick top [P1-01 §3].
// TODO(question): exact WinLoseTime/DisplayTimer UI consumers outside save [P1-01 §8].
const TeardownOrder = "network→units→projectiles→player/economy/triggers→sharing→features→visibility→wind→cleanup→barrier→cadence"

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): campaign progress registry vs file persistence location
// and write timing relative to report ticker remains TODO(question) [P1-01 §8].
type CampaignTransition int

const (
	TransitionRetry    CampaignTransition = iota // reload same mission via state 5 directly [P1-01 §7.5]
	TransitionContinue                           // next mission via post-battle W/L [P1-01 §7.5]
	TransitionReturn                             // TODO(question): Historical analysis omitted; independently worded behavior is needed.
)

// WindDraws performs the two briefing CRT draws before simulation [P0-05][01 §7.3].
// Caller must have CRT seeded; sim not touched. Returns strength and direction.
func WindDraws(min, max int32, crt interface{ Rand() int32 }) (int32, int32) {
	// This is stub for docs: actual draws are in world.Wind.SeedBriefing via session wind.go
	// Keep for citation.
	return min, max
}
