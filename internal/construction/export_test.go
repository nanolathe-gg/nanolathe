package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// constructionStep is the integer-quantum shape of the work step
// [05 "Construction arithmetic"]. Production always reaches
// wideConstructionStepQuantum with the quantum already in its retail type,
// because the decay wrapper's quantum is not an integer and its infinities
// have to survive the divide [05 R-WORK-01 §1][05 R-WORK-01 §11]; a test that
// pins the research numbers for an ordinary builder passes the whole worker
// quantum, so the conversion lives here rather than in production source.
func constructionStep(old float32, worker int32, buildTime int32, maxDamage int32, energyCost, metalCost float32) (float32, int32, float32, float32) {
	return wideConstructionStepQuantum(old, float32(worker), buildTime, maxDamage, energyCost, metalCost)
}

// snapAnchorCell is the placement anchor a site of this footprint snaps to, as
// the production placement seam computes it: each coordinate biased by half
// its extent before the floor to a cell [05 "Factory production lifecycle"]
// C16 [07 §9]. Tests use it to state the cell a product is expected at.
func snapAnchorCell(t *testing.T, wx, wz numeric.Fixed, footX, footZ int32) world.Cell {
	t.Helper()
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		t.Fatalf("footprint %dx%d: %v", footX, footZ, err)
	}
	anchor, err := world.SnapFootprintAnchor(wx, wz, extent)
	if err != nil {
		t.Fatalf("snap (%d,%d) footprint %dx%d: %v", wx.Raw(), wz.Raw(), footX, footZ, err)
	}
	return anchor.Cell()
}
