package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// This independently authored fixture preserves captured Flash137/92 and the
// nearby rally crowd's fixed-point positions/2x2 footprints, translated by
// whole cells onto flat terrain. The captured route was inactive with status
// 512; its sole positional order was phase zero, waiting for a retry deadline.
func capturedCrowdedRally(t *testing.T, modern bool) (*System, *units.Unit, *orders.Queue, *orders.Node) {
	t.Helper()
	terrain := syntheticTerrainForIntegrate()
	profile := wiringProfile
	profile.FootPrintX = 2
	profile.FootPrintZ = 2
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)
	positions := [][2]int64{{98561210, 161477734}, {96586645, 159482946}, {96538814, 157338505}, {94450813, 159436658}, {96491111, 161455403}, {98537517, 159354423}, {94440106, 157331030}, {98531192, 157300041}, {96403707, 155158960}, {92324768, 159416751}}
	var u *units.Unit
	for i, pos := range positions {
		def := wiringDef()
		def.FootprintX = 2
		def.FootprintZ = 2
		setScratchMovement(def, profile)
		x, z := numeric.Fixed(pos[0]-1408*65536), numeric.Fixed(pos[1]-2336*65536)
		h, err := w.Create(def, 0, x, terrain.HeightAt(x, z), z)
		if err != nil {
			t.Fatal(err)
		}
		v := w.Unit(h)
		sys.EnsureUnit(v)
		if i == 0 {
			u = v
		}
	}
	q := orders.QueueForUnit(u)
	var rules orders.Rules = orders.StrictRules{}
	if modern {
		rules = &orders.ModernRules{}
	}
	q.SetBinding(&orders.QueueBinding{Rules: rules, SimRNG: &rng.Simulation{}, Movement: &orders.MovementGoalAdapter{InstallPoint: sys.InstallPointGoal, Release: sys.ReleaseGoalPayload, CrowdedMoveBlocked: sys.CrowdedMoveBlocked}})
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: u.Handle, GoalX: numeric.FixedFromInt(1476 - 1408), GoalZ: numeric.FixedFromInt(2425 - 2336), GoalSupplied: true, Flags: 8})
	n := q.Head()
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: u.Handle, Node: n, X: n.GoalX, Z: n.GoalZ, Radius: 4}) {
		t.Fatal("install point")
	}
	if !sys.ActivateMove(u, n) {
		t.Fatal("activate rally")
	}
	req := sys.PathRequestsSnapshot()[0]
	sys.publishFunc(req, nil, path.StatusRejected)
	n.Phase = 0
	n.DynamicGate = 1
	n.Deadline = 108568
	n.Satisfied = 512
	n.MoveState = orders.MoveEnRoute
	return sys, u, q, n
}

func TestModernCapturedCrowdedRallyCompletesInactiveRetry(t *testing.T) {
	for _, modern := range []bool{false, true} {
		sys, u, q, n := capturedCrowdedRally(t, modern)
		x, z, blocked := sys.CrowdedMoveBlocked(u, n)
		if !blocked {
			t.Fatalf("captured local crowd was not admitted: anchor=%d,%d", x, z)
		}
		route := handleRow(sys.Routes, u.Handle)
		random := *q.Binding().SimRNG
		for tick := uint32(108440); tick <= 108530; tick++ {
			got := sys.serviceGroundFollower(u, n, route, tick)
			if got != (modern && tick == 108530) {
				t.Fatalf("modern=%v tick=%d arrival=%v", modern, tick, got)
			}
		}
		if *q.Binding().SimRNG != random {
			t.Fatal("crowd decision drew RNG")
		}
		if !modern {
			if n.Phase != 0 || n.Satisfied != 512 || !sys.hasControllerGoal(u.Handle) {
				t.Fatal("Strict changed retry/goal")
			}
			continue
		}
		if n.Phase != 1 || n.Satisfied&0x20 == 0 || n.DynamicGate&0x20 == 0 {
			t.Fatalf("arrival failed to wake phase-zero retry: %+v", n)
		}
		if route.Active || route.WantsRepath || sys.hasControllerGoal(u.Handle) || sys.HasPathRequest(u.Handle) {
			t.Fatal("arrival retained stale controller/route/request")
		}
		// Ordinary primary pumping must consume arrival before the retry deadline.
		q.Pump(u, 108530)
		if q.Head() == n || q.LenPrimary() != 0 {
			t.Fatal("normal arrival did not remove terminal move")
		}
		if sys.serviceGroundFollower(u, n, route, 108531) || route.WantsRepath || sys.HasPathRequest(u.Handle) {
			t.Fatal("detached old head restarted its route")
		}
	}
}

func TestCrowdedLocalAdmissionRejectsNonCrowdAndCloserFreeAnchor(t *testing.T) {
	for _, kind := range []string{"empty goal", "enemy", "moving", "structure", "static obstruction", "active route", "closer free anchor"} {
		t.Run(kind, func(t *testing.T) {
			sys, u, _, n := capturedCrowdedRally(t, true)
			goalOccupant := sys.world.Unit(2)
			switch kind {
			case "empty goal":
				c := handleRow(sys.Collisions, goalOccupant.Handle)
				sys.Grid.ClearPlane(PlaneGround, c.StampedAnchor, c.FootPrintX, c.FootPrintZ, c.ID)
			case "enemy":
				goalOccupant.Owner = 1
			case "moving":
				goalOccupant.Move.Speed = 1
			case "structure":
				handleRow(sys.Collisions, goalOccupant.Handle).Building = true
			case "static obstruction":
				sys.Terrain.PlotAt(4, 5).SetHeight(255)
				sys.Terrain.PlotAt(4, 5).SetMaxHeight(255)
			case "active route":
				r := handleRow(sys.Routes, u.Handle)
				r.Active = true
				r.Count = 2
			case "closer free anchor":
				// Remove the southern neighbour (captured Flash126), opening a
				// footprint anchor which is immediately closer to the destination.
				v := sys.world.Unit(5)
				c := handleRow(sys.Collisions, v.Handle)
				sys.Grid.ClearPlane(PlaneGround, c.StampedAnchor, c.FootPrintX, c.FootPrintZ, c.ID)
			}
			if x, z, ok := sys.CrowdedMoveBlocked(u, n); ok {
				t.Fatalf("admitted %s at %d,%d", kind, x, z)
			}
		})
	}
}

// A one-cell mover with an open closer anchor must keep trying even if the
// destination itself is occupied. It has not yet reached this local frontier.
func TestCrowdedGoalAloneDoesNotComplete(t *testing.T) {
	sys, u, _, n, _ := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 5, Z: 2})
	h, err := sys.world.Create(wiringDef(), 0, world.CellToWorld(5), numeric.FixedFromInt(10), world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	sys.EnsureUnit(sys.world.Unit(h))
	if _, _, ok := sys.CrowdedMoveBlocked(u, n); ok {
		t.Fatal("open approach counted as final local frontier")
	}
}
