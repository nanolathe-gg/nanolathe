//go:build retail

package session

// The stock half of the spawner's position fixup [08 R-ENTRY-01 §6]. The
// default-tier test alongside this one asserts the arithmetic against a
// synthetic 5x5 definition; this one asserts that the definition the arithmetic
// was derived from really is 5x5, really is a structure, and really lands where
// the review measured it — so a catalog change that moved ARMMOHO's footprint
// would fail here rather than quietly re-siting every stock geothermal plant.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
)

// The ARMMOHO record of `maps/a shortage of water.ota`, schema 0.
const (
	wu19205MohoKey     = "ARMMOHO"
	wu19205MohoAuthorX = 5696
	wu19205MohoAuthorZ = 6544
	wu19205MohoSnapX   = 5704
	wu19205MohoSnapZ   = 6552
)

func TestMissionPlacementSnapsStockGeothermalPlant(t *testing.T) {
	const w = 1 << 16
	cat, _ := retailcat.Shared(t)
	def, ok := cat.Unit(wu19205MohoKey)
	if !ok || def == nil {
		t.Skipf("retail fixture unit %q is absent", wu19205MohoKey)
	}
	if def.BMCode != 0 {
		t.Fatalf("%s authors BMcode=1; the stock structure census makes it 0 [SC21]", wu19205MohoKey)
	}
	if def.FootprintX != 5 || def.FootprintZ != 5 {
		t.Fatalf("%s footprint %dx%d, want 5x5 [fmt fbi]", wu19205MohoKey, def.FootprintX, def.FootprintZ)
	}
	// A flat plot large enough to hold the authored point with room for the
	// probe's bounds guard; the height assertion belongs to the default-tier
	// test, this one pins the horizontal snap on the real footprint.
	ter := wu19205Terrain(512, 512, 10, 0)
	up := mission.UnitPlacement{UnitName: wu19205MohoKey, X: wu19205MohoAuthorX * w, Z: wu19205MohoAuthorZ * w}
	x, _, z := missionPlacementPosition(ter, def, up)
	if int64(x) != wu19205MohoSnapX*w || int64(z) != wu19205MohoSnapZ*w {
		t.Fatalf("%s snapped to (%d, %d), want (%d, %d) [08 R-ENTRY-01 §6]",
			wu19205MohoKey, int64(x)/w, int64(z)/w, wu19205MohoSnapX, wu19205MohoSnapZ)
	}
}
