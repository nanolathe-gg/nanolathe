package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

type listRowsStage struct {
	shell *gameShell
	panel *ui.Panel
}

func (s listRowsStage) DrawUI(c *client.Client, _ client.UIFrame) {
	gad := s.panel.Window.Gadgets[1]
	s.shell.drawRetailList(c, s.panel, 1, gad, gad.Rect)
}

func listRowsFixture(t *testing.T, height int32, itemHeight int16, locked bool, selected int) *client.Client {
	t.Helper()
	font := formats.GAFEntry{Frames: make([]formats.GAFFrameRef, 256)}
	// I contributes a four-pixel line metric. A occupies exactly its pen row.
	font.Frames['I'].Frame = &formats.GAFFrame{Width: 1, Height: 2}
	font.Frames['A'].Frame = &formats.GAFFrame{Width: 1, Height: 1, YOffset: 2, Pixels: []byte{31}, Transparent: []bool{false}}
	pal := &palette.Tables{}
	for i := range pal.Base {
		pal.Base[i] = [4]byte{byte(i), byte(i), byte(i), 0}
	}
	for i := range pal.Light {
		pal.Light[i] = 201
	}
	shell := &gameShell{assets: &menuAssets{gafFont: &formats.GAF{Entries: []formats.GAFEntry{font}}, pal: pal}}
	attrs := uint32(0x10)
	if locked {
		attrs |= 0x100
	}
	p := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}, {
		Kind: gui.KindListBox, Name: "ROWS", Active: 1, Attribs: attrs,
		Rect: gui.Rect{X: 3, Y: 3, W: 16, H: height}, ItemHeight: itemHeight,
	}}})
	p.SetListAt(1, []string{"A", "A", "A", "A"})
	p.ListAt(1).SetSelected(selected)
	c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 24, Height: 32})
	if err != nil {
		t.Fatal(err)
	}
	c.SetPalette(pal)
	c.SetUIStage(listRowsStage{shell, p})
	return c
}

// The first row draws unconditionally; later rows use the remaining-height
// boundary, including authored rows shorter than the font metric
// [07 R-WGT-01 §4].
func TestTextListPaintRowsUseMetricBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		height     int32
		itemHeight int16
		wantY      []int
	}{
		{"first row below metric", 1, 0, []int{5}},
		{"second row one pixel short", 8, 0, []int{5}},
		{"second row equality", 9, 0, []int{5, 10}},
		{"third row one pixel short", 13, 0, []int{5, 10}},
		{"third row equality", 14, 0, []int{5, 10, 15}},
		{"fourth row equality", 19, 0, []int{5, 10, 15, 20}},
		{"authored tall rows", 20, 8, []int{5, 13, 21}},
		{"authored short rows", 8, 2, []int{5, 7, 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shot := listRowsFixture(t, tc.height, tc.itemHeight, true, 3).ComposeFrameSnapshot()
			for y := 0; y < 32; y++ {
				want := byte(0)
				for _, penY := range tc.wantY {
					if y == penY {
						want = 31
					}
				}
				if got := shot.Indexed[y*24+7]; got != want {
					t.Fatalf("pixel at pen column, row %d = %d, want %d", y, got, want)
				}
			}
		})
	}
}

// Locking suppresses the post-text LHT operation; focus is not its gate
// [07 R-WGT-01 §4].
func TestLockedTextListDoesNotBrightenSelection(t *testing.T) {
	for _, locked := range []bool{false, true} {
		shot := listRowsFixture(t, 14, 0, locked, 0).ComposeFrameSnapshot()
		want := byte(201)
		if locked {
			want = 31
		}
		if got := shot.Indexed[5*24+7]; got != want {
			t.Fatalf("locked=%v selected glyph=%d, want %d", locked, got, want)
		}
		if got := shot.Indexed[10*24+7]; got != 31 {
			t.Fatalf("locked=%v unselected glyph=%d, want 31", locked, got)
		}
	}
}
