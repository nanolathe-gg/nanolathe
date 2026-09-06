package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
)

// TestStockpileToyCountLabel locks the bit-0x08 format of the count-label
// writer [07 R-P0-11 §2]: "%d" of the held byte published on the command page,
// then " +%d" of the pending BUILDWEAPON total from the page unit's secondary
// queue, each half cleared when its own quantity is zero.
func TestStockpileToyCountLabel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		held    int32
		pending uint32
		want    string
	}{
		{"nothing held or pending", 0, 0, ""},
		{"one round on the way", 0, 1, "+1"},
		{"one held", 1, 0, "1"},
		{"held and pending are two quantities", 2, 3, "2 +3"},
	} {
		f := &frame.Frame{}
		f.CommandPage = frame.CommandPageView{Builder: 1, Stockpile: tc.held}
		if tc.pending != 0 {
			f.OrderQueues = []frame.OrderQueueView{{
				Unit:      1,
				Secondary: []frame.OrderView{{Kind: "BuildWeapon", BuildCount: tc.pending}},
			}}
		}
		if got := stockpileCountLabel(f); got != tc.want {
			t.Fatalf("%s: label = %q, want %q [07 R-P0-11 §2]", tc.name, got, tc.want)
		}
	}
}
