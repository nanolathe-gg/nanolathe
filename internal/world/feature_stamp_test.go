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

func TestStampFeatureRectSharesFringeWriter(t *testing.T) {
	terrain := &Terrain{CellW: 6, CellH: 6, Plot: ExpandPlot(flat(6, 6, 1), 6, 6)}
	if err := terrain.StampFeatureRect(1, 1, 0, 2, 2); err != nil {
		t.Fatal(err)
	}
	if got, ok := ResolveFeature(terrain.Plot, 6, 6, 2, 2); !ok || got != 0 {
		t.Fatalf("shared writer fringe resolved to %d/%v", got, ok)
	}
	if err := terrain.StampFeatureRect(0, 0, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	// The unresolved dense-pack anchor guard leaves the first anchor intact.
	if got := terrain.PlotAt(1, 1).Feature(); got != 0 {
		t.Fatalf("overlap replaced live anchor with %#x", got)
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
