package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
)

// workFixture is a builder with every work capability, a bound queue carrying a
// seeded simulation stream and an economy service, and one finished target at
// the same position (so the reach test of [05 R-WORK-01 §2] passes).
func workFixture() (*Queue, *units.Unit, *units.Unit) {
	rng.SeedGlobal(1, 0)
	builderDef := &content.UnitDef{
		BMCode: true, Builder: true,
		CanCapture: true, CanReclamate: true, CanResurrect: true,
		WorkerTime: 300, BuildDistance: 1000, BuildTime: 100,
		FootprintX: 2, FootprintZ: 2, MaxDamage: 100,
	}
	targetDef := &content.UnitDef{
		MaxDamage: 100, BuildTime: 100, BuildCostEnergy: 0, BuildCostMetal: 0,
		FootprintX: 2, FootprintZ: 2, Builder: true, WorkerTime: 300,
	}
	builder := &units.Unit{
		Handle: 1, Def: builderDef, Alive: true, Activated: true, InBuildStance: true,
		Health: 100, MaxHealth: 100,
		X: numeric.Fixed(70 << 16), Y: numeric.Fixed(40 << 16), Z: numeric.Fixed(90 << 16),
	}
	target := &units.Unit{
		Handle: 2, Def: targetDef, Alive: true, Activated: true,
		Health: 100, MaxHealth: 100,
		X: numeric.Fixed(70 << 16), Y: numeric.Fixed(40 << 16), Z: numeric.Fixed(90 << 16),
	}
	target.Move.Mode = 1 // grounded: RepairUnit refuses any other mover mode [04 R-ORD-01 §5]
	q := &Queue{binding: &QueueBinding{
		SimRNG:           rng.Global.Sim,
		StockpileEconomy: &economy.Service{},
		Lookup: func(h pool.Handle) *units.Unit {
			if h == target.Handle {
				return target
			}
			if h == builder.Handle {
				return builder
			}
			return nil
		},
	}}
	q.SetBinding(q.binding)
	BindQueue(builder, q)
	return q, builder, target
}

// pumpUntilEmpty runs ticks until the primary segment drains, standing in for
// the two producers a work record waits on that this build has no seam for: the
// movement layer's arrival bit (nothing binds an arrival handle for a work
// descriptor, see work.go's goal-installer TODO(T25)) and the build-stance
// port. It reports the tick count so a caller can assert progress, not timing.
func pumpUntilEmpty(t *testing.T, q *Queue, u *units.Unit, limit uint32) uint32 {
	t.Helper()
	for tick := uint32(1); tick <= limit; tick++ {
		q.Pump(u, tick)
		if q.LenPrimary() == 0 {
			return tick
		}
		if head := q.Primary()[0]; head.DynamicGate&gateMoveOutcomes != 0 {
			head.Satisfied |= gateArrived // the mover reached the goal [04 R-ORD-01 §0]
		}
	}
	return limit
}

// TestWorkOrdersDispatchAndReachTheirTerminal is this unit's park test. Before
// WU-18-0 every one of these records sat at the head of its unit's queue on a
// gate nothing raises; before this unit they reached the pump's missing-handler
// arm and parked for 30..44 ticks per visit, forever. Each order must now
// dispatch on its first visit and walk its row to a terminal code, leaving the
// queue empty and no missing-handler diagnostic behind [04 §3.3][04 R-ORD-01 §5].
func TestWorkOrdersDispatchAndReachTheirTerminal(t *testing.T) {
	// A fixed slice, not a map: the assertions run in one order (I1).
	for _, name := range []string{
		"Capture", "HelpBuild", "Reclaim", "RepairUnit",
		"RepairUnitNoMove", "Resurrect", "SelfRepair",
	} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		if DescriptorFor(id).Handler == nil {
			t.Fatalf("%s has no handler after every installer ran", name)
		}
		q, builder, target := workFixture()
		q.Push(id, Node{Owner: builder.Handle, Target: target.Handle})
		head := q.Primary()[0]
		if head.DynamicGate != 0 {
			t.Fatalf("%s: fresh record waits on %#x [04 R-ORD-01 §1]", name, head.DynamicGate)
		}

		q.Pump(builder, 1)
		if head.Phase == 0 && q.LenPrimary() != 0 && head.DynamicGate == 0 {
			t.Fatalf("%s: first visit left the record untouched", name)
		}

		if ticks := pumpUntilEmpty(t, q, builder, 4000); q.LenPrimary() != 0 {
			t.Fatalf("%s: still queued after %d ticks at phase %d gate %#x", name, ticks, head.Phase, head.DynamicGate)
		}
		for _, d := range q.Diagnostics() {
			if strings.Contains(d, "no handler") {
				t.Fatalf("%s: parked on the missing-handler arm: %s", name, d)
			}
		}
	}
}

// TestRepairStepHealsOnePointAndBillsOneEnergy locks the repair helper's two
// clamps [05 R-WORK-01 §3]: both terms are replaced by exactly one whenever
// they are at least one, so an accepted visit is one health point and one
// energy unit however large `workertime` is. It also locks the refusal: a
// target already at full health commits nothing, which is the arm that lets
// phase 3 advance to the `Unit repaired` terminal.
func TestRepairStepHealsOnePointAndBillsOneEnergy(t *testing.T) {
	q, builder, target := workFixture()
	target.Health = 50

	if !repairStep(q, builder, target, workerQuantum(builder)) {
		t.Fatal("repair step refused a damaged target with energy carry non-positive")
	}
	if target.Health != 51 {
		t.Fatalf("health = %d after one accepted visit, want 51 (the heal term clamps to one)", target.Health)
	}
	buckets := q.StockpileEconomy.UnitBuckets(builder.Handle)
	if buckets[economy.Energy].Requested != 1 || buckets[economy.Energy].Accepted != 1 {
		t.Fatalf("energy requested/accepted = %v/%v, want 1/1 (the resource term clamps to one, billed to the BUILDER)",
			buckets[economy.Energy].Requested, buckets[economy.Energy].Accepted)
	}

	target.Health = target.Def.MaxDamage
	if repairStep(q, builder, target, workerQuantum(builder)) {
		t.Fatal("repair step committed against a target already at full health")
	}
}

// TestSelfRepairBillsTheRepairerAndHealsThePatient locks the reversed-argument
// identity that [05 R-WORK-01 §3] closed: the order lives on the PATIENT and
// its target is the repairer, so the repairer's energy is billed and the
// patient is healed — the opposite ends from `RepairUnit`.
func TestSelfRepairBillsTheRepairerAndHealsThePatient(t *testing.T) {
	q, patient, repairer := workFixture()
	patient.Health = 50

	n := &Node{Owner: patient.Handle, Target: repairer.Handle, Phase: 1}
	// The row's hold is code 2 with an exact one-tick deadline; before WU-18-7
	// the handler had no tick and returned the pump's own 30..44 wait (code 3)
	// instead [04 R-ORD-01 §5].
	if code := selfRepairHandler(patient, n, 0, 700); code != 2 {
		t.Fatalf("phase 1 returned %d, want the row's hold [04 R-ORD-01 §2]", code)
	}
	if n.Deadline != 701 || n.DynamicGate&1 == 0 {
		t.Fatalf("deadline = %d gate = %#x, want 701 with gate bit 0: `deadline 1` from the tick the handler ran on [04 R-ORD-01 §1]", n.Deadline, n.DynamicGate)
	}
	if patient.Health != 51 {
		t.Fatalf("patient health = %d, want 51", patient.Health)
	}
	if b := q.StockpileEconomy.UnitBuckets(repairer.Handle); b[economy.Energy].Requested != 1 {
		t.Fatalf("repairer energy requested = %v, want 1: SelfRepair bills the repairer, not the patient", b[economy.Energy].Requested)
	}
	if b := q.StockpileEconomy.UnitBuckets(patient.Handle); b[economy.Energy].Requested != 0 {
		t.Fatalf("patient energy requested = %v, want 0", b[economy.Energy].Requested)
	}
}

// TestCaptureBudgetFollowsTheTracedArithmetic locks three relationships of
// [05 R-WORK-01 §6] rather than a census of values: at full health with no
// kills the middle step is the identity so the timer equals `base`; a target at
// half health takes proportionally less; and the clamp on `base` is an upper
// clamp only, at 1800.
//
// The first relationship is also where §6's closing gloss disagrees with its
// own instruction listing — it calls the full-health timer "one tenth of base".
// The arithmetic is asserted; the gloss is reported, not implemented.
func TestCaptureBudgetFollowsTheTracedArithmetic(t *testing.T) {
	const maxDamage = 100
	full := captureBudget(0, 0, maxDamage, maxDamage, 0)
	if full != 150 { // base = trunc(150.0) and the last step is the identity
		t.Fatalf("full-health timer = %d, want 150 = base [05 R-WORK-01 §6]", full)
	}
	half := captureBudget(0, 0, maxDamage/2, maxDamage, 0)
	if half != full*3/4 { // (health + maxdamage) / (2·maxdamage) = 3/4
		t.Fatalf("half-health timer = %d, want %d = base·(health+max)/(2·max)", half, full*3/4)
	}
	if veteran := captureBudget(0, 0, maxDamage, maxDamage, 10); veteran <= full {
		t.Fatalf("veteran timer = %d, want more than %d: the experience factor has no cap", veteran, full)
	}
	// Upper clamp only: an energy cost large enough to pass 1800 saturates.
	if clamped := captureBudget(1000000, 0, maxDamage, maxDamage, 0); clamped != 1800 {
		t.Fatalf("clamped timer = %d, want 1800", clamped)
	}
}

// TestCaptureProgressAdvancesTwoPerVisit locks the progress phase's shape
// [05 R-WORK-01 §6]: the completion test precedes the increment, so the number
// of qualifying visits is ceil(timer/2) and the counter never decays.
func TestCaptureProgressAdvancesTwoPerVisit(t *testing.T) {
	_, builder, target := workFixture()
	n := &Node{Owner: builder.Handle, Target: target.Handle, Phase: 4, Param2: 7}
	visits := 0
	for n.Phase == 4 && visits < 100 {
		code := captureHandler(builder, n, 0, 0)
		if code == 1 {
			break
		}
		visits++
	}
	if want := (7 + 1) / 2; visits != want {
		t.Fatalf("qualifying visits = %d, want ceil(7/2) = %d", visits, want)
	}
	if n.Param1 != uint32(visits*2) {
		t.Fatalf("progress = %d after %d visits, want %d", n.Param1, visits, visits*2)
	}
}

// TestBuildRangeTestSubtractsBothFootprints locks [05 R-WORK-01 §2]'s reach
// test as a relationship: the admitted centre distance is `builddistance` plus
// both ends' half-footprint diagonals, and the comparison is inclusive.
func TestBuildRangeTestSubtractsBothFootprints(t *testing.T) {
	_, builder, target := workFixture()
	builder.Def.BuildDistance = 100
	// Both units are 2x2, so each pad is trunc(8·hypot(2,2)) = trunc(22.62) = 22.
	pad := footprintPad(2, 2)
	if pad != 22 {
		t.Fatalf("footprint pad = %d, want 22 = trunc(8·hypot(2,2))", pad)
	}
	edge := int64(100+2*int(pad)) << 16
	target.X = builder.X + numeric.Fixed(edge)
	if !inBuildRangeOf(builder, target) {
		t.Fatal("a separation of exactly builddistance + both pads must be in reach: the compare is inclusive")
	}
	target.X = builder.X + numeric.Fixed(edge+(1<<16))
	if inBuildRangeOf(builder, target) {
		t.Fatal("one world unit beyond the sum must be out of reach")
	}
}
