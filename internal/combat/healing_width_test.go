package combat

import "testing"

func TestApplyHealingSignedWordsAndUnsignedClamp(t *testing.T) {
	// Both narrowing boundaries belong to the helper, including direct callers
	// outside the packet adapter [06 §9.1][06 R-DMG-01 §4].
	for _, tc := range []struct {
		health, maximum int32
		amount          uint16
		want            int32
	}{
		{32760, 40000, 10, -32766},
		{65535, 100, 1, 0},
		{40000, 50000, 0, -15536},
		{-2, 100, 1, 100},
		{10, 0, 5, 0},
		{32767, -1, 65535, 32766},
	} {
		if got := ApplyHealing(tc.health, tc.maximum, tc.amount); got != tc.want {
			t.Fatalf("heal(%d,%d,%d)=%d, want %d", tc.health, tc.maximum, tc.amount, got, tc.want)
		}
	}
}
