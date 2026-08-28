package ai

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// The following status bits are the exact writes made by the classifier
// sweep. The two high input bits are the building-class and armed bits, both
// written once by the unit allocator initializer from the definition; the
// output bits are kept named only by their observed masks because no
// design-level name is established [08 "Classifier eligibility, destinations,
// and order"].
const (
	classifierEligibleBit uint32 = units.ClassifierEligibleStatus
	classifierBuilding    uint32 = units.BuildingClassStatus
	classifierArmed       uint32 = units.ArmedStatus
	classifierOutputA     uint32 = 0x00040000
	classifierOutputB     uint32 = 0x00080000
	classifierOutputMask  uint32 = 0x00100000
	classifierOutputSet   uint32 = 0x00200000
)

// isCombatUnit reports the conservative combat classification used by
// tactical grouping and order selection. It is deliberately not used by the
// manager task-group writer, which uses runtime status bits and the established
// definition predicates [R-P0-04 §3].
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
	if !content.IsWeaponInactive(def.Weapon1Def) || !content.IsWeaponInactive(def.Weapon2Def) || !content.IsWeaponInactive(def.Weapon3Def) {
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

// classifyGroups is the recovered classifier producer. It scans this manager's
// owner slice in pool order. A unit must carry runtime bit 0x20 and have no
// current group (Group==0). The direct group writer then appends it to the selected record
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
		// branches. The source field is the authored capture flag.
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
		if u.Flags&classifierBuilding != 0 {
			// Buildings: unarmed ones are the eco-toggle task's input, armed
			// ones are parked in the inert null record.
			if u.Flags&classifierArmed == 0 {
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
			case u.Flags&classifierArmed != 0:
				// Ordinary armed ground units land in regroup A, which is the
				// wave-A merge's peer and therefore the source the attack wave
				// bootstraps from [08 "Wave merge"].
				group = 3
			}
		}
		if group != 0 {
			m.writeGroup(u, int8(group))
		}
	}
}

// writeGroup is the direct manager-group writer. It first
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
	if newGroup > 9 {
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
	m.insertGroupMember(u.Handle, group)
	u.Group = group
}

// removeGroupMember is the source-removal half of the direct writer. Retail uses
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

// insertGroupMember is the append half of the direct writer. It has no member cap and
// does not draw RNG; Go's append supplies the growable vector semantics.
func (m *Manager) insertGroupMember(h pool.Handle, group uint8) {
	if m == nil || group < 1 || group > 9 {
		return
	}
	if list := m.groupVector(group); list != nil {
		*list = append(*list, h)
	}
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

// groupCentroid computes centroid of group handles; returns false if empty.
func groupCentroid(handles []pool.Handle, w *units.World) (numeric.Fixed, numeric.Fixed, bool) {
	// The centroid reads stored signed pixel coordinates, averages with integer
	// division, and shifts the result back to 16.16; it does not average the low 16
	// fractional bits of the authoritative position [R-P0-04 "Located
	// producers and transfer order"; 08 "Strategy manager and its task graph"].
	// Keep regroup's centroid in the same domain as wave merge.
	x, z, ok := retailGroupCentroid(handles, w)
	if !ok {
		return 0, 0, false
	}
	return numeric.Fixed(int64(x) << 16), numeric.Fixed(int64(z) << 16), true
}
