package orders

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// vtolWorkFixture is workFixture's air counterpart: a `canfly` builder with
// every work capability, parked on the ground (mover mode 1) so the preamble's
// takeoff arm runs, and one finished grounded target at the same position. The
// builder's build stance is deliberately NOT set: no VTOL twin reads
// `INBUILDSTANCE` [04 R-ORD-01 §7], and every test below depends on that.
func vtolWorkFixture() (*Queue, *units.Unit, *units.Unit) {
	rng.SeedGlobal(1, 0)
	builderDef := &content.UnitDef{
		BMCode: 1, Builder: true, CanFly: true,
		CanCapture: true, CanReclamate: true, CanResurrect: true,
		WorkerTime: 300, BuildDistance: 1000, BuildTime: 100, CruiseAlt: 200,
		FootprintX: 2, FootprintZ: 2, MaxDamage: 100,
	}
	targetDef := &content.UnitDef{
		MaxDamage: 100, BuildTime: 100,
		FootprintX: 2, FootprintZ: 2, Builder: true, WorkerTime: 300,
	}
	builder := &units.Unit{
		Handle: 1, Def: builderDef, Alive: true, Activated: true,
		Health: 100, MaxHealth: 100,
		X: numeric.Fixed(70 << 16), Y: numeric.Fixed(40 << 16), Z: numeric.Fixed(90 << 16),
	}
	// Grounded: the preamble's takeoff arm reads the COMMITTED mode
	// [04 R-AIR-01 §6], so both words carry it [04 R-MOV-01 §8].
	builder.Move.Mode, builder.Move.ModeMirror = 1, 1
	// The target is damaged: `VTOL_RepairUnit`'s admission test refuses a target
	// whose 16-bit health equals its `maxdamage` [04 R-ORD-01 §7], so a
	// full-health fixture would never reach that row's phases at all.
	target := &units.Unit{
		Handle: 2, Def: targetDef, Alive: true, Activated: true,
		Health: 50, MaxHealth: 100,
		X: numeric.Fixed(70 << 16), Y: numeric.Fixed(40 << 16), Z: numeric.Fixed(90 << 16),
	}
	target.Move.Mode, target.Move.ModeMirror = 1, 1 // grounded: VTOL_RepairUnit refuses any other mode
	// The air feature-reclaim row resolves a feature at its goal on every visit
	// [04 R-ORD-01 §5], through the terrain the session's economy service
	// carries. One stock-shaped tree stands on the builder's own cell.
	tree, _ := retailShapedTree()
	q := &Queue{binding: &QueueBinding{
		SimRNG: rng.Global.Sim,
		// `VTOL_HelpBuild` phase 0 refuses a builder whose build list is empty
		// [04 R-ORD-01 §17]; the fixture's builder has one.
		BuildList: func(*content.UnitDef) bool { return true },
		Economy:   &economy.Service{Terrain: reclaimFixtureTerrain([]*content.FeatureDef{tree}, 4, 5)},
		Lookup: func(h pool.Handle) *units.Unit {
			switch h {
			case target.Handle:
				return target
			case builder.Handle:
				return builder
			}
			return nil
		},
	}}
	q.binding.Work = &WorkAdapter{
		Assist: func(_ *units.Unit, n *Node, _ uint32) bool {
			if n == nil || q.binding.Lookup(n.Target) == nil {
				return false
			}
			return true
		},
		Repair: func(_ *units.Unit, _ *units.Unit, n *Node, _ uint32) bool {
			t := q.binding.Lookup(n.Target)
			if t == nil || t.Health >= t.Def.MaxDamage {
				return false
			}
			t.Health++
			return true
		},
	}
	q.binding.Movement = &MovementGoalAdapter{
		InstallPoint:     func(PointGoalRequest) bool { return true },
		InstallAnnulus:   func(AnnulusGoalRequest) bool { return true },
		InstallRectangle: func(RectangleGoalRequest) bool { return true },
		InstallAir:       func(AirGoalRequest) bool { return true },
		Release:          func(*Node) bool { return true },
	}
	q.SetBinding(q.binding)
	BindQueue(builder, q)
	return q, builder, target
}

// TestVTOLWorkTwinsDispatchAndReachTheirTerminal is this unit's park test. Each
// twin must dispatch on its first visit and walk its row to a terminal code,
// leaving the queue empty and no missing-handler diagnostic behind
// [04 §3.3][04 R-ORD-01 §7]. The builder never reports a build stance, which is
// the point: a ground twin would sit in its `INBUILDSTANCE` wait forever and
// the air twins have no such phase.
func TestVTOLWorkTwinsDispatchAndReachTheirTerminal(t *testing.T) {
	// A fixed slice, not a map: the assertions run in one order (I1).
	for _, name := range []string{"VTOL_HelpBuild", "VTOL_Reclaim", "VTOL_RepairUnit"} {
		id := Lookup(name)
		if id == 0 {
			t.Fatalf("%s is not in the descriptor table", name)
		}
		if DescriptorFor(id).Handler == nil {
			t.Fatalf("%s has no handler after every installer ran", name)
		}
		q, builder, target := vtolWorkFixture()
		// The goal is the fixture's feature cell: `VTOL_Reclaim` resolves a
		// feature at its goal before it looks at anything else, and the other
		// two twins overwrite the goal from their target [04 R-ORD-01 §5, §7].
		q.Push(id, Node{Owner: builder.Handle, Target: target.Handle,
			GoalSupplied: true,
			GoalX:        numeric.Fixed(70 << 16), GoalY: numeric.Fixed(40 << 16), GoalZ: numeric.Fixed(90 << 16)})
		head := q.Primary()[0]
		if head.DynamicGate != 0 {
			t.Fatalf("%s: fresh record waits on %#x [04 R-ORD-01 §1]", name, head.DynamicGate)
		}

		q.Pump(builder, 1)
		if head.Phase == 0 && q.LenPrimary() != 0 && head.DynamicGate == 0 {
			t.Fatalf("%s: first visit left the record untouched", name)
		}
		if builder.Move.Mode&0x3 != 2 {
			t.Fatalf("%s: the preamble must put a grounded aircraft airborne [04 R-ORD-01 §7]", name)
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

// TestVTOLHelpBuildPassesTheAbsoluteBearing locks this twin's divergence: it is
// the one `StartBuilding` site in the image that does NOT subtract the
// builder's own heading, so an air builder's script receives a world heading
// [04 R-CB-01 §3][04 R-ORD-01 §7]. The ground `HelpBuild` next to it, given the
// same geometry and the same heading, receives zero.
func TestVTOLHelpBuildPassesTheAbsoluteBearing(t *testing.T) {
	u, vm := cbUnit(cbProgram("StartBuilding"))
	u.X, u.Z = 0, 0
	u.Move.Heading = 49152 // already facing the target
	target := &units.Unit{Handle: 2, X: numeric.Fixed(64 << 16)}
	n := &Node{ID: Lookup("VTOL_HelpBuild"), Owner: u.Handle, Deadline: -1, GoalX: numeric.Fixed(64 << 16), GoalSupplied: true}

	emitStartBuildingAbsolute(u, n, target)

	args := startedArgs(vm)
	if len(args) != 1 || !argsEqual(args[0], []int32{49152}) {
		t.Fatalf("VTOL_HelpBuild arrange %v, want [49152]: the air site passes the raw bearing [04 R-CB-01 §3]", args)
	}
	if n.Flags&FlagStopBuildingPending == 0 {
		t.Fatalf("the emitter must still set the record's StopBuilding-pending flag [R-ORDER-02 §2]")
	}

	// The ground twin, same geometry and heading, gets the relative bearing.
	u2, vm2 := cbUnit(cbProgram("StartBuilding"))
	u2.X, u2.Z = 0, 0
	u2.Move.Heading = 49152
	n2 := &Node{ID: Lookup("HelpBuild"), Owner: u2.Handle, Deadline: -1, GoalX: numeric.Fixed(64 << 16), GoalSupplied: true}
	EmitStartBuilding(u2, n2)
	if args2 := startedArgs(vm2); len(args2) != 1 || !argsEqual(args2[0], []int32{0}) {
		t.Fatalf("ground HelpBuild arrange %v, want [0]: eight of nine sites subtract the heading", args2)
	}
}

// TestVTOLHelpBuildFinishesWithoutATerminalPhase locks the second divergence:
// the air row completes straight out of its work phase — no phase 4, no
// `Building complete` status, no `gate |= 0x2` on the way out — where the
// ground row advances into a terminal phase that emits both
// [04 R-ORD-01 §5][04 R-ORD-01 §7].
func TestVTOLHelpBuildFinishesWithoutATerminalPhase(t *testing.T) {
	q, builder, target := vtolWorkFixture()
	target.Remaining = 0 // the product is finished

	n := &Node{ID: Lookup("VTOL_HelpBuild"), Owner: builder.Handle, Target: target.Handle, Phase: 3, Deadline: -1}
	if code := vtolHelpBuildHandler(builder, n, 0, 700); code != 5 {
		t.Fatalf("work phase on a finished product returned %d, want complete (5)", code)
	}
	if n.DynamicGate&gateCancelCurrent != 0 {
		t.Fatalf("the air row must not arm the cancel-current bit its ground twin sets in phase 4")
	}
	for _, d := range q.Diagnostics() {
		if strings.Contains(d, "Building complete") {
			t.Fatalf("the air row emits no `Building complete` caption: %s", d)
		}
	}

	// The unfinished arm is the row's own hold: deadline 1 from the handler's
	// tick, gate |= 0xA [04 R-ORD-01 §7].
	target.Remaining = 0.5
	n = &Node{ID: Lookup("VTOL_HelpBuild"), Owner: builder.Handle, Target: target.Handle, Phase: 3, Deadline: -1}
	if code := vtolHelpBuildHandler(builder, n, 0, 700); code != 2 {
		t.Fatalf("work phase on an unfinished product returned %d, want hold (2)", code)
	}
	if n.Deadline != 701 || n.DynamicGate&gateWorkRetry != gateWorkRetry {
		t.Fatalf("deadline = %d gate = %#x, want 701 with 0xA armed [04 R-ORD-01 §7]", n.Deadline, n.DynamicGate)
	}
}

// TestVTOLRepairUnitAbandonsOnANullTarget locks this twin's divergence: a null
// target ABANDONS where the ground `RepairUnit` completes, and the admission
// test of [04 R-ORD-01 §7] is a gate the ground row does not have.
func TestVTOLRepairUnitAbandonsOnANullTarget(t *testing.T) {
	_, builder, target := vtolWorkFixture()

	n := &Node{ID: Lookup("VTOL_RepairUnit"), Owner: builder.Handle, Deadline: -1}
	if code := vtolRepairUnitHandler(builder, n, 0, 1); code != 8 {
		t.Fatalf("null target returned %d, want abandon (8) [04 R-ORD-01 §7]", code)
	}
	nGround := &Node{ID: Lookup("RepairUnit"), Owner: builder.Handle, Deadline: -1}
	if code := repairUnitHandler(builder, nGround, 0, 1); code != 5 {
		t.Fatalf("the ground twin returned %d on a null target, want complete (5) [04 R-ORD-01 §5]", code)
	}

	// The admission test refuses a target already at full health, before any
	// phase runs: `Repair mission failed`, abandon.
	target.Health = target.Def.MaxDamage
	n = &Node{ID: Lookup("VTOL_RepairUnit"), Owner: builder.Handle, Target: target.Handle, Deadline: -1}
	if code := vtolRepairUnitHandler(builder, n, 0, 1); code != 8 {
		t.Fatalf("a full-health target returned %d, want abandon (8) on the admission gate", code)
	}
	// Damaged, and the admission passes: the preamble runs and advances.
	target.Health = 50
	n = &Node{ID: Lookup("VTOL_RepairUnit"), Owner: builder.Handle, Target: target.Handle, Deadline: -1}
	if code := vtolRepairUnitHandler(builder, n, 0, 1); code != 1 {
		t.Fatalf("a damaged admissible target returned %d, want advance (1)", code)
	}
}

// TestVTOLReclaimCountdownUsesThirty locks the constant that separates the air
// feature reclaim from the ground one: the countdown seed is 30 where the
// ground row uses 15, so an aircraft takes fifteen more ticks per feature, and
// the two-segment spray gate moves with it [04 R-ORD-01 §7][05 R-WORK-01 §5].
// The relationship asserted is the seed-independent one: the work phase spends
// exactly two per visit, so a seed of 30 costs fifteen visits where the ground
// seed of 15 costs eight. The record is entered at phase 3 with the seed
// already stored, so the assertion is about the countdown alone.
func TestVTOLReclaimCountdownUsesThirty(t *testing.T) {
	_, builder, _ := vtolWorkFixture()

	visits := func(seed uint32) int {
		n := &Node{ID: Lookup("VTOL_Reclaim"), Owner: builder.Handle, Phase: 3, Param1: seed, Deadline: -1,
			GoalSupplied: true,
			GoalX:        numeric.Fixed(70 << 16), GoalZ: numeric.Fixed(90 << 16)}
		count := 0
		for n.Phase == 3 && count < 200 {
			code := vtolReclaimHandler(builder, n, 0, uint32(count))
			count++
			if code == 1 {
				break // the visit that runs the countdown to zero and advances
			}
		}
		return count
	}
	airSeed, groundSeed := uint32(30), uint32(15)
	if got, want := visits(airSeed), 15; got != want {
		t.Fatalf("air countdown took %d visits from a seed of 30, want %d (two per visit)", got, want)
	}
	if got, want := visits(groundSeed), 8; got != want {
		t.Fatalf("the ground seed of 15 takes %d visits, want %d — the air row's extra fifteen ticks are seven more visits", got, want)
	}
}

// TestVTOLReclaimEmitsTheNanolatheSpray locks the play-test fix (pt6-airreclaim):
// [04 R-ORD-01 §7]'s "p1 > 30 -> spray twice to the feature box" is the air
// row's own copy of the ground `Reclaim` row's "p1 > 15 -> spray twice"
// [04 R-ORD-01 §5]. The air handler already wrote the nanolathe-active stamp
// on every qualifying visit, but never called the presentation adapter that
// publishes the spray — a construction aircraft reclaiming a feature stamped
// itself reveal/cloak-active with no nanospray drawn behind it, unlike the
// ground twin two phases over in work.go's reclaimHandler.
func TestVTOLReclaimEmitsTheNanolatheSpray(t *testing.T) {
	q, builder, _ := vtolWorkFixture()
	terrain := q.Binding().Economy.(*economy.Service).Terrain
	q.Binding().World = &WorldQueryAdapter{
		LookupFeature: func(cellX, cellZ int32) (FeatureView, bool) {
			def, ax, az, ok := features.FeatureAt(terrain, world.CellToWorld(cellX), world.CellToWorld(cellZ))
			if !ok {
				return FeatureView{}, false
			}
			return FeatureView{
				CX: int32(ax), CZ: int32(az),
				FootprintX: def.FootprintX, FootprintZ: def.FootprintZ, Height: def.Height,
				DefinitionKey: def.CanonicalKey,
				Metal:         def.Metal, Energy: def.Energy, Reclaimable: def.Reclaimable,
			}, true
		},
	}
	segments := 0
	q.Binding().Presentation = &PresentationAdapter{
		NanolatheFeature: func(*units.Unit, *Node, FeatureView, uint32) bool { segments++; return true },
	}

	// Phase 3, seed 40: one visit takes the countdown from 40 to 38, which
	// clears the row's own `work > 30` gate and must fire exactly one call —
	// the bound adapter is the two-segment producer, not this handler
	// [05 R-WORK-01 §8].
	n := &Node{ID: Lookup("VTOL_Reclaim"), Owner: builder.Handle, Phase: 3, Param1: 40, Deadline: -1,
		GoalSupplied: true,
		GoalX:        numeric.Fixed(70 << 16), GoalZ: numeric.Fixed(90 << 16)}
	if code := vtolReclaimHandler(builder, n, 0, 1); code == 8 {
		t.Fatalf("the visit abandoned instead of holding on its countdown")
	}
	if segments != 1 {
		t.Fatalf("phase 3 with work 38 (> 30) published %d nanolathe segments, want exactly 1 [04 R-ORD-01 §7]", segments)
	}

	// A visit that leaves the countdown at or below 30 must stay silent, same
	// as the ground row's own `<= 15` visits.
	segments = 0
	n = &Node{ID: Lookup("VTOL_Reclaim"), Owner: builder.Handle, Phase: 3, Param1: 32, Deadline: -1,
		GoalSupplied: true,
		GoalX:        numeric.Fixed(70 << 16), GoalZ: numeric.Fixed(90 << 16)}
	if code := vtolReclaimHandler(builder, n, 0, 1); code == 8 {
		t.Fatalf("the visit abandoned instead of holding on its countdown")
	}
	if segments != 0 {
		t.Fatalf("phase 3 with work 30 (not > 30) published %d nanolathe segments, want 0", segments)
	}
}

// TestVTOLRepairPatrolHoldsOnItsOwnDeadline locks the patrol leg's shape
// [04 R-ORD-01 §7]: the record dispatches, arms the marker leg's 45-tick
// deadline with the movement outcomes, and holds — it never parks on a gate
// nothing raises, and an arrival rotates it to the next waypoint. The interrupt
// pre-check restarts on a 30-tick deadline.
//
// The handler is exercised directly because pump.go's ensureMoveHandlers claims
// this descriptor first with the move family's placeholder (see the
// registration note in vtolwork.go).
func TestVTOLRepairPatrolHoldsOnItsOwnDeadline(t *testing.T) {
	q, builder, _ := vtolWorkFixture()
	id := Lookup("VTOL_RepairPatrol")
	if DescriptorFor(id).Handler == nil {
		t.Fatalf("VTOL_RepairPatrol has no handler at all: it would park [PLAN 18 gate 10]")
	}
	n := &Node{ID: id, Owner: builder.Handle, Deadline: -1,
		GoalSupplied: true,
		GoalX:        numeric.Fixed(200 << 16), GoalZ: numeric.Fixed(200 << 16)}
	q.Push(id, *n)
	n = q.Primary()[0]

	if code := vtolRepairPatrolHandler(builder, n, 0, 100); code != 1 {
		t.Fatalf("phase 0 returned %d, want advance (1)", code)
	}
	if n.StaticGate&patrolChainMember == 0 {
		t.Fatalf("the patrol-chain setup must mark this record [04 R-ORD-01 §4]")
	}
	if q.LenPrimary() != 2 {
		t.Fatalf("primary length %d, want 2: the setup appends the return-to-start waypoint", q.LenPrimary())
	}
	n.Phase = 1

	if code := vtolRepairPatrolHandler(builder, n, 0, 100); code != 2 {
		t.Fatalf("phase 1 returned %d, want hold (2)", code)
	}
	if n.Deadline != 145 || n.DynamicGate&gateDeadline == 0 || n.DynamicGate&gateMoveOutcomes != gateMoveOutcomes {
		t.Fatalf("deadline = %d gate = %#x, want 145 with 0xE1 armed [04 R-ORD-01 §7]", n.Deadline, n.DynamicGate)
	}
	if code := vtolRepairPatrolHandler(builder, n, gateArrived, 100); code != 6 {
		t.Fatalf("an arrival returned %d, want rotate (6)", code)
	}
	if code := vtolRepairPatrolHandler(builder, n, pendTargetRemoved, 100); code != 0 {
		t.Fatalf("the interrupt pre-check returned %d, want restart (0)", code)
	}
	if n.Deadline != 130 {
		t.Fatalf("interrupt deadline = %d, want 130 [04 R-ORD-01 §7]", n.Deadline)
	}
}

// TestRepairWaterClause locks both halves of the air-repair water clause of
// [04 R-ORD-01 §7], which was passed unconditionally while the T25 marker
// there stood.
// It drives the single admission `nanoReach`, which the copy that used to live
// in this file (`repairAdmission`) was collapsed into [04 R-ORD-02 §7].
func TestRepairWaterClause(t *testing.T) {
	const sea = 40
	bind := &QueueBinding{World: &WorldQueryAdapter{SeaLevel: func() uint8 { return sea }}}

	mk := func(def *content.UnitDef, y int32) *units.Unit {
		u := &units.Unit{Def: def, Y: numeric.Fixed(int64(y) * 65536)}
		q := QueueForUnit(u)
		q.SetBinding(bind)
		return u
	}
	targetDef := &content.UnitDef{UnitName: "tgt", MaxDamage: 100, ModelTop: 6}
	air := &content.UnitDef{UnitName: "air", CanFly: true, CanReclamate: true}
	amphibAir := &content.UnitDef{UnitName: "amphair", CanFly: true, Amphibious: true, CanReclamate: true}
	walker := &content.UnitDef{UnitName: "walk", CanReclamate: true, MaxWaterDepth: 10}

	hurt := func(y int32) *units.Unit {
		u := mk(targetDef, y)
		u.Health = 50
		u.Move.Mode, u.Move.ModeMirror = 1, 1
		return u
	}

	// Target top at 36+6 = 42, above sea level 40: an aircraft repairs it.
	if !nanoReach(mk(air, 60), hurt(36)) {
		t.Fatalf("aircraft should repair a target whose top is above water")
	}
	// Target top at 30+6 = 36, under sea level 40: retail refuses.
	if nanoReach(mk(air, 60), hurt(30)) {
		t.Fatalf("aircraft must refuse a target whose top is under water")
	}
	// The amphibious disjunct rescues the air half.
	if !nanoReach(mk(amphibAir, 60), hurt(30)) {
		t.Fatalf("amphibious aircraft should repair a submerged target")
	}
	// A walker wades to its own MaxWaterDepth: sea-10 = 30 <= top.
	if !nanoReach(mk(walker, 38), hurt(30)) {
		t.Fatalf("walker should repair a target within its wading depth")
	}
	if nanoReach(mk(walker, 38), hurt(20)) {
		t.Fatalf("walker must refuse a target below its wading depth")
	}

	// No world adapter: the clause passes rather than abandoning the repair.
	loose := &units.Unit{Def: air}
	tgt := &units.Unit{Def: targetDef, Health: 50}
	tgt.Move.Mode, tgt.Move.ModeMirror = 1, 1
	if !nanoReach(loose, tgt) {
		t.Fatalf("unbound queue should not fail the water clause")
	}
}

// TestVTOLHelpBuildPhaseZeroRequiresABuildList locks [04 R-ORD-01 §17]: the air
// help-build row's third phase-0 precondition is the definition's non-empty
// build list — command code 14's test [04 R-ORD-02 §1], not a script slot —
// and a failure is cancel-all before the preamble has any side effect.
func TestVTOLHelpBuildPhaseZeroRequiresABuildList(t *testing.T) {
	_, builder, target := vtolWorkFixture()
	setTestBuildList(builder, func(*content.UnitDef) bool { return false })
	n := &Node{ID: Lookup("VTOL_HelpBuild"), Owner: builder.Handle, Target: target.Handle, Deadline: -1}
	if code := vtolHelpBuildHandler(builder, n, 0, 700); code != 7 {
		t.Fatalf("phase 0 without a build list returned %d, want cancel-all (7)", code)
	}
	if builder.Move.Mode&0x3 != 1 || n.DynamicGate != 0 {
		t.Fatalf("the refusal must precede the preamble: mode %d, gate %#x", builder.Move.Mode&0x3, n.DynamicGate)
	}

	setTestBuildList(builder, func(*content.UnitDef) bool { return true })
	n = &Node{ID: Lookup("VTOL_HelpBuild"), Owner: builder.Handle, Target: target.Handle, Deadline: -1}
	if code := vtolHelpBuildHandler(builder, n, 0, 700); code != 1 {
		t.Fatalf("phase 0 with a build list returned %d, want advance (1)", code)
	}
}
