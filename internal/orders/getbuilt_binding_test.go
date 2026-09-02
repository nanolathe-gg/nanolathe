package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The bound `GetBuilt` handler receives the pump's own satisfied set,
// `(record pending | unit pending) & gate`, and the pump clears the consumed
// bits out of both words before dispatching. `GetBuilt`'s gate is `0x8001`, so
// the shared work step's `0x8000` reaches the handler and its phase-2 body can
// choose its arm [04 R-ORD-01 §10][04 R-ORD-01 §11].
func TestGetBuiltHandlerReceivesTheSatisfiedSet(t *testing.T) {
	getBuiltID := Lookup("GetBuilt")
	if getBuiltID == 0 {
		t.Fatalf("GetBuilt descriptor missing")
	}
	rng.SeedGlobal(1, 0)
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	u.Pending = 0x8000

	var got uint32
	var gotTick uint32
	calls := 0
	q.SetGetBuiltHandler(func(_ *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
		calls++
		got, gotTick = satisfied, tick
		n.DynamicGate = 0x8001
		n.Deadline = int32(tick + 30)
		return Code(2)
	})
	q.Push(getBuiltID, Node{Phase: 2})
	q.primary[0].DynamicGate = 0x8001
	q.primary[0].Satisfied = 1

	q.Pump(u, 45)

	if calls != 1 {
		t.Fatalf("handler calls=%d, want 1", calls)
	}
	if got != 0x8001 {
		t.Fatalf("satisfied=%#x, want 0x8001 — the record's bit 0 ORed with the unit's bit 15, masked by the gate", got)
	}
	if gotTick != 45 {
		t.Fatalf("tick=%d, want 45", gotTick)
	}
	if u.Pending&0x8000 != 0 {
		t.Fatalf("the pump must clear the consumed bit from the unit word: pending=%#x", u.Pending)
	}
	if q.primary[0].Satisfied&1 != 0 {
		t.Fatalf("the pump must clear the consumed bit from the record word: satisfied=%#x", q.primary[0].Satisfied)
	}
	if q.primary[0].Param1 != 0 {
		t.Fatalf("no scratch-word mirror carries the wake any more: Param1=%d", q.primary[0].Param1)
	}
}
