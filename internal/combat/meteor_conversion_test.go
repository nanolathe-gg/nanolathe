package combat

import (
	"math"
	"testing"
)

// Source floats narrow before arithmetic, but arithmetic does not narrow again
// before signed-64 truncation and low-word retention [06 §6.5][01 R-DET-01 §1].
func TestMeteorConversionSourceAndIntegerBoundaries(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want int32
	}{
		{0.3, 99}, {-0.3, -99}, {0, 0}, {math.SmallestNonzeroFloat32, 0},
		{math.NaN(), 0}, {math.Inf(1), 0}, {math.Inf(-1), 0},
	} {
		if got := MeteorDelay(tc.in); got != tc.want {
			t.Errorf("spacing(%v)=%d want %d", tc.in, got, tc.want)
		}
	}
	for _, tc := range []struct {
		in   float64
		want int32
	}{
		{0.7, 20}, {-0.7, -20}, {5, 150}, {60, 1800},
		{134217728, -268435456}, {-134217728, 268435456},
		{4294967296, 0}, {4294967808, 15360}, {-4294967808, -15360},
		{576460752303423488, 0}, {-576460752303423488, 0},
		{math.NaN(), 0}, {math.Inf(1), 0}, {math.Inf(-1), 0},
	} {
		for _, convert := range []func(float64) int32{MeteorDurationTicks, MeteorIntervalTicks} {
			if got := convert(tc.in); got != tc.want {
				t.Errorf("seconds(%v)=%d want %d", tc.in, got, tc.want)
			}
		}
	}
}
