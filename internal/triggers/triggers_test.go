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
		if vt.Slots != 8 {
			t.Fatalf("VTable %q Slots %d want 8 [08 Trigger object]", vt.Name, vt.Slots)
		}
		if vt.RecordSize != vt.Kind.RecordSize() {
			t.Fatalf("VTable %q RecordSize %d want %d", vt.Name, vt.RecordSize, vt.Kind.RecordSize())
		}
		// Documented buckets [08 "Trigger object"] [GAP T10].
		switch vt.RecordSize {
		case 0x0C, 0x10, 0x14, 0x30, 0x32, 0x36, 0x40:
		default:
			t.Fatalf("VTable %q RecordSize %d not in documented buckets {0xC,0x10,0x14,0x30,0x32,0x36,0x40}", vt.Name, vt.RecordSize)
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
		{KindKillEnemyCommander, 0x0C},
		{KindDestroyAllUnits, 0x0C},
		{KindMoveUnitToRadius, 0x40},
		{KindVictoryTimerRunsOut, 0x10},
		{KindDeathTimerRunsOut, 0x10},
		{KindUnitTypePassesX, 0x14},
		{KindAnyUnitPassesZ, 0x14},
		{KindBuildUnitType, 0x30},
		{KindKillUnitType, 0x32},
		{KindAllUnitsKilledOfType, 0x36},
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
	// Boundary single int — stored after arithmetic >>4 [08 "Evaluation"].
	tr, err = ParseLine("AnyUnitPassesX=4500;")
	if err != nil || tr.Kind != KindAnyUnitPassesX || tr.Args[0] != int32(4500)>>4 {
		t.Fatalf("AnyUnitPassesX %v %v want %d", err, tr, int32(4500)>>4)
	}
	// Radius three ints
	tr, err = ParseLine("MoveUnitToRadius=ARMCOM, 1942, 1519, 100")
	if err != nil || tr.Kind != KindMoveUnitToRadius || tr.Type != "ARMCOM" || tr.Args[0] != 1942 || tr.Args[1] != 1519 || tr.Args[2] != 100 {
		t.Fatalf("MoveUnitToRadius %v %v", err, tr)
	}
	// ANYTYPE in boundary — stored after >>4.
	tr, err = ParseLine("UnitTypePassesX=ANYTYPE, 6000")
	if err != nil || !IsANYTYPE(tr.Type) || tr.Args[0] != int32(6000)>>4 {
		t.Fatalf("ANYTYPE boundary %v %v want %d", err, tr, int32(6000)>>4)
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
