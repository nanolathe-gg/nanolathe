package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// activationFixture builds a unit whose script has `Activate` and `Deactivate`
// entry points so a started callback thread is observable without retail
// assets. Only the entry-point table matters here; the bodies never run.
func activationFixture(onOffable bool) (*units.Unit, *cob.VM) {
	prog := &cob.Program{
		Code:        []uint32{0, 0},
		Scripts:     map[string]int{"Activate": 0, "Deactivate": 1},
		ScriptsByID: []int{0, 1},
	}
	vm := cob.NewVM(prog)
	u := &units.Unit{
		Handle: 1,
		Def:    &content.UnitDef{UnitName: "activation-fixture", OnOffable: onOffable},
		Script: vm,
	}
	return u, vm
}

func runningThreads(vm *cob.VM) int {
	n := 0
	for i := range vm.Threads {
		if vm.Threads[i].Status != cob.ThreadIdle {
			n++
		}
	}
	return n
}

// TestActivateOrderRaisesEdgeOnce locks [04 R-ORD-01 §2]: the `Activate`
// handler raises edge bit 0 for an `onoffable` definition, which starts the
// `Activate` callback exactly once — the edge machine's change test suppresses
// a repeat [04 R-UNIT-06 §2] — and reports completion.
func TestActivateOrderRaisesEdgeOnce(t *testing.T) {
	u, vm := activationFixture(true)
	if u.Activated {
		t.Fatal("fixture unit must start inactive")
	}
	id := Lookup("Activate")
	if id == 0 {
		t.Fatal("Activate descriptor missing from the table")
	}
	handler := DescriptorFor(id).Handler
	if handler == nil {
		t.Fatal("Activate descriptor carries no handler")
	}

	if code := handler(u, &Node{ID: id}, 0); code != Code(5) {
		t.Fatalf("Activate result = %d, want 5 (complete)", code)
	}
	if !u.Activated {
		t.Fatal("Activate did not raise the activation bit")
	}
	if got := runningThreads(vm); got != 1 {
		t.Fatalf("started callback threads = %d, want 1", got)
	}

	if code := handler(u, &Node{ID: id}, 0); code != Code(5) {
		t.Fatalf("repeated Activate result = %d, want 5 (complete)", code)
	}
	if !u.Activated {
		t.Fatal("repeated Activate cleared the activation bit")
	}
	if got := runningThreads(vm); got != 1 {
		t.Fatalf("repeated Activate started another thread: threads = %d, want 1", got)
	}
}

// TestActivateOrderIgnoredWithoutOnOffable locks the handlers' own gate: a
// definition without `onoffable` silently accepts and discards the order,
// still reporting completion [04 R-ORD-01 §2][05 R-PROD-01 §2].
func TestActivateOrderIgnoredWithoutOnOffable(t *testing.T) {
	u, vm := activationFixture(false)
	id := Lookup("Activate")
	handler := DescriptorFor(id).Handler
	if handler == nil {
		t.Fatal("Activate descriptor carries no handler")
	}
	if code := handler(u, &Node{ID: id}, 0); code != Code(5) {
		t.Fatalf("Activate result = %d, want 5 (complete)", code)
	}
	if u.Activated {
		t.Fatal("Activate raised the bit on a definition without onoffable")
	}
	if got := runningThreads(vm); got != 0 {
		t.Fatalf("discarded order started %d callback threads, want 0", got)
	}
}

// TestDeactivateOrderLowersEdge is the falling half of [04 R-ORD-01 §2].
func TestDeactivateOrderLowersEdge(t *testing.T) {
	u, vm := activationFixture(true)
	u.SetActivationEdge(true)
	id := Lookup("Deactivate")
	if id == 0 {
		t.Fatal("Deactivate descriptor missing from the table")
	}
	handler := DescriptorFor(id).Handler
	if handler == nil {
		t.Fatal("Deactivate descriptor carries no handler")
	}
	if code := handler(u, &Node{ID: id}, 0); code != Code(5) {
		t.Fatalf("Deactivate result = %d, want 5 (complete)", code)
	}
	if u.Activated {
		t.Fatal("Deactivate did not lower the activation bit")
	}
	// One Activate thread from the raise, one Deactivate thread from the fall.
	if got := runningThreads(vm); got != 2 {
		t.Fatalf("started callback threads = %d, want 2", got)
	}
}
