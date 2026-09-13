package client

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

func TestStrategicIconAuthoredLevels(t *testing.T) {
	for _, tc := range []struct {
		category           string
		level              int
		authored, conflict bool
	}{
		{"KBOT level3", 3, true, false},
		{"CONSTR LEVEL2 LEVEL02 LEVEL2", 2, true, false},
		{"LEVEL1 LEVEL2 LEVEL1", 0, true, true},
		{"LEVEL2 LEVEL LEVEL0 LEVEL-3", 2, true, false},
		{"LEVEL20", 20, true, false},
		{"LEVEL LEVEL0 LEVEL-1 LEVEL+2 LEVEL2x LEVEL2.0 XLEVEL3 LEVEL999999999999999999999", 0, false, false},
		{"", 0, false, false},
	} {
		t.Run(tc.category, func(t *testing.T) {
			got, authored := strategicAuthoredIconLevel(tc.category)
			if got.Level != tc.level || authored != tc.authored || (got.Unresolved != "") != tc.conflict {
				t.Fatalf("got %+v, authored=%t", got, authored)
			}
		})
	}
}

func TestStrategicIconLevelMinimumFactoryRoutes(t *testing.T) {
	cat := &content.Catalog{Units: make(map[string]*content.UnitDef), BuildMenus: make(map[string]*content.BuildMenuPage)}
	add := func(name string, builder bool, bmcode uint8, products ...string) *content.UnitDef {
		u := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: name}, UnitName: name, Builder: builder, BMCode: bmcode}
		cat.Units[name] = u
		if products != nil {
			cat.BuildMenus[name] = &content.BuildMenuPage{Buttons: products}
		}
		return u
	}
	add("commander", true, 1, "basic", "direct", "support", "authored", "conflict", "falsebuilder").Commander = true
	add("basic", true, 0, "constructor").CanMove = true // Stock factories set CanMove [SC21].
	add("constructor", true, 1, "advanced", "basic", "economy")
	add("advanced", true, 0, "advancedconstructor", "combat").Category = "LEVEL8"
	add("advancedconstructor", true, 1, "ultimate", "support")
	add("ultimate", true, 0, "heavy")
	add("heavy", false, 1).Category = "LEVEL" // Bare malformed token permits inference.
	add("combat", false, 1)
	add("economy", false, 0)
	add("direct", false, 0)
	add("support", true, 0) // An empty-menu repair builder is not a factory.
	add("authored", false, 1).Category = "LEVEL7"
	add("conflict", false, 1).Category = "LEVEL2 LEVEL3"
	add("falsebuilder", false, 0, "unreachable")
	add("unreachable", false, 1)
	add("decoy", true, 1, "heavy").Category = "COMMANDER"
	add("cyclea", true, 1, "cycleb")
	add("cycleb", true, 1, "cyclea")
	add("authoredunreachable", false, 1).Category = "LEVEL4"
	add("conflictunreachable", false, 1).Category = "LEVEL2 LEVEL3"
	// A later root offers fewer factories for combat, while the advanced factory
	// uses graph depth despite its conflicting authored LEVEL8.
	add("secondcommander", true, 1, "combat").Commander = true
	cat.BuildMenus["ultimate"].BaseButtonCount = 0 // A download-only final product is traversed.

	graph := strategicIconBuildLevels(cat)
	got := make(map[string]strategicIconLevel)
	for _, name := range cat.SortedUnitKeys() {
		got[name] = strategicResolveIconLevel(cat.Units[name].Category, graph[name])
	}
	for name, level := range map[string]int{
		"commander": 0, "basic": 1, "constructor": 1, "advanced": 2,
		"advancedconstructor": 2, "ultimate": 3, "heavy": 3, "combat": 0,
		"economy": 1, "direct": 0, "support": 0, "authored": 0, "conflict": 0, "authoredunreachable": 4,
	} {
		if g := got[name]; g.Level != level || g.Unresolved != "" {
			t.Errorf("%s: got %+v, want level %d", name, g, level)
		}
	}
	for _, name := range []string{"conflictunreachable", "decoy", "unreachable", "cyclea", "cycleb"} {
		if got[name].Unresolved == "" {
			t.Errorf("%s must remain unresolved: %+v", name, got[name])
		}
	}
	if !strings.Contains(got["heavy"].Evidence, "commander -> basic -> constructor -> advanced -> advancedconstructor -> ultimate -> heavy") {
		t.Errorf("minimum factory route missing: %s", got["heavy"].Evidence)
	}
	if graph["combat"].Evidence != strategicIconBuildLevels(cat)["combat"].Evidence {
		t.Fatal("minimum route evidence must be reproducible")
	}
	if !strings.Contains(got["advanced"].Evidence, "LEVEL8") || !strings.Contains(got["conflict"].Evidence, "LEVEL2, LEVEL3") {
		t.Fatal("graph precedence must preserve differing authored evidence")
	}
	if missing := strategicResolveIconLevel("LEVEL5", strategicIconLevel{}); missing.Level != 5 || missing.Unresolved != "" {
		t.Fatalf("record missing from graph should use its own authored fallback: %+v", missing)
	}
	if len(strategicIconBuildLevels(nil)) != 0 {
		t.Fatal("nil catalog must have no levels")
	}
}

// This read-only audit intentionally compares presentation inferences with
// authored categories; it makes no assertion that those meanings coincide.
func TestStrategicIconLevelRetailAudit(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	graph := strategicIconBuildLevels(cat)
	for _, name := range []string{"armcom", "armck", "armack", "armlab", "armalab", "armmoho", "corcom", "corck", "corack", "corlab", "coralab", "corgant", "corkrog", "corssub"} {
		u, ok := cat.Unit(name)
		if !ok {
			continue
		}
		t.Logf("%s category=%q resolved=%+v graph=%+v", name, u.Category, strategicResolveIconLevel(u.Category, graph[name]), graph[name])
	}
	for _, name := range cat.SortedUnitKeys() {
		u, _ := cat.Unit(name)
		if _, authored := strategicAuthoredIconLevel(u.Category); authored {
			continue
		}
		t.Logf("unauthored %s: %+v", name, strategicResolveIconLevel(u.Category, graph[name]))
	}
}
