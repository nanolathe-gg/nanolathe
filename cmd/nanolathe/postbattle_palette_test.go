package main

import (
	"image/color"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The first glamour presentation must already use black, with no cursor;
// subsequent steps resolve the image through its own palette [08 R-CAMP-01 §6].
func TestPostBattlePaletteAndCursorLifetime(t *testing.T) {
	view := frame.ResultView{Ended: true, Kind: "victory"}
	buf := frame.NewBuffer()
	buf.BeginWrite().Result = view
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: buf, Width: 1, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := &palette.Tables{}
	base.Base[1] = [4]byte{10, 20, 30, 0}
	cl.SetPalette(base)
	cl.SetGammaFactor(1.5)
	cl.SetCursors(&client.Cursors{})
	glamour := &formats.PCX{Width: 1, Height: 1, Pixels: []byte{1}}
	glamour.Palette[1] = color.RGBA{R: 200, G: 100, B: 50, A: 255}
	b := &battleSession{
		postBattle: session.NewPostBattleController(view, session.PostBattleConfig{
			Kind: session.PostBattleCampaign, HasNext: true, CampaignCDOK: true,
			Glamour: "arm01", GlamourLoaded: true,
		}),
		postBattleLastUnit: -1,
		postBattleGlamour:  glamour,
		hud:                &retailBattleHUD{pal: base},
	}
	cl.SetUIStage(resultOverlayStage{hud: b.hud, battle: b})
	for i := 0; i < 40 && b.postBattle.State() != session.PostBattleGlamour; i++ {
		b.stepPostBattle(1.0/30, nil, cl)
		if b.postBattle.State() == session.PostBattleFade {
			if !cl.Cursors().Hidden || cl.GammaFactor() != 1.5 || cl.PaletteTables() != base {
				t.Fatal("darkening must hide the cursor and retain battle palette/gamma")
			}
		}
	}
	if b.postBattle.State() != session.PostBattleGlamour {
		t.Fatal("glamour state was not reached")
	}
	if !cl.Cursors().Hidden || cl.GammaFactor() != 1 {
		t.Fatal("glamour must hide the cursor and use neutral gamma")
	}
	if got := cl.ComposeFrame().RGBAAt(0, 0); got != (color.RGBA{A: 255}) {
		t.Fatalf("first glamour paint = %v, want opaque black", got)
	}
	for i := 0; i < 5; i++ {
		b.stepPostBattle(1.0/30, nil, cl)
	}
	if got := cl.ComposeFrame().RGBAAt(0, 0); got != glamour.Palette[1] {
		t.Fatalf("completed glamour paint = %v, want authored %v", got, glamour.Palette[1])
	}
	// Cleanup also covers leaving results before the reveal completes.
	cl.Cursors().Hidden = true
	b.restorePostBattlePalette(cl)
	if cl.Cursors().Hidden || cl.GammaFactor() != 1.5 || cl.PaletteTables() != base {
		t.Fatal("results cleanup did not restore cursor, battle gamma and palette")
	}
	// LoadGame replaces the battle without taking either result-button route.
	b.postBattleGamma, b.postBattleGammaSaved = 1.5, true
	cl.SetGammaFactor(1)
	cl.Cursors().Hidden = true
	b.teardown(cl)
	if cl.Cursors().Hidden || cl.GammaFactor() != 1.5 {
		t.Fatal("battle replacement leaked temporary results display state")
	}
}

func TestPostBattleCDCheckShowsCursor(t *testing.T) {
	cl, err := client.New(client.Options{Width: 1, Height: 1})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetCursors(&client.Cursors{})
	b := &battleSession{postBattleLastUnit: -1, postBattle: session.NewPostBattleController(
		frame.ResultView{Ended: true, Kind: "victory"},
		session.PostBattleConfig{Kind: session.PostBattleCampaign, CampaignCDOK: false},
	)}
	for i := 0; i < 40 && b.postBattle.State() != session.PostBattleCDIdle; i++ {
		b.stepPostBattle(1.0/30, nil, cl)
	}
	if b.postBattle.State() != session.PostBattleCDIdle || cl.Cursors().Hidden {
		t.Fatal("CD-check dialog must restore a visible pointer")
	}
}

// A revealed column is still animating; cursor visibility follows all actual
// bar targets, including exact equality and keyboard reveal [08 R-CAMP-01 §6].
func TestResultCursorWaitsForCompletedBars(t *testing.T) {
	for _, earlyReveal := range []bool{false, true} {
		cl, err := client.New(client.Options{Width: 640, Height: 480})
		if err != nil {
			t.Fatal(err)
		}
		cl.SetCursors(&client.Cursors{Hidden: true})
		view := frame.ResultView{Ended: true, Kind: "victory", Scores: []frame.ResultScore{{Score: 2}}}
		h := &retailBattleHUD{}
		h.ensureResultPresentation(view, nil)
		h.resultState.active = [7]bool{true, true, true, true, true, true, true}
		h.resultState.group = len(resultBars) - 1
		if earlyReveal {
			h.resultState.group = 0
		}
		b := &battleSession{postBattleClock: 1}
		h.drawResultStats(cl, b, view)
		if !cl.Cursors().Hidden {
			t.Fatal("cursor visible before score reached target")
		}
		b.postBattleClock = 3
		h.drawResultStats(cl, b, view)
		if cl.Cursors().Hidden {
			t.Fatal("cursor hidden after all targets reached")
		}
		if !h.resultState.animating[0][6] {
			t.Fatal("fixture must hit target exactly with animation flag still set")
		}
	}
}
