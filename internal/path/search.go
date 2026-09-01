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

func IsDiagonal(dir uint8) bool { return dir != DirNone && dir&1 != 0 }

func StepCost(dir uint8) int32 {
	if IsDiagonal(dir) {
		return DiagonalCost
	}
	return CardinalCost
}

func TurnPenalty(prev, cur uint8) int32 {
	if prev == DirNone || cur == DirNone {
		return 0
	}
	return TurnPenaltyTable[(int(cur)-int(prev)+8)&7]
}

func ScaledHeuristic(h, scale int32) int32 { return int32((int64(h) * int64(scale)) >> 16) }

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

func opposite(dir uint8) uint8 { return (dir + 4) & 7 }

// walkRay is the forward cardinal walk and alternating two-sided wall follow
// [04 R-PATH-01 §5]. Each side records its (cell,direction) states, giving the
// exact cycle termination without an iteration guess.
func walkRay(start, target Cell, passable func(Cell) uint8, goal Goal, scale int32, visit func(Cell, uint8, bool) bool) rayResult {
	r := rayResult{best: ScaledHeuristic(goal.H(start), scale)}
	cur := start
	if passable(start) == 0 {
		return r
	}
	mark := func(c Cell, d uint8) bool {
		if visit != nil && visit(c, d, false) {
			r.connects = true
			r.best = 0
			return true
		}
		h := ScaledHeuristic(goal.H(c), scale)
		if h < r.best {
			r.best = h
		}
		return false
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
			if mark(next, d) {
				return r
			}
			cur = next
			continue
		}
		upPos, downPos := cur, cur
		// The blocked cardinal candidate was already charged above. The lower
		// cursor's first actual probe is the next sector downward; it stores the
		// negated direction because its steps subtract the probe [04 R-PATH-01 §5].
		upDir, downDir := (d+1)&7, opposite((d+7)&7)
		upOrigin, downOrigin := upPos, downPos
		upOriginDir, downOriginDir := upDir, downDir
		upState := raySideState{}
		downState := raySideState{}
		for {
			if r.best == 0 {
				return r
			}
			if !raySideStep(&upPos, &upDir, false, upOrigin, upOriginDir, &upState, passable, &r, mark) {
				return r
			}
			if upPos != cur && onRayLeg(cur, upPos, target) {
				cur = upPos
				break
			}
			if !raySideStep(&downPos, &downDir, true, downOrigin, downOriginDir, &downState, passable, &r, mark) {
				return r
			}
			if downPos != cur && onRayLeg(cur, downPos, target) {
				cur = downPos
				break
			}
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

type raySideState struct {
	departed     bool
	blockedSweep uint8
}

func raySideStep(pos *Cell, probe *uint8, lower bool, origin Cell, originDir uint8, state *raySideState, passable func(Cell) uint8, result *rayResult, mark func(Cell, uint8) bool) bool {
	if state.departed && *pos == origin && *probe == originDir {
		return false
	}
	d := *probe
	if lower {
		d = opposite(d)
	}
	next := Cell{pos.X + dirDelta[d].X, pos.Z + dirDelta[d].Z}
	result.steps++
	if passable(next) == 0 {
		state.blockedSweep++
		if state.blockedSweep == 8 {
			return false
		}
		if lower {
			*probe = (*probe + 7) & 7
		} else {
			*probe = (*probe + 1) & 7
		}
		return true
	}
	state.departed = state.departed || *pos != origin || *probe != originDir
	state.blockedSweep = 0
	*pos = next
	if mark(next, d) {
		return false
	}
	if lower {
		// The lower cursor stores the negated probe. A successful step advances
		// that stored direction by two sectors before the next alternating probe
		// [04 R-PATH-01 §5].
		*probe = (*probe + 2) & 7
	} else {
		// TODO(question): The preserved upper-success update is -1. A coordinated
		// trace of upper initialization, stored-versus-actual direction, successful
		// transition, and origin-repeat state must settle it; changing this update
		// alone breaks the established wall-rejoin fixtures [04 R-PATH-01 §5].
		*probe = (*probe + 7) & 7
	}
	return true
}

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
}

type SearchResult struct {
	Points     []Point
	Status     Status
	Notified   Status
	Popped     int
	SetupSteps int
	Seeded     bool
}

type entry struct {
	status uint8
	dir    uint8
	node   NodeID
}

type Session struct {
	cfg          SearchConfig
	scale        int32
	entries      map[Cell]entry
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
	goalFlag     map[NodeID]bool
	expanded     bool
}

func NewSession(cfg SearchConfig) *Session {
	s := &Session{cfg: cfg, entries: make(map[Cell]entry), goalSet: make(map[Cell]struct{})}
	s.scale = cfg.Scale
	if s.scale == 0 {
		s.scale = 65536
	}
	s.cfg.Scale = s.scale
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

func (s *Session) passable(c Cell) bool  { return s.passValue(c) != 0 }
func (s *Session) touch(c Cell, e entry) { s.entries[c] = e }

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
			e := s.entries[c]
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
	s.ns = NewNodeStore(s.scale)
	s.heap.Clear()
	s.goalFlag = make(map[NodeID]bool)
	startDir := s.cfg.StartDir
	if startDir > 7 {
		startDir = DirN
	}
	startID := s.ns.Alloc(s.cfg.Start, 0, startH, invalidNodeID, startDir)
	s.ns.Get(startID).Run = 100
	s.ns.SetOpen(startID, true)
	s.touch(s.cfg.Start, entry{status: 1, dir: startDir, node: startID})
	s.heap.Push(startID, s.ns.Get(startID).F)
	s.seeded = true
}

func (s *Session) Config() SearchConfig { return s.cfg }
func (s *Session) Start() Cell          { return s.cfg.Start }
func (s *Session) Goal() Goal           { return s.cfg.Goal }
func (s *Session) Popped() int          { return s.popped }
func (s *Session) SetupSteps() int      { return s.setupSteps }
func (s *Session) Notified() Status     { return s.notified }
func (s *Session) Seeded() bool         { return s.seeded }
func (s *Session) IsDone() bool         { return s.done }

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
	for s.heap.Len() > 0 && s.popped-startPopped < budget {
		id, f, ok := s.heap.Pop()
		if !ok {
			break
		}
		s.popped++
		n := s.ns.Get(id)
		if n.Closed || !n.Open || f != n.F {
			continue
		}
		// Class-layer revisions can change while this working set spans ticks.
		// Revalidate an existing open node at expansion time instead of trusting
		// the value captured when it entered the heap [04 §7.4].
		e := s.entries[n.Cell]
		if s.passValue(n.Cell) == 0 && e.status&8 == 0 {
			n.Open, n.Closed = false, true
			e.status = (e.status &^ 3) | 3
			s.touch(n.Cell, e)
			continue
		}
		n.Open, n.Closed = false, true
		e.status = (e.status &^ 3) | 2
		s.touch(n.Cell, e)
		if e.status&4 != 0 || s.isGoal(n.Cell) {
			s.finish(reconstructRoute(s.cfg.Start, n.Cell, s.ns, routeFootPrint(s.cfg)))
			return s.resultPoints, s.resultStatus, true
		}
		fan := NeighborsForDir(n.Cell, n.Dir, !s.expanded)
		s.expanded = true
		for i := 0; i < fan.Len; i++ {
			c, d := fan.Cells[i], fan.Dirs[i]
			e := s.entries[c]
			value := s.passValue(c)
			state := e.status & 3
			if value == 0 {
				if e.status&8 == 0 {
					e.status = (e.status &^ 3) | 3
					s.touch(c, e)
					continue
				}
				value = 3
			}
			if state != 0 && state != 1 {
				continue
			}
			turn, step := TurnPenalty(n.Dir, d), StepCost(d)
			terrain := int32(0)
			if value <= 1 {
				terrain = SteepCost
			}
			run := uint16(1)
			if d == n.Dir {
				run = n.Run + 1
			}
			short := int32(0)
			if d != n.Dir && n.Parent != invalidNodeID && n.Run < ShortRunLimit {
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
			s.heap.Push(nid, node.F)
			e = entry{status: 1, dir: d, node: nid}
			hs := ScaledHeuristic(node.H, s.scale)
			if s.hasTolerance && hs <= s.tolerance {
				e.status |= 4
				s.goalFlag[nid] = true
			}
			if s.isGoal(c) {
				e.status |= 4
				s.goalFlag[nid] = true
			}
			s.touch(c, e)
		}
	}
	if s.heap.Len() == 0 {
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

func Search(cfg SearchConfig) SearchResult {
	s := NewSession(cfg)
	if !s.done {
		s.Resume(1 << 30)
	}
	return SearchResult{Points: s.resultPoints, Status: s.resultStatus, Notified: s.notified, Popped: s.popped, SetupSteps: s.setupSteps, Seeded: s.seeded}
}

func straightRunLen(ns *NodeStore, id NodeID) int {
	if id == invalidNodeID {
		return 0
	}
	if run := ns.Get(id).Run; run != 0 {
		return int(run)
	}
	n := ns.Get(id)
	if n.Dir == DirNone {
		return 0
	}
	count := 1
	for parent := n.Parent; parent != invalidNodeID; parent = ns.Get(parent).Parent {
		if ns.Get(parent).Dir != n.Dir {
			break
		}
		count++
	}
	return count
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
