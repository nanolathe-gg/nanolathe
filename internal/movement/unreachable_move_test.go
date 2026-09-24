package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Unreachable moves are off under Strict and Community and on under Modern
// (docs/DESIGN_MOVEMENT_PATH.md "Modern unreachable moves"). The off answer
// leaves the System's certificate rows unallocated whatever the publication
// hook and the follower are handed, and the dispatch allocates nothing.
func TestUnreachableMovesAnswers(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		if frontier, dwell := rules.UnreachableMoves(nil); frontier != 0 || dwell != 0 {
			t.Fatalf("%T UnreachableMoves = (%d, %d), want retail (0, 0)", rules, frontier, dwell)
		}
	}
	if frontier, dwell := (&ModernRules{}).UnreachableMoves(nil); frontier != modernUnreachableFrontierCells || dwell != modernUnreachableDwell {
		t.Fatalf("Modern UnreachableMoves = (%d, %d)", frontier, dwell)
	}
	var unbound System
	if frontier, _ := unbound.rules().UnreachableMoves(&unbound); frontier != 0 {
		t.Fatal("an unbound system completes unreachable moves")
	}
	unbound.Rules = &ModernRules{}
	if n := testing.AllocsPerRun(100, func() { _, _ = unbound.rules().UnreachableMoves(&unbound) }); n != 0 {
		t.Fatalf("UnreachableMoves allocated %g times", n)
	}

	for _, rules := range []Rules{nil, StrictRules{}, CommunityRules{}} {
		s := System{Rules: rules}
		u := &units.Unit{Handle: 5, Alive: true}
		s.noteRouteUnavailable(u, nil, path.Request{Unit: 5, Activation: 1})
		s.clearUnreachable(5)
		if s.unreachableArrival(u, nil, 100) || s.unreachable != nil || s.unreachableLive != 0 || s.unreachableGoals != nil {
			t.Fatalf("%T: the off answer touched certificate state", rules)
		}
	}
}

// The certificate row keeps its live count through set, replace and clear,
// and a clear never grows the row.
func TestUnreachableCertificateLiveCount(t *testing.T) {
	var s System
	s.clearUnreachable(9)
	if s.unreachable != nil {
		t.Fatal("clearing an absent certificate grew the row")
	}
	s.setUnreachable(3, unreachableCert{order: &orders.Node{}, since: 10})
	s.setUnreachable(3, unreachableCert{order: &orders.Node{}, since: 11})
	s.setUnreachable(4, unreachableCert{order: &orders.Node{}, since: 12})
	if s.unreachableLive != 2 {
		t.Fatalf("live = %d after two units certified, want 2", s.unreachableLive)
	}
	s.DeactivateMove(3)
	s.ForgetUnit(4)
	if s.unreachableLive != 0 || handleRow(s.unreachable, pool.Handle(3)).order != nil || handleRow(s.unreachable, pool.Handle(4)).order != nil {
		t.Fatalf("deactivation and forgetting left certificates: live %d", s.unreachableLive)
	}
}

// The probe's static view keeps every answer the search's read admits and
// re-reads a blocked anchor with mobile occupants transparent: a stationary
// mover the occupant-age gate walled is passable, while a building (an
// occupant with no mover) and void terrain stay blocked, and unexplored
// ground stays the search's optimistic value 2 whatever lies there
// [04 R-PATH-01 §2][04 R-PATH-01 §14].
func TestStaticPassableSeesThroughMobilesOnly(t *testing.T) {
	tr := layerTerrain(16, 16, 20)
	tr.PlotAt(3, 12).SetFeature(world.PlotFeatureVoid)
	grid := NewOccupancyGrid()
	l := NewClassLayer(kbotsSS2, tr, grid)
	const mobile, building = pool.Handle(7), pool.Handle(8)
	l.movers = stubMovers{mobile: true}
	grid.Stamp(Cell{X: 6, Z: 6}, 1, 1, int(mobile))
	grid.Stamp(Cell{X: 10, Z: 10}, 1, 1, int(building))
	l.NoteCommit(mobile, 0)
	l.watermark = 20
	l.RestampRect(0, 0, 15, 15)
	for _, c := range []struct {
		name   string
		x, z   int32
		static bool
	}{{"parked mobile", 6, 6, true}, {"building", 10, 10, false}, {"void", 3, 12, false}, {"open", 1, 1, true}} {
		if c.name != "open" && l.Value(c.x, c.z) != LayerBlocked {
			t.Fatalf("%s: the search's layer does not wall anchor (%d,%d)", c.name, c.x, c.z)
		}
		if got := l.staticPassable(c.x, c.z, 2, 2, 0, nil) != LayerBlocked; got != c.static {
			t.Fatalf("%s: static view passable=%v, want %v", c.name, got, c.static)
		}
	}
	// Unexplored for player 0: the static view answers the search's own 2,
	// even on void ground and a building.
	l.mapping = func(int32, int32) (uint16, bool) { return 1 << 1, true }
	for _, at := range []Cell{{X: 3, Z: 12}, {X: 10, Z: 10}} {
		if got := l.staticPassable(at.X, at.Z, 2, 2, 0, nil); got != LayerUnmapped {
			t.Fatalf("unexplored anchor %v read %d, want the optimistic %d", at, got, LayerUnmapped)
		}
	}
}
