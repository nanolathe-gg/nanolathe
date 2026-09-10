package ai

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// DebugSnapshot explicitly projects scheduling and group state; catalog/profile
// definitions and callback closures never enter the diagnostic bundle.
func (m *Manager) DebugSnapshot() map[string]any {
	if m == nil {
		return nil
	}
	groups := [][]pool.Handle{m.GroupResource, m.GroupWaveA, m.GroupRegroupA, m.GroupConstruction, m.GroupNull, m.GroupWaveB, m.GroupRegroupB, m.GroupExplore, m.GroupRally}
	for i := range groups {
		groups[i] = append([]pool.Handle(nil), groups[i]...)
	}
	return map[string]any{"player": m.Player,
		"deadlines":            m.Deadlines,
		"countdown":            m.countdown,
		"unit_loss_deadline":   m.unitLossDeadline,
		"wave_a_engaged":       m.waveAEngaged,
		"wave_b_engaged":       m.waveBEngaged,
		"groups_in_task_order": groups,
		"origin_x_raw":         m.OriginX.Raw(),
		"origin_z_raw":         m.OriginZ.Raw(),
		"center_raw":           [3]int64{m.Strategic.CenterX.Raw(), m.Strategic.CenterY.Raw(), m.Strategic.CenterZ.Raw()},
		"radius":               m.Strategic.Radius,
		"last_refresh_tick":    m.Strategic.LastRefreshTick,
		"rally_initialized":    m.rallyInitialized,
		"rally_best_raw":       [3]int64{m.rallyBestX.Raw(), m.rallyBestY.Raw(), m.rallyBestZ.Raw()},
		"rally_best_score":     m.rallyBestScore,
		"rally_targets":        append([]pool.Handle(nil), m.rallyTargets...),
		"omissions":            "strategic per-definition vectors, profile and live tactical scratch are not captured"}
}
