package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// A stock ship's body supplies the visual check for issue #108. No retail
// bytes are stored in the fixture; the ordinary asset gate supplies the model.
func TestRetailSubmergedShipBodyCanBePicked(t *testing.T) {
	c, _ := captureClient(t, 320, 240)
	c.buffer = frame.NewBuffer()
	f := c.buffer.BeginWrite()
	f.Visibility.SeaLevel = 128 << 16
	if err := c.buffer.Publish(1); err != nil {
		t.Fatal(err)
	}
	v := frame.FeatureView{InstanceID: 1, Model: "armpt_dead", DefName: "armpt_dead", Y: -32 << 16, Heading: 8192}
	c.resetListForTest()
	if !c.drawFeatureModel(v) {
		t.Fatal("stock ship wreck did not draw")
	}
	c.replayForTest()
	hit := false
	for y := 1; y < c.height-1 && !hit; y++ {
		for x := 1; x < c.width-1; x++ {
			i := y*c.width + x
			if c.indexed[i] != 0 && c.indexed[i-1] != 0 && c.indexed[i+1] != 0 && c.indexed[i-c.width] != 0 && c.indexed[i+c.width] != 0 && c.FeatureContainsPoint(v, c.cam, int32(x), int32(y)) {
				hit = true
				break
			}
		}
	}
	if !hit {
		t.Fatal("stock submerged ship has no picked body interior")
	}
	writeCapture(t, c, "submerged-armpt-wreck")
}
