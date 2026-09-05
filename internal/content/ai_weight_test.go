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

// TestProfileParserAppliesDefaultsOnMalformedLines locks [08 R-AI-01 §18]:
// a `weight`/`limit` directive whose argument is absent or unconvertible
// applies the established default (0.0 for `weight`, 0 for `limit`) rather
// than being dropped, because the definition's `ai_weight` fragment is
// dispatched "exactly as a line of ai\default.txt is dispatched" with no
// argument-conversion gate. `unknown` stays dropped — it is outside the
// three-keyword table [08 R-AI-01 §12], not a malformed argument.
func TestProfileParserAppliesDefaultsOnMalformedLines(t *testing.T) {
	const fragment = "weight ARMFLAK\nweight ARMFLAK not-a-number\nunknown ARMFLAK 0.5\nlimit ARMFLAK\n"

	// Dropping the directive would leave the map entry absent (the accessor's
	// 100/-1 default would then mask the difference from an applied 0.0/0),
	// so the assertion checks the map directly.
	profile, err := ParseAIProfile([]byte("plan any\n"+fragment), "ai/default.txt", Provenance{})
	if err != nil {
		t.Fatalf("ParseAIProfile: %v", err)
	}
	any := profile.Plans["any"]
	if any == nil {
		t.Fatalf("plan any produced no table")
	}
	weight, ok := any.Weights[CanonicalKey("armflak")]
	if !ok {
		t.Fatalf("weight entry for armflak absent: the profile reader dropped the malformed directive")
	}
	if weight != 0 {
		t.Fatalf("weight after two 0.0-factor directives = %d, want 0 (100 x 0.0 x 0.0, truncated and clamped)", weight)
	}
	if limit, ok := any.Limits[CanonicalKey("armflak")]; !ok || limit != 0 {
		t.Fatalf("profile limit = %v (present=%v), want armflak/0 default", limit, ok)
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
