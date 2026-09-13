package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the production shell loader: fixing only the session's side mirror
// misses the filtered campaign selection and later ENDMSN Start route.
func TestCampaignRetailRestoreAndContinuationKeepSideAndSelection(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	assets := loadMenuAssets(cs)
	if assets.err != nil {
		t.Fatal(assets.err)
	}
	for side, name := range []string{"Arm", "Core"} {
		t.Run(name, func(t *testing.T) {
			path := "camps/" + name + " Campaign.tdf"
			src, err := session.NewMissionWithEntryOptions(cs.fs, nil, path+":MISSION0", 0, 7, 7, session.MissionEntryOptions{SelectedSide: side, SelectedSideSet: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			src.Advance()
			src.Advance()
			summary := session.RetailBattleSummary(src, "campaign identity", "0", 250)
			in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
			if err != nil {
				t.Fatal(err)
			}
			saved := filepath.Join(t.TempDir(), "CAMPAIGN.SAV")
			if err := src.WriteRetailSave(saved, in); err != nil {
				t.Fatal(err)
			}
			g := &gameShell{opts: Options{Root: root, Seed: 7}, cs: cs, assets: assets, frontend: ui.NewFrontend(modeMenuMain), setup: session.DirectSkirmishConfig("ashap plateau"), missionSide: 1 - side}
			if err := g.loadRetailSavePath(saved); err != nil {
				t.Fatal(err)
			}
			if g.missionSide != side || g.missionDifficulty() != 0 || g.missionIdx != 0 || !strings.EqualFold(g.campaignOptions[g.campaignIdx].Path, path) {
				t.Fatal("restore retained unrelated shell identity")
			}
			b := g.battle
			actual, known := b.sess.SideForOwner(0)
			if !known || actual != side || int(b.sess.Econ.Players[0].Side) != side {
				t.Fatal("saved player side disagrees after restore")
			}
			// Publish the fixture's terminal view through the committed frame seam;
			// the actual post-battle adapter must read local side from this session.
			write := b.sess.Snapshot.BeginWrite()
			write.Result = frame.ResultView{Ended: true, Kind: "defeat"}
			if err := b.sess.Snapshot.Publish(b.sess.Clock.GlobalTick + 1); err != nil {
				t.Fatal(err)
			}
			b.ensurePostBattleController()
			if b.postBattle == nil || b.postBattle.Summary().Side != side {
				t.Fatal("results lost restored side")
			}
			for now := uint32(0); now < 200 && b.postBattle.State() != session.PostBattleEndMission; now++ {
				b.postBattle.Step(now, false)
			}
			b.prepareResultPanel()
			if b.hud.resultPanel == nil {
				t.Fatal("missing result panel")
			}
			b.hud.resultPanel.SetListSelection("Missions", 1, 0)
			if !b.prepareResultMissionSelection() {
				t.Fatal("could not select successor")
			}
			if !b.postBattle.Handle(session.PostBattleControlStart, 200) {
				t.Fatal("result Start refused")
			}
			b.routePostBattleStart(nil)
			if g.briefing == nil || g.briefing.mission.CampaignIndex != 1 || g.missionSide != side {
				t.Fatal("result Start failed to retain selected campaign")
			}
			// The request produced by the actual briefing writer must carry Arm's
			// valid zero separately from absent side selection.
			event, err := g.briefing.Dispatch(BriefingActionStart)
			if err != nil {
				t.Fatal(err)
			}
			if !event.Valid || !event.Request.value.SelectedSideSet || event.Request.value.SelectedSide != side {
				t.Fatal("briefing dropped selected side")
			}
			// Re-enter from a Summary-only continuation while the shell starts on the
			// opposite side, with no cached filtered list.
			g.briefing, g.briefingPanel = nil, nil
			g.campaignOptions = nil
			g.missionSide = 1 - side
			c := &session.RetailCampaignContinuation{CampaignPath: path, MissionIndex: 1, Difficulty: 2, Side: side, Thumbs: [25]byte{'W'}}
			if err := g.applyRetailContinuation(c); err != nil {
				t.Fatal(err)
			}
			if g.briefing == nil || g.briefing.mission.CampaignIndex != 1 || g.missionSide != side || g.missionDifficulty() != 2 || g.campaignProgress.Thumbs[0] != 'W' {
				t.Fatalf("continuation lost identity for %d", side)
			}
		})
	}
}
