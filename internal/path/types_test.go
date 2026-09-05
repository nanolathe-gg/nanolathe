package path

import "testing"

// TestStatusConstants locks the two search status words the scheduler and the
// order layer branch on [04 R-PATH-01 §4]: 0x100 "the start already satisfies
// the goal" and 0x200 "the request was rejected". They are compared as bare
// numbers at the call sites, so a renumbering would be silent.
//
// Two field-assignment tests stood beside this one — one checked that a struct
// literal's fields held the values it was written with, the other that a type
// satisfied an interface it is assigned to in non-test code. The compiler
// proves both, and neither states a retail contract.
func TestStatusConstants(t *testing.T) {
	if StatusAlreadySatisfied != 0x100 {
		t.Fatalf("StatusAlreadySatisfied want 0x100 got %#x", StatusAlreadySatisfied)
	}
	if StatusRejected != 0x200 {
		t.Fatalf("StatusRejected want 0x200 got %#x", StatusRejected)
	}
}
