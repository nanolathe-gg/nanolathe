package orders

// Snapshot captures both queue segments for save [P0-I11][04 §3.2].
func (q *Queue) Snapshot() (primary []*Node, secondary []*Node) {
	if q == nil {
		return nil, nil
	}
	// copy slices to avoid alias
	prim := make([]*Node, len(q.primary))
	copy(prim, q.primary)
	sec := make([]*Node, len(q.secondary))
	copy(sec, q.secondary)
	return prim, sec
}

// RestoreSnapshot restores both segments from snapshot [P0-I11].
func (q *Queue) RestoreSnapshot(primary, secondary []*Node) {
	if q == nil {
		return
	}
	prim := make([]*Node, len(primary))
	copy(prim, primary)
	sec := make([]*Node, len(secondary))
	copy(sec, secondary)
	q.primary = prim
	q.secondary = sec
}

// LenPrimaryLocked is same as LenPrimary but for save translation.
func (q *Queue) PrimarySnapshot() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.primary))
	copy(out, q.primary)
	return out
}

func (q *Queue) SecondarySnapshot() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.secondary))
	copy(out, q.secondary)
	return out
}
