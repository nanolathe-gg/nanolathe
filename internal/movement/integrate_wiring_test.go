package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// wiringDef is a minimal movable ground unit for the wiring tests.
func wiringDef() *content.UnitDef {
	return setScratchMovement(&content.UnitDef{
		UnitName: "armflea", MaxDamage: 100, BMCode: 1, CanMove: true,
		MaxVelocity: 8 * 65536, Acceleration: 8 * 65536, BrakeRate: 8 * 65536, TurnRate: 500,
	}, wiringProfile)
}

// wiringProfile is a plain 1x1 ground profile for the wiring tests.
var wiringProfile = Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}

// TestSearchFuncConfigBindsClassLayer locks the production search binding
// [04 §6.1 R-DOC04-B]: searchFunc's config carries the value-form
// PassableValue and the request-revision Revise bound to the requesting
// unit's class layer, the boolean injected form is replaced, and the
// PassableValue closure reads the live layer.
func TestSearchFuncConfigBindsClassLayer(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	h, err := w.Create(wiringDef(), 0, world.CellToWorld(2), terrain.HeightAt(world.CellToWorld(2), world.CellToWorld(2)), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sys.EnsureUnit(w.Unit(h))

	sys.BeginTick(100) // searchFunc binds the revision tick from the tick in scope
	req := path.Request{
		Unit:   h,
		Player: 0,
		Start:  path.Cell{X: 2, Z: 2},
		Goal:   path.PointGoal(path.Cell{X: 9, Z: 9}, 0),
	}
	// A one-pop budget leaves the session alive for config inspection.
	work := sys.searchFunc(req, 65536, 1)
	status, done := work.Status, work.Done
	if done {
		t.Fatalf("one pop must not finish the search (status %d)", status)
	}
	ws := sys.sessions[int(h)]
	if ws == nil || ws.session == nil {
		t.Fatalf("session missing after partial search")
	}
	cfg := ws.session.Config()
	if cfg.PassableValue == nil {
		t.Fatalf("config must carry the layer PassableValue binding [04 §6.1 R-DOC04-B]")
	}
	if cfg.Revise == nil {
		t.Fatalf("config must carry the request revision binding [04 §6.1 R-DOC04-B]")
	}
	// The closure reads the live layer: flat terrain stamps clear; painting
	// the layer blocked flips the same cell to 0.
	cell := path.Cell{X: 7, Z: 3}
	if got := cfg.PassableValue(cell); got != LayerClear {
		t.Fatalf("flat terrain cell want clear(3) got %d", got)
	}
	layer := sys.layerRegistry.For("", wiringProfile)
	layer.setValue(cell.X, cell.Z, LayerBlocked)
	if got := cfg.PassableValue(cell); got != LayerBlocked {
		t.Fatalf("painted layer cell want blocked(0) got %d", got)
	}
	// The revise closure is bound to ReviseFor with the tick in scope: it
	// arms the watermark max(tick,30)−30 on the shared layer [04 §6.1
	// R-DOC04-B].
	cfg.Revise()
	if got := layer.Watermark(); got != revisionWatermark(100) {
		t.Fatalf("revise must arm the watermark on the class layer, want %d got %d", revisionWatermark(100), got)
	}
}

// TestSystemSearchConsultsLayer drives the production search path through
// the System: the terrain is flat, so only the stamped layer can block. A
// painted enclosure of blocked layer cells must reject the request and
// clearing it must restore the route [04 §6.1 R-DOC04-B].
func TestSystemSearchConsultsLayer(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	h, err := w.Create(wiringDef(), 0, world.CellToWorld(2), terrain.HeightAt(world.CellToWorld(2), world.CellToWorld(2)), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sys.EnsureUnit(w.Unit(h))
	sys.BeginTick(50)
	start := path.Cell{X: 2, Z: 2}
	goal := path.Cell{X: 12, Z: 12}

	// Keep this fixture about search consumption, not occupancy bookkeeping: a
	// current commit triggers neither the requester's stale footprint-and-ring
	// restamp [04 R-MOV-03 §3] nor the release's re-wall of the requester's own
	// rectangle [04 R-PATH-01 §14]. It has to be noted BEFORE the first request:
	// the release of a request whose requester still carries a zero commit tick
	// walls that unit's own start cell, and the hand-painted ring below never
	// rewrites (2,2), so the wall would survive every later repaint.
	layer := sys.layerRegistry.For("", wiringProfile)
	layer.NoteCommit(h, 50)

	sys.SubmitMove(h, 0, start, goal)
	sys.Scheduler.Tick(50)
	route := sys.Routes[h]
	if route == nil || route.Count == 0 || route.Status != 0 {
		t.Fatalf("flat terrain must publish a route, got %+v", route)
	}

	// Paint an 8-neighbour enclosure of the start cell directly into the
	// packed layer so this test isolates the search consumer.
	paintRing := func(v uint8) {
		for z := int32(1); z <= 3; z++ {
			for x := int32(1); x <= 3; x++ {
				if x != 2 || z != 2 {
					layer.setValue(x, z, v)
				}
			}
		}
	}
	paintRing(LayerBlocked)
	sys.SubmitMove(h, 0, start, goal)
	sys.Scheduler.Tick(51)
	if route.Status != path.StatusRejected {
		t.Fatalf("layer enclosure must reject the search, status %d", route.Status)
	}

	paintRing(LayerClear)
	sys.SubmitMove(h, 0, start, goal)
	sys.Scheduler.Tick(52)
	if route.Status != 0 || route.Count == 0 {
		t.Fatalf("cleared layer must publish a route again, status %d count %d", route.Status, route.Count)
	}
}

func TestConfiguredSchedulerPublishesOwnerOneRequest(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	sys.ConfigurePath(2, 10, func(player int) bool { return player >= 0 && player < 2 })
	h, err := w.Create(wiringDef(), 1, world.CellToWorld(2), terrain.HeightAt(world.CellToWorld(2), world.CellToWorld(2)), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create owner-one unit: %v", err)
	}
	sys.EnsureUnit(w.Unit(h))
	sys.BeginTick(1)
	sys.SubmitMove(h, 1, path.Cell{X: 2, Z: 2}, path.Cell{X: 9, Z: 9})
	for tick := uint32(1); tick < 10; tick++ {
		sys.Scheduler.Tick(tick)
		if route := sys.Routes[h]; route != nil && route.Count > 0 && route.Status == 0 {
			return
		}
	}
	t.Fatalf("owner-one request did not publish with two-player session topology: route=%+v", sys.Routes[h])
}

// TestMobileBuildRequestsStartAtCommittedAnchor locks the request-init source
// for a build walk whose even footprint makes its transform-centre cell differ
// from its committed footprint anchor. Every admitted request copies the
// cached committed cell; MobileBuild's separately selected approach point
// remains its goal [04 R-PATH-01 §4 step 1][04 §7.4].
func TestMobileBuildRequestsStartAtCommittedAnchor(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	profile := wiringProfile
	profile.FootPrintX, profile.FootPrintZ = 2, 2
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	sys.ConfigurePath(2, 10, func(player int) bool { return player >= 0 && player < 2 })
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)

	def := wiringDef()
	def.UnitName = "c10-builder"
	def.FootprintX, def.FootprintZ = 2, 2
	h, err := w.Create(def, 1, world.CellToWorld(8), 0, world.CellToWorld(18))
	if err != nil {
		t.Fatalf("create 2x2 builder: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	if got, want := sys.pathStartCell(u), (path.Cell{X: 7, Z: 17}); got != want {
		t.Fatalf("committed start = %v, want %v", got, want)
	}
	if got := (path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}); got != (path.Cell{X: 8, Z: 18}) {
		t.Fatalf("fixture transform-centre cell = %v, want asymmetric (8,18)", got)
	}

	id := orders.Lookup("MobileBuild")
	if id == 0 {
		t.Fatal("MobileBuild order unavailable")
	}
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: world.CellToWorld(10), GoalZ: world.CellToWorld(15)})
	head := q.Head()
	selected := path.Cell{X: 8, Z: 13}
	sys.BindMoveGoal(h, head, world.CellToWorld(selected.X), world.CellToWorld(selected.Z))
	wantNode := *head
	schedulerBefore := sys.Scheduler.TraceState()

	if !sys.ActivateMove(u, head) {
		t.Fatal("fresh MobileBuild activation was not accepted")
	}
	requests := sys.PathRequestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("fresh activation requests = %d, want 1", len(requests))
	}
	first := requests[0]
	if first.Start != (path.Cell{X: 7, Z: 17}) {
		t.Fatalf("fresh request start = %v, want cached committed anchor (7,17)", first.Start)
	}
	if goals := first.Goal.Enumerate(nil); !reflect.DeepEqual(goals, []path.Cell{selected}) || !first.Goal.StartSatisfied(selected) || first.Goal.StartSatisfied(path.Cell{X: selected.X + 1, Z: selected.Z}) {
		t.Fatalf("fresh request goal = %v, want selected exact point %v", goals, selected)
	}
	binding := sys.activeOrders[h]
	if binding == nil || binding.order != head || binding.token == 0 || first.Activation != binding.token {
		t.Fatalf("fresh activation identity request=%d binding=%+v head=%p", first.Activation, binding, head)
	}
	if got := sys.Scheduler.TraceState(); !reflect.DeepEqual(got, schedulerBefore) {
		t.Fatalf("request submission consumed scheduler budget: before=%+v after=%+v", schedulerBefore, got)
	}
	if q.Head() != head || !reflect.DeepEqual(*head, wantNode) {
		t.Fatalf("fresh activation mutated order identity/state: head=%p/%p got=%+v want=%+v", q.Head(), head, *head, wantNode)
	}

	// Empty/exhausted publication leaves the same head bound. Once the prior
	// request is gone, the follower must resubmit once at the inclusive
	// LastRequestTick+60 boundary, keeping start, goal and activation identity.
	sys.CancelPathRequest(h)
	route := sys.Routes[h]
	route.Active = false
	route.Count = 3 // stale backing count is irrelevant while inactive.
	route.WantsRepath = true
	route.LastRequestTick = 100
	sys.serviceGroundFollower(u, head, route, 159)
	if got := sys.PathRequestsSnapshot(); len(got) != 0 {
		t.Fatalf("request submitted before inclusive 60-tick boundary: %v", got)
	}
	sys.serviceGroundFollower(u, head, route, 160)
	requests = sys.PathRequestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("boundary resubmission requests = %d, want 1", len(requests))
	}
	retry := requests[0]
	if retry.Start != first.Start || retry.Activation != first.Activation || !reflect.DeepEqual(path.DescribeGoal(retry.Goal), path.DescribeGoal(first.Goal)) {
		t.Fatalf("boundary request changed identity: first=%+v retry=%+v", first, retry)
	}
	if route.LastRequestTick != 160 {
		t.Fatalf("boundary request tick = %d, want 160", route.LastRequestTick)
	}
	sys.serviceGroundFollower(u, head, route, 160)
	if got := sys.PathRequestsSnapshot(); len(got) != 1 {
		t.Fatalf("repeated boundary visit duplicated request: %v", got)
	}
	if got := sys.Scheduler.TraceState(); !reflect.DeepEqual(got, schedulerBefore) {
		t.Fatalf("repath submission consumed scheduler budget: before=%+v after=%+v", schedulerBefore, got)
	}
	if q.Head() != head || !reflect.DeepEqual(*head, wantNode) || sys.activeOrders[h] != binding {
		t.Fatalf("repath mutated order/binding: head=%p/%p got=%+v want=%+v binding=%p/%p", q.Head(), head, *head, wantNode, sys.activeOrders[h], binding)
	}
}

// TestOccupancyCommitNotesRevisionLayers locks the commit-site wiring: a
// successful occupancy commit records the unit's commit tick on every
// allocated class layer, so the request revision pass of [04 §6.1 R-DOC04-B]
// sees it [04 §8.2] C22.
func TestOccupancyCommitNotesRevisionLayers(t *testing.T) {
	terrain := syntheticTerrainFlat()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	h, err := w.Create(wiringDef(), 0, world.CellToWorld(2), terrain.HeightAt(world.CellToWorld(2), world.CellToWorld(2)), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sys.EnsureUnit(w.Unit(h))
	// Two allocated layers: the commit tick must reach every one of them.
	la := sys.ensureLayerRegistry().For("", wiringProfile)
	lb := sys.ensureLayerRegistry().For("tank2", wiringProfile)

	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("Move_Ground not found")
	}
	q := orders.QueueForUnit(w.Unit(h))
	q.Push(id, orders.Node{GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(2)})

	startPoint := Point{X: int32(w.Unit(h).X.Raw() >> 16), Z: int32(w.Unit(h).Z.Raw() >> 16)}
	sys.Routes[h].PublishAtRevision([]Point{startPoint, {X: 192, Z: startPoint.Z}}, sys.staticObstacleRevision())
	// Drive real ticks along an installed route; the occupancy commit succeeds
	// on flat, unoccupied terrain.
	noted := uint32(0)
	for tick := uint32(1); tick <= 40 && noted == 0; tick++ {
		sys.BeginTick(tick)
		res := sys.StepUnit(h, tick)
		sys.EndTick(tick)
		if c, ok := la.CommitTick(h); ok && c != 0 {
			noted = c
			if !res.Moved {
				t.Fatalf("commit tick noted on a tick the mover did not move")
			}
		}
	}
	if noted == 0 {
		t.Fatalf("a movement commit within 40 ticks must note the commit tick")
	}
	if c, ok := lb.CommitTick(h); !ok || c != noted {
		t.Fatalf("commit tick must be noted on every allocated layer, want %d got %d ok %v", noted, c, ok)
	}
}

// TestEnsureUnitStampFeedsClassLayerRevision locks the creation/completion
// stamp's occupant-age publication. A stationary building blocks path search
// only after its frozen stamp tick strictly predates the class watermark; the
// stamp itself does not service the scheduler or submit a request
// [04 R-COLL-01 §4][04 R-PATH-01 §2][04 §6.1 R-DOC04-B].
func TestEnsureUnitStampFeedsClassLayerRevision(t *testing.T) {
	const stampTick = uint32(40)
	terrain := syntheticTerrainFlat()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	sys.ConfigurePath(2, 10, func(player int) bool { return player >= 0 && player < 2 })

	requester, err := w.Create(wiringDef(), 0, world.CellToWorld(2), terrain.HeightAt(world.CellToWorld(2), world.CellToWorld(2)), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	sys.EnsureUnit(w.Unit(requester))
	layer := sys.ensureLayerRegistry().For("", wiringProfile)
	wantRoute := Route{Count: 2, Active: true, Points: [20]Point{{X: 32, Z: 32}, {X: 64, Z: 32}}}
	*sys.Routes[requester] = wantRoute

	buildingDef := &content.UnitDef{UnitName: "armmex", MaxDamage: 100, BMCode: 0, FootprintX: 1, FootprintZ: 1}
	building, err := w.Create(buildingDef, 0, world.CellToWorld(8), terrain.HeightAt(world.CellToWorld(8), world.CellToWorld(8)), world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create building: %v", err)
	}
	requestsBefore := sys.PathRequestsSnapshot()
	schedulerBefore := sys.Scheduler.TraceState()
	sys.BeginTick(stampTick)
	sys.EnsureUnit(w.Unit(building))

	coll := sys.Collisions[building]
	if coll == nil {
		t.Fatal("building collision state missing")
	}
	if occupant, ok := grid.OccupantAt(coll.CachedAnchor); !ok || occupant != int(building) {
		t.Fatalf("creation stamp at %v = %d/%v, want building %d", coll.CachedAnchor, occupant, ok, building)
	}
	if got, ok := layer.CommitTick(building); !ok || got != stampTick {
		t.Fatalf("creation stamp commit tick = %d/%v, want %d/true", got, ok, stampTick)
	}
	if got := sys.PathRequestsSnapshot(); !reflect.DeepEqual(got, requestsBefore) {
		t.Fatalf("EnsureUnit submitted path requests: before=%v after=%v", requestsBefore, got)
	}
	if got := sys.Scheduler.TraceState(); !reflect.DeepEqual(got, schedulerBefore) {
		t.Fatalf("EnsureUnit serviced scheduler: before=%+v after=%+v", schedulerBefore, got)
	}
	if got := *sys.Routes[requester]; !reflect.DeepEqual(got, wantRoute) {
		t.Fatalf("EnsureUnit mutated requester route: got=%+v want=%+v", got, wantRoute)
	}

	// At equality the occupant does not predate the watermark, so it remains
	// traversable. One tick later it belongs to the crossed [40,41) cohort and
	// its footprint is reclassified as blocked. Movement has no RNG input; the
	// request/scheduler assertions above lock the only adjacent call-order seams.
	layer.Revise(stampTick+30, requester, w, sys)
	if got := layer.Watermark(); got != stampTick {
		t.Fatalf("equality watermark = %d, want %d", got, stampTick)
	}
	if got := layer.Value(coll.CachedAnchor.X, coll.CachedAnchor.Z); got == LayerBlocked {
		t.Fatalf("commit tick equal to watermark must remain nonblocked, got %d", got)
	}
	layer.Revise(stampTick+31, requester, w, sys)
	if got := layer.Watermark(); got != stampTick+1 {
		t.Fatalf("crossed watermark = %d, want %d", got, stampTick+1)
	}
	if got := layer.Value(coll.CachedAnchor.X, coll.CachedAnchor.Z); got != LayerBlocked {
		t.Fatalf("stationary building after strict watermark crossing = %d, want blocked(0)", got)
	}
}

func TestEnsureUnitFailedStampDoesNotPublishCommitTick(t *testing.T) {
	terrain := syntheticTerrainFlat()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	layer := sys.ensureLayerRegistry().For("", wiringProfile)
	anchor := Cell{X: 8, Z: 8}
	if !grid.Stamp(anchor, 1, 1, 999) {
		t.Fatal("fixture blocker stamp failed")
	}
	def := &content.UnitDef{UnitName: "armmex", MaxDamage: 100, BMCode: 0, FootprintX: 1, FootprintZ: 1}
	h, err := w.Create(def, 0, world.CellToWorld(anchor.X), terrain.HeightAt(world.CellToWorld(anchor.X), world.CellToWorld(anchor.Z)), world.CellToWorld(anchor.Z))
	if err != nil {
		t.Fatalf("create blocked building: %v", err)
	}
	sys.BeginTick(50)
	sys.EnsureUnit(w.Unit(h))
	if got, ok := layer.CommitTick(h); ok {
		t.Fatalf("failed creation stamp published commit tick %d", got)
	}
	if got, ok := grid.OccupantAt(anchor); !ok || got != 999 {
		t.Fatalf("failed creation stamp changed blocker: got %d/%v", got, ok)
	}
}

// TestBindWorldRefreshesPreallocatedClassLayerRegistry locks the production
// lifecycle where an occupancy commit can allocate the registry before the
// first unit-sweep bind. Revision must still walk owner-1 and the final
// physical pool slot in ascending slot order [01 §6.1–§6.2]
// [04 R-MOV-03 §3].
func TestBindWorldRefreshesPreallocatedClassLayerRegistry(t *testing.T) {
	terrain := syntheticTerrainFlat()
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, wiringProfile, grid)
	registry := sys.ensureLayerRegistry()
	layer := registry.For("", wiringProfile)
	if registry.world != nil && registry.world.Capacity() != 0 {
		t.Fatalf("pre-bind registry world must have no live pool, got %v", registry.world)
	}

	w := newMovementFixtureWorld(8)
	sys.BindWorld(w)
	if registry.world != w {
		t.Fatalf("BindWorld did not refresh preallocated registry: got %p want %p", registry.world, w)
	}
	def := &content.UnitDef{UnitName: "stationary-blocker", MaxDamage: 100, BMCode: 0, FootprintX: 2, FootprintZ: 2}
	hOwner1, err := w.Create(def, 1, world.CellToWorld(8), terrain.HeightAt(world.CellToWorld(8), world.CellToWorld(8)), world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create owner-1 blocker: %v", err)
	}
	if int(hOwner1) <= w.Capacity()/pool.PlayerCount {
		t.Fatalf("owner-1 handle %d must follow first slice end %d", hOwner1, w.Capacity()/pool.PlayerCount)
	}
	last := pool.Handle(w.TotalRecords() - 1)
	hLast, err := w.CreateWithForcedSlot(def, 9, world.CellToWorld(16), terrain.HeightAt(world.CellToWorld(16), world.CellToWorld(16)), world.CellToWorld(16), last)
	if err != nil {
		t.Fatalf("create final-slot blocker: %v", err)
	}
	if hLast != last {
		t.Fatalf("final-slot handle = %d, want %d", hLast, last)
	}

	sys.BeginTick(5)
	sys.EnsureUnit(w.Unit(hOwner1))
	sys.EnsureUnit(w.Unit(hLast))
	if got, ok := layer.CommitTick(hOwner1); !ok || got != 5 {
		t.Fatalf("owner-1 commit = %d/%v, want 5/true", got, ok)
	}
	if got, ok := layer.CommitTick(hLast); !ok || got != 5 {
		t.Fatalf("final-slot commit = %d/%v, want 5/true", got, ok)
	}

	registry.ReviseFor("", wiringProfile, 0, 60)
	for _, cell := range []Cell{{X: 8, Z: 8}, {X: 16, Z: 16}} {
		if got := layer.Value(cell.X, cell.Z); got != LayerBlocked {
			t.Fatalf("full-pool revision anchor %v = %d, want blocked", cell, got)
		}
	}
}
