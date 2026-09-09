package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The ground guard of [04 R-ORD-01 §8]. Every number asserted here is that
// section's: the follow radius is `(FootPrintX(me) + FootPrintX(ward) + 2)·16`
// computed by the handler, the anchor direction is ONE `RNG(65536)` taken at
// admit, the record's goal triple holds the resulting OFFSET, the payload is a
// point goal at `ward + offset` with radius `p1 / 2`, and the cadence is a
// fixed `tick + 30` behind gate `0x19`.

type guardFixture struct {
	guard    *units.Unit
	ward     *units.Unit
	sim      *rng.Simulation
	installs []PointGoalRequest
	annuli   []AnnulusGoalRequest
	releases int
}

// newGuardFixture builds a guard whose footprint X is guardFoot and a ward
// whose footprint X is wardFoot, both resolvable through the guard's queue
// binding, with a movement adapter that records every payload install.
func newGuardFixture(t *testing.T, guardFoot, wardFoot int32) *guardFixture {
	t.Helper()
	f := &guardFixture{}
	f.guard = &units.Unit{
		Handle: 1,
		Def:    &content.UnitDef{UnitName: "guard", FootprintX: guardFoot, FootprintZ: guardFoot, MaxDamage: 100},
		Alive:  true,
		X:      numeric.Fixed(200 << 16),
		Z:      numeric.Fixed(300 << 16),
	}
	f.guard.MaxHealth, f.guard.Health = 100, 100
	f.ward = &units.Unit{
		Handle: 2,
		Def:    &content.UnitDef{UnitName: "ward", FootprintX: wardFoot, FootprintZ: wardFoot, MaxDamage: 100},
		Alive:  true,
		X:      numeric.Fixed(500 << 16),
		Y:      numeric.Fixed(11 << 16),
		Z:      numeric.Fixed(700 << 16),
	}
	// A finished, undamaged ward with no order of its own declines every
	// assist leg, so each visit reaches the follow maintenance.
	f.ward.MaxHealth, f.ward.Health = 100, 100
	f.ward.Remaining = 0

	sim := rng.NewSimulation(7)
	f.sim = &sim
	binding := &QueueBinding{
		SimRNG: f.sim,
		Lookup: func(h pool.Handle) *units.Unit {
			if h == 2 {
				return f.ward
			}
			return nil
		},
		Hostility: func(_, _ *units.Unit) bool { return false },
		Movement: &MovementGoalAdapter{
			InstallPoint: func(req PointGoalRequest) bool {
				f.installs = append(f.installs, req)
				return true
			},
			InstallAnnulus: func(req AnnulusGoalRequest) bool {
				f.annuli = append(f.annuli, req)
				return true
			},
			Release: func(*Node) bool { f.releases++; return true },
		},
	}
	QueueForUnit(f.guard).SetBinding(binding)
	QueueForUnit(f.ward).SetBinding(&QueueBinding{})
	return f
}

func guardNode(f *guardFixture) *Node {
	return &Node{ID: Lookup("Follow_Ground"), Owner: f.guard.Handle, Target: f.ward.Handle, Deadline: -1}
}

// TestGuardAdmitDrawsOnceAndStoresAnOffset locks points 1 and 2 of
// [04 R-ORD-01 §8]: the radius is computed from the two footprint X words with
// no issuer input, one full-circle draw is taken, and the goal triple receives
// `(−sin(h)·r, 0, −cos(h)·r)` as an offset from the ward.
func TestGuardAdmitDrawsOnceAndStoresAnOffset(t *testing.T) {
	f := newGuardFixture(t, 2, 4)
	n := guardNode(f)
	n.Param1 = 999 // whatever the issuer stored survives only until admit runs

	// The same stream, drawn independently, gives the direction the handler
	// must have used — the assertion is on the value, not on a re-draw.
	shadow := rng.NewSimulation(7)
	wantAngle := numeric.Angle(uint16(shadow.Uint32n(0x10000)))

	before := f.sim.Draws()
	if code := guardHandler(f.guard, n, 0, 100); code != Code(1) {
		t.Fatalf("admit want *advance* (1) got %d", code)
	}
	if got := f.sim.Draws() - before; got != 1 {
		t.Fatalf("admit draws = %d, want exactly one RNG(65536) [04 R-ORD-01 §8 point 2][I4]", got)
	}
	// s = 2 + 4 + 2 = 8 cells; p1 = 8 · 16 = 128 world units.
	if n.Param1 != 128 {
		t.Fatalf("p1 = %d, want (2+4+2)·16 = 128", n.Param1)
	}
	r := int32(128) << 16
	wantX := -numeric.Fixed(numeric.MulRound(numeric.Sin(wantAngle), r))
	wantZ := -numeric.Fixed(numeric.MulRound(numeric.Cos(wantAngle), r))
	if n.GoalX != wantX || n.GoalZ != wantZ {
		t.Fatalf("offset = (%v, %v), want (−sin·r, −cos·r) = (%v, %v)", n.GoalX, n.GoalZ, wantX, wantZ)
	}
	if n.GoalY != 0 {
		t.Fatalf("the offset's Y term is zero [04 R-ORD-01 §8 point 2], got %v", n.GoalY)
	}
	// Both components come from the sine table, so the offset's magnitude is r
	// up to the table's own quantisation (entries are round(8192·sin), giving a
	// magnitude error under r/8192).
	mag2 := int64(n.GoalX.Raw())*int64(n.GoalX.Raw()) + int64(n.GoalZ.Raw())*int64(n.GoalZ.Raw())
	want2 := int64(r) * int64(r)
	if diff := mag2 - want2; diff > want2/4096 || diff < -want2/4096 {
		t.Fatalf("offset magnitude² = %d, want ≈ r² = %d [04 R-ORD-01 §8 point 2]", mag2, want2)
	}
	// It is an offset, not a position: it cannot be the ward's own place.
	if n.GoalX == f.ward.X && n.GoalZ == f.ward.Z {
		t.Fatal("the guard's goal triple holds an offset from the ward, not a position")
	}
}

// TestGuardMaintenanceInstallsThePointGoalAndDrawsNothing locks points 3 and 4:
// the payload is a point goal at ward+offset with radius p1/2, the deadline is
// a fixed tick+30, the gate leaves as 0x19, and maintenance takes no draw.
func TestGuardMaintenanceInstallsThePointGoalAndDrawsNothing(t *testing.T) {
	f := newGuardFixture(t, 1, 1)
	n := guardNode(f)
	if code := guardHandler(f.guard, n, 0, 100); code != Code(1) {
		t.Fatalf("admit want *advance* (1) got %d", code)
	}
	n.Phase = 1 // the pump's code-1 effect [04 §3.3]
	offX, offZ := n.GoalX, n.GoalZ

	afterAdmit := f.sim.Draws()
	n.DynamicGate = 0 // the pump wipes the gate before every dispatch [04 §3.3]
	if code := guardHandler(f.guard, n, 0, 100); code != Code(2) {
		t.Fatalf("maintenance want *hold* (2) got %d", code)
	}
	if got := f.sim.Draws() - afterAdmit; got != 0 {
		t.Fatalf("maintenance drew %d times; the direction is drawn once at admit [04 R-ORD-01 §8 point 4][I4]", got)
	}
	if len(f.annuli) != 0 {
		t.Fatalf("the guard installs a POINT goal; the annulus installer is never called [04 R-ORD-01 §8 point 3]")
	}
	if len(f.installs) != 1 {
		t.Fatalf("installs = %d, want 1 point goal", len(f.installs))
	}
	got := f.installs[0]
	if got.X != f.ward.X+offX || got.Z != f.ward.Z+offZ {
		t.Fatalf("goal = (%v, %v), want ward + offset = (%v, %v)", got.X, got.Z, f.ward.X+offX, f.ward.Z+offZ)
	}
	// s = 1 + 1 + 2 = 4, p1 = 64, radius = p1/2 = 32.
	if n.Param1 != 64 || got.Radius != 32 {
		t.Fatalf("p1 = %d radius = %d, want 64 and p1/2 = 32 [04 R-ORD-01 §8 point 3]", n.Param1, got.Radius)
	}
	if n.GoalX != offX || n.GoalZ != offZ {
		t.Fatalf("the install overwrote the record's stored offset: %v/%v", n.GoalX, n.GoalZ)
	}
	if n.Deadline != 130 {
		t.Fatalf("deadline = %d, want a fixed tick+30 = 130", n.Deadline)
	}
	if n.DynamicGate != 0x19 {
		t.Fatalf("gate = %#x, want 0x19 — 0x18 ORed over the deadline setter's bit 0", n.DynamicGate)
	}
	if n.DynamicGate&0x60 != 0 {
		t.Fatalf("arrival 0x20 and path failure 0x40 are NOT gated [04 R-ORD-01 §8 point 4]")
	}
	if n.Phase != 1 {
		t.Fatalf("maintenance leaves the phase at 1, got %d", n.Phase)
	}
}

// TestGuardReissuesEveryThirtyTicksThroughThePump runs the record through the
// real pump and asserts the cadence property the gate produces: the goal is
// re-issued 30 ticks later even when the guard has ARRIVED, because arrival is
// not among the bits the record waits on [04 R-ORD-01 §8 point 4].
func TestGuardReissuesEveryThirtyTicksThroughThePump(t *testing.T) {
	f := newGuardFixture(t, 1, 1)
	q := QueueForUnit(f.guard)
	q.Push(Lookup("Follow_Ground"), Node{Owner: f.guard.Handle, Target: f.ward.Handle})

	q.Pump(f.guard, 100) // admit, then the same cascade re-enters phase 1
	n := q.Primary()[0]
	if n.Phase != 1 || len(f.installs) != 1 {
		t.Fatalf("first pump: phase %d installs %d, want phase 1 and one install", n.Phase, len(f.installs))
	}
	if n.Deadline != 130 {
		t.Fatalf("deadline = %d, want 130", n.Deadline)
	}

	// Arrival lands. The record is not waiting on it, so nothing happens.
	n.Satisfied |= 0x20
	q.Pump(f.guard, 110)
	if len(f.installs) != 1 {
		t.Fatalf("an arrival must not re-dispatch the guard: installs = %d", len(f.installs))
	}

	drawsBeforeReissue := f.sim.Draws()
	q.Pump(f.guard, 130) // the deadline expires
	if len(f.installs) != 2 {
		t.Fatalf("installs = %d after the 30-tick deadline, want the goal re-issued", len(f.installs))
	}
	if got := f.sim.Draws() - drawsBeforeReissue; got != 0 {
		t.Fatalf("the re-issue drew %d times, want none [I4]", got)
	}
	if f.installs[1].X != f.installs[0].X || f.installs[1].Z != f.installs[0].Z {
		t.Fatalf("the re-issue moved the anchor; the direction is drawn once")
	}
	if n.Deadline != 160 || n.DynamicGate != 0x19 {
		t.Fatalf("re-issue want deadline 160 gate 0x19 got %d/%#x", n.Deadline, n.DynamicGate)
	}
}

// TestGuardNoMoveInstallsNoGoal is [04 R-ORD-01 §8 point 5]: the stationary
// guard is a different order, not a follow variant. It calls no goal installer
// of any kind and never moves the unit.
func TestGuardNoMoveInstallsNoGoal(t *testing.T) {
	f := newGuardFixture(t, 1, 1)
	// A weapon on slot 0 so the row's binds are meaningful.
	f.guard.SlotAt(0).Weapon = &content.WeaponDef{Range: 180}
	q := QueueForUnit(f.guard)
	if DescriptorFor(Lookup("Guard_NoMove")).Handler == nil {
		t.Fatal("Guard_NoMove has no handler")
	}
	startX, startZ := f.guard.X, f.guard.Z
	q.Push(Lookup("Guard_NoMove"), Node{Owner: f.guard.Handle, Target: f.ward.Handle})

	for tick := uint32(100); tick <= 400; tick += 5 {
		q.Pump(f.guard, tick)
	}
	if len(f.installs) != 0 || len(f.annuli) != 0 {
		t.Fatalf("Guard_NoMove installed %d point and %d banded goals, want none [04 R-ORD-01 §8 point 5]",
			len(f.installs), len(f.annuli))
	}
	if f.guard.X != startX || f.guard.Z != startZ {
		t.Fatal("Guard_NoMove moved the unit")
	}
	if q.LenPrimary() != 1 {
		t.Fatalf("the stationary guard should still be running, got %d records", q.LenPrimary())
	}
}

// TestGuardNoMovePhases walks the row of [04 R-ORD-01 §3] a phase at a time:
// phase 0 inhibits and waits 30, phase 1 binds slot 0's target and seeds the
// attempt budget with RNG(3)+3, phase 2 counts attempts, phase 3 picks from the
// 640-unit scan.
func TestGuardNoMovePhases(t *testing.T) {
	f := newGuardFixture(t, 1, 1)
	f.guard.SlotAt(0).Weapon = &content.WeaponDef{Range: 180}
	n := &Node{ID: Lookup("Guard_NoMove"), Owner: f.guard.Handle, Deadline: -1}

	if code := guardNoMoveHandler(f.guard, n, 0, 50); code != Code(1) {
		t.Fatalf("phase 0 want *advance* got %d", code)
	}
	if n.Deadline != 80 {
		t.Fatalf("phase 0 deadline = %d, want tick+30 = 80", n.Deadline)
	}

	// Phase 1 with no slot target: deadline 30, hold, and no budget drawn.
	n.Phase = 1
	before := f.sim.Draws()
	if code := guardNoMoveHandler(f.guard, n, 0, 80); code != Code(2) {
		t.Fatalf("phase 1 with no bound target want *hold* got %d", code)
	}
	if f.sim.Draws() != before {
		t.Fatal("phase 1 must not draw when slot 0 holds nothing")
	}

	// Phase 1 with a live slot target: the record binds it and draws RNG(3)+3.
	f.guard.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: f.ward.Handle}
	before = f.sim.Draws()
	if code := guardNoMoveHandler(f.guard, n, 0, 80); code != Code(1) {
		t.Fatalf("phase 1 with a live target want *advance* got %d", code)
	}
	if got := f.sim.Draws() - before; got != 1 {
		t.Fatalf("phase 1 draws = %d, want exactly one RNG(3)", got)
	}
	if n.Target != f.ward.Handle {
		t.Fatalf("the record's smart-reference was not bound to slot 0's target")
	}
	if n.GoalX != f.ward.X || n.GoalZ != f.ward.Z {
		t.Fatal("phase 1 copies the target's position into the record's goal")
	}
	if n.Param1 != 0 || n.Param2 < 3 || n.Param2 > 5 {
		t.Fatalf("p1/p2 = %d/%d, want 0 and RNG(3)+3 in 3..5", n.Param1, n.Param2)
	}
}

// TestChaseRadiiComeFromTheWeaponRange locks [04 R-ORD-01 §8 point 6] with
// [06 R-WPN-05 §1]: every maneuver radius is a factor of the slot's authored
// `range`, and there is no per-substate constant. `d` is odd so the two
// truncating divisions cannot be mistaken for rounding ones (I3).
func TestChaseRadiiComeFromTheWeaponRange(t *testing.T) {
	const d int32 = 333 // odd: d/2 = 166 and d/4 = 83, both truncated toward zero
	f := newGuardFixture(t, 1, 1)
	f.guard.SlotAt(0).Weapon = &content.WeaponDef{Range: d}
	f.guard.Flags |= units.ArmedStatus
	target := f.ward
	target.Y = f.guard.Y // no vertical separation: the strafe arm stays at 1

	n := &Node{ID: Lookup("Attack_Chase"), Owner: f.guard.Handle, Target: target.Handle, Phase: 2, Deadline: -1}

	// Substate 0: point goal at the target, radius d.
	n.Param2 = 0
	chaseManeuver(f.guard, n)
	if got := f.installs[len(f.installs)-1]; got.Radius != d || got.X != target.X {
		t.Fatalf("substate 0 radius = %d at %v, want d = %d at the target", got.Radius, got.X, d)
	}
	if n.Param2 != 1 {
		t.Fatalf("substate 0 advances p2 to 1, got %d", n.Param2)
	}

	// Substate 5: point goal at the target, radius d/2 truncated.
	n.Param2 = 5
	chaseManeuver(f.guard, n)
	if got := f.installs[len(f.installs)-1]; got.Radius != 166 {
		t.Fatalf("substate 5 radius = %d, want trunc(d/2) = 166", got.Radius)
	}

	// The strafe arm (1..4): radius trunc(d/4), and p2 is unchanged.
	n.Param2 = 1
	pointsBefore := len(f.installs)
	chaseManeuver(f.guard, n)
	if got := f.installs[len(f.installs)-1]; got.Radius != 83 {
		t.Fatalf("strafe radius = %d, want trunc(d/4) = 83", got.Radius)
	}
	if n.Param2 != 1 {
		t.Fatalf("the strafe arm leaves p2 alone, got %d", n.Param2)
	}
	if len(f.installs) != pointsBefore+1 {
		t.Fatalf("the strafe arm installs exactly one point goal")
	}

	// Substate 6: point goal at the target, radius 0.
	n.Param2 = 6
	chaseManeuver(f.guard, n)
	if got := f.installs[len(f.installs)-1]; got.Radius != 0 || got.X != target.X || got.Z != target.Z {
		t.Fatalf("substate 6 want radius 0 at the target, got %d at %v/%v", got.Radius, got.X, got.Z)
	}

	// Substates 7 and 8 are the two banded goals: (d, d/2) and (2d, d).
	n.Param2 = 7
	chaseManeuver(f.guard, n)
	if got := f.annuli[len(f.annuli)-1]; got.OuterRadius != d || got.InnerRadius != d/2 {
		t.Fatalf("substate 7 band = (%d, %d), want (d, trunc(d/2)) = (%d, %d)", got.OuterRadius, got.InnerRadius, d, d/2)
	}
	n.Param2 = 8
	chaseManeuver(f.guard, n)
	if got := f.annuli[len(f.annuli)-1]; got.OuterRadius != 2*d || got.InnerRadius != d {
		t.Fatalf("substate 8 band = (%d, %d), want (2d, d) = (%d, %d)", got.OuterRadius, got.InnerRadius, 2*d, d)
	}
	if n.Param2 != 0 {
		t.Fatalf("substate 8 wraps p2 to 0, got %d", n.Param2)
	}

	// No radius in the family is a constant of its own: change the authored
	// range and every one of them moves with it.
	f.guard.SlotAt(0).Weapon = &content.WeaponDef{Range: 2 * d}
	n.Param2 = 0
	chaseManeuver(f.guard, n)
	if got := f.installs[len(f.installs)-1]; got.Radius != 2*d {
		t.Fatalf("substate 0 radius = %d after doubling the weapon range, want %d", got.Radius, 2*d)
	}
}
