package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"math"
	"testing"
)

func TestWaterWindChangePreservesDriftAndReplay(t *testing.T) {
	c := waterMetadataClient(t)
	c.SetEnhanced(true)
	c.terrain.Tidal = 20
	f := &frame.Frame{Tick: 1, Wind: frame.WindView{Heading: 49152, Strength: 5000}}
	c.observeWaterMotion(f)
	for f.Tick < 120 {
		f.Tick++
		c.observeWaterMotion(f)
	}
	before := c.waterMotion
	f.Tick++
	f.Wind = frame.WindView{Heading: 16384, Strength: 0}
	c.observeWaterMotion(f)
	after := c.waterMotion
	// The surface current turns gradually while keeping tidal speed, even
	// when the wind reverses and its strength drops to zero (GPU design §26).
	dx, dz := float64(after.tidalX-before.tidalX), float64(after.tidalZ-before.tidalZ)
	if dx <= 0 || math.Abs(math.Hypot(dx, dz)-1.0/30) > 0.00001 {
		t.Fatalf("tidal current snapped or changed speed on reversal: delta=(%g,%g)", dx, dz)
	}
	if after.x <= before.x || after.x-before.x > .067 || after.energy < .98 {
		t.Fatalf("wind change reset drift or amplitude: %+v -> %+v", before, after)
	}
	c.observeWaterMotion(f)
	if c.waterMotion != after {
		t.Fatal("same tick advanced water")
	}
	for f.Tick < 600 {
		f.Tick++
		c.observeWaterMotion(f)
	}
	if c.waterMotion.vx >= 0 {
		t.Fatal("water did not eventually follow reversed wind")
	}
	if math.Sin(float64(c.waterMotion.tidalHeading)) >= 0 {
		t.Fatal("tidal current did not eventually follow reversed wind")
	}
	before = c.waterMotion
	c.terrain.Tidal = 0
	f.Tick++
	c.observeWaterMotion(f)
	if c.waterMotion.tidalX != before.tidalX || c.waterMotion.tidalZ != before.tidalZ {
		t.Fatal("zero tidal strength retained a directional current")
	}
	c.SetEnhanced(false)
	if c.waterMotion.valid {
		t.Fatal("renderer reset retained water history")
	}
}
