package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// ownerFixture stands a factory able to build `count` mobile products up on a
// flat plot with an open yard, and returns the service, its world, the
// movement system bound to both, and the factory.
func ownerFixture(t *testing.T, count uint32) (*Service, *units.World, *movement.System, *units.Unit) {
	t.Helper()
	lab := newFactoryDef("ownlab", 4, 4, 300)
	lab.YardMap = "yyyy yyyy yyyy yyyy"
	prod := exitMobileDef("ownprod", 1, 1)
	prod.BuildTime = 1
	prod.BuildCostEnergy = 1
	prod.BuildCostMetal = 1
	cat := exitCatalog(lab, prod)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)
	sys := movement.NewSystem(terrain, movement.Profile{}, movement.NewOccupancyGrid())
	sys.SetClasses(cat.Movement)
	sys.BindWorld(w)
	svc.Movement = sys
	for i := range svc.Economy.Players {
		svc.Economy.Players[i].Stock[0] = 1e9
		svc.Economy.Players[i].Stock[1] = 1e9
		svc.Economy.Players[i].Capacity[0] = 2e9
		svc.Economy.Players[i].Capacity[1] = 2e9
	}

	fh, err := w.Create(lab, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(fh)
	bindConstructionFixture(factory, trivialModel(1, [][3]int64{{0, 0, 0}}), true)
	if err := svc.RegisterBuildingPlacement(factory); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if !svc.YardOpenTransaction(factory, true) {
		t.Fatal("factory yard refused to open")
	}
	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{
		Owner: factory.Handle, BuildDefKey: prod.CanonicalKey,
		Param1: prodIdx(cat, prod.CanonicalKey), Param2: count,
		Phase: uint8(State2), Deadline: -1,
	})
	q.Primary()[0].Phase = uint8(State2)
	return svc, w, sys, factory
}

// findOwnedRecord returns the product's queued record for the named descriptor.
func findOwnedRecord(u *units.Unit, name string) *orders.Node {
	q := orders.QueueForUnit(u)
	if q == nil {
		return nil
	}
	for _, seg := range [][]*orders.Node{q.Primary(), q.Secondary()} {
		for _, n := range seg {
			if n != nil && orders.DescriptorFor(n.ID).Name == name {
				return n
			}
		}
	}
	return nil
}

// TestFactoryProductRecordsCarryTheProductAsOwner locks the field [04 §3.2]
// lists as part of every order record and [04 R-ORD-01 §1] hands to every
// handler body alongside it: the owning unit.
//
// [04 R-FAC-02 §4] settles who owns each of the records this package pushes:
// "the product's first order is `BeCarried`, its second is `GetBuilt`", and
// `GetBuilt`'s own completion arm resolves the builder's `QMove`/`QPatrol`
// records "against the product ... and inserted queued on the product", with
// `Park` inserted when nothing was. All five are the PRODUCT's records. The
// factory is their target or their source, never their owner.
//
// Before WU-19-71 all five were pushed as bare `orders.Node{}` literals, so
// every product in the battle named the single movement-controller slot at the
// null handle [04 R-ORD-01 §9], `Queue.ownerUnit` could not resolve the
// record's unit, and `InstallAirGoal` — which refuses an install whose
// resolved unit does not carry `canfly` — rejected every aircraft product's
// inherited rally goal outright.
func TestFactoryProductRecordsCarryTheProductAsOwner(t *testing.T) {
	svc, w, _, factory := ownerFixture(t, 1)
	cat := svc.Catalog

	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: cat}
	var ph pool.Handle
	var pushTick uint32
	for i := 1; i <= 60 && ph == 0; i++ {
		ctx.Tick = uint32(i)
		svc.Economy.TickPlayer(0, uint32(i), w, func() {})
		res := svc.StepUnit(ctx, factory.Handle)
		if res.Completed && res.Product != 0 {
			ph = res.Product
			pushTick = uint32(i)
		}
	}
	if ph == 0 {
		t.Fatalf("factory never produced; messages=%v", svc.Messages())
	}
	product := w.Unit(ph)

	// The product's queue has not been pumped, so the two records the
	// allocation visit pushed are still on it [04 R-FAC-02 §4].
	for _, name := range []string{"BeCarried", "GetBuilt"} {
		n := findOwnedRecord(product, name)
		if n == nil {
			t.Fatalf("product queue has no %s record; queue=%v", name, queueNames(orders.QueueForUnit(product).Primary()))
		}
		if n.Owner != ph {
			t.Fatalf("%s owner = %v, want the PRODUCT %v [04 §3.2][04 R-FAC-02 §4]", name, n.Owner, ph)
		}
		if n.CreationTick == 0 || n.CreationTick > pushTick {
			t.Fatalf("%s creation tick = %d, want the pushing visit's tick in 1..%d [04 §3.2]", name, n.CreationTick, pushTick)
		}
	}
	if n := findOwnedRecord(product, "BeCarried"); n.Target != factory.Handle {
		t.Fatalf("BeCarried target = %v, want the carrier %v — the factory is the TARGET, not the owner [04 R-FAC-02 §1]", n.Target, factory.Handle)
	}

	// No rally on the builder: `GetBuilt`'s completion arm inserts `Park`, and
	// that record is the product's too.
	svc.rallyInheritance(factory, product, pushTick)
	park := findOwnedRecord(product, "Park")
	if park == nil {
		t.Fatalf("rally inheritance queued no Park; queue=%v", queueNames(orders.QueueForUnit(product).Primary()))
	}
	if park.Owner != ph {
		t.Fatalf("Park owner = %v, want the product %v [04 R-FAC-02 §4]", park.Owner, ph)
	}
	if park.CreationTick != pushTick {
		t.Fatalf("Park creation tick = %d, want the GetBuilt visit's %d [04 §3.2]", park.CreationTick, pushTick)
	}
}

// TestInheritedRallyRecordsCarryTheProductAsOwner is the rally half: the two
// descriptors `GetBuilt` resolves off the builder's queue — `QMove` as
// `Move_Ground` and `QPatrol` as `Patrol` [04 R-FAC-02 §4] — are inserted ON
// THE PRODUCT, so they carry the product's handle and not the factory's.
func TestInheritedRallyRecordsCarryTheProductAsOwner(t *testing.T) {
	qMoveID, qPatrolID := orders.Lookup("QMove"), orders.Lookup("QPatrol")
	if qMoveID == 0 || qPatrolID == 0 {
		t.Skip("descriptor table incomplete")
	}
	svc, w, _, factory := ownerFixture(t, 1)

	fq := orders.QueueForUnit(factory)
	fq.Push(qMoveID, orders.NewNodeForOrder(qMoveID, 0, world.CellToWorld(14), 0, world.CellToWorld(14), 1, factory.Handle, true))
	fq.Push(qPatrolID, orders.NewNodeForOrder(qPatrolID, 0, world.CellToWorld(16), 0, world.CellToWorld(16), 1, factory.Handle, true))

	prodDef := svc.Catalog.Units[content.CanonicalKey("ownprod")]
	ph, err := w.Create(prodDef, 0, world.CellToWorld(6), 0, world.CellToWorld(6))
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	product := w.Unit(ph)

	const visitTick = 77
	svc.rallyInheritance(factory, product, visitTick)

	for _, name := range []string{"Move_Ground", "Patrol"} {
		n := findOwnedRecord(product, name)
		if n == nil {
			t.Fatalf("no inherited %s on the product; queue=%v", name, queueNames(orders.QueueForUnit(product).Primary()))
		}
		if n.Owner != ph {
			t.Fatalf("inherited %s owner = %v, want the product %v, not the factory %v [04 R-FAC-02 §4]", name, n.Owner, ph, factory.Handle)
		}
		if n.CreationTick != visitTick {
			t.Fatalf("inherited %s creation tick = %d, want the GetBuilt visit's %d [04 §3.2]", name, n.CreationTick, visitTick)
		}
	}
}

// TestTwoProductsOfOneFactoryHaveIndependentGoalBuckets is the behavioural
// consequence. The movement controller's single goal slot is addressed BY the
// record's owner [04 R-ORD-01 §9], so two products of one factory must reach
// two slots: installing for the second product's inherited rally must leave the
// first product's binding intact, and each product's own detach must be the one
// that raises `0x80` on its own record.
//
// With the owner left null both records named the map entry at handle 0: the
// second product's install evicted the first product's payload, and the first
// product's detach then found nothing of its own to release.
func TestTwoProductsOfOneFactoryHaveIndependentGoalBuckets(t *testing.T) {
	qMoveID := orders.Lookup("QMove")
	if qMoveID == 0 {
		t.Skip("descriptor table incomplete")
	}
	svc, w, sys, factory := ownerFixture(t, 2)

	fq := orders.QueueForUnit(factory)
	fq.Push(qMoveID, orders.NewNodeForOrder(qMoveID, 0, world.CellToWorld(14), 0, world.CellToWorld(14), 1, factory.Handle, true))

	ctx := TickContext{World: w, Economy: svc.Economy, Terrain: svc.Terrain, Catalog: svc.Catalog}
	var made []pool.Handle
	for i := 1; i <= 200 && len(made) < 2; i++ {
		ctx.Tick = uint32(i)
		svc.Economy.TickPlayer(0, uint32(i), w, func() {})
		res := svc.StepUnit(ctx, factory.Handle)
		if res.Completed && res.Product != 0 {
			made = append(made, res.Product)
			svc.rallyInheritance(factory, w.Unit(res.Product), uint32(i))
		}
	}
	if len(made) != 2 {
		t.Fatalf("factory produced %d units, want 2; messages=%v", len(made), svc.Messages())
	}
	if made[0] == made[1] {
		t.Fatalf("both products report handle %v", made[0])
	}

	nA := findOwnedRecord(w.Unit(made[0]), "Move_Ground")
	nB := findOwnedRecord(w.Unit(made[1]), "Move_Ground")
	if nA == nil || nB == nil {
		t.Fatalf("inherited rally missing: A=%v B=%v", nA, nB)
	}
	if nA.Owner != made[0] || nB.Owner != made[1] {
		t.Fatalf("rally owners = (%v,%v), want the two products (%v,%v)", nA.Owner, nB.Owner, made[0], made[1])
	}
	if nA.Owner == nB.Owner {
		t.Fatalf("both products' rally records name owner %v; two units must not share a controller slot [04 R-ORD-01 §9]", nA.Owner)
	}

	for _, h := range made {
		sys.EnsureUnit(w.Unit(h))
	}
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: nA.Owner, Node: nA, X: world.CellToWorld(14), Z: world.CellToWorld(14)}) {
		t.Fatal("install for the first product's record was refused")
	}
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: nB.Owner, Node: nB, X: world.CellToWorld(16), Z: world.CellToWorld(16)}) {
		t.Fatal("install for the second product's record was refused")
	}
	if nA.Satisfied&0x80 != 0 {
		t.Fatalf("first product's pending word = %#x after the OTHER product installed; the rebind bit must not cross units [04 R-ORD-01 §9]", nA.Satisfied)
	}
	if !sys.ReleaseGoal(nA) {
		t.Fatal("release of the first product's record was refused")
	}
	if nA.Satisfied&0x80 == 0 {
		t.Fatalf("first product's pending word = %#x after its own detach, want `0x80`: its payload was evicted by the other product's install", nA.Satisfied)
	}
	if nB.Satisfied&0x80 != 0 {
		t.Fatalf("second product's pending word = %#x after the first product detached; the two buckets are not independent", nB.Satisfied)
	}
}
