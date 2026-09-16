package main

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestHUDNumberFormatsThroughTheTruncatingHelper locks the resource readouts to
// the float→int census of [01 R-DET-01 §1]: every game conversion runs the
// signed 64-bit truncating helper and keeps its LOW 32 bits, so it truncates
// toward zero for both signs and a magnitude past 2³¹ wraps rather than
// saturating. The readouts used to format `int(value)` directly; on a 64-bit
// host that is a 64-bit conversion with no low-word narrowing, and Go leaves it
// undefined once the value leaves the destination's range.
func TestHUDNumberFormatsThroughTheTruncatingHelper(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   float32
		want string
	}{
		{"a fractional stock truncates toward zero", 1234.87, "1234"},
		{"a negative stock truncates toward zero, not downward", -2.7, "-2"},
		{"a magnitude past 2³¹ wraps through the low word", 3e9, "-1294967296"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatHUDNumber(tc.in); got != tc.want {
				t.Fatalf("formatHUDNumber(%v) = %q, want %q [01 R-DET-01 §1]", tc.in, got, tc.want)
			}
			// The same value read through the shared helper: the readout is the
			// census conversion itself, not an independent restatement of it.
			if want := fmt.Sprintf("%d", numeric.TruncateFloat32ToLow32(tc.in)); formatHUDNumber(tc.in) != want {
				t.Fatalf("formatHUDNumber(%v) does not match the truncating helper's low word [01 R-DET-01 §1]", tc.in)
			}
		})
	}
}
