package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// TestVTOLStandbyLoiterHoldsItsDeadline locks the idle circle's cadence: a
// loaded aircraft's loiter point is a fresh bearing and radius about a fixed
// post "redrawn every 30 to 44 ticks" [04 R-AIR-01 §7], not one redrawn every
// time the mover ticks.
//
// The ordinary pump applies the deadline and advances phase 1 into the
// phase-2 loiter in the same visit [04 §3.3][04 R-AIR-01 §7]. Compare successive
// loiter visits rather than a fixed census of random outcomes.
func TestVTOLStandbyLoiterHoldsItsDeadline(t *testing.T) {
	sys, _, u := airFixture(t)
	// The loaded, airborne arm of phase 2: `canfly`, committed mover mode 2,
	// and a non-empty cargo list. An unloaded aircraft takes the other arm and
	// spawns `VTOL_LandIfCan` instead [04 R-AIR-01 §7].
	u.Move.Mode = 2
	u.Attachment.Cargo = []pool.Handle{pool.Handle(u.Handle + 1)}

	head := pushAirOrder(t, u, "VTOL_Standby", u.X, u.Z)
	sys.BindAirOrderLegs()

	var visits []uint32
	last := head.Deadline
	for tick := uint32(1); tick <= 200; tick++ {
		orders.QueueForUnit(u).Pump(u, tick)
		if head.Deadline != last {
			last = head.Deadline
			visits = append(visits, tick)
		}
	}
	// Phase 0 writes a deadline of its own (`tick + 1`), so the first entry is
	// the entry visit and the loiter visits are the ones after it.
	if len(visits) < 4 {
		t.Fatalf("the standby record was revisited %d times in 200 ticks (%v); the loiter must keep cycling [04 R-AIR-01 §7]", len(visits), visits)
	}
	for i := 2; i < len(visits); i++ {
		gap := visits[i] - visits[i-1]
		if gap < 30 || gap > 44 {
			t.Fatalf("loiter points redrawn %d ticks apart (visits %v); [04 R-AIR-01 §7] redraws every 30 to 44 ticks", gap, visits)
		}
	}
}
