package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// pocketFixture is a flat map of 1x1 units: parked friends, void cells, and
// a mover with a sole Move_Ground to goal under Modern order rules
// (docs/DESIGN_MOVEMENT_PATH.md "Modern pocket release").
type pocketFixture struct {
	sys     *System
	u       *units.Unit
	n       *orders.Node
	friends []pool.Handle
}

func newPocketFixture(t *testing.T, rules Rules, size int32, friends, voids []Cell, mover, goal Cell) *pocketFixture {
	t.Helper()
	ter := &world.Terrain{CellW: size, CellH: size, Plot: make([]world.PlotCell, size*size)}
	for i := range ter.Plot {
		ter.Plot[i].SetFeature(world.PlotFeatureNone)
		ter.Plot[i].SetHeight(10)
		ter.Plot[i].SetMinHeight(10)
		ter.Plot[i].SetMaxHeight(10)
	}
	for _, c := range voids {
		ter.PlotAt(c.X, c.Z).SetFeature(world.PlotFeatureVoid)
	}
	sys := NewSystem(ter, wiringProfile, NewOccupancyGrid())
	sys.Rules = rules
	w := newMovementFixtureWorld(200)
	sys.BindWorld(w)
	f := &pocketFixture{sys: sys}
	for _, c := range friends {
		f.friends = append(f.friends, f.create(t, 0, c))
	}
	f.u = w.Unit(f.create(t, 0, mover))
	q := orders.QueueForUnit(f.u)
	q.SetBinding(&orders.QueueBinding{Rules: &orders.ModernRules{}, SimRNG: &rng.Simulation{}, Movement: &orders.MovementGoalAdapter{InstallPoint: sys.InstallPointGoal, Release: sys.ReleaseGoalPayload, CrowdedMoveBlocked: sys.CrowdedMoveBlocked}})
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: f.u.Handle, GoalX: world.CellToWorld(goal.X), GoalZ: world.CellToWorld(goal.Z), GoalSupplied: true})
	f.n = q.Head()
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: f.u.Handle, Node: f.n, X: f.n.GoalX, Z: f.n.GoalZ, Radius: 4}) || !sys.ActivateMove(f.u, f.n) {
		t.Fatal("install the move")
	}
	return f
}

func (f *pocketFixture) create(t *testing.T, owner uint8, c Cell) pool.Handle {
	t.Helper()
	w := f.sys.world
	h, err := w.Create(wiringDef(), owner, world.CellToWorld(c.X), 0, world.CellToWorld(c.Z))
	if err != nil {
		t.Fatal(err)
	}
	f.sys.EnsureUnit(w.Unit(h))
	return h
}

// ringAround is the eight 1x1 anchors around c.
func ringAround(c Cell) []Cell {
	var r []Cell
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			if dx != 0 || dz != 0 {
				r = append(r, Cell{X: c.X + dx, Z: c.Z + dz})
			}
		}
	}
	return r
}

// sealedPocket is the common case: a free goal at (12,10) inside a ring of
// eight parked friends, the mover against the ring's west side.
func sealedPocket(t *testing.T, rules Rules) *pocketFixture {
	return newPocketFixture(t, rules, 20, ringAround(Cell{X: 12, Z: 10}), nil, Cell{X: 10, Z: 10}, Cell{X: 12, Z: 10})
}

// reject publishes nothing for the mover's live request on tick: the
// cannot-get-there publication a certificate is made at [04 R-PATH-01 §7].
func (f *pocketFixture) reject(t *testing.T, tick uint32) {
	t.Helper()
	reqs := f.sys.PathRequestsSnapshot()
	if len(reqs) == 0 {
		t.Fatal("no live request to reject")
	}
	f.sys.tick = tick
	f.sys.publishFunc(reqs[0], nil, path.StatusRejected)
}

// visit runs the follower's closing check on tick and reports whether it
// finished the move.
func (f *pocketFixture) visit(tick uint32) bool {
	f.sys.BeginTick(tick)
	defer f.sys.EndTick(tick)
	return f.sys.pocketArrival(f.u, f.n, tick)
}

// pocketNoJamRules answers the pocket release on and jam release off: the
// extension has nothing to extend, so it is off.
type pocketNoJamRules struct{ ModernRules }

func (*pocketNoJamRules) JamRelease(*System) (uint16, uint32) { return 0, 0 }

func TestPocketReleaseAnswers(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		if near, dwell := rules.PocketRelease(nil); near != 0 || dwell != 0 {
			t.Fatalf("%T PocketRelease = (%d, %d), want retail (0, 0)", rules, near, dwell)
		}
	}
	if near, dwell := (&ModernRules{}).PocketRelease(nil); near != modernPocketNear || dwell != modernPocketDwell {
		t.Fatalf("Modern PocketRelease = (%d, %d)", near, dwell)
	}
	for _, s := range []*System{{}, {Rules: &pocketNoJamRules{}}} {
		if _, _, on := s.pocketPolicy(); on {
			t.Fatalf("%T: the pocket release is on without jam release", s.Rules)
		}
	}
	s := &System{Rules: &ModernRules{}}
	if _, _, on := s.pocketPolicy(); !on {
		t.Fatal("Modern does not release pockets")
	}
	if n := testing.AllocsPerRun(100, func() { _, _, _ = s.pocketPolicy() }); n != 0 {
		t.Fatalf("PocketRelease dispatch allocated %g times", n)
	}
}

// Strict 3.1 and Community 3.9 keep retail's retry: an empty publication
// certifies nothing, the follower never grants or finishes, and no pocket
// state is allocated.
func TestPocketReleaseLeavesStrictAndCommunityUntouched(t *testing.T) {
	for _, rules := range []Rules{nil, StrictRules{}, CommunityRules{}} {
		f := sealedPocket(t, rules)
		f.reject(t, 5)
		for tick := uint32(6); tick <= 400; tick++ {
			if f.visit(tick) {
				t.Fatalf("%T finished the move on tick %d", rules, tick)
			}
		}
		if f.sys.pockets != nil || f.sys.pocketLive != 0 || f.sys.pocketCells != nil || f.sys.pocketSeen != nil || f.sys.pocketStack != nil {
			t.Fatalf("%T allocated pocket-release state", rules)
		}
		if handleRow(f.sys.jamReleases, f.u.Handle).pocket {
			t.Fatalf("%T granted a pocket release", rules)
		}
	}
}

// Only a sealed free pocket is certified, and only at an empty publication:
// the follower alone certifies nothing, and an open side, a moving or routed
// friend, an enemy, a statically blocked goal, a goal a parked friend holds,
// a mover not against the ring or one beyond the near bound get nothing.
func TestPocketReleaseCertifiesOnlyASealedPocket(t *testing.T) {
	f := sealedPocket(t, &ModernRules{})
	for tick := uint32(1); tick <= 60; tick++ {
		f.visit(tick)
	}
	if f.sys.pocketLive != 0 {
		t.Fatal("the follower certified without a failed search")
	}
	f.reject(t, 60)
	if c := handleRow(f.sys.pockets, f.u.Handle); c.order != f.n || c.since != 60 {
		t.Fatalf("a sealed pocket was not certified: %+v", c)
	}

	for _, kind := range []string{"open side", "moving friend", "routed friend", "enemy", "blocked goal", "held goal", "not touching", "too far"} {
		t.Run(kind, func(t *testing.T) {
			goal, mover := Cell{X: 12, Z: 10}, Cell{X: 10, Z: 10}
			friends := ringAround(goal)
			switch kind {
			case "not touching":
				mover = Cell{X: 8, Z: 10}
			case "too far":
				mover = Cell{X: 12 + modernPocketNear + 2, Z: 10}
				friends = append(friends, Cell{X: mover.X + 1, Z: 10})
			}
			f := newPocketFixture(t, &ModernRules{}, 40, friends, nil, mover, goal)
			east := f.sys.world.Unit(f.friends[4]) // (13,10)
			switch kind {
			case "open side":
				c := handleRow(f.sys.Collisions, east.Handle)
				f.sys.Grid.ClearPlane(PlaneGround, c.StampedAnchor, c.FootPrintX, c.FootPrintZ, c.ID)
			case "moving friend":
				east.Move.Speed = 1
			case "routed friend":
				r := handleRow(f.sys.Routes, east.Handle)
				r.Active, r.Count = true, 2
			case "enemy":
				east.Owner = 1
			case "blocked goal":
				f.sys.Terrain.PlotAt(goal.X, goal.Z).SetFeature(world.PlotFeatureVoid)
			case "held goal":
				f.create(t, 0, goal)
			}
			f.reject(t, 5)
			if f.sys.pocketLive != 0 {
				t.Fatal("certified")
			}
		})
	}
}

// The flood is bounded by its window: a pocket that runs on past the window
// is open ground, while the map's own edge is a wall.
func TestPocketReleaseWindowIsOpenAndTheMapEdgeIsAWall(t *testing.T) {
	// A one-cell corridor running south from the goal between walls of parked
	// friends, capped at its north end by the friend the mover stands
	// against, and at its south end when it is closed. The walls flank each
	// cap: a diagonal step tests only its destination [04 §7.1], so a lone
	// cap would leak.
	corridor := func(end int32, capped bool) *pocketFixture {
		friends := []Cell{{X: 10, Z: 9}}
		for z := int32(9); z <= end+1; z++ {
			friends = append(friends, Cell{X: 9, Z: z}, Cell{X: 11, Z: z})
		}
		if capped {
			friends = append(friends, Cell{X: 10, Z: end + 1})
		}
		return newPocketFixture(t, &ModernRules{}, 64, friends, nil, Cell{X: 10, Z: 8}, Cell{X: 10, Z: 10})
	}
	closed := corridor(10+pocketWindowHalf-2, true)
	closed.reject(t, 5)
	if closed.sys.pocketLive != 1 {
		t.Fatal("a corridor closed inside the window was not certified")
	}
	past := corridor(10+pocketWindowHalf+2, false)
	past.reject(t, 5)
	if past.sys.pocketLive != 0 {
		t.Fatal("a corridor running on past the window was certified")
	}

	// A goal on the map's west edge, walled by friends on its other sides.
	goal := Cell{X: 0, Z: 10}
	edge := newPocketFixture(t, &ModernRules{}, 20, []Cell{{X: 0, Z: 9}, {X: 0, Z: 11}, {X: 1, Z: 9}, {X: 1, Z: 10}, {X: 1, Z: 11}}, nil, Cell{X: 2, Z: 10}, goal)
	edge.reject(t, 5)
	if edge.sys.pocketLive != 1 {
		t.Fatal("the map edge did not wall the pocket")
	}
}

// Jam release closes a release near the destination at the first commit
// clear of every friend, which a unit standing against the ring already is.
// A pocket release closes that way only on the unit's own goal anchor; an
// ordinary release near the destination still ends at once.
func TestPocketReleaseEndsOnlyOnItsGoal(t *testing.T) {
	f := sealedPocket(t, &ModernRules{})
	f.reject(t, 5)
	at := uint32(5 + modernPocketDwell)
	if f.visit(at) || !handleRow(f.sys.jamReleases, f.u.Handle).pocket {
		t.Fatal("no pocket release granted after the dwell")
	}
	coll := handleRow(f.sys.Collisions, f.u.Handle)
	f.sys.noteJamRelease(f.u, coll, false, -1, at, modernJamReleaseAfter, modernJamReleaseLifetime)
	if !f.sys.releasing(f.u.Handle, at+1) {
		t.Fatal("the pocket release closed against the ring")
	}
	f.sys.Grid.Clear(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, int(f.u.Handle))
	coll.CachedAnchor = Cell{X: 12, Z: 10}
	f.sys.Grid.Stamp(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, int(f.u.Handle))
	f.sys.noteJamRelease(f.u, coll, false, -1, at+1, modernJamReleaseAfter, modernJamReleaseLifetime)
	if f.sys.releasing(f.u.Handle, at+2) {
		t.Fatal("the pocket release outlived the unit's arrival on its goal")
	}

	g := sealedPocket(t, &ModernRules{})
	setHandleRow(&g.sys.jamReleases, g.u.Handle, jamRelease{until: at + 90, limit: at + 180, cooldown: at + 150})
	g.sys.BeginTick(at)
	g.sys.noteJamRelease(g.u, handleRow(g.sys.Collisions, g.u.Handle), false, -1, at, modernJamReleaseAfter, modernJamReleaseLifetime)
	if g.sys.releasing(g.u.Handle, at+1) {
		t.Fatal("an ordinary release near the destination survived a clear commit")
	}
}

// A certificate grants pocketReleaseGrants releases, each judged once it and
// its cooldown are over; with the grants used and the pocket still sealed,
// the move finishes where the unit stands.
func TestPocketReleaseGrantCapFinishesInPlace(t *testing.T) {
	f := sealedPocket(t, &ModernRules{})
	f.reject(t, 5)
	start := handleRow(f.sys.Collisions, f.u.Handle).CachedAnchor
	tick := uint32(5 + modernPocketDwell)
	for grant := 1; grant <= pocketReleaseGrants; grant++ {
		if f.visit(tick) {
			t.Fatalf("grant %d finished the move", grant)
		}
		jr := handleRow(f.sys.jamReleases, f.u.Handle)
		if !jr.pocket || handleRow(f.sys.pockets, f.u.Handle).grants != uint8(grant) {
			t.Fatalf("release %d not granted: %+v", grant, jr)
		}
		for tick++; tick < jr.cooldown; tick++ {
			if f.visit(tick) {
				t.Fatalf("finished on tick %d, during release %d or its cooldown", tick, grant)
			}
		}
	}
	if !f.visit(tick) || f.sys.pocketLive != 0 {
		t.Fatal("a certificate with its grants used did not finish the move")
	}
	if at := handleRow(f.sys.Collisions, f.u.Handle).CachedAnchor; at != start {
		t.Fatalf("finished at %v, not where the unit stood (%v)", at, start)
	}
}
