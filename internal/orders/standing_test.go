package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// standingFixture is one unit with a bound queue carrying a seeded simulation
// stream, so every draw these handlers take comes from a stream the test owns
// and never from a package global [I4].
func standingFixture(def *content.UnitDef) (*Queue, *units.Unit) {
	rng.SeedGlobal(1, 0)
	if def == nil {
		def = &content.UnitDef{}
	}
	u := &units.Unit{
		Handle:    1,
		Def:       def,
		Alive:     true,
		X:         numeric.Fixed(70 << 16),
		Y:         numeric.Fixed(40 << 16),
		Z:         numeric.Fixed(90 << 16),
		Health:    3000,
		MaxHealth: 3000,
	}
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	BindQueue(u, q)
	return q, u
}

// standingRows is the family this unit implements, with the state each row
// needs before its first visit and the outcome that row prescribes for it.
// A fixed slice, so the assertions run in one order (I1).
var standingRows = []struct {
	name      string
	node      Node
	mobile    bool // definition owns a mover (bmcode 1) [04 R-FAC-02 §5]
	building  bool // status-word bit 29 [04 §3.4 "state bit 29 (immobile)"]
	completes bool // the row's terminal code on its first visit is *complete*
}{
	{name: "Standing_MoveOrder", node: Node{Param1: 2}, completes: true},
	{name: "Standing_FireOrder", node: Node{Param1: 1}, completes: true},
	{name: "Cloak_On", completes: true},
	{name: "Cloak_Off", completes: true},
	{name: "Wait", node: Node{Param1: 90}},         // phase 0 arms its own deadline
	{name: "WaitForAttack", node: Node{Target: 7}}, // phase 0 arms gate 0x18
	{name: "Paralyze", node: Node{Param1: 90}},     // phase 0 arms its own deadline
	{name: "Teleport", completes: true},            // single visit
	{name: "Standby", mobile: true},                // phase 0 arms deadline 1
	{name: "Standby_Mine", building: true},         // phase 0 arms deadline 1
}

// TestStandingFamilyDispatchesOnItsFirstVisitAndNeverParks is the phase's own
// contract (PLAN 18 §2 "Test policy"): every order this unit wires reaches its
// handler on the record's first pump visit and takes its row's own exit —
// completion, or a wait the row itself arms — instead of the pump's
// missing-handler park of [04 §3.3] code 3, which is what these ten records did
// before this unit landed.
//
// The park is diagnosable by construction: it records a diagnostic naming the
// order and writes gate bit 0 with a deadline 30 to 44 ticks out. Asserting no
// diagnostic and no unrequested 30..44 park is therefore the whole test, and it
// is a relationship rather than a census of handler internals.
func TestStandingFamilyDispatchesOnItsFirstVisitAndNeverParks(t *testing.T) {
	const tick = 0 // every row's own deadline is at least one tick out from here
	for _, row := range standingRows {
		id := Lookup(row.name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", row.name)
		}
		if DescriptorFor(id).Handler == nil {
			t.Fatalf("%s has no handler after installation [04 §3.1]", row.name)
		}
		q, u := standingFixture(&content.UnitDef{BMCode: row.mobile})
		if row.building {
			u.Flags |= units.BuildingClassStatus
		}
		node := row.node
		node.Owner = u.Handle
		q.Push(id, node)
		q.Pump(u, tick)

		if diags := q.Diagnostics(); len(diags) != 0 {
			t.Fatalf("%s: dispatch recorded %v, want none [04 §3.3]", row.name, diags)
		}
		if row.completes {
			if q.LenPrimary() != 0 {
				t.Fatalf("%s: primary length = %d, want the completed record freed [04 R-ORD-01 §2]", row.name, q.LenPrimary())
			}
			continue
		}
		if q.LenPrimary() != 1 {
			t.Fatalf("%s: primary length = %d, want the waiting record kept", row.name, q.LenPrimary())
		}
		n := q.Primary()[0]
		if n.DynamicGate == 0 {
			t.Fatalf("%s: the record waits on nothing after its first visit", row.name)
		}
		if n.DynamicGate == 1 && n.Deadline >= tick+30 && n.Deadline <= tick+44 {
			t.Fatalf("%s: parked on the missing-handler wait (gate %#x deadline %d) [04 §3.3] code 3", row.name, n.DynamicGate, n.Deadline)
		}
	}
}

// TestStandingOrdersWriteTheUnitsOwnTwoBitFields locks the deposit of
// [04 R-STANCE-01 §2]: the move order writes bits 18-19 and the fire order bits
// 20-21 of the unit's own status word, each masked to two bits, and neither
// touches the other's pair. The folded three-bit selection aggregate the side
// panel stages is a different word and is never written here [04 R-STANCE-01 §1].
func TestStandingOrdersWriteTheUnitsOwnTwoBitFields(t *testing.T) {
	q, u := standingFixture(nil)
	q.Push(Lookup("Standing_MoveOrder"), Node{Owner: u.Handle, Param1: 2})
	q.Pump(u, 0)
	if got := (u.Flags >> 18) & 3; got != 2 {
		t.Fatalf("move stance = %d, want 2 [04 R-STANCE-01 §2]", got)
	}
	if got := (u.Flags >> 20) & 3; got != 0 {
		t.Fatalf("the move order wrote the fire pair: %d", got)
	}

	// The deposit masks to two bits even though the fire handler's extra
	// effect tests the unmasked value.
	q.Push(Lookup("Standing_FireOrder"), Node{Owner: u.Handle, Param1: 6})
	q.Pump(u, 0)
	if got := (u.Flags >> 20) & 3; got != 2 {
		t.Fatalf("fire stance = %d, want 6 masked to 2 [04 R-STANCE-01 §2]", got)
	}
	if got := (u.Flags >> 18) & 3; got != 2 {
		t.Fatalf("the fire order disturbed the move pair: %d", got)
	}
}

// TestFireOrderClearsAutonomousSlotTargetsOnlyOnZeroOrOne locks the fire
// handler's extra effect and the strictness of its test [04 R-STANCE-01 §2]:
// on an unmasked parameter of exactly 0 or exactly 1 every slot carrying the
// autonomous-targeting bit loses its stored target, the bit itself survives,
// and any other parameter — including one that masks down to the same stance —
// leaves the slots alone.
func TestFireOrderClearsAutonomousSlotTargetsOnlyOnZeroOrOne(t *testing.T) {
	arm := func(u *units.Unit) {
		for slot := 0; slot < units.NumSlots; slot++ {
			s := u.SlotAt(slot)
			s.Flags |= slotTracking
			s.Target = units.Target{Kind: units.TargetUnit, Unit: 9}
		}
	}

	q, u := standingFixture(nil)
	arm(u)
	q.Push(Lookup("Standing_FireOrder"), Node{Owner: u.Handle, Param1: 1})
	q.Pump(u, 0)
	for slot := 0; slot < units.NumSlots; slot++ {
		s := u.SlotAt(slot)
		if s.Target.Kind != units.TargetNone {
			t.Fatalf("slot %d kept its target on a transition to return fire", slot)
		}
		if s.Flags&slotTracking == 0 {
			t.Fatalf("slot %d lost the autonomous-targeting bit; the row does not clear it", slot)
		}
	}

	// 5 masks to 1, so the deposit is the same, but the unmasked test rejects
	// it and the slots keep their targets.
	q2, u2 := standingFixture(nil)
	arm(u2)
	q2.Push(Lookup("Standing_FireOrder"), Node{Owner: u2.Handle, Param1: 5})
	q2.Pump(u2, 0)
	if got := (u2.Flags >> 20) & 3; got != 1 {
		t.Fatalf("fire stance = %d, want 5 masked to 1", got)
	}
	for slot := 0; slot < units.NumSlots; slot++ {
		if u2.SlotAt(slot).Target.Kind != units.TargetUnit {
			t.Fatalf("slot %d was cleared on an unmasked parameter of 5 [04 R-STANCE-01 §2]", slot)
		}
	}
}

// TestCloakOrdersNeedTheDerivedCapability locks the gate of [04 R-ORD-01 §2]:
// the can-cloak capability is `cloakcost > 0`, not `init_cloaked`, and a
// definition without it accepts the order, changes nothing, and still completes.
func TestCloakOrdersNeedTheDerivedCapability(t *testing.T) {
	q, u := standingFixture(&content.UnitDef{CloakCost: 200})
	q.Push(Lookup("Cloak_On"), Node{Owner: u.Handle})
	q.Pump(u, 0)
	if !u.IsCloaked {
		t.Fatal("Cloak_On did not raise the cloak-wanted state on a cloak-capable definition")
	}
	q.Push(Lookup("Cloak_Off"), Node{Owner: u.Handle})
	q.Pump(u, 0)
	if u.IsCloaked {
		t.Fatal("Cloak_Off did not lower the cloak-wanted state")
	}

	// InitCloaked does not confer the capability; cloakcost does.
	q2, u2 := standingFixture(&content.UnitDef{InitCloaked: true})
	u2.IsCloaked = false
	q2.Push(Lookup("Cloak_On"), Node{Owner: u2.Handle})
	q2.Pump(u2, 0)
	if u2.IsCloaked {
		t.Fatal("Cloak_On acted on a definition whose cloakcost is zero [04 R-ORD-01 §2]")
	}
	if q2.LenPrimary() != 0 {
		t.Fatal("Cloak_On must complete even when the definition rejects it")
	}
}

// TestWaitForAttackWaitsOnTheInterruptPairThenCompletes locks the row's two
// phases and the gate it arms: 0x18 is the interrupt pair — 0x8 target loss and
// 0x10, the second interrupt bit [04 R-ORD-01 §0] — and the record completes on
// the visit after either arrives. A null target completes without waiting.
func TestWaitForAttackWaitsOnTheInterruptPairThenCompletes(t *testing.T) {
	q, u := standingFixture(nil)
	q.Push(Lookup("WaitForAttack"), Node{Owner: u.Handle, Target: 7})
	q.Pump(u, 0)
	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the record waiting", q.LenPrimary())
	}
	if got := q.Primary()[0].DynamicGate; got != 0x18 {
		t.Fatalf("gate = %#x, want 0x18 [04 R-ORD-01 §2]", got)
	}
	u.Pending |= 0x8 // the target was taken from it
	q.Pump(u, 1)
	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the record completed on the wake", q.LenPrimary())
	}

	q2, u2 := standingFixture(nil)
	q2.Push(Lookup("WaitForAttack"), Node{Owner: u2.Handle})
	q2.Pump(u2, 0)
	if q2.LenPrimary() != 0 {
		t.Fatal("a null target must complete the record on sight [04 R-ORD-01 §2]")
	}
}

// TestParalyzeClampsTheCreditAndLowersTheStunOnExpiry locks the row's two
// arms: the first visit clamps p1 to 1800, arms the record's own deadline from
// it, zeroes p1 and raises the stun; the visit after expiry sees p1 = 0 and
// lowers it again. The clamp is the assertion that is easy to regress silently.
func TestParalyzeClampsTheCreditAndLowersTheStunOnExpiry(t *testing.T) {
	q, u := standingFixture(nil)
	u.SlotAt(0).Flags |= slotTracking
	u.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: 9}
	q.Push(Lookup("Paralyze"), Node{Owner: u.Handle, Param1: 9000})
	q.Pump(u, 0)

	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the stun record waiting", q.LenPrimary())
	}
	n := q.Primary()[0]
	if n.Deadline != paralyzeMaxCredit {
		t.Fatalf("deadline = %d, want the credit clamped to %d [04 R-ORD-01 §2]", n.Deadline, paralyzeMaxCredit)
	}
	if n.Param1 != 0 {
		t.Fatalf("p1 = %d, want it zeroed after the deadline is armed", n.Param1)
	}
	if !u.Stunned {
		t.Fatal("the stun was not raised")
	}
	if u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatal("the slots were not released")
	}

	q.Pump(u, paralyzeMaxCredit)
	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the record completed on expiry", q.LenPrimary())
	}
	if u.Stunned {
		t.Fatal("the stun was not lowered when the record completed [04 R-ORD-01 §2]")
	}
}

// TestWaitScanVariantDrainsItsBudgetAndCompletes locks the timeout drain of
// [04 R-ORD-01 §2] and [R-ORDER-02 §1]'s "Wait | timeout drain" row: with a
// scan radius set, each failed scan subtracts `RNG(30) + 150` from the budget
// and waits that long, and the record completes once the budget falls below 1.
// The budget therefore runs negative, which is why the test is signed.
func TestWaitScanVariantDrainsItsBudgetAndCompletes(t *testing.T) {
	q, u := standingFixture(nil)
	q.Push(Lookup("Wait"), Node{Owner: u.Handle, Param1: 10, Param2: 100})
	q.Pump(u, 0)

	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the record waiting out its drain", q.LenPrimary())
	}
	n := q.Primary()[0]
	if int32(n.Param1) >= 1 {
		t.Fatalf("p1 = %d, want a budget driven below 1 by a subtraction of at least %d", int32(n.Param1), waitScanBudgetStep)
	}
	if n.Deadline < waitScanBudgetStep || n.Deadline > waitScanBudgetStep+29 {
		t.Fatalf("deadline = %d, want %d..%d [04 R-ORD-01 §2]", n.Deadline, waitScanBudgetStep, waitScanBudgetStep+29)
	}

	q.Pump(u, uint32(n.Deadline))
	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the exhausted budget to complete the record", q.LenPrimary())
	}
}

// TestStandbyNeedsAMoverAndStandbyMineTheBuildingClassBit locks the two phase-0
// admissions of [04 R-ORD-01 §2]. `Standby` cancels the whole queue without a
// mover reference — a building-class definition never owns one, because the
// allocator constructs a mover only for `bmcode 1` [04 R-FAC-02 §5] — while
// `Standby_Mine` admits exactly the opposite unit: one carrying status-word bit
// 29. Every stock mine in the reference install is `bmcode 0` (I14), which is
// what forced that reading; see the TODO(question) at standbyMineHandler.
func TestStandbyNeedsAMoverAndStandbyMineTheBuildingClassBit(t *testing.T) {
	q, u := standingFixture(&content.UnitDef{BMCode: false})
	q.Push(Lookup("Standby"), Node{Owner: u.Handle})
	q.Pump(u, 0)
	if q.LenPrimary() != 0 || q.LenSecondary() != 0 {
		t.Fatalf("Standby without a mover must cancel the whole queue [04 R-ORD-01 §2]; got %d/%d", q.LenPrimary(), q.LenSecondary())
	}

	q2, u2 := standingFixture(&content.UnitDef{BMCode: true})
	q2.Push(Lookup("Standby"), Node{Owner: u2.Handle})
	q2.Pump(u2, 0)
	if q2.LenPrimary() != 1 {
		t.Fatalf("Standby with a mover must keep its record; got %d", q2.LenPrimary())
	}
	if got := q2.Primary()[0].DynamicGate; got&gateSlotClear == 0 {
		t.Fatalf("phase 0 gate = %#x, want the 0x10000 slot-clear bit armed [04 R-ORD-01 §0]", got)
	}

	q3, u3 := standingFixture(&content.UnitDef{})
	q3.Push(Lookup("Standby_Mine"), Node{Owner: u3.Handle})
	q3.Pump(u3, 0)
	if q3.LenPrimary() != 0 {
		t.Fatalf("Standby_Mine without status bit 29 must cancel the queue; got %d", q3.LenPrimary())
	}

	q4, u4 := standingFixture(&content.UnitDef{})
	u4.Flags |= units.BuildingClassStatus
	q4.Push(Lookup("Standby_Mine"), Node{Owner: u4.Handle})
	q4.Pump(u4, 0)
	if q4.LenPrimary() != 1 {
		t.Fatalf("Standby_Mine on a building-class unit must keep its record; got %d", q4.LenPrimary())
	}
}

// TestOpportunityScanIsFireAtWillOnly locks the one behavioral difference
// between return fire and fire at will [04 R-STANCE-01 §3]: the shared scan
// returns nothing without searching unless the unit's standing fire field reads
// exactly 2. Only the gate is asserted — the search itself is the combat
// layer's and is a recorded placeholder at the site.
func TestOpportunityScanIsFireAtWillOnly(t *testing.T) {
	_, u := standingFixture(nil)
	for value := uint32(0); value <= 3; value++ {
		u.Flags = (u.Flags &^ (3 << stanceFireShift)) | (value << stanceFireShift)
		if got := opportunityScan(u); got != nil {
			t.Fatalf("fire stance %d: scan returned a target; the search is not implemented", value)
		}
	}
}
