package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Modern priority is an initial local route, never a change to scheduler order.
// The normal point-goal installer must not lose it before activation.
func TestModernClearanceRetainsBendsWithoutScheduler(t *testing.T) {
	sys := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(2)
	d := &content.UnitDef{UnitName: "clearance", BMCode: 1, CanMove: true, FootprintX: 1, FootprintZ: 1, MaxVelocity: 65536, Acceleration: 65536, BrakeRate: 65536, TurnRate: 1024}
	h, err := w.Create(d, 0, world.CellToWorld(2)+world.CellToWorld(1)/2, 0, world.CellToWorld(2)+world.CellToWorld(1)/2)
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	u := w.Unit(h)
	sys.EnsureUnit(u)
	q := orders.QueueForUnit(u)
	q.SetBinding(&orders.QueueBinding{Lookup: w.Unit, Movement: &orders.MovementGoalAdapter{InstallPoint: sys.InstallPointGoal, Release: sys.ReleaseGoalPayload}})
	id := orders.Lookup("Move_Ground")
	q.Push(id, orders.NewMoveNode(id, world.CellToWorld(4)+world.CellToWorld(1)/2, world.CellToWorld(3)+world.CellToWorld(1)/2, 1, h, true))
	head := q.Head()
	cells := []Cell{{X: 2, Z: 2}, {X: 2, Z: 3}, {X: 3, Z: 3}, {X: 4, Z: 3}}
	sys.StageModernClearance(u, head, cells)
	q.Pump(u, 2)
	if !sys.ActivateMove(u, head) {
		t.Fatal("not activated")
	}
	want := []Point{{X: 40, Z: 40}, {X: 40, Z: 56}, {X: 56, Z: 56}, {X: 72, Z: 56}}
	r := sys.Routes[h]
	if !r.Active || !reflect.DeepEqual(r.Points[:r.Count], want) || sys.HasPathRequest(h) {
		t.Fatalf("lost priority route: %+v pending=%v", r, sys.HasPathRequest(h))
	}
	// A subsequent ordinary goal replacement must have ordinary scheduling.
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: world.CellToWorld(6), Z: world.CellToWorld(6), Radius: 4})
	sys.ActivateMove(u, head)
	if !sys.HasPathRequest(h) {
		t.Fatal("one-shot clearance bypassed a later ordinary request")
	}
	// Queue cleanup must discard an unconsumed hint.
	sys.StageModernClearance(u, head, cells)
	q.RemoveHead()
	q.Push(id, orders.NewMoveNode(id, world.CellToWorld(7), world.CellToWorld(7), 3, h, false))
	if handleRow(sys.clearanceRoutes, h) != nil {
		t.Fatal("production queue teardown retained staged clearance")
	}
}
