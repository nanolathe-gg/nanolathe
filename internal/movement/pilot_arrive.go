package movement

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ArrivePilot (Nanolathe Modern policy, docs/DESIGN_MOVEMENT_PATH.md "Modern
// arrival places") decides who goes where at the end of a move, and what a
// unit does when the place it is making for is taken or cannot be reached.
//
// Patrol work returns keep their saved acquisition position; assigning or
// exchanging their place would turn settling elsewhere into a false return
// (DESIGN_UNITS_ORDERS_COB "Modern patrol work").
//
// Places. Every plain ground move's destination footprint is the unit's
// place, reserved for as long as the order lives; a move first seen after it
// has begun keeps the goal it was given. Units one owner orders on
// one tick to one exact point — a group the command boundary did not spread,
// because the order did not come through it — are given a place each before
// any of them installs its goal: a block of footprints centred on the point,
// its rows across the way the group travels, the units at the front taking
// the far rows and, row by row, keeping their order across the way; then any
// two members of one footprint whose straight lines cross swap places when
// both new places remain passable. A unit sent alone to a point another live
// order reserves, or a parked unit
// or the ground holds, is given the nearest free footprint instead. A place
// is free when the owner believes it passable, no parked unit stands on it,
// and no other live order reserves it. Units the command boundary spread keep
// the places it gave them.
//
// Exchange. A unit within arriveNear world units of its place that finds it
// taken — held by the ground or by a parked unit — takes the nearest free
// footprint, so that arriving never waits on one particular cell.
//
// Every goal change is an ordinary goal install: the order's goal is written
// and the point goal installed exactly as the order's own handler does, and
// the follower re-plans at its next activation. Nothing is moved or stamped
// here, no map is iterated and neither random stream is drawn.
type ArrivePilot struct {
	NoPilot
	// Places turns on places.
	Places bool
	// Exchange turns on place exchange.
	Exchange bool
	// Stuck, when positive, also lets a unit exchange a place that is free
	// but that it has made no way toward for this many ticks while within
	// arriveNear of it.
	Stuck uint32
	// StuckParked narrows Stuck to a unit whose way is held by something
	// that will not move of itself: its last step was refused by ground, a
	// structure or a unit at rest, or it holds no route. A friend passing
	// in front of a unit near its place does not make the unit settle
	// short of it.
	StuckParked bool
	// Sealed, when positive, lets a unit far from its place settle where it
	// stands once it has stood there for this many ticks and the ground
	// itself closes its goal off: a goal on a plateau its kind cannot climb,
	// or across water, is never reached, and the units sent there gather at
	// the nearest ground they can reach. The first of them to get there
	// finish under Modern unreachable moves; the ones the first keep from
	// that ground finish here, behind them.
	Sealed uint32
}

const (
	// arriveNear is how near its place, in world units, a unit must come
	// before it looks at it.
	arriveNear = 160
	// arriveCheckEvery is how often, in ticks, a unit near its place looks
	// at it.
	arriveCheckEvery = 8
	// arriveMaxExchanges bounds the exchanges one order may make.
	arriveMaxExchanges = 6
	// arriveSearchRadius bounds the ring search for a free footprint, in
	// cells.
	arriveSearchRadius = 10
	// arriveUncrossPasses and arriveUncrossPairs bound the swap passes over
	// a block and the pairs one pass examines.
	arriveUncrossPasses = 4
	arriveUncrossPairs  = 1 << 15
	// arriveStood is how far, in world units, a unit may move and still be
	// standing where it stood.
	arriveStood = 48
)

type arriveRow struct {
	// seen is the last head order the scan examined, so that an order is
	// looked at once.
	seen *orders.Node
	// node is the order the place belongs to.
	node *orders.Node
	// place is the place's anchor and fx, fz its footprint; x, z its centre.
	place  Cell
	fx, fz int32
	x, z   numeric.Fixed
	// nextCheck is the next tick the unit looks at its place; exchanges
	// counts the exchanges this order made.
	nextCheck uint32
	exchanges uint8
	// member marks the unit a member of the block being placed, by the
	// claim generation.
	member uint32
	// bestD is the nearest the unit has come to its place, in whole world
	// units squared, and bestAt the tick it last came nearer.
	bestD  int64
	bestAt uint32
	// stoodX, stoodZ is where the unit was, in whole world units, when it
	// last left the ground it stood on, and stoodAt the tick: it has stood
	// since then within arriveStood of that point. stood is false until the
	// order's first visit.
	stoodX, stoodZ int32
	stoodAt        uint32
	stood          bool
}

type arriveFresh struct {
	h     pool.Handle
	owner uint8
	x, z  numeric.Fixed
	node  *orders.Node
}

type arriveMember struct {
	u          *units.Unit
	n          *orders.Node
	depth, lat int64
	fx, fz     int32
	sx, sz     int64 // start, whole world units
	bx, bz     int32 // place anchor
	placed     bool
}

type arriveState struct {
	moveGround, standby orders.ID
	rows                []arriveRow
	live                []*units.Unit
	fresh               []arriveFresh
	members             []arriveMember
	// claim stamps cells with the generation that claimed them; resv holds
	// the handle whose live order reserves a cell, checked lazily against
	// that unit's row.
	claim []uint32
	gen   uint32
	resv  []pool.Handle
}

func (ArrivePilot) state(s *System) *arriveState {
	st, _ := s.PilotState.(*arriveState)
	if st == nil {
		st = &arriveState{moveGround: orders.Lookup("Move_Ground"), standby: orders.Lookup("Standby")}
		s.PilotState = st
	}
	if s.Terrain != nil {
		if n := int(s.Terrain.CellW) * int(s.Terrain.CellH); len(st.claim) != n {
			st.claim = make([]uint32, n)
			st.resv = make([]pool.Handle, n)
			st.gen = 0
		}
	}
	return st
}

func (st *arriveState) row(h pool.Handle) *arriveRow {
	st.rows = growHandleRow(st.rows, int(h))
	return &st.rows[h]
}

// nextGen starts a fresh claim generation.
func (st *arriveState) nextGen() uint32 {
	st.gen++
	if st.gen == 0 {
		clear(st.claim)
		st.gen = 1
	}
	return st.gen
}

func (st *arriveState) claimRect(s *System, ax, az, fx, fz int32) {
	w, h := s.Terrain.CellW, s.Terrain.CellH
	for z := max(az, 0); z < min(az+fz, h); z++ {
		for x := max(ax, 0); x < min(ax+fx, w); x++ {
			st.claim[z*w+x] = st.gen
		}
	}
}

func (st *arriveState) reserve(s *System, h pool.Handle, ax, az, fx, fz int32) {
	w, hgt := s.Terrain.CellW, s.Terrain.CellH
	for z := max(az, 0); z < min(az+fz, hgt); z++ {
		for x := max(ax, 0); x < min(ax+fx, w); x++ {
			st.resv[z*w+x] = h
		}
	}
}

// reservedByOther reports whether a live order of a unit other than self
// reserves cell (x, z).
func (st *arriveState) reservedByOther(s *System, x, z int32, self pool.Handle) bool {
	h := st.resv[z*s.Terrain.CellW+x]
	if h == 0 || h == self {
		return false
	}
	r := handleRow(st.rows, h)
	if r.node == nil || x < r.place.X || z < r.place.Z || x >= r.place.X+r.fx || z >= r.place.Z+r.fz {
		return false
	}
	u := s.world.Unit(h)
	if u == nil || !u.Alive {
		return false
	}
	q := orders.QueueOfUnit(u)
	return q != nil && q.Head() == r.node
}

// arriveGroundMover reports whether u is a finished grounded mobile unit a
// move order drives.
func arriveGroundMover(u *units.Unit) bool {
	return u != nil && u.Alive && !u.Dying && u.Def != nil && !u.Def.CanFly && u.Def.CanMove && u.Def.BMCode == 1 &&
		u.Remaining == 0 && u.Attachment.Carrier == 0
}

// parked reports whether unit id stands and will stay: a building, or a unit
// at rest holding no route, waiting for none and with nothing to do.
func (st *arriveState) parked(s *System, id int) bool {
	coll := handleRow(s.Collisions, pool.Handle(id))
	if coll == nil || coll.Building {
		return true
	}
	if coll.Speed != 0 {
		return false
	}
	if r := handleRow(s.Routes, pool.Handle(id)); r != nil && r.Active && r.Count > 1 {
		return false
	}
	if s.HasPathRequest(pool.Handle(id)) {
		return false
	}
	u := s.world.Unit(pool.Handle(id))
	if u == nil {
		return true
	}
	q := orders.QueueOfUnit(u)
	if q == nil {
		return true
	}
	head := q.Head()
	return head == nil || (head.Flags&orders.FlagAutoOp != 0 && head.ID == st.standby)
}

// BeginTick finds the plain moves ordered since the last tick, before any of
// them installs its goal, and gives each a place.
func (p ArrivePilot) BeginTick(s *System, tick uint32) {
	if s.world == nil || s.Terrain == nil {
		return
	}
	st := p.state(s)
	st.live = s.world.AppendLive(st.live[:0])
	st.fresh = st.fresh[:0]
	for _, u := range st.live {
		if !arriveGroundMover(u) {
			continue
		}
		q := orders.QueueOfUnit(u)
		if q == nil {
			continue
		}
		n := q.Head()
		if n == nil || n.ID != st.moveGround || n.Target != 0 || n.IsPatrolReturn() {
			continue
		}
		row := st.row(u.Handle)
		if row.seen == n {
			continue
		}
		if n.Phase != 0 || n.CreationTick+1 < tick {
			// A move first seen under way, as the computer player's are
			// and a move that waited behind another order is: the goal it
			// has is its place.
			p.adopt(s, st, u.Handle, n)
			continue
		}
		row.seen = n
		st.fresh = append(st.fresh, arriveFresh{h: u.Handle, owner: u.Owner, x: n.GoalX, z: n.GoalZ, node: n})
	}
	if len(st.fresh) == 0 {
		return
	}
	sort.Slice(st.fresh, func(i, j int) bool {
		a, b := &st.fresh[i], &st.fresh[j]
		if a.owner != b.owner {
			return a.owner < b.owner
		}
		if a.x != b.x {
			return a.x < b.x
		}
		if a.z != b.z {
			return a.z < b.z
		}
		return a.h < b.h
	})
	learned := s.rules().LearnedTerrain(s)
	for i := 0; i < len(st.fresh); {
		j := i + 1
		for j < len(st.fresh) && st.fresh[j].owner == st.fresh[i].owner && st.fresh[j].x == st.fresh[i].x && st.fresh[j].z == st.fresh[i].z {
			j++
		}
		run := st.fresh[i:j]
		switch {
		case p.Places && len(run) > 1:
			p.placeBlock(s, st, run, learned)
		case p.Places:
			p.placeSingle(s, st, run[0].h, run[0].node, learned)
		default:
			p.adopt(s, st, run[0].h, run[0].node)
		}
		i = j
	}
}

// footOf is a unit's footprint, at least one cell each way.
func (s *System) footOf(h pool.Handle) (int32, int32) {
	if coll := handleRow(s.Collisions, h); coll != nil {
		return max(int32(coll.FootPrintX), 1), max(int32(coll.FootPrintZ), 1)
	}
	return 1, 1
}

// adopt makes the order's goal the unit's place and reserves it.
func (ArrivePilot) adopt(s *System, st *arriveState, h pool.Handle, n *orders.Node) {
	row := st.row(h)
	row.seen, row.node = n, n
	row.x, row.z = n.GoalX, n.GoalZ
	row.fx, row.fz = s.footOf(h)
	row.place = Cell{X: goalCellForWorld(n.GoalX, row.fx), Z: goalCellForWorld(n.GoalZ, row.fz)}
	row.nextCheck, row.exchanges = 0, 0
	row.bestD, row.bestAt = -1, 0
	row.stood = false
	st.reserve(s, h, row.place.X, row.place.Z, row.fx, row.fz)
}

// setGoal writes a place into an order that has not installed its goal yet.
func setGoal(n *orders.Node, ax, az, fx, fz int32) {
	n.GoalX = numeric.Fixed(int64(ax)<<20 + int64(fx)<<19)
	n.GoalZ = numeric.Fixed(int64(az)<<20 + int64(fz)<<19)
}

// GroupOrdered adopts the places the command boundary gave a group.
func (p ArrivePilot) GroupOrdered(s *System, owner uint8, members []pool.Handle, x, z numeric.Fixed, tick uint32) {
	if s.world == nil || s.Terrain == nil {
		return
	}
	st := p.state(s)
	for _, h := range members {
		u := s.world.Unit(h)
		if !arriveGroundMover(u) {
			continue
		}
		q := orders.QueueOfUnit(u)
		if q == nil {
			continue
		}
		if n := q.Head(); n != nil && n.ID == st.moveGround && n.Phase == 0 && n.Target == 0 && !n.IsPatrolReturn() {
			p.adopt(s, st, h, n)
		}
	}
}

// placeSingle gives a unit sent alone the nearest free footprint to its goal
// when the goal's own is not free.
func (p ArrivePilot) placeSingle(s *System, st *arriveState, h pool.Handle, n *orders.Node, learned *LearnedTerrain) {
	u := s.world.Unit(h)
	fx, fz := s.footOf(h)
	if u != nil {
		st.nextGen()
		ax, az := goalCellForWorld(n.GoalX, fx), goalCellForWorld(n.GoalZ, fz)
		if bx, bz, ok := p.nearestFree(s, st, u, ax, az, fx, fz, learned); ok && (bx != ax || bz != az) {
			setGoal(n, bx, bz, fx, fz)
		}
	}
	p.adopt(s, st, h, n)
}

// placeBlock gives each unit of run, all ordered to one point, a place of
// its own and writes it into the unit's order before the order installs it.
func (p ArrivePilot) placeBlock(s *System, st *arriveState, run []arriveFresh, learned *LearnedTerrain) {
	st.members = st.members[:0]
	var cx, cz int64
	maxFoot := int32(1)
	gen := st.nextGen()
	for _, f := range run {
		u := s.world.Unit(f.h)
		if u == nil {
			continue
		}
		fx, fz := s.footOf(f.h)
		maxFoot = max(maxFoot, fx, fz)
		ux, uz := int64(u.X)>>16, int64(u.Z)>>16
		st.members = append(st.members, arriveMember{u: u, n: f.node, fx: fx, fz: fz, sx: ux, sz: uz})
		st.row(f.h).member = gen
		cx += ux
		cz += uz
	}
	n := int64(len(st.members))
	if n == 0 {
		return
	}
	cx /= n
	cz /= n
	gx, gz := int64(run[0].x)>>16, int64(run[0].z)>>16
	dx, dz := gx-cx, gz-cz
	if dx == 0 && dz == 0 {
		dz = -1
	}
	alongX := absInt64(dx) >= absInt64(dz)
	for i := range st.members {
		m := &st.members[i]
		m.depth = (m.sx-cx)*dx + (m.sz-cz)*dz
		if alongX {
			m.lat = m.sz
		} else {
			m.lat = m.sx
		}
	}
	// Front first; ties across the way, then by handle.
	sort.Slice(st.members, func(i, j int) bool {
		a, b := &st.members[i], &st.members[j]
		if a.depth != b.depth {
			return a.depth > b.depth
		}
		if a.lat != b.lat {
			return a.lat < b.lat
		}
		return a.u.Handle < b.u.Handle
	})
	pitch := int64(maxFoot)
	cols := isqrtInt64Arrive(n-1) + 1
	rows := (n + cols - 1) / cols
	sgn := int64(1)
	if (alongX && dx < 0) || (!alongX && dz < 0) {
		sgn = -1
	}
	for r := int64(0); r < rows; r++ {
		lo, hi := r*cols, min((r+1)*cols, n)
		seg := st.members[lo:hi]
		sort.SliceStable(seg, func(i, j int) bool {
			if seg[i].lat != seg[j].lat {
				return seg[i].lat < seg[j].lat
			}
			return seg[i].u.Handle < seg[j].u.Handle
		})
		k := hi - lo
		for j := range seg {
			m := &seg[j]
			// Offsets in world units from the point: the far row first.
			depthOff := sgn * ((rows - 1) - 2*r) * pitch * 8
			latOff := (2*int64(j) - (k - 1)) * pitch * 8
			px, pz := gx+latOff, gz+depthOff
			if alongX {
				px, pz = gx+depthOff, gz+latOff
			}
			ax := goalCellForWorld(numeric.Fixed(px<<16), m.fx)
			az := goalCellForWorld(numeric.Fixed(pz<<16), m.fz)
			if bx, bz, ok := p.nearestFree(s, st, m.u, ax, az, m.fx, m.fz, learned); ok {
				m.bx, m.bz, m.placed = bx, bz, true
				st.claimRect(s, bx, bz, m.fx, m.fz)
			}
		}
	}
	s.arriveUncross(st.members, learned)
	for i := range st.members {
		m := &st.members[i]
		if m.placed {
			setGoal(m.n, m.bx, m.bz, m.fx, m.fz)
		}
		p.adopt(s, st, m.u.Handle, m.n)
	}
}

// arriveUncross swaps the places of two members of one footprint whose
// straight lines cross and whose new places each remain passable as their
// owner knows the ground. A shared footprint need not mean shared terrain
// admission. Each swap shortens the lines' summed length, so the passes
// settle; they are bounded all the same.
func (s *System) arriveUncross(ms []arriveMember, learned *LearnedTerrain) {
	centre := func(m *arriveMember) (int64, int64) {
		return int64(m.bx)*16 + int64(m.fx)*8, int64(m.bz)*16 + int64(m.fz)*8
	}
	passable := func(m *arriveMember, x, z int32) bool {
		return s.slotPassable(s.existingLayer(m.u.Handle), s.slotMappingWord(m.u), learned,
			s.ProfileFor(m.u.Handle), m.u.Owner, x, z, m.fx, m.fz)
	}
	for pass := 0; pass < arriveUncrossPasses; pass++ {
		swapped, pairs := false, 0
		for i := range ms {
			a := &ms[i]
			if !a.placed {
				continue
			}
			for j := i + 1; j < len(ms) && pairs < arriveUncrossPairs; j++ {
				b := &ms[j]
				if !b.placed || a.fx != b.fx || a.fz != b.fz {
					continue
				}
				pairs++
				ax, az := centre(a)
				bx, bz := centre(b)
				if segmentsCross(a.sx, a.sz, ax, az, b.sx, b.sz, bx, bz) &&
					passable(a, b.bx, b.bz) && passable(b, a.bx, a.bz) {
					a.bx, b.bx = b.bx, a.bx
					a.bz, b.bz = b.bz, a.bz
					swapped = true
				}
			}
		}
		if !swapped {
			return
		}
	}
}

// segmentsCross reports whether segments p1-p2 and q1-q2 cross at a point
// inside both.
func segmentsCross(p1x, p1z, p2x, p2z, q1x, q1z, q2x, q2z int64) bool {
	orient := func(ax, az, bx, bz, cx, cz int64) int {
		v := (bx-ax)*(cz-az) - (bz-az)*(cx-ax)
		switch {
		case v > 0:
			return 1
		case v < 0:
			return -1
		}
		return 0
	}
	o1 := orient(p1x, p1z, p2x, p2z, q1x, q1z)
	o2 := orient(p1x, p1z, p2x, p2z, q2x, q2z)
	o3 := orient(q1x, q1z, q2x, q2z, p1x, p1z)
	o4 := orient(q1x, q1z, q2x, q2z, p2x, p2z)
	return o1*o2 < 0 && o3*o4 < 0
}

// nearestFree finds the free footprint anchor nearest (ax, az), ring by ring
// to arriveSearchRadius cells. Free: the owner believes the ground passable,
// no cell is claimed in the current generation or reserved by another live
// order, and no parked unit outside the block being placed stands on it.
func (p ArrivePilot) nearestFree(s *System, st *arriveState, u *units.Unit, ax, az, fx, fz int32, learned *LearnedTerrain) (int32, int32, bool) {
	profile := s.ProfileFor(u.Handle)
	layer := s.existingLayer(u.Handle)
	mapping := s.slotMappingWord(u)
	w, hgt := s.Terrain.CellW, s.Terrain.CellH
	free := func(x0, z0 int32) bool {
		if x0 < 0 || z0 < 0 || x0+fx > w || z0+fz > hgt {
			return false
		}
		for z := z0; z < z0+fz; z++ {
			for x := x0; x < x0+fx; x++ {
				if st.claim[z*w+x] == st.gen || st.reservedByOther(s, x, z, u.Handle) {
					return false
				}
			}
		}
		if !s.slotPassable(layer, mapping, learned, profile, u.Owner, x0, z0, fx, fz) {
			return false
		}
		if s.Grid == nil {
			return true
		}
		for z := z0; z < z0+fz; z++ {
			for x := x0; x < x0+fx; x++ {
				occ, held := s.Grid.OccupantAt(Cell{X: x, Z: z})
				if !held || occ <= 0 || occ == int(u.Handle) {
					continue
				}
				if handleRow(st.rows, pool.Handle(occ)).member == st.gen {
					continue // a member of the block being placed: it will move
				}
				if st.parked(s, occ) {
					return false
				}
			}
		}
		return true
	}
	for r := int32(0); r <= arriveSearchRadius; r++ {
		best, bx, bz := int64(-1), int32(0), int32(0)
		for z := az - r; z <= az+r; z++ {
			for x := ax - r; x <= ax+r; x++ {
				if max(absInt32(x-ax), absInt32(z-az)) != r {
					continue
				}
				ddx, ddz := int64(x-ax), int64(z-az)
				d := ddx*ddx + ddz*ddz
				if (best < 0 || d < best) && free(x, z) {
					best, bx, bz = d, x, z
				}
			}
		}
		if best >= 0 {
			return bx, bz, true
		}
	}
	return 0, 0, false
}

// Visit looks, every few ticks, at the place of a unit near it and gives the
// unit another when its place is taken or cannot be reached.
func (p ArrivePilot) Visit(s *System, v *Visit) {
	if !p.Exchange || v.Head == nil || v.Head.IsPatrolReturn() || v.Unit == nil || s.Terrain == nil {
		return
	}
	st := p.state(s)
	u := v.Unit
	row := st.row(u.Handle)
	if row.node == nil || row.node != v.Head || v.Head.Phase == 0 {
		return
	}
	const near = int64(arriveNear)
	dx, dz := int64(u.X-row.x)>>16, int64(u.Z-row.z)>>16
	d := dx*dx + dz*dz
	if row.bestD < 0 || d+64 <= row.bestD {
		// Nearer by more than half a cell: progress.
		row.bestD, row.bestAt = d, v.Tick
	}
	if p.Sealed > 0 {
		x, z := int32(int64(u.X)>>16), int32(int64(u.Z)>>16)
		sx, sz := int64(x-row.stoodX), int64(z-row.stoodZ)
		if !row.stood || sx*sx+sz*sz > arriveStood*arriveStood {
			row.stoodX, row.stoodZ, row.stoodAt, row.stood = x, z, v.Tick, true
		}
	}
	if v.Tick < row.nextCheck {
		return
	}
	if d > near*near {
		if p.Sealed > 0 && v.Tick-row.stoodAt >= p.Sealed && v.Coll != nil && row.exchanges < arriveMaxExchanges {
			row.nextCheck = v.Tick + arriveCheckEvery
			if p.sealedOff(s, u, v.Head) {
				p.settle(s, st, u, v.Head, row, v.Coll.CachedAnchor.X, v.Coll.CachedAnchor.Z, v.Tick)
			}
		}
		return
	}
	row.nextCheck = v.Tick + arriveCheckEvery
	if row.exchanges >= arriveMaxExchanges || v.Coll == nil || v.Coll.CachedAnchor == row.place {
		return
	}
	occ, static, held := s.footprintHeld(int(u.Handle), v.Profile, row.place, int16(row.fx), int16(row.fz))
	taken := held && (static || st.parked(s, occ))
	var bx, bz int32
	switch {
	case taken:
		st.nextGen()
		var ok bool
		bx, bz, ok = p.nearestFree(s, st, u, row.place.X, row.place.Z, row.fx, row.fz, s.rules().LearnedTerrain(s))
		if !ok || (bx == row.place.X && bz == row.place.Z) {
			return
		}
	case p.Stuck > 0 && v.Tick-row.bestAt >= p.Stuck && (!p.StuckParked || p.walled(s, st, v)):
		// Free but out of reach for a while: the way in is held. The unit
		// settles where it stands, which is a footprint it holds.
		bx, bz = v.Coll.CachedAnchor.X, v.Coll.CachedAnchor.Z
	default:
		return
	}
	p.settle(s, st, u, v.Head, row, bx, bz, v.Tick)
}

// settle gives u's order the footprint anchored at (bx, bz) for its place.
func (p ArrivePilot) settle(s *System, st *arriveState, u *units.Unit, n *orders.Node, row *arriveRow, bx, bz int32, tick uint32) {
	setGoal(n, bx, bz, row.fx, row.fz)
	radius, _ := orders.MoveGroundGoalRadius(n)
	s.InstallPointGoal(orders.PointGoalRequest{Owner: u.Handle, Node: n, X: n.GoalX, Y: n.GoalY, Z: n.GoalZ, Radius: radius})
	row.x, row.z, row.place = n.GoalX, n.GoalZ, Cell{X: bx, Z: bz}
	row.exchanges++
	row.bestD, row.bestAt = -1, tick
	st.reserve(s, u.Handle, bx, bz, row.fx, row.fz)
}

// sealedOff reports whether the ground closes the goal of u's order off from
// where u stands: the sealed-goal probe of Modern unreachable moves, which
// reads every mobile unit as absent, asked at any distance from the ground
// nearest the goal.
func (p ArrivePilot) sealedOff(s *System, u *units.Unit, head *orders.Node) bool {
	_, goalCell, ok := s.pathCellsForOrder(u, head)
	if !ok {
		return false
	}
	fx, fz := s.pathFootprint(u)
	goal := s.goalForOrderWithFootprint(u, goalCell, head, fx, fz)
	if goal == nil {
		return false
	}
	return s.goalSealedFrom(u, goal, 1<<30)
}

// walled reports whether what keeps the visit's unit from its place will not
// move of itself.
func (p ArrivePilot) walled(s *System, st *arriveState, v *Visit) bool {
	if v.Route == nil || !v.Route.Active || v.Route.Count < 2 {
		return true
	}
	if v.Coll == nil || !v.Coll.Blocked {
		return false
	}
	return v.Coll.BlockerID < 0 || st.parked(s, v.Coll.BlockerID)
}

// Forget drops a unit's place.
func (p ArrivePilot) Forget(s *System, h pool.Handle) {
	if st, _ := s.PilotState.(*arriveState); st != nil && int(h) < len(st.rows) {
		st.rows[h] = arriveRow{}
	}
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// isqrtInt64Arrive is the floor square root of a non-negative value.
func isqrtInt64Arrive(v int64) int64 {
	if v <= 0 {
		return 0
	}
	x := v
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + v/x) / 2
	}
	return x
}
