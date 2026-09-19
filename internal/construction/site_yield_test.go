package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func siteYieldFixture(t *testing.T) (*Service, *units.Unit, *units.Unit, *orders.Node) {
	t.Helper()
	s, b, n := approachFixtureAt(t, 10, 10, world.CellToWorld(5), world.CellToWorld(10))
	s.Rules = &ModernRules{}
	s.Economy = &economy.Service{}
	bindConstructionCombat(s)
	sim := rng.NewSimulation(17)
	s.OrderBinding = &orders.QueueBinding{Lookup: s.World.Unit, SimRNG: &sim, Movement: &orders.MovementGoalAdapter{InstallPoint: s.Movement.InstallPointGoal, Release: s.Movement.ReleaseGoalPayload}}
	s.queueForUnit(b).SetBinding(s.OrderBinding)
	n.Phase, n.DynamicGate, n.Deadline = uint8(State2), 0, -1
	d := *b.Def
	d.Builder = false
	d.StandingMoveOrder = 1
	d.TurnRate = 2000
	d.MaxVelocity, d.Acceleration, d.BrakeRate = 2*65536, 65536/2, 65536/2
	h, err := s.World.Create(&d, b.Owner, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatal(err)
	}
	blocker := s.World.Unit(h)
	s.Movement.EnsureUnit(blocker)
	return s, b, blocker, n
}

// Modern policy: start on the first blocked visit and get the physical footprint
// clear before the existing eleven waits elapse, even with NO scheduler service.
func TestModernSiteClearanceBeatsBuildTimeoutWithoutScheduler(t *testing.T) {
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "Strict", true: "Modern"}[modern], func(t *testing.T) {
			s, b, u, n := siteYieldFixture(t)
			s.Rules = clearanceRules(modern)
			startX, startZ := u.X, u.Z
			draws := s.OrderBinding.SimRNG.Draws()
			s.mobilePlacementVisit(b, n, 1)
			if n.Target != 0 || n.Param3 != 1 || n.Deadline != 31 {
				t.Fatalf("first refusal changed: %+v", n)
			}
			if u.X != startX || u.Z != startZ || s.OrderBinding.SimRNG.Draws() != draws {
				t.Fatal("request moved unit or drew RNG")
			}
			q := orders.QueueOfUnit(u)
			if (q != nil) != modern {
				t.Fatalf("clearance queued=%v modern=%v", q != nil, modern)
			}
			if modern && (q.Head().ID != orders.Lookup("Move_Ground") || q.Head().CreationTick != 1) {
				t.Fatal("move not issued on first blocked visit")
			}
			firstMove, allocated, gaveUp := uint32(0), uint32(0), uint32(0)
			for tick := uint32(2); tick <= 331; tick++ {
				s.Movement.BeginTick(tick)
				if q != nil {
					q.Pump(u, tick)
					if head := q.Head(); head != nil {
						s.Movement.ActivateMove(u, head)
					}
				}
				s.Movement.StepUnit(u.Handle, tick)
				s.Movement.EndTick(tick)
				if firstMove == 0 && (u.X != startX || u.Z != startZ) {
					firstMove = tick
				}
				code := s.mobilePlacementVisit(b, n, tick)
				if n.Target != 0 {
					allocated = tick
					break
				}
				if code == 8 {
					gaveUp = tick
					break
				}
			}
			if modern {
				if firstMove == 0 || firstMove > 3 || allocated == 0 || allocated >= 331 {
					t.Fatalf("missed clearance window: first move=%d allocation=%d give-up=%d unit=%v,%v head=%+v", firstMove, allocated, gaveUp, u.X, u.Z, q.Head())
				}
				t.Logf("first movement tick %d, nanoframe allocation tick %d", firstMove, allocated)
			} else if gaveUp != 331 || firstMove != 0 || allocated != 0 {
				t.Fatalf("Strict timeout changed: move=%d allocation=%d give-up=%d", firstMove, allocated, gaveUp)
			}
		})
	}
}

func TestModernSiteClearancePreservesOrdersAndTerminalTimeout(t *testing.T) {
	for _, kind := range []string{"move", "hold", "enemy", "terminal", "self"} {
		t.Run(kind, func(t *testing.T) {
			s, b, u, n := siteYieldFixture(t)
			switch kind {
			case "move":
				s.queueForUnit(u).Push(orders.Lookup("Move_Ground"), orders.NewMoveNode(orders.Lookup("Move_Ground"), world.CellToWorld(20), world.CellToWorld(20), 0, u.Handle, false))
			case "hold":
				u.Flags &^= StandingMoveMask
			case "enemy":
				u.Owner = 1
			case "terminal":
				n.Param3 = 11
			case "self":
				b = u
			}
			q := orders.QueueOfUnit(u)
			var head *orders.Node
			if q != nil {
				head = q.Head()
			}
			if kind == "self" {
				x, z, fx, fz, _ := s.siteAnchorCell(n)
				extent, _ := world.NewFootprintExtent(fx, fz)
				rect, _ := world.NewFootprintRect(world.NewFootprintAnchor(x, z), extent)
				s.yieldConstructionSite(b, rect, 1)
			} else {
				s.mobilePlacementVisit(b, n, 1)
			}
			after := orders.QueueOfUnit(u)
			if q != after || (after != nil && after.Head() != head) {
				t.Fatal("clearance took over an ineligible blocker")
			}
		})
	}
}

// An impossible escape does not renew or stretch the Modern construction budget.
func TestModernEnclosedSiteStillGivesUpOnTime(t *testing.T) {
	s, b, u, n := siteYieldFixture(t)
	a, fx, fz, _ := s.Movement.CommittedFootprint(u.Handle)
	for z := a.Z - 1; z <= a.Z+int32(fz); z++ {
		for x := a.X - 1; x <= a.X+int32(fx); x++ {
			if x < a.X || x >= a.X+int32(fx) || z < a.Z || z >= a.Z+int32(fz) {
				s.Terrain.PlotAt(x, z).SetOccupantA(30000)
			}
		}
	}
	for tick := uint32(1); tick <= 331; tick++ {
		code := s.mobilePlacementVisit(b, n, tick)
		if (code == 8) != (tick == 331) {
			t.Fatalf("give-up code %d at tick %d", code, tick)
		}
	}
	if n.Target != 0 || orders.QueueOfUnit(u) != nil {
		t.Fatal("enclosed site allocated or acquired impossible clearance")
	}
}

// Installed tank movement and solar footprint must also fit the existing window.
func TestModernInstalledSiteClearance(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	s, b, old, n := siteYieldFixture(t)
	s.Movement.ForgetUnit(old.Handle)
	s.World.FreeImmediate(old.Handle)
	product, _ := cat.Unit("armsolar")
	tank, _ := cat.Unit("armstump")
	s.Catalog.Units[product.CanonicalKey] = product
	s.Catalog.Movement[tank.MovementClass] = cat.Movement[tank.MovementClass]
	s.Movement.SetClasses(s.Catalog.Movement)
	n.BuildDefKey = product.CanonicalKey
	h, err := s.World.Create(tank, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatal(err)
	}
	u := s.World.Unit(h)
	s.Movement.EnsureUnit(u)
	s.mobilePlacementVisit(b, n, 1)
	q := orders.QueueOfUnit(u)
	if q == nil {
		t.Fatal("installed tank received no clearance")
	}
	for tick := uint32(2); tick < 331; tick++ {
		s.Movement.BeginTick(tick)
		q.Pump(u, tick)
		if head := q.Head(); head != nil {
			s.Movement.ActivateMove(u, head)
		}
		s.Movement.StepUnit(h, tick)
		s.Movement.EndTick(tick)
		s.mobilePlacementVisit(b, n, tick)
		if n.Target != 0 {
			t.Logf("installed %s cleared %s for allocation at tick %d", tank.CanonicalKey, product.CanonicalKey, tick)
			return
		}
	}
	t.Fatalf("installed clearance missed timeout: unit=%v,%v head=%+v", u.X, u.Z, q.Head())
}
