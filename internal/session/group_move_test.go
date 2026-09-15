package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func groupMoveFixture(t *testing.T, positions []orders.ResolvePos) (*Session, []pool.Handle) {
	t.Helper()
	def := &content.UnitDef{UnitName: "mover", BMCode: 1, CanMove: true, CanPatrol: true, MaxDamage: 100}
	def.CanonicalKey = "mover"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"mover": def}}
	w := newSessionFixtureWorld(8, cat)
	s := &Session{Units: w, Catalog: cat, Econ: &economy.Service{}, rngSim: rng.NewSimulation(7), rngCrt: rng.NewCRT(9), rngInitialized: true}
	var handles []pool.Handle
	for _, p := range positions {
		h, err := w.Create(def, 0, p.X, p.Y, p.Z)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, h)
	}
	return s, handles
}

// The broadcast computes its centroid before rejecting actors and truncates
// each squared axis before the inclusive cutoff [04 R-STANCE-01 §5]. Both
// gameplay modes use this retail producer; it consumes neither RNG stream.
func TestHumanGroupMoveRetailDestinations(t *testing.T) {
	point := func(x, z numeric.Fixed) orders.ResolvePos { return orders.ResolvePos{X: x, Z: z} }
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		for _, tc := range []struct {
			name                 string
			positions            []orders.ResolvePos
			offsets              []orders.ResolvePos
			refuseLast, assigned bool
		}{
			{"spacing", []orders.ResolvePos{point(100<<16, 100<<16), point(140<<16, 100<<16)}, []orders.ResolvePos{point(-20<<16, 0), point(20<<16, 0)}, false, false},
			{"whole components before average", []orders.ResolvePos{point(100<<16+49152, 0), point(141<<16+49152, 0)}, []orders.ResolvePos{point(-20<<16+49152, 0), point(21<<16+49152, 0)}, false, false},
			{"negative average truncates toward zero", []orders.ResolvePos{point(-81920, 0), point(-49152, 0)}, []orders.ResolvePos{point(-16384, 0), point(16384, 0)}, false, false},
			{"inclusive cutoff and separate squared truncation", []orders.ResolvePos{point(90<<16+256, 30<<16+512), point(-(90<<16 + 256), -(30<<16 + 512)), point(0, 0)}, []orders.ResolvePos{point(90<<16+256, 30<<16+512), point(-(90<<16 + 256), -(30<<16 + 512)), point(0, 0)}, false, false},
			{"outliers", []orders.ResolvePos{point(91<<16, 30<<16), point(-91<<16, -30<<16), point(0, 0)}, []orders.ResolvePos{point(0, 0), point(0, 0), point(0, 0)}, false, false},
			{"refused actor contributes", []orders.ResolvePos{point(100<<16, 0), point(140<<16, 0)}, []orders.ResolvePos{point(-20<<16, 0)}, true, false},
			{"assigned fractional destination", []orders.ResolvePos{point(100<<16+49152, 0)}, []orders.ResolvePos{point(0, 0)}, false, true},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				s, handles := groupMoveFixture(t, tc.positions)
				s.Gameplay = mode
				if tc.refuseLast {
					u := s.Units.Unit(handles[len(handles)-1])
					def := *u.Def
					def.CanMove = false
					u.Def = &def
				}
				sim, crt := s.rngSim, s.rngCrt
				stock := s.Econ.Players[0].Stock
				click := orders.ResolvePos{X: 1000<<16 + 123, Y: 85<<16 + 456, Z: 1000<<16 + 789}
				// Selection and order share one input drain. No selection snapshot
				// or centroid from the preceding publication may be substituted.
				if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanSelectionReplace, Selection: HumanSelectionCommand{Handles: handles}}); err != nil {
					t.Fatal(err)
				}
				if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Position: click, AssignedPosition: tc.assigned}}); err != nil {
					t.Fatal(err)
				}
				s.applyHumanCommands(1)
				for i, offset := range tc.offsets {
					q := orders.QueueForUnit(s.Units.Unit(handles[i]))
					if q == nil || q.Head() == nil {
						t.Fatalf("actor %d has no move", i)
					}
					n := q.Head()
					if n.GoalX != click.X+offset.X || n.GoalZ != click.Z+offset.Z || n.GoalY != click.Y || n.Param1 != 0 {
						t.Fatalf("actor %d goal=(%d,%d,%d) parameter=%d; want (%d,%d,%d),0", i, n.GoalX, n.GoalY, n.GoalZ, n.Param1, click.X+offset.X, click.Y, click.Z+offset.Z)
					}
				}
				if tc.refuseLast {
					q := orders.QueueForUnit(s.Units.Unit(handles[len(handles)-1]))
					if q != nil && q.Head() != nil {
						t.Fatal("refused actor received an order")
					}
				}
				if s.rngSim != sim || s.rngCrt != crt || s.Econ.Players[0].Stock != stock {
					t.Fatal("selection broadcast consumed RNG or resources")
				}
			})
		}
	}
}

func TestHumanGroupMoveExcludesSelectedTarget(t *testing.T) {
	s, h := groupMoveFixture(t, []orders.ResolvePos{{X: 0}, {X: 20 << 16}, {X: 1000 << 16}})
	// Explicit captured selection is normalized to pool order, without
	// counting duplicate handles as additional selected actors.
	s.applyHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Handles: []pool.Handle{h[2], h[1], h[0], h[0]}, Target: h[2], Position: orders.ResolvePos{X: 500 << 16}}}, 1)
	for i, x := range []numeric.Fixed{490 << 16, 510 << 16} {
		n := orders.QueueForUnit(s.Units.Unit(h[i])).Head()
		if n == nil || n.GoalX != x {
			t.Fatalf("actor %d move=%+v; want X=%d", i, n, x)
		}
	}
	if q := orders.QueueForUnit(s.Units.Unit(h[2])); q != nil && q.Head() != nil {
		t.Fatal("selected target received its own order")
	}
}

func TestHumanGroupMoveQueuedToggleUsesOffsetDestination(t *testing.T) {
	s, h := groupMoveFixture(t, []orders.ResolvePos{{X: 100 << 16}, {X: 140 << 16}})
	c := HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 2, Handles: h, Position: orders.ResolvePos{X: 1000 << 16}}}
	s.applyHumanCommand(c, 1)
	c.Order.Queued = true
	c.Order.Position.X = 1200 << 16
	s.applyHumanCommand(c, 2)
	for i, handle := range h {
		q := orders.QueueForUnit(s.Units.Unit(handle))
		if q.LenPrimary() != 2 || q.Primary()[1].GoalX != numeric.Fixed((1180+i*40)<<16) {
			t.Fatalf("queued offset for %d: %+v", i, q.Primary())
		}
	}
	s.applyHumanCommand(c, 3)
	for _, handle := range h {
		if q := orders.QueueForUnit(s.Units.Unit(handle)); q.LenPrimary() != 1 {
			t.Fatal("repeat offset click failed to toggle queued move")
		}
	}
}

func TestHumanGroupPatrolUsesDescriptorFlag(t *testing.T) {
	s, h := groupMoveFixture(t, []orders.ResolvePos{{X: 100 << 16}, {X: 140 << 16}})
	// An immobile unit's patrol rally marker lacks the formation flag, but
	// the unit still contributes to the mobile actor's centroid.
	u := s.Units.Unit(h[0])
	u.Flags |= units.BuildingClassStatus
	s.applyHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Code: 9, Handles: h, Position: orders.ResolvePos{X: 1000 << 16}}}, 1)
	for i, want := range []struct {
		name string
		x    numeric.Fixed
	}{{"QPatrol", 1000 << 16}, {"Patrol", 1020 << 16}} {
		n := orders.QueueForUnit(s.Units.Unit(h[i])).Head()
		if n == nil || orders.DescriptorFor(n.ID).Name != want.name || n.GoalX != want.x {
			t.Fatalf("actor%d order=%+v want%s at%d", i, n, want.name, want.x)
		}
	}
}
