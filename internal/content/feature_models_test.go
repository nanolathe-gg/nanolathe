package content

import (
	"reflect"
	"strings"
	"testing"
)

func TestFeatureClosureIncludesEverySuccessorOnce(t *testing.T) {
	dead := &FeatureDef{Object: "shared"}
	root := &FeatureDef{Object: "shared", FeatureDead: "dead", FeatureReclamate: "reclaimed", FeatureBurnt: "burnt"}
	reclaimed := &FeatureDef{FeatureDeadDef: dead, FeatureDead: "dead"}
	burnt := &FeatureDef{FeatureBurntDef: root, FeatureBurnt: "root"}
	cat := &Catalog{Features: map[string]*FeatureDef{
		"root": root, "alias": root, "dead": dead, "reclaimed": reclaimed, "burnt": burnt,
		"unused": {Object: "missing"},
	}}
	want := []*FeatureDef{root, burnt, dead, reclaimed}
	for _, roots := range [][]string{{"ROOT", "alias", "dead", " "}, {"dead", "alias", "ROOT"}} {
		got, err := cat.FeatureClosure(roots)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("closure = %v, %v; want aliased root, burnt, dead, reclaimed", got, err)
		}
	}
	fs := &countingModelFS{fixtureFS: newFixtureFS(t, fixtureFile{path: "objects3d/shared.3do", data: authoredRequiredModel3DO(t, 0)})}
	if err := cat.ValidateFeatureModels(fs, []string{"root", "alias"}); err != nil {
		t.Fatal(err)
	}
	if fs.modelReads != 1 {
		t.Fatalf("shared model reads = %d, want 1", fs.modelReads)
	}
	if len(cat.Features) != 6 || cat.Features["unused"].Object != "missing" {
		t.Fatal("admission modified discovered definitions")
	}
}

func TestFeatureClosureSortsByDefinitionIdentity(t *testing.T) {
	a := &FeatureDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "zebra"}}
	b := &FeatureDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "alpha"}}
	cat := &Catalog{Features: map[string]*FeatureDef{"a": a, "b": b}}
	got, err := cat.FeatureClosure([]string{"a", "b"})
	if err != nil || !reflect.DeepEqual(got, []*FeatureDef{b, a}) {
		t.Fatalf("closure = %v, %v; want canonical identities alpha then zebra", got, err)
	}
}

func TestFeatureModelAdmissionRetainsRequestedFailure(t *testing.T) {
	for _, link := range []string{"root", "dead", "reclaimed", "burnt"} {
		t.Run(link, func(t *testing.T) {
			root := &FeatureDef{}
			missing := &FeatureDef{Object: "missing"}
			cat := &Catalog{Features: map[string]*FeatureDef{"root": root, "missing": missing}}
			switch link {
			case "root":
				root.Object = "missing"
			case "dead":
				root.FeatureDead = "missing"
			case "reclaimed":
				root.FeatureReclamate = "missing"
			case "burnt":
				root.FeatureBurnt = "missing"
			}
			err := cat.ValidateFeatureModels(newFixtureFS(t), []string{"root"})
			if err == nil || !strings.Contains(err.Error(), "logical path objects3d/missing.3do") {
				t.Fatalf("requested missing model = %v", err)
			}
		})
	}
	cat := &Catalog{Features: map[string]*FeatureDef{"z": {Object: "z"}, "a": {Object: "a"}}}
	err := cat.ValidateFeatureModels(newFixtureFS(t), []string{"z", "a"})
	if err == nil || !strings.Contains(err.Error(), "logical path objects3d/a.3do") {
		t.Fatalf("first failure = %v, want sorted model path a", err)
	}
}

func TestFeatureClosureMissingNameKeepsFatalDiagnostic(t *testing.T) {
	cat := &Catalog{Features: map[string]*FeatureDef{"root": {FeatureDead: "absent"}}}
	for _, names := range [][]string{{"absent"}, {"root"}} {
		_, err := cat.FeatureClosure(names)
		if err == nil || err.Error() != `Record "absent" missing from feature files` {
			t.Fatalf("feature miss = %v", err)
		}
	}
	var empty *Catalog
	if err := empty.ValidateFeatureModels(newFixtureFS(t), []string{" ", ""}); err != nil {
		t.Fatalf("blank root: %v", err)
	}
}

func TestUnitCorpseModelsIgnoreUnrequestedDefinitions(t *testing.T) {
	features := map[string]*FeatureDef{
		"corpse": {FeatureDead: "heap"},
		"heap":   {Object: "missing"},
		"unused": {Object: "also_missing"},
	}
	// An unresolved corpse means no wreck. A resolved corpse's successor is a
	// model requirement, even when another retained record has the same name.
	unknown := &UnitDef{UnitName: "duplicate", Corpse: "absent"}
	known := &UnitDef{UnitName: "duplicate", Corpse: "CoRpSe"}
	got, err := unitCorpseFeatures([]*UnitDef{unknown}, features)
	if err != nil || len(got) != 0 {
		t.Fatalf("unresolved corpse models = %v, %v", got, err)
	}
	got, err = unitCorpseFeatures([]*UnitDef{unknown, known}, features)
	if err != nil || len(got) != 1 || got["objects3d/missing.3do"] != features["heap"] {
		t.Fatalf("resolved corpse models = %v, %v", got, err)
	}
	if err := validateRequiredRecordModels(newFixtureFS(t), nil, nil, got); err == nil || !strings.Contains(err.Error(), "objects3d/missing.3do") {
		t.Fatalf("resolved corpse successor model = %v", err)
	}
}
