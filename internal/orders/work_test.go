package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
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

// ---------------------------------------------------------------------------
// Build assistance (PT3-04) [05 R-WORK-01 §1][05 "Two-stage settlement algorithm"]
// ---------------------------------------------------------------------------

// assistFixture is one nanoframe and `n` builders, all standing on it so the
// reach test of [05 R-WORK-01 §2] passes, each with its own bound queue and all
// sharing one economy service. The i-th builder's `workertime` is
// `30 * quanta[i]`, so its quantum is exactly `quanta[i]`.
func assistFixture(quanta ...int32) (*economy.Service, []*units.Unit, *units.Unit) {
	rng.SeedGlobal(1, 0)
	econ := &economy.Service{}
	frameDef := &content.UnitDef{
		MaxDamage: 100, BuildTime: 100, BuildCostEnergy: 200, BuildCostMetal: 100,
		FootprintX: 2, FootprintZ: 2,
	}
	frame := &units.Unit{
		Handle: 100, Def: frameDef, Alive: true,
		Health: 0, MaxHealth: 100, Remaining: 1,
		X: numeric.Fixed(70 << 16), Y: numeric.Fixed(40 << 16), Z: numeric.Fixed(90 << 16),
	}
	builders := make([]*units.Unit, 0, len(quanta))
	lookup := func(h pool.Handle) *units.Unit {
		if h == frame.Handle {
			return frame
		}
		for _, b := range builders {
			if b.Handle == h {
				return b
			}
		}
		return nil
	}
	for i, quantum := range quanta {
		def := &content.UnitDef{
			BMCode: true, Builder: true,
			WorkerTime: 30 * quantum, BuildDistance: 1000,
			FootprintX: 2, FootprintZ: 2, MaxDamage: 100,
		}
		b := &units.Unit{
			Handle: pool.Handle(i + 1), Def: def, Alive: true, Activated: true, InBuildStance: true,
			Health: 100, MaxHealth: 100,
			X: frame.X, Y: frame.Y, Z: frame.Z,
		}
		builders = append(builders, b)
		q := &Queue{}
		q.SetBinding(&QueueBinding{SimRNG: rng.Global.Sim, StockpileEconomy: econ, Lookup: lookup})
		BindQueue(b, q)
		q.Push(Lookup("HelpBuild"), Node{Owner: b.Handle, Target: frame.Handle})
	}
	return econ, builders, frame
}

// pumpAssistants runs one authoritative tick's worth of assist visits: every
// builder's queue is pumped once, in slot order.
func pumpAssistants(builders []*units.Unit, tick uint32) {
	for _, b := range builders {
		QueueForUnit(b).Pump(b, tick)
	}
}

// TestBuildAssistRatesSumOnOneTarget is PT3-04's regression. Before the fix the
// `HelpBuild` work phase admitted no work at all, so a second builder on a
// nanoframe added nothing: this asserted a strictly decreasing fraction and
// failed on the first tick with remaining still 1.
//
// The relationships locked are [05 R-WORK-01 §1]'s, not a census: each builder
// steps the SHARED fraction by its OWN quantum over the target's build time, so
// two builders advance the frame by the sum of their quanta in one tick, and
// each bills its OWN subrecord for that share of the cost.
func TestBuildAssistRatesSumOnOneTarget(t *testing.T) {
	econ, builders, frame := assistFixture(10, 20)

	pumpAssistants(builders, 1)

	// 10/100 + 20/100 of the fraction in one tick.
	if got, want := frame.Remaining, float32(1)-float32(10)/100-float32(20)/100; got != want {
		t.Fatalf("remaining after one tick with two assistants = %v, want %v (the two quanta sum) [05 R-WORK-01 §1]", got, want)
	}
	// The drain is the sum of the two demands, each recorded against its own
	// builder — there is no shared or pooled billing. The expected shares are
	// re-formed with the same single-precision steps the helper takes, because
	// the second builder starts from the fraction the first one stored.
	var shares [2]float32
	old := float32(1)
	for i, quantum := range []int32{10, 20} {
		next := old - float32(quantum)/float32(100)
		shares[i] = old - next
		old = next
	}
	for i, share := range shares {
		b := econ.UnitBuckets(builders[i].Handle)
		wantE, wantM := 200*share, 100*share
		if b[economy.Energy].Requested != wantE || b[economy.Energy].Accepted != wantE {
			t.Fatalf("builder %d energy requested/accepted = %v/%v, want %v", i, b[economy.Energy].Requested, b[economy.Energy].Accepted, wantE)
		}
		if b[economy.Metal].Requested != wantM || b[economy.Metal].Accepted != wantM {
			t.Fatalf("builder %d metal requested/accepted = %v/%v, want %v", i, b[economy.Metal].Requested, b[economy.Metal].Accepted, wantM)
		}
	}
}

// TestBuildAssistFinishesInTheSummedRateTime locks the consequence a player
// sees: two builders whose quanta sum to one bigger builder's finish the same
// frame in the same number of ticks that bigger builder would need, and every
// contributor's health gain lands on the one shared target.
func TestBuildAssistFinishesInTheSummedRateTime(t *testing.T) {
	run := func(quanta ...int32) (uint32, *units.Unit) {
		_, builders, frame := assistFixture(quanta...)
		var tick uint32
		for tick = 1; tick <= 100; tick++ {
			pumpAssistants(builders, tick)
			if frame.Remaining == 0 {
				break
			}
		}
		return tick, frame
	}
	pair, frame := run(10, 20)
	solo, _ := run(30)
	if pair != solo {
		t.Fatalf("two assistants at quanta 10+20 finished in %d ticks, one at quantum 30 in %d: the rates must sum [05 R-WORK-01 §1]", pair, solo)
	}
	if frame.Health != frame.MaxHealth {
		t.Fatalf("assisted frame health = %d/%d at completion, want full: the health gain is the difference of truncations on the SHARED fraction", frame.Health, frame.MaxHealth)
	}
}

// TestBuildAssistAdmitsEachBuilderSeparately locks the shortfall shape of
// [05 R-ECO-01 §7]: admission is per-builder and all-or-nothing on that
// builder's own carry, so one contributor stalling does not stall the others —
// the proportional split across contributors happens later, in the settlement
// of [05 "Two-stage settlement algorithm"], on what each of them accepted.
func TestBuildAssistAdmitsEachBuilderSeparately(t *testing.T) {
	econ, builders, frame := assistFixture(10, 20)
	// Leave the first builder carrying unpaid debt; the second is clear.
	econ.UnitBuckets(builders[0].Handle)[economy.Metal].Carry = 5

	pumpAssistants(builders, 1)

	if got, want := frame.Remaining, float32(1)-float32(20)/100; got != want {
		t.Fatalf("remaining = %v, want %v: only the unencumbered builder's quantum may be committed", got, want)
	}
	denied := econ.UnitBuckets(builders[0].Handle)
	if denied[economy.Energy].Requested == 0 {
		t.Fatal("a denied two-resource admission still records its demand [05 R-ECO-01 §7]")
	}
	if denied[economy.Energy].Accepted != 0 || denied[economy.Metal].Accepted != 0 {
		t.Fatalf("denied builder accepted %v/%v, want nothing", denied[economy.Energy].Accepted, denied[economy.Metal].Accepted)
	}
}

// TestBuildAssistDefersTheFrameDecay locks the target-side half of the step:
// the worked-this-tick flag of [05 R-WORK-01 §1], carried here on the frame's
// own `GetBuilt` record as the eleven-tick decay deferral of [04 R-FAC-02 §4] —
// the same field and protocol internal/construction's factory step writes, so an
// assistant holds a frame's decay off exactly as its own builder does.
func TestBuildAssistDefersTheFrameDecay(t *testing.T) {
	_, builders, frame := assistFixture(10)
	fq := &Queue{}
	fq.SetBinding(QueueForUnit(builders[0]).Binding())
	BindQueue(frame, fq)
	fq.Push(Lookup("GetBuilt"), Node{})

	pumpAssistants(builders, 40)

	if got := fq.Primary()[0].Param1; got != 40+nanoframeDecayRearm {
		t.Fatalf("GetBuilt decay deferral = %d, want %d", got, 40+nanoframeDecayRearm)
	}
}

// TestFreshNanoframeResolvesToHelpBuild is the order-issue half of PT3-04: a
// nanoframe is created with a remaining fraction of exactly 1, and the
// unfinished predicate used to exclude that value, so a right-click on a
// brand-new frame produced no assist order at all.
func TestFreshNanoframeResolvesToHelpBuild(t *testing.T) {
	_, builders, frame := assistFixture(10)
	if !isUnfinished(frame) {
		t.Fatal("a frame at remaining 1 is unfinished [05 R-WORK-01 §1]")
	}
	if name := resolveContextual(builders[0], frame, nil); name != "HelpBuild" {
		t.Fatalf("contextual resolution against a fresh nanoframe = %q, want HelpBuild [04 R-ORD-01 §5]", name)
	}
	frame.Remaining = 0
	if isUnfinished(frame) {
		t.Fatal("a completed unit is not unfinished")
	}
}

// ---------------------------------------------------------------------------
// Feature reclaim (PT3-05) [04 R-ORD-01 §5][05 R-WORK-01 §5][05 R-ECO-02 §2]
// ---------------------------------------------------------------------------

// reclaimFixtureTerrain is a 16x16 attribute grid with one feature record
// stamped at (cx, cz). Sixteen world units to a cell, so the builder at world
// (70, 90) of the work fixtures stands on cell (4, 5) [03 §2.1].
func reclaimFixtureTerrain(defs []*content.FeatureDef, cx, cz int) *world.Terrain {
	const w, h = 16, 16
	attrs := make([]formats.TNTAttribute, w*h)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	attrs[cz*w+cx] = formats.TNTAttribute{Height: 10, Feature: 0} // record 0 = defs[0]
	return &world.Terrain{
		CellW: w, CellH: h,
		Plot:        world.ExpandPlot(attrs, w, h),
		FeatureDefs: defs,
	}
}

// retailShapedTree is the shape every stock tree has in the reference install
// (features/trees/*.tdf, verified against ~/TotalAnnihilation): no metal, a
// large energy pool, reclaimable, blocking, a height byte, and `smudge01` as
// its `featurereclamate` successor — a non-blocking, non-reclaimable scorch.
// A stock wreck (`*_dead`) is the mirror image: metal, no energy, the same
// successor.
func retailShapedTree() (*content.FeatureDef, *content.FeatureDef) {
	smudge := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "smudge01"},
		FootprintX:       1, FootprintZ: 1,
	}
	tree := &content.FeatureDef{
		DefinitionHeader:    content.DefinitionHeader{CanonicalKey: "tree1"},
		Metal:               0,
		Energy:              250,
		Height:              40,
		FootprintX:          1,
		FootprintZ:          1,
		Reclaimable:         true,
		Blocking:            true,
		FeatureReclamate:    "smudge01",
		FeatureReclamateDef: smudge,
	}
	return tree, smudge
}

func reclaimFixture(defs []*content.FeatureDef) (*Queue, *units.Unit, *economy.Service) {
	q, builder, _ := workFixture()
	econ := queueEconomy(q)
	econ.Terrain = reclaimFixtureTerrain(defs, 4, 5)
	return q, builder, econ
}

// TestFeatureReclaimRemovesTheFeatureAndCreditsItsPools is the PT3-05
// regression. Before the fix a `Reclaim` record parked at the head of its
// builder's queue forever on the 0xE0 approach gate; with that gate forced
// satisfied it ran a countdown of zero straight through phases 3, 4 and 5 and
// completed having credited nothing and removed nothing. The contract is
// [05 R-WORK-01 §5]: the definition's WHOLE energy and metal values go to the
// builder's production accumulators as a one-time completion event, and the
// feature is replaced by its `featurereclamate` successor.
func TestFeatureReclaimRemovesTheFeatureAndCreditsItsPools(t *testing.T) {
	tree, smudge := retailShapedTree()
	q, builder, econ := reclaimFixture([]*content.FeatureDef{tree})

	id := Lookup("Reclaim")
	q.Push(id, Node{Owner: builder.Handle, GoalX: numeric.Fixed(70 << 16), GoalZ: numeric.Fixed(90 << 16)})

	if ticks := pumpUntilEmpty(t, q, builder, 4000); q.LenPrimary() != 0 {
		head := q.Primary()[0]
		t.Fatalf("the reclaim never completed: %d ticks, phase %d, gate %#x", ticks, head.Phase, head.DynamicGate)
	}

	buckets := econ.UnitBuckets(builder.Handle)
	if got := buckets[economy.Energy].Production; got != 250 {
		t.Fatalf("energy production = %v, want the tree's whole 250 pool [05 R-WORK-01 §5]", got)
	}
	if got := buckets[economy.Metal].Production; got != 0 {
		t.Fatalf("metal production = %v, want 0 — a tree carries no metal", got)
	}
	// The grid transition: the tree is gone and its scorch stands in its place
	// [05 "Removal and successor replacement"].
	def, _, _, ok := features.FeatureAt(econ.Terrain, numeric.Fixed(70<<16), numeric.Fixed(90<<16))
	if !ok || def != smudge {
		t.Fatalf("cell holds %v (found %v), want the featurereclamate successor", def, ok)
	}
}

// TestFeatureReclaimIsNotAPerTickDrip locks the half of §5 that a per-tick
// implementation would get wrong: nothing is credited until the countdown
// finishes, and the countdown is the feature's, not the builder's work rate.
func TestFeatureReclaimIsNotAPerTickDrip(t *testing.T) {
	tree, _ := retailShapedTree()
	q, builder, econ := reclaimFixture([]*content.FeatureDef{tree})
	q.Push(Lookup("Reclaim"), Node{Owner: builder.Handle, GoalX: numeric.Fixed(70 << 16), GoalZ: numeric.Fixed(90 << 16)})

	for tick := uint32(1); tick <= 40; tick++ {
		q.Pump(builder, tick)
		if q.LenPrimary() == 0 {
			t.Fatalf("a 250-energy tree finished by tick %d; the countdown is trunc(15+250/2)=140 at two per visit", tick)
		}
		if got := econ.UnitBuckets(builder.Handle)[economy.Energy].Production; got != 0 {
			t.Fatalf("tick %d credited %v before the reclaim completed [05 R-WORK-01 §5]", tick, got)
		}
	}
}

// TestFeatureReclaimSeedIsFifteenPlusHalfThePools locks the countdown seed and
// with it the fact that `workertime` does not enter it: two builders whose work
// rates differ by an order of magnitude seed the same countdown
// [05 R-WORK-01 §5].
func TestFeatureReclaimSeedIsFifteenPlusHalfThePools(t *testing.T) {
	cases := []struct {
		metal, energy int32
		want          int32
	}{
		{0, 0, 15},     // empty pools still cost the fixed fifteen
		{0, 250, 140},  // a stock tree
		{1768, 0, 899}, // a stock heavy wreck
		{86, 0, 58},    // a stock metal deposit-sized pool
		{1, 0, 15},     // trunc(15 + 0.5) is fifteen, not sixteen (I3)
	}
	for _, c := range cases {
		def := &content.FeatureDef{Metal: c.metal, Energy: c.energy}
		if got := featureWork(def, 15); got != c.want {
			t.Fatalf("metal %d energy %d seeded %d, want %d", c.metal, c.energy, got, c.want)
		}
		// The air twin is the same expression with thirty [04 R-ORD-01 §7].
		if got := featureWork(def, 30); got != c.want+15 {
			t.Fatalf("air seed for metal %d energy %d was %d, want %d", c.metal, c.energy, got, c.want+15)
		}
	}
}

// TestFeatureReclaimAbandonsWithoutAFeature locks the row's own entry arm: no
// feature at the goal is `Reclamation failed` and abandon, not a silent
// completion [04 R-ORD-01 §5].
func TestFeatureReclaimAbandonsWithoutAFeature(t *testing.T) {
	tree, _ := retailShapedTree()
	q, builder, _ := reclaimFixture([]*content.FeatureDef{tree})
	// A goal three cells away from the stamped feature.
	q.Push(Lookup("Reclaim"), Node{Owner: builder.Handle, GoalX: numeric.Fixed(150 << 16), GoalZ: numeric.Fixed(150 << 16)})
	q.Pump(builder, 1)
	if q.LenPrimary() != 0 {
		t.Fatalf("an empty cell left the record queued at phase %d", q.Primary()[0].Phase)
	}
	found := false
	for _, d := range q.Diagnostics() {
		if strings.Contains(d, "Reclamation failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no `Reclamation failed` caption: %v", q.Diagnostics())
	}
}
