package cob

import (
	"encoding/binary"
	"fmt"
	"math"
)

// RetailScriptWriterScratch carries the two dwords of every 0x6C piece record
// that the retail writer never writes, and that therefore have no runtime
// source to read [08 R-SAVE-02 §9].
//
// The writer fills one 27-dword stack buffer per piece and calls the three
// piece-level getters BEFORE the axis loop. The first two land in the same
// frame slot — dword 23 — so the second overwrites the first, and the axis
// loop then overwrites dword 23 a third time with axis 2's get-angle result.
// Only the third getter reaches the file, in dword 26. Dwords 24 and 25 are
// never assigned at all: they are uninitialized frame residue, the same two
// words for every piece of one call, and the reader installs them through the
// show/hide and cache setters on load. So the two lost getters are the
// show/hide and cache ones; the shade getter is the survivor.
//
// The residue's VALUE is Unknown by construction — it is whatever the previous
// call left in that stack region, not a function of game state — so there is
// nothing to clone. What goes there is a save-format policy decision the caller
// owns, exactly as units.RetailUnitWriterScratch owns the packed status word's
// three unwritten bits. This type is the seam; it invents no retail constant.
type RetailScriptWriterScratch struct {
	// PieceDword24 and PieceDword25 are written into dwords 24 and 25 of every
	// piece record of one image, which is the shape retail's reused frame
	// produces. The reader treats each as a boolean: nonzero sets the piece's
	// draw bit (dword 24) and cache bit (dword 25); zero clears it.
	PieceDword24 uint32
	PieceDword25 uint32
}

// RetailScriptImage returns a detached Script%i image: the 0x528 VM snapshot,
// the statics, and one 0x6C record per piece [08 R-SAVE-02 §9]. scratch
// supplies the two per-piece dwords the retail writer leaves as frame residue;
// everything else in the image is read from the VM.
func RetailScriptImage(v *VM, scratch RetailScriptWriterScratch) ([]byte, error) {
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
	if err := writeRetailPieceRecords(v, scratch, image[ScriptSnapshotSize+len(v.statics)*4:]); err != nil {
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
func writeRetailPieceRecords(v *VM, scratch RetailScriptWriterScratch, dst []byte) error {
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
				turnMarker, turnSpeed = -1, anim.spinSpeed
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
		// Dwords 24 and 25 are the caller's residue policy; only dword 26 has a
		// runtime source, the shade bit the survivor getter reads. The reader
		// tests each for nonzero, so the boolean image of the live bit is what
		// the setter reproduces.
		binary.LittleEndian.PutUint32(dst[off+24*4:], scratch.PieceDword24)
		binary.LittleEndian.PutUint32(dst[off+25*4:], scratch.PieceDword25)
		shade := uint32(0)
		if p < len(flags) && flags[p]&0x04 != 0 {
			shade = 1
		}
		binary.LittleEndian.PutUint32(dst[off+26*4:], shade)
	}
	return nil
}

// RetailScriptPieceDrawBit and RetailScriptPieceCacheBit name the two piece
// flag bits whose live values a retail Script%i box cannot carry: the writer
// leaves the dwords the reader installs them from uninitialized, so the format
// itself loses per-piece show/hide and cache state across a save
// [08 R-SAVE-02 §9] [04 §"Piece flag polarity"].
const (
	RetailScriptPieceDrawBit  = 0x01
	RetailScriptPieceCacheBit = 0x02
)

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
