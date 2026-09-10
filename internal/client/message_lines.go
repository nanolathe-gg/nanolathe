package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// SetMessageLogos binds the same loaded player-logo art the battle HUD uses.
// The frame selector comes from the committed player's Logo byte, never its
// slot number or side [07 R-HUD-03 §14.4][07 R-HUD-04 §4][I6].
func (c *Client) SetMessageLogos(logos *formats.GAF) {
	if c == nil {
		return
	}
	c.messageLogos = nil
	if logos != nil {
		c.messageLogos, _ = logos.Find("32xlogos")
	}
}

func (c *Client) messageLogo(speaker uint8) *formats.GAFFrame {
	if c.buffer == nil || c.messageLogos == nil || speaker >= frame.PlayerRowSlots {
		return nil
	}
	cur := c.buffer.Current()
	if cur == nil || !cur.Players[speaker].Present {
		return nil
	}
	logo := int(cur.Players[speaker].Logo)
	if logo >= len(c.messageLogos.Frames) {
		return nil
	}
	return c.messageLogos.Frames[logo].Frame
}

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
// same consumer also handles chat and announcement lines [07 R-HUD-03
// §14.4]. Each line installs its own colour-map entry immediately before its
// text call — entry 10 for the record F3 last jumped to, entry 15 for every
// other line — so the highlight cannot leak onto a following line
// [07 R-CAM-01 §14].
func (c *Client) drawMessageLines() {
	if c == nil || c.messageFNT == nil {
		return
	}
	for i, line := range c.MessageLines() {
		y := 52 + i*int(c.messageFNT.Height)
		x := 138
		if line.SpeakerSlot < frame.PlayerRowSlots {
			// The rectangle includes both endpoints. The logo height and text
			// offset each truncate their complete double expression [07
			// R-HUD-03 §14.4]. Missing art must not change the text geometry.
			a := int(float64(c.messageFNT.Height) * 0.8)
			c.UIBlitFrameScaled(c.messageLogo(line.SpeakerSlot), 138, y, a+1, a+1)
			x = int(138.0 + 1.5*float64(a))
		}
		// Resolve the semantic colour before recording: both executors consume
		// physical palette indices [03 §4.3]. The run retains the primary
		// COMIX font, its line spacing and an unbounded width for deferred replay
		// (docs/DESIGN_GPU_RENDERER.md §2.2)[07 R-HUD-03 §14.4].
		c.emitGlyphs(drawlist.Glyphs{
			Font:  c.messageFNT,
			Text:  line.Text,
			X:     int32(x),
			Y:     int32(y),
			Color: c.paletteIndex(line.LogicalColor()),
		})
	}
}
