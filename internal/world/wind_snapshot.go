package world

// Snapshot captures wind field for save [P0-I11][01 §7.3].
func (w *Wind) Snapshot() (min, max, strength int32, heading uint16, scalar float32, dirX, dirZ int32, nextChange, lastChange uint32, changed, pending bool) {
	if w == nil {
		return 0, 0, 0, 0, 0, 0, 0, 0, 0, false, false
	}
	return w.Min, w.Max, w.Strength, w.Heading, w.Scalar, w.DirX, w.DirZ, w.NextChange, w.LastChange, w.Changed, w.pending
}

// RestoreSnapshot restores wind field [P0-I11][01 §7.3].
func (w *Wind) RestoreSnapshot(min, max, strength int32, heading uint16, scalar float32, dirX, dirZ int32, nextChange, lastChange uint32, changed, pending bool) {
	if w == nil {
		return
	}
	w.Min = min
	w.Max = max
	w.Strength = strength
	w.Heading = heading
	w.Scalar = scalar
	w.DirX = dirX
	w.DirZ = dirZ
	w.NextChange = nextChange
	w.LastChange = lastChange
	w.Changed = changed
	w.pending = pending
}
