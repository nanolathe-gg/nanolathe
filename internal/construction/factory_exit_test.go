// Factory exit-spot placement contracts [05 "Factory production lifecycle"]
// C16-C18 and the canonical yard-selected plot occupancy [04 R-COLL-01 §4].
package construction

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/movement"
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
	t.Helper()
	svc := NewService(terrain, cat, newConstructionFixtureWorld(64, cat), &economy.Service{})
	if svc == nil {
		t.Fatal("nil service")
	}
	return svc, svc.World
}

func stepUntil(t *testing.T, svc *Service, cat *content.Catalog, builder *units.Unit, maxSteps int, wantDefs ...string) pool.Handle {
	t.Helper()
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

// TestFactoryExitValidatesInsideOwnCompletedYard locks the retail exemption:
// an accepted yard-open transaction releases c/C pad cells before state 2,
// while the completed factory retains its placement record.
func TestFactoryExitValidatesInsideOwnCompletedYard(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	lab.YardMap = "yyyy yccy yccy yyyy"
	lab.MinWaterDepth = -10000 // established land-profile template [04 §6.1]
	comDef := newProductDef("exitcom", 1, 1, 10, 10)
	comDef.WorkerTime = 300
	comDef.BMCode = true // mobile commander-class fixture
	gate := newProductDef("exitgate", 3, 3, 50, 100)
	mob := exitMobileDef("exitmob", 2, 2)
	cat := exitCatalog(lab, comDef, gate, mob)
	cat.Movement["exitmove"].MinWaterDepth = -10000

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

	labRect, ok := svc.PlacementForProduct(labH)
	if !ok {
		t.Fatal("completed factory did not retain its placement record")
	}
	for _, c := range [][2]int32{{labRect.MinX() + 1, labRect.MinZ() + 1}, {labRect.MinX() + 2, labRect.MinZ() + 1}, {labRect.MinX() + 1, labRect.MinZ() + 2}, {labRect.MinX() + 2, labRect.MinZ() + 2}} {
		if got := terrain.PlotAt(c[0], c[1]).OccupantA(); got != int16(labH) {
			t.Fatalf("closed factory pad %d,%d occupant=%d, want %d", c[0], c[1], got, labH)
		}
	}

	labU := w.Unit(labH)
	if !svc.YardOpenTransaction(labU, true) {
		t.Fatal("unoccupied completed factory yard refused open")
	}
	for z := labRect.MinZ(); z < labRect.MaxZ(); z++ {
		for x := labRect.MinX(); x < labRect.MaxX(); x++ {
			if cell := terrain.PlotAt(x, z); cell.OccupantA() != 0 {
				t.Fatalf("open factory retained ground word at %d,%d: %d", x, z, cell.OccupantA())
			}
		}
	}

	// The completed lab now produces a mobile unit only through its genuinely
	// released c/C pad cells [04 R-FAC-02 §5].
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
	// Completed mobile unit: frame stamps and placement record release.
	if _, ok := svc.PlacementForProduct(prodH); ok {
		t.Fatal("completed mobile unit must not retain frame placement")
	}
	if _, ok := svc.PlacementForProduct(labH); !ok {
		t.Fatal("completed factory placement record was lost during production")
	}
}

// TestForeignOccupantStillBlocksExitSilently keeps the silent-revalidation
// contract for genuinely foreign occupants [05 C17]: no message, no
// allocation, wake bits {1,2} with the exact 15-tick schedule.
func TestForeignOccupantStillBlocksExitSilently(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	mob := exitMobileDef("exitmob", 2, 2)
	cat := exitCatalog(lab, mob)
	cat.Movement["exitmove"].MinWaterDepth = -10000 // established land-profile template [04 §6.1]
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

// TestExitQueryKeepsAggregates locks [04 R-FAC-02 §4]: the factory exit-spot
// query runs in the inline terrain-check mode (mode 1, like a chosen site),
// so the aggregate slope gate rejects a steep exit cell. The skip flag only
// exists for domain-level callers and must not be what the exit path uses.
func TestExitQueryKeepsAggregates(t *testing.T) {
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
	if _, err := svc.validatePlacement(999, rect, mob, yard, false); err == nil || !strings.Contains(err.Error(), "slope") {
		t.Fatalf("exit query accepted steep slope, want aggregate rejection [04 R-FAC-02 §4]; got %v", err)
	}
	if _, err := svc.validatePlacement(999, rect, mob, yard, true); err != nil {
		t.Fatalf("domain-skip query rejected on sloped yard: %v", err)
	}
}

// TestYardStateFollowsBothOccupancyLayers locks the split-plane contract: a
// building holds, in the terrain plot AND in the movement occupancy grid the
// ground validator reads, exactly the cells its current yard state selects
// [04 R-COLL-01 §4]. Retail has one ground word per cell; Nanolathe has two
// layers, and a yard-open transaction that released only the plot left the
// factory blocking its own exit spot forever [04 R-FAC-02 §5].
func TestYardStateFollowsBothOccupancyLayers(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	// 'o' is selected in both yard states, 'c' only while closed, 'y' never
	// [04 R-COLL-01 §4].
	lab.YardMap = "yooy occo occo yooy"
	cat := exitCatalog(lab)
	cat.Movement["exitmove"].MinWaterDepth = -10000 // land-profile template [04 §6.1]
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)
	svc.Movement = movement.NewSystem(terrain, movement.Template(), movement.NewOccupancyGrid())

	h, err := w.Create(lab, 0, world.CellToWorld(8), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	u := w.Unit(h)
	if err := svc.RegisterBuildingPlacement(u); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	// Initial movement occupancy is published before any COB yard write. It
	// must already agree with the closed authored yard state [04 R-COLL-01 §4].
	svc.Movement.BindWorld(w)
	svc.Movement.EnsureUnit(u)
	rect, ok := svc.PlacementForProduct(h)
	if !ok {
		t.Fatal("building did not retain its placement record")
	}
	yard, err := world.ParseYardMap(lab.YardMap, int(rect.Width()), int(rect.Depth()))
	if err != nil {
		t.Fatalf("ParseYardMap: %v", err)
	}
	// Both layers agree with the yard selection for the state under test.
	check := func(stage string, open bool) {
		t.Helper()
		for z := rect.MinZ(); z < rect.MaxZ(); z++ {
			for x := rect.MinX(); x < rect.MaxX(); x++ {
				want := yard[int((z-rect.MinZ())*rect.Width()+(x-rect.MinX()))].Selects(open)
				if got := terrain.PlotAt(x, z).OccupantA() == int16(h); got != want {
					t.Fatalf("%s: plot cell %d,%d held=%v, want %v", stage, x, z, got, want)
				}
				id, held := svc.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z})
				if held != want || (held && id != int(h)) {
					t.Fatalf("%s: grid cell %d,%d held=%v occupant=%d, want held=%v by %d", stage, x, z, held, id, want, h)
				}
			}
		}
	}
	check("closed", false)

	if !svc.YardOpenTransaction(u, true) {
		t.Fatal("unoccupied yard refused open")
	}
	check("open", true)

	if !svc.YardOpenTransaction(u, false) {
		t.Fatal("unoccupied yard refused close")
	}
	check("closed again", false)

	// The exit-spot query is the reader that was deadlocking: with the yard
	// open the released pad admits a mobile product under null self identity
	// [04 R-FAC-02 §4-§5], and with it closed it does not.
	mob := exitMobileDef("exitmob", 2, 2)
	cat.Units[mob.CanonicalKey] = mob
	pad := exitRect(rect.MinX()+1, rect.MinZ()+1, 2)
	padYard := make([]world.YardCell, 4)
	for i := range padYard {
		padYard[i] = 0x06
	}
	if _, err := svc.validatePlacement(h, pad, mob, padYard, false); err == nil {
		t.Fatal("closed yard admitted a product onto its own pad")
	}
	if !svc.YardOpenTransaction(u, true) {
		t.Fatal("unoccupied yard refused reopen")
	}
	if _, err := svc.validatePlacement(h, pad, mob, padYard, false); err != nil {
		t.Fatalf("open yard rejected its own released pad: %v", err)
	}

	// Release drops both layers together.
	if !svc.ReleasePlacement(h) {
		t.Fatal("ReleasePlacement reported nothing to release")
	}
	for z := rect.MinZ(); z < rect.MaxZ(); z++ {
		for x := rect.MinX(); x < rect.MaxX(); x++ {
			if got := terrain.PlotAt(x, z).OccupantA(); got != 0 {
				t.Fatalf("released plot cell %d,%d occupant=%d", x, z, got)
			}
			if _, held := svc.Movement.Grid.OccupantAt(movement.Cell{X: x, Z: z}); held {
				t.Fatalf("released grid cell %d,%d still held", x, z)
			}
		}
	}
}

// TestBlockedExitRetriesOnTheFifteenthTick locks C3 / [05 C17]: the silent
// blocked revalidation "schedules a retry in exactly 15 ticks … and repeats
// every 15 ticks for as long as the footprint is obstructed". The handler used
// to re-run the whole exit-spot query on every visit, so the probe recorded an
// admission attempt on every consecutive tick.
func TestBlockedExitRetriesOnTheFifteenthTick(t *testing.T) {
	lab := newFactoryDef("exitlab", 4, 4, 300)
	mob := exitMobileDef("exitmob", 2, 2)
	cat := exitCatalog(lab, mob)
	cat.Movement["exitmove"].MinWaterDepth = -10000 // land-profile template [04 §6.1]
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)

	hf, err := w.Create(lab, 0, world.CellToWorld(8), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(hf)
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	if err := QueueFactoryBuild(factory, "exitmob", 1, cat); err != nil {
		t.Fatalf("QueueFactoryBuild: %v", err)
	}
	// A foreign identity over the snapped exit rectangle keeps state 2 blocked.
	for _, c := range [][2]int32{{7, 7}, {8, 7}, {7, 8}, {8, 8}} {
		terrain.PlotAt(c[0], c[1]).SetOccupantA(77)
	}
	head := headNodeForTest(factory)

	// The fixture enters with the build stance already high, so state 0 and
	// state 1 both complete inside the first pass and state 2 runs on the
	// first pumped tick [05 "Factory production lifecycle"].
	const start = 100
	var attempts []uint32
	for tick := uint32(start); tick <= start+45; tick++ {
		before := len(svc.AdmissionDiagnostics())
		svc.Pump(factory, tick)
		for _, a := range svc.AdmissionDiagnostics()[before:] {
			attempts = append(attempts, a.Tick)
		}
		if head.Target != 0 {
			t.Fatalf("product allocated despite foreign occupant at tick %d", tick)
		}
	}
	want := []uint32{start, start + 15, start + 30, start + 45}
	if len(attempts) != len(want) {
		t.Fatalf("admission attempts %v, want exactly %v [05 C17]", attempts, want)
	}
	for i, tick := range want {
		if attempts[i] != tick {
			t.Fatalf("admission attempts %v, want %v [05 C17]", attempts, want)
		}
	}
	if head.DynamicGate != WakeBit1|WakeBit2 || head.Deadline != int32(start+60) {
		t.Fatalf("blocked node gate=%d deadline=%d, want gate=%d deadline=%d",
			head.DynamicGate, head.Deadline, WakeBit1|WakeBit2, start+60)
	}
}
