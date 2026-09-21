package movement

import (
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The fixture's cliff is cells (6, 2..3): one mapping block's worth of a slope
// no ground profile here accepts, so the class layer and the commit validator
// both refuse it [04 R-COLL-01 §2] and one lesson is enough to see all of it.
const learnedWallX, learnedWallZ, learnedWallEndZ = 6, 2, 3

// learnedFixture is a flat 20x20 map with that cliff, nothing mapped for any
// player, and one 1x1 mover of player 0 at cell (2,2) holding a Move_Ground
// order due east across the cliff.
func learnedFixture(t *testing.T, rules Rules) (*System, *units.World, pool.Handle, *orders.Node) {
	t.Helper()
	def := wiringDef()
	sys, w, h := releaseFixture(t, def, 2)
	sys.Rules = rules
	for z := int32(learnedWallZ); z <= learnedWallEndZ; z++ {
		sys.Terrain.PlotAt(learnedWallX, z).SetMaxHeight(200)
	}
	u := w.Unit(h)
	unmapped := &orders.QueueBinding{World: &orders.WorldQueryAdapter{
		MappingWord: func(int32, int32) (uint16, bool) { return 0, true },
	}}
	q := orders.QueueForUnit(u)
	q.SetBinding(unmapped)
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: h, GoalX: world.CellToWorld(12), GoalZ: u.Z, GoalSupplied: true})
	head := q.Head()
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: h, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	sys.ActivateMove(u, head)
	sys.CancelPathRequest(h)
	return sys, w, h, head
}

// learnedSearch runs one whole request for the unit; the search starts from the
// unit's committed position and publishes world-unit points.
func learnedSearch(sys *System, h pool.Handle, player uint8, tick uint32) []path.Point {
	sys.BeginTick(tick)
	defer sys.EndTick(tick)
	work := sys.searchFunc(path.Request{
		Unit: h, Player: player,
		Start: path.Cell{X: 2, Z: 2},
		Goal:  path.PointGoal(path.Cell{X: 12, Z: 2}, 0),
	}, 65536, 1<<30)
	return slices.Clone(work.Points)
}

// learnedWalkIntoWall publishes the straight route the blind search produces
// and steps the mover until the commit validator rejects a step.
func learnedWalkIntoWall(t *testing.T, sys *System, w *units.World, h pool.Handle) {
	t.Helper()
	u := w.Unit(h)
	route := handleRow(sys.Routes, h)
	route.PublishAtRevision([]Point{{X: 32, Z: 32}, {X: 200, Z: 32}}, sys.staticObstacleRevision())
	route.LastRequestTick = 100
	steer := handleRow(sys.Steers, h)
	steer.Heading, steer.PendingHeading, u.Move.Heading = 49152, 49152, 49152
	for tick := uint32(101); tick < 140; tick++ {
		if stepOnce(sys, h, tick).Blocked {
			return
		}
	}
	t.Fatal("the mover never reached the cliff")
}

func crossesLearnedWall(points []path.Point) bool {
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		const wallX = learnedWallX*16 + 8 // a 1x1 mover's point on the wall column
		if (a.X < wallX) == (b.X < wallX) {
			continue
		}
		if z := a.Z + (b.Z-a.Z)*(wallX-a.X)/(b.X-a.X); z >= learnedWallZ*16 && z < (learnedWallEndZ+1)*16 {
			return true
		}
	}
	return false
}

// Modern: a step static ground rejects teaches the owner the mapping block the
// search reads for it, the owner's next search routes around the cliff, and
// another player's search is as blind as before
// (docs/DESIGN_MOVEMENT_PATH.md "Modern learned terrain").
func TestModernTerrainRejectionTeachesOnlyTheOwner(t *testing.T) {
	sys, w, h, _ := learnedFixture(t, &ModernRules{})
	if blind := learnedSearch(sys, h, 0, 100); !crossesLearnedWall(blind) {
		t.Fatalf("fixture: the blind route must cross the unmapped cliff, got %v", blind)
	}
	// A second player's mover asks the same question from its own position.
	other, err := w.Create(wiringDef(), 1, world.CellToWorld(2), 0, world.CellToWorld(3))
	if err != nil {
		t.Fatal(err)
	}
	sys.EnsureUnit(w.Unit(other))
	orders.QueueForUnit(w.Unit(other)).SetBinding(orders.QueueForUnit(w.Unit(h)).Binding())
	theirs := learnedSearch(sys, other, 1, 100)
	learnedWalkIntoWall(t, sys, w, h)
	bx, bz := mappingTile(learnedWallX, 2, 1, 1)
	if !sys.learned.Known(bx, bz, 0) {
		t.Fatal("the rejected block was not learned by the owner")
	}
	if sys.learned.Known(bx, bz, 1) {
		t.Fatal("another player learned from this owner's rejection")
	}
	if around := learnedSearch(sys, h, 0, 200); len(around) == 0 || crossesLearnedWall(around) {
		t.Fatalf("the owner's next route still crosses the cliff: %v", around)
	}
	if after := learnedSearch(sys, other, 1, 201); !crossesLearnedWall(after) || !slices.Equal(after, theirs) {
		t.Fatalf("another player's route changed: %v, want %v", after, theirs)
	}
}

// Modern: a step another unit rejects teaches nothing, because the search
// already has a writer for that case — the occupant-age gate [04 R-COLL-01 §3].
func TestModernUnitRejectionTeachesNothing(t *testing.T) {
	sys, w, h, _ := learnedFixture(t, &ModernRules{})
	for z := int32(learnedWallZ); z <= learnedWallEndZ; z++ {
		sys.Terrain.PlotAt(learnedWallX, z).SetMaxHeight(10)
	}
	parked, err := w.Create(wiringDef(), 0, world.CellToWorld(learnedWallX), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatal(err)
	}
	sys.EnsureUnit(w.Unit(parked))
	learnedWalkIntoWall(t, sys, w, h)
	if coll := handleRow(sys.Collisions, h); coll.BlockerID != int(parked) {
		t.Fatalf("fixture: blocker %d, want the parked unit %d", coll.BlockerID, parked)
	}
	if sys.learned != nil {
		t.Fatal("a rejection by another unit taught terrain")
	}
}

// Modern: ground the owner has already mapped has nothing to teach — the search
// reads it from the stamped layer as it is — so an ordinary bump allocates no
// grid and leaves the retail read in place.
func TestModernMappedRejectionTeachesNothing(t *testing.T) {
	sys, w, h, _ := learnedFixture(t, &ModernRules{})
	orders.QueueForUnit(w.Unit(h)).Binding().World.MappingWord = func(int32, int32) (uint16, bool) { return 1, true }
	learnedWalkIntoWall(t, sys, w, h)
	if sys.learned != nil {
		t.Fatal("a rejection on mapped ground taught terrain")
	}
}

// Strict 3.1: the same rejection records nothing, the repath is the route it
// replaced, and neither stream is drawn [04 R-MOV-01 §7].
func TestStrictTerrainRejectionLearnsNothing(t *testing.T) {
	for _, rules := range []Rules{nil, StrictRules{}} {
		rng.SeedGlobal(1, 1)
		sim, crt := *rng.Global.Sim, *rng.Global.Crt
		sys, w, h, _ := learnedFixture(t, rules)
		learnedWalkIntoWall(t, sys, w, h)
		if sys.learned != nil {
			t.Fatal("Strict allocated learned terrain")
		}
		if again := learnedSearch(sys, h, 0, 200); !crossesLearnedWall(again) {
			t.Fatalf("Strict repath avoided ground it never mapped: %v", again)
		}
		if *rng.Global.Sim != sim || *rng.Global.Crt != crt {
			t.Fatal("the rejection drew randomness")
		}
	}
}

// Strict 3.1 after a Modern session: knowledge gathered before the switch is
// not consulted, so the search's inputs are retail's again.
func TestStrictIgnoresTerrainLearnedBeforeASwitch(t *testing.T) {
	sys, w, h, _ := learnedFixture(t, &ModernRules{})
	learnedWalkIntoWall(t, sys, w, h)
	if sys.learned == nil {
		t.Fatal("fixture: Modern learned nothing")
	}
	sys.Rules = StrictRules{}
	if again := learnedSearch(sys, h, 0, 200); !crossesLearnedWall(again) {
		t.Fatalf("Strict read learned terrain: %v", again)
	}
}

var learnedRulesSink bool

// The seam is asked once per rejected commit, so neither answer may allocate
// once the grid exists [docs/DESIGN_GAMEPLAY_RULES.md §3].
func TestMovementRulesDispatchDoesNotAllocate(t *testing.T) {
	sys, w, h, _ := learnedFixture(t, &ModernRules{})
	u := w.Unit(h)
	anchor := Cell{X: learnedWallX, Z: 2}
	if !sys.rules().StaticRejection(sys, u, anchor, 1, 1) {
		t.Fatal("fixture: the first lesson taught nothing")
	}
	for _, rules := range []Rules{nil, &ModernRules{}} {
		sys.Rules = rules
		if allocs := testing.AllocsPerRun(200, func() {
			learnedRulesSink = sys.rules().StaticRejection(sys, u, anchor, 1, 1)
			learnedRulesSink = sys.rules().LearnedTerrain(sys) != nil
		}); allocs != 0 {
			t.Fatalf("rules %T allocated %v per dispatch", rules, allocs)
		}
	}
}
