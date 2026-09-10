package main

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

type displayedResourceStage struct{ h *retailBattleHUD }

func (s displayedResourceStage) DrawUI(c *client.Client, f client.UIFrame) {
	s.h.drawResources(c, f.Committed, f.Resources)
}

// The bars and current numbers share the eased pair; live stocks remain solely
// the step target, and capacities stay live [07 R-HUD-03 §4].
func TestResourcePainterUsesDisplayedPair(t *testing.T) {
	buf := frame.NewBuffer()
	f := buf.BeginWrite()
	f.Economy = append(f.Economy, frame.EconomyView{Player: 0, Energy: 800, Metal: 80, EnergyCapacity: 1000, MetalCapacity: 1000})
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: buf})
	if err != nil {
		t.Fatal(err)
	}
	font := &formats.FNT{Height: 1}
	for ch := byte('0'); ch <= '9'; ch++ {
		font.Glyphs[ch] = &formats.FNTGlyph{Width: 4, Height: 1, Bits: []byte{(ch - '0' + 1) << 4}}
	}
	h := &retailBattleHUD{side: &content.SideDef{EnergyColor: 5, MetalColor: 6}, console: font}
	h.anchors[hud.AnchorEnergyBar] = hud.Rect{X1: 10, Y1: 10, X2: 110, Y2: 11}
	h.anchors[hud.AnchorMetalBar] = hud.Rect{X1: 10, Y1: 15, X2: 110, Y2: 16}
	h.anchors[hud.AnchorEnergyNum] = hud.Rect{X1: 10, Y1: 20}
	h.anchors[hud.AnchorMetalNum] = hud.Rect{X1: 10, Y1: 25}
	c.SetFNT(font)
	c.SetUIStage(displayedResourceStage{h})
	c.BeginPresentationFrame()
	shot := c.ComposeFrameSnapshot()
	for _, tc := range []struct {
		y, width int
		ink      byte
	}{{10, 10, 5}, {15, 1, 6}} {
		for x := 10; x < 110; x++ {
			want := byte(0)
			if x < 10+tc.width {
				want = tc.ink
			}
			if got := shot.Indexed[tc.y*640+x]; got != want {
				t.Fatalf("bar (%d,%d) = %d, want %d", x, tc.y, got, want)
			}
		}
	}
	for _, tc := range []struct {
		y    int
		text string
	}{{20, "100"}, {25, "10"}} {
		want := make([]byte, 640*480)
		client.DrawText(want, 640, 480, font, tc.text, 10, tc.y, 0, h.guiColor(15))
		if !bytes.Equal(shot.Indexed[tc.y*640:(tc.y+1)*640], want[tc.y*640:(tc.y+1)*640]) {
			t.Fatalf("number row %d is not displayed stock %s", tc.y, tc.text)
		}
	}
}
