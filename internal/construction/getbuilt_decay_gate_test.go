package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The shared work step ORs `0x8000` into the target's pending word before the
// zero-quantum test and before admission; `GetBuilt` keeps its gate at `0x8001`;
// and the phase-2 body's `0x8000` arm re-arms 30 ticks and does NOT decay. The
// decay is reached only when a whole deadline passes with no forward step on
// the product [04 R-ORD-01 §11].
func TestGetBuiltWakeArmHoldsWithoutDecaying(t *testing.T) {
	def := newProductDef("armflash", 1, 1, 100, 100)
	def.BuildCostEnergy = 60
	def.WorkerTime = 60 // integer quantum 2 [05 "Construction arithmetic"]
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w := newConstructionFixtureWorld(4, cat)
	h, _ := w.Create(def, 0, 0, 0, 0)
	product := w.Unit(h)
	product.Remaining = 0.5
	product.MaxHealth = 100
	q := orders.QueueForUnit(product)
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State2)})
	node := q.Primary()[0]
	svc := NewService(nil, cat, w, &economy.Service{})

	// A forward step raises the wake in the unit's pending word — the bit the
	// pump ORs into the satisfied set — and mirrors it onto the record.
	svc.applyWorkStep(product, product, 40)
	if product.Pending&pendingUnderConstructionWake == 0 {
		t.Fatalf("forward step did not raise 0x8000 in the unit pending word")
	}
	if node.Param1 == 0 {
		t.Fatalf("forward step did not reach the product's GetBuilt record")
	}

	before := product.Remaining
	if code := svc.handleGetBuiltOrder(product, node, 45); code != 2 {
		t.Fatalf("worked visit code=%d, want hold", code)
	}
	if product.Remaining != before {
		t.Fatalf("worked visit decayed the frame to %v, want %v untouched", product.Remaining, before)
	}
	if node.Deadline != int32(45+getBuiltWorkedPeriod) {
		t.Fatalf("worked visit deadline=%d, want %d", node.Deadline, 45+getBuiltWorkedPeriod)
	}
	if node.DynamicGate != 0x8001 {
		t.Fatalf("worked visit gate=%#x, want 0x8001", node.DynamicGate)
	}
	if node.Param1 != 0 {
		t.Fatalf("the dispatch did not consume the wake")
	}

	// A whole deadline with no forward step reaches the decay: the quantum is
	// -(buildtime*11)/buildcostenergy, so the fraction rises by 11/energy.
	if code := svc.handleGetBuiltOrder(product, node, 75); code != 2 {
		t.Fatalf("expired visit code=%d, want hold", code)
	}
	want := before + float32(11)/float32(60)
	if diff := product.Remaining - want; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("expired visit remaining=%v, want %v", product.Remaining, want)
	}
	if node.Deadline != int32(75+getBuiltDecayPeriod) {
		t.Fatalf("expired visit deadline=%d, want %d", node.Deadline, 75+getBuiltDecayPeriod)
	}
}

// The wake store precedes both the zero-quantum test and the admission, so a
// `workertime` below thirty defers the decay just as an admitted step does
// [04 R-ORD-01 §11].
func TestUnderConstructionWakeRisesOnAZeroQuantum(t *testing.T) {
	def := newProductDef("armflash", 1, 1, 100, 100)
	def.WorkerTime = 10 // below 30: the integer quantum truncates to zero
	product := &units.Unit{Handle: 1, Owner: 0, Alive: true, Def: def, Remaining: 0.5, MaxHealth: 100}
	svc := NewService(nil, nil, nil, nil)
	if svc.applyWorkStep(product, product, 1) {
		t.Fatalf("a zero quantum must not commit work")
	}
	if product.Pending&pendingUnderConstructionWake == 0 {
		t.Fatalf("a zero quantum still raises the wake bit")
	}
}

// An unattended nanoframe still decays: the arm keys on a forward step, not on
// the record's existence [04 R-ORD-01 §11].
func TestGetBuiltDecayStillRunsWithoutWork(t *testing.T) {
	def := newProductDef("armflash", 1, 1, 100, 100)
	def.BuildCostEnergy = 60
	product := &units.Unit{Handle: 1, Owner: 0, Alive: true, Def: def, Remaining: 0.5, MaxHealth: 100}
	node := &orders.Node{Phase: uint8(State2)}
	svc := NewService(nil, nil, nil, &economy.Service{})
	if code := svc.handleGetBuiltOrder(product, node, 40); code != 2 {
		t.Fatalf("code=%d, want hold", code)
	}
	if product.Remaining <= 0.5 {
		t.Fatalf("unattended frame remaining=%v, want a decay above 0.5", product.Remaining)
	}
}

// Zero `buildcostenergy` makes the decay quantum -∞: the reverse arm clamps to
// 1.0, refunds every unit of metal already sunk, floors health at zero and
// kills the frame on its FIRST decay visit. Both fields zero makes it a NaN:
// both entry compares are unordered, so the step writes nothing and raises no
// wake bit [05 R-WORK-01 §11].
func TestMalformedBuildNumbersThroughTheDecayWrapper(t *testing.T) {
	t.Run("zero energy cost kills on the first decay visit", func(t *testing.T) {
		def := newProductDef("armzeroe", 1, 1, 100, 100)
		def.BuildCostEnergy = 0
		def.BuildCostMetal = 200
		cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
		w := newConstructionFixtureWorld(4, cat)
		h, _ := w.Create(def, 0, 0, 0, 0)
		product := w.Unit(h)
		product.Remaining, product.MaxHealth, product.Health = 0.25, 100, 75
		svc := NewService(nil, cat, w, &economy.Service{})
		node := &orders.Node{Phase: uint8(State2)}

		svc.handleGetBuiltOrder(product, node, 10)

		if product.Remaining != 1 {
			t.Fatalf("remaining=%v, want the clamp to 1.0", product.Remaining)
		}
		if product.Health != 0 {
			t.Fatalf("health=%d, want the floor at zero", product.Health)
		}
		if product.Alive || !product.Dying {
			t.Fatalf("a zero-energy-cost frame is removed on its first decay visit")
		}
		buckets := svc.Economy.UnitBuckets(product.Handle)
		if want := float32(200) * 0.75; buckets[economy.Metal].Production != want {
			t.Fatalf("refund=%v, want the full %v of metal already sunk", buckets[economy.Metal].Production, want)
		}
	})

	t.Run("both zero writes nothing", func(t *testing.T) {
		def := newProductDef("armzeroboth", 1, 1, 100, 0)
		def.BuildCostEnergy = 0
		product := &units.Unit{Handle: 1, Owner: 0, Alive: true, Def: def, Remaining: 0.25, MaxHealth: 100, Health: 75}
		svc := NewService(nil, nil, nil, &economy.Service{})
		node := &orders.Node{Phase: uint8(State2)}

		svc.handleGetBuiltOrder(product, node, 10)

		if product.Remaining != 0.25 || product.Health != 75 || !product.Alive {
			t.Fatalf("NaN quantum wrote state: remaining=%v health=%d alive=%v", product.Remaining, product.Health, product.Alive)
		}
		if product.Pending&pendingUnderConstructionWake != 0 {
			t.Fatalf("an unordered quantum reads as negative and must not raise the wake bit")
		}
	})
}
