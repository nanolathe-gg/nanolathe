package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestQueueOverflowCap_Primary(t *testing.T) {
	// [P1-I09] queue is now dynamic with OOM guard far outside stock (105 << 10000).
	// The old 64 cap was inside stock and has been replaced; the guard is now OOMGuardQueue.
	rng.SeedGlobal(1, 0)
	q := &Queue{}
	id := Lookup("Move_Ground")
	if id == 0 {
		t.Fatalf("lookup Move_Ground failed")
	}
	for i := 0; i < OOMGuardQueue+5; i++ {
		q.Push(id, Node{Param1: uint32(i)})
	}
	if len(q.primary) != OOMGuardQueue {
		t.Fatalf("primary OOM guard cap = %d, want %d", len(q.primary), OOMGuardQueue)
	}
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for OOM guard overflow")
	}
	found := false
	for _, d := range q.Diagnostics() {
		if len(d) > 0 && (d[0] == 'o') {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("diagnostics missing OOM guard text: %v", q.Diagnostics())
	}
	// Stock-reachable length 105 must not trigger guard
	q2 := &Queue{}
	for i := 0; i < 105; i++ {
		q2.Push(id, Node{Param1: uint32(i)})
	}
	if len(q2.primary) != 105 {
		t.Fatalf("stock queue 105 should not be capped, got %d", len(q2.primary))
	}
	if len(q2.Diagnostics()) != 0 {
		t.Fatalf("stock queue should not diagnostic, got %v", q2.Diagnostics())
	}
}

func TestQueueOverflowCap_Secondary(t *testing.T) {
	// Secondary is also dynamic with OOM guard [P1-I09]; maxSecondary in corpus is 1 << 10000.
	q := &Queue{}
	id := Lookup("BuildWeapon")
	if id == 0 {
		t.Fatalf("lookup BuildWeapon failed")
	}
	for i := 0; i < OOMGuardQueue+3; i++ {
		q.PushSecondary(id, Node{Param1: uint32(i)})
	}
	if len(q.secondary) != OOMGuardQueue {
		t.Fatalf("secondary OOM guard cap = %d, want %d", len(q.secondary), OOMGuardQueue)
	}
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("expected diagnostic for secondary OOM guard overflow")
	}
	// Stock secondary max 1 should not hit guard
	q2 := &Queue{}
	q2.PushSecondary(id, Node{Param1: 1})
	if len(q2.secondary) != 1 || len(q2.Diagnostics()) != 0 {
		t.Fatalf("stock secondary should not guard")
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
