package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/ui"
)

// TestRetailTextBoxDrawsFocusedCaret locks the visible kind-3 focus cue: the
// input text begins three pixels below its authored rectangle and the one-pixel
// caret begins after the measured byte prefix [07 R-WGT-01 §6].
func TestRetailTextBoxDrawsFocusedCaret(t *testing.T) {
	window := &gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{
		{Kind: gui.KindPanel, Active: 1},
		{Kind: gui.KindTextBox, Name: "GAMENAME", Active: 1, Attribs: 1, MaxChars: 12, Rect: gui.Rect{X: 4, Y: 5, W: 20, H: 14}},
	}}
	panel := ui.NewPanel(window)
	panel.SetText("GAMENAME", "abc")
	if !panel.FocusEditor(1) {
		t.Fatal("GAMENAME did not capture")
	}
	// Put the caret between b and c through the shared editor path, rather than
	// assigning an unobservable renderer-only position.
	panel.ApplyEditorTokens([]input.Token{{Kind: input.TokenEdit, Key: input.KeyLeft}}, nil)

	shell := &gameShell{
		frontend: ui.NewFrontend(modeMenuMain),
		font:     fixedWidthFont(10),
		assets:   &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: window}}},
	}
	shell.frontend.Panels.Replace(panel)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 32, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	snapshot := cl.ComposeFrameSnapshot()

	// fixedWidthFont advances each ASCII byte by one pixel. The caret therefore
	// starts at X + len("ab") and fills the font's line metric from Y+3.
	for y := 8; y < 18; y++ {
		if got := snapshot.Indexed[y*32+6]; got != 9 {
			t.Fatalf("caret pixel (%d,%d) = %d, want GUI entry 9", 6, y, got)
		}
	}
	if got := snapshot.Indexed[8*32+5]; got != 0 {
		t.Fatalf("pixel before caret = %d, want textbox background", got)
	}
}
