package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Cleanup uses the argument-bearing starter even at arity zero; the edge
// starter remains a distinct operation [04 R-CB-01 §2].
func TestCleanupStopBuildingClearsPhysicalArguments(t *testing.T) {
	u, vm := cbUnit(cbProgram("StopBuilding"))
	for i := 0; i < 5; i++ {
		vm.Threads[0].Stack[i] = int32(91 + i)
	}
	n := &Node{Flags: FlagStopBuildingPending}
	emitStopBuilding(u, n)
	thread := &vm.Threads[0]
	if thread.Status != cob.ThreadRunning || thread.SP != 0 {
		t.Fatalf("cleanup callback status=%d logical arguments=%d", thread.Status, thread.SP)
	}
	for i := 0; i < 4; i++ {
		if thread.Stack[i] != 0 {
			t.Fatalf("cleanup preserved stale argument cell %d: %d", i, thread.Stack[i])
		}
	}
	if thread.Stack[4] != 95 || n.Flags&FlagStopBuildingPending != 0 {
		t.Fatal("cleanup changed storage above its four cells or retained the pending flag")
	}
	emitStopBuilding(u, n)
	if vm.Threads[1].Status != cob.ThreadIdle {
		t.Fatal("cleanup emitted twice after consuming its pending flag")
	}
}

// The immediate wake may already overwrite cells above the logical argument
// count. Inspect the entry before it runs [04 R-CB-01 §2][04 R-UNIT-06 §3].
func TestTransportDropHasOneLogicalArgumentAndFourPhysicalCells(t *testing.T) {
	_, carrier, cargo, _ := transportFixture(t,
		&content.UnitDef{UnitName: "carrier", CanMove: true, CanLoad: true, MaxDamage: 100},
		&content.UnitDef{UnitName: "cargo", MaxDamage: 50})
	scriptUnit, vm := cbUnit(cbProgram("TransportDrop"))
	carrier.ScriptState = scriptUnit.ScriptState
	carrier.Attachment.Cargo = []pool.Handle{cargo.Handle}
	for i := 0; i < 5; i++ {
		vm.Threads[0].Stack[i] = int32(91 + i)
	}
	n := &Node{GoalX: numeric.FixedFromInt(300), GoalZ: numeric.FixedFromInt(72)}
	starts := 0
	carrier.ScriptState.Binding.Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Name != "TransportDrop" || e.Phase != "start" {
			return
		}
		starts++
		thread := &vm.Threads[0]
		want := [4]int32{int32(cargo.Handle), 300<<16 | 72, 0, 0}
		if thread.SP != 1 || [4]int32(thread.Stack[:4]) != want || thread.Stack[4] != 95 {
			t.Errorf("drop entry: logical arguments=%d physical cells=%v, want arity 1 and %v", thread.SP, thread.Stack[:5], want)
		}
	})
	if got := groundUnloadHandler(carrier, n, 0, 10); got != 1 {
		t.Fatalf("drop phase returned %d, want advance", got)
	}
	if starts != 1 || vm.Threads[0].Status != cob.ThreadSleeping {
		t.Fatalf("drop starts=%d status=%d: expected one start and immediate wake barrier", starts, vm.Threads[0].Status)
	}
}
