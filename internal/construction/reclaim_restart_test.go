package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// arrangedScripts reports which of the bridge-bound scripts have live threads,
// in allocation order. bindScriptBridge compiles one code word per name at the
// word index that is also the script id, so a deferred arrangement's thread
// program counter names the script it was arranged for.
func arrangedScripts(vm *cob.VM, names ...string) []string {
	var out []string
	for i := range vm.Threads {
		if !vm.IsThreadAlive(i) {
			continue
		}
		pc := vm.Threads[i].PC
		if pc >= 0 && pc < len(names) {
			out = append(out, names[pc])
		}
	}
	return out
}

// TestReclaimOutOfReachRestartEmitsStopThenStart locks the `ReclaimUnit` row's
// restart arm [04 R-ORD-01 §5]: "either fails → deadline 15, `StopBuilding`,
// *restart*", and the restart is what makes the record pass its in-reach phase
// again and emit `StartBuilding` a second time — the emitter runs "not per
// visit, only per restart" [04 R-ORD-01 §5, settling R-ORDER-02 §2].
//
// The two emissions are exactly one each. A reclaimer that walks out of reach
// and back must not leave StartBuilding running for the gap (the script's
// nanolathe stays lit on a builder doing nothing), and must not re-arrange
// StartBuilding on every out-of-reach poll on the way back.
func TestReclaimOutOfReachRestartEmitsStopThenStart(t *testing.T) {
	// Health 100 with the fixture's WorkerTime 30 gives pulse 1: the target
	// survives every visit, so the record stays alive across the excursion.
	s, builder, target, node := reclaimFixture(t, 100, 10)
	target.Remaining = 0
	s.Movement = movement.NewSystem(&world.Terrain{CellW: 32, CellH: 32, Plot: make([]world.PlotCell, 1024)}, movement.Profile{FootPrintX: 1, FootPrintZ: 1}, movement.NewOccupancyGrid())
	s.Movement.BindWorld(s.World)
	s.Movement.EnsureUnit(builder)
	orders.QueueForUnit(builder).SetBinding(&orders.QueueBinding{Lookup: func(pool.Handle) *units.Unit { return builder }})
	vm := bindScriptBridge(t, builder, "StartBuilding", "StopBuilding")
	step := func(tick uint32) {
		s.StepUnit(TickContext{Tick: tick, World: s.World, Economy: s.Economy, Catalog: s.Catalog}, builder.Handle)
	}

	// In reach: the setup visit establishes the pulse and arms StartBuilding.
	step(0)
	if node.Param1 == 0 {
		t.Fatal("setup visit did not establish the reclaim pulse")
	}
	if got := arrangedScripts(vm, "StartBuilding", "StopBuilding"); len(got) != 1 || got[0] != "StartBuilding" {
		t.Fatalf("after the setup visit: arrangements %v, want [StartBuilding]", got)
	}

	// Walk out of reach. The reach test is in whole world units against
	// `builddistance` (10 here) plus the target's radius [05 R-WORK-01 §2].
	builder.X = numeric.Fixed(500 << 16)
	step(2)
	if got := arrangedScripts(vm, "StartBuilding", "StopBuilding"); len(got) != 2 || got[1] != "StopBuilding" {
		t.Fatalf("out-of-reach transition: arrangements %v, want [StartBuilding StopBuilding]", got)
	}
	if node.Phase != 0 {
		t.Fatalf("restart phase=%d, want 0", node.Phase)
	}
	if node.Deadline != int32(2+reclaimRestartDelay) {
		t.Fatalf("restart deadline %d, want %d", node.Deadline, 2+reclaimRestartDelay)
	}

	// Further out-of-reach polls emit nothing: the mid-life emission is gated
	// on the record's pending flag, which the first one cleared.
	step(2)
	step(3)
	if got := arrangedScripts(vm, "StartBuilding", "StopBuilding"); len(got) != 2 {
		t.Fatalf("out-of-reach polls emitted again: arrangements %v", got)
	}

	// Back in reach: one StartBuilding, once, on the visit that re-seeds the
	// pulse after the restart deadline elapses.
	builder.X = 0
	step(4) // inside the 15-tick restart wait: no work, no emission
	if got := arrangedScripts(vm, "StartBuilding", "StopBuilding"); len(got) != 2 {
		t.Fatalf("re-entry emitted before the restart deadline: arrangements %v", got)
	}
	step(2 + reclaimRestartDelay)
	if node.Phase != 1 || node.Param2 != 0 {
		t.Fatal("restart did not re-arm the approach and reset cadence")
	}
	node.Satisfied |= 0x20 // the movement follower's arrival [04 R-PATH-01 §9]
	step(3 + reclaimRestartDelay)
	if node.Param1 == 0 {
		t.Fatal("re-entry did not re-seed the reclaim pulse")
	}
	got := arrangedScripts(vm, "StartBuilding", "StopBuilding")
	if len(got) != 3 || got[2] != "StartBuilding" {
		t.Fatalf("re-entry: arrangements %v, want [StartBuilding StopBuilding StartBuilding]", got)
	}

	// And it stays at one: later in-reach work visits do not re-arrange it.
	step(4 + reclaimRestartDelay)
	step(5 + reclaimRestartDelay)
	if got := arrangedScripts(vm, "StartBuilding", "StopBuilding"); len(got) != 3 {
		t.Fatalf("in-reach work visits re-emitted StartBuilding: arrangements %v", got)
	}
}
