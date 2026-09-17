package cob

import "testing"

// A mod wake routine can await state that only Create would initialize. The
// visual interpreter must retire that loop; the authoritative VM stays uncapped.
func TestPresentationVMStopsNonYieldingRoutine(t *testing.T) {
	p := synthProg([]uint32{0x10064000, 0}, nil, 0, []int{0})
	vm := NewPresentationVM(p, 32)
	if !vm.Start(0, nil) {
		t.Fatal("could not start fixture")
	}
	vm.Drain(1)
	if vm.ActiveThreadCount() != 0 || len(vm.Diagnostics()) != 1 {
		t.Fatal("visual loop did not stop at its bound")
	}
	if NewVM(p).presentationInstructionLimit != 0 {
		t.Fatal("visual bound leaked into authoritative VM")
	}
}
