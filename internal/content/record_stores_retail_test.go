//go:build retail

package content

import "testing"

// TestRetailGateWreckReclaimYieldWraps pins the one stock-visible consequence
// of the reclaim-yield mask: the two Gate wreck/heap pairs author six-figure
// `metal`, and retail stores float(uint16(value)), so the compiled payout is
// the wrapped value, not the authored one
// [05 "Feature catalog and placement"] [02 "Feature record"].
func TestRetailGateWreckReclaimYieldWraps(t *testing.T) {
	catalog := compiledRetailCatalog(t)
	for _, tc := range []struct {
		name string
		want int32
	}{
		{"armgate_dead", 40100},
		{"armgate_heap", 20050},
		{"corgate_dead", 26916},
		{"corgate_heap", 13458},
	} {
		def := catalog.Features[CanonicalKey(tc.name)]
		if def == nil {
			t.Fatalf("%s: absent from the reference install's feature catalog", tc.name)
		}
		if def.Metal != tc.want {
			t.Fatalf("%s: metal = %d, want the wrapped %d", tc.name, def.Metal, tc.want)
		}
	}
}

// TestRetailCloakableWithoutRadiusGetsEighty pins the one stock definition the
// derived `mincloakdistance` substitution reaches: it authors `cloakcost` and
// omits the radius, so the compiled definition carries 80
// [02 "Unit record"] [03 R-VIS-01 §4] pass 4.
func TestRetailCloakableWithoutRadiusGetsEighty(t *testing.T) {
	catalog := compiledRetailCatalog(t)
	found := 0
	for _, def := range catalog.UnitRecords() {
		if def == nil || def.CloakCost <= 0 {
			continue
		}
		if def.MinCloakDistance == 0 {
			t.Fatalf("%s: cloak-capable definition compiled mincloakdistance 0; the derived 80 did not run", def.UnitName)
		}
		if def.MinCloakDistance == 80 {
			found++
		}
	}
	if found == 0 {
		t.Fatal("no cloak-capable definition carries the derived 80; the census expected exactly one")
	}
}
