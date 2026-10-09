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
	cur := c.committedFrame()
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
// the no-speaker sentinel, so they draw directly at x=138; the same consumer
// also handles chat and announcement lines [07 R-HUD-03 §14.4].
//
// The composer selects the primary COMIX FNT, and its glyph height spaces the
// lines and sizes the logo, but the text itself goes through the GAF-font pen
// with no width limit and mode 0. With the window's GAF slot holding
// hattfont12, as it does in battle, the glyph bytes are copied as authored —
// the outlined face — and the per-line foreground is never read. Only a null
// slot reaches the FNT drawer, where each line installs its own colour-map
// entry immediately before its text call — entry 10 for the record F3 last
// jumped to, entry 15 for every other line — so the highlight cannot leak
// onto a following line [03 R-FONT-01 §6][07 R-CAM-01 §14].
func (c *Client) drawMessageLines() {
	if c == nil || c.messageFNT == nil {
		return
	}
	// Ten columns right of the rail and twenty rows below the top strip:
	// (138, 52) beside retail's chrome.
	left, top, _ := c.cam.ChromeInset()
	for i, line := range c.MessageLines() {
		y := int(top) + 20 + i*int(c.messageFNT.Height)
		x := int(left) + 10
		if line.SpeakerSlot < frame.PlayerRowSlots {
			// The rectangle includes both endpoints. The logo height and text
			// offset each truncate their complete double expression [07
			// R-HUD-03 §14.4]. Missing art must not change the text geometry.
			a := int(float64(c.messageFNT.Height) * 0.8)
			c.UIBlitFrameScaled(c.messageLogo(line.SpeakerSlot), x, y, a+1, a+1)
			x = int(float64(x) + 1.5*float64(a))
		}
		if c.messageGAF != nil {
			c.drawMessageGAFText(line.Text, x, y)
			continue
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

// drawMessageGAFText is the GAF-font pen with no width limit and mode 0.
// Control bytes and bytes without a frame neither draw nor advance; a space
// advances without drawing; every other glyph is blitted with its frame
// XOffset and its capital-I-normalized YOffset subtracted from the pen, then
// advances by its frame width [03 R-FONT-01 §6][07 §4].
func (c *Client) drawMessageGAFText(text string, x, y int) {
	glyph := func(code byte) *formats.GAFFrame {
		if code < 0x20 || int(code) >= len(c.messageGAF.Frames) {
			return nil
		}
		return c.messageGAF.Frames[code].Frame
	}
	baseline := 0
	if f := glyph('I'); f != nil {
		baseline = int(f.Height)
	}
	for i := 0; i < len(text) && text[i] != 0; i++ {
		f := glyph(text[i])
		if f == nil {
			continue
		}
		if text[i] != ' ' {
			c.UIBlit(f, x-int(f.XOffset), y-(int(f.YOffset)-baseline))
		}
		x += int(f.Width)
	}
}
