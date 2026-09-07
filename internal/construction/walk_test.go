package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestWalkToSite(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{
			"kb": {FootprintX: 2, FootprintZ: 2, MaxSlope: 10, MaxWaterDepth: 10, MaxWaterSlope: 10},
		},
	}
	builderDef := &content.UnitDef{UnitName: "corcom", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, CanMove: true, BMCode: 1, MaxDamage: 100, WorkerTime: 60, BuildTime: 100, BuildDistance: 60, MovementClass: "kb", MaxVelocity: 65536, Acceleration: 10000, TurnRate: 500, SightDistance: 300}
	builderDef.CanonicalKey = content.CanonicalKey("corcom")
	prodDef := &content.UnitDef{UnitName: "corlab", FootprintX: 6, FootprintZ: 6, YardMap: "oooooo oooooo oooooo oooooo oooooo oooooo", BMCode: 0, MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100}
	prodDef.CanonicalKey = content.CanonicalKey("corlab")
	cat.Units[content.CanonicalKey("corcom")] = builderDef
	cat.Units[content.CanonicalKey("corlab")] = prodDef

	terrain := &world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.SeaLevel = 0

	w := newConstructionFixtureWorld(20, cat)
	// Keep the even-footprint builder's cached committed anchor in bounds; a
	// 2x2 mover centred at cell zero correctly starts at (-1,-1) and exercises
	// request rejection instead of the walk contract [04 R-PATH-01 §4 step 1].
	startX, startZ := world.CellToWorld(2), world.CellToWorld(2)
	hb, _ := w.Create(builderDef, 0, startX, 0, startZ)
	builder := w.Unit(hb)
	bindConstructionFixture(builder, trivialModel(1, nil), false)
	builder.Def = builderDef
	builder.X = startX
	builder.Z = startZ
	builder.Move.Heading = 0
	siteX := world.CellToWorld(10)
	siteZ := world.CellToWorld(10)
	if err := QueueMobileBuild(builder, "corlab", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("queue err %v", err)
	}
	q := orders.QueueForUnit(builder)
	node := q.Primary()[0]
	// State1 is the mobile row's APPROACH phase, parked on retail's `0xE0`
	// gate; the placement phase is reached by the phase advance the movement
	// outcome drives [05 R-WORK-01 §13].
	node.Phase = uint8(State1)
	node.DynamicGate = orders.ApproachWakeGate
	node.Deadline = -1
	grid := movement.NewOccupancyGrid()
	fallback := movement.Profile{FootPrintX: 1, FootPrintZ: 1}
	sys := movement.NewSystem(terrain, fallback, grid)
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	sys.EnsureUnit(builder)
	svc := NewService(terrain, cat, w, nil)
	svc.Movement = sys

	for tick := uint32(0); tick < 500; tick++ {
		pumpApproach(svc, builder, tick)
		if sys.Scheduler != nil {
			sys.Scheduler.Tick(tick)
		}
		sys.BeginTick(tick)
		res := sys.StepUnit(builder.Handle, tick)
		sys.EndTick(tick)
		// Build range is centre to centre with both half-footprint diagonals
		// subtracted, compared inclusively against `builddistance`
		// [05 R-WORK-01 §2][05 R-WORK-01 §12].
		cx, cz, fx0, fz0, okRange := svc.SiteCentrePublic(node)
		if !okRange {
			cx, cz, fx0, fz0 = siteX, siteZ, 1, 1
		}
		inRange := svc.IsWithinNanoRangePublic(builder, cx, cz, fx0, fz0)
		t.Logf("tick %d builder %d %d inRange %v moved %v hasRoute %v phase %d target %d", tick, builder.X.Raw(), builder.Z.Raw(), inRange, res.Moved, res.HasRoute, node.Phase, node.Target)
		if node.Target != 0 {
			t.Logf("allocated at tick %d", tick)
			if !inRange {
				t.Fatalf("allocated while out of build range [05 R-WORK-01 §2]")
			}
			// The builder must stand OUTSIDE the product footprint: the walk
			// targets the rectangle goal's border, whose interior is exactly
			// the product's own cells [04 R-PATH-01 §12][04 R-PATH-01 §13], so
			// the builder never parks under its own building.
			if ax, az, fx, fz, okFoot := svc.siteAnchorCell(node); okFoot {
				cx, cz := world.WorldToCell(builder.X), world.WorldToCell(builder.Z)
				if cx >= ax && cx < ax+fx && cz >= az && cz < az+fz {
					t.Fatalf("allocated while builder centre inside footprint (%d,%d) in [%d,%d)x[%d,%d)", cx, cz, ax, az, ax+fx, az+fz)
				}
			}
			return
		}
	}
	t.Fatalf("never allocated within 500 ticks, final dist builder %d %d site %d %d", builder.X.Raw(), builder.Z.Raw(), siteX.Raw(), siteZ.Raw())
}
