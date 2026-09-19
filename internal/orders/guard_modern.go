package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern guard assistance".
// A guard keeps its ward and queue while borrowing the patrol pad selection.
// The existing landing and repair executors own travel, attachment and healing.
func modernGuardSeekPad(u *units.Unit, n *Node, tick uint32) bool {
	b := bindingFor(u)
	if b == nil || !b.ModernGuardAssistance || !unitCanFly(u) || !combat.AirBelowThreeQuarters(u) {
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
// in stable enumeration order. Selection is read-only and draws no randomness;
// the ordinary work rows retain their own resource admission and RNG effects.
func modernGuardNearbyWork(u *units.Unit, n *Node, tick uint32) bool {
	b := bindingFor(u)
	if b == nil || !b.ModernGuardAssistance || !canRepairGuard(u) || !hasMover(u) {
		return false
	}
	q := QueueOfUnit(u)
	if resources, ok := playerResources(u); ok && resourceAtLeastTwenty(resources.Stock[1], resources.Capacity[1]) {
		for _, target := range scanRepairCandidates(u, u.Def.SightDistance) {
			if id := Resolve(8, u, target, nil); id != 0 {
				releaseGoalPayload(u, n)
				q.PushHead(id, NewNodeForOrder(id, target.Handle, target.X, target.Y, target.Z, tick, u.Handle, false))
				modernGuardWorkRetry(n, tick)
				return true
			}
		}
	}
	if !canResurrect(u) || b.Work == nil || b.Work.CanResurrectFeature == nil {
		return false
	}
	var candidate FeatureView
	found := false
	b.ForEachFeature(func(feature FeatureView) bool {
		if !feature.Reclaimable || !withinPlanarRadius(u, feature.X, feature.Z, u.Def.SightDistance) || !b.Work.CanResurrectFeature(feature) {
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
	q.PushHead(id, NewNodeForOrder(id, 0, candidate.X, candidate.Y, candidate.Z, tick, u.Handle, false))
	modernGuardWorkRetry(n, tick)
	return true
}

// The landing executor leaves a healed patient attached to its repair pad.
// Only Modern aircraft guarding from such a pad may use the normal takeoff
// preamble to resume; transport cargo keeps the retail carried-guard rejection.
func modernGuardOnRepairPad(u *units.Unit) bool {
	b := bindingFor(u)
	if b == nil || !b.ModernGuardAssistance || !unitCanFly(u) {
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
