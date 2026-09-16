package triggers

import (
	"fmt"
	"strings"
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

func TestParseLine(t *testing.T) {
	// Flag-only
	tr, err := parseLine("KillEnemyCommander=1;")
	if err != nil || tr.Kind != KindKillEnemyCommander {
		t.Fatalf("parseLine KillEnemyCommander %v %v", err, tr)
	}
	if tr, err = parseLine("KillEnemyCommander=0;"); err == nil || tr != nil {
		t.Fatalf("zero flag must not build a record: %v %+v", err, tr)
	}
	// Type+count
	tr, err = parseLine("KillUnitType=CORLAB, 1")
	if err != nil || tr.Kind != KindKillUnitType || tr.Type != "CORLAB" || tr.Args[0] != 1 {
		t.Fatalf("parseLine KillUnitType %v %v %v", err, tr.Type, tr.Args)
	}
	// Timer seconds×30 [C17]
	tr, err = parseLine("VictoryTimerRunsOut=3600")
	if err != nil || tr.Args[0] != 3600*30 {
		t.Fatalf("timer %v %v", err, tr)
	}
	if tr, err = parseLine("VictoryTimerRunsOut=0"); err == nil || tr != nil {
		t.Fatalf("zero timer must not build a record: %v %+v", err, tr)
	}
	// Boundary single int — stored after arithmetic >>4 [08 "Evaluation"].
	tr, err = parseLine("AnyUnitPassesX=4500;")
	if err != nil || tr.Kind != KindAnyUnitPassesX || tr.Args[0] != int32(4500)>>4 {
		t.Fatalf("AnyUnitPassesX %v %v want %d", err, tr, int32(4500)>>4)
	}
	if tr, err = parseLine("AnyUnitPassesX=-1;"); err == nil || tr != nil {
		t.Fatalf("negative boundary must not build a record: %v %+v", err, tr)
	}
	for _, value := range []string{"", "nonnumeric", "0x100tail"} {
		if tr, present := ParseCondition("AnyUnitPassesX", value); !present || tr == nil || tr.Args[0] != 0 {
			t.Fatalf("ordinary boundary %q = %+v present=%v, want admitted zero [08 R-TRIG-01 §2]", value, tr, present)
		}
	}
	if tr, present := ParseCondition("AnyUnitPassesX", "0"); !present || tr == nil || tr.Args[0] != 0 {
		t.Fatalf("authored zero boundary must be present: %+v present=%v", tr, present)
	}
	// Radius three ints
	tr, err = parseLine("MoveUnitToRadius=ARMCOM, 1942, 1519, 100")
	if err != nil || tr.Kind != KindMoveUnitToRadius || tr.Type != "ARMCOM" || tr.Args[0] != 1942 || tr.Args[1] != 1519 || tr.Args[2] != 100 {
		t.Fatalf("MoveUnitToRadius %v %v", err, tr)
	}
	tr, err = parseLine("MoveUnitToRadius=ARMCOM2, 1, 2, 3")
	if err != nil || tr.Type != "ARMCOM" {
		t.Fatalf("letters-only scanset did not stop before digit: %v %+v", err, tr)
	}
	// ANYTYPE in boundary — stored after >>4.
	tr, err = parseLine("UnitTypePassesX=ANYTYPE, 6000")
	if err != nil || tr.Type != "" || tr.Args[0] != int32(6000)>>4 {
		t.Fatalf("ANYTYPE boundary %v %v want %d", err, tr, int32(6000)>>4)
	}
	// Name-only records copy the entire authored value, including commas.
	tr, err = parseLine("BuildUnitType=ARMSY, 1")
	if err != nil || tr.Type != "ARMSY, 1" || tr.Args[0] != 0 {
		t.Fatalf("name-only value %v %+v", err, tr)
	}
	// AllUnitsKilledOfType type only
	tr, err = parseLine("AllUnitsKilledOfType=ARMGATE")
	if err != nil || tr.Kind != KindAllUnitsKilledOfType || tr.Type != "ARMGATE" {
		t.Fatalf("AllUnitsKilledOfType %v %v", err, tr)
	}
	// Unknown condition
	if _, err = parseLine("UnknownTrigger=1"); err == nil {
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

	// Ordinary integer-accessor families use decimal-prefix, wrapped TDF
	// conversion rather than the %i scan format [08 R-TRIG-01 §2][fmt tdf].
	if tr, present = ParseCondition("AnyUnitPassesX", "0x100tail"); !present || tr == nil || tr.Args[0] != 0 {
		t.Fatalf("AnyUnit ordinary decimal conversion = %+v present=%v, want admitted zero", tr, present)
	}
	if tr, present = ParseCondition("VictoryTimerRunsOut", "077tail"); !present || tr == nil || tr.Args[0] != 77*30 {
		t.Fatalf("timer ordinary decimal conversion = %+v present=%v, want %d", tr, present, 77*30)
	}
	if tr, present = ParseCondition("VictoryTimerRunsOut", "4294967297tail"); !present || tr == nil || tr.Args[0] != 30 {
		t.Fatalf("timer wrapped integer = %+v present=%v, want 30", tr, present)
	}
	if tr, present = ParseCondition("KillEnemyCommander", "1tail"); !present || tr == nil {
		t.Fatalf("flag decimal prefix did not build: %+v present=%v", tr, present)
	}
}

// parseLine parses a full authored line like "KillUnitType=CORLAB, 1" or
// "AnyUnitPassesX=4500" into a Trigger, handling the condition name,
// ANYTYPE wildcard, and seconds×30 for timer kinds [C14][C15][C17].
func parseLine(line string) (*Trigger, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, fmt.Errorf("triggers: empty line")
	}
	// Split at first '=' as TDF assignment would [08 "Victory and defeat triggers"].
	eq := strings.IndexByte(line, '=')
	var key, rest string
	if eq >= 0 {
		key = strings.TrimSpace(line[:eq])
		rest = strings.TrimSpace(line[eq+1:])
		// Strip trailing ';' as TDF does [fmt tdf].
		rest = strings.TrimSuffix(rest, ";")
		rest = strings.TrimSpace(rest)
	} else {
		// No '=', treat whole line as key with no args (flag-only)
		key = strings.TrimSpace(line)
	}
	if _, ok := KindByName[strings.ToLower(key)]; !ok {
		return nil, fmt.Errorf("triggers: unknown condition %q", key)
	}
	t, present := ParseCondition(key, rest)
	if !present {
		return nil, fmt.Errorf("triggers: condition %q is not present for value %q", key, rest)
	}
	return t, nil
}
