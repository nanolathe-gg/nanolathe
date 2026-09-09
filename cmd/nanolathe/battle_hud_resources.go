package main

// The resource readouts at the top of the screen: the two bars and the
// numeric and text pens the side anchors place [07 §6].

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

func (h *retailBattleHUD) drawResources(c *client.Client, f *frame.Frame) {
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
	// TODO(I10): use the viewing player's saved display deadline when that
	// presentation timing has an owner; this existing 30-tick latch stays
	// intact on View [07 R-HUD-03 §4].
	if !h.rateSampleOK || f.Tick < h.rateSampleTick || f.Tick-h.rateSampleTick >= 30 {
		h.rateSampleTick = f.Tick
		h.rateSample = *res
		h.rateSampleOK = true
	}
	rates := &h.rateSample
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
	h.drawResourceBar(c, energyBar, hud.ResourceFraction(res.Energy, res.EnergyCapacity), byte(energy))
	h.drawResourceBar(c, metalBar, hud.ResourceFraction(res.Metal, res.MetalCapacity), byte(metal))
	h.drawNumber(c, hud.AnchorEnergyNum, float32(res.Energy))
	h.drawNumber(c, hud.AnchorMetalNum, float32(res.Metal))
	h.drawNumberRight(c, hud.AnchorEnergyMax, res.EnergyCapacity)
	h.drawNumberRight(c, hud.AnchorMetalMax, res.MetalCapacity)
	h.drawTextAt(c, hud.AnchorEnergy0, "0", h.guiColor(15))
	h.drawTextAt(c, hud.AnchorMetal0, "0", h.guiColor(15))
	h.drawTextAt(c, hud.AnchorEnergyProduced, hud.FormatEnergyProduced(rates.EnergyProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorEnergyConsumed, hud.FormatEnergyConsumed(rates.EnergyConsumed), h.guiColor(12))
	h.drawTextAt(c, hud.AnchorMetalProduced, hud.FormatMetalProduced(rates.MetalProduced), h.guiColor(10))
	h.drawTextAt(c, hud.AnchorMetalConsumed, hud.FormatMetalConsumed(rates.MetalConsumed), h.guiColor(12))
}

func (h *retailBattleHUD) drawResourceBar(c *client.Client, r hud.Rect, fraction float32, inner byte) {
	left, top, right, bottom := r.Ordered()
	if right <= left || bottom <= top {
		return
	}
	filled := int(float32(right-left) * fraction)
	if filled <= 0 {
		return
	}
	c.UIFillRect(int(left), int(top), filled, int(bottom-top), inner)
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
