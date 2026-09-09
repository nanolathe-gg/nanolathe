package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestStartSatisfiedRequestArrivesThroughTheFollower locks the one outcome the
// composition of [04 R-PATH-01 §4], [04 R-PATH-01 §7] and [04 R-MOV-03 §1]
// leaves for a path request whose start already satisfies its goal.
//
// The three steps, in the order they run:
//
//   - Request setup asks the goal's start predicate about the start cell first
//     of all. Nonzero → notify `0x100`, publish empty, release, return
//     [04 R-PATH-01 §4 step 6].
//   - The empty publication asks the goal "is the unit already at the goal";
//     `0x40` is raised only when that says NO [04 R-PATH-01 §7], so a
//     start-satisfied publication does not raise it. And `0x100` is masked out
//     of every satisfied set — no handler arms it [04 R-ORD-01 §0].
//   - The follower's per-tick service therefore owns the outcome: with a
//     payload installed it asks that payload whether the unit has arrived and,
//     on arrival, raises pending `0x20` on the record the payload belongs to
//     [04 R-MOV-03 §1 "The follower's per-tick service"][04 R-ORD-01 §0].
//
// The condition on that first service step is "with a payload installed" — a
// property of the controller's slot, not of the record's order name. This test
// uses `Attack_Chase`, which is not in the arrival handle's name list and whose
// phase 2 installs a point goal at the target sized from the slot's engagement
// distance [04 R-ORD-01 §3]. Before WU-19-90 the name list deleted the handle
// for it, so `0x20` had no producer, phase 3 never saw one of its `0x40E0`
// re-arm bits, and the chase re-armed its thirty-tick deadline forever.
func TestStartSatisfiedRequestArrivesThroughTheFollower(t *testing.T) {
	cell := path.Cell{X: 2, Z: 2}
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	x, z := world.CellToWorld(cell.X), world.CellToWorld(cell.Z)
	h, err := w.Create(wiringDef(), 0, x, terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)

	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Attack_Chase"), orders.Node{GoalX: x, GoalZ: z})
	head := q.Head()
	if head == nil {
		t.Fatal("no head record")
	}

	// The chase's phase-2 install: a point goal at the target with the slot's
	// engagement distance as its radius [04 R-ORD-01 §3]. The mover stands on
	// the goal cell, so the request that follows is start-satisfied.
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: x, Y: u.Y, Z: z, Radius: 180}) {
		t.Fatal("point goal install refused")
	}
	if !sys.ActivateMove(u, head) {
		t.Fatal("activate move")
	}
	requests := sys.PathRequestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("path requests = %d, want 1", len(requests))
	}
	req := requests[0]

	work := sys.searchFunc(req, 65536, 0)
	if !work.Done || work.Status != path.StatusAlreadySatisfied || len(work.Points) != 0 {
		t.Fatalf("start-satisfied work = %+v [04 R-PATH-01 §4 step 6]", work)
	}
	sys.publishFunc(req, work.Points, work.Status)

	// Exactly the two bits the search and the publisher own, and no more: the
	// publisher's `0x40` is withheld because the goal says the unit is already
	// there [04 R-PATH-01 §7].
	if got := head.Satisfied; got != uint32(path.StatusAlreadySatisfied) {
		t.Fatalf("after start-satisfied publication satisfied = %#x, want only 0x100 [04 R-PATH-01 §7]", got)
	}
	if route := sys.Routes[h]; route == nil || route.Active {
		t.Fatalf("empty publication left an active route: %+v", sys.Routes[h])
	}

	// The follower's service is the producer. One mover tick with the payload
	// installed and the unit at the goal must raise `0x20`.
	sys.Scheduler.Tick(1)
	sys.BeginTick(2)
	sys.StepUnit(h, 2)
	sys.EndTick(2)

	if head.Satisfied&arrivalSatisfiedBit == 0 {
		t.Fatalf("the follower published no arrival for a goal the mover starts inside: satisfied = %#x, "+
			"want 0x20 set [04 R-MOV-03 §1][04 R-ORD-01 §0]", head.Satisfied)
	}
	if head.Satisfied&0x40 != 0 {
		t.Fatalf("cannot-get-there raised for a goal the mover is standing in: satisfied = %#x [04 R-PATH-01 §7]", head.Satisfied)
	}
}

// TestArrivalHandleIsNotBoundWithoutAnInstalledPayload is the other half of the
// same rule: the follower's arrival step runs "with a payload installed"
// [04 R-MOV-03 §1], so a record outside the arrival-handle name list that owns
// no goal payload must not get a handle, and any handle left by a previous
// record must be dropped rather than left to signal for the wrong record.
func TestArrivalHandleIsNotBoundWithoutAnInstalledPayload(t *testing.T) {
	cell := path.Cell{X: 2, Z: 2}
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	x, z := world.CellToWorld(cell.X), world.CellToWorld(cell.Z)
	h, err := w.Create(wiringDef(), 0, x, terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)

	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Attack_Chase"), orders.Node{GoalX: x, GoalZ: z})
	head := q.Head()
	if head == nil {
		t.Fatal("no head record")
	}
	sys.arrivalHandles[h] = &arrivalHandle{order: head}

	sys.bindArrivalHandle(u, head)
	if _, ok := sys.arrivalHandles[h]; ok {
		t.Fatal("an arrival handle was bound for a record that owns no goal payload [04 R-MOV-03 §1]")
	}
}
