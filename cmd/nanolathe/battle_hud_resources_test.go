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

// uiTextStage records one text run and nothing else, through the same
// Client.UIText the HUD's own painters call.
type uiTextStage struct {
	fnt   *formats.FNT
	text  string
	x, y  int
	color byte
}

func (s uiTextStage) DrawUI(c *client.Client, _ client.UIFrame) {
	c.UIText(s.fnt, s.text, s.x, s.y, s.color)
}

// rasterizeUIText composes a 640x480 indexed framebuffer holding only the given
// text run. The expected pixels come from the production recorder and sink, so
// a HUD row is compared against the same rasterizer that painted it rather than
// against a second entry point into it.
func rasterizeUIText(t *testing.T, fnt *formats.FNT, text string, x, y int, color byte) []byte {
	t.Helper()
	buf := frame.NewBuffer()
	buf.BeginWrite()
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: buf})
	if err != nil {
		t.Fatal(err)
	}
	c.SetFNT(fnt)
	c.SetUIStage(uiTextStage{fnt: fnt, text: text, x: x, y: y, color: color})
	c.BeginPresentationFrame()
	return c.ComposeFrameSnapshot().Indexed
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
	}{{10, 11, 5}, {15, 2, 6}} {
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
		want := rasterizeUIText(t, font, tc.text, 10, tc.y, h.guiColor(15))
		if !bytes.Equal(shot.Indexed[tc.y*640:(tc.y+1)*640], want[tc.y*640:(tc.y+1)*640]) {
			t.Fatalf("number row %d is not displayed stock %s", tc.y, tc.text)
		}
	}
}

// Rate pixels follow the strict saved deadline, not settlement publication or
// a first-draw sample. Recomposition cannot refresh them [05 R-ECO-01 §1, §6].
func TestResourcePainterUsesLatchedRatesAtDeadlineBoundary(t *testing.T) {
	buf := frame.NewBuffer()
	c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: buf})
	if err != nil {
		t.Fatal(err)
	}
	font := &formats.FNT{Height: 1}
	for ch := byte(32); ch < 127; ch++ {
		font.Glyphs[ch] = &formats.FNTGlyph{Width: 4, Height: 1, Bits: []byte{(ch%15 + 1) << 4}}
	}
	h := &retailBattleHUD{side: &content.SideDef{}, console: font}
	c.SetFNT(font)
	anchors := []int{hud.AnchorEnergyProduced, hud.AnchorEnergyConsumed, hud.AnchorMetalProduced, hud.AnchorMetalConsumed}
	for row, anchor := range anchors {
		h.anchors[anchor] = hud.Rect{X1: 20, Y1: int32(20 + row*5)}
	}
	c.SetUIStage(displayedResourceStage{h})
	for _, step := range []struct {
		tick       uint32
		live, want float32
	}{{120, 10, 0}, {121, 10, 10}, {150, 20, 10}, {151, 20, 20}} {
		f := buf.BeginWrite()
		f.Economy = append(f.Economy, frame.EconomyView{
			Player: 0, DisplayTimer: 120,
			EnergyProduced: step.live, EnergyConsumed: step.live,
			MetalProduced: step.live, MetalConsumed: step.live,
		})
		if err := buf.Publish(step.tick); err != nil {
			t.Fatal(err)
		}
		c.BeginPresentationFrame()
		for range 2 {
			shot := c.ComposeFrameSnapshot()
			for row, text := range []string{
				hud.FormatEnergyProduced(step.want), hud.FormatEnergyConsumed(step.want),
				hud.FormatMetalProduced(step.want), hud.FormatMetalConsumed(step.want),
			} {
				y := 20 + row*5
				color := h.guiColor(10)
				if row%2 == 1 {
					color = h.guiColor(12)
				}
				want := rasterizeUIText(t, font, text, 20, y, color)
				if !bytes.Equal(shot.Indexed[y*640:(y+1)*640], want[y*640:(y+1)*640]) {
					t.Fatalf("tick %d rate row %d does not show latched %s", step.tick, row, text)
				}
			}
		}
	}
}

// resourceBarStage paints one bar with a chosen stock and capacity so the
// fill arithmetic can be pinned without going through the easing step.
type resourceBarStage struct {
	h               *retailBattleHUD
	r               hud.Rect
	stock, capacity float32
	ink             byte
}

func (s resourceBarStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.h.drawResourceBar(c, s.r, s.stock, s.capacity, s.ink)
}

// The top-strip bar fill is inclusive on both spans [07 R-HUD-03 §4]: with
// fill = ftol(x1 + w*S/C) the painted rectangle is [x1..fill] x [y1..y2]
// through the inclusive rectangle filler [03 R-P0-19-P]. So a bar covers
// ftol(w*S/C)+1 columns and y2-y1+1 rows, a zero stock still paints the single
// column at x1, and a full stock paints w+1 columns. Capacity at or below zero
// paints nothing, because retail's fill sits inside the `C > 0` branch.
func TestResourceBarFillIsInclusive(t *testing.T) {
	const w = 100
	bar := hud.Rect{X1: 10, Y1: 10, X2: 10 + w, Y2: 12}
	for _, tc := range []struct {
		name            string
		stock, capacity float32
		columns, rows   int
	}{
		// ftol(10 + 100*0/1000) = 10: the x1 column alone.
		{"zero stock paints the x1 column", 0, 1000, 1, 3},
		// ftol(10 + 100*1000/1000) = 110 = x2: w+1 columns.
		{"full stock paints w+1 columns", 1000, 1000, w + 1, 3},
		// 100*333/1000 = 33.3 -> ftol 43, so 43-10+1 = 34 columns.
		{"intermediate stock truncates toward zero", 333, 1000, 34, 3},
		// 100*125/1000 = 12.5 -> ftol 22, so 13 columns.
		{"half-column stock keeps the truncated column", 125, 1000, 13, 3},
		// The fill lives inside the C > 0 branch.
		{"zero capacity paints nothing", 500, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := frame.NewBuffer()
			buf.BeginWrite()
			if err := buf.Publish(1); err != nil {
				t.Fatal(err)
			}
			c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: buf})
			if err != nil {
				t.Fatal(err)
			}
			h := &retailBattleHUD{side: &content.SideDef{}}
			c.SetUIStage(resourceBarStage{h: h, r: bar, stock: tc.stock, capacity: tc.capacity, ink: 7})
			c.BeginPresentationFrame()
			shot := c.ComposeFrameSnapshot()
			for y := 9; y <= 13; y++ {
				painted := 0
				for x := 9; x <= 10+w+1; x++ {
					if shot.Indexed[y*640+x] == 7 {
						painted++
					}
				}
				want := 0
				if y >= 10 && y < 10+tc.rows {
					want = tc.columns
				}
				if painted != want {
					t.Fatalf("row %d painted %d columns, want %d", y, painted, want)
				}
			}
			if tc.columns > 0 && shot.Indexed[10*640+10] != 7 {
				t.Fatalf("the column at x1 is not painted")
			}
		})
	}
}

// shareMarkerStage paints one bar's share marker with a chosen threshold,
// live stock and capacity so the gate and the arithmetic can be pinned
// without going through the easing step.
type shareMarkerStage struct {
	h                         *retailBattleHUD
	r                         hud.Rect
	threshold, live, capacity float32
}

func (s shareMarkerStage) DrawUI(c *client.Client, _ client.UIFrame) {
	s.h.drawShareMarker(c, s.r, s.threshold, s.live, s.capacity)
}

// The share marker is three columns wide at m = ftol(x1 + w*T/C) and is drawn
// only while 0 < T < live stock, both bounds strict [07 R-HUD-03 §4].
func TestShareMarkerGateAndColumns(t *testing.T) {
	const w = 100
	bar := hud.Rect{X1: 10, Y1: 10, X2: 10 + w, Y2: 12}
	for _, tc := range []struct {
		name                      string
		threshold, live, capacity float32
		first, columns            int
	}{
		{"zero threshold draws nothing", 0, 800, 1000, 0, 0},
		{"negative threshold draws nothing", -50, 800, 1000, 0, 0},
		// The upper bound is strict, so a threshold that has reached the live
		// stock draws nothing.
		{"threshold equal to live stock draws nothing", 500, 500, 1000, 0, 0},
		{"threshold above live stock draws nothing", 600, 500, 1000, 0, 0},
		// ftol(10 + 100*200/1000) = 30: columns 30, 31 and 32.
		{"threshold below live stock marks three columns", 200, 800, 1000, 30, 3},
		// 100*333/1000 = 33.3 -> ftol 43, so the marker starts at 43.
		{"the marker position truncates toward zero", 333, 800, 1000, 43, 3},
		// The marker lives inside the C > 0 branch.
		{"zero capacity draws nothing", 200, 800, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := frame.NewBuffer()
			buf.BeginWrite()
			if err := buf.Publish(1); err != nil {
				t.Fatal(err)
			}
			c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: buf})
			if err != nil {
				t.Fatal(err)
			}
			h := &retailBattleHUD{side: &content.SideDef{}}
			c.SetUIStage(shareMarkerStage{h: h, r: bar, threshold: tc.threshold, live: tc.live, capacity: tc.capacity})
			c.BeginPresentationFrame()
			shot := c.ComposeFrameSnapshot()
			ink := h.guiColor(12)
			for y := 9; y <= 13; y++ {
				painted, firstX := 0, 0
				for x := 0; x < 640; x++ {
					if shot.Indexed[y*640+x] == ink {
						if painted == 0 {
							firstX = x
						}
						painted++
					}
				}
				want, wantFirst := 0, 0
				if y >= 10 && y <= 12 {
					want, wantFirst = tc.columns, tc.first
				}
				if painted != want {
					t.Fatalf("row %d painted %d marker columns, want %d", y, painted, want)
				}
				if painted > 0 && firstX != wantFirst {
					t.Fatalf("row %d marker starts at %d, want %d", y, firstX, wantFirst)
				}
			}
		})
	}
}

// The fill keys on the eased displayed stock and the marker on the live stock
// [07 R-HUD-03 §4]. With the displayed pair still easing from zero the two
// disagree, and a marker above the displayed stock must still be drawn.
func TestShareMarkerKeysOnLiveStockNotDisplayed(t *testing.T) {
	buf := frame.NewBuffer()
	f := buf.BeginWrite()
	f.Economy = append(f.Economy, frame.EconomyView{
		Player: 0, Energy: 800, Metal: 800,
		EnergyCapacity: 1000, MetalCapacity: 1000,
		// Above the first eased displayed value of 100, below the live 800.
		EnergyShareThreshold: 200,
		// Above the live stock: no marker at all on the metal bar.
		MetalShareThreshold: 900,
	})
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := client.New(client.Options{Width: 640, Height: 480, Buffer: buf})
	if err != nil {
		t.Fatal(err)
	}
	h := &retailBattleHUD{side: &content.SideDef{EnergyColor: 5, MetalColor: 6}}
	h.anchors[hud.AnchorEnergyBar] = hud.Rect{X1: 10, Y1: 10, X2: 110, Y2: 11}
	h.anchors[hud.AnchorMetalBar] = hud.Rect{X1: 10, Y1: 15, X2: 110, Y2: 16}
	c.SetUIStage(displayedResourceStage{h})
	c.BeginPresentationFrame()
	shot := c.ComposeFrameSnapshot()
	ink := h.guiColor(12)
	// The energy fill ends at ftol(10 + 100*100/1000) = 20 from the eased 100,
	// while the marker sits at ftol(10 + 100*200/1000) = 30 from the live 800.
	for x := 10; x <= 20; x++ {
		if got := shot.Indexed[10*640+x]; got != 5 {
			t.Fatalf("energy fill column %d = %d, want the eased fill %d", x, got, 5)
		}
	}
	for x := 21; x < 30; x++ {
		if got := shot.Indexed[10*640+x]; got != 0 {
			t.Fatalf("energy column %d = %d, want nothing between the fill and the marker", x, got)
		}
	}
	for x := 30; x <= 32; x++ {
		if got := shot.Indexed[10*640+x]; got != ink {
			t.Fatalf("energy marker column %d = %d, want dcb[12] = %d", x, got, ink)
		}
	}
	if got := shot.Indexed[10*640+33]; got != 0 {
		t.Fatalf("the energy marker is wider than three columns")
	}
	// The metal threshold is above the live stock, so no marker is drawn even
	// though it is far above the eased displayed stock.
	for x := 0; x < 640; x++ {
		if got := shot.Indexed[15*640+x]; got == ink {
			t.Fatalf("metal marker painted at %d with a threshold above the live stock", x)
		}
	}
}
