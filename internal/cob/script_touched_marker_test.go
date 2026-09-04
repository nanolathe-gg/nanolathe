package cob

// Contract tests for the SCRIPT-TOUCHED MARKER, the producer of order gate bit
// 0x4 [04 R-COB-06]. The finding is Established by an instruction-level census:
// every arm of the engine-write opcode dispatch — all six write arms (ports 1,
// 5, 6, 18, 19, 20) AND the fall-through an identifier with no write arm takes,
// which is where the range check also sends an identifier outside 1..20 — ORs
// bit 2 into the owning unit's order-event word. The bit carries no value.
//
// These tests pin the "every arm, no exceptions" half at the VM. The unit-side
// half (the raise landing in Unit.Pending) is locked in internal/units, and the
// consumer half (the pump's merge waking a parked record) in internal/orders.

import "testing"

// writeProgram authors one script that performs an engine write per (id, value)
// pair, in the retail compiled form: identifier pushed first, value second,
// then the set opcode [R-P0-10].
func writeProgram(t *testing.T, writes [][2]int32) *Program {
	t.Helper()
	var code []uint32
	for _, w := range writes {
		code = append(code,
			0x10021001, uint32(w[0]), // push identifier
			0x10021001, uint32(w[1]), // push value
			0x10082000, // set
		)
	}
	code = append(code, 0x10065000) // return
	prog, err := Load(makeCOB(code, []string{"Probe"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatalf("authoring the probe COB failed: %v", err)
	}
	return prog
}

// runWrites builds a VM over those writes, counts the marker raises, and runs
// the script to completion.
func runWrites(t *testing.T, writes [][2]int32) int {
	t.Helper()
	vm := NewVM(writeProgram(t, writes))
	raises := 0
	vm.BindScriptTouched(func() { raises++ })
	if !vm.StartByName("Probe", nil) {
		t.Fatal("probe script did not start")
	}
	for i := 0; i < 8 && vm.ActiveThreadCount() > 0; i++ {
		vm.Drain(0)
	}
	if vm.ActiveThreadCount() != 0 {
		t.Fatal("probe script did not run to completion")
	}
	return raises
}

// TestEngineWriteRaisesTheMarkerFromEveryWriteArm locks the six bound arms
// [04 R-COB-06]: each of ports 1, 5, 6, 18, 19 and 20 raises the marker in
// addition to its own effect, so a script that writes all six raises it six
// times. The count matters as much as the presence — the raise is per
// execution, not a latch the first write sets.
func TestEngineWriteRaisesTheMarkerFromEveryWriteArm(t *testing.T) {
	arms := []int32{1, 5, 6, 18, 19, 20}
	for _, port := range arms {
		port := port
		vm := NewVM(writeProgram(t, [][2]int32{{port, 1}}))
		raises := 0
		vm.BindScriptTouched(func() { raises++ })
		// Bind the arm so the write actually dispatches into a handler: the
		// marker must be raised alongside the port's own effect, not only when
		// the port is unbound.
		effects := 0
		vm.BindPort(Port(port), func([]int32) int32 { effects++; return 0 })
		if !vm.StartByName("Probe", nil) {
			t.Fatalf("port %d: probe script did not start", port)
		}
		for i := 0; i < 8 && vm.ActiveThreadCount() > 0; i++ {
			vm.Drain(0)
		}
		if effects != 1 {
			t.Fatalf("port %d: the write arm ran %d times, want 1", port, effects)
		}
		if raises != 1 {
			t.Fatalf("port %d: marker raised %d times, want 1 — every write arm raises it in addition to its own effect [04 R-COB-06]", port, raises)
		}
	}

	if got := runWrites(t, [][2]int32{{1, 1}, {5, 1}, {6, 0}, {18, 1}, {19, 1}, {20, 0}}); got != len(arms) {
		t.Fatalf("six write arms raised the marker %d times, want %d [04 R-COB-06]", got, len(arms))
	}
}

// TestEngineWriteRaisesTheMarkerFromTheFallThrough is the trap this unit exists
// to avoid. An identifier with no write arm — ports 2, 3, 4 and 17 are valid
// engine ports whose writes retail ignores — still takes the dispatch's
// fall-through, and that fall-through raises the marker too. So does an
// identifier the range check rejects outright: 0, 21, a large positive and a
// negative all land on the same arm [04 R-COB-06].
func TestEngineWriteRaisesTheMarkerFromTheFallThrough(t *testing.T) {
	noWriteArm := [][2]int32{{2, 3}, {3, 1}, {4, 99}, {17, 50}}
	if got := runWrites(t, noWriteArm); got != len(noWriteArm) {
		t.Fatalf("valid ports with no write arm raised the marker %d times, want %d [04 R-COB-06]", got, len(noWriteArm))
	}

	outOfRange := [][2]int32{{0, 1}, {21, 1}, {1000, 1}, {-1, 1}, {-99999, 7}}
	if got := runWrites(t, outOfRange); got != len(outOfRange) {
		t.Fatalf("identifiers outside 1..20 raised the marker %d times, want %d — the range check sends them to the same fall-through [04 R-COB-06]", got, len(outOfRange))
	}
}

// TestEngineWriteMarkerIgnoresTheValue locks the "carries no value" half: a
// write of zero raises the marker exactly as a write of one does, because the
// marker records that a write happened and nothing about what it wrote. This is
// what makes `set BUSY to 0` wake an INBUILDSTANCE wait [04 R-COB-06].
func TestEngineWriteMarkerIgnoresTheValue(t *testing.T) {
	if got := runWrites(t, [][2]int32{{5, 0}, {5, 0}, {5, 0}}); got != 3 {
		t.Fatalf("three zero-valued writes raised the marker %d times, want 3 [04 R-COB-06]", got)
	}
}

// TestOwnerlessVMDropsTheMarker: a VM built without an owning unit (a fixture,
// or the generic asset-binding seam) has no order-event word to write. The
// engine write must still execute normally.
func TestOwnerlessVMDropsTheMarker(t *testing.T) {
	vm := NewVM(writeProgram(t, [][2]int32{{5, 1}, {99, 1}}))
	effects := 0
	vm.BindPort(Port(5), func([]int32) int32 { effects++; return 0 })
	if !vm.StartByName("Probe", nil) {
		t.Fatal("probe script did not start")
	}
	for i := 0; i < 8 && vm.ActiveThreadCount() > 0; i++ {
		vm.Drain(0)
	}
	if effects != 1 {
		t.Fatalf("port 5 effect ran %d times, want 1 on an ownerless VM", effects)
	}
}
