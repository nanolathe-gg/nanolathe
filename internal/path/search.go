package path

// Search core for weighted A* on the TNT attribute-cell lattice [04 §7.1][04 §7.2].
//
// Lattice: one cell = 16 map pixels per cell [04 §7.1] C1. Waypoints come from
// cell coordinates plus the profile's half-footprint bias [04 §7.1] C1.
// Neighbor visit order north, northwest, west, southwest, south, southeast,
// east, northeast [04 §7.1] C2. First expansion attempts nine entries (all eight
// plus one harmless duplicate); later expansions use a directed five-entry
// fan centered on the parent travel direction [04 §7.1] C2.
// Diagonal steps check ONLY the destination cell [04 §7.1] C3.
// Costs: cardinal 16, diagonal 22 [04 §7.2] C4; turn penalties
// 0,40,60,80,100,80,60,40 by directional difference [04 §7.2] C4; initial
// penalty 30 while heap holds at most one entry [04 §7.2] C4; short-run
// penalty 75 when parent chain straight run <5 and parent exists [04 §7.2] C4.
// Heuristic scaling hScaled = (h*scale)>>16 with signed 64-bit product and
// arithmetic shift, no float [04 §7.2] C6. h evaluated once per allocated node
// [04 §7.2] C7, relaxation adjusts f by g delta alone [04 §7.2] C7.
// Arrival tolerance is write-once threshold via greedy bidirectional ray walk
// storing minimum scaled h on frontier into slot never updated [04 §7.2] C9;
// opened neighbor at or below it gets open-plus-goal status, popping such node
// terminates and reconstructs [04 §7.2] C9. Early exits in order
// start-satisfies (0x100), OOB start (0x200), ray connects (0x100 notify but run),
// ray best >= start (0x200 without seeding) [04 §7.2] C10.
// Passability injected func(cell) bool, OOB = impassable [04 §7.1] C10.
// Reconstruction uses predecessor chain via 64-entry ring at index&63 each time
// direction changes, then start, emission walks masked indices downward
// [04 §7.3] C13. Path must not import movement to avoid cycle; reconstruction
// is implemented locally with same semantics as movement.ReconstructRoute [04 §7.3] C13.

// [04 §7.2] C4 cardinal/diagonal costs — the two corrected constants of PLAN_07.
const (
	CardinalCost      int32 = 16 // [04 §7.2] cardinal 16 (corrected)
	DiagonalCost      int32 = 22 // [04 §7.2] diagonal 22 (corrected)
	InitialPenalty    int32 = 30 // [04 §7.2] fixed initial penalty while heap <=1
	ShortRunPenalty   int32 = 75 // [04 §7.2] short-run penalty when straight run <5
	ShortRunThreshold       = 5  // [04 §7.2] threshold for short-run check
)

// [04 §7.2] C4 direction-change table by absolute directional difference.
// Index is raw directional difference 0..7 (mod 8), values symmetric.
var TurnPenaltyTable = [8]int32{0, 40, 60, 80, 100, 80, 60, 40} // [04 §7.2]

// Dir constants in retail neighbor visit order [04 §7.1] C2.
const (
	DirN    uint8 = 0 // north (0,-1) [04 §7.1]
	DirNW   uint8 = 1 // northwest (-1,-1) [04 §7.1]
	DirW    uint8 = 2 // west (-1,0) [04 §7.1]
	DirSW   uint8 = 3 // southwest (-1,1) [04 §7.1]
	DirS    uint8 = 4 // south (0,1) [04 §7.1]
	DirSE   uint8 = 5 // southeast (1,1) [04 §7.1]
	DirE    uint8 = 6 // east (1,0) [04 §7.1]
	DirNE   uint8 = 7 // northeast (1,-1) [04 §7.1]
	DirNone uint8 = 0xFF
)

// dirDelta maps Dir 0..7 to cell offset in visit order [04 §7.1] C2.
var dirDelta = [8]Cell{
	{0, -1},  // DirN
	{-1, -1}, // DirNW
	{-1, 0},  // DirW
	{-1, 1},  // DirSW
	{0, 1},   // DirS
	{1, 1},   // DirSE
	{1, 0},   // DirE
	{1, -1},  // DirNE
}

// IsDiagonal reports whether dir is a diagonal step [04 §7.2] C4.
func IsDiagonal(dir uint8) bool {
	return dir != DirNone && dir%2 == 1
}

// StepCost returns the base step cost for dir [04 §7.2] C4.
func StepCost(dir uint8) int32 {
	if IsDiagonal(dir) {
		return DiagonalCost
	}
	return CardinalCost
}

// TurnPenalty returns the direction-change penalty for moving from prev to cur [04 §7.2] C4.
// If prev is DirNone (start), penalty is 0.
func TurnPenalty(prev, cur uint8) int32 {
	if prev == DirNone || cur == DirNone {
		return 0
	}
	diff := int(cur) - int(prev)
	if diff < 0 {
		diff += 8
	}
	// diff now 0..7 raw difference [04 §7.2]
	return TurnPenaltyTable[diff]
}

// ScaledHeuristic computes hScaled = (h*scale)>>16 with signed 64-bit product and arithmetic shift [04 §7.2] C6.
// No floating point anywhere in the search [04 §7.2] C6.
func ScaledHeuristic(h, scale int32) int32 {
	return int32((int64(h) * int64(scale)) >> 16)
}

// straightRunLen returns the length of the straight run ending at id [04 §7.2] C4.
// It counts consecutive nodes with same Dir as id's Dir, walking parents.
func straightRunLen(ns *NodeStore, id NodeID) int {
	if id == invalidNodeID {
		return 0
	}
	n := ns.Get(id)
	if n.Dir == DirNone {
		return 0
	}
	dir := n.Dir
	count := 1
	pid := n.Parent
	for pid != invalidNodeID {
		pn := ns.Get(pid)
		if pn.Dir != dir {
			break
		}
		count++
		if count >= 64 {
			break
		}
		pid = pn.Parent
	}
	return count
}

// InBounds reports whether cell lies within bounds inclusive [04 §7.1] C10.
// Bounds is the TNT attribute-cell lattice extent.
func InBounds(c Cell, bounds Rect) bool {
	return c.X >= bounds.Min.X && c.X <= bounds.Max.X && c.Z >= bounds.Min.Z && c.Z <= bounds.Max.Z
}

// isPassableWithBounds wraps injected passability with OOB = impassable [04 §7.1] C10.
func isPassableWithBounds(c Cell, isPassable func(Cell) bool, hasBounds bool, bounds Rect) bool {
	if hasBounds && !InBounds(c, bounds) {
		return false
	}
	if isPassable == nil {
		return true
	}
	return isPassable(c)
}

// neighborsFirst returns the nine-entry first expansion (eight plus harmless duplicate) [04 §7.1] C2.
// The duplicate is DirN (north) appended at the end — harmless because the second visit finds the node already open.
func neighborsFirst(cur Cell) []Cell {
	// 9 entries: visit order N,NW,W,SW,S,SE,E,NE plus duplicate N [04 §7.1] C2
	out := make([]Cell, 0, 9)
	for i := 0; i < 8; i++ {
		d := dirDelta[i]
		out = append(out, Cell{X: cur.X + d.X, Z: cur.Z + d.Z})
	}
	// duplicate N
	out = append(out, Cell{X: cur.X + dirDelta[DirN].X, Z: cur.Z + dirDelta[DirN].Z})
	return out
}

// NeighborsForDir returns the expansion fan for a given parent travel direction [04 §7.1] C2.
// isFirst true -> nine entries wide; false -> five-entry fan centered on dir [04 §7.1] C2.
// Exposed for tests to lock fan shape without running full search.
func NeighborsForDir(cur Cell, dir uint8, isFirst bool) ([]Cell, []uint8) {
	if isFirst {
		cells := make([]Cell, 0, 9)
		dirs := make([]uint8, 0, 9)
		for i := 0; i < 8; i++ {
			d := uint8(i)
			delta := dirDelta[d]
			cells = append(cells, Cell{X: cur.X + delta.X, Z: cur.Z + delta.Z})
			dirs = append(dirs, d)
		}
		// duplicate DirN
		cells = append(cells, Cell{X: cur.X + dirDelta[DirN].X, Z: cur.Z + dirDelta[DirN].Z})
		dirs = append(dirs, DirN)
		return cells, dirs
	}
	// directed five-entry fan centered on dir [04 §7.1] C2
	if dir == DirNone {
		// fallback to all eight if no direction (should not happen for non-first)
		cells := make([]Cell, 0, 8)
		dirs := make([]uint8, 0, 8)
		for i := 0; i < 8; i++ {
			d := uint8(i)
			delta := dirDelta[d]
			cells = append(cells, Cell{X: cur.X + delta.X, Z: cur.Z + delta.Z})
			dirs = append(dirs, d)
		}
		return cells, dirs
	}
	// centered fan: dir-2, dir-1, dir, dir+1, dir+2 modulo 8 [04 §7.1] C2
	cells := make([]Cell, 0, 5)
	dirs := make([]uint8, 0, 5)
	for offset := -2; offset <= 2; offset++ {
		d := uint8((int(dir) + offset + 8) % 8)
		delta := dirDelta[d]
		cells = append(cells, Cell{X: cur.X + delta.X, Z: cur.Z + delta.Z})
		dirs = append(dirs, d)
	}
	return cells, dirs
}

// rayWalk performs the greedy bidirectional ray walk storing minimum scaled h on frontier [04 §7.2] C9.
// It walks greedily from start toward target, always stepping to the passable 8-neighbor minimizing scaled h.
// Returns bestScaled (minimum scaled h among visited passable cells), connects (true if target reached), and hasBest.
func rayWalk(start, target Cell, isPassable func(Cell) bool, hasBounds bool, bounds Rect, goal Goal, scale int32) (best int32, connects bool, hasBest bool) {
	// Greedy walk forward from start [04 §7.2] C9.
	// At each step pick the passable neighbor with minimal scaled h that improves over current.
	// This captures side steps around a single blocked cell while not including beyond-wall cells for a full wall.
	cur := start
	// start must be passable to have been checked before; but compute best anyway
	h0 := goal.H(cur)
	best = ScaledHeuristic(h0, scale) // [04 §7.2] C6
	hasBest = true
	visited := make(map[Cell]struct{}, 64)
	visited[cur] = struct{}{}
	// limit steps to avoid infinite loop; 4k is safe for 10x10 grids
	for iter := 0; iter < 4096; iter++ {
		if cur == target {
			connects = true
			break
		}
		curHs := ScaledHeuristic(goal.H(cur), scale)
		var bestNb Cell
		bestHs := int32(1 << 30)
		found := false
		for _, d := range dirDelta {
			nb := Cell{X: cur.X + d.X, Z: cur.Z + d.Z}
			if _, ok := visited[nb]; ok {
				continue
			}
			if !isPassableWithBounds(nb, isPassable, hasBounds, bounds) {
				continue
			}
			hs := ScaledHeuristic(goal.H(nb), scale)
			if !found || hs < bestHs {
				bestHs = hs
				bestNb = nb
				found = true
			}
		}
		if !found {
			connects = false
			break
		}
		// Greedy requires strictly closer to make progress; otherwise frontier exhausted [04 §7.2] C9
		if bestHs >= curHs {
			connects = false
			break
		}
		cur = bestNb
		visited[cur] = struct{}{}
		if bestHs < best {
			best = bestHs
		}
	}
	// Bidirectional: also walk backward from target toward start and consider its frontier
	// The minimum scaled h on either frontier is the write-once threshold [04 §7.2] C9.
	// For simplicity, run same greedy from target toward start and take min.
	cur = target
	if !isPassableWithBounds(cur, isPassable, hasBounds, bounds) {
		// target blocked, its frontier is neighbors; we already have best from forward walk,
		// but also consider target's neighbors
		for _, d := range dirDelta {
			nb := Cell{X: cur.X + d.X, Z: cur.Z + d.Z}
			if !isPassableWithBounds(nb, isPassable, hasBounds, bounds) {
				continue
			}
			hs := ScaledHeuristic(goal.H(nb), scale)
			if hs < best {
				best = hs
			}
		}
	} else {
		// target passable, walk backward
		visited2 := make(map[Cell]struct{}, 64)
		visited2[cur] = struct{}{}
		// best already includes target h if walk reached it; but if forward didn't reach, backward may find closer frontier near start
		for iter := 0; iter < 4096; iter++ {
			if cur == start {
				connects = true
				break
			}
			curHs := ScaledHeuristic(goal.H(cur), scale)
			var bestNb Cell
			bestHs := int32(1 << 30)
			found := false
			for _, d := range dirDelta {
				nb := Cell{X: cur.X + d.X, Z: cur.Z + d.Z}
				if _, ok := visited2[nb]; ok {
					continue
				}
				if !isPassableWithBounds(nb, isPassable, hasBounds, bounds) {
					continue
				}
				// For backward walk, heuristic still measured to target? Actually H is to target, so from target moving backward, H increases.
				// To find frontier near start, we want minimal H among visited, which will be near target.
				// So picking minimal H from target side will stay near target, not progress to start.
				// Instead for backward walk we should minimize distance to start? But H is defined to target, so minimal is at target.
				// So backward walk not useful for minimizing H; we keep forward walk's best.
				hs := ScaledHeuristic(goal.H(nb), scale)
				if !found || hs < bestHs {
					bestHs = hs
					bestNb = nb
					found = true
				}
			}
			if !found {
				break
			}
			if bestHs >= curHs {
				break
			}
			cur = bestNb
			visited2[cur] = struct{}{}
			if bestHs < best {
				best = bestHs
			}
		}
	}
	return best, connects, hasBest
}

// SearchConfig holds inputs for weighted A* search [04 §7.1][04 §7.2].
type SearchConfig struct {
	Start      Cell
	Goal       Goal
	IsPassable func(Cell) bool // injected, nil means all passable except OOB [04 §7.1]
	Scale      int32           // per-player quantum for hScaled [04 §7.2] C6; 0 defaults to 65536 (1.0)
	Bias       Point           // half-footprint bias added to cell coords for published waypoints [04 §7.1] C1
	HasBounds  bool            // whether Bounds is valid for OOB checks
	Bounds     Rect            // inclusive lattice bounds; OOB = impassable [04 §7.1] C10
}

// SearchResult is the outcome of Search [04 §7.2] C10, [04 §7.3] C13.
type SearchResult struct {
	Points   []Point // published waypoints (cell + bias) [04 §7.1] C1, [04 §7.3] C13
	Status   Status  // final publish status: 0 success, 0x100 already satisfied, 0x200 rejected [04 §7.2] C10
	Notified Status  // early ray notification if any (0x100 connects, 0x200 no closer) [04 §7.2] C10
	Popped   int     // number of heap pops performed
	Seeded   bool    // whether A* was seeded (false for early exits without seeding) [04 §7.2] C10
}

// Session is the resumable search continuation [04 §7.3] C11 C12.
// It retains the heap, node store, write-once tolerance slot, and goal flags across calls
// so a search can stop after exactly N pops with heap+request state retained and resume later
// [04 §7.3] "Budget exhaustion leaves the heap and request active — it does not publish the best partial prefix".
// The one-shot Search wrapper delegates to a Session with an effectively infinite budget so
// existing callers and tests keep working with identical results.
type Session struct {
	cfg SearchConfig
	// TODO(question): does retail re-weight hScaled with the refreshed 150-tick quantum on resumption, or keep the request's original scale? Keeping initial preserves determinism across budget interruptions. [04 §7.2]
	scale        int32
	goalSet      map[Cell]struct{}
	tolerance    int32
	hasTolerance bool
	notified     Status
	seeded       bool
	done         bool
	resultPoints []Point
	resultStatus Status
	popped       int
	ns           *NodeStore
	heap         Heap
	goalFlag     map[NodeID]bool
}

// NewSession creates a resumable search session [04 §7.1][04 §7.2][04 §7.3] C11 C12.
// Initialization runs the same early-exit and ray-walk logic as the one-shot Search,
// including the write-once tolerance slot that is never updated [04 §7.2] C9.
func NewSession(cfg SearchConfig) *Session {
	s := &Session{cfg: cfg}
	scale := cfg.Scale
	if scale == 0 {
		scale = 65536
	}
	s.scale = scale
	s.cfg.Scale = scale
	s.init()
	return s
}

// init performs the pre-seed phases: enumeration, early exits, ray walk, and heap seeding [04 §7.2] C9 C10.
func (s *Session) init() {
	cfg := s.cfg
	scale := s.scale
	if cfg.Goal == nil {
		s.done = true
		s.resultStatus = StatusRejected
		s.notified = StatusRejected
		s.seeded = false
		return
	}
	enumCells := cfg.Goal.Enumerate(nil)
	filtered := make([]Cell, 0, len(enumCells))
	var nearestGoal Cell
	hasNearest := false
	var nearestDist int64
	for _, c := range enumCells {
		if cfg.HasBounds && !InBounds(c, cfg.Bounds) {
			continue
		}
		filtered = append(filtered, c)
		dx := int64(c.X) - int64(cfg.Start.X)
		dz := int64(c.Z) - int64(cfg.Start.Z)
		dist := dx*dx + dz*dz
		if !hasNearest || dist < nearestDist {
			nearestGoal = c
			nearestDist = dist
			hasNearest = true
		}
	}
	goalSet := make(map[Cell]struct{}, len(filtered))
	for _, c := range filtered {
		goalSet[c] = struct{}{}
	}
	s.goalSet = goalSet

	if cfg.Goal.StartSatisfied(cfg.Start) {
		s.done = true
		s.resultStatus = StatusAlreadySatisfied
		s.notified = StatusAlreadySatisfied
		s.seeded = false
		s.resultPoints = nil
		return
	}
	if cfg.HasBounds && !InBounds(cfg.Start, cfg.Bounds) {
		s.done = true
		s.resultStatus = StatusRejected
		s.notified = StatusRejected
		s.seeded = false
		return
	}
	var tolerance int32
	hasTolerance := false
	var notified Status
	if hasNearest {
		best, connects, hasBest := rayWalk(cfg.Start, nearestGoal, cfg.IsPassable, cfg.HasBounds, cfg.Bounds, cfg.Goal, scale)
		if hasBest {
			tolerance = best
			hasTolerance = true
		}
		if connects {
			notified = StatusAlreadySatisfied
		}
		startH := cfg.Goal.H(cfg.Start)
		startScaled := ScaledHeuristic(startH, scale)
		if hasBest && best >= startScaled {
			s.done = true
			s.resultStatus = StatusRejected
			s.notified = StatusRejected
			s.seeded = false
			return
		}
		s.tolerance = tolerance
		s.hasTolerance = hasTolerance
		s.notified = notified
	}
	ns := NewNodeStore(scale)
	var heap Heap
	goalFlag := make(map[NodeID]bool)
	startID := ns.Ensure(cfg.Start, 0, invalidNodeID, DirNone, cfg.Goal)
	ns.SetOpen(startID, true)
	heap.Push(startID, ns.Get(startID).F)
	s.ns = ns
	s.heap = heap
	s.goalFlag = goalFlag
	s.seeded = true
	s.done = false
	s.popped = 0
}

// Resume advances the search by up to budget heap pops [04 §7.3] C11 C12.
// If budget exhaustion stops the search mid-way, it returns done=false with heap+request state retained
// and no publication [04 §7.3] C12. When the search completes (goal reached + reconstructed or heap exhausted /
// rejected) it returns done=true with either points (goal reached) or empty (heap exhausted) [04 §7.3] C12.
// The write-once tolerance slot, node store, and goal flags persist across resumes [04 §7.2] C9 C7.
func (s *Session) Resume(budget int) ([]Point, Status, bool) {
	if s.done {
		return s.resultPoints, s.resultStatus, true
	}
	if !s.seeded {
		s.done = true
		s.resultStatus = StatusRejected
		s.resultPoints = nil
		return s.resultPoints, s.resultStatus, true
	}
	if budget <= 0 {
		return nil, 0, false
	}
	cfg := s.cfg
	scale := s.scale
	ns := s.ns
	heap := &s.heap
	goalSet := s.goalSet
	goalFlag := s.goalFlag
	tolerance := s.tolerance
	hasTolerance := s.hasTolerance
	notified := s.notified

	isEnumeratedGoal := func(c Cell) bool {
		_, ok := goalSet[c]
		return ok
	}

	startPopped := s.popped
	for heap.Len() > 0 && s.popped-startPopped < budget {
		id, f, ok := heap.Pop()
		if !ok {
			break
		}
		node := ns.Get(id)
		if node.Closed {
			continue
		}
		if f != node.F {
			continue
		}
		if !node.Open {
			continue
		}
		ns.SetOpen(id, false)
		ns.SetClosed(id, true)
		s.popped++

		if goalFlag[id] {
			pts := reconstructRoute(cfg.Start, node.Cell, ns, cfg.Bias)
			s.done = true
			s.resultPoints = pts
			s.resultStatus = 0
			return s.resultPoints, s.resultStatus, true
		}
		if isEnumeratedGoal(node.Cell) {
			pts := reconstructRoute(cfg.Start, node.Cell, ns, cfg.Bias)
			s.done = true
			s.resultPoints = pts
			s.resultStatus = 0
			return s.resultPoints, s.resultStatus, true
		}

		isFirst := s.popped == 1
		neighCells, neighDirs := NeighborsForDir(node.Cell, node.Dir, isFirst)
		for idx, nCell := range neighCells {
			dir := neighDirs[idx]
			if !isPassableWithBounds(nCell, cfg.IsPassable, cfg.HasBounds, cfg.Bounds) {
				continue
			}
			step := StepCost(dir)
			turn := TurnPenalty(node.Dir, dir)
			initial := int32(0)
			if heap.Len() <= 1 {
				initial = InitialPenalty
			}
			short := int32(0)
			if node.Parent != invalidNodeID {
				if straightRunLen(ns, id) < ShortRunThreshold {
					short = ShortRunPenalty
				}
			}
			gNew := node.G + step + turn + initial + short

			if nid, exists := ns.Find(nCell); exists {
				if gNew < ns.Get(nid).G {
					if ns.TryRelax(nid, gNew, id, dir) {
						if ns.IsOpen(nid) {
							heap.Fix(nid, ns.Get(nid).F)
						} else if ns.IsClosed(nid) {
							ns.SetClosed(nid, false)
							ns.SetOpen(nid, true)
							heap.Push(nid, ns.Get(nid).F)
						} else {
							heap.Push(nid, ns.Get(nid).F)
							ns.SetOpen(nid, true)
						}
					}
				}
				continue
			}
			newID := ns.Ensure(nCell, gNew, id, dir, cfg.Goal)
			ns.SetOpen(newID, true)
			heap.Push(newID, ns.Get(newID).F)

			h := ns.Get(newID).H
			hs := ScaledHeuristic(h, scale)
			if hasTolerance && hs <= tolerance {
				goalFlag[newID] = true
			}
			if isEnumeratedGoal(nCell) {
				goalFlag[newID] = true
			}
		}
	}

	if s.done {
		return s.resultPoints, s.resultStatus, true
	}
	if heap.Len() == 0 {
		s.done = true
		s.resultPoints = nil
		s.resultStatus = StatusRejected
		if notified != 0 {
			return s.resultPoints, notified, true
		}
		return s.resultPoints, s.resultStatus, true
	}
	return nil, 0, false
}

// Config returns the search configuration (copy) for request-matching.
func (s *Session) Config() SearchConfig { return s.cfg }

// Start returns the session start cell.
func (s *Session) Start() Cell { return s.cfg.Start }

// Goal returns the session goal.
func (s *Session) Goal() Goal { return s.cfg.Goal }

// Popped returns total heap pops performed so far.
func (s *Session) Popped() int { return s.popped }

// Notified returns the early ray notification status if any.
func (s *Session) Notified() Status { return s.notified }

// Seeded reports whether the A* was seeded.
func (s *Session) Seeded() bool { return s.seeded }

// IsDone reports whether the session has completed.
func (s *Session) IsDone() bool { return s.done }

// Search runs weighted A* per [04 §7.1][04 §7.2][04 §7.3] C1-C4,C6,C7,C9,C10.
// Passability comes in as injected func [04 §7.1]; OOB is impassable [04 §7.1] C10.
// hScaled uses full signed 64-bit product + arithmetic shift, no float [04 §7.2] C6.
// Early exits are checked IN ORDER [04 §7.2] C10.
// Arrival tolerance threshold is write-once via rayWalk never updated [04 §7.2] C9.
// Reconstruction uses 64-entry ring at index&63 each time direction changes [04 §7.3] C13.
// This one-shot entry point is a wrapper over the resumable Session so all current
// callers/tests keep working with identical results [04 §7.3] C11 C12.
func Search(cfg SearchConfig) SearchResult {
	sess := NewSession(cfg)
	if sess.done {
		return SearchResult{Points: sess.resultPoints, Status: sess.resultStatus, Notified: sess.notified, Popped: sess.popped, Seeded: sess.seeded}
	}
	points, status, _ := sess.Resume(1 << 30)
	return SearchResult{Points: points, Status: status, Notified: sess.notified, Popped: sess.popped, Seeded: sess.seeded}
}

// reconstructRoute walks predecessor chain producing []Point per [04 §7.3] C13 ring semantics locally.
// This avoids importing movement to prevent cycle; semantics match movement.ReconstructRoute [04 §7.3] C13.
// The caller supplies start cell, goal cell, NodeStore chain, and half-footprint bias [04 §7.1] C1.
func reconstructRoute(start, goal Cell, ns *NodeStore, bias Point) []Point {
	var ring [64]Cell
	next := 0
	changes := 0
	prevDx, prevDz := int32(1<<30), int32(1<<30)
	// Find goal node ID
	goalID, ok := ns.Find(goal)
	if !ok {
		// goal not in store; fallback to publish goal cell + start
		ring[0] = goal
		ring[1] = start
		pts := make([]Point, 2)
		pts[0] = Point{X: start.X + bias.X, Z: start.Z + bias.Z}
		pts[1] = Point{X: goal.X + bias.X, Z: goal.Z + bias.Z}
		return pts
	}
	curID := goalID
	curCell := ns.Get(curID).Cell
	if curCell != start {
		for curCell != start {
			n := ns.Get(curID)
			parentID := n.Parent
			if parentID == invalidNodeID {
				break
			}
			parentCell := ns.Get(parentID).Cell
			dx := curCell.X - parentCell.X
			dz := curCell.Z - parentCell.Z
			if dx != prevDx || dz != prevDz {
				ring[next&63] = curCell // [04 §7.3] C13 index &63
				next++
				changes++
				prevDx, prevDz = dx, dz
			}
			curID = parentID
			curCell = parentCell
			if curID == invalidNodeID {
				break
			}
			// safety break if chain loops
			if changes > 1000 {
				break
			}
		}
	}
	// append start cell [04 §7.3] C13
	ring[next&63] = start
	next++
	total := changes + 1
	if total > 64 {
		total = 64 // [04 §7.3] C13 min(directionChanges+1,64)
	}
	if total <= 0 {
		total = 1
	}
	points := make([]Point, total)
	for i := 0; i < total; i++ {
		idx := (next - 1 - i) & 63 // masked downward, newest first [04 §7.3] C13
		cell := ring[idx]
		points[i] = Point{X: cell.X + bias.X, Z: cell.Z + bias.Z} // [04 §7.1] C1 plus bias [04 §7.3] C13
	}
	return points
}
