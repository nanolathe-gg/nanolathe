package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// isCombatUnit reports the conservative combat classification used by the
// observed-state milestone recorder. It is deliberately not used to populate
// manager tactical groups: the retail group eligibility predicate and initial
// writer are not established [08 "Eco toggle and group-vector population"].
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

// updateGroups applies the established cleanup pass to manager tactical
// vectors. Retail allocates these vectors empty and the bounded writer census
// found no initial population path. The only located manager-vector producer
// is wave merge, which transfers members between already-populated wave
// vectors; it is not an initializer [08 "Strategy manager and its task graph";
// 08 "Eco toggle and group-vector population"].
//
// Do not scan the world or classify units here. Doing so changes an inert stock
// manager into a synthetic order producer. A future runtime trace may add the
// exact producer and lifecycle hooks; until then an empty vector must remain
// empty (TODO(question), R-P0-04).
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
}

// mergeWaveGroups is the sole recovered producer for manager tactical
// vectors. It is intentionally limited to transferring members between the
// two already-populated wave vectors; it never discovers units from World.
// The distance arithmetic is in retail's signed integer world-coordinate
// domain (unit fixed-point positions truncated toward zero before squaring),
// not in authoritative 16.16 coordinates [R-P0-04 "Located producers and
// transfer order"; 08 "Strategy manager and its task graph"].
func mergeWaveGroups(current, peer []pool.Handle, w *units.World, threshold int32) ([]pool.Handle, []pool.Handle) {
	if w == nil || len(current) == 0 {
		return current, peer
	}
	centroidX, centroidZ, ok := retailGroupCentroid(current, w)
	if !ok {
		return current, peer
	}
	for len(current) > 0 {
		count := int64(len(current))
		limit := int64(threshold) * count
		farthest := -1
		var farthestDistance int64
		for i, h := range current {
			u := w.Unit(h)
			if u == nil || !u.Alive {
				continue
			}
			distance := retailDistanceSquared(u, centroidX, centroidZ)
			// Strictly retain the first member on ties: the vector order is
			// the retail deterministic tie-break.
			if farthest < 0 || distance > farthestDistance {
				farthest = i
				farthestDistance = distance
			}
		}
		if farthest < 0 || farthestDistance < limit {
			break
		}
		peer = append(peer, current[farthest])
		current = append(current[:farthest], current[farthest+1:]...)
		centroidX, centroidZ, ok = retailGroupCentroid(current, w)
		if !ok {
			break
		}
	}

	// Recompute the current-group limit after outlier transfers. Members are
	// collected in peer-vector order and transferred in that same order.
	if len(current) == 0 {
		return current, peer
	}
	centroidX, centroidZ, ok = retailGroupCentroid(current, w)
	if !ok {
		return current, peer
	}
	limit := int64(threshold) * int64(len(current))
	collected := make([]pool.Handle, 0, len(peer))
	for _, h := range peer {
		u := w.Unit(h)
		if u != nil && u.Alive && retailDistanceSquared(u, centroidX, centroidZ) < limit {
			collected = append(collected, h)
		}
	}
	if len(collected) == 0 {
		return current, peer
	}
	for _, h := range collected {
		for i, candidate := range peer {
			if candidate != h {
				continue
			}
			peer = append(peer[:i], peer[i+1:]...)
			break
		}
		current = append(current, h)
	}
	return current, peer
}

func retailGroupCentroid(handles []pool.Handle, w *units.World) (int32, int32, bool) {
	if len(handles) == 0 || w == nil {
		return 0, 0, false
	}
	var sumX, sumZ int64
	var count int64
	for _, h := range handles {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			continue
		}
		sumX += int64(retailCoord(u.X))
		sumZ += int64(retailCoord(u.Z))
		count++
	}
	if count == 0 {
		return 0, 0, false
	}
	return int32(sumX / count), int32(sumZ / count), true
}

func retailDistanceSquared(u *units.Unit, x, z int32) int64 {
	if u == nil {
		return 0
	}
	dx := int64(retailCoord(u.X)) - int64(x)
	dz := int64(retailCoord(u.Z)) - int64(z)
	return dx*dx + dz*dz
}

// retailCoord converts a 16.16 position to the signed pixel word read by the
// recovered helpers. The executable reads the high signed word of the raw
// position, so this is an arithmetic shift (floor for negative fractional
// values), not Fixed.Int's truncation-toward-zero path [03 §2.1; I3].
func retailCoord(v numeric.Fixed) int32 {
	return int32(v >> 16)
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
// Alliance-aware: skips allied owners via IsAlliance func [P0-07] ON-06.
func (m *Manager) findEnemyTarget(w *units.World) *units.Unit {
	if w == nil || m == nil {
		return nil
	}
	var best *units.Unit
	var bestDist2 int64 = 1 << 62
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive || u.Remaining != 0 {
			continue
		}
		if m.isAllied(u.Owner) {
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
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// and shifts the result back to 16.16; it does not average the low 16
	// fractional bits of the authoritative position [R-P0-04 "Located
	// producers and transfer order"; 08 "Strategy manager and its task graph"].
	// Keep regroup's centroid in the same domain as wave merge.
	x, z, ok := retailGroupCentroid(handles, w)
	if !ok {
		return 0, 0, false
	}
	return numeric.Fixed(int64(x) << 16), numeric.Fixed(int64(z) << 16), true
}
