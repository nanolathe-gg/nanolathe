package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// grammarCatalog builds the smallest catalog the profile grammar needs: three
// definitions carrying authored category tokens, linked so every definition has
// the stable 1-based catalog ID the category bitsets index [R-P0-03].
func grammarCatalog(t *testing.T, weights map[string]string) *content.Catalog {
	t.Helper()
	mk := func(name, categories, aiWeight string) *content.UnitDef {
		return &content.UnitDef{
			DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey(name)},
			UnitName:         name,
			Category:         categories,
			AIWeight:         aiWeight,
			Downloadable:     true,
		}
	}
	units := map[string]*content.UnitDef{
		"armck":    mk("ARMCK", "ARM LEVEL1 CONSTR", weights["armck"]),
		"armflash": mk("ARMFLASH", "ARM LEVEL1", weights["armflash"]),
		"corck":    mk("CORCK", "CORE LEVEL1 CONSTR", weights["corck"]),
	}
	registry, err := content.CompileCategories(units)
	if err != nil {
		t.Fatalf("CompileCategories: %v", err)
	}
	return &content.Catalog{Units: units, Categories: registry}
}

// grammarProfile builds a text-loaded profile from directive text at a chosen
// active difficulty, without going through the VFS.
func grammarProfile(text string, active Difficulty) *Profile {
	return &Profile{
		Plan:       active,
		directives: content.ParseAIDirectives([]byte(text)),
		textLoaded: true,
	}
}

// TestPlanGateHonoursAnyOnlyInTheFirstArgument locks the retail quirk of
// [08 R-AI-01 §12]: a `plan` walks its arguments and compares THE FIRST
// argument against `any` and the CURRENT argument against the active
// difficulty's keyword, so `any` in a later position never opens the gate.
func TestPlanGateHonoursAnyOnlyInTheFirstArgument(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		active Difficulty
		want   bool
	}{
		{"any first", []string{"any", "easy"}, DifficultyHard, true},
		{"any second", []string{"easy", "any"}, DifficultyHard, false},
		{"any third", []string{"easy", "medium", "any"}, DifficultyHard, false},
		{"named late", []string{"easy", "medium", "hard"}, DifficultyHard, true},
		{"named only", []string{"easy"}, DifficultyHard, false},
		{"no arguments", nil, DifficultyHard, false},
		{"unknown word", []string{"sometimes"}, DifficultyHard, false},
	} {
		if got := planGateOpen(tc.args, tc.active); got != tc.want {
			t.Fatalf("%s: planGateOpen(%v, %q) = %v, want %v", tc.name, tc.args, tc.active, got, tc.want)
		}
	}

	// The same quirk through the whole applier: only the honoured form scales.
	honoured := grammarProfile("plan any easy\nweight ARMFLASH 0.5\n", DifficultyHard)
	honoured.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := honoured.WeightFor("ARMFLASH"); got != 50 {
		t.Fatalf("`plan any easy` under hard: weight = %d, want 50", got)
	}
	ignored := grammarProfile("plan easy any\nweight ARMFLASH 0.5\n", DifficultyHard)
	ignored.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := ignored.WeightFor("ARMFLASH"); got != 100 {
		t.Fatalf("`plan easy any` under hard: weight = %d, want the default 100", got)
	}
}

// TestProfileNameMatcherExactVersusCategory locks the matcher of
// [08 R-AI-01 §12]: a name that hits the definition catalog names exactly one
// type; a miss expands to the whole category bitset registered for the name;
// a name that is neither expands to nothing.
func TestProfileNameMatcherExactVersusCategory(t *testing.T) {
	exact := grammarProfile("plan hard\nweight ARMFLASH 0.5\n", DifficultyHard)
	exact.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := exact.WeightFor("ARMFLASH"); got != 50 {
		t.Fatalf("exact naming: ARMFLASH = %d, want 50", got)
	}
	for _, other := range []string{"ARMCK", "CORCK"} {
		if got := exact.WeightFor(other); got != 100 {
			t.Fatalf("exact naming must not touch %s: %d, want 100", other, got)
		}
	}

	category := grammarProfile("plan hard\nweight LEVEL1 0.5\n", DifficultyHard)
	category.ApplyUnitDefinitions(grammarCatalog(t, nil))
	for _, member := range []string{"ARMCK", "ARMFLASH", "CORCK"} {
		if got := category.WeightFor(member); got != 50 {
			t.Fatalf("category naming: %s = %d, want 50", member, got)
		}
	}

	partial := grammarProfile("plan hard\nweight CONSTR 0.5\n", DifficultyHard)
	partial.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := partial.WeightFor("ARMFLASH"); got != 100 {
		t.Fatalf("CONSTR does not contain ARMFLASH: %d, want 100", got)
	}
	if got := partial.WeightFor("CORCK"); got != 50 {
		t.Fatalf("CONSTR contains CORCK: %d, want 50", got)
	}

	unknown := grammarProfile("plan hard\nweight NOSUCHTHING 0.5\n", DifficultyHard)
	unknown.ApplyUnitDefinitions(grammarCatalog(t, nil))
	for _, member := range []string{"ARMCK", "ARMFLASH", "CORCK"} {
		if got := unknown.WeightFor(member); got != 100 {
			t.Fatalf("unmatched name touched %s: %d, want 100", member, got)
		}
	}
}

// TestWeightLockPrecedenceIsSetOnlyByAnExactNaming locks the precedence case
// [08 R-AI-01 §12] names verbatim: `weight ARMCK 2.0` locks ARMCK's weight
// against any later per-definition text, while `weight LEVEL1 2.0` scales every
// member of the LEVEL1 category and locks none of them.
func TestWeightLockPrecedenceIsSetOnlyByAnExactNaming(t *testing.T) {
	authored := map[string]string{
		"armck":    "weight ARMCK 0.5",
		"armflash": "weight ARMFLASH 0.5",
	}

	locked := grammarProfile("plan hard\nweight ARMCK 2.0\n", DifficultyHard)
	locked.ApplyUnitDefinitions(grammarCatalog(t, authored))
	// 100 * 2.0 clamps to 100 and the exact naming locks the type, so ARMCK's
	// own ai_weight fragment never runs.
	if got := locked.WeightFor("ARMCK"); got != 100 {
		t.Fatalf("exact naming must lock ARMCK: %d, want 100", got)
	}
	// ARMFLASH was never named, so its own fragment still applies.
	if got := locked.WeightFor("ARMFLASH"); got != 50 {
		t.Fatalf("unlocked ARMFLASH fragment: %d, want 50", got)
	}

	unlocked := grammarProfile("plan hard\nweight LEVEL1 2.0\n", DifficultyHard)
	unlocked.ApplyUnitDefinitions(grammarCatalog(t, authored))
	if got := unlocked.WeightFor("ARMCK"); got != 50 {
		t.Fatalf("a category naming locks nothing: ARMCK = %d, want 50", got)
	}
	if got := unlocked.WeightFor("ARMFLASH"); got != 50 {
		t.Fatalf("a category naming locks nothing: ARMFLASH = %d, want 50", got)
	}
}

// TestLimitLockIsSeparateFromTheWeightLock locks the "separate lock vectors"
// half of [08 R-AI-01 §12]: an exact `weight` naming does not stop a later
// `limit` from reaching the same type, and vice versa.
func TestLimitLockIsSeparateFromTheWeightLock(t *testing.T) {
	authored := map[string]string{"armck": "weight ARMCK 0.5\nlimit ARMCK 9"}

	p := grammarProfile("plan hard\nweight ARMCK 2.0\nlimit LEVEL1 3\n", DifficultyHard)
	p.ApplyUnitDefinitions(grammarCatalog(t, authored))
	if got := p.WeightFor("ARMCK"); got != 100 {
		t.Fatalf("the weight lock still holds: %d, want 100", got)
	}
	// `limit LEVEL1 3` is a category naming, so it sets no limit lock and the
	// definition's own fragment reaches the limit pass.
	if got := p.LimitFor("ARMCK"); got != 9 {
		t.Fatalf("limit lock is separate: ARMCK limit = %d, want 9", got)
	}
	if got := p.LimitFor("ARMFLASH"); got != 3 {
		t.Fatalf("category limit reached ARMFLASH: %d, want 3", got)
	}

	q := grammarProfile("plan hard\nlimit ARMCK 3\n", DifficultyHard)
	q.ApplyUnitDefinitions(grammarCatalog(t, authored))
	if got := q.LimitFor("ARMCK"); got != 3 {
		t.Fatalf("exact limit naming must lock ARMCK: %d, want 3", got)
	}
	// The weight lock is untouched by the limit directive, so ARMCK's fragment
	// still scales its weight.
	if got := q.WeightFor("ARMCK"); got != 50 {
		t.Fatalf("weight lock must stay clear: %d, want 50", got)
	}
}

// TestLimitAppliesOnlyToControlByteTwo locks the slot gate of
// [08 R-AI-01 §12]: `limit` applies only to slots whose control byte is 2,
// while the weight table applies to every slot that has a manager.
func TestLimitAppliesOnlyToControlByteTwo(t *testing.T) {
	p := grammarProfile("plan hard\nlimit ARMCK 3\nweight ARMCK 0.5\n", DifficultyHard)
	p.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := p.LimitForControl(2, "ARMCK"); got != 3 {
		t.Fatalf("control byte 2 limit = %d, want 3", got)
	}
	for _, control := range []uint8{0, 1, 3} {
		if got := p.LimitForControl(control, "ARMCK"); got != -1 {
			t.Fatalf("control byte %d limit = %d, want the unlimited -1", control, got)
		}
	}
	if got := p.WeightFor("ARMCK"); got != 50 {
		t.Fatalf("the weight table is not gated on the control byte: %d, want 50", got)
	}
}

// TestSelectionLimitGateFollowsTheControlByte drives the same gate through the
// candidate selection, which is the one production reader of the limit table.
func TestSelectionLimitGateFollowsTheControlByte(t *testing.T) {
	builder := testBuilder("armfav")
	strat := &Strategic{
		Counts:       map[string]int32{"armfav": 2},
		ClassVectors: map[string]ClassVector{"armfav": {C0: 40, C1: 30, C2: 30}},
	}
	profile := &Profile{Weight: map[string]int32{"armfav": 100}, Limit: map[string]int32{"armfav": 2}}
	sel := &testSelector{player: 1, profile: profile, strategic: strat}
	econ := testEcon(1, 800, 1000, 400, 500, 0, 0, 0, 0)

	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(sel, builder, econ, []string{"armfav"}); ok {
		t.Fatal("control byte 2: count 2 at limit 2 must reject")
	}
	econ.Players[1].ControllerState = 1
	rng.SeedGlobal(1, 0)
	if _, ok := SelectWithCandidates(sel, builder, econ, []string{"armfav"}); !ok {
		t.Fatal("control byte 1: the profile limit does not apply, so the candidate must pass")
	}
}

// TestProfileApplicationDrawsNothing locks the draw ledger for the grammar:
// applying a profile to a catalog consumes no random numbers, so a scenario
// that only loads and applies a profile leaves the simulation stream where it
// started (I4).
func TestProfileApplicationDrawsNothing(t *testing.T) {
	stream := rng.NewSimulation(4242)
	before := stream.Draws()
	p := grammarProfile("plan hard\nweight LEVEL1 0.5\nlimit ARMCK 3\n", DifficultyHard)
	p.ApplyUnitDefinitions(grammarCatalog(t, map[string]string{"armflash": "weight ARMFLASH 0.5"}))
	if got := stream.Draws(); got != before {
		t.Fatalf("profile application consumed %d draws, want 0", got-before)
	}
	if p.WeightFor("ARMFLASH") != 25 {
		t.Fatalf("fixture sanity: ARMFLASH = %d, want 25", p.WeightFor("ARMFLASH"))
	}
}

// TestSetDifficultyReplaysTheDirectiveStream locks the difficulty seam: the
// same profile text scores differently under a different difficulty word,
// because the plan gate compares against it [08 R-AI-01 §12].
func TestSetDifficultyReplaysTheDirectiveStream(t *testing.T) {
	p := grammarProfile("plan easy\nweight ARMFLASH 0.5\nplan hard\nweight ARMCK 0.5\n", DifficultyHard)
	p.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := p.WeightFor("ARMCK"); got != 50 {
		t.Fatalf("hard: ARMCK = %d, want 50", got)
	}
	if got := p.WeightFor("ARMFLASH"); got != 100 {
		t.Fatalf("hard: ARMFLASH = %d, want the default 100", got)
	}

	p.SetDifficulty(DifficultyEasy)
	p.ApplyUnitDefinitions(grammarCatalog(t, nil))
	if got := p.WeightFor("ARMFLASH"); got != 50 {
		t.Fatalf("easy: ARMFLASH = %d, want 50", got)
	}
	if got := p.WeightFor("ARMCK"); got != 100 {
		t.Fatalf("easy: ARMCK = %d, want the default 100", got)
	}
}
