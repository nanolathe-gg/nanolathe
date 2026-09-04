package cob

import (
	"encoding/binary"
	"fmt"
	"math"
)

// RetailScriptImage returns a detached Script%i image for VMs whose complete
// writer mapping is represented by the runtime. The 0x528 VM snapshot and
// statics are established and lossless [08 R-SAVE-02 §9].
func RetailScriptImage(v *VM) ([]byte, error) {
	if v == nil || v.prog == nil {
		return nil, fmt.Errorf("cob: retail save: nil VM or program")
	}
	// TODO(question): what VALUE dwords 24 and 25 of each 0x6C piece record
	// hold. The mechanism is now Established [08 R-SAVE-02 §9] (sharpened
	// 2026-09-04, WU-19-155; the marker here previously said two getter results
	// were "lost to writer stack residue" without saying which, and claimed the
	// VM had lost scratch words it never held).
	//
	// The writer fills one 27-dword stack buffer per piece and calls the three
	// piece-level getters FIRST. The first two land in the SAME frame slot —
	// dword 23 — so the second overwrites the first, and the axis loop then
	// overwrites dword 23 a third time with axis 2's get-angle result. Only the
	// third getter reaches the file, in dword 26. Dwords 24 and 25 are never
	// written by the writer at all: they are uninitialized frame residue, the
	// same two words for every piece of one call, and the reader installs them
	// through the show/hide and cache setters on load. So the two lost getters
	// are the show/hide and cache ones and the shade getter is the survivor —
	// which closes doc 08's "which getter was lost is not [Established]"
	// residual and leaves only the residue's value.
	//
	// That value is not a function of game state; it is whatever the previous
	// call left on retail's stack, so there is nothing to clone and nothing to
	// derive. What Nanolathe writes there is a save-format policy decision, not
	// a research one, and it needs a seam this package does not own — the
	// pattern already used for exactly this problem one record over is
	// units.RetailUnitWriterScratch, supplied by the caller through
	// session.RetailSaveInputs. Until that seam exists a piece-bearing program
	// refuses rather than inventing bytes, which means every real unit refuses,
	// since every real COB names pieces.
	if len(v.prog.Pieces) != 0 {
		return nil, fmt.Errorf("cob: retail save: piece record writer scratch is not represented")
	}
	image := make([]byte, ScriptSnapshotSize+len(v.statics)*4)
	binary.LittleEndian.PutUint32(image, v.prog.SourceChecksum)
	for i := range v.Threads {
		off := 4 + i*0xa4
		raw, err := encodeRetailThreadStatus(v.Threads[i].Status)
		if err != nil {
			return nil, fmt.Errorf("cob: retail save: thread %d: %w", i, err)
		}
		t := &v.Threads[i]
		if t.SP < 0 || t.SP > len(t.Stack) {
			return nil, fmt.Errorf("cob: retail save: thread %d stack count %d outside 0..32", i, t.SP)
		}
		if !retailInt32(t.PC) || !retailInt32(t.WaitPiece) || !retailInt32(t.WaitAxis) || !retailInt32(t.WaitThread) {
			return nil, fmt.Errorf("cob: retail save: thread %d contains a word outside signed 32-bit", i)
		}
		binary.LittleEndian.PutUint32(image[off:], raw)
		putCOBI32(image[off+4:], int32(t.PC))
		putCOBI32(image[off+8:], int32(t.SP-1))
		putCOBI32(image[off+0x0c:], t.Sleep)
		putCOBI32(image[off+0x10:], int32(t.WaitPiece))
		putCOBI32(image[off+0x14:], int32(t.WaitAxis))
		putCOBI32(image[off+0x18:], int32(t.WaitThread))
		putCOBI32(image[off+0x1c:], t.SignalMask)
		// The native completion receiver at +0x20 is always zero on write.
		for j := range t.Stack {
			putCOBI32(image[off+0x24+j*4:], t.Stack[j])
		}
	}
	if v.activeThreadCount > 8 {
		return nil, fmt.Errorf("cob: retail save: active thread count %d exceeds 8", v.activeThreadCount)
	}
	binary.LittleEndian.PutUint32(image[0x524:], v.activeThreadCount)
	for i, value := range v.statics {
		putCOBI32(image[ScriptSnapshotSize+i*4:], value)
	}
	return image, nil
}

func encodeRetailThreadStatus(status int) (uint32, error) {
	switch status {
	case ThreadIdle:
		return 0, nil
	case ThreadRunning:
		return 0x01000000, nil
	case ThreadWaitTurn:
		return 0x02100000, nil
	case ThreadWaitMove:
		return 0x02200000, nil
	case ThreadSleeping:
		return 0x02400000, nil
	case ThreadWaitCall:
		return 0x02800000, nil
	default:
		return 0, fmt.Errorf("unknown logical status %d", status)
	}
}

func retailInt32(value int) bool {
	return int64(value) >= math.MinInt32 && int64(value) <= math.MaxInt32
}
func putCOBI32(dst []byte, value int32) { binary.LittleEndian.PutUint32(dst, uint32(value)) }
