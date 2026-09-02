package session

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
)

// ReasonCommanderDeath is the skirmish commander-death termination reason
// [08 "Skirmish configuration"] CommanderDeath default 1 = commander death ends the game.
const ReasonCommanderDeath = "commander_death"

// ReasonAllUnits is the lobby skirmish all-units termination reason [08
// R-SKIR-01 §3][08 R-TRIG-01 §6].
const ReasonAllUnits = "all_units"

// Result is the authoritative latched terminal result for a skirmish session.
// Configured lobby skirmishes do not own the OTA mission-trigger queues; their
// result is computed from team/unit state instead. Direct OTA sessions retain
// the type-specific trigger path [08 "Evaluation"][08 "Skirmish configuration"].
// EndLatch countdown/bits per [P1-01 §2.2] already implemented in progression.go —
// reuse, do not bypass. Result is owned by Session (RS-05 RS-P0-012) not a
// package global; one terminal point is latch visible (Ended) and one stop point
// is State != Battle. The value-zero alliance corner remains deliberately
// narrow: research establishes the live-unit predicate but does not settle
// how a non-local alliance aggregate is reduced. Keep that question at this
// owner/team boundary until the deciding retail trace is available [08
// R-SKIR-01 §3][08 R-TRIG-01 §6].
type Result struct {
	Ended        bool                `json:"ended"`
	Draw         bool                `json:"draw"`
	Kind         string              `json:"kind"` // "victory" | "defeat" | "draw" [RS-05][08]
	WinnerTeam   int                 `json:"winner_team"`
	Winners      []int               `json:"winners,omitempty"`
	Losers       []int               `json:"losers"`
	Reason       string              `json:"reason"`
	Tick         uint32              `json:"tick"`
	ArmedTick    uint32              `json:"armed_tick,omitempty"`
	Countdown    int16               `json:"countdown"`
	Scores       []frame.ResultScore `json:"scores,omitempty"`
	ColumnMaxima [7]int              `json:"column_maxima,omitempty"`
}

// TeamForOwner maps an owner slot to its team identifier [RS-05][08].
// It is the exported form of teamForOwner for presentation (I6) and tests.
func (s *Session) TeamForOwner(owner int) int { return s.teamForOwner(owner) }

// teamForOwner maps an owner slot to its team identifier using
// SkirmishConfig.Players[].AllyGroup with fallback per-owner teams.
// AllyGroup 5 is the unassigned sentinel [08 "Skirmish configuration"]
// [GAP T14]; when it appears we treat each owner as its own hostile team
// (100+owner) so that default skirmishes are FFA. Explicit non-sentinel
// groups share a team. TODO(question): the retail trace names the local
// player's first alliance row for the value-zero alliance aggregate, but does
// not establish how a non-local aggregate is reduced; retain this narrow
// unresolved corner rather than generalizing it [08 R-TRIG-01 §6].
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

// FrozenResultView returns the immutable presentation copy consumed by the
// post-battle controller. It is a value snapshot, including all result slices,
// so presentation sequencing cannot observe a later authoritative mutation
// [03 §2.4][08 R-CAMP-01 §6].
func (s *Session) FrozenResultView() frame.ResultView {
	if s == nil {
		return frame.ResultView{}
	}
	r := s.GetResult()
	return frame.ResultView{
		Ended: r.Ended, Kind: r.Kind, WinnerTeam: r.WinnerTeam,
		Winners: append([]int(nil), r.Winners...), Losers: append([]int(nil), r.Losers...),
		Reason: r.Reason, Tick: r.Tick, ArmedTick: r.ArmedTick,
		Countdown: r.Countdown, Draw: r.Draw,
		Scores: append([]frame.ResultScore(nil), r.Scores...), ColumnMaxima: r.ColumnMaxima,
	}
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

// collectScores builds the immutable per-player result rows [08 R-CAMP-01 §7].
// All counters and economy totals are read after their authoritative event
// sites have committed them; conversions here are presentation-data shaping,
// not simulation mutations.
func (s *Session) collectScores(winner int, draw bool) []frame.ResultScore {
	if s.Econ == nil {
		return nil
	}
	var out []frame.ResultScore
	for i := 0; i < 10; i++ {
		p := s.Econ.Players[i]
		if !s.resultScoreRowEligible(i, p) {
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
		kills := int(p.Kills)
		losses := int(p.Losses)
		energyProduced := int(p.TotalProduced[1])
		metalProduced := int(p.TotalProduced[0])
		energyConsumed := int(p.TotalConsumed[1])
		metalConsumed := int(p.TotalConsumed[0])
		energyWasted := int(p.Waste[1])
		metalWasted := int(p.Waste[0])
		score := s.resultScore(kills)
		name := p.Name
		logo := int(p.Logo)
		if s.Skirmish.NumPlayers > 0 && i < len(s.Skirmish.Players) {
			sp := s.Skirmish.Players[i]
			if name == "" {
				name = sp.Nickname
			}
			if logo == 0 {
				logo = sp.Color
			}
		}
		out = append(out, frame.ResultScore{
			Player: i, Team: team, Name: name, Logo: uint8(logo),
			Kills: kills, Losses: losses,
			EnergyProduced: energyProduced, MetalProduced: metalProduced,
			EnergyConsumed: energyConsumed, MetalConsumed: metalConsumed,
			EnergyWasted: energyWasted, MetalWasted: metalWasted,
			CommandersKilled: int(p.CommanderKills), CommandersLost: int(p.CommanderLosses),
			Score: score, Kind: kind,
		})
	}
	return out
}

// resultScoreRowEligible is the ENDMSN row gate. Skirmish setup metadata is
// authoritative when present; otherwise the runtime player record supplies the
// controller/side fields. Watchers and rejected records never receive a row,
// while the established auxiliary-word escape is retained [08 R-CAMP-01 §7].
func (s *Session) resultScoreRowEligible(i int, p economy.Player) bool {
	if i < 0 || i >= 10 || !p.Exists || p.RejectionReason != 0 {
		return false
	}
	if p.ResultAuxiliary != 0 {
		return true
	}
	controller := p.ControllerState
	side := int(p.Side)
	watcher := p.Watcher
	if s.Skirmish.NumPlayers > 0 && i < len(s.Skirmish.Players) {
		sp := s.Skirmish.Players[i]
		if sp.IsObserver() {
			return false
		}
		if sp.Controller == SkirmishControllerHuman {
			controller = 1
		} else if sp.Controller == SkirmishControllerObserver {
			controller = 3
		} else {
			controller = 2
		}
		side = sp.Side
	}
	if controller != 1 && controller != 2 && controller != 3 {
		return false
	}
	if side == 10 || watcher {
		return false
	}
	return true
}

// resultScore reads authored kill/time multipliers from the selected mission
// and performs the two __ftol conversions in retail order [08 R-CAMP-01 §7].
func (s *Session) resultScore(kills int) int {
	var killMul, timeMul float32
	if s != nil && s.Mission != nil && s.Mission.OTA != nil {
		if globals := mission.DecodeMissionGlobals(s.Mission.OTA.Global); globals != nil {
			killMul = float32(globals.KillMul)
			timeMul = float32(globals.TimeMul)
		}
	}
	var tick uint32
	if s != nil && s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	// The source expression explicitly narrows the elapsed-tick and kill
	// values to the authored single-precision multiplier path before each
	// __ftol conversion [08 R-CAMP-01 §7].
	timeTerm := int64(int32(float32(tick/60) * float32(timeMul)))
	killTerm := int64(int32(float32(kills) * float32(killMul)))
	total := timeTerm + killTerm
	if total < 0 {
		return 0
	}
	return int(total)
}

func resultColumnMaxima(rows []frame.ResultScore) [7]int {
	max := [7]int{10, 10, 100, 100, 100, 100, 100}
	for _, row := range rows {
		values := [...]int{row.Kills, row.Losses, row.EnergyProduced, row.MetalProduced, row.EnergyWasted, row.MetalWasted, row.Score}
		for i, value := range values {
			if value > max[i] {
				max[i] = value
			}
		}
	}
	return max
}

// EvaluateResult is the alliance-aware victory evaluator [08][RS-05][RR-04].
// The authoritative tick invokes it after death finalization each tick. It
// computes active teams from live units each evaluation. Both non-deathmatch
// values use owner live-unit counts; rule 1's commander transition first
// sweeps the owner's remaining units. When <=1 hostile team
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
	if s.result.Ended {
		return false
	}
	// Finalization normally calls this before the evaluator. Keeping the call
	// here also covers direct composition seams that invoke EvaluateResult after
	// filing a death through the unit world [08 R-SKIR-01 §3].
	s.processPendingCommanderDeaths(tick)
	rule := CommanderDeathMode(s.Skirmish.CommanderDeath)
	if rule == CommanderDeathDeathmatch {
		if s.deathmatchActive {
			_ = s.advanceDeathmatch(tick)
			return false
		}
		if !s.deathmatchExhausted {
			// Deathmatch does not end through elimination while respawn remains
			// applicable; a commander loss is the only defeat trigger here [08
			// R-SKIR-01 §3].
			return false
		}
	}
	// Both non-deathmatch values use owner live-unit accounting. Rule 1's
	// owner sweep drives that count to zero; it is not a commander-only scan
	// [08 R-SKIR-01 §3].
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
			if !s.resultOwnerEligible(i) {
				continue
			}
			// A participating slot that has not created any unit is not an
			// eliminated opponent; victory waits for its first allocation
			// [08 R-SKIR-01 §3] "Victory detection". "Not eliminated with
			// nothing alive" is the counters' way of saying "ever created is
			// zero" — the predicate's second term.
			if !s.ownerEliminated(i) && s.Units.LiveCountForPlayer(i) == 0 && !s.resultPending {
				return false
			}
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
				if s.resultOwnerEligible(i) {
					if !s.ownerEliminated(i) && s.Units.LiveCountForPlayer(i) == 0 && !s.resultPending {
						return false
					}
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
	// Active owners are derived from the authoritative live counters. A death
	// remains live until phase-2 finalization, so a same-tick commander sweep
	// cannot arm victory merely because a unit has been marked Dying
	// [08 R-SKIR-01 §3]. In skirmish, each participating player's counter is
	// tested independently; AllyGroup affects result presentation only and does
	// not collapse allied players into one surviving side. A zero live count is
	// the whole test here: by the loop above, every eligible slot has created a
	// unit, so a zero count is exactly ownerEliminated — the sweep's "skip the
	// eliminated slot" leg [08 R-SKIR-01 §3] "Victory detection".
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
	activeOwner := -1
	if s.Skirmish.NumPlayers > 0 {
		n := s.Skirmish.NumPlayers
		if n > len(active) {
			n = len(active)
		}
		for owner := 0; owner < n; owner++ {
			if !s.resultOwnerEligible(owner) || s.Units.LiveCountForPlayer(owner) == 0 {
				continue
			}
			activeOwner = owner
			addActive(owner)
		}
	} else {
		for owner := 0; owner < len(active); owner++ {
			if s.resultOwnerEligible(owner) && s.Units.LiveCountForPlayer(owner) != 0 {
				activeOwner = owner
				addActive(s.teamForOwner(owner))
			}
		}
	}
	if activeCount > 1 {
		return false
	}
	var winner int
	var losers []int
	var winners []int
	var draw bool
	reason := ReasonAllUnits
	if rule != CommanderDeathContinues {
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
		if activeOwner >= 0 {
			winner = s.teamForOwner(activeOwner)
		} else {
			winner = active[0]
		}
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
			Ended:        false,
			Draw:         draw,
			Kind:         kind,
			WinnerTeam:   winner,
			Winners:      append([]int(nil), winners...),
			Losers:       append([]int(nil), losers...),
			Reason:       reason,
			Tick:         0,
			ArmedTick:    tick,
			Countdown:    s.Latch.Countdown,
			Scores:       scores,
			ColumnMaxima: resultColumnMaxima(scores),
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
				Ended:        true,
				Draw:         s.resultPendingDraw,
				Kind:         kind2,
				WinnerTeam:   w2,
				Winners:      winners2,
				Losers:       append([]int(nil), s.resultPendingLosers...),
				Reason:       s.resultPendingReason,
				Tick:         tick,
				ArmedTick:    s.resultArmedTick,
				Countdown:    s.Latch.Countdown,
				Scores:       scores2,
				ColumnMaxima: resultColumnMaxima(scores2),
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
			Ended:        false,
			Draw:         s.resultPendingDraw,
			Kind:         kind2,
			WinnerTeam:   s.resultPendingWinner,
			Winners:      winners2,
			Losers:       append([]int(nil), s.resultPendingLosers...),
			Reason:       s.resultPendingReason,
			Tick:         0,
			ArmedTick:    s.resultArmedTick,
			Countdown:    s.Latch.Countdown,
			Scores:       scores,
			ColumnMaxima: resultColumnMaxima(scores),
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
			Ended:        true,
			Draw:         s.resultPendingDraw,
			Kind:         kind2,
			WinnerTeam:   s.resultPendingWinner,
			Winners:      winners2,
			Losers:       append([]int(nil), s.resultPendingLosers...),
			Reason:       s.resultPendingReason,
			Tick:         tick,
			ArmedTick:    s.resultArmedTick,
			Countdown:    s.Latch.Countdown,
			Scores:       scores,
			ColumnMaxima: resultColumnMaxima(scores),
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
		Ended:        false,
		Draw:         s.resultPendingDraw,
		Kind:         kind2,
		WinnerTeam:   s.resultPendingWinner,
		Winners:      winners2,
		Losers:       append([]int(nil), s.resultPendingLosers...),
		Reason:       s.resultPendingReason,
		Tick:         0,
		ArmedTick:    s.resultArmedTick,
		Countdown:    s.Latch.Countdown,
		Scores:       scores,
		ColumnMaxima: resultColumnMaxima(scores),
	}
	return false
}

// ownerEliminated is the elimination predicate the victory sweep, the phase-2
// player gate and the sharing dispatcher share: live count zero AND at least
// one unit ever created [08 R-SKIR-01 §3][05 R-SHARE-01 §3]. It is derived from
// the player record's two counters; there is no elimination flag to read.
func (s *Session) ownerEliminated(owner int) bool {
	if s == nil {
		return false
	}
	return economy.PlayerEliminated(s.Units, owner)
}

func (s *Session) resultOwnerEligible(owner int) bool {
	if s == nil || owner < 0 || owner >= 10 {
		return false
	}
	if s.Skirmish.NumPlayers > 0 && owner < s.Skirmish.NumPlayers {
		// Skirmish setup rows are the participating-player authority. The
		// economy controller byte is a runtime binding and may still be its
		// zero fixture value when a result is evaluated directly [08
		// R-SKIR-01 §3].
		return !s.Skirmish.Players[owner].IsObserver()
	}
	if s.Econ != nil {
		p := s.Econ.Players[owner]
		return p.Exists && !p.IsObserver && (p.ControllerState == 1 || p.ControllerState == 2)
	}
	return !s.Skirmish.Players[owner].IsObserver()
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
	s.pendingCommanderDeaths = [10]bool{}
	s.deathmatchCountdown = 0
	s.deathmatchNextDue = 0
	s.deathmatchActive = false
	s.deathmatchAttempts = 0
	s.deathmatchExhausted = false
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
