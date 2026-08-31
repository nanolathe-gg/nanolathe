package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestPatrolCyclesRatherThanCompletingAtItsFirstPoint is this unit's defect
// test. While `Patrol` ran `Move_Ground`'s body it completed on the arrival bit
// and its record was unlinked: the unit walked to one waypoint and stopped, and
// no chain, no return-to-start waypoint and no cycle ever existed. The row's own
// body rotates instead [04 R-ORD-01 §4], so an arrival must leave the record
// queued, at the tail, with its phase set back to 1 to re-arm the leg.
func TestPatrolCyclesRatherThanCompletingAtItsFirstPoint(t *testing.T) {
	q, unit, _ := workFixture()
	id := Lookup("Patrol")
	if id == 0 || DescriptorFor(id).Handler == nil {
		t.Fatalf("Patrol has no handler after every installer ran [PLAN 18 gate 10]")
	}
	q.Push(id, Node{Owner: unit.Handle,
		GoalX: numeric.Fixed(200 << 16), GoalZ: numeric.Fixed(200 << 16)})
	patrol := q.Primary()[0]

	// Phase 0: the patrol-chain setup appends the return-to-start waypoint, so
	// the queue grows by one record that no move body would ever have made.
	q.Pump(unit, 1)
	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d after phase 0, want 2: the chain setup appends the return waypoint [04 R-ORD-01 §4]", q.LenPrimary())
	}
	if patrol.StaticGate&patrolChainMember == 0 {
		t.Fatalf("phase 0 must mark this record as a chain member [04 R-ORD-01 §4]")
	}

	// Phase 1 arms the leg on the movement outcomes alone: the gate is ASSIGNED
	// 0xE0 after the deadline setter, so the record carries no timer bit and
	// stalls at its gate until the mover reports [R-ORDER-02 §1].
	q.Pump(unit, 2)
	if patrol.Phase != 2 || patrol.DynamicGate != gateMoveOutcomes {
		t.Fatalf("after phase 1: phase %d gate %#x, want phase 2 gate 0xE0 [04 R-ORD-01 §4]", patrol.Phase, patrol.DynamicGate)
	}

	// The mover arrives.
	patrol.Satisfied |= gateArrived
	q.Pump(unit, 3)

	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d after arrival, want 2: the record must rotate, not complete", q.LenPrimary())
	}
	primary := q.Primary()
	if primary[1] != patrol {
		t.Fatalf("the arrived record must sit at the segment tail after its rotate [04 §3.3]")
	}
	if patrol.Phase != 1 {
		t.Fatalf("rotated record is at phase %d, want 1 so its next visit re-arms the leg", patrol.Phase)
	}
	for _, d := range q.Diagnostics() {
		if strings.Contains(d, "no handler") {
			t.Fatalf("Patrol parked on the missing-handler arm: %s", d)
		}
	}
}

// TestQueuedMoveRotatesOnItsSixtyTickDeadline locks the queued-move pair's whole
// row: "Deadline 60, *rotate*" [04 R-ORD-01 §2][R-ORDER-02 §1]. A rally marker
// on a factory's queue cycles; it does not walk a goal and it does not complete,
// which is what it did while it ran the ground move's body.
func TestQueuedMoveRotatesOnItsSixtyTickDeadline(t *testing.T) {
	// A fixed slice, not a map: the assertions run in one order (I1).
	for _, name := range []string{"QMove", "QPatrol"} {
		q, unit, _ := workFixture()
		id := Lookup(name)
		if id == 0 || DescriptorFor(id).Handler == nil {
			t.Fatalf("%s has no handler after every installer ran", name)
		}
		q.Push(id, Node{Owner: unit.Handle, GoalX: numeric.Fixed(200 << 16)})
		marker := q.Primary()[0]

		q.Pump(unit, 5)

		if q.LenPrimary() != 1 {
			t.Fatalf("%s: primary length %d, want 1: the marker rotates, it never completes", name, q.LenPrimary())
		}
		if marker.Deadline != 65 || marker.DynamicGate&gateDeadline == 0 {
			t.Fatalf("%s: deadline %d gate %#x, want 65 with bit 0 armed [04 R-ORD-01 §2]", name, marker.Deadline, marker.DynamicGate)
		}
		if marker.Phase != 0 {
			t.Fatalf("%s: phase %d, want 0: the row has no phase machine", name, marker.Phase)
		}
	}
}

// TestVTOLRepairPatrolInstallsThroughThePump is the registration half of this
// unit. WU-18-3 wrote and tested `VTOL_RepairPatrol`'s body but could only reach
// it by calling the handler directly: pump.go's move list claimed the descriptor
// first, and a family installer assigns only where the handler is still nil. The
// narrowing gives the name back, so the record must now reach that body through
// an ordinary pump — the chain-setup append and the airborne mover are what only
// the real row does [04 R-ORD-01 §7].
func TestVTOLRepairPatrolInstallsThroughThePump(t *testing.T) {
	q, builder, _ := vtolWorkFixture()
	id := Lookup("VTOL_RepairPatrol")
	if id == 0 || DescriptorFor(id).Handler == nil {
		t.Fatalf("VTOL_RepairPatrol has no handler after every installer ran")
	}
	q.Push(id, Node{Owner: builder.Handle,
		GoalX: numeric.Fixed(200 << 16), GoalZ: numeric.Fixed(200 << 16)})
	head := q.Primary()[0]

	q.Pump(builder, 100)

	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d, want 2: only the row's own phase 0 runs the patrol-chain setup", q.LenPrimary())
	}
	if builder.Move.Mode&0x3 != 2 {
		t.Fatalf("only the row's own preamble puts a grounded aircraft airborne [04 R-ORD-01 §7]")
	}
	if head.Phase != 1 {
		t.Fatalf("phase %d after the first visit, want 1", head.Phase)
	}
	for _, d := range q.Diagnostics() {
		if strings.Contains(d, "no handler") {
			t.Fatalf("VTOL_RepairPatrol parked on the missing-handler arm: %s", d)
		}
	}
}

// TestReconciledLeashAgreesBetweenGroundRowAndAirTwin locks the fold of this
// package's two pursuit-leash helpers onto one [R-STANCE-01 §4]. The ground
// `RepairUnit` and its air twin `VTOL_RepairUnit` cite the same `Attack_Chase`
// pre-check, so they must agree — and the geometry below is exactly where the
// retired helper disagreed with the contract.
//
// The anchor is the origin and the leash is 5 whole world units. At (3.9, 3.9)
// the contract truncates both deltas to 3 and the distance to
// `trunc(hypot(3, 3)) = 4`, which is inside the leash; the retired 16.16 form
// kept the fractions, measured 5.51 and abandoned the order. At (4, 4) the
// distance is `trunc(hypot(4, 4)) = 5` and the inclusive compare ends it.
func TestReconciledLeashAgreesBetweenGroundRowAndAirTwin(t *testing.T) {
	const frac39 = 3*65536 + 58982 // 3.9 world units in 16.16

	// A fixed slice, not a map: the assertions run in one order (I1).
	for _, row := range []struct {
		name    string
		fixture func() (*Queue, *units.Unit, *units.Unit)
		handler func(*units.Unit, *Node, uint32, uint32) Code
	}{
		{"RepairUnit", workFixture, repairUnitHandler},
		{"VTOL_RepairUnit", vtolWorkFixture, vtolRepairUnitHandler},
	} {
		_, actor, target := row.fixture()
		node := func() *Node {
			return &Node{ID: Lookup(row.name), Owner: actor.Handle, Target: target.Handle,
				Deadline: -1, Param3: 5, GuardX: 0, GuardY: 0}
		}

		actor.X, actor.Z = numeric.Fixed(frac39), numeric.Fixed(frac39)
		if code := row.handler(actor, node(), 0, 1); code == 5 {
			t.Fatalf("%s: a unit 4 whole units from its anchor ended a leash-5 order; the compare is on truncated whole units [R-STANCE-01 §4]", row.name)
		}

		actor.X, actor.Z = numeric.Fixed(4<<16), numeric.Fixed(4<<16)
		if code := row.handler(actor, node(), 0, 1); code != 5 {
			t.Fatalf("%s: returned %d at distance 5 with leash 5; the compare is inclusive [R-STANCE-01 §4]", row.name, code)
		}
	}
}
