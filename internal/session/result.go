package session

import (
	"sort"
	"sync"

	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// ReasonCommanderDeath is the skirmish commander-death termination reason
// [08 "Skirmish configuration"] CommanderDeath default 1 = commander death ends the game.
const ReasonCommanderDeath = "commander_death"

// Result is the authoritative latched terminal result for a skirmish session
// [08 "Victory and defeat triggers"] trigger queues polled ONLY for mission type 1
// (campaign); skirmish sessions do NOT poll them — skirmish result computed from
// team/unit state instead. [08 "Skirmish configuration"] CommanderDeath==1.
// EndLatch countdown/bits per [P1-01 §2.2] already implemented in progression.go —
// reuse, do not bypass. Research does NOT decompose the general alliance endgame
// sweep: implement CommanderDeath==1 skirmish rule as: a team is eliminated when
// all its commanders are dead; when <=1 hostile team remains, latch result (draw
// on mutual destruction). TODO(question): for research-silent cases (extra
// commanders per team, resurrected commanders, CommanderDeath==0 annihilation mode)
// behavior is TODO(question) and deferred.
type Result struct {
	Ended      bool   `json:"ended"`
	Draw       bool   `json:"draw"`
	WinnerTeam int    `json:"winner_team"`
	Losers     []int  `json:"losers"`
	Reason     string `json:"reason"`
	Tick       uint32 `json:"tick"`
	ArmedTick  uint32 `json:"armed_tick,omitempty"`
}

// resultState holds per-session pending and latched result. It is stored in a
// package-level sync.Map keyed by *Session so that Session struct defined in
// loop.go does not need to be edited (prohibited). Access is guarded by per-
// entry mutex; deterministic iteration is not needed (I1) because map is not
// iterated for sim state.
type resultState struct {
	mu            sync.Mutex
	result        Result
	pending       bool
	pendingWinner int
	pendingLosers []int
	pendingReason string
	pendingDraw   bool
	armedTick     uint32
	callback      func(Result)
	callbackFired bool
}

var results sync.Map // map[*Session]*resultState

func getResultState(s *Session) *resultState {
	if s == nil {
		return &resultState{}
	}
	if v, ok := results.Load(s); ok {
		if rs, ok := v.(*resultState); ok {
			return rs
		}
	}
	rs := &resultState{}
	actual, _ := results.LoadOrStore(s, rs)
	if v, ok := actual.(*resultState); ok {
		return v
	}
	return rs
}

// teamForOwner maps an owner slot to its team identifier using
// SkirmishConfig.Players[].AllyGroup with fallback per-owner teams.
// AllyGroup 5 is the unassigned sentinel [08 "Skirmish configuration"]
// [GAP T14]; when it appears we treat each owner as its own hostile team
// (100+owner) so that default skirmishes are FFA. Explicit non-sentinel
// groups share a team. TODO(question): research does NOT decompose the general
// alliance endgame sweep; this mapping is the minimal CommanderDeath==1 rule.
func (s *Session) teamForOwner(owner int) int {
	if owner < 0 || owner >= 10 {
		return owner
	}
	if s.Skirmish.NumPlayers == 0 {
		// No skirmish config (isolated result-unit test) → per-owner fallback.
		return 100 + owner
	}
	ag := s.Skirmish.Players[owner].AllyGroup
	if ag == SkirmishDefaultAllyGroup {
		return 100 + owner
	}
	return ag
}

// GetResult returns the authoritative latched result (zero if not yet ended).
func (s *Session) GetResult() Result {
	rs := getResultState(s)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	// Return copy with Losers deep copied to keep immutability.
	r := rs.result
	if len(r.Losers) > 0 {
		cp := make([]int, len(r.Losers))
		copy(cp, r.Losers)
		r.Losers = cp
	}
	return r
}

// SetResultCallback installs a callback that fires exactly once when the
// terminal result becomes visible (after EndLatch Bits). If result already
// ended, it fires immediately (once).
func (s *Session) SetResultCallback(fn func(Result)) {
	rs := getResultState(s)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.result.Ended && !rs.callbackFired {
		rs.callbackFired = true
		// Call without holding lock to avoid reentrancy deadlock if callback
		// calls GetResult.
		r := rs.result
		rs.mu.Unlock()
		fn(r)
		rs.mu.Lock()
		return
	}
	rs.callback = fn
}

// EvaluateResult is the alliance-aware victory evaluator.
// It is callable standalone and hookable so the future central loop can
// invoke it after death finalization each tick. It computes active teams
// from live commanders each evaluation, uses the skirmish CommanderDeath==1
// rule (team eliminated when all its commanders are dead; when <=1 hostile
// team remains, latch result, draw on mutual destruction), preserves the
// researched EndLatch countdown via AdvanceWin/AdvanceLose → countdown → Bits
// before declaring the terminal result visible, and latches exactly once.
// Returns true only on the tick where the latch becomes visible.
// TODO(question): CommanderDeath==0 annihilation mode (defer); extra commanders
// per team and resurrection cases remain TODO(question).
func (s *Session) EvaluateResult(tick uint32) bool {
	if s == nil || s.Units == nil {
		return false
	}
	// TODO(question): CommanderDeath==0 annihilation mode not researched; defer.
	if s.Skirmish.CommanderDeath == 0 {
		return false
	}
	rs := getResultState(s)
	rs.mu.Lock()
	// Fast-path: already latched exactly once.
	if rs.result.Ended {
		rs.mu.Unlock()
		return false
	}

	// Compute allTeams from SkirmishConfig players (fallback per-owner).
	allTeams := make(map[int]struct{})
	if s.Skirmish.NumPlayers > 0 {
		n := s.Skirmish.NumPlayers
		if n > 10 {
			n = 10
		}
		for i := 0; i < n; i++ {
			team := s.teamForOwner(i)
			allTeams[team] = struct{}{}
		}
	} else {
		// Isolated test fallback: collect from units and econ
		for _, u := range s.Units.IterSliced() {
			if u == nil {
				continue
			}
			team := s.teamForOwner(int(u.Owner))
			allTeams[team] = struct{}{}
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				if s.Econ.Players[i].Exists {
					team := s.teamForOwner(i)
					allTeams[team] = struct{}{}
				}
			}
		}
		if len(allTeams) == 0 {
			// Ultimate fallback: per-owner
			for i := 0; i < 10; i++ {
				allTeams[100+i] = struct{}{}
			}
		}
	}

	// Active teams: those with at least one alive non-dying commander.
	active := make(map[int]struct{})
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Dying {
			continue
		}
		if u.Def == nil || !u.Def.Commander {
			continue
		}
		team := s.teamForOwner(int(u.Owner))
		active[team] = struct{}{}
	}
	activeCount := len(active)

	// If more than one hostile team remains, no terminal condition.
	if activeCount > 1 {
		rs.mu.Unlock()
		return false
	}

	// Terminal candidate: 0 or 1 active teams.
	var winner int
	var losers []int
	var draw bool
	reason := ReasonCommanderDeath
	if activeCount == 0 {
		draw = true
		winner = -1
		for t := range allTeams {
			losers = append(losers, t)
		}
		sort.Ints(losers)
	} else {
		// Exactly one active team remains.
		for t := range active {
			winner = t
			break
		}
		for t := range allTeams {
			if t != winner {
				losers = append(losers, t)
			}
		}
		sort.Ints(losers)
	}

	// If not yet pending, arm the latch.
	if !rs.pending {
		rs.pending = true
		rs.pendingWinner = winner
		rs.pendingDraw = draw
		rs.pendingReason = reason
		rs.pendingLosers = append([]int(nil), losers...)
		rs.armedTick = tick
		if draw {
			// Draw: arm without win/lose pending, just countdown to ending.
			s.Latch.Arm()
			s.Latch.Pending = 0
		} else {
			localTeam := s.teamForOwner(int(s.LocalOwner))
			isLocalWin := (winner == localTeam)
			if isLocalWin {
				s.Latch.AdvanceWin(true)
			} else {
				s.Latch.AdvanceLose(true)
			}
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		// Not yet visible until countdown crosses below zero.
		// For draw we armed; for win/lose we armed. Need to check if latch already ending in same tick (unlikely).
		if s.Latch.IsEnding() {
			// Rare immediate latch (if countdown already <0 edge). Commit now.
			rs.result = Result{
				Ended:      true,
				Draw:       draw,
				WinnerTeam: winner,
				Losers:     append([]int(nil), losers...),
				Reason:     reason,
				Tick:       tick,
				ArmedTick:  rs.armedTick,
			}
			if s.Snapshot != nil {
				view := snapshot.ResultView{
					Ended:      true,
					WinnerTeam: winner,
					Reason:     reason,
					Tick:       tick,
					Draw:       draw,
				}
				// Unlock before snapshot call to avoid deadlock if snapshot locks?
				// Snapshot's SetResultView locks independently.
				rs.mu.Unlock()
				s.Snapshot.SetResultView(view)
				rs.mu.Lock()
				if s.State == StateBattle {
					_ = s.TransitionTo(StatePostBattle)
				}
				// Callback after unlock below.
			} else {
				if s.State == StateBattle {
					_ = s.TransitionTo(StatePostBattle)
				}
			}
			// Fall through to callback handling after unlock.
		} else {
			rs.mu.Unlock()
			return false
		}
	} else {
		// Already pending: maybe upgrade win→draw if mutual destruction happened before latch completed.
		if draw && !rs.pendingDraw {
			rs.pendingDraw = true
			rs.pendingWinner = -1
			rs.pendingLosers = append([]int(nil), losers...)
			rs.pendingReason = reason
			s.Latch.Pending = 0 // clear win/lose pending, keep countdown
		}
		// Advance countdown toward latch. For draw we manually decrement; for win/lose we reuse Advance* with isDue=true.
		// Use isDue=true every evaluation for fast deterministic progress (5-6 evaluations to latch) while still reusing EndLatch.
		var latched bool
		if rs.pendingDraw {
			// Manual countdown for draw: reuse Arm's countdown but no win/lose bits.
			if s.Latch.Countdown >= 0 {
				s.Latch.Countdown--
				if s.Latch.Countdown < 0 {
					s.Latch.Bits |= LatchBitEnding
					latched = true
				}
			} else if !s.Latch.IsEnding() {
				s.Latch.Bits |= LatchBitEnding
				latched = true
			}
		} else {
			localTeam := s.teamForOwner(int(s.LocalOwner))
			isLocalWin := (rs.pendingWinner == localTeam)
			if isLocalWin {
				latched = s.Latch.AdvanceWin(true)
			} else {
				latched = s.Latch.AdvanceLose(true)
			}
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		if latched && s.Latch.IsEnding() {
			rs.result = Result{
				Ended:      true,
				Draw:       rs.pendingDraw,
				WinnerTeam: rs.pendingWinner,
				Losers:     append([]int(nil), rs.pendingLosers...),
				Reason:     rs.pendingReason,
				Tick:       tick,
				ArmedTick:  rs.armedTick,
			}
			if s.Snapshot != nil {
				view := snapshot.ResultView{
					Ended:      true,
					WinnerTeam: rs.result.WinnerTeam,
					Reason:     rs.result.Reason,
					Tick:       rs.result.Tick,
					Draw:       rs.result.Draw,
				}
				rs.mu.Unlock()
				s.Snapshot.SetResultView(view)
				rs.mu.Lock()
				if s.State == StateBattle {
					_ = s.TransitionTo(StatePostBattle)
				}
			} else {
				if s.State == StateBattle {
					_ = s.TransitionTo(StatePostBattle)
				}
			}
		} else {
			rs.mu.Unlock()
			return false
		}
	}

	// At this point rs.result.Ended is true and latch is visible.
	// Prepare callback firing exactly once after unlock.
	var cb func(Result)
	var rcopy Result
	if !rs.callbackFired && rs.result.Ended {
		cb = rs.callback
		rcopy = rs.result
		rs.callbackFired = true
		rs.callback = nil
	}
	rs.mu.Unlock()
	if cb != nil {
		cb(rcopy)
	}
	return true
}

// GetResultArmedTick returns the tick when the result was armed (milestone).
func (s *Session) GetResultArmedTick() uint32 {
	rs := getResultState(s)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return rs.armedTick
}

// ClearResult is used only by tests to reset state between fixtures.
func (s *Session) ClearResult() {
	rs := getResultState(s)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.result = Result{}
	rs.pending = false
	rs.pendingWinner = 0
	rs.pendingLosers = nil
	rs.pendingReason = ""
	rs.pendingDraw = false
	rs.armedTick = 0
	rs.callback = nil
	rs.callbackFired = false
}
