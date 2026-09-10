package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// factoryWithAttachedProduct builds the state the two interrupt producers act
// on: a factory whose head record is in the work loop with a live product bound
// as its target reference and the dynamic gate the work loop arms
// (`WakeBit1|WakeBit3`, which is what puts bit 1 in the gate).
func factoryWithAttachedProduct(t *testing.T) (svc *Service, factory, product *units.Unit, node *orders.Node) {
	t.Helper()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 2, 2, 300)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	cat.Units[content.CanonicalKey("armfac")] = facDef
	cat.Units[content.CanonicalKey("armflash")] = prodDef

	w := newTestWorld(10)
	fh, err := w.Create(facDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory = w.Unit(fh)
	factory.Def = facDef
	ph, err := w.Create(prodDef, 0, 4<<16, 0, 4<<16)
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	product = w.Unit(ph)
	product.Def = prodDef
	product.Remaining = 0.5

	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{
		BuildDefKey: "armflash",
		Param1:      prodIdx(cat, "armflash"),
		Param2:      3,
		Phase:       uint8(State3),
	})
	node = q.Primary()[0]
	node.Phase = uint8(State3)
	node.Param2 = 3
	node.Target = product.Handle
	node.DynamicGate = WakeBit1 | WakeBit3

	svc = NewService(nil, cat, w, &economy.Service{})
	bindConstructionCombat(svc)
	svc.SetBuilderLink(product.Handle, factory.Handle)
	return svc, factory, product, node
}

// TestTargetRemovedNoticeReachesTheFactoryInterrupt is mask 8's producer: the
// factory record binds its product as its target reference at creation, and the
// unit-removal walk delivers 0x8 through that binding and unlinks the reference
// when the product under construction is destroyed [04 R-ORD-01 §6]
// [05 "Build request and factory queue behavior"]. The next pump visit then
// runs the established construction-stopped body [05 C22].
func TestTargetRemovedNoticeReachesTheFactoryInterrupt(t *testing.T) {
	svc, factory, product, node := factoryWithAttachedProduct(t)

	if !svc.NotifyProductRemoved(product.Handle) {
		t.Fatal("the product's removal delivered no notice to the factory that bound it")
	}
	if node.Satisfied&InterruptStop == 0 || factory.Pending != 0 {
		t.Fatalf("record/unit pending = %#x/%#x, want record bit 3 only [04 R-ORD-01 §6]", node.Satisfied, factory.Pending)
	}
	if node.Target != 0 {
		t.Fatalf("target reference = %d, want it unlinked after the notice [04 R-ORD-01 §6]", node.Target)
	}

	// The interrupt is tested before the state machine, so the work loop's
	// lost-product cancel-all never runs: the count decrements once and the
	// record survives at state 0 [05 C22].
	svc.RegisterOrderHandlers(orders.QueueForUnit(factory))
	orders.QueueForUnit(factory).Pump(factory, 100)
	if node.Satisfied&InterruptStop != 0 {
		t.Fatal("the pump did not consume the interrupt bit")
	}
	stopped := false
	for _, m := range svc.Messages() {
		if m == "Construction stopped" {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("messages = %v, want the verbatim \"Construction stopped\" [05 C22]", svc.Messages())
	}
	q := orders.QueueForUnit(factory)
	if q.LenPrimary() != 1 {
		t.Fatalf("primary length = %d, want the record to survive the stop [05 C22]", q.LenPrimary())
	}
	if node.Param2 != 2 {
		t.Fatalf("count = %d, want one decrement 3->2 [05 C22]", node.Param2)
	}
	if node.Phase != uint8(State0) {
		t.Fatalf("phase = %d, want a restart at state 0 [05 C22]", node.Phase)
	}
}

// TestNoTargetRemovedNoticeWithoutABoundReference locks the reference lifetime:
// completion, cancel-current and the never-existed unwind all release the
// binding, so a later death of that handle wakes nobody.
func TestNoTargetRemovedNoticeWithoutABoundReference(t *testing.T) {
	svc, factory, product, _ := factoryWithAttachedProduct(t)
	svc.ClearBuilderLink(product.Handle)
	if svc.NotifyProductRemoved(product.Handle) {
		t.Fatal("a released reference still delivered the target-removed notice")
	}
	if factory.Pending&InterruptStop != 0 {
		t.Fatalf("pending word = %#x, want no interrupt raised", factory.Pending)
	}
}

// TestCancelNoticeOnRemovalWithDynamicGateBitOne is mask 2's producer: a record
// removed while its DYNAMIC gate still holds bit 1 (value 2) delivers the
// cancel-current notification through its own operation handler
// [04 R-ORDER-02 §2]. For a factory record that handler is the production
// machine, so the body that runs is cancel-current's — the refund and the
// cause-9 kill [05 C21].
func TestCancelNoticeOnRemovalWithDynamicGateBitOne(t *testing.T) {
	svc, factory, product, node := factoryWithAttachedProduct(t)

	if node.DynamicGate&InterruptCancel == 0 {
		t.Fatalf("the work loop's gate %#x does not hold bit 1; the guard cannot fire", node.DynamicGate)
	}
	if !svc.DeliverCancelNotice(factory, node, 100) {
		t.Fatal("the cancel notification was refused for a record whose gate holds bit 1")
	}

	// Cancel-current's established body: the refund is trunc((1 - remaining) *
	// metalBuildCost) = trunc(0.5 * 100), and the product takes the cause-9
	// kill packet [05 C21].
	if got := svc.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production; got != 50 {
		t.Fatalf("refund = %v, want trunc((1-0.5)*100) = 50 [05 C21]", got)
	}
	if k := svc.LastKill(); k.Damage != Kind9Damage || !k.NoCorpse {
		t.Fatalf("kill packet = %+v, want the 30000 no-corpse cause-9 packet [05 C21]", k)
	}
	if !product.Dying {
		t.Fatal("the product survived cancel-current")
	}

	// The receiver releases the record's gate and its product reference before
	// the removal, so the removal cannot re-enter this same body.
	if node.DynamicGate&InterruptCancel != 0 {
		t.Fatalf("gate = %#x, want bit 1 released before the removal", node.DynamicGate)
	}
	if node.Target != 0 {
		t.Fatalf("target reference = %d, want it released at cancel", node.Target)
	}
	if svc.DeliverCancelNotice(factory, node, 100) {
		t.Fatal("the notice fired a second time on a record no longer waiting on bit 1")
	}
}

// TestCancelNoticeRefusedWithoutDynamicGateBitOne is the guard's other half: the
// notification is delivered only when the record is waiting on bit 1 at removal
// [04 R-ORDER-02 §2]. A factory record between states (gate 0) is not.
func TestCancelNoticeRefusedWithoutDynamicGateBitOne(t *testing.T) {
	svc, factory, _, node := factoryWithAttachedProduct(t)
	node.DynamicGate = 0
	if svc.DeliverCancelNotice(factory, node, 100) {
		t.Fatal("a record with no gate bit 1 accepted the cancel notification")
	}
	if got := svc.Economy.UnitBuckets(factory.Handle)[economy.Metal].Production; got != 0 {
		t.Fatalf("refund = %v, want none: the guard should have refused", got)
	}
}

// TestBuildingBuildStaticMaskCarriesBitThreeNotBitOne is the shape the two
// producers depend on: mask 8 arrives through the STATIC mask (bit 3), which is
// why the notice fires once through the pump, while bit 1 is absent statically
// and armed dynamically by the state machine while a product is attached
// [05 "Build request and factory queue behavior"].
func TestBuildingBuildStaticMaskCarriesBitThreeNotBitOne(t *testing.T) {
	mask := orders.DescriptorFor(orders.Lookup("BuildingBuild")).StaticGate
	if mask&InterruptStop == 0 {
		t.Fatalf("BuildingBuild static mask %#x lacks bit 3", mask)
	}
	if mask&InterruptCancel != 0 {
		t.Fatalf("BuildingBuild static mask %#x carries bit 1; the contract says it does not", mask)
	}
	_, _, _, node := factoryWithAttachedProduct(t)
	if node.DynamicGate&InterruptCancel == 0 {
		t.Fatalf("the work loop's dynamic gate %#x does not arm bit 1", node.DynamicGate)
	}
}
