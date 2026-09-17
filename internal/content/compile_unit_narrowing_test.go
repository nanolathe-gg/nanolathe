package content

import "testing"

// compileOneUnit compiles a single `[UNITINFO]` body through the whole unit
// record compiler so the derived tail runs.
func compileOneUnit(t *testing.T, body string) *UnitDef {
	t.Helper()
	fs := newFixtureFS(t, fixtureFile{path: "units/u.fbi", data: "[UNITINFO]{UnitName=u; " + body + "}"})
	result, err := compileUnitsWithLanguage(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.records) != 1 {
		t.Fatalf("records = %d", len(result.records))
	}
	return result.records[0]
}

// TestMinCloakDistanceDerivedDefault locks the derived substitution the record
// compiler applies in its tail: a cloak-capable definition (`cloakcost > 0`,
// strictly) whose `mincloakdistance` compiled to 0 gets 80 instead
// [02 "Unit record"] ("Two derived fields the key table does not show").
// The gate is strict, so cloakcost 0 or negative leaves the 0 alone, and an
// authored non-zero radius is never replaced.
func TestMinCloakDistanceDerivedDefault(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want int32
	}{
		{"cloakable, key absent", "CloakCost=450;", 80},
		{"cloakable, key authored zero", "CloakCost=450; MinCloakDistance=0;", 80},
		{"cloakable, key authored", "CloakCost=450; MinCloakDistance=12;", 12},
		{"cloakcost zero is not cloak-capable", "CloakCost=0;", 0},
		{"cloakcost negative is not cloak-capable", "CloakCost=-5;", 0},
		{"no cloakcost at all", "MaxDamage=10;", 0},
	} {
		if got := compileOneUnit(t, tc.body).MinCloakDistance; got != tc.want {
			t.Fatalf("%s: mincloakdistance = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestUnitRecordStoreWidths locks the record's narrow integer stores and the
// extension each key's own readers apply [02 R-KEYS-01 §5]. A wrong signedness
// is worse than no narrowing, so both arms are asserted: the sensor group and
// cruisealt sign-extend, everything else here zero-extends. No stock definition
// authors a value outside its field, so this fixture is deliberately
// out-of-range on every key.
func TestUnitRecordStoreWidths(t *testing.T) {
	// 70000 & 0xFFFF = 4464; 300 & 0xFF = 44; 40000 as a signed word = -25536.
	u := compileOneUnit(t, `
TurnRate=70000; WaterLine=300; TransportSize=300; TransportCapacity=-1;
MakesMetal=256; WorkerTime=-1; HealTime=70000; BuildTime=70000;
CruiseAlt=40000; BuildAngle=40000; BuildDistance=70000; SortBias=70000;
ManeuverLeashLength=-1; AttackRunLength=70000; KamikazeDistance=-1;
MaxDamage=70000; SightDistance=40000; RadarDistance=-1; SonarDistance=70000;
RadarDistanceJam=40000; SonarDistanceJam=70000;`)
	for _, tc := range []struct {
		key  string
		got  int32
		want int32
	}{
		{"turnrate", u.TurnRate, 4464},
		{"waterline", u.Waterline, 44},
		{"transportsize", u.TransportSize, 44},
		{"transportcapacity", u.TransportCapacity, 255},
		{"makesmetal", u.MakesMetal, 0},
		{"workertime", u.WorkerTime, 65535},
		{"healtime", u.HealTime, 4464},
		{"buildtime", u.BuildTime, 70000}, // 32-bit store: not narrowed
		{"cruisealt", u.CruiseAlt, -25536},
		{"buildangle", u.BuildAngle, 40000},
		{"builddistance", u.BuildDistance, 4464},
		{"sortbias", u.SortBias, 4464},
		{"maneuverleashlength", u.ManeuverLeashLength, 65535},
		{"attackrunlength", u.AttackRunLength, 4464},
		{"kamikazedistance", u.KamikazeDistance, 65535},
		{"maxdamage", u.MaxDamage, 70000}, // 32-bit store: not narrowed
		{"sightdistance", u.SightDistance, -25536},
		{"radardistance", u.RadarDistance, -1},
		{"sonardistance", u.SonarDistance, 4464},
		{"radardistancejam", u.RadarDistanceJam, -25536},
		{"sonardistancejam", u.SonarDistanceJam, 4464},
	} {
		if tc.got != tc.want {
			t.Fatalf("%s = %d, want %d", tc.key, tc.got, tc.want)
		}
	}
}

// TestMinCloakDistanceSubstitutionSeesTheStoredWord pins the ORDER of the two
// rules on the same key: the 16-bit store runs first, so an authored 65,536
// stores zero and the derived substitution then replaces it with 80, exactly as
// retail's compare against the stored word does [02 "Unit record"].
func TestMinCloakDistanceSubstitutionSeesTheStoredWord(t *testing.T) {
	if got := compileOneUnit(t, "CloakCost=450; MinCloakDistance=65536;").MinCloakDistance; got != 80 {
		t.Fatalf("mincloakdistance = %d, want 80 after the 16-bit store wraps 65536 to 0", got)
	}
	if got := compileOneUnit(t, "CloakCost=450; MinCloakDistance=65600;").MinCloakDistance; got != 64 {
		t.Fatalf("mincloakdistance = %d, want the stored 64 (no substitution)", got)
	}
}
