package main

import (
	"errors"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func campaignWorkflowFixture(t *testing.T) (*contentSet, []mission.Campaign) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "camps"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-core", "b-core", "arm"} {
		side := "CORE"
		if name == "arm" {
			side = "ARM"
		}
		data := "[HEADER]{campaignside=" + side + ";}[MISSION0]{missionname=First;missionfile=first.ota;}[MISSION1]{missionname=Second;missionfile=second.ota;}"
		if err := os.WriteFile(filepath.Join(dir, "camps", name+".tdf"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "maps"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(dir, "maps", name+".ota"), []byte("[GlobalHeader]{[Schema 0]{Type=Easy;}}"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	campaigns, err := mission.Discover(fs)
	if err != nil {
		t.Fatal(err)
	}
	return testContentSet(fs), campaigns
}

func TestCampaignSelectionSurvivesFreshAndOppositeSideListRebuild(t *testing.T) {
	for _, stale := range []bool{false, true} {
		cs, campaigns := campaignWorkflowFixture(t)
		g := &gameShell{cs: cs, missionSide: 0, campaigns: campaigns}
		if stale {
			g.campaignOptions = g.retailCampaignOptions()
		}
		selection, err := g.resolveCampaignSelection("camps/b-core.tdf", 1, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		g.installCampaignSelection(selection)
		g.campaignOptions = g.retailCampaignOptions()
		if g.campaignIdx != 1 || g.missionIdx != 1 || !strings.EqualFold(g.campaignOptions[g.campaignIdx].Path, "camps/b-core.tdf") || g.missionSide != 1 || g.missionDifficulty() != 2 {
			t.Fatalf("wrong rebuilt identity: %+v", selection)
		}
	}
}

func TestFrontendUnavailableChildRetainsParentWithoutMessageBox(t *testing.T) {
	parent := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Name: "HEADER", Active: 1}}})
	g := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuSingle: {unavailable: errors.New("missing single GUI")}}}}
	g.frontend.Open(modeMenuMain, parent, false)
	g.openMenu(modeMenuSingle)
	if g.frontend.Mode != modeMenuMain || g.activePanel() != parent {
		t.Fatal("unavailable child replaced usable parent")
	}
}

func TestCampaignAuthoredAliasesAndResultControlNames(t *testing.T) {
	for _, tc := range []struct{ name, key string }{{"Arm", "side0"}, {"Core", "side1"}, {"Missions", "start"}} {
		if got := frontendCallbackKey(tc.name); got != tc.key {
			t.Fatalf("%s -> %s", tc.name, got)
		}
		if got := frontendCallbackKey(strings.ToLower(tc.name)); got != "" {
			t.Fatalf("case folded %s", tc.name)
		}
	}
	if resultActionForControl("Missions") != ui.ResultActionContinue || resultActionForControl("missions") != ui.ResultActionNone || resultActionForControl("AdjustDiff") != ui.ResultActionNone {
		t.Fatal("result callback vocabulary")
	}
}

func TestCampaignSkirmishRestartRetainsOpenRows(t *testing.T) {
	cfg := session.DirectSkirmishConfig("test")
	cfg.NumPlayers = 4
	cfg.Players[2].Nickname = "first AI"
	cfg.Players[3].Nickname = "second AI"
	g := &gameShell{setup: cfg, retailControllersSet: true, retailControllers: [session.SkirmishMaxPlayers]int{1, 0, 2, 2}}
	compact := g.skirmishConfigForStart("test")
	b := &battleSession{shell: g, sess: &session.Session{Mission: &mission.Mission{Type: mission.TypeSkirmish}, Skirmish: compact}}
	request, ok := b.battleRestartRequest()
	if !ok || !request.ControllersSet || request.Controllers[1] != 0 || request.Controllers[3] != 2 || request.Skirmish.Players[3].Nickname != "second AI" {
		t.Fatalf("restart dropped original rows: %+v", request)
	}
	g.importedRetailBattle = true
	request, ok = b.battleRestartRequest()
	if !ok || request.ControllersSet {
		t.Fatal("imported save borrowed unrelated shell rows")
	}
}

func TestCampaignResultListPrefixesAndAuthoredSelection(t *testing.T) {
	cs, _ := campaignWorkflowFixture(t)
	panel := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Name: "HEADER"}, {Kind: gui.KindListBox, Name: "Missions", Active: 1, Rect: gui.Rect{H: 64}}, {Kind: gui.KindButton, Name: "Difficulty", Stages: 3}}})
	b := &battleSession{fs: cs.fs, hud: &retailBattleHUD{resultPanel: panel}, sess: &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign, CampaignPath: "camps/b-core.tdf"}, Progress: session.BankProgress{Thumbs: [25]byte{'L', 'W'}}}}
	b.postBattle = session.NewPostBattleController(frame.ResultView{Ended: true, Kind: "defeat"}, session.PostBattleConfig{Kind: session.PostBattleCampaign, MissionIndex: 1, Difficulty: 2})
	b.populateResultMissions()
	list := panel.ListAt(panel.Index("Missions"))
	if list.Len() != 2 || list.Items()[0] != string([]byte{0xff, ' '})+"First" || list.Items()[1] != string([]byte{0xfe, ' '})+"Second" || list.Selected() != 1 {
		t.Fatalf("result list %q selected %d", list.Items(), list.Selected())
	}
	list.SetSelected(0)
	b.populateResultMissions()
	if list.Selected() != 0 {
		t.Fatal("repaint reset selected mission")
	}
}

func TestCampaignMissingBriefingRefusesControllerBeforeTakingInput(t *testing.T) {
	cs, campaigns := campaignWorkflowFixture(t)
	g := &gameShell{cs: cs, campaigns: campaigns, missionSide: 1, assets: &menuAssets{}, frontend: ui.NewFrontend(modeMenuMission)}
	selection, err := g.resolveCampaignSelection("camps/b-core.tdf", 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	g.installCampaignSelection(selection)
	g.openCampaignBriefing()
	if g.briefing != nil || g.briefingPanel != nil || g.frontend.Mode != modeMenuMission {
		t.Fatal("missing MSNBRIEF took input ownership")
	}
	if g.assets.briefing == nil || g.assets.briefing.unavailable == nil {
		t.Fatal("missing GUI failure was not retained")
	}
}
