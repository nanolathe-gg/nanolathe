package session

// Campaign progression: MISSION%d contiguous to first gap, MissionList allocation
// tag only, VFS first-provider-wins, language-prefixed missionname [P0-05].
// This file implements latch, scoring, and registry vs bank split [P0-05].

import "math"

// Latch arms to 4 then decrements ~1/s before latch word bits [P0-05].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
const (
	LatchInitial             = 4
	LatchBitEnding    uint16 = 0x04 // [P0-05] ending bit 0x04, not 0x02
	LatchBitEndingAlt uint16 = 0x04 // deprecated alias; retail uses 0x04 only [P0-05]
	LatchBitWin1      uint16 = 0x10
	LatchBitWin2      uint16 = 0x20
	LatchBitLose      uint16 = 0x40
)

// EndLatch holds countdown and win/lose bits [P0-05].
type EndLatch struct {
	Countdown int16
	Bits      uint16
}

// Arm sets countdown to 4 and sets ending bit 0x04 [P0-05].
func (l *EndLatch) Arm() {
	l.Countdown = LatchInitial
	l.Bits |= LatchBitEnding // 0x04 only [P0-05]
}

// Tick decrements roughly once per second (~1/s). Retail decrements via
// wall-clock GetTickCount scaled (~60 scaled units per second) checked post-loop per-tick [P0-05];
// we approximate with tick%30 ==0 (30 ticks ==1s at 30Hz) for determinism.
// TODO(P0-05): verify scaled-clock vs tick%30; approximate with 30 ticks here.
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

// Win sets win bits 0x10|0x20 [P0-05].
func (l *EndLatch) Win() {
	l.Bits |= LatchBitWin1 | LatchBitWin2
	l.Bits &^= LatchBitLose
}

// Lose sets lose bit 0x40 and clears win bits 0x10 and 0x20 [P0-05].
func (l *EndLatch) Lose() {
	l.Bits &^= LatchBitWin1 | LatchBitWin2
	l.Bits |= LatchBitLose
}

// IsWin reports win.
func (l *EndLatch) IsWin() bool { return l.Bits&LatchBitWin1 != 0 }

// IsLose reports lose.
func (l *EndLatch) IsLose() bool { return l.Bits&LatchBitLose != 0 }

// Score computes retail score: int(kills*killmul) + int(ticks/1800.0*timemul) clamp>=0 [P0-05].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Retail does float multiplies via FLD then FTOL trunc toward zero, sum then clamp.
func Score(kills int, killmul float32, ticks uint32, timemul float32) int {
	killPart := float64(kills) * float64(killmul)
	timePart := float64(ticks) / 1800.0 * float64(timemul)
	s := int(math.Trunc(killPart)) + int(math.Trunc(timePart))
	if s < 0 {
		s = 0
	}
	return s
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
// Summary/BetweenMissions=1 and Players/Alliances/W/L boxes [P0-05].
// TODO(P0-05): HAPIBANK persistence for Summary/BetweenMissions etc not yet wired.
type BankProgress struct {
	BetweenMissions int // 1 outside live battle [P0-05]
	Alliances       [11]byte
	WL              [10]byte // 'W'/'L' at 0x391CF+slot [P0-05]
}

// WindDraws performs the two briefing CRT draws before simulation [P0-05][01 §7.3].
// Caller must have CRT seeded; sim not touched. Returns strength and direction.
func WindDraws(min, max int32, crt interface{ Rand() int32 }) (int32, int32) {
	// This is stub for docs: actual draws are in world.Wind.SeedBriefing via session wind.go
	// Keep for citation.
	return min, max
}
