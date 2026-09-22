package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"testing"
)

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
