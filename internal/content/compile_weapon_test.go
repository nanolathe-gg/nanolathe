package content

import (
	"testing"
)

// trunc mirrors the runtime float64→int32 conversion (__ftol, I3); a plain
// int32(...) of a constant expression is a compile-time error in Go when the
// constant has a fraction.
func trunc(f float64) int32 { return int32(f) }

// TestWeaponConversions locks the conversion table of [02 "Weapon record"]:
// each value is a single float expression truncated once after the multiply,
// never composed differently. Authored durations below 1/30 second become 0.
func TestWeaponConversions(t *testing.T) {
	body := `[BIGBERTHA]
{
	ID=36;
	weaponvelocity=3.5;
	startvelocity=1.75;
	weaponacceleration=0.15;
	reloadtime=0.02;
	burstrate=0.04;
	duration=0.5;
	turnrate=90;
	range=100;
	minbarrelangle=-11.25;
	[DAMAGE]
	{
		default=55;
		COMMANDERS=110;
		kbotss2=27;
	}
}
`
	doc := mustParseTDF(t, body)
	sec := doc.Root.Sections()[0]
	wd := compileWeaponSection(sec, "BIGBERTHA", Provenance{})

	if want := trunc(3.5 * 65536.0 / 30.0); wd.WeaponVelocity != want { // trunc(7645.86) = 7645
		t.Fatalf("weaponvelocity = %d, want %d", wd.WeaponVelocity, want)
	}
	if want := trunc(1.75 * 65536.0 / 30.0); wd.StartVelocity != want {
		t.Fatalf("startvelocity = %d, want %d", wd.StartVelocity, want)
	}
	if want := trunc(0.15 * 65536.0 / 900.0); wd.WeaponAcceleration != want {
		t.Fatalf("weaponacceleration = %d, want %d", wd.WeaponAcceleration, want)
	}
	if wd.ReloadTime != 0 { // 0.02*30 = 0.6 -> 0 [02 "Weapon record"]
		t.Fatalf("reloadtime = %d, want 0 (authored below 1/30 s)", wd.ReloadTime)
	}
	if wd.BurstRate != 1 { // 0.04*30 = 1.2 -> 1
		t.Fatalf("burstrate = %d, want 1", wd.BurstRate)
	}
	if wd.Duration != 15 { // 0.5*30 = 15 exactly
		t.Fatalf("duration = %d, want 15", wd.Duration)
	}
	if want := int32(90 / 30.0); wd.TurnRate != want { // turnrate *1/30
		t.Fatalf("turnrate = %d, want %d", wd.TurnRate, want)
	}
	if wd.Range != 100 {
		t.Fatalf("range = %d, want 100", wd.Range)
	}
	// minbarrelangle default -11.25 degrees composed with the pi/180 constant.
	wantAngle := -11.25 * (piOver180())
	if wd.MinBarrelAngle != wantAngle {
		t.Fatalf("minbarrelangle = %v, want %v", wd.MinBarrelAngle, wantAngle)
	}
	// DAMAGE: default fallback plus every other key interned by name [02 "Weapon record"] C4.
	if wd.DamageDefault != 55 {
		t.Fatalf("DamageDefault = %d, want 55", wd.DamageDefault)
	}
	got := wd.DamageKeysSorted()
	if len(got) != 2 || got[0] != "COMMANDERS" || got[1] != "kbotss2" {
		t.Fatalf("damage keys = %v, want [COMMANDERS kbotss2]", got)
	}
	// A weapon with no DAMAGE section has fallback damage zero and no map.
	doc2 := mustParseTDF(t, "[PLAIN]\n{\nID=1;\n}\n")
	wd2 := compileWeaponSection(doc2.Root.Sections()[0], "PLAIN", Provenance{})
	if wd2.DamageDefault != 0 || wd2.Damage != nil {
		t.Fatalf("no-DAMAGE weapon: default=%d map=%v, want 0/nil", wd2.DamageDefault, wd2.Damage)
	}
}

// TestWeaponIDDentityMergesSameIDSections locks record identity by ID
// [02 "Weapon record"]: two sections sharing an ID produce ONE record whose
// catalog name is the later section's; the loser's name leaves the catalog;
// absent keys revert to their defaults because typed reads store
// value-or-default per field.
//
// Measured stock case behind this contract: ID 36 is shared by EARTHQUAKE
// (gamedata/weapons.tdf) and cormine2 (weapons/cormine2_weapon.tdf); units
// reference CORMINE2 only, so the weapons/ file must parse after gamedata's
// for the referenced spelling to survive.
func TestWeaponIDEntityMergesSameIDSections(t *testing.T) {
	first := `[EARTHQUAKE]
{
	ID=36;
	range=720;
	name=Earthquake;
	[DAMAGE]
	{
		default=500;
	}
}
`
	second := `[cormine2]
{
	ID=36;
	weaponvelocity=2.5;
	name=Mine;
}
`
	fs := newFixtureFS(t,
		fixtureFile{path: "gamedata/weapons.tdf", data: first},
		fixtureFile{path: "weapons/cormine2_weapon.tdf", data: second},
	)
	weapons, err := CompileWeapons(fs)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := weapons["earthquake"]; exists {
		t.Fatalf("loser name earthquake survived in catalog: %v", keysOf(weapons))
	}
	merged, ok := weapons["cormine2"]
	if !ok {
		t.Fatalf("winner name cormine2 missing: %v", keysOf(weapons))
	}
	if merged.ID != 36 || merged.Name != "Mine" {
		t.Fatalf("merged def id=%d name=%q, want 36/Mine", merged.ID, merged.Name)
	}
	// Keys the later section omitted revert to defaults (unconditional stores).
	if merged.Range != 32767 {
		t.Fatalf("omitted range reverted to %d, want default 32767", merged.Range)
	}
	if merged.DamageDefault != 0 || merged.Damage != nil {
		t.Fatalf("omitted DAMAGE reverted to %d/%v, want 0/nil", merged.DamageDefault, merged.Damage)
	}
	// Keys it authored survive conversion.
	if merged.WeaponVelocity != trunc(2.5*65536.0/30.0) {
		t.Fatalf("weaponvelocity = %d", merged.WeaponVelocity)
	}
	// Exactly one record per nonnegative ID.
	byID, ok := WeaponByID(weapons, 36)
	if !ok || byID != merged {
		t.Fatalf("WeaponByID(36) = (%v, %v), want the merged record", byID, ok)
	}
	for k, w := range weapons {
		if w.ID == 36 && k != "cormine2" {
			t.Fatalf("duplicate ID 36 record under %q", k)
		}
	}
}

// TestWeaponIDLessSectionsStayDistinct documents the pending question on
// ID-less sections rather than a behavior: all 231 stock sections carry IDs,
// and merging slot -1 would collapse them destructively if the ambiguous
// sentence of [02 "Weapon record"] was misread.
func TestWeaponIDLessSectionsStayDistinct(t *testing.T) {
	a := "[WA]\n{\nrange=10;\n}\n"
	b := "[WB]\n{\nrange=20;\n}\n"
	fs := newFixtureFS(t,
		fixtureFile{path: "weapons/a.tdf", data: a},
		fixtureFile{path: "weapons/b.tdf", data: b},
	)
	weapons, err := CompileWeapons(fs)
	if err != nil {
		t.Fatal(err)
	}
	if len(weapons) != 2 {
		t.Fatalf("got %d records, want 2 distinct ID-less sections: %v", len(weapons), keysOf(weapons))
	}
}
