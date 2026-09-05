package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// The queued-order duplicate toggle is not the build placement's private rule:
// [07 R-P0-11 §6] gives it to the one producer EVERY world order the interface
// issues goes through, so a Shift-click on a queued move removes that move and
// issues nothing, exactly as a Shift-click on a queued build site does.
//
// Before WU-19-232 only HumanMobileBuild ran the test; HumanOrder went straight
// to Push, so a second Shift-click on a queued waypoint added a duplicate
// waypoint instead of cancelling it.
//
// The tolerance term is exercised too: the removing click sits half a cell off
// the queued goal, which [07 R-P0-11 §6] accepts (one map cell per axis,
// inclusive, compared on X and Z independently and never on Y).
func TestQueuedWorldOrderRepeatClickRemovesIt(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	tdef := &content.UnitDef{UnitName: "tank", CanMove: true, MaxDamage: 10}
	tdef.CanonicalKey = "tank"
	cat.Units[tdef.CanonicalKey] = tdef
	w := newSessionFixtureWorld(8, cat)
	h, _ := w.Create(tdef, 0, 0, 0, 0)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}

	fx := func(v int) numeric.Fixed { return numeric.Fixed(v) << 16 }
	click := func(x, z int, queued bool, atTick int) {
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
			Handles:  []pool.Handle{h},
			Code:     2, // move [04 §3.4]
			Position: orders.ResolvePos{X: fx(x), Z: fx(z)},
			Queued:   queued,
		}}); err != nil {
			t.Fatal(err)
		}
		s.applyHumanCommands(uint32(atTick))
	}
	goals := func() []int {
		q := orders.QueueForUnit(w.Unit(h))
		out := make([]int, 0, q.LenPrimary())
		for _, n := range q.Primary() {
			out = append(out, int(n.GoalX>>16))
		}
		return out
	}
	same := func(got []int, want ...int) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	click(100, 100, true, 10)
	click(200, 200, true, 11)
	click(300, 300, true, 12)
	if got := goals(); !same(got, 100, 200, 300) {
		t.Fatalf("three queued waypoints = %v, want [100 200 300]", got)
	}

	// A repeat queued click within one cell of the middle waypoint removes
	// exactly that record and issues nothing.
	click(208, 192, true, 13)
	if got := goals(); !same(got, 100, 300) {
		t.Fatalf("after the repeat click = %v, want [100 300] (the toggle removes one queued order and issues none)", got)
	}

	// Front-most match first, and one node per repeat click: two waypoints
	// within a cell of each other lose the front one only.
	click(100, 100, true, 14)
	click(108, 108, true, 15)
	if got := goals(); !same(got, 300, 108) {
		t.Fatalf("after re-queueing and removing the front-most match = %v, want [300 108]", got)
	}

	// A NON-queued click never runs the test: it is the Replace, so it purges
	// and issues even at a point that already carries a queued order.
	click(108, 108, false, 16)
	if got := goals(); !same(got, 108) {
		t.Fatalf("plain click at a queued point = %v, want [108] (Replace purges and issues; it never toggles)", got)
	}
}

// The target term of the same match rule: "the issued target handle is absent
// (zero), or equals the node's target" [07 R-P0-11 §6]. A queued order issued
// against one unit is not removed by a queued click on another, even when the
// two clicks land within a cell of each other.
func TestQueuedWorldOrderMatchComparesTheTarget(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	tdef := &content.UnitDef{UnitName: "tank", CanMove: true, CanGuard: true, MaxDamage: 10}
	tdef.CanonicalKey = "tank"
	cat.Units[tdef.CanonicalKey] = tdef
	w := newSessionFixtureWorld(8, cat)
	h, _ := w.Create(tdef, 0, 0, 0, 0)
	// Two friendly followable targets standing one cell apart.
	fx := func(v int) numeric.Fixed { return numeric.Fixed(v) << 16 }
	a, _ := w.Create(tdef, 0, fx(100), 0, fx(100))
	b, _ := w.Create(tdef, 0, fx(108), 0, fx(108))
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}

	guard := func(target pool.Handle, atTick int) {
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
			Handles: []pool.Handle{h},
			Code:    7, // guard/follow [04 §3.4]
			Target:  target,
			Queued:  true,
		}}); err != nil {
			t.Fatal(err)
		}
		s.applyHumanCommands(uint32(atTick))
	}
	guard(a, 10)
	if n := orders.QueueForUnit(w.Unit(h)).LenPrimary(); n != 1 {
		t.Fatalf("queued guard on A: %d records, want 1", n)
	}
	// Same kind, within tolerance on both axes, different target: no match.
	guard(b, 11)
	if n := orders.QueueForUnit(w.Unit(h)).LenPrimary(); n != 2 {
		t.Fatalf("queued guard on B: %d records, want 2 (a different target must not match)", n)
	}
	// Same kind, same target: the toggle removes it.
	guard(a, 12)
	prim := orders.QueueForUnit(w.Unit(h)).Primary()
	if len(prim) != 1 || prim[0].Target != b {
		t.Fatalf("repeat guard on A left %d records, want just the guard on B", len(prim))
	}
}
