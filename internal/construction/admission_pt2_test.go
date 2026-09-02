package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// A denied construction pass still records both demands. The admission helper
// adds to the requested accumulators before it tests the carries, so the HUD
// sees the demand of a pass the gate refuses — which is exactly the pass a
// player in deficit is looking at [05 R-ECO-01 §7].
func TestDeniedBuildStepStillRecordsBothRequests(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armfac", 1, 1, 60)
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(12, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	product.Remaining, product.MaxHealth = 0.5, 100

	econ := &economy.Service{}
	buckets := econ.UnitBuckets(factory.Handle)
	if buckets == nil {
		t.Fatal("economy buckets unavailable")
	}
	// Positive energy carry alone denies the whole transaction: the gate is
	// energy first, then metal, and either positive carry refuses the pass
	// [05 R-ECO-01 §7].
	buckets[economy.Energy].Carry = 1

	// The same expression the build step feeds the helper.
	_, _, wantEnergy, wantMetal := ConstructionStep(product.Remaining, WorkerQuantum(facDef.WorkerTime),
		prodDef.BuildTime, product.MaxHealth, prodDef.BuildCostEnergy, prodDef.BuildCostMetal)
	if wantEnergy <= 0 || wantMetal <= 0 {
		t.Fatalf("fixture produced no demand: energy=%v metal=%v", wantEnergy, wantMetal)
	}

	svc := NewService(nil, cat, w, econ)
	node := &orders.Node{BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State3), Target: ph}
	svc.handleState3(factory, node, 7)

	if product.Remaining != 0.5 {
		t.Fatalf("denied pass advanced remaining to %v, want 0.5 [05 \"Two-resource admission\"]", product.Remaining)
	}
	if buckets[economy.Energy].Requested != wantEnergy || buckets[economy.Metal].Requested != wantMetal {
		t.Fatalf("denied pass requested energy=%v metal=%v, want %v/%v",
			buckets[economy.Energy].Requested, buckets[economy.Metal].Requested, wantEnergy, wantMetal)
	}
	if buckets[economy.Energy].Accepted != 0 || buckets[economy.Metal].Accepted != 0 {
		t.Fatalf("denied pass accepted energy=%v metal=%v, want 0/0",
			buckets[economy.Energy].Accepted, buckets[economy.Metal].Accepted)
	}
}

// A carried product takes NO decay visit at all: `GetBuilt` is never reached
// while the product is cargo [04 R-FAC-02 §4]'s 2026-09-02 correction. The
// primary pump reloads the head after every result code [04 R-ORD-01 §10], and
// `BeCarried`'s phase-1 arm sets a ten-tick deadline and returns 2 on every
// visit, so the reload finds the head gated and the pass ends there — the
// record behind it is never walked to.
//
// This test used to be TestCarriedProductDecayVisitCadenceIsTwentyTicks and
// asserted the retracted composition: a first decay visit at tick 351 and one
// every twenty ticks after it, built on §4's since-withdrawn "a *hold* (code 2)
// does NOT stop the walk — the next record is visited in the same pass".
func TestCarriedProductTakesNoDecayVisit(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	// A costly product keeps each decay step small, so the fraction is still
	// below 1.0 after every visit in the measured window and each visit is
	// visible as a change.
	def := newProductDef("armflash", 1, 1, 850, 7240)
	def.BMCode = true
	def.BuildCostEnergy = 1370
	cat.Units[def.CanonicalKey] = def
	w := newConstructionFixtureWorld(8, cat)
	fh, _ := w.Create(newFactoryDef("armlab", 2, 2, 30), 0, 0, 0, 0)
	ph, _ := w.Create(def, 0, 0, 0, 0)
	factory, product := w.Unit(fh), w.Unit(ph)
	if !movement.AttachCargo(w, factory.Handle, product.Handle, 0) {
		t.Fatal("attach failed")
	}
	q := orders.QueueForUnit(product)
	q.Push(orders.Lookup("BeCarried"), orders.Node{Target: factory.Handle})
	q.Push(orders.Lookup("GetBuilt"), orders.Node{Phase: uint8(State0), Deadline: -1})
	svc := NewService(nil, cat, w, nil)
	sim := rng.NewSimulation(12345)
	svc.OrderBinding = &orders.QueueBinding{SimRNG: &sim}
	svc.getBuiltLinks[product.Handle] = factory.Handle
	svc.queueForUnit(product)

	product.Remaining = 0.5
	var visits []uint32
	prev := product.Remaining
	for tick := uint32(1); tick <= 500; tick++ {
		q.Pump(product, tick)
		// A decay visit is the only writer of Remaining here: the negative work
		// arm drives the fraction back up [04 R-FAC-02 §4].
		if product.Remaining != prev {
			visits = append(visits, tick)
			prev = product.Remaining
		}
	}
	if len(visits) != 0 {
		t.Fatalf("carried product saw %d decay visits in 500 ticks (%v); GetBuilt is never visited while carried [04 R-FAC-02 §4]", len(visits), visits)
	}
	// The product is still carried throughout: BeCarried is what stalls the walk.
	if product.Attachment.Carrier == 0 {
		t.Fatal("product detached during the measurement")
	}
	prim := q.Primary()
	if len(prim) != 2 || orders.DescriptorFor(prim[0].ID).Name != "BeCarried" {
		t.Fatalf("primary queue %v, want BeCarried still linked ahead of GetBuilt", prim)
	}
	// GetBuilt is untouched: still the record the factory pushed, never
	// dispatched once in 500 ticks.
	if gb := prim[1]; State(gb.Phase) != State0 || gb.Deadline != -1 || gb.DynamicGate != 0 || gb.Satisfied != 0 {
		t.Fatalf("GetBuilt phase=%d deadline=%d gate=%#x satisfied=%#x, want the untouched pushed record 0/-1/0/0 [04 R-FAC-02 §4]",
			gb.Phase, gb.Deadline, gb.DynamicGate, gb.Satisfied)
	}
	// BeCarried is the record that ran: phase 1, re-armed ten ticks out from
	// its last expiry, gate carrying the deadline setter's bit 0.
	if be := prim[0]; be.Phase != 1 || be.DynamicGate != 1 {
		t.Fatalf("BeCarried phase=%d gate=%#x, want the phase-1 ten-tick hold 1/0x1 [04 R-ORD-01 §2]", be.Phase, be.DynamicGate)
	}
}
