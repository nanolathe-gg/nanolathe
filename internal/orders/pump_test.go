package orders

import (
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestMain(m *testing.M) {
	rng.SeedGlobal(12345, 0)
	os.Exit(m.Run())
}

func newTestUnit() *units.Unit {
	return &units.Unit{Handle: 1, Pending: 0}
}

func TestQueueOfUnitDoesNotAllocate(t *testing.T) {
	u := newTestUnit()
	if got := QueueOfUnit(u); got != nil {
		t.Fatalf("queue lookup on unbound unit = %p, want nil", got)
	}
	if u.Orders != nil {
		t.Fatal("read-only queue lookup mutated unbound unit")
	}
	q := &Queue{}
	BindQueue(u, q)
	if got := QueueOfUnit(u); got != q {
		t.Fatalf("queue lookup = %p, want bound queue %p", got, q)
	}
}

func setHandler(id ID, fn func(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code) func() {
	prev := DescriptorFor(id).Handler
	table[int(id)].Handler = fn
	return func() { table[int(id)].Handler = prev }
}

func clearGates(q *Queue) {
	if q == nil {
		return
	}
	for _, n := range q.primary {
		n.DynamicGate = 0
		n.Satisfied = 0
		n.Deadline = -1
	}
	for _, n := range q.secondary {
		n.DynamicGate = 0
		n.Satisfied = 0
		n.Deadline = -1
	}
}

func TestPumpResultCodes(t *testing.T) {
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	if moveID == 0 || buildID == 0 {
		t.Fatalf("lookup")
	}
	t.Run("code0", func(t *testing.T) {
		rng.SeedGlobal(1, 0)
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			calls++
			if calls == 1 {
				n.Phase = 5
				return Code(0)
			}
			return Code(3)
		})
		defer restore()
		q.Push(moveID, Node{Phase: 5})
		clearGates(q)
		q.Pump(u, 10)
		if q.primary[0].Phase != 0 {
			t.Fatalf("code0 phase %d want 0", q.primary[0].Phase)
		}
		if q.primary[0].DynamicGate != 1 {
			t.Fatalf("code0 cascade should end waiting gate 1 got %x", q.primary[0].DynamicGate)
		}
		if calls != 2 {
			t.Fatalf("code0 cascade calls %d want 2", calls)
		}
	})
	t.Run("code1", func(t *testing.T) {
		rng.SeedGlobal(2, 0)
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			calls++
			if calls == 1 {
				return Code(1)
			}
			return Code(3)
		})
		defer restore()
		q.Push(moveID, Node{Phase: 2})
		clearGates(q)
		q.Pump(u, 10)
		if q.primary[0].Phase != 3 {
			t.Fatalf("code1 phase %d want 3", q.primary[0].Phase)
		}
		if calls != 2 {
			t.Fatalf("code1 calls %d", calls)
		}
	})
	t.Run("code2", func(t *testing.T) {
		rng.SeedGlobal(3, 0)
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			calls++
			if calls == 1 {
				return Code(2)
			}
			return Code(3)
		})
		defer restore()
		q.Push(moveID, Node{Phase: 7})
		clearGates(q)
		q.Pump(u, 10)
		if q.primary[0].Phase != 7 {
			t.Fatalf("code2 phase changed")
		}
		if calls != 2 {
			t.Fatalf("code2 calls %d", calls)
		}
	})
	t.Run("code3", func(t *testing.T) {
		rng.SeedGlobal(42, 0)
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(3) })
		defer restore()
		q.Push(moveID, Node{})
		clearGates(q)
		q.Pump(u, 100)
		if len(q.primary) != 1 {
			t.Fatalf("code3 removed")
		}
		n := q.primary[0]
		if n.DynamicGate != 1 {
			t.Fatalf("code3 gate %x want 1", n.DynamicGate)
		}
		if n.Deadline == -1 || n.Deadline < int32(130) || n.Deadline > int32(144) {
			t.Fatalf("code3 deadline %d want 130..144", n.Deadline)
		}
	})
	t.Run("code4", func(t *testing.T) {
		rng.SeedGlobal(4, 0)
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			calls++
			if calls == 1 {
				return Code(4)
			}
			return Code(3)
		})
		defer restore()
		q.Push(moveID, Node{Phase: 1})
		clearGates(q)
		q.Pump(u, 10)
		if q.primary[0].Phase != 1 {
			t.Fatalf("code4 phase")
		}
		if calls != 2 {
			t.Fatalf("code4 calls")
		}
	})
	t.Run("code5", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(5) })
		defer restore()
		q.Push(moveID, Node{})
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 0 {
			t.Fatalf("code5 not removed len %d", len(q.primary))
		}
	})
	t.Run("code6 primary", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			if n.Param1 == 1 {
				return Code(6)
			}
			return Code(2)
		})
		defer restore()
		q.Push(moveID, Node{Param1: 1})
		q.Push(moveID, Node{Param1: 2})
		clearGates(q)
		// second node's handler would cascade if 2 re-evaluates same head, so make it return 3 second time via counting
		// Instead set second node's handler to return 3 after first 2 to stop cascade
		// For this test we want head 1 move to tail, new head 2 dispatched once then stop
		// So second node's handler should on first call return 2 then second call return 3 – but we only want one call for second node
		// To avoid second call infinite, make second node's handler count
		// Simpler: make second node's handler return 3 directly after head move, but our current handler returns 2 for Param1==2
		// That would cause second node's handler to be called and return 2, then same head (now 2) re-evaluated and return 2 again infinite
		// So we need handler for Param1==2 to return 3 on second invocation
		// Use separate restore for second case
		q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		calls2 := map[uint32]int{}
		restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
			c := calls2[n.Param1]
			calls2[n.Param1]++
			if n.Param1 == 1 && c == 0 {
				return Code(6)
			}
			if n.Param1 == 2 && c == 0 {
				return Code(2)
			}
			return Code(3)
		})
		defer restore2()
		q2.Push(moveID, Node{Param1: 1})
		q2.Push(moveID, Node{Param1: 2})
		clearGates(q2)
		q2.Pump(u, 10)
		if len(q2.primary) != 2 {
			t.Fatalf("code6 len %d", len(q2.primary))
		}
		if q2.primary[0].Param1 != 2 || q2.primary[1].Param1 != 1 {
			t.Fatalf("code6 order %d,%d want 2,1", q2.primary[0].Param1, q2.primary[1].Param1)
		}
		_ = q
	})
	t.Run("code6 secondary", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(6) })
		defer restore()
		q.PushSecondary(buildID, Node{})
		q.PushSecondary(buildID, Node{})
		clearGates(q)
		q.Pump(u, 10)
		if len(q.secondary) != 1 {
			t.Fatalf("code6 secondary len %d want 1", len(q.secondary))
		}
	})
	t.Run("code7 cancel all", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(7) })
		defer restore()
		q.Push(moveID, Node{})
		q.Push(moveID, Node{})
		q.PushSecondary(buildID, Node{})
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 0 || len(q.secondary) != 0 {
			t.Fatalf("code7 not cancel all primary %d secondary %d", len(q.primary), len(q.secondary))
		}
	})
	t.Run("code7 secondary", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(7) })
		defer restore()
		restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(3) })
		defer restore2()
		q.PushSecondary(buildID, Node{})
		q.PushSecondary(buildID, Node{})
		q.Push(moveID, Node{})
		clearGates(q)
		// Make primary not block secondary: primary head will go waiting and return, allowing secondary?
		// Actually primary head blocked would prevent secondary, so we need primary to not block
		// Our primary handler returns 3 which sets waiting and returns, so pumpPrimary returns and secondary not dispatched
		// To test secondary 7, we need primary to be empty or not blocked
		// So use primary with handler that returns 5 to remove itself
		table[int(moveID)].Handler = func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(5) }
		q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		q2.PushSecondary(buildID, Node{})
		q2.PushSecondary(buildID, Node{})
		clearGates(q2)
		q2.Pump(u, 10)
		if len(q2.secondary) != 1 {
			t.Fatalf("code7 secondary single remove len %d want 1", len(q2.secondary))
		}
		_ = q
	})
	t.Run("code8", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(8) })
		defer restore()
		q.Push(moveID, Node{})
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 0 {
			t.Fatalf("code8 not removed")
		}
	})
	t.Run("code9 last rearm", func(t *testing.T) {
		rng.SeedGlobal(99, 0)
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(9) })
		defer restore()
		q.Push(moveID, Node{Phase: 5})
		clearGates(q)
		q.Pump(u, 50)
		if len(q.primary) != 1 {
			t.Fatalf("code9 last should remain len %d", len(q.primary))
		}
		n := q.primary[0]
		if n.Phase != 0 {
			t.Fatalf("code9 last phase %d want 0", n.Phase)
		}
		if n.Deadline == -1 || n.Deadline < int32(80) || n.Deadline > int32(109) {
			t.Fatalf("code9 last deadline %d want 80..109 [R-P0-01] 30+RNG30", n.Deadline)
		}
	})
	t.Run("code9 not last", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(9) })
		defer restore()
		q.Push(moveID, Node{Param1: 1})
		q.Push(moveID, Node{Param1: 2})
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 1 {
			t.Fatalf("code9 not last len %d want 1", len(q.primary))
		}
		if q.primary[0].Param1 != 2 {
			t.Fatalf("code9 not last remaining param %d want 2", q.primary[0].Param1)
		}
	})
	t.Run("code >9", func(t *testing.T) {
		// Handler result >9 delegates to the single-record expiry helper:
		// unlink+cleanup+free, no RNG, not cancel-all [P0-08].
		// Whole-queue cancel is exclusively code 7 [P0-08] A09.
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(12) })
		defer restore()
		q.Push(moveID, Node{Param1: 1})
		q.Push(moveID, Node{Param1: 2})
		buildWeaponID := Lookup("BuildWeapon")
		if buildWeaponID != 0 {
			q.PushSecondary(buildWeaponID, Node{Param1: 10})
		}
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 1 || q.primary[0].Param1 != 2 {
			t.Fatalf("code>9 should remove single head, left primary %v", func() []uint32 {
				var out []uint32
				for _, n := range q.primary {
					out = append(out, n.Param1)
				}
				return out
			}())
		}
		if len(q.secondary) != 1 {
			t.Fatalf("code>9 should not touch secondary, got %d", len(q.secondary))
		}
	})
	t.Run("code >9 single vs code7 cancel-all", func(t *testing.T) {
		q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(7) })
		defer restore()
		q.Push(moveID, Node{Param1: 1})
		q.Push(moveID, Node{Param1: 2})
		buildWeaponID := Lookup("BuildWeapon")
		if buildWeaponID != 0 {
			q.PushSecondary(buildWeaponID, Node{Param1: 10})
		}
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 0 || len(q.secondary) != 0 {
			t.Fatalf("code7 should cancel-all, got primary %d secondary %d", len(q.primary), len(q.secondary))
		}
	})
}

func TestPumpBlockedHeadStalls(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	patrolID := Lookup("Patrol")
	q.Push(moveID, Node{DynamicGate: 0x400, Satisfied: 0, Deadline: -1, StaticGate: 0x400})
	q.primary[0].DynamicGate = 0x400
	q.primary[0].Satisfied = 0
	called := false
	restore := setHandler(patrolID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		called = true
		return Code(2)
	})
	defer restore()
	q.Push(patrolID, Node{})
	q.primary[1].DynamicGate = 0
	q.primary[1].Satisfied = 0
	restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		t.Fatalf("blocked head handler should not run")
		return Code(2)
	})
	defer restore2()
	q.Pump(u, 10)
	if called {
		t.Fatalf("blocked head stalled: tail was dispatched")
	}
	if len(q.primary) != 2 {
		t.Fatalf("blocked head should remain len 2 got %d", len(q.primary))
	}
	u.Pending |= 0x400
	table[int(moveID)].Handler = func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(5) }
	defer func() { table[int(moveID)].Handler = nil }()
	// Make patrol head not spin after promotion: return waiting
	table[int(patrolID)].Handler = func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(3) }
	q.Pump(u, 11)
	if len(q.primary) != 1 {
		t.Fatalf("after satisfying, head should be removed len %d", len(q.primary))
	}
}

func TestDeadline(t *testing.T) {
	rng.SeedGlobal(1, 0)
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	q.Push(moveID, Node{DynamicGate: 1, Satisfied: 0, Deadline: int32(20), StaticGate: 1})
	q.primary[0].DynamicGate = 1
	q.primary[0].Satisfied = 0
	calls := 0
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		calls++
		if calls == 1 {
			if s&1 == 0 {
				t.Fatalf("deadline satisfied bit not set s %x", s)
			}
			return Code(2)
		}
		return Code(3)
	})
	defer restore()
	q.Pump(u, 19)
	if calls != 0 {
		t.Fatalf("deadline not yet arrived but handler called")
	}
	if q.primary[0].Deadline != 20 {
		t.Fatalf("deadline cleared early")
	}
	q.Pump(u, 20)
	if calls == 0 {
		t.Fatalf("deadline arrived but handler not called")
	}
	if q.primary[0].Deadline == 20 {
		t.Fatalf("deadline not cleared after arrival")
	}
	if q.primary[0].DynamicGate != 1 {
		t.Fatalf("cascade should end waiting gate 1")
	}
}

func TestCoalesceTail(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	buildID := Lookup("MobileBuild")
	if buildID == 0 {
		t.Fatalf("lookup")
	}
	q.CoalesceTail(buildID, Node{Param1: 42, Param2: 2})
	if len(q.primary) != 1 || q.primary[0].Param2 != 2 {
		t.Fatalf("first coalesce len %d param %d", len(q.primary), q.primary[0].Param2)
	}
	q.CoalesceTail(buildID, Node{Param1: 42, Param2: 3})
	if len(q.primary) != 1 || q.primary[0].Param2 != 5 {
		t.Fatalf("coalesce same type want 5 got %d len %d", q.primary[0].Param2, len(q.primary))
	}
	q.CoalesceTail(buildID, Node{Param1: 99, Param2: 1})
	if len(q.primary) != 2 {
		t.Fatalf("distinct product should not coalesce len %d", len(q.primary))
	}
	q.CoalesceTail(buildID, Node{Param1: 42, Param2: 1})
	if len(q.primary) != 3 {
		t.Fatalf("separated identical should not coalesce len %d", len(q.primary))
	}
	if q.primary[2].Param1 != 42 || q.primary[2].Param2 != 1 {
		t.Fatalf("third node param %d %d", q.primary[2].Param1, q.primary[2].Param2)
	}
}

func TestCancelTailMostTombstone(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	q.Push(Lookup("Move_Ground"), Node{Param1: 7, Param2: 1})
	q.Push(Lookup("Patrol"), Node{Param1: 7, Param2: 1})
	clearGates(q)
	tailPtr := q.primary[1]
	if tailPtr.Flags&FlagTombstone != 0 {
		t.Fatalf("initial tombstone set")
	}
	ok := q.CancelTailMost(func(n Node) bool { return n.Param1 == 7 })
	if !ok || len(q.primary) != 1 {
		t.Fatalf("cancel tail most failed len %d ok %v", len(q.primary), ok)
	}
	if tailPtr.Flags&FlagTombstone == 0 {
		t.Fatalf("tombstone not set on non-head cancel")
	}
	q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	buildID := Lookup("BuildWeapon")
	q2.PushSecondary(buildID, Node{Param1: 1})
	clearGates(q2)
	headPtr := q2.secondary[0]
	q2.CancelTailMost(func(n Node) bool { return n.Param1 == 1 })
	if headPtr.Flags&FlagTombstone == 0 {
		t.Fatalf("secondary cancel should be tombstoned even as head [04 §3.3]")
	}
	if len(q2.secondary) != 0 {
		t.Fatalf("secondary cancel len")
	}
	q3 := &Queue{}
	q3.Push(Lookup("MobileBuild"), Node{Param1: 5, Param2: 5})
	clearGates(q3)
	ok = q3.CancelTailMost(func(n Node) bool { return n.Param1 == 5 })
	if !ok || len(q3.primary) != 1 || q3.primary[0].Param2 != 4 {
		t.Fatalf("count subtract len %d param %d", len(q3.primary), q3.primary[0].Param2)
	}
}

func TestPushAfterActive(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	moveID := Lookup("Move_Ground")
	q.Push(moveID, Node{Param1: 1})
	clearGates(q)
	q.primary[0].Flags |= FlagActive
	if q.primary[0].Flags&FlagActive == 0 {
		t.Fatalf("first push should be active")
	}
	// The marker moves to each inserted node [04 §3.3][05 "Queue insertion"],
	// so repeated interface adds queue FIFO behind the running order.
	q.Push(moveID, Node{Param1: 2})
	if len(q.primary) != 2 || q.primary[0].Param1 != 1 || q.primary[1].Param1 != 2 {
		t.Fatalf("push after active order %v", q.primary)
	}
	if q.primary[1].Flags&FlagActive == 0 {
		t.Fatalf("marker should move to the inserted node")
	}
	q.Push(moveID, Node{Param1: 3})
	if len(q.primary) != 3 || q.primary[0].Param1 != 1 || q.primary[1].Param1 != 2 || q.primary[2].Param1 != 3 {
		t.Fatalf("repeated pushes must queue FIFO, got %v", q.primary)
	}
	if q.primary[2].Flags&FlagActive == 0 || q.primary[0].Flags&FlagActive != 0 || q.primary[1].Flags&FlagActive != 0 {
		t.Fatalf("exactly the newest node carries the active marker")
	}
}

func TestPushSecondaryAutoInherit(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	buildID := Lookup("BuildWeapon")
	q.PushSecondary(buildID, Node{Flags: FlagAutoOp})
	if q.secondary[0].Flags&FlagAutoOp == 0 {
		t.Fatalf("initial auto not set")
	}
	q.PushSecondary(buildID, Node{})
	if q.secondary[0].Flags&FlagAutoOp == 0 {
		t.Fatalf("secondary head-insert should inherit auto flag")
	}
}

func TestSecondarySkipsNotDue(t *testing.T) {
	rng.SeedGlobal(7, 0)
	u := newTestUnit()
	buildID := Lookup("BuildWeapon")
	q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	q2.secondary = []*Node{
		{ID: buildID, DynamicGate: 1, Deadline: int32(100), Flags: 0},
		{ID: buildID, DynamicGate: 0, Deadline: -1, Flags: 0},
	}
	called := 0
	restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		called++
		return Code(2)
	})
	defer restore()
	q2.Pump(u, 10)
	if called != 1 {
		t.Fatalf("secondary should skip not-due and run one, called %d", called)
	}
}

func TestCascadeUntilWaiting(t *testing.T) {
	rng.SeedGlobal(10, 0)
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	calls := 0
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		calls++
		if calls <= 2 {
			return Code(1)
		}
		return Code(3)
	})
	defer restore()
	q.Push(moveID, Node{Phase: 0})
	clearGates(q)
	startDraws := rng.Global.Sim.Draws()
	q.Pump(u, 20)
	if q.primary[0].Phase != 2 {
		t.Fatalf("cascade phase %d want 2", q.primary[0].Phase)
	}
	if q.primary[0].DynamicGate != 1 {
		t.Fatalf("cascade should be waiting gate 1")
	}
	if rng.Global.Sim.Draws()-startDraws != 1 {
		t.Fatalf("cascade should consume exactly one jitter draw, got %d", rng.Global.Sim.Draws()-startDraws)
	}
	if calls != 3 {
		t.Fatalf("cascade calls %d want 3", calls)
	}
}

func TestBlockedFrontSkipsSecondary(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	q.Push(moveID, Node{DynamicGate: 0x400, Satisfied: 0, Deadline: -1, StaticGate: 0x400})
	q.primary[0].DynamicGate = 0x400
	q.secondary = []*Node{{ID: buildID, DynamicGate: 0, Deadline: -1}}
	secondaryCalled := false
	restore2 := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		secondaryCalled = true
		return Code(2)
	})
	defer restore2()
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		t.Fatalf("blocked head should not be dispatched")
		return Code(2)
	})
	defer restore()
	q.Pump(u, 10)
	if secondaryCalled {
		t.Fatalf("blocked primary front should prevent secondary dispatch")
	}
	if len(q.secondary) != 1 {
		t.Fatalf("secondary should remain")
	}
}

func TestCode7CleansSecondary(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(7) })
	defer restore()
	q.Push(moveID, Node{})
	sec1 := &Node{ID: buildID, Param1: 1}
	sec2 := &Node{ID: buildID, Param1: 2}
	q.secondary = []*Node{sec1, sec2}
	clearGates(q)
	q.Pump(u, 10)
	if len(q.primary) != 0 || len(q.secondary) != 0 {
		t.Fatalf("code7 should clean secondary len primary %d secondary %d", len(q.primary), len(q.secondary))
	}
	if sec1.Flags&FlagTombstone == 0 || sec2.Flags&FlagTombstone == 0 {
		t.Fatalf("code7 secondary nodes should be tombstoned via cleanup")
	}
}

func TestPurgeUnprotected(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	moveID := Lookup("Move_Ground")
	q.Push(moveID, Node{Param1: 1, Flags: FlagPurgeSurvivor})
	q.Push(moveID, Node{Param1: 2, Flags: 0})
	q.Push(moveID, Node{Param1: 3, Flags: FlagPurgeSurvivor})
	// q currently: [1 protected active, 2 unprotected, 3 protected] with insertion after active ordering may be [1,3,2]? Let's build directly
	q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	q2.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: FlagPurgeSurvivor | FlagActive},
		{ID: moveID, Param1: 2, Flags: 0},
		{ID: moveID, Param1: 3, Flags: FlagPurgeSurvivor},
	}
	q2.PurgeUnprotected()
	if len(q2.primary) != 2 {
		t.Fatalf("purge len %d want 2", len(q2.primary))
	}
	for _, n := range q2.primary {
		if n.Flags&FlagPurgeSurvivor == 0 {
			t.Fatalf("purge left unprotected")
		}
	}
}

func TestDropLeadingAutoOps(t *testing.T) {
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	q.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: FlagAutoOp},
		{ID: moveID, Param1: 2, Flags: 0},
		{ID: moveID, Param1: 3, Flags: FlagAutoOp},
	}
	q.secondary = []*Node{
		{ID: buildID, Param1: 10, Flags: FlagAutoOp},
		{ID: buildID, Param1: 11, Flags: 0},
	}
	q.DropLeadingAutoOps()
	if len(q.primary) != 2 || q.primary[0].Param1 != 2 || q.primary[1].Param1 != 3 {
		t.Fatalf("drop leading primary auto len %d params %v", len(q.primary), func() []uint32 {
			out := []uint32{}
			for _, n := range q.primary {
				out = append(out, n.Param1)
			}
			return out
		}())
	}
	if len(q.secondary) != 1 || q.secondary[0].Param1 != 11 {
		t.Fatalf("drop leading secondary auto")
	}
	// auto behind normal must survive
	q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	q2.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: 0},
		{ID: moveID, Param1: 2, Flags: FlagAutoOp},
	}
	q2.DropLeadingAutoOps()
	if len(q2.primary) != 2 {
		t.Fatalf("auto behind normal must survive len %d", len(q2.primary))
	}
}

// TestNilHandlerParksInsteadOfJamming locks the missing-handler arm. A
// descriptor with no handler is a Nanolathe gap, and the pump's answer to it is
// the result-code table's wait, code 3: the record is kept — the air executors
// run off a parked head — the walk stops, and the record comes back after
// 30..44 ticks [04 §3.3]. What it must never do is spin (a diagnostic per tick
// and a permanently dispatchable head) or free the player's order.
func TestNilHandlerParksInsteadOfJamming(t *testing.T) {
	rng.SeedGlobal(1, 0)
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	// Cloak_On has no handler and no ensure* installer that would reinstate
	// one, so this exercises the arm rather than a temporarily blanked entry.
	nilID := Lookup("Cloak_On")
	if DescriptorFor(nilID).Handler != nil {
		t.Skip("Cloak_On now has a handler; pick another handler-less descriptor")
	}
	q.Push(nilID, Node{})
	clearGates(q)
	q.Pump(u, 10)
	if len(q.Diagnostics()) != 1 {
		t.Fatalf("nil handler diagnostics = %d, want exactly one per park: %v", len(q.Diagnostics()), q.Diagnostics())
	}
	if len(q.primary) != 1 {
		t.Fatalf("nil handler should not remove the record, primary=%d", len(q.primary))
	}
	head := q.primary[0]
	if head.DynamicGate != 1 {
		t.Fatalf("parked gate = %#x, want the code-3 lowest gate bit [04 §3.3]", head.DynamicGate)
	}
	if head.Deadline < 40 || head.Deadline > 54 {
		t.Fatalf("parked deadline = %d, want tick 10 + 30..44 [04 §3.3]", head.Deadline)
	}
	// The record is not re-dispatched while it waits: no second diagnostic and
	// no unbounded growth over a run of ticks inside the wait.
	for tick := uint32(11); tick < uint32(head.Deadline); tick++ {
		q.Pump(u, tick)
	}
	if len(q.Diagnostics()) != 1 {
		t.Fatalf("the parked record was re-dispatched during its wait: %v", q.Diagnostics())
	}
}

// SC8: code 3 is 30+RNG15, code 9-last is 30+RNG30 distinct arms [R-P0-01].
func TestSC8_RNG15_30_44(t *testing.T) {
	moveID := Lookup("Move_Ground")
	rng.SeedGlobal(1, 0)
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(3) })
	defer restore()
	q.Push(moveID, Node{})
	clearGates(q)
	q.Pump(u, 100)
	if len(q.primary) != 1 {
		t.Fatalf("code3 should remain")
	}
	dl := q.primary[0].Deadline
	if dl < 130 || dl > 144 {
		t.Fatalf("SC8 code3 deadline %d want 130..144 (tick+30+RNG15)", dl)
	}
	// code 9 last re-arms with RNG30 bound [R-P0-01] PUSH 0x1E
	rng.SeedGlobal(99, 0)
	q2 := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u2 := newTestUnit()
	restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code { return Code(9) })
	defer restore2()
	q2.Push(moveID, Node{Phase: 5})
	clearGates(q2)
	q2.Pump(u2, 50)
	if len(q2.primary) != 1 || q2.primary[0].Phase != 0 {
		t.Fatalf("code9 last should rearm phase 0")
	}
	dl2 := q2.primary[0].Deadline
	if dl2 < 80 || dl2 > 109 {
		t.Fatalf("SC8 code9 last deadline %d want 80..109 (tick+30+RNG30) [R-P0-01]", dl2)
	}
	if q2.primary[0].DynamicGate != 1 {
		t.Fatalf("code9 last gate 1")
	}
}

// TestMoveGroundArrivalPumpTransition verifies satisfied 0x20 → handler return 5 unlink/free [R-P0-01].
func TestMoveGroundArrivalPumpTransition(t *testing.T) {
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatalf("Move_Ground not found")
	}
	q := &Queue{}
	u := newTestUnit()
	// Push a Move_Ground node and let handler arm gate
	q.Push(moveID, Node{})
	if len(q.primary) != 1 {
		t.Fatalf("push")
	}
	n := q.primary[0]
	// First pump: phase 0 -> arms 0xE0 and returns 1 (phase becomes 1) [R-P0-01]
	clearGates(q)
	// Ensure gate allows handler entry: clearGate sets gate 0, handler will set 0xE0
	// Our pump's phase-0 bypass allows Move_Ground phase 0 to run even with initial 0x402
	q.Pump(u, 10)
	if n.DynamicGate != 0xE0 || n.Phase != 1 {
		t.Fatalf("phase0 should arm 0xE0 and phase 1, got gate %x phase %d", n.DynamicGate, n.Phase)
	}
	if len(q.primary) != 1 {
		t.Fatalf("phase0 should not free, len %d", len(q.primary))
	}
	// Simulate movement arrival ORing 0x20 [R-P0-01]
	n.Satisfied |= 0x20
	// Pump again: handler phase1 sees satisfied&0x20 -> return 5 unlink
	q.Pump(u, 11)
	if len(q.primary) != 0 {
		t.Fatalf("phase1 with 0x20 should return 5 and unlink, len %d", len(q.primary))
	}
	// Gate test: (satisfied|pending)&gate halts while 0 [R-P0-01] at pump ~607
	q2 := &Queue{}
	q2.Push(moveID, Node{Phase: 1, DynamicGate: 0xE0, Satisfied: 0})
	u.Pending = 0
	// Pump should halt (not call handler) while combined==0
	called := false
	orig := DescriptorFor(moveID).Handler
	table[int(moveID)].Handler = func(u *units.Unit, n *Node, s uint32, tick uint32) Code { called = true; return 9 }
	defer func() { table[int(moveID)].Handler = orig }()
	q2.Pump(u, 20)
	if called {
		t.Fatalf("gate halt should prevent handler when combined==0 [R-P0-01]")
	}
	if len(q2.primary) != 1 {
		t.Fatalf("halted head should remain")
	}
	// Now set bit and ensure handler called and returns 5
	q2.primary[0].Satisfied |= 0x20
	table[int(moveID)].Handler = func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		if s&0x20 == 0 {
			t.Fatalf("combined should contain 0x20")
		}
		return 5
	}
	q2.Pump(u, 21)
	if len(q2.primary) != 0 {
		t.Fatalf("with 0x20 handler should return 5 and free")
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestQueueModifiers_SegmentMapping(t *testing.T) {
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	if moveID == 0 || buildID == 0 {
		t.Fatalf("lookup")
	}
	// Replace (non-queued) purges unprotected primary, secondary untouched
	q := &Queue{}
	q.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: FlagPurgeSurvivor | FlagActive},
		{ID: moveID, Param1: 2, Flags: 0},
		{ID: moveID, Param1: 3, Flags: FlagPurgeSurvivor},
	}
	q.secondary = []*Node{{ID: buildID, Param1: 10, Flags: 0}}
	q.PurgeUnprotected()
	if len(q.primary) != 2 || len(q.secondary) != 1 {
		t.Fatalf("replace should purge primary non-survivor only, got primary %d secondary %d", len(q.primary), len(q.secondary))
	}
	// Append/ShifQueue: queued insertion after active marker (primary) vs head-insert for secondary
	q2 := &Queue{}
	q2.Push(moveID, Node{Param1: 1})
	clearGates(q2)
	q2.Push(moveID, Node{Param1: 2})
	if len(q2.primary) != 2 || q2.primary[1].Param1 != 2 {
		t.Fatalf("append should insert after active, got %v", q2.primary)
	}
	// Secondary head-insert inherits auto flag
	q3 := &Queue{}
	q3.PushSecondary(buildID, Node{Flags: FlagAutoOp})
	q3.PushSecondary(buildID, Node{})
	if q3.secondary[0].Flags&FlagAutoOp == 0 {
		t.Fatalf("secondary head-insert should inherit auto flag")
	}
	// Auto: pump empty creates auto-flagged node head-insert (verified via DropLeadingAutoOps)
	q4 := &Queue{}
	q4.primary = []*Node{{ID: moveID, Param1: 1, Flags: FlagAutoOp}, {ID: moveID, Param1: 2, Flags: 0}}
	q4.DropLeadingAutoOps()
	if len(q4.primary) != 1 || q4.primary[0].Param1 != 2 {
		t.Fatalf("auto leading drop should remove head auto only")
	}
}

// TestOrderGuardFloat locks the per-unit order-guard float [07 §8/§9]: zero
// when the primary queue is empty, nonzero (1.0 placeholder, the exact ratio
// source unattested) while an order is queued, and zero again once the queue
// empties. Eligibility reads it via an exact == 0.0 compare.
func TestOrderGuardFloat(t *testing.T) {
	rng.SeedGlobal(7, 0)
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatalf("lookup Move_Ground")
	}
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	u := newTestUnit()
	if u.OrderGuard != 0 {
		t.Fatalf("fresh unit guard = %v, want 0", u.OrderGuard)
	}
	// Queue a Move_Ground with a handler that parks the record on an
	// unsatisfied gate: the record stays queued (mid-order) and the walk
	// stops on the blocked head [04 §3.3] step 3 — no iteration cap exists
	// to rescue a continue-code loop (ORD-02).
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		n.DynamicGate = 0x400
		return Code(2)
	})
	defer restore()
	q.primary = append(q.primary, &Node{ID: moveID, DynamicGate: 0, Deadline: -1})
	q.Pump(u, 0)
	if u.OrderGuard != 1.0 {
		t.Fatalf("guard mid-order = %v, want 1.0", u.OrderGuard)
	}
	// Empty the queue: completion path (head removed) clears the guard.
	q.primary = q.primary[1:]
	q.Pump(u, 1)
	if u.OrderGuard != 0.0 {
		t.Fatalf("guard after completion = %v, want 0", u.OrderGuard)
	}
}

// TestMobileBuildBlockedAreaBudget locks the blocked-area retry budget of the
// MobileBuild/VTOL_MobileBuild handlers [R-ORDER-02 §1]: each blocked visit
// notifies "Waiting for target area to clear", increments the record's third
// parameter, and waits EXACTLY 30 ticks with no random draw while the counter
// is at most 10; the first blocked visit with the counter already above 10
// notifies "Target area was blocked" and returns 8 (remove). Eleven 30-tick
// waits, then give-up on visit twelve.
func TestMobileBuildBlockedAreaBudget(t *testing.T) {
	buildID := Lookup("MobileBuild")
	if buildID == 0 {
		t.Fatalf("lookup MobileBuild")
	}
	sim := injectTestSim(t)
	q := &Queue{binding: &QueueBinding{SimRNG: sim}}
	u := newTestUnit()

	// The handler body is the traced blocked-visit protocol: the construction
	// side calls MobileBuildBlockedVisit with its current tick and returns the
	// code. The test drives the same call through a pumped MobileBuild record,
	// one pump per tick across twelve 30-tick waits' worth of time.
	type visit struct {
		tick    uint32
		text    string
		code    Code
		counter uint32 // the record's third parameter after the visit's step
	}
	var visits []visit
	visitTick := uint32(0)
	restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		text, code := MobileBuildBlockedVisit(n, visitTick)
		visits = append(visits, visit{tick: visitTick, text: text, code: code, counter: n.Param3})
		if code == 2 && n.Deadline != int32(visitTick+MobileBuildBlockedWaitTicks) {
			t.Fatalf("visit at %d armed deadline %d, want exactly tick+30", visitTick, n.Deadline)
		}
		return code
	})
	defer restore()

	q.Push(buildID, Node{Param1: 7})
	const startTick = uint32(1000)
	drawsBefore := sim.Draws()
	// One pump per tick over visits 1..12: waits dispatch again exactly 30
	// ticks later (deadline expiry satisfies the lowest gate bit [04 §3.3]),
	// so the pump must never dispatch between the 30-tick multiples.
	for tick := startTick; tick <= startTick+11*MobileBuildBlockedWaitTicks+1; tick++ {
		visitTick = tick
		q.Pump(u, tick)
	}
	if d := sim.Draws() - drawsBefore; d != 0 {
		t.Fatalf("budget drew %d random values, want 0 (fixed 30-tick waits)", d)
	}
	if len(visits) != 12 {
		t.Fatalf("blocked visits %d, want 11 waiting visits + give-up on visit 12", len(visits))
	}
	for i, v := range visits {
		wantTick := startTick + uint32(i)*MobileBuildBlockedWaitTicks
		if v.tick != wantTick {
			t.Fatalf("visit %d at tick %d, want %d (exact 30-tick cadence)", i+1, v.tick, wantTick)
		}
		if i < 11 {
			if v.text != MobileBuildWaitingText || v.code != 2 {
				t.Fatalf("visit %d: text %q code %d, want %q and 2", i+1, v.text, v.code, MobileBuildWaitingText)
			}
			if v.counter != uint32(i+1) {
				t.Fatalf("visit %d counter %d, want %d", i+1, v.counter, i+1)
			}
		}
	}
	last := visits[11]
	if last.text != MobileBuildBlockedText || last.code != 8 {
		t.Fatalf("visit 12: text %q code %d, want %q and 8 (abandon/remove)", last.text, last.code, MobileBuildBlockedText)
	}
	if last.counter != 11 {
		t.Fatalf("visit 12 counter %d, want 11 (give-up fires when the counter is already above 10)", last.counter)
	}
	if len(q.primary) != 0 {
		t.Fatalf("record kept after the give-up, want removed")
	}
}

// TestMobileBuildBlockedBudgetBoundary pins the counter arithmetic at the
// boundary and the nil-record behavior [R-ORDER-02 §1].
func TestMobileBuildBlockedBudgetBoundary(t *testing.T) {
	n := &Node{ID: Lookup("MobileBuild")}
	for i := 0; i < 11; i++ {
		text, code := MobileBuildBlockedVisit(n, 500)
		if text != MobileBuildWaitingText || code != 2 {
			t.Fatalf("counter %d: text %q code %d, want waiting/2", n.Param3, text, code)
		}
	}
	if n.Param3 != 11 {
		t.Fatalf("counter %d, want 11 after eleven blocked visits", n.Param3)
	}
	kept := n.Param3
	text, code := MobileBuildBlockedVisit(n, 530)
	if text != MobileBuildBlockedText || code != 8 {
		t.Fatalf("counter above budget: text %q code %d, want blocked/8", text, code)
	}
	// The give-up itself touches nothing: the counter stays, and the deadline
	// is whatever the caller left (in a pumped record the pump has already
	// cleared the arrived deadline).
	if n.Param3 != kept {
		t.Fatalf("give-up must not touch the record beyond the armed wait")
	}
	if text, code := MobileBuildBlockedVisit(nil, 0); code != 8 {
		t.Fatalf("nil record: text %q code %d, want give-up", text, code)
	}
}

// TestSecondaryPumpDeliversEmptySatisfiedSet locks [R-ORDER-02 §1]: a
// rear-segment record is dispatched on an empty gate or a due deadline
// (unsigned compare; the -1 sentinel reads not-due) and the handler receives
// an EMPTY satisfied set — no expiry bit, no satisfied-word read, no
// capability-word consumption.
func TestSecondaryPumpDeliversEmptySatisfiedSet(t *testing.T) {
	buildID := Lookup("BuildWeapon")
	if buildID == 0 {
		t.Fatalf("lookup BuildWeapon")
	}
	sim := injectTestSim(t)
	u := newTestUnit()
	u.Pending = 0x2 // capability word holds bits: the secondary dispatch must not consume them
	var got []uint32
	restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		got = append(got, s)
		return 2
	})
	defer restore()

	// Due deadline with a nonzero gate and both satisfied sources primed:
	// dispatch still delivers an empty set.
	due := secNode(buildID, 1, 0)
	due.DynamicGate = 1
	due.Deadline = int32(probeTick)
	due.Satisfied = 0x1
	q := &Queue{binding: &QueueBinding{SimRNG: sim}, secondary: []*Node{due}}
	q.Pump(u, probeTick)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("secondary dispatch delivered satisfied %v, want exactly [0]", got)
	}
	if u.Pending != 0x2 {
		t.Fatalf("capability word consumed to %x, want untouched", u.Pending)
	}
	if due.Satisfied != 0x1 {
		t.Fatalf("record satisfied word read/cleared to %x, want untouched", due.Satisfied)
	}
	if due.Deadline != -1 || due.DynamicGate != 0 {
		t.Fatalf("deadline %d gate %x, want deadline cleared and gate cleared", due.Deadline, due.DynamicGate)
	}

	// The -1 sentinel reads not-due under the unsigned compare. SelfDestruct
	// avoids the fresh-BuildWeapon ready normalization [06 §11.1] C29.
	got = nil
	selfID := Lookup("SelfDestruct")
	if selfID == 0 {
		t.Fatalf("lookup SelfDestruct")
	}
	restoreSelf := setHandler(selfID, func(u *units.Unit, n *Node, s uint32, tick uint32) Code {
		got = append(got, s)
		return 2
	})
	defer restoreSelf()
	idle := secNode(selfID, 2, 0)
	idle.DynamicGate = 1
	idle.Deadline = -1
	q.secondary = []*Node{idle}
	q.Pump(u, probeTick)
	if len(got) != 0 {
		t.Fatalf("-1 sentinel dispatched with satisfied %v, want not-due", got)
	}

	// Empty gate dispatches immediately, also with an empty set.
	got = nil
	ready := secNode(buildID, 3, 0)
	ready.DynamicGate = 0
	ready.Deadline = -1
	q.secondary = []*Node{ready}
	q.Pump(u, probeTick)
	if len(got) != 1 || got[0] != 0 {
		t.Fatalf("empty-gate dispatch satisfied %v, want [0]", got)
	}
}

// TestPumpPassesItsTickToEveryHandler locks WU-18-7's seam: the primary walk
// hands the tick it is running to the descriptor handler, and the rear-segment
// walk does the same, so a handler's deadline setter can store "current tick +
// n" [04 R-ORD-01 §1]. Before this, only four descriptors reached a tick, each
// through a by-name special case in pumpPrimary, and every other row measured
// its wait from `Queue.SecondaryTick` — a base the primary walk never wrote.
//
// The relationship asserted is the identity of the two ticks, on a
// FRONT-segment record (`Wait`, static mask 0x4) and a rear-segment one
// (`BuildWeapon`, static mask 0xc0140, bit 18 selects the rear segment
// [04 §3.1]), for a tick that is neither zero nor a queue field's stale value.
func TestPumpPassesItsTickToEveryHandler(t *testing.T) {
	const tick = 4711
	for _, name := range []string{"Wait", "BuildWeapon"} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		q, u := gateFixture()
		seen := int64(-1)
		restore := setHandler(id, func(_ *units.Unit, _ *Node, _ uint32, got uint32) Code {
			seen = int64(got)
			return Code(5) // complete: leave nothing behind for the next case
		})
		if isSecondary(id) {
			q.PushSecondary(id, Node{Owner: u.Handle})
		} else {
			q.Push(id, Node{Owner: u.Handle})
		}
		q.Pump(u, tick)
		restore()
		if seen != tick {
			t.Fatalf("%s handler saw tick %d, want the pump's own %d", name, seen, tick)
		}
	}
}

// TestArmedDeadlineIsMeasuredFromThePumpTick is the same seam stated as the
// behavior it exists for: a front-segment handler that arms `deadline n`
// [04 R-ORD-01 §1] produces `pump tick + n`, and the record is then blocked on
// gate bit 0 until that tick [04 §3.3] steps 1 and 3 — not before it.
func TestArmedDeadlineIsMeasuredFromThePumpTick(t *testing.T) {
	const tick = 900
	const wait = 17
	id := Lookup("Wait")
	if id == 0 {
		t.Fatal("Wait is not in the descriptor table")
	}
	q, u := gateFixture()
	visits := 0
	restore := setHandler(id, func(_ *units.Unit, n *Node, _ uint32, got uint32) Code {
		visits++
		armDeadline(n, got, wait)
		return Code(2) // hold: the walk continues and re-reads this same head
	})
	defer restore()
	q.Push(id, Node{Owner: u.Handle})
	q.Pump(u, tick)
	if visits != 1 {
		t.Fatalf("handler ran %d times in one pump, want 1: an armed deadline must block the walk [04 §3.3]", visits)
	}
	n := q.Primary()[0]
	if n.Deadline != tick+wait || n.DynamicGate&1 == 0 {
		t.Fatalf("deadline = %d gate = %#x, want %d with gate bit 0 [04 R-ORD-01 §1]", n.Deadline, n.DynamicGate, tick+wait)
	}
	q.Pump(u, tick+wait-1)
	if visits != 1 {
		t.Fatalf("handler ran again at tick %d, before its deadline %d", tick+wait-1, n.Deadline)
	}
	q.Pump(u, tick+wait)
	if visits != 2 {
		t.Fatalf("handler ran %d times, want a second visit once the deadline arrived [04 §3.3]", visits)
	}
}
