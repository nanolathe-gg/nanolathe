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
	// The three slots go through the initializer, which is what writes the
	// control byte's "slot is enabled" bit that release and inhibit both
	// require [04 R-ORD-01 §7] [06 R-WPN-05 §3].
	for idx := 0; idx < units.NumSlots; idx++ {
		u.InstallWeapon(idx, &content.WeaponDef{Range: 180})
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
// [04 §3.2], except that the constructor clears bit 9 (0x200) when the push
// below supplies no target — [04 §3.1]'s "constructed without a target unit
// clears it", applied by newNode [04 R-MOV-03 §7] (WU-19-110) — and the
// dynamic gate is empty.
func TestFreshRecordAwaitsNothing(t *testing.T) {
	masked := 0
	for id := range Table() {
		desc := Table()[id]
		if desc.Name == "" {
			continue // the reject sentinel is never inserted [04 §3.1]
		}
		q, u := gateFixture()
		q.Push(ID(id), Node{Owner: u.Handle}) // no target supplied
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
		wantStatic := desc.StaticGate &^ staticTargetObserver // no target supplied [04 §3.1][04 R-MOV-03 §7]
		if n.StaticGate != wantStatic {
			t.Fatalf("%s: static-mask copy = %#x, want the descriptor's %#x with bit 9 cleared (no target supplied) [04 §3.1][04 R-MOV-03 §7]", desc.Name, n.StaticGate, wantStatic)
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

// TestNewNodeClearsTargetSuppliedBitWithoutATarget locks the constructor clear
// [04 §3.1]'s "0x200 cleared when the order is constructed without a target
// unit", closed by [04 R-MOV-03 §7] and applied by newNode in WU-19-110: a
// record built with no target has static bit 9 (staticTargetObserver) clear
// regardless of what the descriptor carries, and a record built WITH a target
// keeps the descriptor's mask untouched. `Capture`'s descriptor mask is 0x200
// (bit 9 alone), so it isolates the bit.
func TestNewNodeClearsTargetSuppliedBitWithoutATarget(t *testing.T) {
	id := Lookup("Capture")
	if id == 0 {
		t.Fatal("Capture is not in the descriptor table")
	}
	desc := DescriptorFor(id)
	if desc.StaticGate&staticTargetObserver == 0 {
		t.Fatalf("Capture's static mask %#x does not carry bit 9; the fixture proves nothing", desc.StaticGate)
	}

	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle}) // no target supplied
	targetless := q.Primary()[0]
	if targetless.StaticGate&staticTargetObserver != 0 {
		t.Fatalf("target-less record static mask = %#x, want bit 9 clear [04 §3.1][04 R-MOV-03 §7]", targetless.StaticGate)
	}

	q2, u2 := gateFixture()
	q2.Push(id, Node{Owner: u2.Handle, Target: 7}) // target supplied
	targeted := q2.Primary()[0]
	if targeted.StaticGate != desc.StaticGate {
		t.Fatalf("targeted record static mask = %#x, want the descriptor's own %#x untouched [04 §3.1][04 §3.2]", targeted.StaticGate, desc.StaticGate)
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

// drivenDescriptors are the records another package runs from its own per-unit
// step, reading and writing the record's phase, dynamic gate and deadline as
// its state machine. The owning package declares each of them on the queue
// through the registration seam of queue_handlers.go; this list mirrors those
// registrations (construction's buildRowsDrivenByStepUnit and movement's
// airRowsDrivenByMoverTick). It is a fixed slice, not a map, so the assertions
// run in one order (I1).
//
// `VTOL_LandIfCan` left this list on 2026-08-31 and the count went from seven
// to six. It is still driven from the mover tick, but a machine that finishes
// needs a way to say so: with no handler nothing freed the record on touchdown
// and it sat at the head of the queue for the rest of the unit's life. It now
// has a descriptor handler that hands off to the air runner, which reports the
// executor's outcome rather than re-running it. See
// TestLandIfCanCompletesOnTouchdown below.
var drivenDescriptors = []string{
	"BuildingBuild", "MobileBuild", "VTOL_MobileBuild", // internal/construction
	"ReclaimUnit", "VTOL_ReclaimUnit", // internal/construction
	"VTOL_Standby", // internal/movement
}

// TestDrivenRecordsKeepTheirOwnersScheduling covers the records the pump does
// not drive. Correcting the record constructor touches every record's
// birth, and these are the ones where the pump must then keep its hands off:
// writing a result code over a live state machine's phase, gate or deadline is
// the factory stall of PLAN 17 §0 row 3 (internal/construction/factory.go gates
// its own step on `node.Deadline >= 0 && tick < node.Deadline`).
//
// Two shapes are checked per descriptor, because the owning package produces
// both: a record its driver has parked on a wake bit and a deadline, and a
// record its driver has left ready (gate 0) between states.
//
// The fixture registers the row the way its owner does, because that
// registration IS the contract under test: the pump learns from the owner, not
// from a value in the descriptor table, that the record is not its to write.
func TestDrivenRecordsKeepTheirOwnersScheduling(t *testing.T) {
	for _, name := range drivenDescriptors {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		if DescriptorFor(id).Handler != nil {
			t.Fatalf("%s has acquired a descriptor handler; its owner's registration must be revisited", name)
		}

		// Birth: the driver's own writes are the only scheduling the record
		// ever carries, so it must not be born waiting on insertion metadata.
		q, u := gateFixture()
		q.SetExternallyDrivenHandler(id)
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
		// pump reaches the dispatch point, dispatches the owner's registration,
		// is told the owner advances the record elsewhere, and must leave
		// without parking it — a park here is the 30..44 tick stall.
		q2, u2 := gateFixture()
		q2.SetExternallyDrivenHandler(id)
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

// TestHandlersAreInstalledBeforeTheFirstPump locks the registration seam: the
// package's init builds the table and runs every family installer, so all 68
// descriptors carry their handler before any pump runs [04 §3.1].
//
// This replaces a test that nil'd a handler in the global table "as a fixture
// that swapped a handler out leaves it" and asserted the pump put it back. The
// pump re-ran all twelve installers for every primary record of every pump to
// make that true. That was production work existing for fixtures, and it hid
// the real contract, which is that the table is complete before play begins —
// retail compiles the handler into each static descriptor.
func TestHandlersAreInstalledBeforeTheFirstPump(t *testing.T) {
	registeredByAnOwner := func(name string) bool {
		if name == "GetBuilt" {
			// The construction service binds this one per queue
			// [04 R-FAC-02 §4].
			return true
		}
		for _, driven := range drivenDescriptors {
			if driven == name {
				return true
			}
		}
		return false
	}
	for id, desc := range Table() {
		switch {
		case desc.Name == "":
			continue // the reject sentinel has no handler
		case registeredByAnOwner(desc.Name):
			// The owning subsystem registers this row on the queue
			// (queue_handlers.go), so the descriptor carries no handler.
			continue
		case desc.Driver != DriverPump:
			// The movement scheduler owns the route lifecycle; see the Driver
			// field.
			continue
		}
		if desc.Handler == nil {
			t.Errorf("descriptor %d %q has no handler, no owner registration and no other driver", id, desc.Name)
		}
	}
}
