package ai

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// RestoreGroupsFromUnits rebuilds only the nine tactical vectors represented by
// the unit save word. Everything else a manager holds — the strategic state,
// the class vectors, the task records and their deadlines and the
// classification countdown — is ordinary battle-entry construction state and is
// deliberately not reconstructed from the bank, because the bank does not carry
// it: retail's per-player reset builds a fresh AI record for every non-remote
// slot on the load path exactly as on a fresh entry, and the restoration
// dispatcher runs afterwards [08 R-ENTRY-01 §3 step 24][08 R-SAVE-02 §11-A].
//
// The one AI word the bank does carry is per unit: the saved group index, whose
// reader "moves the unit out of whatever group it holds and into this one
// (group-vector append)" — the single base-record word with a side effect
// beyond a field copy [08 R-SAVE-02 §6]. This is that append, applied once the
// recursive pass has supplied its completion order. A forward carrier or
// engagement reference appends the referenced unit before the referring unit,
// so a pool scan would change wave bootstrap and distance tie-breaking
// [08 R-SAVE-02 §6][08 R-P0-04 §3].
func (m *Manager) RestoreGroupsFromUnits(restored []*units.Unit) {
	if m == nil {
		return
	}
	for group := uint8(1); group <= 9; group++ {
		if v := m.groupVector(group); v != nil {
			*v = (*v)[:0]
		}
	}
	for _, u := range restored {
		if u == nil || !u.Alive || u.Owner != m.Player {
			continue
		}
		if u.RestoredAIGroup < 1 || u.RestoredAIGroup > 9 {
			continue
		}
		group := uint8(u.RestoredAIGroup)
		m.insertGroupMember(u.Handle, group)
	}
}

// GroupMembers returns a copy of one task group's vector in vector order.
// Group 0 is the ungrouped sentinel and owns no task record, so it reads empty
// [08 R-P0-04 §2]. The copy exists so a caller — the save round-trip gate, a
// diagnostic — can compare membership without holding a pointer into the
// record the classifier and the wave merge mutate every dispatch.
func (m *Manager) GroupMembers(group uint8) []pool.Handle {
	if m == nil || group < 1 || group > 9 {
		return nil
	}
	v := m.groupVector(group)
	if v == nil || len(*v) == 0 {
		return nil
	}
	return append([]pool.Handle(nil), (*v)...)
}
