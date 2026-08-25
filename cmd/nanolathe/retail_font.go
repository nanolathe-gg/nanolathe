package main

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
)

// retailGAFTextFont returns the primary frontend GAF font installed by
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// fallback [07 §4].
func (g *gameShell) retailGAFTextFont() *formats.GAFEntry {
	if g == nil || g.assets == nil || g.assets.gafFont == nil || len(g.assets.gafFont.Entries) == 0 {
		return nil
	}
	return &g.assets.gafFont.Entries[0]
}

func (g *gameShell) hasRetailTextFont() bool {
	return g.retailGAFTextFont() != nil || (g != nil && g.font != nil)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// frame, while every other byte directly indexes the font GAF frame table.
func retailGAFGlyph(font *formats.GAFEntry, code byte) *formats.GAFFrame {
	if font == nil || code < 0x20 || int(code) >= len(font.Frames) {
		return nil
	}
	return font.Frames[int(code)].Frame
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Frame width is the complete advance. Space advances through its frame but
// is deliberately not blitted by the drawer [07 §4].
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// primary GAF font uses the capital-I frame height plus two pixels [07 §4].
func retailGAFTextHeight(font *formats.GAFEntry) int {
	if frame := retailGAFGlyph(font, 'I'); frame != nil {
		return int(frame.Height) + 2
	}
	return 0
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// normalized offset from the supplied pen Y [07 §4].
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// glyph face and outline colors, are copied directly as PALETTE.PAL indices.
// No GUI semantic-color remap or FNT tint is applied. maxWidth < 0 means no
// width limit; otherwise the next glyph must fit in the remaining width.
func drawRetailGAFText(c *client.Client, font *formats.GAFEntry, text string, x, y, maxWidth int) {
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
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			c.UIBlit(frame,
				x-int(frame.XOffset),
				y-(int(frame.YOffset)-baselineHeight),
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
	if font := g.retailGAFTextFont(); font != nil {
		drawRetailGAFText(c, font, text, x, y, maxWidth)
		return
	}
	if g != nil && g.font != nil {
		c.UITextWidth(g.font, text, x, y, maxWidth, fntColor)
	}
}
