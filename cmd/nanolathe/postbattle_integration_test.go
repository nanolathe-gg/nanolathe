package main

import (
	"image/color"
	"testing"
	"time"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/ui"
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

// TestGlamourFadeRunsFromBlackToTheImagePalette locks the direction of the
// glamour fade. It rises out of black into the decoded image's own colours:
// the table builder takes the image's palette as the target and a zeroed block
// as the start, and the picture is blitted once while only the palette moves.
// Running it the other way ends with the image drawn under the game palette,
// which is what a play-test reads as "an image full of artifacts"
// [08 R-CAMP-01 §6].
func TestGlamourFadeRunsFromBlackToTheImagePalette(t *testing.T) {
	var glamour formats.PCX
	glamour.Palette[1] = color.RGBA{R: 200, G: 100, B: 50, A: 255}
	base := &palette.Tables{}
	base.Base[1] = [4]byte{9, 9, 9, 0}
	b := &battleSession{
		postBattleGlamour: &glamour,
		hud:               &retailBattleHUD{pal: base},
	}
	b.initPostBattleGlamourFade(nil)
	if !b.postBattleFadeReady {
		t.Fatal("the fade was not armed")
	}
	if got := b.postBattleFadeDst[4:7]; got[0] != 200 || got[1] != 100 || got[2] != 50 {
		t.Fatalf("fade target = %v, want the glamour image's own palette entry", got)
	}
	if got := b.postBattleFadeCur[4:7]; got[0] != 0 || got[1] != 0 || got[2] != 0 {
		t.Fatalf("fade start = %v, want black", got)
	}
	// Five steps of 200/5 land exactly on the target and no further.
	for i := 0; i < 5; i++ {
		b.advancePostBattleGlamourFade(uint32(i), nil)
	}
	if got := b.postBattleFadeCur[4]; got != 200 {
		t.Fatalf("after five steps the red byte is %d, want the target 200", got)
	}
}

// TestPostBattleStartKeepsTheShellAfterTeardown is the regression for the
// black screen the play-test hit on ENDMSN's Start: the battle's own teardown
// clears every back-reference it holds, `shell` included, so a route that
// reads b.shell after the teardown gets a nil receiver and silently does
// nothing — leaving the front end on its battle mode with no battle installed
// [07 R-FE-01 §10].
func TestPostBattleStartKeepsTheShellAfterTeardown(t *testing.T) {
	b := &battleSession{shell: &gameShell{}}
	b.shell.battle = b
	b.teardown(nil)
	if b.shell != nil {
		t.Fatal("teardown no longer clears the battle's shell reference; the route may read it directly again")
	}
}

// TestCampaignWinAdvancesToTheNextBriefing walks the reported path end to end:
// the first Arm mission entered from its briefing at an 800x600 display mode,
// won, through the whole results sequence and out of ENDMSN's Start. It pins
// the three things the play-test found broken — the loading screen and the
// results screens are 640x480 while the battle is at the chosen mode, the
// glamour screen fades up into the image's own palette, and Start lands on the
// next mission's briefing rather than on nothing
// [07 "The loading screen"][07 R-FE-02 §2][08 R-CAMP-01 §6][07 R-FE-01 §10].
func TestCampaignWinAdvancesToTheNextBriefing(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 800, Height: 600})
	if err != nil {
		t.Fatal(err)
	}
	previous := clPtr
	clPtr = cl
	defer func() { clPtr = previous }()
	cl.SetPalette(shell.assets.pal)
	cl.SetFNT(shell.font)
	cl.SetUIStage(gameShellUIStage{shell: shell})
	shell.display.Width, shell.display.Height = 800, 600
	shell.missionSide = 0
	shell.openMenu(modeMenuMission)
	found := false
	for i := range shell.campaignOptions {
		if shell.campaignOptions[i].Path == "camps/arm campaign.tdf" {
			shell.campaignIdx, found = i, true
		}
	}
	if !found {
		t.Skip("the Arm campaign is not in this install")
	}
	shell.missionIdx = 0
	shell.openCampaignBriefing()
	if shell.briefing == nil {
		t.Fatal("the briefing did not open")
	}
	shell.dispatchBriefing(BriefingActionStart)
	if w, h := cl.Size(); w != retailScreenW || h != retailScreenH {
		t.Fatalf("loading screen surface = %dx%d, want 640x480", w, h)
	}
	for i := 0; i < 2000 && shell.frontend.Mode == modeLoading; i++ {
		shell.stepLoading(0.05)
		time.Sleep(5 * time.Millisecond)
	}
	if shell.battle == nil {
		t.Fatalf("the mission never loaded; mode=%v", shell.frontend.Mode)
	}
	if w, h := cl.Size(); w != 800 || h != 600 {
		t.Fatalf("battle surface = %dx%d, want the chosen 800x600 display mode", w, h)
	}
	b := shell.battle
	sess := b.sess
	// ARM1's victory is a trigger, so an already-satisfied victory queue stands
	// in for playing it [08 R-TRIG-01 §6].
	sess.Mission.Victory = []*triggers.Trigger{{Kind: triggers.KindBuildUnitType, Completed: true}}
	for i := 0; i < 30*20 && !b.isResultVisible(); i++ {
		sess.Step(sess.Clock.ScaledAnchor + 1)
	}
	if !b.isResultVisible() {
		t.Fatalf("the mission never ended; latch=%+v", sess.Latch)
	}
	b.ensurePostBattleController()
	for i := 0; i < 900 && b.postBattle.State() != session.PostBattleEndMission; i++ {
		b.stepPostBattle(1.0/30.0, cl.Input(), cl)
		if b.postBattle.State() == session.PostBattleGlamour && i > 200 {
			// The glamour screen waits for a key once its deadline passes.
			cl.Input().Kbd.SetKey(input.KeySpace, true)
		}
	}
	if got := b.postBattle.State(); got != session.PostBattleEndMission {
		t.Fatalf("the results sequence stalled in state %d", got)
	}
	if w, h := cl.Size(); w != retailScreenW || h != retailScreenH {
		t.Fatalf("ENDMSN surface = %dx%d, want the 640x480 the results controller forces", w, h)
	}
	if shell.resultBackground == nil {
		t.Fatal("ENDMSN opened without its outcome background bitmap")
	}
	b.doResultAction(ui.ResultActionContinue, cl)
	if shell.battle != nil {
		t.Fatal("Start left the finished battle installed")
	}
	if shell.briefing == nil || shell.frontend.Mode != modeMenuMission {
		t.Fatalf("Start left the shell on mode %v with briefing=%v; want the next mission's briefing", shell.frontend.Mode, shell.briefing != nil)
	}
	if shell.missionIdx != 1 {
		t.Fatalf("Start selected mission index %d, want the successor 1", shell.missionIdx)
	}
}
