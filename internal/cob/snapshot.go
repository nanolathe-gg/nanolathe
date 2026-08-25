package cob

import "github.com/nanolathe/nanolathe/internal/model"

// Snapshot captures per-unit VM statics and threads for save [P0-I11][04 §4.2].
func (v *VM) Snapshot() (statics []int32, threads [8]Thread) {
	if v == nil {
		return nil, [8]Thread{}
	}
	if v.statics != nil {
		statics = make([]int32, len(v.statics))
		copy(statics, v.statics)
	}
	return statics, v.Threads
}

// RestoreSnapshot restores statics and threads [P0-I11].
func (v *VM) RestoreSnapshot(statics []int32, threads [8]Thread) {
	if v == nil {
		return
	}
	if statics != nil {
		v.statics = make([]int32, len(statics))
		copy(v.statics, statics)
	} else {
		v.statics = nil
	}
	v.Threads = threads
}

// SnapshotStatics returns statics copy for save translation [P0-I11].
func (v *VM) SnapshotStatics() []int32 {
	if v == nil || v.statics == nil {
		return nil
	}
	cp := make([]int32, len(v.statics))
	copy(cp, v.statics)
	return cp
}

// RestoreStatics sets statics [P0-I11].
func (v *VM) RestoreStatics(s []int32) {
	if v == nil {
		return
	}
	if s == nil {
		v.statics = nil
		return
	}
	cp := make([]int32, len(s))
	copy(cp, s)
	v.statics = cp
}

// SnapshotPieces returns a copy of piece transforms for save [04 §4.6][03 §2.4] P1-I01.
func (v *VM) SnapshotPieces() []model.PieceState {
	if v == nil || v.Pieces == nil {
		return nil
	}
	cp := make([]model.PieceState, len(v.Pieces))
	copy(cp, v.Pieces)
	return cp
}

// RestorePieces sets piece transforms [04 §4.6] P1-I01.
func (v *VM) RestorePieces(p []model.PieceState) {
	if v == nil {
		return
	}
	if p == nil {
		v.Pieces = nil
		return
	}
	cp := make([]model.PieceState, len(p))
	copy(cp, p)
	v.Pieces = cp
}

// SnapshotAnims returns anim state for save [04 §4.6] P1-I01.
func (v *VM) SnapshotAnims() []PieceAnimSave {
	if v == nil || v.anims == nil {
		return nil
	}
	out := make([]PieceAnimSave, len(v.anims))
	for i, pa := range v.anims {
		for ax := 0; ax < 3; ax++ {
			a := pa.axes[ax]
			out[i].Axes[ax] = AxisAnimSave{
				MoveTarget: a.moveTarget,
				MoveSpeed:  a.moveSpeed,
				MoveBusy:   a.moveBusy,
				TurnTarget: a.turnTarget,
				TurnSpeed:  a.turnSpeed,
				TurnBusy:   a.turnBusy,
				SpinSpeed:  a.spinSpeed,
				SpinTarget: a.spinTarget,
				SpinAccel:  a.spinAccel,
				SpinActive: a.spinActive,
			}
		}
	}
	return out
}

// RestoreAnims sets anim state [04 §4.6] P1-I01.
func (v *VM) RestoreAnims(a []PieceAnimSave) {
	if v == nil {
		return
	}
	if a == nil {
		v.anims = nil
		return
	}
	v.anims = make([]pieceAnim, len(a))
	for i, pa := range a {
		for ax := 0; ax < 3; ax++ {
			src := pa.Axes[ax]
			v.anims[i].axes[ax] = axisAnim{
				moveTarget: src.MoveTarget,
				moveSpeed:  src.MoveSpeed,
				moveBusy:   src.MoveBusy,
				turnTarget: src.TurnTarget,
				turnSpeed:  src.TurnSpeed,
				turnBusy:   src.TurnBusy,
				spinSpeed:  src.SpinSpeed,
				spinTarget: src.SpinTarget,
				spinAccel:  src.SpinAccel,
				spinActive: src.SpinActive,
			}
		}
	}
}

// SnapshotFlags returns piece flags for save [04 §4.3] P1-I01.
func (v *VM) SnapshotFlags() []uint8 {
	if v == nil || v.pieceFlags == nil {
		return nil
	}
	cp := make([]uint8, len(v.pieceFlags))
	copy(cp, v.pieceFlags)
	return cp
}

// RestoreFlags sets piece flags [04 §4.3] P1-I01.
func (v *VM) RestoreFlags(f []uint8) {
	if v == nil {
		return
	}
	if f == nil {
		v.pieceFlags = nil
		return
	}
	cp := make([]uint8, len(f))
	copy(cp, f)
	v.pieceFlags = cp
}

// SnapshotFull captures statics, threads, pieces, flags, and anims for save [04 §4.2][04 §4.6][04 §4.3] P1-I01.
func (v *VM) SnapshotFull() (statics []int32, threads [8]Thread, pieces []model.PieceState, flags []uint8, anims []PieceAnimSave) {
	if v == nil {
		return nil, [8]Thread{}, nil, nil, nil
	}
	statics, threads = v.Snapshot()
	pieces = v.SnapshotPieces()
	flags = v.SnapshotFlags()
	anims = v.SnapshotAnims()
	return
}

// RestoreFull restores full VM state [04 §4.2][04 §4.6][04 §4.3] P1-I01.
func (v *VM) RestoreFull(statics []int32, threads [8]Thread, pieces []model.PieceState, flags []uint8, anims []PieceAnimSave) {
	if v == nil {
		return
	}
	v.RestoreSnapshot(statics, threads)
	v.RestorePieces(pieces)
	v.RestoreFlags(flags)
	v.RestoreAnims(anims)
}
