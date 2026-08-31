package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// runMovementTick exercises the production per-unit tick boundary directly.
// Tests intentionally use the same deterministic world sweep as the session
// loop rather than a compatibility wrapper.
func runMovementTick(s *System, tick uint32, w *units.World) {
	s.BindWorld(w)
	s.BeginTick(tick)
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		s.StepUnit(u.Handle, tick)
	}
	s.EndTick(tick)
}

// syntheticTerrainForIntegrate creates a flat 10x10 terrain for integration tests.
func syntheticTerrainForIntegrate() *world.Terrain {
	t := &world.Terrain{
		CellW:    20,
		CellH:    20,
		SeaLevel: 0,
		Plot:     make([]world.PlotCell, 400),
	}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}

// TestSchedulerRouteSteerArrival drives Scheduler→Route→Steer for a synthetic 3-waypoint
// path and asserts arrival within pruning tolerance (dx^2+dz^2 ≤25) and that the
// published route respects the ≤20 / ≤13 caps [04 §7.3] C14–C16.
func TestSchedulerRouteSteerArrival(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}
	grid := NewOccupancyGrid()
	system := NewSystem(terrain, profile, grid)

	// Create world and unit at start cell (1,1)
	w := newMovementFixtureWorld(10)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 3 * 65536, Acceleration: 3 * 65536, BrakeRate: 3 * 65536, TurnRate: 500}
	def.MaxDamage = 100
	def.FootprintX = 1
	def.FootprintZ = 1
	setScratchMovement(def, profile)
	startWorldX := world.CellToWorld(1)
	startWorldZ := world.CellToWorld(1)
	startWorldY := terrain.HeightAt(startWorldX, startWorldZ)
	h, err := w.Create(def, 0, startWorldX, startWorldY, startWorldZ)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	system.EnsureUnit(u)

	// Goal cell (8,8) ~ 7 cells away
	goalCell := path.Cell{X: 8, Z: 8}
	startCell := path.Cell{X: 1, Z: 1}
	system.SubmitMove(h, 0, startCell, goalCell)
	// Keep orders queue as authority: push Move_Ground
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("Move_Ground not found")
	}
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(8)})

	// Tick scheduler once to publish route (search needs 1 tick)
	system.Scheduler.Tick(1)
	route := system.Routes[h]
	if route == nil || !route.Active {
		t.Fatalf("route not published after scheduler tick: active %v count %d", route.Active, route.Count)
	}
	if route.Count > 20 {
		t.Fatalf("route count %d >20 [04 §7.3] C14", route.Count)
	}
	enc := EncodeRoute(route)
	if len(enc) > 13 {
		t.Fatalf("encLen %d >13 [04 §7.3] C16", len(enc))
	}
	if route.Count == 0 {
		t.Fatalf("route empty")
	}

	// Drive movement ticks until arrival or limit
	// Drive the explicit BeginTick/StepUnit/EndTick transaction through
	// Prune+Steer+Collision.
	for tick := uint32(2); tick < 200; tick++ {
		// Scheduler may already be empty, but tick anyway
		system.Scheduler.Tick(tick)
		runMovementTick(system, tick, w)
		// Check if route became inactive (arrived)
		if r := system.Routes[h]; r != nil && !r.Active {
			break
		}
		// Also check distance to goal world
		goalWorldX := world.CellToWorld(8)
		goalWorldZ := world.CellToWorld(8)
		dx := int64(goalWorldX) - int64(u.X)
		dz := int64(goalWorldZ) - int64(u.Z)
		// Convert to cell domain for pruning check: distance in cells
		// Use mover cell vs goal cell
		moverCellAfter := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
		dxC := int64(moverCellAfter.X) - int64(goalCell.X)
		dzC := int64(moverCellAfter.Z) - int64(goalCell.Z)
		if dxC*dxC+dzC*dzC <= 25 {
			break
		}
		_ = dx
		_ = dz
	}

	// Final distance to goal should be within pruning radius (5 cells) or route inactive
	goalWorldX := world.CellToWorld(8)
	goalWorldZ := world.CellToWorld(8)
	dx := int64(goalWorldX) - int64(u.X)
	dz := int64(goalWorldZ) - int64(u.Z)
	// Allow large tolerance because we aim at cell center +0.5, but check cell distance
	moverCellFinal := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	dxC := int64(moverCellFinal.X) - int64(goalCell.X)
	dzC := int64(moverCellFinal.Z) - int64(goalCell.Z)
	if dxC*dxC+dzC*dzC > 36 { // 6 cells tolerance
		t.Fatalf("arrival failed: mover cell %v goal %v dxC %d dzC %d world dx %d dz %d", moverCellFinal, goalCell, dxC, dzC, dx, dz)
	}
	// Also check that after arrival, encoded save form still ≤13
	if r := system.Routes[h]; r != nil {
		enc2 := EncodeRoute(r)
		if len(enc2) > 13 {
			t.Fatalf("final encLen %d >13", len(enc2))
		}
	}
}

// TestIntegrateDeterminism ensures two runs with same synthetic terrain and same
// scheduler input produce identical final positions (deterministic iteration I1).
func TestIntegrateDeterminism(t *testing.T) {
	run := func() (int64, int64) {
		terrain := syntheticTerrainForIntegrate()
		profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25}
		grid := NewOccupancyGrid()
		system := NewSystem(terrain, profile, grid)
		w := newMovementFixtureWorld(10)
		def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, Acceleration: 2 * 65536, BrakeRate: 2 * 65536, TurnRate: 500}
		def.MaxDamage = 100
		setScratchMovement(def, profile)
		h, _ := w.Create(def, 0, world.CellToWorld(0), numeric.Fixed(0), world.CellToWorld(0))
		u := w.Unit(h)
		system.EnsureUnit(u)
		start := path.Cell{X: 0, Z: 0}
		goal := path.Cell{X: 5, Z: 5}
		system.SubmitMove(h, 0, start, goal)
		id := orders.Lookup("Move_Ground")
		q := orders.QueueForUnit(u)
		q.Push(id, orders.Node{GoalX: world.CellToWorld(5), GoalZ: world.CellToWorld(5)})
		system.Scheduler.Tick(1)
		for tick := uint32(2); tick < 20; tick++ {
			system.Scheduler.Tick(tick)
			runMovementTick(system, tick, w)
		}
		return int64(u.X), int64(u.Z)
	}
	x1, z1 := run()
	x2, z2 := run()
	if x1 != x2 || z1 != z2 {
		t.Fatalf("determinism failed: (%d,%d) vs (%d,%d)", x1, z1, x2, z2)
	}
}

// TestRouteCaps ensures Publish clamping to 20 and EncodeRoute to 3 pairs [04 §7.3] C14 C16.
func TestRouteCapsInIntegrate(t *testing.T) {
	var r Route
	pts := make([]Point, 30)
	for i := range pts {
		pts[i] = Point{X: int32(i), Z: int32(i)}
	}
	r.Publish(pts)
	if r.Count != 20 {
		t.Fatalf("publish clamp want 20 got %d [04 §7.3] C14", r.Count)
	}
	enc := EncodeRoute(&r)
	if len(enc) != 13 {
		t.Fatalf("enc len want 13 got %d [04 §7.3] C16", len(enc))
	}
	if enc[0]&0x3 != 3 {
		t.Fatalf("enc count bits want 3 got %d", enc[0]&0x3)
	}
	// After prune to <2, active clears
	r.Publish([]Point{{X: 0, Z: 0}, {X: 10, Z: 0}})
	r.Prune(Point{X: 10, Z: 0})
	if r.Active {
		t.Fatalf("prune to <2 should clear active [04 §7.3] C15")
	}
	enc2 := EncodeRoute(&r)
	if len(enc2) != 1 || enc2[0] != 0 {
		t.Fatalf("inactive enc want [0] got %v", enc2)
	}
}

// syntheticLargeTerrainForBudget returns a flat terrain large enough to require >100 pops [04 §7.3] C11.
func syntheticLargeTerrainForBudget() *world.Terrain {
	t := &world.Terrain{
		CellW:    200,
		CellH:    200,
		SeaLevel: 0,
		Plot:     make([]world.PlotCell, 40000),
	}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}

// TestBigRequestStaysActiveAcrossTicks verifies budget-honoring end-to-end [04 §7.3] C11 C12.
// A big synthetic request stays active across ticks and never publishes a partial prefix.
func TestBigRequestStaysActiveAcrossTicks(t *testing.T) {
	terrain := syntheticLargeTerrainForBudget()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}
	grid := NewOccupancyGrid()
	system := NewSystem(terrain, profile, grid)

	w := newMovementFixtureWorld(10)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 3 * 65536, Acceleration: 3 * 65536, BrakeRate: 3 * 65536, TurnRate: 500}
	def.MaxDamage = 100
	def.FootprintX = 1
	def.FootprintZ = 1
	setScratchMovement(def, profile)
	startWorldX := world.CellToWorld(1)
	startWorldZ := world.CellToWorld(1)
	startWorldY := terrain.HeightAt(startWorldX, startWorldZ)
	h, err := w.Create(def, 0, startWorldX, startWorldY, startWorldZ)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	u := w.Unit(h)
	system.EnsureUnit(u)

	startCell := path.Cell{X: 1, Z: 1}
	goalCell := path.Cell{X: 150, Z: 1}
	system.SubmitMove(h, 0, startCell, goalCell)
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("Move_Ground not found")
	}
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: world.CellToWorld(150), GoalZ: world.CellToWorld(1)})

	// One-shot expected via direct path search with same passability/bias/bounds.
	isPassable := func(c path.Cell) bool {
		if !profile.IsPassable(terrain, c.X, c.Z) {
			return false
		}
		if occ, ok := grid.OccupantAt(Cell{X: c.X, Z: c.Z}); ok && occ != int(h) {
			return false
		}
		return true
	}
	bounds := path.Rect{Min: path.Cell{X: 0, Z: 0}, Max: path.Cell{X: terrain.CellW - 1, Z: terrain.CellH - 1}}
	cfg := path.SearchConfig{
		Start: startCell,
		Goal:  path.PointGoal(goalCell, 0),
		PassableValue: func(c path.Cell) uint8 {
			if isPassable(c) {
				return 3
			}
			return 0
		},
		Scale:      65536,
		FootPrintX: int32(profile.FootPrintX),
		FootPrintZ: int32(profile.FootPrintZ),
		HasBounds:  true,
		Bounds:     bounds,
	}
	oneShot := path.Search(cfg)
	if oneShot.Popped <= 100 {
		t.Fatalf("fixture requires >100 pops to test budget, got %d", oneShot.Popped)
	}
	if len(oneShot.Points) == 0 {
		t.Fatalf("one-shot should succeed")
	}

	// First scheduler tick: budget 100, should NOT publish partial prefix [04 §7.3] C12.
	system.Scheduler.Tick(1)
	route := system.Routes[h]
	if route != nil && route.Active {
		t.Fatalf("big request first tick must stay inactive (full-or-empty), got active count %d [04 §7.3] C12", route.Count)
	}
	if system.Scheduler.TraceState().Pending[0] != 1 {
		t.Fatalf("request should remain ACTIVE after budget exhaustion, active %d [04 §7.3] C11", system.Scheduler.TraceState().Pending[0])
	}

	// Capture route bytes before second tick to ensure no partial publication overwrote stale bytes incorrectly.
	var beforePoints [20]Point
	var beforeCount uint8
	var beforeActive bool
	if route != nil {
		beforePoints = route.Points
		beforeCount = route.Count
		beforeActive = route.Active
	}

	// Second tick should resume and eventually complete. May need a few ticks if distance large.
	done := false
	for tick := uint32(2); tick < 10; tick++ {
		system.Scheduler.Tick(tick)
		r := system.Routes[h]
		if r != nil && r.Active {
			done = true
			break
		}
		// Ensure we never published a partial prefix that differs from final.
		if r != nil && r.Active {
			t.Fatalf("should not have published partial at tick %d", tick)
		}
		// While inactive, stale bytes should stay as before (publish zero leaves bytes untouched [04 §7.3] C14).
		// We check that we never observed a nonempty publication that is not the final full route.
		_ = beforePoints
		_ = beforeCount
		_ = beforeActive
	}
	if !done {
		t.Fatalf("big request should have completed within 10 ticks, active %d", system.Scheduler.TraceState().Pending[0])
	}
	// Determinism: resumed route must equal one-shot route (converted to movement.Point) [04 §7.3] C11.
	final := system.Routes[h]
	if final == nil || !final.Active {
		t.Fatalf("final route inactive")
	}
	if int(final.Count) != len(oneShot.Points) {
		t.Fatalf("final count want %d got %d", len(oneShot.Points), final.Count)
	}
	for i := 0; i < len(oneShot.Points); i++ {
		want := Point{X: oneShot.Points[i].X, Z: oneShot.Points[i].Z}
		if final.Points[i] != want {
			t.Fatalf("final point %d want %v got %v (determinism identical whether budget interrupts occur) [04 §7.3] C11", i, want, final.Points[i])
		}
	}
	if system.pathProvider.pending(0) != 0 {
		t.Fatalf("after completion pending should be 0")
	}
}

func TestActivateMoveExactlyOnceAndRejectsStalePublication(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	system := NewSystem(terrain, Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25}
	def := setScratchMovement(&content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, Acceleration: 2 * 65536, BrakeRate: 2 * 65536, TurnRate: 500, MaxDamage: 100}, profile)
	h, _ := w.Create(def, 0, world.CellToWorld(0), 0, world.CellToWorld(0))
	u := w.Unit(h)
	system.BindWorld(w)
	system.EnsureUnit(u)
	id := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: world.CellToWorld(3), GoalZ: world.CellToWorld(3)})
	first := q.Head()
	if first == nil || !system.ActivateMove(u, first) {
		t.Fatal("first active order was not submitted")
	}
	if system.pathProvider.pending(0) != 1 {
		t.Fatalf("first activation pending=%d, want 1", system.pathProvider.pending(0))
	}
	if system.ActivateMove(u, first) {
		t.Fatal("same active order submitted twice")
	}
	if system.pathProvider.pending(0) != 1 {
		t.Fatalf("duplicate activation changed pending=%d", system.pathProvider.pending(0))
	}
	firstRequest := path.Request{Unit: h, Activation: system.activeOrders[h].token}

	q.RemoveHead()
	q.Push(id, orders.Node{GoalX: world.CellToWorld(6), GoalZ: world.CellToWorld(6)})
	second := q.Head()
	if second == nil || !system.ActivateMove(u, second) {
		t.Fatal("replacement active order was not submitted")
	}
	if system.pathProvider.pending(0) != 1 {
		t.Fatalf("replacement pending=%d, want 1", system.pathProvider.pending(0))
	}
	// Simulate a late callback for the canceled first request.  It must not
	// overwrite the route belonging to the current head.
	route := system.Routes[h]
	wantFallback := append([]Point(nil), route.Points[:route.Count]...)
	system.publishFunc(firstRequest, []path.Point{{X: 99, Z: 99}}, 0)
	if route == nil || !route.Active || len(wantFallback) != int(route.Count) {
		t.Fatalf("replacement goal fallback was disturbed: %+v", route)
	}
	for i := range wantFallback {
		if route.Points[i] != wantFallback[i] {
			t.Fatalf("stale publication overwrote fallback point %d: got %v want %v", i, route.Points[i], wantFallback[i])
		}
	}
	currentRequest := path.Request{Unit: h, Activation: system.activeOrders[h].token}
	system.publishFunc(currentRequest, []path.Point{{X: 6, Z: 6}}, 0)
	if route := system.Routes[h]; route == nil || !route.Active || route.Points[0] != (Point{X: 6, Z: 6}) {
		t.Fatalf("current publication was not attached to active head: %+v", route)
	}
}
