package construction

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func yieldFixture(t *testing.T) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	factoryDef := newFactoryDef("yieldlab", 4, 4, 300)
	factoryDef.YardMap = "yccy yccy yccy yccy"
	product := exitMobileDef("yieldproduct", 1, 1)
	product.YardMap = ""
	product.BuildTime, product.BuildCostMetal, product.BuildCostEnergy = 1, 1, 1
	product.MaxVelocity, product.Acceleration, product.BrakeRate, product.TurnRate = 2*65536, 65536/2, 65536/2, 1000
	product.StandingMoveOrder = 1
	cat := exitCatalog(factoryDef, product)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	return yieldFixtureCatalog(t, cat, factoryDef, product)
}

func yieldFixtureCatalog(t *testing.T, cat *content.Catalog, factoryDef, product *content.UnitDef) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	svc, w := exitService(t, exitTerrain(40, 40), cat)
	svc.ModernFactoryExit = true
	sys := movement.NewSystem(svc.Terrain, movement.Profile{}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	svc.Movement = sys
	sim := rng.NewSimulation(17)
	svc.OrderBinding = &orders.QueueBinding{Lookup: w.Unit, SimRNG: &sim}
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock = [2]float32{1e9, 1e9}
		svc.Economy.Players[i].Capacity = [2]float32{2e9, 2e9}
	}
	fh, err := w.Create(factoryDef, 0, world.CellToWorld(16), 0, world.CellToWorld(16))
	if err != nil {
		t.Fatal(err)
	}
	factory := w.Unit(fh)
	bindConstructionFixture(factory, trivialModel(1, nil), true)
	if err := svc.RegisterBuildingPlacement(factory); err != nil {
		t.Fatal(err)
	}
	if !svc.YardOpenTransaction(factory, true) {
		t.Fatal("yard did not open")
	}
	bh, err := w.Create(product, 0, factory.X, 0, factory.Z)
	if err != nil {
		t.Fatal(err)
	}
	blocker := w.Unit(bh)
	blocker.Flags = blocker.Flags&^StandingMoveMask | 1<<units.StandingMoveShift
	sys.EnsureUnit(blocker)
	q := svc.queueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: product.CanonicalKey, Param1: prodIdx(cat, product.CanonicalKey), Param2: 1, Phase: uint8(State2), Deadline: -1})
	node := q.Head()
	node.Phase = uint8(State2)
	return svc, factory, blocker, node
}

func TestModernFactoryExitPreservesIneligibleBlockers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Service, *units.Unit)
		issue  bool
	}{
		{"idle", func(*Service, *units.Unit) {}, true},
		{"automatic standby", func(s *Service, u *units.Unit) {
			s.queueForUnit(u).Push(orders.Lookup("Standby"), orders.Node{Flags: orders.FlagAutoOp})
		}, true},
		{"Strict", func(s *Service, _ *units.Unit) { s.ModernFactoryExit = false }, false},
		{"hold position", func(_ *Service, u *units.Unit) { u.Flags &^= StandingMoveMask }, false},
		{"enemy", func(_ *Service, u *units.Unit) { u.Owner = 1 }, false},
		{"other owner allied", func(_ *Service, u *units.Unit) { u.Owner = 2 }, false},
		{"carried", func(_ *Service, u *units.Unit) { u.Attachment.Carrier = 1 }, false},
		{"incomplete", func(_ *Service, u *units.Unit) { u.Remaining = 0.5 }, false},
		{"moving", func(_ *Service, u *units.Unit) { u.Move.Speed = 1 }, false},
		{"active route", func(s *Service, u *units.Unit) { s.Movement.Routes[u.Handle].Active = true }, false},
		{"explicit move", func(s *Service, u *units.Unit) {
			s.queueForUnit(u).Push(orders.Lookup("Move_Ground"), orders.NewMoveNode(orders.Lookup("Move_Ground"), world.CellToWorld(30), world.CellToWorld(30), 3, u.Handle, false))
		}, false},
		{"explicit standby", func(s *Service, u *units.Unit) { s.queueForUnit(u).Push(orders.Lookup("Standby"), orders.Node{}) }, false},
		{"guard", func(s *Service, u *units.Unit) {
			s.queueForUnit(u).Push(orders.Lookup("Guard"), orders.Node{Target: 1})
		}, false},
		{"repair", func(s *Service, u *units.Unit) {
			s.queueForUnit(u).Push(orders.Lookup("SelfRepair"), orders.Node{Flags: orders.FlagAutoOp})
		}, false},
		{"rear work", func(s *Service, u *units.Unit) {
			s.queueForUnit(u).Push(orders.Lookup("BuildWeapon"), orders.Node{Param1: 1})
		}, false},
		{"queued behind standby", func(s *Service, u *units.Unit) {
			q := s.queueForUnit(u)
			q.SetPrimary([]*orders.Node{{ID: orders.Lookup("Standby"), Flags: orders.FlagAutoOp}, {ID: orders.Lookup("Move_Ground")}})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, factory, u, node := yieldFixture(t)
			tc.change(svc, u)
			beforeQ := orders.QueueOfUnit(u)
			var beforeHead *orders.Node
			if beforeQ != nil {
				beforeHead = beforeQ.Head()
			}
			nano := &countingNanoSink{}
			svc.Presentation = nano
			resources := svc.Economy.Players
			sim := *svc.OrderBinding.SimRNG
			x, z := u.X, u.Z
			occupancy := append([]world.PlotCell(nil), svc.Terrain.Plot...)
			svc.handleState2(factory, node, 20)
			if node.Target != 0 || node.Deadline != 35 {
				t.Fatalf("blocked allocation changed: %+v", node)
			}
			if svc.Economy.Players != resources || *svc.OrderBinding.SimRNG != sim || nano.count != 0 {
				t.Fatal("clearance changed resources or random streams")
			}
			if u.X != x || u.Z != z || !reflect.DeepEqual(occupancy, svc.Terrain.Plot) {
				t.Fatal("clearance moved/stamped unit directly")
			}
			q := orders.QueueOfUnit(u)
			if tc.issue {
				if q == nil || q.Head() == beforeHead || q.Head().ID != orders.Lookup("Move_Ground") || q.Head().CreationTick != 20 {
					t.Fatalf("no clearance move: %+v", q)
				}
			} else if q != beforeQ || (q != nil && q.Head() != beforeHead) {
				t.Fatal("ineligible unit queue changed")
			}
		})
	}
}

func TestModernFactoryYieldNoReachableDestination(t *testing.T) {
	svc, factory, u, node := yieldFixture(t)
	anchor, fx, fz, _ := svc.Movement.CommittedFootprint(u.Handle)
	// An enclosure leaves passable endpoints outside, but no legal local path.
	for z := anchor.Z - 1; z <= anchor.Z+int32(fz); z++ {
		for x := anchor.X - 1; x <= anchor.X+int32(fx); x++ {
			if x >= anchor.X && x < anchor.X+int32(fx) && z >= anchor.Z && z < anchor.Z+int32(fz) {
				continue
			}
			svc.Terrain.PlotAt(x, z).SetOccupantA(30000)
		}
	}
	for _, tick := range []uint32{1, 16, 31} {
		svc.handleState2(factory, node, tick)
	}
	if orders.QueueOfUnit(u) != nil || node.Target != 0 {
		t.Fatal("enclosed idle blocker acquired an impossible move")
	}
}

func TestModernFactoryYieldResumesProduction(t *testing.T) {
	for _, modern := range []bool{false, true} {
		name := "Strict"
		if modern {
			name = "Modern"
		}
		t.Run(name, func(t *testing.T) {
			svc, factory, u, node := yieldFixture(t)
			svc.ModernFactoryExit = modern
			svc.handleState2(factory, node, 1)
			if node.Target != 0 {
				t.Fatal("allocated over blocker")
			}
			pump := &orders.Pump{World: svc.World}
			ctx := TickContext{World: svc.World, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: svc.Catalog}
			completed := false
			for tick := uint32(2); tick < 900 && !completed; tick++ {
				advanceFactoryEgressTick(t, svc, svc.World, svc.Movement, pump, &ctx, tick, func(pool.Handle) { completed = true })
			}
			if completed != modern {
				t.Fatalf("completed=%v Modern=%v; blocker=(%v,%v), head=%+v factory=%+v", completed, modern, u.X, u.Z, orders.QueueOfUnit(u).Head(), node)
			}
		})
	}
}

func TestModernFactoryYieldRetryAndNeighborPreservation(t *testing.T) {
	svc, factory, u, node := yieldFixture(t)
	svc.handleState2(factory, node, 1)
	first := orders.QueueOfUnit(u).Head()
	if first == nil {
		t.Fatal("no clearance")
	}
	// A second factory with the same clearance region cannot take over an
	// already issued move, and repeated placement retries do not append moves.
	other := *factory
	other.Handle = 60
	for _, tick := range []uint32{2, 16, 31} {
		svc.yieldFactoryExit(&other, exitRect(14, 14, 4), nil, tick)
		svc.handleState2(factory, node, tick)
	}
	if q := orders.QueueOfUnit(u); q.Head() != first || q.LenPrimary() != 1 {
		t.Fatal("another factory replaced or duplicated clearance move")
	}
}

func TestModernFactoryYardCloseYieldsAtSuppliedTick(t *testing.T) {
	for _, modern := range []bool{false, true} {
		svc, factory, u, _ := yieldFixture(t)
		svc.ModernFactoryExit = modern
		if svc.YardOpenTransactionAt(factory, false, 71) || !factory.YardOpen {
			t.Fatal("closed over blocker")
		}
		q := orders.QueueOfUnit(u)
		if !modern {
			if q != nil {
				t.Fatal("Strict close issued movement")
			}
			continue
		}
		if q == nil || q.Head().CreationTick != 71 {
			t.Fatal("denied close did not request timely clearance")
		}
		svc.Movement.ActivateMove(u, q.Head())
		pump := &orders.Pump{World: svc.World}
		// Only the blocker moves: the production queue does not advance here.
		closed := false
		for tick := uint32(72); tick < 900; tick++ {
			svc.Movement.Scheduler.Tick(tick)
			svc.Movement.BeginTick(tick)
			pump.PumpUnit(u.Handle, tick)
			if head := orders.QueueOfUnit(u).Head(); head != nil {
				svc.Movement.ActivateMove(u, head)
			}
			svc.Movement.StepUnit(u.Handle, tick)
			svc.Movement.EndTick(tick)
			if svc.YardOpenTransactionAt(factory, false, tick) {
				closed = true
				break
			}
		}
		if !closed {
			t.Fatal("yard never closed after clearance")
		}
	}
}

func TestModernInstalledFactoryExitYield(t *testing.T) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := content.PreflightSkirmish(fs, cat, "Ashap Plateau", 0)
	if err != nil {
		t.Fatal(err)
	}
	factoryDef, _ := cat.Unit(manifest.KbotLab)
	product, _ := cat.Unit(manifest.LabProduct)
	svc, factory, u, node := yieldFixtureCatalog(t, cat, factoryDef, product)
	// The installed definitions retain authored footprints and movement. This
	// fixture supplies a deterministic center build piece and open stance.
	svc.handleState2(factory, node, 1)
	if node.Target != 0 || orders.QueueOfUnit(u) == nil {
		t.Fatalf("installed exit did not yield: node=%+v", node)
	}
	pump := &orders.Pump{World: svc.World}
	ctx := TickContext{World: svc.World, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: svc.Catalog}
	completed := false
	for tick := uint32(2); tick < 6000 && !completed; tick++ {
		advanceFactoryEgressTick(t, svc, svc.World, svc.Movement, pump, &ctx, tick, func(pool.Handle) { completed = true })
	}
	if !completed {
		t.Fatalf("installed factory did not resume: %s/%s, node=%+v, blocker=(%d,%d)", manifest.KbotLab, manifest.LabProduct, node, numeric.Fixed(u.X), numeric.Fixed(u.Z))
	}
}

func TestModernFactoryYieldDestinationsAvoidEachOtherAndNeighborYards(t *testing.T) {
	svc, factory, first, _ := yieldFixture(t)
	// A neighboring factory has an entirely open yard. It must remain free
	// even though its cells pass terrain and occupancy checks.
	def := newFactoryDef("neighbor", 4, 4, 300)
	def.YardMap = "yyyy yyyy yyyy yyyy"
	h, err := svc.World.Create(def, 0, world.CellToWorld(16), 0, world.CellToWorld(12))
	if err != nil {
		t.Fatal(err)
	}
	neighbor := svc.World.Unit(h)
	if err := svc.RegisterBuildingPlacement(neighbor); err != nil {
		t.Fatal(err)
	}
	neighborRect, _ := svc.PlacementForProduct(h)
	secondHandle, err := svc.World.Create(first.Def, 0, first.X+world.CellToWorld(1), 0, first.Z)
	if err != nil {
		t.Fatal(err)
	}
	second := svc.World.Unit(secondHandle)
	second.Flags = second.Flags&^StandingMoveMask | 1<<units.StandingMoveShift
	svc.Movement.EnsureUnit(second)
	clear, _ := svc.PlacementForProduct(factory.Handle)
	svc.yieldFactoryExit(factory, clear, nil, 8)
	var destinations []world.FootprintRect
	for _, u := range []*units.Unit{first, second} {
		q := orders.QueueOfUnit(u)
		if q == nil || q.Head().ID != orders.Lookup("Move_Ground") {
			t.Fatal("idle blocker did not receive clearance")
		}
		profile := svc.Movement.ProfileFor(u.Handle)
		extent, err := world.NewFootprintExtent(int32(profile.FootPrintX), int32(profile.FootPrintZ))
		if err != nil {
			t.Fatal(err)
		}
		anchor, err := world.SnapFootprintAnchor(q.Head().GoalX, q.Head().GoalZ, extent)
		if err != nil {
			t.Fatal(err)
		}
		rect, err := world.NewFootprintRect(anchor, extent)
		if err != nil {
			t.Fatal(err)
		}
		if yieldOverlap(rect, clear) || yieldOverlap(rect, neighborRect) {
			t.Fatal("clearance endpoint overlaps factory yard")
		}
		if u.X != q.Head().GoalX || u.Z != q.Head().GoalZ {
			// The position stays at its source until the ordinary movement phase.
			destinations = append(destinations, rect)
		} else {
			t.Fatal("clearance destination equals occupied source")
		}
	}
	if yieldOverlap(destinations[0], destinations[1]) {
		t.Fatal("clearance destinations overlap")
	}
}
