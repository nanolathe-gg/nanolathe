package session

import "testing"

// TestMissionPlacementHealthIsUnconditional locks the created-health formula of
// [08 R-TRIG-01 §9] against the guard the spawner used to carry. An authored
// HealthPercentage of 0 means zero health; "absent" is a different state the
// placement decoder already resolves to 100 ([02 "Map files"]), so the spawner
// must never re-read an authored 0 as absent. Before this, 0 left full health.
func TestMissionPlacementHealthIsUnconditional(t *testing.T) {
	for _, tc := range []struct {
		name         string
		max, percent int32
		want         int32
	}{
		{"authored zero is zero, not absent", 1000, 0, 0},
		{"full stays full", 1000, 100, 1000},
		{"half truncates toward zero", 999, 50, 499},
		{"a third truncates toward zero", 100, 33, 33},
		{"above full scales above full", 1000, 150, 1500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := missionPlacementHealth(tc.max, tc.percent); got != tc.want {
				t.Fatalf("missionPlacementHealth(%d, %d) = %d, want %d", tc.max, tc.percent, got, tc.want)
			}
		})
	}
}
