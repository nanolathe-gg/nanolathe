package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// killLeadSession is the smallest session the kill-lead shift runs on: a
// player table, a session-kind discriminant and a committed-event window. No
// unit world is needed — the shift reads only the player records, and the
// credit switch is driven directly through RecordDeathStatistics with the
// packet snapshot [06 §12.1][08 R-CAMP-01 §9].
func killLeadSession(t *testing.T, players int, missionType mission.Type) *Session {
	t.Helper()
	s := &Session{
		Econ:        &economy.Service{},
		Mission:     &mission.Mission{Type: missionType},
		publication: newPublicationState(nil),
	}
	for i := 0; i < players; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = combat.ControlByteHuman
		p.Name = fmt.Sprintf("P%d", i)
		p.SeedScorePanelRank(i) // registration [08 R-SKIR-01 §2]
	}
	return s
}

// killLeadRanks reads the ten rank bytes off the records, in slot order.
func killLeadRanks(s *Session) [10]uint8 {
	var out [10]uint8
	for i := 0; i < 10; i++ {
		out[i] = s.Econ.Players[i].Rank
	}
	return out
}

// creditKill drives one ordinary-cause death of victim's unit by attacker
// through the credit switch. Cause 1 with a complete victim owned by another
// slot is the full-credit path [06 §12.1].
func creditKill(s *Session, attacker, victim uint8) {
	s.RecordDeathStatistics(combat.DeathCreditInput{
		Cause:           combat.CauseOrdinary,
		VictimOwner:     victim,
		AttackerSide:    attacker,
		AttackerPresent: true,
	})
}

func killLeadAnnouncements(s *Session) []frame.Event {
	var out []frame.Event
	for _, e := range s.publication.events.StagingEvents() {
		if e.Kind == frame.KindAnnounce && e.StatusClass == messageClassKillLead {
			out = append(out, e)
		}
	}
	return out
}

// TestKillLeadShiftMovesRanksAndPostsTheLeadLineOnce walks the whole contract of
// [08 R-CAMP-01 §9] "Kill lead" / [06 §12.1 R-WPN-02 §9] on three registered
// slots: the shift's arithmetic, the push-down of the overtaken slots, the
// no-op credit, and the single line posted at the transition to rank zero.
func TestKillLeadShiftMovesRanksAndPostsTheLeadLineOnce(t *testing.T) {
	s := killLeadSession(t, 3, mission.TypeSkirmish)
	if got := killLeadRanks(s); got != [10]uint8{0, 1, 2, 0, 0, 0, 0, 0, 0, 0} {
		t.Fatalf("registration ranks = %v, want the slot index on the three registered slots [08 R-SKIR-01 §2]", got)
	}

	// Slot 0 takes two kills. Its rank is already zero, so the shift's entry
	// gate rejects it outright: nothing moves and nothing is posted, and the
	// line "is re-announced only on a later transition back to rank zero".
	creditKill(s, 0, 1)
	creditKill(s, 0, 1)
	if got := killLeadRanks(s); got != [10]uint8{0, 1, 2, 0, 0, 0, 0, 0, 0, 0} {
		t.Fatalf("a rank-zero slot's kills moved the ranks to %v [08 R-CAMP-01 §9]", got)
	}
	if got := len(killLeadAnnouncements(s)); got != 0 {
		t.Fatalf("a rank-zero slot's kills posted %d lines, want 0", got)
	}

	// Slot 2 takes one kill: k = 1. Slot 1 (0 kills, rank 1) and the rank-2
	// holder itself are the only present slots; slot 1 is strictly below and
	// ranks better, so best = 1, which is better than 2 but not zero. Slots
	// with rank in [1, 2) — slot 1 — are pushed down; slot 2 takes rank 1. The
	// unregistered slots hold rank 0 and are NOT candidates: the scan tests
	// "present", which is what stops a never-registered zero from handing out
	// the lead [06 §12.1 R-WPN-02 §9].
	creditKill(s, 2, 1)
	if got := killLeadRanks(s); got != [10]uint8{0, 2, 1, 0, 0, 0, 0, 0, 0, 0} {
		t.Fatalf("after slot 2's first kill ranks = %v, want slot 2 at 1 and slot 1 pushed to 2", got)
	}
	if got := len(killLeadAnnouncements(s)); got != 0 {
		t.Fatalf("a shift to a nonzero rank posted %d lines, want 0 [08 R-CAMP-01 §9]", got)
	}

	// Slot 2's second kill: k = 2, and slot 0 still holds 2 kills. Two is not
	// STRICTLY below two, so slot 0 is not a candidate and nothing changes —
	// a tie does not overtake.
	creditKill(s, 2, 1)
	if got := killLeadRanks(s); got != [10]uint8{0, 2, 1, 0, 0, 0, 0, 0, 0, 0} {
		t.Fatalf("a tie on kills moved the ranks to %v, want them unchanged [08 R-CAMP-01 §9]", got)
	}
	if got := len(killLeadAnnouncements(s)); got != 0 {
		t.Fatalf("a credit that changes no rank posted %d lines, want 0", got)
	}

	// Slot 2's third kill: k = 3 passes slot 0's 2. best = 0, so slot 0 is
	// pushed to 1, slot 2 takes 0, and the line is posted exactly once. The
	// seven unregistered slots sit at rank 0 and ride the push-down with slot
	// 0: the loop is over "every player whose rank lies in [best, myRank)", not
	// only the ones in the race, and nothing reads their byte
	// [06 §12.1 R-WPN-02 §9].
	creditKill(s, 2, 1)
	if got := killLeadRanks(s); got != [10]uint8{1, 2, 0, 1, 1, 1, 1, 1, 1, 1} {
		t.Fatalf("after the overtake ranks = %v, want slot 2 at 0 and slot 0 pushed to 1", got)
	}
	events := killLeadAnnouncements(s)
	if len(events) != 1 {
		t.Fatalf("the overtake posted %d lines, want exactly 1 [08 R-CAMP-01 §9]", len(events))
	}
	// "translated, then formatted" with the name and the UNIT kill counter,
	// class 2, speaker slot 10 [08 R-CAMP-01 §9][06 §12.1 R-WPN-02 §9].
	if want := fmt.Sprintf("%s has taken the lead with %d kills", "P2", 3); events[0].StatusText != want {
		t.Fatalf("lead line %q, want %q", events[0].StatusText, want)
	}
	if events[0].StatusClass != 2 {
		t.Fatalf("lead line class = %d, want 2 [08 R-CAMP-01 §9]", events[0].StatusClass)
	}
	if events[0].AnnounceSlot != 10 {
		t.Fatalf("lead line speaker slot = %d, want the no-speaker sentinel 10 [07 R-HUD-03 §14.3]", events[0].AnnounceSlot)
	}

	// A fourth kill leaves slot 2 at rank zero: the entry gate stops it, so the
	// line is not repeated.
	creditKill(s, 2, 1)
	if got := len(killLeadAnnouncements(s)); got != 1 {
		t.Fatalf("the lead line was posted %d times, want exactly 1 — it is re-announced only on a later transition back to zero", got)
	}
}

// A watcher slot is never chosen as the new rank: the candidate scan skips
// slots excluded by the runtime bit [08 R-CAMP-01 §9][06 §12.1 R-WPN-02 §9].
// Its rank byte still rides the push-down, because that loop is over "every
// player whose rank lies in [best, myRank)".
func TestKillLeadShiftSkipsWatcherSlotsWhenChoosingTheNewRank(t *testing.T) {
	s := killLeadSession(t, 3, mission.TypeSkirmish)
	// Slot 0 is a watcher with no kills. It holds the best rank, so a scan that
	// did not exclude it would hand slot 2 rank 0 on its first kill.
	s.Econ.Players[0].Watcher = true

	creditKill(s, 2, 1)
	// The only candidate is slot 1 (0 kills, rank 1), so best = 1 — not the
	// watcher's 0. Slot 1 is pushed to 2, slot 2 takes 1, and the watcher keeps
	// rank 0 because 0 is outside [1, 2).
	if got := killLeadRanks(s); got != [10]uint8{0, 2, 1, 0, 0, 0, 0, 0, 0, 0} {
		t.Fatalf("ranks = %v, want the watcher left at 0 and slot 2 at 1 — a watcher is not a candidate", got)
	}
	if got := len(killLeadAnnouncements(s)); got != 0 {
		t.Fatalf("a shift that stopped short of rank zero posted %d lines, want 0", got)
	}
}

// A campaign session runs neither half: the rank update is gated on session
// kinds 2 and 3, and a campaign mission (kind 1) posts nothing
// [08 R-CAMP-01 §9][06 §12.1 R-WPN-02 §9].
func TestKillLeadShiftDoesNotRunInACampaignSession(t *testing.T) {
	s := killLeadSession(t, 3, mission.TypeCampaign)
	for i := 0; i < 4; i++ {
		creditKill(s, 2, 1)
	}
	if got := killLeadRanks(s); got != [10]uint8{0, 1, 2, 0, 0, 0, 0, 0, 0, 0} {
		t.Fatalf("a campaign session moved the ranks to %v, want the registration values [08 R-CAMP-01 §9]", got)
	}
	if got := len(killLeadAnnouncements(s)); got != 0 {
		t.Fatalf("a campaign session posted %d lead lines, want 0", got)
	}
}

// In a rule-2 (Deathmatch) session the RANKING compares commander kills, while
// the line's second argument stays the ordinary unit kill counter
// [08 R-CAMP-01 §9][06 §12.1 R-WPN-02 §9][07 R-HUD-04 §1].
func TestKillLeadShiftComparesCommanderKillsUnderTheDeathmatchRule(t *testing.T) {
	s := killLeadSession(t, 2, mission.TypeSkirmish)
	s.Skirmish.CommanderDeath = 2

	// Slot 0 banks three ordinary kills and no commander kill. Under the
	// deathmatch rule those do not rank it, so slot 1's first commander kill
	// still passes it.
	for i := 0; i < 3; i++ {
		creditKill(s, 0, 1)
	}
	// Two ordinary kills for slot 1 first, so the line's kill count is
	// distinguishable from its commander count.
	creditKill(s, 1, 0)
	creditKill(s, 1, 0)
	if got := killLeadRanks(s)[1]; got != 1 {
		t.Fatalf("ordinary kills moved slot 1 to rank %d under the deathmatch rule, want 1 — the ranking compares commander kills", got)
	}
	if got := len(killLeadAnnouncements(s)); got != 0 {
		t.Fatalf("ordinary kills posted %d lines under the deathmatch rule, want 0", got)
	}

	// One commander kill: k = 1 against slot 0's 0, so slot 1 takes rank 0 and
	// the line is posted with its THREE ordinary kills, not its one commander
	// kill.
	s.RecordDeathStatistics(combat.DeathCreditInput{
		Cause:           combat.CauseOrdinary,
		VictimOwner:     0,
		AttackerSide:    1,
		AttackerPresent: true,
		VictimCommander: true,
	})
	// Slot 0 and the eight unregistered slots all sat at rank 0 and are pushed
	// to 1 together; slot 1 takes 0.
	if got := killLeadRanks(s); got != [10]uint8{1, 0, 1, 1, 1, 1, 1, 1, 1, 1} {
		t.Fatalf("after the commander kill ranks = %v, want slot 1 at 0", got)
	}
	events := killLeadAnnouncements(s)
	if len(events) != 1 {
		t.Fatalf("the commander overtake posted %d lines, want 1", len(events))
	}
	if want := fmt.Sprintf("%s has taken the lead with %d kills", "P1", 3); events[0].StatusText != want {
		t.Fatalf("lead line %q, want %q — the argument is the UNIT kill counter even under rule 2 [06 §12.1 R-WPN-02 §9]", events[0].StatusText, want)
	}
}

// The shift draws from neither RNG stream, unlike the elimination line it sits
// beside on the death path [I4][08 R-CAMP-01 §9].
func TestKillLeadShiftDrawsNoRandomNumbers(t *testing.T) {
	s := killLeadSession(t, 3, mission.TypeSkirmish)
	s.SeedSessionRNG(1, 1)
	simBefore, crtBefore := s.SimRNG().Draws(), s.CrtRNG().Draws()
	for i := 0; i < 4; i++ {
		creditKill(s, 2, 1)
	}
	if len(killLeadAnnouncements(s)) == 0 {
		t.Fatalf("fixture posted no lead line, so the draw census proves nothing")
	}
	if s.SimRNG().Draws() != simBefore || s.CrtRNG().Draws() != crtBefore {
		t.Fatalf("draw counts changed: sim %d->%d, crt %d->%d",
			simBefore, s.SimRNG().Draws(), crtBefore, s.CrtRNG().Draws())
	}
}
