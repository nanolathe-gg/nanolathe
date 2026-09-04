package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestInBuildStanceWaitArmsGateFour locks the helper of [04 R-ORD-01 §1]: it
// returns *advance* (1) once the unit's build-stance level is set, and
// otherwise ASSIGNS the dynamic gate to `extra | 0x4` and returns *hold* (2).
// The assignment is not an OR — a caller's `extra` replaces whatever the record
// was waiting for.
func TestInBuildStanceWaitArmsGateFour(t *testing.T) {
	u := &units.Unit{Handle: 1, Def: &content.UnitDef{}}
	n := &Node{DynamicGate: 0xE0, Deadline: -1} // a stale movement wait

	if code := inBuildStanceWait(u, n, gateCancelCurrent, 10); code != 2 {
		t.Fatalf("stance clear returned %d, want hold (2) [04 R-ORD-01 §1]", code)
	}
	if n.DynamicGate&gateBuildStance == 0 {
		t.Fatalf("gate = %#x, want bit 0x4 armed [04 R-ORD-01 §1]", n.DynamicGate)
	}
	if n.DynamicGate&0xE0 != 0 {
		t.Fatalf("gate = %#x, want the stale movement bits replaced, not ORed [04 R-ORD-01 §1]", n.DynamicGate)
	}
	if n.DynamicGate&gateCancelCurrent == 0 {
		t.Fatalf("gate = %#x, want the caller's extra carried [04 R-ORD-01 §1]", n.DynamicGate)
	}

	u.InBuildStance = true
	n.DynamicGate = 0
	if code := inBuildStanceWait(u, n, gateCancelCurrent, 11); code != 1 {
		t.Fatalf("stance set returned %d, want advance (1) [04 R-ORD-01 §1]", code)
	}
	if n.DynamicGate != 0 {
		t.Fatalf("an advancing wait armed gate %#x, want none [04 R-ORD-01 §1]", n.DynamicGate)
	}
}

// TestGateFourIsSatisfiedByTheScriptTouchedMarker locks the producer closed on
// 2026-09-04 in [04 R-COB-06]: gate bit `0x4` is bit 2 of the unit's
// order-event word — the "script-touched marker" every arm of the COB
// engine-write opcode raises — and the pump's satisfied-set merge
// `(record.pending | owner's order-event word) & gate` is what admits it.
//
// The record here carries NO deadline (`Deadline == -1`), so the only thing
// that can dispatch it is the marker. That is the point of the test: the
// wait's stand-in one-tick deadline must not be the reason a parked record ever
// wakes, because retail's helper sets no deadline at all.
func TestGateFourIsSatisfiedByTheScriptTouchedMarker(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("descriptor table is unavailable")
	}

	q, u := gateFixture()
	dispatched := 0
	var sawSatisfied uint32
	restore := setHandler(moveID, func(_ *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
		dispatched++
		sawSatisfied = satisfied
		// A code-2 hold must arm a gate first, exactly as the real wait does;
		// a bare hold on an ungated head loops forever [04 R-ORD-01 §10].
		n.DynamicGate = gateBuildStance
		return Code(2)
	})
	defer restore()

	q.Push(moveID, Node{Owner: u.Handle})
	n := q.Primary()[0]
	n.DynamicGate = gateBuildStance
	n.Satisfied = 0
	n.Deadline = -1 // no deadline: retail's INBUILDSTANCE wait sets none

	// Nothing has touched an engine port yet, so the head stalls.
	q.Pump(u, 100)
	if dispatched != 0 {
		t.Fatalf("record parked on gate 0x4 was dispatched %d times with no marker; a blocked head stalls [04 §3.3]", dispatched)
	}
	if n.DynamicGate != gateBuildStance {
		t.Fatalf("stalled gate = %#x, want it left armed [04 §3.3]", n.DynamicGate)
	}

	// The producer: the unit's script executed an engine write. The value and
	// the port identifier are irrelevant — the marker carries neither
	// [04 R-COB-06].
	u.Pending |= gateBuildStance
	q.Pump(u, 101)
	if dispatched != 1 {
		t.Fatalf("marker raised: dispatched %d times, want exactly 1 [04 R-COB-06]", dispatched)
	}
	if sawSatisfied&gateBuildStance == 0 {
		t.Fatalf("handler saw satisfied = %#x, want bit 0x4 [04 §3.3]", sawSatisfied)
	}
	if u.Pending&gateBuildStance != 0 {
		t.Fatalf("order-event word = %#x, want bit 0x4 consumed by the visit [04 §3.3][04 R-COB-06]", u.Pending)
	}
}

// TestGateFourMarkerPersistsUntilARecordArmsIt locks the accumulation half of
// [04 R-COB-06]: the marker is raised on the UNIT, and the pump clears from the
// unit's word only the bits the visited record's gate names. A script that
// writes an engine port while nothing is waiting on `0x4` therefore leaves the
// bit standing, and it satisfies the next record that arms the gate on that
// record's FIRST visit — there is no edge and no latch to miss.
func TestGateFourMarkerPersistsUntilARecordArmsIt(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("descriptor table is unavailable")
	}

	q, u := gateFixture()
	// The script wrote a port while the head was waiting on something else.
	u.Pending |= gateBuildStance

	dispatched := 0
	restore := setHandler(moveID, func(_ *units.Unit, n *Node, _ uint32, _ uint32) Code {
		dispatched++
		n.DynamicGate = gateBuildStance // [04 R-ORD-01 §10]: a hold arms first
		return Code(2)
	})
	defer restore()

	q.Push(moveID, Node{Owner: u.Handle})
	n := q.Primary()[0]
	n.DynamicGate = 0x40 // a movement outcome the record is actually waiting for
	n.Satisfied = 0
	n.Deadline = -1

	q.Pump(u, 200)
	if dispatched != 0 {
		t.Fatal("a record whose gate does not name bit 0x4 must not be woken by the marker [04 §3.3]")
	}
	if u.Pending&gateBuildStance == 0 {
		t.Fatalf("order-event word = %#x, want bit 0x4 to persist across a visit whose gate excludes it [04 §3.3]", u.Pending)
	}

	// Now the record parks on the build-stance wait; the standing marker
	// satisfies it immediately.
	n.DynamicGate = gateBuildStance
	q.Pump(u, 201)
	if dispatched != 1 {
		t.Fatalf("the standing marker dispatched %d times, want 1 on the first visit that arms 0x4 [04 R-COB-06]", dispatched)
	}
}
