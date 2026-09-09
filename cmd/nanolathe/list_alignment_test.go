package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"os"
	"testing"
)

// Each capture goes through the real list painter and GAF pen, including the
// post-text table remap [07 R-WGT-01 §4].
func TestListAlignmentHeadingAndInclusiveSelection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attrs    uint32
		text     string
		selected bool
		x        int
		want     byte
	}{
		{"left", 1, "A", false, 5, 31},
		{"right", 4, "A", false, 18, 31},
		{"centre", 2, "A", false, 11, 31},
		{"left precedes right", 5, "A", false, 5, 31},
		{"right precedes centre", 6, "A", false, 18, 31},
		{"heading measures prefix", 2, "&GA", true, 9, 91},
		{"other ampersand prefix", 1, "&XA", false, 5, 31},
		{"selected", 1, "A", true, 5, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			font := formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
			font.Frames['I'].Frame = &formats.GAFFrame{Width: 1, Height: 2}
			for _, code := range []byte{'&', 'G', 'X', 'A'} {
				w := uint16(2)
				if code == 'A' {
					w = 1
				}
				font.Frames[code].Frame = &formats.GAFFrame{Width: w, Height: 1, YOffset: 2, Pixels: []byte{31, 31}, Transparent: []bool{false, false}}
			}
			pal := &palette.Tables{}
			for i := range pal.Base {
				pal.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
			}
			for row := range pal.Shade {
				for i := range pal.Shade[row] {
					pal.Shade[row][i] = byte(i)
				}
			}
			// Only the exact successive four rows turn the text's 31 into 91.
			pal.Shade[13][31] = 41
			pal.Shade[12][41] = 51
			pal.Shade[11][51] = 71
			pal.Shade[10][71] = 91
			for i := range pal.Light {
				pal.Light[i] = 201
			}
			shell := &gameShell{assets: &menuAssets{gafFont: &formats.GAF{Entries: []formats.GAFEntry{font}}, pal: pal}}
			gad := gui.Gadget{Kind: gui.KindListBox, Active: 1, Attribs: tc.attrs | 0x10, Rect: gui.Rect{X: 3, Y: 3, W: 16, H: 14}}
			p := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, gad}})
			p.SetListAt(1, []string{tc.text, ""})
			p.ListAt(1).SetSelected(1)
			if tc.selected {
				p.ListAt(1).SetSelected(0)
			}
			c := bindingClient(t)
			c.SetPalette(pal)
			c.SetUIStage(listRowsStage{shell, p})
			snap := c.ComposeFrameSnapshot()
			if tc.name == "heading measures prefix" {
				writeShellShot(t, c, os.Getenv("NANOLATHE_LIST_PAINTER_SHOT"))
			}
			if got := snap.Indexed[5*snap.Width+tc.x]; got != tc.want {
				t.Fatalf("pen pixel=%d want %d", got, tc.want)
			}
			if tc.name == "selected" {
				for _, pt := range []struct {
					x, y int
					want byte
				}{{4, 6, 0}, {5, 6, 201}, {19, 6, 201}, {20, 6, 0}, {6, 10, 201}, {6, 11, 0}} {
					if got := snap.Indexed[pt.y*snap.Width+pt.x]; got != pt.want {
						t.Fatalf("selection (%d,%d)=%d want %d", pt.x, pt.y, got, pt.want)
					}
				}
			}
		})
	}
}
