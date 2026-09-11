package cob

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/model"
)

// RetailScriptRestore restores one exact Script%i image into an already bound
// VM. The image is validated before any VM field is changed. The program
// signature is compared first and the aggregate size is derived from the
// bound program, as required by [08 R-SAVE-02 §9].
//
// The image is staged completely before mutating v. The native completion
// receiver at record offset 0x20 is intentionally not restored [08 R-SAVE-02 §9].
func RetailScriptRestore(v *VM, image []byte) error {
	if v == nil || v.Program() == nil {
		return fmt.Errorf("cob: retail script restore: nil VM or program")
	}
	prog := v.Program()
	if err := ValidateProgram(prog); err != nil {
		return fmt.Errorf("cob: retail script restore: %w", err)
	}
	want := ScriptSnapshotSize + prog.Statics*4 + len(prog.Pieces)*ScriptPieceSize
	if len(image) != want {
		return fmt.Errorf("cob: retail script restore: image size %d, want %d", len(image), want)
	}
	if binary.LittleEndian.Uint32(image[:4]) != prog.SourceChecksum {
		return fmt.Errorf("cob: retail script restore: program signature mismatch")
	}
	var threads [8]Thread
	for i := range threads {
		off := 4 + i*0xA4
		t := &threads[i]
		rawStatus := binary.LittleEndian.Uint32(image[off:])
		status, err := decodeRetailThreadStatus(rawStatus)
		if err != nil {
			return fmt.Errorf("cob: retail script restore: thread %d: %w", i, err)
		}
		t.Status = status
		t.PC = int(int32(binary.LittleEndian.Uint32(image[off+4:])))
		wireTop := int64(int32(binary.LittleEndian.Uint32(image[off+8:])))
		if wireTop < -1 || wireTop > 31 {
			return fmt.Errorf("cob: retail script restore: thread %d: logical top %d outside -1..31", i, wireTop)
		}
		t.SP = int(wireTop + 1)
		t.Sleep = int32(binary.LittleEndian.Uint32(image[off+12:]))
		t.WaitPiece = int(int32(binary.LittleEndian.Uint32(image[off+16:])))
		t.WaitAxis = int(int32(binary.LittleEndian.Uint32(image[off+20:])))
		t.WaitThread = int(int32(binary.LittleEndian.Uint32(image[off+0x18:])))
		t.SignalMask = int32(binary.LittleEndian.Uint32(image[off+0x1c:]))
		// The native completion receiver at +0x20 is deliberately not restored.
		for j := range t.Stack {
			t.Stack[j] = int32(binary.LittleEndian.Uint32(image[off+0x24+j*4:]))
		}
	}
	active := binary.LittleEndian.Uint32(image[0x524:])
	if active > 8 {
		return fmt.Errorf("cob: retail script restore: active thread count %d", active)
	}
	statics := make([]int32, prog.Statics)
	for i := range statics {
		statics[i] = int32(binary.LittleEndian.Uint32(image[ScriptSnapshotSize+i*4:]))
	}
	type pieceRestore struct {
		anim  pieceAnim
		state model.PieceState
		flags [3]uint32
	}
	pieces := make([]pieceRestore, len(prog.Pieces))
	dirty := true // successful load forces one interpolator pass [08 R-SAVE-02 §9]
	staticOff := ScriptSnapshotSize + prog.Statics*4
	for p := range pieces {
		off := staticOff + p*ScriptPieceSize
		for axis := 0; axis < 3; axis++ {
			d := func(n int) int32 { return int32(binary.LittleEndian.Uint32(image[off+n*4:])) }
			turnMarker := d(6 + axis)
			anim := axisAnim{
				moveTarget: d(axis), moveSpeed: d(3 + axis), moveBusy: d(3+axis) != 0,
				turnSpeed: d(9 + axis), spinTarget: d(12 + axis), spinAccel: d(15 + axis),
			}
			if turnMarker == -1 {
				// 0xffffffff is the continuous-spin marker. The saved turn-speed
				// word is the current spin speed; it is not a uint16 target [04 §4.6].
				anim.spinActive = true
			} else {
				anim.turnTarget = uint16(turnMarker)
				anim.turnBusy = anim.turnSpeed != 0
			}
			pieces[p].anim.axes[axis] = anim
			pieces[p].state.SetTrans(axis, fixedFromRaw(int64(d(18+axis))))
			pieces[p].state.SetAngle(axis, uint16(d(21+axis)))
		}
		pieces[p].flags = [3]uint32{
			binary.LittleEndian.Uint32(image[off+24*4:]),
			binary.LittleEndian.Uint32(image[off+25*4:]),
			binary.LittleEndian.Uint32(image[off+26*4:]),
		}
	}

	copy(v.Threads[:], threads[:])
	for i := range threads {
		// A restored thread is a fresh VM-private allocation, not a
		// continuation of whatever occupied the slot before the load:
		// claimThread both stamps the identity ThreadAliveAs answers for and
		// clears the previous occupant's completion receiver and unconsumed
		// return, matching the receiver's own non-restoration above
		// [08 R-SAVE-02 §9].
		v.claimThread(i)
	}
	copy(v.statics, statics)
	v.activeThreadCount = active
	v.dirty = dirty
	for p := range pieces {
		v.anims[p] = pieces[p].anim
		if p < len(v.pieceBusy) {
			// Retail marks every piece busy on a successful load. The next
			// interpolation pass reduces this to the lanes that remain active;
			// this also gives zero-speed lanes their required one-pass wake [04 §4.6].
			v.pieceBusy[p] = true
		}
		v.Pieces[p].SetTrans(model.AxisX, pieces[p].state.Trans[model.AxisX])
		v.Pieces[p].SetTrans(model.AxisY, pieces[p].state.Trans[model.AxisY])
		v.Pieces[p].SetTrans(model.AxisZ, pieces[p].state.Trans[model.AxisZ])
		v.Pieces[p].SetAngle(model.AxisX, pieces[p].state.RotX)
		v.Pieces[p].SetAngle(model.AxisY, pieces[p].state.RotY)
		v.Pieces[p].SetAngle(model.AxisZ, pieces[p].state.RotZ)
		setRetailPieceFlag(v, p, 0x01, pieces[p].flags[0] != 0)
		setRetailPieceFlag(v, p, 0x02, pieces[p].flags[1] != 0)
		setRetailPieceFlag(v, p, 0x04, pieces[p].flags[2] != 0)
	}
	// The image reference is not serialized. A successful restore gives the
	// presentation cache fresh state to rebuild without treating this Go
	// revision as a retail script field [03 R-COMP-01 §4][08 R-SAVE-02 §9].
	v.invalidateCacheValidity()
	return nil
}

func decodeRetailThreadStatus(raw uint32) (int, error) {
	switch raw {
	case 0:
		return ThreadIdle, nil
	case 0x01000000:
		return ThreadRunning, nil
	case 0x02100000:
		return ThreadWaitTurn, nil
	case 0x02200000:
		return ThreadWaitMove, nil
	case 0x02400000:
		return ThreadSleeping, nil
	case 0x02800000:
		return ThreadWaitCall, nil
	default:
		return 0, fmt.Errorf("cob: unknown raw status %#x", raw)
	}
}

const (
	ScriptSnapshotSize = 0x528
	ScriptPieceSize    = 0x6C
)

func setRetailPieceFlag(v *VM, piece int, mask uint8, on bool) {
	if v == nil {
		return
	}
	if v.renderFlagSet != nil {
		_ = v.renderFlagSet(piece, mask, on)
		return
	}
	flags := v.renderPieceFlags()
	if piece < 0 || piece >= len(flags) {
		return
	}
	if on {
		flags[piece] |= mask
	} else {
		flags[piece] &^= mask
	}
}
