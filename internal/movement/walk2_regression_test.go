package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestFootprintCenterQuantizesToAnchor(t *testing.T) {
	tests := []struct {
		anchor Cell
		fx, fz int16
	}{
		{anchor: Cell{X: 3, Z: 7}, fx: 1, fz: 1},
		{anchor: Cell{X: 3, Z: 7}, fx: 2, fz: 2},
		{anchor: Cell{X: -5, Z: -2}, fx: 3, fz: 2},
	}
	for _, tc := range tests {
		x, z := world.PlacementCenter(tc.anchor.X, tc.anchor.Z, int32(tc.fx), int32(tc.fz))
		state := CollisionState{FootPrintX: tc.fx, FootPrintZ: tc.fz}
		bx, bz := state.HalfBias()
		if got := QuantizedAnchor(int32(x), int32(z), bx, bz); got != tc.anchor {
			t.Fatalf("anchor %v footprint %dx%d quantized to %v", tc.anchor, tc.fx, tc.fz, got)
		}
	}
}

func TestFollowerRequestsContinuationAtSixtyTicks(t *testing.T) {
	profile := wiringProfile
	profile.FootPrintX, profile.FootPrintZ = 2, 2
	system := NewSystem(syntheticTerrainForIntegrate(), profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	def := &content.UnitDef{
		UnitName: "continuation-test", FootprintX: 2, FootprintZ: 2,
		MaxVelocity: 65536, Acceleration: 65536, BrakeRate: 65536, TurnRate: 1024,
	}
	setScratchMovement(def, profile)
	x, z := world.PlacementCenter(4, 6, 2, 2)
	h, err := w.Create(def, 0, x, 0, z)
	if err != nil {
		t.Fatal(err)
	}
	system.BindWorld(w)
	system.EnsureUnit(w.Unit(h))
	q := orders.QueueForUnit(w.Unit(h))
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: h, GoalX: world.CellToWorld(20), GoalZ: world.CellToWorld(22)})
	head := q.Head()
	// `Move_Ground` phase 0 installs its point goal [04 R-ORD-01 §4], and the
	// follower's repath arm runs only "with a payload installed"
	// [04 R-MOV-03 §2 step 3]. The install detaches the active binding, so it
	// runs before the binding is planted here.
	system.InstallPointGoal(orders.PointGoalRequest{Owner: head.Owner, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	system.activeOrders[h] = &activeMove{order: head, token: 9}
	route := system.Routes[h]
	route.Active = true
	route.Count = 1 // published prefix exhausted: fewer than two points
	route.LastRequestTick = 0

	system.serviceGroundFollower(w.Unit(h), head, route, 59)
	if system.HasPathRequest(h) {
		t.Fatal("follower requested before the 60-tick cadence")
	}
	system.serviceGroundFollower(w.Unit(h), head, route, 60)
	requests := system.pathProvider.allRequests()
	if len(requests) != 1 {
		t.Fatalf("requests=%d want 1", len(requests))
	}
	if got := requests[0].Start; got != (path.Cell{X: 4, Z: 6}) {
		t.Fatalf("request start=%v want cached anchor (4,6)", got)
	}
	goal := path.DescribeGoal(requests[0].Goal)
	if goal.Center != (path.Cell{X: 19, Z: 21}) {
		t.Fatalf("request goal=%#v want footprint-biased cell (19,21)", requests[0].Goal)
	}
	system.Scheduler.Tick(60)
	if route.WantsRepath || route.LastRequestTick != 60 {
		t.Fatalf("follower poll state wants=%v tick=%d", route.WantsRepath, route.LastRequestTick)
	}
}

func TestRouteLookaheadAndStrictBrakingGates(t *testing.T) {
	route := &Route{Active: true, Count: 3, Points: [20]Point{{X: 0}, {X: 160}, {X: 160, Z: 160}}}
	t1x, t1z, t2x, t2z := routeTargets(route, 0, 0)
	if t1x != 80<<16 || t1z != 0 || t2x != 160<<16 || t2z != 160<<16 {
		t.Fatalf("targets=(%d,%d) (%d,%d)", t1x, t1z, t2x, t2z)
	}

	steer := &SteerState{Heading: 0, Speed: 16 << 16, TurnRate: 16384, BrakeRate: 8 << 16}
	if followerAccelerates(steer, 16384, 0, 0, 32<<16, 0, 64<<16, 0) {
		t.Fatal("turn-distance equality must brake")
	}
	if followerAccelerates(steer, 0, 0, 0, 64<<16, 0, 16<<16, 0) {
		t.Fatal("stopping-distance equality must brake")
	}
	if !followerAccelerates(steer, 0, 0, 0, 64<<16, 0, 17<<16, 0) {
		t.Fatal("strictly passing both distance gates must accelerate")
	}
}

func TestFollowerSpeedHasNoZeroAccelerationShortcut(t *testing.T) {
	steer := &SteerState{Speed: 10 << 16, MaxVelocity: 100 << 16}
	steer.UpdateFollowerSpeed(100<<16, true, true)
	if steer.Speed != 10<<16 {
		t.Fatalf("zero acceleration changed speed to %d", steer.Speed)
	}
}

// TestGroundRouteAcceptanceGates locks the three acceptance gates of
// [04 R-PATH-01 §8]. They belong to the GOAL INSTALLER, not to publication:
// the search's publisher adopts a route verbatim [04 R-PATH-01 §7]
// [05 R-EGRESS-02]. The arithmetic asserted below is unchanged; only the entry
// point moved, from installGroundRoute to installGroundGoal, and each case now
// seeds the points the installer inherits from the previous goal.
func TestGroundRouteAcceptanceGates(t *testing.T) {
	unit := &units.Unit{X: 0, Z: 0}
	goal := path.PointGoal(path.Cell{X: 20, Z: 0}, 0)
	goalX := numeric.Fixed(320 << 16)

	t.Run("terminal cell", func(t *testing.T) {
		route := &Route{LastRequestTick: 80}
		route.PublishAtRevision([]Point{{X: 0}, {X: 100}, {X: 320}}, 7)
		installGroundGoal(route, unit, goal, goalX, 0, true, true, 7, 100)
		if !route.Active || route.WantsRepath || route.Count != 3 || route.LastRequestTick != 0 {
			t.Fatalf("terminal acceptance route=%+v", route)
		}
	})

	t.Run("strictly inside half", func(t *testing.T) {
		route := &Route{}
		route.PublishAtRevision([]Point{{X: 0}, {X: 100}, {X: 200}}, 7)
		installGroundGoal(route, unit, goal, goalX, 0, true, true, 7, 1)
		if !route.Active || !route.WantsRepath || route.Count != 3 {
			t.Fatalf("half-distance acceptance route=%+v", route)
		}
	})

	t.Run("half equality falls back", func(t *testing.T) {
		route := &Route{}
		route.PublishAtRevision([]Point{{X: 0}, {X: 100}, {X: 160}}, 7)
		installGroundGoal(route, unit, goal, goalX, 0, true, true, 7, 1)
		if !route.Active || !route.WantsRepath || route.Count != 2 ||
			route.Points[0] != (Point{X: 0, Z: 0}) || route.Points[1] != (Point{X: 320, Z: 0}) {
			t.Fatalf("synthetic route=%+v", route)
		}
	})

	t.Run("short route falls back", func(t *testing.T) {
		route := &Route{}
		route.PublishAtRevision([]Point{{X: 0}, {X: 300}}, 7)
		installGroundGoal(route, unit, goal, goalX, 0, true, true, 7, 1)
		if route.Count != 2 || route.Points[1] != (Point{X: 320}) || !route.WantsRepath {
			t.Fatalf("short synthetic route=%+v", route)
		}
	})

	t.Run("rectangle far-edge goal point", func(t *testing.T) {
		route := &Route{}
		rect := path.RectPerimeterGoal(path.Rect{Min: path.Cell{X: 20, Z: 4}, Max: path.Cell{X: 24, Z: 8}})
		goalX, goalZ, ok := groundGoalPoint(rect, unit, 1, 1)
		if !ok || goalX != numeric.Fixed(360<<16) || goalZ != numeric.Fixed(136<<16) {
			t.Fatalf("rectangle goal point=(%d,%d,%v) want middle X 360 and far Z 136", goalX, goalZ, ok)
		}
		route.PublishAtRevision([]Point{{X: 0}, {X: 100}, {X: 160}}, 7)
		installGroundGoal(route, unit, rect, goalX, goalZ, true, true, 7, 1)
		if !route.Active || !route.WantsRepath || route.Count != 2 || route.Points[1] != (Point{X: 360, Z: 136}) {
			t.Fatalf("rectangle synthetic route=%+v", route)
		}
	})
}

func TestAnnulusGoalPointUsesBearingAndBandMidpoint(t *testing.T) {
	goal := path.AnnulusGoal(path.Cell{X: 10, Z: 10}, 32, 64)
	center := numeric.Fixed(168 << 16)
	u := &units.Unit{X: center + 200<<16, Z: center + 100<<16}
	goalX, goalZ, ok := groundGoalPoint(goal, u, 1, 1)
	if !ok {
		t.Fatal("annulus declined its goal point")
	}
	radius := int64(48 << 16)
	bearing := numeric.AngleFromAtan2(200, 100)
	tableAngle := bearing + 0x20
	offsetX := numeric.Fixed((radius*int64(numeric.Sin(tableAngle)) + 0x1000) >> 13)
	offsetZ := numeric.Fixed((radius*int64(numeric.Cos(tableAngle)) + 0x1000) >> 13)
	if goalX != center+offsetX || goalZ != center+offsetZ {
		t.Fatalf("annulus goal point=(%d,%d) want non-axis ring point (%d,%d)", goalX, goalZ, center+offsetX, center+offsetZ)
	}
}

func TestActivateMoveInstallsImmediateSyntheticRoute(t *testing.T) {
	profile := wiringProfile
	system := NewSystem(syntheticTerrainForIntegrate(), profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(2)
	def := &content.UnitDef{
		UnitName: "activation-fallback", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 65536, Acceleration: 65536, BrakeRate: 65536, TurnRate: 1024,
	}
	h, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	system.BindWorld(w)
	u := w.Unit(h)
	system.EnsureUnit(u)
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(9), GoalZ: world.CellToWorld(7)})
	if !system.ActivateMove(u, q.Head()) {
		t.Fatal("move did not activate")
	}
	route := system.Routes[h]
	if route == nil || !route.Active || route.Count != 2 || !route.WantsRepath {
		t.Fatalf("new goal did not install live fallback: %+v", route)
	}
	if route.Points[0] != (Point{X: 16, Z: 16}) || route.Points[1] != (Point{X: 152, Z: 120}) {
		t.Fatalf("activation fallback points=%v", route.Points[:route.Count])
	}
}

func TestCommitTerrainChecksEachFootprintCell(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	terrain.SeaLevel = 0
	terrain.PlotAt(2, 2).SetMinHeight(10)
	terrain.PlotAt(2, 2).SetMaxHeight(14)
	terrain.PlotAt(3, 2).SetMinHeight(18)
	terrain.PlotAt(3, 2).SetMaxHeight(22)
	profile := Profile{FootPrintX: 2, FootPrintZ: 1, MinWaterDepth: -10000, MaxWaterDepth: 10000, MaxSlope: 5}
	// Both cells span 4 on their own pairs and the aggregate span is 12. The
	// footprint classifier and the commit validator now agree, because both
	// are per cell: the aggregate belongs to the structure placement validator
	// alone [04 R-SLOPE-01 §3]. Before WU-19-46 the footprint side rejected
	// this anchor while the commit side accepted both its cells.
	if !profile.IsPassableFootprint(terrain, 2, 2) {
		t.Fatal("per-cell footprint classifier rejected an anchor whose every cell is legal [04 R-SLOPE-01 §3]")
	}
	if !profile.IsPassableCommitCell(terrain, 2, 2) || !profile.IsPassableCommitCell(terrain, 3, 2) {
		t.Fatal("individually legal footprint cells were rejected")
	}

	terrain.SeaLevel = 20
	terrain.PlotAt(4, 4).SetMinHeight(10)
	terrain.PlotAt(4, 4).SetMaxHeight(15)
	profile.MaxSlope = 10
	profile.MaxWaterSlope = 2
	if !profile.IsPassableCommitCell(terrain, 4, 4) {
		t.Fatal("water slope below MaxSlope must not consult MaxWaterSlope")
	}
}

func TestCommitRectangleRejectsFinalMapRowAndColumn(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	if commitRectInBounds(terrain, Cell{X: 19, Z: 1}, 1, 1) {
		t.Fatal("1x1 footprint entered the final column")
	}
	if commitRectInBounds(terrain, Cell{X: 18, Z: 1}, 2, 1) {
		t.Fatal("2x1 footprint whose extent reaches the final column passed")
	}
	if !commitRectInBounds(terrain, Cell{X: 17, Z: 18}, 2, 1) {
		t.Fatal("last legal 2x1 anchor was rejected")
	}
}

func TestGroundPostMoveHeightBranches(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	terrain.SeaLevel = 100
	u := &units.Unit{X: world.CellToWorld(2), Z: world.CellToWorld(2)}

	u.Def = &content.UnitDef{Upright: true, Waterline: 20}
	if got, ok := groundPostMoveHeight(terrain, u); !ok || got != 10<<16 {
		t.Fatalf("upright ground height=(%d,%v) want 10", got, ok)
	}
	u.Def.CanHover = true
	if got, ok := groundPostMoveHeight(terrain, u); !ok || got != 80<<16 {
		t.Fatalf("upright hover height=(%d,%v) want 80", got, ok)
	}
	u.Def = &content.UnitDef{Floater: true, Waterline: 20}
	if got, ok := groundPostMoveHeight(terrain, u); !ok || got != 80<<16 {
		t.Fatalf("floater height=(%d,%v) want 80", got, ok)
	}
}
