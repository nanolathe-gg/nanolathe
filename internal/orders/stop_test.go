package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// stopFixture is one unit with a bound queue carrying a seeded simulation
// stream, so any handler that draws does so from a stream the test owns [I4].
func stopFixture(canFly bool, mode uint8) (*Queue, *units.Unit) {
	rng.SeedGlobal(1, 0)
	u := &units.Unit{
		Handle: 1,
		Def:    &content.UnitDef{CanFly: canFly},
		X:      numeric.Fixed(70 << 16),
		Y:      numeric.Fixed(40 << 16),
		Z:      numeric.Fixed(90 << 16),
	}
	u.Move.Mode = mode
	q := &Queue{binding: &QueueBinding{SimRNG: rng.Global.Sim}}
	BindQueue(u, q)
	return q, u
}

// TestStopOnAirborneAircraftSpawnsLandIfCanAtTheHead locks the airborne arm of
// the `Stop` row [04 R-ORD-01 §2]: an airborne (mover mode 2, [04 R-MOV-01 §8])
// `canfly` unit gets a `VTOL_LandIfCan` record with no target, its own position
// as the goal and zero parameters, inserted at the head [04 R-ORD-01 §1] so it
// runs before anything the queue already held; `Stop` itself then completes.
// This is the producer the landing machine of [04 R-AIR-01 §6] was missing.
func TestStopOnAirborneAircraftSpawnsLandIfCanAtTheHead(t *testing.T) {
	q, u := stopFixture(true, 2)
	q.Push(Lookup("Stop"), Node{Owner: u.Handle})
	q.Push(Lookup("Move_Ground"), Node{Owner: u.Handle}) // an order queued behind it
	clearGates(q)

	q.Pump(u, 40)

	if q.LenPrimary() != 2 {
		t.Fatalf("primary length = %d, want the spawned landing plus the queued move", q.LenPrimary())
	}
	head := q.Primary()[0]
	if name := DescriptorFor(head.ID).Name; name != "VTOL_LandIfCan" {
		t.Fatalf("head = %s, want the spawned VTOL_LandIfCan [04 R-ORD-01 §2]", name)
	}
	if DescriptorFor(q.Primary()[1].ID).Name != "Move_Ground" {
		t.Fatal("the spawn must go in front of the queued order, not behind it [04 R-ORD-01 §1]")
	}
	if head.Target != 0 {
		t.Fatalf("spawned target = %d, want none [04 R-ORD-01 §2]", head.Target)
	}
	if head.GoalX != u.X || head.GoalY != u.Y || head.GoalZ != u.Z {
		t.Fatalf("spawned goal = (%d,%d,%d), want the unit's own position [04 R-ORD-01 §2]", head.GoalX, head.GoalY, head.GoalZ)
	}
	if head.Param1 != 0 || head.Param2 != 0 || head.Param3 != 0 {
		t.Fatalf("spawned p1..p3 = %d,%d,%d, want zero [04 R-ORD-01 §2]", head.Param1, head.Param2, head.Param3)
	}
	if head.CreationTick != 40 {
		t.Fatalf("spawned creation tick = %d, want the dispatching tick [04 §3.2]", head.CreationTick)
	}
	for _, n := range q.Primary() {
		if DescriptorFor(n.ID).Name == "Stop" {
			t.Fatal("Stop must complete on its single visit [04 R-ORD-01 §2]")
		}
	}
}

// TestStopOnGroundUnitCompletesWithoutSpawning locks the other half of the
// gate: the landing spawn needs BOTH the airborne mover mode and `canfly`
// [04 R-ORD-01 §2], so neither a grounded aircraft nor a flightless unit in
// some other mode gets one, and `Stop` is a single-visit completion.
func TestStopOnGroundUnitCompletesWithoutSpawning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		canFly bool
		mode   uint8
	}{
		{"ground unit", false, 1},
		{"landed aircraft", true, 1},
		{"flightless unit that somehow reads airborne", false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, u := stopFixture(tc.canFly, tc.mode)
			q.Push(Lookup("Stop"), Node{Owner: u.Handle})
			clearGates(q)

			q.Pump(u, 40)

			if q.LenPrimary() != 0 {
				t.Fatalf("primary = %d records, want an empty queue: Stop completes and spawns nothing here [04 R-ORD-01 §2]", q.LenPrimary())
			}
		})
	}
}

// TestStopClearsEveryWeaponSlotTarget locks the unconditional slot clear the
// `Stop` row cites [04 R-ORD-01 §2][R-ORDER-02 §2]: all three slots in order,
// with no control-byte condition — including a slot whose clear latch is
// already set, which the cleanup-side form skips.
func TestStopClearsEveryWeaponSlotTarget(t *testing.T) {
	q, u := stopFixture(false, 1)
	u.Slots[0].Target = units.Target{Kind: units.TargetUnit, Unit: 7}
	u.Slots[1].Target = units.Target{Kind: units.TargetUnit, Unit: 8}
	u.Slots[1].Flags |= slotOrderInhibit // already latched: the guarded form would skip it
	u.Slots[2].Target = units.Target{Kind: units.TargetGround, X: 1 << 16, Z: 2 << 16}
	q.Push(Lookup("Stop"), Node{Owner: u.Handle})
	clearGates(q)

	q.Pump(u, 40)

	for slot := 0; slot < units.NumSlots; slot++ {
		if got := u.SlotAt(slot).Target.Kind; got != units.TargetNone {
			t.Fatalf("slot %d target kind = %v, want the empty form [R-ORDER-02 §2]", slot, got)
		}
	}
}

// TestPushHeadInheritsTheDisplacedHeadsAutoFlag locks the head insert's one
// stated flag rule [04 R-ORD-01 §1]: the new head inherits the auto/default-
// operation flag of the record it displaced, so a spawn in front of an idle
// default op is itself a default op and the leading-auto drop still sees one.
func TestPushHeadInheritsTheDisplacedHeadsAutoFlag(t *testing.T) {
	q := &Queue{}
	q.Push(Lookup("Move_Ground"), Node{Flags: FlagAutoOp})
	q.primary[0].Flags |= FlagAutoOp
	spawned := q.PushHead(Lookup("VTOL_LandIfCan"), Node{})
	if spawned == nil || q.Primary()[0] != spawned {
		t.Fatal("PushHead must put the spawned record at the front of the primary segment")
	}
	if spawned.Flags&FlagAutoOp == 0 {
		t.Fatal("the spawned head must inherit the displaced head's auto flag [04 R-ORD-01 §1]")
	}
	if spawned.DynamicGate != 0 {
		t.Fatalf("spawned gate = %#x, want the caller's value: a fresh record awaits nothing [04 §3.2]", spawned.DynamicGate)
	}
}
