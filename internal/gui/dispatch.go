package gui

// Name16Equal compares the fixed-width gadget name field [07 §4].
//
// The loader and runtime UI retain this small authored-record comparison for
// name-based control lookup. The former generic dispatch, modal stack, family
// adapters, and editor hooks were removed because they have no production
// callers.
func Name16Equal(a, b string) bool {
	var ba, bb [16]byte
	copy(ba[:], a)
	copy(bb[:], b)
	return ba == bb
}
