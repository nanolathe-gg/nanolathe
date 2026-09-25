package movement

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// Nanolathe Modern policy (docs/DESIGN_MOVEMENT_PATH.md "Modern wedge
// escape"): a ground mover a wreck was stamped over may leave it. Nothing here
// is a retail claim; Strict 3.1 and Community 3.9 answer Rules.WedgeEscape
// false and never reach any of it.
//
// The feature stamp tests no unit occupancy [05 R-FEAT-01 §3], and a corpse is
// stamped at the dying unit's committed anchor [05 R-FEAT-01 §13]. When a unit
// dies overlapping a friend, its wreck covers cells of the friend's committed
// footprint, and retail holds the friend until the wreck goes: the validator
// tests every cell of every proposed footprint [04 R-COLL-01 §2], so a step
// whose new anchor still covers a wreck cell is rejected, and when every
// neighbouring anchor does, every proposal is; and a search from a start
// anchor the class layer walls is rejected at setup without seeding
// [04 R-PATH-01 §4], so the empty publication raises cannot-get-there and no
// route out ever arrives [04 R-PATH-01 §7].
//
// The policy has three pieces, all keyed to the mover's committed footprint:
//
//   - the commit's static test passes a cell the committed footprint already
//     covers, and tests every cell entering the footprint exactly as before,
//     so the set of rejected cells a mover covers can only shrink — it can
//     leave a wreck and never walk further into one;
//   - a search opened for such a mover re-reads each anchor that overlaps its
//     committed footprint and that the request's own view walls, with the
//     mover's own cells passable and every other cell through that view's
//     per-cell chain; a passing anchor answers the steep tier, so the search
//     prefers the shortest way off, and every other anchor and every other
//     request reads exactly as before;
//   - a mover the static test refuses while it stands on rejected ground,
//     under a route not planned from where it stands, re-plans at the next
//     scheduler call instead of after the re-request throttle
//     [04 R-MOV-01 §7].
//
// A mover the wreck encloses — every step would add a rejected cell — gets an
// empty search and no movement: nothing is fabricated.

// WedgeEscape lets a ground mover leave ground a wreck was stamped over
// (docs/DESIGN_MOVEMENT_PATH.md "Modern wedge escape").
func (*ModernRules) WedgeEscape(*System) bool { return true }

// coveredByFootprint reports whether cell c lies inside the fx-by-fz footprint
// anchored at a.
func coveredByFootprint(c, a Cell, fx, fz int32) bool {
	return c.X >= a.X && c.X < a.X+fx && c.Z >= a.Z && c.Z < a.Z+fz
}

// coversRejected reports whether the committed footprint of coll covers a cell
// the commit's static test rejects for profile p. The committed footprint must
// be the profile's, which is the one the search reads anchors with.
func (s *System) coversRejected(coll *CollisionState, p Profile) bool {
	fx, fz := p.footprintSize()
	if s == nil || s.Terrain == nil || coll == nil || int32(max(coll.FootPrintX, 1)) != fx || int32(max(coll.FootPrintZ, 1)) != fz {
		return false
	}
	a := coll.CachedAnchor
	for z := a.Z; z < a.Z+fz; z++ {
		for x := a.X; x < a.X+fx; x++ {
			if !p.IsPassableCommitCell(s.Terrain, x, z) {
				return true
			}
		}
	}
	return false
}

// wedgedStart reports the committed anchor of requester h when h is a live,
// grounded, uncarried mobile unit whose committed footprint covers a cell the
// commit's static test rejects.
func (s *System) wedgedStart(h pool.Handle, profile Profile) (Cell, bool) {
	if s == nil || s.Terrain == nil || s.world == nil {
		return Cell{}, false
	}
	u := s.world.Unit(h)
	if u == nil || !u.Alive || u.Attachment.Carrier != 0 || (u.Def != nil && u.Def.CanFly) {
		return Cell{}, false
	}
	coll := handleRow(s.Collisions, h)
	if coll == nil || coll.Building || coll.CachedMode&3 != 1 || !s.coversRejected(coll, profile) {
		return Cell{}, false
	}
	return coll.CachedAnchor, true
}

// wedgeExitValue re-reads an anchor that overlaps a wedged requester's
// committed footprint (anchored at start) and that the request's own view read
// as blocked. Cells the requester already covers are passable; every other
// cell of the anchor's footprint must pass the view's per-cell chain — the
// class layer's own (terrain, features, buildings and stale occupants), or,
// for jam release's static view, terrain, features, buildings and the kept
// movers. The bounds are the ones every view applies. A passing anchor answers
// the steep tier: passable, and charged the steep cost [04 R-PATH-01 §3].
func (l *ClassLayer) wedgeExitValue(x, z int32, footX, footZ int16, start Cell, static bool, keep func(id int) bool) uint8 {
	if l == nil || x < 0 || z < 0 || x >= l.W || z >= l.H {
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
			c := Cell{X: cx, Z: cz}
			if coveredByFootprint(c, start, fx, fz) {
				continue // the requester already stands here
			}
			if !static {
				if l.classifyCell(cx, cz) == LayerBlocked {
					return LayerBlocked
				}
				continue
			}
			if l.Profile.classifyCell(l.Terrain, cx, cz) == ClassBlocked {
				return LayerBlocked
			}
			if l.Grid != nil {
				if id, ok := l.Grid.OccupantAt(c); ok && (l.movers == nil || !l.movers.HasMover(pool.Handle(id)) || keep != nil && keep(id)) {
					return LayerBlocked
				}
			}
		}
	}
	return LayerSteep
}

// promptWedgeReplan runs after a ground commit the static test refused, on a
// visit the rule answered on. When the mover covers rejected ground and its
// route was not planned from the anchor it stands on — it was wedged while
// following a route planned before the wreck, or it holds an order's synthetic
// line — the re-request throttle is waived once: the wants-repath bit is set
// and the request tick cleared, so the next scheduler call admits the search
// whose re-read leads it off, instead of up to 60–67 ticks later
// [04 R-MOV-01 §7]. A route planned from here is left alone: the mover is
// turning toward the way that search found, and re-planning would only repeat
// it.
func (s *System) promptWedgeReplan(route *Route, coll *CollisionState, p Profile) {
	if route == nil || !s.coversRejected(coll, p) {
		return
	}
	fx, fz := p.footprintSize()
	a := coll.CachedAnchor
	// The published form of a route's first point: the start cell doubled,
	// narrowed to sixteen bits, plus the footprint, times eight
	// [04 R-PATH-01 §7].
	here := Point{X: (int32(int16(a.X*2)) + fx) * 8, Z: (int32(int16(a.Z*2)) + fz) * 8}
	if route.Active && route.Count > 0 && route.Points[0] == here {
		return
	}
	route.WantsRepath = true
	route.LastRequestTick = 0
}
