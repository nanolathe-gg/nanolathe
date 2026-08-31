package construction

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
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
// the division is the retail truncation boundary [01 §8][05 R-WORK-01 §4]:
//
//	costM = max(target.definition.buildcostmetal, 10)
//	n     = workertime * ((kills + 5) / 5) * target.definition.maxdamage * 15
//	pulse = max(1, trunc(n / (costM * 300)))
//
// Correction (PT3-05): the health operand is the TARGET DEFINITION's
// `maxdamage`, not the instance's current maximum. The two agree for a stock
// unit and part company for anything that has had its maximum adjusted, and §4
// gives the definition word.
func UnitReclaimPulse(builder, target *units.Unit) int32 {
	if builder == nil || target == nil || builder.Def == nil || target.Def == nil {
		return 1
	}
	metalCost := target.Def.BuildCostMetal
	if metalCost < 10 {
		metalCost = 10
	}
	// The kill divisor is a signed integer division by five [05 R-WORK-01 §4].
	killsFactor := (builder.Kills + 5) / 5
	if killsFactor < 0 {
		killsFactor = 0
	}
	denom := int64(metalCost) * 300 // [05 R-WORK-01 §4]
	if denom <= 0 {
		return 1
	}
	maxDamage := target.Def.MaxDamage
	if maxDamage <= 0 {
		maxDamage = target.MaxHealth
	}
	numer := int64(maxDamage) * int64(builder.Def.WorkerTime) * int64(killsFactor) * 15
	pulse := int32(numer / denom) // [01 §8] truncation toward zero
	if pulse <= 1 {
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

// reclaimTargetEligible is the eligibility predicate the executor consults at
// start and re-checks on every work visit [05 R-WORK-01 §4]:
//
//	builder.definition.canreclamate      // capability bit
//	&& (target.status & 3) != 2          // mover mode mirror: not airborne
//	&& !target.definition.cancapture     // commanders cannot be reclaimed
//
// Correction (PT3-05). The previous text said the target's "capture-immunity
// bit ... is not represented by a named UnitDef field yet; do not guess one
// here", and the predicate therefore tested neither target clause: any live
// unit, including a flying one and including a commander, was reclaimable.
// [05 R-WORK-01 §4] closes both. There is no separate capture-immunity flag —
// the predicate reads the SAME `cancapture` key the builder side reads for its
// own capture capability and demands it be clear, which is why a unit authored
// `cancapture=1` is both un-capturable and un-reclaimable, and in stock content
// that is exactly the commanders. The mover-mode clause is the low two bits of
// the target's status word, which units.Unit mirrors as Move.Mode
// [04 R-MOV-01 §8]: 0 none, 1 grounded, 2 airborne.
//
// The command resolver's code-12 unit branch still imposes no owner comparison
// [04 §3.4], so an own unit is a legal reclaim target; that half is unchanged.
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
	if target.Move.Mode&0x3 == 2 {
		return false // airborne [05 R-WORK-01 §4]
	}
	if target.Def.CanCapture {
		return false // the same bit the capture executor rejects [05 R-WORK-01 §4]
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
		// The Reclaim handler's StartBuilding emission sits on the setup visit
		// that establishes the pulse — one of the nine nanolathe/assist sites
		// [R-ORDER-02 §2]. The order-record emitter arranges the name-form
		// StartBuilding and sets the record's StopBuilding-pending flag, so
		// cleanup emits the counterpart on every removal path. Per-activation
		// placement follows the flag's one-counterpart-per-record purpose; the
		// per-visit frequency residual is an Unknown in [04 "Missing and
		// unknown"].
		orders.EmitStartBuilding(builder, node)
	}
	// The order's second accumulator is cadence, not a resource fraction. The
	// visit order is [05 R-WORK-01 §4]'s: the pulse test comes FIRST, on the
	// counter as the visit found it; a firing visit zeroes the counter; then
	// every qualifying visit — firing or not — emits one nano segment,
	// reschedules two ticks, and raises the counter by two.
	//
	// Correction (PT3-05). This used to raise the counter before the test and
	// hold whenever the raised value was still at or below fourteen. That put
	// the first bite one visit early (tick 14 rather than tick 16) and, worse,
	// emitted the nano segment only on the visit that landed a bite, where §4
	// gives "one nano segment per qualifying work visit ... a one-segment /
	// two-tick presentation cadence, distinct from the damage-pulse gate" — so
	// seven of every eight visits drew no nanolathe at all.
	var oldRemaining float32
	fired := false
	if node.Param2 > reclaimPulseThreshold {
		node.Param2 = 0
		pulse := int32(node.Param1)
		if pulse < 1 {
			pulse = 1
		}
		oldRemaining = target.Remaining
		target.LastDamageSide = builder.Owner
		target.LastDamageCause = 5
		// Route the pulse through the world's ordinary damage receiver. Besides
		// preserving the packet boundary, this clamps lethal health to zero
		// before the cause-5 death latch [05 "Unit reclaim"][06 §9.1].
		s.World.ApplyDamage(target.Handle, pulse)
		fired = true
	}
	if s.Presentation != nil {
		s.emitReclaimNano(tick, builder, target)
	}
	if !fired || target.Health > 0 {
		node.Param2 += reclaimCadenceStep
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
	// Selector 6 and one event per admitted reclaim pulse are established. Unit
	// reclaim reverses the ordinary work direction: the target box is the
	// source and the builder's QueryNanoPiece is the destination [05 R-WORK-01
	// §8].
	s.Presentation.EmitNanolathe(frame.Event{
		Tick: tick, Source: builder.Handle, Target: target.Handle, Piece: piece,
		X: target.X, Y: target.Y, Z: target.Z,
		TargetX: source.X(), TargetY: source.Y(), TargetZ: source.Z(),
		EffectID: 6, Mode: 2, Team: builder.Owner,
		Producer:               frame.ProducerBeam,
		PaletteRow:             6,
		NanolatheActiveUntil:   tick + 900,
		NanolatheGeometryKnown: true,
	})
}
