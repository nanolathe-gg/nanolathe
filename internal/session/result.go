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
// is State != Battle. The end predicates themselves no longer have an open
// alliance corner: [08 R-TRIG-01 §6] settles the victory sweep's ally skip as
// the local player's first alliance row, and victorySweep applies it. What
// stays open is only the team identity the post-battle screen prints, at
// teamForOwner below [08 R-SKIR-01 §3][08 R-TRIG-01 §6].
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
// player's first alliance row, which the end predicates now read directly
// (victorySweep), but it does not establish how a non-local aggregate is
// reduced to the one team name a result row prints; retain this narrow
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

// resultWinnersFor is the presentation winner list. A draw and a defeat with
// no surviving opponent both name nobody, so the list stays empty rather than
// carrying the -1 sentinel into the frame [08 R-TRIG-01 §6].
func resultWinnersFor(winner int, draw bool) []int {
	if draw || winner < 0 {
		return nil
	}
	return []int{winner}
}

// localDefeated is the defeat predicate of [08 R-SKIR-01 §3] "Defeat
// detection" for session kinds 2 and 3: the local player's live unit count is
// zero. It is not a team aggregate — an allied peer that is still fighting
// does not keep the local player in the game.
//
// The one guard beyond the counter is the ever-created term of the derived
// elimination predicate. Retail's live count is decremented by the kill-record
// handler, so a slot can only reach zero by having held a unit; our evaluator
// runs from the first sub-tick, before which a fixture that has allocated
// nothing would read zero and latch. The term is unobservable in every state
// retail can reach [08 R-SKIR-01 §3] "Counters".
func (s *Session) localDefeated() bool {
	if s == nil || s.Units == nil {
		return false
	}
	local := int(s.LocalOwner)
	if local < 0 || local >= 10 || !s.resultOwnerEligible(local) {
		return false
	}
	return s.ownerEliminated(local)
}

// victorySweep is the kind-2 elimination sweep of [08 R-TRIG-01 §6] "The
// kind-2 victory sweep": walk slots 0–9; skip the local slot; skip any slot
// whose byte in the local player's first alliance row is non-zero (an ally);
// skip any slot with a zero live-unit count; if any slot survives the skips
// there is no victory; after all ten, victory.
//
// There is no shared-victory bit, no controller or elimination test and no
// rule-word test: those belong to the kind-3 sweep that [08 R-SKIR-01 §3]
// "Victory detection" describes, and that section's closing "allies included"
// sentence is explicitly wrong for kind 2 — allied players are excluded by the
// first alliance row, which battle entry fills from the setup screen's team
// groups [08 R-SKIR-01 §2].
func (s *Session) victorySweep() bool {
	if s == nil || s.Units == nil {
		return false
	}
	local := int(s.LocalOwner)
	for owner := 0; owner < 10; owner++ {
		if owner == local {
			continue
		}
		if s.Econ != nil && s.Econ.DeclaresAlliance(uint8(local), uint8(owner)) {
			continue
		}
		if s.Units.LiveCountForPlayer(owner) == 0 {
			continue
		}
		return false
	}
	return true
}

// EvaluateResult is the skirmish end-condition block. The authoritative tick
// invokes it after death finalization each tick. Both non-deathmatch rule
// values use owner live-unit counts; rule 1's commander transition first
// sweeps the owner's remaining units.
//
// It runs retail's two separate predicates in retail's order for session kinds
// 2/3 [08 R-TRIG-01 §6]: the defeat predicate — the local player's live unit
// count is zero [08 R-SKIR-01 §3] — is evaluated first and takes the lost
// path; only when it is false does the victory sweep run and take the won
// path. Defeat wins a tie, so a wipe that leaves nobody standing is a local
// defeat and not a draw.
//
// **Correction.** The previous implementation folded both predicates into one
// count of surviving sides and latched when at most one remained. That fold
// could not see two of retail's outcomes: a local elimination while two other
// players were still fighting never latched at all, and an allied peer's
// survival blocked a victory the alliance-row skip excludes.
//
// A true predicate arms the shared EndLatch and steps it once per 30-tick due
// via Arm/AdvanceWin/AdvanceLose (4 → -1 over five dues, ~150 ticks) before
// the terminal result becomes visible, and latches exactly once. Returns true
// only on the tick where the latch becomes visible; the latch bits are set
// when Countdown crosses below zero [08 R-TRIG-01 §6] "Countdown and latch".
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
	//
	// The two predicates run in the order [08 R-TRIG-01 §6] establishes for
	// session kinds 2/3: the defeat predicate first and, if true, the lost
	// path; otherwise the victory sweep and, if true, the won path. Defeat
	// therefore wins a tie and at most one predicate advances the shared
	// countdown per due.
	localDefeated := s.localDefeated()
	victory := false
	if !localDefeated {
		victory = s.victorySweep()
	}
	if !localDefeated && !victory {
		return false
	}
	// The team lists are presentation identity only: retail's kind-2 end
	// writes the shared countdown and the latch bits and names no winner
	// [08 R-TRIG-01 §6]. Nanolathe's post-battle screen labels the winning and
	// losing teams, so they are derived here from the same eligibility the
	// score rows use.
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
	var losers []int
	var winners []int
	// Retail's kind-2 end has no draw: the lost path is taken whenever the
	// local live count is zero, whether or not anything else survives
	// [08 R-TRIG-01 §6]. The flag is kept on Result for the presentation view
	// and is never set here.
	const draw = false
	reason := ReasonAllUnits
	if rule != CommanderDeathContinues {
		reason = ReasonCommanderDeath
	}
	winner := -1
	if victory {
		winner = s.teamForOwner(int(s.LocalOwner))
	} else {
		// Local defeat names the lowest-numbered surviving opponent's team so
		// the post-battle screen has a winner to print. When nothing survives
		// there is no team to name and the result stays a local defeat with no
		// winner — retail writes only the latch bits there [08 R-TRIG-01 §6].
		for owner := 0; owner < 10; owner++ {
			if owner == int(s.LocalOwner) || !s.resultOwnerEligible(owner) {
				continue
			}
			if s.Units.LiveCountForPlayer(owner) == 0 {
				continue
			}
			winner = s.teamForOwner(owner)
			break
		}
	}
	if winner >= 0 {
		winners = []int{winner}
	}
	for i := 0; i < allTeamCount; i++ {
		if t := allTeams[i]; t != winner {
			losers = append(losers, t)
		}
	}
	sort.Ints(losers)
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
		// The shared countdown is armed on the won path only when the victory
		// sweep is what fired; the defeat predicate always takes the lost path
		// [08 R-TRIG-01 §6].
		s.Latch.Arm()
		if victory {
			s.Latch.Pending = 1
		} else {
			s.Latch.Pending = 2
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
			winners2 := resultWinnersFor(w2, s.resultPendingDraw)
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
	// Advance countdown only when due (once per 30 ticks) [08 R-TRIG-01 §6].
	isDue := tick >= s.resultNextDue
	if !isDue {
		// Update pending view countdown without advancing.
		scores := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
		kind2 := s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner)
		winners2 := resultWinnersFor(s.resultPendingWinner, s.resultPendingDraw)
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
		winners2 := resultWinnersFor(s.resultPendingWinner, s.resultPendingDraw)
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
	winners2 := resultWinnersFor(s.resultPendingWinner, s.resultPendingDraw)
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
