package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestAttackNoMovePreCheckCompletesOnTheDisengageBit locks the corrected
// pre-check mask of [04 R-ORD-01 §3]: it is `0x10808`, not `0x10008`, so the
// attack family's `0x800` completes the order here exactly as it does in
// `Attack_Chase`'s first pre-check. Of the four bits the phase-1 gate `0x11808`
// waits on, only `0x1000` therefore ever reaches phase 2.
func TestAttackNoMovePreCheckCompletesOnTheDisengageBit(t *testing.T) {
	id := Lookup("Attack_NoMove")
	const targetHandle pool.Handle = 7

	for _, tc := range []struct {
		name      string
		bit       uint32
		completes bool
	}{
		{"disengage 0x800", 0x800, true},
		{"target removed 0x8", pendTargetRemoved, true},
		{"target cloaked 0x10000", pendTargetCloaked, true},
		{"engage 0x1000", 0x1000, false},
	} {
		q, u := gateFixture()
		q.Push(id, Node{Owner: u.Handle, Target: targetHandle})
		// Walk to the waiting phase first, so the bit under test is the only
		// thing that separates a completion from a phase-2 visit.
		q.Pump(u, 40)
		if q.LenPrimary() != 1 || q.Primary()[0].Phase != 2 {
			t.Fatalf("%s: fixture did not reach the waiting phase [04 R-ORD-01 §3]", tc.name)
		}
		n := q.Primary()[0]
		n.Satisfied |= tc.bit
		q.Pump(u, 41)

		completed := q.LenPrimary() == 0
		if completed != tc.completes {
			t.Fatalf("%s: order completed = %v, want %v — the pre-check mask is 0x10808 [04 R-ORD-01 §3]",
				tc.name, completed, tc.completes)
		}
		if tc.completes {
			continue
		}
		// 0x1000 is the one bit that reaches phase 2: the inhibit-all clears
		// every slot target and the record re-arms.
		if got := u.SlotAt(0).Target.Kind; got != units.TargetNone {
			t.Fatalf("%s: slot 0 target kind %v after phase 2's inhibit-all [04 R-ORD-01 §3]", tc.name, got)
		}
		if got := q.Primary()[0].Phase; got != 0 {
			t.Fatalf("%s: phase %d after the re-arm, want 0 [04 §3.3] code 9", tc.name, got)
		}
	}
}

// TestAttackNoMovePhaseZeroReturnsNoSlots locks the other half of the same
// correction: the handler holds exactly ONE inhibit-all-slots call and it is
// phase 2's, so phase 0 is the caption clear alone and leaves every slot where
// it found it [04 R-ORD-01 §3] ([04 R-UNIT-06 §5]'s phase-0 claim is corrected
// there). A slot an earlier order took must still be taken after phase 0.
func TestAttackNoMovePhaseZeroReturnsNoSlots(t *testing.T) {
	id := Lookup("Attack_NoMove")
	q, u := gateFixture()
	// Slot 2 carries a target another record bound; nothing in this row may
	// clear it.
	bindSlotToUnit(u, 2, 9)

	q.Push(id, Node{Owner: u.Handle, Target: 7})
	q.Pump(u, 40)

	got := u.SlotAt(2).Target
	if got.Kind != units.TargetUnit || got.Unit != 9 {
		t.Fatalf("slot 2 target = %+v after phases 0 and 1, want the earlier bind untouched [04 R-ORD-01 §3]", got)
	}
}

// TestCouldNotFireBitFlowsFromTheUnitWordToPhaseTwo locks the consumer half of
// [06 R-WPN-05 §6]. The weapon layer raises `0x1000` on the UNIT's order-event
// word, not on a record's own pending word; the pump merges the two, hands the
// bit to the first record whose gate names it, and clears it from both. That
// record is `Attack_NoMove` phase 2 — the disengage arm — and until one
// consumes it the bit survives across ticks.
func TestCouldNotFireBitFlowsFromTheUnitWordToPhaseTwo(t *testing.T) {
	id := Lookup("Attack_NoMove")
	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle, Target: pool.Handle(7)})
	q.Pump(u, 40)
	if q.LenPrimary() != 1 || q.Primary()[0].Phase != 2 {
		t.Fatal("fixture did not reach the waiting phase [04 R-ORD-01 §3]")
	}

	// The weapon layer's raise. It lands on the unit, latched.
	u.Pending |= units.PendingCouldNotFire
	q.Pump(u, 41)

	if q.LenPrimary() != 1 || q.Primary()[0].Phase != 0 {
		t.Fatalf("phase 2's re-arm did not run: len %d", q.LenPrimary())
	}
	if u.Pending&units.PendingCouldNotFire != 0 {
		t.Fatal("the pump clears the satisfied bit from the unit word once a record's gate names it [06 R-WPN-05 §6]")
	}
	if got := u.SlotAt(0).Target.Kind; got != units.TargetNone {
		t.Fatalf("phase 2 inhibits all slots, slot 0 target kind %v", got)
	}
}

// TestSlotTargetSettersClearTheCouldNotFireBit locks the second of the bit's
// three clearing sites [06 R-WPN-05 §6] [04 R-ORD-01 §7]: both slot target
// setters clear bits 10-14 of the owner's order-event word, so binding a new
// target discards a disengage raised against the previous one. (The third site
// is unit construction, which zeroes the whole word.)
func TestSlotTargetSettersClearTheCouldNotFireBit(t *testing.T) {
	for _, tc := range []struct {
		name string
		bind func(u *units.Unit)
	}{
		{"bind slot to unit", func(u *units.Unit) { bindSlotToUnit(u, 0, 9) }},
		{"bind slot to point", func(u *units.Unit) { bindSlotToPosition(u, 0, 1<<16, 2<<16) }},
	} {
		_, u := gateFixture()
		// Bits 10-14 set, plus one bit outside the span that must survive.
		u.Pending = units.PendingSlotSetterClear | pendTargetRemoved
		tc.bind(u)
		if u.Pending&units.PendingSlotSetterClear != 0 {
			t.Fatalf("%s: bits 10-14 = %#x, want cleared [04 R-ORD-01 §7]", tc.name, u.Pending&units.PendingSlotSetterClear)
		}
		if u.Pending&pendTargetRemoved == 0 {
			t.Fatalf("%s: the setter must clear bits 10-14 and nothing else", tc.name)
		}
	}
}
