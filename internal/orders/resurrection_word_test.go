package orders

import "testing"

// The live delay consumes the unsigned worker word, not the compiler's wider
// authored integer. Wrapping must precede quantum selection [05 R-WORK-01 §7].
func TestResurrectionDelayNarrowsWorkerWordBeforeDivision(t *testing.T) {
	for _, tc := range []struct {
		worker, want int32
	}{
		{-1, 4},            // word 65535, quantum 2184
		{65536, 0},         // word zero retains immediate completion
		{65536 + 29, 0},    // word below thirty retains immediate completion
		{65536 + 30, 9000}, // word thirty gives quantum one
		{65536 + 60, 4500}, // word sixty gives quantum two
	} {
		if got := ResurrectionDelay(30000, tc.worker); got != tc.want {
			t.Errorf("worker time %d: delay %d, want %d", tc.worker, got, tc.want)
		}
	}
}
