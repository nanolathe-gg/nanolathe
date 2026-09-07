package main

import (
	"os"
	"testing"
	"time"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/testsupport"
)

// resetSaveLoadScreenState clears the process-wide dialog singletons so one
// test cannot leak a screen into the next.
func resetSaveLoadScreenState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		saveLoadUI = nil
		saveLoadPanel = nil
		saveLoadAssets = nil
	})
	saveLoadUI = nil
	saveLoadPanel = nil
	saveLoadAssets = nil
}

// retailShellForTest mounts the reference install and builds the frontend the
// way the windowed entry does, then redirects SAVEGAME\ into a temporary
// directory so no test ever writes into the retail install.
func retailShellForTest(t *testing.T) (*gameShell, string) {
	t.Helper()
	root := testsupport.RetailRoot(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail content unavailable: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	shell, err := newGameShell(Options{Root: root}, cs)
	if err != nil {
		t.Skipf("retail frontend assets unavailable: %v", err)
	}
	saveDir := t.TempDir()
	shell.opts.Root = saveDir
	return shell, retailSaveDir(saveDir)
}

// The whole campaign-persistence route, windowless: MAINMENU -> SINGLE ->
// NEWGAME selects MISSION0; the results panel's SaveGame writes a
// between-missions bank naming MISSION1; and the load screen resumes that bank
// at MISSION1's briefing [08 R-CAMP-01 §8] [08 R-SAVE-02 §1] [07 R-FE-01 §8].
func TestCampaignContinuationWalkWritesAndResumesABank(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, saveDir := retailShellForTest(t)

	if shell.frontend.Mode != modeMenuMain {
		t.Fatalf("shell opened on mode %d, want MAINMENU", shell.frontend.Mode)
	}
	shell.activateGadget("SINGLE")
	if shell.frontend.Mode != modeMenuSingle {
		t.Fatalf("SINGLE -> mode %d", shell.frontend.Mode)
	}
	shell.activateGadget("NewCamp")
	if shell.frontend.Mode != modeMenuMission {
		t.Fatalf("NewCamp -> mode %d, want NEWGAME", shell.frontend.Mode)
	}
	if shell.campaignIdx < 0 || shell.campaignIdx >= len(shell.campaignOptions) {
		t.Skip("the reference install exposes no campaign on NEWGAME")
	}
	campaign := shell.campaignOptions[shell.campaignIdx]
	if len(campaign.Missions) < 2 {
		t.Skip("the selected campaign has no successor mission")
	}
	first, second := campaign.Missions[0], campaign.Missions[1]

	// The results surface for a won MISSION0 with MISSION1 as its successor.
	// The controller owns the Advance quirk; the shell only supplies identity.
	shell.battle = &battleSession{
		shell: shell,
		sess: &session.Session{Mission: &mission.Mission{
			Type:                mission.TypeCampaign,
			CampaignPath:        campaign.Path,
			CampaignIndex:       first.Index,
			CampaignMissionName: first.Name,
			Difficulty:          1,
		}},
		postBattle: session.NewPostBattleController(
			frame.ResultView{Ended: true, Kind: "victory"},
			session.PostBattleConfig{
				Kind: session.PostBattleCampaign, CampaignCDOK: true, Windowed: true,
				CampaignPath: campaign.Path, Campaign: campaign.Path,
				Mission: first.Name, Map: first.Name, MissionIndex: first.Index,
				HasNext: true, NextMission: second.Name,
				Difficulty: 1, Players: 1,
			}),
	}

	// ENDMSN's SaveGame opens the save dialog over the results panel.
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromResults); err != nil {
		t.Fatalf("open save screen: %v", err)
	}
	if saveLoadUI == nil || saveLoadUI.Mode() != saveScreenMode {
		t.Fatal("save screen did not open")
	}
	// Type through the production captured GAMENAME editor. This deliberately
	// avoids SetName: the test must prove an empty save directory can create
	// its first named save through ordinary input [07 R-WGT-01 §6].
	menuClient, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("menu client: %v", err)
	}
	for _, r := range "campaign slot" {
		if !menuClient.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: r}) {
			t.Fatal("enqueue save-name token")
		}
	}
	shell.menuInput(menuClient)
	if got := saveLoadUI.Name(); got != "campaign slot" {
		t.Fatalf("production GAMENAME edit = %q, want campaign slot", got)
	}
	// Enter is captured by GAMENAME and fires its authored LOAD action, which
	// commits the name just typed above [07 R-WGT-01 §6][08 R-SAVE-02 §1].
	if !menuClient.Input().EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter}) {
		t.Fatal("enqueue GAMENAME Enter")
	}
	shell.menuInput(menuClient)

	path := session.RetailSavePath(saveDir, "campaign slot")
	bank, err := save.Open(path)
	if err != nil {
		t.Fatalf("the results save wrote no readable bank at %s: %v", path, err)
	}
	summary, ok := save.ReadSummary(bank)
	if !ok {
		t.Fatal("written bank carries no Summary account")
	}
	if summary.BetweenMissions != 1 {
		t.Fatalf("BetweenMissions=%d, want 1", summary.BetweenMissions)
	}
	if summary.Mission != second.Name {
		t.Fatalf("Summary Mission=%q, want the successor %q", summary.Mission, second.Name)
	}
	if summary.Gametype != 1 || summary.Description != "campaign slot" {
		t.Fatalf("summary=%+v", summary)
	}

	// CANCEL returns to the surface underneath, and the battle is retired the
	// way a real results-screen load would retire it.
	shell.activateSaveLoadGadget("CANCEL")
	shell.battle = nil

	// The front end's LoadGame lists the bank and resumes it.
	shell.activateGadget("PrevMenu")
	if shell.frontend.Mode != modeMenuSingle {
		t.Fatalf("PrevMenu -> mode %d, want SINGLE", shell.frontend.Mode)
	}
	shell.activateGadget("LoadGame")
	if saveLoadUI == nil || saveLoadUI.Mode() != loadScreenMode {
		t.Fatal("SINGLE LoadGame did not open the load screen")
	}
	if len(saveLoadUI.Entries()) != 1 || saveLoadUI.Entries()[0].Description != "campaign slot" {
		t.Fatalf("load list=%+v, want the one written slot", saveLoadUI.Entries())
	}
	shell.selectSaveLoadRow(0)
	shell.activateSaveLoadGadget("LOAD")

	if shell.briefing == nil {
		t.Fatal("loading the continuation did not open the campaign briefing")
	}
	if got := campaign.Missions[shell.missionIdx].Index; got != second.Index {
		t.Fatalf("resumed at authored mission %d, want the successor %d", got, second.Index)
	}
	if saveLoadUI != nil {
		t.Fatal("the load dialog stayed on the stack after routing into the briefing")
	}
}

// TestApplyRestoredUnitLimitCarriesSummaryMaxUnits is WU-19-214: retail's
// battle-restoration dispatcher stores the save's own `Summary.maxunits`,
// unclamped, into the configured unit-limit word only when the save carried
// the item at all [08 R-ENTRY-01 §6][08 R-SESS-01 §9]. Nanolathe's configured
// word is g.setup.UnitLimit.
//
// "Carried the item at all" is presence, not a nonzero value; this gate read
// `MaxUnits != 0` until save.Summary gained a presence witness, which made a
// stored zero unreadable as a stored zero.
func TestApplyRestoredUnitLimitCarriesSummaryMaxUnits(t *testing.T) {
	shell := &gameShell{}
	shell.setup.UnitLimit = 250

	// A value far outside the 20..500 start-up clamp stays exactly as saved:
	// the restore applies no clamp [08 R-SESS-01 §9].
	shell.applyRestoredUnitLimit(&save.BattleImage{Summary: save.Summary{MaxUnits: 12, HasMaxUnits: true}})
	if shell.setup.UnitLimit != 12 {
		t.Fatalf("configured UnitLimit = %d, want 12 unclamped", shell.setup.UnitLimit)
	}

	// A save without the item leaves the configured word untouched. Absence is
	// the account's, not the value's: the gate reads HasMaxUnits.
	shell.applyRestoredUnitLimit(&save.BattleImage{Summary: save.Summary{MaxUnits: 400}})
	if shell.setup.UnitLimit != 12 {
		t.Fatalf("configured UnitLimit = %d, want unchanged 12 after a missing item", shell.setup.UnitLimit)
	}

	// A present zero is carried, because "only when present" is the whole
	// condition; a retail bank never stores one, so this is the fidelity seam
	// rather than a stock path [08 R-SESS-01 §9].
	shell.applyRestoredUnitLimit(&save.BattleImage{Summary: save.Summary{MaxUnits: 0, HasMaxUnits: true}})
	if shell.setup.UnitLimit != 0 {
		t.Fatalf("configured UnitLimit = %d, want the present zero carried through", shell.setup.UnitLimit)
	}
	shell.setup.UnitLimit = 12

	// A nil image (defensive) is also a no-op.
	shell.applyRestoredUnitLimit(nil)
	if shell.setup.UnitLimit != 12 {
		t.Fatalf("configured UnitLimit = %d, want unchanged 12 after a nil image", shell.setup.UnitLimit)
	}
}

// An empty SAVEGAME directory closes the load screen again with the authored
// message and opens nothing [08 R-SAVE-02 §2].
func TestLoadScreenRefusesAnEmptyList(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, _ := retailShellForTest(t)
	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromFrontend); err != nil {
		t.Fatalf("open load screen: %v", err)
	}
	if saveLoadUI != nil {
		t.Fatal("the load screen opened over an empty list")
	}
	if modal := shell.frontend.Panels.Modal(); modal == nil || modal.Message() != retailNoSavedGamesMessage {
		t.Fatal("the empty list did not raise the authored refusal")
	}
}

// TestSaveScreenSelectionBindsTheAuthoredNameEditor keeps the controller's
// selected-row copy on the same Panel text that the captured GAMENAME editor
// presents [08 R-SAVE-02 §1][07 R-WGT-01 §6].
func TestSaveScreenSelectionBindsTheAuthoredNameEditor(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, saveDir := retailShellForTest(t)
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatalf("create save directory: %v", err)
	}
	writeContinuationSlot(t, saveDir, "selected slot", time.Unix(1_700_000_000, 0))
	if err := shell.openSaveLoadScreen(saveScreenMode, saveLoadFromResults); err != nil {
		t.Fatalf("open save screen: %v", err)
	}
	if saveLoadPanel == nil || !saveLoadPanel.EditorCaptured() {
		t.Fatal("save GAMENAME did not receive its authored initial capture")
	}
	shell.selectSaveLoadRow(0)
	if got := saveLoadUI.Name(); got != "selected slot" {
		t.Fatalf("selected save name = %q, want selected slot", got)
	}
	if got := saveLoadPanel.TextOf("GAMENAME"); got != "selected slot" {
		t.Fatalf("authored GAMENAME text = %q, want selected slot", got)
	}
}

// The in-battle options menu reaches the same one dialog: ARMOPT's SAVEGAME
// and LOADGAME open the two LOADGAME.GUI directions over the battle
// [07 R-FE-01 §7] [07 R-FE-01 §8].
func TestBattleOptionsMenuOpensTheSaveDialog(t *testing.T) {
	resetSaveLoadScreenState(t)
	shell, saveDir := retailShellForTest(t)
	battle := &battleSession{shell: shell, sess: &session.Session{}}
	shell.battle = battle

	battle.battleState().OpenOptions()
	battle.activateBattleMenuButton("SAVEGAME", nil)
	if saveLoadUI == nil || saveLoadUI.Mode() != saveScreenMode || saveLoadUI.Source() != saveLoadFromBattle {
		t.Fatalf("ARMOPT SAVEGAME did not open the save dialog from the battle: %+v", saveLoadUI)
	}
	if _, err := os.Stat(saveDir); err != nil {
		t.Fatalf("the save screen did not create SAVEGAME\\: %v", err)
	}
	// The one window switches direction through its own route button.
	shell.activateSaveLoadGadget("LoadGame")
	if saveLoadUI.Mode() != loadScreenMode {
		t.Fatal("the LoadGame route button did not switch the dialog direction")
	}
	shell.activateSaveLoadGadget("CANCEL")
	if saveLoadUI != nil {
		t.Fatal("CANCEL did not close the dialog")
	}
}
