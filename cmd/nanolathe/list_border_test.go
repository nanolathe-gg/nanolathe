package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
)

type listBorderStage struct {
	shell *gameShell
	rect  gui.Rect
}

func (s listBorderStage) DrawUI(c *client.Client, _ client.UIFrame) { s.shell.drawListBox(c, s.rect) }

// Exact bottom equality stays in the middle band; overflow overlaps flush
// with the border. Oversized tiles must not paint outside the list surface
// [07 R-FE-02 §4][07 R-WGT-01 §4].
func TestListBorderUsesPanelOverflowAndClip(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int32
		x, y int
		want byte
	}{
		{"exact bottom uses middle", 8, 8, 3, 7, 34},
		{"exact right uses right", 8, 8, 7, 3, 33},
		{"overflow bottom is flush", 10, 9, 3, 8, 37},
		{"overflow right is flush", 10, 9, 9, 3, 33},
		{"smaller than tile", 2, 2, 3, 3, 33},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shell := &gameShell{assets: &menuAssets{common: panelTestBank(t, "LISTBOX", 31, 9)}}
			c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 20, Height: 20})
			if err != nil {
				t.Fatal(err)
			}
			c.SetUIStage(listBorderStage{shell, gui.Rect{X: 3, Y: 3, W: tc.w, H: tc.h}})
			shot := c.ComposeFrameSnapshot()
			if got := shot.Indexed[tc.y*20+tc.x]; got != tc.want {
				t.Fatalf("border pixel=%d want%d", got, tc.want)
			}
			for y := 0; y < 20; y++ {
				for x := 0; x < 20; x++ {
					if x >= 3 && y >= 3 && x < 3+int(tc.w) && y < 3+int(tc.h) {
						continue
					}
					if got := shot.Indexed[y*20+x]; got != 0 {
						t.Fatalf("outside pixel %d,%d=%d", x, y, got)
					}
				}
			}
		})
	}
}
