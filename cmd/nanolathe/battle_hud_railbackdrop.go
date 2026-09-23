package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// drawRailBackdrop paints the side panel art under the rail. Retail stamps
// PANELSIDE at its final origin and leaves the band below its 480 rows at
// palette index 0 [07 §6][07 R-HUD-05], which is right while every rail
// control sits inside the art. When the host layout places controls below
// it — the Expanded sidebar, or an oversized page fitted to a tall surface —
// the art is stretched down the whole rail instead, so no control straddles
// the edge between panel art and black. Stretching keeps the art's vertical
// gradient continuous and its bottom border at the surface edge, where
// retail shows it at 640×480. Host presentation policy
// (DESIGN_INTERFACE_HUD_INPUT §3.3 "Rail backdrop").
func (h *retailBattleHUD) drawRailBackdrop(c *client.Client, b *battleSession, f *frame.Frame, screenH int) {
	side := h.panelSide
	if side == nil {
		return
	}
	gap, tall := hud.RailGap(int32(screenH), int32(side.Width), int32(side.Height))
	if tall && h.railLayoutBelowArt(b, f) {
		// Like a surface gadget, the resampled frame spans the destination
		// rectangle without its GAF offsets [07 §4].
		c.UIBlitFrameScaled(side, 0, 0, int(side.Width), screenH)
		return
	}
	blitBattlePanel(c, side, 0, 0)
	if tall {
		c.UIFillRect(int(gap.X1), int(gap.Y1), int(gap.X2-gap.X1+1), int(gap.Y2-gap.Y1+1), 0)
	}
}

// railLayoutBelowArt reports whether the rail's controls can reach below
// PANELSIDE's authored rows. The Expanded sidebar lays controls down the
// whole rail on every page, so it answers yes without a page, and the
// backdrop does not change with the selection. Otherwise the resolved page
// decides: stock pages end at row 480 [07 R-HUD-05], while an oversized page
// fitted to a tall surface may not. A page that fails to resolve reports its
// diagnostic from the side-page pass.
func (h *retailBattleHUD) railLayoutBelowArt(b *battleSession, f *frame.Frame) bool {
	if b == nil {
		return false
	}
	if b.expandedSidebarActive() {
		return true
	}
	window, _, err := h.windowForRequired(b, f)
	if err != nil || window == nil {
		return false
	}
	return railWindowReachesBelow(window, int32(h.panelSide.Height))
}

// railWindowReachesBelow reports whether any drawable gadget of the page ends
// below row bottom.
func railWindowReachesBelow(window *gui.Window, bottom int32) bool {
	for i, g := range window.Gadgets {
		if i == 0 || g.Active == 0 || g.Kind == gui.KindFont || g.Kind == gui.KindPanel {
			continue
		}
		if r := window.PlacedRect(i); r.Y+r.H > bottom {
			return true
		}
	}
	return false
}
