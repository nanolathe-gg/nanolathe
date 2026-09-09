package ai

import (
	"errors"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func placementTerrain(w, h int32, metal uint8) *world.Terrain {
	t := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, w*h)}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i][7] = metal
	}
	return t
}

func placementCatalog(key, yard string, extractor float64) *content.Catalog {
	return &content.Catalog{Units: map[string]*content.UnitDef{
		key: {
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: key},
			UnitName:         key,
			FootprintX:       2,
			FootprintZ:       2,
			YardMap:          yard,
			ExtractsMetal:    extractor,
		},
	}}
}

func makePlacementManager(cat *content.Catalog, terrain *world.Terrain, metal int32) *Manager {
	r := rng.NewSimulation(7)
	m := &Manager{
		Catalog:      cat,
		Terrain:      terrain,
		RNG:          &r,
		Factory:      &units.Unit{Handle: 7},
		SurfaceMetal: metal,
	}
	if terrain != nil {
		m.Strategic.CenterX = world.CellToWorld(terrain.CellW / 2)
		m.Strategic.CenterZ = world.CellToWorld(terrain.CellH / 2)
		m.Factory.X = m.Strategic.CenterX
		m.Factory.Z = m.Strategic.CenterZ
		m.OriginX = m.Strategic.CenterX
		m.OriginZ = m.Strategic.CenterZ
	}
	// Deterministic constructor products; InitializeRandomState itself is
	// covered in strategic/runtime RNG tests.
	m.Strategic.setupDrawsReady = true
	m.Strategic.LandRegion = PlacementRegion{CellW: 20, CellH: 20}
	m.Strategic.WaterRegion = PlacementRegion{CellW: 20, CellH: 20}
	m.QueueBuildTyped = func(BuildRequest) error { return nil }
	return m
}

func TestPlacementSelectorStrictBoundaryAndNoFallthrough(t *testing.T) {
	cat := placementCatalog("armmex", "oooo", 0.001)
	ter := placementTerrain(16, 16, 0)
	// A positive selector draw with surfaceMetal zero chooses the exhaustive
	// helper. Its empty battle-entry vector fails without falling through to B.
	m := makePlacementManager(cat, ter, 0)
	res := PlaceWithResult(m, "armmex", ter)
	if res.Valid || res.Helper != HelperA || res.Reason != ReasonNoPatchData {
		t.Fatalf("surfaceMetal < draw must select failing A without fallthrough: %+v", res)
	}
	if m.RNG.Draws() != 1 {
		t.Fatalf("A selector consumes one draw, got %d", m.RNG.Draws())
	}

	// Find a deterministic seed whose first bounded draw equals 50. Equality
	// belongs to B because the selector comparison is strict.
	var seed uint32
	for s := uint32(0); s < 100000; s++ {
		r := rng.NewSimulation(s)
		if r.Uint32n(255) == 50 {
			seed = s
			break
		}
	}
	r := rng.NewSimulation(seed)
	m = makePlacementManager(cat, ter, 50)
	m.RNG = &r
	res = PlaceWithResult(m, "armmex", ter)
	if res.Helper != HelperB {
		t.Fatalf("surfaceMetal == draw must select B: %+v", res)
	}
	if m.RNG.Draws() < 5 {
		t.Fatalf("selector plus first scatter trial must consume at least five draws, got %d", m.RNG.Draws())
	}
}

func TestScatterHelperDrawCensus(t *testing.T) {
	tests := []struct {
		name       string
		footX      int32
		footZ      int32
		wantPerTry uint64
	}{
		{"four", 2, 2, 4},
		{"three", 13, 2, 3},
		{"two", 13, 13, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cat := placementCatalog("armsolar", "o", 0)
			def := cat.Units["armsolar"]
			def.FootprintX, def.FootprintZ = tc.footX, tc.footZ
			terrain := placementTerrain(8, 8, 0)
			for i := range terrain.Plot {
				terrain.Plot[i].SetOccupantA(1) // force all in-bounds trials to fail
			}
			m := makePlacementManager(cat, terrain, 0)
			before := m.RNG.Draws()
			res := PlaceWithResult(m, "armsolar", terrain)
			if res.Valid || res.Helper != HelperB || res.Reason != ReasonTooManyTrials || res.Attempts != 30 {
				t.Fatalf("scatter result: %+v", res)
			}
			if got := m.RNG.Draws() - before; got != tc.wantPerTry*30 {
				t.Fatalf("draws=%d, want %d per trial x30", got, tc.wantPerTry)
			}
		})
	}
}

func TestPlacementMetalScoreLimit(t *testing.T) {
	ter := placementTerrain(8, 8, 1)
	extent, err := world.NewFootprintExtent(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	anchor := world.NewFootprintAnchor(2, 2)
	rect, err := world.NewFootprintRect(anchor, extent)
	if err != nil {
		t.Fatal(err)
	}
	score, err := placementMetalScore(ter, rect)
	if err != nil {
		t.Fatal(err)
	}
	if score != 4 {
		t.Fatalf("score arithmetic got %d, want 4", score)
	}
	limit := int64(1 * 2 * 2 * 2)
	if score > limit {
		t.Fatalf("equal score must satisfy limit: score=%d limit=%d", score, limit)
	}
	for i := range ter.Plot {
		ter.Plot[i][7] = 3
	}
	score, err = placementMetalScore(ter, rect)
	if err != nil {
		t.Fatal(err)
	}
	if score != 12 || score <= limit {
		t.Fatalf("score above limit arithmetic got score=%d limit=%d", score, limit)
	}
}

func TestPlacementRejectsMissingDependencies(t *testing.T) {
	cat := placementCatalog("armsolar", "oooo", 0)
	ter := placementTerrain(8, 8, 0)
	cases := []struct {
		name string
		m    *Manager
		want ReasonCode
	}{
		{"terrain", makePlacementManager(cat, nil, 0), ReasonMissingTerrain},
		{"catalog", makePlacementManager(nil, ter, 0), ReasonMissingCatalog},
		{"rng", makePlacementManager(cat, ter, 0), ReasonMissingRNG},
		{"builder", makePlacementManager(cat, ter, 0), ReasonMissingBuilder},
		{"queue", makePlacementManager(cat, ter, 0), ReasonMissingQueue},
	}
	cases[2].m.RNG = nil
	cases[3].m.Factory = nil
	cases[4].m.QueueBuildTyped = nil
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := PlaceWithResult(tc.m, "armsolar", tc.m.Terrain)
			if res.Valid || res.Reason != tc.want || res.Proof == nil {
				t.Fatalf("missing dependency result: %+v", res)
			}
			if tc.m.RNG != nil && tc.m.RNG.Draws() != 0 {
				t.Fatalf("missing %s dependency must consume zero trial draws, got %d", tc.name, tc.m.RNG.Draws())
			}
		})
	}

	m := makePlacementManager(placementCatalog("armsolar", "", 0), ter, 0)
	res := PlaceWithResult(m, "armsolar", ter)
	if res.Valid || res.Reason != ReasonMissingDefinition {
		t.Fatalf("missing building yard must reject explicitly: %+v", res)
	}
	bad := placementCatalog("armsolar", "oooo", 0)
	bad.Units["armsolar"].FootprintX = 0
	m = makePlacementManager(bad, ter, 0)
	res = PlaceWithResult(m, "armsolar", ter)
	if res.Valid || res.Reason != ReasonInvalidFootprint {
		t.Fatalf("invalid footprint must reject explicitly: %+v", res)
	}
	missingRules := placementCatalog("armsolar", "oooo", 0)
	missingRules.Units["armsolar"].BMCode = 1
	m = makePlacementManager(missingRules, ter, 0)
	res = PlaceWithResult(m, "armsolar", ter)
	if res.Valid || res.Reason != ReasonMissingDefinition || res.Proof == nil {
		t.Fatalf("unresolved placement profile must reject explicitly: %+v", res)
	}
	if m.RNG.Draws() != 0 {
		t.Fatalf("unresolved placement profile must consume zero trial draws, got %d", m.RNG.Draws())
	}
}

func TestPlacementPropagatesTypedMobileRequest(t *testing.T) {
	cat := placementCatalog("armsolar", "oooo", 0)
	m := makePlacementManager(cat, placementTerrain(16, 16, 0), 1)
	var got BuildRequest
	m.QueueBuildTyped = func(req BuildRequest) error {
		got = req
		return nil
	}
	res := PlacementResult{
		Valid:  true,
		WorldX: placementWorldCoordinate(3, 2),
		WorldZ: placementWorldCoordinate(4, 3),
	}
	if err := queueExactResult(m, "armsolar", res); err != nil {
		t.Fatalf("typed queue failed: %v", err)
	}
	if got.Kind != BuildKindMobileSite || got.Builder != m.Factory.Handle || got.UnitKey != "armsolar" || got.Count != 1 {
		t.Fatalf("typed request boundary lost fields: %+v", got)
	}
	if got.X != res.WorldX || got.Z != res.WorldZ {
		t.Fatalf("typed request site %d,%d != result %d,%d", got.X, got.Z, res.WorldX, res.WorldZ)
	}
}

func TestPlacementPropagatesTypedQueueError(t *testing.T) {
	cat := placementCatalog("armsolar", "oooo", 0)
	m := makePlacementManager(cat, placementTerrain(16, 16, 0), 1)
	want := errors.New("ordinary queue rejected request")
	m.QueueBuildTyped = func(BuildRequest) error { return want }
	res := PlacementResult{
		Valid:  true,
		WorldX: placementWorldCoordinate(3, 2),
		WorldZ: placementWorldCoordinate(4, 3),
	}
	if err := queueExactResult(m, "armsolar", res); !errors.Is(err, want) {
		t.Fatalf("queue error must cross typed boundary: %v", err)
	}
}

func TestStepTowardCenterUsesFixedPointTruncation(t *testing.T) {
	x, z := stepTowardCenter(numeric.FixedFromInt(0), 0, numeric.FixedFromInt(100), 0, numeric.FixedFromInt(10))
	dist := int64(numeric.FixedFromInt(100))
	scale := (int64(10) << 32) / dist
	wantX := numeric.Fixed((int64(numeric.FixedFromInt(100)) * scale) >> 16)
	if x != wantX || z != 0 {
		t.Fatalf("fixture adapter got %d,%d, want %d,0", x, z, wantX)
	}
}

func TestPlacementWorldCoordinateUsesFootprintMidpoint(t *testing.T) {
	tests := []struct {
		cell, footprint int32
		want            numeric.Fixed
	}{
		{3, 2, numeric.Fixed((2 + 2*3) << 19)},
		{3, 3, numeric.Fixed((3 + 2*3) << 19)},
		{4, 5, numeric.Fixed((5 + 2*4) << 19)},
	}
	for _, tc := range tests {
		if got := placementWorldCoordinate(tc.cell, tc.footprint); got != tc.want {
			t.Errorf("cell=%d footprint=%d got %d want %d", tc.cell, tc.footprint, got, tc.want)
		}
	}
}
