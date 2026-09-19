package content

import "testing"

// weaponWithBody compiles one authored weapon section body.
func weaponWithBody(t *testing.T, body string) *WeaponDef {
	t.Helper()
	doc := mustParseTDF(t, body)
	sec := doc.Root.Sections()[0]
	return compileWeaponSection(sec, "SUBMISSILE", Provenance{})
}

// TestWeaponTargetKeysAreTypedNotUnknown locks the parse of the four non-retail
// target keys: each reaches its own field and none of them is retained in the
// inert Unknown map any more. The evidence for what they mean is in
// research/extensions/weapon-target-keys.md; this test locks only that the
// loader reads them.
func TestWeaponTargetKeysAreTypedNotUnknown(t *testing.T) {
	wd := weaponWithBody(t, `[SUBMISSILE]
{
	ID=7;
	waterweapon=1;
	nottoair=1;
	toaironly=1;
	nottounderwater=1;
	surfacefire=1;
	aimrate=3;
}
`)
	if !wd.NotToAir || !wd.ToAirOnly || !wd.NotToUnderwater || !wd.SurfaceFire {
		t.Fatalf("target keys = %v/%v/%v/%v, want all true",
			wd.NotToAir, wd.ToAirOnly, wd.NotToUnderwater, wd.SurfaceFire)
	}
	for _, key := range []string{"nottoair", "toaironly", "nottounderwater", "surfacefire"} {
		if _, ok := wd.Unknown[key]; ok {
			t.Fatalf("%q is still retained as an unknown key", key)
		}
	}
	if _, ok := wd.Unknown["aimrate"]; !ok {
		t.Fatal("a genuinely unread key stopped reaching Unknown; the fixture proves nothing")
	}
}

// TestWeaponTargetKeysDefaultOffAndLeaveTheDigestAlone is the retail guarantee:
// a record that authors none of the keys canonicalizes to the same bytes it did
// before they had a reader, so the retail catalog digest cannot move. The
// comparison is against a record whose only difference is one authored key.
func TestWeaponTargetKeysDefaultOffAndLeaveTheDigestAlone(t *testing.T) {
	plain := weaponWithBody(t, `[SUBMISSILE]
{
	ID=7;
	waterweapon=1;
	range=600;
}
`)
	if plain.NotToAir || plain.ToAirOnly || plain.NotToUnderwater || plain.SurfaceFire {
		t.Fatal("an unauthored target key does not default to false")
	}
	// A record that authors an unrelated unknown key keeps that key's
	// contribution and nothing else: the extension block is empty.
	withUnknown := weaponWithBody(t, `[SUBMISSILE]
{
	ID=7;
	waterweapon=1;
	range=600;
	aimrate=3;
}
`)
	if withUnknown.Hash == plain.Hash {
		t.Fatal("an authored unknown key did not reach the digest; the fixture proves nothing")
	}
	for _, authored := range []string{"nottoair", "nottounderwater", "surfacefire", "toaironly"} {
		keyed := weaponWithBody(t, `[SUBMISSILE]
{
	ID=7;
	waterweapon=1;
	range=600;
	`+authored+`=1;
}
`)
		if keyed.Hash == plain.Hash {
			t.Fatalf("authoring %q left the definition digest unchanged", authored)
		}
	}
}
