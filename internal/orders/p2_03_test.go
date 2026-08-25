package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestQueueOverflowCap_Primary(t *testing.T) {
	rng.SeedGlobal(1, 0)
	q := &Queue{}
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("lookup Move_Ground failed")
	}
	for i := 0; i < MaxPrimaryQueue+5; i++ {
		q.Push(id, Node{Param1: uint32(i)})
	}
	if len(q.primary) != MaxPrimaryQueue {
		t.Fatalf("primary overflow cap = %d, want %d", len(q.primary), MaxPrimaryQueue)
	}
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for overflow")
	}
	found := false
	for _, d := range q.Diagnostics() {
		if len(d) > 0 && (d[0] == 'o') {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("diagnostics missing overflow text: %v", q.Diagnostics())
	}
}

func TestQueueOverflowCap_Secondary(t *testing.T) {
	q := &Queue{}
	id := Lookup("BuildWeapon")
	if id == 0 {
		t.Fatalf("lookup BuildWeapon failed")
	}
	for i := 0; i < MaxSecondaryQueue+3; i++ {
		q.PushSecondary(id, Node{Param1: uint32(i)})
	}
	if len(q.secondary) != MaxSecondaryQueue {
		t.Fatalf("secondary overflow cap = %d, want %d", len(q.secondary), MaxSecondaryQueue)
	}
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for secondary overflow")
	}
}

func TestQueueIterationLimitDefense(t *testing.T) {
	rng.SeedGlobal(2, 0)
	q := &Queue{}
	u := &units.Unit{Handle: 1, Pending: 0}
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatal("lookup")
	}
	// Handler that always returns 0 (restart) to force infinite cascade without blocking
	restore := setHandler(id, func(u *units.Unit, n *Node, s uint32) Code { return Code(0) })
	defer restore()
	q.Push(id, Node{})
	clearGates(q)
	q.Pump(u, 10)
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected iteration limit diagnostic")
	}
	// Ensure still bounded length and not hung
	if len(q.primary) != 1 {
		t.Fatalf("queue length after limit = %d, want 1", len(q.primary))
	}
}

func TestCoalesceTailWrapAndOverflow(t *testing.T) {
	q := &Queue{}
	id := Lookup("MobileBuild")
	if id == 0 {
		t.Fatalf("lookup MobileBuild")
	}
	q.CoalesceTail(id, Node{Param1: 42, Param2: 0xffffffff})
	if q.primary[0].Param2 != 0xffffffff {
		t.Fatalf("initial coalesce param %d", q.primary[0].Param2)
	}
	q.CoalesceTail(id, Node{Param1: 42, Param2: 2})
	// wrap low32 like retail add
	if q.primary[0].Param2 != 1 { // 0xffffffff +2 = 1 wrap uint32
		t.Fatalf("wrap add got %d want 1", q.primary[0].Param2)
	}
}
