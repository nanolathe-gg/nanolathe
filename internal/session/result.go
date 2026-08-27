package session

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/frame"
)

// ReasonCommanderDeath is the skirmish commander-death termination reason
// [08 "Skirmish configuration"] CommanderDeath default 1 = commander death ends the game.
const ReasonCommanderDeath = "commander_death"

// ReasonAllUnits is the lobby skirmish all-units termination reason. The
// value-zero survival sweep is a supported inference pending an exact retail
// evaluator trace [08 "Skirmish configuration"].
const ReasonAllUnits = "all_units"

// Result is the authoritative latched terminal result for a skirmish session.
// Configured lobby skirmishes do not own the OTA mission-trigger queues; their
// result is computed from team/unit state instead. Direct OTA sessions retain
// the type-specific trigger path [08 "Evaluation"][08 "Skirmish configuration"].
// EndLatch countdown/bits per [P1-01 §2.2] already implemented in progression.go —
// reuse, do not bypass. Result is owned by Session (RS-05 RS-P0-012) not a
// package global; one terminal point is latch visible (Ended) and one stop point
// is State != Battle. Research does NOT decompose the general alliance endgame
// sweep: CommanderDeath==1 eliminates a team when all its commanders are dead;
// CommanderDeath==0 uses the supported-inference all-live-unit survival rule.
// TODO(question): trace the exact retail value-zero team-elimination sweep and
// its handling of extra/resurrected commanders; the UI text and observed setup
// semantics establish the distinction but not the executable sweep.
type Result struct {
	Ended      bool                `json:"ended"`
	Draw       bool                `json:"draw"`
	Kind       string              `json:"kind"` // "victory" | "defeat" | "draw" [RS-05][08]
	WinnerTeam int                 `json:"winner_team"`
	Winners    []int               `json:"winners,omitempty"`
	Losers     []int               `json:"losers"`
	Reason     string              `json:"reason"`
	Tick       uint32              `json:"tick"`
	ArmedTick  uint32              `json:"armed_tick,omitempty"`
	Countdown  int16               `json:"countdown"`
	Scores     []frame.ResultScore `json:"scores,omitempty"`
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
		cp := make([]frame.ResultScore, len(r.Scores))
		copy(cp, r.Scores)
		r.Scores = cp
	}
	return r
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
// Kills/Losses are tracked by player counters, but are not yet in
// economy.Player [P1-01 §2.3]. Until those counters and authored multipliers
// are wired, the snapshot carries neutral zero counters and score, plus the
// result kind per team.
func (s *Session) collectScores(winner int, draw bool) []frame.ResultScore {
	if s.Econ == nil {
		return nil
	}
	var out []frame.ResultScore
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
		kills := 0
		losses := 0
		// TODO(question): identify the authored kill/time multipliers and wire the
		// player kill/loss counters before deriving a nonzero score [P1-01 §2.3][P1-01 §8].
		score := 0
		out = append(out, frame.ResultScore{Player: i, Team: team, Kills: kills, Losses: losses, Score: score, Kind: kind})
	}
	return out
}

// EvaluateResult is the alliance-aware victory evaluator [08][RS-05][RR-04].
// The authoritative tick invokes it after death finalization each tick. It
// computes active teams from live units each evaluation. CommanderDeath==1
// counts only commanders; CommanderDeath==0 counts every unit, including
// buildings, as a supported inference from the lobby's commander-versus-all-
// units setup semantics [08 "Skirmish configuration"]. When <=1 hostile team
// remains it latches the result (draw on mutual destruction), preserving the
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
	commanderOnly := false
	switch s.Skirmish.CommanderDeath {
	case 0:
		// Supported inference: the menu's continue-after-commander-death mode
		// keeps a team alive while any unit remains. TODO(question): trace the
		// exact retail value-zero all-units sweep [08 "Skirmish configuration"].
	case 1:
		commanderOnly = true
	default:
		// TODO(question): value two has an established commander-respawn path,
		// but Session does not yet implement that placement/resource sequence.
		return false
	}
	if s.result.Ended {
		return false
	}
	// Compute allTeams from SkirmishConfig players (fallback per-owner).
	var allTeams [10]int
	allTeamCount := 0
	addTeam := func(team int) {
		for i := 0; i < allTeamCount; i++ {
			if allTeams[i] == team {
				return
			}
		}
		if allTeamCount < len(allTeams) {
			allTeams[allTeamCount] = team
			allTeamCount++
		}
	}
	if s.Skirmish.NumPlayers > 0 {
		n := s.Skirmish.NumPlayers
		if n > 10 {
			n = 10
		}
		for i := 0; i < n; i++ {
			addTeam(s.teamForOwner(i))
		}
	} else {
		for _, u := range s.Units.IterSliced() {
			if u == nil {
				continue
			}
			addTeam(s.teamForOwner(int(u.Owner)))
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				if s.Econ.Players[i].Exists {
					addTeam(s.teamForOwner(i))
				}
			}
		}
		if allTeamCount == 0 {
			for i := 0; i < 10; i++ {
				addTeam(100 + i)
			}
		}
	}
	// Active teams: those with at least one alive non-dying commander.
	var active [10]int
	activeCount := 0
	addActive := func(team int) {
		for i := 0; i < activeCount; i++ {
			if active[i] == team {
				return
			}
		}
		if activeCount < len(active) {
			active[activeCount] = team
			activeCount++
		}
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Dying {
			continue
		}
		if commanderOnly && (u.Def == nil || !u.Def.Commander) {
			continue
		}
		addActive(s.teamForOwner(int(u.Owner)))
	}
	if activeCount > 1 {
		return false
	}
	var winner int
	var losers []int
	var winners []int
	var draw bool
	reason := ReasonAllUnits
	if commanderOnly {
		reason = ReasonCommanderDeath
	}
	if activeCount == 0 {
		draw = true
		winner = -1
		for i := 0; i < allTeamCount; i++ {
			losers = append(losers, allTeams[i])
		}
		sort.Ints(losers)
	} else {
		winner = active[0]
		winners = []int{winner}
		for i := 0; i < allTeamCount; i++ {
			t := allTeams[i]
			if t != winner {
				losers = append(losers, t)
			}
		}
		sort.Ints(losers)
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
		// Store pending result metadata for the next committed frame (countdown
		// 4, not yet Ended).
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
		s.result = pending
		// Check immediate latch edge (Countdown already <0) — rare
		if s.Latch.IsEnding() {
			// commit now
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
			if s.State == StateBattle {
				_ = s.TransitionTo(StatePostBattle)
			}
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
		if s.State == StateBattle {
			_ = s.TransitionTo(StatePostBattle)
		}
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
	return false
}

// GetResultArmedTick returns the tick when the result was armed (milestone).
func (s *Session) GetResultArmedTick() uint32 {
	if s == nil {
		return 0
	}
	return s.resultArmedTick
}

// ResetResultForRetry clears result and latch for a clean retry [RS-05].
func (s *Session) ResetResultForRetry() {
	if s == nil {
		return
	}
	s.result = Result{}
	s.resultPending = false
	s.resultPendingWinner = 0
	s.resultPendingLosers = nil
	s.resultPendingReason = ""
	s.resultPendingDraw = false
	s.resultArmedTick = 0
	s.resultNextDue = 0
	s.Latch = NewEndLatch()
	s.VictoryDone = false
	s.DefeatDone = false
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			s.Econ.Players[i].GameEnded = false
			s.Econ.Players[i].EndGameCountdown = -1
		}
	}
}
