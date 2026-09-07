package mission

import "testing"

func TestNetworkSchemaContinuesThroughAllMatchingSections(t *testing.T) {
	ota := mustLoadOTA(t, `[GlobalHeader] {
 [Schema 0] { Type=Network 1; [specials] { [a] { specialwhat=StartPos1; } } }
 [Schema 1] { Type=Network 1; [specials] { [a] { specialwhat=StartPos1; } [b] { specialwhat=StartPos2; } } }
 [Schema 2] { Type=Network 2; [specials] { [a] { specialwhat=StartPos1; } [b] { specialwhat=StartPos2; } } }
 [Schema 3] { Type=Network 3; [specials] { [a] { specialwhat=StartPos1; } [b] { specialwhat=StartPos2; } [c] { specialwhat=StartPos3; } } }
 }`)
	for _, tc := range []struct {
		want int
		name string
	}{{1, "Schema 0"}, {2, "Schema 2"}, {3, "Schema 3"}, {4, "Schema 3"}, {0, "Schema 3"}} {
		got, err := SelectNetworkSchema(ota, tc.want)
		if err != nil || got.Name != tc.name {
			t.Fatalf("want count %d: %v, %v; expected %s", tc.want, got, err, tc.name)
		}
	}
}
