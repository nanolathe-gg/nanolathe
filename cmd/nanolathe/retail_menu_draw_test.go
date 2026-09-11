package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// The map chooser leaves its parent screen visible outside its own surface
// [07 §4]. Opening a fresh runtime window must preserve that parent's art.
func TestMapChooserPreservesParentBackground(t *testing.T) {
	parent := &gui.Window{Name: "guis/skirmish.gui", Rect: gui.Rect{W: 32, H: 24}}
	chooser := &gui.Window{Name: "guis/selmap.gui", Rect: gui.Rect{X: 8, Y: 4, W: 16, H: 12}}
	bitmap := func(index byte) *formats.PCX {
		pixels := make([]byte, 32*24)
		for i := range pixels {
			pixels[i] = index
		}
		return &formats.PCX{Width: 32, Height: 24, Pixels: pixels}
	}
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuSkirmish), assets: &menuAssets{
		panel: map[shellMode]*retailPanelAssets{
			modeMenuSkirmish: {window: parent, background: bitmap(31)},
			modeMenuMap:      {window: chooser, background: bitmap(47)},
		},
	}}
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 32, Height: 24})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetUIStage(gameShellUIStage{shell: shell})
	shell.openMenu(modeMenuSkirmish)
	shell.openMenu(modeMenuMap)
	if shell.frontend.Panels.SaveUnder().Window == parent {
		t.Fatal("test requires a cloned runtime parent")
	}
	snapshot := cl.ComposeFrameSnapshot()
	for y := 0; y < snapshot.Height; y++ {
		for x := 0; x < snapshot.Width; x++ {
			want := byte(31)
			if x >= 8 && x < 24 && y >= 4 && y < 16 {
				want = 47
			}
			if got := snapshot.Indexed[y*snapshot.Width+x]; got != want {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestRetailMapChooserPreservesSkirmishScreen(t *testing.T) {
	shell, _, cl := retailAssetShell(t)
	shell.openMenu(modeMenuSkirmish)
	before := cl.ComposeFrameSnapshot()
	shell.activateGadget("SelectMap")
	if shell.frontend.Mode != modeMenuMap {
		t.Fatal("SelectMap did not open the chooser")
	}
	r := shell.activePanel().Window.Rect
	after := cl.ComposeFrameSnapshot()
	writeShellShot(t, cl, os.Getenv("NANOLATHE_MAP_SELECT_SHOT"))
	for y := 0; y < after.Height; y++ {
		for x := 0; x < after.Width; x++ {
			if int32(x) >= r.X && int32(x) < r.X+r.W && int32(y) >= r.Y && int32(y) < r.Y+r.H {
				continue
			}
			i := y*after.Width + x
			if after.Indexed[i] != before.Indexed[i] {
				t.Fatalf("chooser changed parent pixel (%d,%d): %d -> %d", x, y, before.Indexed[i], after.Indexed[i])
			}
		}
	}
}

// TestRetailTextBoxDrawsFocusedCaret locks the visible kind-3 focus cue: the
// input text begins three pixels below its authored rectangle and the one-pixel
// caret begins after the measured byte prefix [07 R-WGT-01 §6].
func TestRetailTextBoxDrawsFocusedCaret(t *testing.T) {
	window := &gui.Window{Rect: gui.Rect{W: 32, H: 12}, Gadgets: []gui.Gadget{
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
	for y := 8; y < 12; y++ {
		if got := snapshot.Indexed[y*32+6]; got != 9 {
			t.Fatalf("caret pixel (%d,%d) = %d, want GUI entry 9", 6, y, got)
		}
	}
	// The retained editor extends past its owning child surface.
	for y := 12; y < 18; y++ {
		if got := snapshot.Indexed[y*32+6]; got != 0 {
			t.Fatalf("caret escaped child window at y=%d: %d", y, got)
		}
	}
	if got := snapshot.Indexed[8*32+5]; got != 0 {
		t.Fatalf("pixel before caret = %d, want textbox background", got)
	}
}

// Production painters must retain record identity even when authored names
// collide; named mutation still affects only the first match [07 R-FE-02 §5].
func TestRetailDuplicateGadgetsDrawIndependentState(t *testing.T) {
	snapshot := duplicateGadgetSnapshot(t)
	for _, tc := range []struct {
		x     int
		color byte
	}{{8, snapshot.Indexed[12*snapshot.Width+2]}, {32, 7}, {56, 11}, {80, 13}, {104, 17}} {
		if got := snapshot.Indexed[12*snapshot.Width+tc.x]; got != tc.color {
			t.Fatalf("gadget pixel x=%d = %d, want %d", tc.x, got, tc.color)
		}
	}
}

func duplicateGadgetSnapshot(t *testing.T) client.ComposedFrameSnapshot {
	t.Helper()
	window := &gui.Window{Rect: gui.Rect{W: 128, H: 40}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel}}}
	art := &formats.GAFEntry{Name: "fixture"}
	for i, color := range []byte{3, 7, 11, 13, 17} {
		pixels := make([]byte, 16*16)
		for j := range pixels {
			pixels[j] = color
		}
		art.Frames = append(art.Frames, formats.GAFFrameRef{Frame: &formats.GAFFrame{Width: 16, Height: 16, Compressed: 1, Pixels: pixels, Transparent: make([]bool, len(pixels))}})
		name := []string{"ART", "ART", "art", " ART", "ART "}[i]
		window.Gadgets = append(window.Gadgets, gui.Gadget{Kind: gui.KindSurface, Name: name, Art: "fixture", Active: 1, Status: int16(i), Rect: gui.Rect{X: int32(8 + i*24), Y: 8, W: 16, H: 16}})
	}
	panel := ui.NewPanel(window)
	panel.SetActive("ART", false)
	shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: window, art: &formats.GAF{Entries: []formats.GAFEntry{*art}}}}}}
	shell.frontend.Panels.Replace(panel)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 128, Height: 40})
	if err != nil {
		t.Fatal(err)
	}
	pal := &palette.Tables{}
	pal.Base[20] = [4]byte{30, 40, 50, 255}
	pal.Base[7] = [4]byte{55, 170, 200, 255}
	pal.Base[11] = [4]byte{220, 165, 65, 255}
	pal.Base[13] = [4]byte{170, 100, 210, 255}
	pal.Base[17] = [4]byte{80, 185, 120, 255}
	cl.SetPalette(pal)
	cl.SetUIStage(gameShellUIStage{shell: shell})
	return cl.ComposeFrameSnapshot()
}
