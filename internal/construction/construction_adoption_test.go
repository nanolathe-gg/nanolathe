package construction

import (
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
	w := units.NewSliced(10, cat)
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
	svc.AllowSyntheticPlacement = true
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
	// Seven more admitted visits exceed the cadence gate and reclaim fatally;
	// the removal emits the StopBuilding counterpart through cleanup.
	reclaimVisits(s, builder, node, 2, 4, 6, 8, 10, 12, 14)
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
