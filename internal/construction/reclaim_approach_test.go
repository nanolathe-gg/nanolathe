package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The reach term unit reclaim adds to `builddistance` is the definition's
// radius word — the whole part of `(Xextent + Zextent)/3`, with both extents
// footprint-derived [04 R-ORD-01 §5][05 R-WORK-01 §2][02 R-CAT-01 §7]. It is
// silently easy to regress back to a bare `builddistance`, which is what this
// locks: the values below are the arithmetic, not a sample.
func TestUnitReclaimTargetRadiusIsTheFootprintRadiusWord(t *testing.T) {
	for _, tc := range []struct {
		footX, footZ int32
		want         int32
	}{
		{1, 1, 10}, // (2 << 20)/3 = 699050; high half 10
		{2, 2, 21},
		{3, 3, 32},
		{6, 6, 64},
		{0, 0, 0},
	} {
		def := &content.UnitDef{FootprintX: tc.footX, FootprintZ: tc.footZ}
		if got := reclaimTargetRadius(def); got != tc.want {
			t.Fatalf("radius for %dx%d = %d, want %d [04 R-ORD-01 §5]", tc.footX, tc.footZ, got, tc.want)
		}
	}
}

// The compare is `<= r*r` on whole world units, so the boundary itself is in
// range [05 R-WORK-01 §2].
func TestUnitReclaimReachIsInclusiveAndCountsTheTargetRadius(t *testing.T) {
	builderDef := &content.UnitDef{BuildDistance: 100, CanReclamate: true}
	targetDef := &content.UnitDef{FootprintX: 1, FootprintZ: 1}
	builder := &units.Unit{Def: builderDef}
	target := &units.Unit{Def: targetDef}
	const r = 100 + 10 // builddistance + the 1x1 radius word
	builder.X = numeric.FixedFromInt(r)
	if !reclaimInRange(builder, target) {
		t.Fatalf("separation of exactly %d world units must be in reach [05 R-WORK-01 §2]", r)
	}
	builder.X = numeric.FixedFromInt(r + 1)
	if reclaimInRange(builder, target) {
		t.Fatalf("separation of %d world units must be out of reach [05 R-WORK-01 §2]", r+1)
	}
	// Without the radius term the boundary would sit at `builddistance`; assert
	// the extra ten units are actually reachable.
	builder.X = numeric.FixedFromInt(105)
	if !reclaimInRange(builder, target) {
		t.Fatalf("the target's own radius must extend the reach past builddistance [04 R-ORD-01 §5]")
	}
}

// An out-of-reach `ReclaimUnit` visit installs the row's rectangle goal on the
// TARGET's footprint, which is what makes internal/session's activation
// boundary give the reclaimer its mover [04 R-ORD-01 §5][04 R-PATH-01 §12].
// Before WU-19-166 the visit installed nothing and the reclaimer never moved.
func TestUnitReclaimOutOfRangeInstallsTheTargetRectangleGoal(t *testing.T) {
	cat := &content.Catalog{
		Units:    map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{"kb": {FootprintX: 2, FootprintZ: 2, MaxSlope: 10, MaxWaterDepth: 10, MaxWaterSlope: 10}},
	}
	builderDef := &content.UnitDef{
		UnitName: "corck", FootprintX: 2, FootprintZ: 2, BMCode: 1, CanMove: true,
		CanReclamate: true, WorkerTime: 60, BuildDistance: 60, MaxDamage: 100,
		MovementClass: "kb", MaxVelocity: 65536, Acceleration: 10000, TurnRate: 500,
	}
	builderDef.CanonicalKey = content.CanonicalKey("corck")
	targetDef := &content.UnitDef{
		UnitName: "corlab", FootprintX: 6, FootprintZ: 6, BMCode: 0,
		MaxDamage: 100, BuildTime: 300, BuildCostMetal: 100,
	}
	targetDef.CanonicalKey = content.CanonicalKey("corlab")
	cat.Units[builderDef.CanonicalKey] = builderDef
	cat.Units[targetDef.CanonicalKey] = targetDef

	terrain := &world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	w := newConstructionFixtureWorld(20, cat)
	bh, err := w.Create(builderDef, 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create builder: %v", err)
	}
	builder := w.Unit(bh)
	builder.Def = builderDef
	th, err := w.Create(targetDef, 1, world.CellToWorld(20), 0, world.CellToWorld(20))
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	target := w.Unit(th)
	target.Def = targetDef
	target.Health, target.MaxHealth = 100, 100
	target.Move.Mode = 1

	q := orders.QueueForUnit(builder)
	q.Push(orders.Lookup("ReclaimUnit"), orders.Node{Owner: builder.Handle, Target: target.Handle, Deadline: -1})
	node := q.Head()

	sys := movement.NewSystem(terrain, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	sys.EnsureUnit(builder)
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.Movement = sys

	svc.StepUnit(TickContext{Tick: 1, World: w, Economy: svc.Economy, Catalog: cat, Terrain: terrain}, builder.Handle)

	if node.MoveState != orders.MoveEnRoute {
		t.Fatalf("out-of-reach visit left MoveState=%d, want MoveEnRoute", node.MoveState)
	}
	if !sys.HasGroundGoal(builder.Handle, node) {
		t.Fatalf("out-of-reach ReclaimUnit installed no ground goal [04 R-ORD-01 §5]")
	}
	if node.Phase != 1 || node.Param1 == 0 || node.Param2 != 0 || node.Deadline != 16 {
		t.Fatal("approach did not seed pulse and arm the fifteen-tick wait")
	}
	oldX, oldZ, _ := sys.MoveGoalFor(builder.Handle, node)
	target.X += numeric.FixedFromInt(64)
	svc.StepUnit(TickContext{Tick: 15}, builder.Handle)
	if x, _, _ := sys.MoveGoalFor(builder.Handle, node); x != oldX {
		t.Fatal("approach moved before its deadline")
	}
	svc.StepUnit(TickContext{Tick: 16}, builder.Handle)
	if x, z, _ := sys.MoveGoalFor(builder.Handle, node); x != oldX+numeric.FixedFromInt(64) || z != oldZ {
		t.Fatalf("new approach retained the old target rectangle: x/z=%d/%d", x, z)
	}
	node.Satisfied |= 0x20
	svc.StepUnit(TickContext{Tick: 17}, builder.Handle)
	if node.Phase != 3 || node.Param2 != 0 {
		t.Fatal("arrival bypassed the script stance wait")
	}
}
