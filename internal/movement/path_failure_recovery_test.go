package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestPathFailureRecoveryRearmsEverySixtyTicks locks the follower-owned
// recovery state machine. A rejected publication clears wants-repath, the
// next follower visit re-arms it, and the request is admitted at exactly
// lastRequestTick+60 with no retry ceiling [04 R-MOV-01 §7][04 R-PATH-01 §6,
// §7–§8]. Replacing the order clears the old binding and prevents the old head
// from submitting again.
func TestPathFailureRecoveryRearmsEverySixtyTicks(t *testing.T) {
	system := NewSystem(syntheticTerrainForIntegrate(), wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	system.BindWorld(w)
	system.ConfigurePath(1, 10, func(player int) bool { return player >= 0 && player < 1 })
	def := &content.UnitDef{
		UnitName: "retry-follower", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 2 * int32(worldUnitsPerCell), Acceleration: 2 * int32(worldUnitsPerCell),
		BrakeRate: 2 * int32(worldUnitsPerCell), TurnRate: 65535,
	}
	setScratchMovement(def, wiringProfile)
	h, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	system.EnsureUnit(u)
	moveID := orders.Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("Move_Ground order is unavailable")
	}
	q := orders.QueueForUnit(u)
	q.Push(moveID, orders.Node{Owner: h, GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(1)})
	head := q.Head()
	if head == nil {
		t.Fatal("no head")
	}
	// `Move_Ground` phase 0 installs a point goal of radius `(int16)p1 + 4`
	// [04 R-ORD-01 §4]; the follower's repath arm runs only "with a payload
	// installed" [04 R-MOV-03 §2 step 3], so the fixture installs one as the
	// handler would.
	system.InstallPointGoal(orders.PointGoalRequest{Owner: head.Owner, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	if !system.ActivateMove(u, head) {
		t.Fatal("activate move")
	}
	first := system.PathRequestsSnapshot()
	if len(first) != 1 {
		t.Fatalf("initial requests = %d, want 1", len(first))
	}

	// Fail the initial request. The publisher clears wants-repath; the next
	// follower visit re-arms it, but the request remains throttled until tick 60.
	system.tick = 0
	system.CancelPathRequest(h)
	system.publishFunc(first[0], nil, path.StatusRejected)
	route := system.Routes[h]
	if route == nil || route.WantsRepath {
		t.Fatalf("failed publication wants-repath=%v, want false before follower visit", route != nil && route.WantsRepath)
	}
	system.serviceGroundFollower(u, head, route, 1)
	if !route.WantsRepath {
		t.Fatal("follower did not re-arm after failed publication")
	}
	system.serviceGroundFollower(u, head, route, 59)
	if got := system.PathRequestsSnapshot(); len(got) != 0 {
		t.Fatalf("request admitted before tick 60: %v", got)
	}
	system.serviceGroundFollower(u, head, route, 60)
	second := system.PathRequestsSnapshot()
	if len(second) != 1 || route.LastRequestTick != 0 {
		t.Fatalf("tick-60 staging=%v timestamp=%d, want one request and unchanged zero timestamp", second, route.LastRequestTick)
	}
	system.Scheduler.Tick(60)
	if route.LastRequestTick != 60 {
		t.Fatalf("tick-60 poll timestamp=%d, want 60", route.LastRequestTick)
	}

	// A second failure must remain recoverable. No session-side retry counter is
	// involved; the same follower state reaches the next exact boundary.
	system.tick = 60
	system.CancelPathRequest(h)
	system.publishFunc(second[0], nil, path.StatusRejected)
	system.serviceGroundFollower(u, head, route, 61)
	system.serviceGroundFollower(u, head, route, 119)
	if got := system.PathRequestsSnapshot(); len(got) != 0 {
		t.Fatalf("second request admitted before tick 120: %v", got)
	}
	system.serviceGroundFollower(u, head, route, 120)
	third := system.PathRequestsSnapshot()
	if len(third) != 1 {
		t.Fatalf("tick-120 staged request=%v, want one", third)
	}
	// Repeated service in the same poll window cannot duplicate the request.
	system.serviceGroundFollower(u, head, route, 120)
	if got := system.PathRequestsSnapshot(); len(got) != 1 {
		t.Fatalf("duplicate request at tick 120: %v", got)
	}
	system.Scheduler.Tick(120)
	if route.LastRequestTick != 120 {
		t.Fatalf("tick-120 poll timestamp=%d, want 120", route.LastRequestTick)
	}

	// Replacing the authoritative head cancels the old request and clears the
	// old follower state. Calling the old head afterward cannot re-arm it.
	oldToken := system.activeOrders[h].token
	system.CancelPathRequest(h)
	q.RemoveHead()
	q.Push(moveID, orders.Node{Owner: h, GoalX: world.CellToWorld(9), GoalZ: world.CellToWorld(1)})
	newHead := q.Head()
	if newHead == nil || !system.ActivateMove(u, newHead) {
		t.Fatal("activate replacement")
	}
	if system.activeOrders[h].order != newHead || system.activeOrders[h].token == oldToken {
		t.Fatalf("replacement binding=%+v, old token=%d", system.activeOrders[h], oldToken)
	}
	if route.Status != 0 || system.HasPathFailure(h) {
		t.Fatalf("head replacement retained old path status route=%d failure=%v", route.Status, system.HasPathFailure(h))
	}
	if route.WantsRepath == false && len(system.PathRequestsSnapshot()) == 0 {
		t.Fatal("replacement did not submit its own request")
	}
	system.CancelPathRequest(h)
	system.serviceGroundFollower(u, head, route, 180)
	if got := system.PathRequestsSnapshot(); len(got) != 0 {
		t.Fatalf("old head re-armed after replacement: %v", got)
	}

	// A transport transition uses the same reset boundary. The carried unit
	// keeps its order record for unload, but must not retain a live path request
	// or follower deadline while it is slaved to the carrier.
	route.Active = true
	route.WantsRepath = true
	route.LastRequestTick = 200
	u.Attachment.Carrier = 99
	system.BeginTick(201)
	system.StepUnit(h, 201)
	if route.Active || route.WantsRepath || route.LastRequestTick != 0 || system.activeOrders[h] != nil {
		t.Fatalf("transport cleanup left follower state active=%v wants=%v last=%d binding=%+v", route.Active, route.WantsRepath, route.LastRequestTick, system.activeOrders[h])
	}
	system.EndTick(201)
}
