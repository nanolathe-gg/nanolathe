package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
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
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{
			"kb": {FootprintX: 2, FootprintZ: 2, MaxSlope: 10, MaxWaterDepth: 10, MaxWaterSlope: 10},
		},
	}
	builderDef := &content.UnitDef{
		UnitName: "corcom", FootprintX: 2, FootprintZ: 2, YardMap: "o",
		Builder: true, CanMove: true, BMCode: true, MaxDamage: 100,
		WorkerTime: 60, BuildTime: 100, BuildDistance: 60, MovementClass: "kb",
		MaxVelocity: 65536, Acceleration: 10000, TurnRate: 500, SightDistance: 300,
	}
	builderDef.CanonicalKey = content.CanonicalKey("corcom")
	prodDef := &content.UnitDef{
		UnitName: "corlab", FootprintX: 6, FootprintZ: 6,
		YardMap: "oooooo oooooo oooooo oooooo oooooo oooooo",
		BMCode:  false, MaxDamage: 100, BuildTime: 100,
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
	hb, err := w.Create(builderDef, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
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
	node.Phase = uint8(State2)

	sys := movement.NewSystem(terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	sys.EnsureUnit(builder)

	svc := NewService(terrain, cat, w, nil)
	svc.Movement = sys
	return svc, builder, node
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
		// needsApproach is the same test with the sign flipped.
		if got := svc.needsApproach(builder, node); got == tc.want {
			t.Fatalf("needsApproach at %d units = %v, want %v", tc.dist, got, !tc.want)
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
