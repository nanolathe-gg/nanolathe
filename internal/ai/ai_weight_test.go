package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

func TestSelectionUsesAuthoredAIWeightAndEmbeddedLimit(t *testing.T) {
	builder := testBuilder("armcom")
	candidate := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflak")},
		UnitName:         "armflak",
		AIWeight:         "weight ARMFLAK 0.5",
		// Only a definition carrying the authored downloadable flag is visited
		// by the two per-definition passes [08 R-AI-01 §12].
		Downloadable: true,
	}
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{
		"armcom":  builder.Def,
		"armflak": candidate,
	}}
	sel := &testSelector{
		player: 1,
		profile: &Profile{
			Weight: map[string]int32{"armflak": 100},
			Limit:  map[string]int32{},
		},
		strategic: &Strategic{
			Catalog:      catalog,
			Counts:       map[string]int32{},
			ClassVectors: map[string]ClassVector{"armflak": {C0: 40, C1: 30, C2: 30}},
		},
		catalog: catalog,
	}
	econ := testEcon(1, 800, 1000, 400, 500, 300, 10, 0, 0)
	rng.SeedGlobal(12345, 0)
	candidateResult, ok := SelectWithCandidates(sel, builder, econ, []string{"armflak"})
	if !ok {
		t.Fatal("authored ai_weight candidate was rejected")
	}
	// The calm fixture has mix (metal=25, energy=0, other=75), so weight 50
	// halves the unweighted score 37 to 18 after truncation.
	if candidateResult.Score != 18 {
		t.Fatalf("authored ai_weight score = %d, want 18", candidateResult.Score)
	}
	if got := sel.profile.WeightFor("ARMFLAK"); got != 50 {
		t.Fatalf("effective profile weight = %d, want 50", got)
	}

	candidate.AIWeight = "weight ARMFLAK 1\nlimit ARMFLAK 0"
	// Definitions are immutable after compilation; use a fresh profile/catalog
	// binding to exercise the embedded limit without reapplying the first view.
	catalog2 := &content.Catalog{Units: map[string]*content.UnitDef{
		"armcom":  builder.Def,
		"armflak": candidate,
	}}
	sel2 := &testSelector{
		player:  1,
		profile: &Profile{Weight: map[string]int32{}, Limit: map[string]int32{}},
		strategic: &Strategic{
			Catalog:      catalog2,
			Counts:       map[string]int32{},
			ClassVectors: map[string]ClassVector{"armflak": {C0: 40, C1: 30, C2: 30}},
		},
		catalog: catalog2,
	}
	if _, ok := SelectWithCandidates(sel2, builder, econ, []string{"armflak"}); ok {
		t.Fatal("embedded ai_weight limit=0 should reject candidate")
	}
	if got := sel2.profile.LimitFor("armflak"); got != 0 {
		t.Fatalf("embedded limit = %d, want 0", got)
	}
}

func TestAuthoredAIWeightPreservesFactorAndNarrowsEachDirective(t *testing.T) {
	def := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflak")},
		AIWeight:         "weight ARMFLAK 0.299",
		Downloadable:     true,
	}
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{"armflak": def}}
	profile := &Profile{Weight: map[string]int32{"armflak": 99}, Limit: map[string]int32{}}
	profile.ApplyUnitDefinitions(catalog)
	if got := profile.WeightFor("armflak"); got != 29 {
		t.Fatalf("99 * .299 weight = %d, want 29", got)
	}

	// An exact naming sets that type's weight lock as soon as the directive is
	// applied, and the handler skips every locked type, so a second directive
	// naming the same type exactly is inert: 99 * 1.5 truncates to 148 and
	// clamps to 100, and the *.299 that follows never runs [08 R-AI-01 §12].
	def.AIWeight = "weight ARMFLAK 1.5\nweight ARMFLAK 0.299"
	profile = &Profile{Weight: map[string]int32{"armflak": 99}, Limit: map[string]int32{}}
	profile.ApplyUnitDefinitions(catalog)
	if got := profile.WeightFor("armflak"); got != 100 {
		t.Fatalf("exact naming locks the type: weight = %d, want 100", got)
	}
}

func TestAbsentAuthoredAIWeightKeepsProfileDefaults(t *testing.T) {
	catalog := &content.Catalog{Units: map[string]*content.UnitDef{
		"armflak": {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("armflak")}},
	}}
	profile := &Profile{Weight: map[string]int32{}, Limit: map[string]int32{}}
	profile.ApplyUnitDefinitions(catalog)
	if got := profile.WeightFor("armflak"); got != 100 {
		t.Fatalf("absent ai_weight = %d, want default 100", got)
	}
	if got := profile.LimitFor("armflak"); got != -1 {
		t.Fatalf("absent embedded limit = %d, want default -1", got)
	}
}
