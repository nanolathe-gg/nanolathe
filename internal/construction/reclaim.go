package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const (
	reclaimCadenceStep    uint32 = 2  // [05 "Unit reclaim"]
	reclaimPulseThreshold uint32 = 14 // [05 "Unit reclaim"]
	// The ground working phase restarts behind this explicit wait. The air
	// counterpart uses thirty ticks [04 R-ORD-01 §5][04 R-ORD-01 §7].
	reclaimRestartDelay uint32 = 15
)

// reclaimPulseFactor is the `k` of [05 R-WORK-01 §4]'s pulse helper, 15 at its
// only call site.
const reclaimPulseFactor int32 = 15

// UnitReclaimPulse computes the one-time pulse stored on a ReclaimUnit order.
// The value is established at order setup, not recomputed as the target loses
// health [05 R-WORK-01 §4]:
//
//	costM = max(target.definition.buildcostmetal, 10.0f)
//	n     = (int32)( (uint16)builder.definition.workertime
//	               * ((int32)((uint16)builder.kills + 5) / 5)
//	               * (int32)target.definition.maxdamage
//	               * 15 )                        // 32-bit SIGNED product
//	v     = trunc( (double)(uint32)n / (costM * 300.0f) )
//	pulse = (v <= 1) ? 1 : v
//
// Correction (PT3-05): the health operand is the TARGET DEFINITION's
// `maxdamage`, not the instance's current maximum. The two agree for a stock
// unit and part company for anything that has had its maximum adjusted, and §4
// gives the definition word.
//
// CORRECTION (AU-7) — THE PRODUCT WRAPS AT 32 BITS AND IS RE-READ UNSIGNED.
// This used to form the product in int64, which cannot overflow. §4 is explicit
// that it is a 32-bit signed multiply and that the widening to 64 bits happens
// AFTER the wrap, with a zero high word: "the 32-bit product can overflow
// silently for large `workertime × maxdamage`; because it is then re-read as an
// *unsigned* 32-bit quantity, an overflowed product becomes a very large
// positive pulse rather than a negative one." The int64 form quietly produced a
// modest pulse exactly where retail produces an enormous one — a Krogoth-class
// target (maxdamage in the tens of thousands) reclaimed by a veteran builder is
// inside stock reach, and there the two answers are a slow reclaim versus a
// target deleted by the first pulse.
//
// Two-complement multiplication is associative and commutative modulo 2^32, so
// the operand order below is §4's for readability, not for the value.
//
// The divisor is FLOATING: `costM` is the definition's metal cost taken as a
// single-precision maximum against 10, multiplied by 300 in single precision,
// and the (already unsigned) numerator is widened to double and divided by it,
// with one truncation toward zero [01 §8]. The integer division this used to do
// agrees over the authored range — every stock `costM × 300` is a whole number
// well inside float32's exact-integer range — but it is not what §4 gives.
//
// The kill count is read as a uint16 before the `+5` and the signed divide by
// five, exactly as §4 writes it; a `killsFactor < 0` clamp that used to sit here
// was ours and is gone with the unsigned read that makes it unreachable.
func UnitReclaimPulse(builder, target *units.Unit) int32 {
	if builder == nil || target == nil || builder.Def == nil || target.Def == nil {
		return 1
	}
	// costM = max(buildcostmetal, 10.0f), in single precision [05 R-WORK-01 §4].
	metalCost := target.Def.BuildCostMetal
	if metalCost < 10 {
		metalCost = 10
	}
	// The kill divisor is a signed integer division by five over an unsigned
	// 16-bit read of the kill count [05 R-WORK-01 §4].
	killsFactor := (int32(uint16(builder.Kills)) + 5) / 5
	// The 32-bit signed product. Go's int32 multiply wraps, which IS the retail
	// multiply; the re-read as uint32 is the zero-high-word widening.
	//
	// The third operand is `(int32)target.definition.maxdamage` — the
	// DEFINITION word and nothing else [05 R-WORK-01 §4]. A substitution of the
	// target's live maximum health for a non-positive `maxdamage` used to sit
	// here; it was ours, not §4's, and §4 needs no such arm: a zero definition
	// word zeroes the product, and the pulse's own `(v <= 1) ? 1 : v` clamp
	// below is the established answer for it.
	n := int32(uint16(builder.Def.WorkerTime)) * killsFactor
	n *= target.Def.MaxDamage
	n *= reclaimPulseFactor
	v := float64(uint32(n)) / float64(metalCost*300)
	pulse := int32(v) // [01 §8] truncation toward zero
	if pulse <= 1 {
		pulse = 1
	}
	return pulse
}

// reclaimTargetRadius is the per-target-definition reach term unit reclaim adds
// to the builder's `builddistance` [05 R-WORK-01 §2][04 R-ORD-01 §5]: "the
// target's model radius the whole part of the definition's
// `(Xextent + Zextent)/3` word".
//
// The word is the unit-record compiler's, not a model measurement
// [02 R-CAT-01 §7]: the bounding record's X and Z bounds are footprint-derived
// `±(Footprint << 20)/2`, so the two extents are `FootprintX << 20` and
// `FootprintZ << 20` in 16.16, their sum is divided by three as an integer, and
// the reach term is the high half of that word — the radius in whole world
// units. Only ground unit reclaim uses it; the build, repair and assist reach of
// [05 R-WORK-01 §12] point 3 has no model radius at all.
func reclaimTargetRadius(def *content.UnitDef) int32 {
	if def == nil {
		return 0
	}
	min, max := def.BoundingExtents()
	// Formed at the definition compiler's own 32-bit width [02 R-CAT-01 §7].
	word := (max[0] - min[0] + max[2] - min[2]) / 3
	return int32(int16(word >> 16))
}

// reclaimInRange is the `ReclaimUnit` work visit's reach test, exactly
// [05 R-WORK-01 §2][04 R-ORD-01 §5]:
//
//	r  = (uint16)builder.definition.builddistance + (int16)targetRadius
//	in = ((dx*dx) >> 32) + ((dz*dz) >> 32) <= r*r
//
// It is a squared form in whole world units, with each square truncated
// separately out of its own 64-bit product — not the planar
// centre-minus-half-diagonals form the build, repair and assist rows share.
//
// CORRECTION (WU-19-166). This compared `builddistance` in 16.16 against the
// squared 16.16 separation with no target term at all, under a note claiming
// "the retail validator adds a target footprint-radius term ... the exact
// Go-side footprint extent mapping is not yet closed [GAP T25]". It is closed,
// and was before that note was written: [05 R-WORK-01 §2] gives the expression
// above, [04 R-ORD-01 §5] names the radius word, and [02 R-CAT-01 §7] derives
// it from the footprint. Dropping the term left every reclaim reach short by
// the target's own radius — ten world units for a one-cell target, twenty-one
// for a two-cell one.
func reclaimInRange(builder, target *units.Unit) bool {
	if builder == nil || target == nil || builder.Def == nil || target.Def == nil {
		return false
	}
	dx := int64(builder.X) - int64(target.X)
	dz := int64(builder.Z) - int64(target.Z)
	r := int64(uint16(builder.Def.BuildDistance)) + int64(reclaimTargetRadius(target.Def))
	return (dx*dx)>>32+(dz*dz)>>32 <= r*r
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

// installReclaimApproachGoal is the `ReclaimUnit` row's own approach mechanism
// [04 R-ORD-01 §5]: "not yet arrived (`0x20` absent) → rectangle goal on the
// TARGET footprint". It is the same [04 R-PATH-01 §12] goal the mobile-build
// approach installs and the feature `Reclaim` row installs on a feature
// footprint — internal/movement's constructor grows the argument rectangle by
// the MOVER's own footprint, so the enumerated border is the ring of anchor
// cells at which the reclaimer stands flush against the target and the target's
// own occupied cells are interior, never enumerated [04 R-PATH-01 §12].
//
// Every non-arrived phase-1 visit installs the rectangle afresh, so a moving
// target updates the approach at the row's fifteen-tick cadence. The movement
// installer owns eviction and event clearing [04 R-ORD-01 §1].
//
// An aircraft installs nothing here. The five VTOL work twins build an air
// marker in their own phase 0 and never take a ground goal [04 R-ORD-01 §7],
// which is the same reason needsApproach exempts a flying builder.
func (s *Service) installReclaimApproachGoal(builder *units.Unit, node *orders.Node, target *units.Unit) bool {
	if s == nil || s.Movement == nil || builder == nil || node == nil || target == nil {
		return false
	}
	if builder.Def == nil || target.Def == nil || builder.Def.CanFly {
		return false
	}
	cellX, cellZ, ok := s.unitFootprintAnchor(target, target.X, target.Z)
	if !ok {
		return false
	}
	footX, footZ := world.FootprintForUnit(s.Catalog, target.Def)
	return s.Movement.InstallRectangleGoal(orders.RectangleGoalRequest{
		Owner: builder.Handle,
		Node:  node,
		CellX: cellX,
		CellZ: cellZ,
		Width: footX,
		Depth: footZ,
	})
}

// stepUnitReclaim dispatches through the ordinary queue gate and result-code
// machinery, but only during the construction-owned unit window [04 §3.3].
func (s *Service) stepUnitReclaim(builder *units.Unit, node *orders.Node, tick uint32) WorkResult {
	res := WorkResult{Builder: builder.Handle, Owner: builder.Owner, State: State(node.Phase), Product: node.Target}
	q := orders.QueueForUnit(builder)
	if q == nil || q.Head() != node {
		return res
	}
	previous := s.reclaimStepNode
	s.reclaimStepNode = node
	defer func() { s.reclaimStepNode = previous }()
	orders.ContinueUnitReclaim(builder, node, tick)
	res.State = State(node.Phase)
	if q.Head() != node {
		res.Product = 0
	}
	return res
}

// unitReclaimVisit keeps the ground and air phase structures distinct while
// sharing only the pulse visit [04 R-ORD-01 §5][04 R-ORD-01 §7].
func (s *Service) unitReclaimVisit(builder *units.Unit, node *orders.Node, satisfied, tick uint32) orders.Code {
	target := s.World.Unit(node.Target)
	if target == nil || satisfied&0x10008 != 0 {
		return 5
	}
	air := orders.DescriptorFor(node.ID).Name == "VTOL_ReclaimUnit"
	if node.Phase == 0 {
		if builder.Def == nil || builder.Def.BMCode != 1 || (air && !builder.Def.CanFly) {
			if !air {
				orders.NotifyStatus(builder, 7, "Reclamation failed")
			}
			return 7
		}
		if !builder.Def.CanReclamate {
			orders.NotifyStatus(builder, 7, "Reclamation failed")
			return 7
		}
		if !reclaimTargetEligible(builder, target) {
			orders.NotifyStatus(builder, 7, "That unit cannot be reclaimed")
			if !air {
				orders.NotifyStatus(builder, 7, "Reclamation failed")
			}
			return 8
		}
	}
	if air {
		switch node.Phase {
		case 0:
			return orders.AirUnitReclaimSetup(builder, node, target)
		case 1:
			node.Param1, node.Param2 = uint32(UnitReclaimPulse(builder, target)), 0
			return orders.AirUnitReclaimSetup(builder, node, target)
		case 2:
			if satisfied&0x40 != 0 {
				return 9
			}
			node.DynamicGate |= 0x10008
		default:
			return 7
		}
	} else {
		switch node.Phase {
		case 0, 2, 3, 4:
			return orders.GroundUnitReclaimSetup(builder, node, satisfied, tick)
		case 1:
			if satisfied&0x20 != 0 {
				node.MoveState = orders.MoveArrived
				return 1
			}
			if !s.installReclaimApproachGoal(builder, node, target) {
				return 7
			}
			node.MoveState = orders.MoveEnRoute
			node.Param1, node.Param2 = uint32(UnitReclaimPulse(builder, target)), 0
			node.DynamicGate |= 0x100e8 | 1
			node.Deadline = int32(tick + reclaimRestartDelay)
			return 2
		case 5:
		default:
			return 7
		}
	}
	var inRange bool
	if air {
		dx, dz := int64(builder.X)-int64(target.X), int64(builder.Z)-int64(target.Z)
		r := int64(uint16(builder.Def.BuildDistance))
		inRange = (dx*dx)>>32+(dz*dz)>>32 <= r*r
	} else {
		inRange = reclaimInRange(builder, target)
	}
	if !inRange || !reclaimTargetEligible(builder, target) {
		delay := reclaimRestartDelay
		if air {
			delay = 30
		} else {
			orders.EmitStopBuilding(builder, node)
		}
		node.DynamicGate |= 1
		node.Deadline = int32(tick + delay)
		return 0 // restart, retaining the row's explicit wait [04 §3.3]
	}
	node.MoveState = orders.MoveArrived
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
	if node.Param2 > reclaimPulseThreshold {
		node.Param2 = 0
		pulse := int32(node.Param1)
		if pulse < 1 {
			pulse = 1
		}
		// A reclaim pulse is an ordinary locally delivered kind-5 packet. The
		// combat receiver applies defender-side scaling, records provenance, and
		// latches a lethal result for the later death finalizer [05 R-WORK-01
		// §4][06 §9.1][06 §9.2].
		s.Combat.AcceptDamage(s.World, tick, combat.DamageInput{
			Victim: target.Handle, Attacker: builder.Handle, Nominal: pulse,
			Kind: uint8(combat.CauseReclaim),
		})
	}
	// "both pass → ... stamp `tick + 900`; spray" — one of the ten reveal-stamp
	// handler sites [04 R-ORD-01 §5 "The reveal stamp"]. The write is an
	// outright store to the one shared reveal/cloak deadline field on every
	// qualifying visit (fired or not), never a maximum [03 R-VIS-01 §6]; its
	// only reader is the cloak debit gate [05 R-ECO-01 §9].
	if !air {
		builder.RevealDeadline = tick + 900
	}
	if s.Presentation != nil {
		s.emitReclaimNano(tick, builder, target)
	}
	// The pulse does not own fatal cleanup. Even after a lethal packet, this
	// visit completes its cloak, presentation, reschedule, and counter update;
	// phase 2 later finalizes the death and its cause-5 refund [05 R-WORK-01
	// §4][06 §12.1].
	node.Param2 += reclaimCadenceStep
	node.Deadline = int32(tick + reclaimCadenceStep)
	node.DynamicGate |= 1
	return 2
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
	//
	// The box is the target unit's own — its world position plus the six signed
	// extents of its definition's bounding record [02 R-CAT-01 §7] — and it has
	// to ride the event, because the flag is what tells presentation which end
	// carries the extent. Publishing the corner pair alone left the client
	// re-deriving the bounds and reading them as the DESTINATION, so a reclaim
	// spray was born and died inside the victim and never reached the builder.
	boxMin, boxMax := target.NanolatheBox()
	s.Presentation.EmitNanolathe(frame.Event{
		Tick: tick, Source: builder.Handle, Target: target.Handle, Piece: piece,
		X: boxMin[0], Y: boxMin[1], Z: boxMin[2],
		TargetX: source.X(), TargetY: source.Y(), TargetZ: source.Z(),
		EffectID: 6, Mode: 2, Team: builder.Owner,
		Producer:                frame.ProducerBeam,
		PaletteRow:              6,
		NanolatheActiveUntil:    tick + 900,
		NanolatheGeometryKnown:  true,
		NanolatheTargetBoxKnown: true,
		NanolatheTargetMin:      boxMin,
		NanolatheTargetMax:      boxMax,
		NanolatheBoxAtSource:    true,
	})
}
