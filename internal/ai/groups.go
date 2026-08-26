package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// high input bits below retain opaque retail semantics; the output bits are
// kept named only by their observed masks because no design-level name is
// established [R-P0-04 "Classifier eligibility, destinations, and order"].
const (
	classifierEligibleBit uint32 = units.ClassifierEligibleStatus
	classifierInputA      uint32 = 0x20000000
	classifierInputB      uint32 = 0x80000000
	classifierOutputA     uint32 = 0x00040000
	classifierOutputB     uint32 = 0x00080000
	classifierOutputMask  uint32 = 0x00100000
	classifierOutputSet   uint32 = 0x00200000
)

// isCombatUnit reports the conservative combat classification used by the
// observed-state milestone recorder. It is deliberately not used by the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// group writer"].
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

// updateGroups applies the established lifecycle cleanup pass to manager
// tactical vectors. Classification is intentionally separate: retail calls
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// pass therefore cannot discover a unit merely because a task callback ran
// [R-P0-04].
func (m *Manager) updateGroups(w *units.World) {
	if m == nil || w == nil {
		return
	}
	m.GroupResource = cleanGroup(m.GroupResource, w, m.Player)
	// Clean existing groups.
	m.GroupWaveA = cleanGroup(m.GroupWaveA, w, m.Player)
	m.GroupRegroupA = cleanGroup(m.GroupRegroupA, w, m.Player)
	m.GroupConstruction = cleanGroup(m.GroupConstruction, w, m.Player)
	m.GroupNull = cleanGroup(m.GroupNull, w, m.Player)
	m.GroupWaveB = cleanGroup(m.GroupWaveB, w, m.Player)
	m.GroupRegroupB = cleanGroup(m.GroupRegroupB, w, m.Player)
	m.GroupExplore = cleanGroup(m.GroupExplore, w, m.Player)
	m.GroupRally = cleanGroup(m.GroupRally, w, m.Player)
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// owner slice in pool order. A unit must carry runtime bit 0x20 and have no
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// and stores that record number back in Group. The two high runtime bits and
// the definition predicates retain their opaque retail names; their exact
// tests are established even where the semantic names are not [R-P0-04].
func (m *Manager) classifyGroups(w *units.World) {
	if m == nil || w == nil {
		return
	}
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive || u.Owner != m.Player {
			continue
		}
		if u.Flags&classifierEligibleBit == 0 {
			continue
		}
		// The status-byte/word writes precede the group-zero gate in the retail
		// sweep. They are observable on already-grouped units as well as on
		// newly classified units, so do not move them inside the assignment
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		if u.Def != nil && u.Def.CanCapture {
			u.Flags = (u.Flags &^ classifierOutputB) | classifierOutputA
		} else {
			u.Flags = (u.Flags &^ classifierOutputA) | classifierOutputB
		}
		u.Flags = (u.Flags &^ classifierOutputMask) | classifierOutputSet
		if u.Group != 0 {
			continue
		}
		group := uint8(0)
		if u.Flags&classifierInputA != 0 {
			if u.Flags&classifierInputB == 0 {
				group = 1
			} else {
				group = 5
			}
		} else if u.Def != nil {
			switch {
			case u.Def.Builder:
				group = 4
			case u.Def.CanFly:
				group = 8
			case u.Def.MaxSlope > 0:
				group = 7
			case u.Flags&classifierInputB != 0:
				group = 3
			}
		}
		if group != 0 {
			m.writeGroup(u, int8(group))
		}
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// removes the unit from its old manager record by replacing the removed slot
// with the last element, then appends to the destination and finally stores
// the new group value. A newGroup of -1 is represented by Group==0 because
// Unit.Group is the public 0..9 control-group field; death callers do not
// retain a negative sentinel after the unit is torn down. Group zero is the
// ungrouped record and is intentionally not one of the nine task vectors.
// There is no gameplay cap and no RNG in this writer [R-P0-04].
func (m *Manager) writeGroup(u *units.Unit, newGroup int8) {
	if m == nil || u == nil {
		return
	}
	m.removeGroupMember(u.Group, u.Handle)
	if newGroup < 0 {
		u.Group = 0
		return
	}
	group := uint8(newGroup)
	if group == 0 {
		u.Group = 0
		return
	}
	if group > 9 {
		return
	}
	m.insertGroupMember(u.Handle, group)
	u.Group = group
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// replace-with-last, not stable compaction; preserving that order matters to
// subsequent farthest-member ties and save bytes [R-P0-04].
func (m *Manager) removeGroupMember(group uint8, h pool.Handle) bool {
	if m == nil || group == 0 || group > 9 {
		return false
	}
	list := m.groupVector(group)
	if list == nil {
		return false
	}
	for i, candidate := range *list {
		if candidate != h {
			continue
		}
		last := len(*list) - 1
		(*list)[i] = (*list)[last]
		*list = (*list)[:last]
		return true
	}
	return false
}

// groupVector returns the mutable task vector for its retail record number.
// The switch deliberately enumerates every record, including the currently
// inert null slot, so a later lifecycle/control-group writer cannot silently
// collapse one of the nine records into another task [R-P0-04].
func (m *Manager) groupVector(group uint8) *[]pool.Handle {
	if m == nil {
		return nil
	}
	switch group {
	case 1:
		return &m.GroupResource
	case 2:
		return &m.GroupWaveA
	case 3:
		return &m.GroupRegroupA
	case 4:
		return &m.GroupConstruction
	case 5:
		return &m.GroupNull
	case 6:
		return &m.GroupWaveB
	case 7:
		return &m.GroupRegroupB
	case 8:
		return &m.GroupExplore
	case 9:
		return &m.GroupRally
	default:
		return nil
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// does not draw RNG; Go's append supplies the growable vector semantics.
func (m *Manager) insertGroupMember(h pool.Handle, group uint8) {
	if m == nil || group < 1 || group > 9 {
		return
	}
	if list := m.groupVector(group); list != nil {
		*list = append(*list, h)
	}
}

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// The vector transfer itself is performed by mergeWaveGroups; this helper
// keeps the named instance field coherent for subsequent classifier gates and
// control-group/save consumers.
func (m *Manager) stampGroupValues(w *units.World, handles []pool.Handle, group uint8) {
	if m == nil || w == nil || group == 0 || group > 9 {
		return
	}
	for _, h := range handles {
		if u := w.Unit(h); u != nil {
			u.Group = group
		}
	}
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
	return containsHandle(m.GroupResource, h) || containsHandle(m.GroupWaveA, h) || containsHandle(m.GroupRegroupA, h) || containsHandle(m.GroupConstruction, h) || containsHandle(m.GroupNull, h) || containsHandle(m.GroupWaveB, h) || containsHandle(m.GroupRegroupB, h) || containsHandle(m.GroupExplore, h) || containsHandle(m.GroupRally, h)
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
