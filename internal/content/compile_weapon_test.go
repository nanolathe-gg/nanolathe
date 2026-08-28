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

// TestWeaponSameIDWholeRecordReplacement locks the same-ID merge contract
// [02 §5 R-CONTENT-02]: a later section with an already-seen ID REPLACES the
// record — every parser-owned field stored authored-or-default, catalog name
// included. There is no sparse merge: keys the later section omits revert to
// their defaults, and values the earlier section authored do not survive.
//
// The stock corpus never exercises this (the parsed family's IDs are all
// unique [02 §5 R-CONTENT-02]); the fixture locks the merge rule for mission
// and mod data. Discovery order here is the sorted logical paths of the
// weapons/ directory (the documented lexical divergence [SPEC_CONFLICTS SC3]),
// so z_second.tdf parses after a_first.tdf.
func TestWeaponSameIDWholeRecordReplacement(t *testing.T) {
	first := `[EARTHQUAKE]
{
	ID=36;
	range=720;
	name=Earthquake;
	weaponvelocity=1.5;
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
		fixtureFile{path: "weapons/a_first.tdf", data: first},
		fixtureFile{path: "weapons/z_second.tdf", data: second},
	)
	weapons, dups, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		t.Fatal(err)
	}
	// The replaced record's catalog name is the LATER section's; the earlier
	// name matches no record [02 §5 R-CONTENT-02].
	if _, exists := weapons["earthquake"]; exists {
		t.Fatalf("replaced name earthquake survived in catalog: %v", keysOf(weapons))
	}
	merged, ok := weapons["cormine2"]
	if !ok {
		t.Fatalf("surviving name cormine2 missing: %v", keysOf(weapons))
	}
	if merged.ID != 36 || merged.Name != "Mine" {
		t.Fatalf("surviving def id=%d name=%q, want 36/Mine", merged.ID, merged.Name)
	}
	// Keys the later section omitted revert to defaults — authored-or-default
	// stores replace the whole record, including the earlier DAMAGE table.
	if merged.Range != 32767 {
		t.Fatalf("omitted range = %d, want default 32767", merged.Range)
	}
	if merged.DamageDefault != 0 || merged.Damage != nil {
		t.Fatalf("omitted DAMAGE survived the replacement as %d/%v, want 0/nil", merged.DamageDefault, merged.Damage)
	}
	if merged.WeaponVelocity != trunc(2.5*65536.0/30.0) {
		t.Fatalf("weaponvelocity = %d, want the later section's %d", merged.WeaponVelocity, trunc(2.5*65536.0/30.0))
	}
	// Exactly one record occupies the slot.
	byID, ok := WeaponByID(weapons, 36)
	if !ok || byID != merged {
		t.Fatalf("WeaponByID(36) = (%v, %v), want the surviving record", byID, ok)
	}
	// Diagnostics record every name that shared the slot, in discovery order,
	// the surviving name last.
	if len(dups) != 1 {
		t.Fatalf("dups=%v want 1 for ID 36", dups)
	}
	if dups[0].ID != 36 || len(dups[0].Keys) != 2 || dups[0].Keys[0] != "earthquake" || dups[0].Keys[1] != "cormine2" || dups[0].Winner != "cormine2" {
		t.Fatalf("dup=%v want ID 36 Keys [earthquake cormine2] Winner cormine2", dups[0])
	}
}

// TestWeaponFamilyIsWeaponsDirOnly locks the discovery family [02 §5
// R-CONTENT-02]: the weapon family is exactly Weapons/*.tdf and retail never
// parses gamedata/weapons.tdf — that file is inert. A gamedata section that
// would collide with a weapons/ section (the historical ID 36 case) must
// contribute nothing: no extra record, no collision diagnostic.
func TestWeaponFamilyIsWeaponsDirOnly(t *testing.T) {
	inert := `[earthquake]
{
	ID=36;
	range=720;
	name=Earthquake;
}
`
	family := `[cormine2]
{
	ID=36;
	name=Mine;
}
`
	fs := newFixtureFS(t,
		fixtureFile{path: "gamedata/weapons.tdf", data: inert},
		fixtureFile{path: "weapons/cormine2_weapon.tdf", data: family},
	)
	weapons, dups, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		t.Fatal(err)
	}
	// gamedata/weapons.tdf contributed nothing: no earthquake record under any
	// spelling, and ID 36 is the weapons/ section alone.
	if _, exists := weapons["earthquake"]; exists {
		t.Fatalf("inert gamedata/weapons.tdf contributed a record: %v", keysOf(weapons))
	}
	w36, ok := weapons["cormine2"]
	if !ok || w36.ID != 36 {
		t.Fatalf("ID 36 record = %v, want cormine2 from weapons/", w36)
	}
	if w36.Provenance.LogicalPath != "weapons/cormine2_weapon.tdf" {
		t.Fatalf("cormine2 provenance = %q, want weapons/cormine2_weapon.tdf", w36.Provenance.LogicalPath)
	}
	// With gamedata unread there is no same-ID collision to diagnose.
	if len(dups) != 0 {
		t.Fatalf("collision diagnostics %v, want none — gamedata/weapons.tdf is never read", dups)
	}
}

// TestWeaponDuplicateDiscoveryOrderFieldOverwrite locks that same-ID sections
// resolve by discovery order — the later section owns the record — and that
// the diagnostics record the shared names in discovery order [02 §5
// R-CONTENT-02]. Discovery order across two files is the sorted logical paths
// [SPEC_CONFLICTS SC3]: weapons/alpha.tdf parses before weapons/beta.tdf.
func TestWeaponDuplicateDiscoveryOrderFieldOverwrite(t *testing.T) {
	first := `[ALPHA]
{
	ID=42;
	range=100;
	weaponvelocity=1.0;
	name=First;
}
`
	second := `[BETA]
{
	ID=42;
	range=200;
	weaponvelocity=2.0;
	name=Second;
}
`
	fs := newFixtureFS(t,
		fixtureFile{path: "weapons/alpha.tdf", data: first},
		fixtureFile{path: "weapons/beta.tdf", data: second},
	)
	weapons, dups, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		t.Fatal(err)
	}
	// Survivor is the later section, whole record.
	if _, exists := weapons["alpha"]; exists {
		t.Fatalf("replaced name alpha survived: %v", keysOf(weapons))
	}
	beta, ok := weapons["beta"]
	if !ok {
		t.Fatalf("winner beta missing: %v", keysOf(weapons))
	}
	if beta.ID != 42 {
		t.Fatalf("beta ID=%d want 42", beta.ID)
	}
	if beta.Name != "Second" {
		t.Fatalf("beta Name=%q want Second (later section's display name)", beta.Name)
	}
	if beta.Range != 200 {
		t.Fatalf("range=%d want 200 (later section's value)", beta.Range)
	}
	if want := trunc(2.0 * 65536.0 / 30.0); beta.WeaponVelocity != want {
		t.Fatalf("weaponvelocity=%d want %d (later section's value)", beta.WeaponVelocity, want)
	}
	if byID, ok := WeaponByID(weapons, 42); !ok || byID != beta {
		t.Fatalf("WeaponByID(42) want beta, got %v %v", byID, ok)
	}
	if len(dups) != 1 {
		t.Fatalf("dups=%v want 1 entry for ID 42", dups)
	}
	d := dups[0]
	if d.ID != 42 || len(d.Keys) != 2 || d.Keys[0] != "alpha" || d.Keys[1] != "beta" || d.Winner != "beta" {
		t.Fatalf("dup=%v want ID 42 Keys [alpha beta] Winner beta (last)", d)
	}
	// The catalog index projects the same survivor.
	cat := &Catalog{Weapons: weapons}
	cat.RebuildWeaponIndex()
	if w, ok := cat.WeaponByID(42); !ok || w != beta {
		t.Fatalf("Catalog.WeaponByID(42) want beta, got %v %v", w, ok)
	}
}

// TestWeaponIDLessSectionsInert locks the settled ID-less contract [02 §5
// R-CONTENT-02]: a section without an authored ID (default -1) is inert. All
// ID-less sections write into the one unreachable scratch slot just before
// record 0 — the last one wins it — and the name-keyed catalog never reaches
// that slot, so ID-less sections appear only in diagnostics and can never be
// resolved by name, even when an ID'd section shares their name.
func TestWeaponIDLessSectionsInert(t *testing.T) {
	idlessA := "[WA]\n{\nrange=10;\n}\n"
	idlessB := "[WB]\n{\nrange=20;\n}\n"
	shadowed := "[SHARED]\n{\nrange=30;\n}\n"
	real := "[SHARED]\n{\nID=7;\nrange=70;\nname=Real;\n}\n"
	fs := newFixtureFS(t,
		fixtureFile{path: "weapons/a.tdf", data: idlessA},
		fixtureFile{path: "weapons/b.tdf", data: idlessB},
		fixtureFile{path: "weapons/m_shared.tdf", data: shadowed},
		fixtureFile{path: "weapons/z_real.tdf", data: real},
	)
	weapons, dups, err := CompileWeaponsWithDuplicates(fs)
	if err != nil {
		t.Fatal(err)
	}
	// Only the ID'd section enters the catalog; the ID-less names — including
	// the shared one — resolve to nothing.
	if len(weapons) != 1 {
		t.Fatalf("catalog = %v, want only the ID'd section", keysOf(weapons))
	}
	w, ok := weapons["shared"]
	if !ok || w.ID != 7 || w.Range != 70 {
		t.Fatalf("shared = (%v, %v), want the ID-7 record", w, ok)
	}
	if _, exists := weapons["wa"]; exists {
		t.Fatal("ID-less section WA entered the catalog")
	}
	if _, exists := weapons["wb"]; exists {
		t.Fatal("ID-less section WB entered the catalog")
	}
	// Diagnostics: the ID-less sections shared the scratch slot (ID -1); the
	// last one parsed — shared, from the sorted-last of the three ID-less
	// files — wins it.
	var scratch *WeaponDuplicate
	for i := range dups {
		if dups[i].ID == -1 {
			scratch = &dups[i]
		}
	}
	if scratch == nil {
		t.Fatalf("no scratch-slot diagnostics among %v", dups)
	}
	if len(scratch.Keys) != 3 || scratch.Keys[0] != "wa" || scratch.Keys[1] != "wb" || scratch.Keys[2] != "shared" || scratch.Winner != "shared" {
		t.Fatalf("scratch-slot diagnostic = %v, want [wa wb shared] winner shared (last)", scratch)
	}
	// The ID'd section has no collision diagnostic.
	for i := range dups {
		if dups[i].ID == 7 {
			t.Fatalf("unexpected collision diagnostic for ID 7: %v", dups[i])
		}
	}
}

// TestWeaponNameScanAndLinkSentinel locks the runtime name resolution and the
// FBI link miss policy [02 §5 R-CONTENT-02]: a name resolves by a linear scan
// of the record table from slot 0 upward, case-insensitively, first match
// wins; a link (weapon1..3, explodeas, selfdestructas) that names no record
// fills its slot with a reference to record 0 — the inactive sentinel, stock
// [noweapon] — not an error. Record 0 is inactive even when named.
func TestWeaponNameScanAndLinkSentinel(t *testing.T) {
	noweapon := &WeaponDef{ID: 0}
	noweapon.CanonicalKey = "noweapon"
	alpha := &WeaponDef{ID: 5, Name: "Alpha"}
	alpha.CanonicalKey = "alpha"
	beta := &WeaponDef{ID: 9, Name: "Beta"}
	beta.CanonicalKey = "beta"
	cat := &Catalog{Weapons: map[string]*WeaponDef{
		"noweapon": noweapon, "alpha": alpha, "beta": beta,
	}}
	cat.RebuildWeaponIndex()

	// Slot order: records walk from slot 0 upward.
	records := cat.WeaponRecordsByID()
	if len(records) != 3 || records[0] != noweapon || records[1] != alpha || records[2] != beta {
		t.Fatalf("records = %v, want [noweapon alpha beta] in slot order", records)
	}
	// Case-insensitive first match.
	if w, ok := cat.WeaponByName("BETA"); !ok || w != beta {
		t.Fatalf("WeaponByName(BETA) = (%v, %v), want beta", w, ok)
	}
	if _, ok := cat.WeaponByName("gamma"); ok {
		t.Fatal("gamma resolved but no record carries that name")
	}
	// A resolved link is active unless it points at record 0.
	if w, active := cat.WeaponLink("alpha"); !active || w != alpha {
		t.Fatalf("WeaponLink(alpha) = (%v, %v), want active alpha", w, active)
	}
	// Miss: the link references record 0, inactive.
	if w, active := cat.WeaponLink("medium_unitex"); active || w != noweapon {
		t.Fatalf("WeaponLink(medium_unitex) = (%v, %v), want the record-0 sentinel, inactive", w, active)
	}
	// Naming record 0 directly is also inactive.
	if w, active := cat.WeaponLink("noweapon"); active || w != noweapon {
		t.Fatalf("WeaponLink(noweapon) = (%v, %v), want record 0, inactive", w, active)
	}

	// A catalog without a record 0 expresses the miss as the explicit inactive
	// marker: nil def, active false.
	bare := &Catalog{Weapons: map[string]*WeaponDef{"alpha": alpha}}
	bare.RebuildWeaponIndex()
	if w, active := bare.WeaponLink("medium_unitex"); active || w != nil {
		t.Fatalf("WeaponLink miss without record 0 = (%v, %v), want nil, inactive", w, active)
	}
}
