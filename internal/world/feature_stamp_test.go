package world

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
)

func TestFeatureStampSourceOrderAndOrphans(t *testing.T) {
	const width, height = int32(8), int32(8)
	attrs := flat(width, height, 5)
	// Two overlapping authored footprints. The second anchor is itself inside
	// the first rectangle; their shared fringe seam must belong to the later
	// source-order stamp, not to a left/above propagation path.
	attrs[1*width+1].Feature = 0
	attrs[2*width+2].Feature = 1
	for z := int32(1); z < 4; z++ {
		for x := int32(1); x < 4; x++ {
			if (x != 1 || z != 1) && (x != 2 || z != 2) {
				attrs[z*width+x].Feature = PlotFeatureFringe
			}
		}
	}
	for z := int32(2); z < 5; z++ {
		for x := int32(2); x < 5; x++ {
			if x != 2 || z != 2 {
				attrs[z*width+x].Feature = PlotFeatureFringe
			}
		}
	}
	// Authored fringe with no covering footprint is discarded after stamping.
	attrs[7*width+7].Feature = PlotFeatureFringe
	terrain := &Terrain{
		CellW: width, CellH: height,
		Plot: ExpandPlot(attrs, int(width), int(height)),
		FeatureDefs: []*content.FeatureDef{
			{FootprintX: 3, FootprintZ: 3},
			{FootprintX: 3, FootprintZ: 3},
		},
	}
	terrain.stampFeatureAnchors()

	if got, ok := ResolveFeature(terrain.Plot, int(width), int(height), 3, 3); !ok || got != 1 {
		t.Fatalf("shared seam resolved to %d/%v, want later feature 1", got, ok)
	}
	if got := terrain.PlotAt(3, 3).AnchorDXSigned(); got != -1 {
		t.Fatalf("shared seam dx = %d, want -1 from later anchor", got)
	}
	if got := terrain.PlotAt(7, 7).Feature(); got != PlotFeatureNone {
		t.Fatalf("orphan fringe = %#x, want empty", got)
	}
}

func TestFeatureStampSignedFieldLimit(t *testing.T) {
	const width, height = int32(131), int32(2)
	attrs := flat(width, height, 1)
	attrs[0].Feature = 0
	for x := int32(1); x < width; x++ {
		attrs[x].Feature = PlotFeatureFringe
	}
	terrain := &Terrain{
		CellW: width, CellH: height,
		Plot:        ExpandPlot(attrs, int(width), int(height)),
		FeatureDefs: []*content.FeatureDef{{FootprintX: width, FootprintZ: 1}},
	}
	terrain.stampFeatureAnchors()
	if got, ok := ResolveFeature(terrain.Plot, int(width), int(height), 128, 0); !ok || got != 0 {
		t.Fatalf("representable -128 delta resolved to %d/%v, want feature 0", got, ok)
	}
	if got := terrain.PlotAt(129, 0).Feature(); got != PlotFeatureNone {
		t.Fatalf("out-of-range signed delta cell = %#x, want empty", got)
	}
}

// TestDensePackReplacesADestructibleFeatureEntirely locks the first branch of
// [05 R-FEAT-01 §3-A]: the covered-cell test is "feature word not 0xFFFF", so a
// fringe cell is torn down like an anchor; the teardown walks the fringe back to
// its anchor, frees it, and clears its WHOLE footprint — the earlier anchor does
// not survive with a truncated fringe. The later stamp then proceeds.
func TestDensePackReplacesADestructibleFeatureEntirely(t *testing.T) {
	defs := []*content.FeatureDef{{FootprintX: 2, FootprintZ: 2}, {FootprintX: 2, FootprintZ: 2}}
	terrain := &Terrain{CellW: 6, CellH: 6, Plot: ExpandPlot(flat(6, 6, 1), 6, 6), FeatureDefs: defs}
	if err := terrain.StampFeatureRect(1, 1, 0, 2, 2); err != nil {
		t.Fatal(err)
	}
	if got, ok := ResolveFeature(terrain.Plot, 6, 6, 2, 2); !ok || got != 0 {
		t.Fatalf("shared writer fringe resolved to %d/%v", got, ok)
	}
	// The new footprint (0,0)-(1,1) covers the earlier ANCHOR at (1,1).
	if err := terrain.StampFeatureRect(0, 0, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	if got := terrain.PlotAt(0, 0).Feature(); got != 1 {
		t.Fatalf("later anchor = %#x, want feature 1", got)
	}
	if got, ok := ResolveFeature(terrain.Plot, 6, 6, 1, 1); !ok || got != 1 {
		t.Fatalf("contested cell resolved to %d/%v, want feature 1", got, ok)
	}
	// Feature 0's whole footprint is gone, including the fringe cells outside
	// the new rectangle.
	for _, c := range [][2]int32{{2, 1}, {1, 2}, {2, 2}} {
		if got := terrain.PlotAt(c[0], c[1]).Feature(); got != PlotFeatureNone {
			t.Fatalf("earlier feature survived at (%d,%d) as %#x, want empty", c[0], c[1], got)
		}
	}
}

// TestDensePackOverAnIndestructibleFeatureVetoesTheStamp locks the second
// branch: the teardown of an indestructible anchor returns 0 and the stamp
// fails at once, leaving the cells torn so far torn — so the new feature's own
// anchor cell is not written. Skipping the contested cell and keeping both
// anchors, which this writer used to do, is neither of retail's outcomes.
func TestDensePackOverAnIndestructibleFeatureVetoesTheStamp(t *testing.T) {
	defs := []*content.FeatureDef{
		{FootprintX: 2, FootprintZ: 2},                       // 0: destructible, torn first
		{FootprintX: 2, FootprintZ: 2, Indestructible: true}, // 1: the veto
		{FootprintX: 4, FootprintZ: 1},                       // 2: the stamp under test
	}
	terrain := &Terrain{CellW: 8, CellH: 8, Plot: ExpandPlot(flat(8, 8, 1), 8, 8), FeatureDefs: defs}
	if err := terrain.StampFeatureRect(0, 0, 0, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := terrain.StampFeatureRect(2, 0, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	// A 4x1 stamp along row 0 tears down feature 0 (cells 0 and 1), then
	// reaches feature 1's anchor at (2,0) and is vetoed.
	if err := terrain.StampFeatureRect(0, 0, 2, 4, 1); err == nil {
		t.Fatal("a footprint covering an indestructible feature was stamped")
	}
	if got := terrain.PlotAt(0, 0).Feature(); got != PlotFeatureNone {
		t.Fatalf("vetoed stamp wrote its own anchor: %#x", got)
	}
	// Torn so far, left torn: feature 0 is gone.
	for _, c := range [][2]int32{{1, 0}, {0, 1}, {1, 1}} {
		if got := terrain.PlotAt(c[0], c[1]).Feature(); got != PlotFeatureNone {
			t.Fatalf("cell (%d,%d) = %#x, want the earlier feature torn", c[0], c[1], got)
		}
	}
	// The indestructible feature is untouched, anchor and fringe alike.
	if got := terrain.PlotAt(2, 0).Feature(); got != 1 {
		t.Fatalf("indestructible anchor = %#x, want feature 1", got)
	}
	if got, ok := ResolveFeature(terrain.Plot, 8, 8, 3, 1); !ok || got != 1 {
		t.Fatalf("indestructible fringe resolved to %d/%v, want feature 1", got, ok)
	}
	// A fringe cell of an indestructible feature vetoes just as its anchor does.
	if err := terrain.StampFeatureRect(3, 1, 2, 1, 1); err == nil {
		t.Fatal("a footprint covering only an indestructible FRINGE cell was stamped")
	}
}

func TestFeatureStampEmptySourceDoesNotCreateFringe(t *testing.T) {
	attrs := []formats.TNTAttribute{{Height: 1, Feature: 0}, {Height: 1, Feature: PlotFeatureNone}, {Height: 1, Feature: PlotFeatureNone}, {Height: 1, Feature: PlotFeatureNone}}
	terrain := &Terrain{CellW: 2, CellH: 2, Plot: ExpandPlot(attrs, 2, 2), FeatureDefs: []*content.FeatureDef{{FootprintX: 2, FootprintZ: 2}}}
	terrain.stampFeatureAnchors()
	if got := terrain.PlotAt(1, 0).Feature(); got != PlotFeatureNone {
		t.Fatalf("empty source was synthesized as %#x", got)
	}
}
