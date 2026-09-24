package path

import "slices"

// Nanolathe Modern policy: docs/DESIGN_MOVEMENT_PATH.md "Modern unreachable
// moves". Nothing here is a retail claim; no Strict or Community path calls it.

// GoalSealedProbe is the outcome of ProbeGoalSealed.
type GoalSealedProbe struct {
	// Sealed reports that the setup ray's wall follow closed its loop, or
	// exhausted every sector, without touching a goal cell, reaching its
	// target or entering the goal's zero-heuristic band. The walk is a
	// two-sided trace of the boundary of the obstacle it last struck; closing
	// that loop without meeting the axis leg to the target means no cell of
	// the leg beyond the obstacle is reachable from the hit cell, which the
	// walk itself reached from the start.
	Sealed bool
	// Steps is the ray's own step count, the unit of work the scheduler
	// charges for the same walk [04 R-PATH-01 §5].
	Steps int
	// Frontier is the first touched cell, in walk order, with the lowest
	// unweighted heuristic, the start included: the tolerance frontier the
	// walk found [04 R-PATH-01 §5].
	Frontier Cell
}

// goalSetLinear is the enumerated goal size up to which membership is a plain
// scan; larger sets are sorted once and searched.
const goalSetLinear = 16

// ProbeGoalSealed runs the search's setup ray [04 R-PATH-01 §5]
// [04 R-PATH-01 §15] from start toward goal over passable and reports whether
// it ended sealed. It changes nothing: it opens no session, lends no
// workspace and charges no scheduler. The ray target is the nearest
// enumerated goal cell with the search's own first-minimum tie-break, and a
// touched in-bounds enumerated goal cell ends the walk as a connection, as it
// does in the search. An out-of-bounds or blocked start, a start the goal
// already satisfies and a goal with no enumerated cell all answer not sealed,
// so every uncertain case keeps the caller's retail path.
//
// The heuristic weight is 1.0: the verdict depends only on the walk's geometry
// and on whether it touched the zero band, never on how the threshold is
// scaled. scratch is reused for the enumerated goal cells and returned for
// the next call, so a warmed caller does not allocate.
func ProbeGoalSealed(start Cell, goal Goal, hasBounds bool, bounds Rect, passable func(Cell) uint8, scratch []Cell) (GoalSealedProbe, []Cell) {
	if goal == nil || passable == nil || goal.StartSatisfied(start) || (hasBounds && !InBounds(start, bounds)) {
		return GoalSealedProbe{}, scratch
	}
	pass := func(c Cell) uint8 {
		if hasBounds && !InBounds(c, bounds) {
			return 0
		}
		return passable(c)
	}
	if pass(start) == 0 {
		return GoalSealedProbe{}, scratch
	}
	cells := goal.Enumerate(scratch[:0])
	var nearest Cell
	var nearestDist int64
	have := false
	for _, c := range cells {
		dx, dz := int64(c.X)-int64(start.X), int64(c.Z)-int64(start.Z)
		if d := dx*dx + dz*dz; !have || d < nearestDist {
			nearest, nearestDist, have = c, d, true
		}
	}
	if !have {
		return GoalSealedProbe{}, cells
	}
	// Membership only, never iteration order: the nearest cell is chosen
	// above, before a large set is sorted into a canonical (Z, X) order.
	cmp := func(a, b Cell) int {
		if a.Z != b.Z {
			return int(a.Z - b.Z)
		}
		return int(a.X - b.X)
	}
	if len(cells) > goalSetLinear {
		slices.SortFunc(cells, cmp)
	}
	isGoal := func(c Cell) bool {
		if hasBounds && !InBounds(c, bounds) {
			return false
		}
		if len(cells) > goalSetLinear {
			_, found := slices.BinarySearchFunc(cells, c, cmp)
			return found
		}
		return slices.Contains(cells, c)
	}
	frontier, frontierH := start, goal.H(start)
	r := walkRay(start, nearest, pass, goal, 1<<16, func(c Cell, _ uint8, _ bool) bool {
		if h := goal.H(c); h < frontierH {
			frontier, frontierH = c, h
		}
		return isGoal(c)
	})
	return GoalSealedProbe{Sealed: !r.connects && r.best != 0, Steps: r.steps, Frontier: frontier}, cells
}

// RouteEndCell inverts the route representation for a published route's last
// point [04 R-PATH-01 §7]: the cell whose world point, with the footprint
// offset the search published it under, is that point. It answers false for
// an empty route or a point no cell maps to.
func RouteEndCell(points []Point, footX, footZ int32) (Cell, bool) {
	if len(points) == 0 {
		return Cell{}, false
	}
	p := points[len(points)-1]
	if p.X%8 != 0 || p.Z%8 != 0 {
		return Cell{}, false
	}
	x, z := p.X/8-footX, p.Z/8-footZ
	if x%2 != 0 || z%2 != 0 {
		return Cell{}, false
	}
	return Cell{X: x / 2, Z: z / 2}, true
}
