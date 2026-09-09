package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// ENDMSN population must establish its widget owner before any rendering;
// otherwise the first admitted button event disappears [08 R-CAMP-01 §8].
func TestEndMissionPopulationOwnsInputBeforeFirstDraw(t *testing.T) {
	controller := session.NewPostBattleController(frame.ResultView{Ended: true, Kind: "victory"}, session.PostBattleConfig{
		Kind: session.PostBattleCampaign, MissionIndex: 1, HasNext: true, CampaignCDOK: true, ProgressCommitted: true,
	})
	window := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {Kind: gui.KindButton, Name: "MainMenu", Active: 1, Rect: gui.Rect{X: 20, Y: 20, W: 30, H: 10}}}}
	b := &battleSession{postBattle: controller, hud: &retailBattleHUD{resultWin: window}}
	for now := 0; now < 30 && controller.State() != session.PostBattleEndMission; now++ {
		b.postBattleClock = float64(now)
		b.stepPostBattle(0, nil, nil)
	}
	if controller.State() != session.PostBattleEndMission || b.hud.resultPanel == nil {
		t.Fatal("population did not open the result input owner")
	}
	p := b.hud.resultPanel
	r := window.PlacedRect(1)
	in := input.NewState()
	in.Mouse.SetPosition(float32(r.X+1), float32(r.Y+1))
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	if _, fired := b.hud.resultControlName(in); fired {
		t.Fatal("press fired before release")
	}
	b.prepareResultPanel()
	if b.hud.resultPanel != p {
		t.Fatal("repeated preparation replaced captured panel")
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetButton(input.MouseButtonLeft, false)
	if name, fired := b.hud.resultControlName(in); !fired || name != "MainMenu" {
		t.Fatalf("first gesture=(%q,%v)", name, fired)
	}
}
