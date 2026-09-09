package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestIdleRefillMissionCondition locks the three-term condition of the primary
// pump's idle refill [04 §3.3, "Closed — the idle-queue refill from
// `defaultmissiontype`"][02 R-KEYS-01 §1], re-verified against the executable
// on 2026-08-31: the owner's controller state is 1 or 2 — the two ACTIVE player
// states, human and computer, not "the two computer-player states" the section
// used to name — and the authored name resolves through the descriptor
// registry's own case-insensitive lookup, with an unrecognised or empty name
// giving the reject sentinel and no record.
func TestIdleRefillMissionCondition(t *testing.T) {
	standby := Lookup("VTOL_Standby")
	if standby == 0 {
		t.Fatal("VTOL_Standby is unavailable")
	}
	unit := func(name string) *units.Unit {
		return &units.Unit{Def: &content.UnitDef{UnitName: "wakeidle", DefaultMissionType: name}}
	}
	for _, state := range []uint8{1, 2} {
		id, ok := IdleRefillMission(unit("VTOL_Standby"), state)
		if !ok || id != standby {
			t.Fatalf("controller state %d refilled id=%d ok=%v, want %d true", state, id, ok, standby)
		}
	}
	// The registry comparator is case-insensitive [04 §3.1].
	if id, ok := IdleRefillMission(unit("vtol_standby"), 1); !ok || id != standby {
		t.Fatalf("case-folded name refilled id=%d ok=%v, want %d true", id, ok, standby)
	}
	// Every other controller state is refused; state 3 is the eliminated/watch
	// player whose units are skipped by the sweep entirely [04 §8.3].
	for _, state := range []uint8{0, 3, 4} {
		if _, ok := IdleRefillMission(unit("VTOL_Standby"), state); ok {
			t.Fatalf("controller state %d must not refill", state)
		}
	}
	if _, ok := IdleRefillMission(unit(""), 1); ok {
		t.Fatal("an empty defaultmissiontype must not refill")
	}
	if _, ok := IdleRefillMission(unit("NotAnOrderName"), 1); ok {
		t.Fatal("an unrecognised defaultmissiontype must not refill: index 0 is the reject sentinel")
	}
	if _, ok := IdleRefillMission(nil, 1); ok {
		t.Fatal("a nil unit must not refill")
	}
}

// TestRefillIdleHeadInsertsAnAutoRecord locks the insertion half of the same
// closure: the record is created by the pump itself, carries the auto-op flag,
// and head-inserts into the segment the mission's descriptor selects; and a
// later ordinary order drops it, because "issuing a front-segment record drops
// leading auto/default records ... wherever they live" [04 §3.3].
func TestRefillIdleHeadInsertsAnAutoRecord(t *testing.T) {
	standby := Lookup("VTOL_Standby")
	moveID := Lookup("Move_Ground")
	if standby == 0 || moveID == 0 {
		t.Fatal("descriptor table is unavailable")
	}
	u := &units.Unit{Handle: 7, Def: &content.UnitDef{UnitName: "wakeidle2", DefaultMissionType: "VTOL_Standby"}}
	q := &Queue{}
	BindQueue(u, q)
	if !q.refillIdleWithState(u, 1) {
		t.Fatal("refill declined an idle unit with a default op")
	}
	if len(q.primary) != 1 || q.primary[0].ID != standby {
		t.Fatalf("refill primary=%v, want one VTOL_Standby record", q.primary)
	}
	if q.primary[0].Flags&FlagAutoOp == 0 {
		t.Fatal("the refilled record must carry the auto-op flag")
	}
	if q.refillIdleWithState(u, 1) {
		t.Fatal("refill must fire only on an empty front segment")
	}
	q.Push(moveID, Node{Deadline: -1})
	if len(q.primary) != 1 || q.primary[0].ID != moveID {
		t.Fatalf("issuing an order left the auto record in place: %v", q.primary)
	}
}

// TestMobileBuildUnreachableVisit locks the approach-failure arm of the
// mobile-build row [04 R-ORD-01 §5]: the `0x40` "cannot get there" bit plus a
// failing reach test ends the order with the verbatim caption and code 8. It is
// the only exit from an approach the search can never satisfy — the follower
// re-requests every sixty ticks forever with no ceiling [04 R-MOV-01 §7].
func TestMobileBuildUnreachableVisit(t *testing.T) {
	if text, code := MobileBuildUnreachableVisit(0x40, true); code != 8 || text != MobileBuildUnreachableText {
		t.Fatalf("0x40 out of reach gave (%q, %d), want (%q, 8)", text, code, MobileBuildUnreachableText)
	}
	if _, code := MobileBuildUnreachableVisit(0x40, false); code != 2 {
		t.Fatalf("0x40 in reach must continue the approach, got code %d", code)
	}
	if _, code := MobileBuildUnreachableVisit(0x20|0x80|1, true); code != 2 {
		t.Fatalf("without 0x40 the approach continues, got code %d", code)
	}
}

// TestApproachGateDeliversTheWakeThroughTheHandlerArgument locks the delivery
// path a work approach uses now that its record parks on retail's own gate
// [05 R-WORK-01 §13]: the owning subsystem arms `0xE0`, and the pump's step 3
// computes `(record.satisfied | unit.pending) & gate`, clears the delivered bits
// out of BOTH accumulating words and hands the set to the registered handler as
// its `satisfied` argument [04 §3.3]. A handler that reports it did not advance
// the record still receives that argument — that is what retires the seam this
// test replaces (a `DeliverApproachWake` helper that repeated the computation
// for a caller outside the pump).
func TestApproachGateDeliversTheWakeThroughTheHandlerArgument(t *testing.T) {
	if ApproachWakeGate != 0x20|0x40|0x80 {
		t.Fatalf("approach gate = %#x, want 0xE0 [05 R-WORK-01 §13]", ApproachWakeGate)
	}
	id := Lookup("MobileBuild")
	if id == 0 {
		t.Fatal("MobileBuild descriptor missing")
	}
	u := &units.Unit{Pending: 0x80 | 0x2}
	n := &Node{ID: id, Deadline: -1, DynamicGate: ApproachWakeGate, Satisfied: 0x40 | 0x1}
	q := NewQueueWith([]*Node{n}, nil)

	visits := 0
	var seen uint32
	q.SetOwnedHandler(id, func(_ *units.Unit, _ *Node, satisfied uint32, _ uint32) (Code, bool) {
		visits++
		seen = satisfied
		return 0, false
	})

	q.Pump(u, 7)
	if visits != 1 {
		t.Fatalf("armed record was dispatched %d times, want exactly one visit", visits)
	}
	if seen != 0x40|0x80 {
		t.Fatalf("handler satisfied argument = %#x, want 0xc0", seen)
	}
	// Bits outside the gate are untouched; delivered bits are consumed, so a
	// second visit sees an empty set rather than the same edge again.
	if n.Satisfied != 0x1 {
		t.Fatalf("record satisfied = %#x, want the ungated bit 0x1 alone", n.Satisfied)
	}
	if u.Pending != 0x2 {
		t.Fatalf("unit pending = %#x, want the ungated bit 0x2 alone", u.Pending)
	}
	// The pump clears the dispatched record's gate; a handler that advanced
	// nothing leaves the record where it was, so the owner re-arms on its own
	// next step and an unsatisfied gate stalls the head [04 §3.3].
	if n.DynamicGate != 0 {
		t.Fatalf("dispatched record kept gate %#x, want the pump's clear", n.DynamicGate)
	}
	n.DynamicGate = ApproachWakeGate
	q.Pump(u, 8)
	if visits != 1 {
		t.Fatalf("a re-armed record with nothing satisfied was dispatched again (%d visits)", visits)
	}
}
