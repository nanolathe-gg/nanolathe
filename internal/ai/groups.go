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
			// The regroup-B key is the definition's MinWaterDepth word, the
			// value the FBI compile copies from the movement class, and the
			// test is `>= 1`: a definition that may stand in water goes to
			// regroup B. This row used to read the MaxSlope word, which the
			// 2026-08-29 correction retired as the wrong label [08 R-P0-04 §3
			// "Classifier eligibility, destinations, and order"][08 R-AI-03 §6].
			// Every stock land definition leaves MinWaterDepth at its movement
			// template value, so the wrong key sent every armed ground unit to
			// regroup B and left regroup A — and therefore wave A — empty for
			// the whole battle.
			case u.Def.MinWaterDepth >= 1:
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

// OnUnitDeath is the death-teardown caller of the direct writer. Retail's
// whole-image caller census names six caller classes for the writer; this is
// the "death teardown (remove sentinel, so a dying unit leaves its record)"
// class [R-P0-04 §3 "The direct manager-group writer"]. It removes u from its
// own stored group record only — swap-delete source removal, no destination,
// no RNG draw — and leaves every other record untouched. Callers invoke this
// once, at the FinalizeDeath boundary, on the dying unit's current owner's
// manager (see internal/session/session.go's Units.OnDeath handler); a unit
// captured before death and re-classified nowhere keeps its pre-capture
// owner's stored record entry, which this call cannot reach — see
// reconcileGroupRecord's comment for that remaining case.
func (m *Manager) OnUnitDeath(u *units.Unit) {
	if m == nil || u == nil {
		return
	}
	m.writeGroup(u, -1)
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

// reconcileGroupRecord drops the entries a retail group record cannot hold.
//
// Manager.OnUnitDeath now runs the direct writer's remove-sentinel form at
// the FinalizeDeath boundary (internal/session/session.go's Units.OnDeath
// handler), so an ordinary death no longer leaves a window before the next
// 30-entry sweep. One retail-accurate path still can: capture has no writer
// of its own in the census [R-P0-04 §3] — CaptureUnit changes only u.Owner,
// never u.Group or any record — so a captured unit keeps its pre-capture
// owner's record entry exactly as retail's raw pointer would. If that unit
// later dies, OnUnitDeath fires for its *current* (post-capture) owner's
// manager, which never held the handle, so the removal is a no-op there and
// the pre-capture owner's record keeps the now-freed handle. Should the pool
// later recycle that slot, the stale entry resolves to a *different* live
// unit whose stored group is some other record — not a member under the
// writer's own invariant — and inflates the counts the wave hysteresis reads
// [08 R-AI-01 §4] and stalls the wave merge's farthest-member transfer.
//
// The purge is order-preserving. The direct writer's swap-delete order is
// reproduced for a real removal (see removeGroupMember); these entries have no
// retail counterpart at all, so compacting them out leaves exactly the
// sequence of real members retail's record would have held.
func (m *Manager) reconcileGroupRecord(group uint8, w *units.World) {
	if m == nil || w == nil {
		return
	}
	list := m.groupVector(group)
	if list == nil || len(*list) == 0 {
		return
	}
	kept := (*list)[:0]
	for _, h := range *list {
		u := w.Unit(h)
		if u == nil || !u.Alive || u.Owner != m.Player || u.Group != group {
			continue
		}
		kept = append(kept, h)
	}
	*list = kept
}

// reconcileGroupRecords applies reconcileGroupRecord to all nine task records
// in ascending record order [08 R-P0-04 §2].
func (m *Manager) reconcileGroupRecords(w *units.World) {
	for group := uint8(1); group <= 9; group++ {
		m.reconcileGroupRecord(group, w)
	}
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
	x, _, z, ok := retailGroupCentroid3(handles, w)
	return x, z, ok
}

func retailGroupCentroid3(handles []pool.Handle, w *units.World) (int32, int32, int32, bool) {
	if len(handles) == 0 || w == nil {
		return 0, 0, 0, false
	}
	var sumX, sumY, sumZ, count int32
	for _, h := range handles {
		u := w.Unit(h)
		if u == nil || !u.Alive {
			continue
		}
		sumX += retailCoord(u.X)
		sumY += retailCoord(u.Y)
		sumZ += retailCoord(u.Z)
		count++
	}
	if count == 0 {
		return 0, 0, 0, false
	}
	return sumX / count, sumY / count, sumZ / count, true
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
	return int32(int16(v >> 16))
}

// isInAnyGroup reports whether h is in any AI group.
func (m *Manager) isInAnyGroup(h pool.Handle) bool {
	if m == nil {
		return false
	}
	return containsHandle(m.GroupResource, h) || containsHandle(m.GroupWaveA, h) || containsHandle(m.GroupRegroupA, h) || containsHandle(m.GroupConstruction, h) || containsHandle(m.GroupNull, h) || containsHandle(m.GroupWaveB, h) || containsHandle(m.GroupRegroupB, h) || containsHandle(m.GroupExplore, h) || containsHandle(m.GroupRally, h)
}

// groupCentroid computes the three-axis task centroid [08 R-AI-01 §9]. Each
// coordinate is its signed 16-bit high word; sums wrap at int32 width, division
// truncates toward zero, and the final 16.16 shift wraps to one 32-bit word.
func groupCentroid(handles []pool.Handle, w *units.World) (numeric.Fixed, numeric.Fixed, numeric.Fixed, bool) {
	x, y, z, ok := retailGroupCentroid3(handles, w)
	if !ok {
		return 0, 0, 0, false
	}
	return numeric.Fixed(int32(x << 16)), numeric.Fixed(int32(y << 16)), numeric.Fixed(int32(z << 16)), true
}
