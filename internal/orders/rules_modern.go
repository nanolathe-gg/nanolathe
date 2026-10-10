// The approved Modern order policies, gathered behind the package's rule seam.
// Each body is the one that previously ran behind a mode projection on the
// queue binding; reaching a method here already means Modern is selected, so
// the projection test is gone and nothing else changed.

package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ModernRules carries the approved Modern order policies. It holds no state:
// every body reads the acting unit's own queue binding, so one value serves a
// whole session and is used by pointer.
type ModernRules struct{ CommunityRules }

// Modern lets the pointer target the visible sunken wreck, independently of
// the renderer (interface design "Modern submerged wreck picking").
func (*ModernRules) PicksSubmergedWrecks() bool { return true }

// Modern's Hold Fire keeps automatic combat off the weapon slots, because the
// launch gate lets a slot an order holds fire through it. It closes the guard's
// forced combat join, which retail's force flag bypasses — the guard keeps its
// follow/assistance record and its ordinary movement and repair behavior — and
// the stationary guard's takeover, and it retires running automatic combat at
// the stance write (retireAutomaticCombat).
// Nanolathe Modern policy: docs/DESIGN_UNITS_ORDERS_COB.md "Modern Hold Fire".
func (*ModernRules) HoldsFire(u *units.Unit) bool {
	return u != nil && u.Flags>>stanceFireShift&stanceFieldMask == 0
}

// An accepted Modern bombing pass reaches release and finishes overflight
// before its return-to-post leash applies. Phase 6 is dispatched after
// overflight; ordinary completion then clears weapon targets and resumes the
// queued return move. Target and cancel checks still precede this decision.
// Nanolathe Modern policy: DESIGN_MOVEMENT_PATH §3.4.1.
func (*ModernRules) DeferBomberLeash(u *units.Unit, n *Node) bool {
	return n != nil && n.Phase >= 1 && n.Phase <= 5 && DescriptorFor(n.ID).Name == "AirStrike"
}

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern guard assistance".
// A guard keeps its ward and queue while borrowing the patrol pad selection.
// The existing landing and repair executors own travel, attachment and healing.
func (*ModernRules) GuardSeeksPad(u *units.Unit, n *Node, tick uint32) bool {
	b := bindingFor(u)
	if b == nil || !unitCanFly(u) || !combat.AirBelowThreeQuarters(u) {
		return false
	}
	pad := pickCandidate(u, airBasePads(u))
	if pad == nil {
		return false
	}
	releaseGoalPayload(u, n)
	if !spawnPatrolLanding(u, pad, tick) {
		return false
	}
	modernGuardWorkRetry(n, tick)
	return true
}

// Modern guards finish their ward's assistance first, then select nearby work
// in stable enumeration order. A ward that is a factory with production still
// queued is never idle, even between products, so its guard keeps following
// and assisting it. Selection is read-only and draws no randomness; the
// ordinary work rows retain their own resource admission and RNG effects.
func (*ModernRules) GuardWorksNearby(u *units.Unit, n *Node, tick uint32) bool {
	b := bindingFor(u)
	if b == nil || !canRepairGuard(u) || !hasMover(u) || !rulesOfUnit(u).AllowAutomaticRepair(u, tick) {
		return false
	}
	ward := getLookupForWard(n, u)
	if ward == nil || wardHasQueuedProduction(ward) {
		return false
	}
	q := QueueOfUnit(u)
	if resources, ok := playerResources(u); ok && resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1]) {
		for _, target := range scanModernGuardRepairCandidates(u) {
			if !modernWithinPoint(ward.X, ward.Z, target.X, target.Z, modernWorkRadius) {
				continue
			}
			if id := Resolve(8, u, target, nil); id != 0 {
				releaseGoalPayload(u, n)
				work := NewNodeForOrder(id, target.Handle, target.X, target.Y, target.Z, tick, u.Handle, false)
				work.automaticWork = true
				work.workAssignment = n
				q.PushHead(id, work)
				modernGuardWorkRetry(n, tick)
				return true
			}
		}
	}
	if !canResurrect(u) || b.Work == nil || b.Work.CanResurrectFeatureHook() == nil {
		return false
	}
	var candidate FeatureView
	found := false
	b.ForEachFeature(func(feature FeatureView) bool {
		if !feature.Reclaimable || !withinPlanarRadius(u, feature.X, feature.Z, u.Def.SightDistance) || !modernWithinPoint(ward.X, ward.Z, feature.X, feature.Z, modernWorkRadius) || !b.Work.CanResurrectFeatureHook()(feature) {
			return scanNext
		}
		candidate, found = feature, true
		return scanStop
	})
	if !found {
		return false
	}
	id := Lookup("Resurrect")
	releaseGoalPayload(u, n)
	work := NewNodeForOrder(id, 0, candidate.X, candidate.Y, candidate.Z, tick, u.Handle, false)
	work.automaticWork, work.workAssignment = true, n
	q.PushHead(id, work)
	modernGuardWorkRetry(n, tick)
	return true
}

// wardHasQueuedProduction reports whether the ward is a factory with a factory
// build record still in its front segment. Between two products that record
// has no target while the last product clears the pad, so the guard's
// join-the-ward's-order leg finds nothing to join [04 R-UNIT-06 §1], but the
// factory is not idle. Rally records do not count: a factory whose production
// has drained keeps only those.
// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern guard assistance".
func wardHasQueuedProduction(ward *units.Unit) bool {
	q := QueueOfUnit(ward)
	if q == nil {
		return false
	}
	for _, rec := range q.primary {
		if rec != nil && rec.ID == rowBuildingBuild && rec.Flags&FlagTombstone == 0 {
			return true
		}
	}
	return false
}

// The landing executor leaves a healed patient attached to its repair pad.
// Only Modern aircraft guarding from such a pad may use the normal takeoff
// preamble to resume; transport cargo keeps the retail carried-guard rejection.
func (*ModernRules) GuardResumesFromPad(u *units.Unit) bool {
	b := bindingFor(u)
	if b == nil || !unitCanFly(u) {
		return false
	}
	pad := lookupTarget(u, u.Attachment.Carrier)
	return pad != nil && pad.Def != nil && pad.Def.Builder && pad.Def.IsAirBase
}

// A failed landing or unreachable work target may complete in the same pump.
// Keep the retained guard behind its ordinary maintenance interval so it cannot
// select that same job repeatedly before simulation time advances.
func modernGuardWorkRetry(n *Node, tick uint32) {
	n.DynamicGate = gateDeadline
	n.Deadline = int32(tick + 30)
}

// Nanolathe Modern policy: guard assistance keeps its unit-slot first pick and
// whole-position sight test (DESIGN_UNITS_ORDERS_COB "Modern guard assistance").
// Patrol's retail spatial gather has a different ordering and arithmetic.
func scanModernGuardRepairCandidates(u *units.Unit) []*units.Unit {
	b := bindingFor(u)
	if b == nil || b.World == nil || b.World.ForEachUnitHook() == nil {
		return nil
	}
	var out []*units.Unit
	b.ForEachUnit(func(h pool.Handle, candidate *units.Unit) bool {
		if candidate != nil && candidate.Alive && repairCandidate(b, u, h, candidate) && withinPlanarRadius(u, candidate.X, candidate.Z, u.Def.SightDistance) {
			out = append(out, candidate)
		}
		return scanNext
	})
	return out
}
