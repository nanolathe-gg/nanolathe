package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"testing"
)

func TestWaterWindChangePreservesDriftAndReplay(t *testing.T) {
	c := waterMetadataClient(t)
	c.SetEnhanced(true)
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
	c.SetEnhanced(false)
	if c.waterMotion.valid {
		t.Fatal("renderer reset retained water history")
	}
}
