package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// The modal's private surface clips the post-art rectangle shader, whose
// signed -20 selects SHD row 12 [07 R-WGT-01 §3][03 R-COMP-02 §5].
func TestBattleModalGreyArtShadingAndInstalledGeometry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		attr   uint32
		stages uint8
		want   byte
	}{
		{"plain", 0, 0, 134}, {"staged", 0, 2, 132}, {"arrow", 0x800, 0, 131},
		{"checkbox", guiAttribCheckbox, 0, 34}, {"cycle", guiAttribCycle, 0, 35},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := widgetArtEntry("modal", 31, 32, 33, 34, 35)
			// The selected frame is intentionally wider than installed base geometry.
			for i := range entry.Frames {
				entry.Frames[i].Frame = &formats.GAFFrame{Width: 12, Height: 4, Compressed: 1, Pixels: make([]byte, 48), Transparent: make([]bool, 48)}
				for j := range entry.Frames[i].Frame.Pixels {
					entry.Frames[i].Frame.Pixels[j] = byte(31 + i)
				}
			}
			gad := gui.Gadget{Kind: gui.KindButton, Active: 1, GrayedOut: 1, Attribs: tc.attr, Stages: tc.stages, ButtonArtResolved: true, ButtonArt: &entry, Rect: gui.Rect{X: -2, Y: 2, W: 8, H: 4}}
			window := &gui.Window{Rect: gui.Rect{X: 8, Y: 4, W: 8, H: 12}, OriginX: 8, OriginY: 4, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, gad}}
			panel := ui.NewPanel(window)
			panel.SetStatusAt(1, 1)
			panel.SetStageAt(1, 1)
			pal := &palette.Tables{}
			for row := range pal.Shade {
				for index := range pal.Shade[row] {
					pal.Shade[row][index] = byte(index)
				}
			}
			for index := 0; index < 256; index++ {
				pal.Shade[12][index] = byte(index + 100)
			}
			h := &retailBattleHUD{pal: pal}
			c := bindingClient(t)
			c.SetUIStage(painterBindingStage(func(c *client.Client) {
				c.UIFillRect(0, 0, 32, 24, 7)
				h.drawGUIWindowState(c, window, nil, "", panel, nil)
			}))
			snap := c.ComposeFrameSnapshot()
			if dir := os.Getenv("NANOLATHE_MODAL_SHADE_CAPTURE"); dir != "" {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
				// Diagnostic grayscale displays the actual indexed output at integer zoom.
				img := image.NewGray(image.Rect(0, 0, 256, 192))
				for y := 0; y < 192; y++ {
					for x := 0; x < 256; x++ {
						img.Pix[y*img.Stride+x] = snap.Indexed[(y/8)*32+x/8]
					}
				}
				file, err := os.Create(filepath.Join(dir, tc.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(file, img); err != nil {
					file.Close()
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if got := h.modalGadgetRect(window, 1, nil); got != window.PlacedRect(1) {
				t.Fatalf("selected art changed installed rect: %+v", got)
			}
			if got := snap.Indexed[7*32+9]; got != tc.want {
				t.Fatalf("grey art pixel=%d want %d", got, tc.want)
			}
			if got := snap.Indexed[7*32+7]; got != 7 {
				t.Fatalf("shade escaped window: %d", got)
			}
			// Installed right edge is x=14. A larger selected frame may paint beyond
			// it, but the post-art rectangle shader uses installed dimensions.
			wantOutside := tc.want
			if tc.name != "checkbox" && tc.name != "cycle" {
				wantOutside -= 100
			}
			if got := snap.Indexed[7*32+14]; got != wantOutside {
				t.Fatalf("shade resized to selected art: %d want %d", got, wantOutside)
			}
		})
	}
}
