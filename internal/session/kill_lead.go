package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// The kill-lead rank shift and its status line [08 R-CAMP-01 §9 "Kill lead"]
// [06 §12.1 R-WPN-02 §9].
//
// The central death handler runs the leader announcement immediately after the
// credit switch, in the same slot visit, before the cause-5 bounty and the
// death explosion [06 §12.1]. It maintains one per-player leaderboard rank
// byte — the same byte the Space-held score panel scans in rank order
// [07 R-HUD-04 §1] — and posts a line the one time a slot becomes sole leader.
//
// Nothing here draws from either RNG stream, unlike the elimination line next
// door, and nothing here reads the clock beyond stamping the committed event
// with the sub-tick it happened in [I4][I6].

// messageClassKillLead is the ring class the kill-lead line is posted under
// [08 R-CAMP-01 §9]. It is class 2, not the elimination line's class 4: the
// two announcements share the one 30-slot message ring but not its routing
// class, and only the low four bits of the stored byte are the class
// [07 R-HUD-03 §14.3].
const messageClassKillLead uint8 = 2

// killLeadSpeakerSlot is the speaker byte the line carries. Ten is the ring's
// no-speaker sentinel — no real slot reaches it — so the line draws no owner
// logo and plays no `MessageArrived` cue, which is exactly what
// [08 R-CAMP-01 §9] means by "attributed to slot 10" [07 R-HUD-03 §14.3].
const killLeadSpeakerSlot uint8 = 10

// killLeadNeutralSide is the neutral side value the entry gate rejects
// [08 R-CAMP-01 §9][07 R-HUD-04 §1].
const killLeadNeutralSide uint8 = 10

// killLeadDeathmatchRule is the commander-death rule word that switches the
// COMPARED counter from ordinary kills to commander kills [08 R-CAMP-01 §9]
// [07 R-HUD-04 §1]; it is the same word and the same value the score panel
// selects its printed pair with.
const killLeadDeathmatchRule = 2

// killLeadLineFormat is the message retail looks up in the translation table
// and then formats: "translated, then formatted" [08 R-CAMP-01 §9]. Its two
// arguments are the player's name and the **unit** kill counter — the ordinary
// one, even in the rule-2 session where the ranking compared commander kills
// [06 §12.1 R-WPN-02 §9].
//
// The lookup is the identity fallback. This session carries no translation
// table — the only table this build loads is bound at the presentation edge,
// for the options button's label — and `content.TranslationTable.Translate`
// returns its input unchanged when no table is loaded [02 "Translation
// table"]. The source string is therefore the formatted line's format string,
// the same convention the elimination tails next door follow.
const killLeadLineFormat = "%s has taken the lead with %d kills"

// runsKillLeadShift is the session-kind gate: the rank update runs in a
// skirmish or a multiplayer session (kinds 2 and 3) and in no other
// [08 R-CAMP-01 §9][06 §12.1 R-WPN-02 §9]. A campaign mission posts nothing
// and shifts nothing.
//
// It reads the loaded mission's type for the same reason
// postsEliminationAnnouncement does — the session kind is not retained on
// Session — and the two concepts coincide here because this engine builds
// exactly two kinds of battle and never a kind-3 one; a restored non-campaign
// battle is re-staged as a skirmish mission
// [08 R-SESS-01 §7 "Consequence for single-player"].
func (s *Session) runsKillLeadShift() bool {
	return s != nil && s.Mission != nil && s.Mission.Type == mission.TypeSkirmish
}

// killLeadUsesCommanderCounter reports whether the ranking compares commander
// kills instead of ordinary kills. The selector is the session's
// commander-death rule word, and the deathmatch value is 2 — the same word,
// read raw, that the score panel's printed pair is chosen by
// [08 R-CAMP-01 §9][07 R-HUD-04 §1][07 R-FE-01 §7].
func (s *Session) killLeadUsesCommanderCounter() bool {
	return s != nil && s.Skirmish.CommanderDeath == killLeadDeathmatchRule
}

// killLeadScore is the counter the comparison reads for one slot: the
// commander-kill counter in a rule-2 session, the ordinary kill counter
// otherwise. The counters are the signed 16-bit per-player words the credit
// switch writes at the death site [06 §12.1][08 R-CAMP-01 §9].
func killLeadScore(kills, commanderKills int16, commander bool) int {
	if commander {
		return int(commanderKills)
	}
	return int(kills)
}

// applyKillLeadShift is the leader-announcement step of the central death
// handler, run after the credit switch has moved the crediting slot's counter
// [06 §12.1]. slot is the crediting slot.
//
// The whole contract of [08 R-CAMP-01 §9] "Kill lead", with the arithmetic
// [06 §12.1 R-WPN-02 §9] states in the same terms:
//
//   - entry gates: the session is kind 2 or 3; the crediting slot's record is
//     present; its controller byte is 1, 2 or 3; its side is not the neutral
//     10; and its rank byte is not already zero;
//   - `k` is the crediting slot's counter under the rule-2 selection above;
//   - `best` is the minimum rank over the OTHER present, non-excluded slots
//     whose counter is STRICTLY below `k`;
//   - the shift runs only when `best` is strictly better (lower) than the
//     crediting slot's current rank: every slot whose rank lies in
//     `[best, old)` is pushed down by one, and the crediting slot takes
//     `best`;
//   - only when `best` is exactly zero — a slot newly becoming sole leader —
//     is the line posted, class 2, speaker slot 10.
//
// Doc 08 phrases the candidate scan as "the lowest rank among non-watcher
// slots whose counter is strictly below k and whose rank is below the
// crediting slot's"; doc 06 takes the minimum over all candidates and then
// tests it against the current rank. The two agree exactly — a minimum over a
// set is below a bound iff the subset below that bound is non-empty, and the
// two minima then coincide — so the loop below carries doc 06's shape and doc
// 08's early rank filter is redundant rather than a second condition.
//
// The "present, not excluded by a runtime bit" filter is doc 06's wording for
// doc 08's "non-watcher"; the exclusion is the same predicate the published
// row's watcher term uses, so the panel and the ranking agree about which
// slots are in the race [07 R-HUD-04 §1]. The filter matters: without the
// present test an unregistered slot, whose rank byte is the zero its record
// was allocated with, would be a standing rank-zero candidate and would hand
// the lead to whoever killed first.
//
// The push-down loop visits every one of the ten slots, not only the
// qualifying ones — "every player whose rank lies in `[best, myRank)` is
// pushed down by one" — which is why unregistered slots ride along; they are
// invisible to the panel either way. Slots are visited 0..9 ascending and no
// RNG is drawn [I1][I4].
func (s *Session) applyKillLeadShift(slot int) {
	if s == nil || s.Econ == nil || slot < 0 || slot >= len(s.Econ.Players) {
		return
	}
	// The Survival attacker is not a player and never takes the lead
	// (DESIGN_SURVIVAL §8).
	if !s.runsKillLeadShift() || s.isSurvivalAttacker(slot) {
		return
	}
	me := &s.Econ.Players[slot]
	if !me.Exists {
		return
	}
	if me.ControllerState != 1 && me.ControllerState != 2 && me.ControllerState != 3 {
		return
	}
	if me.Side == killLeadNeutralSide {
		return
	}
	old := me.Rank
	if old == 0 {
		return // already sole leader; the line is re-announced only on a later transition back to zero
	}
	commander := s.killLeadUsesCommanderCounter()
	k := killLeadScore(me.Kills, me.CommanderKills, commander)

	best := old
	found := false
	for j := 0; j < len(s.Econ.Players); j++ { // slots 0..9 ascending [I1]
		if j == slot {
			continue // "the OTHER player records" [06 §12.1 R-WPN-02 §9]
		}
		q := &s.Econ.Players[j]
		if !q.Exists {
			continue
		}
		// The runtime exclusion is the watcher bit. IsObserver is this build's
		// own observer controller kind and is ORed in for the same reason the
		// published row ORs it: such a slot must be excluded the way a retail
		// watcher would be [07 R-HUD-04 §1].
		if q.Watcher || q.IsObserver {
			continue
		}
		if killLeadScore(q.Kills, q.CommanderKills, commander) >= k {
			continue // strictly below k, so a tie does not yield the rank
		}
		if !found || q.Rank < best {
			best, found = q.Rank, true
		}
	}
	if !found || best >= old {
		return // nothing moves and nothing is posted
	}
	for j := range s.Econ.Players {
		if r := s.Econ.Players[j].Rank; r >= best && r < old {
			s.Econ.Players[j].Rank = r + 1
		}
	}
	me.Rank = best
	if best != 0 {
		return
	}
	s.announceKillLead(slot)
}

// announceKillLead posts the lead line. It runs only from the `best == 0` arm
// above, which is the one transition [08 R-CAMP-01 §9] posts on.
//
// The line crosses the publication boundary as a committed event; the
// presentation edge owns the ring and the drawing [03 §2.4][I6]. Its tick is
// the sub-tick the death is being finalized in — the clock's global tick,
// which the sub-tick incremented before phase 1 and which is the same value
// phase 2 hands the finalizer [01 §4.4].
func (s *Session) announceKillLead(slot int) {
	if s == nil || s.publication == nil || s.publication.events == nil {
		return
	}
	name := ""
	kills := 0
	if p := s.playerRecord(slot); p != nil {
		name = p.Name
		// The unit kill counter, even in a rule-2 session where the ranking
		// compared commander kills [06 §12.1 R-WPN-02 §9].
		kills = int(p.Kills)
	}
	var tick uint32
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	s.publication.events.EmitAnnounce(frame.Event{
		Tick:         tick,
		StatusText:   fmt.Sprintf(killLeadLineFormat, name, kills),
		StatusClass:  messageClassKillLead,
		AnnounceSlot: killLeadSpeakerSlot,
	})
}
