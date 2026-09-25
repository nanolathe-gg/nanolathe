package community

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestShippedTableIdentity(t *testing.T) {
	wantDigest := map[string]string{
		"ota":        "9f578f1bfff90fb537a3bbc061e207522b1be8daa0a2e1990b6c8a3efcfb6e1b",
		"prota":      "66ef84a5fc72bf71e8f756e7237a22f9c5d3d5d82203c4dff7d78f0453e0ef9b",
		"escalation": "90edd0191a613f76736f3dc3de82aff92245db2dcd6758ebda32ce148fc2c2eb",
		"tazero":     "2b1c493261fc088e9e3c317b201d31d87805a7f11f74250fccf06ea2008020e3",
		"bta":        "1651b0fd12fbb0b3ec3c4b1da01bdfb8b37175b049f4887a851e9f9039834285",
		"mayhem":     "973b1470b63ec5f6c409c7ba5d9c4ec9af2e4e624ccfb444093df8bb5c1daa74",
		"twilight":   "973b1470b63ec5f6c409c7ba5d9c4ec9af2e4e624ccfb444093df8bb5c1daa74",
	}
	for name, want := range wantDigest {
		got, err := Table(name)
		if err != nil {
			t.Fatalf("Table(%q): %v", name, err)
		}
		if got.Digest() != want {
			t.Errorf("Table(%q).Digest() = %s, want %s", name, got.Digest(), want)
		}
	}

	prota, _ := Table("prota")
	if !prota.AreaDamageOverflow || !prota.GridClaimTieBreak || prota.OffMapAircraftMarginTiles != 1 {
		t.Fatalf("prota matrix flags = %+v", prota)
	}
	if prota.ProjectileCapacity != 3000 || prota.ExplosionCapacity != 3000 || prota.DebrisCapacity != 1000 {
		t.Fatalf("prota pool capacities = %d/%d/%d", prota.ProjectileCapacity, prota.ExplosionCapacity, prota.DebrisCapacity)
	}
	if prota.PathStepAllowance != 66650 || prota.UnitLimit != 1500 {
		t.Fatalf("prota preference defaults = path %d, units %d", prota.PathStepAllowance, prota.UnitLimit)
	}
	escalation, _ := Table("escalation")
	if escalation.RepairRate != (RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 3}) || !escalation.BuildWeaponSlotGuard || !escalation.AirCorpseFall {
		t.Fatalf("escalation-only values = %+v", escalation)
	}
	ota, _ := Table("ota")
	if ota.ConstructionKickout || ota.GuardingBuildersHold || ota.PatrollingBuilderFilters || ota.AreaDamageOverflow || ota.GridClaimTieBreak {
		t.Fatalf("ota profile enabled profile-gated behavior: %+v", ota)
	}
}

func TestResolvePrecedenceAndStrictIdentity(t *testing.T) {
	falseValue := false
	zero := 0
	configuredLimit := 1000
	got, err := Resolve(false,
		Overrides{Table: "escalation", UnitLimit: &configuredLimit},
		Overrides{AreaDamageOverflow: &falseValue, UnitLimit: &zero},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.AreaDamageOverflow || got.UnitLimit != 0 {
		t.Fatalf("later explicit false/zero did not win: %+v", got)
	}
	if !got.AirCorpseFall || !got.RepairRate.Enabled {
		t.Fatalf("table replacement did not preserve escalation values: %+v", got)
	}

	strict, err := Resolve(true, Overrides{Table: "does-not-exist", UnitLimit: &configuredLimit})
	if err != nil {
		t.Fatalf("Strict source was not ignored: %v", err)
	}
	if strict != (Features{}) {
		t.Fatalf("Strict features = %+v, want zero", strict)
	}
}

func TestOverridesJSONPreservesFalseAndZero(t *testing.T) {
	var got Overrides
	if err := json.Unmarshal([]byte(`{"areaDamageOverflow":false,"unitLimit":0,"repairRate":{"enabled":false}}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.AreaDamageOverflow == nil || *got.AreaDamageOverflow || got.UnitLimit == nil || *got.UnitLimit != 0 {
		t.Fatalf("decoded pointers = %+v", got)
	}
	if got.RepairRate == nil || got.RepairRate.Enabled == nil || *got.RepairRate.Enabled {
		t.Fatalf("decoded repair override = %+v", got.RepairRate)
	}
	for _, raw := range []string{
		`{"areaDamageOverflwo":true}`,
		`{"repairRate":{"multipler":3}}`,
		`{} {}`,
	} {
		if err := json.Unmarshal([]byte(raw), &got); err == nil {
			t.Errorf("json.Unmarshal(%q) succeeded", raw)
		}
	}
}

func TestSnapRadiiClampToSelectedTableMaxima(t *testing.T) {
	nine := 9
	for _, tc := range []struct {
		name      string
		wantMex   int
		wantWreck int
	}{
		{"prota", 3, 1},
		{"escalation", 0, 1},
		{"bta", 1, 1},
		{"ota", 0, 0},
	} {
		got, err := Resolve(false, Overrides{
			Table:           tc.name,
			MexSnapRadius:   &nine,
			WreckSnapRadius: &nine,
		})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", tc.name, err)
		}
		if got.MexSnapRadius != tc.wantMex || got.WreckSnapRadius != tc.wantWreck {
			t.Errorf("Resolve(%q) snap radii = %d/%d, want %d/%d", tc.name, got.MexSnapRadius, got.WreckSnapRadius, tc.wantMex, tc.wantWreck)
		}
	}
}

func TestParseOverrideAndValidation(t *testing.T) {
	parsed, err := ParseOverride("repairRate.repairMultiplier=3")
	if err != nil || parsed.RepairRate == nil || parsed.RepairRate.RepairMultiplier == nil || *parsed.RepairRate.RepairMultiplier != 3 {
		t.Fatalf("ParseOverride repair multiplier = %+v, %v", parsed, err)
	}
	parsed, err = ParseOverride("areaDamageOverflow=false")
	if err != nil || parsed.AreaDamageOverflow == nil || *parsed.AreaDamageOverflow {
		t.Fatalf("ParseOverride explicit false = %+v, %v", parsed, err)
	}
	for _, malformed := range []string{
		"missing-equals",
		"unknown=true",
		"areaDamageOverflow=1",
		"unitLimit=twenty",
		"unitLimit=19",
		"repairRate.repairMultiplier=101",
		"pathStepAllowance=-1",
		"pathStepAllowance=2147483648",
		"projectileCapacity=32768",
		"explosionCapacity=65536",
		"table=unknown",
		"table=",
	} {
		if _, err := ParseOverride(malformed); err == nil {
			t.Errorf("ParseOverride(%q) succeeded", malformed)
		}
	}

	tooHigh := 101
	if _, err := Resolve(false, Overrides{RepairRate: &RepairRateOverrides{RepairMultiplier: &tooHigh}}); err == nil {
		t.Error("repair multiplier above source bound succeeded")
	}
	tooFewUnits := 19
	if _, err := Resolve(false, Overrides{UnitLimit: &tooFewUnits}); err == nil {
		t.Error("unit limit below approved settings bound succeeded")
	}
	negative := -1
	if _, err := Resolve(false, Overrides{PathStepAllowance: &negative}); err == nil {
		t.Error("negative path allowance succeeded")
	}
}

func TestResolveDefaultsToMainlineAndDigestStable(t *testing.T) {
	got, err := Resolve(false)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := Table(Mainline)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve(false) = %+v, want mainline %+v", got, want)
	}
	const wantDigest = "66ef84a5fc72bf71e8f756e7237a22f9c5d3d5d82203c4dff7d78f0453e0ef9b"
	firstDigest := got.Digest()
	secondDigest := got.Digest()
	if firstDigest != secondDigest {
		t.Fatal("digest changed between calls")
	}
	if firstDigest != wantDigest {
		t.Fatalf("mainline digest = %s, want %s", firstDigest, wantDigest)
	}
}

// TestProTAPackageSwitchesOffInEveryTableAndOverridable locks the selection
// policy of DESIGN_COMMUNITY_PATCH §4.7: the five ProTA 4.8 package switches
// are false in every shipped table (mainline prota included), so retail
// content keeps its AI under Community 3.9 and Modern, and they are reachable
// only through a gameplay source, which Strict ignores.
func TestProTAPackageSwitchesOffInEveryTableAndOverridable(t *testing.T) {
	for _, name := range tableNames() {
		f, err := Table(name)
		if err != nil {
			t.Fatal(err)
		}
		if f.AIDifficultyIncome || f.AIStockpileProducts || f.TargetLockRelease || f.AIApplianceEnergy || f.AIBuilderStopThreshold ||
			f.WorkingWeaponsAutonomous || f.AttackSingleSlotTake || f.MapFeatureOwnerEleven || f.ResurrectionTextFix {
			t.Fatalf("Table(%q) enables a ProTA package switch: %+v", name, f)
		}
	}
	var profile Overrides
	if err := json.Unmarshal([]byte(`{"table":"prota","aiDifficultyIncome":true,"aiStockpileProducts":true,"targetLockRelease":true,"aiApplianceEnergy":true,"aiBuilderStopThreshold":true}`), &profile); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(false, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !got.AIDifficultyIncome || !got.AIStockpileProducts || !got.TargetLockRelease || !got.AIApplianceEnergy || !got.AIBuilderStopThreshold {
		t.Fatalf("content-profile block did not enable the package switches: %+v", got)
	}
	mainline, _ := Table(Mainline)
	if got.Digest() == mainline.Digest() {
		t.Fatal("enabled package switches did not enter the digest")
	}
	off, err := ParseOverride("aiDifficultyIncome=false")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ = Resolve(false, profile, off); got.AIDifficultyIncome || !got.AIStockpileProducts {
		t.Fatalf("command-line override did not win field by field: %+v", got)
	}
	if strict, _ := Resolve(true, profile); strict != (Features{}) {
		t.Fatalf("Strict resolved package switches: %+v", strict)
	}
}
