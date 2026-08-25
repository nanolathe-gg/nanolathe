package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestPumpUnit_Isolation(t *testing.T) {
	rng.SeedGlobal(100, 0)
	cat := &content.Catalog{}
	w := units.New(20, cat)
	def := &content.UnitDef{UnitName: "armflea", CanMove: true, MaxDamage: 100}
	hA, _ := w.Create(def, 0, 0, 0, 0)
	hB, _ := w.Create(def, 0, 0, 0, 0)
	uA := w.Unit(hA)
	uB := w.Unit(hB)
	qA := QueueForUnit(uA)
	qB := QueueForUnit(uB)
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatalf("Move_Ground not found")
	}
	// Handlers: each advance phase then wait, so we can detect pumping.
	restore := setHandler(moveID, func(u *units.Unit, n *Node, s uint32) Code {
		// Advance phase once, then wait.
		if n.Phase == 0 {
			return Code(1)
		}
		return Code(3)
	})
	defer restore()
	qA.Push(moveID, Node{Phase: 0, DynamicGate: 0, Deadline: -1, StaticGate: 0})
	qB.Push(moveID, Node{Phase: 0, DynamicGate: 0, Deadline: -1, StaticGate: 0})
	// newNode fills DynamicGate from descriptor if 0, so clear for ready dispatch
	qA.Primary()[0].DynamicGate = 0
	qA.Primary()[0].Deadline = -1
	qA.Primary()[0].Satisfied = 0
	qB.Primary()[0].DynamicGate = 0
	qB.Primary()[0].Deadline = -1
	qB.Primary()[0].Satisfied = 0
	// Ensure both start at phase 0
	if qA.Primary()[0].Phase != 0 || qB.Primary()[0].Phase != 0 {
		t.Fatalf("setup phase")
	}
	pump := &Pump{World: w}
	resA := pump.PumpUnit(hA, 10)
	if resA.Err != nil {
		t.Fatalf("PumpUnit A err %v", resA.Err)
	}
	// A should have advanced to phase 1 (code 1 increments, then next dispatch in same pump would go to 3 wait)
	// Actually handler returns 1 then pump continues to next dispatch in same call: phase 1 then handler returns 3 wait and sets deadline.
	// So after one PumpUnit, phase should be 1 and DynamicGate 1.
	if qA.Primary()[0].Phase != 1 {
		t.Fatalf("A phase %d want 1 after PumpUnit", qA.Primary()[0].Phase)
	}
	// B must remain at phase 0, untouched.
	if qB.Primary()[0].Phase != 0 {
		t.Fatalf("B phase %d want 0 (isolation violated)", qB.Primary()[0].Phase)
	}
	// Pump B now should advance B but not A further.
	resB := pump.PumpUnit(hB, 11)
	if resB.Err != nil {
		t.Fatalf("PumpUnit B err %v", resB.Err)
	}
	if qB.Primary()[0].Phase != 1 {
		t.Fatalf("B phase after its own pump %d want 1", qB.Primary()[0].Phase)
	}
	if qA.Primary()[0].Phase != 1 {
		t.Fatalf("A phase after B pump should stay 1, got %d", qA.Primary()[0].Phase)
	}
	// Also test that pumping non-existent handle returns error without touching others.
	invalid := pool.Handle(9999)
	resInvalid := pump.PumpUnit(invalid, 12)
	if resInvalid.Err == nil {
		t.Fatalf("invalid handle should error")
	}
	if qA.Primary()[0].Phase != 1 || qB.Primary()[0].Phase != 1 {
		t.Fatalf("invalid pump should not affect queues")
	}
}

func TestPumpUnit_HeadBlockingPreserved(t *testing.T) {
	rng.SeedGlobal(101, 0)
	cat := &content.Catalog{}
	w := units.New(20, cat)
	def := &content.UnitDef{UnitName: "test", CanMove: true, MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	q := QueueForUnit(u)
	moveID := Lookup("Move_Ground")
	_ = moveID
	// Blocked head test: first node has gate 0x400 unsatisfied, second node ready.
	// PumpUnit should not dispatch second node because head blocks.
	q.Push(Lookup("Move_Ground"), Node{DynamicGate: 0x400, Satisfied: 0, Deadline: -1, StaticGate: 0x400})
	q.Primary()[0].DynamicGate = 0x400
	q.Primary()[0].Satisfied = 0
	patrolID := Lookup("Patrol")
	called := false
	restore := setHandler(patrolID, func(u *units.Unit, n *Node, s uint32) Code { called = true; return Code(2) })
	defer restore()
	q.Push(patrolID, Node{})
	q.Primary()[1].DynamicGate = 0
	// Pump via PumpUnit should stall on head, not call patrol.
	pump := &Pump{World: w}
	pump.PumpUnit(h, 10)
	if called {
		t.Fatalf("head-blocking violated: secondary-equivalent patrol was dispatched while head blocked")
	}
	if len(q.Primary()) != 2 {
		t.Fatalf("blocked head should remain len 2")
	}
}

func TestPumpUnit_SecondarySkipNotDue(t *testing.T) {
	rng.SeedGlobal(102, 0)
	cat := &content.Catalog{}
	w := units.New(20, cat)
	def := &content.UnitDef{UnitName: "test", CanMove: true, MaxDamage: 100}
	h, _ := w.Create(def, 0, 0, 0, 0)
	u := w.Unit(h)
	q := QueueForUnit(u)
	buildID := Lookup("BuildWeapon")
	if buildID == 0 {
		t.Skip("BuildWeapon not in table")
	}
	// Ensure primary not blocking: empty primary.
	// Setup secondary with two nodes: first not due, second ready.
	q.Secondary() // ensure queue exists
	// Directly set secondary via exported setter
	q.SetSecondary([]*Node{
		{ID: buildID, DynamicGate: 1, Deadline: int32(100)},
		{ID: buildID, DynamicGate: 0, Deadline: -1},
	})
	called := 0
	restore := setHandler(buildID, func(u *units.Unit, n *Node, s uint32) Code { called++; return Code(2) })
	defer restore()
	pump := &Pump{World: w}
	// Primary empty => secondary can be pumped; first not due should be skipped, second dispatched.
	pump.PumpUnit(h, 10)
	if called != 1 {
		t.Fatalf("secondary skip-not-due: want 1 dispatch, got %d", called)
	}
}
