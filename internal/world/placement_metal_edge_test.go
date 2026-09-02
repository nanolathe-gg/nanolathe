package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestSampleMetalOverTheMapEdge locks the per-cell bounds test of
// [05 R-PROD-01 §6]: the walk resolves each coordinate of the stamped
// rectangle through a bounds-checked lookup, an off-map coordinate contributes
// nothing at all — not even the +1 — and the in-bounds cells of the same
// rectangle still accumulate.
//
// The regression this exists to catch is the one it replaces: rejecting the
// whole sample with an out-of-bounds error whenever any part of the rectangle
// left the map. That made an extractor placed against a map edge store a rate
// of zero — indistinguishable, downstream, from a definition that does not
// extract, because the settlement reads the stored rate and never
// `extractsmetal` [05 R-PROD-01 §1].
//
// The terrain is authored here: a four-by-four flat plot with a uniform
// surface-metal byte, so every covered cell contributes byte+1 = 4 and the
// expected sums are cell counts times four.
func TestSampleMetalOverTheMapEdge(t *testing.T) {
	const surfaceMetal = 3
	const perCell = surfaceMetal + 1

	ter := synth(t, 4, 4, flat(4, 4, 1), nil)
	if err := ter.ApplySchema(&content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: surfaceMetal}}}, 0); err != nil {
		t.Fatalf("ApplySchema: %v", err)
	}

	cases := []struct {
		name           string
		cx, cz         int32
		footX, footZ   int
		coveredCells   int
		whatItExercise string
	}{
		{"wholly inside", 0, 0, 3, 3, 9, "the unchanged case: every cell of the rectangle is on the map"},
		{"across the low corner", -1, -1, 3, 3, 4, "two off-map rows and columns drop out; the 2x2 that remains still sums"},
		{"across the high corner", 3, 3, 3, 3, 1, "only the last cell of the map is covered"},
		{"one column off the left edge", -1, 1, 2, 2, 2, "a partial row: the off-map column contributes nothing, not even its +1"},
		{"wholly off the map", -5, -5, 2, 2, 0, "no cell resolves, so the sum is zero — and it is not an error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rate, sum, err := ter.SampleMetalWithFootprintSum(tc.cx, tc.cz, tc.footX, tc.footZ, 1)
			if err != nil {
				t.Fatalf("%s: sampling a rectangle that leaves the map is not an error [05 R-PROD-01 §6]: %v", tc.whatItExercise, err)
			}
			want := uint16(tc.coveredCells * perCell)
			if sum != want {
				t.Fatalf("%s: footprint accumulator = %d, want %d (%d covered cells x (metal byte %d + 1))",
					tc.whatItExercise, sum, want, tc.coveredCells, surfaceMetal)
			}
			if rate != float32(want) {
				t.Fatalf("%s: rate = %v, want %v at multiplier 1", tc.whatItExercise, rate, float32(want))
			}
		})
	}

	// The multiplier still applies to the partial sum, so an edge extractor
	// yields a real rate rather than the zero the rectangle-level rejection
	// produced.
	rate, _, err := ter.SampleMetalWithFootprintSum(-1, -1, 3, 3, 2)
	if err != nil {
		t.Fatalf("edge sample with a multiplier: %v", err)
	}
	if want := float32(4 * perCell * 2); rate != want {
		t.Fatalf("edge rate at multiplier 2 = %v, want %v", rate, want)
	}

	// An unseeded plot is still refused, and a degenerate footprint is still
	// rejected: relaxing the rectangle bounds did not relax those.
	if _, _, err := ter.SampleMetalWithFootprintSum(0, 0, 0, 3, 1); err == nil {
		t.Fatal("a zero-width footprint was accepted")
	}
	unseeded := synth(t, 4, 4, flat(4, 4, 1), nil)
	if _, _, err := unseeded.SampleMetalWithFootprintSum(0, 0, 3, 3, 1); err == nil {
		t.Fatal("sampling an unseeded metal field was allowed")
	}
}
