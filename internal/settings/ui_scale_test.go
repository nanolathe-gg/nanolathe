package settings

import "testing"

// A size stored under the sidebar-only key carries over to the UI scale, and a
// size above the maximum reads as the maximum
// (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
func TestUIScaleNormalize(t *testing.T) {
	p := Presentation{LegacySidebarScale: 2}
	p.Normalize()
	if p.UIScale != 2 || p.LegacySidebarScale != 0 {
		t.Fatalf("legacy 2 normalized to UIScale %d, legacy %d", p.UIScale, p.LegacySidebarScale)
	}
	p = Presentation{UIScale: 3}
	p.Normalize()
	if p.UIScale != MaxChromeScale {
		t.Fatalf("UIScale 3 normalized to %d, want %d", p.UIScale, MaxChromeScale)
	}
}
