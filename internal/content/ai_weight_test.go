package content

import "testing"

func TestUnitAIWeightCompilesAndLeavesAILimitInert(t *testing.T) {
	def := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMFLAK;
 ai_weight=weight ARMFLAK 0.5;
 ai_limit=limit ARMFLAK 2;
}
`).Root.Sections()[0], "units/armflak.fbi", "", Provenance{})
	if def.AIWeight != "weight ARMFLAK 0.5" {
		t.Fatalf("ai_weight = %q, want authored directive", def.AIWeight)
	}
	if _, ok := def.Unknown["ai_weight"]; ok {
		t.Fatal("typed ai_weight was retained as Unknown")
	}
	if got := def.Unknown["ai_limit"]; got != "limit ARMFLAK 2" {
		t.Fatalf("ai_limit Unknown = %q, want raw inert directive", got)
	}

	defaults := compileUnitSection(mustParseTDF(t, `[UNITINFO]
{
 UnitName=ARMLAB;
}
`).Root.Sections()[0], "units/armlab.fbi", "", Provenance{})
	if defaults.AIWeight != "" {
		t.Fatalf("absent ai_weight = %q, want empty default", defaults.AIWeight)
	}
}

func TestParseAIWeightUsesProfileArithmetic(t *testing.T) {
	directives := ParseAIWeight([]byte("weight ARMFLAK 0.5\nweight ARMFLAK 2\nlimit ARMFLAK 3"))
	if len(directives.Weights) != 2 {
		t.Fatalf("weight directive count = %d, want 2", len(directives.Weights))
	}
	if got := directives.Weights[0]; got.Type != CanonicalKey("armflak") || got.Factor != float32(0.5) {
		t.Fatalf("first weight directive = %#v, want armflak/.5", got)
	}
	if got := directives.Weights[1]; got.Type != CanonicalKey("armflak") || got.Factor != float32(2) {
		t.Fatalf("second weight directive = %#v, want armflak/2", got)
	}
	if got := directives.Limits[CanonicalKey("armflak")]; got != 3 {
		t.Fatalf("embedded limit = %d, want 3", got)
	}
}

func TestParseAIWeightMalformedLinesIgnored(t *testing.T) {
	directives := ParseAIWeight([]byte("weight ARMFLAK\nweight ARMFLAK not-a-number\nunknown ARMFLAK 0.5\nlimit ARMFLAK\n"))
	if len(directives.Weights) != 0 {
		t.Fatalf("malformed weights = %#v, want none", directives.Weights)
	}
	if len(directives.Limits) != 0 {
		t.Fatalf("malformed limits = %#v, want none", directives.Limits)
	}
}

// TestUnknownPlanNameLeavesTheGateClear locks [08 R-AI-01 §12]: a `plan`
// directive clears the gate before walking its arguments and sets it only on a
// match, so a name outside any/easy/medium/hard disables every directive after
// it until the next `plan`. The relationship under test is that the weight
// authored under the unknown plan is absent while the identical weight
// authored under a valid plan is present.
func TestUnknownPlanNameLeavesTheGateClear(t *testing.T) {
	profile, err := ParseAIProfile([]byte(
		"plan hard\n"+
			"weight ARMFLAK 0.5\n"+
			"plan nonsense\n"+
			"weight ARMCK 0.5\n"), "ai/default.txt", Provenance{})
	if err != nil {
		t.Fatalf("ParseAIProfile: %v", err)
	}
	hard := profile.Plans["hard"]
	if hard == nil {
		t.Fatalf("plan hard produced no table")
	}
	if _, ok := hard.Weights[CanonicalKey("ARMFLAK")]; !ok {
		t.Fatalf("weight under an open gate was dropped")
	}
	for name, pl := range profile.Plans {
		if _, ok := pl.Weights[CanonicalKey("ARMCK")]; ok {
			t.Fatalf("weight after `plan nonsense` reached plan %q: the gate did not clear", name)
		}
	}
}
