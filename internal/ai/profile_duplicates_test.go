package ai

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The map-only fixture seam carries two retained records under distinct fixture
// keys. Both records author the same UnitName; the ordinary name resolves to
// the first. CompileCategories supplies their separate membership bits, while
// UnitRecords preserves both pointers in index order [02 R-CAT-01 §5].
func duplicateProfileCatalog(t *testing.T, fragment string) *content.Catalog {
	t.Helper()
	first := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "dup"}, UnitName: "DUP", Category: "GROUP", Downloadable: true}
	later := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "dup"}, UnitName: "DUP", Category: "GROUP", Downloadable: true, AIWeight: fragment}
	defs := map[string]*content.UnitDef{"dup": first, "dup_later_fixture": later}
	categories, err := content.CompileCategories(defs)
	if err != nil {
		t.Fatal(err)
	}
	return &content.Catalog{Units: defs, Categories: categories}
}

func TestProfileDuplicateExactLocksAndCategoryExpansion(t *testing.T) {
	cat := duplicateProfileCatalog(t, "weight GROUP 0.5")
	profile := grammarProfile("plan hard\nweight DUP 0.8\nlimit DUP 2\nlimit GROUP 5\n", DifficultyHard)
	profile.ApplyUnitDefinitions(cat)
	first, later := cat.UnitRecords()[0], cat.UnitRecords()[1]
	// The exact name locks only the first record. The later record's category
	// fragment still runs once in each pass and scales itself twice [08 R-AI-01 §§12,18].
	if got := profile.WeightForIndex(1); got != 80 {
		t.Fatalf("first weight=%d, want80", got)
	}
	if got := profile.WeightForIndex(2); got != 25 {
		t.Fatalf("later weight=%d, want25", got)
	}
	if profile.LimitForIndex(1) != 2 || profile.LimitForIndex(2) != 5 {
		t.Fatal("exact limit lock spread to the later duplicate")
	}
	if profile.WeightFor("DUP") != 80 || profile.LimitFor("dup") != 2 {
		t.Fatal("name views did not select first equal record")
	}
	if profile.WeightForDefinition(later) != 25 || profile.WeightForDefinition(first) != 80 {
		t.Fatal("definition accessor collapsed duplicate identity")
	}
	if profile.LimitForDefinition(controlByteComputer, later) != 5 || profile.LimitForDefinition(1, later) != -1 {
		t.Fatal("definition limit lost record identity or control-byte gate")
	}
	profile.ApplyUnitDefinitionsForPlayers(cat, 2)
	if profile.WeightForIndex(2) != 25 {
		t.Fatal("same binding applied the fragment again")
	}
}

func TestProfileDuplicateRebindingAndDifficultyInvalidateDerivedState(t *testing.T) {
	cat := duplicateProfileCatalog(t, "weight GROUP 0.5")
	profile := grammarProfile("plan hard\nweight DUP 0.8\nplan easy\nweight DUP 0.4\n", DifficultyHard)
	profile.ApplyUnitDefinitions(cat)
	clone := cat.Clone()
	profile.ApplyUnitDefinitions(clone)
	if profile.WeightForIndex(1) != 80 || profile.WeightForIndex(2) != 25 {
		t.Fatal("catalog clone changed profile results")
	}
	// Profiles have no custom copy/serialization hook: derived tables are
	// replaced on reapply, so a value copy can change difficulty independently.
	copied := *profile
	copied.SetDifficulty(DifficultyEasy)
	if copied.WeightForIndex(1) != 100 || copied.LimitForIndex(1) != -1 {
		t.Fatal("difficulty retained stale derived record state")
	}
	copied.ApplyUnitDefinitions(clone)
	if copied.WeightForIndex(1) != 40 || copied.WeightForIndex(2) != 25 {
		t.Fatal("difficulty replay lost duplicate identity")
	}
	if profile.WeightForIndex(1) != 80 {
		t.Fatal("copied profile mutated original derived table")
	}
	copied.SetDifficulty(DifficultyHard)
	copied.ApplyUnitDefinitionsForPlayers(clone, 2)
	if copied.WeightForIndex(2) != 6 {
		t.Fatalf("two-player replay=%d, want6", copied.WeightForIndex(2))
	}
}

func TestProfileFixtureRebindingDoesNotCompoundDerivedWeights(t *testing.T) {
	cat := duplicateProfileCatalog(t, "weight GROUP 0.5")
	profile := &Profile{Weight: map[string]int32{"dup": 80}}
	profile.ApplyUnitDefinitions(cat)
	before := profile.WeightForIndex(1)
	profile.ApplyUnitDefinitions(cat.Clone())
	if profile.WeightForIndex(1) != before {
		t.Fatal("fixture catalog rebind compounded already derived weights")
	}
}
