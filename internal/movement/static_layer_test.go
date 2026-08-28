package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestStaticLayerIgnoresTransientMover locks [04 §8.2] static-layer pathing:
// mobile units are NOT A* walls; pathing runs on the static layer (terrain +
// static features + yard/building occupancy) and arbitrates at commit.
// A route must be found through a cell occupied only by a transient mover.
func TestStaticLayerIgnoresTransientMover(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	// Flat terrain ensures IsPassableFootprint is true everywhere for ground profile.
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, profile, grid)
	w := units.New(100, nil)
	sys.BindWorld(w)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500, FootprintX: 1, FootprintZ: 1, MaxDamage: 100, BMCode: true, CanMove: true}
	def2 := &content.UnitDef{UnitName: "armflea2", MaxVelocity: 2 * 65536, TurnRate: 500, FootprintX: 1, FootprintZ: 1, MaxDamage: 100, BMCode: true, CanMove: true}
	// Transient mover occupying (5,5)
	moverX := world.CellToWorld(5)
	moverZ := world.CellToWorld(5)
	moverY := terrain.HeightAt(moverX, moverZ)
	hMover, err := w.Create(def, 0, moverX, moverY, moverZ)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	sys.EnsureUnit(w.Unit(hMover))
	if occ, ok := grid.OccupantAt(Cell{X: 5, Z: 5}); !ok || occ != int(hMover) {
		t.Fatalf("mover should occupy (5,5) got %v ok %v want %d", occ, ok, hMover)
	}
	// Requester at (1,1) wants (8,8) diagonal through (5,5)
	reqX := world.CellToWorld(1)
	reqZ := world.CellToWorld(1)
	reqY := terrain.HeightAt(reqX, reqZ)
	hReq, err := w.Create(def2, 0, reqX, reqY, reqZ)
	if err != nil {
		t.Fatalf("create requester: %v", err)
	}
	sys.EnsureUnit(w.Unit(hReq))
	// Keep orders queue authority: push Move_Ground
	id := orders.Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("Move_Ground not found")
	}
	goalX := world.CellToWorld(8)
	goalZ := world.CellToWorld(8)
	q := orders.QueueForUnit(w.Unit(hReq))
	q.Push(id, orders.Node{GoalX: goalX, GoalZ: goalZ})
	head := q.Head()
	if head == nil {
		t.Fatalf("head nil")
	}
	// ActivateMove binds one request to the current head [04 §7.3]
	if !sys.ActivateMove(w.Unit(hReq), head) {
		t.Fatalf("ActivateMove failed")
	}
	// Scheduler tick should publish a successful route even though (5,5) is occupied [04 §8.2]
	sys.Scheduler.Tick(1)
	route := sys.Routes[hReq]
	if route == nil || !route.Active {
		t.Fatalf("route should be active through transient mover cell [04 §8.2] static layer, got active=%v status=%v", route.Active, route.Status)
	}
	if route.Status == path.StatusRejected {
		t.Fatalf("route rejected despite static-layer ignore of mover")
	}
	// Route should be deterministic regardless of mover presence.
	// Compute reference route without mover via direct path.Search on same static passability.
	refProfile := sys.ProfileFor(hReq)
	refIsPassable := func(c path.Cell) bool {
		if !refProfile.IsPassableFootprint(terrain, c.X, c.Z) {
			return false
		}
		return true // static layer only
	}
	bias := path.Point{X: int32(refProfile.FootPrintX / 2), Z: int32(refProfile.FootPrintZ / 2)}
	bounds := path.Rect{Min: path.Cell{X: 0, Z: 0}, Max: path.Cell{X: terrain.CellW - 1, Z: terrain.CellH - 1}}
	cfg := path.SearchConfig{
		Start:      path.Cell{X: world.WorldToCell(reqX), Z: world.WorldToCell(reqZ)},
		Goal:       path.PointGoal(path.Cell{X: world.WorldToCell(goalX), Z: world.WorldToCell(goalZ)}, 0),
		IsPassable: refIsPassable,
		Scale:      65536,
		Bias:       bias,
		HasBounds:  true,
		Bounds:     bounds,
	}
	ref := path.Search(cfg)
	if len(ref.Points) == 0 || ref.Status != 0 {
		t.Fatalf("reference static route should succeed")
	}
	if int(route.Count) != len(ref.Points) {
		t.Fatalf("route count with mover present %d vs reference static %d should match [04 §8.2] mover not a wall", route.Count, len(ref.Points))
	}
	for i := 0; i < len(ref.Points); i++ {
		if route.Points[i].X != ref.Points[i].X || route.Points[i].Z != ref.Points[i].Z {
			t.Fatalf("point %d with mover %v vs without %v diverged [04 §8.2]", i, route.Points[i], ref.Points[i])
		}
	}
	// Commit-stage arbitration still sees the mover: validate that TryFastPath not taken and blocked handling would trigger if stepping into occupied cell.
	// We simply check that OccupancyGrid still reports mover at (5,5) after search.
	if occ, ok := grid.OccupantAt(Cell{X: 5, Z: 5}); !ok || occ != int(hMover) {
		t.Fatalf("grid should still report mover after search")
	}
	revBefore := grid.Revision()
	// Moving the requester one tick toward goal should still be possible (search succeeded) and revision should bump on stamp/clear, but search did not consume revision eagerly [04 §7.4].
	_ = revBefore
}

// TestStaticLayerDeterministicFixture ensures no map iteration / no RNG in sim order [I1][I4].
func TestStaticLayerDeterministicFixture(t *testing.T) {
	run := func() []Point {
		terrain := syntheticTerrainForIntegrate()
		profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}
		grid := NewOccupancyGrid()
		sys := NewSystem(terrain, profile, grid)
		w := units.New(100, nil)
		sys.BindWorld(w)
		def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 2 * 65536, TurnRate: 500, FootprintX: 1, FootprintZ: 1, MaxDamage: 100, BMCode: true, CanMove: true}
		moverX := world.CellToWorld(5)
		moverZ := world.CellToWorld(5)
		hMover, _ := w.Create(def, 0, moverX, terrain.HeightAt(moverX, moverZ), moverZ)
		sys.EnsureUnit(w.Unit(hMover))
		hReq, _ := w.Create(def, 0, world.CellToWorld(1), terrain.HeightAt(world.CellToWorld(1), world.CellToWorld(1)), world.CellToWorld(1))
		sys.EnsureUnit(w.Unit(hReq))
		id := orders.Lookup("Move_Ground")
		q := orders.QueueForUnit(w.Unit(hReq))
		q.Push(id, orders.Node{GoalX: world.CellToWorld(8), GoalZ: world.CellToWorld(8)})
		sys.ActivateMove(w.Unit(hReq), q.Head())
		sys.Scheduler.Tick(1)
		route := sys.Routes[hReq]
		if route == nil || !route.Active {
			return nil
		}
		out := make([]Point, route.Count)
		copy(out, route.Points[:route.Count])
		return out
	}
	a := run()
	b := run()
	if len(a) != len(b) {
		t.Fatalf("determinism: len %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("determinism: point %d %v vs %v", i, a[i], b[i])
		}
	}
}

// TestStaticLayerCommitStillBlocks verifies that even though search ignores the mover,
// commit-stage still sees it via OccupancyGrid and ApplyBlocked would cap speed [04 §8.2] C24.
func TestStaticLayerCommitStillBlocks(t *testing.T) {
	terrain := syntheticTerrainForIntegrate()
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255}
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, profile, grid)
	w := units.New(100, nil)
	sys.BindWorld(w)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 65536, TurnRate: 500, FootprintX: 1, FootprintZ: 1, MaxDamage: 100, BMCode: true, CanMove: true}
	// Mover at (2,0)
	hMover, _ := w.Create(def, 0, world.CellToWorld(2), 0, world.CellToWorld(0))
	sys.EnsureUnit(w.Unit(hMover))
	// Blocker destination cell (1,0) is occupied by mover (placed at 2,0 but we will move blocker to 1,0)
	// Actually ensure mover at (1,0)
	// Clear and move mover to (1,0) for test
	grid.Clear(Cell{X: 2, Z: 0}, 1, 1, int(hMover))
	grid.Stamp(Cell{X: 1, Z: 0}, 1, 1, int(hMover))
	moverColl := sys.Collisions[hMover]
	if moverColl != nil {
		moverColl.CachedAnchor = Cell{X: 1, Z: 0}
		moverColl.OldAnchor = Cell{X: 1, Z: 0}
	}
	hReq, _ := w.Create(def, 0, world.CellToWorld(0), 0, world.CellToWorld(0))
	sys.EnsureUnit(w.Unit(hReq))
	// Requester wants to step into (1,0) which is occupied
	reqColl := sys.Collisions[hReq]
	reqColl.VX = int32(world.CellToWorld(1).Raw() - world.CellToWorld(0).Raw())
	reqColl.VZ = 0
	reqColl.Speed = 80000
	reqColl.Heading = 16384 // east
	reqColl.MaxVelocity = 80000
	perCell := func(c Cell) bool {
		if occ, ok := grid.OccupantAt(c); ok && occ != reqColl.ID {
			return false
		}
		return true
	}
	fast, blocked := reqColl.CommitOne(grid, reqColl.Mode, perCell, nil)
	if fast {
		t.Fatalf("should not be fast path")
	}
	if !blocked {
		t.Fatalf("commit should block on mover occupancy [04 §8.2] C24")
	}
	if reqColl.Speed != 40000 {
		t.Fatalf("blocked speed should cap at MaxVelocity/2 [04 §8.2] C24 got %d want 40000", reqColl.Speed)
	}
}
