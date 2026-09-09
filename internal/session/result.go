package session

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
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

// teamForOwner maps an owner slot to its team identifier: the lowest slot in
// the owner's alliance, offset by 100 so the identifier is never confused with
// a slot index. A slot allied with nobody is its own team, which is what makes
// a default skirmish free-for-all.
//
// The alliance comes off the player record's first alliance row, not the setup
// row's ally-group ordinal. Battle entry converts the groups into that row once
// [08 R-SKIR-01 §2], the row is the last item of each `Player%i` account and
// survives a load, and the ally-group ordinal does not — a restored battle's
// setup rows read back as group 0 for every slot, which used to fold two allies
// into one arbitrary team and, on a group-5 default, split an allied pair into
// two. See player_record.go.
//
// This grouping is Nanolathe presentation only [I6], and the marker that used
// to stand here — asking how retail reduces a non-local aggregate to the one
// team name a result row prints — asked about something that does not exist.
// [08 R-CAMP-01 §7] establishes the post-battle board exactly: ten rows of 58
// bytes, one per player SLOT in slot order, each a 30-byte name copy and seven
// 32-bit integers (kills, losses, energy/metal produced, energy/metal wasted,
// score). There is no team column and no aggregation step to match. The end
// predicates read the local player's first alliance row directly (victorySweep,
// [08 R-TRIG-01 §6]); nothing downstream of them names a team.
func (s *Session) teamForOwner(owner int) int {
	if owner < 0 || owner >= 10 {
		return owner
	}
	for j := 0; j < owner; j++ {
		if s.ownersAllied(owner, j) {
			return 100 + j
		}
	}
	return 100 + owner
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
		energyProduced := int(numeric.TruncateFloat64ToLow32(p.TotalProduced[1]))
		metalProduced := int(numeric.TruncateFloat64ToLow32(p.TotalProduced[0]))
		energyConsumed := int(numeric.TruncateFloat64ToLow32(p.TotalConsumed[1]))
		metalConsumed := int(numeric.TruncateFloat64ToLow32(p.TotalConsumed[0]))
		energyWasted := int(numeric.TruncateFloat64ToLow32(p.Waste[1]))
		metalWasted := int(numeric.TruncateFloat64ToLow32(p.Waste[0]))
		score := s.resultScore(kills)
		// The name and the colour are the player record's. The board copies a
		// 30-byte name out of the record and the row's colour gadget indexes
		// the record's logo byte [08 R-CAMP-01 §7]; both are written by the
		// row-to-player conversion and persisted by the `Player%i` account,
		// while the setup row this used to fall back to is gone after a load.
		logo, _ := s.colourForOwner(i)
		out = append(out, frame.ResultScore{
			Player: i, Team: team, Name: p.Name, Logo: logo,
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

// resultScoreRowEligible is the ENDMSN row gate, and every term of it is a
// player-record read [08 R-CAMP-01 §7]: "a slot gets a row when its record
// exists, its controller is 1, 2 or 3 ..., its side is not the neutral 10 and
// its lobby record's watcher bit (0x40) is clear — or when the slot's auxiliary
// word is non-zero ... and, in either case, its rejection-reason byte is 0".
//
// The setup-row override this gate used to apply first is gone. The setup
// record is a pre-battle mirror a load does not rebuild [08 R-SKIR-01 §2]
// "Save persistence", so after a load it answered controller 0 / side 0 for
// every slot — and, because the override was preferred whenever NumPlayers was
// non-zero, a restored observer slot was classified as an ordinary human and
// took a row. See player_record.go.
func (s *Session) resultScoreRowEligible(i int, p economy.Player) bool {
	if i < 0 || i >= 10 || !p.Exists || p.RejectionReason != 0 {
		return false
	}
	if p.ResultAuxiliary != 0 {
		return true
	}
	if p.ControllerState != 1 && p.ControllerState != 2 && p.ControllerState != 3 {
		return false
	}
	if p.Side == neutralSideIndex {
		return false
	}
	// The gate's last term is the lobby record's watcher bit. This build
	// carries the multiplayer bit (`Watcher`, no writer outside a network
	// lobby) and the observer byte battle entry writes (`IsObserver`,
	// [05 "Authoritative settlement order"]) as separate fields, and either one
	// set means the slot is watching rather than playing.
	return !p.IsObserver && !p.Watcher
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
	return Score(kills, killMul, tick, timeMul)
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
// The one test before the walk is the rule word: [08 R-SKIR-01 §3] "Victory
// detection" opens with "the elimination sweep run from the same due returns
// false immediately when the rule word is 2" — deathmatch never ends by
// elimination, because the local player's own elimination is what arms the
// respawn.
//
// Inside the walk there is no shared-victory bit and no controller or
// elimination test: those belong to the kind-3 sweep that [08 R-SKIR-01 §3]
// "Victory detection" describes, and that section's closing "allies included"
// sentence is explicitly wrong for kind 2 — allied players are excluded by the
// first alliance row, which battle entry fills from the setup screen's team
// groups [08 R-SKIR-01 §2].
func (s *Session) victorySweep() bool {
	if s == nil || s.Units == nil {
		return false
	}
	if CommanderDeathMode(s.Skirmish.CommanderDeath) == CommanderDeathDeathmatch {
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

// EvaluateResult is the skirmish end-condition block for session kinds 2 and
// 3. It is NOT a per-sub-tick evaluator: [08 R-TRIG-01 §6] "The due tick is the
// settlement deadline" places this block inside the LOCAL slot's settlement
// deadline block, after that slot's UpdateTime has advanced by 30 and before
// its settlement gate chain, so it runs exactly once per settlement due and a
// load resumes it on the saved UpdateTime phase. Session.endConditionBlock is
// the seam that forwards that due here; nothing else may call it on an
// arbitrary tick.
//
// **Correction.** The previous implementation was called every sub-tick and
// kept two private deadlines of its own — one for the result poll and one for
// the deathmatch respawn — arming them from whatever tick the predicate first
// became true. [05 R-ECO-01 §1] and [08 R-TRIG-01 §6] establish that retail
// carries one deadline word per slot and that the end-condition block rides it;
// a private deadline evaluates the predicates between dues and diverges after a
// load, because a retail save carries only the settlement deadline.
//
// It runs retail's two predicates in retail's order [08 R-TRIG-01 §6]: the
// defeat predicate — the local player's live unit count is zero
// [08 R-SKIR-01 §3] — is evaluated first and takes the lost path; only when it
// is false does the victory sweep run and take the won path. Defeat wins a tie,
// so a wipe that leaves nobody standing is a local defeat and not a draw.
//
// A true predicate steps the one shared countdown (Latch.Countdown); a false
// due neither resets nor advances it. On the due that takes the countdown below
// zero the rule word selects: 2 respawns the local commander, anything else
// writes the end latch [08 R-SKIR-01 §3] "Defeat detection". Returns true only
// on the due where the latch becomes visible.
func (s *Session) EvaluateResult(tick uint32) bool {
	if s == nil || s.Units == nil {
		return false
	}
	if s.result.Ended {
		return false
	}
	// Finalization normally runs the owner sweep at the death boundary. Keeping
	// the call here also covers direct composition seams that reach this block
	// after filing a death through the unit world [08 R-SKIR-01 §3].
	s.processPendingCommanderDeaths(tick)
	rule := CommanderDeathMode(s.Skirmish.CommanderDeath)

	// The two predicates, in the order [08 R-TRIG-01 §6] establishes for
	// session kinds 2/3: the defeat predicate first and, if true, the lost
	// path; otherwise the victory sweep and, if true, the won path. At most one
	// predicate steps the shared countdown per due. victorySweep already
	// answers false under rule 2, where deathmatch never ends by elimination
	// [08 R-SKIR-01 §3] "Victory detection".
	localDefeated := s.localDefeated()
	victory := false
	if !localDefeated {
		victory = s.victorySweep()
	}
	if !localDefeated && !victory {
		// "A false due neither resets nor advances the countdown"
		// [08 R-TRIG-01 §6] "Countdown and latch".
		return false
	}

	// The pending metadata is presentation identity only: retail's kind-2 end
	// writes the shared countdown and the latch bits and names no winner
	// [08 R-TRIG-01 §6]. It is rebuilt on every true due so the view — and the
	// terminal due's own commit — describe the predicate that fired on that
	// due, not the one that armed the countdown.
	if !s.resultPending {
		s.resultPending = true
		s.resultArmedTick = tick
	}
	s.resultPendingDraw = false
	s.resultPendingReason = ReasonAllUnits
	if rule != CommanderDeathContinues {
		s.resultPendingReason = ReasonCommanderDeath
	}
	s.resultPendingWinner, s.resultPendingLosers = s.resultTeams(victory)
	s.deathmatchActive = rule == CommanderDeathDeathmatch

	terminal := s.advanceSharedCountdown(victory)
	s.publishEndCountdown()
	if !terminal {
		s.result = s.pendingResultView()
		return false
	}
	// The rule word alone selects the terminal due's arm [08 R-SKIR-01 §3]
	// "Defeat detection"; no exhaustion or attempt state qualifies it.
	if rule == CommanderDeathDeathmatch {
		s.deathmatchActive = false
		s.settleDeathmatchRespawn()
		return false
	}
	s.deathmatchActive = false
	// The terminal due's path selects the latch bits: ending plus, on the won
	// path, the two win bits, or, on the lost path, the lose bit with the first
	// win bit cleared [08 R-TRIG-01 §6] "Countdown and latch".
	s.Latch.Bits |= LatchBitEnding
	if victory {
		s.Latch.Win()
	} else {
		s.Latch.Lose()
	}
	s.Latch.Pending = 0
	s.publishEndCountdown()
	s.result = s.endedResultView(tick)
	if s.State == StateBattle {
		_ = s.TransitionTo(StatePostBattle)
	}
	return true
}

// advanceSharedCountdown is the one signed 16-bit countdown of
// [08 R-TRIG-01 §6] "Countdown and latch", shared by every path: a true
// predicate finds it negative and sets it to 4; each later true due decrements
// it; the due whose decrement takes it below zero is the terminal one — the
// sixth consecutive true due, 150 ticks after the first. Only true dues reach
// here.
//
// It returns true on the terminal due and writes no latch bits, because the
// rule word decides there between the deathmatch respawn and the end latch
// [08 R-SKIR-01 §3] "Defeat detection".
func (s *Session) advanceSharedCountdown(won bool) bool {
	if won {
		s.Latch.Pending = 1
	} else {
		s.Latch.Pending = 2
	}
	if s.Latch.Countdown < 0 {
		s.Latch.Arm()
		return false
	}
	s.Latch.Countdown--
	return s.Latch.Countdown < 0
}

// settleDeathmatchRespawn is the rule-2 arm of the terminal due: the countdown
// has crossed below zero and the rule word selects the respawn rather than the
// end latch [08 R-SKIR-01 §3] "Defeat detection". A successful respawn leaves
// the countdown at its unarmed -1, which is where the decrement already put it,
// and drops the pending result so the next elimination arms a fresh countdown.
func (s *Session) settleDeathmatchRespawn() {
	s.Latch.Pending = 0
	s.deathmatchAttempts = 0
	if s.respawnLocalCommander() {
		s.clearPendingResult()
		return
	}
	// There is no post-exhaustion transition to find. The terminal due's arm is
	// selected by the RULE WORD alone — "the rule word selects: 2 → respawn
	// (...); any other value → ... in skirmish (kind 2) it writes the end latch
	// directly" [08 R-SKIR-01 §3] "Defeat detection" — so a rule-2 session can
	// never reach the latch write, whether the bounded search created a
	// commander or not. Retail's create call sits inside the accepted-candidate
	// branch; when 9999 candidates are all rejected the block simply ends with
	// the countdown still below zero, and the next true due re-arms it to 4 and
	// tries again 150 ticks later [08 R-TRIG-01 §6] "Countdown and latch".
	//
	// **Correction.** The marker that stood here said the transition was
	// untraced, and the code it defended latched a *defeat* on the second
	// terminal due by gating the rule-2 arm on this flag. That is invented
	// state: it ends a deathmatch that retail keeps running. The flag is now
	// diagnostic only — DeathmatchStatus reports that the last search exhausted
	// — and decides nothing.
	s.deathmatchExhausted = true
	s.clearPendingResult()
}

func (s *Session) clearPendingResult() {
	s.resultPending = false
	s.resultPendingWinner = 0
	s.resultPendingLosers = nil
	s.resultPendingReason = ""
	s.resultPendingDraw = false
	s.resultArmedTick = 0
	s.result = Result{}
}

// publishEndCountdown mirrors the shared countdown and the ending bit onto every
// player record. Retail keeps both as globals that each slot's settlement gate
// chain reads ([05 R-ECO-01 §1] steps 6 and 7); writing them from inside the
// local slot's block is what makes a lower-numbered slot settle this tick on the
// pre-write value and a higher-numbered slot on the new one.
func (s *Session) publishEndCountdown() {
	if s == nil || s.Econ == nil {
		return
	}
	for i := 0; i < 10; i++ {
		s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
		s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
	}
}

// resultTeams derives the winning team and the losing team list the post-battle
// screen prints. Retail names neither: its kind-2 end writes only the shared
// countdown and the latch bits [08 R-TRIG-01 §6]. Both lists come from the same
// row eligibility the score rows use.
func (s *Session) resultTeams(victory bool) (int, []int) {
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
	// Registered slots in slot order [I1]. The participating set is the player
	// record's, so this walk is the same before and after a load; the count the
	// setup record carries is not restored [08 R-SKIR-01 §2] "Save
	// persistence".
	for i := 0; i < 10; i++ {
		if s.resultOwnerEligible(i) {
			addTeam(s.teamForOwner(i))
		}
	}
	if allTeamCount == 0 {
		// An unwired composition with no player table at all: name the teams
		// the live units imply, and failing that every slot.
		for _, u := range s.Units.IterSliced() {
			if u == nil {
				continue
			}
			addTeam(s.teamForOwner(int(u.Owner)))
		}
	}
	if allTeamCount == 0 {
		for i := 0; i < 10; i++ {
			addTeam(100 + i)
		}
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
	var losers []int
	for i := 0; i < allTeamCount; i++ {
		if t := allTeams[i]; t != winner {
			losers = append(losers, t)
		}
	}
	sort.Ints(losers)
	return winner, losers
}

// pendingResultView is the armed-but-not-latched view: the countdown is
// visible, Ended is not. Tick stays zero because no terminal tick exists yet.
func (s *Session) pendingResultView() Result {
	scores := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
	return Result{
		Ended:        false,
		Draw:         s.resultPendingDraw,
		Kind:         s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner),
		WinnerTeam:   s.resultPendingWinner,
		Winners:      resultWinnersFor(s.resultPendingWinner, s.resultPendingDraw),
		Losers:       append([]int(nil), s.resultPendingLosers...),
		Reason:       s.resultPendingReason,
		Tick:         0,
		ArmedTick:    s.resultArmedTick,
		Countdown:    s.Latch.Countdown,
		Scores:       scores,
		ColumnMaxima: resultColumnMaxima(scores),
	}
}

// endedResultView is the terminal view committed on the due that writes the
// latch.
func (s *Session) endedResultView(tick uint32) Result {
	scores := s.collectScores(s.resultPendingWinner, s.resultPendingDraw)
	return Result{
		Ended:        true,
		Draw:         s.resultPendingDraw,
		Kind:         s.resultKindFor(s.resultPendingDraw, s.resultPendingWinner),
		WinnerTeam:   s.resultPendingWinner,
		Winners:      resultWinnersFor(s.resultPendingWinner, s.resultPendingDraw),
		Losers:       append([]int(nil), s.resultPendingLosers...),
		Reason:       s.resultPendingReason,
		Tick:         tick,
		ArmedTick:    s.resultArmedTick,
		Countdown:    s.Latch.Countdown,
		Scores:       scores,
		ColumnMaxima: resultColumnMaxima(scores),
	}
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

// resultOwnerEligible is the participating-player test the result sweep and the
// team walk share: a registered, non-observing slot whose controller is human
// or computer [08 R-SKIR-01 §3].
//
// It reads the player record. The setup-row branch that used to run first —
// preferred whenever NumPlayers was non-zero — is gone: a load restores only
// the five rule words and the map name into the setup record
// [08 R-SKIR-01 §2] "Save persistence", so every restored row read back as
// controller 0, which is a human, and a restored observer slot was counted as a
// participant. See player_record.go.
func (s *Session) resultOwnerEligible(owner int) bool {
	p := s.playerRecord(owner)
	if p == nil {
		// No player table at all: an unwired composition, where the setup row
		// is the only thing left to read (sideForOwner ends the same way).
		return owner >= 0 && owner < len(s.Skirmish.Players) && !s.Skirmish.Players[owner].IsObserver()
	}
	return p.Exists && !p.IsObserver && (p.ControllerState == 1 || p.ControllerState == 2)
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
	s.clearPendingResult()
	s.pendingCommanderDeaths = [10]bool{}
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
