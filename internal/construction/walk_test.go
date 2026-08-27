package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestWalkToSite(t *testing.T) {
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{
			"kb": {FootprintX: 2, FootprintZ: 2, MaxSlope: 10, MaxWaterDepth: 10, MaxWaterSlope: 10},
		},
	}
	builderDef := &content.UnitDef{UnitName: "corcom", FootprintX: 2, FootprintZ: 2, YardMap: "o", Builder: true, CanMove: true, BMCode: true, MaxDamage: 100, WorkerTime: 60, BuildTime: 100, BuildDistance: 60, MovementClass: "kb", MaxVelocity: 65536, Acceleration: 10000, TurnRate: 500, SightDistance: 300}
	builderDef.CanonicalKey = content.CanonicalKey("corcom")
	prodDef := &content.UnitDef{UnitName: "corlab", FootprintX: 6, FootprintZ: 6, YardMap: "oooooo oooooo oooooo oooooo oooooo oooooo", BMCode: false, MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100}
	prodDef.CanonicalKey = content.CanonicalKey("corlab")
	cat.Units[content.CanonicalKey("corcom")] = builderDef
	cat.Units[content.CanonicalKey("corlab")] = prodDef

	terrain := &world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 32*32)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	terrain.SeaLevel = 0

	w := units.New(20, cat)
	hb, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builder := w.Unit(hb)
	builder.Def = builderDef
	builder.X = numeric.Fixed(0)
	builder.Z = numeric.Fixed(0)
	builder.Move.Heading = 0
	siteX := world.CellToWorld(10)
	siteZ := world.CellToWorld(10)
	if err := QueueMobileBuild(builder, "corlab", siteX, siteZ, 1, cat); err != nil {
		t.Fatalf("queue err %v", err)
	}
	q := orders.QueueForUnit(builder)
	node := q.Primary()[0]
	node.Phase = uint8(State2)
	grid := movement.NewOccupancyGrid()
	fallback := movement.Profile{FootPrintX: 1, FootPrintZ: 1}
	sys := movement.NewSystem(terrain, fallback, grid)
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	sys.EnsureUnit(builder)
	svc := NewService(terrain, cat, w, nil)
	svc.Movement = sys
	svc.AllowSyntheticPlacement = true

	for tick := uint32(0); tick < 500; tick++ {
		svc.Pump(builder, tick)
		if sys.Scheduler != nil {
			sys.Scheduler.Tick(tick)
		}
		sys.BeginTick(tick)
		res := sys.StepUnit(builder.Handle, tick)
		sys.EndTick(tick)
		// Build range is measured to the nearest point of the site's footprint
		// rectangle, not to its centre: measuring to the centre would require a
		// stock commander to stand inside a 6x6 lab's own footprint to build it
		// (see Service.siteRangePoint) [R-P0-06][fmt fbi].
		rx, rz, okRange := svc.SiteRangePointPublic(node, builder.X, builder.Z)
		if !okRange {
			rx, rz = siteX, siteZ
		}
		dist2 := (int64(rx)-int64(builder.X))*(int64(rx)-int64(builder.X)) + (int64(rz)-int64(builder.Z))*(int64(rz)-int64(builder.Z))
		reach := int64(builder.Def.BuildDistance) * 65536
		t.Logf("tick %d builder %d %d dist2 %d reach2 %d moved %v hasRoute %v phase %d target %d", tick, builder.X.Raw(), builder.Z.Raw(), dist2, reach*reach, res.Moved, res.HasRoute, node.Phase, node.Target)
		if node.Target != 0 {
			t.Logf("allocated at tick %d", tick)
			if dist2 > reach*reach {
				t.Fatalf("allocated while out of range dist2 %d reach2 %d", dist2, reach*reach)
			}
			return
		}
	}
	t.Fatalf("never allocated within 500 ticks, final dist builder %d %d site %d %d", builder.X.Raw(), builder.Z.Raw(), siteX.Raw(), siteZ.Raw())
}
