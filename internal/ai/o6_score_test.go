package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

func o6Def(key string) *content.UnitDef {
	return &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(key)},
		UnitName:         key,
	}
}

// TestScoreInputsReadSettledRuntimeAggregates locks the four AI-facing player
// fields. Stock and the pass-reporting counters are intentionally different so
// a Stock+PassProduced adapter cannot pass this fixture [R-P0-05].
func TestScoreInputsReadSettledRuntimeAggregates(t *testing.T) {
	var svc economy.Service
	p := &svc.Players[2]
	p.Stock[economy.Energy], p.Capacity[economy.Energy] = 900, 1000
	p.Stock[economy.Metal], p.Capacity[economy.Metal] = 400, 500
	p.PassProduced[economy.Energy], p.PassProduced[economy.Metal] = 700, 70
	p.PassConsumed[economy.Energy], p.PassConsumed[economy.Metal] = 1, 2
	p.AIProduction[economy.Energy], p.AIProduction[economy.Metal] = 12, 2
	p.AIConsumption[economy.Energy], p.AIConsumption[economy.Metal] = 3, 1

	in := ScoreInputsFromEconomy(&svc, 2)
	want := ScoreInputs{CurEnergy: 900, CapEnergy: 1000, CurMetal: 400, CapMetal: 500, ProdEnergy: 12, ProdMetal: 2, NetEnergy: 9, NetMetal: 1}
	if in != want {
		t.Fatalf("score inputs = %+v, want settled runtime fields %+v", in, want)
	}
}

// TestClassVectorUsesRuntimeDefinitionFields locks the recovered bit/field
// mappings and arithmetic for one definition [P0-01 §2.2–§4] [R-P0-05].
func TestClassVectorUsesRuntimeDefinitionFields(t *testing.T) {
	def := o6Def("builder")
	def.BuildCostMetal = 0
	def.BuildCostEnergy = 0
	def.CanAttack = true // class-vector input [08 R-P0-05 §5]
	def.Builder = true   // class-vector input [08 R-P0-05 §5]
	def.CanFly = true    // class-vector input [08 R-P0-05 §5]
	def.MakesMetal = 1   // class-vector input [08 R-P0-05 §5]
	def.RadarDistance = 1
	def.SonarDistance = 1
	def.MaxSlope = 0
	// Weapon slot reads: damage word (DAMAGE/default) /40 plus range word
	// (range) /100 plus 5; reloadtime must not enter this sum [08 "Class
	// routine weapon reads"]. 400/40 = 10, 600/100 = 6. The ID is non-zero:
	// ID 0 is the record-0 inactive sentinel and would be skipped [02 §5
	// R-CONTENT-02].
	def.Weapon1Def = &content.WeaponDef{ID: 1, DamageDefault: 400, Range: 600, ReloadTime: 999}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	s := &Strategic{Catalog: cat, Counts: map[string]int32{def.CanonicalKey: 0}, ClassVectors: map[string]ClassVector{def.CanonicalKey: {}}, SingleVectors: map[string]int8{def.CanonicalKey: 0}}
	s.recomputeClassVectors()

	// single: start value 1 + makesmetal 10 + weapon budget (11 + 10 + 5 + 6)
	// = 43; the costs are zero, so the two cost terms contribute nothing here.
	// other: can-attack REPLACES the start value 1 with 21, then + 30 + 25 + 40
	// + 15 + 5 = 136, times four for a zero count, clamped at the upper bound.
	// metal: base 0 plus the makesmetal term +25, inside [0, 100]; energy zero.
	if got, want := s.SingleVectors[def.CanonicalKey], int8(43); got != want {
		t.Fatalf("single vector = %d, want %d", got, want)
	}
	if got, want := s.ClassVectors[def.CanonicalKey], (ClassVector{C0: 100, C1: 25, C2: 0}); got != want {
		t.Fatalf("class vector = %+v, want %+v [08 R-P0-05 §5]", got, want)
	}
}

func TestGuardedSolarAndExtractorEnergyVectorsAndScores(t *testing.T) {
	solar := o6Def("corsolar")
	solar.BuildCostMetal = 141
	solar.BuildCostEnergy = 790
	solar.EnergyUse = -20
	mex := o6Def("cormex")
	mex.BuildCostMetal = 51
	mex.BuildCostEnergy = 514
	mex.EnergyUse = 3
	mex.ExtractsMetal = .001
	cat := &content.Catalog{Units: map[string]*content.UnitDef{
		solar.CanonicalKey: solar,
		mex.CanonicalKey:   mex,
	}}
	s := &Strategic{Catalog: cat}
	s.BindEnergyEnvironment(func() (float32, float32) { return 0, 0 })
	s.Init([]string{solar.CanonicalKey, mex.CanonicalKey})

	// The solar collector's metal coefficient is 0 - 0.02*141 = -2.82, which the
	// metal clamp's lower bound of ZERO raises to 0 before the truncation; the
	// extractor's energy coefficient is -1.285 - 5*3 = -16.285, which the energy
	// clamp's lower bound of ZERO raises to 0. Neither is negative in retail
	// [08 R-P0-05 §5].
	if got, want := s.ClassVectors[solar.CanonicalKey], (ClassVector{C0: 100, C1: 0, C2: 98}); got != want {
		t.Fatalf("CORSOLAR class vector = %+v, want %+v [05 R-PROD-01 §1][08 R-P0-05 §5]", got, want)
	}
	if got, want := s.ClassVectors[mex.CanonicalKey], (ClassVector{C0: 100, C1: 98, C2: 0}); got != want {
		t.Fatalf("CORMEX class vector = %+v, want %+v [05 R-PROD-01 §1][08 R-P0-05 §5]", got, want)
	}

	// These settled inputs produce the exact 50 metal / 50 energy / 0 other
	// mix, making both recovered energy coefficients observable in the score.
	in := ScoreInputs{
		CurEnergy: 200, CapEnergy: 1000, NetEnergy: 1, ProdEnergy: 200,
		CurMetal: 300, CapMetal: 500, NetMetal: 1, ProdMetal: 5,
	}
	if metal, energy, other := ComputeMix(in); metal != 50 || energy != 50 || other != 0 {
		t.Fatalf("mix = (%d,%d,%d), want (50,50,0)", metal, energy, other)
	}
	// With the metal and energy coefficients clamped at zero below, neither
	// definition contributes a negative term: the collector scores its energy
	// coefficient alone and the extractor its metal coefficient alone
	// [08 R-P0-05 §5].
	if got := ComputeScore(in, s.ClassVectors[solar.CanonicalKey], 100); got != 49 {
		t.Fatalf("CORSOLAR score = %d, want 49", got)
	}
	if got := ComputeScore(in, s.ClassVectors[mex.CanonicalKey], 100); got != 49 {
		t.Fatalf("CORMEX score = %d, want 49", got)
	}
}

func TestClassVectorOldProxiesCannotAffectChoice(t *testing.T) {
	a := o6Def("a")
	b := o6Def("b")
	// These fields were used as proxies by the pre-O6 implementation. They
	// must not affect the recovered class result. `bmcode` is no longer among
	// them: [08 R-P0-05 §9] names it as the category flag itself, so it is
	// asserted separately below and both definitions keep the building value
	// here.
	b.CanMove = true
	b.MaxVelocity = 99
	b.CanPatrol = true
	b.OnOffable = true
	b.Stealth = true
	b.NoRestrict = true
	b.Waterline = 99
	b.FootprintX, b.FootprintZ = 9, 9
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"a": a, "b": b}}
	s := &Strategic{Catalog: cat, Counts: map[string]int32{"a": 0, "b": 0}, ClassVectors: map[string]ClassVector{"a": {}, "b": {}}, SingleVectors: map[string]int8{"a": 0, "b": 0}}
	s.InitClassVectors()
	s.recomputeClassVectors()
	if s.InitVectors["a"] != s.InitVectors["b"] {
		t.Fatalf("a non-category proxy leaked into init vectors: a=%d b=%d", s.InitVectors["a"], s.InitVectors["b"])
	}
	if s.ClassVectors["a"] != s.ClassVectors["b"] || s.SingleVectors["a"] != s.SingleVectors["b"] {
		t.Fatalf("old proxy fields changed class choice: a=%+v/%d b=%+v/%d", s.ClassVectors["a"], s.SingleVectors["a"], s.ClassVectors["b"], s.SingleVectors["b"])
	}
}

// TestInitVectorCategoryFlagIsBMCode locks [08 R-P0-05 §9]: the initialization
// pass adds 40 when the authored `bmcode` byte is zero — the building class,
// the same byte the placement validator dispatches on [08 R-AI-03 §7.4] — and
// 20 when the definition's compiled build-option list EXISTS, which is exactly
// when it carries the authored `builder` flag. A plain building is 40, a
// builder-flagged building 60, a mobile definition 0 or 20.
//
// `emptybuilder` is the case that was wrong before: a builder whose compiled
// menu resolves to no entries still receives the list, so it still gets +20
// [08 R-ENTRY-02 §2][02 R-CAT-01 §5].
func TestInitVectorCategoryFlagIsBMCode(t *testing.T) {
	building := o6Def("building")
	factory := o6Def("factory")
	factory.Builder = true
	mobile := o6Def("mobile")
	mobile.BMCode = 1
	mobileBuilder := o6Def("mobilebuilder")
	mobileBuilder.BMCode = 1
	mobileBuilder.Builder = true
	emptyBuilder := o6Def("emptybuilder")
	emptyBuilder.Builder = true
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"building": building, "factory": factory, "mobile": mobile,
			"mobilebuilder": mobileBuilder, "emptybuilder": emptyBuilder,
		},
		BuildMenus: map[string]*content.BuildMenuPage{
			"factory":       {Buttons: []string{"mobile"}},
			"mobilebuilder": {Buttons: []string{"building"}},
			"emptybuilder":  {Buttons: nil},
		},
	}
	s := &Strategic{Catalog: cat}
	s.Init([]string{"building", "factory", "mobile", "mobilebuilder", "emptybuilder"})
	for _, tt := range []struct {
		key  string
		want int8
	}{{"building", 40}, {"factory", 60}, {"mobile", 0}, {"mobilebuilder", 20}, {"emptybuilder", 60}} {
		if got := s.InitVectors[tt.key]; got != tt.want {
			t.Fatalf("init vector %q = %d, want %d [08 R-P0-05 §9]", tt.key, got, tt.want)
		}
	}
}
