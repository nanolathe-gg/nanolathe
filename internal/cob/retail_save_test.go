package cob

import "testing"

func TestRetailScriptImageRoundTripSnapshotAndStatics(t *testing.T) {
	prog := &Program{Statics: 1, SourceChecksum: 0x12345678}
	vm := NewVM(prog)
	vm.Threads[0] = Thread{Status: ThreadSleeping, PC: 7, SP: 2, Sleep: 11, WaitPiece: 3, WaitAxis: 1, WaitThread: -1, SignalMask: 0x55, Stack: [32]int32{17, 19}}
	vm.statics[0] = 23
	vm.activeThreadCount = 1
	image, err := RetailScriptImage(vm)
	if err != nil {
		t.Fatal(err)
	}
	copyVM := NewVM(prog)
	if err := RetailScriptRestore(copyVM, image); err != nil {
		t.Fatal(err)
	}
	got := copyVM.Threads[0]
	if got.Status != ThreadSleeping || got.PC != 7 || got.SP != 2 || got.Sleep != 11 || got.WaitThread != -1 || got.Stack[1] != 19 || copyVM.statics[0] != 23 || copyVM.activeThreadCount != 1 {
		t.Fatalf("script round trip mismatch: thread=%#v statics=%v active=%d", got, copyVM.statics, copyVM.activeThreadCount)
	}
}

func TestRetailScriptImageRejectsUnrepresentedPieceWriterScratch(t *testing.T) {
	vm := NewVM(&Program{Pieces: []string{"base"}})
	if _, err := RetailScriptImage(vm); err == nil {
		t.Fatal("piece-bearing script image accepted without writer scratch")
	}
}
