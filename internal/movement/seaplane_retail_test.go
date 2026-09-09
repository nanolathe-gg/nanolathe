//go:build retail

package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// TestStockSeaplanesCompileToALandableWaterFloor ties the two halves of the
// seaplane fix together against the real install: the TDF duplicate-key rule
// must resolve these records' maximum water depth to the second spelling, and
// the aircraft water rule of [04 R-AIR-01 §6a] must then leave their floor
// below sea level because they are `amphibious`.
//
// Both halves are needed and neither is visible from the other's package, which
// is why this lives here rather than in formats: the formats tests lock the
// resolution on an authored fixture, and this one locks that the stock corpus
// actually carries the shape those tests describe.
func TestStockSeaplanesCompileToALandableWaterFloor(t *testing.T) {
	cat, _ := retailcat.Shared(t)

	// The eight seaplanes. Each authors `MaxWaterDepth=0` early in its
	// `[UNITINFO]` and `maxwaterdepth=255` at the end, plus `amphibious=1`.
	for _, key := range []string{"armseap", "corseap", "armcsa", "corcsa", "armsfig", "corsfig", "armsehak", "corhunt"} {
		def, ok := cat.Unit(key)
		if !ok || def == nil {
			t.Fatalf("%s is missing from the compiled catalog", key)
		}
		if !def.CanFly || !def.Amphibious {
			t.Fatalf("%s: canfly=%v amphibious=%v, want both true — a seaplane is a can-fly definition that also authors amphibious",
				key, def.CanFly, def.Amphibious)
		}
		p := NewScratchProfile(def)
		if p.MaxWaterDepth != 255 {
			t.Fatalf("%s: MaxWaterDepth=%d, want 255. The record authors two case variants of the key and retail reads "+
				"the last one parsed [fmt tdf \"Duplicate keys\"]; reading the first gives 0, which puts the water floor "+
				"at sea level and makes [04 R-AIR-01 §6a]'s amphibious arm unreachable", key, p.MaxWaterDepth)
		}
		// The predicate's own arithmetic, over any sea level: the floor stays
		// below it, so water is landable ground [04 R-AIR-01 §6a].
		const sea = 40
		if floor := int32(sea) - p.MaxWaterDepth; floor >= sea {
			t.Fatalf("%s: water floor %d is not below sea level %d", key, floor, sea)
		}
	}

	// A non-amphibious aircraft that authors the same pair still cannot land on
	// water: the rule raises its floor back to sea level whatever the depth says.
	for _, key := range []string{"armfig", "armlance", "cortitan", "corveng"} {
		def, ok := cat.Unit(key)
		if !ok || def == nil {
			t.Fatalf("%s is missing from the compiled catalog", key)
		}
		if def.Amphibious {
			t.Fatalf("%s authors amphibious; the fixture assumed it does not", key)
		}
		if p := NewScratchProfile(def); p.MaxWaterDepth != 255 {
			t.Fatalf("%s: MaxWaterDepth=%d, want 255 — it authors the same case-variant pair", key, p.MaxWaterDepth)
		}
	}
}
