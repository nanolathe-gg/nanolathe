package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// approachFixture builds the smallest world that exercises the mobile-build
// approach: a 2x2 mobile builder, a 6x6 structure product, flat terrain, and a
// queued MOBILEBUILD node whose Goal is the site centre.
func approachFixture(t *testing.T, siteCellX, siteCellZ int32) (*Service, *units.Unit, *orders.Node) {
	t.Helper()
	// Cell 2 keeps the even-footprint builder's committed anchor in bounds; see
	// the comment at the Create call below.
	return approachFixtureAt(t, siteCellX, siteCellZ, world.CellToWorld(2), world.CellToWorld(2))
}

// approachFixtureAt is approachFixture with the builder's start position given.
// The position matters to any test that asks about ARRIVAL: the follower tests
// the mover's committed anchor [04 R-MOV-03 §2], and a unit whose world position
// is assigned after creation keeps the anchor its creation stamped, because a
// mover that does not move commits nothing. A test that needs the builder to
// stand somewhere for the arrival predicate must therefore be created there.
func approachFixtureAt(t *testing.T, siteCellX, siteCellZ int32, startX, startZ numeric.Fixed) (*Service, *units.Unit, *orders.Node) {
	t.Helper()
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{
			"kb": {FootprintX: 2, FootprintZ: 2, MaxSlope: 10, MaxWaterDepth: 10, MaxWaterSlope: 10},
		},
	}
	builderDef := &content.UnitDef{
		UnitName: "corcom", FootprintX: 2, FootprintZ: 2, YardMap: "o",
		Builder: true, CanMove: true, BMCode: 1, MaxDamage: 100,
		WorkerTime: 60, BuildTime: 100, BuildDistance: 60, MovementClass: "kb",
		MaxVelocity: 65536, Acceleration: 10000, TurnRate: 500, SightDistance: 300,
	}
	builderDef.CanonicalKey = content.CanonicalKey("corcom")
	prodDef := &content.UnitDef{
		UnitName: "corlab", FootprintX: 6, FootprintZ: 6,
		YardMap: "oooooo oooooo oooooo oooooo oooooo oooooo",
		BMCode:  0, MaxDamage: 100, BuildTime: 100,
		BuildCostMetal: 100, BuildCostEnergy: 100,
	}
	prodDef.CanonicalKey = content.CanonicalKey("corlab")
	cat.Units[builderDef.CanonicalKey] = builderDef
	cat.Units[prodDef.CanonicalKey] = prodDef

	terrain := &world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}

	w := newConstructionFixtureWorld(20, cat)
	// A 2x2 mover centred at cell zero has cached anchor (-1,-1), which is an
	// intentionally invalid retail request start. Keep this general approach
	// fixture in bounds so it tests the goal install rather than the path setup
	// bounds exit [04 R-PATH-01 §4 step 1].
	hb, err := w.Create(builderDef, 0, startX, 0, startZ)
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	builder := w.Unit(hb)
	builder.Def = builderDef
	bindConstructionFixture(builder, trivialModel(1, nil), false)

	siteX, siteZ := world.CellToWorld(siteCellX), world.CellToWorld(siteCellZ)
	if err := QueueMobileBuild(builder, "corlab", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("queue mobile build: %v", err)
	}
	// The queue is restored with its session context explicitly attached. The
	// movement lifecycle must never synthesize context from legacy queue fields.
	binding := &orders.QueueBinding{SimRNG: rng.Global.Sim, Lookup: w.Unit}
	orders.QueueForUnit(builder).SetBinding(binding)
	node := orders.QueueForUnit(builder).Primary()[0]
	// The record is left in the state the approach phase's own first visit
	// leaves it in: phase State1 — the mobile row's approach — parked on retail's
	// `0xE0` gate with no deadline [05 R-WORK-01 §13]. Arming the gate here is
	// what lets a test raise one of the three movement outcomes and have the
	// ORDER pump deliver it, which since WU-19-225 is the only delivery path.
	node.Phase = uint8(State1)
	node.DynamicGate = orders.ApproachWakeGate
	node.Deadline = -1

	sys := movement.NewSystem(terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	sys.EnsureUnit(builder)

	svc := NewService(terrain, cat, w, nil)
	svc.Movement = sys
	return svc, builder, node
}

// pumpApproach runs one tick of the two pumps in the session's own order
// (internal/session/step.go): the ORDER pump first, then this service's per-unit
// step. The order matters and is the whole point of the pair — a record parked
// on `0xE0` receives its movement outcome as the registered handler's satisfied
// argument, and only the order pump computes that set [04 §3.3]
// [05 R-WORK-01 §13].
func pumpApproach(svc *Service, builder *units.Unit, tick uint32) {
	if q := orders.QueueOfUnit(builder); q != nil {
		svc.RegisterOrderHandlers(q)
		q.Pump(builder, tick)
	}
	svc.Pump(builder, tick)
}

// TestApproachInstallsTheRectangleGoal locks [04 R-PATH-01 §13]: the approach
// phase installs the RECTANGLE goal of [04 R-PATH-01 §12] on the product
// footprint and stops. "There is no candidate enumeration, no range filter, no
// sort, no bounded list and no point goal. The candidates are the grown
// rectangle's border cells, enumerated by the search."
//
// The previous version of this test asserted the opposite — that the walk goal
// enumerated exactly ONE cell, "a single selected point goal" — which is the
// mechanism §13 struck.
func TestApproachInstallsTheRectangleGoal(t *testing.T) {
	const siteCellX, siteCellZ = 10, 10
	svc, builder, node := approachFixture(t, siteCellX, siteCellZ)
	// Place the builder well outside reach so the walk gate opens.
	builder.X, builder.Z = world.CellToWorld(1), world.CellToWorld(1)

	anchorX, anchorZ, footX, footZ, ok := svc.siteAnchorCell(node)
	if !ok {
		t.Fatalf("site anchor unresolved for a queued MOBILEBUILD node")
	}
	if !svc.NeedsWalk(builder, node) {
		t.Fatalf("a builder %d cells from the site should need to walk", siteCellX-1)
	}
	svc.ensureWalk(builder, node)

	// The order's stored position is still the footprint centre [07 §9]; the
	// approach must not have moved it.
	if node.GoalX != world.CellToWorld(siteCellX) || node.GoalZ != world.CellToWorld(siteCellZ) {
		t.Fatalf("order goal was rewritten: got (%d,%d) want (%d,%d)",
			node.GoalX, node.GoalZ, world.CellToWorld(siteCellX), world.CellToWorld(siteCellZ))
	}

	var req path.Request
	found := false
	for _, r := range svc.Movement.PathRequestsSnapshot() {
		if r.Unit == builder.Handle {
			req, found = r, true
			break
		}
	}
	if !found {
		t.Fatalf("ensureWalk submitted no path request")
	}
	if req.Activation == 0 {
		t.Fatal("MobileBuild walk bypassed the active-order activation boundary")
	}

	// The payload the search is aimed at is the rectangle, not a point.
	trace := path.DescribeGoal(req.Goal)
	if trace.Unknown || trace.Kind != 3 {
		t.Fatalf("walk goal kind = %d (unknown=%v), want the rectangle-perimeter goal (kind 3) [04 R-PATH-01 §13]", trace.Kind, trace.Unknown)
	}
	// The stored rectangle is the product footprint grown by the BUILDER's own
	// footprint on the west and north and by one cell on the east and south, all
	// four bounds inclusive [04 R-PATH-01 §12].
	bx, bz := world.FootprintForUnit(svc.Catalog, builder.Def)
	want := path.Rect{
		Min: path.Cell{X: anchorX - bx, Z: anchorZ - bz},
		Max: path.Cell{X: anchorX + footX, Z: anchorZ + footZ},
	}
	if trace.Rect != want {
		t.Fatalf("goal rectangle %+v, want the grown rectangle %+v [04 R-PATH-01 §12]", trace.Rect, want)
	}

	// The candidate set is the whole border, handed to the search — not one
	// chosen cell. A rectangle of (footX+bx+1) x (footZ+bz+1) has that many
	// border cells; the point is that it is emphatically more than one, and that
	// the site's own cells are interior and never enumerated.
	cells := req.Goal.Enumerate(nil)
	wantBorder := 2*(want.Max.X-want.Min.X+1) + 2*(want.Max.Z-want.Min.Z+1) - 4
	if int32(len(cells)) != wantBorder {
		t.Fatalf("goal enumerated %d cells, want the whole border %d [04 R-MOV-03 §9]", len(cells), wantBorder)
	}
	for _, c := range cells {
		if insideRect(c, anchorX, anchorZ, footX, footZ) {
			t.Fatalf("border cell %+v lies on the product footprint; the site's own cells are interior [04 R-PATH-01 §12]", c)
		}
	}

	// The construction handler and session reconciliation can both visit this
	// seam in one tick. The current head must retain exactly one request and
	// activation token [04 R-MOV-01 §3], and re-installing the payload every
	// visit would evict the mover's binding and resubmit.
	svc.ensureWalk(builder, node)
	repeated := svc.Movement.PathRequestsSnapshot()
	if len(repeated) != 1 || repeated[0].Unit != builder.Handle || repeated[0].Activation != req.Activation {
		t.Fatalf("repeated ensureWalk changed the active request: first=%#v repeated=%#v", req, repeated)
	}
}

// TestApproachReachIsCentreMinusPads locks [05 R-WORK-01 §2] as corrected by
// [05 R-WORK-01 §12]: the reach test measures planar centre to centre — the
// builder's origin to the site's snapped footprint centre — subtracts each
// end's `trunc(8·hypot(footX, footZ))` half-diagonal, and compares inclusively
// against `builddistance`.
//
// The reading this replaces compared `builddistance` in 16.16 against the
// distance to the NEAREST POINT of the site rectangle, which §12 retires by
// name. With a 60-pixel `builddistance` the two differ by the whole 6x6
// footprint: the old form admitted a builder 60 units from the site's edge,
// this one admits a builder up to 149 units from its centre.
func TestApproachReachIsCentreMinusPads(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)

	// Both pads, spelled out. 8·hypot(2,2)=22.62 truncates to 22;
	// 8·hypot(6,6)=67.88 truncates to 67 [05 R-WORK-01 §2].
	if got := nanoFootprintPad(2, 2); got != 22 {
		t.Fatalf("builder pad = %d, want trunc(8*hypot(2,2)) = 22", got)
	}
	if got := nanoFootprintPad(6, 6); got != 67 {
		t.Fatalf("product pad = %d, want trunc(8*hypot(6,6)) = 67", got)
	}

	centreX, centreZ, footX, footZ, ok := svc.SiteCentrePublic(node)
	if !ok {
		t.Fatalf("site centre unresolved")
	}
	if footX != 6 || footZ != 6 {
		t.Fatalf("site footprint = %dx%d, want the product's 6x6", footX, footZ)
	}

	// builddistance 60 + 22 + 67 = 149 whole world units of centre separation.
	const limit = 149
	for _, tc := range []struct {
		dist int64
		want bool
		why  string
	}{
		{limit - 1, true, "inside the limit"},
		{limit, true, "AT the limit — the comparison is inclusive [05 R-WORK-01 §2]"},
		{limit + 1, false, "one world unit past the limit"},
	} {
		builder.X = centreX - numeric.Fixed(tc.dist<<16)
		builder.Z = centreZ
		if got := svc.isWithinNanoRange(builder, centreX, centreZ, footX, footZ); got != tc.want {
			t.Fatalf("reach at %d units of centre separation = %v, want %v (%s)", tc.dist, got, tc.want, tc.why)
		}
		// outOfReach is the same test with the sign flipped. It is no longer
		// needsApproach: since WU-19-218 that predicate is the approach PHASE
		// and consults this expression only on the `0x40` wake
		// [05 R-WORK-01 §13]. TestReachIsConsultedOnTheNoRouteWakeAlone below
		// locks where it is asked.
		if got := svc.OutOfReachPublic(builder, node); got == tc.want {
			t.Fatalf("outOfReach at %d units = %v, want %v", tc.dist, got, !tc.want)
		}
	}

	// The left side is SIGNED: a builder standing inside the product's own
	// half-diagonal makes `distWorld − builderPad − targetPad` negative and
	// passes [05 R-WORK-01 §12] point 1.
	builder.X, builder.Z = centreX, centreZ
	if !svc.isWithinNanoRange(builder, centreX, centreZ, footX, footZ) {
		t.Fatalf("a builder standing on the site centre must pass the reach test with a negative left side")
	}
}

func insideRect(c path.Cell, minX, minZ, w, d int32) bool {
	return c.X >= minX && c.X < minX+w && c.Z >= minZ && c.Z < minZ+d
}

// TestApproachGoalRebindsOverARestoredRoute covers the save/load path. The
// movement goal payload is derived state that no save box carries — the arrival
// handle beside it is not persisted either — so the owner must re-establish it
// on the first tick after a restore, even when the restored route is still
// active and the walk submission is therefore skipped. Installing after the
// idempotency guards would leave the mover steering at the order's stored
// position for the whole length of that restored route [04 §8.3].
func TestApproachGoalRebindsOverARestoredRoute(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	builder.X, builder.Z = world.CellToWorld(1), world.CellToWorld(1)

	// Stand in for a restore: an active route with no movement goal or active
	// order bound. The points are deliberately distinct so adoption cannot be
	// mistaken for fresh activation's synthetic two-point route.
	svc.Movement.EnsureUnit(builder)
	route := &movement.Route{Active: true, Count: 2, LastRequestTick: 100}
	route.Points[0] = movement.Point{X: 17, Z: 19}
	route.Points[1] = movement.Point{X: 23, Z: 29}
	svc.Movement.Routes[builder.Handle] = route
	svc.Movement.ClearMoveGoal(builder.Handle)

	// Adoption occurs at a nonzero system tick distinct from the route's
	// existing request tick. Only the wants-repath poll may stamp that field.
	svc.Movement.BeginTick(137)
	svc.ensureWalk(builder, node)
	svc.Movement.EndTick(137)

	if !svc.Movement.HasGroundGoal(builder.Handle, node) {
		t.Fatalf("movement goal payload was not reinstalled over a restored route")
	}
	if !route.Active || route.Count != 2 || route.Points[0] != (movement.Point{X: 17, Z: 19}) || route.Points[1] != (movement.Point{X: 23, Z: 29}) {
		t.Fatalf("restored route was replaced during adoption: %+v", route)
	}
	if route.LastRequestTick != 100 {
		t.Fatalf("restored route request tick changed during adoption: got %d want 100", route.LastRequestTick)
	}
}

// ---------------------------------------------------------------------------
// WU-19-218 / WU-19-225: where the reach expression is consulted, and how the
// visit that consults it is delivered.
// ---------------------------------------------------------------------------

// TestReachIsConsultedOnTheNoRouteWakeAlone is the body-level lock for
// [05 R-WORK-01 §12] point 2 and [05 R-WORK-01 §13]: the mobile-build row's
// phase 1 is dispatched only on `0x20`/`0x40`/`0x80` (gate `0xE0`), the reach
// expression sits under `satisfied & 0x40`, and there is no other range term in
// the row. A visit carrying `0x20` or `0x80` without `0x40` skips the expression
// entirely — standing on the footprint's border IS the reach [04 §7.2].
//
// The first case is the one a per-visit consultation could not express: a
// builder comfortably inside `builddistance` is STILL approaching while no
// movement outcome has been delivered, because retail's phase 1 has not been
// dispatched at all.
//
// The record's PHASE is the state under test, because that is where retail
// keeps the approach's progress and where the save box carries it (byte `0x09`,
// [08 R-SAVE-ORDER-01]): State1 is the approach, State2 is placement.
func TestReachIsConsultedOnTheNoRouteWakeAlone(t *testing.T) {
	for _, tc := range []struct {
		name        string
		wake        uint32
		inReach     bool
		approaching bool
		abandon     bool
		why         string
	}{
		{"no wake, in reach", 0, true, true, false, "phase 1 is not dispatched without a movement outcome"},
		{"no wake, out of reach", 0, false, true, false, "same: the expression is not what keeps it walking"},
		{"0x40, out of reach", 0x40, false, true, true, "the one consultation, and it fails: status 7 and abandon"},
		{"0x40, in reach", 0x40, true, false, false, "the one consultation, and it passes"},
		{"0x20, out of reach", 0x20, false, false, false, "an arrival retires the approach with NO distance test"},
		{"0x80, out of reach", 0x80, false, false, false, "a released goal object likewise carries no distance test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, builder, node := approachFixture(t, 10, 10)
			if tc.inReach {
				// The site centre itself: the left side goes negative and the
				// signed compare passes [05 R-WORK-01 §12] point 1.
				cx, cz, _, _, ok := svc.SiteCentrePublic(node)
				if !ok {
					t.Fatal("queued MobileBuild site has no footprint centre")
				}
				builder.X, builder.Z = cx, cz
			}
			if got := svc.OutOfReachPublic(builder, node); got == tc.inReach {
				t.Fatalf("fixture reach = out %v, want in %v", got, tc.inReach)
			}
			code, ran := svc.MobileBuildWakeVisitPublic(builder, node, tc.wake, 0)
			if tc.abandon {
				if !ran || code != 8 {
					t.Fatalf("abandon arm = (%d, %v), want (8, true) (%s) [04 R-ORD-01 §5]", code, ran, tc.why)
				}
			} else if ran || code != 0 {
				t.Fatalf("visit = (%d, %v), want (0, false): this row is advanced by its owner's step (%s)", code, ran, tc.why)
			}
			if got := svc.needsApproach(builder, node); got != tc.approaching {
				t.Fatalf("needsApproach = %v, want %v (%s)", got, tc.approaching, tc.why)
			}
			wantPhase := State2
			if tc.approaching {
				wantPhase = State1
			}
			if State(node.Phase) != wantPhase {
				t.Fatalf("phase after the visit = %d, want %d (%s) [05 R-WORK-01 §13]", node.Phase, wantPhase, tc.why)
			}
		})
	}
}

// TestApproachWakeArrivesThroughTheOrderPump is the wiring half of the same
// contract, and the lock on the seam WU-19-225 removed: nothing in
// internal/construction reads the accumulating satisfied word. The approach
// arms `0xE0`, the ORDER pump computes `(record.satisfied | unit.pending) &
// gate`, consumes the bits out of both words and hands them to this service's
// registered handler [04 §3.3][05 R-WORK-01 §13].
func TestApproachWakeArrivesThroughTheOrderPump(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	cx, cz, fx, fz, ok := svc.SiteCentrePublic(node)
	if !ok {
		t.Fatal("queued MobileBuild site has no footprint centre")
	}
	limit := int64(builder.Def.BuildDistance) + int64(nanoFootprintPad(builder.Def.FootprintX, builder.Def.FootprintZ)) + int64(nanoFootprintPad(fx, fz))
	builder.X = cx - numeric.Fixed((limit-10)<<16)
	builder.Z = cz

	if node.DynamicGate != orders.ApproachWakeGate {
		t.Fatalf("approach gate = %#x, want 0xE0 [05 R-WORK-01 §13]", node.DynamicGate)
	}
	node.Satisfied |= 0x40
	q := orders.QueueOfUnit(builder)
	svc.RegisterOrderHandlers(q)
	q.Pump(builder, 0)

	if State(node.Phase) != State2 {
		t.Fatalf("phase %d after the pump delivered `0x40` in reach, want State2", node.Phase)
	}
	if node.Satisfied&orders.ApproachWakeGate != 0 {
		t.Fatalf("wake bits survived the dispatch: satisfied = %#x [04 §3.3]", node.Satisfied)
	}
	if node.DynamicGate != 0 {
		t.Fatalf("dispatched record kept gate %#x, want the pump's clear [04 §3.3]", node.DynamicGate)
	}
}

// TestBorderArrivalBeginsWorkWithNoDistanceTest is the border case itself: the
// builder halts on the rectangle goal's border — the ring of anchor cells at
// which its footprint is flush against the site [04 R-PATH-01 §12] — while the
// centre-minus-pads value is still above `builddistance`, and retail starts
// building there [05 R-WORK-01 §13]. The per-visit consultation this unit
// retired kept such a builder walking forever.
//
// The case is rare rather than impossible, which is what the retired marker
// said: for a square product the border corner sits about `(f+2)·8·sqrt(2)`
// world units from the site centre and the two half-diagonal pads subtract
// about as much again. What opens the gap is the asymmetry the two quantities
// are authored from — the rectangle grows by the MOVER's footprint, which
// world.FootprintForUnit reads from the movement class, while the reach's pads
// are the DEFINITION's own footprint pair [05 R-WORK-01 §2][04 R-PATH-01 §12].
// A definition whose footprint word is smaller than its movement class's is
// steered further out than its own pad accounts for, and that is what this
// fixture authors: definition footprint 1x1, movement class 2x2.
func TestBorderArrivalBeginsWorkWithNoDistanceTest(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	anchorX, anchorZ, _, _, ok := svc.siteAnchorCell(node)
	if !ok {
		t.Fatal("queued MobileBuild site has no footprint anchor")
	}
	// The north-west corner of the goal rectangle: the installer grows the
	// product footprint by the MOVER's own 2x2 on the west and north
	// [04 R-PATH-01 §12], so this cell is on the border and the product's own
	// cells stay interior.
	extent, err := world.NewFootprintExtent(2, 2)
	if err != nil {
		t.Fatalf("builder extent: %v", err)
	}
	centre, err := world.CenterForFootprint(world.NewFootprintAnchor(anchorX-2, anchorZ-2), extent)
	if err != nil {
		t.Fatalf("builder centre: %v", err)
	}
	builder.X, builder.Z = centre.X(), centre.Z()
	if svc.mustClearSite(builder, node) {
		t.Fatal("a border cell must leave the product's own cells clear [04 R-PATH-01 §12]")
	}
	// Centre separation from the border corner is hypot(64,64) = 90 whole world
	// units. With the definition's own footprint at 1x1 the builder pad is
	// trunc(8·hypot(1,1)) = 11 and the product's is 67, leaving 12 against a
	// `builddistance` of 5 [05 R-WORK-01 §2].
	builder.Def.FootprintX, builder.Def.FootprintZ = 1, 1
	builder.Def.BuildDistance = 5
	if !svc.OutOfReachPublic(builder, node) {
		t.Fatal("fixture does not exercise the border case: the reach expression still passes")
	}

	node.Satisfied |= 0x20 // the follower's arrival [04 R-PATH-01 §8]
	pumpApproach(svc, builder, 0)
	if State(node.Phase) == State1 {
		t.Fatal("an arrival at the border must retire the approach [05 R-WORK-01 §13]")
	}
	if node.Target == 0 {
		t.Fatal("a builder that arrived at the border must begin work with no distance test")
	}
}

// TestNoRouteWakeInReachBeginsWork is the passing half of the one consultation:
// `0x40` delivered, the centre-to-centre distance minus both pads inside
// `builddistance`, so the approach retires in that same visit and the record
// falls into the placement validator and the creator [05 R-WORK-01 §13].
func TestNoRouteWakeInReachBeginsWork(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	cx, cz, fx, fz, ok := svc.SiteCentrePublic(node)
	if !ok {
		t.Fatal("queued MobileBuild site has no footprint centre")
	}
	// Ten world units inside the 149-unit limit of TestApproachReachIsCentreMinusPads.
	limit := int64(builder.Def.BuildDistance) + int64(nanoFootprintPad(builder.Def.FootprintX, builder.Def.FootprintZ)) + int64(nanoFootprintPad(fx, fz))
	builder.X = cx - numeric.Fixed((limit-10)<<16)
	builder.Z = cz
	if svc.OutOfReachPublic(builder, node) {
		t.Fatal("fixture must stand inside the reach limit")
	}

	node.Satisfied |= 0x40
	pumpApproach(svc, builder, 0)
	if State(node.Phase) == State1 {
		t.Fatal("a passing reach test on the `0x40` wake retires the approach [05 R-WORK-01 §13]")
	}
	if node.Target == 0 {
		t.Fatal("the record must fall into the validator and create its product in that visit")
	}
}

// TestNoRouteWakeOutOfReachAbandons is the failing half, and it is the row's own
// arm: `0x40` with the reach test failing is status 7 `I can't reach the
// construction site` and code 8 — the pump unlinks and frees the record
// [04 R-ORD-01 §5][05 R-WORK-01 §13]. Before WU-19-225 this arm lived at the
// session boundary and read a wake delivered through a seam; it now runs inside
// the phase-1 body the pump dispatches, so the whole abandon is observable from
// one pump visit.
func TestNoRouteWakeOutOfReachAbandons(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	if !svc.OutOfReachPublic(builder, node) {
		t.Fatal("fixture must stand outside the reach limit")
	}
	type caption struct {
		kind uint8
		text string
	}
	var captions []caption
	q := orders.QueueOfUnit(builder)
	binding := q.Binding()
	binding.Presentation = &orders.PresentationAdapter{
		Status: func(_ *units.Unit, kind uint8, text string) bool {
			captions = append(captions, caption{kind: kind, text: text})
			return true
		},
	}
	q.SetBinding(binding)
	node.Satisfied |= 0x40
	pumpApproach(svc, builder, 0)

	if node.Target != 0 {
		t.Fatal("an out-of-reach builder must not create a product")
	}
	if q.LenPrimary() != 0 {
		t.Fatalf("the abandoned record is still queued: %d primary records, want the pump's unlink [04 §3.3]", q.LenPrimary())
	}
	if len(captions) != 1 || captions[0].kind != 7 || captions[0].text != orders.MobileBuildUnreachableText {
		t.Fatalf("abandon captions = %+v, want one status 7 %q [04 R-ORD-01 §5]", captions, orders.MobileBuildUnreachableText)
	}
}

// TestBuilderAlreadyOnTheBorderStartsAtOnce closes the question the wake-driven
// approach raises loudest: what happens to a builder that is ALREADY standing on
// the goal rectangle's border when the record reaches the head. Retail's phase 1
// is dispatched only on a movement outcome [05 R-WORK-01 §13], so the answer has
// to come from the follower, not from a distance test — and the follower's
// per-tick service asks the payload the arrival question first, "with a payload
// installed ... not the presence of a published route" [04 R-MOV-03 §2], so an
// already-satisfied start raises `0x20` on the next mover tick instead of
// waiting for the 60-tick re-request of [04 R-MOV-01 §7] or for a wake that
// never comes. The search's own already-satisfied notification is `0x100`, which
// is masked out of every satisfied set [04 R-ORD-01 §0] and therefore cannot
// stand in for it.
//
// The lock is the tick budget: the record must create its product within a
// handful of ticks, not tens. Without it a regression in the arrival service
// would surface only as an AI that builds at half speed.
func TestBuilderAlreadyOnTheBorderStartsAtOnce(t *testing.T) {
	// The builder must be CREATED on the border, not moved there: the follower
	// reads the committed anchor and a mover that never moves commits nothing.
	const siteCellX, siteCellZ = 10, 10
	prodExtent, err := world.NewFootprintExtent(6, 6)
	if err != nil {
		t.Fatalf("product extent: %v", err)
	}
	siteAnchor, err := world.SnapFootprintAnchor(world.CellToWorld(siteCellX), world.CellToWorld(siteCellZ), prodExtent)
	if err != nil {
		t.Fatalf("site anchor: %v", err)
	}
	site := siteAnchor.Cell()
	// West-north corner of the rectangle the installer grows by the mover's own
	// 2x2 footprint [04 R-PATH-01 §12]: anchor (originX-2, originZ-2).
	moverExtent, err := world.NewFootprintExtent(2, 2)
	if err != nil {
		t.Fatalf("builder extent: %v", err)
	}
	corner, err := world.CenterForFootprint(world.NewFootprintAnchor(site.X-2, site.Z-2), moverExtent)
	if err != nil {
		t.Fatalf("border centre: %v", err)
	}

	svc, builder, node := approachFixtureAt(t, siteCellX, siteCellZ, corner.X(), corner.Z())
	if svc.mustClearSite(builder, node) {
		t.Fatal("the border corner must leave the product's own cells clear [04 R-PATH-01 §12]")
	}
	sys := svc.Movement

	const budget = 8
	for tick := uint32(0); tick < budget; tick++ {
		pumpApproach(svc, builder, tick)
		if sys.Scheduler != nil {
			sys.Scheduler.Tick(tick)
		}
		sys.BeginTick(tick)
		sys.StepUnit(builder.Handle, tick)
		sys.EndTick(tick)
		if node.Target != 0 {
			return
		}
	}
	t.Fatalf("a builder already on the rectangle border did not start within %d ticks: phase=%d gate=%#x satisfied=%#x",
		budget, node.Phase, node.DynamicGate, node.Satisfied)
}

// TestApproachPhaseSurvivesTheSaveRoundTrip is the save half of WU-19-225, and
// the reason the approach's progress is a PHASE rather than a Go-only field on
// the record. [08 R-SAVE-ORDER-01] gives the 58-byte order record twelve words
// and one phase byte — `0x09`, "handler-private phase/state byte ... handlers
// own its interpretation" — and nothing else. A mark kept beside that record
// does not survive a save; the phase does.
//
// The defect this locks out: a builder saved mid-approach came back with its
// approach mark cleared and walked its whole route again.
func TestApproachPhaseSurvivesTheSaveRoundTrip(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	builder.X, builder.Z = world.CellToWorld(1), world.CellToWorld(1)
	if !svc.NeedsWalk(builder, node) {
		t.Fatal("fixture must start inside the approach phase")
	}

	stable := map[pool.Handle]uint16{builder.Handle: 9}
	resolve := func(h pool.Handle) (uint16, bool) { id, ok := stable[h]; return id, ok }
	roundTrip := func() *orders.Node {
		t.Helper()
		images, err := orders.RetailOrderImages(builder, resolve)
		if err != nil {
			t.Fatalf("project orders: %v", err)
		}
		records := make([]save.OrderRecord, len(images))
		for i, image := range images {
			records[i] = save.OrderRecord{
				ParentStableID: image.ParentStableID, Sequence: image.Sequence, Secondary: image.Secondary,
				Main: image.Main, SubtypeCode: image.SubtypeCode, Subtype: image.Subtype,
				DescriptorName: image.DescriptorName, BuildTypeName: image.BuildTypeName,
			}
		}
		if err := orders.RetailRestoreOrders(builder, records, map[uint16]pool.Handle{9: builder.Handle}, nil); err != nil {
			t.Fatalf("restore orders: %v", err)
		}
		return orders.QueueOfUnit(builder).Primary()[0]
	}

	restored := roundTrip()
	if State(restored.Phase) != State1 {
		t.Fatalf("restored phase = %d, want the approach phase State1 [08 R-SAVE-ORDER-01]", restored.Phase)
	}
	if !svc.NeedsWalk(builder, restored) {
		t.Fatal("a builder saved mid-approach must resume its walk [04 R-PATH-01 §13]")
	}

	// A record whose approach has already retired restores past it and does not
	// walk again: "the work phase has no range test at all" [05 R-WORK-01 §12].
	restored.Phase = uint8(State2)
	again := roundTrip()
	if State(again.Phase) != State2 {
		t.Fatalf("restored phase = %d, want the placement phase State2", again.Phase)
	}
	if svc.needsApproach(builder, again) {
		t.Fatal("a record saved past its approach must not restart the walk")
	}
}
