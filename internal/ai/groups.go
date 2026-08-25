package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// isCombatUnit reports whether def is a combat unit eligible for wave/explore groups.
// We exclude builders and pure economy buildings; require mobility and attack capability [P0-02][08].
// TODO(question): exact retail classification for group eligibility is not established; this heuristic uses CanMove/CanAttack/weapon presence.
func isCombatUnit(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	if def.Builder {
		return false
	}
	// OnOffable metal makers are economy, not combat.
	if def.OnOffable && def.MakesMetal != 0 && def.ExtractsMetal == 0 {
		return false
	}
	if def.ExtractsMetal != 0 {
		return false
	}
	if def.IsFeature {
		return false
	}
	if !def.CanMove && def.MaxVelocity == 0 {
		return false
	}
	// Need attack capability: CanAttack or weapon present or specific attack flags.
	if def.CanAttack || def.CanGuard || def.CanPatrol {
		return true
	}
	if def.Weapon1Def != nil || def.Weapon2Def != nil || def.Weapon3Def != nil {
		return true
	}
	// Fallback: mobile units with BMCode false? Keep conservative.
	return false
}

// isBuilderUnit reports whether def is a builder eligible for construction tasks.
func isBuilderUnit(def *content.UnitDef) bool {
	if def == nil {
		return false
	}
	return def.Builder
}

// isInHandle checks membership deterministically via linear scan (small groups).
func containsHandle(list []pool.Handle, h pool.Handle) bool {
	for _, v := range list {
		if v == h {
			return true
		}
	}
	return false
}

// cleanGroup removes dead, non-owned, or incomplete handles in place.
func cleanGroup(list []pool.Handle, w *units.World, player uint8) []pool.Handle {
	if w == nil {
		return list[:0]
	}
	n := 0
	for _, h := range list {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Owner != player || u.Remaining != 0 {
			continue
		}
		list[n] = h
		n++
	}
	return list[:n]
}

// updateGroups maintains AI groups from unit creation/death/completion.
// It is called each tick before task dispatch to ensure groups reflect live state [P0-I12].
// Groups are populated deterministically by scanning units.World in sliced order (player asc, slot asc) [I1].
func (m *Manager) updateGroups(w *units.World) {
	if m == nil || w == nil {
		return
	}
	// Clean existing groups.
	m.GroupWaveA = cleanGroup(m.GroupWaveA, w, m.Player)
	m.GroupWaveB = cleanGroup(m.GroupWaveB, w, m.Player)
	m.GroupExplore = cleanGroup(m.GroupExplore, w, m.Player)
	m.GroupRally = cleanGroup(m.GroupRally, w, m.Player)
	m.GroupRegroupA = cleanGroup(m.GroupRegroupA, w, m.Player)
	m.GroupRegroupB = cleanGroup(m.GroupRegroupB, w, m.Player)

	// Scan for ungrouped combat units and assign to groups.
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive || u.Owner != m.Player || u.Remaining != 0 {
			continue
		}
		if u.Def == nil {
			continue
		}
		if !isCombatUnit(u.Def) {
			continue
		}
		h := u.Handle
		if containsHandle(m.GroupWaveA, h) || containsHandle(m.GroupWaveB, h) || containsHandle(m.GroupExplore, h) || containsHandle(m.GroupRally, h) || containsHandle(m.GroupRegroupA, h) || containsHandle(m.GroupRegroupB, h) {
			continue
		}
		// Prefer wave groups up to waveMax, then explore, then rally.
		if len(m.GroupWaveA) < waveMax {
			m.GroupWaveA = append(m.GroupWaveA, h)
		} else if len(m.GroupWaveB) < waveMax {
			m.GroupWaveB = append(m.GroupWaveB, h)
		} else if len(m.GroupExplore) < 10 {
			m.GroupExplore = append(m.GroupExplore, h)
		} else if len(m.GroupRally) < 10 {
			m.GroupRally = append(m.GroupRally, h)
		} else if len(m.GroupRegroupA) < waveMax {
			m.GroupRegroupA = append(m.GroupRegroupA, h)
		} else if len(m.GroupRegroupB) < waveMax {
			m.GroupRegroupB = append(m.GroupRegroupB, h)
		}
	}
}

// isInAnyGroup reports whether h is in any AI group.
func (m *Manager) isInAnyGroup(h pool.Handle) bool {
	if m == nil {
		return false
	}
	return containsHandle(m.GroupWaveA, h) || containsHandle(m.GroupWaveB, h) || containsHandle(m.GroupExplore, h) || containsHandle(m.GroupRally, h) || containsHandle(m.GroupRegroupA, h) || containsHandle(m.GroupRegroupB, h)
}

// findEnemyTarget deterministically selects an enemy unit nearest strategic center.
// If no enemy units, returns nil. Deterministic tie-break by handle ascending [I1].
func (m *Manager) findEnemyTarget(w *units.World) *units.Unit {
	if w == nil {
		return nil
	}
	var best *units.Unit
	var bestDist2 int64 = 1 << 62
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive || u.Remaining != 0 {
			continue
		}
		if int(u.Owner) == int(m.Player) {
			continue
		}
		if u.Def == nil {
			continue
		}
		dx := int64(u.X) - int64(m.Strategic.CenterX)
		dz := int64(u.Z) - int64(m.Strategic.CenterZ)
		d2 := dx*dx + dz*dz
		if best == nil || d2 < bestDist2 || (d2 == bestDist2 && u.Handle < best.Handle) {
			best = u
			bestDist2 = d2
		}
	}
	return best
}

// enemyCentroid returns a target position for attack: enemy unit if found, else map center or strategic center.
// Deterministic, no RNG.
func (m *Manager) enemyCentroid(w *units.World) (numeric.Fixed, numeric.Fixed) {
	if t := m.findEnemyTarget(w); t != nil {
		return t.X, t.Z
	}
	if m.Terrain != nil {
		cx := world.CellToWorld(m.Terrain.CellW / 2)
		cz := world.CellToWorld(m.Terrain.CellH / 2)
		// Offset per player to ensure distinct but deterministic attack points for different managers
		// Use player*16 cells offset to avoid exact overlap while staying in bounds
		off := numeric.Fixed(int32(m.Player)*16*65536) % (numeric.Fixed(m.Terrain.CellW*16*65536) / 4)
		// Keep within half quadrant
		return cx + off, cz + off
	}
	return m.Strategic.CenterX, m.Strategic.CenterZ
}

// groupCentroid computes centroid of group handles; returns false if empty.
func groupCentroid(handles []pool.Handle, w *units.World) (numeric.Fixed, numeric.Fixed, bool) {
	if len(handles) == 0 || w == nil {
		return 0, 0, false
	}
	var sumX, sumZ int64
	var n int64
	for _, h := range handles {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			continue
		}
		sumX += int64(u.X)
		sumZ += int64(u.Z)
		n++
	}
	if n == 0 {
		return 0, 0, false
	}
	return numeric.Fixed(sumX / n), numeric.Fixed(sumZ / n), true
}
