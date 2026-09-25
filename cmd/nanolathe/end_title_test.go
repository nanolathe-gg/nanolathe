package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

type endTitleStage struct {
	hud    *retailBattleHUD
	battle *battleSession
}

func (s endTitleStage) DrawUI(c *client.Client, presented client.UIFrame) {
	s.hud.drawEndTitle(c, s.battle, presented.Committed)
}

// The in-battle end titles share one gate, the local slot's watcher bit, and
// otherwise follow the latch's outcome bit; a draw has none [07 §11].
func TestEndTitleGateAndOutcome(t *testing.T) {
	h := &retailBattleHUD{victoryFrame: &formats.GAFFrame{}, defeatFrame: &formats.GAFFrame{}}
	won := frame.ResultView{Ended: true, Kind: "victory"}
	lost := frame.ResultView{Ended: true, Kind: "defeat"}
	if h.endTitleFrame(won, false) != h.victoryFrame || h.endTitleFrame(lost, false) != h.defeatFrame {
		t.Fatal("the outcome bit did not select its title")
	}
	if h.endTitleFrame(won, true) != nil || h.endTitleFrame(lost, true) != nil {
		t.Fatal("a watching local slot got an end title")
	}
	if h.endTitleFrame(frame.ResultView{Ended: true, Kind: "victory", Draw: true}, false) != nil {
		t.Fatal("a draw got an end title")
	}
	if h.endTitleFrame(frame.ResultView{Kind: "victory"}, false) != nil {
		t.Fatal("an unlatched result got an end title")
	}
}

// The title uses the pause title's view-centre hotspot less the frame's
// authored offsets [07 R-HUD-05], and the watcher gate reads the local slot
// only [07 §11].
func TestEndTitleAnchorAndWatcherGate(t *testing.T) {
	const w, h = 800, 600
	f := &formats.GAFFrame{Width: 5, Height: 2, XOffset: 3, YOffset: -2, Pixels: make([]byte, 10), Transparent: make([]bool, 10)}
	for i := range f.Pixels {
		f.Pixels[i] = byte(i + 40)
	}
	shoot := func(local uint8, watcher int) []byte {
		buf := frame.NewBuffer()
		fr := buf.BeginWrite()
		fr.Result = frame.ResultView{Ended: true, Kind: "victory"}
		fr.Selection.LocalPlayer = local
		if watcher >= 0 {
			fr.Players[watcher].Watcher = true
		}
		if err := buf.Publish(1); err != nil {
			t.Fatal(err)
		}
		c, err := client.New(client.Options{Buffer: buf, Width: w, Height: h})
		if err != nil {
			t.Fatal(err)
		}
		c.SetUIStage(endTitleStage{hud: &retailBattleHUD{victoryFrame: f}, battle: &battleSession{}})
		return c.ComposeFrameSnapshot().Indexed
	}
	left, top := (w+128)/2-3, h/2+2
	shot := shoot(1, 4)
	for y := 0; y < 2; y++ {
		for x := 0; x < 5; x++ {
			if got, want := shot[(top+y)*w+left+x], f.Pixels[y*5+x]; got != want {
				t.Fatalf("title pixel (%d,%d) = %d, want %d", left+x, top+y, got, want)
			}
		}
	}
	shot = shoot(1, 1)
	for i, v := range shot {
		if v != 0 {
			t.Fatalf("a watching local slot drew a title pixel at %d", i)
		}
	}
}

// The title stays on the battle picture through the whole darkening and is
// gone once ENDMSN replaces the picture [07 §11][08 R-CAMP-01 §6].
func TestEndTitleStaysUntilEndMission(t *testing.T) {
	view := frame.ResultView{Ended: true, Kind: "defeat"}
	buf := frame.NewBuffer()
	buf.BeginWrite().Result = view
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: buf, Width: 1, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetCursors(&client.Cursors{})
	b := &battleSession{
		postBattle:         session.NewPostBattleController(view, session.PostBattleConfig{Kind: session.PostBattleSkirmish}),
		postBattleLastUnit: -1,
		hud:                &retailBattleHUD{pal: &palette.Tables{}},
	}
	sawFade := false
	for i := 0; i < 60 && b.postBattle.State() != session.PostBattleEndMission; i++ {
		if !b.endTitleOnPicture() {
			t.Fatalf("title left the picture in state %d", b.postBattle.State())
		}
		sawFade = sawFade || b.postBattle.State() == session.PostBattleFade
		b.stepPostBattle(1.0/30, nil, cl)
	}
	if !sawFade || b.postBattle.State() != session.PostBattleEndMission {
		t.Fatalf("sequence did not run the darkening into ENDMSN (state %d)", b.postBattle.State())
	}
	if b.endTitleOnPicture() {
		t.Fatal("the title stayed over ENDMSN")
	}
}
