package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy (DESIGN_MOVEMENT_PATH "Modern jam release"): a
// ground mover that friendly units have held in place for a second stops
// colliding with friendly ground units for a short release window and plans
// over the static view, so a friendly jam always drains. Terrain, features,
// structures and enemies still block, a same-way mover ahead is still a queue
// to wait in, and the release ends before the unit's route end so an overlap
// is never carried into a destination. Retail rejects every occupied
// footprint cell and keeps a blocked mover proposing the same step
// [04 R-COLL-01 §1][04 R-COLL-01 §2].

// Modern jam-release tuning.
const (
	// jamReleaseCooldown is the ticks after a release ends before the same
	// unit may be released again.
	jamReleaseCooldown = 60
	// jamReleaseQueueTurn is the heading difference, about 60° on the
	// sixteen-bit circle, below which a moving friendly blocker is a queue
	// member heading the same way rather than a jam.
	jamReleaseQueueTurn = 0x2AAA
	// jamReleaseEndClear is the distance in world units from the movement
	// goal or the route's final point inside which no release starts and a
	// running one ends.
	jamReleaseEndClear = 128
)

// jamRelease is one unit's jam-release state: the run of consecutive jammed
// ticks, the tick the current release ends, the latest tick it may be held
// open to, the first tick a new one may start, and whether the route planned
// over the static view is still owed a re-plan. The zero value is an
// unjammed unit that has never been released.
type jamRelease struct {
	run      uint16
	replan   bool
	until    uint32
	limit    uint32
	cooldown uint32
}

// JamRelease releases a unit after jamAfter jammed ticks for lifetime ticks
// (docs/DESIGN_MOVEMENT_PATH.md "Modern jam release").
func (*ModernRules) JamRelease(*System) (uint16, uint32) {
	return modernJamReleaseAfter, modernJamReleaseLifetime
}

// Modern jam-release timing: one second jammed, three seconds released.
const (
	modernJamReleaseAfter    = 30
	modernJamReleaseLifetime = 90
)

// releasing reports whether h is inside a release window on tick.
func (s *System) releasing(h pool.Handle, tick uint32) bool {
	return handleRow(s.jamReleases, h).until > tick
}

// friendlyMover reports whether occupant occ is a live, grounded, uncarried
// mobile unit of self's owner or of an owner mutually allied with it, through
// this tick's alliance query.
func (s *System) friendlyMover(self *units.Unit, occ int) (*CollisionState, bool) {
	other := handleRow(s.Collisions, pool.Handle(occ))
	if other == nil || other.Building || other.Mode != 1 || s.world == nil {
		return nil, false
	}
	ou := s.world.Unit(pool.Handle(occ))
	if ou == nil || !ou.Alive || ou.Attachment.Carrier != 0 {
		return nil, false
	}
	if ou.Owner != self.Owner && (s.passAlliance == nil || !s.passAlliance(self.Owner, ou.Owner) || !s.passAlliance(ou.Owner, self.Owner)) {
		return nil, false
	}
	return other, true
}

// hostileMover returns the released search's wall test: unit id belongs to
// neither owner nor an owner mutually allied with it, through this tick's
// alliance query.
func (s *System) hostileMover(owner uint8) func(id int) bool {
	return func(id int) bool {
		ou := s.world.Unit(pool.Handle(id))
		if ou == nil || ou.Owner == owner {
			return false
		}
		return s.passAlliance == nil || !s.passAlliance(owner, ou.Owner) || !s.passAlliance(ou.Owner, owner)
	}
}

// jamReleaseIgnores reports whether the commit's occupant test may treat
// occupant occ's cells as free for self: one of the two is releasing, occ is
// a friendly ground unit, and occ is not a same-way mover self should queue
// behind. It reads committed state only and writes nothing.
func (s *System) jamReleaseIgnores(self *units.Unit, mine *CollisionState, occ int, tick uint32) bool {
	if !s.releasing(self.Handle, tick) && !s.releasing(pool.Handle(occ), tick) {
		return false
	}
	other, ok := s.friendlyMover(self, occ)
	return ok && !s.sameWayMover(mine, other, pool.Handle(occ))
}

// sameWayMover reports whether other (unit occ) holds an active route and
// heads within jamReleaseQueueTurn of mine.
func (s *System) sameWayMover(mine, other *CollisionState, occ pool.Handle) bool {
	if r := handleRow(s.Routes, occ); r == nil || !r.Active || r.Count < 2 {
		return false
	}
	turn := int16(mine.Heading - other.Heading)
	if turn < 0 {
		turn = -turn
	}
	return uint16(turn) < jamReleaseQueueTurn
}

// jamReleaseEndOK reports whether u stands farther than jamReleaseEndClear
// from its movement goal and, when it has a route, from the route's final
// point. A unit with neither is never clear: a release must not be carried
// into a destination.
func (s *System) jamReleaseEndOK(u *units.Unit, route *Route, x, z int32) bool {
	d2, hasGoal := s.distSqToGoal(u)
	if hasGoal && d2 <= uint64(jamReleaseEndClear<<16)*uint64(jamReleaseEndClear<<16) {
		return false
	}
	if route == nil || !route.Active || route.Count < 2 {
		return hasGoal
	}
	end := route.Points[route.Count-1]
	dx, dz := int64(end.X-x), int64(end.Z-z)
	return dx*dx+dz*dz > jamReleaseEndClear*jamReleaseEndClear
}

// noteJamRelease runs after a ground mover's commit on a tick the bound rules
// release jams. A release in progress ends at the first commit near the
// destination that leaves the unit clear of every friendly unit, and is held
// open, up to twice its lifetime, while the unit still stands inside one: an
// overlap must never outlive the release, or the friend it stands in would
// block every later step. Otherwise it counts jammed ticks and starts a
// release when the run reaches jamAfter. A tick is jammed when the commit was
// blocked by a friendly mover that is not a same-way queue member, or when the
// unit has no route and a friendly ground unit touches its footprint — its
// search failed because parked friends walled it in.
func (s *System) noteJamRelease(u *units.Unit, coll *CollisionState, blocked bool, blocker int, tick uint32, jamAfter uint16, lifetime uint32) {
	st := handleRow(s.jamReleases, u.Handle)
	route := handleRow(s.Routes, u.Handle)
	routed := route != nil && route.Active && route.Count >= 2
	x, z := coll.X>>16, coll.Z>>16
	if st.until > tick {
		inside := s.insideFriend(u, coll)
		switch {
		case !inside && !s.jamReleaseEndOK(u, route, x, z):
			st.until = tick
			st.cooldown = tick + jamReleaseCooldown
		case inside && st.until <= tick+1 && tick+1 < st.limit:
			st.until = tick + 2
			st.cooldown = st.until + jamReleaseCooldown
		default:
			return
		}
		setHandleRow(&s.jamReleases, u.Handle, st)
		return
	}
	if st.replan {
		// The window has closed: the route planned through friends now
		// leads into units that block again, so plan afresh.
		st.replan = false
		if route != nil {
			route.WantsRepath = true
		}
	}
	jammed, friendBlocked := false, false
	if blocked && blocker > 0 {
		if other, ok := s.friendlyMover(u, blocker); ok {
			jammed = !s.sameWayMover(coll, other, pool.Handle(blocker))
			friendBlocked = jammed
		}
	}
	if !jammed && blocked && !routed {
		jammed = s.touchesFriendly(u, coll)
	}
	if !jammed {
		st.run = 0
		// Write only a change, so an unjammed unit never grows the row.
		if handleRow(s.jamReleases, u.Handle) != st {
			setHandleRow(&s.jamReleases, u.Handle, st)
		}
		return
	}
	if st.run < 0xffff {
		st.run++
	}
	// Near the destination a release starts only to take the unit through
	// the friend that blocks it, or out of one it stands inside; the end
	// rule above then ends it at the first commit clear of every friend. A
	// goal a parked friend already holds is crowded arrival's to finish, so
	// it never starts one there.
	if st.run >= jamAfter && tick >= st.cooldown && (s.jamReleaseEndOK(u, route, x, z) || s.insideFriend(u, coll) || friendBlocked && !s.goalHeldByParkedFriend(u, coll)) {
		st.run = 0
		st.until = tick + lifetime
		st.limit = tick + 2*lifetime
		st.cooldown = st.until + jamReleaseCooldown
		st.replan = true
		if route != nil {
			// Re-plan promptly: the release's searches read the static view.
			route.WantsRepath = true
			route.LastRequestTick = 0
		}
	}
	setHandleRow(&s.jamReleases, u.Handle, st)
}

// goalHeldByParkedFriend reports whether a friendly ground unit with no active
// route holds a cell of u's goal footprint — the crowded destination that
// Modern crowded arrival finishes where the unit stands
// (DESIGN_MOVEMENT_PATH "Modern crowded arrival").
func (s *System) goalHeldByParkedFriend(u *units.Unit, coll *CollisionState) bool {
	gx, gz, ok := s.moveGoalForUnit(u)
	if !ok || s.Grid == nil {
		return false
	}
	fx, fz := int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	ax, az := goalCellForWorld(gx, fx), goalCellForWorld(gz, fz)
	for z := az; z < az+fz; z++ {
		for x := ax; x < ax+fx; x++ {
			occ, held := s.Grid.OccupantAt(Cell{X: x, Z: z})
			if !held || occ <= 0 || occ == int(u.Handle) {
				continue
			}
			if _, friendly := s.friendlyMover(u, occ); !friendly {
				continue
			}
			if r := handleRow(s.Routes, pool.Handle(occ)); r == nil || !r.Active || r.Count < 2 {
				return true
			}
		}
	}
	return false
}

// insideFriend reports whether a friendly ground unit holds a cell of u's
// committed footprint — the overlap a release leaves behind.
func (s *System) insideFriend(u *units.Unit, coll *CollisionState) bool {
	if s.Grid == nil {
		return false
	}
	fx, fz := int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	a := coll.CachedAnchor
	for z := a.Z; z < a.Z+fz; z++ {
		for x := a.X; x < a.X+fx; x++ {
			if occ, held := s.Grid.OccupantAt(Cell{X: x, Z: z}); held && occ > 0 && occ != int(u.Handle) {
				if _, ok := s.friendlyMover(u, occ); ok {
					return true
				}
			}
		}
	}
	return false
}

// touchesFriendly reports whether a friendly ground unit holds a cell of the
// ring around u's committed footprint.
func (s *System) touchesFriendly(u *units.Unit, coll *CollisionState) bool {
	if s.Grid == nil {
		return false
	}
	fx, fz := int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	a := coll.CachedAnchor
	for z := a.Z - 1; z <= a.Z+fz; z++ {
		for x := a.X - 1; x <= a.X+fx; x++ {
			if occ, held := s.Grid.OccupantAt(Cell{X: x, Z: z}); held && occ > 0 && occ != int(u.Handle) {
				if _, ok := s.friendlyMover(u, occ); ok {
					return true
				}
			}
		}
	}
	return false
}

// resetJamRun forgets h's run of jammed ticks, so a new order starts counting
// afresh; a release in progress keeps its window. It never grows the row.
func (s *System) resetJamRun(h pool.Handle) {
	if s != nil && int(h) < len(s.jamReleases) {
		s.jamReleases[h].run = 0
	}
}

// clearJamRelease drops h's jam-release state, if any. It never grows the row.
func (s *System) clearJamRelease(h pool.Handle) {
	if s != nil && int(h) < len(s.jamReleases) {
		s.jamReleases[h] = jamRelease{}
	}
}
