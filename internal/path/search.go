package path

// Search is the deterministic ground path search on the TNT attribute-cell
// lattice [04 §7.1][04 R-PATH-01 §1–§11]. A Session retains the request
// between budget slices; a budget boundary never publishes a partial route.

const (
	CardinalCost    int32 = 16 // [04 §7.2]
	DiagonalCost    int32 = 22 // [04 §7.2]
	SteepCost       int32 = 30 // [04 R-PATH-01 §3]
	ShortRunPenalty int32 = 75 // [04 R-PATH-01 §3]
	ShortRunLimit         = 5  // [04 R-PATH-01 §3]
	// StartRun is the straight-run counter the seeded start node carries. It
	// is above ShortRunLimit, which is the whole mechanism that keeps the
	// short-run penalty off the first step [04 R-PATH-01 §4] step 11.
	StartRun uint16 = 100
)

var TurnPenaltyTable = [8]int32{0, 40, 60, 80, 100, 80, 60, 40} // [04 §7.2]

const (
	DirN    uint8 = 0
	DirNW   uint8 = 1
	DirW    uint8 = 2
	DirSW   uint8 = 3
	DirS    uint8 = 4
	DirSE   uint8 = 5
	DirE    uint8 = 6
	DirNE   uint8 = 7
	DirNone uint8 = 0xff
)

var dirDelta = [8]Cell{{0, -1}, {-1, -1}, {-1, 0}, {-1, 1}, {0, 1}, {1, 1}, {1, 0}, {1, -1}}

// IsDiagonal reports whether dir is one of the four diagonal sectors. The
// eight sectors alternate cardinal and diagonal from north, so the low bit
// decides [04 §7.2].
func IsDiagonal(dir uint8) bool { return dir != DirNone && dir&1 != 0 }

// StepCost is the per-step cost of moving one cell in dir: cardinal 16,
// diagonal 22 [04 §7.2].
func StepCost(dir uint8) int32 {
	if IsDiagonal(dir) {
		return DiagonalCost
	}
	return CardinalCost
}

// TurnPenalty is the extra cost of changing heading from prev to cur, indexed
// by the sector delta modulo eight [04 §7.2]. A step out of, or into, the
// no-direction sentinel costs nothing.
func TurnPenalty(prev, cur uint8) int32 {
	if prev == DirNone || cur == DirNone {
		return 0
	}
	return TurnPenaltyTable[(int(cur)-int(prev)+8)&7]
}

// ScaledHeuristic applies the per-request 16.16 heuristic weight to a raw
// heuristic. The product is formed at full signed width and shifted down, so
// it floors rather than truncating toward zero [04 §7.2] [I3].
func ScaledHeuristic(h, scale int32) int32 { return int32((int64(h) * int64(scale)) >> 16) }

// InBounds reports whether c lies inside bounds, both edges inclusive.
func InBounds(c Cell, bounds Rect) bool {
	return c.X >= bounds.Min.X && c.X <= bounds.Max.X && c.Z >= bounds.Min.Z && c.Z <= bounds.Max.Z
}

// Fan is one centered neighbour fan: the first Len entries of Cells and Dirs
// are the neighbours in expansion order. It is a value so that expanding a
// node costs no allocation; nine is the widest fan [04 §7.1].
type Fan struct {
	Cells [9]Cell
	Dirs  [9]uint8
	Len   int
}

// NeighborsForDir fills the centered fan. The first fan has nine entries, with
// the reverse direction repeated at both ends; later fans have five
// [04 §7.1].
//
// It returns the fan by value rather than two slices because it is called once
// per expanded node, and two heap allocations per node dominated a search.
// Order is unchanged: off runs from -width to +width about dir.
func NeighborsForDir(cur Cell, dir uint8, first bool) Fan {
	width := 2
	if first {
		width = 4
		if dir > 7 {
			dir = DirN
		}
	}
	var fan Fan
	for off := -width; off <= width; off++ {
		d := uint8((int(dir) + off + 8) & 7)
		fan.Cells[fan.Len] = Cell{cur.X + dirDelta[d].X, cur.Z + dirDelta[d].Z}
		fan.Dirs[fan.Len] = d
		fan.Len++
	}
	return fan
}

type rayResult struct {
	best     int32
	connects bool
	steps    int
}

// walkRay is the forward cardinal walk and alternating two-sided wall follow
// [04 R-PATH-01 §5][04 R-PATH-01 §15]. Its only product is the acceptance
// threshold: the minimum scaled heuristic over every cell it touched.
func walkRay(start, target Cell, passable func(Cell) uint8, goal Goal, scale int32, visit func(Cell, uint8, bool) bool) rayResult {
	r := rayResult{best: ScaledHeuristic(goal.H(start), scale)}
	cur := start
	if passable(start) == 0 {
		return r
	}
	// touch marks a cell as the ray does — touched bit, direction byte,
	// ray-visited bit — and reports the acceptable-terminal bit, which ends the
	// ray with a zero threshold [04 R-PATH-01 §5].
	touch := func(c Cell, d uint8) bool {
		if visit != nil && visit(c, d, false) {
			r.connects = true
			r.best = 0
			return true
		}
		return false
	}
	// fold takes a touched cell's scaled heuristic into the threshold. A
	// rejoin cell is never folded: the greedy walk resumes from it at once
	// [04 R-PATH-01 §15] step 5.
	fold := func(c Cell) {
		if h := ScaledHeuristic(goal.H(c), scale); h < r.best {
			r.best = h
		}
	}
	for {
		r.steps++ // the scheduler charge precedes the best-cost check [04 R-PATH-01 §5]
		if r.best == 0 || cur == target {
			if cur == target {
				r.best = 0
			}
			r.connects = cur == target || r.connects
			return r
		}
		var d uint8
		switch {
		case target.X < cur.X:
			d = DirW
		case target.X > cur.X:
			d = DirE
		case target.Z <= cur.Z:
			d = DirN
		default:
			d = DirS
		}
		next := Cell{cur.X + dirDelta[d].X, cur.Z + dirDelta[d].Z}
		if passable(next) != 0 {
			if touch(next, d) {
				return r
			}
			fold(next)
			cur = next
			continue
		}
		// WALL FOLLOW [04 R-PATH-01 §15]. Two cursors start on the cell the
		// walk was standing on: the upper cursor carries its actual probe
		// direction a and rotates upward; the lower carries a STORED probe s,
		// steps in the opposite sector and rotates downward. Both first
		// re-probe the blocked cardinal d. The ray ends when the cursors meet
		// (steps 3 and 8), when a sweep exhausts all eight sectors, or when a
		// touched cell carries the acceptable-terminal bit; it rejoins the
		// greedy walk when a cursor lands on the axis-aligned leg from hit to
		// the target.
		hit := cur
		posA, posB := hit, hit
		a, s := (d+2)&7, (d+2)&7
		departed := false
		rejoined := false
		for !rejoined {
			// One charge per cursor pair, however many blocked cells either
			// cursor probes [04 R-PATH-01 §15] step 1.
			r.steps++
			// Upper cursor: sentinel a-3, start a-2 (two sectors back toward
			// the wall it follows); +1 while blocked [04 R-PATH-01 §15] step 2.
			sentinel := (a + 5) & 7
			a = (a + 6) & 7
			candA := Cell{posA.X + dirDelta[a].X, posA.Z + dirDelta[a].Z}
			for passable(candA) == 0 {
				if a == sentinel {
					return r
				}
				a = (a + 1) & 7
				candA = Cell{posA.X + dirDelta[a].X, posA.Z + dirDelta[a].Z}
			}
			// Meet test: standing on the lower cursor's cell and about to
			// retrace its last step backwards [04 R-PATH-01 §15] step 3.
			if posA == posB && a == s && departed {
				return r
			}
			posA = candA
			departed = true
			if touch(candA, a) {
				return r
			}
			if onRayLeg(hit, candA, target) {
				cur = candA
				rejoined = true
				break
			}
			fold(candA)
			// Lower cursor: sentinel s+3, stored start s+2, actual step is the
			// opposite sector; -1 while blocked [04 R-PATH-01 §15] step 7.
			sentinel = (s + 3) & 7
			s = (s + 2) & 7
			candB := Cell{posB.X - dirDelta[s].X, posB.Z - dirDelta[s].Z}
			for passable(candB) == 0 {
				if s == sentinel {
					return r
				}
				s = (s + 7) & 7
				candB = Cell{posB.X - dirDelta[s].X, posB.Z - dirDelta[s].Z}
			}
			// Meet test: the upper cursor has stepped onto this cell while
			// the lower is about to step onto its old one [04 R-PATH-01 §15]
			// step 8.
			if posA == posB && a == s {
				return r
			}
			posB = candB
			// The lower cursor's direction byte is its STORED probe — the
			// reverse of the step it took [04 R-PATH-01 §15] step 9.
			if touch(candB, s) {
				return r
			}
			if onRayLeg(hit, candB, target) {
				cur = candB
				rejoined = true
				break
			}
			fold(candB)
		}
	}
}

func onRayLeg(hit, candidate, target Cell) bool {
	dx, dz := target.X-hit.X, target.Z-hit.Z
	cx, cz := candidate.X-hit.X, candidate.Z-hit.Z
	if dx < 0 {
		dx, cx = -dx, -cx
	}
	if dz < 0 {
		dz, cz = -dz, -cz
	}
	return (cz == 0 && 0 < cx && cx <= dx) || (cx == dx && 0 < cz && cz <= dz)
}

// SearchConfig is one route request: where the search starts, what goal it is
// trying to reach, the heuristic weight, the mover's footprint, an optional
// bounding rectangle, and the two callbacks the search reads the world
// through [04 §7.2] [04 R-PATH-01].
type SearchConfig struct {
	Start Cell
	Goal  Goal
	Scale int32
	// FootPrintX and FootPrintZ are the authored footprint dimensions used by
	// the established cell-to-world route conversion [04 R-PATH-01 §7].
	FootPrintX    int32
	FootPrintZ    int32
	HasBounds     bool
	Bounds        Rect
	PassableValue func(Cell) uint8
	Revise        func()
	// StartDir supplies the unit heading's quantized sector. Zero is north,
	// retaining the pre-existing API's zero-value behavior [04 R-PATH-01 §4].
	StartDir uint8
	// Workspace is the scheduler's per-cell table, offered to this search. It
	// is an optimization and never a behaviour: a search that is not given one,
	// or that is given one another search still holds, keeps its own map and
	// produces the same visit order and the same route [04 §7.2].
	Workspace *Workspace
}

// A completed search reports its route points, its terminating status, the
// status it notified along the way and the counters the scheduler charges work
// against; Session exposes each of them as it produces them, so no whole-search
// result record exists in package source.

type entry struct {
	status uint8
	dir    uint8
	node   NodeID
}

// Session is one search in progress. The scheduler drives it in bounded
// increments through Resume, so a search that exceeds a tick's work share
// continues on the next tick rather than being restarted [04 §7.3].
type Session struct {
	cfg          SearchConfig
	scale        int32
	entries      cellIndex
	goalSet      map[Cell]struct{}
	nearest      Cell
	nearestDist  int64
	haveNearest  bool
	tolerance    int32
	hasTolerance bool
	notified     Status
	seeded       bool
	done         bool
	resultPoints []Point
	resultStatus Status
	popped       int
	setupSteps   int
	ns           *NodeStore
	heap         Heap
	expanded     bool
}

// NewSession opens a search for cfg and seeds it. A zero Scale means the
// unweighted heuristic, 1.0 in 16.16.
func NewSession(cfg SearchConfig) *Session {
	s := &Session{cfg: cfg, entries: newMapIndex(), goalSet: make(map[Cell]struct{})}
	s.scale = cfg.Scale
	if s.scale == 0 {
		s.scale = 65536
	}
	s.cfg.Scale = s.scale
	// The scheduler's table, when it is free and this request is bounded
	// [04 §7.2]. A search that does not get it keeps the map and answers
	// identically; Release hands it back.
	if cfg.Workspace != nil {
		s.entries.bindWorkspace(cfg.Workspace, cfg.Bounds, cfg.HasBounds)
	}
	s.init()
	return s
}

func (s *Session) passValue(c Cell) uint8 {
	if s.cfg.HasBounds && !InBounds(c, s.cfg.Bounds) {
		return 0
	}
	if s.cfg.PassableValue != nil {
		return s.cfg.PassableValue(c)
	}
	return 0
}

func (s *Session) touch(c Cell, e entry) { s.entries.set(c, e) }

// Release hands the scheduler's per-cell table back. Every path that drops a
// session owes this call; a session that is dropped without it leaves the
// table lent, which costs the next search the table and nothing else.
func (s *Session) Release() {
	if s != nil {
		s.entries.release()
	}
}

func (s *Session) init() {
	if s.cfg.Revise != nil {
		s.cfg.Revise()
	}
	if s.cfg.Goal == nil {
		s.done, s.resultStatus, s.notified = true, StatusRejected, StatusRejected
		return
	}
	for _, c := range s.cfg.Goal.Enumerate(nil) {
		dx, dz := int64(c.X)-int64(s.cfg.Start.X), int64(c.Z)-int64(s.cfg.Start.Z)
		dist := dx*dx + dz*dz
		if !s.haveNearest || dist < s.nearestDist {
			s.nearest, s.nearestDist, s.haveNearest = c, dist, true
		}
		if !s.cfg.HasBounds || InBounds(c, s.cfg.Bounds) {
			s.touch(c, entry{status: 4, dir: DirNone})
			s.goalSet[c] = struct{}{}
		}
	}
	if s.cfg.Goal.StartSatisfied(s.cfg.Start) {
		s.done, s.resultStatus, s.notified = true, StatusAlreadySatisfied, StatusAlreadySatisfied
		return
	}
	if s.cfg.HasBounds && !InBounds(s.cfg.Start, s.cfg.Bounds) {
		s.done, s.resultStatus, s.notified = true, StatusRejected, StatusRejected
		return
	}
	startH := s.cfg.Goal.H(s.cfg.Start)
	startScaled := ScaledHeuristic(startH, s.scale)
	if s.haveNearest {
		r := walkRay(s.cfg.Start, s.nearest, s.passValue, s.cfg.Goal, s.scale, func(c Cell, d uint8, _ bool) bool {
			e := s.entries.get(c)
			goal := e.status&4 != 0
			e.status |= 8
			e.dir = d
			s.touch(c, e)
			return goal
		})
		s.setupSteps = r.steps
		s.tolerance, s.hasTolerance = r.best, true
		// The returned ray threshold, rather than the terminal connection bit,
		// drives the early notification. A zero threshold is the established
		// already-satisfied notification even when the ray did not touch a goal
		// cell [04 R-PATH-01 §4 step 9].
		if r.best == 0 {
			s.notified = StatusAlreadySatisfied
		} else {
			s.notified = StatusRejected
			if r.best >= startScaled {
				s.done, s.resultStatus = true, StatusRejected
				return
			}
		}
	}
	s.ns = newSessionNodeStore(s.scale, &s.entries)
	s.heap.Clear()
	startDir := s.cfg.StartDir
	if startDir > 7 {
		startDir = DirN
	}
	// g = 0, f = the start's scaled heuristic, run = 100. The stored terrain
	// term is deliberately left zero: the start cell is closed at its own pop
	// before any relaxation can reach it, so the field is never read
	// [04 R-PATH-01 §4] step 11.
	startID := s.ns.Alloc(s.cfg.Start, 0, startH, invalidNodeID, startDir)
	s.ns.Get(startID).Run = StartRun
	s.ns.SetOpen(startID, true)
	s.touch(s.cfg.Start, entry{status: 1, dir: startDir, node: startID})
	s.heap.Push(startID, s.ns.Get(startID).F)
	s.seeded = true
}

// Config returns the request this session was opened for.
func (s *Session) Config() SearchConfig { return s.cfg }

// Start returns the cell the search began from.
func (s *Session) Start() Cell { return s.cfg.Start }

// Goal returns the goal family the search is trying to satisfy.
func (s *Session) Goal() Goal { return s.cfg.Goal }

// Popped returns how many nodes the search has taken off the heap so far —
// the work the scheduler charges against the player's share [04 §7.3].
func (s *Session) Popped() int { return s.popped }

// SetupSteps returns the setup work performed before the first expansion.
func (s *Session) SetupSteps() int { return s.setupSteps }

// Notified returns the status the search reported while running, which is not
// necessarily the status it finishes with.
func (s *Session) Notified() Status { return s.notified }

// Seeded reports whether the start node reached the heap. A start the goal
// already satisfies, or one the world rejects, is never seeded.
func (s *Session) Seeded() bool { return s.seeded }

// IsDone reports whether the search has produced its final result.
func (s *Session) IsDone() bool { return s.done }

// Resume expands at most budget nodes and reports the route, the status and
// whether the search finished. A search that exhausts its budget without
// finishing returns false and keeps its state for the next call, which is how
// the scheduler spreads one search over several ticks [04 §7.3].
func (s *Session) Resume(budget int) ([]Point, Status, bool) {
	if s.done {
		return s.resultPoints, s.resultStatus, true
	}
	if !s.seeded {
		s.done, s.resultStatus = true, StatusRejected
		return nil, s.resultStatus, true
	}
	if budget <= 0 {
		return nil, 0, false
	}
	startPopped := s.popped
	for s.heap.HasCandidate() && s.popped-startPopped < budget {
		id, f, ok := s.heap.BeginExpand()
		if !ok {
			break
		}
		s.popped++
		n := s.ns.Get(id)
		if n.Closed || !n.Open || f != n.F {
			continue
		}
		// Terminal flags are consumed before closing; pop never probes the
		// class layer again [04 R-PATH-01 §1].
		e := s.entries.get(n.Cell)
		if e.status&4 != 0 {
			s.finish(reconstructRoute(s.cfg.Start, n.Cell, s.ns, routeFootPrint(s.cfg)))
			return s.resultPoints, s.resultStatus, true
		}
		n.Open, n.Closed = false, true
		e.status = 2
		s.touch(n.Cell, e)
		fan := NeighborsForDir(n.Cell, n.Dir, !s.expanded)
		s.expanded = true
		for i := 0; i < fan.Len; i++ {
			c, d := fan.Cells[i], fan.Dirs[i]
			e := s.entries.get(c)
			state := e.status & 3
			if state != 0 && state != 1 {
				continue
			}
			// Only untouched neighbors probe the live layer. An open node
			// keeps its stored terrain term on relaxation [04 R-PATH-01 §1].
			value := uint8(3)
			if state == 0 {
				value = s.passValue(c)
			}
			// A blocked cell is skipped unless the pre-search ray already
			// stepped onto it. One that carries the ray-visited bit is costed
			// like any other neighbour, keeping its probe value of 0: the
			// terrain term below is driven by the probe result itself, so a
			// blocked-but-ray-visited cell pays the steep tier, not nothing
			// [04 R-PATH-01 §3].
			if value == 0 && e.status&8 == 0 {
				e.status = (e.status &^ 3) | 3
				s.touch(c, e)
				continue
			}
			turn, step := TurnPenalty(n.Dir, d), StepCost(d)
			// terrainTerm = (passability > 1) ? 0 : 30 — blocked (0) and steep
			// (1) both pay 30; unexplored (2) and clear (3) pay nothing
			// [04 R-PATH-01 §3].
			terrain := int32(0)
			if value <= 1 {
				terrain = SteepCost
			}
			run := uint16(1)
			if d == n.Dir {
				run = n.Run + 1
			}
			// The short-run 75 is gated by the parent's straight-run counter
			// alone. No parent-identity test guards it: the start node is
			// seeded with run 100, which is what keeps the 75 off the first
			// step [04 R-PATH-01 §4] step 11.
			short := int32(0)
			if d != n.Dir && n.Run < ShortRunLimit {
				short = ShortRunPenalty
			}
			gNew := n.G + turn + step + terrain + short
			if e.node != invalidNodeID {
				// The first allocation owns the terrain term. A later
				// relaxation reuses it rather than probing/recomputing the
				// node's heuristic-side terrain field [04 R-PATH-01 §3].
				terrain = int32(s.ns.Get(e.node).TerrainTerm)
				gNew = n.G + turn + step + terrain + short
				if state == 1 && s.ns.TryRelax(e.node, gNew, id, d) {
					node := s.ns.Get(e.node)
					node.Run = run
					s.heap.Fix(e.node, node.F)
				}
				continue
			}
			nid := s.ns.Alloc(c, gNew, s.cfg.Goal.H(c), id, d)
			node := s.ns.Get(nid)
			node.Run, node.TerrainTerm, node.Open = run, uint16(terrain), true
			s.heap.Open(nid, node.F)
			// Opening ORs the state into the existing flags, preserving both
			// ray visitation and enumerated terminals [04 R-PATH-01 §1].
			e = entry{status: e.status | 1, dir: d, node: nid}
			hs := ScaledHeuristic(node.H, s.scale)
			if s.hasTolerance && hs <= s.tolerance {
				e.status |= 4
			}
			if s.isGoal(c) {
				e.status |= 4
			}
			s.touch(c, e)
		}
	}
	if !s.heap.HasCandidate() {
		s.done, s.resultStatus = true, StatusRejected
		return nil, s.resultStatus, true
	}
	return nil, 0, false
}

func (s *Session) isGoal(c Cell) bool {
	_, ok := s.goalSet[c]
	return ok
}

func (s *Session) finish(points []Point) {
	s.done, s.resultPoints, s.resultStatus = true, points, 0
}

func routeFootPrint(cfg SearchConfig) Point {
	return Point{X: cfg.FootPrintX, Z: cfg.FootPrintZ}
}

func reconstructRoute(start, goal Cell, ns *NodeStore, footprint Point) []Point {
	goalID, ok := ns.Find(goal)
	if !ok {
		return nil
	}
	var ring [64]Cell
	ring[0] = goal
	d := ns.Get(goalID).Dir
	n := 1
	curID, cur := goalID, goal
	for cur != start {
		node := ns.Get(curID)
		d2 := node.Dir
		if d2 != d {
			ring[n&63] = cur
			n++
			d = d2
		}
		if d > 7 {
			return nil
		}
		cur.X -= dirDelta[d].X
		cur.Z -= dirDelta[d].Z
		parent, exists := ns.Find(cur)
		if !exists {
			return nil
		}
		curID = parent
	}
	ring[n&63] = start
	n++
	count := n
	if count > 64 {
		count = 64
	}
	points := make([]Point, count)
	for i := range points {
		c := ring[(n-1-i)&63]
		points[i] = worldPoint(c, footprint)
	}
	return points
}

func worldPoint(c Cell, footprint Point) Point {
	// The int16 conversion is part of the route representation, before the
	// footprint offset and final factor of eight [04 R-PATH-01 §7].
	x := int32(int16(c.X*2)) + footprint.X
	z := int32(int16(c.Z*2)) + footprint.Z
	return Point{X: x * 8, Z: z * 8}
}
