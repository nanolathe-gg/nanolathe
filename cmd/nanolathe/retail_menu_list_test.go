package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

func TestRetailListWrapUsesDelimitedRunsAndExactFit(t *testing.T) {
	measure := func(text string) int { return len(text) }
	lines := retailListWrapLines("abc def\rghi", measure, 3)
	want := []string{"abc", "def", "ghi"}
	if len(lines) != len(want) {
		t.Fatalf("lines=%q, want %q", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("line %d=%q, want %q", i, lines[i], want[i])
		}
	}
}

// This small authored-font raster goes through the production list painter.
// The second run must start one tall-list line step below the first
// [07 R-WGT-01 §4]. Set NANOLATHE_LIST_TALL_SHOT to write its PNG.
func TestRetailTallListWrapsRaster(t *testing.T) {
	font := formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
	font.Frames['I'].Frame = &formats.GAFFrame{Width: 1, Height: 2}
	font.Frames['A'].Frame = &formats.GAFFrame{Width: 2, Height: 1, YOffset: 2, Pixels: []byte{31, 31}, Transparent: []bool{false, false}}
	font.Frames[' '].Frame = &formats.GAFFrame{Width: 1, Height: 1, Transparent: []bool{true}}
	pal := &palette.Tables{}
	for i := range pal.Base {
		pal.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
	}
	shell := &gameShell{assets: &menuAssets{gafFont: &formats.GAF{Entries: []formats.GAFEntry{font}}, pal: pal}}
	p := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {
		Kind: gui.KindListBox, Active: 1, Attribs: 0x100 | 1,
		Rect: gui.Rect{X: 3, Y: 3, W: 4, H: 20}, ItemHeight: 11,
	}}})
	p.FillTextListAt(1, []string{"A A"}, nil, 4)
	c := bindingClient(t)
	c.SetPalette(pal)
	c.SetUIStage(listRowsStage{shell, p})
	shot := c.ComposeFrameSnapshot()
	writeShellShot(t, c, os.Getenv("NANOLATHE_LIST_TALL_SHOT"))
	for _, y := range []int{5, 11} {
		if got := shot.Indexed[y*shot.Width+5]; got != 31 {
			t.Fatalf("wrapped glyph at y=%d is %d, want 31", y, got)
		}
	}
}
