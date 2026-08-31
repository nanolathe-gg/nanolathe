package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The nanoframe decay of [04 R-ORD-01 §5] is held off for one decay period by
// every admitted construction step. This is the effect side of the section's
// unfound `0x8000` producer; the arithmetic that forces it is written out on
// deferGetBuiltDecay. Without the gate a construction kbot's work step loses
// to its own site's decay and the frame runs backwards forever.
func TestGetBuiltDecayIsDeferredByAdmittedWork(t *testing.T) {
	def := newProductDef("armflash", 1, 1, 100, 100)
	def.BuildCostEnergy = 60
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

	// A work step at tick 40 pushes the next decay visit to tick 51.
	deferGetBuiltDecay(product, 40)
	if node.Param1 != 40+getBuiltDecayPeriod {
		t.Fatalf("deferred to %d, want %d", node.Param1, 40+getBuiltDecayPeriod)
	}
	if code := svc.handleGetBuiltOrder(product, node, 45); code != 2 {
		t.Fatalf("deferred visit code=%d, want hold", code)
	}
	if product.Remaining != 0.5 {
		t.Fatalf("deferred visit decayed the frame to %v, want 0.5 untouched", product.Remaining)
	}
	if node.Deadline != 51 {
		t.Fatalf("deferred visit deadline=%d, want the deferred tick 51", node.Deadline)
	}

	// With no further work the period expires and the established negative arm
	// runs unchanged: -(11*100/60) truncates to -18, i.e. +18/100 remaining.
	if code := svc.handleGetBuiltOrder(product, node, 51); code != 2 {
		t.Fatalf("expired visit code=%d, want hold", code)
	}
	want := float32(0.5) + float32(18)/float32(100)
	if product.Remaining != want {
		t.Fatalf("expired visit remaining=%v, want %v", product.Remaining, want)
	}
}

// An unattended nanoframe still decays: the gate keys on admitted work, not on
// the record's existence [04 R-ORD-01 §5].
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
