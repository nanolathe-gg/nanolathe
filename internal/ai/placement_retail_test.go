package ai

import (
	"errors"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestBuildMetalSpotsRowMajorAndFilters(t *testing.T) {
	terrain := placementTerrain(4, 3, 0)
	terrain.FeatureDefs = []*content.FeatureDef{
		{Metal: 0x10001, Indestructible: true},
		{Metal: 7, Indestructible: false},
		{Metal: 0, Indestructible: true},
	}
	terrain.PlotAt(3, 0).SetFeature(0)
	terrain.PlotAt(0, 1).SetFeature(1)
	terrain.PlotAt(1, 1).SetFeature(world.PlotFeatureFringe)
	terrain.PlotAt(2, 1).SetFeature(2)
	terrain.PlotAt(1, 2).SetFeature(0)
	got := BuildMetalSpots(terrain)
	want := []MetalSpot{{CellX: 3, CellZ: 0, Metal: 1}, {CellX: 1, CellZ: 2, Metal: 1}}
	if len(got) != len(want) {
		t.Fatalf("spots=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("spot %d=%+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMetalSpotHeapTieMechanics(t *testing.T) {
	h := []MetalSpot{{CellX: 1}, {CellX: 2}, {CellX: 3}, {CellX: 4}}
	makeMetalSpotHeap(h)
	var got []int16
	for len(h) != 0 {
		got = append(got, popMetalSpot(&h).CellX)
	}
	want := []int16{3, 1, 4, 2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("equal-key pop order=%v, want %v", got, want)
		}
	}
}

func TestExhaustiveEarlyStopUsesSquaredSlack(t *testing.T) {
	cat := placementCatalog("armmex", "o", 0.001)
	def := cat.Units["armmex"]
	def.FootprintX, def.FootprintZ = 3, 3
	terrain := placementTerrain(64, 64, 0)
	for z := int32(20); z < 23; z++ {
		for x := int32(20); x < 23; x++ {
			terrain.PlotAt(x, z)[7] = 1
		}
	}
	for z := int32(20); z < 23; z++ {
		for x := int32(33); x < 36; x++ {
			terrain.PlotAt(x, z)[7] = 9
		}
	}
	m := makePlacementManager(cat, terrain, 0)
	m.Strategic.MetalSpots = []MetalSpot{{CellX: 20, CellZ: 20}, {CellX: 33, CellZ: 20}}
	pd, failure := resolveRetailPlacementDef(m, "armmex")
	if failure.Proof != nil {
		t.Fatal(failure.Proof)
	}
	origin := retailPlacementPoint{x: placementWorldCoordinate(20, 3), z: placementWorldCoordinate(20, 3)}
	res := retailExtractorHelperA(m, pd, origin, 160, terrain)
	if !res.Valid || res.CellX != 20 || res.CellZ != 20 || res.Score != 9 || res.Attempts != 1 {
		t.Fatalf("squared-slack result=%+v", res)
	}
	if m.RNG.Draws() != 0 {
		t.Fatalf("exhaustive helper consumed %d draws", m.RNG.Draws())
	}
}

func TestPlacementRadiusUsesWorldUnitsAndPersistsFailure(t *testing.T) {
	cat := placementCatalog("armmex", "o", 0.001)
	terrain := placementTerrain(16, 16, 0) // larger world extent is 256
	m := makePlacementManager(cat, terrain, -1)
	for i, want := range []int32{160, 320, 320} {
		res := PlaceWithResult(m, "armmex", terrain)
		if res.Valid || res.Helper != HelperA || res.Reason != ReasonNoPatchData {
			t.Fatalf("attempt %d=%+v", i, res)
		}
		if m.Strategic.Radius != want {
			t.Fatalf("attempt %d radius=%d, want %d world units", i, m.Strategic.Radius, want)
		}
	}
}

func TestPlacementDeterministicCandidateAndRNG(t *testing.T) {
	cat := placementCatalog("armsolar", "o", 0)
	terrain := placementTerrain(64, 64, 0)
	makeRun := func() (PlacementResult, uint32, uint64) {
		m := makePlacementManager(cat, terrain, 0)
		r := rng.NewSimulation(991)
		m.RNG = &r
		res := PlaceWithResult(m, "armsolar", terrain)
		return res, r.State, r.Draws()
	}
	a, stateA, drawsA := makeRun()
	b, stateB, drawsB := makeRun()
	if !a.Valid || !b.Valid {
		t.Fatalf("deterministic fixture did not place: a=%+v b=%+v", a, b)
	}
	if a.CellX != b.CellX || a.CellZ != b.CellZ || a.Attempts != b.Attempts || stateA != stateB || drawsA != drawsB {
		t.Fatalf("runs differ: a=%+v state=%d/%d b=%+v state=%d/%d", a, stateA, drawsA, b, stateB, drawsB)
	}
	if a.WorldX != placementWorldCoordinate(a.CellX, int32(a.FootX)) || a.WorldZ != placementWorldCoordinate(a.CellZ, int32(a.FootZ)) {
		t.Fatalf("world output was not footprint-centre quantized: %+v", a)
	}
}

func TestScatterLatticeNarrowsToSignedWord(t *testing.T) {
	tests := []struct {
		name          string
		q, draw, want int32
		cell, offset  int16
	}{
		{name: "positive overflow wraps", q: 32760, cell: 20, offset: 10, draw: 0, want: -32766},
		{name: "negative division truncates toward zero", q: -21, cell: 20, offset: 1, draw: 2, want: -17},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retailScatterCell(tt.q, tt.cell, tt.offset, tt.draw); got != tt.want {
				t.Fatalf("retailScatterCell(%d,%d,%d,%d)=%d, want %d", tt.q, tt.cell, tt.offset, tt.draw, got, tt.want)
			}
		})
	}
}

// TestPlacementValidatorDispatchesOnBMCodeInBothModes locks the corrected
// dispatch of [08 R-AI-03 §4]: both helpers send a `bmcode == 0` definition
// (every building) to the yard-map blocker and a `bmcode != 0` definition
// (mobile) to the plain footprint walk. The two class labels in that section
// were inverted when first written; the previous form of this test asserted
// that the scatter helper never read the authored yard, which was the inverted
// reading [08 R-AI-03 §4 correction][08 R-AI-03 §7.4].
func TestPlacementValidatorDispatchesOnBMCodeInBothModes(t *testing.T) {
	cat := placementCatalog("armgeo", "GGGG", 0)
	terrain := placementTerrain(16, 16, 0)
	m := makePlacementManager(cat, terrain, 0)
	pd, failure := resolveRetailPlacementDef(m, "armgeo")
	if failure.Proof != nil {
		t.Fatal(failure.Proof)
	}

	// A building: the geothermal requirement of the authored yard bites in
	// both modes, because both run the yard blocker.
	for _, exhaustive := range []bool{true, false} {
		if _, _, reason, err := validateRetailAICandidate(terrain, pd, 2, 2, exhaustive); err == nil || reason != ReasonBlocked {
			t.Fatalf("exhaustive=%v accepted missing geothermal: reason=%v err=%v", exhaustive, reason, err)
		}
	}

	// A mobile definition walks the plain footprint in both modes: no yard
	// bytes, so the same site is legal.
	mobileCat := placementCatalog("armpw", "GGGG", 0)
	mobileCat.Units["armpw"].BMCode = 1
	mobileCat.Units["armpw"].CanFly = true // any compiled mobility domain; the yard is what is under test
	mm := makePlacementManager(mobileCat, terrain, 0)
	mpd, mfailure := resolveRetailPlacementDef(mm, "armpw")
	if mfailure.Proof != nil {
		t.Fatal(mfailure.Proof)
	}
	for _, exhaustive := range []bool{true, false} {
		if _, _, reason, err := validateRetailAICandidate(terrain, mpd, 2, 2, exhaustive); err != nil || reason != ReasonSuccess {
			t.Fatalf("mobile exhaustive=%v read authored yard: reason=%v err=%v", exhaustive, reason, err)
		}
	}

	occupied := placementTerrain(16, 16, 0)
	occupied.PlotAt(2, 2).SetOccupantA(7)
	if _, _, reason, err := validateRetailAICandidate(occupied, pd, 2, 2, false); err == nil || reason != ReasonBlocked {
		t.Fatalf("scatter accepted occupied cell: reason=%v err=%v", reason, err)
	}

	// Yard bit 5 is the blocking-feature test; 'G' does not carry it, so the
	// blocking rejection is asserted through a yard that does ('o' = 0x2f).
	blockedCat := placementCatalog("armsolar", "oooo", 0)
	bm := makePlacementManager(blockedCat, terrain, 0)
	bpd, bfailure := resolveRetailPlacementDef(bm, "armsolar")
	if bfailure.Proof != nil {
		t.Fatal(bfailure.Proof)
	}
	blocked := placementTerrain(16, 16, 0)
	blocked.FeatureDefs = []*content.FeatureDef{{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "rock"}, Blocking: true}}
	blocked.PlotAt(2, 2).SetFeature(0)
	if _, _, reason, err := validateRetailAICandidate(blocked, bpd, 2, 2, false); err == nil || reason != ReasonBlocked {
		t.Fatalf("scatter accepted blocking feature: reason=%v err=%v", reason, err)
	}

	depth := placementTerrain(16, 16, 0)
	depth.SeaLevel = 10
	bpd.rules.MaxWaterDepth = 5
	if _, _, reason, err := validateRetailAICandidate(depth, bpd, 2, 2, false); err == nil || reason != ReasonBlocked {
		t.Fatalf("scatter accepted excessive depth: reason=%v err=%v", reason, err)
	}

	slope := placementTerrain(16, 16, 0)
	slope.PlotAt(2, 2)[5], slope.PlotAt(2, 2)[6] = 10, 0
	bpd.rules.MaxWaterDepth = 0
	if _, _, reason, err := validateRetailAICandidate(slope, bpd, 2, 2, false); err == nil || reason != ReasonBlocked {
		t.Fatalf("scatter accepted excessive slope: reason=%v err=%v", reason, err)
	}
}

// TestExhaustiveCandidateRejectsRowZeroLikeColumnZero locks [08 R-AI-03 §7.1]:
// the blocker's row test rejects row 0 exactly as its column test rejects
// column 0, and Nanolathe additionally rejects a negative row as its own
// deterministic choice (retail reads before the grid there; Unknown whether
// that faults).
func TestExhaustiveCandidateRejectsRowZeroLikeColumnZero(t *testing.T) {
	cat := placementCatalog("armsolar", "oooo", 0)
	terrain := placementTerrain(16, 16, 0)
	m := makePlacementManager(cat, terrain, 0)
	pd, failure := resolveRetailPlacementDef(m, "armsolar")
	if failure.Proof != nil {
		t.Fatal(failure.Proof)
	}
	for _, tt := range []struct{ x, z int32 }{{2, 0}, {0, 2}, {2, -1}} {
		if _, _, reason, err := validateRetailAICandidate(terrain, pd, tt.x, tt.z, true); err == nil || reason != ReasonOutOfBounds {
			t.Fatalf("exhaustive accepted candidate %d,%d: reason=%v err=%v", tt.x, tt.z, reason, err)
		}
	}
	// Row 0 is rejected only by the exhaustive blocker's entry test; the
	// scatter helper's bounds test admits it.
	if _, _, reason, err := validateRetailAICandidate(terrain, pd, 2, 0, false); err != nil || reason != ReasonSuccess {
		t.Fatalf("scatter rejected row 0: reason=%v err=%v", reason, err)
	}
}

func TestScatterEnforcesMinWaterDepthZeroUpperBand(t *testing.T) {
	cat := placementCatalog("armsolar", "oooo", 0)
	terrain := placementTerrain(16, 16, 0)
	for i := range terrain.Plot {
		terrain.Plot[i][5], terrain.Plot[i][6] = 1, 1
	}
	m := makePlacementManager(cat, terrain, 0)
	pd, failure := resolveRetailPlacementDef(m, "armsolar")
	if failure.Proof != nil {
		t.Fatal(failure.Proof)
	}
	if _, _, reason, err := validateRetailAICandidate(terrain, pd, 2, 2, false); err == nil || reason != ReasonBlocked {
		t.Fatalf("scatter accepted height above zero upper band: reason=%v err=%v", reason, err)
	}
}

func TestScatterScoreIsTheTrialFootprintSumAndInclusive(t *testing.T) {
	cat := placementCatalog("armsolar", "o", 0)
	terrain := placementTerrain(64, 64, 1)
	run := func(seed uint32) PlacementResult {
		m := makePlacementManager(cat, terrain, 1) // limit 8, footprint sum 4
		r := rng.NewSimulation(seed)
		m.RNG = &r
		return PlaceWithResult(m, "armsolar", terrain)
	}
	first := run(44)
	// The limit test compares the valid trial's own footprint metal-byte sum,
	// which the yard blocker leaves in the accumulator on entry — retail's
	// test, not a divergence from it [08 R-AI-03 §4 correction]. There is no
	// process-global writer that could couple this run to another player.
	second := run(44)
	if !first.Valid || !second.Valid || first.Score != 4 || second.Score != 4 || first.CellX != second.CellX || first.CellZ != second.CellZ {
		t.Fatalf("session-local inclusive score differs: first=%+v second=%+v", first, second)
	}
}

func TestPlacementSuccessResetsRadiusBeforeQueueFailure(t *testing.T) {
	cat := placementCatalog("armsolar", "o", 0)
	terrain := placementTerrain(64, 64, 0)
	m := makePlacementManager(cat, terrain, 0)
	m.Strategic.Radius = 320
	m.QueueBuildTyped = func(BuildRequest) error { return errors.New("fixture queue rejection") }
	res := PlaceWithResult(m, "armsolar", terrain)
	if res.Valid || res.Reason != ReasonQueueFailed {
		t.Fatalf("queue failure result=%+v", res)
	}
	if m.Strategic.Radius != 0 {
		t.Fatalf("root success restored radius to %d after caller failure", m.Strategic.Radius)
	}
}

func TestPlacementOriginIncludesYAndExactBoundary(t *testing.T) {
	builder := retailPlacementPoint{}
	center := retailPlacementPoint{x: numeric.FixedFromInt(300), y: numeric.FixedFromInt(400)}
	got := retailPlacementOrigin(builder, center, 160)
	dist := int64(numeric.FixedFromInt(500))
	scale := (int64(160) << 32) / dist
	wantX := numeric.Fixed((int64(center.x) * scale) >> 16)
	wantY := numeric.Fixed((int64(center.y) * scale) >> 16)
	if got.x != wantX || got.y != wantY || got.z != 0 || got.y == 0 {
		t.Fatalf("three-axis origin=%+v, want %d,%d,0", got, wantX, wantY)
	}
	if at := retailPlacementOrigin(builder, center, 500); at != center {
		t.Fatalf("distance equal to radius must choose centre: %+v", at)
	}
}

func TestPlacementSignedWordOverflowBoundariesDoNotDraw(t *testing.T) {
	r := rng.NewSimulation(31337)
	wantState, wantDraws := r.State, r.Draws()

	builderWord := int32(0x7ffeffff)
	centerWord := int32(uint32(builderWord) + uint32(4<<16))
	wantOrigin := int32(uint32(builderWord) + uint32(2<<16))
	got := retailPlacementOrigin(
		retailPlacementPoint{x: numeric.Fixed(builderWord)},
		retailPlacementPoint{x: numeric.Fixed(centerWord)},
		2,
	)
	if got.x != numeric.Fixed(wantOrigin) || got.x >= 0 {
		t.Fatalf("overflowing origin x=%d, want signed word %d", got.x, wantOrigin)
	}

	cell, footprint := int32(4095), int32(3)
	word := footprint + 2*cell
	wantWorld := numeric.Fixed(word << 19)
	if gotWorld := placementWorldCoordinate(cell, footprint); gotWorld != wantWorld || gotWorld <= 0 {
		t.Fatalf("overflowing world output=%d, want signed word %d", gotWorld, wantWorld)
	}

	if gotCell := retailOriginCell(numeric.Fixed(int64(1)<<31), 1); gotCell != -2048 {
		t.Fatalf("origin cell from narrowed 0x80000000 word=%d, want -2048", gotCell)
	}
	if r.State != wantState || r.Draws() != wantDraws {
		t.Fatalf("pure overflow arithmetic changed RNG state/draws: state=%d/%d draws=%d/%d", r.State, wantState, r.Draws(), wantDraws)
	}
}

func TestPlacementRepresentativeRetailAssetsGuarded(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount retail assets: %v", err)
	}
	defer fs.Close()
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("compile retail catalog: %v", err)
	}
	terrain, err := world.Load(fs, cat, "ashap plateau")
	if err != nil {
		t.Fatalf("load ashap plateau: %v", err)
	}
	mh := cat.Maps[content.CanonicalKey("ashap plateau")]
	if mh == nil || len(mh.Schemas) == 0 {
		t.Fatal("ashap plateau has no schema")
	}
	if err := terrain.ApplySchema(mh, 0); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	type representative struct {
		name string
		key  string
	}
	reps := map[string]string{}
	for _, key := range cat.SortedUnitKeys() {
		def := cat.Units[key]
		if def == nil || (def.BMCode == 0 && def.YardMap == "") {
			continue
		}
		rules, err := world.PlacementRulesForUnit(cat, def)
		if err != nil {
			continue
		}
		if def.BMCode == 0 && def.ExtractsMetal != 0 && reps["extractor"] == "" {
			reps["extractor"] = key
		}
		if def.BMCode == 0 && def.ExtractsMetal == 0 && reps["non-extractor"] == "" {
			reps["non-extractor"] = key
		}
		if rules.MinWaterDepth < 0 && reps["land"] == "" {
			reps["land"] = key
		}
		if rules.MinWaterDepth >= 0 && reps["water-capable"] == "" {
			reps["water-capable"] = key
		}
	}
	ordered := []representative{{"land", reps["land"]}, {"water-capable", reps["water-capable"]}, {"extractor", reps["extractor"]}, {"non-extractor", reps["non-extractor"]}}
	for _, rep := range ordered {
		t.Run(rep.name, func(t *testing.T) {
			if rep.key == "" {
				t.Skipf("retail catalog has no %s structure fixture", rep.name)
			}
			run := func() (PlacementResult, uint32, uint64) {
				m := makePlacementManager(cat, terrain, mh.Schemas[0].SurfaceMetal)
				r := rng.NewSimulation(700 + uint32(len(rep.name)))
				m.RNG = &r
				m.Strategic.setupDrawsReady = false
				if !m.Strategic.InitializeRandomState(&r) {
					t.Fatal("strategic region initialization failed")
				}
				m.Strategic.InitializeMetalSpots(terrain)
				res := PlaceWithResult(m, rep.key, terrain)
				return res, r.State, r.Draws()
			}
			a, stateA, drawsA := run()
			b, stateB, drawsB := run()
			if a.Helper != b.Helper || a.Reason != b.Reason || a.CellX != b.CellX || a.CellZ != b.CellZ || a.Attempts != b.Attempts || stateA != stateB || drawsA != drawsB {
				t.Fatalf("asset-backed runs differ: a=%+v state=%d/%d b=%+v state=%d/%d", a, stateA, drawsA, b, stateB, drawsB)
			}
		})
	}
}
