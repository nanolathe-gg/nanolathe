package camera

import "testing"

// A widened rail moves the viewport's leading edge, the clamp floor and the
// centring with it, so the map's column 0 still reaches the viewport's edge
// beside a magnified sidebar (DESIGN_INTERFACE_HUD_INPUT "Modern UI
// scale"). Zero insets stay retail's.
func TestChromeInsetWidensTheViewport(t *testing.T) {
	c := &Camera{ViewW: 2560, ViewH: 1440, MapW: 8192, MapH: 8192, Chrome: ChromeInsets{Left: 256}}
	if w, h := c.BattleView(); w != 2560-256 || h != 1440-64 {
		t.Fatalf("BattleView() = %dx%d, want %dx%d", w, h, 2560-256, 1440-64)
	}
	c.JumpTo(-1000, 0)
	if c.X != -256 {
		t.Fatalf("clamped X = %d, want -256: column 0 at the viewport's leading edge", c.X)
	}
	c.JumpToBattleViewCenter(4000, 4000)
	if got := 4000 - c.X; got != 256+(2560-256)/2 {
		t.Fatalf("target screen X = %d, want the viewport centre %d", got, 256+(2560-256)/2)
	}
	retail := &Camera{}
	if l, top, b := retail.ChromeInset(); l != OriginX || top != OriginY || b != OriginY {
		t.Fatalf("zero ChromeInset() = %d,%d,%d, want retail's %d,%d,%d", l, top, b, OriginX, OriginY, OriginY)
	}
}
