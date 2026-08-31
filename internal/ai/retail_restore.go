package ai

import "github.com/nanolathe/nanolathe/internal/units"

// RestoreGroupsFromUnits rebuilds only the nine tactical vectors represented
// by the unit save word.  Strategic records, manager tasks, and their
// countdowns are normal battle initialization state and are intentionally not
// reconstructed from absent save data [08 R-SAVE-02 §§6,11].
func (m *Manager) RestoreGroupsFromUnits(w *units.World) {
	if m == nil || w == nil {
		return
	}
	for group := uint8(1); group <= 9; group++ {
		if v := m.groupVector(group); v != nil {
			*v = (*v)[:0]
		}
	}
	for _, u := range w.IterSliced() {
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
