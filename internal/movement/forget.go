package movement

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// ForgetUnit drops every piece of per-handle movement state the system holds
// and releases the handle's occupancy contribution. It is the only lifecycle
// cleanup API: death finalization, load reset, and reclassification all route
// through it.
//
// Cleanup used to be open-coded at each call site over the four exported rows
// — routes, steers, flights and collisions — and the two sites had drifted
// apart: one cancelled the scheduler and one did not, one deleted routes
// twice, and neither cleared the resolved profile or any of the cached
// per-handle bands. Pool slots are reused by handle, so a new unit landing on
// a dead unit's slot inherited that unit's footprint, slope profile, mover
// tier, sound band, avoidance cadence and arrival binding, which is how stale
// pathing and placement failures outlive the unit that caused them
// [04 §8.2] C22.
//
// Occupancy is cleared from the collision state's cached anchor, which is the
// footprint the grid was actually stamped with — recomputing it from the
// unit's current position would subtract a different rectangle than was added.
func (s *System) ForgetUnit(h pool.Handle) {
	if s == nil {
		return
	}
	for i := len(s.repairLandings) - 1; i >= 0; i-- {
		if s.repairLandings[i].unit.Handle == h {
			s.discardRepairLanding(s.repairLandings[i].node)
		}
	}
	// Release the grid stamp before the collision record that describes it.
	if coll := handleRow(s.Collisions, h); coll != nil && s.Grid != nil {
		if coll.Building && len(coll.Yard) == int(coll.FootPrintX)*int(coll.FootPrintZ) {
			s.clearBuildingGrid(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, coll.Yard, coll.YardOpen, int(h))
		} else {
			s.Grid.Clear(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, int(h))
		}
		// Unit finalisation at death or free is one of the footprint clear's
		// callers, so it owes the clear's class-layer maintenance
		// [04 R-COLL-01 §4]. Without it the layers keep the blocked value the
		// occupant-age gate wrote for this unit: the occupant word is gone, the
		// collision record is about to be, and the revision pass walks live
		// units only, so the cells a unit died on would hard-block for the rest
		// of the battle exactly as a building's do [04 R-PATH-01 §14].
		s.noteFootprintClear(h, coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, !coll.Building)
	}
	// Cancels the scheduler request and drops the active order binding.
	s.DeactivateMove(h)
	s.clearJamRelease(h)
	if s.Scheduler != nil {
		s.CancelPathRequest(h)
	}
	// Release retained objects even when their controller binding has already
	// arrived or been displaced [04 R-ORD-01 §9].
	s.forgetRecordGoals(h)

	// Unit finalisation unlinks, so a reused pool slot never inherits a dead
	// unit's place in a sector bucket [04 R-COLL-01 §11] item 1.
	s.Grid.ForgetFiling(int(h))
	// The same inheritance rule for the occupant-age clock: retail frees the
	// MOVER with the unit and the clock is a word on it [04 R-PATH-01 §14], so
	// the next unit in this slot reads a zero-initialized tick. It must run
	// AFTER noteFootprintClear above, whose watermark gate compares the tick
	// the dying unit held while it stood on the rectangle it is releasing.
	s.forgetOccupancyCommit(h)
	setHandleRow(&s.Collisions, h, nil)
	setHandleRow(&s.Routes, h, nil)
	setHandleRow(&s.Steers, h, nil)
	setHandleRow(&s.Flights, h, nil)
	setHandleRow(&s.profiles, h, nil)
	setHandleRow(&s.prevMoveTier, h, 0)
	setHandleRow(&s.prevSFXBand, h, 0)
	setHandleRow(&s.pathFailures, h, nil)
	setHandleRow(&s.activeOrders, h, nil)
	setHandleRow(&s.clearanceRoutes, h, nil)
	setHandleRow(&s.arrivalHandles, h, nil)
	setHandleRow(&s.moveGoals, h, nil)

	// sessions is indexed by handle rather than keyed by it.
	s.dropPathSession(int(h))
}
