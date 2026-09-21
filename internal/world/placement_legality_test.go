package world

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func legalityTerrain(t *testing.T, w, h int32, height uint8) *Terrain {
	t.Helper()
	attrs := make([]formats.TNTAttribute, int(w*h))
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: PlotFeatureNone}
	}
	return &Terrain{CellW: w, CellH: h, SeaLevel: height, Plot: ExpandPlot(attrs, int(w), int(h))}
}

// TestCheckPlacementEntryBoundsByClass locks the building blocker's entry
// bounds, which are strict on BOTH edges [05 R-ECO-02 §1][08 R-AI-03 §7.1]:
// with the anchor (x,z) and footprint (fx,fz) in cells the validator requires
// x >= 1, z >= 1, x + fx < mapWidthCells and z + fz < mapHeightCells, so a
// building footprint never covers column 0 or row 0 and its last covered
// column and row are at most width-2 and height-2. The mobile side admits
// column 0 and row 0 [04 R-COLL-01 §2] and keeps the half-open ceiling this
// validator has always used; this test used to lock the permissive envelope
// for buildings too (EC-01).
func TestCheckPlacementEntryBoundsByClass(t *testing.T) {
	ter := legalityTerrain(t, 4, 4, 100)
	extent, _ := NewFootprintExtent(2, 2)
	yard := []YardCell{0, 0, 0, 0}

	// Anchor 1 with the last covered column and row at width-2 is the largest
	// admitted building rectangle on this map.
	fits, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: fits, Yard: yard}); err != nil {
		t.Fatalf("building footprint ending at width-2 rejected: %v", err)
	}
	// One cell further out covers the map's last column and row: x + fx == 4
	// fails the strict x + fx < mapWidthCells.
	lastRing, _ := NewFootprintRect(NewFootprintAnchor(2, 2), extent)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: lastRing, Yard: yard}); err == nil {
		t.Fatal("building footprint covering the map's last column and row accepted")
	}
	// Column 0 and row 0 are unreachable for a building on either axis.
	for _, anchor := range []FootprintAnchor{NewFootprintAnchor(0, 0), NewFootprintAnchor(0, 1), NewFootprintAnchor(1, 0)} {
		rect, _ := NewFootprintRect(anchor, extent)
		if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard}); err == nil {
			t.Fatalf("building footprint anchored at (%d,%d) accepted", anchor.Cell().X, anchor.Cell().Z)
		}
	}

	// The mobile class keeps its own envelope: the low edge admits cell 0 and
	// the ceiling stays half-open.
	for _, anchor := range []FootprintAnchor{NewFootprintAnchor(0, 0), NewFootprintAnchor(2, 2)} {
		rect, _ := NewFootprintRect(anchor, extent)
		if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true}); err != nil {
			t.Fatalf("mobile footprint anchored at (%d,%d) rejected: %v", anchor.Cell().X, anchor.Cell().Z, err)
		}
	}
	oob, _ := NewFootprintRect(NewFootprintAnchor(3, 3), extent)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: oob, Mobile: true}); err == nil {
		t.Fatal("mobile rectangle extending beyond the map accepted")
	}
}

func TestCheckPlacementAggregatesAndStrictSlope(t *testing.T) {
	ter := legalityTerrain(t, 4, 4, 100)
	// Two sampled cells establish minLow=90 and maxHigh=100. The equal
	// threshold passes; one more unit of span rejects strictly.
	ter.PlotAt(1, 1).SetMinHeight(90)
	ter.PlotAt(1, 1).SetMaxHeight(95)
	ter.PlotAt(2, 1).SetMinHeight(92)
	ter.PlotAt(2, 1).SetMaxHeight(100)
	extent, _ := NewFootprintExtent(2, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	yard := []YardCell{0x08, 0x08} // slope aggregate participation
	rules := PlacementRules{Terrain: true, ProfileResolved: true, MaxSlope: 10, MaxWaterDepth: 255, Waterline: 0}
	result, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Rules: rules})
	if err != nil {
		t.Fatalf("equal slope threshold rejected: %v", err)
	}
	if result.SiteHeight != 90 {
		t.Fatalf("site height %d, want aggregate minimum 90", result.SiteHeight)
	}
	ter.PlotAt(2, 1).SetMaxHeight(101)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Rules: rules}); err == nil || !strings.Contains(err.Error(), "slope") {
		t.Fatalf("strict slope rejection = %v, want slope error", err)
	}
}

// TestCheckPlacementBuildingAndMobileWaterSlopeSelection locks the class split
// of [04 R-P0-08]: the building yard walk compares its rectangle aggregate
// against the land MaxSlope alone — it has no water pair
// [04 R-SLOPE-01 §3 "Bounded census"] — while the mobile validator selects the
// pair PER CELL from that cell's own `hmin >= SeaLevel`
// [04 R-COLL-01 §2 step 5].
//
// The limits below are clamp-consistent: MaxWaterSlope caps MaxSlope at
// compile time [04 §6.1], so the water pair is the looser of the two. This
// test used to assert the opposite, with the mobile pair chosen from the
// rectangle's aggregate water state (DS-WV-02).
func TestCheckPlacementBuildingAndMobileWaterSlopeSelection(t *testing.T) {
	ter := legalityTerrain(t, 4, 1, 100)
	// A land cell: hmin at sea level, slope 8.
	ter.PlotAt(0, 0).SetMinHeight(100)
	ter.PlotAt(0, 0).SetMaxHeight(108)
	// A water cell: hmin below sea level, the same slope 8.
	ter.PlotAt(1, 0).SetMinHeight(92)
	ter.PlotAt(1, 0).SetMaxHeight(100)
	one, _ := NewFootprintExtent(1, 1)
	land, _ := NewFootprintRect(NewFootprintAnchor(0, 0), one)
	water, _ := NewFootprintRect(NewFootprintAnchor(1, 0), one)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 5, MaxWaterSlope: 12, MaxWaterDepth: 255, MinWaterDepth: -10000}

	if _, err := ter.CheckPlacement(PlacementQuery{Rect: land, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("mobile land cell accepted a slope above MaxSlope")
	}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: water, Mobile: true, Rules: rules}); err != nil {
		t.Fatalf("mobile water cell rejected a slope inside MaxWaterSlope: %v", err)
	}
	// The building walk has no water pair, so the same water cell is judged
	// against MaxSlope and rejects.
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: water, Yard: []YardCell{0x08}, Rules: rules}); err == nil {
		t.Fatal("building yard walk selected a water slope pair")
	}
}

func TestCheckPlacementDepthAndHeightPeakStrictness(t *testing.T) {
	ter := legalityTerrain(t, 3, 3, 100)
	extent, _ := NewFootprintExtent(1, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	yard := []YardCell{0x18}
	// MaxWaterDepth rejects only below Sea-MaxWaterDepth; equality passes.
	ter.PlotAt(1, 1).SetMinHeight(90)
	ter.PlotAt(1, 1).SetMaxHeight(90)
	rules := PlacementRules{Terrain: true, ProfileResolved: true, MaxWaterDepth: 10, MaxSlope: 255}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Rules: rules}); err != nil {
		t.Fatalf("depth equality rejected: %v", err)
	}
	ter.PlotAt(1, 1).SetMinHeight(89)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Rules: rules}); err == nil {
		t.Fatal("below water-depth boundary accepted")
	}
	// Bit 4's peak gate is also strict: equality with the site height passes.
	ter.PlotAt(1, 1).SetMinHeight(100)
	ter.PlotAt(1, 1).SetMaxHeight(100)
	rules = PlacementRules{Terrain: true, ProfileResolved: true, MaxSlope: 255, MinWaterDepth: 0, MaxWaterDepth: 255}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: []YardCell{0x18}, Rules: rules}); err != nil {
		t.Fatalf("height peak equality rejected: %v", err)
	}
	// MinWaterDepth accepts equality at Sea-MinWaterDepth and rejects one
	// unit above it. The aggregate uses the larger bit-4 maximum as well.
	ter.PlotAt(1, 1).SetMinHeight(90)
	ter.PlotAt(1, 1).SetMaxHeight(90)
	rules = PlacementRules{ProfileResolved: true, MaxSlope: 255, MaxWaterDepth: 255, MinWaterDepth: 10}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: []YardCell{0x18}, Rules: rules}); err != nil {
		t.Fatalf("minimum depth equality rejected: %v", err)
	}
	ter.PlotAt(1, 1).SetMaxHeight(91)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: []YardCell{0x18}, Rules: rules}); err == nil {
		t.Fatal("minimum depth one-above boundary accepted")
	}
}

func TestCheckPlacementMinWaterDepthZeroAndLandDefault(t *testing.T) {
	ter := legalityTerrain(t, 3, 3, 0)
	ter.PlotAt(1, 1).SetMinHeight(1)
	ter.PlotAt(1, 1).SetMaxHeight(1)
	extent, _ := NewFootprintExtent(1, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 255, MaxWaterSlope: 255, MaxWaterDepth: 10000, MinWaterDepth: 0}
	query := PlacementQuery{Rect: rect, Mobile: true, Rules: rules}
	if _, err := ter.CheckPlacement(query); err == nil || !strings.Contains(err.Error(), "minimum water depth") {
		t.Fatalf("height one above zero upper band accepted: %v", err)
	}

	query.Rules.MinWaterDepth = -10000
	if _, err := ter.CheckPlacement(query); err != nil {
		t.Fatalf("land template no-minimum value rejected: %v", err)
	}
}

func TestCheckPlacementFeatureYardAndMobileModes(t *testing.T) {
	ter := legalityTerrain(t, 3, 3, 100)
	extent, _ := NewFootprintExtent(1, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	ter.FeatureDefs = []*content.FeatureDef{{Blocking: true}}
	ter.PlotAt(1, 1).SetFeature(0)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: []YardCell{0x20}}); err == nil {
		t.Fatal("blocking feature accepted by yard bit 5")
	}
	// Mobile mode has no yard map but still applies feature and occupancy
	// checks to every cell, plus the terrain aggregate.
	ter.PlotAt(1, 1).SetFeature(PlotFeatureNone)
	ter.PlotAt(1, 1).SetOccupantA(7)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true}); err == nil {
		t.Fatal("mobile occupied cell accepted")
	}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Self: 7}); err != nil {
		t.Fatalf("mobile self occupancy rejected: %v", err)
	}
}

func TestPlacementPreviewAndCommitShareCanonicalResult(t *testing.T) {
	// The map is one cell wider and deeper than the rectangle needs: a
	// building anchor is never column or row 0 [05 R-ECO-02 §1].
	ter := legalityTerrain(t, 5, 5, 100)
	extent, _ := NewFootprintExtent(3, 2)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	query := PlacementQuery{Rect: rect, Yard: []YardCell{0x18, 0x18, 0x18, 0x18, 0x18, 0x18}, Rules: PlacementRules{Terrain: true, ProfileResolved: true, MaxSlope: 20, MaxWaterDepth: 255}}
	preview, err := ter.CheckPlacement(query)
	if err != nil {
		t.Fatalf("preview rejected: %v", err)
	}
	commit, err := ter.CheckPlacement(query)
	if err != nil {
		t.Fatalf("commit rejected: %v", err)
	}
	if preview != commit {
		t.Fatalf("preview result %#v differs from commit %#v", preview, commit)
	}
}
