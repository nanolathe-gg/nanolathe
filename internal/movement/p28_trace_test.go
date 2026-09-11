package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
)

// A capture without pre-enabled history must still distinguish a staged
// request from an absent request and expose the controller's actual binding.
func TestMovementSnapshotExposesRepathAndBindingWithoutHistory(t *testing.T) {
	s, w, h := releaseFixture(t, wiringDef(), 2)
	u := w.Unit(h)
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("RepairUnit"), orders.Node{Owner: h, Phase: 1, CreationTick: 100})
	n := q.Head()
	s.tick = 200
	s.InstallRectangleGoal(orders.RectangleGoalRequest{Owner: h, Node: n, CellX: 8, CellZ: 8, Width: 4, Depth: 4})
	s.ActivateMove(u, n)
	route := handleRow(s.Routes, h)
	route.LastRequestTick = 190
	before := *route
	first := s.ParitySnapshot(w, 200)
	if len(first) != 1 {
		t.Fatalf("snapshot rows=%d", len(first))
	}
	m := first[0]
	if !m.CurrentWantsRepath || m.LastRequestTick != 190 || m.Staged == nil || m.Pending != nil || m.GroundGoal == nil || m.GroundGoal.Kind != 3 || !m.GoalMatchesActiveOrder || !m.ActiveOrderIsPrimaryHead {
		t.Fatalf("missing follower admission/binding state: %+v", m)
	}
	if *route != before || s.Scheduler.TraceEnabled() || !reflect.DeepEqual(first, s.ParitySnapshot(w, 200)) {
		t.Fatal("snapshot advanced state or enabled historical tracing")
	}
	m.ActiveOrder.Phase = 99
	m.GroundGoal.Rect.Min.X = -100
	m.Staged.Start.X = -100
	after := s.ParitySnapshot(w, 200)[0]
	if n.Phase != 1 || after.ActiveOrder.Phase != 1 || after.GroundGoal.Rect.Min.X == -100 || after.Staged.Start.X == -100 {
		t.Fatal("snapshot aliases live binding or request state")
	}
}

func TestP28CollisionHistoryOnlyCommitsWhenEnabled(t *testing.T) {
	s := NewSystem(nil, Profile{}, nil)
	if got := s.CollisionHistory(1); got != nil {
		t.Fatalf("disabled trace returned %v", got)
	}
	s.EnableParityTrace()
	if s.Scheduler == nil {
		t.Fatal("movement scheduler unexpectedly nil")
	}
	if pending, result := s.Scheduler.TraceFor(1); pending != nil || result != nil {
		t.Fatalf("empty scheduler exposed trace: %v %v", pending, result)
	}
	// The scheduler trace surface is enabled by the same opt-in boundary; no
	// read is allowed to synthesize a request or collision record.
	s.pathProvider.Submit(path.Request{Unit: 1, Player: 0, Start: path.Cell{}, Goal: path.PointGoal(path.Cell{X: 1}, 0)})
	if pending, result := s.Scheduler.TraceFor(1); pending != nil || result != nil || !s.pathProvider.HasRequest(1) {
		t.Fatalf("provider request must remain outside scheduler trace: pending=%v result=%v provider=%v", pending, result, s.pathProvider.HasRequest(1))
	}
}

func TestP28CollisionTraceLimitAndReset(t *testing.T) {
	s := NewSystem(nil, Profile{}, nil)
	s.EnableParityTrace()
	s.collisionHistory = append(s.collisionHistory,
		collisionHistoryEntry{Tick: 1, Slot: 1},
		collisionHistoryEntry{Tick: 2, Slot: 2})
	s.SetParityTraceLimit(1)
	if len(s.collisionHistory) != 1 || s.collisionHistory[0].Tick != 2 {
		t.Fatalf("collision history was not bounded: %+v", s.collisionHistory)
	}
	s.ResetParityTrace()
	if len(s.collisionHistory) != 0 || s.ParityTraceDropped() {
		t.Fatalf("collision reset retained diagnostics: history=%v dropped=%v", s.collisionHistory, s.ParityTraceDropped())
	}
}
