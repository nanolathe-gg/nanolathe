package construction

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

const (
	reclaimCadenceStep    uint32 = 2  // [05 "Unit reclaim"]
	reclaimPulseThreshold uint32 = 14 // [05 "Unit reclaim"]
)

// UnitReclaimPulse computes the one-time pulse stored on a ReclaimUnit order.
// The value is established at order setup, not recomputed as the target loses
// health. All operands are integer fields in the authored/runtime records, and
// the division is the retail truncation boundary [01 §8][05 "Unit reclaim"].
func UnitReclaimPulse(builder, target *units.Unit) int32 {
	if builder == nil || target == nil || builder.Def == nil || target.Def == nil {
		return 1
	}
	metalCost := target.Def.BuildCostMetal
	if metalCost < 10 {
		metalCost = 10
	}
	killsFactor := (builder.Kills + 5) / 5
	if killsFactor < 0 {
		killsFactor = 0
	}
	denom := int64(metalCost) * 300 // [05 "Unit reclaim"]
	if denom <= 0 {
		return 1
	}
	numer := int64(target.MaxHealth) * int64(builder.Def.WorkerTime) * int64(killsFactor) * 15
	pulse := int32(numer / denom) // [01 §8] truncation toward zero
	if pulse < 1 {
		pulse = 1
	}
	return pulse
}

// reclaimInRange applies the established build-distance gate. The retail
// validator adds a target footprint-radius term to BuildDistance; the exact
// Go-side footprint extent mapping is not yet closed, so this intentionally
// uses the proven point-goal radius and records the residual rather than
// inventing a geometry conversion [GAP T25][04 §7.2].
func reclaimInRange(builder, target *units.Unit) bool {
	if builder == nil || target == nil || builder.Def == nil {
		return false
	}
	radius := numeric.FixedFromInt(int64(builder.Def.BuildDistance))
	dx := int64(builder.X - target.X)
	dz := int64(builder.Z - target.Z)
	if radius < 0 {
		radius = -radius
	}
	return dx*dx+dz*dz <= int64(radius)*int64(radius)
}

// reclaimTargetEligible preserves the command-layer target family: this is a
// unit target, unlike feature reclaim. The command resolver's code-12 unit
// branch does not impose an owner comparison; ownership and the target's
// capture-immunity bit are separate fields in the retail handler. The latter
// is not represented by a named UnitDef field yet; do not guess one here [04
// §3.4][05 "Unit reclaim"][GAP T25].
func reclaimTargetEligible(builder, target *units.Unit) bool {
	if builder == nil || target == nil || builder.Def == nil || target.Def == nil {
		return false
	}
	if !builder.Alive || builder.Dying || !target.Alive || target.Dying {
		return false
	}
	if !builder.Def.CanReclamate {
		return false
	}
	return true
}

func isReclaimUnitNode(n *orders.Node) bool {
	if n == nil {
		return false
	}
	name := orders.DescriptorFor(n.ID).Name
	return name == "ReclaimUnit" || name == "VTOL_ReclaimUnit"
}

// stepUnitReclaim advances one ReclaimUnit state-machine visit. It is called
// by Service.StepUnit, after order dispatch and before movement. Out-of-range
// work remains pending so the movement/order integration can approach the
// target when that boundary is wired; no progress, health, cadence, or refund
// is changed while out of range [05 "Unit reclaim"].
func (s *Service) stepUnitReclaim(builder *units.Unit, node *orders.Node, tick uint32) WorkResult {
	res := WorkResult{Builder: builder.Handle, Owner: builder.Owner, State: State(node.Phase), Product: node.Target}
	if !reclaimTargetEligible(builder, s.World.Unit(node.Target)) {
		// A rejected target is a completed/failed command, not a reason to
		// retain a dead or friendly target forever. Queue cleanup is centralized
		// through RemoveHead so active-marker/tombstone behavior stays canonical.
		if q := orders.QueueForUnit(builder); q != nil && q.Head() == node {
			q.RemoveHead()
		}
		res.Product = 0
		return res
	}
	target := s.World.Unit(node.Target)
	if !reclaimInRange(builder, target) {
		node.MoveState = orders.MoveEnRoute
		// TODO(question): the session movement bridge must submit the target
		// point-goal with reclaim's BuildDistance radius. This narrow service
		// leaves the order pending rather than synthesizing a second movement
		// node or applying an unproven approach transform.
		return res
	}
	node.MoveState = orders.MoveArrived
	if node.Deadline >= 0 && tick < uint32(node.Deadline) {
		return res
	}
	if node.Param1 == 0 {
		node.Param1 = uint32(UnitReclaimPulse(builder, target))
	}
	// The order's second accumulator is cadence, not a resource fraction. A
	// valid in-range visit advances it by two; a pulse is applied only after it
	// exceeds fourteen, then the accumulator is reset [05 "Unit reclaim"].
	node.Param2 += reclaimCadenceStep
	if node.Param2 <= reclaimPulseThreshold {
		node.Deadline = int32(tick + reclaimCadenceStep)
		return res
	}
	node.Param2 = 0
	pulse := int32(node.Param1)
	if pulse < 1 {
		pulse = 1
	}
	oldRemaining := target.Remaining
	// Route the pulse through the world's ordinary damage receiver. Besides
	// preserving the packet boundary, this clamps lethal health to zero before
	// the cause-5 death latch [05 "Unit reclaim"][06 §9.1].
	s.World.ApplyDamage(target.Handle, pulse)
	if s.Presentation != nil {
		s.emitReclaimNano(tick, builder, target)
	}
	if target.Health > 0 {
		node.Deadline = int32(tick + reclaimCadenceStep)
		return res
	}
	// Cause-5 is a no-corpse/no-explosion death path. Capture remaining before
	// any death-side hook can mutate it; the metal-only payment follows the
	// cause-5 death latch [05 "Unit reclaim"][06 §12.1].
	s.World.Destroy(target.Handle, units.DeathReclaimed)
	if s.Economy != nil {
		var controller uint8
		if int(builder.Owner) < len(s.Economy.Players) {
			controller = s.Economy.Players[builder.Owner].ControllerState
		}
		// The cause-5 latch is established before its death-side metal payment;
		// no pulse-level resource credit is made [05 "Unit reclaim"][06 §12.1].
		s.Economy.CreditUnitReclaimRefund(builder.Handle, oldRemaining, target.Def.BuildCostMetal, controller)
	}
	// Release only construction-owned derived state. Session death observers
	// may call the same idempotent helper; the second call is a no-op and cannot
	// duplicate occupancy clearing or link cleanup [R-P0-09].
	s.ReleasePlacement(target.Handle)
	delete(s.builderLinks, target.Handle)
	delete(s.getBuiltLinks, target.Handle)
	if q := orders.QueueForUnit(builder); q != nil && q.Head() == node {
		q.RemoveHead()
	}
	res.Product = 0
	res.Completed = true
	return res
}

func (s *Service) emitReclaimNano(tick uint32, builder, target *units.Unit) {
	if s == nil || s.Presentation == nil || builder == nil || target == nil {
		return
	}
	piece, source, ok := s.QueryNanoPiece(builder)
	if !ok {
		return
	}
	// Selector 6 and one event per admitted reclaim pulse are established;
	// exact strip/lifetime/color fields remain presentation TODO [R-P0-06].
	s.Presentation.EmitNanolathe(presentation.Event{
		Tick: tick, Source: builder.Handle, Target: target.Handle, Piece: piece,
		X: source.X(), Y: source.Y(), Z: source.Z(),
		TargetX: target.X, TargetY: target.Y, TargetZ: target.Z,
		EffectID: 6, Mode: 2, Team: builder.Owner,
	})
}
