package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern danger response".
// These are prototype tuning, not claims about retail behavior.
const dangerMemoryTicks uint32 = 180
const dangerDecisionTicks uint32 = 30
const dangerRetreatDistance int64 = 64
const dangerImpactDistance int32 = 256

type dangerContact struct {
	unit        *units.Unit // identity as well as handle: a reused pool slot is a new unit
	handle      pool.Handle
	tick        uint32
	failedUntil uint32
	x, z        numeric.Fixed // last visible position; never updated while hidden
}

// An anonymous impact records only a local hazard point; there is no identity
// to resolve, track, attack or invalidate when a hidden unit moves or dies.
type dangerImpact struct {
	valid  bool
	sector uint8
	tick   uint32
	x, z   numeric.Fixed
}

type dangerState struct {
	impacts                   [4]dangerImpact
	contacts                  [4]dangerContact
	response                  *Node
	resume                    *Node
	anchorX, anchorY, anchorZ numeric.Fixed
	anchored                  bool
	withdrew                  bool
	returnMove                *Node
	nextDecision              uint32
	quietUntil                uint32
	opportunityTarget         pool.Handle
}

// ObserveDanger accepts only locally observed hostile launch/damage events.
// The session binds the queue first; neither entry creates one for Strict.
func ObserveDanger(victim, attacker *units.Unit, tick uint32) {
	rulesOfUnit(victim).ObserveDanger(victim, attacker, tick)
}

// ObserveImpact receives the observed incoming bearing from combat, composed
// by session without an attacker reference. Modern uses this local direction
// only for withdrawal (DESIGN_UNITS_ORDERS_COB "Modern danger response").
func ObserveImpact(u *units.Unit, bearing numeric.Angle, tick uint32) {
	rulesOfUnit(u).ObserveImpact(u, bearing, tick)
}

func (*ModernRules) ObserveImpact(u *units.Unit, bearing numeric.Angle, tick uint32) {
	q := QueueOfUnit(u)
	if q == nil || u == nil || !u.Alive || u.Remaining != 0 {
		return
	}
	// Prototype tuning: group repeated impacts by eight compass sectors and
	// keep at most four directions, replacing the oldest with stable tie order.
	sector := uint8((uint32(bearing)+4096)>>13) & 7
	index := -1
	for i, point := range q.danger.impacts {
		if point.valid && point.sector == sector {
			index = i
			break
		}
	}
	if index < 0 {
		index = 0
		for i, point := range q.danger.impacts {
			if !point.valid || tick-point.tick >= dangerMemoryTicks {
				index = i
				break
			}
			if tick-point.tick > tick-q.danger.impacts[index].tick {
				index = i
			}
		}
	}
	q.danger.impacts[index] = dangerImpact{
		valid: true, sector: sector, tick: tick,
		x: u.X + numeric.FixedFromInt(int64(numeric.MulRound(numeric.Sin(bearing), dangerImpactDistance))),
		z: u.Z + numeric.FixedFromInt(int64(numeric.MulRound(numeric.Cos(bearing), dangerImpactDistance))),
	}
}

// StepDangerResponse runs immediately before the ordinary primary order pump.
func StepDangerResponse(u *units.Unit, tick uint32) {
	rulesOfUnit(u).StepDangerResponse(u, tick)
}

func (*ModernRules) ObserveDanger(u, attacker *units.Unit, tick uint32) {
	q := QueueOfUnit(u)
	if q == nil || u == nil || !u.Alive || u.Remaining != 0 || attacker == nil || attacker == u || !attacker.Alive {
		return
	}
	b := q.Binding()
	if b == nil || b.DangerVisible == nil || !b.DangerVisible(u, attacker) || !scanHostile(b, u, attacker) {
		return
	}
	slot := -1
	for i := range q.danger.contacts {
		if q.danger.contacts[i].unit == attacker {
			slot = i
			break
		}
	}
	if slot < 0 {
		slot = 0
		for i := range q.danger.contacts {
			c := &q.danger.contacts[i]
			if c.unit == nil {
				slot = i
				break
			}
			if tick-c.tick > tick-q.danger.contacts[slot].tick {
				slot = i
			}
		}
	}
	failed := uint32(0)
	if q.danger.contacts[slot].unit == attacker {
		failed = q.danger.contacts[slot].failedUntil
	}
	q.danger.contacts[slot] = dangerContact{unit: attacker, handle: attacker.Handle, tick: tick, failedUntil: failed, x: attacker.X, z: attacker.Z}
}

func (*ModernRules) ProtectWorkOnDamage(u *units.Unit) bool {
	q := QueueOfUnit(u)
	return protectedDangerWork(q) || q != nil && (q.danger.response != nil || len(q.primary) > 0 && dangerEligibleHead(q))
}

// KeepsMoveOnDamage is Modern AI move retention (Nanolathe Modern policy,
// user-authorized 2026-09-25; DESIGN_UNITS_ORDERS_COB "Modern AI move
// retention"): the construction throttle's purge [08 R-AI-01 §11] spares the
// queue of a Modern AI player's unit whose running record is a move, so a
// commander or constructor that controller sends away from fire keeps going.
// A Classic player's unit is purged as before. It reads the queue and the
// binding's player predicate only: no draw, no resource, no state.
func (*ModernRules) KeepsMoveOnDamage(u *units.Unit) bool {
	if u == nil {
		return false
	}
	b := bindingFor(u)
	if b == nil || b.ModernAIPlayer == nil || !b.ModernAIPlayer(u.Owner) {
		return false
	}
	return runningMove(QueueOfUnit(u))
}

// runningMove reports whether q's running record, past the temporary control
// records, is a move record.
func runningMove(q *Queue) bool {
	if q == nil {
		return false
	}
	for _, n := range q.primary {
		if n == nil {
			continue
		}
		name := DescriptorFor(n.ID).Name
		if temporaryControlRecord(name) {
			continue
		}
		return name == "Move_Ground" || name == "VTOL_Move"
	}
	return false
}

// temporaryControlRecord names the records the protected-work reads skip to
// reach the running assignment: activation, cloak and the two standing
// orders, which finish at once, and a stun, which holds the unit without
// replacing its work.
func temporaryControlRecord(name string) bool {
	switch name {
	case "Activate", "Deactivate", "Cloak_On", "Cloak_Off", "Standing_MoveOrder", "Standing_FireOrder", "Paralyze":
		return true
	}
	return false
}

func protectedDangerWork(q *Queue) bool {
	if q == nil {
		return false
	}
	// Read the running chain, skipping temporary control records. A later
	// queued assignment is not current work. Construction owns its own approach
	// payload; only an already started work parent behind a move child qualifies.
	for i, n := range q.primary {
		if n == nil {
			continue
		}
		name := DescriptorFor(n.ID).Name
		if temporaryControlRecord(name) {
			continue
		}
		switch name {
		case "MobileBuild", "VTOL_MobileBuild", "BuildingBuild", "HelpBuild", "VTOL_HelpBuild", "GetBuilt":
			return true
		case "RepairUnit", "RepairUnitNoMove", "VTOL_RepairUnit", "SelfRepair":
			return !n.automaticWork
		case "Move_Ground", "VTOL_Move":
			if i+1 < len(q.primary) && q.primary[i+1] != nil && q.primary[i+1].Phase > 0 {
				continue
			}
		}
		return false
	}
	return false
}

func (*ModernRules) AllowAutomaticRepair(u *units.Unit, tick uint32) bool {
	q := QueueOfUnit(u)
	if q == nil {
		return true
	}
	if q.danger.response != nil || q.danger.quietUntil != 0 && int32(tick-q.danger.quietUntil) < 0 {
		return false
	}
	for _, point := range q.danger.impacts {
		if point.valid && tick-point.tick < dangerMemoryTicks {
			return false
		}
	}
	for _, c := range q.danger.contacts {
		if c.unit != nil && tick-c.tick < dangerMemoryTicks {
			return false
		}
	}
	return true
}

// Every producer command wins, including queued commands and stance changes.
// Handler spawns use PushHead and do not enter this boundary.
func (*ModernRules) BeforeCommand(q *Queue) {
	if q == nil {
		return
	}
	q.finishDangerResponse()
	if n := q.danger.returnMove; q.containsDangerNode(n) {
		q.cleanupNode(n)
		q.spliceOutPrimary(n)
	}
	q.danger = dangerState{}
}

func (q *Queue) containsDangerNode(n *Node) bool {
	if n == nil {
		return false
	}
	for _, p := range q.primary {
		if p == n {
			return true
		}
	}
	return false
}

func (q *Queue) finishDangerResponse() {
	n := q.danger.response
	q.danger.response = nil
	if q.containsDangerNode(n) {
		if len(q.primary) > 0 && q.primary[0] != n {
			n.Flags |= FlagTombstone
		}
		q.cleanupNode(n)
		q.spliceOutPrimary(n)
	}
	// A suspended assignment lost its movement payload. Restart its own handler
	// so patrol/guard reconstructs that goal instead of waiting for a lost wake.
	if n = q.danger.resume; q.containsDangerNode(n) {
		n.Phase, n.DynamicGate, n.Satisfied, n.MoveState = 0, 0, 0, 0
		n.PathStatus = 0
		n.Deadline = -1
	}
	q.danger.resume = nil
}

func dangerEligibleHead(q *Queue) bool {
	if len(q.primary) == 0 {
		return true
	}
	n := q.primary[0]
	if n == nil {
		return false
	}
	if n.automaticWork {
		return true
	}
	switch DescriptorFor(n.ID).Name {
	case "Move_Ground", "VTOL_Move":
		return dangerMovementFailed(n)
	case "Standby", "Standby_Mine", "VTOL_Standby", "Patrol", "RepairPatrol", "VTOL_Patrol", "VTOL_RepairPatrol", "Follow_Ground", "VTOL_Follow", "Guard_NoMove":
		return true
	}
	// Direct move, attack, reclaim, repair and unknown/restored orders win.
	return false
}

func (q *Queue) suspendDangerAssignment(u *units.Unit) {
	// Automatic repair's patient is expendable; its underlying patrol/guard and
	// later explicit commands are retained. Construction was rejected above.
	for len(q.primary) > 0 && q.primary[0] != nil && q.primary[0].automaticWork {
		q.RemoveHead()
	}
	if len(q.primary) > 0 {
		q.danger.resume = q.primary[0]
		releaseGoalPayload(u, q.danger.resume)
		// Store a restartable assignment even if a save/switch happens mid-response.
		q.danger.resume.Phase, q.danger.resume.DynamicGate, q.danger.resume.Satisfied = 0, 0, 0
		q.danger.resume.MoveState, q.danger.resume.PathStatus = 0, 0
		q.danger.resume.Deadline = -1
	}
}

func dangerDistance(x, z, a, b numeric.Fixed) int64 {
	dx, dz := (x.Raw()>>16)-(a.Raw()>>16), (z.Raw()>>16)-(b.Raw()>>16)
	return dx*dx + dz*dz
}

func (*ModernRules) StepDangerResponse(u *units.Unit, tick uint32) {
	q := QueueOfUnit(u)
	if q == nil || u == nil || u.Def == nil || !u.Alive {
		return
	}
	b := q.Binding()
	if b == nil || b.Lookup == nil || b.DangerVisible == nil {
		return
	}
	q.reconsiderAutomaticTarget(u, tick)
	d := &q.danger
	any := false
	for i := range d.impacts {
		point := &d.impacts[i]
		if point.valid && tick-point.tick >= dangerMemoryTicks {
			*point = dangerImpact{}
		}
		any = any || point.valid
	}
	for i := range d.contacts {
		c := &d.contacts[i]
		if c.unit == nil {
			continue
		}
		if tick-c.tick >= dangerMemoryTicks || b.Lookup(c.handle) != c.unit {
			*c = dangerContact{}
			continue
		}
		if b.DangerVisible(u, c.unit) {
			if !c.unit.Alive || !scanHostile(b, u, c.unit) {
				*c = dangerContact{}
				continue
			}
			c.x, c.z = c.unit.X, c.unit.Z
		}
		any = true
	}
	if !any {
		q.finishDangerResponse()
		if d.withdrew && d.anchored && u.Flags>>stanceMoveShift&stanceFieldMask == 1 && (u.X != d.anchorX || u.Z != d.anchorZ) {
			// Expiry still cleans a displaced reaction, but returning to post
			// waits for Paralyze or another control head to release the unit.
			if u.Stunned || !dangerEligibleHead(q) {
				return
			}
			if id := Resolve(2, u, nil, &ResolvePos{X: d.anchorX, Y: d.anchorY, Z: d.anchorZ}); id != 0 {
				d.returnMove = q.PushHead(id, NewNodeForOrder(id, 0, d.anchorX, d.anchorY, d.anchorZ, tick, u.Handle, false))
			}
		}
		d.anchored, d.withdrew = false, false
		return
	}
	if protectedDangerWork(q) || u.Stunned {
		return
	}
	move := u.Flags >> stanceMoveShift & stanceFieldMask
	fire := u.Flags >> stanceFireShift & stanceFieldMask
	if d.response != nil && !q.containsDangerNode(d.response) {
		q.finishDangerResponse()
	}
	// A temporary control/task head owns dispatch until it is removed. Do not
	// let retargeting or a wait replacement jump ahead of that record. Contact
	// aging and expired-response cleanup have already run above.
	if d.response != nil && (len(q.primary) == 0 || q.primary[0] != d.response) {
		return
	}
	// Reconsider a holding wait before the normal pump can complete it and
	// run the suspended assignment during an otherwise still-dangerous visit.
	if d.response != nil && d.response.ID == Lookup("Wait") && int32(tick-d.nextDecision) >= 0 {
		q.finishDangerResponse()
	}
	if d.response != nil {
		if !dangerMovementFailed(d.response) && q.reconsiderDangerTarget(u, tick) {
			return
		}
		n := d.response
		stationary := DescriptorFor(n.ID).Name == "Attack_NoMove"
		valid := (move != 0 || stationary) && !dangerMovementFailed(n)
		if n.Target != 0 {
			target := b.Lookup(n.Target)
			known := fire == 2 && n.Target == d.opportunityTarget
			for _, c := range d.contacts {
				if c.unit == target && c.unit != nil {
					known = true
					break
				}
			}
			valid = valid && fire != 0 && known && target != nil && b.DangerVisible(u, target) && target.Alive && !leashBroken(u, n)
			if stationary {
				valid = valid && canEngageSlot(u, n.Target, 0)
			}
			if dangerMovementFailed(n) {
				for i := range d.contacts {
					if d.contacts[i].handle == n.Target {
						d.contacts[i].failedUntil = tick + 90
					}
				}
			}
		}
		if valid {
			return
		}
		q.finishDangerResponse()
		d.nextDecision = tick + dangerDecisionTicks
		d.quietUntil = tick + dangerDecisionTicks
		return
	}
	if !dangerEligibleHead(q) {
		return
	}
	// A script or explicit slot target can exist without an attack queue row.
	for slot := 0; slot < units.NumSlots; slot++ {
		if s := u.SlotAt(slot); s != nil && s.Target.Kind != units.TargetNone && s.Flags&units.SlotFlagAutonomous == 0 {
			return
		}
	}
	if d.nextDecision != 0 && int32(tick-d.nextDecision) < 0 || u.Attachment.Carrier != 0 {
		return
	}
	if !d.anchored {
		d.anchorX, d.anchorY, d.anchorZ = u.X, u.Y, u.Z
		d.anchored = true
	}
	d.nextDecision = tick + dangerDecisionTicks
	var selected *units.Unit
	selectedSlot := 0
	selectedStationary := false
	bestDistance := int64(0)
	if fire != 0 && b.DangerCanRespond != nil {
		for _, c := range d.contacts {
			target := c.unit
			if target == nil || c.failedUntil != 0 && int32(tick-c.failedUntil) < 0 || !b.DangerVisible(u, target) || target.Def == nil || target.Def.DefinitionMask().Intersects(u.Def.NoChaseCategoryMask) {
				continue
			}
			// A maneuver response never moves its anchor outward between decisions.
			leash := int64(uint16(u.Def.ManeuverLeashLength))

			for slot := 0; slot < units.NumSlots; slot++ {
				if unitCanFly(u) && slot != 0 {
					continue
				} // air executors use the primary weapon
				if !b.DangerCanRespond(u, target, slot) {
					continue
				}
				stationary := slot == 0 && !unitCanFly(u) && canEngageSlot(u, target.Handle, slot)
				if !stationary && (move == 0 || !hasLiveMover(u) || !u.Def.CanMove || move == 1 && (leash == 0 || dangerDistance(target.X, target.Z, d.anchorX, d.anchorZ) >= leash*leash)) {
					continue
				}
				distance := wholePlanarDistanceSquared(u, target)
				if selected == nil || distance < bestDistance || distance == bestDistance && target.Handle < selected.Handle {
					selected, selectedSlot, bestDistance = target, slot, distance
					selectedStationary = stationary
				}
				break
			}
		}
	}
	if selected != nil {
		id := Resolve(3, u, selected, nil)
		if selectedStationary {
			id = Lookup("Attack_NoMove")
		}
		if id != 0 {
			q.suspendDangerAssignment(u)
			if move == 1 && !selectedStationary {
				if moveID := Resolve(2, u, nil, &ResolvePos{X: d.anchorX, Y: d.anchorY, Z: d.anchorZ}); moveID != 0 {
					d.returnMove = q.PushHead(moveID, NewNodeForOrder(moveID, 0, d.anchorX, d.anchorY, d.anchorZ, tick, u.Handle, false))
				}
			}
			n := NewNodeForOrder(id, selected.Handle, selected.X, selected.Y, selected.Z, tick, u.Handle, false)
			n.Param1 = uint32(selectedSlot)
			if move == 1 && !selectedStationary {
				n.Param3 = uint32(uint16(u.Def.ManeuverLeashLength))
				n.GuardX, n.GuardY = int16(d.anchorX.Raw()>>16), int16(d.anchorZ.Raw()>>16)
			}
			d.response = q.PushHead(id, n)
			return
		}
	}
	// No suitable visible threat can be answered within this stance. A mobile
	// unit may withdraw along a movement-owned locally feasible corridor.
	if move == 0 || !hasLiveMover(u) || !u.Def.CanMove || b.DangerStepFeasible == nil {
		return
	}
	x, z, ok := q.dangerWithdrawal(u, move)
	if !ok {
		if d.withdrew {
			q.suspendDangerAssignment(u)
			d.response = q.PushHead(Lookup("Wait"), Node{Owner: u.Handle, Param1: dangerDecisionTicks})
		}
		return
	}
	id := Resolve(2, u, nil, &ResolvePos{X: x, Y: u.Y, Z: z})
	if id == 0 {
		return
	}
	q.suspendDangerAssignment(u)
	d.response = q.PushHead(id, NewNodeForOrder(id, 0, x, u.Y, z, tick, u.Handle, false))
	d.withdrew = d.response != nil
}

func (q *Queue) dangerWithdrawal(u *units.Unit, move uint32) (numeric.Fixed, numeric.Fixed, bool) {
	d, b := &q.danger, q.Binding()
	// Maximize the minimum separation from every remembered threat. Fixed
	// cardinal/diagonal traversal breaks ties without a draw or map iteration.
	score := func(x, z numeric.Fixed) int64 {
		best := int64(1 << 62)
		for _, point := range d.impacts {
			if point.valid {
				best = min(best, dangerDistance(x, z, point.x, point.z))
			}
		}
		for _, c := range d.contacts {
			if c.unit != nil {
				best = min(best, dangerDistance(x, z, c.x, c.z))
			}
		}
		return best
	}
	// A clear direct escape wins over every detour. Local detour admission
	// cannot guarantee that the ordinary path follower will execute it promptly.
	// Only when all safer direct choices fail do we try going around a crowd.
	for _, feasible := range [2]func(*units.Unit, numeric.Fixed, numeric.Fixed) bool{b.DangerStepFeasible, b.DangerRouteFeasible} {
		if feasible == nil {
			continue
		}
		best := score(u.X, u.Z)
		var x, z numeric.Fixed
		found := false
		for _, radius := range [3]int64{dangerRetreatDistance, 32, 16} {
			for _, dir := range [8][2]int64{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}} {
				step := radius
				if dir[0] != 0 && dir[1] != 0 {
					step = radius * 181 / 256 // 45, 22, 11; conservative diagonal tuning
				}
				cx, cz := u.X+numeric.Fixed(dir[0]*step<<16), u.Z+numeric.Fixed(dir[1]*step<<16)
				leash := int64(uint16(u.Def.ManeuverLeashLength))
				if move == 1 && (leash == 0 || dangerDistance(cx, cz, d.anchorX, d.anchorZ) >= leash*leash) {
					continue
				}
				value := score(cx, cz)
				if value <= best || !feasible(u, cx, cz) {
					continue
				}
				best, x, z, found = value, cx, cz, true
			}
		}
		if found {
			return x, z, true
		}
	}
	return 0, 0, false
}

// Modern observes the event now and stages its response at the next normal
// order visit. The damage path can still offer autonomous slots their attacker.
func (*ModernRules) RetaliationOrder(*units.Unit, *units.Unit) bool { return false }

// Reuse combat's full threat ranking; the order layer never copies its score.
// Acquire's third argument is a range limit, and zero means authored range.
func (q *Queue) reconsiderDangerTarget(u *units.Unit, tick uint32) bool {
	d, b := &q.danger, q.Binding()
	n := d.response
	if n == nil || n.Target == 0 || u.Flags>>stanceFireShift&stanceFieldMask != 2 || d.nextDecision != 0 && int32(tick-d.nextDecision) < 0 || b.Weapons == nil || b.Weapons.Acquire == nil {
		return false
	}
	d.nextDecision = tick + dangerDecisionTicks
	slot := 0
	if DescriptorFor(n.ID).Name == "Attack_Chase" {
		slot = int(n.Param1)
	}
	handle, ok := b.Weapons.Acquire(u, slot, 0)
	if !ok || handle == 0 || handle == n.Target {
		return false
	}
	target := b.Lookup(handle)
	if target == nil || !b.DangerVisible(u, target) || !canEngageSlot(u, handle, slot) || !automaticTargetWithinLeash(n, target) {
		return false
	}
	id := Resolve(3, u, target, nil)
	stationary := slot == 0 && !unitCanFly(u)
	if stationary {
		id = Lookup("Attack_NoMove")
	}
	if id == 0 {
		return false
	}
	if !stationary && u.Flags>>stanceMoveShift&stanceFieldMask == 0 {
		return false
	}
	if unitCanFly(u) {
		// Air reactions retain their existing descriptor-specific restart path.
		replacement := NewNodeForOrder(id, handle, target.X, target.Y, target.Z, tick, u.Handle, false)
		replacement.Param1 = uint32(slot)
		replacement.Param3, replacement.GuardX, replacement.GuardY = n.Param3, n.GuardX, n.GuardY
		q.cleanupNode(n)
		q.spliceOutPrimary(n)
		d.response = q.PushHead(id, replacement)
	} else {
		retargetAutomaticOrder(u, n, target, slot)
	}
	d.opportunityTarget = handle
	return true
}

// A failed automatic reaction must not cancel the assignment it interrupted.
// Explicit orders retain their ordinary failure semantics in both modes.
func (*ModernRules) ReactionResult(q *Queue, n *Node, code Code, tick uint32) Code {
	if code != 7 || q == nil || n == nil || q.danger.response != n {
		return code
	}
	for i := range q.danger.contacts {
		if q.danger.contacts[i].handle == n.Target && n.Target != 0 {
			q.danger.contacts[i].failedUntil = tick + 90
		}
	}
	q.danger.nextDecision = tick + dangerDecisionTicks
	return 8
}

// The route publisher reports an empty route away from its goal on the owned
// record, independently of the diagnostic MoveState [04 R-PATH-01 §7]
// [04 R-PATH-01 §9][04 R-COLL-01 §6]. Search rejection alone, arrival and
// payload release are different notifications and do not establish failure.
func dangerMovementFailed(n *Node) bool {
	return n != nil && (n.MoveState == MoveBlocked || n.Satisfied&gateNoRoute != 0)
}

// Modern producer provenance is transient and deliberately absent in Strict.
func (*ModernRules) MarkAutomaticAttack(n *Node) { n.automaticAttack = true }

func automaticTargetSlot(n *Node) int {
	if DescriptorFor(n.ID).Name == "Attack_Chase" {
		return int(n.Param1)
	}
	return 0
}

func (*ModernRules) PreserveAutomaticTarget(u *units.Unit, slot int) bool {
	q := QueueOfUnit(u)
	if q == nil || len(q.primary) == 0 || q.primary[0] == nil {
		return false
	}
	n := q.primary[0]
	if n != q.danger.response && !n.automaticAttack && DescriptorFor(n.ID).Name != "Guard_NoMove" {
		return false
	}
	s := u.SlotAt(slot)
	return slot == automaticTargetSlot(n) && n.Target != 0 && s != nil && s.Target.Kind == units.TargetUnit && s.Target.Unit == n.Target
}

// Acquire owns scoring, visibility and hysteresis. Orders additionally require
// a current shot and retain their authored movement leash.
func acquireAutomaticTarget(u *units.Unit, n *Node) *units.Unit {
	b := bindingOfUnit(u)
	if b == nil || b.Lookup == nil || b.DangerVisible == nil || b.Weapons == nil || b.Weapons.Acquire == nil {
		return nil
	}
	slot := automaticTargetSlot(n)
	h, ok := b.Weapons.Acquire(u, slot, 0)
	if !ok || h == 0 {
		return nil
	}
	target := b.Lookup(h)
	if target == nil || !target.Alive || !b.DangerVisible(u, target) || !canEngageSlot(u, h, slot) {
		return nil
	}
	if !automaticTargetWithinLeash(n, target) {
		return nil
	}
	return target
}

// Both ordinary automatic attacks and danger reactions keep the original
// maneuver target boundary when replacing a chase target in place.
func automaticTargetWithinLeash(n *Node, target *units.Unit) bool {
	if DescriptorFor(n.ID).Name != "Attack_Chase" || n.Param3 == 0 {
		return true
	}
	leash := int64(uint16(n.Param3))
	return dangerDistance(target.X, target.Z, numeric.FixedFromInt(int64(n.GuardX)), numeric.FixedFromInt(int64(n.GuardY))) < leash*leash
}

func (*ModernRules) GuardTarget(u *units.Unit, n *Node) (pool.Handle, bool) {
	if u == nil || u.Stunned {
		return 0, true
	}
	if u.Flags>>stanceFireShift&stanceFieldMask == 2 {
		if target := acquireAutomaticTarget(u, n); target != nil {
			return target.Handle, true
		}
	}
	// Return Fire retains its existing response but never scans for an opportunity.
	if u.Flags>>stanceFireShift&stanceFieldMask != 0 {
		b := bindingOfUnit(u)
		if b != nil && b.Lookup != nil && b.DangerVisible != nil {
			if target := b.Lookup(n.Target); target != nil && target.Alive && b.DangerVisible(u, target) && canEngageSlot(u, n.Target, 0) {
				return n.Target, true
			}
		}
	}
	return 0, true
}

func (q *Queue) reconsiderAutomaticTarget(u *units.Unit, tick uint32) {
	if u.Stunned || u.Flags>>stanceFireShift&stanceFieldMask != 2 || len(q.primary) == 0 {
		return
	}
	n := q.primary[0]
	if n == nil || n == q.danger.response {
		return
	}
	name := DescriptorFor(n.ID).Name
	if name != "Guard_NoMove" && (!n.automaticAttack || name != "Attack_Chase" && name != "Attack_NoMove") {
		return
	}
	// Admission still belongs to the handler; do not skip it or an accepted air pass.
	if n.Phase == 0 || n.nextAutomaticTargetTick != 0 && int32(tick-n.nextAutomaticTargetTick) < 0 {
		return
	}
	n.nextAutomaticTargetTick = tick + dangerDecisionTicks
	target := acquireAutomaticTarget(u, n)
	if target == nil || target.Handle == n.Target {
		return
	}
	retargetAutomaticOrder(u, n, target, automaticTargetSlot(n))
}

// Rebinding keeps the asynchronous Aim alive, like the normal unit-target
// setter [06 R-WPN-05 §3]. Combat's drift gate requests a new aim if needed.
// Clearing here would cancel the script while retaining its outstanding latch.
func retargetAutomaticOrder(u *units.Unit, n *Node, target *units.Unit, slot int) {
	releaseGoalPayload(u, n)
	n.BindTarget(target.Handle)
	if DescriptorFor(n.ID).Name != "Attack_Chase" {
		n.GoalX, n.GoalY, n.GoalZ = target.X, target.Y, target.Z
	}
	n.Phase, n.DynamicGate, n.Satisfied, n.MoveState, n.PathStatus = 1, 0, 0, 0, 0
	n.Deadline = -1
	bindSlotToUnit(u, slot, target.Handle)
}
