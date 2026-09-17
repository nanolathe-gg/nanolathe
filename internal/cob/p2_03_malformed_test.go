package cob

import "testing"

func TestStackOverflowAtPhysicalWindowKillsThread(t *testing.T) {
	// The host must stop malformed code at the 32-word record boundary. The
	// native consequence beyond that record is unknown, so this locks only the
	// safe host boundary.
	code := make([]uint32, 0, 2*threadWindowWords+3)
	for i := 0; i <= threadWindowWords; i++ {
		code = append(code, 0x10021001, 1)
	}
	code = append(code, 0x10065000)
	prog := synthProg(code, []string{"base"}, 0, []int{0})
	vm := NewVM(prog)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadIdle {
		t.Fatalf("stack overflow should kill thread, status %d", vm.Threads[0].Status)
	}
	if vm.Threads[0].SP > threadWindowWords {
		t.Fatalf("SP overflow %d >%d", vm.Threads[0].SP, threadWindowWords)
	}
}

func TestBadPieceIndexKillsThread(t *testing.T) {
	// Move with piece 99 where pieces len is 2 should kill thread [P1-11] §2.4
	prog := synthProg([]uint32{
		0x10021001, 1, // speed
		0x10021001, 2, // target
		0x10001000, 99, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := NewVM(prog)
	// Ensure Pieces length 2 via program's piece table (synthProg with 0 pieces gives 0; we need explicit)
	// synthProg second arg is pieces count? Check helper: pieces param used to build piece names
	// For test, we set Pieces via prog.Pieces length directly by constructing program with 2 pieces
	prog.Pieces = []string{"p0", "p1"}
	vm.SetProgram(prog)
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadIdle {
		t.Fatalf("bad piece should kill thread, status %d", vm.Threads[0].Status)
	}
}

func TestCorruptSaveOffsetBoundsVsRetail(t *testing.T) {
	// P2-03 corrupt save box: header offset bounds-check vs retail no-check
	// Nanolathe rejects out-of-bounds offset with error (I11 divergence),
	// retail would read unsafe/truncate — explicit fallback not crash.
	header := make([]byte, 44)
	// version 4 little endian
	header[0] = 4
	// numScripts 1, numPieces 1, codeLen 4 (one instruction)
	header[4] = 1
	header[8] = 1
	header[12] = 4
	// offsets: code at 44, script indexes at 60, names etc beyond
	// we will corrupt code offset to 0xFFFFFF to trigger bounds check
	corrupt := append([]byte(nil), header...)
	// Set OffScriptCode (0x24) to 0xFFFFFF (beyond file)
	corrupt[0x24] = 0xFF
	corrupt[0x25] = 0xFF
	corrupt[0x26] = 0xFF
	corrupt[0x27] = 0x0F
	_, err := Load(append(corrupt, make([]byte, 20)...))
	if err == nil {
		t.Fatalf("corrupt code offset should be rejected [P2-03] fallback, not crash")
	}
	// Truncated header (<44) also rejected without panic
	if _, err := Load([]byte{0, 0}); err == nil {
		t.Fatalf("truncated header should be rejected [P2-03]")
	}
}

// A callback that faults every tick used to append to the diagnostic buffer
// forever: nothing in a battle drains it, so a long game grew it by tens of
// megabytes. The buffer keeps the first entries — those carry the fault — and
// counts the rest, and the steady state allocates nothing [P2-03].
func TestFallbackDiagnosticsStopGrowing(t *testing.T) {
	// Divide by zero, the cheapest repeatable fallback.
	prog := synthProg([]uint32{
		0x10021001, 10,
		0x10021001, 0,
		0x10034000,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := NewVM(prog)
	faults := maxDiagnostics * 4
	for i := 0; i < faults; i++ {
		vm.Threads[0].Status = ThreadRunning
		vm.Threads[0].PC = 0
		vm.Threads[0].SP = 0
		vm.Drain(1)
	}
	if len(vm.Diagnostics()) != maxDiagnostics {
		t.Fatalf("buffer holds %d entries after %d faults, want the %d cap", len(vm.Diagnostics()), faults, maxDiagnostics)
	}
	if vm.DroppedDiagnostics() == 0 {
		t.Fatal("the discarded faults were not counted")
	}
	if int(vm.DroppedDiagnostics())+len(vm.Diagnostics()) != faults {
		t.Fatalf("kept %d + dropped %d, want %d faults accounted for",
			len(vm.Diagnostics()), vm.DroppedDiagnostics(), faults)
	}
	// Past the cap the recorder must not allocate.
	if n := testing.AllocsPerRun(100, func() { vm.recordDiagnostic("cob: divide by zero or overflow") }); n != 0 {
		t.Fatalf("a dropped diagnostic allocated %v times per call", n)
	}
	vm.ClearDiagnostics()
	if len(vm.Diagnostics()) != 0 || vm.DroppedDiagnostics() != 0 {
		t.Fatalf("clear left %d entries and %d dropped", len(vm.Diagnostics()), vm.DroppedDiagnostics())
	}
}
