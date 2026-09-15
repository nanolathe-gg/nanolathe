package movement

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type modernClearanceRoute struct {
	order *orders.Node
	cells []Cell
}

// StageModernClearance gives an idle blocker's proven local path priority over
// global path searches. Construction alone calls this under the central Modern
// policy. The next ordinary move activation consumes it; no extra simulation
// visit, speed change or occupancy write occurs here. See
// DESIGN_MOVEMENT_PATH, "Modern construction clearance priority".
func (s *System) StageModernClearance(u *units.Unit, head *orders.Node, cells []Cell) {
	if s == nil || u == nil || head == nil || head.ID != orders.Lookup("Move_Ground") || !isPrimaryHead(u, head) || len(cells) < 2 || len(cells) > 20 {
		return
	}
	setHandleRow(&s.clearanceRoutes, u.Handle, &modernClearanceRoute{order: head, cells: slices.Clone(cells)})
}

func (s *System) consumeModernClearance(u *units.Unit, head *orders.Node, start, goal path.Cell) bool {
	hint := handleRow(s.clearanceRoutes, u.Handle)
	if hint == nil {
		return false
	}
	setHandleRow(&s.clearanceRoutes, u.Handle, nil)
	if hint.order != head || hint.cells[0] != (Cell{X: start.X, Z: start.Z}) || hint.cells[len(hint.cells)-1] != (Cell{X: goal.X, Z: goal.Z}) {
		return false
	}
	fx, fz := s.pathFootprint(u)
	points := make([]Point, len(hint.cells))
	points[0] = Point{X: int32(u.X) >> 16, Z: int32(u.Z) >> 16}
	for i := 1; i < len(hint.cells); i++ {
		c, prev := hint.cells[i], hint.cells[i-1]
		dx, dz := c.X-prev.X, c.Z-prev.Z
		if !((dx == 0 && (dz == -1 || dz == 1)) || (dz == 0 && (dx == -1 || dx == 1))) {
			return false
		}
		points[i] = Point{X: c.X*16 + fx*8, Z: c.Z*16 + fz*8}
	}
	// Route ownership and arrival were bound by normal activation. Collision
	// and repath remain ordinary follower work; only initial search is avoided.
	route := handleRow(s.Routes, u.Handle)
	if route == nil {
		return false
	}
	installGroundRoute(route, points, s.staticObstacleRevision())
	route.Status = 0
	s.ClearPathFailure(u.Handle)
	return true
}

// Explicit record teardown discards a pending hint. Goal installers use their
// private release arm instead, so the initial install can still consume it.
func (s *System) discardModernClearance(n *orders.Node) {
	if hint := handleRow(s.clearanceRoutes, n.Owner); hint != nil && hint.order == n {
		setHandleRow(&s.clearanceRoutes, n.Owner, nil)
	}
}
