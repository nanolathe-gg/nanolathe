package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestQueryNanoPiecePreservesSeedAndCompletesBlockedCallback verifies that a
// concrete builder query returns its partial zero seed while the sleeping COB
// thread remains owned by the VM until a later drain [04 §4.2][04 §5.3].
func TestQueryNanoPiecePreservesSeedAndCompletesBlockedCallback(t *testing.T) {
	prog := &cob.Program{
		Code:        []uint32{0x10021001, 100, 0x10013000, 0x10065000}, // sleep, then return
		Scripts:     map[string]int{"QueryNanoPiece": 0},
		ScriptsByID: []int{0},
		Pieces:      []string{"base"},
	}
	vm := cob.NewVM(prog)
	mdl := trivialModel(1, nil)
	binding := &cob.Binding{
		VM:        vm,
		Model:     mdl,
		PieceMap:  []int{0},
		Callbacks: cob.NewCallbackBridge(vm),
	}
	builder := &units.Unit{
		Handle: 1,
		X:      world.CellToWorld(10),
		Y:      numeric.FixedFromInt(2),
		Z:      world.CellToWorld(4),
		Script: vm,
		ScriptState: &units.ScriptState{
			VM: vm, Binding: binding,
		},
	}

	piece, pos, ok := (&Service{}).QueryNanoPiece(builder)
	if !ok || piece != 0 || pos.X() != builder.X || pos.Y() != builder.Y || pos.Z() != builder.Z {
		t.Fatalf("blocked query piece=%d pos=(%d,%d,%d) ok=%t, want zero-seeded builder origin", piece, pos.X().Raw(), pos.Y().Raw(), pos.Z().Raw(), ok)
	}
	if liveThreads(vm) != 1 {
		t.Fatalf("blocked QueryNanoPiece thread was not retained: live=%d", liveThreads(vm))
	}
	vm.Drain(1)
	if liveThreads(vm) != 1 {
		t.Fatalf("partial query completed before its sleep elapsed: live=%d", liveThreads(vm))
	}
	vm.Drain(1)
	vm.Drain(1)
	if liveThreads(vm) != 0 {
		t.Fatalf("blocked QueryNanoPiece thread remained after drain: live=%d", liveThreads(vm))
	}
}
