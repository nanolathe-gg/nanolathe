package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// The allied row's square takes the dot colour table by the player's logo
// colour, not the slot, and draws only with alliedDotSwatches on
// [draw-engine-interface "Allied resource bars"].
func TestAlliedDotSwatchIndexesByLogo(t *testing.T) {
	p := settings.DefaultPresentation()
	f := &frame.Frame{}
	f.Players[2].Logo = 5
	if _, ok := alliedDotSwatch(p, f, 2); ok {
		t.Fatal("swatch drawn with the setting off")
	}
	p.AlliedDotSwatches = 1
	if dot, ok := alliedDotSwatch(p, f, 2); !ok || dot != uint8(p.PlayerDotColors[5]) {
		t.Fatalf("swatch = %d %v, want the logo-5 colour %d", dot, ok, p.PlayerDotColors[5])
	}
	if dot, ok := alliedDotSwatch(p, f, 10); !ok || dot != 0 {
		t.Fatalf("player index 10 = %d %v, want palette index 0", dot, ok)
	}
}

func TestCommunityWeatherReferenceFallbackAndRounding(t *testing.T) {
	solar := &content.UnitDef{EnergyUse: -20}
	wind := &content.UnitDef{WindGenerator: 40}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"armsolar": solar, "armwin": wind}}
	current, lo, hi := communityWeatherPower(cat, 62, 63, 6000)
	if current != 0 || lo != 1 || hi != 40 {
		t.Fatalf("weather=%d/%d/%d", current, lo, hi)
	}
	delete(cat.Units, "armsolar")
	current, _, _ = communityWeatherPower(cat, 5000, 0, 5000)
	if current != 30 {
		t.Fatalf("missing solar did not reset reference wind: %d", current)
	}
	for _, tc := range []struct {
		value float32
		want  string
	}{{9999, "9999"}, {10000, "10.0K"}, {100000, "100K"}} {
		if got := communityStorageText(tc.value); got != tc.want {
			t.Fatalf("stock %v=%s want=%s", tc.value, got, tc.want)
		}
	}
}
