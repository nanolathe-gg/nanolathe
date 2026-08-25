package ai

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Milestone stage names in causal order [P0-07] ON-06.
// Each is set only from observed production state, never because a table entry exists.
const (
	MilestoneProfileLoaded         = "ProfileLoaded"
	MilestonePlacementSelected     = "PlacementSelected"
	MilestoneBuildRequestAccepted  = "BuildRequestAccepted"
	MilestoneNanoframeObserved     = "NanoframeObserved"
	MilestoneFactoryCompleted      = "FactoryCompleted"
	MilestoneFactoryProductQueued  = "FactoryProductQueued"
	MilestoneCombatUnitCompleted   = "CombatUnitCompleted"
	MilestoneGroupAssigned         = "GroupAssigned"
	MilestoneAttackMoveIssued      = "AttackMoveIssued"
	MilestoneHostileDamageObserved = "HostileDamageObserved"
)

// milestoneOrder is the canonical order for deterministic iteration (I1).
var milestoneOrder = []string{
	MilestoneProfileLoaded,
	MilestonePlacementSelected,
	MilestoneBuildRequestAccepted,
	MilestoneNanoframeObserved,
	MilestoneFactoryCompleted,
	MilestoneFactoryProductQueued,
	MilestoneCombatUnitCompleted,
	MilestoneGroupAssigned,
	MilestoneAttackMoveIssued,
	MilestoneHostileDamageObserved,
}

// milestoneSet returns true if the stage is a known milestone.
func isMilestoneStage(s string) bool {
	for _, k := range milestoneOrder {
		if k == s {
			return true
		}
	}
	return false
}

// recordMilestone sets the milestone to tick if not already set [P0-07].
// It preserves the first tick (earliest observation) for determinism (I1).
// Only called from observed state paths, never from table existence.
func (m *Manager) recordMilestone(stage string, tick uint32) {
	if m == nil || !isMilestoneStage(stage) {
		return
	}
	if m.milestones == nil {
		m.milestones = make(map[string]uint32, len(milestoneOrder))
	}
	if _, ok := m.milestones[stage]; ok {
		return
	}
	m.milestones[stage] = tick
}

// MissedQueueCallbacks reports typed build requests dropped because the
// session never bound QueueBuildTyped [RX-01][F-P0-004]. Production sessions
// bind at manager creation; nonzero means a fixture-built AI lacks the binder.
func (m *Manager) MissedQueueCallbacks() uint32 {
	if m == nil {
		return 0
	}
	return m.missedQueueCallbacks
}

// Milestones returns a deterministic copy of stage→tick [P0-07] ON-06.
// Iteration over the returned map is nondeterministic per Go, but the
// caller can iterate milestoneOrder for stable order; the map itself is a copy.
func (m *Manager) Milestones() map[string]uint32 {
	if m == nil || m.milestones == nil {
		return map[string]uint32{}
	}
	cp := make(map[string]uint32, len(m.milestones))
	for k, v := range m.milestones {
		cp[k] = v
	}
	return cp
}

// MilestonesOrdered returns milestones in canonical order for testing (I1).
func (m *Manager) MilestonesOrdered() [][2]interface{} {
	if m == nil || m.milestones == nil {
		return nil
	}
	out := make([][2]interface{}, 0, len(m.milestones))
	for _, k := range milestoneOrder {
		if v, ok := m.milestones[k]; ok {
			out = append(out, [2]interface{}{k, v})
		}
	}
	return out
}

// milestonesSortedKeys returns sorted keys for deterministic debug (I1).
func milestonesSortedKeys(mm map[string]uint32) []string {
	keys := make([]string, 0, len(mm))
	for k := range mm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isAllied reports whether owner is allied with m.Player via IsAlliance func [P0-07].
// Default same-owner-only when IsAlliance is nil (I1).
func (m *Manager) isAllied(owner uint8) bool {
	if m == nil {
		return owner == 0
	}
	if m.IsAlliance != nil {
		return m.IsAlliance(m.Player, owner)
	}
	return owner == m.Player
}

// isHostile reports whether owner is hostile (not allied and not self).
func (m *Manager) isHostile(owner uint8) bool {
	if m == nil {
		return false
	}
	return !m.isAllied(owner)
}

// observeMilestones scans world state for production-visible milestones [P0-07].
// It is called each Tick after updateGroups and strategic refresh, observing
// units world state via existing hooks, never table-only.
func (m *Manager) observeMilestones(tick uint32, w *units.World) {
	if m == nil {
		return
	}
	// ProfileLoaded: observed when profile is non-nil after successful LoadProfile/NewManager.
	// Set from observation of manager state, not table entry: only when Profile non-nil and not yet set.
	if m.Profile != nil {
		m.recordMilestone(MilestoneProfileLoaded, tick)
	}
	if w == nil {
		return
	}
	// Scan for nanoframe and completions.
	hasNanoframe := false
	hasFactoryCompleted := false
	hasCombatCompleted := false
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if !m.isAllied(u.Owner) {
			continue
		}
		// Nanoframe: owned allied unit under construction (Remaining !=0)
		if u.Remaining != 0 {
			hasNanoframe = true
		}
		// Factory completed: allied completed builder that is immobile factory (Builder && footprint>=4 && MaxVelocity==0) — retail factories have CanMove true but MaxVelocity 0 [ON-11].
		if u.Remaining == 0 && u.Def != nil && u.Def.Builder && u.Def.FootprintX >= 4 && u.Def.MaxVelocity == 0 {
			// Consider it factory if it has build menu or footprint
			hasFactoryCompleted = true
		}
		if u.Remaining == 0 && u.Def != nil && isCombatUnit(u.Def) {
			hasCombatCompleted = true
		}
	}
	if hasNanoframe {
		m.recordMilestone(MilestoneNanoframeObserved, tick)
	}
	if hasFactoryCompleted {
		// Only record after nanoframe observed to preserve causal order (optional)
		m.recordMilestone(MilestoneFactoryCompleted, tick)
	}
	if hasCombatCompleted {
		m.recordMilestone(MilestoneCombatUnitCompleted, tick)
	}
	// FactoryProductQueued: observe factory order queues.
	if hasFactoryCompleted {
		for _, u := range w.IterSliced() {
			if u == nil || !u.Alive || u.Remaining != 0 {
				continue
			}
			if !m.isAllied(u.Owner) {
				continue
			}
			if u.Def == nil || !u.Def.Builder || u.Def.FootprintX < 4 || u.Def.MaxVelocity != 0 {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			if len(q.Primary()) > 0 {
				m.recordMilestone(MilestoneFactoryProductQueued, tick)
				break
			}
		}
	}
	// GroupAssigned: at least one combat unit assigned to AI groups.
	if len(m.GroupWaveA) > 0 || len(m.GroupWaveB) > 0 || len(m.GroupExplore) > 0 || len(m.GroupRally) > 0 || len(m.GroupRegroupA) > 0 || len(m.GroupRegroupB) > 0 {
		m.recordMilestone(MilestoneGroupAssigned, tick)
	}
}

// ObserveHostileDamage records HostileDamageObserved when damage to a hostile is observed via normal combat [P0-07].
// It is the public hook for combat/impact to notify the manager; session can also call it from world ApplyDamage path.
// Only hostile (non-allied) targets count; allied or self damage does not set the milestone.
func (m *Manager) ObserveHostileDamage(tick uint32, target pool.Handle, w *units.World) {
	if m == nil || w == nil || target == 0 {
		return
	}
	u := w.Unit(target)
	if u == nil {
		return
	}
	if !m.isHostile(u.Owner) {
		return
	}
	m.recordMilestone(MilestoneHostileDamageObserved, tick)
}
