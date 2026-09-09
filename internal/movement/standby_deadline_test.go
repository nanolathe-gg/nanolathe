package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// TestVTOLStandbyLoiterHoldsItsDeadline locks the idle circle's cadence: a
// loaded aircraft's loiter point is a fresh bearing and radius about a fixed
// post "redrawn every 30 to 44 ticks" [04 R-AIR-01 §7], not one redrawn every
// time the mover ticks.
//
// The record's deadline is the executor's own wait — phase 2's loaded arm arms
// `tick + 30 + random below 15` and returns to phase 1 — and `VTOL_Standby` is
// registered as externally driven, so the order pump never dispatches it and
// never applies the deadline gate on its behalf. The mover tick's dispatch is
// the only place that wait can be honoured.
//
// It was not, and that was the play-test report of a hovering Atlas jiggling:
// phase 1 (acquire, failing) and phase 2 (three draws, a new marker) ran on
// alternate ticks, so the command bearing was replaced every second tick.
//
// The assertion is the interval between two loiter visits, not a census of
// them: each visit rewrites the deadline, so the gaps between deadline writes
// are the cadence. A gap is 31 to 45 mover ticks — the drawn 30..44 wait, plus
// the one tick phase 1 spends failing its acquisition before phase 2 runs
// again.
func TestVTOLStandbyLoiterHoldsItsDeadline(t *testing.T) {
	sys, _, u := airFixture(t)
	// The loaded, airborne arm of phase 2: `canfly`, committed mover mode 2,
	// and a non-empty cargo list. An unloaded aircraft takes the other arm and
	// spawns `VTOL_LandIfCan` instead [04 R-AIR-01 §7].
	u.Move.Mode = 2
	u.Attachment.Cargo = []pool.Handle{pool.Handle(u.Handle + 1)}

	head := pushAirOrder(t, u, "VTOL_Standby", u.X, u.Z)
	st := sys.airStateFor(u, head)

	var visits []uint32
	last := head.Deadline
	for tick := uint32(1); tick <= 200; tick++ {
		sys.tick = tick
		sys.runAirExecutor(u, head, st)
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
		if gap < 31 || gap > 45 {
			t.Fatalf("loiter points redrawn %d ticks apart (visits %v); [04 R-AIR-01 §7] redraws every 30 to 44 ticks, one tick later here because phase 1 fails its acquisition first", gap, visits)
		}
	}
}
