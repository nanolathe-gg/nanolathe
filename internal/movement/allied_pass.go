package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy (DESIGN_MOVEMENT_PATH "Modern allied
// pass-through"): two ground movers of the same or mutually allied owners
// that meet heading against each other, both mid-route, may pass through each
// other's footprints instead of deadlocking head-on. Retail rejects every
// occupied footprint cell [04 R-COLL-01 §1][04 R-COLL-01 §2].

// alliedPassMinTurn is the least heading difference, about 120° on the
// sixteen-bit circle, at which two movers count as meeting head-on. Modern
// policy tuning.
const alliedPassMinTurn = 0x5555

// alliedPassEndClear is the distance in world units from its route's final
// point inside which neither unit passes, so an overlap is never carried into
// a crowded destination. Modern policy tuning.
const alliedPassEndClear = 64

// routeEndClear reports whether the route's final point is farther than
// alliedPassEndClear from (x, z), both in whole world units.
func routeEndClear(r *Route, x, z int32) bool {
	if r == nil || !r.Active || r.Count < 2 {
		return false
	}
	end := r.Points[r.Count-1]
	dx, dz := int64(end.X-x), int64(end.Z-z)
	return dx*dx+dz*dz > alliedPassEndClear*alliedPassEndClear
}

// alliedPassPartner reports whether the ground mover self (whose collision
// record is mine) may treat occupant occ's cells as free. It reads committed
// state only and writes nothing.
func (s *System) alliedPassPartner(self *units.Unit, mine *CollisionState, occ int) bool {
	other, ok := s.friendlyMover(self, occ)
	if !ok {
		return false
	}
	if !routeEndClear(handleRow(s.Routes, pool.Handle(occ)), other.X>>16, other.Z>>16) ||
		!routeEndClear(handleRow(s.Routes, self.Handle), mine.X>>16, mine.Z>>16) {
		return false
	}
	turn := int16(mine.Heading - other.Heading)
	if turn < 0 {
		turn = -turn
	}
	return uint16(turn) >= alliedPassMinTurn
}
