package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// vtolAirOrders is the family this unit implements, in the order the plan lists
// them. `VTOL_Evade` is first because its descriptor carries a zero static mask
// [04 §3.1], so a record of it dispatches on sight and is the order that proves
// the family reaches a handler at all.
var vtolAirOrders = []string{
	"VTOL_Evade",
	"VTOL_SeekAttack",
	"VTOL_SeekGuard",
	"VTOL_GetRepaired",
	"AirStrike",
	"AirToAir",
	"AirToGround",
	"AirToGroundHover",
}

// TestVTOLAirOrdersDispatchAndDoNotPark is the unit's own gate: every order it
// owns reaches a handler on its first visit, and none of them is left sitting
// on a gate nothing will raise — the defect PLAN 18 §0 exists to remove.
//
// "Does not park" is asserted as a relationship, not a code: after one pump the
// record has either left the queue (the handler ended the order) or it is still
// the head with a gate that CAN be satisfied — which, with no movement system
// bound to supply the legs, is the one-tick deadline hold of `airHandOff`, and
// the pump's own deadline expiry raises its bit. The one thing that must never
// appear is the pump's missing-handler diagnostic.
func TestVTOLAirOrdersDispatchAndDoNotPark(t *testing.T) {
	for _, name := range vtolAirOrders {
		q, u := gateFixture()
		// A live target reference: three of these rows end the order on a null
		// one before they ever reach a leg [04 R-AIR-01 §7][04 R-AIR-01 §8].
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return u }})
		q.Push(Lookup(name), Node{Owner: u.Handle, Target: 7})
		q.Pump(u, 40)

		for _, d := range q.Diagnostics() {
			if strings.Contains(d, "no handler for") {
				t.Fatalf("%s: %s — the descriptor reached the pump's missing-handler arm [04 §3.3]", name, d)
			}
		}
		if q.LenPrimary() == 0 {
			continue // the handler ended the order; nothing is parked
		}
		n := q.Primary()[0]
		if n.DynamicGate != 0 && n.Deadline == -1 {
			t.Fatalf("%s: gate %#x with no deadline — the record waits on bits nothing here raises [04 §3.3]", name, n.DynamicGate)
		}
		if n.Deadline != -1 && n.DynamicGate&1 == 0 {
			t.Fatalf("%s: deadline %d without gate bit 0; the deadline setter always ORs it [04 R-ORD-01 §1]", name, n.Deadline)
		}
	}
}

// TestVTOLEvadeEntryIsTheSharedOne locks the reuse [04 R-AIR-01 §8] states:
// "Established — `VTOL_Evade`. Entry returns 5 on a null target or when the
// satisfied set intersects `0x10008`." That is `airEntry` with mask
// `pendTargetGone`, the same entry the four air-attack executors run — §8's own
// step 1 lists `VTOL_Evade` beside `AirToGroundHover` under that mask.
func TestVTOLEvadeEntryIsTheSharedOne(t *testing.T) {
	for _, tc := range []struct {
		name      string
		target    pool.Handle
		satisfied uint32
	}{
		{name: "null target", target: 0},
		{name: "target removed", target: 7, satisfied: pendTargetRemoved},
		{name: "target cloaked", target: 7, satisfied: pendTargetCloaked},
	} {
		q, u := gateFixture()
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return u }})
		n := &Node{ID: Lookup("VTOL_Evade"), Owner: u.Handle, Target: tc.target}
		if code := vtolEvadeHandler(u, n, tc.satisfied, 40); code != 5 {
			t.Fatalf("%s: code %d, want 5 [04 R-AIR-01 §8]", tc.name, code)
		}
	}
}

// TestVTOLGetRepairedIsATwoPhaseWait locks [04 R-AIR-01 §7]'s whole row: a null
// target aborts; phase 0 advances the moment health has REACHED `MaxDamage`
// (unsigned, `MaxDamage <= health`, so exactly full advances) and otherwise
// waits 30 ticks on gate `0x8`; phase 1 completes.
//
// The non-strict compare is the part that is easy to regress: with a strict
// one, a unit repaired to exactly full would wait another 30 ticks for a repair
// that can no longer happen.
func TestVTOLGetRepairedIsATwoPhaseWait(t *testing.T) {
	q, u := gateFixture()
	q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return u }})
	u.Def.MaxDamage = 100
	u.Health = 40

	n := &Node{ID: Lookup("VTOL_GetRepaired"), Owner: u.Handle, Target: 7}
	if code := vtolGetRepairedHandler(u, n, 0, 40); code != 2 {
		t.Fatalf("damaged phase 0 returned %d, want the 2 that holds [04 R-AIR-01 §7]", code)
	}
	if n.Deadline != 70 || n.DynamicGate&pendTargetRemoved == 0 || n.DynamicGate&1 == 0 {
		t.Fatalf("deadline %d gate %#x, want tick+30 with 0x8 and the deadline bit [04 R-AIR-01 §7]", n.Deadline, n.DynamicGate)
	}

	u.Health = 100 // exactly whole: the compare is non-strict
	if code := vtolGetRepairedHandler(u, n, 0, 70); code != 1 {
		t.Fatalf("whole phase 0 returned %d, want 1 — `MaxDamage <= health` is not strict [04 R-AIR-01 §7]", code)
	}
	n.Phase = 1
	if code := vtolGetRepairedHandler(u, n, 0, 71); code != 5 {
		t.Fatalf("phase 1 returned %d, want 5 [04 R-AIR-01 §7]", code)
	}

	n.Target = 0
	if code := vtolGetRepairedHandler(u, n, 0, 72); code != 8 {
		t.Fatalf("null target returned %d, want the 8 that abandons [04 R-AIR-01 §7]", code)
	}
}

// TestVTOLAirFamilyOwnsTheFourAirAttackRows locks the registration order this
// unit depends on: vtolair.go's installer runs before combat.go's, so the four
// air-attack descriptors carry the handler that hands their legs to
// internal/movement rather than combat.go's documented placeholder. Reordering
// handlerInstallers would silently restore the placeholder, and the only
// visible symptom would be aircraft that complete an attack order instantly.
func TestVTOLAirFamilyOwnsTheFourAirAttackRows(t *testing.T) {
	installHandlers()
	for _, name := range []string{"AirStrike", "AirToAir", "AirToGround", "AirToGroundHover"} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s missing from the descriptor table", name)
		}
		handler := Table()[int(id)].Handler
		if handler == nil {
			t.Fatalf("%s has no handler", name)
		}
		// Identity is compared through behaviour rather than function pointers,
		// which Go does not compare: the placeholder completes a record whose
		// entry sequence fell through, this family holds it for its legs.
		q, u := gateFixture()
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return u }})
		n := &Node{ID: id, Owner: u.Handle, Target: 7}
		if code := handler(u, n, 0, 40); code != 2 {
			t.Fatalf("%s: entry fall-through returned %d, want the 2 that hands off to the legs", name, code)
		}
	}
}

// TestLandIfCanCompletesOnTouchdown covers the record lifecycle the landing
// machine could not finish while `VTOL_LandIfCan` had no descriptor handler:
// the executor reached touchdown, nothing read that, and the record stayed at
// the head of the queue jamming every order behind it.
//
// The runner stands in for internal/movement here — this package cannot import
// it — and answers exactly as `reportAirMachineOutcome` does: hold while the
// machine is working, complete once it has finished.
func TestLandIfCanCompletesOnTouchdown(t *testing.T) {
	id := Lookup("VTOL_LandIfCan")
	if id == 0 {
		t.Fatalf("VTOL_LandIfCan is not in the descriptor table")
	}
	if DescriptorFor(id).Handler == nil {
		t.Fatalf("VTOL_LandIfCan must have a descriptor handler so its record can finish")
	}

	landed := false
	u := &units.Unit{Def: &content.UnitDef{UnitName: "flier", CanFly: true}}
	q := QueueForUnit(u)
	q.SetBinding(&QueueBinding{Movement: &MovementGoalAdapter{
		RunAir: func(_ *units.Unit, n *Node, _ uint32, tick uint32) (Code, bool) {
			if landed {
				return Code(5), true
			}
			n.Deadline = int32(tick + 1)
			n.DynamicGate |= 1
			return Code(2), true
		},
	}})

	q.Push(id, Node{Owner: u.Handle})
	moveID := Lookup("Move_Ground")
	if moveID == 0 {
		t.Fatalf("Move_Ground is not in the descriptor table")
	}
	q.Push(moveID, Node{Owner: u.Handle})

	// While the machine works the record holds its place at the head, and the
	// queued order behind it does not run.
	for tick := uint32(1); tick <= 5; tick++ {
		q.Pump(u, tick)
		if len(q.Primary()) == 0 || DescriptorFor(q.Primary()[0].ID).Name != "VTOL_LandIfCan" {
			t.Fatalf("tick %d: landing record left the head while still working", tick)
		}
	}

	// Touchdown: the next visit frees the record and the queued order becomes
	// the head. This is the whole defect — before the handler existed, this
	// loop ran forever.
	landed = true
	for tick := uint32(6); tick <= 12; tick++ {
		q.Pump(u, tick)
		if len(q.Primary()) > 0 && DescriptorFor(q.Primary()[0].ID).Name != "VTOL_LandIfCan" {
			return
		}
		if len(q.Primary()) == 0 {
			return
		}
	}
	t.Fatalf("landing record never freed after touchdown; the queue is jammed")
}
