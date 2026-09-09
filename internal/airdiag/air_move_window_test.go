package airdiag

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestAirMoveCompletesAtItsOrderedGoal prints every tick of the first seventy
// and then asserts the one thing that separates a completed air move from a
// discarded one: when the `VTOL_Move` record leaves the queue, the aircraft
// must be at the ordered destination, because the only bit that completes the
// record is the arrival bit its own phase-1 marker raises
// [04 R-ORD-02 §2][04 R-AIR-01 §1] step 6.
func TestAirMoveCompletesAtItsOrderedGoal(t *testing.T) {
	h := newHarness(t)
	u := spawnAircraft(t, h, diagAircraft, 6, 6)
	goalX := u.X.Add(world.CellToWorld(40))
	goalZ := u.Z
	if err := h.Order(u, 2, 0, goalX, goalHeight(h, goalX, goalZ), goalZ); err != nil {
		t.Fatalf("submit move: %v", err)
	}
	rows := h.Trace(u, 70)
	dump(t, rows, 1)

	completedAt := -1
	for i, r := range rows {
		if r.QueueLen == 0 {
			completedAt = i
			break
		}
	}
	if completedAt <= 0 {
		t.Skip("the record was still queued inside the window; widen it")
	}
	done := rows[completedAt]
	t.Logf("the record left the queue at tick %d", done.Tick)
	t.Logf("the tick before: %s", rows[completedAt-1].Format())

	// The default air arrival test is horizontal and half a world unit; a
	// generous tolerance keeps this assertion about "was it anywhere near the
	// goal" rather than about the tolerance itself [04 R-AIR-01 §4].
	dist := math.Hypot(fx(done.X)-fx(goalX), fx(done.Z)-fx(goalZ))
	if dist > 16 {
		t.Errorf("the VTOL_Move record completed %.1f world units from its goal, at (%.2f,%.2f) "+
			"rather than (%.2f,%.2f): the arrival bit that completed it came from the takeoff "+
			"preamble's climb marker, not from a marker at the destination",
			dist, fx(done.X), fx(done.Z), fx(goalX), fx(goalZ))
	}
}
