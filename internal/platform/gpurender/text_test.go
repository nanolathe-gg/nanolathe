package gpurender

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/client"
)

func TestFNTLayoutMatchesSoftwareAfterFirstCodeTableBias(t *testing.T) {
	f := &formats.FNT{Height: 1, Baseline: -2, FirstCode: 32, Glyphs: [256]*formats.FNTGlyph{
		'A': {Width: 2, Height: 1, Bits: []byte{0xc0}},
	}}
	if got := baselineDescender(f); got != -2 {
		t.Fatalf("baseline = %d, want -2", got)
	}
	if code, ok := resolveGlyph(f, 'A'); !ok || code != 'A' {
		t.Fatalf("A resolves as %d, %v", code, ok)
	}
	if _, ok := resolveGlyph(f, 31); ok {
		t.Fatal("code below the first table entry resolved")
	}
	if got, want := measureText(f, " A"), client.MeasureText(f, " A"); got != want {
		t.Fatalf("GPU measure = %d, software = %d", got, want)
	}
	if got, want := truncateToWidth(f, "AAA", 4), client.TruncateToWidth(f, "AAA", 4); got != want {
		t.Fatalf("GPU truncate = %q, software = %q", got, want)
	}
}
