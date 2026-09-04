package orders

// Contract test for the deletion half of WU-19-148. Retail's `INBUILDSTANCE`
// wait sets NO deadline: "Nothing in either body touches the record's deadline
// word. A record parked on 0x4 is woken by exactly one thing: the unit's script
// writing an engine port" [04 R-COB-06]. This build used to arm the shared
// one-tick deadline setter as a stand-in, because gate bit 0x4 had no producer
// and the record would otherwise have parked forever. The producer now exists
// (the COB engine-write opcode, via units.Unit.raiseScriptTouched), so the
// stand-in is gone and this test is what keeps it gone.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestInBuildStanceWaitSetsNoDeadline pins the body: a holding wait writes the
// gate and nothing else. Neither the deadline word nor the deadline gate bit
// may be touched [04 R-COB-06].
func TestInBuildStanceWaitSetsNoDeadline(t *testing.T) {
	u := &units.Unit{Handle: 1, Def: &content.UnitDef{}}
	n := &Node{Deadline: -1}

	if code := inBuildStanceWait(u, n, 0, 500); code != 2 {
		t.Fatalf("stance clear returned %d, want hold (2) [04 R-ORD-01 §1]", code)
	}
	if n.Deadline != -1 {
		t.Fatalf("deadline = %d, want it untouched at -1 — retail's INBUILDSTANCE wait sets no deadline [04 R-COB-06]", n.Deadline)
	}
	if n.DynamicGate&gateDeadline != 0 {
		t.Fatalf("gate = %#x, want the deadline bit clear [04 R-COB-06]", n.DynamicGate)
	}
	if n.DynamicGate != gateBuildStance {
		t.Fatalf("gate = %#x, want exactly 0x4 for a caller passing no extra [04 R-ORD-01 §1]", n.DynamicGate)
	}
}

// TestParkedBuildStanceRecordWaitsForTheMarkerNotTheClock is the behavioral
// half. A record parked by the real helper must stall for as many ticks as the
// script stays silent — under the stand-in it re-dispatched on the very next
// tick — and must advance on the visit after the marker is raised, whatever the
// tick count is by then [04 R-COB-06].
func TestParkedBuildStanceRecordWaitsForTheMarkerNotTheClock(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatal("descriptor table is unavailable")
	}

	q, u := gateFixture()
	dispatched := 0
	restore := setHandler(moveID, func(unit *units.Unit, n *Node, _ uint32, tick uint32) Code {
		dispatched++
		if code := inBuildStanceWait(unit, n, 0, tick); code != 1 {
			return code
		}
		// The wait advanced. A real work handler would go on to its next
		// phase; the probe completes so the walk terminates — code 1 alone
		// re-dispatches the head forever [04 §3.3].
		return Code(5)
	})
	defer restore()

	q.Push(moveID, Node{Owner: u.Handle})
	n := q.Primary()[0]

	// First visit: the stance is clear, so the record parks on gate 0x4.
	q.Pump(u, 1000)
	if dispatched != 1 {
		t.Fatalf("first visit dispatched %d times, want 1", dispatched)
	}
	if n.DynamicGate != gateBuildStance {
		t.Fatalf("parked gate = %#x, want exactly 0x4 [04 R-ORD-01 §1]", n.DynamicGate)
	}

	// Sixty silent ticks. Under the deleted one-tick stand-in this dispatched
	// sixty more times; with retail's deadline-free park it dispatches none.
	for tick := uint32(1001); tick <= 1060; tick++ {
		q.Pump(u, tick)
	}
	if dispatched != 1 {
		t.Fatalf("a parked build-stance record dispatched %d times over sixty silent ticks, want 1 — the wake must come from the script-touched marker, not a clock [04 R-COB-06]", dispatched)
	}

	// The unit's script writes an engine port — any port; the marker carries no
	// value — and raises the stance on the way through.
	u.InBuildStance = true
	u.Pending |= units.PendingScriptTouched
	q.Pump(u, 1061)
	if dispatched != 2 {
		t.Fatalf("after the marker the record dispatched %d times, want 2 [04 R-COB-06]", dispatched)
	}
	if len(q.Primary()) != 0 {
		t.Fatalf("the wait advanced but the record is still queued (%d records) [04 R-ORD-01 §1]", len(q.Primary()))
	}
}
