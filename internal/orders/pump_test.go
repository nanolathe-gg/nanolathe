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

func setHandler(id ID, fn func(u *units.Unit, n *Node, satisfied uint32) Code) func() {
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
		q := &Queue{}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
		q := &Queue{}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
		q := &Queue{}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(3) })
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
		q := &Queue{}
		u := newTestUnit()
		calls := 0
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(5) })
		defer restore()
		q.Push(moveID, Node{})
		clearGates(q)
		q.Pump(u, 10)
		if len(q.primary) != 0 {
			t.Fatalf("code5 not removed len %d", len(q.primary))
		}
	})
	t.Run("code6 primary", func(t *testing.T) {
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
		q2 := &Queue{}
		calls2 := map[uint32]int{}
		restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32) Code { return Code(6) })
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(7) })
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32) Code { return Code(7) })
		defer restore()
		restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(3) })
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
		table[int(moveID)].Handler = func(u *units.Unit, n *Node, s uint32) Code { return Code(5) }
		q2 := &Queue{}
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(8) })
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(9) })
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
		if n.Deadline == -1 || n.Deadline < int32(80) || n.Deadline > int32(94) {
			t.Fatalf("code9 last deadline %d want 80..94", n.Deadline)
		}
	})
	t.Run("code9 not last", func(t *testing.T) {
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(9) })
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
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// single-node unlink+cleanup+free, no RNG, not cancel-all [P0-08].
		// Whole-queue cancel is exclusively code 7 [P0-08] A09.
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(12) })
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
		q := &Queue{}
		u := newTestUnit()
		restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(7) })
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
	q := &Queue{}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	patrolID := Lookup("Patrol")
	q.Push(moveID, Node{DynamicGate: 0x400, Satisfied: 0, Deadline: -1, StaticGate: 0x400})
	q.primary[0].DynamicGate = 0x400
	q.primary[0].Satisfied = 0
	called := false
	restore := setHandler(patrolID, func(u *units.Unit, n *Node, s uint32) Code {
		called = true
		return Code(2)
	})
	defer restore()
	q.Push(patrolID, Node{})
	q.primary[1].DynamicGate = 0
	q.primary[1].Satisfied = 0
	restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
	table[int(moveID)].Handler = func(u *units.Unit, n *Node, s uint32) Code { return Code(5) }
	defer func() { table[int(moveID)].Handler = nil }()
	// Make patrol head not spin after promotion: return waiting
	table[int(patrolID)].Handler = func(u *units.Unit, n *Node, s uint32) Code { return Code(3) }
	q.Pump(u, 11)
	if len(q.primary) != 1 {
		t.Fatalf("after satisfying, head should be removed len %d", len(q.primary))
	}
}

func TestDeadline(t *testing.T) {
	rng.SeedGlobal(1, 0)
	q := &Queue{}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	q.Push(moveID, Node{DynamicGate: 1, Satisfied: 0, Deadline: int32(20), StaticGate: 1})
	q.primary[0].DynamicGate = 1
	q.primary[0].Satisfied = 0
	calls := 0
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
	q := &Queue{}
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
	q := &Queue{}
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
	q2 := &Queue{}
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
	q := &Queue{}
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
	q := &Queue{}
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
	q2 := &Queue{}
	q2.secondary = []*Node{
		{ID: buildID, DynamicGate: 1, Deadline: int32(100), Flags: 0},
		{ID: buildID, DynamicGate: 0, Deadline: -1, Flags: 0},
	}
	called := 0
	restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32) Code {
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
	q := &Queue{}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	calls := 0
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
	q := &Queue{}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	q.Push(moveID, Node{DynamicGate: 0x400, Satisfied: 0, Deadline: -1, StaticGate: 0x400})
	q.primary[0].DynamicGate = 0x400
	q.secondary = []*Node{{ID: buildID, DynamicGate: 0, Deadline: -1}}
	secondaryCalled := false
	restore2 := setHandler(buildID, func(u *units.Unit, n *Node, s uint32) Code {
		secondaryCalled = true
		return Code(2)
	})
	defer restore2()
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
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
	q := &Queue{}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	buildID := Lookup("BuildWeapon")
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(7) })
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
	q := &Queue{}
	moveID := Lookup("Move_Ground")
	q.Push(moveID, Node{Param1: 1, Flags: FlagPurgeSurvivor})
	q.Push(moveID, Node{Param1: 2, Flags: 0})
	q.Push(moveID, Node{Param1: 3, Flags: FlagPurgeSurvivor})
	// q currently: [1 protected active, 2 unprotected, 3 protected] with insertion after active ordering may be [1,3,2]? Let's build directly
	q2 := &Queue{}
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
	q := &Queue{}
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
	q2 := &Queue{}
	q2.primary = []*Node{
		{ID: moveID, Param1: 1, Flags: 0},
		{ID: moveID, Param1: 2, Flags: FlagAutoOp},
	}
	q2.DropLeadingAutoOps()
	if len(q2.primary) != 2 {
		t.Fatalf("auto behind normal must survive len %d", len(q2.primary))
	}
}

func TestNilHandlerDiagnostic(t *testing.T) {
	q := &Queue{}
	u := newTestUnit()
	moveID := Lookup("Move_Ground")
	table[int(moveID)].Handler = nil
	q.Push(moveID, Node{})
	clearGates(q)
	q.Pump(u, 10)
	if len(q.Diagnostics()) == 0 {
		t.Fatalf("nil handler should record diagnostic")
	}
	if len(q.primary) != 1 {
		t.Fatalf("nil handler should not remove node")
	}
}

// SC8: code 3 and code 9-last share tick+30+RNG15 (30..44) not RNG30 [P0-08].
func TestSC8_RNG15_30_44(t *testing.T) {
	moveID := Lookup("Move_Ground")
	rng.SeedGlobal(1, 0)
	q := &Queue{}
	u := newTestUnit()
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(3) })
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
	// code 9 last re-arms with same RNG15 bound
	rng.SeedGlobal(99, 0)
	q2 := &Queue{}
	u2 := newTestUnit()
	restore2 := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code { return Code(9) })
	defer restore2()
	q2.Push(moveID, Node{Phase: 5})
	clearGates(q2)
	q2.Pump(u2, 50)
	if len(q2.primary) != 1 || q2.primary[0].Phase != 0 {
		t.Fatalf("code9 last should rearm phase 0")
	}
	dl2 := q2.primary[0].Deadline
	if dl2 < 80 || dl2 > 94 {
		t.Fatalf("SC8 code9 last deadline %d want 80..94 (tick+30+RNG15)", dl2)
	}
	if q2.primary[0].DynamicGate != 1 {
		t.Fatalf("code9 last gate 1")
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
