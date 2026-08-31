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
	// TODO(question): two of the three per-piece flag getter results are lost
	// to writer stack residue before the 0x6C record is emitted. The VM does not
	// retain those scratch words, so emitting a value would invent retail bytes.
	// A byte-level writer trace or a captured save at this call site must supply
	// the residue policy before piece-bearing Script images can be written.
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
