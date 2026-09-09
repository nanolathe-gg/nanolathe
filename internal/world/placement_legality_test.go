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

func TestCheckPlacementBoundsAndHalfOpenRect(t *testing.T) {
	ter := legalityTerrain(t, 4, 4, 100)
	extent, _ := NewFootprintExtent(2, 2)
	inside, _ := NewFootprintRect(NewFootprintAnchor(2, 2), extent)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: inside, Yard: []YardCell{0, 0, 0, 0}}); err != nil {
		t.Fatalf("edge-touching half-open rectangle rejected: %v", err)
	}
	oob, _ := NewFootprintRect(NewFootprintAnchor(3, 3), extent)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: oob, Yard: []YardCell{0, 0, 0, 0}}); err == nil {
		t.Fatal("rectangle extending beyond map accepted")
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

func TestCheckPlacementBuildingAndMobileWaterSlopeSelection(t *testing.T) {
	ter := legalityTerrain(t, 3, 1, 100)
	ter.PlotAt(0, 0).SetMinHeight(90)
	ter.PlotAt(0, 0).SetMaxHeight(95)
	ter.PlotAt(1, 0).SetMinHeight(92)
	ter.PlotAt(1, 0).SetMaxHeight(100)
	extent, _ := NewFootprintExtent(2, 1)
	rect, _ := NewFootprintRect(NewFootprintAnchor(0, 0), extent)
	rules := PlacementRules{ProfileResolved: true, MaxSlope: 10, MaxWaterSlope: 5, MaxWaterDepth: 255}
	// The building aggregate uses MaxSlope even when the footprint is over
	// water; only mobile placement selects MaxWaterSlope.
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: []YardCell{0x08, 0x08}, Rules: rules}); err != nil {
		t.Fatalf("building MaxSlope path rejected: %v", err)
	}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Mobile: true, Rules: rules}); err == nil {
		t.Fatal("mobile MaxWaterSlope path accepted the stricter water slope")
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
	ter := legalityTerrain(t, 4, 4, 100)
	extent, _ := NewFootprintExtent(3, 2)
	rect, _ := NewFootprintRect(NewFootprintAnchor(0, 1), extent)
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
