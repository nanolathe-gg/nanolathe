package movement

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Modern wedge escape (docs/DESIGN_MOVEMENT_PATH.md "Modern wedge escape").

// noWedgeRules is Modern with wedge escape switched off, so a test can show
// that the policy changes nothing a mover off rejected ground does.
type noWedgeRules struct{ ModernRules }

func (*noWedgeRules) WedgeEscape(*System) bool { return false }

var wedgeProfile = Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 30, BadWaterSlope: 15}

// wedgeFixture is a flat 20x20 map with one 2x2 mover of player 0 standing at
// anchor (6,6), holding a Move_Ground order to cell (16,6); after the mover is
// placed, the cells of [x0,x1)×[z0,z1) become void, the way a wreck stamped
// over a standing unit makes its cells fail the static test, and the layers
// are restamped over them as the feature service does.
func wedgeFixture(t *testing.T, rules Rules, x0, z0, x1, z1 int32) (*System, *units.World, pool.Handle) {
	t.Helper()
	sys := NewSystem(syntheticTerrainForIntegrate(), wedgeProfile, NewOccupancyGrid())
	sys.Rules = rules
	w := newMovementFixtureWorld(4)
	sys.BindWorld(w)
	def := setScratchMovement(&content.UnitDef{
		UnitName: "wedge-test", MaxDamage: 100, CanMove: true,
		MaxVelocity: 2 * 65536, Acceleration: 2 * 65536, BrakeRate: 2 * 65536, TurnRate: 65535,
	}, wedgeProfile)
	centre := numeric.Fixed(int64(6*16+16) << 16)
	h, err := w.Create(def, 0, centre, 0, centre)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)
	if a := handleRow(sys.Collisions, h).CachedAnchor; a != (Cell{X: 6, Z: 6}) {
		t.Fatalf("fixture: anchor %v, want (6,6)", a)
	}
	for z := z0; z < z1; z++ {
		for x := x0; x < x1; x++ {
			sys.Terrain.PlotAt(x, z).SetFeature(world.PlotFeatureVoid)
		}
	}
	sys.NoteFeatureFootprint(x0, z0, int16(x1-x0), int16(z1-z0))
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: h, GoalX: world.CellToWorld(16), GoalZ: centre, GoalSupplied: true})
	head := q.Head()
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	sys.ActivateMove(u, head)
	sys.CancelPathRequest(h)
	return sys, w, h
}

// wedgeSearch runs one whole request for the mover; the search starts from its
// committed anchor.
func wedgeSearch(sys *System, h pool.Handle, tick uint32) []path.Point {
	sys.BeginTick(tick)
	defer sys.EndTick(tick)
	work := sys.searchFunc(path.Request{Unit: h, Player: 0, Start: path.Cell{X: 6, Z: 6}, Goal: path.PointGoal(path.Cell{X: 16, Z: 6}, 0)}, 65536, 1<<30)
	return slices.Clone(work.Points)
}

// coveredRejected counts the committed footprint's cells the static test
// rejects.
func coveredRejected(sys *System, h pool.Handle) int {
	coll := handleRow(sys.Collisions, h)
	n := 0
	for z := coll.CachedAnchor.Z; z < coll.CachedAnchor.Z+2; z++ {
		for x := coll.CachedAnchor.X; x < coll.CachedAnchor.X+2; x++ {
			if !wedgeProfile.IsPassableCommitCell(sys.Terrain, x, z) {
				n++
			}
		}
	}
	return n
}

// follow publishes points as the mover's route and steps it; it reports the
// first tick its committed footprint is clear of rejected cells (0 if never)
// and fails if the count of rejected cells it covers ever grows.
func follow(t *testing.T, sys *System, h pool.Handle, points []Point, from, to uint32) uint32 {
	t.Helper()
	route := handleRow(sys.Routes, h)
	route.PublishAtRevision(points, sys.staticObstacleRevision())
	route.LastRequestTick = from
	covered := coveredRejected(sys, h)
	for tick := from; tick < to; tick++ {
		stepOnce(sys, h, tick)
		now := coveredRejected(sys, h)
		if now > covered {
			t.Fatalf("tick %d: the mover walked further into rejected ground (%d -> %d cells)", tick, covered, now)
		}
		if covered = now; covered == 0 {
			return tick
		}
	}
	return 0
}

func toRoute(points []path.Point) []Point {
	out := make([]Point, len(points))
	for i, p := range points {
		out[i] = Point(p)
	}
	return out
}

// A wreck stamped over the mover's west half and two cells beyond it toward
// the goal. Strict and Community reject the search at setup and every step
// along the straight line, drawing nothing. Under Modern the straight line
// is still refused — the cells beyond enter the footprint — but the search
// leads off the wreck and the commit lets the mover take that route, without
// its ever covering more rejected cells.
func TestWedgeEscapeLeavesAWreckStampedOverTheMover(t *testing.T) {
	line := []Point{{X: 112, Z: 112}, {X: 16*16 + 16, Z: 112}}
	for _, rules := range []Rules{nil, StrictRules{}, CommunityRules{}} {
		rng.SeedGlobal(1, 1)
		sim, crt := *rng.Global.Sim, *rng.Global.Crt
		sys, _, h := wedgeFixture(t, rules, 6, 6, 10, 8)
		if got := wedgeSearch(sys, h, 100); len(got) != 0 {
			t.Fatalf("%T: a search from the wedged start published %v", rules, got)
		}
		if freed := follow(t, sys, h, line, 101, 160); freed != 0 {
			t.Fatalf("%T: the mover left rejected ground on tick %d", rules, freed)
		}
		if a := handleRow(sys.Collisions, h).CachedAnchor; a != (Cell{X: 6, Z: 6}) {
			t.Fatalf("%T: the wedged mover moved to %v", rules, a)
		}
		if *rng.Global.Sim != sim || *rng.Global.Crt != crt {
			t.Fatalf("%T: the wedge drew randomness", rules)
		}
	}
	rng.SeedGlobal(1, 1)
	sim, crt := *rng.Global.Sim, *rng.Global.Crt
	sys, _, h := wedgeFixture(t, &ModernRules{}, 6, 6, 10, 8)
	if freed := follow(t, sys, h, line, 101, 160); freed != 0 {
		t.Fatalf("the straight line through the wreck freed the mover on tick %d; the fixture needs the search", freed)
	}
	route := wedgeSearch(sys, h, 200)
	if len(route) < 2 {
		t.Fatalf("Modern's search published nothing from the wedged start: %v", route)
	}
	if freed := follow(t, sys, h, toRoute(route), 201, 320); freed == 0 {
		t.Fatalf("the mover never left the wreck along %v", route)
	}
	if *rng.Global.Sim != sim || *rng.Global.Crt != crt {
		t.Fatal("wedge escape drew randomness")
	}
}

// A mover the wreck encloses — every step adds a rejected cell — gets no route
// and no movement.
func TestWedgeEscapeFabricatesNothingWhenEnclosed(t *testing.T) {
	sys, _, h := wedgeFixture(t, &ModernRules{}, 5, 5, 9, 9)
	if got := wedgeSearch(sys, h, 100); len(got) != 0 {
		t.Fatalf("an enclosed mover got a route: %v", got)
	}
	line := []Point{{X: 112, Z: 112}, {X: 16*16 + 16, Z: 112}}
	if freed := follow(t, sys, h, line, 101, 160); freed != 0 {
		t.Fatalf("an enclosed mover left on tick %d", freed)
	}
	if a := handleRow(sys.Collisions, h).CachedAnchor; a != (Cell{X: 6, Z: 6}) {
		t.Fatalf("an enclosed mover moved to %v", a)
	}
}

// A mover that covers no rejected cell searches exactly as it would without
// the policy, beside a wreck or not.
func TestWedgeEscapeLeavesOtherSearchesAlone(t *testing.T) {
	for _, wall := range [][4]int32{{9, 4, 11, 12}, {8, 6, 10, 8}} {
		off, _, ho := wedgeFixture(t, &noWedgeRules{}, wall[0], wall[1], wall[2], wall[3])
		on, _, hm := wedgeFixture(t, &ModernRules{}, wall[0], wall[1], wall[2], wall[3])
		want, got := wedgeSearch(off, ho, 100), wedgeSearch(on, hm, 100)
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Fatalf("wall %v: route %v, want the unchanged %v", wall, got, want)
		}
	}
}

// The re-read passes the requester's own cells and nothing else: every other
// cell of an overlapping anchor keeps the view's own per-cell answer — a
// stale parked mobile walls the layer view but not the static view unless the
// view keeps it, and a building walls both.
func TestWedgeExitValueKeepsTheViewsOtherCells(t *testing.T) {
	tr := layerTerrain(16, 16, 20)
	for z := int32(6); z <= 7; z++ {
		for x := int32(6); x <= 7; x++ {
			tr.PlotAt(x, z).SetFeature(world.PlotFeatureVoid)
		}
	}
	grid := NewOccupancyGrid()
	l := NewClassLayer(kbotsSS2, tr, grid)
	const requester, parked, building = pool.Handle(5), pool.Handle(7), pool.Handle(8)
	l.movers = stubMovers{requester: true, parked: true}
	grid.Stamp(Cell{X: 6, Z: 6}, 2, 2, int(requester))
	grid.Stamp(Cell{X: 7, Z: 5}, 1, 1, int(parked))
	grid.Stamp(Cell{X: 5, Z: 7}, 1, 1, int(building))
	l.NoteCommit(requester, 0)
	l.NoteCommit(parked, 0)
	l.watermark = 20
	l.RestampRect(0, 0, 15, 15)
	start := Cell{X: 6, Z: 6}
	keepParked := func(id int) bool { return id == int(parked) }
	for _, c := range []struct {
		name                 string
		x, z                 int32
		layer, static, keept uint8
	}{
		{"own footprint", 6, 6, LayerSteep, LayerSteep, LayerSteep},
		{"east, clear beyond", 7, 6, LayerSteep, LayerSteep, LayerSteep},
		{"north, a stale parked mobile", 6, 5, LayerBlocked, LayerSteep, LayerBlocked},
		{"west, a building", 5, 6, LayerBlocked, LayerBlocked, LayerBlocked},
		{"off the map", -1, 6, LayerBlocked, LayerBlocked, LayerBlocked},
	} {
		if l.Value(c.x, c.z) != LayerBlocked {
			t.Fatalf("%s: fixture: the layer admits anchor (%d,%d)", c.name, c.x, c.z)
		}
		if got := l.wedgeExitValue(c.x, c.z, 2, 2, start, false, nil); got != c.layer {
			t.Fatalf("%s: layer view %d, want %d", c.name, got, c.layer)
		}
		if got := l.wedgeExitValue(c.x, c.z, 2, 2, start, true, nil); got != c.static {
			t.Fatalf("%s: static view %d, want %d", c.name, got, c.static)
		}
		if got := l.wedgeExitValue(c.x, c.z, 2, 2, start, true, keepParked); got != c.keept {
			t.Fatalf("%s: static view keeping the parked mobile %d, want %d", c.name, got, c.keept)
		}
	}
}

// A mover wedged under a route planned elsewhere has its re-request throttle
// waived at the first refused step, so the search that leads it off comes on
// the next scheduler call; a route planned from where it stands is left
// alone, and Strict and Community never touch the throttle.
func TestWedgeEscapeReplansAWedgedRoutePromptly(t *testing.T) {
	refused := func(rules Rules, first Point) *Route {
		sys, w, h := wedgeFixture(t, rules, 6, 6, 10, 8)
		route := handleRow(sys.Routes, h)
		route.PublishAtRevision([]Point{first, {X: 16*16 + 16, Z: 112}}, sys.staticObstacleRevision())
		route.LastRequestTick = 195
		steer := handleRow(sys.Steers, h)
		steer.Heading, steer.PendingHeading = 0xC000, 0xC000 // east, into the wreck
		w.Unit(h).Move.Heading = 0xC000
		for tick := uint32(200); tick < 240; tick++ {
			if stepOnce(sys, h, tick).Blocked {
				return route
			}
		}
		t.Fatal("fixture: the wedged mover was never refused")
		return nil
	}
	elsewhere := Point{X: 96, Z: 112} // the first point a search from anchor (5,6) publishes
	here := Point{X: 112, Z: 112}     // and from anchor (6,6), where the mover stands
	if r := refused(&ModernRules{}, elsewhere); r.LastRequestTick != 0 || !r.WantsRepath {
		t.Fatalf("a route planned elsewhere kept its throttle: request tick %d, wants %v", r.LastRequestTick, r.WantsRepath)
	}
	if r := refused(&ModernRules{}, here); r.LastRequestTick != 195 {
		t.Fatalf("a route planned from the wedged anchor was re-planned: request tick %d", r.LastRequestTick)
	}
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		if r := refused(rules, elsewhere); r.LastRequestTick != 195 {
			t.Fatalf("%T waived the throttle: request tick %d", rules, r.LastRequestTick)
		}
	}
}

var wedgeAnswerSink bool

// Strict, Community and an unbound System answer off, Modern answers on, and
// the dispatch allocates nothing.
func TestWedgeEscapeAnswers(t *testing.T) {
	for _, rules := range []Rules{nil, StrictRules{}, CommunityRules{}} {
		sys := &System{Rules: rules}
		if sys.rules().WedgeEscape(sys) {
			t.Fatalf("%T answers on", rules)
		}
	}
	sys := &System{Rules: &ModernRules{}}
	if !sys.rules().WedgeEscape(sys) {
		t.Fatal("Modern answers off")
	}
	if n := testing.AllocsPerRun(100, func() { wedgeAnswerSink = sys.rules().WedgeEscape(sys) }); n != 0 {
		t.Fatalf("dispatch allocates %.0f", n)
	}
}
