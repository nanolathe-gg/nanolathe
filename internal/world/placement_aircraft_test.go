package world

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func TestPlacementRulesClasslessAircraftDoesNotNeedGroundProfile(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armfig"},
		UnitName:         "armfig",
		BMCode:           true,
		CanFly:           true,
	}
	rules, err := PlacementRulesForUnit(&content.Catalog{Movement: map[string]*content.MovementClass{}}, def)
	if err != nil {
		t.Fatalf("class-less aircraft rejected: %v", err)
	}
	if rules.Domain != content.MobilityAircraft || !rules.ProfileResolved {
		t.Fatalf("rules=%+v, want aircraft with resolved admission domain", rules)
	}
}

func TestPlacementRulesClasslessGroundIsPermanentError(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "broken"},
		UnitName:         "broken",
		BMCode:           true,
	}
	_, err := PlacementRulesForUnit(&content.Catalog{Movement: map[string]*content.MovementClass{}}, def)
	if !errors.Is(err, ErrUnclassifiedMobile) {
		t.Fatalf("error=%v, want ErrUnclassifiedMobile", err)
	}
}

func TestAircraftPlacementSkipsGroundAggregatesButChecksBounds(t *testing.T) {
	terrain := &Terrain{CellW: 1, CellH: 1, SeaLevel: 0, Plot: make([]PlotCell, 1)}
	terrain.Plot[0].SetFeature(PlotFeatureNone)
	terrain.Plot[0].SetMinHeight(0)
	terrain.Plot[0].SetMaxHeight(255)
	extent, err := NewFootprintExtent(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	rect, err := NewFootprintRect(NewFootprintAnchor(0, 0), extent)
	if err != nil {
		t.Fatal(err)
	}
	rules := PlacementRules{Domain: content.MobilityAircraft, ProfileResolved: true}
	if _, err := terrain.CheckPlacement(PlacementQuery{Rect: rect, Rules: rules, Mobile: true}); err != nil {
		t.Fatalf("aircraft should not use ground slope aggregate: %v", err)
	}
	outside, _ := NewFootprintRect(NewFootprintAnchor(1, 0), extent)
	if _, err := terrain.CheckPlacement(PlacementQuery{Rect: outside, Rules: rules, Mobile: true}); err == nil {
		t.Fatal("aircraft out-of-bounds placement unexpectedly accepted")
	}
}
