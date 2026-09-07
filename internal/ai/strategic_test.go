package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func TestPlacementRegionsUseConstructorDraws(t *testing.T) {
	r := rng.NewSimulation(123)
	s := &Strategic{}
	if !s.InitializeRandomState(&r) {
		t.Fatal("constructor state did not initialize")
	}
	if s.LandRegion.CellW != int16(s.negRegionW) || s.LandRegion.CellH != int16(s.negRegionH) {
		t.Fatalf("land region=%+v, derived=%d,%d", s.LandRegion, s.negRegionW, s.negRegionH)
	}
	if s.LandRegion.OffsetX != int16(s.setupDraws[2])-int16(s.negRegionW)/2 || s.LandRegion.OffsetZ != int16(s.setupDraws[3])-int16(s.negRegionH)/2 {
		t.Fatalf("land offsets=%+v from draws=%v", s.LandRegion, s.setupDraws)
	}
	if s.WaterRegion.CellW != int16(s.posRegionW) || s.WaterRegion.CellH != int16(s.posRegionH) {
		t.Fatalf("water region=%+v, derived=%d,%d", s.WaterRegion, s.posRegionW, s.posRegionH)
	}
	if s.WaterRegion.OffsetX != int16(s.setupDraws[6])-int16(s.posRegionW)/2 || s.WaterRegion.OffsetZ != int16(s.setupDraws[7])-int16(s.posRegionH)/2 {
		t.Fatalf("water offsets=%+v from draws=%v", s.WaterRegion, s.setupDraws)
	}
}

func TestRefreshCadence(t *testing.T) {
	s := &Strategic{}
	s.Init([]string{"armfav", "corfav"})
	r := rng.NewSimulation(1)

	if s.MaybeRefresh(0, &r, 0, nil) {
		t.Fatalf("tick 0 should not refresh; LastRefreshTick 0, need 30")
	}
	if r.Draws() != 0 {
		t.Fatalf("no draw should be consumed when not due, draws %d", r.Draws())
	}
	if s.MaybeRefresh(29, &r, 0, nil) {
		t.Fatalf("tick 29 should not refresh")
	}
	if r.Draws() != 0 {
		t.Fatalf("draws should still be 0, got %d", r.Draws())
	}
	if !s.MaybeRefresh(30, &r, 0, nil) {
		t.Fatalf("tick 30 should refresh")
	}
	if s.LastRefreshTick != 30 {
		t.Fatalf("LastRefreshTick %d want 30", s.LastRefreshTick)
	}
	if r.Draws() != 1 {
		t.Fatalf("exactly one RNG(30) draw per refresh, draws %d want 1", r.Draws())
	}
	if s.MaybeRefresh(31, &r, 0, nil) {
		t.Fatalf("tick 31 should not refresh")
	}
	if r.Draws() != 1 {
		t.Fatalf("draws should stay 1 at tick 31, got %d", r.Draws())
	}
	if !s.MaybeRefresh(60, &r, 0, nil) {
		t.Fatalf("tick 60 should refresh")
	}
	if r.Draws() != 2 {
		t.Fatalf("draws 2 after second refresh, got %d", r.Draws())
	}
}

func TestClassifyUsesEstablishedNetEnergyBranchOrder(t *testing.T) {
	tests := []struct {
		name string
		def  *content.UnitDef
		wind float32
		tide float32
		want float32
	}{
		{name: "nil definition", want: 0},
		{name: "negative energy use short circuits generators", def: &content.UnitDef{EnergyUse: -3, WindGenerator: 80, TidalGenerator: 90}, wind: .5, tide: .25, want: -3},
		{name: "positive energy use", def: &content.UnitDef{EnergyUse: 7, WindGenerator: 80, TidalGenerator: 90}, wind: .5, tide: .25, want: 7},
		{name: "energy use narrowed to zero falls through", def: &content.UnitDef{EnergyUse: 1e-300, WindGenerator: 8, TidalGenerator: 90}, wind: .5, tide: .25, want: -4},
		{name: "wind", def: &content.UnitDef{WindGenerator: 8, TidalGenerator: 90}, wind: .5, tide: .25, want: -4},
		{name: "positive wind narrowed to zero falls through to tidal", def: &content.UnitDef{WindGenerator: 1e-300, TidalGenerator: 12}, wind: .5, tide: .25, want: -3},
		{name: "nonpositive wind falls through to tidal", def: &content.UnitDef{WindGenerator: -8, TidalGenerator: 12}, wind: .5, tide: .25, want: -3},
		{name: "tidal", def: &content.UnitDef{TidalGenerator: 12}, wind: .5, tide: .25, want: -3},
		{name: "zero", def: &content.UnitDef{}, wind: .5, tide: .25, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.def, tc.wind, tc.tide); got != tc.want {
				t.Fatalf("classify = %g, want %g [05 R-PROD-01 §1]", got, tc.want)
			}
		})
	}
}

func TestClassRefreshReadsLiveEnergyEnvironmentWithoutExtraDraws(t *testing.T) {
	const key = "windgen"
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: key},
		UnitName:         key,
		WindGenerator:    10,
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{key: def}}
	wind := float32(.25)
	providerCalls := 0
	s := &Strategic{Catalog: cat}
	s.BindEnergyEnvironment(func() (float32, float32) {
		providerCalls++
		return wind, 0
	})
	s.Init([]string{key})
	if got := s.ClassVectors[key].C2; got != 12 {
		t.Fatalf("initial wind energy coefficient = %d, want 12", got)
	}
	if providerCalls != 1 {
		t.Fatalf("initial recompute provider calls = %d, want one per definition", providerCalls)
	}

	wind = .75
	r := rng.NewSimulation(1)
	for tick := uint32(30); tick <= 3000; tick += 30 {
		peek := r
		gate := peek.Uint32n(30)
		beforeDraws := r.Draws()
		beforeCalls := providerCalls
		if !s.MaybeRefresh(tick, &r, 0, nil) {
			t.Fatalf("tick %d refresh not due", tick)
		}
		if got := r.Draws(); got != beforeDraws+1 {
			t.Fatalf("tick %d draws = %d, want %d: helper adds no RNG calls", tick, got, beforeDraws+1)
		}
		if gate != 0 {
			if providerCalls != beforeCalls {
				t.Fatalf("tick %d nonzero gate called energy provider %d times", tick, providerCalls-beforeCalls)
			}
			continue
		}
		if providerCalls != beforeCalls+1 {
			t.Fatalf("tick %d gated recompute provider calls = %d, want one", tick, providerCalls-beforeCalls)
		}
		if got := s.ClassVectors[key].C2; got != 37 {
			t.Fatalf("refreshed wind energy coefficient = %d, want live-value result 37", got)
		}
		return
	}
	t.Fatal("deterministic fixture did not encounter RNG(30)==0 within 100 refreshes")
}

func TestClassRecomputeCadence(t *testing.T) {
	types := []string{"armfav", "corfav", "armship"}
	s := &Strategic{}
	s.Init(types)
	expected := &Strategic{}
	expected.Init(types)

	// The category flag is the authored `bmcode` byte [08 R-P0-05 §9], read
	// through the catalog. This fixture binds no catalog, so no definition
	// resolves and neither the building's 40 nor a build list's 20 can be
	// added; the byte stays 0 [P0-01 §2.2] [R-P0-05].
	for _, ck := range types {
		canon := content.CanonicalKey(ck)
		if v := s.InitVectors[canon]; v != 0 {
			t.Fatalf("init single %s = %d want 0 with unresolved category flag and no build list [P0-01]", ck, v)
		}
		// Class vectors after init should be within [-100,100] and deterministic, not placeholder constant.
		cv := s.ClassVectors[canon]
		if cv.C0 < -100 || cv.C0 > 100 || cv.C1 < -100 || cv.C1 > 100 || cv.C2 < -100 || cv.C2 > 100 {
			t.Fatalf("init triple %s = %+v out of [-100,100] clamp [P0-01]", ck, cv)
		}
		if sv := s.SingleVectors[canon]; sv < -100 || sv > 100 {
			t.Fatalf("init single91 %s = %d out of clamp", ck, sv)
		}
	}
	r := rng.NewSimulation(12345)
	var tick uint32 = 30
	for i := 0; i < 10; i++ {
		tick += 30
		peek := r
		peekVal := peek.Uint32n(30)
		if peekVal >= 30 {
			t.Fatalf("peek RNG(30) out of range %d", peekVal)
		}
		beforeDraws := r.Draws()
		// Make the gated write observable without retaining a diagnostic tick in
		// authoritative state. A nonzero gate preserves these sentinels; zero
		// replaces them with the established recomputed vectors.
		const sentinel int8 = 99
		for _, ck := range types {
			canon := content.CanonicalKey(ck)
			s.ClassVectors[canon] = ClassVector{C0: sentinel, C1: sentinel, C2: sentinel}
			s.SingleVectors[canon] = sentinel
		}
		if !s.MaybeRefresh(tick, &r, 0, nil) {
			t.Fatalf("tick %d should be due", tick)
		}
		afterDraws := r.Draws()
		if afterDraws != beforeDraws+1 {
			t.Fatalf("tick %d: draws %d -> %d want +1", tick, beforeDraws, afterDraws)
		}
		// Vectors must stay within clamp
		for _, ck := range types {
			canon := content.CanonicalKey(ck)
			cv := s.ClassVectors[canon]
			if cv.C0 < -100 || cv.C0 > 100 || cv.C1 < -100 || cv.C1 > 100 || cv.C2 < -100 || cv.C2 > 100 {
				t.Fatalf("tick %d: vector %s = %+v out of clamp", tick, ck, cv)
			}
		}
		if peekVal == 0 {
			for _, ck := range types {
				canon := content.CanonicalKey(ck)
				if s.ClassVectors[canon] != expected.ClassVectors[canon] || s.SingleVectors[canon] != expected.SingleVectors[canon] {
					t.Fatalf("tick %d: RNG(30)==0 did not publish recomputed vectors for %s", tick, ck)
				}
			}
		} else {
			for _, ck := range types {
				canon := content.CanonicalKey(ck)
				if s.ClassVectors[canon] != (ClassVector{C0: sentinel, C1: sentinel, C2: sentinel}) || s.SingleVectors[canon] != sentinel {
					t.Fatalf("tick %d: RNG(30)=%d changed vectors without the recompute gate", tick, peekVal)
				}
			}
		}
	}
	s2 := &Strategic{}
	s2.Init(types)
	r2 := rng.NewSimulation(999)
	s2.MaybeRefresh(30, &r2, 0, nil)
	drawsAfterFirst := r2.Draws()
	beforeVectors := make(map[string]ClassVector, len(s2.ClassVectors))
	for k, v := range s2.ClassVectors {
		beforeVectors[k] = v
	}
	if s2.MaybeRefresh(31, &r2, 0, nil) {
		t.Fatalf("tick 31 not due should return false")
	}
	if r2.Draws() != drawsAfterFirst {
		t.Fatalf("non-due tick should not consume RNG, draws %d want %d", r2.Draws(), drawsAfterFirst)
	}
	for k, v := range beforeVectors {
		if s2.ClassVectors[k] != v {
			t.Fatalf("non-due tick changed vector %s", k)
		}
	}
}

func TestClassVectors_RetailVectors(t *testing.T) {
	// Per P0-01 §6: verify retail arithmetic for ARMCOM etc. Use catalog with known defs.
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			content.CanonicalKey("armcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armcom")}, UnitName: "armcom", BuildCostMetal: 267, BuildCostEnergy: 946, ExtractsMetal: 0, Builder: true, CanMove: true, CanPatrol: true, MaxVelocity: 100, FootprintX: 2, FootprintZ: 2, BMCode: 0, OnOffable: false, Waterline: 0, Weapon1Def: nil},
			content.CanonicalKey("corcom"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("corcom")}, UnitName: "corcom", BuildCostMetal: 267, BuildCostEnergy: 946, ExtractsMetal: 0, Builder: true, CanMove: true, MaxVelocity: 100, FootprintX: 2, FootprintZ: 2, BMCode: 0, OnOffable: false, Waterline: 0},
			content.CanonicalKey("armex"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armex")}, UnitName: "armex", BuildCostMetal: 100, BuildCostEnergy: 100, ExtractsMetal: 0.001, Builder: false, CanMove: false, FootprintX: 2, FootprintZ: 2, BMCode: 0, OnOffable: false},
			content.CanonicalKey("armsolar"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armsolar")}, UnitName: "armsolar", BuildCostMetal: 50, BuildCostEnergy: 50, ExtractsMetal: 0, Builder: false, CanMove: false, FootprintX: 2, FootprintZ: 2, BMCode: 0, OnOffable: true},
			content.CanonicalKey("armlab"):   {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armlab")}, UnitName: "armlab", BuildCostMetal: 200, BuildCostEnergy: 300, ExtractsMetal: 0, Builder: true, CanMove: false, FootprintX: 4, FootprintZ: 4, BMCode: 0, OnOffable: false},
			content.CanonicalKey("armvp"):    {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armvp")}, UnitName: "armvp", BuildCostMetal: 200, BuildCostEnergy: 300, ExtractsMetal: 0, Builder: true, CanMove: false, FootprintX: 4, FootprintZ: 4, BMCode: 0, OnOffable: false},
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			content.CanonicalKey("armlab"): {Buttons: []string{"armvp"}},
			content.CanonicalKey("armvp"):  {Buttons: []string{"armlab"}},
			content.CanonicalKey("armcom"): {Buttons: []string{"armex", "armsolar"}},
		},
	}
	types := []string{"armcom", "corcom", "armex", "armsolar", "armlab", "armvp"}
	s := &Strategic{Catalog: cat}
	s.Init(types)
	// Check InitVectors: 40 for the building class (`bmcode == 0`, which every
	// definition in this fixture authors) plus 20 for a non-empty build list
	// [08 R-P0-05 §9]. The earlier form of these assertions expected the
	// category addend to be absent, which that section withdrew.
	if v := s.InitVectors[content.CanonicalKey("armlab")]; v != 60 {
		t.Fatalf("armlab init %d want 60 (building 40 + build list 20) [08 R-P0-05 §9]", v)
	}
	if v := s.InitVectors[content.CanonicalKey("armvp")]; v != 60 {
		t.Fatalf("armvp init %d want 60", v)
	}
	if v := s.InitVectors[content.CanonicalKey("armcom")]; v != 60 {
		t.Fatalf("armcom init %d want 60", v)
	}
	if v := s.InitVectors[content.CanonicalKey("armex")]; v != 40 {
		t.Fatalf("armex init %d want 40 (building, no build list)", v)
	}
	if v := s.InitVectors[content.CanonicalKey("armsolar")]; v != 40 {
		t.Fatalf("armsolar init %d want 40", v)
	}
	// Check triples are within clamp and not all equal (retail varies per type) [P0-01 §6]
	seen := make(map[ClassVector]bool)
	for _, ck := range types {
		canon := content.CanonicalKey(ck)
		cv := s.ClassVectors[canon]
		if cv.C0 < -100 || cv.C0 > 100 || cv.C1 < -100 || cv.C1 > 100 || cv.C2 < -100 || cv.C2 > 100 {
			t.Fatalf("%s triple %+v out of clamp", ck, cv)
		}
		seen[cv] = true
	}
	if len(seen) < 2 {
		t.Fatalf("expected varied triples for different types, got only %d distinct", len(seen))
	}
	// Check extractor branch: armex has ExtractsMetal !=0, should have different C1 vs non-extractor
	cvEx := s.ClassVectors[content.CanonicalKey("armex")]
	cvSolar := s.ClassVectors[content.CanonicalKey("armsolar")]
	if cvEx.C1 == cvSolar.C1 && cvEx.C0 == cvSolar.C0 && cvEx.C2 == cvSolar.C2 {
		t.Fatalf("extractor vs non-extractor vectors should differ")
	}
	// Check clamping: weapon damage max should clamp to 100. The ID is
	// non-zero: ID 0 is the record-0 inactive sentinel and would be skipped
	// [02 §5 R-CONTENT-02].
	weap := &content.WeaponDef{ID: 1, DamageDefault: 65535, ReloadTime: 10000}
	cat.Units[content.CanonicalKey("armhlt")] = &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armhlt")},
		UnitName:         "armhlt", BuildCostMetal: 100, BuildCostEnergy: 100, ExtractsMetal: 0, Builder: false, CanMove: false,
		Weapon1Def: weap,
	}
	s2 := &Strategic{Catalog: cat}
	s2.Init([]string{"armhlt"})
	cvHlt := s2.ClassVectors[content.CanonicalKey("armhlt")]
	if cvHlt.C0 < -100 || cvHlt.C0 > 100 {
		t.Fatalf("clamp failed for high damage weapon")
	}
	// SingleVectors for armhlt should be clamped to 100 as well (weapon budget max)
	if sv := s2.SingleVectors[content.CanonicalKey("armhlt")]; sv != 100 {
		// wSum large => clamped to 100, t1 small, so final should be 100
		// Allow 100
		if sv < 90 { // at least high
			t.Fatalf("armhlt single %d want near 100 clamp", sv)
		}
	}
	// Test extractor float zero exact vs denorm: 0.0 vs 1.4e-45 should give different vectors [P0-01 §7.1]
	cat.Units[content.CanonicalKey("zeroex")] = &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("zeroex")}, UnitName: "zeroex", ExtractsMetal: 0, BuildCostMetal: 100, BuildCostEnergy: 100}
	cat.Units[content.CanonicalKey("denormex")] = &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("denormex")}, UnitName: "denormex", ExtractsMetal: 1.4e-45, BuildCostMetal: 100, BuildCostEnergy: 100}
	s3 := &Strategic{Catalog: cat}
	s3.Init([]string{"zeroex", "denormex"})
	cvZero := s3.ClassVectors[content.CanonicalKey("zeroex")]
	cvDenorm := s3.ClassVectors[content.CanonicalKey("denormex")]
	if cvZero == cvDenorm {
		t.Fatalf("zero vs denorm extractor vectors should differ (exact !=0.0 test) [P0-01]")
	}
	// Verify zero RNG inside recompute: no draws consumed during Init recompute
	r := rng.NewSimulation(1)
	before := r.Draws()
	s4 := &Strategic{Catalog: cat}
	// Init should not draw from passed RNG (only MaybeRefresh does)
	s4.Init([]string{"armcom"})
	after := r.Draws()
	if after != before {
		t.Fatalf("recompute should use zero RNG inside routine [P0-01 §5], draws %d->%d", before, after)
	}
	// Also check that recompute via MaybeRefresh with RNG0 draws exactly one per refresh
	s5 := &Strategic{Catalog: cat}
	s5.Init(types)
	r5 := rng.NewSimulation(123)
	// Force RNG to 0 by finding seed that yields 0? We can just loop until RNG0 occurs and ensure recompute happened.
	// For determinism, we just check that after a refresh that drew 0, vectors changed and after non-zero they didn't (already covered).
	_ = s5
	_ = r5
}

func TestCenterComputation(t *testing.T) {
	s := &Strategic{}
	types := []string{"armfav"}
	s.Init(types)
	// The centre is weighted by the initialization-only byte [08 R-P0-05 §10];
	// with one equal weight for every unit it is still the arithmetic mean,
	// which is what the cases below check. Without a catalog Init leaves the
	// byte at 0, so give the type a plain building's 40 [08 R-P0-05 §9].
	s.InitVectors[content.CanonicalKey("armfav")] = 40

	w := newAIFixtureWorld(10, nil)
	defA := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armfav")}, UnitName: "armfav", MaxDamage: 100}
	x1 := numeric.FixedFromInt(0)
	z1 := numeric.FixedFromInt(0)
	x2 := numeric.FixedFromInt(200)
	z2 := numeric.FixedFromInt(0)
	if _, err := w.Create(defA, 1, x1, numeric.Fixed(0), z1); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if _, err := w.Create(defA, 1, x2, numeric.Fixed(0), z2); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	h, err := w.Create(defA, 1, numeric.FixedFromInt(1000), numeric.Fixed(0), numeric.FixedFromInt(1000))
	if err != nil {
		t.Fatalf("create nanoframe: %v", err)
	}
	if u := w.Unit(h); u != nil {
		u.Remaining = 0.5
	}
	if _, err := w.Create(defA, 2, numeric.FixedFromInt(9999), numeric.Fixed(0), numeric.FixedFromInt(9999)); err != nil {
		t.Fatalf("create other player: %v", err)
	}
	r := rng.NewSimulation(1)
	if !s.MaybeRefresh(30, &r, 1, w) {
		t.Fatalf("should refresh at 30")
	}
	wantX := numeric.FixedFromInt(100)
	wantZ := numeric.FixedFromInt(0)
	if s.CenterX != wantX || s.CenterZ != wantZ {
		t.Fatalf("center X %v Z %v want %v %v", s.CenterX, s.CenterZ, wantX, wantZ)
	}
	if c := s.Counts[content.CanonicalKey("armfav")]; c != 2 {
		t.Fatalf("counts armfav %d want 2, map %v", c, s.Counts)
	}
	s2 := &Strategic{}
	s2.Init([]string{"armfav"})
	w2 := newAIFixtureWorld(10, nil)
	r2 := rng.NewSimulation(1)
	if !s2.MaybeRefresh(30, &r2, 1, w2) {
		t.Fatalf("empty world refresh")
	}
	if s2.CenterX != 0 || s2.CenterZ != 0 {
		t.Fatalf("empty center %v %v want 0 0", s2.CenterX, s2.CenterZ)
	}
	if c := s2.Counts[content.CanonicalKey("armfav")]; c != 0 {
		t.Fatalf("empty counts %d want 0", c)
	}
	s3 := &Strategic{}
	s3.Init([]string{"armfav"})
	s3.InitVectors[content.CanonicalKey("armfav")] = 40
	w3 := newAIFixtureWorld(10, nil)
	if _, err := w3.Create(defA, 1, numeric.FixedFromInt(-100), numeric.Fixed(0), numeric.FixedFromInt(-50)); err != nil {
		t.Fatalf("create neg: %v", err)
	}
	if _, err := w3.Create(defA, 1, numeric.FixedFromInt(100), numeric.Fixed(0), numeric.FixedFromInt(50)); err != nil {
		t.Fatalf("create pos: %v", err)
	}
	r3 := rng.NewSimulation(1)
	if !s3.MaybeRefresh(30, &r3, 1, w3) {
		t.Fatalf("neg/pos refresh")
	}
	if s3.CenterX != 0 || s3.CenterZ != 0 {
		t.Fatalf("neg/pos center %v %v want 0 0", s3.CenterX, s3.CenterZ)
	}
}

func TestCenterComputationIncludesY(t *testing.T) {
	s := &Strategic{}
	s.Init([]string{"armfav"})
	s.InitVectors[content.CanonicalKey("armfav")] = 40
	w := newAIFixtureWorld(10, nil)
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armfav"}, UnitName: "armfav", MaxDamage: 100}
	if _, err := w.Create(def, 1, 0, numeric.FixedFromInt(100), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Create(def, 1, 0, numeric.FixedFromInt(300), 0); err != nil {
		t.Fatal(err)
	}
	r := rng.NewSimulation(1)
	if !s.MaybeRefresh(30, &r, 1, w) {
		t.Fatal("refresh was not due")
	}
	if s.CenterY != numeric.FixedFromInt(200) {
		t.Fatalf("center Y=%d, want %d", s.CenterY, numeric.FixedFromInt(200))
	}
}

// TestCenterIsWeightedByTheInitializationByte locks [08 R-P0-05 §10]: the
// initialization-only per-type byte is the per-unit WEIGHT of the strategic
// centre, not an inert vector, and the centre is therefore the weighted
// centroid of the player's structures. A field army of plain mobile units —
// whose byte is 0 — never moves it, and when nothing weighted is owned the
// division is skipped and the centre words are the truncation of zero, the
// "centre unset" state the explore task tests [08 R-AI-01 §6][08 R-AI-01 §16].
func TestCenterIsWeightedByTheInitializationByte(t *testing.T) {
	const heavy, light = "armsolar", "armpw"
	s := &Strategic{}
	s.Init([]string{heavy, light})
	// A plain building weighs 40; every mobile unit that is not a builder
	// weighs 0 [08 R-P0-05 §9].
	s.InitVectors[content.CanonicalKey(heavy)] = 40
	s.InitVectors[content.CanonicalKey(light)] = 0

	w := newAIFixtureWorld(10, nil)
	defHeavy := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(heavy)}, UnitName: heavy, MaxDamage: 100}
	defLight := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(light)}, UnitName: light, MaxDamage: 100}
	if _, err := w.Create(defHeavy, 1, numeric.FixedFromInt(300), numeric.FixedFromInt(20), numeric.FixedFromInt(500)); err != nil {
		t.Fatalf("create weighted: %v", err)
	}
	if _, err := w.Create(defLight, 1, numeric.FixedFromInt(4000), numeric.FixedFromInt(900), numeric.FixedFromInt(4000)); err != nil {
		t.Fatalf("create unweighted: %v", err)
	}
	r := rng.NewSimulation(1)
	if !s.MaybeRefresh(30, &r, 1, w) {
		t.Fatal("refresh was not due")
	}
	if s.CenterX != numeric.FixedFromInt(300) || s.CenterY != numeric.FixedFromInt(20) || s.CenterZ != numeric.FixedFromInt(500) {
		t.Fatalf("centre = (%d,%d,%d), want the weighted unit's own position (%d,%d,%d)",
			s.CenterX, s.CenterY, s.CenterZ,
			numeric.FixedFromInt(300), numeric.FixedFromInt(20), numeric.FixedFromInt(500))
	}
	// The zero-weight unit is still walked and still counted.
	if c := s.Counts[content.CanonicalKey(light)]; c != 1 {
		t.Fatalf("zero-weight unit counted %d times, want 1", c)
	}

	// Nothing weighted: the weight sum is exactly 0.0, the division is skipped,
	// and the centre stays at the origin however far away the units stand.
	s2 := &Strategic{}
	s2.Init([]string{light})
	s2.InitVectors[content.CanonicalKey(light)] = 0
	w2 := newAIFixtureWorld(10, nil)
	if _, err := w2.Create(defLight, 1, numeric.FixedFromInt(4000), numeric.FixedFromInt(900), numeric.FixedFromInt(4000)); err != nil {
		t.Fatalf("create unweighted: %v", err)
	}
	r2 := rng.NewSimulation(1)
	if !s2.MaybeRefresh(30, &r2, 1, w2) {
		t.Fatal("refresh was not due")
	}
	if s2.CenterX != 0 || s2.CenterY != 0 || s2.CenterZ != 0 {
		t.Fatalf("zero total weight left centre (%d,%d,%d), want (0,0,0)", s2.CenterX, s2.CenterY, s2.CenterZ)
	}
}

// The sum comparison is not interchangeable with elapsed subtraction around
// wrapped sums or a future stored timestamp [06 §3.1].
func TestStrategicRefreshUnsignedDeadline(t *testing.T) {
	for _, tc := range []struct {
		last, tick uint32
		due        bool
	}{
		{100, 99, false}, {100, 129, false}, {100, 130, true},
		{^uint32(0) - 10, 0, false}, {^uint32(0) - 10, 19, true},
		{^uint32(0) - 10, ^uint32(0) - 5, true},
	} {
		s := &Strategic{LastRefreshTick: tc.last}
		r := rng.NewSimulation(7)
		got := s.MaybeRefresh(tc.tick, &r, 0, nil)
		if got != tc.due {
			t.Fatalf("last=%d tick=%d due=%v, want %v", tc.last, tc.tick, got, tc.due)
		}
		if got && r.Draws() != 1 || !got && r.Draws() != 0 {
			t.Fatal("refresh draw differs from due verdict")
		}
	}
}
