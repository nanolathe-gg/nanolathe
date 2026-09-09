package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

// retailGAFTextFont returns the primary frontend GAF-font slot. Frontend text
// prefers this slot and uses the active FNT only when the slot is null
// [07 §4].
func (g *gameShell) retailGAFTextFont() *formats.GAFEntry {
	if g == nil || g.assets == nil || g.assets.gafFont == nil || len(g.assets.gafFont.Entries) == 0 {
		return nil
	}
	return &g.assets.gafFont.Entries[0]
}

func (g *gameShell) hasRetailTextFont() bool {
	return g.retailGAFTextFont() != nil || (g != nil && g.font != nil)
}

// retailGAFGlyph maps bytes at or above space directly to the font GAF frame
// table; control bytes have no frame [07 §4].
func retailGAFGlyph(font *formats.GAFEntry, code byte) *formats.GAFFrame {
	if font == nil || code < 0x20 || int(code) >= len(font.Frames) {
		return nil
	}
	return font.Frames[int(code)].Frame
}

// retailGAFTextWidth sums each glyph frame's complete width. Space advances
// through its frame but is not blitted [07 §4].
func retailGAFTextWidth(font *formats.GAFEntry, text string) int {
	width := 0
	for i := 0; i < len(text); i++ {
		if text[i] == 0 {
			break
		}
		if frame := retailGAFGlyph(font, text[i]); frame != nil {
			width += int(frame.Width)
		}
	}
	return width
}

// retailGAFTextHeight is the widget layout metric: the primary GAF font uses
// the capital-I frame height plus two pixels [07 §4].
func retailGAFTextHeight(font *formats.GAFEntry) int {
	if frame := retailGAFGlyph(font, 'I'); frame != nil {
		return int(frame.Height) + 2
	}
	return 0
}

// retailGAFBaselineHeight is the capital-I height subtracted from every
// loaded GAF-font frame's runtime YOffset. The glyph blitter then subtracts
// that normalized offset from the supplied pen Y [07 §4].
func retailGAFBaselineHeight(font *formats.GAFEntry) int {
	if frame := retailGAFGlyph(font, 'I'); frame != nil {
		return int(frame.Height)
	}
	return 0
}

func (g *gameShell) retailTextWidth(text string) int {
	if font := g.retailGAFTextFont(); font != nil {
		return retailGAFTextWidth(font, text)
	}
	return client.MeasureText(g.font, text)
}

func (g *gameShell) retailTextHeight() int {
	if font := g.retailGAFTextFont(); font != nil {
		if height := retailGAFTextHeight(font); height != 0 {
			return height
		}
	}
	if g != nil && g.font != nil && g.font.Height != 0 {
		return int(g.font.Height)
	}
	return 11
}

// drawRetailGAFText copies opaque GAF glyph pixels, including face and outline
// colors, directly as PALETTE.PAL indices.
// No GUI semantic-color remap or FNT tint is applied. maxWidth < 0 means no
// width limit; otherwise the next glyph must fit in the remaining width.
func drawRetailGAFText(c *client.Client, font *formats.GAFEntry, text string, x, y, maxWidth int) {
	drawRetailGAFTextLit(c, font, text, x, y, maxWidth, nil, 0)
}

// drawRetailGAFTextClipped draws the same opaque glyphs into the modal
// window's private surface, clipping glyph bearings and text beyond the
// authored window [07 §4].
func drawRetailGAFTextClipped(c *client.Client, font *formats.GAFEntry, text string, x, y, maxWidth, clipX, clipY, clipW, clipH int) {
	if c == nil || font == nil {
		return
	}
	baselineHeight := retailGAFBaselineHeight(font)
	remaining := maxWidth
	for i := 0; i < len(text); i++ {
		code := text[i]
		if code == 0 {
			return
		}
		frame := retailGAFGlyph(font, code)
		if frame == nil {
			continue
		}
		advance := int(frame.Width)
		if remaining >= 0 && remaining < advance {
			return
		}
		if code != 0x20 {
			c.UIBlitClipped(frame,
				x-int(frame.XOffset),
				y-(int(frame.YOffset)-baselineHeight),
				clipX, clipY, clipW, clipH,
			)
		}
		x += advance
		if remaining >= 0 {
			remaining -= advance
		}
	}
}

// drawRetailGAFTextLit draws each glyph normally at shade level zero; a
// non-zero level remaps it through PALETTE.LHT. Only loading-screen stage
// labels pass a non-zero level [07 §4][03 §4.3.1].
func drawRetailGAFTextLit(c *client.Client, font *formats.GAFEntry, text string, x, y, maxWidth int, pal *palette.Tables, shade int) {
	if c == nil || font == nil {
		return
	}
	baselineHeight := retailGAFBaselineHeight(font)
	remaining := maxWidth
	for i := 0; i < len(text); i++ {
		code := text[i]
		if code == 0 {
			return
		}
		frame := retailGAFGlyph(font, code)
		if frame == nil {
			continue
		}
		advance := int(frame.Width)
		if remaining >= 0 && remaining < advance {
			return
		}
		if code != 0x20 {
			// Font loading normalizes each frame's YOffset by the capital-I
			// height. Glyph placement subtracts that normalized value and the
			// frame XOffset from the text pen; ordinary GUI art uses a different
			// placement contract [07 §4].
			c.UIBlitLit(frame,
				x-int(frame.XOffset),
				y-(int(frame.YOffset)-baselineHeight),
				pal, shade,
			)
		}
		x += advance
		if remaining >= 0 {
			remaining -= advance
			if remaining < 0 {
				return
			}
		}
	}
}

func (g *gameShell) drawRetailString(c *client.Client, text string, x, y, maxWidth int, fntColor byte) {
	g.drawRetailStringLit(c, text, x, y, maxWidth, fntColor, 0)
}

func (g *gameShell) drawRetailStringLit(c *client.Client, text string, x, y, maxWidth int, fntColor byte, shade int) {
	if font := g.retailGAFTextFont(); font != nil {
		var pal *palette.Tables
		if g.assets != nil {
			pal = g.assets.pal
		}
		drawRetailGAFTextLit(c, font, text, x, y, maxWidth, pal, shade)
		return
	}
	if g != nil && g.font != nil {
		c.UITextWidth(g.font, text, x, y, maxWidth, fntColor)
	}
}
