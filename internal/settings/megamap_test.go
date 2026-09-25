package settings

import "testing"

// The eight `Megamap*Color` settings default to -1, which keeps each ring's
// research default; a palette index survives normalization and anything
// outside 0..255 returns to -1 (DESIGN_INTERFACE_HUD_INPUT §3.15).
func TestMegamapRingColorsDefaultAndNormalize(t *testing.T) {
	p := DefaultPresentation()
	for i, value := range p.MegamapRingColors() {
		if *value != MegamapColorDefault {
			t.Fatalf("ring colour %d defaults to %d, want -1", i, *value)
		}
	}
	p.MegamapWeapon2Color, p.MegamapAntinukeColor, p.MegamapRadarColor = 0, 255, 256
	p.MegamapSonarColor = -7
	p.Normalize()
	if p.MegamapWeapon2Color != 0 || p.MegamapAntinukeColor != 255 {
		t.Fatalf("palette indices changed: %d %d", p.MegamapWeapon2Color, p.MegamapAntinukeColor)
	}
	if p.MegamapRadarColor != MegamapColorDefault || p.MegamapSonarColor != MegamapColorDefault {
		t.Fatalf("out-of-range colours kept: %d %d", p.MegamapRadarColor, p.MegamapSonarColor)
	}
	if DefaultPresentation().AlliedDotSwatches != 0 {
		t.Fatal("allied dot swatches default on")
	}
}
