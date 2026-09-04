//go:build retail

package content

import (
	"sort"
	"testing"
)

// TestStockWeaponLinksComplete locks stock-corpus link completeness [02 §5
// R-CONTENT-02]: with the reference install present, every unit weapon link
// (weapon1..3, explodeas, selfdestructas) resolves to a non-nil record —
// authored or empty, a name matching no record (including an empty one, which
// can never match a record's blanked-out catalog name) resolves to the
// record-0 inactive sentinel, not nil and not an error [06 R-DMG-01 §5].
//
// Retail itself misses exactly the placeholder explosion names below
// (measured on the reference install): ten units author explodeas or
// selfdestructas names that exist in no parsed weapon file, and each of those
// links must carry the record-0 [noweapon] def. A nil link anywhere, or an
// authored-hit sentinel link outside this set, means the weapon family lost
// or gained a record — a packaging regression, not retail behavior. (Every
// unarmed slot in the corpus also resolves to the sentinel by construction —
// an empty name is a miss like any other — so it is not tracked in
// wantMisses, which lists only misses on an *authored* name.)
func TestStockWeaponLinksComplete(t *testing.T) {
	cat := compiledRetailCatalog(t)
	w0, ok := cat.WeaponByID(0)
	if !ok || w0.CanonicalKey != "noweapon" {
		t.Fatalf("record 0 = (%v, %v), want the [noweapon] sentinel", w0, ok)
	}
	wantMisses := []string{
		"armmmkr/explodeas=BIG_BUILDINGEX",
		"armmmkr/selfdestructas=BIG_BUILDING",
		"armseap/explodeas=MEDIUM_UNITEX",
		"armsfig/explodeas=MEDIUM_UNITEX",
		"armsjam/explodeas=MEDIUM_UNITEX",
		"coratl/explodeas=MEDIUM_UNITEX",
		"corlevlr/explodeas=MEDIUM_UNITEX",
		"cormmkr/explodeas=BIG_BUILDINGEX",
		"cormmkr/selfdestructas=BIG_BUILDING",
		"corseap/explodeas=MEDIUM_UNITEX",
		"corsfig/explodeas=MEDIUM_UNITEX",
		"corsjam/explodeas=MEDIUM_UNITEX",
	}
	var gotMisses []string
	for _, key := range cat.SortedUnitKeys() {
		u := cat.Units[key]
		for _, link := range []struct {
			family, authored string
			def              *WeaponDef
		}{
			{"weapon1", u.Weapon1, u.Weapon1Def},
			{"weapon2", u.Weapon2, u.Weapon2Def},
			{"weapon3", u.Weapon3, u.Weapon3Def},
			{"explodeas", u.ExplodeAs, u.ExplodeAsDef},
			{"selfdestructas", u.SelfDestructAs, u.SelfDestructAsDef},
		} {
			if link.def == nil {
				t.Errorf("unit %s %s %q: link is nil, want the record-0 sentinel", key, link.family, link.authored)
				continue
			}
			if link.authored == "" {
				// An unarmed slot: must be the sentinel itself, since the
				// family carries record 0 (checked above) [06 R-DMG-01 §5].
				if link.def != w0 {
					t.Errorf("unit %s %s (unarmed): def = %v, want the record-0 [noweapon] sentinel", key, link.family, link.def)
				}
				continue
			}
			if link.def.ID == 0 {
				gotMisses = append(gotMisses, key+"/"+link.family+"="+link.authored)
			}
		}
	}
	sort.Strings(gotMisses)
	if len(gotMisses) != len(wantMisses) {
		t.Fatalf("sentinel links = %v, want exactly %v", gotMisses, wantMisses)
	}
	for i := range wantMisses {
		if gotMisses[i] != wantMisses[i] {
			t.Fatalf("sentinel links[%d] = %q, want %q (full set: %v)", i, gotMisses[i], wantMisses[i], gotMisses)
		}
	}
	// The brief's named stock example, asserted directly: armseap explodes as
	// MEDIUM_UNITEX, a name in no parsed weapon file, and the link carries
	// the record-0 sentinel — inactive by its zero slot number.
	seap, ok := cat.Unit("armseap")
	if !ok {
		t.Fatal("armseap missing from the stock catalog")
	}
	if seap.ExplodeAs != "MEDIUM_UNITEX" {
		t.Fatalf("armseap explodeas = %q, want MEDIUM_UNITEX", seap.ExplodeAs)
	}
	if seap.ExplodeAsDef != w0 {
		t.Fatalf("armseap explodeas def = %v, want the record-0 [noweapon] sentinel", seap.ExplodeAsDef)
	}
}
