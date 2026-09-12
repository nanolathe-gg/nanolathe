package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Artistic cooling for the requested modern prototype (GPU design §28).
// Both phases sample committed age; replay and pausing cannot reheat a wreck.
func (c *Client) applyWreckHeat(g *drawlist.ModelGeometry, f frame.FeatureView) {
	// Packets may reuse storage, so clear even when the source stops qualifying.
	g.WreckEmission = [3]float32{}
	g.WreckHeatStrength, g.WreckHeatTime, g.WreckHeatScale = 0, 0, 0
	if !c.enhanced || !f.WreckHeatKnown || c.buffer == nil {
		return
	}
	cur := c.buffer.Current()
	if cur == nil || f.Y < cur.Visibility.SeaLevel || !SnapshotPointVisible(cur.Visibility, f.X, f.Y, f.Z, cur.ViewingPlayer) {
		return
	}
	elapsed := cur.Tick - f.WreckBornTick
	if elapsed >= 300 {
		return
	}
	age := float32(elapsed)
	if c.interpolation {
		age += c.TickFraction()
	}
	flash := max(1-age/6, 0)
	cool := max(1-age/180, 0)
	red, amber := cool*cool, cool*cool*cool*cool
	g.WreckEmission = [3]float32{0.75*red + 0.15*flash, 0.20*amber + 0.55*flash, 0.015*amber + 0.42*flash}
	heat := max(1-age/300, 0)
	g.WreckHeatStrength = 0.55 * heat * heat
	g.WreckHeatScale = float32(c.viewScale().Float())
	g.WreckHeatTime = float32(cur.Tick%3600) + float32((f.CX*13+f.CZ*7)&255)
	if c.interpolation {
		g.WreckHeatTime += c.TickFraction()
	}
}
