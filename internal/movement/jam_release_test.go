package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// jamCase steps a mover heading east against a unit holding the cell ahead
// for up to ticks ticks and reports the first tick the mover's commit was not
// rejected, or 0 (DESIGN_MOVEMENT_PATH "Modern jam release").
type jamCase struct {
	rules        Rules
	ownerB       uint8
	headingB     uint16 // the blocker's committed heading; 0xC000 is east
	blockerRoute bool
	moverEndCell int32
	blockerFoot  int32 // the blocker's square footprint in cells; 0 is 1
	oneWayAlly   bool  // the mover's owner declares the blocker's owner allied, not back
	moverCell    int32 // the mover's starting cell along the row; 0 is 5
	routeless    bool  // the mover never holds a route
	sameHeading  bool  // the blocker always heads exactly as the mover does
}

func runJamCase(t *testing.T, c jamCase, ticks uint32) (freed uint32, sys *System) {
	t.Helper()
	sys, a, b, step := setupJamCase(t, c)
	for tick := uint32(1); tick <= ticks; tick++ {
		if !step(tick).Blocked {
			return tick, sys
		}
		if got := handleRow(sys.Collisions, a).BlockerID; got != int(b) {
			t.Fatalf("tick %d: rejected by %d, not by the blocker %d", tick, got, b)
		}
	}
	return 0, sys
}

// setupJamCase builds the fixture and returns a stepper that re-publishes both
// routes each tick (the fixture has no scheduler) and runs one mover visit.
func setupJamCase(t *testing.T, c jamCase) (*System, pool.Handle, pool.Handle, func(tick uint32) StepResult) {
	t.Helper()
	sys := NewSystem(syntheticTerrainForIntegrate(), Profile{FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255}, NewOccupancyGrid())
	sys.Rules = c.rules
	w := newMovementFixtureWorld(4)
	def := setScratchMovement(&content.UnitDef{
		UnitName: "jam-release-test", FootprintX: 1, FootprintZ: 1, BMCode: 1,
		MaxVelocity: 2 * int32(worldUnitsPerCell), Acceleration: 2 * int32(worldUnitsPerCell),
		BrakeRate: 2 * int32(worldUnitsPerCell), TurnRate: 65535,
	}, Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255})
	blockerDef := def
	bx := world.CellToWorld(7)
	if f := int16(c.blockerFoot); f > 1 {
		// A parked f-by-f friend whose footprint starts at cell 7 of the row.
		blockerDef = setScratchMovement(&content.UnitDef{
			UnitName: "jam-release-wide", FootprintX: int32(f), FootprintZ: int32(f), BMCode: 1,
			MaxVelocity: int32(worldUnitsPerCell), Acceleration: int32(worldUnitsPerCell),
			BrakeRate: int32(worldUnitsPerCell), TurnRate: 65535,
		}, Profile{FootPrintX: f, FootPrintZ: f, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 255, MaxWaterSlope: 255})
		bx = numeric.Fixed(int64(7)<<20 + int64(f)<<19)
	}
	row := world.CellToWorld(10)
	b, err := w.Create(blockerDef, c.ownerB, bx, 0, row)
	if err != nil {
		t.Fatal(err)
	}
	moverCell := c.moverCell
	if moverCell == 0 {
		moverCell = 5
	}
	a, err := w.Create(def, 0, world.CellToWorld(moverCell), 0, row)
	if err != nil {
		t.Fatal(err)
	}
	sys.BindWorld(w)
	// The blocker registers first, so a mover placed inside it overlaps an
	// incumbent that keeps its cells, as a wedge does.
	sys.EnsureUnit(w.Unit(b))
	sys.EnsureUnit(w.Unit(a))
	rowZ := int32(row.Raw() >> 16)
	move := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(w.Unit(a))
	q.Push(move, orders.Node{Owner: a, GoalX: world.CellToWorld(c.moverEndCell), GoalZ: row, GoalSupplied: true})
	if c.oneWayAlly {
		q.SetBinding(&orders.QueueBinding{Lookup: w.Unit, World: &orders.WorldQueryAdapter{DeclaresAlliance: func(from, toward uint8) bool { return from == 0 && toward == c.ownerB }}})
	}
	head := q.Head()
	sys.InstallPointGoal(orders.PointGoalRequest{Owner: head.Owner, Node: head, X: head.GoalX, Z: head.GoalZ, Radius: 4})
	setHandleRow(&sys.activeOrders, a, &activeMove{order: head, token: 7})
	step := func(tick uint32) StepResult {
		// Both routes are re-published each tick so the fixture has no
		// scheduler and the blocker never moves: only the commit is tested.
		handleRow(sys.Collisions, b).Heading = c.headingB
		if c.sameHeading {
			handleRow(sys.Collisions, b).Heading = handleRow(sys.Collisions, a).Heading
		}
		if c.blockerRoute {
			handleRow(sys.Routes, b).PublishAtRevision([]Point{{X: 7*16 + 8, Z: rowZ}, {X: 30*16 + 8, Z: rowZ}}, sys.staticObstacleRevision())
		}
		if !c.routeless {
			ax := handleRow(sys.Collisions, a).X >> 16
			handleRow(sys.Routes, a).PublishAtRevision([]Point{{X: ax, Z: rowZ}, {X: c.moverEndCell*16 + 8, Z: rowZ}}, sys.staticObstacleRevision())
		}
		sys.BeginTick(tick)
		res := sys.StepUnit(a, tick)
		sys.EndTick(tick)
		return res
	}
	return sys, a, b, step
}

func TestJamRelease(t *testing.T) {
	const ticks = 60
	for _, tc := range []struct {
		name  string
		c     jamCase
		freed uint32
	}{
		{"strict stays blocked", jamCase{rules: StrictRules{}, moverEndCell: 30}, 0},
		{"community stays blocked", jamCase{rules: CommunityRules{}, moverEndCell: 30}, 0},
		// Released after thirty jammed ticks, the mover commits on the next.
		{"modern releases a unit parked friends hold", jamCase{rules: &ModernRules{}, moverEndCell: 30}, modernJamReleaseAfter + 1},
		{"modern keeps a same-way queue", jamCase{rules: &ModernRules{}, headingB: 0xC000, blockerRoute: true, moverEndCell: 30}, 0},
		{"modern never releases into an enemy", jamCase{rules: &ModernRules{}, ownerB: 1, moverEndCell: 30}, 0},
		// Near the destination the release only takes the unit through the
		// friend that blocks it (see TestJamReleaseNearDestinationEndsWhenClear).
		{"modern releases near the route end to pass the blocker", jamCase{rules: &ModernRules{}, moverEndCell: 12}, modernJamReleaseAfter + 1},
		{"modern never releases into a one-way ally", jamCase{rules: &ModernRules{}, ownerB: 1, oneWayAlly: true, moverEndCell: 30}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			freed, sys := runJamCase(t, tc.c, ticks)
			if freed != tc.freed {
				t.Fatalf("freed on tick %d, want %d", freed, tc.freed)
			}
			if _, strict := tc.c.rules.(StrictRules); strict && sys.jamReleases != nil {
				t.Fatal("a Strict run allocated jam-release state")
			}
		})
	}
}

func TestJamReleaseAnswers(t *testing.T) {
	for _, r := range []Rules{StrictRules{}, CommunityRules{}} {
		if after, life := r.JamRelease(nil); after != 0 || life != 0 {
			t.Fatalf("%T releases jams: (%d, %d)", r, after, life)
		}
	}
	if after, life := (&ModernRules{}).JamRelease(nil); after != 30 || life != 90 {
		t.Fatalf("Modern answers (%d, %d), want (30, 90)", after, life)
	}
}

// A release that reaches the destination guard while the unit still stands
// inside a wide parked friend is held open until the unit is clear, so it is
// never left inside a friend that would block every later step (review probe:
// 1x1 mover, parked 3x3 friend on cells 7-9, goal at cell 15).
func TestJamReleaseNeverEndsInsideAFriend(t *testing.T) {
	sys, a, _, step := setupJamCase(t, jamCase{rules: &ModernRules{}, moverEndCell: 15, blockerFoot: 3})
	u := sys.world.Unit(a)
	for tick := uint32(1); tick <= 400; tick++ {
		step(tick)
		coll := handleRow(sys.Collisions, a)
		if !sys.releasing(a, tick+1) && sys.insideFriend(u, coll) && tick > modernJamReleaseAfter+1 {
			t.Fatalf("tick %d: release over with the mover still inside the friend at %+v (state %+v)", tick, coll.CachedAnchor, handleRow(sys.jamReleases, a))
		}
	}
	if x := handleRow(sys.Collisions, a).CachedAnchor.X; x < 10 {
		t.Fatalf("mover never cleared the friend: anchor x %d", x)
	}
}

// Forgetting a unit drops its release, and a finished order forgets the run.
func TestJamReleaseStateIsCleared(t *testing.T) {
	sys, a, _, step := setupJamCase(t, jamCase{rules: &ModernRules{}, moverEndCell: 30})
	for tick := uint32(1); tick <= modernJamReleaseAfter-1; tick++ {
		step(tick)
	}
	if handleRow(sys.jamReleases, a).run == 0 {
		t.Fatal("fixture did not count jammed ticks")
	}
	sys.DeactivateMove(a)
	if handleRow(sys.jamReleases, a).run != 0 {
		t.Fatal("a deactivated move kept its jammed run")
	}
	setHandleRow(&sys.jamReleases, a, jamRelease{until: 500, limit: 600})
	sys.ForgetUnit(a)
	if handleRow(sys.jamReleases, a) != (jamRelease{}) {
		t.Fatal("a forgotten unit kept its release")
	}
}

// Near its destination a released unit passes the friend that blocked it and
// the release ends at the first commit that leaves it clear of every friend.
func TestJamReleaseNearDestinationEndsWhenClear(t *testing.T) {
	sys, a, _, step := setupJamCase(t, jamCase{rules: &ModernRules{}, moverEndCell: 12})
	u := sys.world.Unit(a)
	passed := false
	for tick := uint32(1); tick <= 90; tick++ {
		step(tick)
		coll := handleRow(sys.Collisions, a)
		if coll.CachedAnchor.X > 7 && !sys.insideFriend(u, coll) {
			passed = true
			if sys.releasing(a, tick+1) {
				t.Fatalf("tick %d: release still open after the mover cleared the friend near its destination", tick)
			}
			break
		}
	}
	if !passed {
		t.Fatal("mover never passed the friend blocking it near its destination")
	}
}

// A unit wedged inside a same-way friend is no queue member: the pair would
// wait on itself for ever, so the jam counts and the release passes the
// friend it overlaps. A route-less unit standing inside a friend proposes no
// step and never reads blocked, yet is wedged all the same and is released
// (DESIGN_MOVEMENT_PATH "Modern jam release").
func TestJamReleaseFreesWedgedUnits(t *testing.T) {
	queued := jamCase{rules: &ModernRules{}, sameHeading: true, blockerRoute: true, moverEndCell: 30, blockerFoot: 5}
	if freed, _ := runJamCase(t, queued, 60); freed != 0 {
		t.Fatalf("a unit behind a same-way friend it does not overlap left the queue on tick %d", freed)
	}
	wedged := queued
	wedged.moverCell = 8 // inside the friend's cells 7-11, too deep to leave in one step
	sys, a, _, step := setupJamCase(t, wedged)
	if !sys.insideFriend(sys.world.Unit(a), handleRow(sys.Collisions, a)) {
		t.Fatal("fixture: the mover does not start inside the friend")
	}
	cleared := uint32(0)
	for tick := uint32(1); tick <= 150 && cleared == 0; tick++ {
		step(tick)
		if handleRow(sys.Collisions, a).CachedAnchor.X >= 12 {
			cleared = tick
		}
	}
	if cleared == 0 {
		t.Fatal("a unit wedged inside a same-way friend never got past it")
	}
	stuck := jamCase{rules: &ModernRules{}, moverEndCell: 30, blockerFoot: 3, moverCell: 8, routeless: true}
	sys, a, _, step = setupJamCase(t, stuck)
	released := false
	for tick := uint32(1); tick <= 45 && !released; tick++ {
		step(tick)
		released = sys.releasing(a, tick+1)
	}
	if !released {
		t.Fatal("a route-less unit standing inside a friend was never released")
	}
	strict := stuck
	strict.rules = StrictRules{}
	sys, a, _, step = setupJamCase(t, strict)
	for tick := uint32(1); tick <= 45; tick++ {
		step(tick)
	}
	if sys.releasing(a, 46) || sys.jamReleases != nil {
		t.Fatal("Strict released a wedged unit")
	}
}
