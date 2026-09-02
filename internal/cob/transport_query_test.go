package cob

import "testing"

// TestTransportQueryOpcodeStackShapes locks the stack effects of the two
// interpreter slots [fmt cob] labels "cargo-membership read" and
// "carrier-identity read". [04 §4.4] and [04 R-COB-03 §5] establish them as
// transport queries, not engine port reads: the one-argument query pops a
// cargo identifier and pushes 1 on a match or 0 on an empty or exhausted list;
// the zero-argument query pops nothing and pushes this unit's carrier
// identifier, or 0 when the unit is not carried.
//
// The relationship under test is the stack arithmetic, which is what an
// authored script would observe: 0x10044000 is net zero (one pop, one push)
// and 0x10045000 is net +1 (no pop, one push). It also pins the answer for a
// VM with no transport linkage — 0 in both cases, which is the established
// answer for an empty list and an uncarried unit, not a placeholder. Neither
// opcode appears in any of the 841 retail COBs ([fmt cob] asset census).
func TestTransportQueryOpcodeStackShapes(t *testing.T) {
	const pushConst = 0x10021001 // push-constant [fmt cob][04 §4.3]
	const ret = 0x10065000

	t.Run("cargo membership pops its identifier and pushes one value", func(t *testing.T) {
		// push 0x5A5A (a sentinel below the query), push 7 (the cargo id),
		// query, return. A net-zero opcode leaves the sentinel underneath and
		// the query result on top.
		prog := &Program{
			Code:        []uint32{pushConst, 0x5A5A, pushConst, 7, 0x10044000, ret},
			Scripts:     map[string]int{"S": 0},
			ScriptsByID: []int{0},
		}
		vm := NewVM(prog)
		if !vm.Start(0, nil) {
			t.Fatalf("Start failed")
		}
		vm.Drain(1)
		if got := vm.Threads[0].Stack[0]; got != 0x5A5A {
			t.Fatalf("the query consumed more than its identifier: slot 0 = %#x want %#x", got, 0x5A5A)
		}
		if got := vm.Threads[0].Stack[1]; got != 0 {
			t.Fatalf("an unbound VM has no cargo list, so the query must push 0 [04 R-COB-03 §5]; got %d", got)
		}
	})

	t.Run("carrier identity pops nothing and pushes one value", func(t *testing.T) {
		prog := &Program{
			Code:        []uint32{pushConst, 0x5A5A, 0x10045000, ret},
			Scripts:     map[string]int{"S": 0},
			ScriptsByID: []int{0},
		}
		vm := NewVM(prog)
		if !vm.Start(0, nil) {
			t.Fatalf("Start failed")
		}
		vm.Drain(1)
		if got := vm.Threads[0].Stack[0]; got != 0x5A5A {
			t.Fatalf("the zero-argument query popped a value: slot 0 = %#x want %#x", got, 0x5A5A)
		}
		if got := vm.Threads[0].Stack[1]; got != 0 {
			t.Fatalf("an unbound VM is not carried, so the query must push 0 [04 R-COB-03 §5]; got %d", got)
		}
	})
}
