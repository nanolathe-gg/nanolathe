package movement

import "github.com/nanolathe/nanolathe/internal/pool"

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
		s.Grid.Clear(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, int(h))
	}
	// Cancels the scheduler request and drops the active order binding.
	s.DeactivateMove(h)
	if s.Scheduler != nil {
		s.CancelPathRequest(h)
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
