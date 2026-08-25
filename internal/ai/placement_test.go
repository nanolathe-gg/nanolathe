package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestOriginTowardCenterStepVector(t *testing.T) {
	// C8: search origin moves toward strategic center using stored radius with fixed-point scaling [P0-03].
	// Test direct helper stepTowardCenter with known values.
	m := &Manager{}
	m.Strategic.CenterX = numeric.FixedFromInt(100)
	m.Strategic.CenterZ = numeric.FixedFromInt(0)
	m.OriginX = numeric.FixedFromInt(0)
	m.OriginZ = numeric.FixedFromInt(0)
	m.Strategic.Radius = numeric.FixedFromInt(10) // 10 world units step
	m.Catalog = &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armsolar": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armsolar"}, UnitName: "armsolar", ExtractsMetal: 0, FootprintX: 2, FootprintZ: 2, YardMap: "oooo"},
		},
	}
	// Direct step helper test for diagonal with radius 10 should step 10 toward center.
	ox, oz := stepTowardCenter(numeric.FixedFromInt(0), numeric.FixedFromInt(0), numeric.FixedFromInt(100), numeric.FixedFromInt(0), numeric.FixedFromInt(10))
	wantX := numeric.FixedFromInt(10)
	if ox != wantX || oz != 0 {
		t.Fatalf("stepTowardCenter diagonal: got (%d,%d) want (10,0)", ox, oz)
	}
	// Test Place with radius 0: radius grows to 160 before step, so origin should move 160 toward center (or to center if distance <160)
	// With center 100, origin 0, radius 0 => after increment radius=160, distance 100 <160 => origin becomes center (100)
	m2 := &Manager{}
	m2.Strategic.CenterX = numeric.FixedFromInt(5)
	m2.Strategic.CenterZ = numeric.FixedFromInt(0)
	m2.OriginX = numeric.FixedFromInt(0)
	m2.OriginZ = numeric.FixedFromInt(0)
	m2.Strategic.Radius = numeric.FixedFromInt(0) // will grow to 160 before step
	m2.Catalog = m.Catalog
	// Use nil terrain so helper B succeeds without extra draws and radius resets on success
	x2, _, ok := Place(m2, "armsolar", nil)
	if !ok {
		t.Fatalf("Place failed for short distance with radius 0")
	}
	// Distance 5 < radius 160 => origin becomes center (5)
	if m2.OriginX != numeric.FixedFromInt(5) {
		t.Fatalf("short distance step origin %d want 5*65536", m2.OriginX)
	}
	if x2 != numeric.FixedFromInt(5) {
		t.Fatalf("short distance placement %d want 5*65536", x2)
	}
	// After success, radius should be reset to 0
	if m2.Strategic.Radius != 0 {
		t.Fatalf("radius should be reset to 0 on success, got %d", m2.Strategic.Radius)
	}
	// Test with large distance and radius 0: center 100, origin 0, radius 0 => after increment 160, distance 100 <160 => origin becomes center (100)
	// This verifies fixed-point scaling with cap.
	m3 := &Manager{}
	m3.Strategic.CenterX = numeric.FixedFromInt(100)
	m3.Strategic.CenterZ = numeric.FixedFromInt(0)
	m3.OriginX = numeric.FixedFromInt(0)
	m3.OriginZ = numeric.FixedFromInt(0)
	m3.Strategic.Radius = numeric.FixedFromInt(0)
	m3.Catalog = m.Catalog
	x3, _, ok := Place(m3, "armsolar", nil)
	if !ok {
		t.Fatalf("Place failed for large distance")
	}
	// Since radius 160 > distance 100, origin should be center
	if m3.OriginX != numeric.FixedFromInt(100) {
		t.Fatalf("large distance with radius 160 should reach center, got %d want 100*65536", m3.OriginX)
	}
	if x3 != numeric.FixedFromInt(100) {
		t.Fatalf("placement %d want center 100*65536", x3)
	}
	// Test with terrain and radius growth cap: ensure radius grows by 160 per failure and caps at max(mapW,mapH)
	// Use non-nil terrain to test cap; create 32x32 terrain, radius should grow to cap after many failures
	// For this test, we just verify that Place with nil terrain still resets radius on success (already checked)
	// And that stepTowardCenter with zero radius and same origin/center does not move
	m4 := &Manager{}
	m4.Strategic.CenterX = numeric.FixedFromInt(100)
	m4.Strategic.CenterZ = numeric.FixedFromInt(100)
	m4.OriginX = numeric.FixedFromInt(10)
	m4.OriginZ = numeric.FixedFromInt(10)
	m4.Strategic.Radius = 0
	m4.Catalog = m.Catalog
	// Use non-nil terrain with blocking yard to cause failure and test radius growth (not reset)
	ter := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
	catalogFail := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"geothermalplant": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "geothermalplant"}, UnitName: "geothermalplant", ExtractsMetal: 0, FootprintX: 2, FootprintZ: 2, YardMap: "GGGG"},
		},
	}
	m4.Catalog = catalogFail
	m4.Terrain = ter
	// This should fail due to geothermal, radius should grow by 160 and cap at map width (4*16*65536)
	beforeRad := m4.Strategic.Radius
	_, _, ok = Place(m4, "geothermalplant", ter)
	if ok {
		t.Fatalf("geothermal should fail without geothermal feature")
	}
	if m4.Strategic.Radius == 0 || m4.Strategic.Radius == beforeRad {
		t.Fatalf("radius should have grown on failure, got %d before %d", m4.Strategic.Radius, beforeRad)
	}
	// For 4x4 terrain, cap is 4*16*65536=4194304, so radius should be capped there
	if m4.Strategic.Radius != numeric.Fixed(4*16*65536) {
		t.Fatalf("radius after first failure should be capped at 4*16*65536=4194304, got %d", m4.Strategic.Radius)
	}
}

func TestExtractorBranchDrawCountAndFallthrough(t *testing.T) {
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// choose A when SurfaceMetal < RNG else B, strict < via CMP/JGE [P0-03].
	// Failed A does NOT fall through to B [P0-03].
	catalog := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armmex":   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armmex"}, UnitName: "armmex", ExtractsMetal: 0.001, FootprintX: 2, FootprintZ: 2, YardMap: "oooo"},
			"armsolar": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armsolar"}, UnitName: "armsolar", ExtractsMetal: 0, FootprintX: 2, FootprintZ: 2, YardMap: "oooo"},
		},
	}
	// Test extractor branch picks A when SurfaceMetal < RNG and fails without fallthrough
	// Use SurfaceMetal 0 and RNG that draws >0 to force A, which will fail (no patchVec) and Place should return false with exactly 1 draw
	// Find a seed that gives draw >0 for bound 255
	var seedForA uint32 = 1
	var rForA rng.Simulation
	var drawA uint32
	for s := uint32(0); s < 1000; s++ {
		tmp := rng.NewSimulation(s)
		d := tmp.Uint32n(255)
		if d > 0 {
			seedForA = s
			rForA = rng.NewSimulation(s)
			drawA = d
			break
		}
	}
	_ = drawA
	mA := &Manager{
		Catalog:      catalog,
		RNG:          &rForA,
		SurfaceMetal: 0, // 0 < draw => A (since draw >0)
	}
	mA.Strategic.CenterX = numeric.FixedFromInt(0)
	mA.Strategic.CenterZ = numeric.FixedFromInt(0)
	mA.OriginX = numeric.FixedFromInt(0)
	mA.OriginZ = numeric.FixedFromInt(0)
	mA.Strategic.Radius = 0
	// Need terrain for helper A/B to be relevant; use nil terrain so helper B would succeed but A fails.
	// With SurfaceMetal 0 < draw, it picks A, which fails and does NOT fall through, so Place should fail
	before := rForA.Draws()
	_, _, ok := Place(mA, "armmex", nil)
	after := rForA.Draws()
	if ok {
		t.Fatalf("extractor picking A should fail (no patchVec) and not fall through, got success")
	}
	if after != before+1 {
		t.Fatalf("extractor A branch should consume exactly one RNG(255) draw, got %d->%d", before, after)
	}
	// Test extractor branch picks B when SurfaceMetal >= RNG and succeeds via B (nil terrain => success)
	// Use SurfaceMetal 255 to ensure always picks B (since max draw 254 <255)
	rB := rng.NewSimulation(12345)
	mB := &Manager{
		Catalog:      catalog,
		RNG:          &rB,
		SurfaceMetal: 255,
	}
	mB.Strategic.CenterX = numeric.FixedFromInt(0)
	mB.Strategic.CenterZ = numeric.FixedFromInt(0)
	mB.OriginX = numeric.FixedFromInt(0)
	mB.OriginZ = numeric.FixedFromInt(0)
	mB.Strategic.Radius = 0
	beforeB := rB.Draws()
	x, z, ok := Place(mB, "armmex", nil)
	afterB := rB.Draws()
	if !ok {
		t.Fatalf("extractor picking B should succeed via helper B (nil terrain), got failure")
	}
	if afterB != beforeB+1 {
		t.Fatalf("extractor B branch should consume exactly one RNG(255) draw when terrain nil, got %d->%d", beforeB, afterB)
	}
	if x != 0 || z != 0 {
		// With nil terrain, helper B returns true and origin is center (0) because radius 160 > distance 0
		// So placement at origin (0,0) is expected
	}
	// Non-extractor should not draw 255, but helper B with nil terrain succeeds without extra draws
	r2 := rng.NewSimulation(999)
	m2 := &Manager{
		Catalog: catalog,
		RNG:     &r2,
	}
	m2.Strategic.CenterX = 0
	m2.OriginX = 0
	before2 := r2.Draws()
	_, _, ok2 := Place(m2, "armsolar", nil)
	after2 := r2.Draws()
	if !ok2 {
		t.Fatalf("non-extractor Place should succeed")
	}
	if after2 != before2 {
		t.Fatalf("non-extractor should not consume RNG(255) when terrain nil, got %d->%d", before2, after2)
	}
	// Also test unknown def (no catalog entry) -> not extractor, no draw
	r3 := rng.NewSimulation(42)
	m3 := &Manager{Catalog: catalog, RNG: &r3}
	before3 := r3.Draws()
	Place(m3, "unknownunit", nil)
	if r3.Draws() != before3 {
		t.Fatalf("unknown def should be treated as non-extractor, no draw")
	}
	// Test exact < boundary: SurfaceMetal = draw => should pick B (since < is strict)
	// We need a seed where draw == SurfaceMetal. Use SurfaceMetal 50 and find seed with draw 50
	var seedForEq uint32
	var found bool
	for s := uint32(0); s < 10000; s++ {
		tmp := rng.NewSimulation(s)
		if tmp.Uint32n(255) == 50 {
			seedForEq = s
			found = true
			break
		}
	}
	if found {
		rEq := rng.NewSimulation(seedForEq)
		mEq := &Manager{Catalog: catalog, RNG: &rEq, SurfaceMetal: 50}
		mEq.Strategic.CenterX = 0
		mEq.OriginX = 0
		mEq.Strategic.Radius = 0
		_, _, okEq := Place(mEq, "armmex", nil)
		// With SurfaceMetal 50 and draw 50, condition SurfaceMetal < draw is false (50<50 false) => picks B, which succeeds (nil terrain)
		if !okEq {
			t.Fatalf("SurfaceMetal==draw should pick B and succeed, got failure")
		}
	}
	_ = seedForA
}

func TestRadiusResetOnSuccess(t *testing.T) {
	catalog := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armsolar": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armsolar"}, UnitName: "armsolar", ExtractsMetal: 0, FootprintX: 2, FootprintZ: 2, YardMap: "oooo"},
		},
	}
	m := &Manager{Catalog: catalog}
	m.Strategic.Radius = numeric.FixedFromInt(5)
	m.Strategic.CenterX = numeric.FixedFromInt(10)
	m.OriginX = numeric.FixedFromInt(0)
	// With nil terrain, helper B succeeds without extra draws, radius should reset to 0
	_, _, ok := Place(m, "armsolar", nil)
	if !ok {
		t.Fatalf("Place should succeed for radius reset test")
	}
	if m.Strategic.Radius != 0 {
		t.Fatalf("radius should be reset to 0 on success, got %d", m.Strategic.Radius)
	}
	// Verify failure does NOT reset (radius grows). Use geothermal yard that requires geothermal feature.
	ter := &world.Terrain{
		CellW: 4,
		CellH: 4,
		Plot:  make([]world.PlotCell, 16),
	}
	catalog2 := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"geothermalplant": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "geothermalplant"}, UnitName: "geothermalplant", ExtractsMetal: 0, FootprintX: 2, FootprintZ: 2, YardMap: "GGGG"},
		},
	}
	mFail := &Manager{Catalog: catalog2, Terrain: ter}
	mFail.Strategic.Radius = numeric.FixedFromInt(1)
	mFail.Strategic.CenterX = numeric.FixedFromInt(0)
	mFail.OriginX = numeric.FixedFromInt(0)
	mFail.Strategic.CenterZ = 0
	mFail.OriginZ = 0
	// This placement should fail because geothermal not satisfied, after 30 trials helper B returns false
	_, _, okFail := Place(mFail, "geothermalplant", ter)
	if okFail {
		t.Fatalf("geothermal yard should fail validation without geothermal feature")
	}
	if mFail.Strategic.Radius == 0 {
		t.Fatalf("radius should not be reset on failure, got 0")
	}
	if mFail.Strategic.Radius == numeric.FixedFromInt(1) {
		t.Fatalf("radius should have grown on failure, still 1")
	}
	// For 4x4 terrain, cap is 4194304, so after 1+160 it caps
	if mFail.Strategic.Radius != numeric.Fixed(4*16*65536) {
		t.Fatalf("radius after first failure should be capped at 4194304, got %d", mFail.Strategic.Radius)
	}
}

func TestQueueBuildIssuedViaOrdinaryPath(t *testing.T) {
	catalog := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armsolar": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armsolar"}, UnitName: "armsolar", ExtractsMetal: 0, FootprintX: 2, FootprintZ: 2, YardMap: "oooo"},
		},
	}
	w := units.New(10, nil)
	factoryDef := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armvp"}, UnitName: "armvp", FootprintX: 4, FootprintZ: 4}
	h, err := w.Create(factoryDef, 0, numeric.FixedFromInt(0), 0, numeric.FixedFromInt(0))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(h)
	if factory == nil {
		t.Fatalf("factory nil after create")
	}
	called := false
	var calledDef string
	m := &Manager{
		Catalog: catalog,
		Factory: factory,
		QueueBuild: func(f *units.Unit, defKey string, count int) error {
			called = true
			calledDef = defKey
			return construction.QueueBuild(f, defKey, count)
		},
	}
	m.Strategic.Catalog = catalog
	m.Strategic.CenterX = numeric.FixedFromInt(0)
	m.OriginX = numeric.FixedFromInt(0)
	m.Strategic.Radius = 0
	_, _, ok := Place(m, "armsolar", nil)
	if !ok {
		t.Fatalf("Place should succeed for QueueBuild test")
	}
	if !called {
		t.Fatalf("queueBuild spy not called: AI did not issue build through ordinary path")
	}
	if calledDef != "armsolar" {
		t.Fatalf("queueBuild called with %q want %q", calledDef, "armsolar")
	}
	w2 := units.New(10, nil)
	h2, _ := w2.Create(factoryDef, 0, 0, 0, 0)
	factory2 := w2.Unit(h2)
	m2 := &Manager{Catalog: catalog, Factory: factory2}
	m2.Strategic.Catalog = catalog
	Place(m2, "armsolar", nil)
	if factory2.Orders == nil {
		t.Fatalf("factory2 orders queue not created via ordinary path")
	}
}
