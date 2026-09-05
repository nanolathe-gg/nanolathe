package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
)

// TestOwnedHandlerIsDispatchedLikeADescriptorHandler locks the first half of
// the registration seam: a row whose body belongs to another package is
// dispatched by the pump, with the pump's own satisfied set, and its result
// code goes through the ordinary epilogue [04 §3.3].
func TestOwnedHandlerIsDispatchedLikeADescriptorHandler(t *testing.T) {
	id := Lookup("MobileBuild")
	if id == 0 {
		t.Fatal("MobileBuild is not in the descriptor table")
	}
	q, u := gateFixture()
	calls := 0
	var gotSatisfied, gotTick uint32
	q.SetOwnedHandler(id, func(_ *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool) {
		calls++
		gotSatisfied, gotTick = satisfied, tick
		// Every handler returning a continuing code first arms a gate on the
		// record, or the walk would not terminate [04 R-ORD-01 §10]. The
		// reload then finds this record blocked and the pass ends.
		n.DynamicGate = 0x8000
		n.Deadline = int32(tick + 30)
		return Code(1), true // *advance the phase* [04 §3.3]
	})
	q.Push(id, Node{Owner: u.Handle})
	n := q.Primary()[0]
	n.DynamicGate = 0x8001
	n.Satisfied = 1
	u.Pending = 0x8000

	q.Pump(u, 45)

	if calls == 0 {
		t.Fatal("the registered handler was never dispatched")
	}
	if gotSatisfied != 0x8001 {
		t.Fatalf("satisfied = %#x, want 0x8001 — the record's bit 0 ORed with the unit's bit 15, masked by the gate", gotSatisfied)
	}
	if gotTick != 45 {
		t.Fatalf("tick = %d, want 45", gotTick)
	}
	if n.Phase == 0 {
		t.Fatal("result code 1 must advance the record's phase [04 §3.3]")
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("a registered row was diagnosed as missing a handler: %v", diags)
	}
}

// TestExternallyDrivenRegistrationEndsThePassUntouched locks the second half:
// a registration that reports it did not advance the record is the owner
// saying its own per-unit step does. Writing a result code over such a record
// overwrites its owner's deadline — for a build row that is the "the plant
// will not build another" stall of PLAN 17 §0 row 3.
func TestExternallyDrivenRegistrationEndsThePassUntouched(t *testing.T) {
	id := Lookup("BuildingBuild")
	if id == 0 {
		t.Fatal("BuildingBuild is not in the descriptor table")
	}
	q, u := gateFixture()
	q.SetExternallyDrivenHandler(id)
	q.Push(id, Node{Owner: u.Handle})
	// A second record behind it proves the pass ends here rather than walking
	// on: a driven head is a stop, exactly as a blocked head is.
	q.Push(Lookup("Stop"), Node{Owner: u.Handle})
	n := q.Primary()[0]
	n.Phase = 2

	const tick = 40
	q.Pump(u, tick)

	if q.LenPrimary() != 2 {
		t.Fatalf("primary length = %d, want both records kept", q.LenPrimary())
	}
	if n.Phase != 2 || n.DynamicGate != 0 || n.Deadline != -1 {
		t.Fatalf("the pump wrote an externally driven record: phase %d gate %#x deadline %d", n.Phase, n.DynamicGate, n.Deadline)
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("an owned record was diagnosed as missing a handler: %v", diags)
	}
}

// TestUnregisteredOwnedRowStillParks is the regression guard the missing-handler
// arm exists for. A row that carries neither a descriptor handler nor an
// owner's registration is not silently ignored: it is parked with the
// contract's own wait, code 3, and diagnosed once per wait [04 §3.3].
//
// This is what makes the registration a real statement rather than a comment —
// the pump's behavior differs on whether the owner made it.
func TestUnregisteredOwnedRowStillParks(t *testing.T) {
	id := Lookup("VTOL_Standby")
	if id == 0 {
		t.Fatal("VTOL_Standby is not in the descriptor table")
	}
	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle})
	n := q.Primary()[0]

	const tick = 40
	q.Pump(u, tick)

	if n.DynamicGate != 1 || n.Deadline < tick+30 || n.Deadline > tick+44 {
		t.Fatalf("unregistered row: gate %#x deadline %d, want the code-3 park [04 §3.3]", n.DynamicGate, n.Deadline)
	}
	if len(q.Diagnostics()) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one naming the unowned order", q.Diagnostics())
	}
}

// TestGetBuiltBindsThroughTheSameSeam keeps the named `GetBuilt` accessor and
// the general registration one mechanism: the construction service's binding
// [04 R-FAC-02 §4] lands in the same per-row slot, and it always reports that
// it advanced the record, because its owner runs it from inside the pump visit.
func TestGetBuiltBindsThroughTheSameSeam(t *testing.T) {
	q, _ := gateFixture()
	if h := q.OwnedHandlerFor(Lookup("GetBuilt")); h != nil {
		t.Fatal("a fresh queue must carry no GetBuilt registration")
	}
	q.SetGetBuiltHandler(func(*units.Unit, *Node, uint32, uint32) Code { return Code(7) })
	h := q.OwnedHandlerFor(Lookup("GetBuilt"))
	if h == nil {
		t.Fatal("SetGetBuiltHandler did not register on the per-row seam")
	}
	if code, ran := h(nil, nil, 0, 0); !ran || code != 7 {
		t.Fatalf("GetBuilt registration reported (%d, %v), want (7, true)", code, ran)
	}
	q.SetGetBuiltHandler(nil)
	if q.OwnedHandlerFor(Lookup("GetBuilt")) != nil {
		t.Fatal("a nil GetBuilt handler must clear the registration")
	}
}

// TestOwnedRegistrationsSurviveQueueReplacement covers the lifecycle boundary.
// A replacement queue that arrived without the owners' registrations would put
// the pump back to writing result codes over a live state machine until the
// next bind, which is the stall the registration exists to prevent.
func TestOwnedRegistrationsSurviveQueueReplacement(t *testing.T) {
	id := Lookup("ReclaimUnit")
	if id == 0 {
		t.Fatal("ReclaimUnit is not in the descriptor table")
	}
	q, u := gateFixture()
	q.SetExternallyDrivenHandler(id)

	replacement := &Queue{}
	BindQueue(u, replacement)
	if replacement.OwnedHandlerFor(id) == nil {
		t.Fatal("the replacement queue lost the owner's registration")
	}
	if QueueOfUnit(u) != replacement {
		t.Fatal("BindQueue did not install the replacement")
	}
}
