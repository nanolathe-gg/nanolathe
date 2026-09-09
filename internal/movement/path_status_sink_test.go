package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func newGroundPathStatusFixture(t *testing.T, start, goal path.Cell) (*System, *units.Unit, *orders.Queue, *orders.Node, path.Request) {
	t.Helper()
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	h, err := w.Create(wiringDef(), 0, world.CellToWorld(start.X), terrain.HeightAt(world.CellToWorld(start.X), world.CellToWorld(start.Z)), world.CellToWorld(start.Z))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(goal.X), GoalZ: world.CellToWorld(goal.Z)})
	head := q.Head()
	if head == nil || !sys.ActivateMove(u, head) {
		t.Fatal("activate move")
	}
	requests := sys.PathRequestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("path requests = %d, want 1", len(requests))
	}
	return sys, u, q, head, requests[0]
}

// TestGroundPathStatusSink locks the order-record notification boundary.
// Search setup owns 0x100/0x200, empty publication owns 0x40, and neither may
// wake a node after its activation or queue-head identity is stale
// [04 R-PATH-01 §4][04 R-PATH-01 §7][04 R-PATH-01 §9]
// [04 R-COLL-01 §6].
func TestGroundPathStatusSink(t *testing.T) {
	t.Run("start satisfied then empty at goal", func(t *testing.T) {
		cell := path.Cell{X: 2, Z: 2}
		sys, _, _, head, req := newGroundPathStatusFixture(t, cell, cell)
		work := sys.searchFunc(req, 65536, 0)
		if !work.Done || work.Status != path.StatusAlreadySatisfied || len(work.Points) != 0 {
			t.Fatalf("start-satisfied work = %+v", work)
		}
		if got := head.Satisfied; got != uint32(path.StatusAlreadySatisfied) {
			t.Fatalf("setup satisfied bits = %#x, want 0x100", got)
		}

		sys.publishFunc(req, work.Points, work.Status)
		if got := head.Satisfied; got != uint32(path.StatusAlreadySatisfied) {
			t.Fatalf("empty-at-goal bits = %#x, want only 0x100", got)
		}
		if route := sys.Routes[req.Unit]; route == nil || route.Status != path.StatusAlreadySatisfied {
			t.Fatalf("route diagnostic not preserved: %+v", route)
		}
	})

	t.Run("out of bounds then empty away from goal", func(t *testing.T) {
		sys, u, _, head, req := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 5})
		// Setup reads the mover's CACHED COMMITTED CELL at admission, not the
		// cell the request was submitted with [04 R-PATH-01 §4] step 1, so the
		// off-map start of step 8 is staged on the committed anchor.
		sys.Collisions[u.Handle].CachedAnchor = Cell{X: 25, Z: 25}
		work := sys.searchFunc(req, 65536, 0)
		if !work.Done || work.Status != path.StatusRejected || len(work.Points) != 0 {
			t.Fatalf("out-of-bounds work = %+v", work)
		}
		if got := head.Satisfied; got != uint32(path.StatusRejected) {
			t.Fatalf("setup rejected bits = %#x, want 0x200", got)
		}

		sys.publishFunc(req, work.Points, work.Status)
		if got := head.Satisfied; got != uint32(path.StatusRejected)|0x40 {
			t.Fatalf("empty-away bits = %#x, want 0x240", got)
		}
		route := sys.Routes[req.Unit]
		failure, ok := sys.PathFailureRecord(req.Unit)
		if route == nil || route.Status != path.StatusRejected || !ok || failure.Status != path.StatusRejected {
			t.Fatalf("route/path-failure diagnostics route=%+v failure=%+v/%v", route, failure, ok)
		}
	})

	t.Run("ray connected notifies while search continues", func(t *testing.T) {
		sys, _, _, head, req := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 2})
		work := sys.searchFunc(req, 65536, 0)
		if work.Done {
			t.Fatalf("connected ray should seed resumable search: %+v", work)
		}
		if got := head.Satisfied; got != uint32(path.StatusAlreadySatisfied) {
			t.Fatalf("connected-ray bits = %#x, want 0x100", got)
		}
	})

	t.Run("ray miss notifies without an out of bounds start", func(t *testing.T) {
		sys, _, _, head, req := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 2})
		// The production no-terrain branch is wholly impassable. With no bounds
		// installed, this isolates the ray-miss 0x200 from the OOB early exit.
		sys.Terrain = nil
		work := sys.searchFunc(req, 65536, 0)
		if !work.Done || work.Status != path.StatusRejected {
			t.Fatalf("blocked-ray work = %+v", work)
		}
		if got := head.Satisfied; got != uint32(path.StatusRejected) {
			t.Fatalf("missed-ray bits = %#x, want 0x200", got)
		}
	})

	t.Run("stale activation cannot wake replacement", func(t *testing.T) {
		sys, u, q, oldHead, stale := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 2})
		q.RemoveHead()
		q.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(7), GoalZ: world.CellToWorld(2)})
		newHead := q.Head()
		if newHead == nil || !sys.ActivateMove(u, newHead) {
			t.Fatal("activate replacement")
		}
		oldHead.Satisfied, newHead.Satisfied = 0, 0
		stale.Goal = path.PointGoal(stale.Start, 0)
		work := sys.searchFunc(stale, 65536, 0)
		sys.publishFunc(stale, work.Points, work.Status)
		if oldHead.Satisfied != 0 || newHead.Satisfied != 0 {
			t.Fatalf("stale activation woke order: old=%#x new=%#x", oldHead.Satisfied, newHead.Satisfied)
		}
	})

	t.Run("stale queue head cannot wake successor", func(t *testing.T) {
		sys, _, q, oldHead, stale := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 2})
		q.RemoveHead()
		q.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(7), GoalZ: world.CellToWorld(2)})
		newHead := q.Head()
		oldHead.Satisfied, newHead.Satisfied = 0, 0
		stale.Goal = path.PointGoal(stale.Start, 0)
		work := sys.searchFunc(stale, 65536, 0)
		sys.publishFunc(stale, work.Points, work.Status)
		if oldHead.Satisfied != 0 || newHead.Satisfied != 0 {
			t.Fatalf("stale head woke order: old=%#x new=%#x", oldHead.Satisfied, newHead.Satisfied)
		}
	})
}
