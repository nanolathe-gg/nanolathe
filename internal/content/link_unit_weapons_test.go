package content

import "testing"

// The unit weapon-link contract [02 §5 R-CONTENT-02]: an authored link
// (weapon1..3, explodeas, selfdestructas) resolves by the runtime name scan —
// case-insensitive, first match — and a MISS fills the slot with a reference
// to record 0, the inactive sentinel (stock [noweapon], ID 0), not an error
// and not a silent nil. Only a weapon family without any record 0 leaves the
// nil inactive marker. The sentinel is inactive by its zero slot number:
// consumers test WeaponDef.ID == 0. The superseded same-ID name case — the
// other way a name can miss — is locked by the CNT-01 weapon-family tests in
// compile_weapon_test.go and not duplicated here.

// newLinkFixtureUnits builds one unit def with all five link names authored
// the way an FBI would, keyed like the catalog.
func newLinkFixtureUnits(weapon1, weapon2, weapon3, explodeAs, selfDestructAs string) map[string]*UnitDef {
	return map[string]*UnitDef{
		"linkme": {
			DefinitionHeader: DefinitionHeader{CanonicalKey: "linkme"},
			UnitName:         "LINKME",
			Weapon1:          weapon1,
			Weapon2:          weapon2,
			Weapon3:          weapon3,
			ExplodeAs:        explodeAs,
			SelfDestructAs:   selfDestructAs,
		},
	}
}

// TestLinkUnitWeaponsMissResolvesToRecord0Sentinel locks the miss policy on a
// family that carries record 0 [02 §5 R-CONTENT-02]: every missed link across
// all five families resolves to the [noweapon] record — non-nil, ID 0 — while
// a hit returns the named record case-insensitively and an empty name stays
// unresolved.
func TestLinkUnitWeaponsMissResolvesToRecord0Sentinel(t *testing.T) {
	noweapon := &WeaponDef{ID: 0}
	noweapon.CanonicalKey = "noweapon"
	bertha := &WeaponDef{ID: 7}
	bertha.CanonicalKey = "bigbertha"
	weapons := map[string]*WeaponDef{"noweapon": noweapon, "bigbertha": bertha}

	units := newLinkFixtureUnits("BigBertha", "Medium_Unitex", "Missing_Three", "MEDIUM_UNITEX", "BIG_BUILDING")
	LinkUnitWeapons(units, weapons)
	u := units["linkme"]

	// Hit: case-insensitive first match, the named record itself.
	if u.Weapon1Def != bertha {
		t.Fatalf("weapon1 BigBertha = %v, want the bigbertha record", u.Weapon1Def)
	}
	// Misses in every family: the record-0 sentinel, inactive by its zero
	// slot number — not nil, not an error.
	for _, link := range []struct {
		family string
		def    *WeaponDef
	}{{"weapon2", u.Weapon2Def}, {"weapon3", u.Weapon3Def}, {"explodeas", u.ExplodeAsDef}, {"selfdestructas", u.SelfDestructAsDef}} {
		if link.def != noweapon {
			t.Fatalf("%s miss = %v, want the record-0 [noweapon] sentinel", link.family, link.def)
		}
		if link.def.ID != 0 {
			t.Fatalf("%s sentinel ID = %d, want 0 (consumers recognize the inactive sentinel by the zero slot)", link.family, link.def.ID)
		}
	}
}

// TestLinkUnitWeaponsMissWithoutRecord0NilInactive locks the no-record-0
// corner [02 §5 R-CONTENT-02]: when the family carries no record 0 at all, a
// missed link resolves to the explicit nil inactive marker — still not an
// error — and a hit still resolves normally.
func TestLinkUnitWeaponsMissWithoutRecord0NilInactive(t *testing.T) {
	bertha := &WeaponDef{ID: 7}
	bertha.CanonicalKey = "bigbertha"
	weapons := map[string]*WeaponDef{"bigbertha": bertha}

	units := newLinkFixtureUnits("BigBertha", "", "MEDIUM_UNITEX", "MEDIUM_UNITEX", "")
	LinkUnitWeapons(units, weapons)
	u := units["linkme"]

	if u.Weapon1Def != bertha {
		t.Fatalf("weapon1 BigBertha = %v, want the bigbertha record", u.Weapon1Def)
	}
	for _, link := range []struct {
		family string
		def    *WeaponDef
	}{{"weapon3", u.Weapon3Def}, {"explodeas", u.ExplodeAsDef}} {
		if link.def != nil {
			t.Fatalf("%s miss without a record 0 = %v, want the explicit nil inactive marker", link.family, link.def)
		}
	}
	if u.Weapon2Def != nil || u.SelfDestructAsDef != nil {
		t.Fatalf("empty names resolved: weapon2=%v selfdestructas=%v, want both nil", u.Weapon2Def, u.SelfDestructAsDef)
	}
}

// TestLinkUnitWeaponsNamingRecord0DirectlyIsInactive locks that a link naming
// record 0 by name resolves to the record — active neither way: inactivity is
// a property of the zero slot number, not of how the link was reached [02 §5
// R-CONTENT-02].
func TestLinkUnitWeaponsNamingRecord0DirectlyIsInactive(t *testing.T) {
	noweapon := &WeaponDef{ID: 0}
	noweapon.CanonicalKey = "noweapon"
	weapons := map[string]*WeaponDef{"noweapon": noweapon}

	units := newLinkFixtureUnits("NoWeapon", "", "", "", "")
	LinkUnitWeapons(units, weapons)

	if units["linkme"].Weapon1Def != noweapon {
		t.Fatalf("weapon1 NoWeapon = %v, want the record-0 [noweapon] def", units["linkme"].Weapon1Def)
	}
	if units["linkme"].Weapon1Def.ID != 0 {
		t.Fatalf("record-0 link ID = %d, want 0", units["linkme"].Weapon1Def.ID)
	}
}

// TestClonePreservesRecord0SentinelLinks locks that Catalog.Clone keeps the
// sentinel links the compile produced [02 §5 R-CONTENT-02]: a missed link in
// the original rewires to the clone's own record-0 inactive sentinel (ID 0,
// inactive) — not nil — while an active link rewires to the clone's copy of
// its record and no link is shared with the original catalog.
func TestClonePreservesRecord0SentinelLinks(t *testing.T) {
	noweapon := &WeaponDef{ID: 0}
	noweapon.CanonicalKey = "noweapon"
	bertha := &WeaponDef{ID: 7}
	bertha.CanonicalKey = "bigbertha"
	weapons := map[string]*WeaponDef{"noweapon": noweapon, "bigbertha": bertha}

	units := newLinkFixtureUnits("BigBertha", "Medium_Unitex", "", "Medium_Unitex", "")
	LinkUnitWeapons(units, weapons)
	if units["linkme"].Weapon2Def != noweapon || units["linkme"].ExplodeAsDef != noweapon {
		t.Fatalf("fixture precondition: sentinel links not produced (%v, %v)", units["linkme"].Weapon2Def, units["linkme"].ExplodeAsDef)
	}

	c := &Catalog{Weapons: weapons, Units: units}
	c.RebuildWeaponIndex()
	clone := c.Clone()

	cu, ok := clone.Unit("linkme")
	if !ok {
		t.Fatalf("clone lost unit linkme")
	}
	cloneSentinel := clone.Weapons["noweapon"]
	if cloneSentinel == nil || cloneSentinel.ID != 0 {
		t.Fatalf("clone weapon table lost record 0: %v", cloneSentinel)
	}
	for _, link := range []struct {
		family string
		def    *WeaponDef
	}{{"weapon2", cu.Weapon2Def}, {"explodeas", cu.ExplodeAsDef}} {
		if link.def == nil {
			t.Fatalf("clone dropped %s sentinel link to nil, want the clone's record-0 sentinel [02 §5 R-CONTENT-02]", link.family)
		}
		if link.def.ID != 0 {
			t.Fatalf("clone %s link ID = %d, want 0", link.family, link.def.ID)
		}
		if IsWeaponInactive(link.def) != true {
			t.Fatalf("clone %s link must be inactive by the canonical predicate", link.family)
		}
		if link.def != cloneSentinel {
			t.Fatalf("clone %s link must hold the clone's own record-0 pointer", link.family)
		}
		if link.def == units["linkme"].Weapon2Def {
			t.Fatalf("clone %s link shares the original's sentinel pointer; clone must not share mutable state", link.family)
		}
	}
	// Active links still rewire to the clone's copy, not the original's record.
	if cu.Weapon1Def != clone.Weapons["bigbertha"] {
		t.Fatalf("clone weapon1 = %v, want the clone's bigbertha record", cu.Weapon1Def)
	}
	if cu.Weapon1Def == bertha {
		t.Fatalf("clone weapon1 shares the original's bigbertha pointer")
	}
	if IsWeaponInactive(cu.Weapon1Def) {
		t.Fatalf("active link must stay active through clone")
	}
}
