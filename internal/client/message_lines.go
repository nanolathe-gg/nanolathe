package client

import (
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/frame"
)

// MessageRing returns the client's shared caption/chat ring [07 R-HUD-03
// §14]. Retail has one ring, fed by unit captions, chat and the game-speed
// announcement alike; callers outside this package (the battle shell's
// hotkeys and status line) use this accessor instead of holding a second
// instance, so every poster shares the same 30 entries and the same
// visited/jumped cursors.
func (c *Client) MessageRing() *frame.MessageRing {
	if c == nil {
		return nil
	}
	return &c.messages
}

// drawMessageLines is the master-composer message column. Unit captions use
// the no-speaker sentinel, so they draw directly at x=138 with dcb[15]; the
// same consumer also handles future chat and announcement lines [07 R-HUD-03
// §14.4]. Each line installs its own colour-map entry immediately before its
// text call — entry 10 for the record F3 last jumped to, entry 15 for every
// other line — so the highlight cannot leak onto a following line
// [07 R-CAM-01 §14].
func (c *Client) drawMessageLines() {
	if c == nil || c.fnt == nil {
		return
	}
	for i, line := range c.MessageLines() {
		if line.SpeakerSlot != 10 {
			// Real-speaker logo composition is not yet connected to the
			// committed player roster. Captions and chat carry sentinel 10;
			// leave unresolved logo art absent rather than inventing a colour.
			continue
		}
		y := 52 + i*int(c.fnt.Height)
		// Record then execute inline: the classic sink runs the same FNT
		// rasterizer with the same pen and max-width 0 (the zero value of
		// Glyphs.MaxWidth), exactly as the direct DrawText call did, so the
		// message column lands in per-frame order under the committed-frame list
		// (docs/DESIGN_GPU_RENDERER.md §2.2)[07 R-HUD-03 §14.4][03 §7.1].
		c.emitGlyphs(drawlist.Glyphs{
			Font:  c.fnt,
			Text:  line.Text,
			X:     138,
			Y:     int32(y),
			Color: line.LogicalColor(),
		})
	}
}
