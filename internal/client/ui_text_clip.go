package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// UITextWidthClipped records the usual FNT run with the private GUI surface
// boundary that constrains pixels after the retail width truncation.
func (c *Client) UITextWidthClipped(fnt *formats.FNT, text string, x, y, maxWidth int, color byte, clipX, clipY, clipW, clipH int) {
	if c == nil || c.fnt == nil || fnt == nil || clipW <= 0 || clipH <= 0 {
		return
	}
	c.emitGlyphs(drawlist.Glyphs{
		Font: fnt, Text: text, X: int32(x), Y: int32(y), Color: color, MaxWidth: int32(maxWidth),
		HasClip: true, Clip: drawlist.Rect{X: int32(clipX), Y: int32(clipY), W: int32(clipW), H: int32(clipH)},
	})
}
