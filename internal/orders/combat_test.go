package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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

	// The engage bit arrives: phase 2 inhibits all three slots and re-arms.
	// Which bit reaches phase 2 matters — the pre-check eats `0x8`, `0x10000`
	// AND `0x800` before the phase switch ever runs, so `0x1000` is the only
	// one of the gate's four that phase 2 ever sees [04 R-ORD-01 §3].
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
// point installer that the retired accepted-placeholder marker had dropped: the arrival radius
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

// TestKamikazeDeadlineIsFormedFromThePumpTick locks `Attack_Kamikaze`'s
// "deadline 60" [04 R-ORD-01 §3] against the failure mode WU-18-4 named when it
// declined to fabricate a tick base for a handler that had none.
//
// The row's phase-1 else-arm is `phase = 0`, *hold*. Its watchdog is the
// deadline the deadline setter stores as `current tick + 60`, plus the gate bit
// 0 the setter ORs in [04 R-ORD-01 §1]. Formed from the real tick, the deadline
// lies in the future, so the record blocks on gate 0xE1 and the walk ends
// ([04 §3.3] step 3). Formed from a fabricated base — 0, or a per-queue counter
// that has not moved — the deadline is already in the past, step 1 hands the
// record bit 0 back on the very next look, phase 1 resets it to phase 0, and
// phase 0 re-arms the same expired deadline: the pump spins inside one visit.
//
// The property that separates the two is asserted directly: the deadline is
// strictly ahead of the tick the record ran on.
func TestKamikazeDeadlineIsFormedFromThePumpTick(t *testing.T) {
	const tick uint32 = 1000 // well past 60: a fabricated base of 0 is already expired here
	id := Lookup("Attack_Kamikaze")
	q, u := gateFixture()
	q.Push(id, Node{Owner: u.Handle, Target: 7})
	q.Pump(u, tick)

	n := q.Primary()[0]
	if n.Phase != 1 {
		t.Fatalf("phase = %d after the first pump, want 1: phase 0 advances [04 R-ORD-01 §3]", n.Phase)
	}
	if n.Deadline != int32(tick+60) {
		t.Fatalf("deadline = %d, want exactly tick+60 = %d [04 R-ORD-01 §3][04 R-ORD-01 §1]", n.Deadline, tick+60)
	}
	if n.Deadline <= int32(tick) {
		t.Fatalf("deadline %d is not ahead of the tick %d — a fabricated base spins the pump", n.Deadline, tick)
	}
	if n.DynamicGate != 0xE1 {
		t.Fatalf("gate = %#x, want 0xE1: the three movement bits plus the deadline setter's bit 0", n.DynamicGate)
	}

	// The walk is blocked, not held: a second pump on the same tick changes
	// nothing and cannot reach the handler [04 §3.3] step 3.
	q.Pump(u, tick)
	if n.Phase != 1 || n.Deadline != int32(tick+60) || n.DynamicGate != 0xE1 {
		t.Fatalf("a blocked head was re-dispatched: phase %d deadline %d gate %#x", n.Phase, n.Deadline, n.DynamicGate)
	}

	// The watchdog fires: the movement layer reported nothing, so phase 1 takes
	// its else-arm, resets to phase 0 and holds. The hold reloads the head
	// [04 R-ORD-01 §10], so phase 0 re-issues the goal in the SAME pass and
	// advances back to phase 1 with a deadline formed from this tick; the next
	// reload finds gate 0xE1 unsatisfied and the pass ends. That is §3.3's
	// cascade, bounded by exactly one round trip — and it is the property this
	// test exists for: with a fabricated tick base the fresh deadline would be
	// in the past, step 1 would hand bit 0 straight back, and the cascade would
	// never end.
	//
	// Before WU-19-73 the hold advanced a cursor instead of reloading the head,
	// so the pass ended with the record parked at phase 0.
	q.Pump(u, tick+60)
	if n.Phase != 1 {
		t.Fatalf("phase = %d after the deadline expired, want 1: phase 1 resets to 0, the reload re-issues the goal and advances [04 R-ORD-01 §3][04 R-ORD-01 §10]", n.Phase)
	}
	if n.Deadline != int32(tick+60+60) {
		t.Fatalf("deadline = %d after the watchdog, want tick+60+60 = %d: the re-issue forms it from the pump tick", n.Deadline, tick+120)
	}
	if n.DynamicGate != 0xE1 {
		t.Fatalf("gate = %#x after the watchdog, want 0xE1", n.DynamicGate)
	}
}

// TestAttackUTypeAcquiresTheAuthoredType is the fixture for the mission `a
// <typename>` verb of [04 §3.6]. `internal/mission`'s handleA queues an
// `AttackUType` record whose p1 is the catalog index of the authored type and
// whose target is empty; [04 R-ORD-01 §3] then makes phase 1 "scan every live
// unit from the second slot on whose definition index equals p1 and whose owner
// is hostile to mine", score each `d² − RNG(d²/2)`, keep the lowest (ties → the
// later unit), resolve command code 3 against it and spawn the resolved attack
// at the head.
//
// The identity the verb depends on is asserted first, because it is the half
// that fails silently: the mission verb stores Catalog.UnitDefIndex(key) and the
// scan compares UnitDef.UnitDefID. Both are the 1-based position of the
// canonical key in sorted order, so they must agree for any compiled catalog —
// and if they ever stop agreeing, an authored `a ARMPW` acquires nothing at all.
//
// The test lives in this package because internal/mission's own test files are
// not this unit's to write; the record it pushes is the one handleA builds.
func TestAttackUTypeAcquiresTheAuthoredType(t *testing.T) {
	hunter := &content.UnitDef{UnitName: "hunter", MaxDamage: 100, CanAttack: true}
	prey := &content.UnitDef{UnitName: "prey", MaxDamage: 100}
	other := &content.UnitDef{UnitName: "other", MaxDamage: 100}
	catalogUnits := map[string]*content.UnitDef{
		content.CanonicalKey("hunter"): hunter,
		content.CanonicalKey("prey"):   prey,
		content.CanonicalKey("other"):  other,
	}
	registry, err := content.CompileCategories(catalogUnits)
	if err != nil {
		t.Fatalf("compile categories: %v", err)
	}
	cat := &content.Catalog{Units: catalogUnits, Categories: registry}

	// productIdentity's arithmetic, verbatim from internal/mission.
	idx, ok := cat.UnitDefIndex(content.CanonicalKey("prey"))
	if !ok {
		t.Fatal("the authored type is not in the catalog")
	}
	if idx != prey.UnitDefID {
		t.Fatalf("Catalog.UnitDefIndex = %d but UnitDef.UnitDefID = %d: the mission verb and the scan would name different definitions [04 §3.6][04 R-ORD-01 §3]", idx, prey.UnitDefID)
	}

	q, u := gateFixture()
	u.Def = hunter
	u.Owner = 0
	u.Alive = true
	u.Flags |= units.ArmedStatus // the armed branch of code 3 [R-ORD-02 §1]

	friendlyPrey := &units.Unit{Handle: 2, Owner: 0, Def: prey, Alive: true, X: numeric.Fixed(80 << 16), Z: numeric.Fixed(90 << 16)}
	hostileOther := &units.Unit{Handle: 3, Owner: 1, Def: other, Alive: true, X: numeric.Fixed(90 << 16), Z: numeric.Fixed(90 << 16)}
	hostilePrey := &units.Unit{Handle: 4, Owner: 1, Def: prey, Alive: true, X: numeric.Fixed(300 << 16), Z: numeric.Fixed(90 << 16)}
	livePool := []*units.Unit{u, friendlyPrey, hostileOther, hostilePrey}

	q.binding.Hostility = func(actor, candidate *units.Unit) bool { return actor.Owner != candidate.Owner }
	q.binding.Lookup = func(h pool.Handle) *units.Unit {
		for _, candidate := range livePool {
			if candidate.Handle == h {
				return candidate
			}
		}
		return nil
	}
	q.binding.World = &WorldQueryAdapter{
		ForEachUnit: func(visit func(pool.Handle, *units.Unit) bool) {
			for _, candidate := range livePool {
				if visit(candidate.Handle, candidate) {
					return
				}
			}
		},
	}

	id := Lookup("AttackUType")
	q.Push(id, Node{Owner: u.Handle, BuildDefKey: content.CanonicalKey("prey"), Param1: idx})
	hunt := q.Primary()[0]

	// Phase 0 arms `deadline RNG(90) + 1` and blocks on its gate bit 0.
	const tick uint32 = 500
	q.Pump(u, tick)
	if hunt.Phase != 1 || hunt.DynamicGate != 1 {
		t.Fatalf("after phase 0: phase %d gate %#x, want phase 1 waiting on gate bit 0 [04 R-ORD-01 §3]", hunt.Phase, hunt.DynamicGate)
	}
	if hunt.Deadline < int32(tick+1) || hunt.Deadline > int32(tick+90) {
		t.Fatalf("deadline %d outside tick+1..tick+90 [04 R-ORD-01 §3]", hunt.Deadline)
	}

	// The deadline expires and phase 1 scans.
	q.Pump(u, uint32(hunt.Deadline))
	head := q.Primary()[0]
	if head == hunt {
		t.Fatalf("phase 1 spawned nothing: the hunt is still the head record")
	}
	if head.Target != hostilePrey.Handle {
		t.Fatalf("spawned attack targets %d, want the hostile unit of the authored type (%d)", head.Target, hostilePrey.Handle)
	}
	if name := DescriptorFor(head.ID).Name; name != "Attack_Chase" {
		t.Fatalf("spawned descriptor %q, want the resolver's answer for command code 3 against a unit", name)
	}
}
