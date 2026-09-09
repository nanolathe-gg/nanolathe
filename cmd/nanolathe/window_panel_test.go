package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"testing"
)

type windowPanelStage struct {
	shell *gameShell
	panel *ui.Panel
	modal bool
}

func (s windowPanelStage) DrawUI(c *client.Client, _ client.UIFrame) {
	if s.modal {
		s.shell.drawRetailModal(c)
	} else {
		s.shell.drawRetailWindow(c, modeMenuMain, s.panel)
	}
}

func panelTestBank(t *testing.T, name string, base byte, frames int) *formats.GAF {
	t.Helper()
	e := nineSliceEntry(t, frames, 4, 4)
	e.Name = name
	for i := range e.Frames {
		f := e.Frames[i].Frame
		f.XOffset = 19
		f.YOffset = -7
		for j := range f.Pixels {
			f.Pixels[j] = base + byte(i)
		}
	}
	return &formats.GAF{Entries: []formats.GAFEntry{*e}}
}

// These authored planes exercise the actual frontend callers. Tile arithmetic
// has a strict bottom overflow but an inclusive right edge [07 R-FE-02 §4].
func TestFrontendPanelUsesResolvedNineSliceAndChildClip(t *testing.T) {
	for _, size := range [][2]int{{8, 8}, {10, 9}, {2, 2}} {
		for _, route := range []string{"own", "common", "fallback", "modal"} {
			t.Run(fmt.Sprintf("%s-%dx%d", route, size[0], size[1]), func(t *testing.T) {
				own := panelTestBank(t, "PanelArt", 31, 9)
				common := panelTestBank(t, "PanelArt", 51, 9)
				base := byte(31)
				var page *formats.GAF = own
				if route == "common" {
					page = nil
					base = 51
				}
				if route == "fallback" {
					page = nil
					common.Entries[0].Name = "BackTile"
					base = 51
				}
				window := &gui.Window{Rect: gui.Rect{X: 3, Y: 3, W: int32(size[0]), H: int32(size[1])}, Header: gui.Header{Panel: "PanelArt"}}
				p := ui.NewPanel(window)
				asset := &retailPanelAssets{window: window, art: page}
				shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{common: common, panel: map[shellMode]*retailPanelAssets{modeMenuMain: asset}, message: asset}}
				if route == "modal" {
					shell.frontend.Panels.PushModal(p)
				}
				c, e := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 20, Height: 20})
				if e != nil {
					t.Fatal(e)
				}
				c.SetUIStage(windowPanelStage{shell: shell, panel: p, modal: route == "modal"})
				shot := c.ComposeFrameSnapshot()
				for y := 0; y < 20; y++ {
					for x := 0; x < 20; x++ {
						want := byte(0)
						rx, ry := x-3, y-3
						if rx >= 0 && ry >= 0 && rx < size[0] && ry < size[1] {
							switch size[0] {
							case 2:
								want = base + 2
							case 8:
								want = base
								if rx >= 4 {
									want += 2
								}
								if ry >= 4 {
									want += 3
								}
							case 10:
								want = base
								if rx >= 6 {
									want += 2
								} else if rx >= 4 {
									want++
								}
								if ry >= 5 {
									want += 6
								} else if ry >= 4 {
									want += 3
								}
							}
						}
						if got := shot.Indexed[y*20+x]; got != want {
							t.Fatalf("pixel %d,%d=%d want %d", x, y, got, want)
						}
					}
				}
			})
		}
	}
}

func TestFrontendPanelSingleFrameAndArtlessFill(t *testing.T) {
	for _, art := range []bool{true, false} {
		t.Run(fmt.Sprint(art), func(t *testing.T) {
			w := &gui.Window{Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 9}, Header: gui.Header{Panel: "single"}}
			p := ui.NewPanel(w)
			var bank *formats.GAF
			if art {
				bank = panelTestBank(t, "single", 41, 1)
			}
			shell := &gameShell{frontend: ui.NewFrontend(modeMenuMain), assets: &menuAssets{panel: map[shellMode]*retailPanelAssets{modeMenuMain: {window: w, art: bank}}}}
			c, e := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 20, Height: 20})
			if e != nil {
				t.Fatal(e)
			}
			c.SetUIStage(windowPanelStage{shell: shell, panel: p})
			shot := c.ComposeFrameSnapshot()
			points := [][3]int{{2, 2, 17}, {3, 3, 17}, {4, 4, 20}, {10, 5, 0}, {11, 10, 0}, {1, 2, 0}, {12, 5, 0}}
			if art {
				points = [][3]int{{2, 2, 41}, {5, 5, 41}, {6, 5, 0}, {2, 6, 0}, {11, 10, 0}}
			}
			for _, p := range points {
				if got := shot.Indexed[p[1]*20+p[0]]; got != byte(p[2]) {
					t.Fatalf("pixel %d,%d=%d want %d", p[0], p[1], got, p[2])
				}
			}
		})
	}
}
