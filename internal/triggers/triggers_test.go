package triggers

import (
	"testing"
)

func TestVTableCompleteness(t *testing.T) {
	// All 18 trigger vtables present with their record sizes [08 "Trigger object"] [GAP T10] [C15].
	if len(VTables) != KindCount {
		t.Fatalf("VTables length %d want %d", len(VTables), KindCount)
	}
	if KindCount != 18 {
		t.Fatalf("KindCount %d want 18 [C15]", KindCount)
	}
	seen := make(map[Kind]bool)
	seenName := make(map[string]bool)
	for i, vt := range VTables {
		if Kind(i) != vt.Kind {
			t.Fatalf("VTables[%d] Kind %d want %d", i, vt.Kind, i)
		}
		if seen[vt.Kind] {
			t.Fatalf("duplicate Kind %d", vt.Kind)
		}
		seen[vt.Kind] = true
		if vt.Name != vt.Kind.String() {
			t.Fatalf("VTable %d Name %q want %q", vt.Kind, vt.Name, vt.Kind.String())
		}
		if seenName[vt.Name] {
			t.Fatalf("duplicate Name %q", vt.Name)
		}
		seenName[vt.Name] = true
		if vt.Slots != 6 {
			t.Fatalf("VTable %q Slots %d want 6 [08 R-TRIG-01 §2]", vt.Name, vt.Slots)
		}
		if vt.RecordSize != vt.Kind.RecordSize() {
			t.Fatalf("VTable %q RecordSize %d want %d", vt.Name, vt.RecordSize, vt.Kind.RecordSize())
		}
		// Documented buckets [08 "Trigger object"] [GAP T10].
		switch vt.RecordSize {
		case 12, 16, 20, 44, 48, 50, 52, 54, 64:
		default:
			t.Fatalf("VTable %q RecordSize %d not in the established allocation set", vt.Name, vt.RecordSize)
		}
	}
	// Ensure every Kind is covered.
	for k := 0; k < KindCount; k++ {
		if !seen[Kind(k)] {
			t.Fatalf("missing Kind %d %q", k, Kind(k).String())
		}
		if Kind(k).RecordSize() == 0 {
			t.Fatalf("Kind %q RecordSize 0", Kind(k).String())
		}
	}
	// Spot-check documented sizes per bucket [GAP T10].
	checks := []struct {
		kind Kind
		size int
	}{
		{KindKillEnemyCommander, 12},
		{KindDestroyAllUnits, 12},
		{KindMoveUnitToRadius, 64},
		{KindVictoryTimerRunsOut, 16},
		{KindDeathTimerRunsOut, 16},
		{KindUnitTypePassesX, 52},
		{KindAnyUnitPassesZ, 20},
		{KindBuildUnitType, 50},
		{KindCaptureUnitType, 44},
		{KindKillUnitType, 48},
		{KindAllUnitsKilledOfType, 54},
	}
	for _, c := range checks {
		if got := c.kind.RecordSize(); got != c.size {
			t.Fatalf("Kind %q RecordSize %d want %d", c.kind.String(), got, c.size)
		}
	}
	// Verify 18 distinct names as per C15 lists.
	expectedNames := map[string]bool{
		"KillEnemyCommander":   true,
		"DestroyAllUnits":      true,
		"KillAllMobileUnits":   true,
		"BuildUnitType":        true,
		"CaptureUnitType":      true,
		"KillAllOfType":        true,
		"KillUnitType":         true,
		"MoveUnitToRadius":     true,
		"UnitTypePassesX":      true,
		"UnitTypePassesZ":      true,
		"VictoryTimerRunsOut":  true,
		"CommanderKilled":      true,
		"AllUnitsKilled":       true,
		"AllUnitsKilledOfType": true,
		"UnitTypeKilled":       true,
		"DeathTimerRunsOut":    true,
		"AnyUnitPassesX":       true,
		"AnyUnitPassesZ":       true,
	}
	if len(expectedNames) != 18 {
		t.Fatalf("expected map size wrong")
	}
	for n := range expectedNames {
		if _, ok := KindByName[nToLower(n)]; !ok {
			t.Fatalf("KindByName missing %q [C15]", n)
		}
	}
}

func nToLower(s string) string {
	// helper for test; equivalent to strings.ToLower
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func TestDefaultTriggers(t *testing.T) {
	// With no authored victory condition the engine inserts a default destroy-all-units;
	// with no defeat condition, a default all-units-killed [08 "Default triggers"] [C16].
	var vic []*Trigger
	var def []*Trigger
	vic, def = EnsureDefaults(vic, def)
	if len(vic) != 1 || vic[0].Kind != KindDestroyAllUnits {
		t.Fatalf("default victory want DestroyAllUnits got %v", vic)
	}
	if len(def) != 1 || def[0].Kind != KindAllUnitsKilled {
		t.Fatalf("default defeat want AllUnitsKilled got %v", def)
	}
	// Non-empty queues not injected.
	vic2 := []*Trigger{New(KindKillUnitType, "ARMCOM", 1)}
	def2 := []*Trigger{New(KindDeathTimerRunsOut, "", SecondsToTicks(60))}
	vic2, def2 = EnsureDefaults(vic2, def2)
	if len(vic2) != 1 || vic2[0].Kind != KindKillUnitType {
		t.Fatalf("non-empty victory should not inject")
	}
	if len(def2) != 1 {
		t.Fatalf("non-empty defeat should not inject")
	}
}

func TestParseArgsFormats(t *testing.T) {
	// Two argument formats: <name>,<int> and <name>,<int>,<int>,<int> [C14][08].
	typ, args, isFour, err := ParseArgs("CORLAB, 1")
	if err != nil || typ != "CORLAB" || args[0] != 1 || isFour {
		t.Fatalf("ParseArgs CORLAB,1: %v %q %v %v", err, typ, args, isFour)
	}
	typ, args, isFour, err = ParseArgs("ARMCOM, 1942, 1519, 100")
	if err != nil || typ != "ARMCOM" || args[0] != 1942 || args[1] != 1519 || args[2] != 100 || !isFour {
		t.Fatalf("ParseArgs ARMCOM,1942,1519,100: %v %q %v %v", err, typ, args, isFour)
	}
	// ANYTYPE wildcard [C14].
	typ, _, _, err = ParseArgs("ANYTYPE, 5")
	if err != nil || !IsANYTYPE(typ) {
		t.Fatalf("ANYTYPE parse failed %v %q", err, typ)
	}
	// Single int boundary for Any* [08 "Defeat trigger types"].
	typ, args, _, err = ParseArgs("4500")
	if err != nil || typ != "" || args[0] != 4500 {
		t.Fatalf("single int parse %v %q %v", err, typ, args)
	}
	// Mixed case ANYTYPE
	if !IsANYTYPE("anytype") || !IsANYTYPE("AnYtYpE") {
		t.Fatalf("IsANYTYPE case-insensitive")
	}
	if IsANYTYPE("ARMCOM") {
		t.Fatalf("IsANYTYPE false positive")
	}
}

func TestParseLine(t *testing.T) {
	// Flag-only
	tr, err := ParseLine("KillEnemyCommander=1;")
	if err != nil || tr.Kind != KindKillEnemyCommander {
		t.Fatalf("ParseLine KillEnemyCommander %v %v", err, tr)
	}
	if tr, err = ParseLine("KillEnemyCommander=0;"); err == nil || tr != nil {
		t.Fatalf("zero flag must not build a record: %v %+v", err, tr)
	}
	// Type+count
	tr, err = ParseLine("KillUnitType=CORLAB, 1")
	if err != nil || tr.Kind != KindKillUnitType || tr.Type != "CORLAB" || tr.Args[0] != 1 {
		t.Fatalf("ParseLine KillUnitType %v %v %v", err, tr.Type, tr.Args)
	}
	// Timer seconds×30 [C17]
	tr, err = ParseLine("VictoryTimerRunsOut=3600")
	if err != nil || tr.Args[0] != 3600*30 {
		t.Fatalf("timer %v %v", err, tr)
	}
	if tr, err = ParseLine("VictoryTimerRunsOut=0"); err == nil || tr != nil {
		t.Fatalf("zero timer must not build a record: %v %+v", err, tr)
	}
	// Boundary single int — stored after arithmetic >>4 [08 "Evaluation"].
	tr, err = ParseLine("AnyUnitPassesX=4500;")
	if err != nil || tr.Kind != KindAnyUnitPassesX || tr.Args[0] != int32(4500)>>4 {
		t.Fatalf("AnyUnitPassesX %v %v want %d", err, tr, int32(4500)>>4)
	}
	if tr, err = ParseLine("AnyUnitPassesX=-1;"); err == nil || tr != nil {
		t.Fatalf("negative boundary must not build a record: %v %+v", err, tr)
	}
	for _, value := range []string{"", "nonnumeric"} {
		if tr, present := ParseCondition("AnyUnitPassesX", value); present || tr != nil {
			t.Fatalf("malformed boundary %q built a record: %+v", value, tr)
		}
	}
	if tr, present := ParseCondition("AnyUnitPassesX", "0"); !present || tr == nil || tr.Args[0] != 0 {
		t.Fatalf("authored zero boundary must be present: %+v present=%v", tr, present)
	}
	// Radius three ints
	tr, err = ParseLine("MoveUnitToRadius=ARMCOM, 1942, 1519, 100")
	if err != nil || tr.Kind != KindMoveUnitToRadius || tr.Type != "ARMCOM" || tr.Args[0] != 1942 || tr.Args[1] != 1519 || tr.Args[2] != 100 {
		t.Fatalf("MoveUnitToRadius %v %v", err, tr)
	}
	tr, err = ParseLine("MoveUnitToRadius=ARMCOM2, 1, 2, 3")
	if err != nil || tr.Type != "ARMCOM" {
		t.Fatalf("letters-only scanset did not stop before digit: %v %+v", err, tr)
	}
	// ANYTYPE in boundary — stored after >>4.
	tr, err = ParseLine("UnitTypePassesX=ANYTYPE, 6000")
	if err != nil || tr.Type != "" || tr.Args[0] != int32(6000)>>4 {
		t.Fatalf("ANYTYPE boundary %v %v want %d", err, tr, int32(6000)>>4)
	}
	// Name-only records copy the entire authored value, including commas.
	tr, err = ParseLine("BuildUnitType=ARMSY, 1")
	if err != nil || tr.Type != "ARMSY, 1" || tr.Args[0] != 0 {
		t.Fatalf("name-only value %v %+v", err, tr)
	}
	// AllUnitsKilledOfType type only
	tr, err = ParseLine("AllUnitsKilledOfType=ARMGATE")
	if err != nil || tr.Kind != KindAllUnitsKilledOfType || tr.Type != "ARMGATE" {
		t.Fatalf("AllUnitsKilledOfType %v %v", err, tr)
	}
	// Unknown condition
	if _, err = ParseLine("UnknownTrigger=1"); err == nil {
		t.Fatalf("unknown should error")
	}
}

func TestParseConditionScanIntegerFamilies(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  [3]int32
	}{
		{name: "hexadecimal", value: "ARMCOM, 0x10tail, 0, 0", want: [3]int32{16, 0, 0}},
		{name: "octal", value: "ARMCOM, 077tail, 0, 0", want: [3]int32{63, 0, 0}},
		{name: "signed", value: "ARMCOM, -25tail, +17suffix, -010rest", want: [3]int32{-25, 17, -8}},
		{name: "missing", value: "ARMCOM", want: [3]int32{}},
		{name: "failed", value: "ARMCOM, nope, +, 0x", want: [3]int32{}},
		{name: "binary prefix is not an extension", value: "ARMCOM, 0b10, 0B11, 0", want: [3]int32{}},
		{name: "underscore stops numeric prefix", value: "ARMCOM, 1_2, 0x1_0, 07_7", want: [3]int32{1, 1, 7}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, present := ParseCondition("MoveUnitToRadius", tt.value)
			if !present || tr == nil {
				t.Fatalf("ParseCondition(%q) did not build the authored record", tt.value)
			}
			if tr.Args != tt.want {
				t.Fatalf("ParseCondition(%q) args = %v, want %v", tt.value, tr.Args, tt.want)
			}
		})
	}

	tr, present := ParseCondition("UnitTypePassesX", "ARMCOM, 0x100tail")
	if !present || tr == nil || tr.Args[0] != 16 {
		t.Fatalf("type boundary applies scan conversion before >>4: %+v present=%v", tr, present)
	}

	// The ordinary integer-accessor families do not use the %i prefix scan.
	if tr, present = ParseCondition("AnyUnitPassesX", "0x100tail"); present || tr != nil {
		t.Fatalf("AnyUnit boundary unexpectedly used scan conversion: %+v present=%v", tr, present)
	}
	if tr, present = ParseCondition("VictoryTimerRunsOut", "077tail"); present || tr != nil {
		t.Fatalf("timer unexpectedly used scan conversion: %+v present=%v", tr, present)
	}
}
