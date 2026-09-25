package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// DrawMegamapSurface records the megamap overview: one indexed w×h surface at
// (x, y), copied into the frame's surface arena so a deferred or pipelined
// replay owns its bytes, exactly as the minimap packet does. Identity and
// revision let the modern executor keep the upload until the host recomposes
// it (DESIGN_INTERFACE_HUD_INPUT §3.15). Both renderers replay the same
// packet, so the view is identical in each.
func (c *Client) DrawMegamapSurface(pixels []byte, x, y, w, h int32, identity, revision uint64) {
	if c == nil || w <= 0 || h <= 0 || len(pixels) < int(w)*int(h) {
		return
	}
	n := int(w) * int(h)
	off := len(c.surfaceArena)
	c.surfaceArena = append(c.surfaceArena, pixels[:n]...)
	owned := c.surfaceArena[off:len(c.surfaceArena):len(c.surfaceArena)]
	c.emitSurface(drawlist.Surface{Pixels: owned, SrcW: w, SrcH: h,
		Dst: drawlist.Rect{X: x, Y: y, W: w, H: h}, Identity: identity, Revision: revision})
}
