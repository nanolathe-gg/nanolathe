package cob

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
