package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
)

// TestPostBattleAdapterFreezesRoutesAndDrainsOnce covers the client-side
// adapter without starting Ebitengine. A successor is selected by the typed
// controller, while the already-committed campaign mark remains unchanged and
// the effect cursor does not replay an earlier request [08 R-CAMP-01 §6–8].
func TestPostBattleAdapterFreezesRoutesAndDrainsOnce(t *testing.T) {
	progress := session.BankProgress{}
	progress.ApplyCampaignResult(1, true)
	before := progress
	controller := session.NewPostBattleController(frame.ResultView{Ended: true, Kind: "victory"}, session.PostBattleConfig{
		Kind:              session.PostBattleCampaign,
		MissionIndex:      1,
		HasNext:           true,
		CampaignCDOK:      true,
		Progress:          &progress,
		ProgressCommitted: true,
	})
	b := &battleSession{postBattle: controller}
	for now := 0; now < 30 && controller.State() != session.PostBattleEndMission; now++ {
		b.postBattleClock = float64(now)
		b.stepPostBattle(0, nil, nil)
	}
	if controller.State() != session.PostBattleEndMission {
		t.Fatalf("controller state=%d, want ENDMSN", controller.State())
	}
	if progress != before {
		t.Fatalf("adapter changed committed progress: before=%+v after=%+v", before, progress)
	}
	consumed := b.postBattleEffectPos
	b.consumePostBattleEffects(30, nil)
	if b.postBattleEffectPos != consumed {
		t.Fatalf("effect cursor replayed: before=%d after=%d", consumed, b.postBattleEffectPos)
	}
	if !controller.Handle(session.PostBattleControlStart, 30) {
		t.Fatal("typed Start was not admitted")
	}
	if next, ok := controller.SelectedMission(); !ok || next != 2 {
		t.Fatalf("selected mission=(%d,%v), want (2,true)", next, ok)
	}
}

func TestPostBattleMissionRoutingUsesAuthoredIndex(t *testing.T) {
	options := []mission.Campaign{{Missions: []mission.Stub{
		{Index: 3, Name: "Third"},
		{Index: 7, Name: "Seventh"},
	}}}
	if got, ok := campaignMissionUIIndex(options, 0, 7); !ok || got != 1 {
		t.Fatalf("authored mission 7 mapped to (%d,%v), want (1,true)", got, ok)
	}
	if _, ok := campaignMissionUIIndex(options, 0, 8); ok {
		t.Fatal("unknown authored mission index was accepted")
	}
}

func TestPostBattlePaletteFadeArithmetic(t *testing.T) {
	var glamour formats.PCX
	glamour.Palette[0].R = 100
	glamour.Palette[0].G = 4
	glamour.Palette[0].A = 255
	cur := postBattlePaletteBytes(&glamour)
	if cur[0] != 100 || cur[1] != 4 || cur[3] != 0 {
		t.Fatalf("decoded palette bytes=%v, want RGB+pad", cur[:4])
	}
	if got := postBattleFadeStep(100, 0); got != -20 {
		t.Fatalf("large negative fade step=%d, want -20", got)
	}
	if got := postBattleFadeStep(0, 4); got != 1 {
		t.Fatalf("small positive fade step=%d, want 1", got)
	}
	if got := postBattleFadeStep(4, 0); got != -1 {
		t.Fatalf("small negative fade step=%d, want -1", got)
	}
}

func TestSecondNewCampaignResetsProgressThumbs(t *testing.T) {
	g := &gameShell{}
	g.openMissionMenu(false)
	g.campaignProgress.Thumbs[3] = 'W'
	g.campaignProgress.WL[3] = 'W'
	g.openMissionMenu(false)
	if !g.campaignProgressSet {
		t.Fatal("new campaign did not arm progress adoption")
	}
	for i, mark := range g.campaignProgress.Thumbs {
		if mark != 'U' {
			t.Fatalf("second campaign thumb[%d]=%q, want U", i, mark)
		}
	}
	if g.campaignProgress.WL[3] != 0 {
		t.Fatalf("second campaign retained prior W/L mark %q", g.campaignProgress.WL[3])
	}

	continued := &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign}}
	g.campaignProgress = session.BankProgress{}
	g.campaignProgress.Thumbs[3] = 'W'
	g.campaignProgressSet = true
	g.adoptCampaignProgress(continued)
	if continued.Progress.Thumbs[3] != 'W' {
		t.Fatal("continuation did not preserve copied campaign mark")
	}
}
