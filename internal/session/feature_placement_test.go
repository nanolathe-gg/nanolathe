package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
)

// featureDefFixture is an authored compiled definition: sprite when filename
// is non-empty, otherwise the centre-referenced class [02 R-MAP-01 §8].
func featureDefFixture(key, filename string, footX, footZ int32) *content.FeatureDef {
	return &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(key)},
		Filename:         filename,
		FootprintX:       footX,
		FootprintZ:       footZ,
		Blocking:         true,
	}
}

func missionFeatureSession(t *testing.T, defs ...*content.FeatureDef) *Session {
	t.Helper()
	terrain := strictMinimalTerrain()
	cat := &content.Catalog{Features: map[string]*content.FeatureDef{}}
	for _, def := range defs {
		cat.Features[def.CanonicalKey] = def
	}
	return &Session{
		Catalog:  cat,
		World:    terrain,
		Features: features.NewService(terrain, nil, nil, nil),
	}
}

func missionWithFeatures(places ...mission.FeaturePlacement) *mission.Mission {
	return &mission.Mission{Features: places}
}

func placementAt(name string, x, z int32) mission.FeaturePlacement {
	return mission.FeaturePlacement{Name: name, X: x, Z: z, RawX: x, RawZ: z}
}

// The authored pair is the feature's CENTRE for a definition that names no
// sprite: the anchor is the authored cell minus half the footprint, so a 2x2
// entry authored at (10,10) anchors at (9,9). A sprite definition anchors at
// the authored cell itself [02 R-MAP-01 §8][05 R-FEAT-01 §3].
func TestMissionFeatureAnchorIsCentreReferencedForNonSprites(t *testing.T) {
	solid := featureDefFixture("wreck", "", 2, 2)
	sprite := featureDefFixture("tree", "trees", 1, 1)
	s := missionFeatureSession(t, solid, sprite)
	m := missionWithFeatures(placementAt("wreck", 10, 10), placementAt("tree", 20, 20))

	if err := stampMissionFeatures(s, m); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if inst := s.Features.InstanceAt(9, 9); inst == nil || inst.Def != solid {
		t.Fatalf("2x2 non-sprite authored at (10,10) is not anchored at (9,9): %#v", inst)
	}
	if s.Features.InstanceAt(10, 10) != nil {
		t.Fatal("the authored cell itself holds the anchor; the half-footprint subtraction did not run")
	}
	if inst := s.Features.InstanceAt(20, 20); inst == nil || inst.Def != sprite {
		t.Fatalf("1x1 sprite authored at (20,20) is not anchored there: %#v", inst)
	}
}

// The halving is a signed division truncating toward zero, so an odd
// footprint loses the half cell rather than rounding up, and a negative
// stored footprint truncates toward zero as well [02 R-MAP-01 §8].
func TestMissionFeatureAnchorHalvingTruncatesTowardZero(t *testing.T) {
	wide := featureDefFixture("wide", "", 3, 3)
	s := missionFeatureSession(t, wide)
	if err := stampMissionFeatures(s, missionWithFeatures(placementAt("wide", 10, 10))); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if s.Features.InstanceAt(9, 9) == nil {
		t.Fatal("3-wide footprint authored at (10,10) must anchor at (9,9): 3/2 truncates to 1")
	}

	negative := featureDefFixture("negative", "", -3, -3)
	if x, z := missionFeatureAnchor(negative, 10, 10); x != 11 || z != 11 {
		t.Fatalf("negative footprint anchor = (%d,%d), want (11,11): -3/2 truncates toward zero to -1", x, z)
	}
}

// The in-bounds test is applied to the SUBTRACTED anchor. An entry authored
// inside the map whose centre-referenced anchor falls off the north-west edge
// is skipped, and the entry after it still places [02 R-MAP-01 §8].
func TestMissionFeatureAnchorBoundsRunAfterSubtraction(t *testing.T) {
	solid := featureDefFixture("wreck", "", 2, 2)
	s := missionFeatureSession(t, solid)
	m := missionWithFeatures(placementAt("wreck", 0, 0), placementAt("wreck", 5, 5))
	if err := stampMissionFeatures(s, m); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if s.Features.InstanceAt(0, 0) != nil {
		t.Fatal("an anchor pushed to (-1,-1) by the subtraction must be skipped, not clamped to (0,0)")
	}
	if s.Features.InstanceAt(4, 4) == nil {
		t.Fatal("a skipped entry must not abort the placements after it")
	}

	// An entry whose authored cell is past the far edge can still produce an
	// in-bounds anchor: the raw pair is never what the test reads. The stamp's
	// own far-edge bound then decides, which is why the helper — not the loop
	// — is what this asserts.
	if x, z := missionFeatureAnchor(solid, s.World.CellW, s.World.CellH); x != s.World.CellW-1 || z != s.World.CellH-1 {
		t.Fatalf("anchor for an authored cell one past the edge = (%d,%d), want (%d,%d)", x, z, s.World.CellW-1, s.World.CellH-1)
	}
}

// Both battle entries run one implementation, so the anchor rule cannot drift
// between a campaign and a skirmish start [02 R-MAP-01 §8].
func TestCampaignAndSkirmishFeaturePassesAgree(t *testing.T) {
	solid := featureDefFixture("wreck", "", 2, 2)
	m := missionWithFeatures(placementAt("wreck", 10, 10))

	campaign := missionFeatureSession(t, solid)
	skirmish := missionFeatureSession(t, solid)
	if err := placeFeatures(campaign, m); err != nil {
		t.Fatalf("campaign pass: %v", err)
	}
	if err := skirmishPlaceFeatures(skirmish, m); err != nil {
		t.Fatalf("skirmish pass: %v", err)
	}
	for cz := int32(0); cz < campaign.World.CellH; cz++ {
		for cx := int32(0); cx < campaign.World.CellW; cx++ {
			a := campaign.World.PlotAt(cx, cz)
			b := skirmish.World.PlotAt(cx, cz)
			if a.Feature() != b.Feature() {
				t.Fatalf("cell (%d,%d): campaign feature word %#x, skirmish %#x", cx, cz, a.Feature(), b.Feature())
			}
		}
	}
	if campaign.Features.InstanceAt(9, 9) == nil {
		t.Fatal("campaign pass placed nothing at the centre-referenced anchor")
	}
}
