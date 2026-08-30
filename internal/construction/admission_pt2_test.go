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

// The decay-visit cadence a carried product actually sees. GetBuilt's phase-2
// arm re-arms eleven ticks out, but the walk can only reach GetBuilt on a tick
// on which the BeCarried record ahead of it has just expired, and that record
// re-arms every ten ticks. The two compose to a visit every twenty ticks from
// the first decay visit [04 R-FAC-02 §4].
func TestCarriedProductDecayVisitCadenceIsTwentyTicks(t *testing.T) {
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
	if len(visits) < 5 {
		t.Fatalf("carried product saw %d decay visits in 500 ticks: %v", len(visits), visits)
	}
	if visits[0] != 351 {
		t.Fatalf("first decay visit at tick %d, want 351 [04 R-FAC-02 §4]", visits[0])
	}
	for i := 1; i < len(visits); i++ {
		if got := visits[i] - visits[i-1]; got != 20 {
			t.Fatalf("decay visits %v: interval %d between %d and %d, want 20 [04 R-FAC-02 §4]",
				visits, got, visits[i-1], visits[i])
		}
	}
	// The product is still carried throughout: BeCarried is what paces the walk.
	if product.Attachment.Carrier == 0 {
		t.Fatal("product detached during the measurement")
	}
	if len(q.Primary()) != 2 || orders.DescriptorFor(q.Primary()[0].ID).Name != "BeCarried" {
		t.Fatalf("primary queue %v, want BeCarried still linked ahead of GetBuilt", q.Primary())
	}
}
