package cob

import "testing"

// TestBoundTransportQueriesAnswerFromTheLinkage locks the two transport
// queries against a bound cargo linkage [04 §4.4][04 R-COB-03 §5]: the
// one-argument query walks this unit's own cargo list and pushes 1 on the
// first match and 0 when the list is empty or exhausted; the zero-argument
// query follows this unit's carrier back-pointer and pushes that unit's
// identifier, or 0 when it is not being carried.
//
// The stack shapes are pinned by TestTransportQueryOpcodeStackShapes; what
// this adds is the answer itself, which was hard-coded to zero while the VM
// had no linkage to read.
func TestBoundTransportQueriesAnswerFromTheLinkage(t *testing.T) {
	const pushConst = 0x10021001 // push-constant [fmt cob][04 §4.3]
	const ret = 0x10065000

	membership := func(t *testing.T, cargo []int32, carrier int32, query int32) int32 {
		t.Helper()
		prog := &Program{
			Code:        []uint32{pushConst, uint32(query), 0x10044000, ret},
			Scripts:     map[string]int{"S": 0},
			ScriptsByID: []int{0},
		}
		vm := NewVM(prog)
		vm.BindTransportQueries(
			func(id int32) bool {
				for i := range cargo {
					if cargo[i] == id {
						return true
					}
				}
				return false
			},
			func() int32 { return carrier },
		)
		if !vm.Start(0, nil) {
			t.Fatal("Start failed")
		}
		vm.Drain(1)
		return vm.Threads[0].Stack[0]
	}

	identity := func(t *testing.T, carrier int32) int32 {
		t.Helper()
		prog := &Program{
			Code:        []uint32{0x10045000, ret},
			Scripts:     map[string]int{"S": 0},
			ScriptsByID: []int{0},
		}
		vm := NewVM(prog)
		vm.BindTransportQueries(nil, func() int32 { return carrier })
		if !vm.Start(0, nil) {
			t.Fatal("Start failed")
		}
		vm.Drain(1)
		return vm.Threads[0].Stack[0]
	}

	// A carrier holding two cargo units: both are members, anything else is not.
	carrierCargo := []int32{11, 12}
	if got := membership(t, carrierCargo, 0, 11); got != 1 {
		t.Fatalf("head of the cargo list answered %d, want 1", got)
	}
	if got := membership(t, carrierCargo, 0, 12); got != 1 {
		t.Fatalf("second cargo entry answered %d, want 1", got)
	}
	if got := membership(t, carrierCargo, 0, 13); got != 0 {
		t.Fatalf("an exhausted list answered %d, want 0", got)
	}

	// A lone unit: an empty list answers 0, and it is not carried.
	if got := membership(t, nil, 0, 11); got != 0 {
		t.Fatalf("an empty cargo list answered %d, want 0", got)
	}
	if got := identity(t, 0); got != 0 {
		t.Fatalf("an uncarried unit answered %d, want 0", got)
	}

	// Cargo reads its carrier's identifier back.
	if got := identity(t, 7); got != 7 {
		t.Fatalf("a carried unit answered %d, want its carrier 7", got)
	}
}
