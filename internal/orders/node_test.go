package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

func nodeTestCatalog() *content.Catalog {
	one := &content.UnitDef{UnitName: "one"}
	one.CanonicalKey = content.CanonicalKey(one.UnitName)
	two := &content.UnitDef{UnitName: "two"}
	two.CanonicalKey = content.CanonicalKey(two.UnitName)
	return &content.Catalog{Units: map[string]*content.UnitDef{
		one.CanonicalKey: one,
		two.CanonicalKey: two,
	}}
}

func TestMobileBuildNodeCarriesRetryCounterNotOrientation(t *testing.T) {
	// [04 §3.2][R-ORDER-02 §1] the mobile-build record's third parameter is
	// the blocked-area retry counter, zeroed by the handler's setup path —
	// a fresh node starts the budget at zero and stores no orientation.
	cat := nodeTestCatalog()
	for _, tc := range []struct {
		name string
		get  func() Node
	}{
		{name: "mobile", get: func() Node { return NewMobileBuildNode(cat, "one", 4, 8, 0x1234, 1, 2, 3, false) }},
		{name: "mobile explicit id", get: func() Node {
			return NewMobileBuildNodeWithID(Lookup("VTOL_MobileBuild"), cat, "one", 4, 8, 0x1234, 1, 2, 3, false)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := tc.get()
			if n.Param3 != 0 {
				t.Fatalf("Param3=%d, want the retry counter zeroed", n.Param3)
			}
			if n.Param1 != 1 || n.Param2 != 1 {
				t.Fatalf("Param1=%d Param2=%d, want index 1 count 1", n.Param1, n.Param2)
			}
			if n.GoalX != 4 || n.GoalZ != 8 {
				t.Fatalf("site anchor GoalX=%d GoalZ=%d, want 4/8", n.GoalX, n.GoalZ)
			}
		})
	}
}

func TestBuildConstructorsKeepZeroForUnresolvedCatalogDefinition(t *testing.T) {
	constructors := []struct {
		name string
		get  func(*content.Catalog, string) Node
	}{
		{name: "factory", get: func(cat *content.Catalog, key string) Node {
			return NewFactoryBuildNode(cat, key, 1, 2, 3, false)
		}},
		{name: "mobile", get: func(cat *content.Catalog, key string) Node {
			return NewMobileBuildNode(cat, key, 0, 0, 0, 1, 2, 3, false)
		}},
		{name: "mobile explicit id", get: func(cat *content.Catalog, key string) Node {
			return NewMobileBuildNodeWithID(Lookup("MobileBuild"), cat, key, 0, 0, 0, 1, 2, 3, false)
		}},
	}
	for _, tc := range constructors {
		t.Run(tc.name, func(t *testing.T) {
			for _, test := range []struct {
				name string
				cat  *content.Catalog
				key  string
				want uint32
			}{
				{name: "known", cat: nodeTestCatalog(), key: "one", want: 1},
				{name: "unknown", cat: nodeTestCatalog(), key: "missing", want: 0},
				{name: "nil catalog", cat: nil, key: "one", want: 0},
			} {
				t.Run(test.name, func(t *testing.T) {
					n := tc.get(test.cat, test.key)
					if n.Param1 != test.want {
						t.Fatalf("Param1=%d, want %d for %q", n.Param1, test.want, test.key)
					}
				})
			}
		})
	}
}
