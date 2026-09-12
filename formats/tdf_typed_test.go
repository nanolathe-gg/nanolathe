package formats

import (
	"math"
	"testing"
)

// TestTypedAccessors locks the accessor family's rules [02 §4].
func TestTypedAccessors(t *testing.T) {
	document, err := ParseTDF([]byte("[A] { i=  -12abc; f=2.5xyz; fx=1.5; empty=; junk=zzz; }"))
	if err != nil {
		t.Fatal(err)
	}
	section := document.Root.Section("a")

	if got := section.IntValue("i", 99); got != -12 {
		t.Fatalf("IntValue = %d, want -12 (trailing junk ignored)", got)
	}
	if got := section.IntValue("missing", 99); got != 99 {
		t.Fatalf("absent IntValue = %d, want the caller default", got)
	}
	if got := section.IntValue("junk", 99); got != 0 {
		t.Fatalf("unparsable IntValue = %d, want 0", got)
	}
	if got := section.FloatValue("f", 9); got != 2.5 {
		t.Fatalf("FloatValue = %v, want 2.5", got)
	}
	// The fixed-point default is stored verbatim: it is already 16.16.
	if got := section.FixedValue("fx", 65536); got != 98304 {
		t.Fatalf("FixedValue(1.5) = %d, want 98304", got)
	}
	if got := section.FixedValue("missing", 65536); got != 65536 {
		t.Fatalf("absent FixedValue = %d, want the verbatim default 65536", got)
	}
	// A string field reports authored-empty versus missing independently of
	// its returned text; numeric accessors return only the converted value.
	if value, found := section.StringValue("empty", "fallback"); !found || value != "" {
		t.Fatalf("StringValue(empty) = %q, found=%v; want authored empty", value, found)
	}
	if value, found := section.StringValue("missing", "fallback"); found || value != "fallback" {
		t.Fatalf("StringValue(missing) = %q, found=%v", value, found)
	}
	if !section.BoolValue("i", false) || section.BoolValue("missing", false) {
		t.Fatal("BoolValue must be numeric-nonzero with a caller default")
	}
}

// TestFixedTruncatesTowardZero: the conversion truncates, it does not round.
func TestFixedFromAuthoredTruncates(t *testing.T) {
	if got := FixedFromAuthored(-1.5); got != -98304 {
		t.Fatalf("FixedFromAuthored(-1.5) = %d, want -98304", got)
	}
	if got := FixedFromAuthored(0.0000001); got != 0 {
		t.Fatalf("sub-unit value did not truncate to zero: %d", got)
	}
}

func TestFixedFromAuthoredRetainsSigned64LowWord(t *testing.T) {
	tests := []struct {
		input float64
		want  int32
	}{
		{32768, -2147483648},
		{65536, 0},
		{-32768.5, 2147450880},
		{math.Inf(1), 0},
	}
	for _, test := range tests {
		if got := FixedFromAuthored(test.input); got != test.want {
			t.Errorf("FixedFromAuthored(%v) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestTDFFloatDecimalScannerEdges(t *testing.T) {
	// Established linked-CRT prefix rules, including its alternate exponent
	// letter; these cases avoid unresolved long-mantissa rounding [fmt tdf].
	for _, tc := range []struct {
		text string
		want float64
	}{
		{"\v\f -1.25tail", -1.25}, {"1.25D2", 125}, {"5d-1", 0.5},
		{".5E+1junk", 5}, {"12e", 12}, {"12d-", 12}, {"12E+oops", 12},
		{"12e+-3", 12}, {"12-3", 12}, {"-junk", 0}, {"+.", 0},
		{"Inf", 0}, {"NaN", 0}, {"0x1p2", 0}, {"1.#INF", 1},
		{"1,5", 1},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := parseTDFFloat(tc.text); math.Float64bits(got) != math.Float64bits(tc.want) {
				t.Fatalf("parseTDFFloat(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestTDFFloatRetainsRangeResultAndZeroSign(t *testing.T) {
	// Overflow remains infinity, nonzero underflow retains its sign, and
	// all-zero mantissas remain zero even with a huge exponent [fmt tdf].
	for _, tc := range []struct {
		text string
		want float64
	}{
		{"1e400", math.Inf(1)}, {"-1D400", math.Inf(-1)},
		{"1e99999", math.Inf(1)}, {"-1e99999", math.Inf(-1)},
		{"1e-400", 0}, {"-1e-99999", math.Copysign(0, -1)},
		{"-0", math.Copysign(0, -1)}, {"-0e99999", math.Copysign(0, -1)},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := parseTDFFloat(tc.text); math.Float64bits(got) != math.Float64bits(tc.want) {
				t.Fatalf("parseTDFFloat(%q) = %v, want %v with matching sign", tc.text, got, tc.want)
			}
		})
	}
}
