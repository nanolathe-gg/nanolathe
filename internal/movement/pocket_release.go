package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: docs/DESIGN_MOVEMENT_PATH.md "Modern pocket
// release", an extension of Modern jam release. Nothing here is a retail
// claim; Strict 3.1 and Community 3.9 answer (0, 0) from Rules.PocketRelease
// and never reach any of it.
//
// The unit it answers for is sealed out of its own free destination: a
// packed formation filled around the unit's slot before the unit got there,
// so the slot is free but every way in is held by parked friends. The unit's
// searches are rejected at setup, the move's retry installs no synthetic
// line, and the unit stands still without a route for as long as the record
// is the last primary one [04 R-ORD-01 §4]. Jam release counts rejected
// commits, and a unit at rest commits nothing; crowded arrival needs the goal
// itself held; unreachable moves certify over a static view in which every
// mobile is transparent, so the goal looks reachable.
//
// Certificate. At the empty publication that raises the cannot-get-there bit
// on the live order [04 R-PATH-01 §7], an eligible unit standing against a
// parked friend within near cells of its goal is judged by a bounded flood
// over footprint anchors in a fixed window around the goal anchor, from the
// goal footprint, stepping in the search's eight directions with only the
// destination footprint tested [04 §7.1]. An anchor is open when its
// footprint is statically passable in the owner's static view — terrain,
// features and buildings as the unreachable-move probe reads them,
// unexplored ground optimistic — and no cell of it is held by a parked
// friend: a live, grounded, uncarried, complete mobile of the unit's owner
// or a mutually allied owner, with zero speed and no active route. Every
// other occupant — moving friends, enemies, the unit itself — is
// transparent, so it can only open the pocket. The pocket is sealed when the
// goal's own footprint is open and the flood closes inside the window
// without reaching the unit's anchor or one of its eight neighbours. A goal
// a parked friend holds is crowded arrival's; a statically blocked one is
// unreachable moves'.
//
// Release. Once the dwell has passed since the order's first certificate,
// and while no release is running or cooling down, the follower re-floods
// from where the unit stands and, if the pocket is still sealed, grants a
// jam release marked as a pocket release: the unit plans over the static
// view, passes through the ring and completes by ordinary arrival at its own
// slot. Jam release's near-destination end rule closes such a release only
// on the unit's own goal anchor (jam_release.go). A route published for the
// order clears the certificate, except the release's own route, and each
// goal re-install by the order's retry during the release is re-planned at
// the next poll. After pocketReleaseGrants releases that did not get the
// unit in, the move finishes where the unit stands, through the
// crowded-arrival completion.

// pocketCert is one unit's pocket-release certificate. The zero value is no
// certificate.
type pocketCert struct {
	// order is the move the certificate belongs to; nil is no certificate.
	order *orders.Node
	// since is the tick of the order's first certificate, the dwell's start.
	since uint32
	// grants counts the pocket releases granted for this certificate.
	grants uint8
	// token is the order activation the running release last planned for;
	// a goal re-install under a new activation is re-planned once, promptly.
	token uint64
}

// Modern pocket-release tuning (docs/DESIGN_MOVEMENT_PATH.md "Modern pocket
// release"). None is a retail constant.
const (
	// modernPocketNear is the largest per-axis cell distance between a
	// unit's committed anchor and its goal anchor at which it is judged.
	modernPocketNear = 16
	// modernPocketDwell is the ticks from the order's first certificate to
	// the first release: jam release's own jammed-tick trigger.
	modernPocketDwell = 30
	// pocketWindowHalf is the flood window's half size in anchors around the
	// goal anchor: a pocket that reaches farther is treated as open.
	pocketWindowHalf = 16
	// pocketReleaseGrants is how many pocket releases a certificate may
	// grant before the move finishes where the unit stands.
	pocketReleaseGrants = 2
)

// PocketRelease is off under Strict 3.1: a unit keeps its move and the
// order's own retry for as long as the record is the last primary one
// [04 R-ORD-01 §4]. Community inherits the answer.
func (StrictRules) PocketRelease(*System) (int32, uint32) { return 0, 0 }

// PocketRelease releases a sealed-out unit into its own free slot
// (docs/DESIGN_MOVEMENT_PATH.md "Modern pocket release").
func (*ModernRules) PocketRelease(*System) (int32, uint32) {
	return modernPocketNear, modernPocketDwell
}

// pocketPolicy is the bound answer. The pocket release is an extension of
// jam release and is off whenever jam release is.
func (s *System) pocketPolicy() (near int32, dwell uint32, on bool) {
	near, dwell = s.rules().PocketRelease(s)
	if near <= 0 {
		return 0, 0, false
	}
	if jamAfter, lifetime := s.rules().JamRelease(s); jamAfter == 0 || lifetime == 0 {
		return 0, 0, false
	}
	return near, dwell, true
}

// pocketArrival is the follower's closing check, asked on every ground
// follower visit after crowded arrival and unreachable moves, and at no cost
// while no certificate is live. True finishes head where the unit stands,
// through the ordinary arrival, once the certificate has used its grants.
func (s *System) pocketArrival(u *units.Unit, head *orders.Node, tick uint32) bool {
	if s.pocketLive == 0 || u == nil || head == nil {
		return false
	}
	cert := handleRow(s.pockets, u.Handle)
	if cert.order == nil {
		return false
	}
	near, dwell, on := s.pocketPolicy()
	if !on || cert.order != head {
		s.clearPocket(u.Handle) // the session left the policy, or the order changed
		return false
	}
	if tick-cert.since < dwell {
		return false
	}
	// A release running or cooling down — one this certificate granted, or
	// an ordinary jam release — is the attempt under way: it is judged once
	// it is over, not by a flood on every visit meanwhile.
	if jr := handleRow(s.jamReleases, u.Handle); jr.until > tick || tick < jr.cooldown {
		if jr.pocket && jr.until > tick {
			s.keepPocketPlanning(u.Handle, head, cert)
		}
		return false
	}
	coll, goal, gate := s.pocketCandidate(u, head, near)
	switch {
	case gate == pocketAway:
		return false // wait until the unit stands against the formation
	case gate != pocketReady || !s.pocketSealed(u, coll, goal):
		s.clearPocket(u.Handle)
		return false
	}
	if s.grantPocketRelease(u, tick) {
		return false
	}
	s.clearPocket(u.Handle)
	return true
}

// keepPocketPlanning keeps a pocket release planning through the order's own
// retry. A certified unit's move waits in its code-9 retry, which
// re-installs the goal every 30–59 ticks [04 R-ORD-01 §4]; a re-install
// drops the route the release planned through the ring, and the goal
// installer keeps a request stamp under ten ticks old [04 R-PATH-01 §8], so
// a re-install soon after the grant's own search would hold the replacement
// back for the whole re-route throttle while the window ran out. Each new
// activation during the release is re-planned at the next poll, once.
func (s *System) keepPocketPlanning(h pool.Handle, head *orders.Node, cert pocketCert) {
	b := handleRow(s.activeOrders, h)
	if b == nil || b.order != head || b.token == cert.token {
		return
	}
	cert.token = b.token
	s.setPocket(h, cert)
	if r := handleRow(s.Routes, h); r != nil && !r.Active {
		r.WantsRepath = true
		r.LastRequestTick = 0
	}
}

// The cheap gates' verdicts, in the order pocketCandidate applies them.
const (
	pocketNoMover    = iota // no stamped ground mover
	pocketIneligible        // the record is not eligible, or the goal is not near
	pocketAway              // eligible and near, but not against a parked friend
	pocketReady             // every gate passed: run the flood
)

// pocketCandidate applies the cheap gates in order: a stamped ground mover,
// the shared record eligibility of the Modern move-completion policies
// (orders.UnreachableMoveArrival: a sole primary Move_Ground with no target
// and no automatic or danger provenance, on a live, complete, unstunned,
// uncarried ground mover), a goal anchor within near cells per axis but not
// under the unit, and a parked friend against the unit. It returns the
// collision record, the goal anchor and the verdict.
func (s *System) pocketCandidate(u *units.Unit, head *orders.Node, near int32) (*CollisionState, Cell, int) {
	coll := handleRow(s.Collisions, u.Handle)
	if coll == nil || !coll.HasStamp || coll.StampedPlane != PlaneGround || coll.Building {
		return nil, Cell{}, pocketNoMover
	}
	if !orders.UnreachableMoveArrival(u, head) {
		return coll, Cell{}, pocketIneligible
	}
	gx, gz, ok := s.moveGoalFor(u.Handle, head)
	if !ok {
		return coll, Cell{}, pocketIneligible
	}
	fx, fz := int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	goal := Cell{X: goalCellForWorld(gx, fx), Z: goalCellForWorld(gz, fz)}
	at := coll.CachedAnchor
	if d := max(at.X-goal.X, goal.X-at.X, at.Z-goal.Z, goal.Z-at.Z); d == 0 || d > near {
		return coll, goal, pocketIneligible
	}
	if !s.touchesParkedFriend(u, coll) {
		return coll, goal, pocketAway
	}
	return coll, goal, pocketReady
}

// notePocketRejection is asked at the empty publication that raises the
// cannot-get-there bit on u's live order n [04 R-PATH-01 §7], after
// unreachable moves. It certifies the pocket sealed, keeping the tick of the
// order's first certificate, or clears the certificate.
func (s *System) notePocketRejection(u *units.Unit, n *orders.Node) {
	near, _, on := s.pocketPolicy()
	if !on || u == nil || n == nil {
		return
	}
	coll, goal, gate := s.pocketCandidate(u, n, near)
	if gate != pocketReady || !s.pocketSealed(u, coll, goal) {
		s.clearPocket(u.Handle)
		return
	}
	cert := handleRow(s.pockets, u.Handle)
	if cert.order != n {
		cert = pocketCert{order: n, since: s.tick}
	}
	s.setPocket(u.Handle, cert)
}

// notePocketRouteFound clears h's certificate when a route published for its
// order: the unit's own search has found a way on. A pocket release's own
// route, planned through the ring, keeps it: the release is the attempt
// being judged.
func (s *System) notePocketRouteFound(h pool.Handle, n *orders.Node) {
	if jr := handleRow(s.jamReleases, h); jr.pocket && jr.until > s.tick {
		return
	}
	if s.pocketLive > 0 && handleRow(s.pockets, h).order == n {
		s.clearPocket(h)
	}
}

// grantPocketRelease starts a jam release that takes u through the ring of
// parked friends into its free pocket, keeping the certificate so a failed
// attempt is judged again; it reports false when jam release is off, a
// release is running or cooling down (the closing check never asks then), or
// the certificate has used its grants.
func (s *System) grantPocketRelease(u *units.Unit, tick uint32) bool {
	jamAfter, lifetime := s.rules().JamRelease(s)
	if jamAfter == 0 || lifetime == 0 {
		return false
	}
	cert := handleRow(s.pockets, u.Handle)
	jr := handleRow(s.jamReleases, u.Handle)
	if cert.grants >= pocketReleaseGrants || jr.until > tick || tick < jr.cooldown {
		return false
	}
	cert.grants++
	if b := handleRow(s.activeOrders, u.Handle); b != nil {
		cert.token = b.token
	}
	s.setPocket(u.Handle, cert)
	jr = jamRelease{until: tick + lifetime, limit: tick + 2*lifetime, cooldown: tick + lifetime + jamReleaseCooldown, replan: true, pocket: true}
	setHandleRow(&s.jamReleases, u.Handle, jr)
	if route := handleRow(s.Routes, u.Handle); route != nil {
		// Plan promptly over the released static view.
		route.WantsRepath = true
		route.LastRequestTick = 0
	}
	return true
}

// pocketAtGoal reports whether u's committed anchor is its movement goal's
// anchor: the unit stands in its own slot, clear of the ring it crossed.
func (s *System) pocketAtGoal(u *units.Unit, coll *CollisionState) bool {
	gx, gz, ok := s.moveGoalForUnit(u)
	if !ok {
		return false
	}
	fx, fz := int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	return coll.CachedAnchor == Cell{X: goalCellForWorld(gx, fx), Z: goalCellForWorld(gz, fz)}
}

// parkedFriend reports whether occupant occ is a parked friend of self: a
// live, grounded, uncarried, complete mobile of self's owner or of an owner
// mutually allied with it, standing still with no active route.
func (s *System) parkedFriend(self *units.Unit, occ int) bool {
	other, ok := s.friendlyMover(self, occ)
	if !ok || other.Speed != 0 {
		return false
	}
	ou := s.world.Unit(pool.Handle(occ))
	if ou == nil || ou.Remaining != 0 || ou.Move.Speed != 0 {
		return false
	}
	r := handleRow(s.Routes, pool.Handle(occ))
	return r == nil || !r.Active || r.Count < 2
}

// touchesParkedFriend reports whether a parked friend holds a cell of the
// ring around u's committed footprint.
func (s *System) touchesParkedFriend(u *units.Unit, coll *CollisionState) bool {
	if s.Grid == nil || s.world == nil {
		return false
	}
	fx, fz := int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	a := coll.CachedAnchor
	for z := a.Z - 1; z <= a.Z+fz; z++ {
		for x := a.X - 1; x <= a.X+fx; x++ {
			if z >= a.Z && z < a.Z+fz && x >= a.X && x < a.X+fx {
				continue
			}
			if occ, held := s.Grid.OccupantAt(Cell{X: x, Z: z}); held && occ > 0 && occ != int(u.Handle) && s.parkedFriend(u, occ) {
				return true
			}
		}
	}
	return false
}

// pocketWindow is one flood's window: the anchors x0..x1, z0..z1 around the
// goal (inclusive), the cell columns their footprints cover, and the layer
// and learned grid its static view reads — the rule is asked once per flood,
// never per anchor.
type pocketWindow struct {
	x0, z0, x1, z1 int32 // anchors, inclusive
	w, h           int32 // anchor counts
	cw             int32 // cell columns: anchors plus the footprint's reach
	fx, fz         int32
	layer          *ClassLayer
	learned        *LearnedTerrain
}

// Cell classes in the flood's lazily filled cell scratch.
const (
	pocketCellUnknown uint8 = iota
	pocketCellFree
	pocketCellWall
)

// preparePocketWindow sizes and clears the scratch for a window around goal.
// It reports false when the terrain or the layer is missing.
func (s *System) preparePocketWindow(u *units.Unit, coll *CollisionState, goal Cell) (pocketWindow, bool) {
	var win pocketWindow
	if s.Terrain == nil || s.Grid == nil || s.world == nil {
		return win, false
	}
	win.fx, win.fz = int32(max(coll.FootPrintX, 1)), int32(max(coll.FootPrintZ, 1))
	// Anchors whose footprint lies on the map.
	win.x0, win.z0 = max(goal.X-pocketWindowHalf, 0), max(goal.Z-pocketWindowHalf, 0)
	win.x1 = min(goal.X+pocketWindowHalf, s.Terrain.CellW-win.fx)
	win.z1 = min(goal.Z+pocketWindowHalf, s.Terrain.CellH-win.fz)
	if win.x1 < win.x0 || win.z1 < win.z0 {
		return win, false
	}
	win.w, win.h = win.x1-win.x0+1, win.z1-win.z0+1
	win.cw = win.w + win.fx - 1
	reg := s.ensureLayerRegistry()
	if reg == nil {
		return win, false
	}
	reg.BindMappingWord(s.mappingWordSource(u.Handle))
	win.layer = reg.For(s.classKeyFor(u.Handle), s.ProfileFor(u.Handle))
	if win.layer == nil {
		return win, false
	}
	win.learned = s.rules().LearnedTerrain(s)
	cells := int(win.cw * (win.h + win.fz - 1))
	s.pocketCells = growScratch(s.pocketCells, cells)
	clear(s.pocketCells[:cells])
	anchors := int(win.w * win.h)
	s.pocketSeen = growScratch(s.pocketSeen, anchors)
	clear(s.pocketSeen[:anchors])
	s.pocketStack = s.pocketStack[:0]
	return win, true
}

// pocketWall reports whether a parked friend of u holds cell (x, z) of the
// window, classifying the cell on first use.
func (s *System) pocketWall(win *pocketWindow, u *units.Unit, x, z int32) bool {
	k := (z-win.z0)*win.cw + x - win.x0
	switch s.pocketCells[k] {
	case pocketCellFree:
		return false
	case pocketCellWall:
		return true
	}
	wall := false
	if occ, held := s.Grid.OccupantAt(Cell{X: x, Z: z}); held && occ > 0 && occ != int(u.Handle) {
		wall = s.parkedFriend(u, occ)
	}
	s.pocketCells[k] = pocketCellFree
	if wall {
		s.pocketCells[k] = pocketCellWall
	}
	return wall
}

// pocketOpen reports whether anchor a (inside the window) is open: no parked
// friend holds a cell of its footprint and the footprint is statically
// passable in u's static view.
func (s *System) pocketOpen(win *pocketWindow, u *units.Unit, a Cell) bool {
	for z := a.Z; z < a.Z+win.fz; z++ {
		for x := a.X; x < a.X+win.fx; x++ {
			if s.pocketWall(win, u, x, z) {
				return false
			}
		}
	}
	return win.layer.staticPassable(a.X, a.Z, int16(win.fx), int16(win.fz), u.Owner, win.learned) != LayerBlocked
}

// pocketSteps is the search's neighbour order [04 §7.1] C2.
var pocketSteps = [8]Cell{{Z: -1}, {X: -1, Z: -1}, {X: -1}, {X: -1, Z: 1}, {Z: 1}, {X: 1, Z: 1}, {X: 1}, {X: 1, Z: -1}}

// pocketSealed is the certificate: the flood from the goal footprint closes
// inside its window without reaching u's anchor or one of its neighbours.
//
// The flood runs depth first, nearest the unit first. The verdict depends
// only on the connected set of open anchors around the goal — sealed when no
// member steps across the window border onto the map or stands next to the
// unit — so the order changes the work and never the answer. An open goal is
// then usually proved open by a walk towards the unit rather than by flooding
// the open ground around the goal.
func (s *System) pocketSealed(u *units.Unit, coll *CollisionState, goal Cell) bool {
	win, ok := s.preparePocketWindow(u, coll, goal)
	if !ok || goal.X < win.x0 || goal.X > win.x1 || goal.Z < win.z0 || goal.Z > win.z1 {
		return false
	}
	at := coll.CachedAnchor
	if !s.pocketOpen(&win, u, goal) {
		// Only a free pocket is certified. A goal a parked friend holds is
		// crowded arrival's to finish, and a release into it would stack the
		// unit on the friend; a statically blocked goal is unreachable
		// moves' to judge.
		return false
	}
	gap := func(c Cell) int32 { return max(c.X-at.X, at.X-c.X, c.Z-at.Z, at.Z-c.Z) }
	s.pocketSeen[(goal.Z-win.z0)*win.w+goal.X-win.x0] = true
	stack := append(s.pocketStack[:0], goal)
	defer func() { s.pocketStack = stack[:0] }()
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if gap(c) <= 1 {
			return false // the unit stands at the pocket
		}
		var next [8]Cell
		m := 0
		for _, d := range pocketSteps {
			n := Cell{X: c.X + d.X, Z: c.Z + d.Z}
			if n.X < win.x0 || n.X > win.x1 || n.Z < win.z0 || n.Z > win.z1 {
				// Off the map is a wall; past the window is open ground.
				if n.X >= 0 && n.Z >= 0 && n.X+win.fx <= s.Terrain.CellW && n.Z+win.fz <= s.Terrain.CellH {
					return false
				}
				continue
			}
			k := (n.Z-win.z0)*win.w + n.X - win.x0
			if s.pocketSeen[k] {
				continue
			}
			s.pocketSeen[k] = true
			if !s.pocketOpen(&win, u, n) {
				continue
			}
			// Insert farthest first, so the neighbour nearest the unit ends
			// on top of the stack; equal distances keep the search order.
			i := m
			for i > 0 && gap(next[i-1]) < gap(n) {
				next[i] = next[i-1]
				i--
			}
			next[i] = n
			m++
		}
		stack = append(stack, next[:m]...)
	}
	return true
}

// growScratch returns a slice of length n reusing buf's storage.
func growScratch[T any](buf []T, n int) []T {
	if cap(buf) < n {
		return make([]T, n)
	}
	return buf[:n]
}

// setPocket writes one certificate row, keeping the live count the
// follower's closing check gates on. The row grows only here, so a rule set
// that answers off never allocates it.
func (s *System) setPocket(h pool.Handle, cert pocketCert) {
	if handleRow(s.pockets, h).order != nil {
		s.pocketLive--
	}
	if cert.order != nil {
		s.pocketLive++
	}
	setHandleRow(&s.pockets, h, cert)
}

// clearPocket drops h's certificate, if any. It never grows the row.
func (s *System) clearPocket(h pool.Handle) {
	if s != nil && int(h) < len(s.pockets) && s.pockets[h] != (pocketCert{}) {
		s.setPocket(h, pocketCert{})
	}
}
