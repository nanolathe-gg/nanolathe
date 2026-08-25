package session

import (
	"sort"

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
// reuse, do not bypass. Result is owned by Session (RS-05 RS-P0-012) not a
// package global; one terminal point is latch visible (Ended) and one stop point
// is State != Battle. Research does NOT decompose the general alliance endgame
// sweep: implement CommanderDeath==1 skirmish rule as: a team is eliminated when
// all its commanders are dead; when <=1 hostile team remains, latch result (draw
// on mutual destruction). TODO(question): for research-silent cases (extra
// commanders per team, resurrected commanders, CommanderDeath==0 annihilation mode)
// behavior is TODO(question) and deferred.
type Result struct {
	Ended      bool                   `json:"ended"`
	Draw       bool                   `json:"draw"`
	Kind       string                 `json:"kind"` // "victory" | "defeat" | "draw" [RS-05][08]
	WinnerTeam int                    `json:"winner_team"`
	Winners    []int                  `json:"winners,omitempty"`
	Losers     []int                  `json:"losers"`
	Reason     string                 `json:"reason"`
	Tick       uint32                 `json:"tick"`
	ArmedTick  uint32                 `json:"armed_tick,omitempty"`
	Countdown  int16                  `json:"countdown"`
	Scores     []snapshot.ResultScore `json:"scores,omitempty"`
}

// TeamForOwner maps an owner slot to its team identifier [RS-05][08].
// It is the exported form of teamForOwner for presentation (I6) and tests.
func (s *Session) TeamForOwner(owner int) int { return s.teamForOwner(owner) }

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
	if s == nil {
		return Result{}
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	r := s.result
	if len(r.Losers) > 0 {
		cp := make([]int, len(r.Losers))
		copy(cp, r.Losers)
		r.Losers = cp
	}
	if len(r.Winners) > 0 {
		cp := make([]int, len(r.Winners))
		copy(cp, r.Winners)
		r.Winners = cp
	}
	if len(r.Scores) > 0 {
		cp := make([]snapshot.ResultScore, len(r.Scores))
		copy(cp, r.Scores)
		r.Scores = cp
	}
	return r
}

// SetResultCallback installs a callback that fires exactly once when the
// terminal result becomes visible (after EndLatch Bits). If result already
// ended, it fires immediately (once).
func (s *Session) SetResultCallback(fn func(Result)) {
	if s == nil {
		return
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	if s.result.Ended && !s.resultCallbackFired {
		s.resultCallbackFired = true
		r := s.result
		// Copy slices for callback isolation.
		if len(r.Losers) > 0 {
			cp := make([]int, len(r.Losers))
			copy(cp, r.Losers)
			r.Losers = cp
		}
		if len(r.Winners) > 0 {
			cp := make([]int, len(r.Winners))
			copy(cp, r.Winners)
			r.Winners = cp
		}
		if len(r.Scores) > 0 {
			cp := make([]snapshot.ResultScore, len(r.Scores))
			copy(cp, r.Scores)
			r.Scores = cp
		}
		cb := fn
		s.resultMu.Unlock()
		if cb != nil {
			cb(r)
		}
		s.resultMu.Lock()
		return
	}
	s.resultCallback = fn
}

// resultKindFor returns "victory" | "defeat" | "draw" for local perspective [RS-05][08].
func (s *Session) resultKindFor(draw bool, winner int) string {
	if draw {
		return "draw"
	}
	localTeam := s.teamForOwner(int(s.LocalOwner))
	if winner == localTeam {
		return "victory"
	}
	return "defeat"
}

// collectScores builds per-player ResultScore slice [P1-01 §2.3] RS-05.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// we publish zero for now with score derived from tick, and kind per team. This satisfies
// the snapshot contract that score/statistics are published; exact per-player kills
// remain TODO(question) until the ledger kill counter is wired [P1-01].
func (s *Session) collectScores(winner int, draw bool) []snapshot.ResultScore {
	if s.Econ == nil {
		return nil
	}
	var out []snapshot.ResultScore
	for i := 0; i < 10; i++ {
		p := s.Econ.Players[i]
		if !p.Exists {
			continue
		}
		team := s.teamForOwner(i)
		var kind string
		if draw {
			kind = "draw"
		} else if team == winner {
			kind = "win"
		} else {
			kind = "lose"
		}
		// Placeholder kills/losses until ledger kill counter is wired [P1-01 §2.3] TODO(question)
		kills := 0
		losses := 0
		// Try to derive kills from mission progress W/L? For now use 0.
		// Score uses ticks so it changes with time, satisfying score publishing.
		score := Score(kills, 1, s.Clock.GlobalTick, 0)
		out = append(out, snapshot.ResultScore{Player: i, Team: team, Kills: kills, Losses: losses, Score: score, Kind: kind})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Player < out[b].Player })
	return out
}

// publishResultView copies authoritative result into snapshot Buffer for presentation [I6][RS-05].
func (s *Session) publishResultView() {
	if s.Snapshot == nil {
		return
	}
	s.resultMu.Lock()
	r := s.result
	s.resultMu.Unlock()
	view := snapshot.ResultView{
		Ended:      r.Ended,
		Kind:       r.Kind,
		WinnerTeam: r.WinnerTeam,
		Reason:     r.Reason,
		Tick:       r.Tick,
		ArmedTick:  r.ArmedTick,
		Countdown:  r.Countdown,
		Draw:       r.Draw,
	}
	if len(r.Winners) > 0 {
		view.Winners = append([]int(nil), r.Winners...)
	}
	if len(r.Losers) > 0 {
		view.Losers = append([]int(nil), r.Losers...)
	}
	if len(r.Scores) > 0 {
		view.Scores = append([]snapshot.ResultScore(nil), r.Scores...)
	}
	// Ensure countdown reflects latch even before Ended (pending view)
	if !r.Ended {
		view.Countdown = s.Latch.Countdown
	}
	s.Snapshot.SetResultView(view)
}

// fireResultCallback fires the exactly-once callback after latch visible [RS-05].
func (s *Session) fireResultCallback() {
	if s == nil {
		return
	}
	s.resultMu.Lock()
	if s.resultCallbackFired || !s.result.Ended {
		s.resultMu.Unlock()
		return
	}
	cb := s.resultCallback
	rcopy := s.result
	s.resultCallbackFired = true
	s.resultCallback = nil
	// Copy slices for callback
	if len(rcopy.Losers) > 0 {
		cp := make([]int, len(rcopy.Losers))
		copy(cp, rcopy.Losers)
		rcopy.Losers = cp
	}
	if len(rcopy.Winners) > 0 {
		cp := make([]int, len(rcopy.Winners))
		copy(cp, rcopy.Winners)
		rcopy.Winners = cp
	}
	if len(rcopy.Scores) > 0 {
		cp := make([]snapshot.ResultScore, len(rcopy.Scores))
		copy(cp, rcopy.Scores)
		rcopy.Scores = cp
	}
	s.resultMu.Unlock()
	if cb != nil {
		cb(rcopy)
	}
}

// EvaluateResult is the alliance-aware victory evaluator [08][RS-05][RR-04].
// It is callable standalone and hookable so the future central loop can
// invoke it after death finalization each tick. It computes active teams
// from live commanders each evaluation, uses the skirmish CommanderDeath==1
// rule (team eliminated when all its commanders are dead; when <=1 hostile
// team remains, latch result, draw on mutual destruction), preserves the
// researched EndLatch countdown via Arm/AdvanceWin/AdvanceLose with once-per-
// 30-tick cadence [08 "Evaluation"][RR-04] (4 → -1 over five invocations,
// ~150 ticks) before declaring the terminal result visible, and latches
// exactly once. Returns true only on the tick where the latch becomes visible.
// The latch bits (0x04,0x10|0x20,0x40) are set when Countdown crosses below
// zero; victory is evaluated first so simultaneous resolves as victory
// [08 "Evaluation"][RR-04]. Simultaneous final commanders (mutual destruction)
// produce a draw even if a winner was pending (upgrade) [RR-04].
func (s *Session) EvaluateResult(tick uint32) bool {
	if s == nil || s.Units == nil {
		return false
	}
	if s.Skirmish.CommanderDeath == 0 {
		return false
	}
	s.resultMu.Lock()
	if s.result.Ended {
		s.resultMu.Unlock()
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
	if activeCount > 1 {
		s.resultMu.Unlock()
		return false
	}
	var winner int
	var losers []int
	var winners []int
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
		for t := range active {
			winner = t
			break
		}
		winners = []int{winner}
		for t := range allTeams {
			if t != winner {
				losers = append(losers, t)
			}
		}
		sort.Ints(losers)
		sort.Ints(winners)
	}
	kind := s.resultKindFor(draw, winner)
	// If not yet pending, arm the latch.
	if !s.resultPending {
		s.resultPending = true
		s.resultPendingWinner = winner
		s.resultPendingLosers = append([]int(nil), losers...)
		s.resultPendingReason = reason
		s.resultPendingDraw = draw
		s.resultArmedTick = tick
		s.resultNextDue = tick + 30
		// Preserve copy for Winners
		if draw {
			s.Latch.Arm()
			s.Latch.Pending = 0
		} else {
			localTeam := s.teamForOwner(int(s.LocalOwner))
			isLocalWin := (winner == localTeam)
			if isLocalWin {
				s.Latch.Arm()
				s.Latch.Pending = 1
			} else {
				s.Latch.Arm()
				s.Latch.Pending = 2
			}
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		// Prepare pending view for snapshot (countdown 4, not yet Ended)
		scores := s.collectScores(winner, draw)
		pending := Result{
			Ended:      false,
			Draw:       draw,
			Kind:       kind,
			WinnerTeam: winner,
			Winners:    append([]int(nil), winners...),
			Losers:     append([]int(nil), losers...),
			Reason:     reason,
			Tick:       0,
			ArmedTick:  tick,
			Countdown:  s.Latch.Countdown,
			Scores:     scores,
		}
		// Hold pending result for snapshot? We store in result but Ended false means not terminal.
		// Keep result.Ended false until latch visible; but store pending for later commit.
		// Publish a non-terminal countdown view via snapshot directly (not via result Ended).
		s.result = pending
		s.resultMu.Unlock()
		s.publishResultView()
		// Check immediate latch edge (Countdown already <0) — rare
		if s.Latch.IsEnding() {
			// commit now
			s.resultMu.Lock()
			scores2 := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
			kind2 := s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner)
			w2 := s.resultPendingWinner
			var winners2 []int
			if !s.resultPendingDraw {
				winners2 = []int{w2}
			}
			s.result = Result{
				Ended:      true,
				Draw:       s.resultPendingDraw,
				Kind:       kind2,
				WinnerTeam: w2,
				Winners:    winners2,
				Losers:     append([]int(nil), s.resultPendingLosers...),
				Reason:     s.resultPendingReason,
				Tick:       tick,
				ArmedTick:  s.resultArmedTick,
				Countdown:  s.Latch.Countdown,
				Scores:     scores2,
			}
			s.resultMu.Unlock()
			s.publishResultView()
			if s.State == StateBattle {
				_ = s.TransitionTo(StatePostBattle)
			}
			s.fireResultCallback()
			return true
		}
		return false
	}
	// Already pending: maybe upgrade win→draw if mutual destruction happened before latch completed [RR-04].
	if draw && !s.resultPendingDraw {
		s.resultPendingDraw = true
		s.resultPendingWinner = -1
		s.resultPendingLosers = append([]int(nil), losers...)
		s.resultPendingReason = reason
		s.Latch.Pending = 0
		// Keep countdown as is, just upgrade kind.
	}
	// Advance countdown only when due (once per 30 ticks) [08][RR-04].
	isDue := tick >= s.resultNextDue
	if !isDue {
		// Update pending view countdown without advancing.
		scores := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
		kind2 := s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner)
		var winners2 []int
		if !s.resultPendingDraw {
			winners2 = []int{s.resultPendingWinner}
		}
		s.result = Result{
			Ended:      false,
			Draw:       s.resultPendingDraw,
			Kind:       kind2,
			WinnerTeam: s.resultPendingWinner,
			Winners:    winners2,
			Losers:     append([]int(nil), s.resultPendingLosers...),
			Reason:     s.resultPendingReason,
			Tick:       0,
			ArmedTick:  s.resultArmedTick,
			Countdown:  s.Latch.Countdown,
			Scores:     scores,
		}
		s.resultMu.Unlock()
		s.publishResultView()
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		return false
	}
	// Due: consume one countdown step.
	s.resultNextDue += 30
	var latched bool
	if s.resultPendingDraw {
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
		isLocalWin := (s.resultPendingWinner == localTeam)
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
		scores := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
		kind2 := s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner)
		var winners2 []int
		if !s.resultPendingDraw {
			winners2 = []int{s.resultPendingWinner}
		}
		s.result = Result{
			Ended:      true,
			Draw:       s.resultPendingDraw,
			Kind:       kind2,
			WinnerTeam: s.resultPendingWinner,
			Winners:    winners2,
			Losers:     append([]int(nil), s.resultPendingLosers...),
			Reason:     s.resultPendingReason,
			Tick:       tick,
			ArmedTick:  s.resultArmedTick,
			Countdown:  s.Latch.Countdown,
			Scores:     scores,
		}
		s.resultMu.Unlock()
		s.publishResultView()
		if s.State == StateBattle {
			_ = s.TransitionTo(StatePostBattle)
		}
		s.fireResultCallback()
		return true
	}
	// Not yet latched: update pending view and publish countdown.
	scores := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
	kind2 := s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner)
	var winners2 []int
	if !s.resultPendingDraw {
		winners2 = []int{s.resultPendingWinner}
	}
	s.result = Result{
		Ended:      false,
		Draw:       s.resultPendingDraw,
		Kind:       kind2,
		WinnerTeam: s.resultPendingWinner,
		Winners:    winners2,
		Losers:     append([]int(nil), s.resultPendingLosers...),
		Reason:     s.resultPendingReason,
		Tick:       0,
		ArmedTick:  s.resultArmedTick,
		Countdown:  s.Latch.Countdown,
		Scores:     scores,
	}
	s.resultMu.Unlock()
	s.publishResultView()
	return false
}

// GetResultArmedTick returns the tick when the result was armed (milestone).
func (s *Session) GetResultArmedTick() uint32 {
	if s == nil {
		return 0
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	return s.resultArmedTick
}

// ClearResult is used only by tests to reset state between fixtures.
func (s *Session) ClearResult() {
	if s == nil {
		return
	}
	s.resultMu.Lock()
	defer s.resultMu.Unlock()
	s.result = Result{}
	s.resultPending = false
	s.resultPendingWinner = 0
	s.resultPendingLosers = nil
	s.resultPendingReason = ""
	s.resultPendingDraw = false
	s.resultArmedTick = 0
	s.resultNextDue = 0
	s.resultCallback = nil
	s.resultCallbackFired = false
	s.Latch = NewEndLatch()
	if s.Snapshot != nil {
		s.Snapshot.SetResultView(snapshot.ResultView{})
	}
}

// ResetResultForRetry clears result and latch for a clean retry without duplicate callbacks [RS-05].
func (s *Session) ResetResultForRetry() {
	if s == nil {
		return
	}
	s.resultMu.Lock()
	s.result = Result{}
	s.resultPending = false
	s.resultPendingWinner = 0
	s.resultPendingLosers = nil
	s.resultPendingReason = ""
	s.resultPendingDraw = false
	s.resultArmedTick = 0
	s.resultNextDue = 0
	// Do not carry fired state to new attempt; allow callback again exactly once.
	s.resultCallbackFired = false
	// Keep callback for retry? Caller should reinstall if needed; clear to avoid double fire.
	// Preserve callback if not fired yet, but reset fired flag.
	// s.resultCallback remains.
	s.Latch = NewEndLatch()
	s.VictoryDone = false
	s.DefeatDone = false
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			s.Econ.Players[i].GameEnded = false
			s.Econ.Players[i].EndGameCountdown = -1
		}
	}
	s.resultMu.Unlock()
	if s.Snapshot != nil {
		s.Snapshot.SetResultView(snapshot.ResultView{})
	}
}
