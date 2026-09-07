package content

import (
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
)

// These cases distinguish a packed-bit write from both byte truncation and
// full-integer truth, including the differently formed stores corrected in
// [02 R-KEYS-01 §5]. The parsed source remains available independently.
func TestCompiledFlagStoresRetainOnlyLowBit(t *testing.T) {
	for _, tc := range []struct {
		value int32
		bit   bool
	}{
		{0, false}, {1, true}, {2, false}, {3, true}, {-2, false},
		{-1, true}, {256, false}, {257, true}, {65536, false},
	} {
		t.Run(fmt.Sprint(tc.value), func(t *testing.T) {
			doc, err := formats.ParseTDF([]byte(fmt.Sprintf(`[UNITINFO]{
unitname=FLAG; builder=%[1]d; canfly=%[1]d; onoffable=%[1]d;
mobilestandorders=%[1]d; firestandorders=%[1]d; maxdamage=%[1]d;
}[WEAPON]{lineofsight=%[1]d; paralyzer=%[1]d; targetable=%[1]d;}
[FEATURE]{animating=%[1]d; animtrans=%[1]d; shadtrans=%[1]d;
geothermal=%[1]d; autoreclaimable=%[1]d; reclaimable=%[1]d;}`, tc.value)))
			if err != nil {
				t.Fatal(err)
			}
			section := doc.Root.Section("UNITINFO")
			u := compileUnitSection(section, "units/flag.fbi", "", Provenance{})
			w := compileWeaponSection(doc.Root.Section("WEAPON"), "weapon", Provenance{})
			f := compileFeatureSection(doc.Root.Section("FEATURE"), "feature", Provenance{})
			for name, got := range map[string]bool{
				"builder": u.Builder, "canfly": u.CanFly, "onoffable": u.OnOffable,
				"mobilestandorders": u.MobileStandOrders, "firestandorders": u.FireStandOrders,
				"lineofsight": w.LineOfSight, "paralyzer": w.Paralyzer, "targetable": w.Targetable,
				"geothermal": f.Geothermal, "autoreclaimable": f.Autoreclaimable, "reclaimable": f.Reclaimable,
			} {
				if got != tc.bit {
					t.Errorf("%s=%d compiled as %v, want %v", name, tc.value, got, tc.bit)
				}
			}
			var bit int32
			if tc.bit {
				bit = 1
			}
			if f.Animating != bit || f.AnimTrans != bit || f.ShadTrans != bit {
				t.Errorf("animation flags = %d/%d/%d, want %d", f.Animating, f.AnimTrans, f.ShadTrans, bit)
			}
			if u.MaxDamage != tc.value || section.IntValue("builder", 0) != tc.value {
				t.Fatal("compiling a flag changed the full integer field or lossless source")
			}
		})
	}
}

func TestFeatureAutoreclaimableDefaultSurvivesFlagStore(t *testing.T) {
	doc, err := formats.ParseTDF([]byte("[FEATURE]{}"))
	if err != nil {
		t.Fatal(err)
	}
	f := compileFeatureSection(doc.Root.Section("FEATURE"), "feature", Provenance{})
	if !f.Autoreclaimable || f.Reclaimable {
		t.Fatalf("default autoreclaimable/reclaimable = %v/%v", f.Autoreclaimable, f.Reclaimable)
	}
}
