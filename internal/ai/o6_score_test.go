package ai

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
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
	def.CanAttack = true // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	def.Builder = true   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	def.CanFly = true    // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	def.MakesMetal = 1   // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	def.RadarDistance = 1
	def.SonarDistance = 1
	def.MaxSlope = 0
	// Weapon slot reads: damage word (DAMAGE/default) /40 plus range word
	// (range) /100 plus 5; reloadtime must not enter this sum [08 "Class
	// routine weapon reads"]. 400/40 = 10, 600/100 = 6.
	def.Weapon1Def = &content.WeaponDef{DamageDefault: 400, Range: 600, ReloadTime: 999}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	s := &Strategic{Catalog: cat, Counts: map[string]int32{def.CanonicalKey: 0}, ClassVectors: map[string]ClassVector{def.CanonicalKey: {}}, SingleVectors: map[string]int8{def.CanonicalKey: 0}}
	s.recomputeClassVectors()

	// single: (11 base + makesmetal 10) + weapon budget 11 + 10 + 5 + 6 = 43.
	// other: (21 + 30 + 25 + 40 + 15 + 5) * 4 * 3 clamps to 100.
	// metal: 0 - makesmetal 25 = -25; energy is zero.
	if got, want := s.SingleVectors[def.CanonicalKey], int8(43); got != want {
		t.Fatalf("single vector = %d, want %d", got, want)
	}
	if got, want := s.ClassVectors[def.CanonicalKey], (ClassVector{C0: 100, C1: -25, C2: 0}); got != want {
		t.Fatalf("class vector = %+v, want %+v", got, want)
	}
}

func TestClassVectorOldProxiesCannotAffectChoice(t *testing.T) {
	a := o6Def("a")
	b := o6Def("b")
	// These fields were used as proxies by the pre-O6 implementation. They
	// must not affect the recovered class result.
	b.BMCode = true
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
		t.Fatalf("opaque [layout omitted] proxy leaked into init vectors: a=%d b=%d", s.InitVectors["a"], s.InitVectors["b"])
	}
	if s.ClassVectors["a"] != s.ClassVectors["b"] || s.SingleVectors["a"] != s.SingleVectors["b"] {
		t.Fatalf("old proxy fields changed class choice: a=%+v/%d b=%+v/%d", s.ClassVectors["a"], s.SingleVectors["a"], s.ClassVectors["b"], s.SingleVectors["b"])
	}
}
