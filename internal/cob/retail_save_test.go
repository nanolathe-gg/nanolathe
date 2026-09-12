package cob

import (
	"encoding/binary"
	"testing"
)

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

// TestRetailScriptImagePieceRecordsRoundTrip locks the piece tail against its
// own reader: the six per-axis animation words, the translation lane and the
// angle accumulator survive, the continuous-spin marker survives as a spin
// rather than a turn, and each piece's independent draw/cache/shade flags come back [08 R-SAVE-02 §9].
func TestRetailScriptImagePieceRecordsRoundTrip(t *testing.T) {
	prog := &Program{Pieces: []string{"base", "turret"}, SourceChecksum: 0xabcd1234}
	vm := NewVM(prog)
	vm.anims[0].axes[1] = axisAnim{moveTarget: 0x30000, moveSpeed: 0x1000, moveBusy: true, turnTarget: 0x4000, turnSpeed: 0x200, turnBusy: true}
	vm.anims[1].axes[2] = axisAnim{spinActive: true, turnSpeed: 0x700, spinTarget: 0x800, spinAccel: 0x900}
	vm.Pieces[1].SetTrans(1, fixedFromRaw(-0x24000))
	vm.Pieces[1].SetAngle(2, 0xbeef)
	vm.pieceFlags[0] = 0x07
	vm.pieceFlags[1] = 0x00 // hidden, uncached and unshaded

	image, err := RetailScriptImage(vm)
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
	if got := copyVM.anims[1].axes[2]; !got.spinActive || got.turnSpeed != 0x700 || got.spinTarget != 0x800 || got.spinAccel != 0x900 {
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
	for p, flags := range copyVM.pieceFlags {
		if flags&0x07 != vm.pieceFlags[p]&0x07 {
			t.Fatalf("piece %d flags %#x, want %#x", p, flags, vm.pieceFlags[p])
		}
	}
}

// The serializer must read the live binding in COB order. Hidden and uncached
// pieces vary independently and must not be replaced with creation defaults
// [08 R-SAVE-02 §9]. The fixture's local VM flags deliberately disagree.
func TestRetailScriptImagePreservesBoundPieceFlags(t *testing.T) {
	prog := &Program{Pieces: []string{"body", "flash", "barrel"}}
	vm := NewVM(prog)
	flags := []uint8{0x07, 0x04, 0x01}
	vm.BindRenderFlagHandlers(func() []uint8 { return flags }, nil)
	image, err := RetailScriptImage(vm)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range [][3]uint32{{1, 1, 1}, {0, 0, 1}, {1, 0, 0}} {
		for bit, value := range want {
			got := binary.LittleEndian.Uint32(image[ScriptSnapshotSize+p*ScriptPieceSize+(24+bit)*4:])
			if got != value {
				t.Fatalf("piece %d flag %d = %d, want %d", p, bit, got, value)
			}
		}
	}
	copyVM := NewVM(prog)
	if err := RetailScriptRestore(copyVM, image); err != nil {
		t.Fatal(err)
	}
	for p, want := range flags {
		if got := copyVM.pieceFlags[p] & 0x07; got != want {
			t.Fatalf("piece %d flags = %#x, want %#x", p, got, want)
		}
	}
}

func TestRetailScriptRestorePieceFlagsUseLowBit(t *testing.T) {
	prog := &Program{Pieces: []string{"piece"}}
	image, err := RetailScriptImage(NewVM(prog))
	if err != nil {
		t.Fatal(err)
	}
	// Noncanonical words distinguish the adapter's low-bit setters from a
	// nonzero test [08 R-SAVE-02 §9].
	for bit, value := range []uint32{2, 3, 2} {
		binary.LittleEndian.PutUint32(image[ScriptSnapshotSize+(24+bit)*4:], value)
	}
	vm := NewVM(prog)
	if err := RetailScriptRestore(vm, image); err != nil {
		t.Fatal(err)
	}
	if got := vm.pieceFlags[0] & 0x07; got != 0x02 {
		t.Fatalf("restored flags = %#x, want cache only", got)
	}
}
