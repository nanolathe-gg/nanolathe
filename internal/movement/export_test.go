package movement

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Test-only seams. Each of these was reachable from tests alone: production
// drives the commit sweep from the mover tick, reads the carried link off the
// unit it already holds, and computes the arrival threshold and the speed cap
// inside the follower. Keeping them here says so.

// commitSweep commits a slice of movers synchronously in deterministic slot
// order [04 §8.2] C22 I1. It sorts states by ID ascending (pool slot asc) and
// runs CommitOne for each, so claim-first blocks later movers, vacated cells
// are reusable in the same sweep, and head-on swaps block [04 §8.2] C22. One
// unit's clear/commit/stamp finishes before the next slot [04 §8.2] C22.
//
// perCellFactory and aggregateFactory are injected per-mover predicates, so a
// test can supply the occupancy validator the mover tick supplies in
// production.
func commitSweep(states []*CollisionState, grid *OccupancyGrid, perCellFactory func(*CollisionState) func(Cell) bool, aggregateFactory func(*CollisionState) func() bool) {
	if len(states) == 0 {
		return
	}
	// deterministic iteration: slot ascending [I1][01 §6.2]
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	for _, s := range states {
		if s == nil {
			continue
		}
		var perCell func(Cell) bool
		var aggregate func() bool
		if perCellFactory != nil {
			perCell = perCellFactory(s)
		}
		if aggregateFactory != nil {
			aggregate = aggregateFactory(s)
		}
		// proposedMode is current mode unless caller injects variation; use s.Mode
		s.CommitOne(grid, s.Mode, perCell, aggregate)
	}
}

// clampSpeed returns the capped speed without mutating state, so a test can
// check the asymmetric table and the halving without constructing a full
// SteerState tick [04 §8.1] C20 C21.
func clampSpeed(target int32, pitchDelta int32, maxVelocity int32, heightWord int16, seaLevel uint8, defFlags uint32) int32 {
	s := &SteerState{
		MaxVelocity: maxVelocity,
		HeightWord:  heightWord,
		SeaLevel:    seaLevel,
		DefFlags:    defFlags,
	}
	cap := s.SpeedCap(pitchDelta)
	if target < 0 {
		target = 0 // no reverse [04 §8.1] C20
	}
	if target > cap {
		target = cap
	}
	return target
}

// cargoCount returns the live carried count filtered by parent == carrier
// [04 §10.2]. Production reads the cargo list it is already holding.
func cargoCount(w *units.World, carrierHandle pool.Handle) int {
	if w == nil {
		return 0
	}
	carrier := w.Unit(carrierHandle)
	if carrier == nil {
		return 0
	}
	n := 0
	for _, h := range carrier.Attachment.Cargo {
		u := w.Unit(h)
		if u != nil && u.Attachment.Carrier == carrierHandle {
			n++
		}
	}
	return n
}

// isCarried reports whether a unit is currently carried [04 §10.2].
func isCarried(w *units.World, h pool.Handle) bool {
	if w == nil {
		return false
	}
	u := w.Unit(h)
	return u != nil && u.Attachment.Carrier != 0
}

// pending reports how many path requests a player's staging list holds.
func (p *pathProvider) pending(player uint8) int { return len(p.requests[player]) }
