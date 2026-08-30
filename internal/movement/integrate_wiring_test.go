package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

// wiringDef is a minimal movable ground unit for the wiring tests.
func wiringDef() *content.UnitDef {
	return &content.UnitDef{
		UnitName: "armflea", MaxDamage: 100, BMCode: true, CanMove: true,
		MaxVelocity: 8 * 65536, Acceleration: 8 * 65536, BrakeRate: 8 * 65536, TurnRate: 500,
	}
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
	sess := sys.sessions[int(h)]
	if sess == nil {
		t.Fatalf("session missing after partial search")
	}
	cfg := sess.Config()
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

	sys.SubmitMove(h, 0, start, goal)
	sys.Scheduler.Tick(50)
	route := sys.Routes[h]
	if route == nil || route.Count == 0 || route.Status != 0 {
		t.Fatalf("flat terrain must publish a route, got %+v", route)
	}

	// Paint an 8-neighbour enclosure of the start cell. The enclosure lies
	// outside the 1x1 footprint the revision pass re-stamps, so only the
	// layer can have blocked it.
	layer := sys.layerRegistry.For("", wiringProfile)
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
	sys.ConfigurePath(2, 10)
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
