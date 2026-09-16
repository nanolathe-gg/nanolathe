package world

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// percellTerrain is an authored flat plot at the given sea level. Callers
// overwrite the derived pair of the cells they care about; every other cell
// stays at height 0 with a zero slope.
func percellTerrain(t *testing.T, w, h int32, sea uint8) *Terrain {
	t.Helper()
	attrs := make([]formats.TNTAttribute, int(w*h))
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: sea, Feature: PlotFeatureNone}
	}
	return &Terrain{CellW: w, CellH: h, SeaLevel: sea, Plot: ExpandPlot(attrs, int(w), int(h))}
}

func percellRect(t *testing.T, cx, cz, w, d int32) FootprintRect {
	t.Helper()
	extent, err := NewFootprintExtent(w, d)
	if err != nil {
		t.Fatalf("extent: %v", err)
	}
	rect, err := NewFootprintRect(NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		t.Fatalf("rect: %v", err)
	}
	return rect
}

// TestMobilePlacementHasNoRectangleSlopeAggregate is the DS-WV-02 contract.
//
// The rectangle aggregate — min of the sampled lows, max of the sampled highs,
// one comparison against the limit — exists ONLY in the structure placement
// validator's yard-map walk [04 R-P0-08 "footprint aggregates and strict
// comparisons"][04 R-SLOPE-01 §3 "Bounded census"]. The mobile validator tests
// each covered cell on its own derived pair and has no aggregate at all
// [04 R-P0-08 "mobile terrain validator"][04 R-COLL-01 §2 "the mode-1 scan"].
//
// The fixture is the exact case that separates the two rules: two cells whose
// own slopes are well inside the limit but whose heights lie far apart, so the
// rectangle span is four times the limit. Retail admits it for a mobile
// product and refuses it for a building yard whose cells carry bit 3.
func TestMobilePlacementHasNoRectangleSlopeAggregate(t *testing.T) {
	ter := percellTerrain(t, 4, 4, 0)
	// Two plateaus 40 apart, each individually gentle.
	ter.PlotAt(1, 1).SetMinHeight(100)
	ter.PlotAt(1, 1).SetMaxHeight(105)
	ter.PlotAt(2, 1).SetMinHeight(140)
	ter.PlotAt(2, 1).SetMaxHeight(145)
	rect := percellRect(t, 1, 1, 2, 1)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 10, MaxWaterSlope: 10, MaxWaterDepth: 10000, MinWaterDepth: -10000}

	result, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules})
	if err != nil {
		t.Fatalf("mobile footprint whose every cell is legal was rejected: %v", err)
	}
	// The published site height is still the sampled minimum; it is a
	// publication, not a legality aggregate [04 R-P0-08].
	if result.SiteHeight != 100 {
		t.Fatalf("site height %d, want the sampled minimum 100", result.SiteHeight)
	}

	// The same rectangle as a building yard, bit 3 on both cells: the
	// aggregate span 45 exceeds MaxSlope 10 and rejects.
	_, err = ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: []YardCell{0x08, 0x08}, Rules: rules})
	if err == nil || !strings.Contains(err.Error(), "slope") {
		t.Fatalf("building yard aggregate = %v, want a slope rejection", err)
	}
}

// TestMobilePlacementSlopeEqualityIsLegal locks the strictness of the mobile
// comparison: rejection is a strict greater-than, so a cell slope exactly at
// the limit passes and one unit more rejects [04 R-P0-08 "mobile terrain
// validator"][04 R-COLL-01 §2 "All comparisons are signed and strict"].
func TestMobilePlacementSlopeEqualityIsLegal(t *testing.T) {
	ter := percellTerrain(t, 4, 4, 0)
	rect := percellRect(t, 1, 1, 1, 1)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 10, MaxWaterSlope: 10, MaxWaterDepth: 10000, MinWaterDepth: -10000}

	ter.PlotAt(1, 1).SetMinHeight(100)
	ter.PlotAt(1, 1).SetMaxHeight(110) // slope == MaxSlope
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err != nil {
		t.Fatalf("slope equal to the limit rejected: %v", err)
	}
	ter.PlotAt(1, 1).SetMaxHeight(111)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("slope one above the limit accepted")
	}
}

// TestMobilePlacementSelectsThePairPerCell covers a mixed land/water footprint:
// the pair is chosen from EACH cell's own `hmin >= SeaLevel`, not from the
// rectangle's aggregate water state [04 R-P0-08][04 R-COLL-01 §2 step 5].
//
// Both cells below carry the same slope. One sits on land and is judged
// against MaxSlope; one sits in water and is judged against the looser
// MaxWaterSlope (the compile-time clamp of [04 §6.1] makes the water limit the
// looser of the pair). Under the old rectangle rule the whole footprint took
// the water pair as soon as any part of it was below sea level, so the land
// cell escaped its own limit.
func TestMobilePlacementSelectsThePairPerCell(t *testing.T) {
	ter := percellTerrain(t, 4, 4, 100)
	// Water cell: hmin below sea level, slope 8.
	ter.PlotAt(1, 1).SetMinHeight(90)
	ter.PlotAt(1, 1).SetMaxHeight(98)
	// Land cell: hmin at sea level, slope 8.
	ter.PlotAt(2, 1).SetMinHeight(100)
	ter.PlotAt(2, 1).SetMaxHeight(108)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 5, MaxWaterSlope: 12, MaxWaterDepth: 255, MinWaterDepth: -10000}

	waterOnly := percellRect(t, 1, 1, 1, 1)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: waterOnly, Mobile: true, Rules: rules}); err != nil {
		t.Fatalf("water cell inside MaxWaterSlope rejected: %v", err)
	}
	mixed := percellRect(t, 1, 1, 2, 1)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: mixed, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("mixed footprint let the land cell take the water pair")
	}
}

// TestMobilePlacementDepthGatesArePerCell locks the two depth gates on the
// mobile path as cell tests [04 R-COLL-01 §2 steps 3-4]: a single covered cell
// deeper than MaxWaterDepth, or shallower than MinWaterDepth, rejects the
// footprint while its neighbours are legal. Equality at either limit passes.
func TestMobilePlacementDepthGatesArePerCell(t *testing.T) {
	ter := percellTerrain(t, 4, 4, 100)
	rect := percellRect(t, 1, 1, 2, 1)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 10, MinWaterDepth: -10000}

	// (1,1) exactly at Sea-MaxWaterDepth passes; one below rejects.
	ter.PlotAt(1, 1).SetMinHeight(90)
	ter.PlotAt(1, 1).SetMaxHeight(90)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err != nil {
		t.Fatalf("depth equality rejected: %v", err)
	}
	ter.PlotAt(1, 1).SetMinHeight(89)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("a single cell below MaxWaterDepth accepted")
	}

	// The shallow gate on the other cell of the same rectangle.
	ter.PlotAt(1, 1).SetMinHeight(90)
	rules.MinWaterDepth = 10
	ter.PlotAt(2, 1).SetMinHeight(90)
	ter.PlotAt(2, 1).SetMaxHeight(90) // exactly Sea-MinWaterDepth
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err != nil {
		t.Fatalf("minimum depth equality rejected: %v", err)
	}
	ter.PlotAt(2, 1).SetMaxHeight(91)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("a single cell above Sea-MinWaterDepth accepted")
	}
}

// TestMobilePlacementTerrainGatesOnlyInModeOne keeps the mode gating the fix
// must not disturb: the inline terrain tests belong to terrain-check mode 1,
// and a caller outside it — an aircraft product, an unresolved profile, or an
// explicit skip — still gets the bounds, feature and occupancy gates but no
// terrain legality at all [04 §6.4][04 R-COLL-01 §2 "class dispatch"].
func TestMobilePlacementTerrainGatesOnlyInModeOne(t *testing.T) {
	ter := percellTerrain(t, 4, 4, 0)
	ter.PlotAt(1, 1).SetMinHeight(0)
	ter.PlotAt(1, 1).SetMaxHeight(255) // a cliff no profile could admit
	rect := percellRect(t, 1, 1, 1, 1)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 1, MaxWaterSlope: 1, MaxWaterDepth: 10000, MinWaterDepth: -10000}

	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("mode 1 accepted an impassable cell")
	}
	skipped := PlacementQuery{Rect: rect, Mobile: true, Rules: rules, SkipTerrainAggregates: true}
	result, err := ter.CheckPlacement(skipped)
	if err != nil {
		t.Fatalf("a query outside the inline terrain mode was rejected: %v", err)
	}
	// siteHeight is still published for the allocator [04 R-P0-08].
	if result.SiteHeight != 0 {
		t.Fatalf("site height %d, want the sampled minimum 0", result.SiteHeight)
	}
	// And the occupancy gate still applies outside the terrain mode.
	ter.PlotAt(1, 1).SetOccupantA(9)
	if _, err := ter.CheckPlacement(skipped); err == nil {
		t.Fatal("occupancy gate skipped with the terrain gates")
	}
}
