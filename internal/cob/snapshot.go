package cob

// SnapshotFlags returns a copy of piece draw/cache/shade/shadow flags for the
// simulation-to-presentation publication boundary [04 §4.3][03 §2.4] [04 §"Piece flag polarity"].
// When the VM is bound to a unit's render-piece record, the snapshot reflects that unit-owned
// array; otherwise it returns the VM-local fallback (fixture VMs).
// It remains live presentation state; the removed VM snapshots were
// save/restore-only continuation helpers.
func (v *VM) SnapshotFlags() []uint8 {
	if v == nil {
		return nil
	}
	flags := v.renderPieceFlags()
	if flags == nil {
		return nil
	}
	cp := make([]uint8, len(flags))
	copy(cp, flags)
	return cp
}
