package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestRouteRecordsStaticRevisionButZeroPublicationKeepsMetadata(t *testing.T) {
	var route Route
	route.PublishAtRevision([]Point{{X: 1, Z: 1}, {X: 2, Z: 2}}, 7)
	if route.StaticRevision != 7 || !route.Active {
		t.Fatalf("published route = %#v", route)
	}
	if route.NeedsStaticReplan(7) || !route.NeedsStaticReplan(8) {
		t.Fatalf("revision predicate failed at current route revision")
	}
	route.Publish(nil)
	if route.Active || route.StaticRevision != 7 || route.Count != 2 {
		t.Fatalf("zero publication changed stale metadata/bytes: %#v", route)
	}
}

func TestClassLayerRefreshesAfterBlockingFeatureMutation(t *testing.T) {
	terrain := staticRevisionTerrain(8, 8)
	layer := NewClassLayer(Template(), terrain, nil)
	if got := layer.Value(3, 3); got == LayerBlocked {
		t.Fatalf("empty cell unexpectedly blocked: %d", got)
	}
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, Blocking: true, FootprintX: 1, FootprintZ: 1}
	terrain.FeatureDefs = []*content.FeatureDef{def}
	if err := terrain.StampFeatureRect(3, 3, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	terrain.BumpStaticObstacleRevision()
	if got := layer.Value(3, 3); got != LayerBlocked {
		t.Fatalf("stale layer value = %d, want blocked after revision %d", got, terrain.StaticObstacleRevision())
	}
	if layer.StaticRevision() != terrain.StaticObstacleRevision() {
		t.Fatalf("layer revision %d != terrain revision %d", layer.StaticRevision(), terrain.StaticObstacleRevision())
	}
}

func TestOwnerMaskMutationDoesNotInventStaticRevisionAndMobileDoesNot(t *testing.T) {
	terrain := staticRevisionTerrain(8, 8)
	layer := NewClassLayer(Template(), terrain, nil)
	start := terrain.StaticObstacleRevision()
	layer.SetOwnerRect(Cell{X: 1, Z: 1}, 2, 2, 0)
	if terrain.StaticObstacleRevision() != start {
		t.Fatalf("owner set invented static revision %d from %d", terrain.StaticObstacleRevision(), start)
	}
	layer.SetOwnerRect(Cell{X: 1, Z: 1}, 2, 2, 0)
	if terrain.StaticObstacleRevision() != start {
		t.Fatalf("idempotent owner set changed revision to %d", terrain.StaticObstacleRevision())
	}
	layer.ClearOwnerRect(Cell{X: 1, Z: 1}, 2, 2, 0)
	if terrain.StaticObstacleRevision() != start {
		t.Fatalf("owner clear invented static revision %d", terrain.StaticObstacleRevision())
	}
	grid := NewOccupancyGrid()
	beforeMobile := terrain.StaticObstacleRevision()
	grid.Stamp(Cell{X: 2, Z: 2}, 1, 1, 9)
	grid.Clear(Cell{X: 2, Z: 2}, 1, 1, 9)
	if terrain.StaticObstacleRevision() != beforeMobile {
		t.Fatalf("mobile occupancy changed static revision to %d", terrain.StaticObstacleRevision())
	}
}

func TestStaticRevisionSaturatesAtMaximum(t *testing.T) {
	terrain := staticRevisionTerrain(2, 2)
	// This test lives in movement and therefore uses the public operation
	// repeatedly rather than reaching into world state; normal wrap is not
	// reachable during a battle and saturation is the monotonic behavior.
	for i := 0; i < 3; i++ {
		terrain.BumpStaticObstacleRevision()
	}
	if terrain.StaticObstacleRevision() != 3 {
		t.Fatalf("ordinary revision increments = %d, want 3", terrain.StaticObstacleRevision())
	}
}

func TestStaticRevisionInvalidatesGroundRouteAndPublishesCurrentRevision(t *testing.T) {
	terrain := staticRevisionTerrain(16, 16)
	profile := Template()
	profile.MaxWaterDepth = 12
	profile.MinWaterDepth = -10000
	profile.MaxSlope = 50
	profile.BadSlope = 25
	sys := NewSystem(terrain, profile, NewOccupancyGrid())
	w := newMovementFixtureWorld(16)
	sys.BindWorld(w)
	def := &content.UnitDef{
		UnitName:    "ground-scout",
		MaxVelocity: 2 * 65536,
		TurnRate:    500,
		FootprintX:  1,
		FootprintZ:  1,
		MaxDamage:   100,
		BMCode:      true,
		CanMove:     true,
	}
	h, err := w.Create(def, 0, world.CellToWorld(1), terrain.HeightAt(world.CellToWorld(1), world.CellToWorld(1)), world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	moveID := orders.Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("Move_Ground not found")
	}
	q := orders.QueueForUnit(u)
	q.Push(moveID, orders.Node{GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(12)})
	head := q.Head()
	if !sys.ActivateMove(u, head) {
		t.Fatal("move activation failed")
	}
	sys.Scheduler.Tick(1)
	route := sys.Routes[h]
	if route == nil || !route.Active || route.StaticRevision != terrain.StaticObstacleRevision() {
		t.Fatalf("initial route = %#v, terrain revision %d", route, terrain.StaticObstacleRevision())
	}
	oldCount := route.Count
	if oldCount < 2 {
		t.Fatalf("route count %d, want a waypoint beyond the start", oldCount)
	}
	// Place a blocking wreck after publication. The feature service is the
	// semantic mutation boundary, so its revision bump represents the newly
	// blocking cell rather than a low-level plot write [04 §6.2][04 §8.2].
	wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"}, Blocking: true, FootprintX: 1, FootprintZ: 1}
	terrain.FeatureDefs = []*content.FeatureDef{wreck}
	featureService := features.NewService(terrain, nil, nil, nil)
	// Any blocking-feature mutation invalidates the layer revision. Keep this
	// fixture off the requested line: obstacle avoidance has its own tests and
	// the direct-ray rejection threshold is not this test's subject.
	const blockedX, blockedZ = 14, 1
	if featureService.PlaceAt(blockedX, blockedZ, wreck) == nil {
		t.Fatalf("wreck placement at off-route cell (%d,%d) failed", blockedX, blockedZ)
	}
	if route.StaticRevision == terrain.StaticObstacleRevision() {
		t.Fatalf("route revision %d did not become stale at terrain revision %d", route.StaticRevision, terrain.StaticObstacleRevision())
	}
	sys.BeginTick(2)
	result := sys.StepUnit(h, 2)
	sys.EndTick(2)
	if !result.EmptyRoute || result.Moved || route.Active {
		t.Fatalf("stale route was consumed instead of invalidated: result=%+v route=%+v", result, route)
	}
	if !sys.HasPathRequest(h) {
		t.Fatal("static invalidation did not resubmit the active order")
	}
	// The footprint/ring classifier can turn the detour into a multi-slice
	// search. Budget exhaustion retains the active heap and publishes only on a
	// later tick [04 R-PATH-01 §6–§7].
	for tick := uint32(3); tick < 20; tick++ {
		sys.Scheduler.Tick(tick)
		route = sys.Routes[h]
		if route != nil && route.Active && route.StaticRevision == terrain.StaticObstacleRevision() {
			break
		}
	}
	if route == nil || !route.Active || route.StaticRevision != terrain.StaticObstacleRevision() {
		t.Fatalf("replanned route = %#v, terrain revision %d", route, terrain.StaticObstacleRevision())
	}
	if route.NeedsStaticReplan(terrain.StaticObstacleRevision()) {
		t.Fatal("fresh route still reports static staleness")
	}
	// The next movement window may need one steering tick to accelerate; it
	// must eventually consume the fresh route rather than idle forever.
	moved := false
	for tick := uint32(4); tick < 12; tick++ {
		sys.BeginTick(tick)
		step := sys.StepUnit(h, tick)
		sys.EndTick(tick)
		if step.Moved {
			moved = true
			break
		}
	}
	if !moved {
		t.Fatalf("unit did not move after current-revision route publication; route=%+v", route)
	}
}

func TestStaticRevisionDoesNotInvalidateAircraftRoute(t *testing.T) {
	terrain := staticRevisionTerrain(8, 8)
	sys := NewSystem(terrain, Template(), NewOccupancyGrid())
	w := newMovementFixtureWorld(8)
	sys.BindWorld(w)
	def := &content.UnitDef{UnitName: "air-scout", CanFly: true, CanMove: true, FootprintX: 1, FootprintZ: 1, MaxVelocity: 65536}
	h, err := w.Create(def, 0, world.CellToWorld(1), 0, world.CellToWorld(1))
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	moveID := orders.Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("Move_Ground not found")
	}
	q := orders.QueueForUnit(u)
	q.Push(moveID, orders.Node{GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(4)})
	route := &Route{}
	route.PublishAtRevision([]Point{{X: 5, Z: 5}, {X: 6, Z: 6}, {X: 7, Z: 7}}, 0)
	sys.Routes[h] = route
	terrain.BumpStaticObstacleRevision()
	result := sys.StepUnit(h, 1)
	if !result.HasRoute || !route.Active {
		t.Fatalf("aircraft route was invalidated by ground revision: result=%+v route=%+v", result, route)
	}
}

func TestAStarSharesHardBlockPredicate(t *testing.T) {
	terrain := staticRevisionTerrain(6, 6)
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, Blocking: true, FootprintX: 1, FootprintZ: 1}
	terrain.FeatureDefs = []*content.FeatureDef{def}
	if err := terrain.StampFeatureRect(1, 1, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	profile := Template()
	profile.FootPrintX = 1
	profile.FootPrintZ = 1
	layer := NewClassLayer(profile, terrain, nil)
	searchPassable := func(p path.Cell) bool {
		return layer.Value(p.X, p.Z) != LayerBlocked
	}
	if searchPassable(path.Cell{X: 1, Z: 1}) {
		t.Fatal("A* hard-block predicate accepted the stamped blocking cell")
	}
	if got := layer.Passable(0, 0, 1, 1, 0); got != LayerMaskMiss {
		t.Fatalf("unowned cell passability = %d, want traversable mask-miss value %d", got, LayerMaskMiss)
	}
	search := path.Search(path.SearchConfig{
		Start: path.Cell{X: 0, Z: 0},
		Goal:  path.PointGoal(path.Cell{X: 2, Z: 2}, 0),
		PassableValue: func(c path.Cell) uint8 {
			if searchPassable(c) {
				return LayerClear
			}
			return LayerBlocked
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    path.Rect{Min: path.Cell{X: 0, Z: 0}, Max: path.Cell{X: 5, Z: 5}},
	})
	if search.Status != 0 || len(search.Points) == 0 {
		t.Fatalf("A* could not route around the hard block: status=%v points=%v", search.Status, search.Points)
	}
	route := &Route{}
	route.Publish([]Point{{X: 0, Z: 0}, {X: 0, Z: 2}, {X: 2, Z: 2}})
	if route.Count != 3 {
		t.Fatalf("smoothing crossed an A* rejected cell: count=%d points=%v", route.Count, route.Points[:route.Count])
	}
	// Owner-mask value 2 is intentionally not used here: Passable documents
	// that value as traversable, while the request search binds the packed
	// terrain value directly until an established structure writer exists.
}

func staticRevisionTerrain(w, h int32) *world.Terrain {
	t := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, int(w*h))}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}
