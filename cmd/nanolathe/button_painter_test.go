package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/ui"
)

func painterButton(gad gui.Gadget) *ui.Panel {
	return ui.NewPanel(&gui.Window{Rect: gui.Rect{W: 32, H: 24}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}, gad}})
}

// Edge colours are deliberately asserted by position, not by the ambiguous
// raised/sunken terminology: [07 R-FE-02 §4] supplies the caller table.
func TestArtlessButtonUsesRuntimeDownAndLowGreyBit(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		down, grey            int
		top, bottom, interior byte
	}{
		{"up", 0, 0, 17, 0, 20},
		{"down without host pointer", 1, 0, 0, 17, 20},
		{"grey", 1, 1, 0, 19, 19},
		{"upper grey bit", 0, 2, 17, 0, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gad := gui.Gadget{Kind: gui.KindButton, Active: 1, ButtonArtResolved: true, GrayedOut: int16(tc.grey), Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}}
			p := painterButton(gad)
			p.SetStatusAt(1, tc.down)
			g, c := &gameShell{}, bindingClient(t)
			c.SetUIStage(painterBindingStage(func(c *client.Client) { g.drawRetailButton(c, p, 1, gad, gad.Rect) }))
			snap := c.ComposeFrameSnapshot()
			pixel := func(x, y int) byte { return snap.Indexed[y*snap.Width+x] }
			if pixel(5, 2) != tc.top || pixel(5, 11) != tc.bottom || pixel(6, 6) != tc.interior {
				t.Fatalf("top/bottom/interior=%d/%d/%d, want %d/%d/%d", pixel(5, 2), pixel(5, 11), pixel(6, 6), tc.top, tc.bottom, tc.interior)
			}
			if pixel(1, 2) != 0 || pixel(12, 11) != 0 {
				t.Fatal("bevel escaped authored rectangle")
			}
		})
	}
}

func captionPainterFont() *formats.FNT {
	f := &formats.FNT{Height: 3}
	for _, code := range []byte{'A', 'B', 'C'} {
		f.Glyphs[code] = &formats.FNTGlyph{Width: 2, Height: 3, Bits: []byte{255}}
	}
	return f
}

func TestButtonCaptionKeyDecorationAndStationaryPressedPen(t *testing.T) {
	gad := gui.Gadget{Kind: gui.KindButton, Active: 1, Attribs: 2, QuickKey: 'b', Text: "ABC", Rect: gui.Rect{X: 2, Y: 2, W: 24, H: 12}}
	p := painterButton(gad)
	p.SetFlashRow(1, 9)
	g, c := &gameShell{font: captionPainterFont()}, bindingClient(t)
	c.SetFNT(g.font)
	c.SetUIStage(painterBindingStage(func(c *client.Client) { g.drawRetailTextState(c, p, 1, gad, gad.Rect) }))
	up := c.ComposeFrameSnapshot()
	// Inclusive right=25, text width=6: penX=11. Metric=3: penY=6.
	if got := up.Indexed[8*up.Width+13]; got != 2 {
		t.Fatalf("centred quickkey underline=%d, want GUI entry 2", got)
	}
	p.SetStatusAt(1, 1)
	down := c.ComposeFrameSnapshot()
	if !bytes.Equal(up.Indexed, down.Indexed) {
		t.Fatal("down word moved caption pen")
	}
	gad.GrayedOut = 1
	grey := c.ComposeFrameSnapshot()
	if got := grey.Indexed[8*grey.Width+13]; got != 9 {
		t.Fatalf("grey caption key=%d, want ordinary foreground without underline", got)
	}
	// The build branch keeps decoration when grey; it moves to bottom-4-metric.
	gad.Attribs, gad.Rect.H = 0x20, 16
	build := c.ComposeFrameSnapshot()
	if got := build.Indexed[10*build.Width+13]; got != 10 {
		t.Fatalf("grey build key=%d, want GUI entry 10", got)
	}
	if got := build.Indexed[10*build.Width+11]; got != 9 {
		t.Fatalf("build prefix=%d, want gadget foreground 9", got)
	}
	writeShellShot(t, c, os.Getenv("NANOLATHE_BUTTON_PAINTER_SHOT"))
}

func TestCaptionQuickKeyFirstTerminatedByteMatch(t *testing.T) {
	for _, tc := range []struct {
		text string
		key  byte
		want int
	}{{"aBA", 'A', 0}, {"AB\x00C", 'C', -1}, {"\xc0\xe0", 0xe0, 1}, {"A", 0, -1}} {
		if got := captionQuickKeyIndex(tc.text, tc.key); got != tc.want {
			t.Errorf("key %d in %q=%d, want %d", tc.key, tc.text, got, tc.want)
		}
	}
}
