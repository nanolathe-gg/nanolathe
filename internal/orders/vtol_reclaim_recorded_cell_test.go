package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestVTOLReclaimPayoutTakesTheRecordedPosition is the air twin of
// TestFeatureReclaimPayoutTakesTheRecordedPosition. `VTOL_Reclaim` resolves the
// feature at its goal on every visit and used to hand that resolved ANCHOR to
// the shared payout, where the ground row hands the position the ORDER
// recorded. Retail's two handlers both hold a pointer to the record's own
// position from their entry and pass THAT to the one payout helper, which
// resolves it twice and reads its guard's instance-attached bit from the first,
// unhopped cell [05 R-FEAT-01 §15][05 R-WORK-01 §5][04 R-ORD-01 §7].
//
// The two cells only differ for a multi-cell feature, so the fixture stamps a
// two-by-two sprite tree and raises the anchor's bit — a burning or already
// animating feature. Reclaimed from the anchor the guard refuses and nothing is
// credited; reclaimed from a fringe cell it reads that cell's clear bit and the
// whole pool is paid.
func TestVTOLReclaimPayoutTakesTheRecordedPosition(t *testing.T) {
	cases := []struct {
		name         string
		goalX, goalZ numeric.Fixed
		want         float32
	}{
		// Cell (4,5) is the anchor; cell (5,5) is a fringe member of the same
		// two-by-two footprint.
		{"recorded position is the anchor cell", numeric.Fixed(70 << 16), numeric.Fixed(90 << 16), 0},
		{"recorded position is a fringe cell", numeric.Fixed(86 << 16), numeric.Fixed(90 << 16), 250},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, builder, _ := vtolWorkFixture()
			tree, _ := retailShapedTree()
			tree.FootprintX, tree.FootprintZ = 2, 2
			tree.Filename = "trees" // the guard's definition bit: a sprite source
			econ := &economy.Service{Terrain: reclaimFixtureTerrain([]*content.FeatureDef{tree}, 4, 5)}
			q.binding.Economy = econ
			if err := econ.Terrain.StampFeatureRect(4, 5, 0, 2, 2); err != nil {
				t.Fatalf("stamping the two-by-two footprint: %v", err)
			}
			// The live animation instance the guard's cell bit stands for.
			econ.Terrain.PlotAt(4, 5).SetOccupied(true)

			q.Push(Lookup("VTOL_Reclaim"), Node{Owner: builder.Handle, GoalSupplied: true,
				GoalX: c.goalX, GoalY: numeric.Fixed(40 << 16), GoalZ: c.goalZ})
			if ticks := pumpUntilEmpty(t, q, builder, 4000); q.LenPrimary() != 0 {
				t.Fatalf("the air reclaim never completed in %d ticks", ticks)
			}
			if got := econ.UnitBuckets(builder.Handle)[economy.Energy].Production; got != c.want {
				t.Fatalf("energy credited = %v, want %v [05 R-FEAT-01 §15]", got, c.want)
			}
		})
	}
}
