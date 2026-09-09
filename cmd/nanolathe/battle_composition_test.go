package main

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type atomicTestUIStage struct{}

func (*atomicTestUIStage) DrawUI(*client.Client, client.UIFrame) {}

func TestBattleCompositionAdaptersSnapshotEqualRequest(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()

	cfg := session.DirectSkirmishConfig(opts.Map)
	source := func() BattleSeedSource {
		return &scriptedBattleSeedSource{pairs: []BattleSeeds{{Simulation: 37, CRT: 41}}}
	}
	direct, err := directMapBattleRequest(opts, cs, source())
	if err != nil {
		t.Fatal(err)
	}
	menu, err := skirmishBattleRequest(opts, cs, cfg, headlessScenarioSkirmish, nil, source())
	if err != nil {
		t.Fatal(err)
	}
	displayless, _, err := headlessFreshBattleRequest(opts, cs, source())
	if err != nil {
		t.Fatal(err)
	}

	var baseline string
	for _, tc := range []struct {
		name    string
		request freshBattleRequest
	}{
		{name: "direct", request: direct},
		{name: "menu", request: menu},
		{name: "displayless", request: displayless},
	} {
		t.Run(tc.name, func(t *testing.T) {
			composed, err := composeAuthoritativeBattle(tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if baseline == "" {
				baseline = composed.InitialFingerprint
			} else if composed.InitialFingerprint != baseline {
				t.Fatalf("initial hash = %s, want %s", composed.InitialFingerprint, baseline)
			}
			if composed.Identity != opts.Map || composed.SimulationSeed != 37 || composed.CRTSeed != 41 || composed.LocalOwner != 0 {
				t.Fatalf("setup identity = %+v", composed)
			}
			if composed.TerrainWidth < 1 || composed.TerrainHeight < 1 || composed.Session.Econ == nil {
				t.Fatalf("terrain/player setup is incomplete: %+v", composed)
			}
		})
	}
}

func TestEnterBattlePreparationFailureIsAtomic(t *testing.T) {
	oldSession := &session.Session{Snapshot: frame.NewBuffer()}
	oldSession.InitAudio(vfs.New())
	oldCamera := &camera.Camera{X: 17, Z: 23}
	oldUI := ui.NewProductionBattleState()
	oldBattle := &battleSession{sess: oldSession, cam: oldCamera, battleUI: oldUI}
	frontend := ui.NewFrontend(modeMenuMission)
	progress := session.BankProgress{BetweenMissions: 1, WL: [10]byte{'L'}, Thumbs: [25]byte{'W'}}
	contentFS := vfs.New()
	shell := &gameShell{
		battle: oldBattle, cam: oldCamera, frontend: frontend,
		loading: newLoadingState("old"), campaignProgress: progress,
		campaignProgressSet: true, cs: &contentSet{fs: contentFS}, audioOwner: oldSession.Audio,
	}
	cl, err := client.New(client.Options{Width: 64, Height: 64, Buffer: oldSession.Snapshot})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetCamera(oldCamera)
	cl.SetAudioService(oldSession.Audio)
	oldStage := &atomicTestUIStage{}
	cl.SetUIStage(oldStage)
	previousClient := clPtr
	clPtr = cl
	defer func() { clPtr = previousClient }()
	candidate := &session.Session{
		World:    &world.Terrain{CellW: 64, CellH: 64},
		Mission:  &mission.Mission{Type: mission.TypeCampaign},
		Snapshot: frame.NewBuffer(),
	}
	if err := shell.enterBattle(candidate, nil); err == nil {
		t.Fatal("candidate with missing palette unexpectedly succeeded")
	}
	if candidate.Audio != oldSession.Audio || !reflect.DeepEqual(candidate.Progress, progress) {
		t.Fatal("candidate-only audio/progress adoption did not occur before failure")
	}
	if shell.battle != oldBattle || shell.cam != oldCamera || shell.loading == nil || shell.frontend.Mode != modeMenuMission {
		t.Fatal("failed preparation changed active shell state")
	}
	if !reflect.DeepEqual(shell.campaignProgress, progress) || !shell.campaignProgressSet {
		t.Fatal("failed preparation changed campaign progress")
	}
	if cl.Buffer() != oldSession.Snapshot {
		t.Fatal("failed preparation changed client snapshot")
	}
	if got := reflect.ValueOf(cl).Elem().FieldByName("cam").Pointer(); got != reflect.ValueOf(oldCamera).Pointer() {
		t.Fatal("failed preparation changed client camera")
	}
	if got := reflect.ValueOf(cl).Elem().FieldByName("audioService").Pointer(); got != reflect.ValueOf(oldSession.Audio).Pointer() {
		t.Fatal("failed preparation changed client audio")
	}
	uiStage := reflect.ValueOf(cl).Elem().FieldByName("uiStage")
	if uiStage.IsNil() || uiStage.Elem().Pointer() != reflect.ValueOf(oldStage).Pointer() {
		t.Fatal("failed preparation changed client UI stage")
	}
}

// TestRetailSavedCameraWinsOverCommanderCentering also locks the load's shape:
// it is a *jump*, so the desired origin ends equal to the restored current
// origin and no glide survives the load [07 R-CAM-01 §14].
func TestRetailSavedCameraWinsOverCommanderCentering(t *testing.T) {
	// The fixture carries map extents because retail clamps the loaded origin
	// too; with none, this would measure the clamp rather than the load.
	cam := camera.NewFromTerrain(2048, 2048, 2048, 2048, 640, 480)
	cam.X, cam.Z = 1, 2
	cam.GlideTo(900, 900)
	applyRetailSavedCamera(cam, &save.Camera{XPosition: 317, ZPosition: 419})
	if cam.X != 317 || cam.Z != 419 {
		t.Fatalf("saved camera = %d,%d, want 317,419", cam.X, cam.Z)
	}
	if cam.Follow.Desired.X != cam.X || cam.Follow.Desired.Z != cam.Z {
		t.Fatalf("desired origin = %d,%d after a load, want the current origin %d,%d",
			cam.Follow.Desired.X, cam.Follow.Desired.Z, cam.X, cam.Z)
	}
}

// TestCameraResetZeroesBothOrigins locks the world rebuild's camera-block
// reset: the block spans the current and the desired origin, so both read
// (0, 0) afterwards [07 R-CAM-01 §14].
func TestCameraResetZeroesBothOrigins(t *testing.T) {
	cam := camera.NewFromTerrain(2048, 2048, 2048, 2048, 640, 480)
	cam.X, cam.Z = 500, 600
	cam.GlideTo(900, 900)
	cam.JumpTo(0, 0)
	if cam.X != 0 || cam.Z != 0 {
		t.Fatalf("current origin = %d,%d after the reset, want 0,0", cam.X, cam.Z)
	}
	if cam.Follow.Desired.X != 0 || cam.Follow.Desired.Z != 0 {
		t.Fatalf("desired origin = %d,%d after the reset, want 0,0", cam.Follow.Desired.X, cam.Follow.Desired.Z)
	}
}

// TestWatcherBattleStartCameraJumpsToViewCentreOrigin locks the world-rebuild
// tail's watcher branch: retail's origin is (trunc(viewW/2), trunc(viewH/2)),
// which in this build's frame of reference is that value less the leading
// inset, and the jump leaves no glide [07 R-CAM-01 §14][03 §4.1].
func TestWatcherBattleStartCameraJumpsToViewCentreOrigin(t *testing.T) {
	cam := camera.NewFromTerrain(4096, 4096, 4096, 4096, 640, 480)
	watcherBattleStartCamera(cam)
	viewW, viewH := cam.BattleView()
	wantX, wantZ := cam.BattleViewCenterOrigin(2*(viewW/2), 2*(viewH/2))
	if cam.X != wantX || cam.Z != wantZ {
		t.Fatalf("watcher origin = %d,%d, want %d,%d", cam.X, cam.Z, wantX, wantZ)
	}
	if cam.Follow.Desired.X != cam.X || cam.Follow.Desired.Z != cam.Z {
		t.Fatalf("watcher jump left a glide: desired %d,%d current %d,%d",
			cam.Follow.Desired.X, cam.Follow.Desired.Z, cam.X, cam.Z)
	}
}

func TestBattleTeardownDropsPresentationAndLoadingReferences(t *testing.T) {
	fs := vfs.New()
	sess := &session.Session{Snapshot: frame.NewBuffer()}
	sess.InitAudio(fs)
	oldBuffer := sess.Snapshot
	oldUI := ui.NewProductionBattleState()
	oldUI.SetLatch(input.LatchAttack)
	battle := &battleSession{
		sess: sess, cam: &camera.Camera{}, hud: &retailBattleHUD{}, fs: fs,
		battleUI: oldUI,
	}
	shell := &gameShell{battle: battle, cam: battle.cam, loading: newLoadingState("old")}
	battle.shell = shell

	cl, err := client.New(client.Options{Width: 64, Height: 64, Buffer: oldBuffer})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(nil)
	cl.SetCamera(battle.cam)
	cl.SetPalette(&palette.Tables{})
	cl.SetFNT(&formats.FNT{})
	cl.SetAudioService(sess.Audio)
	cl.SetUIStage(battleHUDUIStage{hud: battle.hud, battle: battle})
	cl.Input().Kbd.SetKey(input.KeyA, true)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)

	shell.teardownBattle(cl)
	shell.teardownBattle(cl) // idempotent

	if shell.battle != nil || shell.cam != nil || shell.loading != nil {
		t.Fatalf("shell retained battle/camera/loading: battle=%p camera=%p loading=%p", shell.battle, shell.cam, shell.loading)
	}
	if battle.sess != nil || battle.hud != nil || battle.cam != nil || battle.battleUI != nil || battle.shell != nil || battle.controller != nil {
		t.Fatalf("battle retained composition references: %+v", battle)
	}
	if oldUI.Latch() != input.LatchNormal || oldUI.Input.BuildDef != "" {
		t.Fatalf("old input latch survived teardown: %+v", oldUI.Input)
	}
	if cl.Buffer() == oldBuffer {
		t.Fatal("client still targets the old session snapshot")
	}
	for _, field := range []string{"terrain", "cam", "pal", "fnt", "uiStage", "audioService"} {
		value := reflect.ValueOf(cl).Elem().FieldByName(field)
		if !value.IsValid() || !value.IsNil() {
			t.Fatalf("client field %s still retains the old battle", field)
		}
	}
	if cl.Input().Kbd.KeyHeld(input.KeyA) || cl.Input().Mouse.Held(input.MouseButtonLeft) {
		t.Fatal("client input latch survived teardown")
	}
	if audio.GlobalOutput() != nil {
		t.Fatal("battle audio backend survived teardown")
	}
}

func TestFreshLoadRebindsFrontendAfterBattleTeardown(t *testing.T) {
	fs := vfs.New()
	sess := &session.Session{Snapshot: frame.NewBuffer()}
	sess.InitAudio(fs)
	oldBuffer := sess.Snapshot
	oldBattle := &battleSession{
		sess: sess, cam: &camera.Camera{}, hud: &retailBattleHUD{}, fs: fs,
		battleUI: ui.NewProductionBattleState(),
	}
	assets := &menuAssets{pal: &palette.Tables{}, font: &formats.FNT{}, panel: make(map[shellMode]*retailPanelAssets)}
	shell := &gameShell{
		battle: oldBattle, cam: oldBattle.cam, assets: assets,
		frontend: ui.NewFrontend(modeBattle),
	}
	oldBattle.shell = shell

	cl, err := client.New(client.Options{Width: 64, Height: 64, Buffer: oldBuffer})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetCamera(oldBattle.cam)
	cl.SetPalette(&palette.Tables{})
	cl.SetFNT(&formats.FNT{})
	cl.SetAudioService(sess.Audio)
	cl.SetUIStage(battleHUDUIStage{hud: oldBattle.hud, battle: oldBattle})
	previousClient := clPtr
	clPtr = cl
	defer func() { clPtr = previousClient }()

	shell.beginFreshBattleLoad("next", modeMenuMission, freshBattleRequest{}, nil)

	if shell.battle != nil || shell.loading == nil || shell.frontend.Mode != modeLoading {
		t.Fatalf("loading handoff = battle %p loading %p mode %v", shell.battle, shell.loading, shell.frontend.Mode)
	}
	if shell.cam == nil || cl.Buffer() == oldBuffer {
		t.Fatalf("frontend camera/snapshot not restored: camera=%p buffer=%p", shell.cam, cl.Buffer())
	}
	clientValue := reflect.ValueOf(cl).Elem()
	for _, field := range []string{"terrain", "audioService"} {
		if value := clientValue.FieldByName(field); !value.IsNil() {
			t.Fatalf("client field %s retained battle state", field)
		}
	}
	for _, field := range []string{"cam", "pal", "fnt", "uiStage"} {
		if value := clientValue.FieldByName(field); value.IsNil() {
			t.Fatalf("client field %s was not rebound for loading", field)
		}
	}
	if got := clientValue.FieldByName("cam").Pointer(); got != reflect.ValueOf(shell.cam).Pointer() {
		t.Fatal("loading client does not target the frontend camera")
	}
	if got := clientValue.FieldByName("pal").Pointer(); got != reflect.ValueOf(assets.pal).Pointer() {
		t.Fatal("loading client does not target the authored frontend palette")
	}
	if got := clientValue.FieldByName("fnt").Pointer(); got != reflect.ValueOf(assets.font).Pointer() {
		t.Fatal("loading client does not target the authored frontend font")
	}
	if got := clientValue.FieldByName("uiStage").Elem().Type(); got != reflect.TypeOf(gameShellUIStage{}) {
		t.Fatalf("loading UI stage type = %v, want game-shell stage", got)
	}
	if oldBattle.sess != nil || oldBattle.hud != nil || oldBattle.cam != nil {
		t.Fatal("old battle survived the loading handoff")
	}
}

func TestLoadingEntryFailurePreservesReturnTarget(t *testing.T) {
	state := newLoadingState("")
	state.done <- loadResult{sess: &session.Session{Catalog: &content.Catalog{}}}
	shell := &gameShell{
		loading: state, loadingReturn: modeMenuMission,
		frontend: ui.NewFrontend(modeLoading),
	}
	previousClient := clPtr
	clPtr = nil
	defer func() { clPtr = previousClient }()

	shell.stepLoading(0)

	if shell.frontend.Mode != modeMenuMission || shell.loadingReturn != modeMenuMission {
		t.Fatalf("failed entry returned to mode %v with target %v, want mission", shell.frontend.Mode, shell.loadingReturn)
	}
}

func TestBattleRequestAdaptersShareUnavailableContentDiagnostic(t *testing.T) {
	opts := Options{Map: "ashap plateau", Mission: "camps/Arm Campaign.tdf:MISSION0"}
	cfg := session.DirectSkirmishConfig(opts.Map)
	_, directErr := directMapBattleRequest(opts, nil, nil)
	_, menuErr := skirmishBattleRequest(opts, nil, cfg, headlessScenarioSkirmish, nil, nil)
	_, _, displaylessErr := headlessFreshBattleRequest(opts, nil, nil)
	if directErr == nil || menuErr == nil || displaylessErr == nil {
		t.Fatalf("adapter errors = %v / %v / %v", directErr, menuErr, displaylessErr)
	}
	if directErr.Error() != menuErr.Error() || directErr.Error() != displaylessErr.Error() {
		t.Fatalf("diagnostic families differ: %q / %q / %q", directErr, menuErr, displaylessErr)
	}
}
