package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Route points have already been converted from cells to integer world
// coordinates [04 §7.3][04 R-MOV-01 §3]. Publication must only add the fixed
// fraction, or the Shift overlay draws a route sixteen times too far away.
func TestOrderRoutePublicationPreservesWorldCoordinates(t *testing.T) {
	s := newLoopTestSession(t, 1)
	u := s.Units.Unit(1)
	q := orders.QueueForUnit(u)
	q.CancelAll()
	id := orders.Lookup("Move_Ground")
	q.Push(id, orders.Node{Owner: u.Handle, GoalX: 120 << 16, GoalZ: 88 << 16})
	q.Push(id, orders.Node{Owner: u.Handle, GoalX: 152 << 16, GoalZ: 104 << 16})
	route := &movement.Route{}
	s.Movement.Routes[u.Handle] = route
	for tick, points := range [][]movement.Point{
		{{X: 40, Z: 56}, {X: 120, Z: 88}},
		{{X: -8, Z: 24}, {X: 152, Z: 104}},
	} {
		route.Publish(points)
		s.publishSnapshot(uint32(tick + 1))
		queues := s.Snapshot.Current().OrderQueues
		if len(queues) != 1 || len(queues[0].Primary) != 2 {
			t.Fatalf("published queues = %+v", queues)
		}
		got := queues[0].Primary[0].Route
		if len(got) != len(points) {
			t.Fatalf("route length = %d, want %d", len(got), len(points))
		}
		for i, p := range points {
			if got[i].X != numeric.Fixed(p.X)<<16 || got[i].Z != numeric.Fixed(p.Z)<<16 {
				t.Fatalf("route point %d = (%d,%d), want world (%d,%d) in 16.16", i, got[i].X, got[i].Z, p.X, p.Z)
			}
		}
		if len(queues[0].Primary[1].Route) != 0 {
			t.Fatal("queued successor inherited the active route")
		}
	}
}
