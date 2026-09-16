package main

// The resource readouts at the top of the screen: the two bars and the
// numeric and text pens the side anchors place [07 §6].

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func (h *retailBattleHUD) drawResources(c *client.Client, f *frame.Frame, displayed client.DisplayedResources) {
	var res *frame.EconomyView
	for i := range f.Economy {
		if f.Economy[i].Player == f.ViewingPlayer {
			res = &f.Economy[i]
			break
		}
	}
	if res == nil {
		return
	}
	// The host presentation boundary owns the saved-deadline rate latch.
	// Drawing or speculative recording only consumes its value [05 R-ECO-01 §6].
	rates := &displayed
	energy := h.side.EnergyColor
	metal := h.side.MetalColor
	if energy < 0 || energy > 255 {
		energy = 0
	}
	if metal < 0 || metal > 255 {
		metal = 0
	}
	energyBar, _ := h.anchors.ByIndex(hud.AnchorEnergyBar)
	metalBar, _ := h.anchors.ByIndex(hud.AnchorMetalBar)
	h.drawResourceBar(c, energyBar, displayed.Energy, res.EnergyCapacity, byte(energy))
	h.drawShareMarker(c, energyBar, res.EnergyShareThreshold, res.Energy, res.EnergyCapacity)
	h.drawResourceBar(c, metalBar, displayed.Metal, res.MetalCapacity, byte(metal))
	h.drawShareMarker(c, metalBar, res.MetalShareThreshold, res.Metal, res.MetalCapacity)
	h.drawNumber(c, hud.AnchorEnergyNum, displayed.Energy)
	h.drawNumber(c, hud.AnchorMetalNum, displayed.Metal)
	h.drawNumberRight(c, hud.AnchorEnergyMax, res.EnergyCapacity)
	h.drawNumberRight(c, hud.AnchorMetalMax, res.MetalCapacity)
	h.drawTextAt(c, hud.AnchorEnergy0, "0", h.guiColor(15))
	h.drawTextAt(c, hud.AnchorMetal0, "0", h.guiColor(15))
	h.drawTextAt(c, hud.AnchorEnergyProduced, hud.FormatEnergyProduced(rates.EnergyProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorEnergyConsumed, hud.FormatEnergyConsumed(rates.EnergyConsumed), h.guiColor(12))
	h.drawTextAt(c, hud.AnchorMetalProduced, hud.FormatMetalProduced(rates.MetalProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorMetalConsumed, hud.FormatMetalConsumed(rates.MetalConsumed), h.guiColor(12))
}

// drawResourceBar paints one top-strip stock bar [07 R-HUD-03 §4]. With S the
// eased displayed stock, C the capacity and w = x2 - x1:
//
//	fill := ftol(x1 + w * S / C)            // single-precision product and quotient
//	[x1 .. fill] x [y1 .. y2] filled with the side's energycolor / metalcolor
//
// Both spans are the inclusive rectangle filler [03 R-P0-19-P], the same
// primitive the footer damage bar uses, so the bar covers ftol(w*S/C)+1 columns
// and a zero stock still paints the one column at x1 rather than nothing.
//
// The arithmetic is deliberately not hud.FooterBarFill's: that bar is a
// truncating signed integer divide over hit points [07 R-HUD-03 §2], while this
// one is a single-precision product and quotient over the stored stock singles,
// truncated toward zero at the end [01 §8]. Capacity at or below zero draws
// nothing at all, because retail's fill sits inside the `C > 0` branch.
func (h *retailBattleHUD) drawResourceBar(c *client.Client, r hud.Rect, stock, capacity float32, inner byte) {
	left, top, right, bottom := r.Ordered()
	if right < left || bottom < top || capacity <= 0 {
		return
	}
	fill := numeric.TruncateFloat32ToLow32(float32(left) + float32(right-left)*stock/capacity)
	// The displayed stock is eased within 0..capacity [05 R-ECO-01 §6], so
	// these two guards cannot fire on a well-formed snapshot; they keep a
	// malformed one from painting outside the anchor.
	fill = min(max(fill, left), right)
	c.UIFillRect(int(left), int(top), int(fill-left+1), int(bottom-top+1), inner)
}

// drawShareMarker paints the automatic-sharing threshold tick over one
// top-strip bar [07 R-HUD-03 §4]. It sits in the same `C > 0` branch as the
// fill and runs immediately after it, so the marker overwrites the fill it
// crosses. With T the player's SetShareEnergy / SetShareMetal threshold
// ([05 R-SHARE-01 §3]), L the LIVE stock, C the capacity and w = x2 - x1:
//
//	if 0 < T < L:
//	    m := ftol( x1 + w * T / C )        // single-precision product and quotient
//	    [m .. m+2] x [y1 .. y2] filled with dcb[12]
//
// Both bounds are strict, so a zero threshold and a threshold that has reached
// the live stock both draw nothing. The gate reads the live stock, not the
// eased displayed stock the fill uses, so the marker appears and disappears a
// frame ahead of the fill that is still catching up [05 R-ECO-01 §6]. The
// three columns come from the inclusive rectangle filler [03 R-P0-19-P], the
// same primitive the fill uses.
func (h *retailBattleHUD) drawShareMarker(c *client.Client, r hud.Rect, threshold, live, capacity float32) {
	left, top, right, bottom := r.Ordered()
	if right < left || bottom < top || capacity <= 0 {
		return
	}
	if threshold <= 0 || threshold >= live {
		return
	}
	m := numeric.TruncateFloat32ToLow32(float32(left) + float32(right-left)*threshold/capacity)
	c.UIFillRect(int(m), int(top), 3, int(bottom-top+1), h.guiColor(12))
}

func (h *retailBattleHUD) drawNumber(c *client.Client, index int, value float32) {
	r, ok := h.anchors.ByIndex(index)
	if !ok {
		return
	}
	h.drawNumberAtPoint(c, r.X1, r.Y1, value)
}

func (h *retailBattleHUD) drawNumberRight(c *client.Client, index int, value float32) {
	r, ok := h.anchors.ByIndex(index)
	if !ok {
		return
	}
	text := fmt.Sprintf("%d", int(value))
	x := r.X1 - int32(client.MeasureText(h.console, text))
	c.UIText(h.console, text, int(x), int(r.Y1), h.guiColor(15))
}

func (h *retailBattleHUD) drawNumberAtPoint(c *client.Client, x, y int32, value float32) {
	// The retail resource display is an integer text field; the authoritative
	// stock remains float32, and conversion here truncates toward zero [01 §8].
	c.UIText(h.console, fmt.Sprintf("%d", int(value)), int(x), int(y), h.guiColor(15))
}

func (h *retailBattleHUD) drawTextAt(c *client.Client, index int, text string, color byte) {
	r, ok := h.anchors.ByIndex(index)
	if !ok || text == "" {
		return
	}
	c.UIText(h.console, text, int(r.X1), int(r.Y1), color)
}
