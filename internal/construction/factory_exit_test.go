// Factory exit-spot placement contracts [05 "Factory production lifecycle"]
// C16-C18 and the structures registry that stands in for retail's separate
// building-mask layer [04 §6.2].
package construction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func exitTerrain(w, h int32) *world.Terrain {
	t := &world.Terrain{CellW: w, CellH: h, Plot: make([]world.PlotCell, w*h)}
	for i := range t.Plot {
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
		t.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	return t
}

func exitMobileDef(name string, fx, fz int32) *content.UnitDef {
	d := newProductDef(name, fx, fz, 50, 100)
	d.BMCode = true
	d.MovementClass = "exitmove"
	return d
}

func exitCatalog(defs ...*content.UnitDef) *content.Catalog {
	cat := &content.Catalog{
		Units:    map[string]*content.UnitDef{},
		Movement: map[string]*content.MovementClass{},
	}
	for _, d := range defs {
		cat.Units[d.CanonicalKey] = d
	}
	// thresholds mirror a stock tank-class profile; values asserted by
	// TestExitQuerySkipsAggregatesSiteKeepsThem, not here.
	cat.Movement["exitmove"] = &content.MovementClass{
		FootprintX: 1, FootprintZ: 1,
		MaxSlope: 15, BadSlope: 7,
		MaxWaterDepth: 12, MinWaterDepth: 0,
	}
	return cat
}

func exitService(t *testing.T, terrain *world.Terrain, cat *content.Catalog) (*Service, *units.World) {
	svc := NewService(terrain, cat, units.NewSliced(64, cat), &economy.Service{})
	if svc == nil {
		t.Fatal("nil service")
	}
	return svc, svc.World
}

func stepUntil(t *testing.T, svc *Service, cat *content.Catalog, builder *units.Unit, maxSteps int, wantDefs ...string) pool.Handle {
	ctx := TickContext{World: svc.World, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	for i := 0; i < maxSteps; i++ {
		ctx.Tick++
		svc.Economy.TickPlayer(int(builder.Owner), uint32(i+1), svc.World, func() {})
		svc.StepUnit(ctx, builder.Handle)
		if t.Failed() {
			return 0
		}
		var found pool.Handle
		for _, u := range svc.World.Iter() {
			if u == nil || !u.Alive || u.Def == nil || u.Handle == builder.Handle {
				continue
			}
			if u.Remaining > 0 || u.Flags&FlagCompleted == 0 {
				continue
			}
			for _, want := range wantDefs {
				if content.CanonicalKey(u.Def.UnitName) == content.CanonicalKey(want) {
					found = u.Handle
				}
			}
		}
		if found != 0 {
			return found
		}
	}
	if h := headNodeForTest(builder); h != nil {
		t.Logf("[stage-node] id=%d(%s) phase=%d dl=%d target=%d gate=%d", h.ID, orders.DescriptorFor(h.ID).Name, h.Phase, h.Deadline, h.Target, h.DynamicGate)
	} else {
		t.Log("[stage-node] queue-empty")
	}
	var unitsTxt []string
	for _, u := range svc.World.Iter() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		unitsTxt = append(unitsTxt, fmt.Sprintf("%d:%s rem=%.4f hp=%d compl=%v", u.Handle, u.Def.UnitName, u.Remaining, u.Health, u.Flags&FlagCompleted != 0))
	}
	t.Fatalf("builder %d never produced %v within %d steps; alive=[%s]; messages=%v", builder.Handle, wantDefs, maxSteps, strings.Join(unitsTxt, " | "), svc.Messages())
	return 0
}

func exitRect(x, z, foot int32) world.FootprintRect {
	e, err := world.NewFootprintExtent(foot, foot)
	if err != nil {
		panic(err)
	}
	r, err := world.NewFootprintRect(world.NewFootprintAnchor(x, z), e)
	if err != nil {
		panic(err)
	}
	return r
}

func headNodeForTest(u *units.Unit) *orders.Node {
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return nil
	}
	return q.Primary()[0]
}

// TestFactoryExitValidatesInsideOwnCompletedYard locks the observed deadlock
// fix: after a factory completes, its frame occupancy stamps are released and
// the finished building registers in the structures registry, so its first
// production validates inside its own yard instead of retrying in silent
// blocked revalidation every 15 ticks forever.
func TestFactoryExitValidatesInsideOwnCompletedYard(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	comDef := newProductDef("exitcom", 1, 1, 10, 10)
	comDef.WorkerTime = 300
	comDef.BMCode = true // mobile commander-class fixture
	gate := newProductDef("exitgate", 3, 3, 50, 100)
	mob := exitMobileDef("exitmob", 2, 2)
	cat := exitCatalog(lab, comDef, gate, mob)

	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)
	// Deep stocks so the two-resource admission never starves the fixture.
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock[0] = 1e9
		svc.Economy.Players[i].Stock[1] = 1e9
		svc.Economy.Players[i].Capacity[0] = 2e9
		svc.Economy.Players[i].Capacity[1] = 2e9
	}

	hc, _ := w.Create(comDef, 0, world.CellToWorld(4), 0, world.CellToWorld(10))
	com := w.Unit(hc)

	const siteX, siteZ = 6, 10
	if err := QueueMobileBuild(com, "exitlab", world.CellToWorld(siteX), world.CellToWorld(siteZ), 1, cat); err != nil {
		t.Fatalf("QueueMobileBuild: %v", err)
	}
	labH := stepUntil(t, svc, cat, com, 20000, "exitcom", "exitlab")

	for z := int32(siteZ); z < siteZ+4; z++ {
		for x := int32(siteX); x < siteX+4; x++ {
			if cell := terrain.PlotAt(x, z); cell.OccupantA() != 0 {
				t.Fatalf("completed building still occupies plot %d,%d (occA=%d): its own exit validation would deadlock", x, z, cell.OccupantA())
			}
		}
	}
	if _, ok := svc.PlacementForProduct(labH); ok {
		t.Fatal("frame placement record should be retired at completion")
	}
	labRect := exitRect(siteX, siteZ, 4)
	if h, blocked := svc.StructureBlocks(0, labRect); !blocked || h != labH {
		t.Fatalf("StructureBlocks(0, lab rect) = (%d,%v), want (%d,true)", h, blocked, labH)
	}
	if _, blocked := svc.StructureBlocks(labH, labRect); blocked {
		t.Fatal("a structure must never block against itself")
	}

	// The completed lab now produces a mobile unit from its authored exit
	// transform while its own footprint remains registered in the building mask.
	labU := w.Unit(labH)
	bindConstructionFixture(labU, trivialModel(1, nil), true)
	if err := QueueFactoryBuild(labU, "exitmob", 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}
	prodH := stepUntil(t, svc, cat, labU, 4000, "exitmob")
	prodU := w.Unit(prodH)
	if prodU == nil || !prodU.Alive || prodU.Remaining != 0 || prodU.Flags&FlagCompleted == 0 {
		t.Fatalf("mobile product did not complete: alive=%v rem=%.3f flags=%x", prodU != nil && prodU.Alive, prodU.Remaining, func() uint32 {
			if prodU == nil {
				return 0
			}
			return prodU.Flags
		}())
	}
	// Completed mobile unit: frame stamps released, no structure registration.
	if _, ok := svc.PlacementForProduct(prodH); ok {
		t.Fatal("completed mobile unit must not retain frame placement")
	}
	if _, isStruct := svc.structures[prodH]; isStruct {
		t.Fatal("completed mobile unit must not register as a structure")
	}
	if _, isStruct := svc.structures[labH]; !isStruct {
		t.Fatal("completed factory must register as a structure")
	}
}

// TestForeignOccupantStillBlocksExitSilently keeps the silent-revalidation
// contract for genuinely foreign occupants [05 C17]: no message, no
// allocation, wake bits {1,2} with the exact 15-tick schedule.
func TestForeignOccupantStillBlocksExitSilently(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	mob := exitMobileDef("exitmob", 2, 2)
	cat := exitCatalog(lab, mob)
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)
	// Deep stocks so the two-resource admission never starves the fixture.
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock[0] = 1e9
		svc.Economy.Players[i].Stock[1] = 1e9
		svc.Economy.Players[i].Capacity[0] = 2e9
		svc.Economy.Players[i].Capacity[1] = 2e9
	}

	hf, _ := w.Create(lab, 0, world.CellToWorld(8), 0, world.CellToWorld(8))
	factory := w.Unit(hf)
	bindConstructionFixture(factory, trivialModel(1, nil), true)

	if err := QueueFactoryBuild(factory, "exitmob", 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}
	// Blanket the snapped exit rectangle (handle 1 -> zero offset -> exit at
	// the factory origin) with one foreign identity.
	for _, c := range [][2]int32{{7, 7}, {8, 7}, {7, 8}, {8, 8}} {
		terrain.PlotAt(c[0], c[1]).SetOccupantA(77)
	}

	head := headNodeForTest(factory)
	svc.Pump(factory, 100) // state 0 raises activate and runs state 1 once
	if head.Phase < uint8(State1) {
		t.Fatalf("state after pump = %d, want >=1", head.Phase)
	}
	factory.InBuildStance = true // authored yard-door state for this fixture
	blockedSeen := false
	for i := 0; i < 5; i++ {
		svc.Pump(factory, uint32(101+i*15))
		if head.Phase == uint8(State2) && head.Deadline > 0 {
			blockedSeen = true
		}
		if head.Target != 0 {
			t.Fatalf("product allocated despite foreign occupant: target=%d", head.Target)
		}
	}
	if !blockedSeen {
		t.Fatal("silent blocked-revalidation schedule never observed")
	}
	for _, m := range svc.Messages() {
		if strings.Contains(m, "Starting construction") {
			t.Fatalf("blocked revalidation emitted message %q; must be silent", m)
		}
	}

	// Clearing the foreign occupant lets the next cycle allocate.
	for z := int32(0); z < 24; z++ {
		for x := int32(0); x < 24; x++ {
			if c := terrain.PlotAt(x, z); c.OccupantA() == 77 {
				c.SetOccupantA(0)
			}
		}
	}
	svc.Pump(factory, 200)
	svc.Pump(factory, 201)
	if head.Target == 0 {
		t.Fatalf("no allocation after occupant left; phase=%d deadline=%d messages=%v", head.Phase, head.Deadline, svc.Messages())
	}
}

// TestExitQuerySkipsAggregatesSiteKeepsThem pins the Supported inference that
// factory exit-spot validation runs outside the inline terrain-check mode
// [04 §6.4]: aggregate slope gates stay armed for chosen sites but not for
// exits. Inference because the exit caller's mode value remains unresolved;
// TODO(question) records what would settle it.
func TestExitQuerySkipsAggregatesSiteKeepsThem(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	mob := exitMobileDef("exitmob", 2, 2)
	cat := exitCatalog(lab, mob)
	terrain := exitTerrain(24, 24)
	cliff := terrain.PlotAt(11, 11)
	cliff.SetMaxHeight(90)
	cliff.SetMinHeight(82)
	svc, _ := exitService(t, terrain, cat)

	rect := exitRect(10, 10, 2)
	yard := make([]world.YardCell, 4)
	for i := range yard {
		yard[i] = 0x06
	}
	if _, err := svc.validatePlacement(999, rect, mob, yard, true); err != nil {
		t.Fatalf("exit query rejected on sloped yard: %v", err)
	}
	if _, err := svc.validatePlacement(999, rect, mob, yard, false); err == nil || !strings.Contains(err.Error(), "slope") {
		t.Fatalf("site query accepted steep slope, want aggregate rejection; got %v", err)
	}
}
