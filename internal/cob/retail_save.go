package cob

import (
	"encoding/binary"
	"fmt"
	"math"
)

// RetailScriptImage returns a detached Script%i image: the VM snapshot,
// statics and per-piece animation and draw/cache/shade state [08 R-SAVE-02 §9].
func RetailScriptImage(v *VM) ([]byte, error) {
	if v == nil || v.prog == nil {
		return nil, fmt.Errorf("cob: retail save: nil VM or program")
	}
	image := make([]byte, ScriptSnapshotSize+len(v.statics)*4+len(v.prog.Pieces)*ScriptPieceSize)
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
	if err := writeRetailPieceRecords(v, image[ScriptSnapshotSize+len(v.statics)*4:]); err != nil {
		return nil, err
	}
	return image, nil
}

// writeRetailPieceRecords emits the `0x6C·P` piece tail. It is the exact
// inverse of the reader in RetailScriptRestore: for axis a the dwords at
// `a, 3+a, 6+a, 9+a, 12+a, 15+a` are the six per-axis animation words, `18+a`
// the piece's translation lane and `21+a` its angle accumulator; dwords 24, 25
// and 26 are what the reader feeds to the show/hide, cache and shade setters
// [08 R-SAVE-02 §9] [04 §4.6].
func writeRetailPieceRecords(v *VM, dst []byte) error {
	flags := v.renderPieceFlags()
	for p := range v.prog.Pieces {
		if p >= len(v.anims) || p >= len(v.Pieces) {
			return fmt.Errorf("cob: retail save: piece %d has no runtime record", p)
		}
		off := p * ScriptPieceSize
		for axis := 0; axis < 3; axis++ {
			anim := &v.anims[p].axes[axis]
			// The turn lane and the spin lane share dwords 6+a and 9+a: a
			// continuous spin writes the marker 0xffffffff and its current spin
			// speed, an ordinary turn writes the uint16 target and its turn
			// speed. The reader discriminates on the marker [04 §4.6].
			turnMarker, turnSpeed := int32(anim.turnTarget), anim.turnSpeed
			if anim.spinActive {
				turnMarker = -1
			}
			putCOBI32(dst[off+(0+axis)*4:], anim.moveTarget)
			putCOBI32(dst[off+(3+axis)*4:], anim.moveSpeed)
			putCOBI32(dst[off+(6+axis)*4:], turnMarker)
			putCOBI32(dst[off+(9+axis)*4:], turnSpeed)
			putCOBI32(dst[off+(12+axis)*4:], anim.spinTarget)
			putCOBI32(dst[off+(15+axis)*4:], anim.spinAccel)
			trans := int64(v.Pieces[p].GetTrans(axis).Raw())
			if !retailInt32(int(trans)) {
				return fmt.Errorf("cob: retail save: piece %d axis %d translation is outside signed 32-bit", p, axis)
			}
			putCOBI32(dst[off+(18+axis)*4:], int32(trans))
			putCOBI32(dst[off+(21+axis)*4:], int32(v.Pieces[p].GetAngle(axis)))
		}
		// The three getters persist each piece independently [08 R-SAVE-02 §9].
		// Read through the binding: COB piece order can differ from model order.
		for bit := 0; bit < 3; bit++ {
			value := uint32(0)
			if p < len(flags) {
				value = uint32(flags[p] >> bit & 1)
			}
			binary.LittleEndian.PutUint32(dst[off+(24+bit)*4:], value)
		}
	}
	return nil
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
		return 0, fmt.Errorf("cob: unknown logical status %d", status)
	}
}

func retailInt32(value int) bool {
	return int64(value) >= math.MinInt32 && int64(value) <= math.MaxInt32
}
func putCOBI32(dst []byte, value int32) { binary.LittleEndian.PutUint32(dst, uint32(value)) }
