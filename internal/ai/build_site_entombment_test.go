package ai_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestComputerCommanderIsNeverBuiltOver is the PT5 liveness lock, and it is a
// live-asset test: it is skipped when the reference install is absent.
//
// Reproduction it locks: on Great Divide the computer commander queued a solar
// collector on the cells it was standing on, the nanoframe was stamped there,
// and the finished structure's occupancy enclosed the commander so its path
// search could no longer leave. The slot then stopped developing — five live
// units at tick 9000 against nineteen on a seed that did not hit the defect.
//
// Retail cannot reach that state: the footprint validator rejects "any nonzero
// occupant other than the passed self identity"
// [05 "control-byte bit roles in the footprint validator"][04 R-COLL-01 §2],
// every placement caller passes a null self identity [04 R-COLL-01 §6], and
// ground movers write the same occupancy word buildings write
// [04 R-COLL-01 §4]. The order takes the blocked-area budget of
// [R-ORDER-02 §1] instead, and the builder walks off the rectangle it was
// given as a goal [04 §7.2][R-ORD-01 §5].
//
// The assertion is deliberately narrow: a COMMANDER is never a factory
// product, so a structure holding a commander's cells is always the defect and
// never the legitimate product-on-the-pad overlap of [04 R-COLL-01 §3].
func TestComputerCommanderIsNeverBuiltOver(t *testing.T) {
	const seed = 2 // the seed that reproduced the defect
	sess := liveSkirmish(t, "Great Divide", seed)
	if sess.World == nil {
		t.Skip("nanolathe: composed session carries no terrain")
	}
	computer := -1
	for player, mgr := range sess.AI {
		if mgr != nil && player != int(sess.LocalOwner) {
			computer = player
			break
		}
	}
	if computer < 0 {
		t.Skip("nanolathe: composed skirmish has no computer slot")
	}
	advanceSession(sess, 6000)

	live := 0
	for _, u := range sess.Units.IterSliced() {
		if u == nil || !u.Alive || int(u.Owner) != computer {
			continue
		}
		live++
		if u.Def == nil || !u.Def.CanCapture || u.Flags&units.BuildingClassStatus != 0 {
			continue
		}
		fx, fz := world.FootprintForUnit(sess.Catalog, u.Def)
		extent, err := world.NewFootprintExtent(fx, fz)
		if err != nil {
			continue
		}
		anchor, err := world.SnapFootprintAnchor(u.X, u.Z, extent)
		if err != nil {
			continue
		}
		cell := anchor.Cell()
		for dz := int32(0); dz < fz; dz++ {
			for dx := int32(0); dx < fx; dx++ {
				plot := sess.World.PlotAt(cell.X+dx, cell.Z+dz)
				if plot == nil {
					continue
				}
				if occ := plot.OccupantA(); occ != 0 && uint64(occ) != uint64(u.Handle) {
					t.Fatalf("commander %d of slot %d stands on cell %d,%d held by unit %d: a structure was placed over a mobile builder [04 R-COLL-01 §2]",
						u.Handle, computer, cell.X+dx, cell.Z+dz, occ)
				}
			}
		}
	}
	// The stalled run reached five units by tick 9000; a slot that is still
	// developing is well past that by tick 6000.
	if live <= 5 {
		t.Fatalf("computer slot %d holds %d live units at tick 6000: the slot stopped developing", computer, live)
	}
}
