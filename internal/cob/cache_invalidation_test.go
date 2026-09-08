package cob

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func cacheTestVM(code []uint32) *VM {
	return NewVM(&Program{Code: code, Pieces: []string{"base"}, Scripts: map[string]int{"Create": 0}, ScriptsByID: []int{0}, SourceChecksum: 1})
}

func TestCacheInvalidationGatesCachedPieceMutations(t *testing.T) {
	vm := cacheTestVM(nil)
	if vm.CacheRevision() != 0 {
		t.Fatalf("initial cache revision = %d, want 0", vm.CacheRevision())
	}
	if vm.setPieceTrans(0, 0, 0) {
		t.Fatal("unchanged translation reported a write")
	}
	if vm.CacheRevision() != 0 {
		t.Fatal("unchanged cached translation invalidated")
	}
	vm.setPieceTrans(0, 0, numeric.Fixed(7))
	if vm.CacheRevision() != 1 {
		t.Fatalf("changed cached translation revision = %d, want 1", vm.CacheRevision())
	}
	vm.setPieceAngle(0, 1, 9)
	if vm.CacheRevision() != 2 {
		t.Fatalf("changed cached angle revision = %d, want 2", vm.CacheRevision())
	}
	vm.pieceFlags[0] &^= 0x02
	vm.setPieceTrans(0, 0, numeric.Fixed(8))
	vm.setPieceAngle(0, 1, 10)
	vm.markAnimationDirty(0)
	if vm.CacheRevision() != 2 {
		t.Fatalf("live pose or busy marker revision = %d, want 2", vm.CacheRevision())
	}
}

func TestCacheInvalidationFlagSetterRules(t *testing.T) {
	vm := cacheTestVM(nil)
	vm.pieceFlags[0] |= 0x01 // geometry draw default is unit-owned in production
	if !vm.setRenderFlag(0, 0x01, false) || vm.CacheRevision() != 1 {
		t.Fatalf("changed cached hide revision = %d, want 1", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x01, false) || vm.CacheRevision() != 1 {
		t.Fatalf("unchanged hide revision = %d, want 1", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x01, true) || vm.CacheRevision() != 2 {
		t.Fatalf("changed cached show revision = %d, want 2", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x01, true) || vm.CacheRevision() != 2 {
		t.Fatalf("unchanged show revision = %d, want 2", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x02, true) || vm.CacheRevision() != 3 {
		t.Fatalf("unchanged cache revision = %d, want 3", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x02, true) || vm.CacheRevision() != 4 {
		t.Fatalf("repeated cache revision = %d, want 4", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x04, true) || vm.CacheRevision() != 5 {
		t.Fatalf("unchanged shade revision = %d, want 5", vm.CacheRevision())
	}
	if !vm.setRenderFlag(0, 0x04, true) || vm.CacheRevision() != 6 {
		t.Fatalf("repeated shade revision = %d, want 6", vm.CacheRevision())
	}
}

func TestCacheInvalidationImmediateAndInterpolatedPoseWriters(t *testing.T) {
	vm := cacheTestVM([]uint32{
		0x1000b000, 0, 0, 0x10065000, // move-now base, X
		0x10001000, 0, 0, 0x10065000, // move base, X
	})
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 0
	vm.Threads[0].stackPush(9)
	vm.Drain(1)
	if got := vm.CacheRevision(); got != 1 {
		t.Fatalf("immediate changed pose revision = %d, want 1", got)
	}
	vm.Threads[0].Status = ThreadRunning
	vm.Threads[0].PC = 4
	vm.Threads[0].stackPush(30) // speed: one raw unit per tick
	vm.Threads[0].stackPush(12) // target
	vm.Drain(1)
	if got := vm.CacheRevision(); got != 2 {
		t.Fatalf("interpolated changed pose revision = %d, want 2", got)
	}
}

func TestRetailRestoreInvalidatesPresentationWithoutPersistingRevision(t *testing.T) {
	vm := cacheTestVM(nil)
	image := make([]byte, ScriptSnapshotSize+ScriptPieceSize)
	binary.LittleEndian.PutUint32(image, vm.Program().SourceChecksum)
	before := vm.CacheRevision()
	if err := RetailScriptRestore(vm, image); err != nil {
		t.Fatal(err)
	}
	if got := vm.CacheRevision(); got != before+1 || vm.CacheValidityRevision() != 1 {
		t.Fatalf("restore cache revision = %d, want %d", got, before+1)
	}
}

func TestCacheInvalidationCausesSurviveBetweenPublications(t *testing.T) {
	vm := cacheTestVM(nil)
	vm.setRenderFlag(0, 0x02, false)
	if vm.CacheRevision() != 1 || vm.CacheValidityRevision() != 0 {
		t.Fatal("dont-cache cleared validity instead of only discarding image")
	}
	vm.setPieceTrans(0, 0, 7)
	vm.setRenderFlag(0, 0x02, true)
	if vm.CacheRevision() != 2 || vm.CacheValidityRevision() != 0 {
		t.Fatal("live move plus cache did not retain valid-body fallback")
	}
	vm.setPieceTrans(0, 0, 8)
	vm.setRenderFlag(0, 0x02, false)
	if vm.CacheRevision() != 4 || vm.CacheValidityRevision() != 1 {
		t.Fatal("cached move invalidation was lost behind later image discard")
	}
	before := vm.CacheValidityRevision()
	vm.setRenderFlag(0, 0x04, true)
	vm.setRenderFlag(0, 0x04, true)
	if vm.CacheValidityRevision() != before {
		t.Fatal("shade setters cleared validity")
	}
}

func TestProgramRebindInvalidatesBothPresentationLanes(t *testing.T) {
	vm := cacheTestVM(nil)
	if err := vm.SetProgramChecked(vm.Program()); err != nil {
		t.Fatal(err)
	}
	if vm.CacheRevision() != 1 || vm.CacheValidityRevision() != 1 {
		t.Fatal("successful rebind retained stale image state")
	}
	before, validity := vm.CacheRevision(), vm.CacheValidityRevision()
	if err := vm.SetProgramChecked(&Program{Statics: -1}); err == nil {
		t.Fatal("invalid program accepted")
	}
	if vm.CacheRevision() != before || vm.CacheValidityRevision() != validity {
		t.Fatal("failed rebind changed presentation state")
	}
}
