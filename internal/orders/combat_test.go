package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// combatOrders is this unit's nine descriptors, a fixed slice so the
// assertions run in one order (I1).
var combatOrders = []string{
	"Attack_NoMove", "Attack_Kamikaze", "AttackSpecial", "AttackUType", "Suppress",
	"AirStrike", "AirToAir", "AirToGround", "AirToGroundHover",
}

// TestCombatOrdersDispatchAndNeverPark is PLAN 18's own gate for this family:
// every one of the nine reaches its handler on its FIRST pump visit — the
// property [04 R-ORD-01 §1]'s zeroed dynamic gate provides — and none of them
// ends the visit in the pump's missing-handler park, the 30..44-tick stall with
// a diagnostic that WU-18-0's gate_test.go locks for an unwired descriptor.
//
// The relationship asserted is per descriptor and structural: a record either
// left the queue under its row's terminal code, or it is waiting on a gate its
// own row armed (0xE1 for the kamikaze approach — its three movement bits plus
// the deadline setter's bit 0, 0x11808 for the stationary attack, 0x1C00 for
// suppression) — never on gate bit 0 alone with a deadline the pump wrote.
//
// `AttackUType` is the one row whose own wait IS gate bit 0 with a deadline:
// phase 0 arms `deadline RNG(90) + 1` [04 R-ORD-01 §3], so it is checked
// against that range instead. Before WU-18-7 gave handlers the tick it could
// not arm one at all.
func TestCombatOrdersDispatchAndNeverPark(t *testing.T) {
	const tick = 40
	for _, name := range combatOrders {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		if DescriptorFor(id).Handler == nil {
			t.Fatalf("%s has no handler after the installer list ran", name)
		}
		q, u := gateFixture()
		u.Def.CanAttack = true // AttackUType phase 0 requires it [04 R-ORD-01 §3]
		q.Push(id, Node{Owner: u.Handle})
		q.Pump(u, tick)

		for _, d := range q.Diagnostics() {
			if strings.Contains(d, "no handler for") {
				t.Fatalf("%s: pump reported %q; the record never reached its handler", name, d)
			}
		}
		if q.LenPrimary() == 0 {
			continue // left under its row's terminal code
		}
		n := q.Primary()[0]
		if name == "AttackUType" {
			if n.DynamicGate != 1 || n.Deadline < tick+1 || n.Deadline > tick+90 {
				t.Fatalf("%s: gate %#x deadline %d, want gate bit 0 with a deadline in %d..%d — `deadline RNG(90) + 1` from the tick it ran on [04 R-ORD-01 §3]", name, n.DynamicGate, n.Deadline, tick+1, tick+90)
			}
			continue
		}
		if n.DynamicGate == 1 && n.Deadline >= tick+30 {
			t.Fatalf("%s: parked on gate bit 0 with deadline %d — the pump's missing-handler stall [04 §3.3]", name, n.Deadline)
		}
		if n.DynamicGate == 0 && n.Deadline == -1 {
			t.Fatalf("%s: left ready with no gate and no deadline; the walk would re-dispatch it forever", name)
		}
	}
}

// TestAttackNoMoveBindsThenReleasesOnTargetLoss walks the whole row of
// `Attack_NoMove` [04 R-ORD-01 §3]: phase 0 is the caption clear, phase 1 binds
// slot 0 to the target and arms `0x11808`, and the phase-2 visit that the
// target-removed bit `0x8` unblocks ([04 R-ORD-01 §6]) clears all three slots
// and re-arms.
//
// The gate value is the contract that is easy to regress: it is the cloak bit
// `0x10000`, the engage/disengage pair `0x1000`/`0x800`, and the target-removed
// bit `0x8` [04 R-ORD-01 §0], and dropping any one of them makes the record
// deaf to the event it is waiting for.
func TestAttackNoMoveBindsThenReleasesOnTargetLoss(t *testing.T) {
	id := Lookup("Attack_NoMove")
	q, u := gateFixture()
	const targetHandle pool.Handle = 7
	q.Push(id, Node{Owner: u.Handle, Target: targetHandle})
	q.Pump(u, 40)

	n := q.Primary()[0]
	if n.Phase != 2 {
		t.Fatalf("phase = %d after the first pump, want the cascade through 0 and 1 [04 §3.3]", n.Phase)
	}
	if n.DynamicGate != 0x11808 {
		t.Fatalf("gate = %#x, want 0x11808 [04 R-ORD-01 §3]", n.DynamicGate)
	}
	if got := u.SlotAt(0).Target; got.Kind != units.TargetUnit || got.Unit != targetHandle {
		t.Fatalf("slot 0 target = %+v, want the record's target bound as a unit [04 R-ORD-01 §1]", got)
	}

	// One of the engage/disengage bits arrives: phase 2 inhibits all three
	// slots and re-arms. Which bits reach phase 2 matters — the pre-check eats
	// `0x8` and `0x10000` before the phase switch ever runs, so phase 2 is
	// "reached when any of those bits arrive" only for the `0x800`/`0x1000`
	// pair [04 R-ORD-01 §3].
	n.Satisfied |= 0x1000
	q.Pump(u, 41)

	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the record kept by the re-arm [04 §3.3] code 9", q.LenPrimary())
	}
	if u.SlotAt(0).Target.Kind != units.TargetNone {
		t.Fatalf("slot 0 still bound after the inhibit-all step [04 R-ORD-01 §3]")
	}
	if q.Primary()[0].Phase != 0 {
		t.Fatalf("phase = %d, want the code-9 last-record re-arm to reset it [04 §3.3]", q.Primary()[0].Phase)
	}

	// The target-removed bit instead ends the order outright, from any phase.
	q2, u2 := gateFixture()
	q2.Push(id, Node{Owner: u2.Handle, Target: targetHandle})
	q2.Primary()[0].Satisfied |= pendTargetRemoved
	q2.Primary()[0].DynamicGate = pendTargetRemoved
	q2.Pump(u2, 42)
	if q2.LenPrimary() != 0 {
		t.Fatalf("a target-removed record survived; the pre-check completes it [04 R-ORD-01 §3][04 R-ORD-01 §6]")
	}
}

// TestSuppressBindsTheGroundSlotsToItsGoalPoint locks the two halves of
// `Suppress` phase 1 that are easy to get backwards [04 R-ORD-01 §3]: with p1
// other than 2 it releases slots 0 and 1 and binds BOTH to the goal position
// (not just the picked one), and it arms `0x1C00`.
//
// It also locks the ground-position bind's whole-unit truncation
// [04 R-ORD-01 §1]: the stored X and Z are the goal's integer world units, so a
// fractional goal is truncated toward zero rather than carried at 16.16.
func TestSuppressBindsTheGroundSlotsToItsGoalPoint(t *testing.T) {
	id := Lookup("Suppress")
	q, u := gateFixture()
	goalX := numeric.Fixed(120<<16) + numeric.Fixed(0x8000) // 120.5 world units
	goalZ := numeric.Fixed(64 << 16)
	q.Push(id, Node{Owner: u.Handle, GoalX: goalX, GoalZ: goalZ})
	q.Pump(u, 40)

	n := q.Primary()[0]
	if n.Phase != 2 || n.DynamicGate != 0x1C00 {
		t.Fatalf("phase = %d gate = %#x, want phase 2 waiting on 0x1C00 [04 R-ORD-01 §3]", n.Phase, n.DynamicGate)
	}
	for _, idx := range []int{0, 1} {
		got := u.SlotAt(idx).Target
		if got.Kind != units.TargetGround {
			t.Fatalf("slot %d kind = %v, want a ground bind [04 R-ORD-01 §3]", idx, got.Kind)
		}
		if got.X != numeric.Fixed(120<<16) || got.Z != goalZ {
			t.Fatalf("slot %d position = (%v,%v), want the whole-unit goal (120,64) [04 R-ORD-01 §1]", idx, got.X, got.Z)
		}
	}
	if u.SlotAt(2).Target.Kind != units.TargetNone {
		t.Fatalf("slot 2 was bound; only p1 = 2 binds it [04 R-ORD-01 §3]")
	}
}

// TestKamikazeArrivalSpawnsTheImmediateSelfDestruct locks the arrival arm of
// `Attack_Kamikaze` [04 R-ORD-01 §3]: the movement layer's arrival bit `0x20`
// head-inserts a `SelfDestruct` carrying p1 = 1 — the no-countdown path of
// [04 R-ORD-01 §2] — and completes the kamikaze record. p1 is the whole
// difference between detonating now and starting a five-second countdown.
//
// Correction (WU-18-1, merge). This asserted the spawned record on the head of
// the PRIMARY segment. The head insert goes to "the front of the segment the
// record's rear-segment flag selects" [04 R-ORD-01 §1], and `SelfDestruct`
// carries that flag: its row ends "the record lives on the rear segment"
// [04 R-ORD-01 §2], and [04 R-SPEC-01 §13] says the same from the other side —
// the record "blocks nothing on the front segment". The segment is part of the
// contract, so it is asserted here and the spawn site is corrected with it.
// Only the primary walk is run, because the full pump would go on to dispatch
// the rear record in the same tick and free it again.
func TestKamikazeArrivalSpawnsTheImmediateSelfDestruct(t *testing.T) {
	id := Lookup("Attack_Kamikaze")
	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle, GoalX: u.X, GoalZ: u.Z})
	q.Pump(u, 40)

	n := q.Primary()[0]
	if n.DynamicGate&0xE0 != 0xE0 {
		t.Fatalf("gate = %#x, want the three movement outcomes armed [04 R-ORD-01 §3]", n.DynamicGate)
	}
	// "deadline 60" is the watchdog that re-issues the goal when none of those
	// three arrives; the deadline setter also ORs gate bit 0 [04 R-ORD-01 §1].
	// WU-18-4 could not form it because the handler had no tick; WU-18-7 gave
	// it one, and it is measured from the tick this pump ran on.
	if n.Deadline != 40+60 || n.DynamicGate&1 == 0 {
		t.Fatalf("deadline = %d gate = %#x, want 100 = tick 40 + 60 with gate bit 0 [04 R-ORD-01 §3]", n.Deadline, n.DynamicGate)
	}
	n.Satisfied |= 0x20 // the follower observes arrival [04 R-ORD-01 §0]
	q.pumpPrimary(u, 41)

	if q.LenPrimary() != 0 {
		t.Fatalf("primary length = %d, want the kamikaze freed and nothing left on the front segment [04 R-SPEC-01 §13]", q.LenPrimary())
	}
	if q.LenSecondary() != 1 {
		t.Fatalf("secondary length = %d, want the spawned rear-segment record [04 R-ORD-01 §1]", q.LenSecondary())
	}
	head := q.Secondary()[0]
	if DescriptorFor(head.ID).Name != "SelfDestruct" {
		t.Fatalf("rear head is %s, want the head-inserted SelfDestruct [04 R-ORD-01 §1]", DescriptorFor(head.ID).Name)
	}
	if head.Param1 != 1 {
		t.Fatalf("spawned p1 = %d, want 1 (immediate, no countdown) [04 R-ORD-01 §3]", head.Param1)
	}
}

// TestAirAttackEntryEndsOnTheManeuverLeash locks step 5 of the four air
// executors' shared entry sequence [04 R-AIR-01 §8]: the compare is on whole
// world units against the record's anchor pair and it is INCLUSIVE, so a unit
// exactly at the leash distance is already back-to-post. A strict compare here
// would let an aircraft sit one unit outside its post forever.
// Updated by WU-18-5. This test used to assert that BOTH outcomes free the
// record, because WU-18-4's `airAttackHandler` completed with a diagnostic once
// the entry sequence fell through — the placeholder that stood in for the phase
// legs. Those legs now exist (internal/movement/airorders.go) and vtolair.go
// claims the four descriptors ahead of combat.go, so the fall-through hands the
// record to them and it survives. The contract this test exists for is
// unchanged and is what is asserted below: the leash arm ends the order, one
// whole world unit further out does not.
func TestAirAttackEntryEndsOnTheManeuverLeash(t *testing.T) {
	for _, tc := range []struct {
		leash    uint32
		leashHit bool
	}{
		{leash: 30, leashHit: true},  // leash <= distance: return 5 from step 5
		{leash: 31, leashHit: false}, // still inside the leash: the entry falls through
	} {
		q, u := gateFixture()
		// gateFixture places the unit at (70, 90); an anchor at (70, 60) is
		// exactly 30 whole world units away.
		q.SetBinding(&QueueBinding{SimRNG: q.binding.SimRNG, Lookup: func(pool.Handle) *units.Unit { return u }})
		q.Push(Lookup("AirToAir"), Node{Owner: u.Handle, Target: 7, GuardX: 70, GuardY: 60, Param3: tc.leash})
		q.Pump(u, 40)

		freed := q.LenPrimary() == 0
		if freed != tc.leashHit {
			t.Fatalf("leash %d: order ended = %v, want %v [04 R-AIR-01 §8] step 5", tc.leash, freed, tc.leashHit)
		}
	}
}

// TestCombatFamilyNeverOverwritesAnotherInstaller keeps the registration seam
// honest: ensureCombatHandlers assigns only where the descriptor's Handler is
// still nil, so re-running the installer list — which the pump does on every
// walk — can never take a descriptor from the family that claimed it first
// (I1).
func TestCombatFamilyNeverOverwritesAnotherInstaller(t *testing.T) {
	id := Lookup("Attack_NoMove")
	probe := func(*units.Unit, *Node, uint32, uint32) Code { return Code(5) }
	restore := setHandler(id, probe)
	defer restore()

	ensureCombatHandlers()
	if DescriptorFor(id).Handler == nil {
		t.Fatal("installer cleared a descriptor it does not own")
	}
	// The probe completes unconditionally; the real handler cancels a record
	// with an out-of-range phase, so the two are distinguishable.
	if got := DescriptorFor(id).Handler(nil, &Node{Target: 1, Phase: 9}, 0, 0); got != Code(5) {
		t.Fatalf("installer overwrote an already-assigned handler: got code %d from the probe's slot", got)
	}
}

// TestInstallPointGoalRoutesRadius locks the two halves of [04 R-ORD-01 §1]'s
// point installer that the retired TODO(T25) had dropped: the arrival radius
// reaches the payload owner, and a `canfly` owner gets a release instead of an
// install.
func TestInstallPointGoalRoutesRadius(t *testing.T) {
	var installs []PointGoalRequest
	var releases []*Node
	adapter := &MovementGoalAdapter{
		InstallPoint: func(req PointGoalRequest) bool { installs = append(installs, req); return true },
		Release:      func(n *Node) bool { releases = append(releases, n); return true },
	}
	bind := &QueueBinding{Movement: adapter}

	ground := &units.Unit{Def: &content.UnitDef{UnitName: "ground"}}
	QueueForUnit(ground).SetBinding(bind)
	n := &Node{Owner: ground.Handle, Satisfied: 0x3E0}
	installPointGoal(ground, n, numeric.Fixed(7<<16), 0, numeric.Fixed(9<<16), 0x150)

	if len(installs) != 1 {
		t.Fatalf("installs = %d, want 1", len(installs))
	}
	if installs[0].Radius != 0x150 {
		t.Fatalf("radius = %#x, want 0x150 — the installer dropped it", installs[0].Radius)
	}
	if installs[0].Node != n {
		t.Fatalf("install carried the wrong node identity")
	}
	if n.Satisfied&0x3E0 != 0 {
		t.Fatalf("pending 0x20..0x200 not cleared: %#x", n.Satisfied)
	}
	if n.GoalX != numeric.Fixed(7<<16) || n.GoalZ != numeric.Fixed(9<<16) {
		t.Fatalf("goal triple not written for a ground owner")
	}
	if len(releases) != 0 {
		t.Fatalf("a ground owner must not take the release-only arm")
	}

	// canfly: release only, no install, and no goal triple write.
	flier := &units.Unit{Def: &content.UnitDef{UnitName: "flier", CanFly: true}}
	QueueForUnit(flier).SetBinding(bind)
	fn := &Node{Owner: flier.Handle, Satisfied: 0x3E0}
	installPointGoal(flier, fn, numeric.Fixed(7<<16), 0, numeric.Fixed(9<<16), 0x150)
	if len(installs) != 1 {
		t.Fatalf("canfly owner installed a point goal [04 R-ORD-01 §1]")
	}
	if len(releases) != 1 || releases[0] != fn {
		t.Fatalf("canfly owner did not release its previous payload")
	}
	if fn.Satisfied&0x3E0 != 0 {
		t.Fatalf("release-only arm must still clear pending 0x20..0x200: %#x", fn.Satisfied)
	}
	if fn.GoalX != 0 || fn.GoalZ != 0 {
		t.Fatalf("canfly owner must not write the goal triple")
	}
}
