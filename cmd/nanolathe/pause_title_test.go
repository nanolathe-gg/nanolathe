package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
)

type pausedTitleStage struct{ hud *retailBattleHUD }

func (s pausedTitleStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.hud.drawPausedTitle(c)
}

// The title hotspot is the centre of the view to the right of the rail, and
// its signed offsets are independent of its dimensions [07 R-HUD-05].
func TestPausedTitleUsesViewCentreAndAuthoredOffsets(t *testing.T) {
	for _, size := range [][2]int{{640, 480}, {800, 600}, {1024, 768}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			f := &formats.GAFFrame{
				Width: 7, Height: 3, XOffset: 2, YOffset: -4,
				Pixels: make([]byte, 21), Transparent: make([]bool, 21),
			}
			for i := range f.Pixels {
				f.Pixels[i] = byte(i + 31)
			}
			c, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: size[0], Height: size[1]})
			if err != nil {
				t.Fatal(err)
			}
			c.SetUIStage(pausedTitleStage{hud: &retailBattleHUD{pausedFrame: f}})
			shot := c.ComposeFrameSnapshot()
			left, top := (size[0]+128)/2-2, size[1]/2+4
			for y := 0; y < size[1]; y++ {
				for x := 0; x < size[0]; x++ {
					want := byte(0)
					if x >= left && x < left+7 && y >= top && y < top+3 {
						want = f.Pixels[(y-top)*7+x-left]
					}
					if got := shot.Indexed[y*size[0]+x]; got != want {
						t.Fatalf("pixel (%d,%d) = %d, want %d; title origin (%d,%d)", x, y, got, want, left, top)
					}
				}
			}
		})
	}
}
