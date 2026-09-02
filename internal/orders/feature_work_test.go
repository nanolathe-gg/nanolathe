package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// WU-19-5 — the two feature work rows at any distance
// [04 R-ORD-01 §5][05 R-WORK-01 §5][05 R-WORK-01 §5-A][05 R-WORK-01 §7].
//
// Everything here is about the half of both rows that the build used to skip:
// phase 0's rectangle goal on the FEATURE's footprint, and the advance that
// puts the record behind gate 0xE0 while the mover walks. The countdown, the
// payout and the credit's owner are locked by work_test.go's PT3-05 tests and
// are not restated.

// distantFeatureFixture is a builder whose `builddistance` is a single world
// unit standing at world (70, 90) — cell (4, 5) — with a feature stamped at
// (cx, cz). Everything outside the builder's own cell is therefore out of
// reach, which is what makes the approach observable at all: the shipped work
// fixture authors `builddistance` 1000, more than the whole 16x16 test map.
//
// The returned recorder captures every goal payload the row installs, and the
// returned counter counts published feature nanolathe segments.
type featureWorkFixture struct {
	q        *Queue
	builder  *units.Unit
	econ     *economy.Service
	rects    []RectangleGoalRequest
	points   []PointGoalRequest
	segments int
	// resurrectPhases records the record phase at every Work.Resurrect call, in
	// call order — the seam is phase-dispatched, so the order is the contract.
	resurrectPhases []uint8
}

func newFeatureWorkFixture(t *testing.T, defs []*content.FeatureDef, cx, cz int) *featureWorkFixture {
	t.Helper()
	f := &featureWorkFixture{}
	builderDef := &content.UnitDef{
		BMCode: true, Builder: true,
		CanReclamate: true, CanResurrect: true,
		WorkerTime: 300, BuildDistance: 1, BuildTime: 100,
		FootprintX: 1, FootprintZ: 1, MaxDamage: 100,
	}
	f.builder = &units.Unit{
		Handle: 1, Def: builderDef, Alive: true, Activated: true, InBuildStance: true,
		Health: 100, MaxHealth: 100,
		X: numeric.Fixed(70 << 16), Y: numeric.Fixed(40 << 16), Z: numeric.Fixed(90 << 16),
	}
	f.econ = &economy.Service{Terrain: reclaimFixtureTerrain(defs, cx, cz)}
	binding := &QueueBinding{
		Economy: f.econ,
		Lookup: func(h pool.Handle) *units.Unit {
			if h == f.builder.Handle {
				return f.builder
			}
			return nil
		},
	}
	binding.Movement = &MovementGoalAdapter{
		InstallPoint: func(req PointGoalRequest) bool {
			f.points = append(f.points, req)
			return true
		},
		InstallAnnulus:   func(AnnulusGoalRequest) bool { return true },
		InstallRectangle: func(req RectangleGoalRequest) bool { f.rects = append(f.rects, req); return true },
		InstallAir:       func(AirGoalRequest) bool { return true },
		Release:          func(*Node) bool { return true },
	}
	// The world adapter is what featureViewAtGoal reads for the spray target;
	// it resolves the anchored feature exactly as internal/session composes it
	// over the terrain's own fringe hop [05 R-ECO-02 §2].
	binding.World = &WorldQueryAdapter{
		LookupFeature: func(cellX, cellZ int32) (FeatureView, bool) {
			def, ax, az, ok := features.FeatureAt(f.econ.Terrain, world.CellToWorld(cellX), world.CellToWorld(cellZ))
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
	binding.Presentation = &PresentationAdapter{
		NanolatheFeature: func(*units.Unit, *Node, FeatureView, uint32) bool { f.segments++; return true },
	}
	f.q = &Queue{binding: binding}
	f.q.SetBinding(binding)
	BindQueue(f.builder, f.q)
	return f
}

// TestFeatureReclaimBeyondRangeInstallsTheFootprintRectangle locks phase 0 of
// the `Reclaim` row: "rectangle goal on the feature's footprint (origin cell,
// size), gate = 0xE0, advance" [04 R-ORD-01 §5]. The origin is the ANCHOR
// cell — a feature's stamp writes its definition index there and the fringe
// sentinel across the rest of the footprint [05 R-ECO-02 §2].
//
// These are the CONSTRUCTOR's arguments, and they are the target's alone: the
// growth by the mover's own footprint happens inside the goal class
// [04 R-PATH-01 §12], so what this seam passes is unchanged by WU-19-24 and
// internal/movement's TestRectangleGoalIsTheTargetFootprintGrownByTheMover
// owns the other half.
func TestFeatureReclaimBeyondRangeInstallsTheFootprintRectangle(t *testing.T) {
	tree, _ := retailShapedTree()
	tree.FootprintX, tree.FootprintZ = 3, 2
	f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 9, 11)

	// The goal is the feature's cell, five cells from the builder and far
	// outside its one-unit `builddistance`.
	f.q.Push(Lookup("Reclaim"), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(9), GoalZ: world.CellToWorld(11)})
	head := f.q.Primary()[0]
	f.q.Pump(f.builder, 1)

	if len(f.rects) != 1 {
		t.Fatalf("phase 0 installed %d rectangle goals and %d point goals, want exactly one rectangle on the feature footprint", len(f.rects), len(f.points))
	}
	got := f.rects[0]
	if got.CellX != 9 || got.CellZ != 11 {
		t.Fatalf("rectangle origin (%d,%d), want the feature's anchor cell (9,11) with no half-footprint offset [05 R-ECO-02 §2]", got.CellX, got.CellZ)
	}
	if got.Width != 3 || got.Depth != 2 {
		t.Fatalf("rectangle size %dx%d, want the feature definition's 3x2 footprint", got.Width, got.Depth)
	}
	if got.Node != head {
		t.Fatalf("the rectangle was installed for a record other than the one that asked [04 R-ORD-01 §1]")
	}
}

// TestFeatureWorkOutOfRangeAdvancesAndWaitsOnTheGate is the retirement of the
// placeholder thirty-tick hold. Both rows arm 0xE0 and ADVANCE; the gate is the
// wait, and phase 1 does not run again until the follower reports arrival
// (0x20), no route (0x40) or a payload release (0x80) [04 R-ORD-01 §5].
//
// The old placeholder held at phase 0 behind a plain thirty-tick re-poll, which
// is why a right-click on a distant rock or wreck never reclaimed: the record
// re-tested reach forever and the mover was never asked to walk.
func TestFeatureWorkOutOfRangeAdvancesAndWaitsOnTheGate(t *testing.T) {
	for _, name := range []string{"Reclaim", "Resurrect"} {
		tree, _ := retailShapedTree()
		f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 9, 11)
		f.q.Push(Lookup(name), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(9), GoalZ: world.CellToWorld(11)})
		head := f.q.Primary()[0]

		f.q.Pump(f.builder, 1)
		if head.Phase != 1 {
			t.Fatalf("%s: out of range the record stayed at phase %d; the row advances and waits behind its gate", name, head.Phase)
		}
		if head.DynamicGate != gateMoveOutcomes {
			t.Fatalf("%s: gate %#x after phase 0, want the movement-outcome set %#x", name, head.DynamicGate, gateMoveOutcomes)
		}
		if head.DynamicGate&gateDeadline != 0 {
			t.Fatalf("%s: phase 0 armed the shared deadline setter; the thirty-tick re-poll placeholder is meant to be gone", name)
		}

		// Thirty-one further ticks with nothing satisfied: the record must not
		// wake at all. The placeholder woke it every thirtieth tick.
		for tick := uint32(2); tick <= 40; tick++ {
			f.q.Pump(f.builder, tick)
		}
		if head.Phase != 1 {
			t.Fatalf("%s: the record reached phase %d with no movement outcome; nothing but the gate may advance it", name, head.Phase)
		}
		if f.builder.Def.CanReclamate && len(f.rects) != 1 {
			t.Fatalf("%s: %d goal installs over 40 ticks, want the row's single phase-0 install", name, len(f.rects))
		}

		// Arrival is what releases it.
		head.Satisfied |= gateArrived
		f.q.Pump(f.builder, 41)
		if head.Phase == 1 {
			t.Fatalf("%s: arrival did not release the record from phase 1", name)
		}
	}
}

// TestFeatureReclaimSpraysTwiceOnlyWhileAboveFifteen locks the engine's only
// two-segment producer: "p1 > 15 -> draw the spray twice to the feature box"
// [04 R-ORD-01 §5], which is also why "the last eight visits of every feature
// reclaim emit no nano at all" [05 R-WORK-01 §5].
//
// The relationship, not a census: the countdown starts at trunc(15 + 250/2) =
// 140 and falls by two a visit, so exactly the visits that leave it above 15
// emit two segments each and the rest emit none.
func TestFeatureReclaimSpraysTwiceOnlyWhileAboveFifteen(t *testing.T) {
	tree, _ := retailShapedTree()
	f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 4, 5)
	// Stand the builder on the feature's own cell so the row reaches its work
	// phases without a mover; the approach is the previous test's subject.
	f.builder.X, f.builder.Z = world.CellToWorld(4), world.CellToWorld(5)

	f.q.Push(Lookup("Reclaim"), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(5)})
	head := f.q.Primary()[0]

	twoSegmentVisits, silentVisits := 0, 0
	for tick := uint32(1); tick <= 400 && f.q.LenPrimary() > 0; tick++ {
		before, countdown := f.segments, head.Param1
		f.q.Pump(f.builder, tick)
		// A work visit is one that changed the countdown from phase 3 or later;
		// the ticks between them sit behind the row's own two-tick deadline and
		// change nothing. The first visit is reached in the same pump as the
		// seeding write, because phases 0, 1 and 2 all return *advance* and the
		// pump restarts from the head after each one.
		if head.Param1 == countdown || head.Phase < 3 {
			continue
		}
		switch f.segments - before {
		case 0:
			silentVisits++
		case 2:
			twoSegmentVisits++
		default:
			t.Fatalf("a work visit published %d feature segments; the row draws the spray twice or not at all [04 R-ORD-01 §5]", f.segments-before)
		}
	}
	if f.q.LenPrimary() != 0 {
		t.Fatalf("the reclaim never completed")
	}
	// 140 down by two: the counter is above fifteen for the first 62 visits
	// (140-2k > 15 up to k = 62) and at or below it for the last seven, and
	// phases 3 and 4 SHARE the body, so phase 4 runs it once more after phase 3
	// advanced on a non-positive counter [04 R-ORD-01 §5] — nine dark visits in
	// all, the eight of §5's "last eight visits" plus the shared phase-4 one.
	if twoSegmentVisits != 62 || silentVisits != 9 {
		t.Fatalf("visits: %d emitted two segments and %d emitted none, want 62 and 9 for a 250-energy tree", twoSegmentVisits, silentVisits)
	}
}

// TestFeatureReclaimTakesOneHeightDrawAndTheAirTwinNone is the I4 half of both
// rows. The ground row's single bounded draw is bounded by the feature
// definition's height byte and lives in the phase that forms the spray target
// [05 R-WORK-01 §5]; the air twin takes none [04 R-ORD-01 §7]. A height byte
// below two returns zero WITHOUT advancing the seed [01 §8].
func TestFeatureReclaimTakesOneHeightDrawAndTheAirTwinNone(t *testing.T) {
	cases := []struct {
		height int32
		want   uint64
	}{
		{40, 1}, // a stock tree
		{1, 0},  // below two: no draw at all
		{0, 0},
	}
	for _, c := range cases {
		tree, _ := retailShapedTree()
		tree.Height = c.height
		f := newFeatureWorkFixture(t, []*content.FeatureDef{tree}, 4, 5)
		f.builder.X, f.builder.Z = world.CellToWorld(4), world.CellToWorld(5)
		sim := f.q.simForJitter()
		if sim == nil {
			t.Skip("the fixture binds no simulation stream")
		}
		before := sim.Draws()
		f.q.Push(Lookup("Reclaim"), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(5)})
		for tick := uint32(1); tick <= 400 && f.q.LenPrimary() > 0; tick++ {
			f.q.Pump(f.builder, tick)
		}
		if got := sim.Draws() - before; got != c.want {
			t.Fatalf("height %d: the whole reclaim took %d simulation draws, want %d [05 R-WORK-01 §5][01 §8]", c.height, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Resurrect [04 R-ORD-01 §5][05 R-WORK-01 §7]
// ---------------------------------------------------------------------------

// bindResurrectSeam installs a Work.Resurrect adapter shaped exactly like the
// session's: phase 3 resolves the corpse's name against a catalogue and writes
// p1/p2, phase 5 allocates the unit, removes the feature and binds the product
// as the record's target with remaining 0 and health 1 [05 R-WORK-01 §7].
// `resolve` reporting false is "the corpse name resolved to no definition";
// `allocate` reporting nil is a refused allocation.
func bindResurrectSeam(f *featureWorkFixture, resolved *content.UnitDef, allocate func() *units.Unit) {
	f.q.Binding().Work = &WorkAdapter{
		Resurrect: func(builder *units.Unit, n *Node, _ uint32) bool {
			f.resurrectPhases = append(f.resurrectPhases, n.Phase)
			if resolved == nil {
				return false
			}
			switch n.Phase {
			case 3:
				n.BuildDefKey = resolved.CanonicalKey
				n.Param2 = uint32(ResurrectionDelay(resolved.BuildTime, builder.Def.WorkerTime))
				return true
			case 5:
				product := allocate()
				if product == nil {
					return false
				}
				def, _, cx, cz := (*content.FeatureDef)(nil), 0, 0, 0
				def, cx, cz, _ = features.FeatureAt(f.econ.Terrain, n.GoalX, n.GoalZ)
				_ = def
				f.econ.Terrain.PlotAt(int32(cx), int32(cz)).SetFeature(world.PlotFeatureNone)
				product.Remaining = 0
				product.Health = 1
				n.Target = product.Handle
				return true
			}
			return false
		},
	}
}

// TestResurrectionProducesTheUnitAndRemovesTheCorpse walks the whole row.
// Phase 5 "allocates a unit of the resolved definition at the feature's
// recorded position and owner byte ... removes the feature ... and then sets
// the new unit's remaining fraction to 0 and its health to 1. The unit is
// therefore *finished* but at one hit point" [05 R-WORK-01 §7], and phase 6
// raises `Resurrection complete` and head-inserts the repair successor
// [04 R-ORD-01 §5].
func TestResurrectionProducesTheUnitAndRemovesTheCorpse(t *testing.T) {
	corpse := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armaap_dead"},
		Metal:            1768, Height: 20, FootprintX: 2, FootprintZ: 2, Reclaimable: true,
	}
	f := newFeatureWorkFixture(t, []*content.FeatureDef{corpse}, 4, 5)
	f.builder.X, f.builder.Z = world.CellToWorld(4), world.CellToWorld(5)

	productDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "armaap"},
		BuildTime:        3000, MaxDamage: 500, FootprintX: 2, FootprintZ: 2,
	}
	product := &units.Unit{Handle: 2, Def: productDef, Alive: true, Health: 500, MaxHealth: 500, Remaining: 1}
	// The successor `RepairUnit` refuses any target whose mover mode is not
	// grounded [04 R-ORD-01 §5]; a resurrected building is grounded.
	product.Move.Mode = 1
	prior := f.q.Binding().Lookup
	f.q.Binding().Lookup = func(h pool.Handle) *units.Unit {
		if h == product.Handle {
			return product
		}
		return prior(h)
	}
	bindResurrectSeam(f, productDef, func() *units.Unit { return product })

	f.q.Push(Lookup("Resurrect"), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(5)})
	var captions []string
	f.q.Binding().Presentation.Status = func(_ *units.Unit, kind uint8, text string) bool {
		captions = append(captions, text)
		return true
	}

	completed := false
	for tick := uint32(1); tick <= 2000; tick++ {
		f.q.Pump(f.builder, tick)
		if f.q.LenPrimary() > 0 && DescriptorFor(f.q.Primary()[0].ID).Name != "Resurrect" {
			completed = true
			break
		}
		if f.q.LenPrimary() == 0 {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatalf("the resurrection never reached its terminal; phase %d", f.q.Primary()[0].Phase)
	}
	if product.Remaining != 0 || product.Health != 1 {
		t.Fatalf("resurrected unit remaining=%v health=%d, want 0 and 1 [05 R-WORK-01 §7]", product.Remaining, product.Health)
	}
	if _, _, _, ok := features.FeatureAt(f.econ.Terrain, world.CellToWorld(4), world.CellToWorld(5)); ok {
		t.Fatalf("the corpse is still on the grid after phase 5 removed it")
	}
	// The seam is phase-dispatched, and the order is the contract.
	if len(f.resurrectPhases) != 2 || f.resurrectPhases[0] != 3 || f.resurrectPhases[1] != 5 {
		t.Fatalf("Work.Resurrect was called at phases %v, want exactly phase 3 then phase 5", f.resurrectPhases)
	}
	found := false
	for _, c := range captions {
		if c == "Resurrection complete" {
			found = true
		}
	}
	if !found {
		t.Fatalf("captions %q carry no `Resurrection complete` [05 R-WORK-01 §7]", captions)
	}
	// "resolve command code 8 (repair) against the new unit and, when
	// resolvable, spawn it at the head" — a finished unit resolves to
	// `RepairUnit` for a ground builder [04 §3.4].
	if f.q.LenPrimary() == 0 {
		t.Fatalf("phase 6 spawned no successor record [04 R-ORD-01 §5]")
	}
	if name := DescriptorFor(f.q.Primary()[0].ID).Name; name != "RepairUnit" {
		t.Fatalf("successor record is %s, want RepairUnit against the resurrected unit", name)
	}
	if f.q.Primary()[0].Target != product.Handle {
		t.Fatalf("the successor repairs handle %d, not the resurrected unit", f.q.Primary()[0].Target)
	}
}

// TestResurrectionOfAnUnresolvableCorpseUsesRetailsMisspelling locks the two
// distinct failure strings of [05 R-WORK-01 §7]: `Resurrection failed` for "no
// feature at the recorded position" and `Ressurection failed` — retail's
// doubled `s` — for "the corpse name did not resolve to a unit definition".
// Both are reproduced verbatim, and they are not interchangeable.
func TestResurrectionOfAnUnresolvableCorpseUsesRetailsMisspelling(t *testing.T) {
	corpse := &content.FeatureDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "notaunit_dead"},
		Height:           20, FootprintX: 1, FootprintZ: 1, Reclaimable: true,
	}
	f := newFeatureWorkFixture(t, []*content.FeatureDef{corpse}, 4, 5)
	f.builder.X, f.builder.Z = world.CellToWorld(4), world.CellToWorld(5)
	bindResurrectSeam(f, nil, nil) // the catalogue resolves nothing

	var captions []string
	f.q.Binding().Presentation = &PresentationAdapter{
		Status: func(_ *units.Unit, _ uint8, text string) bool {
			captions = append(captions, text)
			return true
		},
	}
	f.q.Push(Lookup("Resurrect"), Node{Owner: f.builder.Handle, GoalX: world.CellToWorld(4), GoalZ: world.CellToWorld(5)})
	for tick := uint32(1); tick <= 50 && f.q.LenPrimary() > 0; tick++ {
		f.q.Pump(f.builder, tick)
	}
	if f.q.LenPrimary() != 0 {
		t.Fatalf("an unresolvable corpse left the record at phase %d; the row abandons", f.q.Primary()[0].Phase)
	}
	last := ""
	if len(captions) > 0 {
		last = captions[len(captions)-1]
	}
	if last != "Ressurection failed" {
		t.Fatalf("caption %q, want retail's doubled-s `Ressurection failed` [05 R-WORK-01 §7]", last)
	}

	// The single-s spelling belongs to the OTHER failure: no feature at all.
	g := newFeatureWorkFixture(t, []*content.FeatureDef{corpse}, 4, 5)
	var other []string
	g.q.Binding().Presentation = &PresentationAdapter{
		Status: func(_ *units.Unit, _ uint8, text string) bool { other = append(other, text); return true },
	}
	g.q.Push(Lookup("Resurrect"), Node{Owner: g.builder.Handle, GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(12)})
	g.q.Pump(g.builder, 1)
	if len(other) != 1 || other[0] != "Resurrection failed" {
		t.Fatalf("an empty cell raised %q, want the single-s `Resurrection failed`", other)
	}
}

// TestResurrectionDelayIsThreeTenthsOfBuildTime locks the arithmetic the seam
// leaves in this package: `trunc(0.3 · buildtime / (workertime / 30))` with the
// inner division integer, and the sub-thirty `workertime` edge whose infinity
// converts to a ZERO low word — so the resurrection completes immediately with
// no spray, rather than waiting forever [05 R-WORK-01 §7][01 §7].
func TestResurrectionDelayIsThreeTenthsOfBuildTime(t *testing.T) {
	cases := []struct {
		buildTime, workerTime, want int32
	}{
		{3000, 300, 90},   // q = 10, trunc(900/10)
		{3000, 320, 90},   // q floors to 10: workertime is not proportional
		{3000, 29, 0},     // sub-thirty: the out-of-range conversion's zero low word
		{1000, 30, 300},   // q = 1
		{1001, 30, 300},   // trunc(300.3)
		{-1000, 30, -300}, // a negative authored buildtime never equals zero
	}
	for _, c := range cases {
		if got := ResurrectionDelay(c.buildTime, c.workerTime); got != c.want {
			t.Fatalf("delay(buildtime %d, workertime %d) = %d, want %d", c.buildTime, c.workerTime, got, c.want)
		}
	}
}

// TestLiveTargetRectangleOriginIsTheFootprintSnap locks the origin the
// `RepairUnit` and `Capture` rows pass to the rectangle installer. Retail hands
// the constructor the target's committed anchor cell pair — the footprint snap
// `(pos − foot·2^19 + 2^19) >> 20` of [04 R-ORD-01 §1], the cell containing the
// footprint's minimum edge — and the same quantity is what the follower's
// arrival test reads back off the mover [04 R-PATH-01 §12].
//
// Corrected 2026-09-01 (WU-19-24): the rows derived the origin as
// `WorldToCell(pos) − footprint/2`. That agrees with the snap for a target
// resting exactly on its footprint centre, which is why it survived — every
// fixture target is a placed building — but it is a different cell for any
// off-centre target with a footprint of two cells or more, which is every
// repairable mover mid-step.
func TestLiveTargetRectangleOriginIsTheFootprintSnap(t *testing.T) {
	// The reverse conversion of [04 R-ORD-01 §1]: a footprint centred on
	// anchor cell `a` sits at `pos = (foot + 2a)·2^19`. The snap must return
	// `a` for every footprint size.
	for foot := int32(1); foot <= 4; foot++ {
		for _, a := range []int32{0, 9, 37} {
			pos := numeric.Fixed((int64(foot) + 2*int64(a)) << 19)
			if got := footprintAnchorCell(pos, foot); got != a {
				t.Fatalf("footprint %d centred on anchor %d snapped to %d", foot, a, got)
			}
		}
	}
	// An off-centre 2x2 target ten world units past the centre of anchor 9.
	// The minimum edge stands at cell 9.625, which snaps to 10; the retired
	// `WorldToCell(170) − 2/2` derivation answered 9.
	pos := numeric.Fixed(170 << 16)
	if got := footprintAnchorCell(pos, 2); got != 10 {
		t.Fatalf("off-centre 2x2 target snapped to %d, want the committed anchor 10 [04 R-ORD-01 §1]", got)
	}
	if old := world.WorldToCell(pos) - 2/2; old != 9 {
		t.Fatalf("the retired derivation is assumed to answer 9 here; it answered %d", old)
	}
	// A footprint below one cell is read as one rather than dividing by zero
	// or biasing the snap backwards.
	if got, want := footprintAnchorCell(numeric.Fixed(1<<19), 0), footprintAnchorCell(numeric.Fixed(1<<19), 1); got != want {
		t.Fatalf("a zero footprint snapped to %d, want the one-cell answer %d", got, want)
	}
}
