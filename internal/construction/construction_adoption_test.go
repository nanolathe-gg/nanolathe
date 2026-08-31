package construction

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// bindScriptBridge attaches a minimal production COB binding carrying the
// named scripts, so deferred arrangements allocate observable threads. The
// two-instruction program body mirrors the existing edge-dedup fixture.
func bindScriptBridge(t *testing.T, u *units.Unit, names ...string) *cob.VM {
	t.Helper()
	scripts := make(map[string]int, len(names))
	ids := make([]int, len(names))
	for i, n := range names {
		scripts[n] = i
		ids[i] = i
	}
	prog := &cob.Program{Code: make([]uint32, len(names)), Scripts: scripts, ScriptsByID: ids, Pieces: []string{"base"}}
	vm := cob.NewVM(prog)
	bridge := cob.NewCallbackBridge(vm)
	u.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{VM: vm, Model: trivialModel(1, nil), PieceMap: []int{0}, Callbacks: bridge}}
	u.Script = vm
	return vm
}

// TestMobileBuildEmitsStartBuildingThroughOrders locks the MobileBuild/VTOL_
// MobileBuild adoption of the order-record StartBuilding emitter [R-ORDER-02
// §2]: the success epilogue sets the record's StopBuilding-pending flag (the
// flag's only writer is orders.EmitStartBuilding) and the flag drives the
// StopBuilding counterpart on the record's removal path.
func TestMobileBuildEmitsStartBuildingThroughOrders(t *testing.T) {
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	builderDef := &content.UnitDef{UnitName: "armck", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", Builder: true, MaxDamage: 100, WorkerTime: 30, CanMove: true}
	builderDef.CanonicalKey = content.CanonicalKey("armck")
	prodDef := &content.UnitDef{UnitName: "armllt", FootprintX: 2, FootprintZ: 2, YardMap: "oooo", MaxDamage: 100, BuildTime: 100, BuildCostMetal: 100, BuildCostEnergy: 100}
	prodDef.CanonicalKey = content.CanonicalKey("armllt")
	cat.Units[content.CanonicalKey("armck")] = builderDef
	cat.Units[content.CanonicalKey("armllt")] = prodDef

	terrain := &world.Terrain{CellW: 20, CellH: 20, Plot: make([]world.PlotCell, 400)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	w := newConstructionFixtureWorld(10, cat)
	hb, _ := w.Create(builderDef, 0, numeric.Fixed(0), numeric.Fixed(0), numeric.Fixed(0))
	builder := w.Unit(hb)
	builder.Def = builderDef
	vm := bindScriptBridge(t, builder, "StartBuilding", "StopBuilding")

	if err := QueueMobileBuild(builder, "armllt", world.CellToWorld(10), world.CellToWorld(10), 1, cat); err != nil {
		t.Fatalf("QueueMobileBuild: %v", err)
	}
	// Cleanup resolves the record's owner through the queue binding [R-ORDER-02 §2].
	orders.QueueForUnit(builder).Lookup = func(pool.Handle) *units.Unit { return builder }
	node := orders.QueueForUnit(builder).Primary()[0]
	node.Phase = uint8(State2)
	svc := NewService(terrain, cat, w, &economy.Service{})
	svc.Pump(builder, 0)
	if node.Target == 0 {
		t.Fatalf("legal site did not allocate")
	}
	if node.Flags&orders.FlagStopBuildingPending == 0 {
		t.Fatal("mobile-build success did not set the record's StopBuilding-pending flag")
	}
	if liveThreads(vm) != 1 {
		t.Fatalf("StartBuilding arrangements %d, want exactly one deferred start", liveThreads(vm))
	}
	// Completion removes the record; cleanup emits the StopBuilding counterpart
	// and clears the flag [R-ORDER-02 §2].
	if prod := w.Unit(node.Target); prod != nil {
		prod.Remaining = 0
	}
	node.Phase = uint8(State4)
	svc.Pump(builder, 1)
	if orders.QueueForUnit(builder).LenPrimary() != 0 {
		t.Fatal("completed mobile build record was not removed")
	}
	if node.Flags&orders.FlagStopBuildingPending != 0 {
		t.Fatal("cleanup did not clear the StopBuilding-pending flag")
	}
	if liveThreads(vm) != 2 {
		t.Fatalf("StopBuilding counterpart missing: threads %d, want 2", liveThreads(vm))
	}
}

// TestReclaimEmitsStartBuildingThroughOrders locks the Reclaim (unit reclaim)
// adoption of the order-record StartBuilding emitter [R-ORDER-02 §2]: the
// setup visit that establishes the pulse arranges StartBuilding and sets the
// record's pending flag; the fatal-completion removal emits the counterpart.
func TestReclaimEmitsStartBuildingThroughOrders(t *testing.T) {
	s, builder, target, node := reclaimFixture(t, 1, 10)
	builder.Def.WorkerTime = 300 // pulse 15, one pulse reclaims fatally
	// Cleanup resolves the record's owner through the queue binding [R-ORDER-02 §2].
	orders.QueueForUnit(builder).Lookup = func(pool.Handle) *units.Unit { return builder }
	vm := bindScriptBridge(t, builder, "StartBuilding", "StopBuilding")
	target.Remaining = 0.25

	// Setup visit: pulse established, StartBuilding arranged, flag set.
	s.StepUnit(TickContext{Tick: 0, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
	if node.Param1 == 0 {
		t.Fatal("setup visit did not establish the reclaim pulse")
	}
	if node.Flags&orders.FlagStopBuildingPending == 0 {
		t.Fatal("reclaim setup visit did not set the record's StopBuilding-pending flag")
	}
	if liveThreads(vm) != 1 {
		t.Fatalf("StartBuilding arrangements %d, want exactly one deferred start", liveThreads(vm))
	}
	// Eight more admitted visits exceed the cadence gate and reclaim fatally;
	// the removal emits the StopBuilding counterpart through cleanup. The
	// eighth was added with the PT3-05 cadence correction: [05 R-WORK-01 §4]
	// tests the counter before raising it, so the pulse fires on the visit that
	// sees 16 rather than on the one that raises the counter to 16.
	reclaimVisits(s, builder, node, 2, 4, 6, 8, 10, 12, 14, 16)
	if !target.Dying {
		t.Fatal("reclaim did not complete fatally")
	}
	if orders.QueueForUnit(builder).LenPrimary() != 0 {
		t.Fatal("reclaim record survived fatal completion")
	}
	if node.Flags&orders.FlagStopBuildingPending != 0 {
		t.Fatal("cleanup did not clear the StopBuilding-pending flag")
	}
	if liveThreads(vm) != 2 {
		t.Fatalf("StopBuilding counterpart missing: threads %d, want 2", liveThreads(vm))
	}
}

// TestNoOtherStartBuildingFlagWriterInConstruction proves the adoption is
// exclusive: no construction source writes or clears the order record's
// StopBuilding-pending flag directly — the only writer is the order-record
// emitter orders.EmitStartBuilding [R-ORDER-02 §2] — and the slot-form
// heading emission stays at its single construction-command site where it
// writes no flag (corrected [04 §5.3]).
func TestNoOtherStartBuildingFlagWriterInConstruction(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	emitSites := map[string]bool{}
	headingSites := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if strings.Contains(src, "FlagStopBuildingPending") {
			t.Errorf("%s touches the StopBuilding-pending flag directly; only orders.EmitStartBuilding may write it [R-ORDER-02 §2]", name)
		}
		if strings.Contains(src, "orders.EmitStartBuilding(") {
			emitSites[name] = true
		}
		if n := strings.Count(src, "StartBuildingHeading("); n > 0 {
			headingSites[name] = true
			if n != 1 {
				t.Errorf("%s issues the slot-form heading emission %d times, want the single construction-command site", name, n)
			}
		}
	}
	for _, want := range []string{"factory.go", "reclaim.go"} {
		if !emitSites[want] {
			t.Errorf("%s does not adopt orders.EmitStartBuilding for its nanolathe/assist site [R-ORDER-02 §2]", want)
		}
	}
	if len(headingSites) != 1 || !headingSites["factory.go"] {
		t.Errorf("slot-form heading emission sites %v, want only factory.go (the construction-command producer)", headingSites)
	}
}

// ---------------------------------------------------------------------------
// Build assistance across the two packages (PT3-04)
// ---------------------------------------------------------------------------

// assistFixture stands a factory up in its work state on one nanoframe and puts
// `assistantQuanta` separate builders on the same frame through the order pump's
// `HelpBuild` row. It is the cross-package shape of the defect: the frame's
// owner is driven by this package's state machine and the assistants by
// internal/orders, and both call the shared construction step of
// [05 R-WORK-01 §1] against the one shared remaining fraction.
func assistFixture(t *testing.T, factoryWorkerTime int32, assistantQuanta ...int32) (*Service, *units.Unit, []*units.Unit, *units.Unit) {
	t.Helper()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	factoryDef := newFactoryDef("assist_factory", 1, 1, factoryWorkerTime)
	factoryDef.BuildDistance = 1000
	productDef := newProductDef("assist_product", 1, 1, 100, 100)
	cat.Units[factoryDef.CanonicalKey] = factoryDef
	cat.Units[productDef.CanonicalKey] = productDef
	assistDefs := make([]*content.UnitDef, len(assistantQuanta))
	for i, quantum := range assistantQuanta {
		d := newFactoryDef(fmt.Sprintf("assist_helper_%d", i), 1, 1, 30*quantum)
		d.BMCode = true
		d.CanMove = true
		d.BuildDistance = 1000
		assistDefs[i] = d
		cat.Units[d.CanonicalKey] = d
	}

	w := newConstructionFixtureWorld(16, cat)
	fh, err := w.Create(factoryDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	ph, err := w.Create(productDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	factory, product := w.Unit(fh), w.Unit(ph)
	factory.Activated = true
	factory.InBuildStance = true
	product.Remaining = 1
	product.Health = 0
	product.Flags &^= FlagCompleted

	econ := &economy.Service{}
	binding := &orders.QueueBinding{
		StockpileEconomy: econ,
		Lookup:           func(h pool.Handle) *units.Unit { return w.Unit(h) },
	}
	svc := NewService(exitTerrain(16, 16), cat, w, econ)
	svc.OrderBinding = binding

	fq := orders.QueueForUnit(factory)
	fq.SetBinding(binding)
	fq.Push(orders.Lookup(FactoryBuildOrder), orders.Node{
		BuildDefKey: productDef.CanonicalKey, Param2: 1, Phase: uint8(State3), Target: ph,
	})

	assistants := make([]*units.Unit, 0, len(assistDefs))
	for _, d := range assistDefs {
		ah, err := w.Create(d, 0, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		a := w.Unit(ah)
		a.Activated = true
		a.InBuildStance = true
		aq := orders.QueueForUnit(a)
		aq.SetBinding(binding)
		aq.Push(orders.Lookup("HelpBuild"), orders.Node{Owner: ah, Target: ph})
		assistants = append(assistants, a)
	}
	return svc, factory, assistants, product
}

// assistTick is one authoritative tick over the fixture: the frame's owner
// advances through this package's pump, then each assistant through the order
// pump, in slot order.
func assistTick(svc *Service, factory *units.Unit, assistants []*units.Unit, tick uint32) {
	svc.Pump(factory, tick)
	for _, a := range assistants {
		orders.QueueForUnit(a).Pump(a, tick)
	}
}

// TestAssistantsAddTheirRateToAFactoryProduct is PT3-04's cross-package
// regression, and covers two of the three cases the defect named: a builder
// assisting a factory's production, and several builders on one frame. Before
// the fix the `HelpBuild` work phase admitted no work, so the frame advanced by
// the factory's quantum alone no matter how many builders were attached.
func TestAssistantsAddTheirRateToAFactoryProduct(t *testing.T) {
	// Factory quantum 1, assistants 20 and 9: 30 quanta over a build time of
	// 100 advances the frame by 0.30 in one tick.
	svc, factory, assistants, product := assistFixture(t, 30, 20, 9)

	assistTick(svc, factory, assistants, 1)

	want := float32(1)
	for _, quantum := range []int32{1, 20, 9} {
		want -= float32(quantum) / float32(100)
	}
	if product.Remaining != want {
		t.Fatalf("remaining after one tick = %v, want %v: the owner's and both assistants' quanta sum on the one shared fraction [05 R-WORK-01 §1]", product.Remaining, want)
	}
	// Each contributor billed its own subrecord; nothing is pooled.
	for i, u := range append([]*units.Unit{factory}, assistants...) {
		b := svc.Economy.UnitBuckets(u.Handle)
		if b[economy.Metal].Accepted <= 0 || b[economy.Energy].Accepted <= 0 {
			t.Fatalf("contributor %d accepted %v energy / %v metal, want its own share of the drain [05 R-ECO-01 §7]", i, b[economy.Energy].Accepted, b[economy.Metal].Accepted)
		}
	}
}

// TestAssistedFrameCompletesUnderItsOwnersTransition locks the completion end:
// an assisted frame reaches a zero fraction sooner, and the completion posture
// is applied exactly once, by the owner whose state machine owns the product
// [04 R-FAC-02 §3].
//
// The one extra tick is deliberate and is the divergence work.go's `HelpBuild`
// work phase records: [05 R-WORK-01 §1] ends "on both arms and also on the
// admission-refused path, if remaining == 0 run the completion transition",
// i.e. whichever builder zeroed the fraction completes the product. The
// transition needs this package's Service (occupancy retirement, cargo detach,
// the activation edge) and internal/orders cannot reach it, so when an ASSISTANT
// lands the last increment the posture arrives on the owner's next visit
// instead of within the same one.
func TestAssistedFrameCompletesUnderItsOwnersTransition(t *testing.T) {
	run := func(assistants ...int32) uint32 {
		svc, factory, helpers, product := assistFixture(t, 30, assistants...)
		var zeroed uint32
		for tick := uint32(1); tick <= 200; tick++ {
			assistTick(svc, factory, helpers, tick)
			if product.Remaining == 0 {
				if zeroed == 0 {
					zeroed = tick
					continue // the owner's next visit runs the transition
				}
				break
			}
		}
		if product.Flags&FlagCompleted == 0 {
			t.Fatal("frame reached a zero fraction without the completion posture [04 R-FAC-02 §3]")
		}
		if product.Health != product.MaxHealth {
			t.Fatalf("completed frame health = %d/%d", product.Health, product.MaxHealth)
		}
		return zeroed
	}
	alone := run()
	helped := run(9)
	if helped >= alone {
		t.Fatalf("assisted build took %d ticks, unassisted %d: the assistant's rate must shorten it [05 R-WORK-01 §1]", helped, alone)
	}
}
