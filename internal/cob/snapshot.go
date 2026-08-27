package cob

// SnapshotFlags returns a copy of piece draw/cache/shade/shadow flags for the
// simulation-to-presentation publication boundary [04 §4.3][03 §2.4].
// It remains live presentation state; the removed VM snapshots were
// save/restore-only continuation helpers.
func (v *VM) SnapshotFlags() []uint8 {
	if v == nil || v.pieceFlags == nil {
		return nil
	}
	cp := make([]uint8, len(v.pieceFlags))
	copy(cp, v.pieceFlags)
	return cp
}
