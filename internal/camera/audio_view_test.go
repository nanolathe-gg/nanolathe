package camera

import "testing"

func TestBattleViewOriginUsesBeamInsetAtNativeAndZoomedScale(t *testing.T) {
	c := &Camera{X: 10, Z: 20, ViewW: 640, ViewH: 480}
	if x, z := c.BattleViewOrigin(); x != 138 || z != 52 {
		t.Fatalf("native beam origin = (%d,%d), want (138,52)", x, z)
	}
	c.Scale = 2
	if x, z := c.BattleViewOrigin(); x != 74 || z != 36 {
		t.Fatalf("zoomed beam origin = (%d,%d), want (74,36)", x, z)
	}
	if w, h := c.BattleView(); w != 256 || h != 208 {
		t.Fatalf("zoomed beam = %dx%d, want 256x208", w, h)
	}
}
