package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// Modern design policy, not a retail temperature model. Reused packet storage
// must cool, and hidden/old/submerged features must not advertise heat.
func TestWreckCoolingUsesPublishedBirthAndClearsReusedPacket(t *testing.T) {
	c, _, _ := newFeatureRasterClient(t)
	c.buffer = &frame.Buffer{}
	c.enhanced = true
	f := featureRasterView()
	f.WreckHeatKnown = true
	f.WreckBornTick = 0
	var g drawlist.ModelGeometry
	publish := func(tick uint32, visible bool) {
		c.buffer = frame.NewBuffer()
		*c.buffer.BeginWrite() = *stripTestFrame(visible)
		if err := c.buffer.Publish(tick); err != nil {
			t.Fatal(err)
		}
	}
	publish(0, true)
	c.applyWreckHeat(&g, f)
	if g.WreckEmission[0] <= g.WreckEmission[1] || g.WreckEmission[1] <= g.WreckEmission[2] || g.WreckHeatStrength <= 0 {
		t.Fatalf("birth zero did not start warm: %+v", g)
	}
	birth := g
	c.applyWreckHeat(&g, f)
	if g.WreckEmission != birth.WreckEmission || g.WreckHeatTime != birth.WreckHeatTime {
		t.Fatal("drawing advanced heat age")
	}
	publish(6, true)
	c.applyWreckHeat(&g, f)
	if g.WreckEmission[1] >= birth.WreckEmission[1]/2 {
		t.Fatal("brief pale flash did not cool to orange")
	}
	publish(180, true)
	c.applyWreckHeat(&g, f)
	if g.WreckEmission != [3]float32{} || g.WreckHeatStrength <= 0 || g.WreckHeatStrength >= birth.WreckHeatStrength {
		t.Fatal("emission and residual shimmer did not cool independently")
	}
	publish(300, true)
	c.applyWreckHeat(&g, f)
	if g.WreckHeatStrength != 0 {
		t.Fatal("expired wreck still shimmers")
	}
	for _, mode := range []string{"unknown", "classic", "hidden", "submerged", "future"} {
		publish(301, mode != "hidden")
		next := f
		next.WreckBornTick = 301
		c.enhanced = mode != "classic"
		if mode == "unknown" {
			next.WreckHeatKnown = false
		}
		if mode == "submerged" {
			next.Y = -1
		}
		if mode == "future" {
			next.WreckBornTick = 302
		}
		g = birth
		c.applyWreckHeat(&g, next)
		if g.WreckEmission != [3]float32{} || g.WreckHeatStrength != 0 {
			t.Fatalf("%s retained heat", mode)
		}
	}
}
