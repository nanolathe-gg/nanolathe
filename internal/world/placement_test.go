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
	// Retail skips characters outside the table rather than rejecting the
	// definition, so a typo consumes the character and the next usable one
	// fills the cell [04 §6.2].
	got, err := ParseYardMap("Zo", 1, 1)
	if err != nil {
		t.Fatalf("unknown yardmap character rejected the map: %v", err)
	}
	if got[0] != 0x2f {
		t.Fatalf("skipped character left %#x, want the following o", got[0])
	}
}

// TestYardMapLengthMismatch locks retail's two padding rules [04 §6.2]: a
// string shorter than the footprint repeats its last character, and one longer
// than the footprint is read only as far as the last cell. Forty-six of the 126
// stock yard maps disagree with their own footprint — ARMESTOR authors one
// character for 4x4, ARMSOLAR authors 27 for 5x5 — so rejecting a mismatch
// makes those buildings unplaceable.
func TestYardMapLengthMismatch(t *testing.T) {
	short, err := ParseYardMap("o", 2, 2)
	if err != nil {
		t.Fatalf("single-character yard map rejected: %v", err)
	}
	for i, b := range short {
		if b != 0x2f {
			t.Fatalf("cell %d = %#x, want the repeated o", i, b)
		}
	}

	// Nine characters over a 5x5 footprint: the ninth repeats for the rest.
	partial, err := ParseYardMap("ooooooooC", 5, 5)
	if err != nil {
		t.Fatalf("short yard map rejected: %v", err)
	}
	for i, b := range partial {
		want := YardCell(0x2f)
		if i >= 8 {
			want = 0x35
		}
		if b != want {
			t.Fatalf("cell %d = %#x, want %#x", i, b, want)
		}
	}

	// Whitespace between rows is skipped, not counted.
	rows, err := ParseYardMap("oo cc", 2, 2)
	if err != nil {
		t.Fatalf("spaced yard map rejected: %v", err)
	}
	if rows[0] != 0x2f || rows[1] != 0x2f || rows[2] != 0x2d || rows[3] != 0x2d {
		t.Fatalf("spaced yard map parsed as %#x", rows)
	}

	// Trailing characters past the last cell are never read.
	long, err := ParseYardMap("ooooC", 2, 2)
	if err != nil {
		t.Fatalf("long yard map rejected: %v", err)
	}
	for i, b := range long {
		if b != 0x2f {
			t.Fatalf("cell %d = %#x, want o — the trailing C must be ignored", i, b)
		}
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

// TestBitFiveNeedsTheBlockingFlag locks bit 5 [04 §6.2]: the validator resolves
// the covered cell's feature to a definition and reads its authored blocking
// flag. Presence alone is not enough — metal patches are 3x3 non-blocking
// features, and an extractor whose yard map is all `o` has to be placeable on
// its own deposit.
func TestBitFiveNeedsTheBlockingFlag(t *testing.T) {
	yard, err := ParseYardMap("oooo", 2, 2) // o = 0x2f: bit 5 set, bit 6 clear
	if err != nil {
		t.Fatal(err)
	}
	if yard[0]&0x20 == 0 || yard[0]&0x40 != 0 {
		t.Fatalf("o must carry bit 5 and not bit 6, got %#x", yard[0])
	}

	patch := &content.FeatureDef{Metal: 200, Indestructible: true}
	ter := placementFixture(t, patch)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err != nil {
		t.Fatalf("a non-blocking metal patch blocked placement: %v", err)
	}

	tree := &content.FeatureDef{Blocking: true, Reclaimable: true}
	ter = placementFixture(t, tree)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err == nil {
		t.Fatal("a blocking feature did not fail bit 5")
	}
}

// TestUnresolvableReferenceBlocks locks the out-of-range rule of [04 §6.2]: an
// index that does not bind and a void sentinel occupy without resolving, so bit
// 5 blocks. A fringe cell whose anchor hop leads nowhere does not: retail's hop
// reads the anchor and falls out with a zero, the same answer an empty cell
// gives.
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
		blocks  bool
	}{
		{"unbound index", 0, true},
		{"void sentinel", PlotFeatureVoid, true},
		{"orphan fringe", PlotFeatureFringe, false},
	} {
		ter := placementFixture(t, nil) // record 0 binds to nothing
		ter.Plot[1*6+1].SetFeature(tc.feature)
		err := ter.ValidatePlacement(1, 1, free, 2, 2, 0)
		if tc.blocks && err == nil {
			t.Fatalf("%s did not block bit 5", tc.name)
		}
		if !tc.blocks && err != nil {
			t.Fatalf("%s blocked bit 5: %v", tc.name, err)
		}
	}
	// Empty ground passes.
	ter := placementFixture(t, nil)
	if err := ter.ValidatePlacement(1, 1, free, 2, 2, 0); err != nil {
		t.Fatalf("empty ground rejected: %v", err)
	}
}

// TestIndestructibleBlocksBitSix locks bit 6 [04 §6.2]. The validator reads the
// high byte of the feature definition's flag word and tests its bit 1, which is
// the word's bit 9: `indestructible`. It is not the reclaimable flag, which
// sits at bit 7 of the same word and is never read here.
func TestIndestructibleBlocksBitSix(t *testing.T) {
	yard, err := ParseYardMap("ffff", 2, 2) // f = 0x6f: bits 5 and 6 both set
	if err != nil {
		t.Fatal(err)
	}
	if yard[0]&0x40 == 0 {
		t.Fatal("f must carry bit 6")
	}
	// Use a yard byte with bit 6 but not bit 5 so the two gates are separable.
	bitSixOnly := []YardCell{0x40, 0x40, 0x40, 0x40}

	rock := &content.FeatureDef{Indestructible: true}
	ter := placementFixture(t, rock)
	ter.Plot[1*6+1].SetFeature(0)
	if err := ter.ValidatePlacement(1, 1, bitSixOnly, 2, 2, 0); err == nil {
		t.Fatal("an indestructible feature did not fail bit 6")
	}

	// Destructible features pass, reclaimable or not.
	for _, scrap := range []*content.FeatureDef{{Reclaimable: true}, {Reclaimable: false}} {
		ter = placementFixture(t, scrap)
		ter.Plot[1*6+1].SetFeature(0)
		if err := ter.ValidatePlacement(1, 1, bitSixOnly, 2, 2, 0); err != nil {
			t.Fatalf("a destructible feature failed bit 6: %v", err)
		}
	}
}

// TestOccupancyRespectsSelf locks bits 1-2: "reject any nonzero occupant other
// than the passed self identity" [04 §6.2]. Occupants live in the layer-A/B
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestOccupancyRespectsSelf(t *testing.T) {
	yard := []YardCell{0x06, 0x06, 0x06, 0x06} // bits 1-2 only
	ter := placementFixture(t, nil)
	ter.Plot[1*6+1].SetOccupantA(42)

	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err == nil {
		t.Fatal("an occupied cell was accepted during construction")
	}
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 99); err == nil {
		t.Fatal("a cell occupied by another unit was accepted")
	}
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 42); err != nil {
		t.Fatalf("a cell occupied by self was rejected: %v", err)
	}
	// The air word does NOT reject. This assertion used to read the other way
	// round; it was written while nothing in the tree ever wrote the air word,
	// so it locked an untested reading. [04 R-COLL-01 §2] is explicit for the
	// occupancy test: "Only the ground word is read; the air word is never
	// consulted, so a landed or hovering airborne unit never blocks a ground
	// mover through this test." WU-19-20 gave mode-2 movers that word
	// [04 R-COLL-01 §4], and rejecting on it jams a stock aircraft plant on its
	// own hovering products.
	ter.Plot[1*6+1].SetOccupantA(0)
	ter.Plot[1*6+1].SetOccupantB(7)
	if err := ter.ValidatePlacement(1, 1, yard, 2, 2, 0); err != nil {
		t.Fatalf("an airborne occupant rejected a placement [04 R-COLL-01 §2]: %v", err)
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
