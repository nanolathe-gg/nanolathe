package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// Established: activation descriptors skip the nonqueued replacement purge,
// head-insert, then complete without consuming the displaced mission's gate
// [04 R-ORD-01 §2, §13]. A toggle during a mobile build therefore preserves
// both the pending build and the move queued after it.
func TestHumanActivationPreservesMissionQueue(t *testing.T) {
	def := &content.UnitDef{BMCode: 1, OnOffable: true, MaxDamage: 100}
	def.CanonicalKey = "builder"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}}
	w := newSessionFixtureWorld(4, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	s := &Session{Units: w, Catalog: cat, LocalOwner: 0}
	s.bindOrderQueue(u)
	q := orders.QueueForUnit(u)
	q.Push(orders.Lookup("MobileBuild"), orders.Node{Owner: h})
	build := q.Primary()[0]
	build.Phase, build.DynamicGate, build.Deadline = 2, 0xe, -1
	build.Param2 = 3
	build.Flags |= orders.FlagStopBuildingPending
	q.Push(orders.Lookup("Move_Ground"), orders.Node{Owner: h, QueuedIssue: true})
	move := q.Primary()[1]
	for i, activate := range []bool{true, false} {
		tick := uint32(100 + i)
		s.applyHumanCommand(HumanCommand{Kind: HumanActivation, Activation: HumanActivationCommand{Unit: h, Activate: activate}}, tick)
		if q.LenPrimary() != 3 || q.Primary()[1] != build || q.Primary()[2] != move {
			t.Fatalf("activation=%v discarded or reordered a mission", activate)
		}
		q.Pump(u, tick)
		if u.Activated != activate {
			t.Fatalf("activation=%v did not run before the waiting build", activate)
		}
		if q.LenPrimary() != 2 || q.Primary()[0] != build || q.Primary()[1] != move {
			t.Fatalf("activation=%v completion changed the mission queue", activate)
		}
		if build.Phase != 2 || build.DynamicGate != 0xe || build.Deadline != -1 || build.Param2 != 3 || build.Flags&orders.FlagStopBuildingPending == 0 {
			t.Fatalf("activation=%v changed the waiting build: %+v", activate, build)
		}
	}
}
