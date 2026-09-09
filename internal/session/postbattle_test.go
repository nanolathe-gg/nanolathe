package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// stepUntil advances one presentation unit at a time until the controller
// reaches want, and returns the unit index the caller should use next.
//
// Driving by state rather than by a fixed tick count is deliberate. The fade
// of [08 R-CAMP-01 §6] state 3 draws a step only when `now > deadline` and
// then sets the deadline to `now + 1`, so ten fade steps span twenty
// presentation units, not ten. Hard-coding the arrival tick of a later state
// encodes that cadence as a constant and silently mis-times every assertion
// after it.
func stepUntil(t *testing.T, c *PostBattleController, now uint32, want PostBattleState) uint32 {
	t.Helper()
	for range 200 {
		if c.State() == want {
			return now
		}
		c.Step(now, false)
		now++
	}
	t.Fatalf("controller never reached state %d: stuck at %d, order %v", want, c.State(), c.StateOrder())
	return now
}

func advanceToEndMission(t *testing.T, c *PostBattleController, withGlamour bool) {
	t.Helper()
	now := stepUntil(t, c, 0, PostBattleOutcome)
	// The outcome action routes to the shell, opens the glamour fade, or
	// populates ENDMSN [08 R-CAMP-01 §6] state 5.
	c.Step(now, false)
	now++
	if !withGlamour {
		return
	}
	// State 6 applies exactly one fade step per unit until the palette equals
	// its target. Six units model a palette whose slowest byte differs by six:
	// the fade table's divisor is five, but completion is equality-driven, so
	// the controller cannot infer it from a step count [08 R-CAMP-01 §6].
	const glamourUnits = 6
	for range glamourUnits {
		c.Step(now, false)
		now++
	}
	if c.AdmitControl(PostBattleControlKey) {
		t.Fatal("glamour input admitted before explicit fade completion")
	}
	if !c.GlamourFadeDone(now) {
		t.Fatal("glamour fade completion refused")
	}
	// "the glamour deadline is now + rate (one second)", and the comparison is
	// strict: at the deadline itself nothing is due yet [08 R-CAMP-01 §6].
	due := now + 30
	c.Step(due, false)
	if c.Handle(PostBattleControlKey, due) {
		t.Fatal("glamour input admitted at the hold deadline")
	}
	c.Step(due+1, false)
	if !c.Handle(PostBattleControlKey, due+1) {
		t.Fatal("glamour input was not admitted after the hold deadline")
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
	advanceToEndMission(t, c, true)
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
	advanceToEndMission(t, c, false)
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
	advanceToEndMission(t, c, false)
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
		advanceToEndMission(t, c, false)
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
