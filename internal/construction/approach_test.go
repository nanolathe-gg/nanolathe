package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// approachFixture builds the smallest world that exercises build-site
// approach selection: a mobile builder, a large structure product, flat
// terrain, and a queued MOBILEBUILD node whose Goal is the site centre.
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
	hb, err := w.Create(builderDef, 0, 0, 0, 0)
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

// TestBuildApproachIsOutsideTheFootprint locks the contract of [07 §9] "The
// click" and [04 §7.4]: the order's stored position stays the footprint
// centre, while the goal handed to path search is a perimeter candidate that
// leaves the whole site clear. Routing the builder at the centre instead put
// it inside its own site, where the null-self commit check
// [05 "Silent blocked revalidation before allocation"] rejects the placement
// on every retry.
func TestBuildApproachIsOutsideTheFootprint(t *testing.T) {
	const siteCellX, siteCellZ = 10, 10
	svc, builder, node := approachFixture(t, siteCellX, siteCellZ)

	anchorX, anchorZ, footX, footZ, ok := svc.siteAnchorCell(node)
	if !ok {
		t.Fatalf("site anchor unresolved for a queued MOBILEBUILD node")
	}

	// The order's stored position is still the footprint centre [07 §9]; the
	// approach must not have moved it.
	if node.GoalX != world.CellToWorld(siteCellX) || node.GoalZ != world.CellToWorld(siteCellZ) {
		t.Fatalf("order goal was rewritten: got (%d,%d) want (%d,%d)",
			node.GoalX, node.GoalZ, world.CellToWorld(siteCellX), world.CellToWorld(siteCellZ))
	}

	// Guard the guard: the naive goal — the cell containing the stored centre —
	// really is inside the footprint, so a pass below is not vacuous.
	centre := path.Cell{X: world.WorldToCell(node.GoalX), Z: world.WorldToCell(node.GoalZ)}
	if !insideRect(centre, anchorX, anchorZ, footX, footZ) {
		t.Fatalf("fixture is not exercising the bug: centre cell %+v is already outside site [%d,%d)x[%d,%d)",
			centre, anchorX, anchorX+footX, anchorZ, anchorZ+footZ)
	}

	goal, standX, standZ, ok := svc.SelectBuildApproach(builder, node)
	if !ok {
		t.Fatalf("no approach candidate selected for a legal site on flat terrain")
	}
	// The builder's whole footprint, not merely its anchor cell, must clear the
	// site: a builder parked on its own build site is not standing "around" the
	// footprint at all [04 §7.4].
	_, _, bMinX, bMinZ, bMaxX, bMaxZ, okPt := svc.builderStandPoint(builder, goal)
	if !okPt {
		t.Fatalf("selected candidate %+v has no stand point", goal)
	}
	if rectsOverlap(bMinX, bMinZ, bMaxX, bMaxZ, anchorX, anchorZ, anchorX+footX, anchorZ+footZ) {
		t.Fatalf("builder footprint [%d,%d)x[%d,%d) at approach anchor %+v covers the site [%d,%d)x[%d,%d)",
			bMinX, bMaxX, bMinZ, bMaxZ, goal, anchorX, anchorX+footX, anchorZ, anchorZ+footZ)
	}

	// The stand point must snap back to the anchor it was derived from, with a
	// full half cell of margin on each side: the mover halts anywhere inside
	// the local steering threshold [04 §3.5], and an even-extent builder aimed
	// at a cell centre would sit exactly on the snap boundary.
	for _, delta := range []numeric.Fixed{0, 4 * 65536, -4 * 65536} {
		gotX, gotZ, okSnap := svc.builderFootprintAnchor(builder, standX+delta, standZ+delta)
		if !okSnap || gotX != goal.X || gotZ != goal.Z {
			t.Fatalf("stand point offset by %d snaps to anchor (%d,%d), want %+v", delta, gotX, gotZ, goal)
		}
	}

	// Selection is a pure function of world state: no RNG, no map iteration.
	again, againX, againZ, ok := svc.SelectBuildApproach(builder, node)
	if !ok || again != goal || againX != standX || againZ != standZ {
		t.Fatalf("approach selection is not deterministic: first %+v, second %+v (ok=%v)", goal, again, ok)
	}
}

// TestEnsureWalkSubmitsApproachNotCentre proves the selected candidate is what
// actually reaches path search: the walk request's goal is the approach cell,
// never the footprint centre [07 §9][04 §7.4].
func TestEnsureWalkSubmitsApproachNotCentre(t *testing.T) {
	const siteCellX, siteCellZ = 10, 10
	svc, builder, node := approachFixture(t, siteCellX, siteCellZ)
	// Place the builder well outside reach so the walk gate opens.
	builder.X, builder.Z = world.CellToWorld(1), world.CellToWorld(1)

	if !svc.NeedsWalk(builder, node) {
		t.Fatalf("a builder %d cells from the site should need to walk", siteCellX-1)
	}
	_, standX, standZ, ok := svc.SelectBuildApproach(builder, node)
	if !ok {
		t.Fatalf("no approach candidate selected")
	}
	want := path.Cell{X: world.WorldToCell(standX), Z: world.WorldToCell(standZ)}
	svc.ensureWalk(builder, node)

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
	centre := path.Cell{X: world.WorldToCell(node.GoalX), Z: world.WorldToCell(node.GoalZ)}
	cells := req.Goal.Enumerate(nil)
	if len(cells) != 1 {
		t.Fatalf("walk goal should be a single selected point goal [04 §7.4], got %d cells", len(cells))
	}
	if cells[0] == centre {
		t.Fatalf("walk goal is the footprint centre %+v; it must be a perimeter candidate [04 §7.4]", centre)
	}
	if cells[0] != want {
		t.Fatalf("walk goal %+v is not the selected approach candidate %+v", cells[0], want)
	}
	// The movement-goal handle carries the same point, so the mover steers at
	// the approach rather than the order's stored position [04 §8.3][04 §7.4].
	gx, gz, bound := svc.Movement.MoveGoalFor(builder.Handle, node)
	if !bound || gx != standX || gz != standZ {
		t.Fatalf("movement goal handle is (%d,%d) bound=%v, want the approach point (%d,%d)", gx, gz, bound, standX, standZ)
	}
	if gx == node.GoalX && gz == node.GoalZ {
		t.Fatalf("movement goal handle is the order's stored position; it must be the approach point [04 §7.4]")
	}
}

func insideRect(c path.Cell, minX, minZ, w, d int32) bool {
	return c.X >= minX && c.X < minX+w && c.Z >= minZ && c.Z < minZ+d
}

// TestApproachGoalRebindsOverARestoredRoute covers the save/load path. The
// movement-goal handle is derived state that no save box carries — the arrival
// handle beside it is not persisted either — so the owner must re-establish it
// on the first tick after a restore, even when the restored route is still
// active and the walk submission is therefore skipped. Binding after the
// idempotency guards would leave the mover steering at the order's stored
// position for the whole length of that restored route [04 §8.3].
func TestApproachGoalRebindsOverARestoredRoute(t *testing.T) {
	svc, builder, node := approachFixture(t, 10, 10)
	builder.X, builder.Z = world.CellToWorld(1), world.CellToWorld(1)

	// Stand in for a restore: an active route with no movement goal bound.
	svc.Movement.EnsureUnit(builder)
	svc.Movement.Routes[builder.Handle] = &movement.Route{Active: true, Count: 2}
	svc.Movement.ClearMoveGoal(builder.Handle)

	svc.ensureWalk(builder, node)

	_, standX, standZ, ok := svc.SelectBuildApproach(builder, node)
	if !ok {
		t.Fatalf("no approach candidate selected")
	}
	gx, gz, bound := svc.Movement.MoveGoalFor(builder.Handle, node)
	if !bound || gx != standX || gz != standZ {
		t.Fatalf("movement goal was not rebound over a restored route: got (%d,%d) bound=%v, want (%d,%d)", gx, gz, bound, standX, standZ)
	}
	if gx == node.GoalX && gz == node.GoalZ {
		t.Fatalf("movement goal fell back to the order's stored position after restore")
	}
	// The active route must still suppress a duplicate submission.
	for _, r := range svc.Movement.PathRequestsSnapshot() {
		if r.Unit == builder.Handle {
			t.Fatalf("ensureWalk resubmitted a path request while a route was active")
		}
	}
}
