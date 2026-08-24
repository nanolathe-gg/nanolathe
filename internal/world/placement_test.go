package world

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
)

// TestYardMapControlBytes locks the ten control bytes exactly
// [04 §6.2], [05 "Geothermal requirement"] C10.
func TestYardMapControlBytes(t *testing.T) {
	want := map[rune]YardCell{
		'.': 0x00, 'C': 0x35, 'G': 0x8f, 'O': 0x2b, 'Y': 0x31,
		'c': 0x2d, 'f': 0x6f, 'o': 0x2f, 'w': 0x37, 'y': 0x29,
	}
	for ch, expected := range want {
		got, err := ParseYardMap(string(ch), 1, 1)
		if err != nil {
			t.Fatalf("%q: %v", ch, err)
		}
		if got[0] != expected {
			t.Fatalf("%q -> %#x, want %#x", ch, got[0], expected)
		}
	}
	// G is the only control byte carrying bit 7, the geothermal requirement.
	for ch, b := range want {
		if (b&0x80 != 0) != (ch == 'G') {
			t.Fatalf("%q has bit 7 = %v", ch, b&0x80 != 0)
		}
	}
	if _, err := ParseYardMap("Z", 1, 1); err == nil {
		t.Fatal("unknown yardmap character was accepted")
	}
	if _, err := ParseYardMap("oo", 2, 2); err == nil {
		t.Fatal("wrong-length yardmap was accepted")
	}
}

// placementFixture builds a 6x6 terrain with one feature record.
func placementFixture(t *testing.T, def *content.FeatureDef) *Terrain {
	t.Helper()
	attrs := make([]formats.TNTAttribute, 36)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: PlotFeatureNone}
	}
	ter := &Terrain{
		CellW: 6, CellH: 6,
		Plot:        ExpandPlot(attrs, 6, 6),
		FeatureDefs: []*content.FeatureDef{def},
	}
	return ter
}

// TestGeothermalRequiresTheFlag locks [05 "Geothermal requirement"]: bit 7
// succeeds only when a covered cell holds a feature whose catalog entry carries
// the geothermal flag. The old code accepted ANY real feature whenever no
// catalog was supplied, which let a geothermal plant be built on a tree.
func TestGeothermalRequiresTheFlag(t *testing.T) {
	yard, err := ParseYardMap("GGGG", 2, 2)
	if err != nil {
		t.Fatal(err)
	}

	// A non-geothermal feature under the footprint does not satisfy it.
	tree := &content.FeatureDef{Reclaimable: true}
	ter := placementFixture(t, tree)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err == nil {
		t.Fatal("geothermal requirement was satisfied by a non-geothermal feature")
	} else if !strings.Contains(err.Error(), "geothermal") {
		t.Fatalf("wrong rejection: %v", err)
	}

	// The flagged feature satisfies it.
	vent := &content.FeatureDef{Geothermal: true, Reclaimable: true}
	ter = placementFixture(t, vent)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err != nil {
		t.Fatalf("geothermal placement on a vent rejected: %v", err)
	}

	// An empty footprint does not satisfy it.
	ter = placementFixture(t, vent)
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err == nil {
		t.Fatal("geothermal requirement was satisfied by empty ground")
	}
}

// TestUnresolvableReferenceBlocks locks the out-of-range rule of [04 §6.2]: an
// index that does not bind, a void sentinel, and a fringe cell whose anchor hop
// leads nowhere all behave as blocking for bit 5 and non-satisfying for bit 7.
func TestUnresolvableReferenceBlocks(t *testing.T) {
	free, err := ParseYardMap("ffff", 2, 2) // f = 0x6f, bit 5 set
	if err != nil {
		t.Fatal(err)
	}
	if free[0]&0x20 == 0 {
		t.Fatal("f must carry bit 5")
	}
	for _, tc := range []struct {
		name    string
		feature uint16
	}{
		{"unbound index", 0},
		{"void sentinel", PlotFeatureVoid},
		{"orphan fringe", PlotFeatureFringe},
	} {
		ter := placementFixture(t, nil) // record 0 binds to nothing
		ter.Plot[1*6+1].SetFeature(tc.feature)
		if err := ter.ValidatePlacement(1, 1, free, 2, 2, 0); err == nil {
			t.Fatalf("%s did not block bit 5", tc.name)
		}
	}
	// Empty ground passes.
	ter := placementFixture(t, nil)
	if err := ter.ValidatePlacement(1, 1, free, 2, 2, 0); err != nil {
		t.Fatalf("empty ground rejected: %v", err)
	}
}

// TestNonReclaimableBlocksBitSix locks bit 6 [04 §6.2]. It used to be tagged
// TODO(T25) even though content.FeatureDef already carries the flag and T25's
// accepted-blocked list does not include it.
func TestNonReclaimableBlocksBitSix(t *testing.T) {
	yard, err := ParseYardMap("ffff", 2, 2) // f = 0x6f: bits 5 and 6 both set
	if err != nil {
		t.Fatal(err)
	}
	if yard[0]&0x40 == 0 {
		t.Fatal("f must carry bit 6")
	}
	// Use a yard byte with bit 6 but not bit 5 so the two gates are separable.
	bitSixOnly := []YardCell{0x40, 0x40, 0x40, 0x40}

	rock := &content.FeatureDef{Reclaimable: false}
	ter := placementFixture(t, rock)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, bitSixOnly, 2, 2, 0); err == nil {
		t.Fatal("a non-reclaimable feature did not fail bit 6")
	}

	scrap := &content.FeatureDef{Reclaimable: true}
	ter = placementFixture(t, scrap)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, bitSixOnly, 2, 2, 0); err != nil {
		t.Fatalf("a reclaimable feature failed bit 6: %v", err)
	}
}

// TestOccupancyRespectsSelf locks bits 1-2: "reject any nonzero occupant other
// than the passed self identity" [04 §6.2].
func TestOccupancyRespectsSelf(t *testing.T) {
	yard := []YardCell{0x06, 0x06, 0x06, 0x06} // bits 1-2 only
	ter := placementFixture(t, nil)
	ter.Plot[1*6+1].SetOccupied(true)
	ter.Plot[1*6+1].SetAnchorWord(42)

	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err == nil {
		t.Fatal("an occupied cell was accepted during construction")
	}
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 99); err == nil {
		t.Fatal("a cell occupied by another unit was accepted")
	}
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 42); err != nil {
		t.Fatalf("a cell occupied by self was rejected: %v", err)
	}
}

// TestPlacementBoundsCheckedFirst: "The placement validator first bounds-checks
// the rectangle against the map" [04 §6.2].
func TestPlacementBoundsCheckedFirst(t *testing.T) {
	yard, _ := ParseYardMap("oooo", 2, 2)
	ter := placementFixture(t, nil)
	for _, tc := range [][2]int32{{-1, 0}, {0, -1}, {5, 0}, {0, 5}} {
		if err := ter.ValidatePlacement(tc[0], tc[1], yard, 2, 2, 0); err == nil {
			t.Fatalf("placement at %v was accepted", tc)
		}
	}
}
