package movement

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// ForgetUnit drops every piece of per-handle movement state the system holds
// and releases the handle's occupancy contribution. It is the only lifecycle
// cleanup API: death finalization, load reset, and reclassification all route
// through it.
//
// Cleanup used to be open-coded at each call site over the four exported maps
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
	// Release the grid stamp before the collision record that describes it.
	if coll, ok := s.Collisions[h]; ok && coll != nil && s.Grid != nil {
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
	if s.Scheduler != nil {
		s.CancelPathRequest(h)
	}
	// Death/transport teardown may run after the unit is no longer resolvable;
	// release the node-bound payload while its identity is still available.
	if g := s.moveGoals[h]; g != nil {
		s.releaseGoalNode(g.order)
	}
	if st := s.airOrders[h]; st != nil {
		s.releaseGoalNode(st.order)
	}

	delete(s.Collisions, h)
	delete(s.Routes, h)
	delete(s.Steers, h)
	delete(s.Flights, h)
	delete(s.profiles, h)
	delete(s.prevMoveTier, h)
	delete(s.prevSFXBand, h)
	delete(s.tickCarried, h)
	delete(s.pathFailures, h)
	delete(s.activeOrders, h)
	delete(s.arrivalHandles, h)
	delete(s.moveGoals, h)

	// sessions is indexed by handle rather than keyed by it.
	if idx := int(h); idx >= 0 && idx < len(s.sessions) {
		s.sessions[idx] = nil
	}
}
