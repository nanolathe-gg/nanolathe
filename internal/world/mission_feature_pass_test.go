package world

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// The mission file's `[features]` pass runs BEFORE the edge/lava void sweep
// [02 R-MAP-01 §6]. Two things follow, and this locks both:
//
//   - a footprint reaching a cell the sweep would convert still stamps, where
//     a pass run after the sweep is vetoed by the dense-pack teardown and the
//     whole feature is lost [05 R-FEAT-01 §3 step 3];
//   - the cells of that footprint which lie in a strip still end VOID, because
//     the sweep converts fringe as well as empty [03 R-TERR-01 §2].
func TestMissionFeaturePassRunsBeforeTheVoidSweep(t *testing.T) {
	const w, h = 12, 12
	def := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wall"},
		FootprintX:       2,
		FootprintZ:       2,
		Blocking:         true,
	}
	ter := synth(t, w, h, flat(w, h, 10), []*content.FeatureDef{def})
	ter.applyVoidFixup(nil)

	// On this flat map the south walk voids rows 4..10, so a 2x2 footprint
	// anchored at (5,3) covers one ordinary row and one voided row.
	if got := ter.PlotAt(5, 4).Feature(); got != PlotFeatureVoid {
		t.Fatalf("fixture precondition: cell (5,4) = %#x, want the swept void %#x", got, PlotFeatureVoid)
	}
	if got := ter.PlotAt(5, 3).Feature(); got != PlotFeatureNone {
		t.Fatalf("fixture precondition: cell (5,3) = %#x, want empty %#x", got, PlotFeatureNone)
	}

	// The same stamp attempted against the swept plot is refused outright:
	// that is the ordering defect, stated as the fixture's own control.
	if err := ter.StampFeatureRect(5, 3, 0, 2, 2); err == nil {
		t.Fatal("control: a footprint over a swept cell must be refused when the pass runs after the sweep")
	}

	var stamped error
	ter.RunMissionFeaturePass(func() {
		stamped = ter.StampFeatureRect(5, 3, 0, 2, 2)
	})
	if stamped != nil {
		t.Fatalf("mission feature pass: %v", stamped)
	}
	if got := ter.PlotAt(5, 3).Feature(); got != 0 {
		t.Fatalf("anchor (5,3) = %#x, want the live index 0", got)
	}
	if got := ter.PlotAt(6, 3).Feature(); got != PlotFeatureFringe {
		t.Fatalf("footprint cell (6,3) = %#x, want fringe %#x", got, PlotFeatureFringe)
	}
	// The two footprint cells inside the south strip are converted by the
	// replayed sweep, exactly as the loader's own order converts them.
	for _, cx := range []int32{5, 6} {
		if got := ter.PlotAt(cx, 4).Feature(); got != PlotFeatureVoid {
			t.Fatalf("footprint cell (%d,4) = %#x, want the sweep's void %#x", cx, got, PlotFeatureVoid)
		}
	}
	// Every other cell the load-time sweep converted is still void: the undo
	// is undone again, not left open.
	for _, cell := range []struct{ cx, cz int32 }{{11, 5}, {10, 5}, {5, 0}, {5, 10}} {
		if got := ter.PlotAt(cell.cx, cell.cz).Feature(); got != PlotFeatureVoid {
			t.Fatalf("swept cell (%d,%d) = %#x, want %#x after the replay", cell.cx, cell.cz, got, PlotFeatureVoid)
		}
	}
	// ... and a cell no rule reaches is untouched by either half.
	if got := ter.PlotAt(2, 2).Feature(); got != PlotFeatureNone {
		t.Fatalf("interior cell (2,2) = %#x, want empty %#x", got, PlotFeatureNone)
	}
}

// A terrain the sweep never ran on gets no sweep invented for it: the pass
// runs the stamps and nothing else. Authored fixtures and hand-built plots
// depend on that.
func TestMissionFeaturePassLeavesAnUnsweptTerrainAlone(t *testing.T) {
	const w, h = 12, 12
	def := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wall"},
		FootprintX:       1,
		FootprintZ:       1,
	}
	ter := synth(t, w, h, flat(w, h, 10), []*content.FeatureDef{def})
	ran := false
	ter.RunMissionFeaturePass(func() {
		ran = true
		if err := ter.StampFeatureRect(5, 5, 0, 1, 1); err != nil {
			t.Fatalf("stamp: %v", err)
		}
	})
	if !ran {
		t.Fatal("the stamps must run whether or not the sweep has")
	}
	if got := ter.PlotAt(5, 5).Feature(); got != 0 {
		t.Fatalf("anchor (5,5) = %#x, want the live index 0", got)
	}
	for _, cell := range []struct{ cx, cz int32 }{{11, 5}, {5, 0}, {5, 10}} {
		if got := ter.PlotAt(cell.cx, cell.cz).Feature(); got != PlotFeatureNone {
			t.Fatalf("cell (%d,%d) = %#x on an unswept terrain, want empty %#x", cell.cx, cell.cz, got, PlotFeatureNone)
		}
	}
}
