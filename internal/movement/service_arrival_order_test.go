package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestServiceAsksArrivalBeforeSteering locks step (1) of the follower's
// per-tick service against the ordering [04 R-MOV-01 §3] states for the whole
// service: "once per mover tick, **before steering**". With a payload
// installed the service asks it whether the unit has arrived and, on arrival,
// raises `0x20` and releases the payload [04 R-MOV-03 §2 "The follower's
// per-tick service"][04 R-PATH-01 §8]; the steering gate then finds no
// waypoint and brakes without turning [04 R-MOV-01 §3].
//
// The case that regressed: a goal installed at a point the mover has ALREADY
// satisfied. The goal installer's synthetic fallback gives the follower a
// two-point straight line at that point whenever the held route fails both
// acceptance gates ([04 R-PATH-01 §8] step 5.3), so an arrival ask made after
// steering instead of before lets the mover accelerate for one tick first —
// StartMoving, a MoveRateN, then StopMoving on the next tick, with no
// displacement. The ground guard reinstalls its follow goal every 30 ticks
// whether or not it has arrived [04 R-ORD-01 §8 point 4], so a settled guard
// played that triple once a second forever.
//
// The offset here is three cells at a radius of 48 whole world units:
// `floor(48/16)² = 9` squared cells, so a three-cell separation arrives
// inclusively [04 §8.3].
func TestServiceAsksArrivalBeforeSteering(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	start := path.Cell{X: 5, Z: 5}
	x, z := world.CellToWorld(start.X), world.CellToWorld(start.Z)
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

	goalX := world.CellToWorld(start.X + 3)
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: goalX, Y: u.Y, Z: z, Radius: 48}) {
		t.Fatal("point goal install refused")
	}
	if !sys.ActivateMove(u, head) {
		t.Fatal("activate move")
	}

	beforeX, beforeZ := u.X, u.Z
	sys.Scheduler.Tick(1)
	sys.BeginTick(2)
	res := sys.StepUnit(h, 2)
	sys.EndTick(2)

	if head.Satisfied&arrivalSatisfiedBit == 0 {
		t.Fatalf("no arrival for a goal the mover already satisfies: satisfied = %#x, want 0x20 set "+
			"[04 R-MOV-03 §2][04 §8.3]", head.Satisfied)
	}
	if !res.Arrived {
		t.Fatal("StepUnit reported no arrival although the service raised 0x20 [04 R-MOV-03 §2]")
	}
	if u.X != beforeX || u.Z != beforeZ {
		t.Fatalf("the mover displaced on the install tick: (%d,%d) -> (%d,%d); the service's arrival must "+
			"precede steering [04 R-MOV-01 §3]", beforeX>>16, beforeZ>>16, u.X>>16, u.Z>>16)
	}
	if u.Move.Speed != 0 {
		t.Fatalf("speed = %d after an install at an already-satisfied goal, want 0: the steering gate must "+
			"find no waypoint and brake [04 R-MOV-01 §3]", u.Move.Speed)
	}
	if u.MoveTier != 0 {
		t.Fatalf("move tier = %d, want 0 — a StartMoving/MoveRateN pair fired for a mover that never "+
			"displaced [04 §5.2]", u.MoveTier)
	}
}
