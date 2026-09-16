package main

import (
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func restartWindowForTest() *gui.Window {
	return &gui.Window{Rect: gui.Rect{W: 300, H: 200}, Header: gui.Header{CrDefault: "CANCEL", EscDefault: "CANCEL", DefaultFocus: "Difficulty"}, Gadgets: []gui.Gadget{
		{},
		{Kind: gui.KindLabel, Name: "MISSIONNAME", Active: 1, Rect: gui.Rect{X: 5, Y: 5, W: 200, H: 12}},
		{Kind: gui.KindLabel, Name: "MISSIONNAME1", Active: 1, Rect: gui.Rect{X: 5, Y: 20, W: 200, H: 12}},
		{Kind: gui.KindButton, Name: "Difficulty", Active: 1, Stages: 3, Labels: []string{"Easy", "Medium", "Hard"}, Rect: gui.Rect{X: 5, Y: 40, W: 80, H: 20}},
		{Kind: gui.KindButton, Name: "CANCEL", Active: 1, Rect: gui.Rect{X: 5, Y: 70, W: 80, H: 20}},
		{Kind: gui.KindButton, Name: "RESTART", Active: 1, Rect: gui.Rect{X: 100, Y: 70, W: 80, H: 20}},
	}}
}

func TestBattleRestartReplacesExitAndCancelReturnsToPausedRoot(t *testing.T) {
	state := ui.NewProductionBattleState()
	state.OpenOptions()
	state.ShowExit()
	state.ShowRestart()
	if got := state.Modal(); got != ui.BattleModalRestart {
		t.Fatalf("restart modal = %d, want restart", got)
	}
	if state.HasExitLayer() {
		t.Fatal("EXITMENU remained beneath RESTART.GUI")
	}
	if got := state.Activate("Difficulty"); got != ui.BattleModalActionRestartDifficulty {
		t.Fatalf("Difficulty action = %d, want stage action", got)
	}
	if got := state.Activate("RESTART"); got != ui.BattleModalActionRestart {
		t.Fatalf("RESTART action = %d, want restart action", got)
	}
	state.Activate("CANCEL")
	if got := state.Modal(); got != ui.BattleModalOptions {
		t.Fatalf("restart cancel modal = %d, want surviving options root", got)
	}
}

func TestBattleRestartConfiguresAuthoredNameDifficultyAndFocus(t *testing.T) {
	window := restartWindowForTest()
	b := &battleSession{
		sess: &session.Session{Mission: &mission.Mission{
			Type:                mission.TypeCampaign,
			CampaignMissionName: "A long mission name that wraps here",
			Difficulty:          2,
		}},
		hud: &retailBattleHUD{restartWin: window},
	}
	b.openBattleRestartDialog()
	if got := b.restart.panel.Focused(); got != 3 {
		t.Fatalf("focused gadget = %d, want Difficulty index 3", got)
	}
	if got := b.restartGadgetStage(3); got != 2 {
		t.Fatalf("difficulty stage = %d, want hard", got)
	}
	if got := b.restartGadgetText(window, 3); got != "Hard" {
		t.Fatalf("difficulty text = %q, want Hard", got)
	}
	if got := b.restartGadgetText(window, 1); got == "" {
		t.Fatal("MISSIONNAME was not populated")
	}
	b.restart.setMissionName(nil, "short "+strings.Repeat("A", 195)+" tail")
	if b.restart.missionName != "short" || b.restart.missionName1 == "" {
		t.Fatalf("wrapped mission labels = %q / %q, want split at the measured space", b.restart.missionName, b.restart.missionName1)
	}
}

func TestBattleRestartRequestPreservesCampaignOrSkirmishEntryIdentity(t *testing.T) {
	campaign := &battleSession{sess: &session.Session{Mission: &mission.Mission{
		Type: mission.TypeCampaign, CampaignPath: "camps/arm.tdf", CampaignIndex: 4,
	}}, restart: battleRestartState{difficulty: 2}}
	request, ok := campaign.battleRestartRequest()
	if !ok || !request.Campaign || request.Difficulty != 2 || request.CampaignPath != "camps/arm.tdf" || request.CampaignIndex != 4 {
		t.Fatalf("campaign restart request = %+v, ok=%v", request, ok)
	}

	config := session.SkirmishConfig{MapName: "Ashap Plateau", NumPlayers: 4, Difficulty: 1}
	config.Players[3].Nickname = "Core Commander"
	skirmish := &battleSession{sess: &session.Session{
		Mission:  &mission.Mission{Type: mission.TypeSkirmish},
		Skirmish: config,
	}, restart: battleRestartState{difficulty: 0}}
	request, ok = skirmish.battleRestartRequest()
	if !ok || request.Campaign || request.Difficulty != 0 || request.Skirmish.MapName != config.MapName || request.Skirmish.NumPlayers != 4 || request.Skirmish.Players[3].Nickname != "Core Commander" {
		t.Fatalf("skirmish restart request = %+v, ok=%v", request, ok)
	}
}

func TestBattleRestartAcceptsOnlyThroughLifecycleCallback(t *testing.T) {
	b := &battleSession{sess: &session.Session{Mission: &mission.Mission{
		Type: mission.TypeCampaign, CampaignPath: "camps/core.tdf", CampaignIndex: 1,
	}}, restart: battleRestartState{difficulty: 1}}
	var got battleRestartRequest
	b.restartBattle = func(_ *client.Client, request battleRestartRequest) { got = request }
	b.acceptBattleRestart(nil)
	if !got.Campaign || got.CampaignPath != "camps/core.tdf" || got.CampaignIndex != 1 || got.Difficulty != 1 {
		t.Fatalf("lifecycle restart request = %+v", got)
	}
}

func TestBattleRestartProductionPointerAndKeyboardRoutes(t *testing.T) {
	window := restartWindowForTest()
	state := ui.NewProductionBattleState()
	state.OpenOptions()
	state.ShowExit()
	state.ShowRestart()
	b := &battleSession{
		sess:     &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign, CampaignPath: "camps/core.tdf", CampaignIndex: 1}},
		hud:      &retailBattleHUD{restartWin: window},
		battleUI: state,
	}
	b.openBattleRestartDialog()

	// The real widget pass owns the staged pointer gesture: press then release
	// advances Difficulty exactly once before routing its authored callback.
	in := input.NewState()
	in.Mouse.SetPosition(10, 45)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleMenuInput(in, nil)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleBattleMenuInput(in, nil)
	if got := b.restart.difficulty; got != 1 {
		t.Fatalf("pointer Difficulty stage = %d, want 1", got)
	}

	// RESTART uses the production menu transition and reaches the lifecycle
	// callback only after the widget's release inside its authored rectangle.
	var got battleRestartRequest
	b.restartBattle = func(_ *client.Client, request battleRestartRequest) { got = request }
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(110, 75)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleMenuInput(in, nil)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleBattleMenuInput(in, nil)
	if !got.Campaign || got.Difficulty != 1 {
		t.Fatalf("pointer restart request = %+v", got)
	}

	// Enter and Escape have no ordinary-mode token. Header defaults describe
	// GUI token handling, but this child receives none from the battle path.
	state.ShowRestart()
	b.openBattleRestartDialog()
	in = input.NewState()
	in.Kbd.SetKey(input.KeyEscape, true)
	b.handleBattleMenuInput(in, nil)
	if state.Modal() != ui.BattleModalRestart {
		t.Fatalf("Escape modal = %d, want unchanged restart", state.Modal())
	}
	// CANCEL is still a normal pointer-fired child action and returns only to
	// the paused options root.
	in = input.NewState()
	in.Mouse.SetPosition(10, 75)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleMenuInput(in, nil)
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	b.handleBattleMenuInput(in, nil)
	if state.Modal() != ui.BattleModalOptions {
		t.Fatalf("pointer CANCEL modal = %d, want options root", state.Modal())
	}
}

func TestBattleRestartPointerCaptureDrivesPainterState(t *testing.T) {
	window := restartWindowForTest()
	b := &battleSession{hud: &retailBattleHUD{restartWin: window}}
	b.openBattleRestartDialog()
	index := b.restart.panel.Index("Difficulty")
	in := input.NewState()

	// Entering while held after an outside press does not acquire capture.
	in.Mouse.SetPosition(200, 45)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleRestartInput(in, nil)
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(10, 45)
	b.handleBattleRestartInput(in, nil)
	if got := b.restart.panel.DownAt(index); got != 0 {
		t.Fatalf("outside press entering button down=%d, want 0", got)
	}

	// A captured pointer clears the down word when it leaves. Returning while
	// still held restores it in the input service [07 R-WGT-01 §3].
	in = input.NewState()
	in.Mouse.SetPosition(10, 45)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	b.handleBattleRestartInput(in, nil)
	if got := b.restart.panel.DownAt(index); got != 1 {
		t.Fatalf("inside press down=%d, want 1", got)
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(200, 45)
	b.handleBattleRestartInput(in, nil)
	if got := b.restart.panel.DownAt(index); got != 0 {
		t.Fatalf("captured leave down=%d, want 0", got)
	}
	in.Mouse.SetPosition(10, 45)
	b.handleBattleRestartInput(in, nil)
	if got := b.restart.panel.DownAt(index); got != 1 {
		t.Fatalf("captured return down=%d, want 1", got)
	}
}

func TestBattleRestartStagedFrameUsesPanelStageWithoutShell(t *testing.T) {
	entry := widgetArtEntry("Difficulty", 3, 7, 11)
	gad := gui.Gadget{Kind: gui.KindButton, Name: "Difficulty", Active: 1, Stages: 3, ButtonArtResolved: true, ButtonArt: &entry, Rect: gui.Rect{W: 10, H: 10}}
	h := &retailBattleHUD{}
	if got := h.modalGadgetFrameState(gad, nil, 0, 2, false); got != entry.Frames[2].Frame {
		t.Fatal("shell-less staged button did not retain selected stage frame")
	}
	// An explicit unresolved external button slot terminates lookup. It must
	// not silently borrow the common button plate.
	gad.ExternalArtResolved, gad.ExternalArt = true, nil
	h.common = &formats.GAF{Entries: []formats.GAFEntry{widgetArtEntry("BUTTONS0", 19, 23)}}
	if got := h.modalGadgetArtFrames(gad, nil); got != 0 {
		t.Fatalf("explicit nil external art frame count=%d, want 0", got)
	}
}

func TestBattleRestartSkirmishRetainsRowsBeforeFreshEntry(t *testing.T) {
	g := &gameShell{}
	request := battleRestartRequest{Skirmish: session.SkirmishConfig{MapName: "Case-Sensitive Map", NumPlayers: 3, Difficulty: 2}}
	request.Skirmish.Players[0].Controller = session.SkirmishDefaultController
	request.Skirmish.Players[1].Controller = 1
	request.Skirmish.Players[2].Controller = 1
	g.restartSkirmishEntry(request)
	if g.setup.MapName != request.Skirmish.MapName || g.setup.NumPlayers != 3 || g.setup.Difficulty != 2 {
		t.Fatalf("retained skirmish setup = %+v", g.setup)
	}
	if got := g.retailControllers; got[0] != 1 || got[1] != 2 || got[2] != 2 {
		t.Fatalf("retained row conversion = %v", got[:3])
	}
}

func TestBattleRestartCampaignRetainsTeardownScoreAndSelectedMission(t *testing.T) {
	shell, _ := retailShellForTest(t)
	shell.activateGadget("SINGLE")
	shell.activateGadget("NewCamp")
	if shell.campaignIdx < 0 || shell.campaignIdx >= len(shell.campaignOptions) {
		t.Skip("reference install exposes no campaign")
	}
	campaign := shell.campaignOptions[shell.campaignIdx]
	if len(campaign.Missions) == 0 {
		t.Skip("selected campaign has no missions")
	}
	stub := campaign.Missions[0]
	old := &session.Session{Mission: &mission.Mission{
		Type: mission.TypeCampaign, CampaignPath: campaign.Path, CampaignIndex: stub.Index, Difficulty: 2,
	}, CampaignSlot: stub.Index, Latch: session.EndLatch{Bits: session.LatchBitWin1}}
	old.Progress.Thumbs[stub.Index] = 'U'
	shell.battle = &battleSession{sess: old, shell: shell}
	shell.restartBattle(nil, battleRestartRequest{Campaign: true, CampaignPath: campaign.Path, CampaignIndex: stub.Index, Difficulty: 1})
	if shell.battle != nil || shell.briefing == nil {
		t.Fatal("campaign restart did not retire battle and enter the campaign preparation route")
	}
	if shell.campaignProgress.Thumbs[stub.Index] != 'W' {
		t.Fatalf("restart teardown mark = %q, want W", shell.campaignProgress.Thumbs[stub.Index])
	}
	if shell.campaignIdx < 0 || shell.campaignOptions[shell.campaignIdx].Path != campaign.Path || shell.missionIdx < 0 || shell.campaignOptions[shell.campaignIdx].Missions[shell.missionIdx].Index != stub.Index {
		t.Fatalf("campaign reopen/set selection = campaign %d mission %d", shell.campaignIdx, shell.missionIdx)
	}
	if shell.missionDifficulty() != 1 {
		t.Fatalf("campaign restart difficulty = %d, want 1", shell.missionDifficulty())
	}
}

func TestBattleRestartRemountsOriginalContentRoot(t *testing.T) {
	shell, _ := retailShellForTest(t)
	original := shell.cs.root
	shell.opts.Root = t.TempDir() // SAVEGAME redirection must not become a mount root.
	if !shell.prepareBattleRestartContent() {
		t.Fatal("restart content remount failed")
	}
	t.Cleanup(func() { _ = shell.cs.Close() })
	if shell.cs.root != original {
		t.Fatalf("restart mount root = %q, want original %q", shell.cs.root, original)
	}
}

// TestBattleRestartRetailShot writes the actual production RESTART.GUI layer
// when NANOLATHE_RESTART_SHOT names an output PNG. It is opt-in because the
// image is review evidence, while the assertions remain fast in the default
// test tier.
func TestBattleRestartRetailShot(t *testing.T) {
	path := os.Getenv("NANOLATHE_RESTART_SHOT")
	if path == "" {
		t.Skip("set NANOLATHE_RESTART_SHOT to capture RESTART.GUI")
	}
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	for step := int32(1); step <= 30 && sess.Snapshot.Current() == nil; step++ {
		sess.Step(step)
	}
	if sess.Snapshot.Current() == nil {
		t.Fatal("battle did not publish a snapshot")
	}
	const width, height = 640, 480
	cam := &camera.Camera{ViewW: width, ViewH: height, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	centerBattleStartCamera(sess, cam)
	b := &battleSession{sess: sess, cat: cat, cam: cam, shell: shell}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, shell, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	b.openBattleMenu()
	b.activateBattleMenuButton("EXIT", nil)
	b.activateBattleMenuButton("RESTART", nil)
	if b.battleState().Modal() != ui.BattleModalRestart || b.restart.panel == nil {
		t.Fatal("production restart route did not open RESTART.GUI")
	}
	if difficulty := b.restart.panel.Index("Difficulty"); difficulty >= 0 {
		b.restart.panel.SetStageAt(difficulty, 2)
		b.restart.difficulty = 2
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.fs)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, cl.ComposeFrame()); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
