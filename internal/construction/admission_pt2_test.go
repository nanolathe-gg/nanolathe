package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
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

// An admitted BuildingBuild/MobileBuild work step stamps the acting unit's
// shared reveal/cloak deadline outright to tick + 300 — the phase-3 work
// visit both handlers share in handleState3 [04 R-ORD-01 §5][03 R-VIS-01
// §6]. The economy consumer is the cloak-payment gate, which is why a prior,
// larger deadline must not survive: the write is unconditional, never a
// maximum.
func TestAdmittedMobileBuildStepStampsSharedRevealDeadline(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armck", 1, 1, 60) // a mobile builder, not a factory
	prodDef := newProductDef("armflash", 1, 1, 100, 100)
	cat.Units[facDef.CanonicalKey] = facDef
	cat.Units[prodDef.CanonicalKey] = prodDef
	w := newConstructionFixtureWorld(12, cat)
	fh, _ := w.Create(facDef, 0, 0, 0, 0)
	ph, _ := w.Create(prodDef, 0, 0, 0, 0)
	builder, product := w.Unit(fh), w.Unit(ph)
	product.Remaining, product.MaxHealth = 0.5, 100
	builder.RevealDeadline = 999999 // a prior, larger value must not survive.

	econ := &economy.Service{}
	svc := NewService(nil, cat, w, econ)
	mobileID := orders.Lookup(MobileBuildOrder)
	if mobileID == 0 {
		t.Fatal("MobileBuild descriptor missing")
	}
	node := &orders.Node{ID: mobileID, BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State3), Target: ph}
	const tick = 7
	svc.handleState3(builder, node, tick)

	if product.Remaining == 0.5 {
		t.Fatal("fixture work step was denied; want an admitted pass so the stamp fires")
	}
	// "MobileBuild (work phase)" is one of the ten reveal-stamp handler sites
	// at tick + 300 [04 R-ORD-01 §5 "The reveal stamp"].
	if want := uint32(tick) + 300; builder.RevealDeadline != want {
		t.Fatalf("RevealDeadline=%d want %d (tick+300)", builder.RevealDeadline, want)
	}
}

// TestAdmittedBuildingBuildStepDoesNotStampRevealDeadline locks the
// correction (RWU-19-26): unlike MobileBuild, BuildingBuild's phase-3 work
// visit — the very same handleState3 loop — never writes the shared
// reveal/cloak deadline [04 R-ORD-01 §5 "The reveal stamp"].
func TestAdmittedBuildingBuildStepDoesNotStampRevealDeadline(t *testing.T) {
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
	factory.RevealDeadline = 42 // must survive untouched.

	econ := &economy.Service{}
	svc := NewService(nil, cat, w, econ)
	buildingID := orders.Lookup(FactoryBuildOrder)
	if buildingID == 0 {
		t.Fatal("BuildingBuild descriptor missing")
	}
	node := &orders.Node{ID: buildingID, BuildDefKey: prodDef.CanonicalKey, Param2: 1, Phase: uint8(State3), Target: ph}
	const tick = 7
	svc.handleState3(factory, node, tick)

	if product.Remaining == 0.5 {
		t.Fatal("fixture work step was denied; want an admitted pass to exercise the non-stamp")
	}
	if factory.RevealDeadline != 42 {
		t.Fatalf("RevealDeadline=%d want untouched 42; BuildingBuild must never write the reveal stamp", factory.RevealDeadline)
	}
}

// A carried product that no builder works on DOES decay, on `GetBuilt`'s own
// arms: phase 0 arms 300 ticks, phase 1 arms 30, and every phase-2 visit
// without the wake bit runs the negative work step and re-arms 11
// [04 R-ORD-01 §11][04 R-FAC-02 §4]. The cadence is therefore 331, then every
// 11 ticks, with nothing aligning it to `BeCarried` — because `BeCarried` is
// not ahead of `GetBuilt`. The attach commit head-inserts `BeCarried`
// ([04 R-FAC-02 §1] step 5) and the queued `GetBuilt` insertion then takes the
// head-insert branch its static bit 5 selects [04 R-ORD-01 §13], leaving
// `[GetBuilt, BeCarried]`.
//
// This test has now been rewritten twice. It was
// TestCarriedProductDecayVisitCadenceIsTwentyTicks (a first visit at 351 and a
// twenty-tick cadence, built on §4's withdrawn "a hold does not stop the
// walk"), then TestCarriedProductTakesNoDecayVisit (no visit at all, built on
// §4's "GetBuilt is never visited while the product is carried"). Both rested
// on `BeCarried` being the head, which the 2026-09-04 trace of the producer
// insertion disproves; §11's "the factory case is unaffected" carve-out is
// corrected with it, and the plain contract — 30 unworked ticks, 11 after a
// decay visit — now holds for factory products too.
func TestCarriedProductDecaysWhenUnworked(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	// A costly product keeps each decay step small, so the fraction is still
	// below 1.0 after every visit in the measured window and each visit is
	// visible as a change.
	def := newProductDef("armflash", 1, 1, 850, 7240)
	def.BMCode = 1
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
	if len(visits) == 0 {
		t.Fatal("carried product saw no decay visit in 500 ticks; an unworked nanoframe decays whether it is cargo or not [04 R-ORD-01 §11]")
	}
	if visits[0] != 331 {
		t.Fatalf("first decay visit at tick %d, want 331 — phase 0 arms 300 and phase 1 arms 30 [04 R-FAC-02 §4]", visits[0])
	}
	for i := 1; i < len(visits); i++ {
		if visits[i]-visits[i-1] != 11 {
			t.Fatalf("decay visits %v: gap %d at index %d, want the 11-tick re-arm [04 R-ORD-01 §11]", visits, visits[i]-visits[i-1], i)
		}
	}
	// The product is still carried throughout: nothing in this fixture detaches
	// it, and `BeCarried` never runs to observe the carrier at all.
	if product.Attachment.Carrier == 0 {
		t.Fatal("product detached during the measurement")
	}
	prim := q.Primary()
	if len(prim) != 2 || orders.DescriptorFor(prim[0].ID).Name != "GetBuilt" {
		t.Fatalf("primary queue %v, want GetBuilt at the head ahead of BeCarried [04 R-ORD-01 §13]", prim)
	}
	// BeCarried is untouched: the pass stops at the gated GetBuilt head after
	// every code [04 R-ORD-01 §10], so the record behind it is never walked to.
	if be := prim[1]; be.Phase != 0 || be.Deadline != -1 || be.DynamicGate != 0 || be.Satisfied != 0 {
		t.Fatalf("BeCarried phase=%d deadline=%d gate=%#x satisfied=%#x, want the untouched pushed record 0/-1/0/0 [04 R-ORD-01 §10]",
			be.Phase, be.Deadline, be.DynamicGate, be.Satisfied)
	}
	// GetBuilt is the record that ran: phase 2, holding on the 11-tick decay
	// re-arm with the wake bit and the deadline setter's bit 0 in its gate.
	if gb := prim[0]; State(gb.Phase) != State2 || gb.DynamicGate != 0x8001 {
		t.Fatalf("GetBuilt phase=%d gate=%#x, want the phase-2 decay hold 2/0x8001 [04 R-ORD-01 §11]", gb.Phase, gb.DynamicGate)
	}
}
