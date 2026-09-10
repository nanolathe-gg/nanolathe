package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
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
	if c == nil || c.messageFNT == nil {
		return
	}
	for i, line := range c.MessageLines() {
		if line.SpeakerSlot != 10 {
			// Real-speaker logo composition is not yet connected to the
			// committed player roster. Captions and chat carry sentinel 10;
			// leave unresolved logo art absent rather than inventing a colour.
			continue
		}
		y := 52 + i*int(c.messageFNT.Height)
		// Resolve the semantic colour before recording: both executors consume
		// physical palette indices [03 §4.3]. The run retains the primary
		// COMIX font, its line spacing and an unbounded width for deferred replay
		// (docs/DESIGN_GPU_RENDERER.md §2.2)[07 R-HUD-03 §14.4].
		c.emitGlyphs(drawlist.Glyphs{
			Font:  c.messageFNT,
			Text:  line.Text,
			X:     138,
			Y:     int32(y),
			Color: c.paletteIndex(line.LogicalColor()),
		})
	}
}
