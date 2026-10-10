package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

type developerFooterStage struct{ b *battleSession }

func (s developerFooterStage) DrawUI(c *client.Client, _ client.UIFrame) {
	c.BeginChromeRegion(s.b.stripRegion())
	s.b.hud.drawFooter(c, s.b, &frame.Frame{Tick: 30})
	c.EndChromeRegion()
}

type developerFooterTrace struct {
	overlayTrace
	text []drawlist.Glyphs
}

func (s *developerFooterTrace) Glyphs(g drawlist.Glyphs) { s.text = append(s.text, g) }

// Film information replaces the ordinary footer inside the bottom strip's
// active drawing region. Its rows must use that region's virtual height,
// including a framebuffer height with a remainder (Modern UI scale).
func TestDeveloperFooterUsesChromeHeight(t *testing.T) {
	for _, size := range [][3]int{{640, 480, 1}, {2560, 1440, 2}, {2561, 1441, 2}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
			t.Cleanup(b.cl.Close)
			font := &formats.FNT{Height: 8}
			for ch := 32; ch < 127; ch++ {
				font.Glyphs[ch] = &formats.FNTGlyph{Width: 1, Height: 8, Bits: []byte{128, 128, 128, 128, 128, 128, 128, 128}}
			}
			b.hud = &retailBattleHUD{primaryFont: font}
			b.cl.Resize(size[0], size[1])
			b.cl.SetFNT(font)
			b.cl.SetEnhanced(true)
			prefs := settings.DefaultPresentation()
			prefs.UIScale = size[2]
			b.hostPresentation = &prefs
			b.resolveChromeScale()
			b.dispatchLocalCommand("+dev")
			b.handleDeveloperShortcuts(developerFunction(input.KeyF11, false), b.cl)
			b.handleDeveloperShortcuts(developerToken('i'), b.cl)
			if !b.developer.film || !b.developer.information {
				t.Fatal("developer information did not open")
			}
			b.cl.SetUIStage(developerFooterStage{b})
			trace := &developerFooterTrace{}
			b.cl.RecordModernFrame().Replay(trace)
			if len(trace.text) != 6 {
				t.Fatalf("footer recorded %d fields, want 6", len(trace.text))
			}
			r := b.stripRegion()
			_, virtualHeight := r.VirtualSize(size[0], size[1])
			lower := int32(virtualHeight - int(font.Height) - 1)
			for _, g := range trace.text {
				wantY := lower
				if g.Text == "UNITS 0\\-" || g.Text == "PACKETS: - - -" || g.X == 130 && g.Text != "PFSTATE -, PFABLE -" {
					wantY -= 16
				}
				if g.Y != wantY {
					t.Fatalf("%q row = %d, want %d within virtual height %d", g.Text, g.Y, wantY, virtualHeight)
				}
				bottom := (g.Y+int32(font.Height))*r.Scale + r.OffsetY
				if bottom >= int32(size[1]) || bottom < int32(size[1])-64 {
					t.Fatalf("%q draws outside bottom strip at %d", g.Text, bottom)
				}
			}
		})
	}
}
