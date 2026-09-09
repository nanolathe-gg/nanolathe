package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// combatArrivalFixture is the smallest stage on which an order handler can
// install a ground goal payload and the follower can answer it: one 1x1 mover
// standing on a flat cell, with the queue binding's movement port wired to the
// system's own installers and to the record-level release helper — the same two
// callbacks internal/session composes [04 R-ORD-01 §1].
func combatArrivalFixture(t *testing.T) (*System, *units.World, *units.Unit, *orders.Queue) {
	t.Helper()
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	x, z := world.CellToWorld(4), world.CellToWorld(4)
	h, err := w.Create(wiringDef(), 0, x, terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	u := w.Unit(h)
	sys.EnsureUnit(u)

	sim := rng.NewSimulation(0x12345677)
	q := orders.QueueForUnit(u)
	q.SetBinding(&orders.QueueBinding{
		SimRNG: &sim,
		Lookup: w.Unit,
		Movement: &orders.MovementGoalAdapter{
			Ready:        func() bool { return true },
			InstallPoint: sys.InstallPointGoal,
			Release:      sys.ReleaseGoalPayload,
		},
	})
	return sys, w, u, q
}

// TestAttackKamikazeArrivalRaisesTheSatisfiedBit is the probe for
// `Attack_Kamikaze` phase 1, whose only completion condition is satisfied
// `0x20`: "Phase 0 … point goal at the goal with radius max(16,
// kamikazedistance); deadline 60; gate |= 0xE0; advance. Phase 1: satisfied
// 0x20 → status 6 (`Arrived`), spawn SelfDestruct … complete"
// [04 R-ORD-01 §3].
//
// The row installs a point goal like any other, so the follower's per-tick
// service is its producer: "with a payload installed, ask it whether the unit
// has arrived; on arrival raise pending `0x20` on the owning record"
// [04 R-MOV-03 §2]. Nothing tested that composition — the row is not in the
// arrival handle's name list and reaches a handle only through the installed
// payload WU-19-90 made the condition.
//
// The mover starts on its own goal, so the arrival is the start-satisfied one
// and no search has to run for the probe to be about the handshake.
func TestAttackKamikazeArrivalRaisesTheSatisfiedBit(t *testing.T) {
	sys, w, u, q := combatArrivalFixture(t)
	q.Push(orders.Lookup("Attack_Kamikaze"), orders.Node{
		Owner: u.Handle, GoalX: u.X, GoalY: u.Y, GoalZ: u.Z, Deadline: -1,
	})
	head := q.Head()
	if head == nil {
		t.Fatal("no head record")
	}

	// Phase 0 is the installer. Its payload is what gives the record an arrival
	// handle at all [04 R-MOV-03 §2].
	q.Pump(u, 1)
	if head.Phase != 1 {
		t.Fatalf("phase %d after the first visit, want phase 1 [04 R-ORD-01 §3]", head.Phase)
	}
	if !sys.HasGroundGoal(u.Handle, head) {
		t.Fatal("Attack_Kamikaze phase 0 installed no ground payload, so the follower has nothing to ask [04 R-ORD-01 §3]")
	}
	if !sys.ActivateMove(u, head) {
		t.Fatal("activate move")
	}
	sys.Scheduler.Tick(1)
	runMovementTick(sys, 2, w)

	if head.Satisfied&arrivalSatisfiedBit == 0 {
		t.Fatalf("satisfied = %#x after a mover tick on the goal, want `0x20` set [04 R-MOV-03 §2][04 R-ORD-01 §0]", head.Satisfied)
	}
	// The same step detaches: the point class never persists past its own
	// arrival [04 R-PATH-01 §8].
	if sys.HasGroundGoal(u.Handle, head) {
		t.Fatal("the payload survived its own arrival [04 R-PATH-01 §8]")
	}

	// The bit is a completion, not a diagnostic: the next visit takes phase 1's
	// `0x20` arm and the record leaves the primary front.
	q.Pump(u, 3)
	if q.LenPrimary() > 0 && q.Primary()[0] == head {
		t.Fatalf("the kamikaze record is still the primary front at phase %d with satisfied %#x [04 R-ORD-01 §3]", head.Phase, head.Satisfied)
	}
}

// TestSuppressArrivalRaisesTheSatisfiedBit is the same probe for `Suppress`
// phase 2, which arms gate `0xE0` over an installed point goal: "phase 2 …
// p2 < 1 → re-arm; point goal at the goal radius p2, gate = 0xE0, p2 −=
// RNG(engagementDistance(p1) / 3), phase = 1, return 4 — each wake walks the
// unit closer by a random fraction of a third of its range"
// [04 R-ORD-01 §3].
//
// The row only reaches that walk-closer leg with a resolved weapon slot behind
// it, because p2 is the slot's engagement distance — the weapon's authored
// range [06 R-WPN-05 §1] — and a zero collapses phase 2 onto its own `p2 < 1`
// re-arm. The fixture therefore installs a weapon rather than seeding p2, so
// the probe covers the row as the pump reaches it.
func TestSuppressArrivalRaisesTheSatisfiedBit(t *testing.T) {
	sys, w, u, q := combatArrivalFixture(t)
	u.InstallWeapon(0, &content.WeaponDef{Range: 96})
	q.Push(orders.Lookup("Suppress"), orders.Node{
		Owner: u.Handle, GoalX: u.X, GoalY: u.Y, GoalZ: u.Z, Deadline: -1,
	})
	head := q.Head()
	if head == nil {
		t.Fatal("no head record")
	}

	// Phases 0 and 1 store the engagement distance and bind the slots, and
	// phase 1 arms gate `0x1C00` before advancing [04 R-ORD-01 §3]. The pump
	// then stops at that gate until one of its bits arrives [04 §3.3] step 3,
	// and the attack family's engage/disengage bits belong to doc 06's weapon
	// layer [04 R-ORD-01 §0], which this fixture does not run. The probe hands
	// the record one of the gated bits so that the visit under test — phase 2,
	// the installer — is reached at all; which producer raised it is not what
	// this probe is about.
	for tick := uint32(1); tick <= 4 && head.Phase != 2; tick++ {
		q.Pump(u, tick)
	}
	if head.Phase != 2 || head.DynamicGate != 0x1C00 {
		t.Fatalf("phase %d gate %#x before the install visit, want phase 2 behind gate 0x1C00 [04 R-ORD-01 §3]",
			head.Phase, head.DynamicGate)
	}
	head.Satisfied |= 0x1000
	q.Pump(u, 5)
	if !sys.HasGroundGoal(u.Handle, head) {
		t.Fatalf("Suppress installed no ground payload (phase %d, p2 %d): with none the follower has no arrival to answer [04 R-ORD-01 §3]",
			head.Phase, head.Param2)
	}
	if head.DynamicGate&arrivalGateMask == 0 {
		t.Fatalf("gate %#x after the install, want `0xE0` armed over it [04 R-ORD-01 §3]", head.DynamicGate)
	}
	if !sys.ActivateMove(u, head) {
		t.Fatal("activate move")
	}
	sys.Scheduler.Tick(9)
	runMovementTick(sys, 10, w)

	if head.Satisfied&arrivalSatisfiedBit == 0 {
		t.Fatalf("satisfied = %#x after a mover tick on the goal, want `0x20` set [04 R-MOV-03 §2][04 R-ORD-01 §0]", head.Satisfied)
	}
	if sys.HasGroundGoal(u.Handle, head) {
		t.Fatal("the payload survived its own arrival [04 R-PATH-01 §8]")
	}
}
