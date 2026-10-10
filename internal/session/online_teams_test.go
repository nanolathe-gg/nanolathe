package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// onlineTeamFixture is an online result fixture with one human row per
// group, alliances and shared-victory bits set from the groups as battle
// entry and the configuration set them (group 5 is no team), and one live
// unit per row.
func onlineTeamFixture(t *testing.T, groups ...int) (*Session, *content.UnitDef, []pool.Handle) {
	t.Helper()
	w, def := eliminationFixtureWorld(t)
	var present [10]bool
	for i := range groups {
		present[i] = true
	}
	s := &Session{
		Units: w, Econ: &economy.Service{}, Mission: &mission.Mission{Type: mission.TypeSkirmish}, State: StateBattle,
		Latch: NewEndLatch(), onlineResults: newOnlineResultState(present),
	}
	s.SeedSessionRNG(17, 31)
	seats := make([]MatchSeat, len(groups))
	for i, g := range groups {
		seats[i].AllyGroup = uint8(g)
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: 1, EndGameCountdown: -1}
		s.Skirmish.Players[i].AllyGroup = g
	}
	s.Skirmish.NumPlayers = len(groups)
	for i := range groups {
		s.onlineResults.sharedVictory[i] = matchTeamOfTwo(seats, i)
		for j := range groups {
			s.Econ.Players[i].Allies[j] = skirmishPlayersAllied(s.Skirmish, i, j)
		}
	}
	handles := make([]pool.Handle, len(groups))
	for i := range groups {
		handles[i] = onlineResultCreate(t, s, def, uint8(i))
	}
	return s, def, handles
}

// onlineArmed reports which rows' results are armed and with what kind.
func onlineArmed(s *Session, n int) []string {
	out := make([]string, n)
	for i := range out {
		if row := s.onlineResults.seats[i]; row.armed {
			out[i] = row.result.Kind
		}
	}
	return out
}

func onlineEvaluateAll(s *Session, n int, tick uint32) {
	for i := 0; i < n; i++ {
		s.evaluateOnlineSeatResult(i, tick)
	}
}

func sameKinds(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// In a three-seat free-for-all no row shares victory, so a live opponent
// always denies it, and the last survivor wins [08 R-SKIR-01 §3] "Victory
// detection".
func TestOnlineVictoryFreeForAllThreeSeats(t *testing.T) {
	s, _, h := onlineTeamFixture(t, 5, 5, 5)
	onlineEvaluateAll(s, 3, 0)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "", "") {
		t.Fatalf("a battle with three live seats armed %q", got)
	}
	onlineResultKill(t, s, h[1])
	onlineEvaluateAll(s, 3, 30)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "defeat", "") {
		t.Fatalf("one elimination of three: %q, want only the defeat", got)
	}
	onlineResultKill(t, s, h[2])
	onlineEvaluateAll(s, 3, 60)
	if got := onlineArmed(s, 3); !sameKinds(got, "victory", "defeat", "defeat") {
		t.Fatalf("the last survivor: %q", got)
	}
	if r := s.onlineResults.seats[0].result; r.WinnerTeam != s.teamForOwner(0) || len(r.Losers) != 2 {
		t.Fatalf("free-for-all winner %d, losers %v", r.WinnerTeam, r.Losers)
	}
}

// Teams of two share victory: a live teammate does not deny it, a live
// opponent does, and both teammates win once the other team is eliminated,
// the one already defeated included in neither [08 R-SKIR-01 §3]
// [05 R-SHARE-01 §1]. A mutual wipe is defeat with no winner.
func TestOnlineVictoryTeamsShareVictory(t *testing.T) {
	s, _, h := onlineTeamFixture(t, 0, 0, 1, 1)
	onlineResultKill(t, s, h[2])
	onlineEvaluateAll(s, 4, 0)
	if got := onlineArmed(s, 4); !sameKinds(got, "", "", "defeat", "") {
		t.Fatalf("a team that has not yet won: %q", got)
	}
	onlineResultKill(t, s, h[3])
	onlineEvaluateAll(s, 4, 30)
	if got := onlineArmed(s, 4); !sameKinds(got, "victory", "victory", "defeat", "defeat") {
		t.Fatalf("an eliminated team: %q", got)
	}
	if a, b := s.onlineResults.seats[0].result.WinnerTeam, s.onlineResults.seats[1].result.WinnerTeam; a != b || a != s.teamForOwner(0) {
		t.Fatalf("teammates name winners %d and %d", a, b)
	}

	// Victory needs the mutual alliance as well as both shared-victory bits.
	s, _, h = onlineTeamFixture(t, 0, 0, 1)
	onlineResultKill(t, s, h[2])
	s.Econ.Players[1].Allies[0] = false
	onlineEvaluateAll(s, 3, 0)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "", "defeat") {
		t.Fatalf("a one-sided alliance shared victory: %q", got)
	}
	s.Econ.Players[1].Allies[0] = true
	s.onlineResults.sharedVictory[1] = false
	onlineEvaluateAll(s, 3, 30)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "", "defeat") {
		t.Fatalf("a cleared shared-victory bit shared victory: %q", got)
	}

	// Defeat precedes victory: the last two units of both teams falling
	// together is two defeats and no victory.
	s, _, h = onlineTeamFixture(t, 0, 0, 1, 1)
	for _, handle := range h {
		onlineResultKill(t, s, handle)
	}
	onlineEvaluateAll(s, 4, 0)
	if got := onlineArmed(s, 4); !sameKinds(got, "defeat", "defeat", "defeat", "defeat") {
		t.Fatalf("a mutual wipe: %q", got)
	}
	for i := 0; i < 4; i++ {
		if r := s.onlineResults.seats[i].result; len(r.Winners) != 0 {
			t.Fatalf("seat %d names winners %v after a mutual wipe", i, r.Winners)
		}
	}
}

// A seat that has created nothing yet denies victory even to a team, and
// counts as not eliminated in the alliance walk [08 R-SKIR-01 §3].
func TestOnlineVictoryWaitsForUncreatedSeat(t *testing.T) {
	s, def, h := onlineTeamFixture(t, 0, 0, 1)
	onlineResultKill(t, s, h[2])
	s.Econ.Players[3] = economy.Player{Exists: true, ControllerState: 1, EndGameCountdown: -1}
	s.onlineResults.seats[3] = onlineSeatResult{present: true, latch: NewEndLatch()}
	onlineEvaluateAll(s, 2, 0)
	if got := onlineArmed(s, 2); !sameKinds(got, "", "") {
		t.Fatalf("an uncreated seat allowed victory: %q", got)
	}
	onlineResultKill(t, s, onlineResultCreate(t, s, def, 3))
	onlineEvaluateAll(s, 2, 30)
	if got := onlineArmed(s, 2); !sameKinds(got, "victory", "victory") {
		t.Fatalf("after the late seat's elimination: %q", got)
	}
}

// Survival evaluates no victory online either: survivors whose attacker has
// no live unit between waves do not win, defeat still arms, and a final
// result carries the Survival line (DESIGN_SURVIVAL §8).
func TestOnlineSurvivalHasNoVictory(t *testing.T) {
	s, _, h := onlineTeamFixture(t, 2, 2, 2, 5)
	s.onlineResults.seats[3] = onlineSeatResult{} // the attacker row is no seat
	s.Survival = &survivalState{attacker: 3, team: []uint8{0, 1, 2}}
	onlineResultKill(t, s, h[3])
	onlineEvaluateAll(s, 3, 0)
	if got := onlineArmed(s, 3); !sameKinds(got, "", "", "") {
		t.Fatalf("survivors armed %q against an empty attacker", got)
	}
	s.Survival = nil
	onlineEvaluateAll(s, 3, 30)
	if got := onlineArmed(s, 3); !sameKinds(got, "victory", "victory", "victory") {
		t.Fatalf("the fixture's skirmish control: %q", got)
	}

	s, _, h = onlineTeamFixture(t, 2, 2, 2, 5)
	s.onlineResults.seats[3] = onlineSeatResult{}
	s.Survival = &survivalState{attacker: 3, team: []uint8{0, 1, 2}, survived: 4}
	onlineResultKill(t, s, h[1])
	for i := 0; i < 6; i++ {
		onlineEvaluateAll(s, 3, uint32(30*i))
	}
	r := s.ResultForSeat(1)
	if !r.Ended || r.Kind != "defeat" || r.Survival == nil || r.Survival.Waves != 4 || r.Survival.TicksAlive != r.Tick {
		t.Fatalf("defeated survivor's result %+v", r)
	}
	if other := s.ResultForSeat(0); other.Kind != "" || other.Survival != nil {
		t.Fatalf("a living survivor's result %+v", other)
	}
}

// Online entry needs at least two human seats, which a computer row cannot
// make up, and only a human as the local seat (DESIGN_MULTIPLAYER §16.6).
// Computers the room host adds join two or more humans
// (TestOnlineComputerSeatsComposeAlike).
func TestOnlineEntryNeedsTwoHumansAndAHumanLocalSeat(t *testing.T) {
	humans := restrictionMatchConfig(t, nil, true)
	inputs := rehearsalFixtureInputs(t, humans)
	if _, err := NewPlaytestSkirmish(inputs, humans, 2, nil); err == nil {
		t.Fatal("a local seat past the human rows was admitted")
	}
	s, err := NewPlaytestSkirmish(inputs, humans, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.closeAIControllers()
	if !s.onlineResults.seats[0].present || !s.onlineResults.seats[1].present || s.onlineResults.seats[2].present {
		t.Fatal("presence does not follow the human rows")
	}
	computer := restrictionMatchConfig(t, nil, false)
	if _, err := NewPlaytestSkirmish(rehearsalFixtureInputs(t, computer), computer, 0, nil); err == nil {
		t.Fatal("one human and a computer were admitted online")
	}
}
