package triggers

import "testing"

// TestScanConversionFailureStillBuildsRecord locks the half of
// [08 R-TRIG-01 §2] that governs behaviour: the builder never tests the scan
// family's conversion count, so a condition whose integer argument is missing
// or unconvertible still yields a record.
//
// This is the easy silent regression — rejecting the condition looks like
// defensive parsing and would delete a mission's victory or defeat trigger
// outright. The value the failed conversion leaves behind is retail's own
// stack residue and is deliberately not asserted here; only the record's
// existence and its successfully converted neighbours are.
func TestScanConversionFailureStillBuildsRecord(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value string
		typ   string
	}{
		{"missing integer", "KillUnitType", "ARMCOM", "ARMCOM"},
		{"unconvertible integer", "UnitTypePassesX", "ARMCOM,nowhere", "ARMCOM"},
		{"missing tail integers", "MoveUnitToRadius", "ARMCOM,64", "ARMCOM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseCondition(tc.key, tc.value)
			if !ok || got == nil {
				t.Fatalf("%s=%q must still build a record", tc.key, tc.value)
			}
			if got.Type != tc.typ {
				t.Fatalf("name argument: got %q want %q", got.Type, tc.typ)
			}
		})
	}

	// A converted neighbour must survive its failed sibling: MoveUnitToRadius
	// takes X, Z and radius in that order, so the X that did convert stays.
	got, ok := ParseCondition("MoveUnitToRadius", "ARMCOM,64")
	if !ok || got == nil {
		t.Fatal("MoveUnitToRadius must build")
	}
	if got.Args[0] != 64 {
		t.Fatalf("converted X argument: got %d want 64", got.Args[0])
	}
}
