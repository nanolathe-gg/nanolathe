package cob

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestRetailScriptRestoreRejectsExcessiveExternalStaticsBeforeSizing(t *testing.T) {
	vm := &VM{prog: &Program{Statics: MaxProgramStaticBytes/4 + 1}}
	if err := RetailScriptRestore(vm, nil); err == nil || !strings.Contains(err.Error(), "static storage") {
		t.Fatalf("restore excessive statics error = %v", err)
	}
}

func TestRetailScriptRestoreSignatureAtomicAndFullState(t *testing.T) {
	prog := &Program{Code: []uint32{0x10065000}, Scripts: map[string]int{"Create": 0}, ScriptsByID: []int{0}, Statics: 1, Pieces: []string{"base"}, SourceChecksum: 0x12345678}
	vm := NewVM(prog)
	vm.Threads[0].Status = ThreadRunning
	image := make([]byte, ScriptSnapshotSize+4+ScriptPieceSize)
	binary.LittleEndian.PutUint32(image, 0x87654321)
	off := 4
	binary.LittleEndian.PutUint32(image[off:], 0x02400000) // saved sleeping status
	binary.LittleEndian.PutUint32(image[off+8:], 9)        // wire top 9 => SP count 10
	binary.LittleEndian.PutUint32(image[off+12:], 17)
	binary.LittleEndian.PutUint32(image[off+32:], 0xdeadbeef)   // native receiver; ignored
	binary.LittleEndian.PutUint32(image[off+36:], 0x12345678)   // first portable window word
	binary.LittleEndian.PutUint32(image[off+0xA0:], 0x87654321) // final portable window word
	binary.LittleEndian.PutUint32(image[off+0x18:], uint32(^uint32(0)))
	binary.LittleEndian.PutUint32(image[off+0x1c:], 0x55aa)
	binary.LittleEndian.PutUint32(image[0x524:], 1)
	if err := RetailScriptRestore(vm, image); err == nil {
		t.Fatal("signature mismatch accepted")
	}
	if vm.Threads[0].Status != ThreadRunning || vm.ScriptDirty() {
		t.Fatal("signature rejection mutated VM")
	}
	binary.LittleEndian.PutUint32(image, prog.SourceChecksum)
	binary.LittleEndian.PutUint32(image[ScriptSnapshotSize:], 99)
	piece := ScriptSnapshotSize + 4
	binary.LittleEndian.PutUint32(image[piece+18*4:], 0x00020000)
	binary.LittleEndian.PutUint32(image[piece+21*4:], 1234)
	binary.LittleEndian.PutUint32(image[piece+24*4:], 1)
	// The turn-target word is the continuous-spin marker, even when its
	// current speed is zero; it must not be inferred from target/acceleration.
	binary.LittleEndian.PutUint32(image[piece+6*4:], 0xffffffff)
	if err := RetailScriptRestore(vm, image); err != nil {
		t.Fatal(err)
	}
	if vm.Threads[0].Status != ThreadSleeping || vm.Threads[0].Sleep != 17 || vm.Threads[0].SP != 10 || vm.Threads[0].WaitThread != -1 || vm.Threads[0].SignalMask != 0x55aa || vm.Threads[0].Stack[0] != 0x12345678 || vm.Threads[0].Stack[31] != -2023406815 || vm.ActiveThreadCount() != 1 || !vm.ScriptDirty() {
		t.Fatalf("restored VM state: thread=%#v active=%d dirty=%v", vm.Threads[0], vm.ActiveThreadCount(), vm.ScriptDirty())
	}
	// A restored thread is a first-class VM-private allocation: ThreadAliveAs
	// must answer for it, not just IsThreadAlive.
	if id := vm.ThreadIdentity(0); id == 0 || !vm.ThreadAliveAs(0, id) {
		t.Fatalf("restored thread identity=%d, ThreadAliveAs=%v; want nonzero and alive", id, vm.ThreadAliveAs(0, id))
	}
	if got := vm.Pieces[0].Trans[0]; got != 0x00020000 {
		t.Fatalf("piece translation=%v", got)
	}
	if !vm.anims[0].axes[0].spinActive || vm.anims[0].axes[0].turnSpeed != 0 || !vm.ScriptDirty() || !vm.pieceBusy[0] {
		t.Fatalf("spin marker/dirty not restored: %+v dirty=%v pieceBusy=%v", vm.anims[0].axes[0], vm.ScriptDirty(), vm.pieceBusy[0])
	}
	before := vm.Threads[0]
	binary.LittleEndian.PutUint32(image[off:], 0xdeadbeef)
	if err := RetailScriptRestore(vm, image); err == nil {
		t.Fatal("unknown raw status accepted")
	}
	if vm.Threads[0] != before {
		t.Fatal("unknown raw status rejection mutated thread")
	}
	binary.LittleEndian.PutUint32(image[off:], 0x02400000)
	binary.LittleEndian.PutUint32(image[off+8:], 32)
	if err := RetailScriptRestore(vm, image); err == nil {
		t.Fatal("out-of-range wire top accepted")
	}
	if vm.Threads[0] != before {
		t.Fatal("out-of-range wire top rejection mutated thread")
	}
	image = image[:len(image)-1]
	if err := RetailScriptRestore(vm, image); err == nil {
		t.Fatal("short script image accepted")
	}
}

func TestDecodeRetailThreadStatus(t *testing.T) {
	for _, tc := range []struct {
		raw  uint32
		want int
	}{
		{0, ThreadIdle},
		{0x01000000, ThreadRunning},
		{0x02100000, ThreadWaitTurn},
		{0x02200000, ThreadWaitMove},
		{0x02400000, ThreadSleeping},
		{0x02800000, ThreadWaitCall},
	} {
		got, err := decodeRetailThreadStatus(tc.raw)
		if err != nil || got != tc.want {
			t.Fatalf("raw status %#x -> %d, %v; want %d", tc.raw, got, err, tc.want)
		}
	}
}
