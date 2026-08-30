package main

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/vfs"
)

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
				baseline = composed.InitialHash
			} else if composed.InitialHash != baseline {
				t.Fatalf("initial hash = %s, want %s", composed.InitialHash, baseline)
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
