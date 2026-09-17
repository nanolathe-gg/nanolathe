package content

import (
	"fmt"
	"testing"
)

// inRangeTrunc supplies independently bounded expected values for ordinary
// fixtures. Exceptional definition-parser conversion is covered by explicit
// literal expectations below, never by this test helper.
func inRangeTrunc(f float64) int32 {
	if f < -2147483648.0 || f >= 2147483648.0 {
		panic("test expected value is not a signed-32 integer")
	}
	return int32(f)
}

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

	if want := inRangeTrunc(3.5 * (65536.0 / 30.0)); wd.WeaponVelocity != want { // trunc(7645.86) = 7645
		t.Fatalf("weaponvelocity = %d, want %d", wd.WeaponVelocity, want)
	}
	if want := inRangeTrunc(1.75 * (65536.0 / 30.0)); wd.StartVelocity != want {
		t.Fatalf("startvelocity = %d, want %d", wd.StartVelocity, want)
	}
	if want := inRangeTrunc(0.15 * (65536.0 / 900.0)); wd.WeaponAcceleration != want {
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

func TestWeaponDefinitionNarrowingRetainsLowWords(t *testing.T) {
	doc := mustParseTDF(t, `[edge]
{
ID=1;
weaponvelocity=1966080;
duration=143165577;
reloadtime=143165577;
}
`)
	weapon := compileWeaponSection(doc.Root.Sections()[0], "edge", Provenance{})
	if weapon.WeaponVelocity != 0 {
		t.Fatalf("weaponvelocity = %d, want 0 after signed-64 low-word store", weapon.WeaponVelocity)
	}
	if weapon.Duration != 14 {
		t.Fatalf("duration = %d, want 14 after low32 then uint16 store", weapon.Duration)
	}
	if weapon.ReloadTime != 14 {
		t.Fatalf("reloadtime = %d, want 14 after low32 then int16 store", weapon.ReloadTime)
	}
}

// TestWeaponSameIDWholeRecordReplacement locks the same-ID merge contract
// [02 §5 R-CONTENT-02]: a later section with an already-seen ID REPLACES the
// record — every parser-owned field stored authored-or-default, catalog name
// included. Omitted scalars revert to their defaults; the damage override
// table is the separate append exception [06 R-DMG-01 §1].
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
	// stores replace the scalar fields, including the earlier DAMAGE default.
	if merged.Range != 32767 {
		t.Fatalf("omitted range = %d, want default 32767", merged.Range)
	}
	if merged.DamageDefault != 0 || merged.Damage != nil {
		t.Fatalf("omitted DAMAGE survived the replacement as %d/%v, want 0/nil", merged.DamageDefault, merged.Damage)
	}
	if merged.WeaponVelocity != inRangeTrunc(2.5*(65536.0/30.0)) {
		t.Fatalf("weaponvelocity = %d, want the later section's %d", merged.WeaponVelocity, inRangeTrunc(2.5*(65536.0/30.0)))
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
	if want := inRangeTrunc(2.0 * (65536.0 / 30.0)); beta.WeaponVelocity != want {
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

// TestWeaponFirestarterTruncatesToByte locks [06 R-WPN-05 §10]: the loader
// stores only the low 8 bits of the authored `firestarter` integer
// ([02 "Weapon record"] lists the field as 8-bit), so an authored value of
// 256 compiles to 0 and 257 compiles to 1 — every reader of
// WeaponDef.Firestarter already sees the byte retail would test, with no
// truncation left for a caller to apply.
func TestWeaponFirestarterTruncatesToByte(t *testing.T) {
	cases := []struct {
		authored int
		want     int32
	}{
		{authored: 0, want: 0},
		{authored: 70, want: 70},
		{authored: 255, want: 255},
		{authored: 256, want: 0},
		{authored: 257, want: 1},
	}
	for _, tc := range cases {
		body := `[FIRETEST]
{
	ID=1;
	firestarter=` + fmt.Sprintf("%d", tc.authored) + `;
}
`
		doc := mustParseTDF(t, body)
		sec := doc.Root.Sections()[0]
		wd := compileWeaponSection(sec, "FIRETEST", Provenance{})
		if wd.Firestarter != tc.want {
			t.Fatalf("firestarter=%d compiled to %d, want %d", tc.authored, wd.Firestarter, tc.want)
		}
	}
}

// TestWeaponDurationKeysWrapTo16Bits locks WU-19-163: retail stores
// weapontimer, turnrate, reloadtime, randomdecay, flighttime and holdtime as
// 16-bit words, so the *30 (or *1/30) truncated tick count is wrapped a
// second time to the field's width before it reaches the record
// [06 §4.2, §4.3, §6.6, §6.7, §7.3][07 "in-flight camera move"]. An authored
// negative therefore reaches the compiled record as a small wrapped value —
// at most 65,535 for the unsigned stores, within -32768..32767 for the
// signed ones — never as an arbitrary-magnitude 32-bit negative.
//
// burstrate, duration and smokedelay are unsigned 16-bit stores too: every
// reader zero-extends the word and compares unsigned [06 R-WPN-05 §12]
// [02 R-KEYS-01 §6], so they wrap exactly as weapontimer does; this test locks
// the zero-extension against a sign-extending regression (a value above
// 32767/30 seconds is the case that tells the two apart).
func TestWeaponDurationKeysWrapTo16Bits(t *testing.T) {
	body := `[DURTEST]
{
	ID=1;
	weapontimer=-1;
	randomdecay=-1;
	flighttime=-1;
	burstrate=-1;
	duration=-1;
	smokedelay=-1;
	turnrate=-40;
	reloadtime=-1200;
	holdtime=-1200;
}
`
	doc := mustParseTDF(t, body)
	sec := doc.Root.Sections()[0]
	wd := compileWeaponSection(sec, "DURTEST", Provenance{})

	// Unsigned 16-bit stores: trunc(-1*30) = -30, zero-extended from a 16-bit
	// store wraps to 65,536-30 = 65,506.
	if wd.WeaponTimer != 65506 {
		t.Fatalf("weapontimer(-1) = %d, want 65506 [06 §7.3]", wd.WeaponTimer)
	}
	if wd.RandomDecay != 65506 {
		t.Fatalf("randomdecay(-1) = %d, want 65506 [06 §4.3]", wd.RandomDecay)
	}
	if wd.FlightTime != 65506 {
		t.Fatalf("flighttime(-1) = %d, want 65506 [06 §6.6]", wd.FlightTime)
	}
	// turnrate: trunc(-40 * 1/30) = -1, zero-extended wraps to 65,535.
	if wd.TurnRate != 65535 {
		t.Fatalf("turnrate(-40) = %d, want 65535 [06 §6.7]", wd.TurnRate)
	}

	// Signed 16-bit stores: trunc(-1200*30) = -36000, which is outside the
	// int16 range and wraps (sign-extended back) to 29,536 rather than
	// staying at -36000.
	if wd.ReloadTime != 29536 {
		t.Fatalf("reloadtime(-1200) = %d, want 29536 [06 §4.2]", wd.ReloadTime)
	}
	if wd.HoldTime != 29536 {
		t.Fatalf("holdtime(-1200) = %d, want 29536 [07 \"in-flight camera move\"]", wd.HoldTime)
	}

	// Unsigned 16-bit stores read zero-extended [06 R-WPN-05 §12]: -1 wraps to
	// 65,506 like weapontimer, never to -30.
	if wd.BurstRate != 65506 {
		t.Fatalf("burstrate(-1) = %d, want 65506 [06 R-WPN-05 §12]", wd.BurstRate)
	}
	if wd.Duration != 65506 {
		t.Fatalf("duration(-1) = %d, want 65506 [06 R-WPN-05 §12]", wd.Duration)
	}
	if wd.SmokeDelay != 65506 {
		t.Fatalf("smokedelay(-1) = %d, want 65506 [06 R-WPN-05 §12]", wd.SmokeDelay)
	}

	// The case that separates zero- from sign-extension: 1100 s is 33,000
	// ticks, above 32,767 and below 65,536. A sign-extending reader would see
	// -32,536; the zero-extending readers see 33,000 [06 R-WPN-05 §12]. The
	// same authored value wraps the signed reloadtime store to -32,536.
	above := mustParseTDF(t, `[ABOVE]
{
	ID=2;
	burstrate=1100;
	duration=1100;
	smokedelay=1100;
	reloadtime=1100;
}
`)
	wa := compileWeaponSection(above.Root.Sections()[0], "ABOVE", Provenance{})
	for _, c := range []struct {
		name string
		got  int32
	}{{"burstrate", wa.BurstRate}, {"duration", wa.Duration}, {"smokedelay", wa.SmokeDelay}} {
		if c.got != 33000 {
			t.Fatalf("%s(1100) = %d, want 33000 (zero-extended) [06 R-WPN-05 §12]", c.name, c.got)
		}
	}
	if wa.ReloadTime != -32536 {
		t.Fatalf("reloadtime(1100) = %d, want -32536 (sign-extended) [06 §4.2]", wa.ReloadTime)
	}
}

// A later same-ID record replaces scalars while retaining the override table
// [02 R-CONTENT-02][06 R-DMG-01 §1].
func TestWeaponSameIDRetainsDamageOverrides(t *testing.T) {
	for _, later := range []string{"", "[DAMAGE]{default=9; ARMCOM=30; CORCOM=40;}"} {
		t.Run(later, func(t *testing.T) {
			fs := newFixtureFS(t, fixtureFile{path: "weapons/duplicates.tdf", data: "[old]{ID=7; range=9; [DAMAGE]{default=8; ARMCOM=20; ARMCK=25;}}" +
				"[new]{ID=7;" + later + "}"})
			weapons, _, err := CompileWeaponsWithDuplicates(fs)
			if err != nil {
				t.Fatal(err)
			}
			w := weapons["new"]
			if w.Damage["ARMCK"] != 25 {
				t.Fatalf("retained ARMCK override = %d, want 25", w.Damage["ARMCK"])
			}
			if w.Range != 32767 {
				t.Fatalf("range = %d, want default 32767", w.Range)
			}
			if later == "" {
				if w.DamageDefault != 0 || w.Damage["ARMCOM"] != 20 {
					t.Fatalf("absent DAMAGE: default=%d overrides=%v", w.DamageDefault, w.Damage)
				}
			} else if w.DamageDefault != 9 || w.Damage["ARMCOM"] != 30 || w.Damage["CORCOM"] != 40 {
				t.Fatalf("appended DAMAGE: default=%d overrides=%v", w.DamageDefault, w.Damage)
			}
		})
	}
}

// A later case-variant override must precede retained fold-equal entries for
// the runtime lower-bound lookup [06 R-DMG-01 §1]. A lexical case tie-break
// would return an earlier uppercase key when the later key is lowercase.
func TestWeaponSameIDDamageCaseVariantWins(t *testing.T) {
	for _, keys := range [][2]string{{"ARMCOM", "armcom"}, {"armcom", "ARMCOM"}} {
		t.Run(keys[1], func(t *testing.T) {
			fs := newFixtureFS(t, fixtureFile{path: "weapons/duplicates.tdf", data: fmt.Sprintf("[old]{ID=7;[DAMAGE]{%s=20;}}[new]{ID=7;[DAMAGE]{%s=30;}}[last]{ID=7;}", keys[0], keys[1])})
			weapons, _, err := CompileWeaponsWithDuplicates(fs)
			if err != nil {
				t.Fatal(err)
			}
			w := weapons["last"]
			order := w.DamageKeysSorted()
			if len(order) != 2 || order[0] != keys[1] || w.Damage[order[0]] != 30 || w.Damage[keys[0]] != 20 {
				t.Fatalf("override order=%v values=%v, want later spelling first with value 30", order, w.Damage)
			}
			clone := cloneWeapon(w)
			clone.damageOrder[0] = "changed"
			if w.DamageKeysSorted()[0] != keys[1] {
				t.Fatal("clone shares damage lookup order")
			}
		})
	}
}

func TestWeaponSameIDRetainedDamageContributesToHash(t *testing.T) {
	var hashes [2]string
	for i := range hashes {
		fs := newFixtureFS(t, fixtureFile{path: "weapons/duplicates.tdf", data: fmt.Sprintf("[old]{ID=7;[DAMAGE]{ARMCOM=%d;}}[new]{ID=7;}", 20+i)})
		weapons, _, err := CompileWeaponsWithDuplicates(fs)
		if err != nil {
			t.Fatal(err)
		}
		hashes[i] = weapons["new"].Hash
	}
	if hashes[0] == hashes[1] {
		t.Fatal("retained damage changes must change the surviving definition hash")
	}
}

func TestWeaponDamageLookupOrderContributesToHash(t *testing.T) {
	var hashes [2]string
	for i, blocks := range [][2]string{{"ARMCOM=20;", "armcom=30;"}, {"armcom=30;", "ARMCOM=20;"}} {
		fs := newFixtureFS(t, fixtureFile{path: "weapons/duplicates.tdf", data: "[old]{ID=7;[DAMAGE]{" + blocks[0] + "}}[new]{ID=7;[DAMAGE]{" + blocks[1] + "}}"})
		weapons, _, err := CompileWeaponsWithDuplicates(fs)
		if err != nil {
			t.Fatal(err)
		}
		w := weapons["new"]
		if w.Damage["ARMCOM"] != 20 || w.Damage["armcom"] != 30 {
			t.Fatalf("case-order fixture changed values: %v", w.Damage)
		}
		hashes[i] = w.Hash
	}
	if hashes[0] == hashes[1] {
		t.Fatal("different lower-bound damage winners must change the definition hash")
	}
}

func TestWeaponLookupRetainsSlotOrderWithoutSharingMutableSlice(t *testing.T) {
	first := &WeaponDef{ID: 2}
	first.CanonicalKey = "shared"
	later := &WeaponDef{ID: 9}
	later.CanonicalKey = "shared"
	cat := &Catalog{Weapons: map[string]*WeaponDef{"first": first, "later": later}}
	cat.RebuildWeaponIndex()
	records := cat.WeaponRecordsByID()
	records[0] = later
	if got, ok := cat.WeaponByName("SHARED"); !ok || got != first {
		t.Fatal("runtime lookup lost first slot or borrowed caller slice")
	}
	if n := testing.AllocsPerRun(100, func() { cat.WeaponByName("shared") }); n != 0 {
		t.Fatalf("compiled runtime lookup allocates %v times", n)
	}
	clone := cat.Clone()
	got, ok := clone.WeaponByName("shared")
	if !ok || got == first || got != clone.Weapons["first"] {
		t.Fatal("cloned index references original catalog")
	}
}

// TestWeaponIntegerStoreWidths locks the per-key stored widths of the weapon
// record's remaining integer scalars [02 R-KEYS-01 §5]. The widths are not
// uniform, and the point of the test is that they stay non-uniform: `range`,
// `coverage` and `shakemagnitude` are 32-bit stores and must NOT be wrapped,
// while `areaofeffect`, `burst`, `sprayangle`, `accuracy`, `tolerance` and
// `pitchtolerance` are 16-bit and `rendertype`, `color` and `color2` are
// bytes. Each narrow field also carries the extension its readers apply —
// unsigned for `areaofeffect` [06 §9.3], `tolerance` and `pitchtolerance`
// [06 R-WPN-03 §1], signed for `burst` [06 §4.3], `sprayangle` and `accuracy`
// [06 R-WPN-03 §1], the raw byte for the three presentation keys
// [06 R-WFX-01 §1] — so an authored value outside the width is the case that
// tells a sign-extending store from a zero-extending one.
func TestWeaponIntegerStoreWidths(t *testing.T) {
	body := `[WIDTHTEST]
{
	ID=1;
	range=100000;
	coverage=100000;
	shakemagnitude=100000;
	areaofeffect=70000;
	burst=40000;
	sprayangle=40000;
	accuracy=40000;
	tolerance=40000;
	pitchtolerance=40000;
	rendertype=260;
	color=255;
	color2=511;
}
`
	doc := mustParseTDF(t, body)
	wd := compileWeaponSection(doc.Root.Sections()[0], "WIDTHTEST", Provenance{})
	cases := []struct {
		key  string
		got  int32
		want int32
	}{
		{"range", wd.Range, 100000},                   // 32-bit store, unwrapped
		{"coverage", wd.Coverage, 100000},             // 32-bit store, unwrapped
		{"shakemagnitude", wd.ShakeMagnitude, 100000}, // 32-bit store, unwrapped
		{"areaofeffect", wd.AreaOfEffect, 4464},       // 70000 & 0xFFFF, zero-extended
		{"burst", wd.Burst, -25536},                   // 40000 wrapped signed 16-bit
		{"sprayangle", wd.SprayAngle, -25536},         // 40000 wrapped signed 16-bit
		{"accuracy", wd.Accuracy, -25536},             // 40000 wrapped signed 16-bit
		{"tolerance", wd.Tolerance, 40000},            // 16-bit, zero-extended
		{"pitchtolerance", wd.PitchTolerance, 40000},  // 16-bit, zero-extended
		{"rendertype", wd.RenderType, 4},              // 260 & 0xFF
		{"color", wd.Color, 255},                      // the byte the selector reads back as -1
		{"color2", wd.Color2, 255},                    // 511 & 0xFF
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s compiled to %d, want %d", tc.key, tc.got, tc.want)
		}
	}
}
