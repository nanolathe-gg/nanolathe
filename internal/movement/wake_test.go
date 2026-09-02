package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

// wakeTerrain is a flat, fully passable square. The wake tests are about who
// wakes a waiting order record, never about terrain cost.
func wakeTerrain(n int32) *world.Terrain {
	t := &world.Terrain{CellW: n, CellH: n, SeaLevel: 0, Plot: make([]world.PlotCell, int(n*n))}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}

var wakeProfile = Profile{
	FootPrintX: 2, FootPrintZ: 2, MinWaterDepth: -10000, MaxWaterDepth: 12,
	MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127,
}

// TestFactoryExitColumnAllProductsLeave is the egress liveness lock for
// [04 R-EGRESS-01]. Eight no-rally products are respawned at one exit cell, each
// only once the previous one has cleared it. Every one of them must leave.
//
// The three ways this used to jam are all wake failures and all are fixed here:
// the publisher used to run the goal installer's acceptance gates over every
// publication and overwrite a perfectly good two-point route with a synthetic
// straight line back into the blocker ([05 R-EGRESS-02]); a record behind the
// blocked head could steal the mover's binding and delete the head's arrival
// handle; and with neither `0x20` nor `0x40` ever reaching it, `Park` phase 1 —
// the only place the thirty-tick re-arm is armed — was never visited.
func TestFactoryExitColumnAllProductsLeave(t *testing.T) {
	const n = 48
	const products = 8
	sys := NewSystem(wakeTerrain(n), wakeProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(products + 2)
	sys.BindWorld(w)

	def := &content.UnitDef{
		UnitName: "wakeprod", FootprintX: 2, FootprintZ: 2,
		MaxVelocity: 2 * 65536, Acceleration: 65536 / 2, BrakeRate: 65536 / 2, TurnRate: 1000,
		MinWaterDepth: -10000, MaxDamage: 100, BMCode: true,
	}
	parkID := orders.Lookup("Park")
	if parkID == 0 {
		t.Fatal("Park order is unavailable")
	}
	exitX, exitZ := int32(20), int32(20)
	freeExit := func() bool {
		for dz := int32(0); dz < 2; dz++ {
			for dx := int32(0); dx < 2; dx++ {
				if _, ok := sys.Grid.OccupantAt(Cell{X: exitX + dx, Z: exitZ + dz}); ok {
					return false
				}
			}
		}
		return true
	}

	pump := &orders.Pump{World: w}
	spawned := 0
	lastSpawn := uint32(0)
	for tick := uint32(1); tick <= 4000 && spawned < products; tick++ {
		if freeExit() && tick >= lastSpawn+15 {
			h, err := w.Create(def, 0, world.CellToWorld(exitX), 0, world.CellToWorld(exitZ))
			if err != nil {
				t.Fatalf("create product %d: %v", spawned, err)
			}
			sys.EnsureUnit(w.Unit(h))
			orders.QueueForUnit(w.Unit(h)).Push(parkID, orders.Node{Deadline: -1})
			spawned++
			lastSpawn = tick
		}
		sys.Scheduler.Tick(tick)
		sys.BeginTick(tick)
		for _, u := range w.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			pump.PumpUnit(u.Handle, tick)
			if q := orders.QueueOfUnit(u); q != nil {
				if prim := q.Primary(); len(prim) > 0 && orders.DescriptorFor(prim[0].ID).Name == "Park" {
					sys.ActivateMove(u, prim[0])
				}
			}
			sys.StepUnit(u.Handle, tick)
		}
		sys.EndTick(tick)
	}
	if spawned < products {
		t.Fatalf("only %d of %d products left the exit cell: the column jammed", spawned, products)
	}
}

// TestActivateMoveBindsOnlyThePrimaryHead locks the ownership rule of
// [04 §3.3] step 3 and [04 R-PATH-01 §8]: the pump stops at a blocked head, so
// no record behind it can be running a handler, and the follower holds exactly
// one goal object. A bind request for a record that is not the primary head is
// refused outright and leaves the head's binding untouched — the alternation
// that used to cancel the head's path request and delete its arrival handle on
// every other tick.
func TestActivateMoveBindsOnlyThePrimaryHead(t *testing.T) {
	sys := NewSystem(wakeTerrain(32), wakeProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	sys.BindWorld(w)
	sys.ConfigurePath(1, 10)
	def := &content.UnitDef{
		UnitName: "wakehead", FootprintX: 1, FootprintZ: 1,
		MaxVelocity: 65536, Acceleration: 65536, BrakeRate: 65536, TurnRate: 65535,
		MinWaterDepth: -10000, BMCode: true,
	}
	h, err := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)

	moveID := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(u)
	q.Push(moveID, orders.Node{GoalX: world.CellToWorld(10), GoalZ: world.CellToWorld(2), Deadline: -1})
	q.Push(moveID, orders.Node{GoalX: world.CellToWorld(20), GoalZ: world.CellToWorld(2), Deadline: -1})
	prim := q.Primary()
	if len(prim) != 2 {
		t.Fatalf("primary length %d, want 2", len(prim))
	}
	head, behind := prim[0], prim[1]
	if !sys.ActivateMove(u, head) {
		t.Fatal("head failed to activate")
	}
	bindingBefore := sys.activeOrders[h]
	handleBefore := sys.arrivalHandles[h]
	if bindingBefore == nil || handleBefore == nil {
		t.Fatalf("head bind incomplete binding=%v handle=%v", bindingBefore, handleBefore)
	}
	if sys.ActivateMove(u, behind) {
		t.Fatal("a record behind the head must not take the mover")
	}
	if sys.activeOrders[h] != bindingBefore || sys.arrivalHandles[h] != handleBefore {
		t.Fatalf("refused bind disturbed the head: binding=%v handle=%v", sys.activeOrders[h], sys.arrivalHandles[h])
	}
}

// TestPublicationAdoptsTwoPointRouteVerbatim locks [05 R-EGRESS-02]. Collinear
// removal collapses a straight A* run to exactly two points; the publisher
// adopts them as published ([04 R-PATH-01 §7]) and must not apply the goal
// installer's three-or-more acceptance gates, which would reject the route and
// overwrite it with a straight line at the goal.
func TestPublicationAdoptsTwoPointRouteVerbatim(t *testing.T) {
	route := &Route{}
	pts := []Point{{X: 32, Z: 32}, {X: 32, Z: 400}}
	installGroundRoute(route, pts, 7)
	if !route.Active || route.Count != 2 || route.Points[0] != pts[0] || route.Points[1] != pts[1] {
		t.Fatalf("publication rewrote the route: %+v", route)
	}
	if route.WantsRepath {
		t.Fatal("a nonempty publication clears wants-repath [04 R-PATH-01 §7]")
	}
	// An empty publication clears the active bit and leaves the stored bytes.
	installGroundRoute(route, nil, 7)
	if route.Active || route.Count != 2 || route.Points[1] != pts[1] {
		t.Fatalf("empty publication must clear active only: %+v", route)
	}
}

// TestHelpBuildInstallsAnnulusAndArrivesBesideTarget locks the assist approach
// of [04 R-ORD-01 §5] as corrected by [04 R-ORD-01 §12]: `HelpBuild` phase 0
// installs an annulus at the target's position with inner `half` over the
// TARGET's definition footprint and outer `builddistance + half` with
// `builddistance` the BUILDER's, and the follower's arrival question is that
// payload's own start predicate ([04 R-MOV-03 §2]). The fixture's builder is
// 2x2 and its site 4x4 precisely so the two halves differ. Before this the work
// family reached neither ActivateMove nor an arrival handle, so an out-of-reach
// assistant never took a step and its 0xE8 approach gate had no producer at all.
func TestHelpBuildInstallsAnnulusAndArrivesBesideTarget(t *testing.T) {
	sys := NewSystem(wakeTerrain(48), wakeProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	sys.BindWorld(w)
	sys.ConfigurePath(1, 10)
	builderDef := &content.UnitDef{
		UnitName: "wakeassist", FootprintX: 2, FootprintZ: 2, BuildDistance: 60, Builder: true,
		MaxVelocity: 65536, Acceleration: 65536, BrakeRate: 65536, TurnRate: 65535,
		MinWaterDepth: -10000, BMCode: true,
	}
	siteDef := &content.UnitDef{UnitName: "wakesite", FootprintX: 4, FootprintZ: 4, MaxDamage: 100}

	siteH, err := w.Create(siteDef, 0, world.CellToWorld(24), 0, world.CellToWorld(24))
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	bh, err := w.Create(builderDef, 0, world.CellToWorld(6), 0, world.CellToWorld(24))
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	builder := w.Unit(bh)
	sys.EnsureUnit(builder)

	helpID := orders.Lookup("HelpBuild")
	if helpID == 0 {
		t.Fatal("HelpBuild order is unavailable")
	}
	site := w.Unit(siteH)
	q := orders.QueueForUnit(builder)
	q.Push(helpID, orders.Node{Target: siteH, GoalX: site.X, GoalY: site.Y, GoalZ: site.Z, Deadline: -1})
	head := q.Head()

	if !sys.ActivateMove(builder, head) {
		t.Fatal("HelpBuild head failed to reach the mover")
	}
	ah := sys.arrivalHandles[bh]
	if ah == nil || ah.payload == nil {
		t.Fatalf("HelpBuild bound no annulus payload: %+v", ah)
	}
	trace := path.DescribeGoal(ah.payload)
	// The radicand is the TARGET's footprint pair; only builddistance is the
	// builder's [04 R-ORD-01 §12][05 R-WORK-01 §2].
	half := orders.AssistApproachHalf(siteDef.FootprintX, siteDef.FootprintZ)
	if trace.Kind != 2 || trace.A != half || trace.B != builderDef.BuildDistance+half {
		t.Fatalf("annulus=%+v want kind 2 inner %d outer %d", trace, half, builderDef.BuildDistance+half)
	}
	// The builder's own pair would give a different band, so this test would
	// pass on the withdrawn reading only by coincidence.
	if builderHalf := orders.AssistApproachHalf(builderDef.FootprintX, builderDef.FootprintZ); builderHalf == half {
		t.Fatalf("fixture footprints must separate the two readings: both give %d", half)
	}
	// The band is measured in cells after the >>4 quantisation of
	// [04 R-PATH-01 §9], so a builder standing between inner/16 and outer/16
	// cells of the goal centre has arrived and one still far away has not.
	if ah.payload.StartSatisfied(path.Cell{X: 6, Z: 24}) {
		t.Fatal("a builder eighteen cells away must not read as arrived")
	}
	if !ah.payload.StartSatisfied(path.Cell{X: trace.Center.X - half/16, Z: trace.Center.Z}) {
		t.Fatalf("a builder on the inner band edge must read as arrived (half=%d centre=%v)", half, trace.Center)
	}
	if ah.payload.StartSatisfied(trace.Center) {
		t.Fatal("the goal centre itself lies inside the band's hole, not on it")
	}
}
