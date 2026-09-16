package session

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
)

// Retail's campaign result is the local win/loss latch and per-player score
// rows [08 R-CAMP-01 §7–§8]. Team identifiers are host presentation metadata;
// campaign results must use the same domain as their score rows. Empty setup
// rows exercise the identity boundary left by a load [08 R-SAVE-02 §11].
func TestCampaignResultUsesScoreTeamIdentity(t *testing.T) {
	for _, local := range []int{0, 1} {
		for _, victory := range []bool{true, false} {
			for _, emptySetup := range []bool{false, true} {
				t.Run(fmt.Sprintf("local%d/victory%t/emptySetup%t", local, victory, emptySetup), func(t *testing.T) {
					s := newLoopTestSession(t, 2)
					s.Mission.Type = mission.TypeCampaign
					s.Mission.Units = []mission.UnitPlacement{{}}
					s.Latch = NewEndLatch()
					for owner := 0; owner < 2; owner++ {
						controller := uint8(2)
						if owner == local {
							controller = 1
						}
						s.Econ.Players[owner].ControllerState = controller
						s.Skirmish.Players[owner].Controller = SkirmishControllerComputer
						if owner == local {
							s.Skirmish.Players[owner].Controller = SkirmishControllerHuman
						}
					}
					if emptySetup {
						s.Skirmish.Players = [10]SkirmishPlayer{}
					}
					// The authored timer selects the result while both score-row
					// owners remain live. The other timer cannot finish first.
					winSeconds, loseSeconds := int32(1), int32(100)
					wantKind, wantMark, winnerOwner := "victory", byte('W'), local
					if !victory {
						winSeconds, loseSeconds = loseSeconds, winSeconds
						wantKind, wantMark, winnerOwner = "defeat", 'L', 1-local
					}
					s.Mission.Victory = []*triggers.Trigger{triggers.New(triggers.KindVictoryTimerRunsOut, "", triggers.SecondsToTicks(winSeconds))}
					s.Mission.Defeat = []*triggers.Trigger{triggers.New(triggers.KindDeathTimerRunsOut, "", triggers.SecondsToTicks(loseSeconds))}
					for tick := uint32(0); tick <= 180 && s.State == StateBattle; tick += 30 {
						s.pollMissionTriggers(tick)
					}
					r := s.GetResult()
					winner, loser := s.teamForOwner(winnerOwner), s.teamForOwner(1-winnerOwner)
					if !r.Ended || r.Kind != wantKind || r.WinnerTeam != winner || !slices.Equal(r.Winners, []int{winner}) || !slices.Equal(r.Losers, []int{loser}) {
						t.Fatalf("result=%+v, want %s winner team %d loser team %d", r, wantKind, winner, loser)
					}
					if len(r.Scores) != 2 {
						t.Fatalf("score rows=%d, want both participants", len(r.Scores))
					}
					for _, row := range r.Scores {
						want := "lose"
						if row.Player == winnerOwner {
							want = "win"
						}
						if row.Team != s.teamForOwner(row.Player) || row.Kind != want {
							t.Errorf("score row=%+v, want %s in its player's team", row, want)
						}
					}
					if got := s.Progress.Thumbs[s.CampaignSlot]; got != wantMark {
						t.Errorf("campaign mark=%q, want %q from the local latch", got, wantMark)
					}
				})
			}
		}
	}
}
