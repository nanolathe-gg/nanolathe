package content

import (
	"math"
	"slices"
	"strings"
	"testing"
)

func unitWithBody(t *testing.T, body string) *UnitDef {
	t.Helper()
	doc := mustParseTDF(t, body)
	return compileUnitSection(doc.Root.Section("UNITINFO"), "units/test.fbi", "", Provenance{
		LogicalPath: "units/test.fbi",
		ProviderID:  "fixture",
	})
}

func TestCommunityUnitKeysParseAndPreserveAbsentIdentity(t *testing.T) {
	plain := unitWithBody(t, `[UNITINFO]
{
	UnitName=TEST;
}
`)
	if plain.Rotations != FacingSouth {
		t.Fatalf("absent rotations = %04b, want south only", plain.Rotations)
	}
	if !slices.Equal(plain.VeterancyThresholds, defaultVeterancyThresholds[:]) {
		t.Fatalf("absent veterancy thresholds = %v, want defaults", plain.VeterancyThresholds)
	}
	if plain.VeterancyAccuracyBuffRate != 12 {
		t.Fatalf("absent veterancy rate = %d, want 12", plain.VeterancyAccuracyBuffRate)
	}

	// Extension defaults are available to consumers but emit no canonical
	// bytes unless their source key was authored.
	withoutPresence := *plain
	withoutPresence.Rotations = FacingSouth | FacingEast | FacingNorth | FacingWest
	withoutPresence.VeterancyThresholds = []uint32{99}
	withoutPresence.VeterancyAccuracyBuffRate = 99
	withoutPresence.PreviewPieces = "body"
	if got := HashDefinition(writeUnitCanonical(&withoutPresence)); got != plain.Hash {
		t.Fatalf("absent extension fields changed retail identity: got %s want %s", got, plain.Hash)
	}

	authored := unitWithBody(t, `[UNITINFO]
{
	UnitName=TEST;
	Rotations=?wEn;
	VeterancyThresholds=7abc 0x10 3 -1 +8;
	VeterancyAccuracyBuffRate=-4;
	TransportedExplodeAs=BOOM;
	TransportedSelfDestructAs=KABOOM;
	PreviewPieces=body, turret;
	PreviewPiecesS=south;
	PreviewPiecesE=east;
	PreviewPiecesN=north;
	PreviewPiecesW=west;
	PreviewFaceOpponent=2;
	PreviewObject3D=ghost.3do;
}
`)
	if authored.Rotations != FacingSouth|FacingEast|FacingNorth|FacingWest {
		t.Fatalf("authored rotations = %04b, want all facings", authored.Rotations)
	}
	wantThresholds := []uint32{3, math.MaxUint32, 8}
	if !slices.Equal(authored.VeterancyThresholds, wantThresholds) {
		t.Fatalf("authored thresholds = %v, want %v", authored.VeterancyThresholds, wantThresholds)
	}
	if authored.VeterancyAccuracyBuffRate != 0 {
		t.Fatalf("nonpositive veterancy rate = %d, want off (0)", authored.VeterancyAccuracyBuffRate)
	}
	if authored.TransportedExplodeAs != "BOOM" || authored.TransportedSelfDestructAs != "KABOOM" {
		t.Fatalf("transported names = %q/%q", authored.TransportedExplodeAs, authored.TransportedSelfDestructAs)
	}
	if authored.PreviewPieces != "body, turret" || authored.PreviewPiecesS != "south" || authored.PreviewPiecesE != "east" || authored.PreviewPiecesN != "north" || authored.PreviewPiecesW != "west" || !authored.PreviewFaceOpponent || authored.PreviewObject3D != "ghost.3do" {
		t.Fatalf("preview metadata was not retained: %#v", authored)
	}
	for _, key := range []string{"Rotations", "VeterancyThresholds", "VeterancyAccuracyBuffRate", "TransportedExplodeAs", "TransportedSelfDestructAs", "PreviewPieces", "PreviewPiecesS", "PreviewPiecesE", "PreviewPiecesN", "PreviewPiecesW", "PreviewFaceOpponent", "PreviewObject3D"} {
		if _, ok := authored.Unknown[key]; ok {
			t.Fatalf("typed key %q remained in Unknown", key)
		}
	}
	if authored.Hash == plain.Hash {
		t.Fatal("authored extension metadata did not contribute to identity")
	}

	for _, raw := range []string{"", "nonsense 7abc 0x10"} {
		u := unitWithBody(t, "[UNITINFO]\n{\nUnitName=TEST;\nVeterancyThresholds="+raw+";\n}\n")
		if !slices.Equal(u.VeterancyThresholds, defaultVeterancyThresholds[:]) {
			t.Fatalf("thresholds %q = %v, want defaults", raw, u.VeterancyThresholds)
		}
		if u.Hash == plain.Hash {
			t.Fatalf("authored thresholds %q were indistinguishable from absence", raw)
		}
	}
}

func TestCommunityWeaponFlagsUseStoredLowBit(t *testing.T) {
	plain := weaponWithBody(t, `[WEAPON]
{
	ID=7;
}
`)
	wd := weaponWithBody(t, `[WEAPON]
{
	ID=7;
	notoverwater=1;
	notoverland=2;
	nomapweaponalert=3;
	reloadbar=-1;
}
`)
	if !wd.NoOverWater || wd.NoOverLand || !wd.NoMapWeaponAlert || !wd.ReloadBar {
		t.Fatalf("extension low bits = water:%v land:%v alert:%v reload:%v", wd.NoOverWater, wd.NoOverLand, wd.NoMapWeaponAlert, wd.ReloadBar)
	}
	for _, key := range []string{"notoverwater", "notoverland", "nomapweaponalert", "reloadbar"} {
		if _, ok := wd.Unknown[key]; ok {
			t.Fatalf("typed key %q remained in Unknown", key)
		}
	}
	if wd.Hash == plain.Hash {
		t.Fatal("authored weapon extension flags did not contribute to identity")
	}
	falseAuthored := weaponWithBody(t, `[WEAPON]
{
	ID=7;
	notoverwater=2;
}
`)
	if falseAuthored.NoOverWater || falseAuthored.Hash == plain.Hash {
		t.Fatal("authored false flag must stay false and remain distinct from absence")
	}
}

func TestTransportedExplosionLinksAndClearsMiss(t *testing.T) {
	u := unitWithBody(t, `[UNITINFO]
{
	UnitName=TEST;
	TransportedExplodeAs=BOOM;
	TransportedSelfDestructAs=MISSING;
}
`)
	before := u.Hash
	boom := &WeaponDef{DefinitionHeader: DefinitionHeader{CanonicalKey: "boom"}, ID: 12}
	warnings := linkUnitWeaponRecords([]*UnitDef{u}, map[string]*WeaponDef{"boom": boom})
	if u.TransportedExplodeAs != "BOOM" || u.TransportedExplodeAsDef != boom {
		t.Fatalf("resolved transported explosion = %q/%p, want BOOM/%p", u.TransportedExplodeAs, u.TransportedExplodeAsDef, boom)
	}
	if u.TransportedSelfDestructAs != "" || u.TransportedSelfDestructAsDef != nil {
		t.Fatalf("unresolved transported self-destruct = %q/%v, want empty/nil", u.TransportedSelfDestructAs, u.TransportedSelfDestructAsDef)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `expected weapon "MISSING"`) {
		t.Fatalf("unresolved warning = %v", warnings)
	}
	if u.Hash == before {
		t.Fatal("clearing the unresolved compiled name did not refresh identity")
	}
}
