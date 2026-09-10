package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

type messageGlyphCollector struct {
	drawlist.Sink
	runs []drawlist.Glyphs
}

func (s *messageGlyphCollector) Glyphs(g drawlist.Glyphs) { s.runs = append(s.runs, g) }

// Established: each line maps its logical foreground independently, including
// an ordinary line following the F3 destination [07 R-HUD-03 §14.4][03 §4.3].
func TestMessageGlyphsResolvePaletteBeforeReplay(t *testing.T) {
	font := &formats.FNT{Height: 3}
	font.Glyphs['A'] = &formats.FNTGlyph{Width: 1, Height: 1, Bits: []byte{0x80}}
	pal := &palette.Tables{}
	pal.Logical[10], pal.Logical[15] = 91, 173
	// A second application of the logical map must also be distinguishable.
	pal.Logical[91], pal.Logical[173] = 22, 23
	c := &Client{width: 160, height: 80, indexed: make([]byte, 160*80),
		messageFNT: font, pal: pal, messages: *frame.NewMessageRing()}
	c.messages.Append("A", 1, 1, 10, 0)
	c.messages.Append("A", 1, 0, 10, 0)
	if _, ok := c.messages.NextUnvisitedSource(func(pool.Handle) bool { return true }); !ok {
		t.Fatal("F3 did not select the live-source line")
	}
	c.drawMessageLines()
	var recorded messageGlyphCollector
	c.list.Replay(&recorded)
	if len(recorded.runs) != 2 {
		t.Fatalf("recorded %d glyph runs, want both lines", len(recorded.runs))
	}
	c.list.Replay(c.classicSink())
	for i, want := range []byte{91, 173} {
		g := recorded.runs[i]
		if g.Color != want {
			t.Errorf("recorded line %d colour = %d, want physical index %d", i, g.Color, want)
		}
		if got := c.indexed[(52+i*int(font.Height))*c.width+138]; got != want {
			t.Errorf("rasterized line %d colour = %d, want physical index %d", i, got, want)
		}
	}
}

func TestMessageFontBindingPreservesSideFontDigits(t *testing.T) {
	primary, console := &formats.FNT{Height: 3}, &formats.FNT{Height: 7}
	c := &Client{messages: *frame.NewMessageRing()}
	c.SetFNT(console)
	c.SetMessageFNT(primary)
	c.messages.Append("A", 1, 0, 10, 0)
	c.drawGroupDigit(10, 20, 1)
	c.drawMessageLines()
	var recorded messageGlyphCollector
	c.list.Replay(&recorded)
	if len(recorded.runs) != 2 || recorded.runs[0].Font != console || recorded.runs[1].Font != primary {
		t.Fatalf("group digit and message lost independent font bindings: %+v", recorded.runs)
	}
	c.SetFNT(nil)
	if c.fnt != nil || c.messageFNT != nil {
		t.Fatal("clearing the shared font retained a battle font binding")
	}
}
