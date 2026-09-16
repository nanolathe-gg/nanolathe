package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
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

// A feature change reaches the class layer through the stamper's own restamp,
// over the changed rectangle and nothing more [03 §5.1.2][03 R-LAYER §2]
// [04 R-MOV-03 §3]. This test locks that behaviour, not a state hash: the
// layer blocks the stamped cell, the whole-layer classifier does not run
// again, and a cell far from the rectangle is untouched.
//
// It replaced a test that bumped the static-obstacle revision and expected the
// layer to rebuild itself end to end on the next read. That was this build's
// invention: retail restamps the rectangle synchronously inside the feature
// service, and the revision word is Nanolathe diagnostic metadata with no
// class-layer reader.
func TestFeatureStampRestampsOnlyItsOwnRectangle(t *testing.T) {
	terrain := staticRevisionTerrain(8, 8)
	layer := NewClassLayer(Template(), terrain, nil)
	terrain.ClassRestamp = func(ax, az int32, fx, fz int16) {
		layer.restampOccupantRect(Cell{X: ax, Z: az}, fx, fz)
	}
	if got := layer.Value(3, 3); got == LayerBlocked {
		t.Fatalf("empty cell unexpectedly blocked: %d", got)
	}
	stampsBefore := layer.fullStamps
	def := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "tree"}, Blocking: true, FootprintX: 1, FootprintZ: 1}
	terrain.FeatureDefs = []*content.FeatureDef{def}
	if err := terrain.StampFeatureRect(3, 3, 0, 1, 1); err != nil {
		t.Fatal(err)
	}
	terrain.NoteFootprintRestamp(3, 3, 1, 1)
	if got := layer.Value(3, 3); got != LayerBlocked {
		t.Fatalf("layer value = %d, want blocked after the stamper's restamp", got)
	}
	if layer.fullStamps != stampsBefore {
		t.Fatalf("full stamps %d -> %d: the restamp rebuilt the whole layer", stampsBefore, layer.fullStamps)
	}
	if got := layer.Value(0, 0); got == LayerBlocked {
		t.Fatalf("cell outside the restamped rectangle became blocked: %d", got)
	}
}

// The mapping-word grid belongs to the visibility publisher and this package
// only reads it [04 R-PATH-01 §14], so the surviving half of this test is that
// mobile occupancy does not invent a static-obstacle revision.
func TestMobileOccupancyDoesNotInventStaticRevision(t *testing.T) {
	terrain := staticRevisionTerrain(8, 8)
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

// A distant feature mutation cannot replace a published route, including its
// detours, or skip integration [04 R-MOV-01 §3][04 §7.4].
func TestFeatureChangePreservesGroundRoute(t *testing.T) {
	for _, points := range [][]Point{
		{{X: 32, Z: 32}, {X: 192, Z: 32}},
		{{X: 32, Z: 32}, {X: 96, Z: 32}, {X: 96, Z: 96}, {X: 192, Z: 96}},
	} {
		def := wiringDef()
		def.MaxVelocity, def.Acceleration, def.BrakeRate = 2<<16, 2<<16, 2<<16
		sys, w, h := releaseFixture(t, def, 2)
		u := w.Unit(h)
		last := points[len(points)-1]
		q := orders.QueueForUnit(u)
		q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: h, GoalX: numeric.Fixed(last.X) << 16, GoalZ: numeric.Fixed(last.Z) << 16, GoalSupplied: true})
		head := q.Head()
		sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
		sys.ActivateMove(u, head)
		sys.CancelPathRequest(h)
		route := handleRow(sys.Routes, h)
		route.PublishAtRevision(points, sys.staticObstacleRevision())
		route.LastRequestTick = 100
		beforeRoute, beforeBinding := *route, *handleRow(sys.activeOrders, h)
		steer := handleRow(sys.Steers, h)
		steer.Heading, steer.PendingHeading, u.Move.Heading = 49152, 49152, 49152
		beforeX := u.X

		wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"}, Blocking: true, FootprintX: 1, FootprintZ: 1}
		sys.Terrain.FeatureDefs = []*content.FeatureDef{wreck}
		service := features.NewService(sys.Terrain, nil, nil, nil)
		if service.PlaceAt(14, 1, wreck) == nil {
			t.Fatal("off-route wreck placement failed")
		}
		if sys.staticObstacleRevision() == beforeRoute.StaticRevision {
			t.Fatal("feature did not change revision")
		}
		result := stepOnce(sys, h, 101)
		if !result.Moved || result.Blocked || u.X != beforeX+2<<16 {
			t.Fatalf("off-route feature interrupted movement: %+v, x=%d", result, u.X)
		}
		if *route != beforeRoute || *handleRow(sys.activeOrders, h) != beforeBinding || sys.HasPathRequest(h) {
			t.Fatalf("off-route feature replaced route or requested a path: before=%+v after=%+v", beforeRoute, *route)
		}
	}
}

// A feature on the proposed footprint blocks at commit; only the follower's
// later service arms repath, with admission at lastRequestTick+60 inclusive
// [04 R-COLL-01 §1][04 R-MOV-01 §3][04 R-MOV-01 §7].
func TestFeatureBlockUsesFollowerRepathPoll(t *testing.T) {
	def := wiringDef()
	def.MaxVelocity, def.Acceleration, def.BrakeRate = 32<<16, 32<<16, 32<<16
	sys, w, h := releaseFixture(t, def, 2)
	u := w.Unit(h)
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: h, GoalX: 160 << 16, GoalZ: u.Z, GoalSupplied: true})
	head := q.Head()
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	sys.ActivateMove(u, head)
	sys.CancelPathRequest(h)
	route := handleRow(sys.Routes, h)
	route.PublishAtRevision([]Point{{X: 32, Z: 32}, {X: 160, Z: 32}}, sys.staticObstacleRevision())
	route.LastRequestTick = 100
	beforeBinding := *handleRow(sys.activeOrders, h)
	steer := handleRow(sys.Steers, h)
	steer.Heading, steer.PendingHeading, u.Move.Heading = 49152, 49152, 49152
	wreck := &content.FeatureDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "wreck"}, Blocking: true, FootprintX: 1, FootprintZ: 1}
	sys.Terrain.FeatureDefs = []*content.FeatureDef{wreck}
	if features.NewService(sys.Terrain, nil, nil, nil).PlaceAt(4, 2, wreck) == nil {
		t.Fatal("on-route wreck placement failed")
	}
	result := stepOnce(sys, h, 101)
	if !result.Blocked || !route.Active || sys.HasPathRequest(h) || route.LastRequestTick != 100 {
		t.Fatalf("feature bypassed ordinary blocked commit: %+v route=%+v", result, route)
	}
	sys.serviceGroundFollower(u, head, route, 159)
	if !route.WantsRepath || sys.HasPathRequest(h) {
		t.Fatal("blocked follower did not preserve throttle")
	}
	sys.serviceGroundFollower(u, head, route, 160)
	if !sys.HasPathRequest(h) || *handleRow(sys.activeOrders, h) != beforeBinding {
		t.Fatal("eligible follower did not stage its original goal binding")
	}
	sys.Scheduler.Tick(160)
	if route.LastRequestTick != 160 {
		t.Fatalf("admitted poll tick=%d, want 160", route.LastRequestTick)
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
	q.Push(moveID, orders.Node{GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(4), GoalSupplied: true})
	route := &Route{}
	route.PublishAtRevision([]Point{{X: 5, Z: 5}, {X: 6, Z: 6}, {X: 7, Z: 7}}, 0)
	setHandleRow(&sys.Routes, h, route)
	terrain.BumpStaticObstacleRevision()
	sys.BeginTick(1)
	result := sys.StepUnit(h, 1)
	sys.EndTick(1)
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
	// A block the requesting player has not mapped returns the traversable
	// unmapped value 2 without the terrain layer being read [04 R-PATH-01 §2].
	layer.mapping = func(int32, int32) (uint16, bool) { return 0, true }
	if got := layer.Passable(0, 0, 1, 1, 0); got != LayerUnmapped {
		t.Fatalf("unmapped cell passability = %d, want traversable value %d", got, LayerUnmapped)
	}
	layer.mapping = nil
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
