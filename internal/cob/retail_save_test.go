package cob

import "testing"

func TestRetailScriptImageRoundTripSnapshotAndStatics(t *testing.T) {
	prog := &Program{Statics: 1, SourceChecksum: 0x12345678}
	vm := NewVM(prog)
	vm.Threads[0] = Thread{Status: ThreadSleeping, PC: 7, SP: 2, Sleep: 11, WaitPiece: 3, WaitAxis: 1, WaitThread: -1, SignalMask: 0x55, Stack: [32]int32{17, 19}}
	vm.statics[0] = 23
	vm.activeThreadCount = 1
	image, err := RetailScriptImage(vm, RetailScriptWriterScratch{})
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

// TestRetailScriptImagePieceRecordsRoundTrip locks the piece tail against its
// own reader: the six per-axis animation words, the translation lane and the
// angle accumulator survive, the continuous-spin marker survives as a spin
// rather than a turn, and the shade bit — the one piece flag the writer's
// surviving getter reaches — comes back [08 R-SAVE-02 §9].
func TestRetailScriptImagePieceRecordsRoundTrip(t *testing.T) {
	prog := &Program{Pieces: []string{"base", "turret"}, SourceChecksum: 0xabcd1234}
	vm := NewVM(prog)
	vm.anims[0].axes[1] = axisAnim{moveTarget: 0x30000, moveSpeed: 0x1000, moveBusy: true, turnTarget: 0x4000, turnSpeed: 0x200, turnBusy: true}
	vm.anims[1].axes[2] = axisAnim{spinActive: true, spinSpeed: 0x700, spinTarget: 0x800, spinAccel: 0x900}
	vm.Pieces[1].SetTrans(1, fixedFromRaw(-0x24000))
	vm.Pieces[1].SetAngle(2, 0xbeef)
	vm.pieceFlags[0] = 0x07
	vm.pieceFlags[1] = 0x03 // shade cleared on piece 1

	image, err := RetailScriptImage(vm, RetailScriptWriterScratch{PieceDword24: 1, PieceDword25: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := ScriptSnapshotSize + 2*ScriptPieceSize; len(image) != want {
		t.Fatalf("image is %d bytes, want %d", len(image), want)
	}
	copyVM := NewVM(prog)
	if err := RetailScriptRestore(copyVM, image); err != nil {
		t.Fatal(err)
	}
	if got := copyVM.anims[0].axes[1]; got.moveTarget != 0x30000 || got.moveSpeed != 0x1000 || !got.moveBusy || got.turnTarget != 0x4000 || got.turnSpeed != 0x200 || !got.turnBusy || got.spinActive {
		t.Fatalf("piece 0 axis 1 animation mismatch: %#v", got)
	}
	if got := copyVM.anims[1].axes[2]; !got.spinActive || got.spinSpeed != 0x700 || got.spinTarget != 0x800 || got.spinAccel != 0x900 {
		t.Fatalf("piece 1 axis 2 spin mismatch: %#v", got)
	}
	if got := copyVM.Pieces[1].GetTrans(1); int64(got.Raw()) != -0x24000 {
		t.Fatalf("piece 1 translation %d, want %d", int64(got.Raw()), -0x24000)
	}
	if got := copyVM.Pieces[1].GetAngle(2); got != 0xbeef {
		t.Fatalf("piece 1 angle %#x, want 0xbeef", got)
	}
	if copyVM.pieceFlags[0]&0x04 == 0 || copyVM.pieceFlags[1]&0x04 != 0 {
		t.Fatalf("shade bit not carried: %#x %#x", copyVM.pieceFlags[0], copyVM.pieceFlags[1])
	}
	// The two residue dwords are the caller's policy, not runtime state: the
	// reader installs whatever the writer supplied through the draw and cache
	// setters, so the pair chosen here is what comes back for every piece.
	for p, flags := range copyVM.pieceFlags {
		if flags&(RetailScriptPieceDrawBit|RetailScriptPieceCacheBit) != RetailScriptPieceDrawBit|RetailScriptPieceCacheBit {
			t.Fatalf("piece %d draw/cache bits %#x, want the supplied residue pair", p, flags)
		}
	}
}

// TestRetailScriptImageResidueClearsDrawBit is the reason the shell does not
// leave the scratch at its Go zero value: a zero residue pair is a legal
// image, and it makes every piece of every restored unit undrawn
// [08 R-SAVE-02 §9] [04 §"Piece flag polarity"].
func TestRetailScriptImageResidueClearsDrawBit(t *testing.T) {
	prog := &Program{Pieces: []string{"base"}}
	vm := NewVM(prog)
	vm.pieceFlags[0] = 0x07
	image, err := RetailScriptImage(vm, RetailScriptWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	copyVM := NewVM(prog)
	copyVM.pieceFlags[0] = 0x07
	if err := RetailScriptRestore(copyVM, image); err != nil {
		t.Fatal(err)
	}
	if copyVM.pieceFlags[0]&RetailScriptPieceDrawBit != 0 {
		t.Fatalf("zero residue left the draw bit set: %#x", copyVM.pieceFlags[0])
	}
}
