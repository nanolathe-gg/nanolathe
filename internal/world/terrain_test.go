package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// synth builds a Terrain directly from attribute cells, bypassing the VFS.
func synth(t *testing.T, cellW, cellH int32, attrs []formats.TNTAttribute, defs []*content.FeatureDef) *Terrain {
	t.Helper()
	ter := &Terrain{
		CellW:       cellW,
		CellH:       cellH,
		Version:     VersionCanonical,
		Plot:        ExpandPlot(attrs, int(cellW), int(cellH)),
		FeatureDefs: defs,
	}
	ter.stampFeatureAnchors()
	return ter
}

func flat(cellW, cellH int32, height uint8) []formats.TNTAttribute {
	attrs := make([]formats.TNTAttribute, cellW*cellH)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: height, Feature: PlotFeatureNone}
	}
	return attrs
}

// TestFloorPairIsDerived is the core of R6: CoarseHeightAt averages the derived
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// both bytes zero, so the query returned 0 for every cell of every map.
func TestFloorPairIsDerived(t *testing.T) {
	attrs := flat(4, 4, 10)
	attrs[1*4+1].Height = 30 // one peak, at cell (1,1)
	ter := synth(t, 4, 4, attrs, nil)

	// Cell (0,0) spans corners (0,0),(1,0),(0,1),(1,1) so it sees the peak.
	if lo, hi := ter.PlotAt(0, 0).MinHeight(), ter.PlotAt(0, 0).MaxHeight(); lo != 10 || hi != 30 {
		t.Fatalf("cell (0,0) floor pair = %d/%d, want 10/30", lo, hi)
	}
	if got := ter.CoarseHeightAt(0, 0); got != 20*65536 {
		t.Fatalf("CoarseHeightAt(0,0) = %d, want %d", got, 20*65536)
	}
	// A cell far from the peak keeps a flat pair.
	if got := ter.CoarseHeightAt(3, 3); got != 10*65536 {
		t.Fatalf("CoarseHeightAt(3,3) = %d, want %d", got, 10*65536)
	}
	// The whole grid must not be zero, which is what the bug looked like.
	if ter.CoarseHeightAt(2, 2) == 0 {
		t.Fatal("coarse height is still zero — the floor pair was not derived")
	}
}

// TestFringeAnchorsResolve is the other half of R6. A multi-cell feature stores
// its index in the anchor and fills covered cells with the fringe sentinel
// [fmt tnt]; consumers reach the index only through the anchor offsets
// [04 §6.2]. Unstamped, every fringe cell resolved to nothing.
func TestFringeAnchorsResolve(t *testing.T) {
	attrs := flat(4, 4, 5)
	// A 3x3 feature (record 0) anchored at cell (1,1) — the shape [fmt tnt]
	// documents for ArchMetal1 on Coast To Coast.
	attrs[1*4+1].Feature = 0
	for dz := 0; dz < 3; dz++ {
		for dx := 0; dx < 3; dx++ {
			if dx == 0 && dz == 0 {
				continue
			}
			attrs[(1+dz)*4+(1+dx)].Feature = PlotFeatureFringe
		}
	}
	defs := []*content.FeatureDef{{FootprintX: 3, FootprintZ: 3}}
	ter := synth(t, 4, 4, attrs, defs)

	for dz := 0; dz < 3; dz++ {
		for dx := 0; dx < 3; dx++ {
			cx, cz := 1+dx, 1+dz
			got, ok := ResolveFeature(ter.Plot, 4, 4, cx, cz)
			if !ok || got != 0 {
				t.Fatalf("cell (%d,%d) resolved to %d/%v, want feature 0", cx, cz, got, ok)
			}
		}
	}
	// An empty cell still resolves to nothing.
	if _, ok := ResolveFeature(ter.Plot, 4, 4, 0, 0); ok {
		t.Fatal("empty cell resolved to a feature")
	}
}

// TestUnboundFeatureNameIsNotAnIndex locks R9's out-of-range rule: a map naming
// a feature the catalog does not have behaves as out-of-range, not as a load
// failure and not as a valid index [04 §6.2].
func TestUnboundFeatureNameIsNotAnIndex(t *testing.T) {
	ter := synth(t, 2, 2, flat(2, 2, 1), []*content.FeatureDef{nil})
	if _, ok := ter.FeatureDefAt(0); ok {
		t.Fatal("an unbound record resolved to a definition")
	}
	if _, ok := ter.FeatureDefAt(PlotFeatureFringe); ok {
		t.Fatal("a sentinel resolved to a definition")
	}
	if _, ok := ter.FeatureDefAt(99); ok {
		t.Fatal("an out-of-range index resolved to a definition")
	}
}

// TestSurfaceMetalSeeding locks R6's metal half. Before ApplySchema the field is
// unseeded and sampling must refuse rather than quietly return the footprint
// count; after it, the sum is extractsMetal * sum(cellMetal+1)
// [05 "Terrain metal extraction"].
func TestSurfaceMetalSeeding(t *testing.T) {
	ter := synth(t, 4, 4, flat(4, 4, 1), nil)
	if _, err := ter.SampleMetal(0, 0, 3, 3, 1); err == nil {
		t.Fatal("sampling an unseeded metal field was allowed")
	}

	mh := &content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: 3}}}
	if err := ter.ApplySchema(mh, 0); err != nil {
		t.Fatal(err)
	}
	got, err := ter.SampleMetal(0, 0, 3, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Nine cells, each contributing metal+1 = 4.
	if got != 36 {
		t.Fatalf("sampled metal = %v, want 36 (9 cells x (3+1))", got)
	}
	// A zero-metal cell still contributes one [05 "Terrain metal extraction"].
	if err := ter.ApplySchema(&content.MapHeader{Schemas: []content.MapSchema{{SurfaceMetal: 0}}}, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ = ter.SampleMetal(0, 0, 3, 3, 1); got != 9 {
		t.Fatalf("zero-metal sample = %v, want 9", got)
	}
}

// TestLOSHeightAggregates locks R8: the LOS query aggregates over the 2x2 cells
// a 32-pixel visibility tile covers rather than sampling the tile origin
// [03 §2.3], [03 §2.1].
func TestLOSHeightAggregates(t *testing.T) {
	attrs := flat(4, 4, 10)
	attrs[1*4+1].Height = 90 // a ridge inside tile (0,0), not at its origin
	ter := synth(t, 4, 4, attrs, nil)

	if got := ter.LOSHeightAt(0, 0); got != 90 {
		t.Fatalf("LOSHeightAt(0,0) = %d, want 90 (a ridge off the tile origin must still occlude)", got)
	}
	if got := ter.LOSHeightAt(1, 1); got != 10 {
		t.Fatalf("LOSHeightAt(1,1) = %d, want 10", got)
	}
	// It is a separate query from the bilinear one and must not be substituted.
	if numeric := ter.HeightAt(0, 0); numeric == 90*65536 {
		t.Fatal("HeightAt and LOSHeightAt must not agree on a ridge corner")
	}
}

// TestHeightAtBilinear locks C7: integer bilinear over the four neighbouring
// cell heights with the signed right-shift bias, never a float lerp [03 §2.3].
func TestHeightAtBilinear(t *testing.T) {
	attrs := flat(2, 2, 0)
	attrs[1].Height = 16 // cell (1,0)
	ter := synth(t, 2, 2, attrs, nil)

	// Halfway along x inside cell (0,0): 8 map pixels into a 16-pixel cell.
	half := numeric.Fixed(8 * 65536)
	if got := ter.HeightAt(half, 0); got != 8*65536 {
		t.Fatalf("HeightAt(half cell) = %d, want %d", got, 8*65536)
	}
}
