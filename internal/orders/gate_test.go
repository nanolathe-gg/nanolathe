package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// gateFixture is one unit with a bound queue carrying a seeded simulation
// stream, so a parked record's 30..44 tick draw comes from a stream the test
// owns [I4].
func gateFixture() (*Queue, *units.Unit) {
	rng.SeedGlobal(1, 0)
	u := &units.Unit{
		Handle: 1,
		Def:    &content.UnitDef{},
		X:      numeric.Fixed(70 << 16),
		Y:      numeric.Fixed(40 << 16),
		Z:      numeric.Fixed(90 << 16),
	}
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	BindQueue(u, q)
	return q, u
}

// TestFreshRecordAwaitsNothing locks the record constructor against the defect
// WU-18-0 corrected: the dynamic gate was seeded from the descriptor's static
// mask. The two fields are different things — [04 §3.1]'s census makes the
// static mask insertion metadata (target/goal presence, segment select,
// nanolathe class), while [04 §3.3] makes the dynamic gate the set of bits the
// record is waiting for — and "the record constructor zeroes the dynamic gate
// and the pending word" is the contract [04 R-ORD-01 §1].
//
// The relationship asserted is per descriptor, over the table's own slice
// order (I1): the static mask survives on the record's own copy field
// [04 §3.2], and the dynamic gate is empty.
func TestFreshRecordAwaitsNothing(t *testing.T) {
	masked := 0
	for id := range Table() {
		desc := Table()[id]
		if desc.Name == "" {
			continue // the reject sentinel is never inserted [04 §3.1]
		}
		q, u := gateFixture()
		q.Push(ID(id), Node{Owner: u.Handle})
		if q.LenPrimary()+q.LenSecondary() != 1 {
			t.Fatalf("%s: push queued %d records, want one", desc.Name, q.LenPrimary()+q.LenSecondary())
		}
		var n *Node
		if q.LenPrimary() > 0 {
			n = q.Primary()[0]
		} else {
			n = q.Secondary()[0] // the rear segment, for a descriptor that selects it
		}
		if n.DynamicGate != 0 {
			t.Fatalf("%s: fresh dynamic gate = %#x, want 0 [04 R-ORD-01 §1]", desc.Name, n.DynamicGate)
		}
		if n.StaticGate != desc.StaticGate {
			t.Fatalf("%s: static-mask copy = %#x, want the descriptor's %#x [04 §3.2]", desc.Name, n.StaticGate, desc.StaticGate)
		}
		if n.Satisfied != 0 {
			t.Fatalf("%s: fresh pending word = %#x, want 0 [04 R-ORD-01 §1]", desc.Name, n.Satisfied)
		}
		if desc.StaticGate != 0 {
			masked++
		}
	}
	if masked == 0 {
		t.Fatal("no descriptor carries a nonzero static mask; the fixture proves nothing")
	}
}

// TestStaticMaskedRecordDispatchesOnItsFirstVisit is the contract every WU-18-x
// family depends on: a record whose descriptor carries a nonzero static mask
// and a wired handler reaches that handler on its FIRST pump visit, with an
// empty satisfied set [04 R-ORD-01 §1], instead of parking at the head of the
// queue on a gate nothing raises [04 §3.3] step 3.
//
// `Capture` is the subject because it is one of the 35 orders this phase wires
// and its static mask is nonzero (0x200, "constructed with a target unit"), so
// the probe handler installed here stands in for the real one exactly as a
// family unit's will.
func TestStaticMaskedRecordDispatchesOnItsFirstVisit(t *testing.T) {
	id := Lookup("Capture")
	if id == 0 {
		t.Fatal("Capture is not in the descriptor table")
	}
	if DescriptorFor(id).StaticGate == 0 {
		t.Fatal("Capture's static mask is zero; the test no longer covers the defect")
	}
	visits, sawSatisfied := 0, uint32(0xffffffff)
	restore := setHandler(id, func(_ *units.Unit, _ *Node, satisfied uint32, _ uint32) Code {
		visits++
		sawSatisfied = satisfied
		return Code(5) // complete [04 R-ORD-01 §1]
	})
	defer restore()

	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle, Target: 7})
	q.Pump(u, 40)

	if visits != 1 {
		t.Fatalf("handler visits = %d on the first pump, want 1 [04 §3.3]", visits)
	}
	if sawSatisfied != 0 {
		t.Fatalf("satisfied set = %#x on the first visit, want empty [04 R-ORD-01 §1]", sawSatisfied)
	}
	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the completed record freed [04 §3.3] code 5", q.LenPrimary())
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("dispatch recorded %v, want none", diags)
	}
}

// TestUnwiredStaticMaskedRecordReachesTheDiagnosticArm is the other half of the
// same correction. Before it, a record whose descriptor had a nonzero static
// mask and no handler blocked at [04 §3.3] step 3 and never reached the pump's
// missing-handler arm, so 33 unimplemented orders were invisible: they jammed
// their unit silently. Now the record dispatches, finds no handler, and takes
// the bounded park the arm applies — [04 §3.3]'s own code 3, gate bit 0 and a
// deadline 30 to 44 ticks out — with a diagnostic naming the order.
func TestUnwiredStaticMaskedRecordReachesTheDiagnosticArm(t *testing.T) {
	id := Lookup("Resurrect")
	if id == 0 || DescriptorFor(id).StaticGate == 0 {
		t.Fatal("Resurrect must exist and carry a nonzero static mask")
	}
	if DescriptorFor(id).Handler != nil {
		t.Skip("Resurrect now has a handler; its own unit's test owns this case")
	}
	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle, Target: 7})

	const tick = 40
	q.Pump(u, tick)

	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the parked record kept", q.LenPrimary())
	}
	n := q.Primary()[0]
	if n.DynamicGate != 1 {
		t.Fatalf("parked gate = %#x, want the lowest gate bit [04 §3.3] code 3", n.DynamicGate)
	}
	if n.Deadline < tick+30 || n.Deadline > tick+44 {
		t.Fatalf("parked deadline = %d, want %d..%d [04 §3.3] code 3", n.Deadline, tick+30, tick+44)
	}
	if len(q.Diagnostics()) != 1 {
		t.Fatalf("diagnostics = %v, want exactly one naming the unimplemented order", q.Diagnostics())
	}
}

// drivenDescriptors are the seven handler-less records another package runs
// from its own per-unit step, reading and writing the record's phase, dynamic
// gate and deadline as its state machine (handlerlessButDriven, pump.go). The
// list is a fixed slice, not a map, so the assertions run in one order (I1).
var drivenDescriptors = []string{
	"BuildingBuild", "MobileBuild", "VTOL_MobileBuild", // internal/construction
	"ReclaimUnit", "VTOL_ReclaimUnit", // internal/construction
	"VTOL_LandIfCan", "VTOL_Standby", // internal/movement
}

// TestDrivenRecordsKeepTheirOwnersScheduling covers the seven records the pump
// does not drive. Correcting the record constructor touches every record's
// birth, and these are the ones where the pump must then keep its hands off:
// writing a result code over a live state machine's phase, gate or deadline is
// the factory stall of PLAN 17 §0 row 3 (internal/construction/factory.go gates
// its own step on `node.Deadline >= 0 && tick < node.Deadline`).
//
// Two shapes are checked per descriptor, because the owning package produces
// both: a record its driver has parked on a wake bit and a deadline, and a
// record its driver has left ready (gate 0) between states.
func TestDrivenRecordsKeepTheirOwnersScheduling(t *testing.T) {
	for _, name := range drivenDescriptors {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		if DescriptorFor(id).Handler != nil {
			t.Fatalf("%s has acquired a descriptor handler; handlerlessButDriven must be revisited", name)
		}

		// Birth: the driver's own writes are the only scheduling the record
		// ever carries, so it must not be born waiting on insertion metadata.
		q, u := gateFixture()
		q.Push(id, Node{Owner: u.Handle})
		fresh := q.Primary()[0]
		if fresh.DynamicGate != 0 || fresh.Deadline != -1 {
			t.Fatalf("%s: born with gate %#x deadline %d, want 0 and -1 [04 R-ORD-01 §1]", name, fresh.DynamicGate, fresh.Deadline)
		}

		// Parked by its driver: a wake bit plus a deadline still in the future.
		const tick = 40
		fresh.Phase = 2
		fresh.DynamicGate = 0x8001
		fresh.Deadline = tick + 20
		q.Pump(u, tick)
		if q.LenPrimary() != 1 {
			t.Fatalf("%s: primary length = %d, want the driven record kept", name, q.LenPrimary())
		}
		if fresh.Phase != 2 || fresh.DynamicGate != 0x8001 || fresh.Deadline != tick+20 {
			t.Fatalf("%s: pump rewrote a driven record to phase %d gate %#x deadline %d", name, fresh.Phase, fresh.DynamicGate, fresh.Deadline)
		}

		// Ready between states: gate cleared by its driver, no deadline. The
		// pump reaches the dispatch point, finds no handler, and must leave
		// without parking it — a park here is the 30..44 tick stall.
		q2, u2 := gateFixture()
		q2.Push(id, Node{Owner: u2.Handle})
		ready := q2.Primary()[0]
		ready.Phase = 3
		q2.Pump(u2, tick)
		if q2.LenPrimary() != 1 {
			t.Fatalf("%s: ready record was removed by the pump", name)
		}
		if ready.Phase != 3 || ready.DynamicGate != 0 || ready.Deadline != -1 {
			t.Fatalf("%s: pump parked a ready driven record: phase %d gate %#x deadline %d", name, ready.Phase, ready.DynamicGate, ready.Deadline)
		}
		if diags := q2.Diagnostics(); len(diags) != 0 {
			t.Fatalf("%s: driven record was diagnosed as missing a handler: %v", name, diags)
		}
	}
}

// TestHandlerInstallersRunBeforeTheDescriptorIsRead locks the ordering the
// registration seam depends on. Descriptor is a value: a copy taken before the
// installers run carries the Handler the entry held at that moment, so reading
// it first made the first record of the first pump see a nil handler for a
// family that had just been installed and park itself for 30..44 ticks with a
// diagnostic that was already untrue. Every family this phase adds installs
// through the same list, so the ordering is theirs too.
func TestHandlerInstallersRunBeforeTheDescriptorIsRead(t *testing.T) {
	id := Lookup("Stop")
	if id == 0 {
		t.Fatal("Stop is not in the descriptor table")
	}
	table[int(id)].Handler = nil // as a fixture that swapped a handler out leaves it
	defer func() { table[int(id)].Handler = stopHandler }()

	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle})
	q.Pump(u, 40)

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want Stop dispatched and completed on its first visit [04 R-ORD-01 §2]", q.LenPrimary())
	}
	if diags := q.Diagnostics(); len(diags) != 0 {
		t.Fatalf("diagnostics = %v, want none: the installer runs before the descriptor is read", diags)
	}
}
