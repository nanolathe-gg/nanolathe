package main

import (
	"fmt"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Host layout adapts the source's optional income panel to Nanolathe's HUD;
// every changing value comes from the committed frame (interface design).
func (b *battleSession) incomeWidget() (int, int) { w, _ := b.surfaceSize(); return int(w) - 18, 36 }
func (b *battleSession) serviceCommunityIncome(mouse input.MouseState) bool {
	if b == nil || b.hostPreferences().AlliedResources == 0 {
		return false
	}
	x, y := b.incomeWidget()
	inside := int(mouse.X) >= x && int(mouse.X) < x+14 && int(mouse.Y) >= y && int(mouse.Y) < y+14
	if mouse.Pressed(input.MouseButtonLeft) && inside {
		b.incomePointerCaptured = true
		return true
	}
	if b.incomePointerCaptured {
		if mouse.Released(input.MouseButtonLeft) {
			if inside {
				b.incomeMinimized = !b.incomeMinimized
			}
			b.incomePointerCaptured = false
		}
		return true
	}
	return false
}
func communityStorageText(value float32) string {
	if value < 10000 {
		return fmt.Sprintf("%.0f", value)
	}
	if value < 100000 {
		return fmt.Sprintf("%.1fK", value/1000)
	}
	return fmt.Sprintf("%.0fK", value/1000)
}
func (h *retailBattleHUD) drawCommunityIncome(c *client.Client, b *battleSession, f *frame.Frame) {
	if h == nil || c == nil || b == nil || f == nil || b.hostPreferences().AlliedResources == 0 || h.console == nil {
		return
	}
	local := int(f.ViewingPlayer)
	if local >= len(f.Players) {
		return
	}
	x, y := b.incomeWidget()
	c.UIFillRect(x, y, 14, 14, h.guiColor(0))
	label := "-"
	if b.incomeMinimized {
		label = "+"
	}
	c.UIText(h.console, label, x+3, y+1, h.guiColor(15))
	if b.incomeMinimized {
		return
	}
	left := max(128, x-205)
	top := y + 16
	_, height := c.Size()
	for _, e := range f.Economy {
		slot := int(e.Player)
		if !e.Active || slot == local || slot >= len(f.Players) || !f.Players[local].Allies[slot] || !f.Players[slot].Present {
			continue
		}
		if top+38 > height-32 {
			break
		}
		c.UIFillRect(left, top, x+14-left, 38, h.guiColor(0))
		c.UIText(h.console, f.Players[slot].Name, left+3, top, h.guiColor(15))
		for i, row := range []struct {
			stock, capacity, income float32
			color                   int32
			label                   string
		}{
			{e.Metal, e.MetalCapacity, e.MetalProduced, h.side.MetalColor, "M"},
			{e.Energy, e.EnergyCapacity, e.EnergyProduced, h.side.EnergyColor, "E"},
		} {
			yy := top + 12 + i*12
			text := row.label + " " + communityStorageText(row.stock)
			c.UIText(h.console, text, left+3, yy, h.guiColor(15))
			h.drawResourceBar(c, hud.Rect{X1: int32(left + 58), Y1: int32(yy + 3), X2: int32(left + 137), Y2: int32(yy + 8)}, row.stock, row.capacity, byte(row.color))
			income := fmt.Sprintf("+%.0f", row.income)
			if i == 0 {
				income = fmt.Sprintf("+%.1f", row.income)
			}
			c.UIText(h.console, income, left+144, yy, h.guiColor(10))
		}
		top += 40
	}
}

func communityWeatherPower(cat *content.Catalog, speed, minSpeed, maxSpeed int32) (current, lo, hi int32) {
	solar, generator := int32(0), int32(0)
	if cat != nil {
		if def, ok := cat.Unit("armsolar"); ok {
			solar = -numeric.TruncateFloat64ToLow32(def.EnergyUse)
		}
		if def, ok := cat.Unit("armwin"); ok {
			generator = numeric.TruncateFloat64ToLow32(def.WindGenerator)
		}
	}
	if solar == 0 || generator == 0 {
		generator = 30
	}
	// Nanolathe's retained retail wind hard limit [05 "Wind generation"].
	power := func(v int32) int32 { return min((generator*v+2500)/5000, generator) }
	return power(speed), power(minSpeed), power(maxSpeed)
}
func (h *retailBattleHUD) drawCommunityWeather(c *client.Client, b *battleSession, f *frame.Frame) {
	if h == nil || c == nil || b == nil || f == nil || b.hostPreferences().WeatherReport == 0 || h.console == nil || b.sess == nil || b.sess.World == nil || b.sess.Wind == nil {
		return
	}
	current, lo, hi := communityWeatherPower(b.cat, f.Wind.Strength, b.sess.Wind.Min, b.sess.Wind.Max)
	tidal := numeric.TruncateFloat32ToLow32(b.sess.World.Tidal)
	energy, _ := h.anchors.ByIndex(hud.AnchorEnergyBar)
	metal, _ := h.anchors.ByIndex(hud.AnchorMetalBar)
	produced, _ := h.anchors.ByIndex(hud.AnchorEnergyProduced)
	consumed, _ := h.anchors.ByIndex(hud.AnchorEnergyConsumed)
	x := int(max(energy.X2, metal.X2)) + 110
	if x <= 110 {
		x = 366
	}
	y1, y2 := int(produced.Y1), int(consumed.Y1)
	if y1 <= 0 {
		y1 = int(energy.Y1)
	}
	if y2 <= 0 {
		y2 = int(metal.Y1)
	}
	if y2 <= y1 {
		y2 = y1 + 16
	}
	width, height := c.Size()
	wind := fmt.Sprintf("Wind : +%d (%d-%d)", current, lo, hi)
	tide := fmt.Sprintf("Tidal : +%d", tidal)
	clock := standaloneClockText("Game Time", f.Tick)
	clockX := min(x+117, width-client.MeasureText(h.console, clock)-4)
	x = min(x, max(0, clockX-client.MeasureText(h.console, wind)-4))
	y1 = max(0, min(y1, height-int(h.console.Height)))
	y2 = max(0, min(y2, height-int(h.console.Height)))
	c.UIText(h.console, wind, x, y1, h.guiColor(10))
	c.UIText(h.console, tide, x, y2, h.guiColor(10))
	c.UIText(h.console, clock, max(0, clockX), (y1+y2)/2, h.guiColor(7))
}

// +bps retains the retail transport display in the strategic view as well.
// Nanolathe has no network transport, so both measured rates are zero. The
// independent clock preference continues to own game-time text [07 §3].
func (h *retailBattleHUD) drawCommunityBPS(c *client.Client, b *battleSession) {
	if h == nil || c == nil || b == nil || !b.bpsVisible || h.console == nil {
		return
	}
	_, height := c.Size()
	y := height - 95
	for _, text := range []string{"Send - 0.0 K/s", "Receive - 0.0 K/s"} {
		c.UIText(h.console, text, 129, y, h.guiColor(15))
		y += int(h.console.Height)
		color := h.guiColor(15)
		c.UIFillRect(129, y, 64, 1, color)
		c.UIFillRect(129, y+7, 64, 1, color)
		c.UIFillRect(129, y, 1, 8, color)
		c.UIFillRect(192, y, 1, 8, color)
		y += 9
	}
}
