package cob

import (
	"testing"
)

// TestPieceFlagOpcodePolarity checks each of the six flag opcodes toggles exactly its bit [04 §"Piece flag polarity"].
// Lower opcode sets, higher clears: show(0x10005000)/hide(0x10006000) bit0, cache(0x10007000)/dont-cache(0x10008000) bit1, shade(0x1000d000)/dont-shade(0x1000e000) bit2.
func TestPieceFlagOpcodePolarity(t *testing.T) {
	// Standalone VM path (no unit) still must toggle exactly its bit via VM-local fallback.
	// We test both the fallback and the bound path.
	// Define cases
	cases := []struct {
		name   string
		opcode uint32
		mask   uint8
		set    bool
	}{
		{"show sets bit0 0x10005000", 0x10005000, 0x01, true},
		{"hide clears bit0 0x10006000", 0x10006000, 0x01, false},
		{"cache sets bit1 0x10007000", 0x10007000, 0x02, true},
		{"dont-cache clears bit1 0x10008000", 0x10008000, 0x02, false},
		{"shade sets bit2 0x1000d000", 0x1000d000, 0x04, true},
		{"dont-shade clears bit2 0x1000e000", 0x1000e000, 0x04, false},
	}
	// Fallback VM-local tests
	for _, tc := range cases {
		t.Run("fallback/"+tc.name, func(t *testing.T) {
			prog := &Program{
				Code:        []uint32{tc.opcode, 0, 0x10065000},
				Scripts:     map[string]int{"S": 0},
				Pieces:      []string{"a", "b"},
				ScriptsByID: []int{0},
			}
			vm := NewVM(prog)
			// Prepare: piece 0 to isolate bit
			var prepare uint8
			if tc.set {
				// Defaults are drawable+cached+shaded (0x07) only for a
				// geometry piece; a bare attachment piece defaults 0x06
				// (draw flag clear). To isolate the bit under test, start
				// from the bare-piece default 0x06 and clear the target
				// bit, so the set-opcode must be the only writer.
				prepare = 0x06 &^ tc.mask
			} else {
				prepare = 0x07 // geometry default has all three bits set
			}
			// Set piece 0 to prepare, piece1 to stable sentinel
			vm.pieceFlags[0] = prepare
			vm.pieceFlags[1] = 0x07
			beforeOther := vm.pieceFlags[1]
			vm.Threads[0].Status = ThreadRunning
			vm.Threads[0].PC = 0
			vm.Drain(1)
			if vm.Threads[0].Status != ThreadIdle {
				t.Fatalf("thread not terminated status %d", vm.Threads[0].Status)
			}
			var want uint8
			if tc.set {
				want = prepare | tc.mask
			} else {
				want = prepare &^ tc.mask
			}
			got := vm.pieceFlags[0]
			if got != want {
				t.Fatalf("piece 0 flags %#x want %#x (prepare %#x mask %#x set %v)", got, want, prepare, tc.mask, tc.set)
			}
			diff := prepare ^ got
			if diff != tc.mask {
				t.Fatalf("changed bits %#x want exactly %#x", diff, tc.mask)
			}
			if vm.pieceFlags[1] != beforeOther {
				t.Fatalf("other piece changed: before %#x after %#x", beforeOther, vm.pieceFlags[1])
			}
			// Snapshot must reflect same
			snap := vm.SnapshotFlags()
			if snap[0] != got || snap[1] != beforeOther {
				t.Fatalf("snapshot mismatch: snap[0]%#x got%#x snap[1]%#x want%#x", snap[0], got, snap[1], beforeOther)
			}
		})
	}
	// Bound path via unit record
	for _, tc := range cases {
		t.Run("bound/"+tc.name, func(t *testing.T) {
			// Create a fake unit storage
			unitFlags := []uint8{0x06, 0x07}
			var prepare uint8
			if tc.set {
				prepare = 0x06 &^ tc.mask
				if tc.mask == 0x01 {
					prepare = 0x06
				}
				if tc.mask == 0x02 {
					prepare = 0x06 &^ 0x02 // 0x04 | maybe bit0? Actually 0x06 is 0x02|0x04, so clearing 0x02 gives 0x04
					if prepare == 0x06&^0x02 {
						// keep as is
					}
				}
				if tc.mask == 0x04 {
					prepare = 0x06 &^ 0x04 // 0x02
				}
			} else {
				prepare = 0x07
			}
			unitFlags[0] = prepare
			unitFlags[1] = 0x07
			beforeOther := unitFlags[1]
			prog := &Program{
				Code:        []uint32{tc.opcode, 0, 0x10065000},
				Scripts:     map[string]int{"S": 0},
				Pieces:      []string{"a", "b"},
				ScriptsByID: []int{0},
			}
			vm := NewVM(prog)
			// Bind to unit storage [04 §"Piece flag polarity"].
			vm.BindRenderFlags(unitFlags)
			// Also test handler path
			// For this bound test we use direct slice sharing; also test handler variant in second subcase
			vm.Threads[0].Status = ThreadRunning
			vm.Threads[0].PC = 0
			vm.Drain(1)
			var want uint8
			if tc.set {
				want = prepare | tc.mask
			} else {
				want = prepare &^ tc.mask
			}
			if unitFlags[0] != want {
				t.Fatalf("bound unit flags[0] %#x want %#x (prepare %#x)", unitFlags[0], want, prepare)
			}
			diff := prepare ^ unitFlags[0]
			if diff != tc.mask {
				t.Fatalf("bound changed bits %#x want %#x", diff, tc.mask)
			}
			if unitFlags[1] != beforeOther {
				t.Fatalf("bound other piece changed")
			}
			// Verify VM snapshot reflects unit
			snap := vm.SnapshotFlags()
			if snap[0] != want || snap[1] != beforeOther {
				t.Fatalf("bound snapshot mismatch: snap %#v unit %#v", snap, unitFlags)
			}
			// Also verify that handler path (get/set funcs) same behavior
			unitFlags2 := []uint8{prepare, 0x07}
			vm2 := NewVM(prog)
			vm2.BindRenderFlagHandlers(func() []uint8 { return unitFlags2 }, func(piece int, mask uint8, set bool) bool {
				if piece < 0 || piece >= len(unitFlags2) {
					return false
				}
				if set {
					unitFlags2[piece] |= mask
				} else {
					unitFlags2[piece] &^= mask
				}
				return true
			})
			vm2.Threads[0].Status = ThreadRunning
			vm2.Threads[0].PC = 0
			vm2.Drain(1)
			if unitFlags2[0] != want {
				t.Fatalf("handler unit flags[0] %#x want %#x", unitFlags2[0], want)
			}
			if unitFlags2[0]^prepare != tc.mask {
				t.Fatalf("handler diff wrong")
			}
		})
	}
}

// TestPieceFlagsInitialSnapshot locks the VM's own piece-flag allocation
// against the retail fill pass [04 §4.3]: the array is zero-filled, bit 1
// (cache) and bit 2 (shade) are set unconditionally, and bit 0 (draw) is set
// only where the piece's model object has at least three vertices — which this
// layer cannot see, so it leaves bit 0 clear and the unit-creation binding that
// owns the model link supplies it [R-COB-01 §1].
//
// It used to assert 0x07 here, bit 0 included, describing it as a fixture
// fallback: a VM with no external binding drew nothing until Create ran. That
// made this array a second and more permissive default than retail's, and it
// is unreachable in production — the unit-owned table is installed before
// Create runs [04 §"Piece flag polarity"].
func TestPieceFlagsInitialSnapshot(t *testing.T) {
	prog := &Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"S": 0},
		Pieces:      []string{"a", "b"},
		ScriptsByID: []int{0},
	}
	vm := NewVM(prog)
	snap := vm.SnapshotFlags()
	if len(snap) != 2 {
		t.Fatalf("snap len %d want 2", len(snap))
	}
	for i, f := range snap {
		if f != 0x06 {
			t.Fatalf("piece %d initial flags %#x, want the fill pass's unconditional cache|shade 0x06 [04 §4.3]", i, f)
		}
	}
}

// TestDontShadowIsNoOpOnUnits confirms the disable-shadow opcode is an empty adapter on units [R-COB-01 §1].
func TestDontShadowIsNoOpOnUnits(t *testing.T) {
	prog := &Program{
		Code:        []uint32{0x1000a000, 0, 0x10065000},
		Scripts:     map[string]int{"S": 0},
		Pieces:      []string{"a"},
		ScriptsByID: []int{0},
	}
	vm := NewVM(prog)
	before := vm.SnapshotFlags()
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	after := vm.SnapshotFlags()
	if len(before) != len(after) {
		t.Fatalf("dont-shadow changed len")
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("dont-shadow changed flags[%d] %#x -> %#x [R-COB-01 §1]", i, before[i], after[i])
		}
	}
	// Bound path also no-op
	unitFlags := []uint8{0x07}
	vm2 := NewVM(prog)
	vm2.BindRenderFlags(unitFlags)
	vm2.Threads[0].Status = ThreadRunning
	vm2.Threads[0].PC = 0
	vm2.Drain(1)
	if unitFlags[0] != 0x07 {
		t.Fatalf("bound dont-shadow changed unit flags %#x", unitFlags[0])
	}
}
