package content

import (
	"sort"
	"strings"
	"testing"
)

// The lower-bound winner must survive same-ID parsing and current-value edits;
// it is not the lexical first spelling [06 R-DMG-01 §1].
func TestDamageOverrideKeepsCompiledWinnerAndLiveValue(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "weapons/lookup.tdf", data: "[old]{ID=7;[DAMAGE]{ARMCOM=20;armck=-70000;}}[new]{ID=7;[DAMAGE]{armcom=30;}}"})
	weapons, _, err := CompileWeaponsWithDuplicates(fs, RetailLimits())
	if err != nil {
		t.Fatal(err)
	}
	w := weapons["new"]
	for _, name := range []string{"ARMCOM", "armcom", "ArMcOm"} {
		if got, ok := w.DamageOverride(name); !ok || got != 30 {
			t.Fatalf("lookup %q = %d, %t; want the later variant's 30", name, got, ok)
		}
	}
	if got, ok := w.DamageOverride("ARMCK"); !ok || got != -70000 {
		t.Fatalf("signed override = %d, %t", got, ok)
	}
	keys := w.DamageKeysSorted()
	keys[0] = "changed"
	w.Damage["armcom"] = 0
	if got, ok := w.DamageOverride("armcom"); !ok || got != 0 {
		t.Fatalf("live zero override = %d, %t", got, ok)
	}
	for _, name := range []string{"", "missing", " armcom "} {
		if _, ok := w.DamageOverride(name); ok {
			t.Fatalf("unexpected match for %q", name)
		}
	}
}

func TestDamageOverrideAuthoredMapRemainsLive(t *testing.T) {
	w := &WeaponDef{Damage: map[string]int32{"armcom": 30, "ARMCOM": 20}}
	if got, ok := w.DamageOverride("ArMcOm"); !ok || got != 20 {
		t.Fatalf("authored map lexical fallback = %d, %t", got, ok)
	}
	delete(w.Damage, "ARMCOM")
	w.Damage["corcom"] = -3
	if got, ok := w.DamageOverride("CORCOM"); !ok || got != -3 {
		t.Fatalf("new authored key = %d, %t", got, ok)
	}
	if got, ok := w.DamageOverride("ARMCOM"); !ok || got != 30 {
		t.Fatalf("removed authored key still shadows remaining key: %d, %t", got, ok)
	}
}

func TestCompiledDamageLookupDoesNotCopyTable(t *testing.T) {
	w := &WeaponDef{Damage: map[string]int32{"armcom": 20}, damageOrder: []string{"armcom"}}
	if got := testing.AllocsPerRun(100, func() { w.DamageOverride("armcom") }); got != 0 {
		t.Fatalf("lowercase compiled lookup allocates %v objects", got)
	}
}

// Keep the previous caller's full lookup as a local benchmark reference. This
// makes allocation changes inspectable without depending on the host's load.
func BenchmarkCompiledDamageLookup(b *testing.B) {
	w := &WeaponDef{Damage: map[string]int32{"ARMCK": 20, "ARMCOM": 30, "CORCOM": 40}, damageOrder: []string{"ARMCK", "ARMCOM", "CORCOM"}}
	b.Run("copy-table", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			keys := w.DamageKeysSorted()
			i := sort.Search(len(keys), func(i int) bool { return strings.ToLower(keys[i]) >= strings.ToLower("ARMCOM") })
			if i >= len(keys) || !strings.EqualFold(keys[i], "ARMCOM") || w.Damage[keys[i]] != 30 {
				b.Fatal("reference lookup changed")
			}
		}
	})
	b.Run("direct-table", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if got, ok := w.DamageOverride("ARMCOM"); !ok || got != 30 {
				b.Fatal("lookup changed")
			}
		}
	})
}
