package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

func advanceToEndMission(c *PostBattleController, withGlamour bool) {
	c.Step(0, false) // entry → fade setup
	c.Step(1, false) // fade setup → fade
	for now := uint32(2); now < 20; now++ {
		c.Step(now, false)
		if c.State() == PostBattleCDCheck {
			break
		}
	}
	c.Step(20, false) // CD check → outcome
	if withGlamour {
		c.Step(21, false) // outcome → glamour
		// Six requests model a palette whose slowest byte differs by six. The
		// table divisor is five, but completion is equality-driven.
		for now := uint32(22); now < 28; now++ {
			c.Step(now, false)
		}
		if c.AdmitControl(PostBattleControlKey) {
			panic("glamour input admitted before explicit fade completion")
		}
		c.GlamourFadeDone(28)
		c.Step(58, false) // strict hold boundary: due is 58, so no sound yet
		if c.Handle(PostBattleControlKey, 58) {
			panic("glamour input admitted at the hold deadline")
		}
		c.Step(59, false) // after hold; sound request is emitted once
		if !c.Handle(PostBattleControlKey, 59) {
			panic("glamour input was not admitted after the hold deadline")
		}
	} else {
		c.Step(21, false) // outcome → ENDMSN (or router for final win)
	}
}

func TestPostBattleCampaignWinSequenceAndProgressOnce(t *testing.T) {
	progress := &BankProgress{}
	result := frame.ResultView{Ended: true, Kind: "victory", Tick: 123}
	c := NewPostBattleController(result, PostBattleConfig{
		Kind: PostBattleCampaign, MissionIndex: 2, HasNext: true,
		CampaignCDOK: true, Progress: progress, ProgressCommitted: true, Difficulty: 1,
		Glamour: "bitmaps/glamour/core03.pcx", GlamourLoaded: true,
		GlamourSound: "glamour03",
	})
	progress.ApplyCampaignResult(2, true) // authoritative score-teardown writer
	beforeProgress := *progress
	advanceToEndMission(c, true)
	if c.State() != PostBattleEndMission {
		t.Fatalf("campaign win should reach ENDMSN, got state %d order %v", c.State(), c.StateOrder())
	}
	want := []PostBattleState{PostBattleEntry, PostBattleFadeSetup, PostBattleFade, PostBattleCDCheck, PostBattleOutcome, PostBattleGlamour, PostBattleEndMission}
	got := c.StateOrder()
	if len(got) != len(want) {
		t.Fatalf("state order length %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("state order[%d]=%d, want %d: %v", i, got[i], want[i], got)
		}
	}
	if !c.ProgressApplied() || progress.WL[2] != 'W' || progress.Thumbs[2] != 'W' || progress.BetweenMissions != beforeProgress.BetweenMissions {
		t.Fatalf("campaign progress not applied: %+v applied=%v", progress, c.ProgressApplied())
	}
	if *progress != beforeProgress {
		t.Fatal("post-battle controller must not apply campaign progress a second time")
	}
	if next, ok := c.NextMission(); !ok || next != 3 {
		t.Fatalf("next mission=(%d,%v), want (3,true)", next, ok)
	}
	effects := c.Effects()
	fades, glamour := 0, 0
	for _, e := range effects {
		if e.Kind == PostBattleEffectFadeOutStep {
			fades++
		}
		if e.Kind == PostBattleEffectGlamourFadeStep {
			glamour++
		}
	}
	if fades != 10 || glamour != 6 {
		t.Fatalf("fade counts=(%d,%d), want (10,6)", fades, glamour)
	}
}

func TestPostBattleSkirmishDoesNotWriteCampaignProgress(t *testing.T) {
	progress := &BankProgress{}
	c := NewPostBattleController(frame.ResultView{Ended: true, Kind: "victory"}, PostBattleConfig{
		Kind: PostBattleSkirmish, MissionIndex: 1, HasNext: true,
		CampaignCDOK: true, Progress: progress,
	})
	advanceToEndMission(c, false)
	if c.State() != PostBattleEndMission {
		t.Fatalf("skirmish should reach ENDMSN, got state %d", c.State())
	}
	if c.ProgressApplied() || progress.WL != [10]byte{} || progress.Thumbs != [25]byte{} || progress.BetweenMissions != 0 {
		t.Fatalf("skirmish mutated campaign progress: %+v", progress)
	}
	for _, e := range c.Effects() {
		if e.Kind == PostBattleEffectNetworkStats {
			t.Fatal("skirmish must not emit multiplayer network/statistics effect")
		}
	}
	if next, ok := c.NextMission(); ok || next != 0 {
		t.Fatalf("skirmish exposed campaign successor (%d,%v)", next, ok)
	}
	if c.AdmitControl(PostBattleControlStart) {
		t.Fatal("skirmish must not admit campaign Start control")
	}
	if !c.AdmitControl(PostBattleControlMainMenu) {
		t.Fatal("skirmish must admit MainMenu in ENDMSN")
	}
}

func TestPostBattleFinalWinSkipsUnavailableEndingMedia(t *testing.T) {
	c := NewPostBattleController(frame.ResultView{Ended: true, Kind: "victory"}, PostBattleConfig{
		Kind: PostBattleCampaign, MissionIndex: 24, CampaignCDOK: true,
		EndingMedia: false, Nomovie: false,
	})
	advanceToEndMission(c, false)
	if c.State() != PostBattleOutcome || !c.Routed() {
		t.Fatalf("final win without ending media should route to router, got %d order %v", c.State(), c.StateOrder())
	}
	seen := false
	for _, e := range c.Effects() {
		if e.Kind == PostBattleEffectEndingMediaSkipped {
			seen = true
		}
	}
	if !seen {
		t.Fatal("missing explicit ending-media skip effect")
	}
}

func TestPostBattleFinalWinEndingMovieSideMapping(t *testing.T) {
	for _, tc := range []struct {
		side int
		want string
	}{
		{side: 0, want: "3.zrb"}, // Arm ending state
		{side: 1, want: "4.zrb"}, // Core ending state
	} {
		c := NewPostBattleController(frame.ResultView{Ended: true, Kind: "victory"}, PostBattleConfig{
			Kind: PostBattleCampaign, MissionIndex: 24, CampaignCDOK: true,
			EndingMedia: true, LocalSide: tc.side,
		})
		advanceToEndMission(c, false)
		if !c.Routed() || c.State() != PostBattleOutcome {
			t.Fatalf("side %d final win state=%d routed=%v", tc.side, c.State(), c.Routed())
		}
		var got []string
		for _, e := range c.Effects() {
			if e.Kind == PostBattleEffectEndingMovie {
				got = append(got, e.Resource)
			}
		}
		if len(got) != 2 || got[0] != tc.want || got[1] != "5.zrb" {
			t.Fatalf("side %d ending media=%v, want [%q 5.zrb]", tc.side, got, tc.want)
		}
	}
}
