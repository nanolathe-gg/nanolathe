package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: docs/DESIGN_MOVEMENT_PATH.md "Modern unreachable
// moves". Nothing here is a retail claim; Strict 3.1 and Community 3.9 answer
// (0, 0) from Rules.UnreachableMoves and never reach any of it.
//
// A route search that publishes nothing for a live order was rejected at
// setup (its ray found no cell nearer the goal than the start) or exhausted
// its open list. Both read the class layer, which walls stationary mobile
// units [04 R-PATH-01 §14], so neither says whether the goal is out of reach
// or merely behind a crowd. At the one site where an empty publication raises
// the cannot-get-there bit [04 R-PATH-01 §7], the movement system re-runs the
// same setup ray from the unit's committed cell over a static view of the
// same layer — terrain, features and buildings, the owner's mapping and
// learned knowledge, every mobile occupant transparent — and records a
// certificate only when that walk closes its boundary loop without reaching
// the goal and the unit already stands near the walk's frontier. Unexplored
// ground stays optimistic exactly as the search reads it, so fog never
// produces a certificate.
//
// The certificate belongs to the order record, not to one activation: the
// retail retry re-installs the goal under a new activation every 30–59 ticks
// [04 R-ORD-01 §4], and each later empty publication re-certifies while
// keeping the first certification tick. Once the dwell has passed, the
// follower re-probes from where the unit stands; if the goal is still sealed
// and the unit still at the frontier, the order completes through the
// ordinary arrival and release, exactly as crowded arrival does. Orders owns
// which records may finish this way (orders.UnreachableMoveArrival).

// unreachableCert is one unit's certificate. A nil order is no certificate.
type unreachableCert struct {
	// order is the move the certificate belongs to. Holding the pointer keeps
	// the identity unique for the certificate's lifetime.
	order *orders.Node
	// activation is the request activation whose empty publication last
	// certified the order; the follower completes only while it is live.
	activation uint64
	// since is the tick of the order's first certification, the start of the
	// dwell. Re-certification of the same order keeps it.
	since uint32
	// goal is the goal object the closing probe re-tests.
	goal path.Goal
}

// Modern tuning for unreachable moves (docs/DESIGN_MOVEMENT_PATH.md "Modern
// unreachable moves"). Six cells is the 96 world units of Modern crowded
// arrival; 90 ticks is its dwell. Neither is a retail constant.
const (
	modernUnreachableFrontierCells = 6
	modernUnreachableDwell         = 90
)

// UnreachableMoves certifies sealed goals and finishes eligible moves at
// their frontier after the dwell (docs/DESIGN_MOVEMENT_PATH.md "Modern
// unreachable moves").
func (*ModernRules) UnreachableMoves(*System) (int32, uint32) {
	return modernUnreachableFrontierCells, modernUnreachableDwell
}

// noteRouteUnavailable is asked once per empty publication that raised the
// cannot-get-there bit on u's live order n [04 R-PATH-01 §7]. Under Strict it
// returns after one zero answer. Orders' eligibility is asked before the
// probe, so an ineligible record pays nothing more.
func (s *System) noteRouteUnavailable(u *units.Unit, n *orders.Node, r path.Request) {
	frontier, _ := s.rules().UnreachableMoves(s)
	if frontier <= 0 || u == nil || n == nil {
		return
	}
	if !orders.UnreachableMoveArrival(u, n) {
		s.clearUnreachable(u.Handle)
		return
	}
	if !s.goalSealedFrom(u, r.Goal, frontier) {
		// A crowd or an open gap: the retail retry continues, and a later
		// certification starts a fresh dwell.
		s.clearUnreachable(u.Handle)
		return
	}
	since := s.tick
	if old := handleRow(s.unreachable, u.Handle); old.order == n {
		since = old.since
	}
	s.setUnreachable(u.Handle, unreachableCert{order: n, activation: r.Activation, since: since, goal: r.Goal})
}

// noteRouteFound voids a certificate when a route published for its order
// ends in the goal's zero band: the search has just shown the goal reachable
// (a reclaimed wreck, an opened gate). A route to the tolerance frontier ends
// outside the band and leaves the certificate standing.
func (s *System) noteRouteFound(h pool.Handle, n *orders.Node, goal path.Goal, points []path.Point) {
	if s.unreachableLive == 0 || goal == nil {
		return
	}
	if cert := handleRow(s.unreachable, h); cert.order == nil || cert.order != n {
		return
	}
	profile := s.ProfileFor(h)
	if end, ok := path.RouteEndCell(points, int32(profile.FootPrintX), int32(profile.FootPrintZ)); ok && goal.H(end) == 0 {
		s.clearUnreachable(h)
	}
}

// unreachableArrival is the follower's question, asked only while some
// certificate is live. True completes head through the ordinary arrival.
func (s *System) unreachableArrival(u *units.Unit, head *orders.Node, tick uint32) bool {
	if s.unreachableLive == 0 || u == nil || head == nil {
		return false
	}
	cert := handleRow(s.unreachable, u.Handle)
	if cert.order == nil {
		return false
	}
	if cert.order != head {
		s.clearUnreachable(u.Handle) // the order changed
		return false
	}
	frontier, dwell := s.rules().UnreachableMoves(s)
	if frontier <= 0 {
		s.clearUnreachable(u.Handle) // the session left Modern
		return false
	}
	// Between the retry's re-install and its search's publication the
	// certificate is waiting to be renewed or cleared; only the activation it
	// was made for may complete.
	binding := handleRow(s.activeOrders, u.Handle)
	if binding == nil || binding.order != head || binding.token != cert.activation {
		return false
	}
	if tick-cert.since < dwell {
		return false // the ordinary retry continues through the dwell
	}
	// Closing probe: the goal must still be sealed from where the unit now
	// stands, with the unit still at the frontier. A goal that reopened
	// during the dwell is reached by the ordinary retry instead.
	sealed := s.goalSealedFrom(u, cert.goal, frontier)
	s.clearUnreachable(u.Handle)
	return sealed && orders.UnreachableMoveArrival(u, head)
}

// goalSealedFrom probes the static view from u's committed cell and reports
// whether the goal is sealed with u within frontier cells, per axis, of the
// walk's frontier cell. A sealed unit farther away — held back by a crowd,
// or still walking — keeps the retail retry and may close in.
func (s *System) goalSealedFrom(u *units.Unit, goal path.Goal, frontier int32) bool {
	if s.Terrain == nil || goal == nil || (u.Def != nil && u.Def.CanFly) {
		return false
	}
	profile := s.ProfileFor(u.Handle)
	reg := s.ensureLayerRegistry()
	if reg == nil {
		return false
	}
	reg.BindMappingWord(s.mappingWordSource(u.Handle))
	layer := reg.For(s.classKeyFor(u.Handle), profile)
	if layer == nil {
		return false
	}
	footX, footZ := profile.FootPrintX, profile.FootPrintZ
	owner := u.Owner
	learned := s.rules().LearnedTerrain(s)
	start := s.pathStartCell(u)
	bounds := path.Rect{Max: path.Cell{X: s.Terrain.CellW - 1, Z: s.Terrain.CellH - 1}}
	var probe path.GoalSealedProbe
	probe, s.unreachableGoals = path.ProbeGoalSealed(start, goal, true, bounds, func(c path.Cell) uint8 {
		return layer.staticPassable(c.X, c.Z, footX, footZ, owner, learned)
	}, s.unreachableGoals)
	if !probe.Sealed {
		return false
	}
	dx, dz := probe.Frontier.X-start.X, probe.Frontier.Z-start.Z
	return max(dx, -dx, dz, -dz) <= frontier
}

// setUnreachable writes one certificate row, keeping the live count the
// follower gates on. The row grows only here, so Strict never allocates it.
func (s *System) setUnreachable(h pool.Handle, c unreachableCert) {
	if handleRow(s.unreachable, h).order != nil {
		s.unreachableLive--
	}
	if c.order != nil {
		s.unreachableLive++
	}
	setHandleRow(&s.unreachable, h, c)
}

// clearUnreachable drops h's certificate, if any. It never grows the row.
func (s *System) clearUnreachable(h pool.Handle) {
	if s != nil && handleRow(s.unreachable, h).order != nil {
		s.setUnreachable(h, unreachableCert{})
	}
}

// staticPassable is the search's passability read with mobile occupants made
// transparent. Every anchor the layer admits keeps its answer, so the view is
// never stricter than the search's own read; unexplored ground stays the
// search's optimistic value 2 unless the owner learned it under Modern
// learned terrain. A blocked anchor on known ground is re-read inside the
// restamp bound from the terrain and feature chain and from building
// occupancy (an occupant with no mover) over its footprint
// [04 R-PATH-01 §2][04 R-PATH-01 §14][04 R-SLOPE-01 §3].
func (l *ClassLayer) staticPassable(x, z int32, footX, footZ int16, player uint8, learned *LearnedTerrain) uint8 {
	v := l.Passable(x, z, footX, footZ, player)
	if v == LayerUnmapped && learned != nil {
		if bx, bz := mappingTile(x, z, footX, footZ); learned.Known(bx, bz, player) {
			v = l.Value(x, z)
		}
	}
	if v != LayerBlocked {
		return v
	}
	if x < 0 || z < 0 || x >= l.W || z >= l.H {
		return LayerBlocked
	}
	if bx, bz := mappingTile(x, z, footX, footZ); bx < 0 || bz < 0 || bx >= l.W>>1 || bz >= l.H>>1 {
		return LayerBlocked
	}
	fx, fz := l.footprintSize()
	if x+fx >= l.W || z+fz >= l.H {
		return LayerBlocked // the restamp's own edge bound
	}
	for cz := z; cz < z+fz; cz++ {
		for cx := x; cx < x+fx; cx++ {
			if l.Profile.classifyCell(l.Terrain, cx, cz) == ClassBlocked {
				return LayerBlocked
			}
			if l.Grid != nil {
				if id, ok := l.Grid.OccupantAt(Cell{X: cx, Z: cz}); ok && (l.movers == nil || !l.movers.HasMover(pool.Handle(id))) {
					return LayerBlocked
				}
			}
		}
	}
	return LayerSteep
}
