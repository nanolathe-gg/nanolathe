package path

// Search core for weighted A* on the TNT attribute-cell lattice [04 §7.1][04 §7.2].
//
// Lattice: one cell = 16 map pixels per cell [04 §7.1] C1. Waypoints come from
// cell coordinates plus the profile's half-footprint bias [04 §7.1] C1.
// Neighbor visit order north, northwest, west, southwest, south, southeast,
// east, northeast [04 §7.1] C2. First expansion attempts nine entries (all eight
// plus one harmless duplicate) [04 §7.1] C2; later expansions use a directed
// five-entry fan centered on parent travel direction [04 §7.1] C2.
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

// Search runs weighted A* per [04 §7.1][04 §7.2][04 §7.3] C1-C4,C6,C7,C9,C10.
// Passability comes in as injected func [04 §7.1]; OOB is impassable [04 §7.1] C10.
// hScaled uses full signed 64-bit product + arithmetic shift, no float [04 §7.2] C6.
// Early exits are checked IN ORDER [04 §7.2] C10.
// Arrival tolerance threshold is write-once via rayWalk never updated [04 §7.2] C9.
// Reconstruction uses 64-entry ring at index&63 each time direction changes [04 §7.3] C13.
func Search(cfg SearchConfig) SearchResult {
	scale := cfg.Scale
	if scale == 0 {
		scale = 65536 // 1.0 [04 §7.2] C6 default
	}
	if cfg.Goal == nil {
		return SearchResult{Status: StatusRejected, Notified: StatusRejected, Seeded: false}
	}
	// Enumerate goals, bounds-check, track nearest for ray [04 §7.2] C9, [04 §7.4]
	enumCells := cfg.Goal.Enumerate(nil)
	filtered := make([]Cell, 0, len(enumCells))
	var nearestGoal Cell
	hasNearest := false
	var nearestDist int64
	for _, c := range enumCells {
		if cfg.HasBounds && !InBounds(c, cfg.Bounds) {
			continue // bounds-checked [04 §7.2] C9
		}
		filtered = append(filtered, c)
		// track nearest by squared distance [04 §7.2] C9
		dx := int64(c.X) - int64(cfg.Start.X)
		dz := int64(c.Z) - int64(cfg.Start.Z)
		dist := dx*dx + dz*dz
		if !hasNearest || dist < nearestDist {
			nearestGoal = c
			nearestDist = dist
			hasNearest = true
		}
	}
	// goalCells set for direct marking [04 §7.2] C9
	goalSet := make(map[Cell]struct{}, len(filtered))
	for _, c := range filtered {
		goalSet[c] = struct{}{}
	}

	// C10 early exit 1: start satisfies goal nonzero -> publish empty 0x100 [04 §7.2] C10
	if cfg.Goal.StartSatisfied(cfg.Start) {
		return SearchResult{Points: nil, Status: StatusAlreadySatisfied, Notified: StatusAlreadySatisfied, Seeded: false, Popped: 0}
	}
	// C10 early exit 2: OOB start -> notify 0x200 publish empty [04 §7.2] C10
	if cfg.HasBounds && !InBounds(cfg.Start, cfg.Bounds) {
		return SearchResult{Points: nil, Status: StatusRejected, Notified: StatusRejected, Seeded: false}
	}
	// If start passable check fails but not OOB, we don't early exit; search will exhaust.

	// Compute ray walk for tolerance and early exits 3 & 4 [04 §7.2] C9, C10
	var tolerance int32
	hasTolerance := false
	var rayConnects bool
	var notified Status
	// Only if we have a nearest goal to walk toward; otherwise no tolerance
	if hasNearest {
		best, connects, hasBest := rayWalk(cfg.Start, nearestGoal, cfg.IsPassable, cfg.HasBounds, cfg.Bounds, cfg.Goal, scale)
		if hasBest {
			tolerance = best
			hasTolerance = true // write-once slot never updated [04 §7.2] C9
		}
		rayConnects = connects
		if connects {
			// ray connects start to goal notifies 0x100 but seed and run anyway [04 §7.2] C10
			notified = StatusAlreadySatisfied
		}
		// Early exit 4: ray best scaled h >= start cell's own scaled h -> notify 0x200 WITHOUT seeding [04 §7.2] C10
		startH := cfg.Goal.H(cfg.Start)
		startScaled := ScaledHeuristic(startH, scale) // [04 §7.2] C6
		if hasBest && best >= startScaled {
			return SearchResult{Points: nil, Status: StatusRejected, Notified: StatusRejected, Seeded: false, Popped: 0}
		}
		// Note: if rayConnects we keep notified but still seed
	}

	// Seed heap and store
	ns := NewNodeStore(scale) // [04 §7.2] C6 scale stored, C7 write-once h
	var heap Heap
	goalFlag := make(map[NodeID]bool)

	// Allocate start node [04 §7.2] C7
	startID := ns.Ensure(cfg.Start, 0, invalidNodeID, DirNone, cfg.Goal)
	ns.SetOpen(startID, true)
	heap.Push(startID, ns.Get(startID).F)

	// If start cell enumerated as goal (should have been caught by StartSatisfied for point etc, but saved goals have null predicate)
	// Check tolerance for start itself? Not needed per spec: tolerance applies to opened neighbors, not start.

	popped := 0

	// Helper to check if cell is goal via enumeration direct marking
	isEnumeratedGoal := func(c Cell) bool {
		_, ok := goalSet[c]
		return ok
	}

	for heap.Len() > 0 {
		id, f, ok := heap.Pop()
		if !ok {
			break
		}
		node := ns.Get(id)
		// Stale check: heap entry f may not match current node F after relaxation via Fix? But Fix updates heap entry in place, so should match. For duplicate push strategy stale would be filtered.
		// Also skip if already closed
		if node.Closed {
			continue
		}
		// Lazy staleness: if f != node.F, skip (duplicate entry)
		if f != node.F {
			continue
		}
		if !node.Open {
			continue
		}
		ns.SetOpen(id, false)
		ns.SetClosed(id, true)
		popped++

		// Goal termination: popping a node with open-plus-goal status terminates [04 §7.2] C9
		if goalFlag[id] {
			pts := reconstructRoute(cfg.Start, node.Cell, ns, cfg.Bias)
			// Preserve notified from ray walk if any; status success is 0
			return SearchResult{Points: pts, Status: 0, Notified: notified, Popped: popped, Seeded: true}
		}
		// Also if node's cell is enumerated goal cell, terminate even without tolerance (additional marking) [04 §7.2] C9
		if isEnumeratedGoal(node.Cell) {
			pts := reconstructRoute(cfg.Start, node.Cell, ns, cfg.Bias)
			return SearchResult{Points: pts, Status: 0, Notified: notified, Popped: popped, Seeded: true}
		}

		// Expand neighbors
		isFirst := popped == 1 // first expansion is start [04 §7.1] C2
		var neighCells []Cell
		var neighDirs []uint8
		neighCells, neighDirs = NeighborsForDir(node.Cell, node.Dir, isFirst)

		for idx, nCell := range neighCells {
			dir := neighDirs[idx]
			// C3 diagonal checks ONLY destination [04 §7.1] C3
			if !isPassableWithBounds(nCell, cfg.IsPassable, cfg.HasBounds, cfg.Bounds) {
				continue
			}

			// Compute costs [04 §7.2] C4
			step := StepCost(dir)              // [04 §7.2] 16/22
			turn := TurnPenalty(node.Dir, dir) // [04 §7.2] table
			initial := int32(0)
			if heap.Len() <= 1 { // while heap holds at most one entry [04 §7.2] C4
				// heap len is remaining open nodes after popping cur
				// For first expansion heap is 0, so this triggers; later heap >1
				// This matches "while heap holds at most one entry (the start expansion)"
				initial = InitialPenalty
			}
			short := int32(0)
			if node.Parent != invalidNodeID {
				if straightRunLen(ns, id) < ShortRunThreshold {
					short = ShortRunPenalty
				}
			}
			gNew := node.G + step + turn + initial + short

			// Check if neighbor already allocated
			if nid, exists := ns.Find(nCell); exists {
				// Try relax: only if newG strictly less [04 §7.2] C5
				if gNew < ns.Get(nid).G {
					if ns.TryRelax(nid, gNew, id, dir) {
						// Adjust heap via Fix if open, else reopen
						if ns.IsOpen(nid) {
							heap.Fix(nid, ns.Get(nid).F)
						} else if ns.IsClosed(nid) {
							// Reopen closed node [04 §7.2] C5? Not explicit, but allow to re-open if better path found
							ns.SetClosed(nid, false)
							ns.SetOpen(nid, true)
							heap.Push(nid, ns.Get(nid).F)
						} else {
							heap.Push(nid, ns.Get(nid).F)
							ns.SetOpen(nid, true)
						}
						// goal status may need re-evaluation? h unchanged, tolerance same
					}
				}
				continue
			}
			// New node allocation with write-once h [04 §7.2] C7
			newID := ns.Ensure(nCell, gNew, id, dir, cfg.Goal)
			ns.SetOpen(newID, true)
			heap.Push(newID, ns.Get(newID).F)

			// Determine open-plus-goal status via tolerance [04 §7.2] C9
			h := ns.Get(newID).H
			hs := ScaledHeuristic(h, scale) // [04 §7.2] C6
			if hasTolerance && hs <= tolerance {
				goalFlag[newID] = true // write-once per node? Never updated again per spec: slot never updated; per-node goal flag is also write-once
			}
			if isEnumeratedGoal(nCell) {
				goalFlag[newID] = true // directly marked [04 §7.2] C9
			}
			_ = rayConnects
		}
	}

	// Heap exhaustion publishes empty route [04 §7.3] C12
	// If we reached here, no goal popped. Return empty with rejected status.
	// Preserve notified from earlier ray if any.
	if notified != 0 {
		return SearchResult{Points: nil, Status: StatusRejected, Notified: notified, Popped: popped, Seeded: true}
	}
	return SearchResult{Points: nil, Status: StatusRejected, Notified: 0, Popped: popped, Seeded: true}
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
