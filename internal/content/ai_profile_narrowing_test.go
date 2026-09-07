package content

import "testing"

func TestAIWeightStoreUsesFloat32FactorAndLowWord(t *testing.T) {
	tests := []struct {
		name   string
		cur    int32
		factor float64
		want   int32
	}{
		{"full low-word wrap", 1, 4294967296, 0},
		{"positive low word clamps high", 1, 4294967808, 100},
		{"factor narrows before product", 100, 21474836.48, 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := aiWeightStore(test.cur, test.factor); got != test.want {
				t.Fatalf("aiWeightStore(%d, %v) = %d, want %d", test.cur, test.factor, got, test.want)
			}
		})
	}
}
