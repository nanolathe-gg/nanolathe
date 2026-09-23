// This file: the per-unit weapon service and the projectile phase.

package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func (s *Service) emitEvent(ev Event) {
	if s != nil && s.Events != nil {
		s.Events(ev)
	}
}

// aimNameForSlot returns the Aim* function name for slotIdx [04 §5.3][06 §3.3].

func badMaskForSlot(def *content.UnitDef, slotIdx int) content.CategoryMask {
	if def == nil {
		return content.CategoryMask{}
	}
	switch slotIdx {
	case 0:
		return def.BadTargetCategoryWPRIMask
	case 1:
		return def.BadTargetCategoryWSECMask
	case 2:
		return def.BadTargetCategoryWSPEMask
	default:
		return content.CategoryMask{}
	}
}

// registryOwnerDeclaresAllianceWithCandidate reports the row-A declaration
// read by the target registry and autonomous retention. The scanning player's
// row is indexed by the candidate owner's ally group; in a single-player seat
// that group is the candidate's player slot [06 §3.1]. This is deliberately
// not a symmetric relation [05 R-SHARE-01 §1].
func registryOwnerDeclaresAllianceWithCandidate(owner, candidateOwner uint8, econ *economy.Service) bool {
	if owner == candidateOwner {
		return true
	}
	if econ == nil || int(owner) >= combatPlayerSlots || int(candidateOwner) >= combatPlayerSlots {
		return false
	}
	return econ.Players[owner].Allies[candidateOwner]
}

// isCloakedUnit supplies Candidate.Cloaked, which is step 2 of the
// direct-visibility predicate the primary-list rebuild applies — "the
// candidate's cloak bit is set — reject" [06 §3.1]. That bit is the INSTANCE
// cloaked bit and nothing else [03 R-VIS-01 §6]: the definition flags used to
// be ORed in here, and both were wrong.
//
//   - `init_cloaked` has exactly one consumer, the unit constructor, which
//     seeds the cloak-REQUESTED status bit from it; the instance bit read here
//     is written only by the settlement's transition service, on a pass the
//     owner actually paid for [05 R-ECO-01 §9]. Reading the definition flag
//     made an unpaid mine untargetable.
//   - `stealth` is the contact callback's third reject: it suppresses radar
//     and sonar detection outright, with no distance or elevation term, and
//     never touches line of sight [03 R-VIS-01 §5]. It has no business in a
//     cloak predicate at all, and reading it here made every stealth unit
//     permanently unshootable.
//
// The two bits are separate fields (WU-19-92): Unit.Hidden is the instance bit
// this reads, Unit.IsCloaked is the request, and a unit whose owner could not
// pay the upkeep is targetable while still requesting cloak.
func isCloakedUnit(u *units.Unit) bool {
	if u == nil {
		return false
	}
	return u.Hidden
}

// callbackBridgeForUnit returns the callback bridge attached by strict unit
// composition. A playable unit always has this binding; an unattached unit
// cannot run weapon callbacks [04 §4.1][04 §5.3].
func (s *Service) callbackBridgeForUnit(u *units.Unit) *cob.CallbackBridge {
	if u == nil {
		return nil
	}
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		return binding.Callbacks
	}
	return nil
}

// UnitStepSummary reports the per-unit Aim handshake and firing result.
// Stable integer/fixed-point state only; it observes behavior, consumes no RNG,
// and alters no simulation decisions.
type UnitStepSummary struct {
	Dispatched       bool  // any Aim* dispatched this visit [04 §5.3]
	DispatchSlot     int   // weapon slot of the first dispatch (-1 if none)
	DispatchWeaponID int32 // weapon ID of the first dispatch
	ReturnSeen       bool  // synchronous Aim start-failure delivery before this call returned
	ReturnValue      int32 // last synchronous delivery; deferred readiness lives on the slot
	Drained          bool  // always false: the normal drain belongs to the session [04 R-MOV-03 §1]
	Fired            int   // projectiles created via TryFire this visit [06 §4]
}

type slotPrep struct {
	needLatch  bool
	needResult bool
	weapon     *content.WeaponDef
	tgtPos     Vec3
}

// retailYawFromGo converts this build's absolute yaw into retail's.
//
// A retail yaw `a` denotes the planar direction `(-sin a, -cos a)`, and the
// bearing it solves is `atan2q(muzzle - target)`; this build solves
// `atan2(target - muzzle)` and builds velocity from `+sin`/`+cos`
// [06 R-WPN-05 §4]. Both flips cancel for a projectile, so the two numberings
// differ by exactly half a turn: `atan2(-x, -z) = atan2(x, z) + 0x8000`.
// Anything that leaves the projectile arithmetic — a value handed to an
// authored script, or one compared against a unit heading, which this build
// already carries in retail's convention [04 R-MOV-01 §4] — must carry the
// shift. Adding 0x8000 is its own inverse, so this converts both ways.
func retailYawFromGo(goYaw uint16) uint16 { return goYaw + 0x8000 }

// aimYawForScript is the first argument of an Aim* dispatch: the bearing minus
// the unit's heading, modulo the circle, so zero means *dead ahead* and a
// positive value lies on the unit's left [06 R-WPN-05 §4]. The second argument
// is the absolute pitch, unshifted.
func aimYawForScript(goYaw, heading uint16) uint16 { return retailYawFromGo(goYaw) - heading }

// RetailYaw is a projectile's stored yaw in retail's numbering, the one every
// research sentence and every consumer outside this package's own velocity
// arithmetic is written for [06 R-WPN-05 §11]. The publication boundary hands
// the renderer this value: the projectile angle block folds `yaw - 0x8000`
// from retail's yaw word [03 §5.2], so handing it this build's word — which
// is already half a turn from retail's — drew every 3DO projectile facing
// away from its own motion.
func RetailYaw(goYaw numeric.Angle) uint16 { return retailYawFromGo(uint16(goYaw)) }

// hitDirectionByte is byte 7 of the damage packet, the argument HitByWeapon's
// two 400-radius trig components are built from [06 §9.1]: the high byte of
//
//	atan2q(record.X - victim.X, record.Z - victim.Z) - victim.heading
//
// — a fresh bearing from the record's CURRENT point, in retail's own
// numbering (the bearing helper's raw result, no half-turn involved), minus
// the victim's heading so that a shot arriving from dead ahead reads 0x80 at
// every heading. It is not derived from the projectile's yaw: a splash hit
// off the record's line of flight reads the direction of the burst, not of
// the shot. Correction: this build used to hand over `p.Yaw >> 8`, which
// dropped the heading term and carried this build's half-turn numbering, so
// the script's hit direction was wrong for every heading but one.
func hitDirectionByte(p *Projectile, victim *units.Unit) uint8 {
	bearing := numeric.AngleFromAtan2(p.Pos.X.Raw()-victim.X.Raw(), p.Pos.Z.Raw()-victim.Z.Raw())
	return uint8((uint16(bearing) - victim.Move.Heading) >> 8)
}

// ForgetUnit releases implementation-side Aim tracking with the unit record
// [04 §2.4]. The pool handle can immediately name another unit [P0-16]; its
// weapon receivers must not inherit the dead record's deferred work [06 §3.3].
func (s *Service) ForgetUnit(h pool.Handle) {
	if s == nil {
		return
	}
	for idx := 0; idx < NumSlots; idx++ {
		delete(s.pendingAims, pendingKey{Unit: h, Slot: idx})
	}
	delete(s.deathNotified, h)
}

// StepWeaponsForUnit runs the per-unit weapon pipeline for one unit visit ON-04 [06 §3.3][06 §4][04 §5.3][GAP T15].
// It is the authoritative per-unit step; the session owns the deterministic
// pool-order loop and invokes this method directly [06 §1.2] C1 (I1).
// Dependencies are the session's world, visibility, terrain, economy, catalog,
// simulation RNG, and CRT RNG services.
// Each slot completes its Aim dispatch and firing decision before the next
// begins. The session then runs the normal delta-one script drain; deferred
// Aim returns from that drain are available on a later visit [04 R-MOV-03 §1].
// Missing script or thread exhaustion never authorizes fire (explicit check) [GAP T15] [06 §3.3].
func (s *Service) StepWeaponsForUnit(u *units.Unit, tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) UnitStepSummary {
	var sum UnitStepSummary
	sum.DispatchSlot = -1
	if s == nil || u == nil {
		return sum
	}
	if !u.Alive || u.Dying {
		return sum
	}
	s.rules().CombatTick(s, tick, false)
	// The stunned mark is neither raised nor lowered here any more (WU-19-80).
	// Its one setter is the stun task's first visit and its one clearer is that
	// task's re-activation with a zero credit — the Paralyze row of
	// internal/orders — because [06 R-DMG-01 §11] gives the bit a single setter
	// and [06 §10] makes the stun a scheduled wait with no per-tick decrement:
	// nothing counts it down. The expiry-driven clear that used to stand here
	// lowered the mark on the tick the stored deadline passed whether or not the
	// row had re-activated, so the mark and the row could disagree.
	//
	// There is no `if u.Stunned { return }` here any more, and there must not
	// be one: retail's weapon phase does not read the stunned bit at all
	// [06 §10]. A paralyzed unit is silent because the Paralyze row ran the
	// release verb on all three slots — which clears the autonomy bit and the
	// target [04 R-UNIT-06 §5 part 3] — and because the row's head wait blocks
	// the list runner, so no order can hand the unit a new target while it
	// stands stunned. WU-19-80 kept the early return only because the slot loop
	// below did not yet read the autonomy bit and a stunned unit would have
	// re-acquired into a slot the row had just released; the loop reads it now,
	// so the mechanism covers the case and the over-approximation goes. Its
	// side effects mattered: it also skipped the reload decrement and the Aim
	// handshake, neither of which [06 §10] stops ("damage, healing, death,
	// economy settlement, cloak/upkeep ... are outside the blocked task
	// runner").
	bridge := s.callbackBridgeForUnit(u)
	// Failed target resolution clears only the request latch, including an
	// already-empty target [06 R-WPN-04 §1][06 R-WPN-05 §3].
	// The phase-5 scanner has a separate target-word-only clear [06 §3.2].
	clearSlotTarget := func(slot *units.Slot, idx int) {
		slot.Target = units.Target{Kind: units.TargetNone}
		slot.Aim.IssueBit = false
		slot.Flags &^= 0x01
		if bridge != nil {
			bridge.TargetCleared(int32(idx))
		}
	}
	for idx := 0; idx < NumSlots; idx++ {
		slot := u.SlotAt(idx)
		if slot == nil || !slot.IsPopulated() || !slot.IsEnabled() {
			continue
		}
		// The slot visit's first step: decrement a nonzero signed-16 reload countdown.
		// It happens for every populated slot, before the target is resolved
		// and before any later gate can skip the visit, so a weapon that is
		// out of range or waiting on Aim still recovers its shot
		// [06 §1.2][06 §4.1]. The fire-time pipeline tests this updated word,
		// so a one-tick reload reaches zero and may fire on this same visit.
		if slot.Reload != 0 {
			slot.Reload = int32(int16(slot.Reload - 1))
		}
		// Community CP-WPN-4 is asked at its source-defined position: reload
		// already counted down, while target resolution, aim, RNG and resource
		// work have not begun. A refused slot retains its target and aim state.
		if !s.rules().SlotMayFire(s, u, slot.Weapon, terrain) {
			continue
		}
		// Countdown recovery continues, but Hold Fire starts no aim or shot
		// work for a slot the unit still owns itself. A slot an order holds
		// takes the ordinary path below. Retain targets for stance/mode
		// resumption.
		// Nanolathe Modern policy: docs/DESIGN_WEAPONS_PROJECTILES.md §2.6.1.
		if s.holdsFire(u, slotOrdered(slot.Flags)) {
			continue
		}
		// Autonomous maintenance runs in phase 5 after AI dispatch [06 §3.2].
		// This phase resolves and fires the target already installed at entry.
		if slot.Target.Kind == units.TargetUnit && slot.Target.Unit != 0 {
			tu := w.Unit(slot.Target.Unit)
			if tu == nil || !tu.Alive || tu.Dying {
				clearSlotTarget(slot, idx)
				continue
			}
		}
		if slot.Target.Kind == units.TargetNone {
			slot.Aim.IssueBit = false
			slot.Flags &^= units.SlotFlagAimLatch
			continue
		}
		weapon := slot.Weapon
		if weapon == nil {
			continue
		}
		var tgtPos Vec3
		var tgtHandle pool.Handle
		if slot.Target.Kind == units.TargetUnit {
			tgtHandle = slot.Target.Unit
			tu := w.Unit(tgtHandle)
			if tu == nil {
				continue
			}
			// The target-point resolver's live-unit outcome [06 R-WPN-04 §1]:
			// `SweetSpot` on the TARGET's script, then that piece's vertex-box
			// centre added to the target's position. This point — not the
			// unit's ground position — is what the aim solve, the shot-time
			// gate and the creator all receive [06 §3.3].
			//
			// The PRE-FIRE LEAD of [06 §3.3] is applied to that resolved
			// point and only there: projectile guidance never leads
			// [06 §6.7]. Its five gates and its arithmetic live in
			// PreFireLeadPoint; a shot that fails any gate keeps the unled
			// point. Everything downstream of this line — the aim solve, the
			// shot-time range and medium gate, and the creator's stored
			// target point — therefore receives the LED point, which is what
			// makes the range test measure to where the target will be.
			//
			// The target mover's velocity triple reaches here through
			// units.MoveState, which internal/movement publishes beside the
			// scalar speed at every commit [04 R-MOV-01 §1][04 R-COLL-01 §1].
			// This site used to carry an open-question marker saying the
			// triple was not available; it is.
			tgtPos = PreFireLeadPoint(u, tu, slot, weapon, s.unitTargetPoint(tu), s)
		} else {
			tgtPos = Vec3{X: slot.Target.X, Y: PointTargetHeight(terrain, slot.Target.X, slot.Target.Z), Z: slot.Target.Z}
		}
		// Executor selection follows the live slot ladder, independently of
		// event reconstruction and active projectile motion [06 §3.3][06 §6.2].
		if !hasLiveWeaponExecutor(weapon) {
			continue
		}
		needLatch, needResult := aimRequirement(weapon)
		pre := &slotPrep{weapon: weapon, tgtPos: tgtPos, needLatch: needLatch, needResult: needResult}
		switch {
		case weapon.Turret:
			if !slot.Aim.IssueBit && (weapon.Ballistic || weapon.LineOfSight) {
				yaw, pitch, ok := turretAimGeometry(u, weapon, idx, bridge, tgtPos, terrain)
				// A failed fresh solve preserves angles and readiness, then
				// continues to the ordinary reload/fire gates [06 §3.3].
				if ok {
					slot.DesiredYaw, slot.DesiredPitch = yaw, pitch
					s.dispatchSlotAim(u, slot, idx, tick, bridge, yaw, pitch, &sum)
				}
			}
		case weapon.VLaunch:
			if !slot.Aim.IssueBit && (!weapon.Stockpile || slot.Ammo != 0) {
				s.dispatchSlotAim(u, slot, idx, tick, bridge, 0, 0, &sum)
			}
		}
		// Fixed and dropped executors have no aim-time piece query. The forced
		// muzzle query belongs inside their admitted fire attempt [06 R-P0-07].
		s.firePreparedSlot(u, slot, idx, pre, tick, terrain, econ, simRNG, w, catalog, &sum)
	}
	return sum
}

// turretAimGeometry queries the aim origin only at a fresh Aim dispatch or
// inside an admitted, ready turret executor [06 R-P0-07]. Its yaw is relative
// to the live hull heading on both sides of the drift comparison [06 R-WPN-05 §4].
func turretAimGeometry(u *units.Unit, weapon *content.WeaponDef, idx int, bridge *cob.CallbackBridge, target Vec3, terrain *world.Terrain) (yaw, pitch uint16, ok bool) {
	piece := int32(-1)
	if bridge != nil {
		piece = bridge.AimPiece(cob.WeaponSlot(idx)).QueryValue()
	}
	origin, resolved := muzzleWorldPosResolved(u, piece)
	if !resolved {
		return 0, 0x8000, false
	}
	dx, dy, dz := target.X.Sub(origin.X), target.Y.Sub(origin.Y), target.Z.Sub(origin.Z)
	yaw = aimYawForScript(uint16(YawFromDelta(dx, dz)), u.Move.Heading)
	if weapon.Ballistic {
		var gravity numeric.Fixed
		if terrain != nil {
			gravity = terrain.Gravity
		}
		pitch, ok = BallisticSolve(dx, dy, dz, numeric.Fixed(weapon.WeaponVelocity), gravity, weapon.MinBarrelAngle)
		return yaw, pitch, ok
	}
	return yaw, uint16(PitchFromDelta(dx, dy, dz)), true
}

// dispatchSlotAim installs a fresh receiver without advancing the VM [06 §3.3].
// Deferred returns update the slot, not the already-returned visit summary.
func (s *Service) dispatchSlotAim(u *units.Unit, slot *units.Slot, idx int, tick uint32, bridge *cob.CallbackBridge, yaw, pitch uint16, sum *UnitStepSummary) {
	slot.Aim.Ready = false
	if bridge != nil {
		key := pendingKey{Unit: u.Handle, Slot: idx}
		returned, value := false, int32(0)
		result := bridge.Aim(cob.WeaponSlot(idx), yaw, pitch, func(ret cob.CallbackReturn) {
			delete(s.pendingAims, key)
			returned, value = true, ret.Value
			// Every outstanding callback addresses this slot's receiver. A
			// zero delivery cannot revoke a different callback's grant
			// [04 R-CB-01 §6]; only a fresh dispatch clears the receiver.
			slot.Aim.CompleteAim(ret.Value)
		})
		if returned {
			sum.ReturnSeen, sum.ReturnValue = true, value
		}
		if result.Started {
			if s.pendingAims == nil {
				s.pendingAims = make(map[pendingKey]pendingAim)
			}
			s.pendingAims[key] = pendingAim{ThreadIdx: result.Thread, DispatchedTick: tick}
			if !sum.Dispatched {
				sum.Dispatched, sum.DispatchSlot, sum.DispatchWeaponID = true, idx, slot.Weapon.ID
			}
		}
	}
	slot.Aim.IssueBit = true
	slot.Flags |= units.SlotFlagAimLatch
}

// firePreparedSlot completes one slot before the next slot begins its weapon
// work. The unit's normal COB drain is deliberately outside this method.
func (s *Service) firePreparedSlot(u *units.Unit, slot *units.Slot, idx int, pre *slotPrep, tick uint32, terrain *world.Terrain, econ *economy.Service, simRNG *rng.Simulation, w *units.World, catalog *content.Catalog, sum *UnitStepSummary) {
	if slot == nil || !slot.IsPopulated() || !slot.IsEnabled() || pre == nil {
		return
	}
	key := pendingKey{Unit: u.Handle, Slot: idx}
	if slot.Reload != 0 {
		return
	}
	weapon := pre.weapon
	if !checkAdmission(s, u, weapon, pre.tgtPos, terrain) {
		u.Pending |= units.PendingCouldNotFire
		return
	}
	if weapon.Stockpile {
		if slot.Ammo <= 0 {
			return
		}
	} else if econ != nil {
		eCost, mCost := float32(weapon.EnergyPerShot), float32(weapon.MetalPerShot)
		p := &econ.Players[u.Owner]
		if (eCost != 0 || mCost != 0) && (p.Stock[economy.Energy] < eCost || p.Stock[economy.Metal] < mCost) {
			return
		}
	}
	// Physical admission and cost/ammunition checks precede the executor's
	// readiness test, so a pending Aim cannot suppress failed-shot feedback
	// from the outer gate [06 R-P0-07][06 R-WPN-05 §6].
	if (pre.needLatch && !slot.Aim.IssueBit) || (pre.needResult && !slot.Aim.Ready) {
		return
	}
	if weapon.Turret {
		yaw, pitch, ok := turretAimGeometry(u, weapon, idx, s.callbackBridgeForUnit(u), pre.tgtPos, terrain)
		if !ok || !DriftGatePass(weapon, unitStationary(u), slot.DesiredYaw, yaw, slot.DesiredPitch, pitch) {
			if !ok {
				u.Pending |= units.PendingCouldNotFire
			}
			slot.Aim.IssueBit = false
			slot.Flags &^= units.SlotFlagAimLatch
			delete(s.pendingAims, key)
			return
		}
	}
	if !tryFireForSlot(u, slot, idx, tick, terrain, simRNG, s, w, catalog, pre.tgtPos) {
		return
	}
	if pre.needResult || pre.needLatch {
		slot.Aim.IssueBit = false
		slot.Aim.Ready = false
		slot.Flags &^= units.SlotFlagAimLatch
		if s.pendingAims != nil {
			delete(s.pendingAims, key)
		}
	}
	if weapon.Stockpile {
		if slot.Ammo > 0 {
			slot.Ammo--
		}
	} else {
		slot.Reload = int32(int16(s.storedReload(u.Def, u.Health, u.MaxHealth, u.Kills, weapon.ReloadTime)))
	}
	// Every successful launch wakes its owning order after ammunition/reload
	// storage and before any ordinary resource debit [06 §4.2][06 R-WPN-05 §6].
	if weapon.CommandFire {
		u.Pending |= 0x800
	} else {
		u.Pending |= 0x400
	}
	if !weapon.Stockpile && econ != nil && (weapon.EnergyPerShot != 0 || weapon.MetalPerShot != 0) {
		economy.ImmediateDebit(&econ.Players[u.Owner], econ.UnitBuckets(u.Handle), float32(weapon.EnergyPerShot), float32(weapon.MetalPerShot))
	}
	sum.Fired++
}

// TickWeapons runs the integrated per-unit weapon pipeline [06 §3][06 §4][04 §5.3][GAP T15].
// Compatibility wrapper: loops over units in deterministic order and delegates to StepWeaponsForUnit ON-04.
// TEST-ONLY and non-authoritative: no production caller, because the session's own unit visit calls
// StepWeaponsForUnit directly (DESIGN_WEAPONS_PROJECTILES §2.1). The authoritative behavior — including
// the unit iteration order — is the session's, not this loop's.
func (s *Service) TickWeapons(tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil || w == nil {
		return
	}
	// DET-01: no global fallback; session must inject simRNG.
	if simRNG == nil {
		// nil means no draw (return 0 without advancing) — production always injects.
	}
	if w.IsSliced() {
		for player := 0; player < 10; player++ {
			start, end, ok := w.SliceForPlayer(player)
			if !ok {
				continue
			}
			for slot := start; slot <= end; slot++ {
				u := w.Unit(pool.Handle(slot))
				if u == nil || !u.Alive || u.Dying {
					continue
				}
				// The stunned over-approximation lives once, at the top of
				// StepWeaponsForUnit [06 R-DMG-01 §11]; the copy that stood
				// here also lowered the mark on expiry, which is the Paralyze
				// row's job and not this loop's [06 §10].
				s.StepWeaponsForUnit(u, tick, w, vis, terrain, econ, catalog, simRNG, crtRNG)
			}
		}
	} else {
		buckets := make([][]*units.Unit, 10)
		// The pool-order walk is only the unsliced fixture path's; the sliced
		// path above indexes the pool directly and never read this list.
		for _, u := range w.Iter() {
			if u == nil || u.Dying {
				continue
			}
			// As above: the stunned skip is StepWeaponsForUnit's one
			// over-approximation [06 R-DMG-01 §11], and the mark's expiry
			// belongs to the Paralyze row [06 §10].
			buckets[u.Owner] = append(buckets[u.Owner], u)
		}
		for player := 0; player < 10; player++ {
			for _, u := range buckets[player] {
				s.StepWeaponsForUnit(u, tick, w, vis, terrain, econ, catalog, simRNG, crtRNG)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// The autonomous scan's per-unit preconditions and round-robin cadence
// [06 §3.2]
// ---------------------------------------------------------------------------

// stanceFireAtWill is the value a unit's two-bit standing-fire field must read
// for the autonomous scan to act on it [06 §3.2][04 R-STANCE-01 §1]: 2, the
// FIRE AT WILL frame of the side panel's stance button. 0 is HOLD FIRE and 1 is
// RETURN FIRE — value 1 retaliates through the damage-intake offer of
// [06 R-WPN-04 §2] but never hunts, and only 2 opens this scan.
const stanceFireAtWill uint32 = 2

// autonomousScanDivisor is the divisor of the scan's per-call unit budget
// [06 §3.2]: it visits `word / 30 + 1` array entries per player per tick.
//
// The word is the session's PER-PLAYER UNIT LIMIT — a setup constant, not a
// counter — so the budget is constant for the whole session
// [06 §3.2 "The budget word is the per-player unit limit"]. With the stock
// campaign default of 200 that is `200/30 + 1 = 7` records per player per tick.
const autonomousScanDivisor = 30

// combatPlayerSlots is the session's ten player slots [05 "Player slot"]. The
// cursor is indexed over that fixed range, never a map (I1).
const combatPlayerSlots = 10

// ---------------------------------------------------------------------------
// The per-side target registry: both candidate lists and the secondary-list
// gate [06 §3.1]
// ---------------------------------------------------------------------------

// targetRegistryPeriod is the registry's rebuild cadence [06 §3.1]: a side's
// registry is rebuilt from the whole unit array only when
// `lastRebuild + 30 <= currentTick`, once per side, from the per-player phase.
// An acquisition can therefore read a list up to thirty ticks stale, which is
// why the per-attempt filter re-tests liveness.
const targetRegistryPeriod uint32 = 30

// targetRegistry is the per-side target registry of [06 §3.1] — "one object per
// player slot, holding two candidate lists plus a per-definition census, a
// weighted centroid and a gate flag".
//
// Three of those members are modelled here: the **primary** candidate list, the
// **secondary** candidate list and the secondary-list **gate**. The census, the
// centroid and the third list live elsewhere: the third list is
// AirBaseRegistry, and internal/ai owns the census and the centroid
// [08 R-AI-01 §16].
//
// The primary list is filed at the REBUILD, with the direct-visibility
// predicate evaluated there and never again: "Automatic acquisition never scans
// the unit array. It draws from a per-side target registry ... from which each
// acquisition attempt filters a fresh array" [06 §3.1]. An attempt therefore
// reads a list up to thirty ticks stale — including entries for units that died
// or turned invisible in between, and excluding a unit that became visible
// since — which is why the per-attempt filter re-tests liveness and nothing
// else.
//
// Rows are indexed by player slot over the fixed ten-slot range, never a map
// (I1).
type targetRegistry struct {
	// lastRebuild is the MIRROR of the one cadence word [06 §3.1]. The gate
	// that decides a slot's due is the strategic refresh's, in internal/ai;
	// this row is stamped by the same call, so the two halves of the one retail
	// routine cannot drift apart.
	lastRebuild [combatPlayerSlots]uint32
	gate        [combatPlayerSlots]bool
	primary     [combatPlayerSlots][]pool.Handle
	secondary   [combatPlayerSlots][]pool.Handle

	// walkScratch is the destination the rebuild's pool-order walk appends
	// into, so the walk that runs for every side on the cadence tick reuses one
	// buffer instead of allocating a live-unit slice per side.
	walkScratch []*units.Unit
}

// primaryList returns one side's primary list as the last rebuild left it.
// The slice is the registry's own storage; callers filter it into a fresh
// vector and never write through it.
func (r *targetRegistry) primaryList(owner uint8) []pool.Handle {
	if r == nil || int(owner) >= combatPlayerSlots {
		return nil
	}
	return r.primary[owner]
}

// secondaryList returns one side's secondary list as the last rebuild left it.
// The slice is the registry's own storage; callers filter it into a fresh
// vector and never write through it.
func (r *targetRegistry) secondaryList(owner uint8) []pool.Handle {
	if r == nil || int(owner) >= combatPlayerSlots {
		return nil
	}
	return r.secondary[owner]
}

// targetingUpgradeGateFor reads one side's secondary-list gate as the last
// rebuild left it [06 §3.1].
func (s *Service) targetingUpgradeGateFor(owner uint8) bool {
	if s == nil || int(owner) >= combatPlayerSlots {
		return false
	}
	return s.targets.gate[owner]
}

// TargetRegistryRebuildTick reports the tick one slot's registry was last
// rebuilt on — the mirror of the one cadence word [06 §3.1]. Zero means no
// rebuild has run: the first is due at tick 30, because the state's constructor
// seeds the word to zero and "the tick-0 priming finds `0 + 30 <= 0` false and
// draws nothing" [06 §3.1 "Which slots draw"].
//
// It exists so a caller can check that the two halves of the one routine agree.
func (s *Service) TargetRegistryRebuildTick(slot uint8) uint32 {
	if s == nil || int(slot) >= combatPlayerSlots {
		return 0
	}
	return s.targets.lastRebuild[slot]
}

// PrimaryTargets returns one side's primary candidate list exactly as that
// side's last registry rebuild left it [06 §3.1]. It is read-only: the slice is
// the registry's own storage, and a caller must neither write through it nor
// retain it across a rebuild.
//
// Its one consumer outside this package is the sensor phase's minimum-cloak
// proximity pass, whose candidate set is "the primary candidate list of
// registry[unit.ownerSlot]" and nothing else [03 R-VIS-01 §4] pass 4. That pass
// re-tests liveness and the death latch at use, because the list is up to
// thirty ticks stale by construction — which is the point of reading it rather
// than scanning: the breach test is "an enemy I could see up to a second ago is
// within mincloakdistance", not "an enemy is within mincloakdistance". A
// hostile that never passed the rebuild's direct-visibility predicate is not on
// the list and can never suppress cloak, however close it comes.
func (s *Service) PrimaryTargets(slot uint8) []pool.Handle {
	if s == nil {
		return nil
	}
	return s.targets.primaryList(slot)
}

// RebuildTargetRegistryIfDue is this package's half of the ONE retail routine
// of [06 §3.1] — the routine that is both "the per-side target registry
// rebuild" of doc 06 and "the 30-tick strategic refresh" of
// [08 R-AI-01 §16] — reached at the per-player phase for one slot.
//
// Call it once per visited slot, in ascending slot order, AFTER that slot's
// manager tick and BEFORE its per-unit visits: "For a visited slot, in order:
// the manager tick ...; then the cadence gate runs when the slot's strategic
// state exists — the gate is null-checked, never controller-checked; then the
// slot's per-unit visits" [06 §3.1 "Which slots draw"]. Which sides rebuild
// therefore does not depend on which units exist, and a slot whose strategic
// state exists rebuilds even while it owns nothing.
//
// The cadence is decided by the caller's single gate. The comparison repeated
// below is a MIRROR of it, not a second clock: it makes a duplicate call inside
// one window a no-op and keeps this package's own tests able to drive the
// rebuild directly. Because the only production caller is the strategic
// refresh's gate, and both stamp on the same tick from the same zero start,
// the two words cannot disagree — which is the whole point of moving the call
// here [06 §3.1].
//
// It reports whether it rebuilt. It consumes NO random draw; see
// rebuildTargetRegistry.
func (s *Service) RebuildTargetRegistryIfDue(tick uint32, slot uint8, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service) bool {
	if s != nil {
		s.rules().CombatTick(s, tick, false)
	}
	if s == nil || w == nil || int(slot) >= combatPlayerSlots {
		return false
	}
	if tick < s.targets.lastRebuild[slot]+targetRegistryPeriod {
		return false
	}
	s.rebuildTargetRegistry(tick, slot, w, vis, terrain, econ)
	return true
}

// rebuildTargetRegistry rebuilds one side's registry on the cadence of
// [06 §3.1] — `if (registry.lastRebuildTick + 30 <= currentTick)` — walking the
// whole unit array once, in slot order, and classifying each unit whose alive
// bit is set and whose death latch is clear:
//
//   - a unit of the registry owner's OWN player slot takes the counting branch,
//     whose one member modelled here is the secondary-list gate: the rebuild
//     writes the constant 1 when that unit is complete, activated and carries
//     `istargetingupgrade`. Nothing is summed, and an ally's upgrade unit does
//     not count — the branch is entered only on an exact owner-slot match
//     (refinement of 2026-09-02, points 1 and 2);
//   - a HOSTILE unit — the registry owner's alliance row, indexed by the
//     candidate's owner, reads zero — joins the PRIMARY list when the
//     direct-visibility predicate accepts it here, at rebuild time, and the
//     SECONDARY list when its runtime seen bit is set. "The two tests are
//     independent, so a unit can be on both lists, either, or neither."
//
// The primary-list entry above also requires "a runtime exclusion status bit
// is clear" [06 §3.1]. RWU-19-38 settled that bit by whole-image writer/reader
// census: it is bit 15 (0x8000) of the status word, the mission `Immunity`
// flag, named `units.ImmunityStatus` here — the same bit `Unit.MakeSelectable`
// clears. It gates PRIMARY-list entry only; the secondary seen-bit list is
// unaffected, so an immune unit the local observer can see still reaches the
// secondary list and remains a fallback candidate when the registry's
// secondary-list gate is open [06 §3.1 "the primary-list exclusion bit is the
// mission Immunity bit"].
//
// It is reached only through RebuildTargetRegistryIfDue, from the per-player
// phase, at the slot position [06 §3.1] gives it.
//
// It consumes NO random draw. [06 §3.1] gives the rebuild one simulation draw
// of bound 30 whose zero outcome runs the strategic refresh, and this build
// already takes exactly that draw — in internal/ai, at the per-player phase,
// once per side per thirty ticks. The two are one retail routine seen from two
// documents: [08 R-AI-01 §16] names the 30-tick strategic refresh's three
// vectors as "non-allied live units that pass the ordinary visibility
// predicate", "non-allied units carrying one further runtime status bit" and
// "the player's own active units whose definition is both a builder and an air
// base" — this section's primary, secondary and third lists — and names the
// same refresh as the writer of the targeting-upgrade flag and of the census
// and centroid [08 "Strategic state construction and refresh"]. The strategic
// state IS the target registry, its 30-tick refresh IS this rebuild, and the
// class-vector recomputation its zero outcome gates is §3.1's `refreshStrategy`
// [08 "Class-vector recomputation loop and inputs"]. Drawing again here would
// consume the same retail draw twice and desynchronize every later consumer of
// the stream (I4).
//
// This build still splits one retail routine across two packages — the lists
// and the gate here, the census, the centroid and the draw in internal/ai —
// but the two halves no longer keep independent clocks. WU-19-126 joined them:
// the strategic refresh's gate is the one gate, and on a due it calls
// RebuildTargetRegistryIfDue for that slot before recomputing the census and
// before taking the bound-30 draw, which is the retail order (lists, census
// and centroid, then the draw whose zero outcome recomputes the class
// vectors) [06 §3.1][08 R-AI-01 §16]. The word here is a mirror of that gate.
func (s *Service) rebuildTargetRegistry(tick uint32, owner uint8, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service) {
	if s == nil || w == nil || int(owner) >= combatPlayerSlots {
		return
	}
	p := int(owner)
	r := &s.targets
	if tick < r.lastRebuild[p]+targetRegistryPeriod {
		return // `lastRebuild + 30 <= currentTick` [06 §3.1]
	}
	r.lastRebuild[p] = tick
	gate := false
	pri := r.primary[p][:0] // both lists are cleared at every rebuild [06 §3.1]
	sec := r.secondary[p][:0]
	r.walkScratch = w.AppendLive(r.walkScratch[:0]) // pool slot ascending [06 §3.1] (I1)
	for _, u := range r.walkScratch {
		if u == nil || !u.Alive || u.Dying {
			continue // alive bit set, death latch clear [06 §3.1]
		}
		if u.Owner == owner {
			if unitOpensTargetingUpgradeGate(u, owner) {
				gate = true // the constant 1, not a count [06 §3.1]
			}
			continue // an own unit is never a candidate for its owner's lists
		}
		if registryOwnerDeclaresAllianceWithCandidate(owner, u.Owner, econ) {
			continue // neither hostile nor own: skipped entirely [06 §3.1]
		}
		if u.Flags&units.ImmunityStatus == 0 && directlyVisibleAtRebuild(owner, u, vis) {
			pri = append(pri, u.Handle) // unit-array order [06 §3.1] (I1)
		}
		if u.Flags&visibility.SeenBit != 0 {
			sec = append(sec, u.Handle) // the same order, independent test
		}
	}
	r.gate[p] = gate
	r.primary[p] = pri
	r.secondary[p] = sec
}

// directlyVisibleAtRebuild is the primary list's direct-visibility predicate
// [06 §3.1], evaluated for one OBSERVING PLAYER — the registry owner — and one
// candidate, at the rebuild and nowhere else.
//
// It delegates the section's ordered owner, cloak, sonar, water and four-probe
// checks to visibility.Target, populated from the candidate's definition box.
// The reaction-acquisition adapter constructs that same target rather than a
// separate center-point predicate.
//
// The observer is a player slot, not a shooter: the registry is per side, and
// "in word-mask mode every probe tests the local player's bit, not the
// observer's" is the visibility service's own business [03 §3.2].
func directlyVisibleAtRebuild(owner uint8, cand *units.Unit, vis *visibility.Service) bool {
	if cand == nil || vis == nil {
		return false // hostile list entry is visibility-gated [06 §3.1]
	}
	return vis.IsVisible(visibility.PlayerID(owner), visibilityTarget(cand, cand.Flags))
}

// visibilityTarget forms the direct-visibility probe from the definition's
// bounding record: start at min X/max Y/min Z, then carry the three spans
// through the visibility predicate's four probes [06 §3.1][03 §3.2].
func visibilityTarget(cand *units.Unit, status uint32) visibility.Target {
	if cand == nil {
		return visibility.Target{}
	}
	min, max := cand.Def.BoundingExtents()
	return visibility.TargetFromBounds(visibility.Target{
		UnitID:  uint16(cand.Handle),
		OriginX: cand.X, OriginY: cand.Y, OriginZ: cand.Z, Flying: cand.Move.ModeMirror == 2,
		FootprintX: int32(cand.CachedOccupancyX), FootprintZ: int32(cand.CachedOccupancyZ),
		FootprintSizeX: int32(cand.FootprintSizeX), FootprintSizeZ: int32(cand.FootprintSizeZ),
		Owner: visibility.PlayerID(cand.Owner),
		X:     cand.X, Y: cand.Y, Z: cand.Z,
		Hidden: isCloakedUnit(cand), Status: status,
	}, min, max)
}

func (s *Service) acquireTargetForSlot(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, econ *economy.Service, catalogs ...*content.Catalog) (pool.Handle, bool) {
	return s.acquireTargetForSlotRange(u, slot, idx, w, vis, terrain, simRNG, econ, -1, catalogs...)
}

// weaponlessSearchRuns reports whether the shared unit-level target search may
// run for a slot that holds NO active weapon [06 §3.2].
//
// Exactly one caller reaches it that way: the sight-distance caller — the
// opportunity scan of [04 R-STANCE-01 §3], which "makes exactly one
// acquisition call with weapon slot 0" — on a shooter whose definition carries
// `kamikaze`. That is the stock case, not an edge case: every stock `kamikaze`
// definition (the twelve mines, `armvader` and `corroach`) authors an empty
// `weapon1`, which resolves to the record-0 inactive sentinel and therefore
// enables no slot [02 §5 R-CONTENT-02][06 §1.2], while [04 R-SPEC-01 §1]
// states both that the kamikaze order variant is reached by the unarmed
// definitions — "in stock content, the ones with no weapon of their own" —
// and that "any registered enemy within `sightdistance` of an idle
// fire-at-will kamikaze unit is a candidate". A search that refused a
// weaponless slot would leave no stock kamikaze definition with any candidate
// at all, and no stock mine could proximity-detonate.
//
// Nothing weapon-derived is consulted on this path. Check 3 bypasses the §3.1
// physical gate ENTIRELY for a `kamikaze` shooter, and that gate is where
// every weapon operand lives (range, medium, air, ballistic); check 5 cannot
// fire, because no weapon is not a paralyzer; the bad-target mask that buckets
// the survivors is carried by the UNIT definition, indexed by slot number
// rather than by the weapon record [06 §3.1]; and the filter radius is the
// caller's sight distance.
//
// Every other caller still requires an active weapon: the autonomous per-slot
// scan (rangeLimit < 0) visits resolved, enabled slots only, and shot
// admission, the manual/fire adapters and the reaction offer keep their own
// weapon tests.
func weaponlessSearchRuns(u *units.Unit, rangeLimit int32) bool {
	return rangeLimit >= 0 && u != nil && u.Def != nil && u.Def.Kamikaze
}

// acquireTargetForSlotRange is the non-mutating form used by order-facing
// acquisition. A nonnegative range overrides only the query's range operand;
// the compiled WeaponDef remains immutable [02 "Weapon record"][06 §3.2].
func (s *Service) acquireTargetForSlotRange(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, econ *economy.Service, rangeLimit int32, catalogs ...*content.Catalog) (pool.Handle, bool) {
	if u == nil || w == nil || slot == nil {
		return 0, false
	}
	if slot.Weapon == nil && !weaponlessSearchRuns(u, rangeLimit) {
		return 0, false
	}
	var catalog *content.Catalog
	if len(catalogs) != 0 {
		catalog = catalogs[0]
	}
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
	acq := slotAcquisition(s, u, slot, idx, w, vis, terrain, simRNG, catalog, seaLevel, rangeLimit)
	// The filter radius is the caller's, the gate's range clause is the slot
	// weapon's [06 §3.2][06 R-WPN-05 §1] clause 5.
	candidates := s.primaryCandidates(u, w, seaLevel, vis, econ, catalog, acq.FilterRange)
	// The registry's secondary-list gate and its secondary list [06 §3.1].
	// Both belong to the SCANNING PLAYER — the registry is per side and is the
	// same for every slot of every unit that player owns — so they are read
	// here, by owner, and never from the shooter's own definition.
	acq.HasUpgrade = s.targetingUpgradeGateFor(u.Owner)
	// Check 3 of the picked-candidate order [06 §3.2]: the SHOOTER's `kamikaze`
	// flag bypasses the §3.1 physical gate for every picked candidate. This is
	// the shared unit-level target search both callers of §3.2 reach — the
	// autonomous per-slot scan and the order-facing sight-distance form — and
	// the check belongs to it, not to the gate routine, so slotAcquisition does
	// not set the field and the reaction offer's §3.1-only admission keeps its
	// gate [06 R-WPN-04 §2 part 3].
	//
	// In stock content this check is what makes the mines and the crawling
	// bombs work at all: they carry no weapon, so the bypass is the only reason
	// the sight-distance caller ever returns them a candidate — see
	// weaponlessSearchRuns above [04 R-SPEC-01 §1]. (This used to claim the
	// bypass was "stock-inert", on the reasoning that the search is entered
	// only through a resolved weapon slot. That reasoning was the defect: it
	// silenced every stock mine and crawling bomb.)
	acq.KamikazeShooter = u.Def != nil && u.Def.Kamikaze
	// Check 2's two shooter-side disjuncts [06 §3.2]. Like check 3 they belong
	// to the picked-candidate order, not to the §3.1 gate routine, so
	// slotAcquisition does not set them and the reaction offer's §3.1-only
	// admission is unaffected [06 R-WPN-04 §2 part 3].
	acq.ShooterControlByte = s.PlayerControlByteFor(u.Owner)
	// Check 4 of the picked-candidate order [06 §3.2]: "for the SIGHT-DISTANCE
	// CALLER ONLY, the candidate's definition index must be clear of the
	// `nochasecategory` mask". That caller is the opportunity scan of
	// [04 R-STANCE-01 §3] — the idle/loiter arms of `Standby`, `Standby_Mine`,
	// `Patrol`, `VTOL_Standby`, `VTOL_Patrol` and `VTOL_SeekAttack`, "with its
	// range argument taken from the definition's `sightdistance`" — and it is
	// the only caller that supplies a radius here. The autonomous scan
	// (rangeLimit < 0), the reaction offer's §3.1-only admission and the order
	// handlers all leave the mask zero, so they bypass check 4 exactly as
	// [06 §3.2] and [06 R-WPN-04 §2 part 3] require.
	if rangeLimit >= 0 && u.Def != nil {
		acq.NoChaseMask = u.Def.NoChaseCategoryMask
	}
	// acq.ShootAll stays false: it is the session mode-flags word's bit 10,
	// whose only retail writer is the `+ShootAll` chat command, and the word is
	// zero-filled with no loader, settings writer or save restore touching that
	// bit — so a stock session runs with it clear
	// [06 §3.2 "The option bit of check 2"][07 R-CAM-01 §6]. Nanolathe has no
	// `+shootall` typed command; when one is added it writes a session flag
	// that arrives here.
	if len(candidates) == 0 && acq.HasUpgrade {
		candidates = s.secondaryCandidates(u, w, seaLevel, vis, econ, catalog, acq.FilterRange)
	}
	s.targetQuery = TargetQuery{Candidates: candidates, Acquisition: acq, Shooter: u, Slot: slot, Index: idx, World: w, Terrain: terrain, Visibility: vis, Economy: econ, Catalog: catalog}
	return s.rules().SelectTarget(s, &s.targetQuery)
}

// primaryCandidates materializes the cached primary registry's preliminary
// query in stored order. It refreshes liveness and applies the planar range
// query before allocating its local candidate snapshot; physical admission and
// the second, ordered range gate still run per sampled pick. Hostility and
// visibility remain those of the last rebuild [06 §3.1].
func (s *Service) primaryCandidates(u *units.Unit, w *units.World, seaLevel numeric.Fixed, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, rangeLimit int32) []Candidate {
	if s == nil || u == nil || w == nil {
		return nil
	}
	return s.materializeCandidatesInRange(u, w, s.targets.primaryList(u.Owner), seaLevel, vis, econ, catalog, rangeLimit)
}

// secondaryCandidates materializes the scanning player's secondary list into
// the per-attempt filter's candidate array [06 §3.1].
//
// The list holds handles filed at the last registry rebuild, up to thirty ticks
// ago, "including entries for units that died in between — which is why the
// per-attempt filter re-tests liveness". Liveness is therefore re-tested here
// and NOTHING else is: no visibility, category, sensor, medium or alliance test
// touches the list again, and the distance test is the one AcquireTarget's
// shared gate applies to both lists.
func (s *Service) secondaryCandidates(u *units.Unit, w *units.World, seaLevel numeric.Fixed, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, rangeLimit int32) []Candidate {
	if s == nil || u == nil || w == nil {
		return nil
	}
	return s.materializeCandidatesInRange(u, w, s.targets.secondaryList(u.Owner), seaLevel, vis, econ, catalog, rangeLimit)
}

// materializeCandidatesInRange forms one attempt-local snapshot from a registry
// list. The sampler consumes this local buffer directly, avoiding a second
// filtered copy. One allocation remains for a nonempty selected population,
// proportional to the registry list; there is no new unit cap [06 §3.1][06 §3.2].
func (s *Service) materializeCandidatesInRange(u *units.Unit, w *units.World, list []pool.Handle, seaLevel numeric.Fixed, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, rangeLimit int32) []Candidate {
	if len(list) == 0 {
		return nil
	}
	out := s.candidateScratch[:0]
	for _, h := range list {
		cand := w.Unit(h)
		if cand == nil || cand.Handle == u.Handle {
			continue
		}
		if !cand.Alive || cand.Dying {
			continue // alive bit set, death latch clear [06 §3.1]
		}
		if !WithinRange(u.X, u.Z, cand.X, cand.Z, rangeLimit) {
			continue
		}
		out = append(out, acquisitionCandidate(u, cand, seaLevel, cand.Flags, catalog))
	}
	s.candidateScratch = out
	if len(out) == 0 {
		// The empty result stays nil: the caller distinguishes "nothing
		// selected" by length, and returning the empty buffer would hand the
		// next call a slice it also owns.
		return nil
	}
	return out
}

// acquisitionCandidate builds the §3.1 candidate record for one unit as seen by
// one shooter. Factored out of acquireTargetForSlotRange so the reaction
// routine's per-slot offer tests exactly the same predicate the autonomous scan
// does [06 §3.1][06 R-WPN-04 §2 part 3].
func acquisitionCandidate(u *units.Unit, cand *units.Unit, seaLevel numeric.Fixed, status uint32, catalog *content.Catalog) Candidate {
	var catMask content.CategoryMask
	if cand.Def != nil {
		catMask = cand.Def.DefinitionMask()
	}
	candGate := gateEndForUnit(cand)
	_, boundsMax := cand.Def.BoundingExtents()
	probeY := cand.Y.Add(numeric.Fixed(boundsMax[1]))
	return Candidate{
		Handle:               cand.Handle,
		X:                    cand.X,
		Z:                    cand.Z,
		Y:                    cand.Y,
		CategoryMask:         catMask,
		CategoryMaskResolved: catalog != nil && cand.Def != nil,
		Hostile:              true,
		OwnSide:              u.Owner == cand.Owner,
		Cloaked:              isCloakedUnit(cand),
		// Direct visibility's water rejection reads the first hull probe, whose
		// Y starts at the definition box's maximum extent [06 §3.1].
		Underwater:     probeY < seaLevel,
		UnderwaterSeen: status&visibility.SonarBit != 0,
		// The gate's target-side operands [06 §3.1][06 R-WPN-05 §1]: the model
		// top-height word the height clause adds, the committed mover mode the
		// `toairweapon` clause requires to read exactly 2, and the water
		// branch's `floater`/`canhover` pair. The `AirTarget` field that stood
		// here carried the definition's `canfly`, which admits a landed
		// aircraft an anti-air weapon cannot engage.
		ModelTop:  candGate.ModelTop,
		MoverMode: candGate.MoverMode,
		UnitMode:  candGate.UnitMode,
		Floater:   candGate.Floater,
		CanHover:  candGate.CanHover,
		// The stunned mark travels with the candidate; only a paralyzer slot
		// reads it [06 §3.2] check 5 [06 R-DMG-01 §11].
		Stunned: cand.Stunned,
		// Check 2's candidate-side disjunct [06 §3.2][04 R-SPEC-01 §5]. A
		// candidate with no definition reads as not authoring the flag, which
		// is the same answer the absent key gives.
		ShootMe: cand.Def != nil && cand.Def.ShootMe,
	}
}

// slotAcquisition builds one weapon slot's acquisition-time gate set. A
// nonnegative rangeLimit overrides the QUERY radius only [06 §3.2]; the gate's
// range clause stays the slot weapon's authored `range` [06 R-WPN-05 §1].
//
// The slot may hold NO active weapon, but only on the one path
// weaponlessSearchRuns admits: the sight-distance caller on a `kamikaze`
// shooter. Every operand this builder takes from the weapon record belongs to
// the §3.1 physical gate or to check 5, and check 3 bypasses that gate
// entirely for such a shooter while no weapon is not a paralyzer, so the zero
// values below are never consulted on that path [06 §3.2][04 R-SPEC-01 §1].
// The bad-target mask is NOT one of them: it is the unit definition's own
// per-slot bitset [06 §3.1], and the weaponless search buckets on it exactly
// as an armed one does.
func slotAcquisition(s *Service, u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, catalog *content.Catalog, seaLevel numeric.Fixed, rangeLimit int32) Acquisition {
	weapon := slot.Weapon
	var (
		gateRange                                  int32
		waterWeapon, toAir, ballistic, isParalyzer bool
	)
	if weapon != nil {
		gateRange = weapon.Range
		waterWeapon, toAir = weapon.WaterWeapon, weapon.ToAirWeapon
		ballistic, isParalyzer = weapon.Ballistic, weapon.Paralyzer
	}
	// The caller's radius filters and materializes the candidate array; it is
	// not the gate's range clause. The §3.1 filter's radius "depends on the
	// caller: the autonomous scan passes the slot weapon's authored `range`,
	// while the sight-distance caller passes the unit definition's sight
	// distance" [06 §3.2], while the unit-to-unit gate's last clause is always
	// "an inclusive signed 32-bit compare against the slot weapon's `range`"
	// [06 R-WPN-05 §1] clause 5, because that gate "takes the shooter unit, the
	// target unit and the slot" and knows nothing of the caller
	// [06 R-WPN-05 §9]. The two coincide for the autonomous caller and differ
	// for the opportunity scan of [04 R-STANCE-01 §3].
	filterRange := gateRange
	if rangeLimit >= 0 {
		filterRange = rangeLimit
	}
	acq := Acquisition{
		Service:  s,
		Weapon:   weapon,
		ShooterX: u.X,
		ShooterZ: u.Z,
		ShooterY: u.Y,
		// The shooter half of the non-water height clause carries the model
		// top-height word too [06 §3.1][06 R-WPN-05 §1] clause 2.
		ShooterModelTop: modelTop(u),
		SeaLevel:        seaLevel,
		Range:           gateRange,
		FilterRange:     filterRange,
		BadTargetMask:   badMaskForSlot(u.Def, idx),
		MaskResolved:    catalog != nil && u.Def != nil,
		WaterWeapon:     waterWeapon,
		ToAir:           toAir,
		Ballistic:       ballistic,
		// A paralyzer slot rejects candidates that already carry the stunned
		// mark [06 §3.2] check 5; every other weapon ignores it, and a slot
		// with no weapon is not one.
		Paralyzer: isParalyzer,
		RNG:       simRNG,
	}
	if vis == nil {
		// Hostile acquisition is visibility-gated. Keep the predicate installed
		// even when the caller supplied no service so Acquisition fails closed.
		acq.Visible = func(Candidate) bool { return false }
	} else {
		acq.Visible = func(c Candidate) bool {
			candUnit := w.Unit(c.Handle)
			if candUnit == nil {
				return false
			}
			return vis.IsVisible(visibility.PlayerID(u.Owner), visibilityTarget(candUnit, candUnit.Flags))
		}
	}
	if ballistic {
		acq.BallisticFeasible = func(c Candidate) bool {
			// The unit-to-unit gate's ballistic clause solves on the delta
			// between the two units' OWN positions [06 R-WPN-05 §1]; no piece
			// is queried. Only the aim-time solve and the shot-time gate see a
			// composed point [06 §3.3][06 R-WPN-05 §9].
			muzzle := Vec3{X: u.X, Y: u.Y, Z: u.Z}
			var tgt Vec3
			if tu := w.Unit(c.Handle); tu != nil {
				tgt = Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}
			} else {
				return false
			}
			dx := tgt.X.Sub(muzzle.X)
			dy := tgt.Y.Sub(muzzle.Y)
			dz := tgt.Z.Sub(muzzle.Z)
			var grav numeric.Fixed
			if terrain != nil {
				grav = terrain.Gravity
			}
			vel := numeric.Fixed(int64(weapon.WeaponVelocity))
			_, ok := BallisticSolve(dx, dy, dz, vel, grav, weapon.MinBarrelAngle)
			return ok
		}
	}
	return acq
}

// SlotAcquisitionAdmits reports whether one candidate passes the §3.1
// acquisition physical gate for one of a unit's weapon slots — the admission
// the reaction routine's per-slot offer names [06 R-WPN-04 §2 part 3].
//
// It is exported because the damage path does not carry the world, terrain,
// and catalog operands the gate needs; the session binds it into
// ReactionSeams.SlotAcquisitionAdmits with those in hand. The legacy
// visibility and ledger arguments remain on that seam's call shape but are not
// read here. It draws no RNG: it is the physical gate alone, never registry
// membership, visibility, alliance, or the scoring pass (I4).
func (s *Service) SlotAcquisitionAdmits(u *units.Unit, idx int, cand *units.Unit, w *units.World, _ *visibility.Service, terrain *world.Terrain, _ *economy.Service, catalog *content.Catalog) bool {
	if u == nil || cand == nil || w == nil {
		return false
	}
	slot := u.SlotAt(idx)
	if slot == nil || slot.Weapon == nil {
		return false
	}
	if cand.Handle == u.Handle || !cand.Alive || cand.Dying {
		return false
	}
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
	c := acquisitionCandidate(u, cand, seaLevel, 0, catalog)
	acq := slotAcquisition(s, u, slot, idx, w, nil, terrain, nil, catalog, seaLevel, -1)
	return acq.admits(c)
}

// checkAdmission is the slot pipeline's shot-time gate [06 §3.3]. It is the
// gate's ONLY site inside the pipeline and it carries exactly the three clauses
// of [06 R-WPN-05 §9], which shotTimeAdmits writes down once for both callers.
//
// It used to mix the acquisition gate's target-side clauses in here — a
// target-Y test, a `toairweapon`/`canfly` test — and to compare the shooter's
// raw 16.16 Y against sea level instead of the whole-unit word plus the model
// top height. [06 R-WPN-05 §9] establishes that the two admissions are two
// distinct routines and that this one has no target-side clause of any kind:
// the unit-to-unit gate runs when a target is INSTALLED on a slot (the order
// handlers and the autonomous scan's per-candidate test), never per shot. The
// merged form refused every ground/point target for a non-water weapon,
// because a point target carries no Y and 0 is never above sea level.
func checkAdmission(s *Service, u *units.Unit, weapon *content.WeaponDef, tgtPos Vec3, terrain *world.Terrain) bool {
	return s.rules().ShotTimeAdmitted(ShotTimeAdmission{
		Service: s,
		Shooter: u,
		Weapon:  weapon,
		Target:  tgtPos,
		Terrain: terrain,
	})
}

func muzzleWorldPosResolved(u *units.Unit, piece int32) (Vec3, bool) {
	if u == nil {
		return Vec3{}, false
	}
	// A negative query result selects the ordinary unit-origin muzzle path;
	// only a nonnegative piece requires the strict COB/model composition [06 §4.1].
	if piece < 0 {
		return Vec3{X: u.X, Y: u.Y, Z: u.Z}, true
	}
	if binding := u.COBBinding(); binding != nil {
		origin, ok := binding.ComposePiece(int(piece), u.Move.Heading, u.Move.Pitch, u.Move.Bank)
		if !ok {
			// The locator's own answer for a unit with no render table or a
			// piece index outside the piece count is the ZERO offset, so the
			// world point is the unit's position [04 R-COB-03 §2]. Declining
			// here instead — which this site used to do — skipped the slot
			// visit outright, so a script answering a piece its model does
			// not carry silenced the weapon rather than firing from the unit.
			return Vec3{X: u.X, Y: u.Y, Z: u.Z}, true
		}
		// ComposePiece is retail's piece locator: its triple is already the
		// WORLD offset `(x, y, −z)`, and the muzzle is that triple added to
		// the unit's own position with no further sign change
		// [03 R-RAST-01 §8] [06 §4.1]. The model/world Z mirror
		// [03 R-RAST-01 §2] is the locator's, applied once on its output;
		// this site used to apply it here instead, and the world point is
		// unchanged by moving it there. Getting the sense wrong reflects a
		// muzzle authored forward of the unit's origin to the same distance
		// behind it, which moves both the spawn point and the aim delta the
		// pitch and yaw solvers are handed [06 §3.3].
		return Vec3{X: u.X.Add(origin[0]), Y: u.Y.Add(origin[1]), Z: u.Z.Add(origin[2])}, true
	}
	return Vec3{}, false
}

// unitStationary reports the shooter's movement tier being category 0, which is
// the predicate the zero-tolerance drift gate selects on [06 R-WPN-03 §2]
// [04 §5.2]. There is no class or category-mask test here: the tight gate
// applies to a stationary unit of any kind and the loose gate to any unit whose
// tier is 1, 2 or 3.
//
// It reads the tier the movement integrator CACHED, not the four terms live.
// The gate's "is it moving" question is answered by the two tier bits the
// classifier writes as its final act, and the classifier's own blocked term is
// the mover's persisted blocked flag, rewritten only at a cross-cell or
// mode-changing proposal's verdict [04 R-COLL-01 §5][04 §5.2 "the mover inhibit
// bit is the blocked flag"]. So a unit whose last cross-cell proposal was
// rejected reads tier 0 — and therefore aims under the tight gate — until its
// next verdict, whatever its speed word says in the meantime.
//
// Corrected 2026-09-02 (WU-19-58). This used to recompute the terms here as
// `carrier != 0 || Move.Speed == 0`, with the blocked term missing entirely
// because the integrator passed it as a literal false. That was wrong twice: a
// blocked mover took the loose gate where retail takes the tight one, and a
// stale verdict could not reach the gate at all.
func unitStationary(u *units.Unit) bool {
	if u == nil {
		return true
	}
	return u.MoveTier == 0
}

// tryFireForSlot is the fire-time half of one slot visit: it binds the
// executor's seams and hands the resolved target point to the family spawner.
//
// targetPoint is the point the slot visit resolved before the executor ran
// [06 R-WPN-04 §1] — for a unit target the `SweetSpot` vertex-box centre, for
// a point target the sea-floored terrain height — and it is what the creator
// solves toward [06 §6.3]. The executor receives it from the pipeline rather
// than re-resolving, so the shot leaves toward exactly the point the aim solve
// and the shot-time gate measured.
func tryFireForSlot(u *units.Unit, slot *units.Slot, idx int, tick uint32, terrain *world.Terrain, simRNG *rng.Simulation, svc *Service, w *units.World, catalog *content.Catalog, targetPoint Vec3) bool {
	if u == nil || slot == nil || slot.Weapon == nil || svc == nil {
		return false
	}
	weapon := slot.Weapon
	var tgt Target
	switch slot.Target.Kind {
	case units.TargetUnit:
		tgt = Target{Kind: TargetUnit, Unit: slot.Target.Unit}
	case units.TargetGround:
		// The creator needs the same point the aim solve used, height
		// included: without a Y the ordinary creator solves its pitch against
		// sea level and the ballistic creator's arc lands short of, or through,
		// the ground the order named [06 R-WPN-04 §1].
		tgt = Target{Kind: TargetPoint, X: slot.Target.X, Y: PointTargetHeight(terrain, slot.Target.X, slot.Target.Z), Z: slot.Target.Z}
	default:
		tgt = Target{Kind: TargetNone}
	}
	var gravity numeric.Fixed
	if terrain != nil {
		gravity = terrain.Gravity
	}
	bridge := svc.callbackBridgeForUnit(u)
	// The normal muzzle path — the unit's own position — is where a shot
	// starts only when no piece resolves: a script that answers a negative
	// piece, or a unit with no script to ask [06 §4.1].
	origin := Vec3{X: u.X, Y: u.Y, Z: u.Z}
	targetWorld := func(h pool.Handle) (Vec3, bool) {
		if h == 0 || h != slot.Target.Unit || w.Unit(h) == nil {
			return Vec3{}, false
		}
		return targetPoint, true // the resolved point, not the unit's position [06 R-WPN-04 §1]
	}
	// [06 §13.2] wire weapon-start events to same service sink installed at composition [06 §4.1] C2
	fireEvents := &combatFireEvents{svc: svc, tick: tick, shooter: u.Handle, pos: origin}
	muzzleWorld := func(piece int32) (Vec3, bool) {
		pos, ok := muzzleWorldPosResolved(u, piece)
		if ok {
			// The start sound and start smoke are emitted at the record, which
			// sits at the muzzle the executor just resolved [06 §4.1].
			fireEvents.pos = pos
		}
		return pos, ok
	}
	// The fire-time muzzle query is the FORCED `Query[k]` — cell 0 seeded 0,
	// `AimFrom[k]` never consulted — run synchronously inside the executor on
	// every shot [06 §4.1][R-P0-07]. Every executor family calls the same
	// routine: the turret after its drift gate, line-of-sight before its
	// solve, vertical launch after its aim-ready test, dropped first of all.
	// A script without the entry leaves the seed, so the piece is 0, the
	// root; a unit with no script at all takes the bare-position path.
	//
	// This used to hand back the slot's retained word, which the aim-time
	// visit had overwritten with the AimFrom piece: the aim origin and the
	// spawn point are two different pieces on most stock models (a Peewee
	// aims from `ruparm`/`luparm` and fires from `rfire`/`lfire`), so the
	// shot left from the shoulder.
	var cSlot Slot
	muzzlePieceFn := func(slotIdx int) int32 {
		piece := int32(-1)
		if bridge != nil {
			piece = bridge.QueryWeapon(cob.WeaponSlot(slotIdx)).QueryValue()
		}
		if weapon.Turret {
			// The drift gate accepted retail-relative yaw. Convert after the
			// synchronous muzzle query and before accuracy draws [06 §4.4].
			cSlot.DesiredYaw += u.Move.Heading
		}
		return piece
	}
	scriptAdapter := &fireScriptAdapter{bridge: bridge, unit: u}
	ports := FirePorts{
		ShooterSide: uint8(u.Owner),
		// The shooter itself, so the fill can run its real-shooter branch in
		// place: the reference beside the side byte and the `tick + 600`
		// reveal stamp, both ahead of the start sound [06 §4.1]. This is the
		// only site that binds a real shooter to a fresh projectile record.
		Shooter:     u,
		Origin:      origin,
		MuzzlePiece: muzzlePieceFn,
		// The fire-time interceptor rescan, bound ONLY for an `interceptor`
		// weapon [06 §11.2]. The vertical-launch executor performs it
		// immediately before firing and the vertical creator stores what it
		// returns as the new interceptor's matched-projectile link
		// [06 §4.4][06 §6.6]. It is the same scan the aim-time acquisition
		// ran — same gates, same pool order — re-run because the pool has
		// moved since: a candidate may have been claimed by another launcher,
		// or left coverage, in the ticks between the two.
		//
		// The `combat.Slot` TryFire hands back is the pipeline's per-shot copy;
		// the ammunition byte the scan gates on is the same value, so the copy
		// is read rather than the live slot to keep the executor reading one
		// slot record.
		InterceptorRescan: interceptorRescanPort(svc, u, weapon, catalog),
		MuzzleWorld:       muzzleWorld,
		TargetWorld:       targetWorld,
		Gravity:           gravity,
		Script:            scriptAdapter,
		Events:            fireEvents,
		RNG:               simRNG,
		Spy:               nil,
		// The three shooter terms of the turret spread's computed bound
		// [06 §4.4] [06 R-WPN-03 §4].
		ShooterHealth:    u.Health,
		ShooterMaxHealth: u.MaxHealth,
		ShooterKills:     u.Kills,
	}
	// The fire-attempt query, armed only for a rule set that previews the
	// launch: the retail path presents nothing and pays nothing
	// [06 R-WPN-05 §1]. The spawner fills in the muzzle, aim and post-spread
	// slot and puts the query to the rule set at the one admission point.
	//
	// The query is the service's, not this frame's, because the spawner hands
	// it to an interface, which would force a heap query on every attempt. The
	// attempt puts back the value it found, so the field is a stack of one and
	// an attempt reached from a fire callback cannot clobber an outer one. It
	// is transient scratch: no phase reads it outside the attempt that armed
	// it, and nothing saves it.
	var savedQuery ShotQuery
	if previewsShot(svc.rules()) {
		var target *units.Unit
		if tgt.Kind == TargetUnit && w != nil {
			target = w.Unit(tgt.Unit)
		}
		savedQuery = svc.shotQuery
		svc.shotQuery = ShotQuery{Service: svc, World: w, Shooter: u, Tick: tick, Terrain: terrain, Target: target, Wind: svc.ProjectileWind}
		ports.Shot = &svc.shotQuery
	}
	cSlot = Slot{
		Weapon:       weapon,
		Reload:       slot.Reload,
		Flags:        slot.Flags,
		DesiredYaw:   slot.DesiredYaw,
		DesiredPitch: slot.DesiredPitch,
		Ammo:         slot.Ammo,
		MuzzlePiece:  slot.MuzzlePiece,
		// The `T0` divisor the ballistic creator reads. It is copied, never
		// written back: aiming/firing preserve the initialized or restored word
		// [06 R-WPN-05 §3][06 §6.4][08 R-SAVE-WEAPON-01].
		DistanceWord: slot.DistanceWord,
		Aim:          slot.Aim,
		Target:       tgt,
	}
	// Fire and RockUnit run before cSlot is copied back. Bind recoil to the
	// shot-state owner so RockUnit observes the just-mutated spread [06
	// R-WPN-05 §5].
	scriptAdapter.slot = &cSlot
	_, ok := TryFire(svc, &cSlot, idx, tgt, tick, ports)
	blocked := false
	if ports.Shot != nil {
		blocked, svc.shotQuery = ports.Shot.Blocked || ports.Shot.Covered, savedQuery
	}
	if blocked {
		// Modern policy: a refused launch keeps the relative Aim pair. The
		// muzzle query converted its temporary yaw to absolute; copying that
		// back would add hull heading again on every retry [06 R-WPN-05 §4].
		slot.MuzzlePiece = cSlot.MuzzlePiece
		return false
	}
	// The spread's mutation of the slot's stored angles is retained whether or
	// not the allocation succeeded [06 §4.4].
	slot.DesiredYaw = cSlot.DesiredYaw
	slot.DesiredPitch = cSlot.DesiredPitch
	if ok {
		slot.MuzzlePiece = cSlot.MuzzlePiece
		slot.Aim = cSlot.Aim
		slot.Flags = cSlot.Flags
		// The shooter reference and side byte are no longer re-bound here.
		// [06 §4.1] writes both inside the common initializer, before the
		// start sound and the Fire/RockUnit callbacks; TryFire now does that
		// from FirePorts.Shooter, and stamps the reveal deadline in the same
		// branch. Repeating the writes after the spawner returned was harmless
		// but put them on the wrong side of the fixed callback order.
		return true
	}
	slot.MuzzlePiece = cSlot.MuzzlePiece
	slot.Aim = cSlot.Aim
	return false
}

type fireScriptAdapter struct {
	bridge *cob.CallbackBridge
	unit   *units.Unit
	slot   *Slot
}

func (a *fireScriptAdapter) FireWeapon(slotIdx int) {
	if a == nil || a.unit == nil {
		return
	}
	if a.bridge == nil {
		return
	}
	a.bridge.Fire(cob.WeaponSlot(slotIdx))
}

func (a *fireScriptAdapter) RockUnit(slotIdx int) {
	if a == nil || a.unit == nil {
		return
	}
	if a.bridge == nil {
		return
	}
	u := a.unit
	if a.slot == nil {
		return
	}
	// RockUnit's recoil direction is the slot's stored yaw minus the heading,
	// in retail's convention [06 R-WPN-05 §3][06 R-WPN-05 §5]. By this point
	// the stored yaw is absolute and carries the accuracy draw — which for an
	// ordinary-family weapon is the ONLY thing the draw reaches.
	rel := int16(a.slot.DesiredYaw - u.Move.Heading)
	a.bridge.RockUnit(rel)
}

// combatFireEvents forwards weapon start sound/smoke to the service event sink
// [06 §4.1] C2 [06 §13.2] ordering: start sound before Fire/Rock, start smoke after.
type combatFireEvents struct {
	svc     *Service
	tick    uint32
	shooter pool.Handle
	pos     Vec3
}

func (c *combatFireEvents) StartSound(name string) {
	if c == nil || c.svc == nil || name == "" {
		return
	}
	c.svc.emitEvent(Event{Kind: EventStartSound, Tick: c.tick, Source: c.shooter, Position: c.pos, Sound: name})
}

func (c *combatFireEvents) StartSmoke(h pool.Handle) {
	if c == nil || c.svc == nil {
		return
	}
	c.svc.emitEvent(Event{Kind: EventStartSmoke, Tick: c.tick, Source: c.shooter, Target: h, Position: c.pos})
}

// EventStartSound and EventStartSmoke are weapon-start presentation events emitted
// before/after Fire callbacks [06 §4.1] C2 [06 §13.2]. EventTrailSmoke is the
// projectile phase's trail-style puff (the trail-deadline branch and the
// non-burn-blow timer-expiry puff — the same E-family smoke at the projectile
// position [06 §13.2][R-STRIP-01 §1 strip 9]). Defined here to keep the
// service file as the sole owner of the start-event wiring; pool.go owns the
// impact ordering sink [06 §13.2] C27.
const (
	EventStartSound EventKind = 10 // [06 §4.1] C2 [06 §13.2]
	EventStartSmoke EventKind = 11 // [06 §4.1] C2 [06 §13.2]
	EventTrailSmoke EventKind = 12 // [06 §13.2][R-STRIP-01 §1 strip 9]
	// EventDamageFlash is the minimap blink of [06 R-WPN-04 §2]: every accepted
	// non-heal packet writes the victim's flash byte before the reaction step,
	// and while it is nonzero the minimap does not draw that unit's dot.
	EventDamageFlash EventKind = 13
)

// DamageFlashTicks is the flash's life in unit visits. The byte is written to
// 240 and decremented as a SIGNED byte once per visit while it is nonzero —
// 240 reads as -16 — so it reaches zero after sixteen visits [06 R-WPN-04 §2].
const DamageFlashTicks int32 = 16

// TickProjectiles is the projectile phase [06 §5]. It captures the active span
// once, then walks it in ascending order: each record takes either its burst
// expansion branch or its motion branch, followed by collision and impact as
// applicable. Records appended during that walk, including burst clones, wait
// for the next tick. The tail compactor is the exception: it reads the CURRENT
// count and includes those records [06 §4.3] [06 §5.1] [I1].
func (s *Service) TickProjectiles(tick uint32, w *units.World, terrain *world.Terrain, windState *world.Wind, featSvc *features.Service, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil {
		return
	}
	// Capture the active prefix before the phase's one ascending walk. Each
	// captured record exclusively takes its burst or motion branch, so a clone
	// appended by burst expansion lies beyond the prefix and waits until the
	// next tick [06 §7.1] [06 §4.3] [06 §5.1] [I1].
	entry := s.Count()
	// DET-01: no global fallback; session must inject simRNG.
	// ON-04 stable lookup: use once-compiled catalog index, not per-tick map rebuild
	// A burst anchor is parked at its shooter's muzzle and re-runs the
	// piece-to-world conversion with the piece identity the root stored at
	// creation — no COB query, so it can never alternate between the Query
	// and AimFrom callbacks [06 §4.3][06 "Per-slot and aim-time pipeline"]
	// [R-P0-07]. Returning a zero vector here instead parked every burst
	// anchor at the world origin and teleported each pellet there with it,
	// which is why burst weapons never reached their targets.
	muzzleForBurst := func(shooter pool.Handle, piece int16) (Vec3, bool) {
		if w == nil || shooter == 0 {
			return Vec3{}, false
		}
		su := w.Unit(shooter)
		if su == nil || !su.Alive {
			return Vec3{}, false
		}
		return muzzleWorldPosResolved(su, int32(piece))
	}
	// ON-04 stable lookup for burst params: use once-compiled index deterministically
	// [06 §6.4] ballistic/dropped drift: adds all three global wind values directly to position
	var windVec Vec3
	var gravity numeric.Fixed
	var seaLevel numeric.Fixed
	if terrain != nil {
		gravity = terrain.Gravity
		seaLevel = terrain.SeaLevelWorld()
	}
	if windState != nil {
		// The published wind words go in AS THEY ARE [06 R-WPN-05 §8]: the X
		// word is `-2 x sin(heading, speed)` and the Z word `-2 x cos(heading,
		// speed)` over the integer wind speed [01 §7.3], and the ballistic and
		// dropped integrators add them to the 16.16 position words with no
		// shift. So a shell drifts by `windX / 65536` world units per tick —
		// at the largest stock `maxwindspeed` of 5,000, about 0.15 world units
		// per tick. Storing them as raw 16.16 velocity increments, which is
		// what this does, is exact; the marker asking whether the scale was
		// raw or world units is retired. The x8 of the smoke family and the x2
		// of the feature fire probe belong to those contracts [03 R-WIND-01],
		// not here. The Y word has no writer and is the zero the battle
		// started with.
		windVec = Vec3{
			X: numeric.Fixed(int64(windState.DirX)),
			Y: numeric.Fixed(0),
			Z: numeric.Fixed(int64(windState.DirZ)),
		}
		_ = windState.Scalar // scalar published to wind generators [01 §7.3] I2 allowlist
	}
	// The live lookups behind the guidance target-point helper [06 §6.7]. Both
	// are built ONCE, outside the record loop, so the per-record cost is a call
	// and not a closure allocation. The projectile lookup does not filter dead
	// records on purpose: retail dereferences a projectile-to-projectile link
	// with no liveness check at all [06 §5.2].
	guidance := GuidanceEnv{
		Service: s,
		Projectile: func(h pool.Handle) *Projectile {
			idx := int(h) - 1
			if idx < 0 || idx >= len(s.Records) {
				return nil
			}
			return &s.Records[idx]
		},
		Unit: func(h pool.Handle) *units.Unit {
			if w == nil {
				return nil
			}
			return w.Unit(h)
		},
		// The cruise helper's below-threshold branch samples the terrain for
		// `max(terrainHeight(storedTarget), seaLevel)` [06 §6.8].
		Terrain: terrain,
	}
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		p := &s.Records[i]
		if p.BurstRemaining > 0 {
			// Cancel only the unlaunched remainder. The parked template never
			// becomes a moving shot, and previously cloned pellets continue.
			// A burst an ordered shot began completes whatever becomes of the
			// order.
			// Nanolathe Modern policy: docs/DESIGN_WEAPONS_PROJECTILES.md §2.6.1.
			if w != nil && s.holdsFire(w.Unit(p.Shooter), p.OrderedBurst) {
				s.MarkDead(h)
				continue
			}
			s.advanceBurstAt(i, tick, simRNG, s.weaponLookupFor(catalog), muzzleForBurst)
			continue
		}
		var weapon *content.WeaponDef
		if catalog != nil {
			if w, ok := catalog.WeaponByID(p.WeaponID); ok {
				weapon = w
			}
		}
		if weapon == nil {
			s.MarkDead(h)
			continue
		}
		// The authored propeller advance is common record entry work: it runs
		// before the selected family tests expiry or motion [06 §6.1][06 §7.1].
		// Meteor's separate velocity-derived accumulator remains in its family.
		if weapon.Propeller {
			p.PropellerYaw = p.PropellerYaw.Add(1024)
		}
		// [06 §5.1] has no top-of-visit dead filter. A record retired earlier
		// in this tick still takes its ordinary branch before tail compaction.
		preMotionY := int16(p.Pos.Y.Raw() >> 16)
		// The family dispatch of [06 §6.2] lives in Advance, which is this
		// switch and nothing else; keeping one copy keeps the phase and the
		// terrain-admission preview reading the same ladder.
		res := Advance(p, weapon, tick, windVec, gravity, seaLevel, guidance)
		if res == AdvanceRetire {
			// Timer expiry without burn-blow emits exactly ONE trail-style
			// puff and then retires silently — no sound, no shake, no
			// explosion art, no damage; burn-blow expiry routes into the
			// full central impact instead [06 §13.2][R-STRIP-01 §1 strip 9,
			// the projectile phase's impact branch]. Only the expiry
			// retirement puffs: steering-failure and nil-record retires
			// never reach their expiry deadline.
			if MotionFamilyForWeapon(weapon) == MotionBallistic && !weapon.BurnBlow && tick >= p.ExpiryTick {
				s.emitEvent(Event{Kind: EventTrailSmoke, Tick: tick, Source: p.Shooter, Target: h, Position: p.Pos})
			}
			s.MarkDead(h)
			continue
		}
		if res == AdvanceImpact {
			// Motion-triggered impact has no direct unit. A direct recipient is
			// supplied only by the ordinary unit-contact ladder [06 §9.1].
			impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, 0)
			continue
		}
		if res == AdvanceImpactThenContinue {
			// Self-propelled burn-blow expiry impacts before its existing
			// velocity continues through motion and collision [06 §6.6].
			impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, 0)
			ContinueSelfPropImpactMotion(p)
		}
		if res == AdvanceImpactThenRebuildSelfProp {
			// Steering failure impacts before rebuilding velocity; motion and
			// collision still run in this visit [06 §6.7].
			impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, 0)
			p.Velocity = VelocityFromAngles(p.Yaw, p.Pitch, p.Speed)
			ContinueSelfPropImpactMotion(p)
		}
		// A first two-phase expiry transition has already applied gravity and
		// position in AdvanceSelfProp; it still falls through to collision and
		// the common live-record tail [06 §6.6][06 §7.1].
		// Collision rejects an off-map point before linked proximity can
		// impact. Motion-triggered impacts above retain their earlier position
		// in the family visit [06 §8.1][06 R-DMG-01 §14].
		if projectileOffMap(p, terrain) {
			victim, keep := s.communityOffMapProjectile(p, w, terrain, tick)
			if !keep {
				s.MarkDead(h)
			} else if victim != 0 {
				impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, victim)
				// The ordinary impact path intentionally leaves noexplode records
				// live. CP-ENV-1 explicitly spends an off-map direct hit anyway.
				if weapon.NoExplode {
					s.MarkDead(h)
				}
			}
			continue
		}
		// The linked-projectile test is the first contact-ladder operation.
		// Its impact may retire records and append effects, so run it before
		// inspecting the cell contacts that follow [06 §8.1][06 §11.2].
		if projectileProximityContact(s, p, weapon) {
			impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, 0)
		}
		hitUnit, hitFeature, isWaterTerrain, isOffMap, terrainContact, _ := checkCollision(p, weapon, w, terrain, featSvc, s.OpaqueLiquidMode)
		if isOffMap {
			s.MarkDead(h)
			continue
		}
		needImpact := false
		var directTarget pool.Handle
		if hitUnit != 0 {
			needImpact = true
			directTarget = hitUnit
		} else if hitFeature != nil {
			needImpact = true
		} else if isWaterTerrain {
			needImpact = true
		} else if terrainContact {
			needImpact = true
		}
		if needImpact {
			impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, directTarget)
		}
		// The engine's in-map ladder gets first refusal. Only a still-live,
		// ordinary round gets the canonical off-map bucket second chance;
		// noexplode has no reliable spent signal and is excluded [CP-ENV-1].
		if !s.IsDead(h) && !weapon.NoExplode {
			if victim := s.communityOnMapOffMapVictim(p, weapon, w, terrain); victim != 0 {
				impactProjectile(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, victim)
			}
		}
		// Trail puffs: the smoke-trail flag plus smoke delay, ALIVE records
		// only, never burst parents, past the next-trail deadline. The
		// deadline update is ADDITIVE — the smoke delay is added — so a
		// zero delay emits on every eligible tick after the first
		// [06 §13.2][R-STRIP-01 §1 strip 9, the projectile phase's
		// trail-window branch]. A visit that just impacted marked the
		// record dead, which skips the puff.
		if weapon.SmokeTrail && p.BurstRemaining == 0 && !s.Slots.IsDead(h) && tick < p.ExpiryTick && p.SmokeDeadline < tick {
			s.emitEvent(Event{Kind: EventTrailSmoke, Tick: tick, Source: p.Shooter, Target: h, Position: p.Pos})
			p.SmokeDeadline += uint32(weapon.SmokeDelay)
		}
		if !s.Slots.IsDead(h) {
			emitWaterCrossing(s, h, p, weapon, terrain, tick, preMotionY)
		}
	}
	s.Compact(nil)
	s.rules().CombatTick(s, tick, true)
}

func projectileProximityContact(s *Service, p *Projectile, weapon *content.WeaponDef) bool {
	if s == nil || p == nil || weapon == nil || p.TargetProjectile == 0 {
		return false
	}
	// Guidance links address the raw backing arena. They are neither active
	// pool handles nor targetability references [06 §5.2][06 §8.1].
	idx := int(p.TargetProjectile) - 1
	return idx >= 0 && idx < len(s.Records) && ProjectileInInterceptorBlast(p.Pos, s.Records[idx].Pos, weapon.AreaOfEffect)
}

func projectileOffMap(p *Projectile, terrain *world.Terrain) bool {
	if terrain == nil {
		return false
	}
	cx, cz := world.WorldToCell(p.Pos.X), world.WorldToCell(p.Pos.Z)
	return cx < 0 || cz < 0 || cx >= terrain.CellW || cz >= terrain.CellH
}

func checkCollision(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, opaqueLiquid bool) (hitUnit pool.Handle, hitFeature *features.Instance, isWaterTerrain bool, isOffMap bool, terrainContact bool, bounce bool) {
	return checkCollisionWithContact(p, weapon, w, terrain, featSvc, opaqueLiquid, nil)
}

// Modern forecasts replace only unit occupancy on copied projectile state;
// terrain, features and the contact ordering remain the same [06 §8.1].
func checkCollisionWithContact(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, opaqueLiquid bool, contact func(*Projectile, *units.World, *world.Terrain, int32, int32) pool.Handle) (hitUnit pool.Handle, hitFeature *features.Instance, isWaterTerrain bool, isOffMap bool, terrainContact bool, bounce bool) {
	if contact == nil {
		contact = contactUnitInCell
	}
	if terrain != nil {
		if projectileOffMap(p, terrain) {
			isOffMap = true
			return
		}
		cx := world.WorldToCell(p.Pos.X)
		cz := world.WorldToCell(p.Pos.Z)
		// Step 2 [06 §8.1]: the cached average floor height, written on every
		// in-map tick before the unit-slot tests and after the in-map test —
		// an off-map record retires above without sampling a cell
		// [R-DMG-01 §14]. Unsigned division of the cell's two height bytes; no
		// later test in this ladder reads it, the draw pass does [03 §5.4].
		if cell := terrain.PlotAt(cx, cz); cell != nil {
			p.CachedFloorHeight = int16((uint16(cell.MaxHeight()) + uint16(cell.MinHeight())) / 2)
		}
		// Steps 3 and 4 of the ladder run BEFORE feature, terrain and water
		// [06 §8.1]: the two unit slots of the projectile's own cell, then
		// "units-only early return", and only then feature resolution. This
		// build used to resolve feature/terrain/water first, which made a unit
		// standing on a feature cell — or wading — unreachable by a shell that
		// arrived in the same cell.
		if hit := contact(p, w, terrain, cx, cz); hit != 0 {
			hitUnit = hit
			return
		}
		// A units-only miss leaves the projectile flying. It must not continue
		// into feature, ground, bounce, or water handling [06 §8.1].
		if weapon != nil && weapon.UnitsOnly {
			return
		}
		// Step 6 [06 §8.1]: feature resolution. The cached cell pair is
		// consulted ONLY once a feature resolves and its height test passes
		// [R-DMG-01 §13]: a matching pair cancels this feature's impact and
		// leaves the cache alone, otherwise the pair is overwritten and the
		// feature is hit. Nothing writes the pair on a featureless cell, so it
		// survives across ticks — and across record reuse, since no creator
		// clears it. This build used to compare and overwrite the pair on every
		// in-map tick before looking for a feature, which made the cache a
		// one-tick memory instead of retail's last-feature-cell memory.
		featureContact := func() bool {
			cache := [2]int32{p.CacheCellX, p.CacheCellZ}
			suppressed := FeatureCacheSuppressed(&cache, int32(cx), int32(cz))
			p.CacheCellX, p.CacheCellZ = cache[0], cache[1]
			return !suppressed
		}
		if cell := terrain.PlotAt(cx, cz); cell != nil && !cell.IsEmpty() {
			ax, az := int(cx), int(cz)
			if cell.IsFringe() {
				ax += int(cell.AnchorDXSigned())
				az += int(cell.AnchorDZSigned())
			}
			if defIdx, ok := world.ResolveFeature(terrain.Plot, int(terrain.CellW), int(terrain.CellH), ax, az); ok {
				if def, ok := terrain.FeatureDefAt(defIdx); ok && def != nil {
					top := int16(int32(cell.MinHeight()) + int32(uint8(def.Height)))
					if int16(p.Pos.Y.Raw()>>16) < top && featureContact() {
						if featSvc != nil {
							hitFeature = featSvc.InstanceAt(ax, az)
						}
						if hitFeature == nil {
							hitFeature = &features.Instance{CX: ax, CZ: az, Def: def}
						}
						return
					}
				}
			}
		}
		cell := terrain.PlotAt(cx, cz)
		if cell != nil && int16(p.Pos.Y.Raw()>>16) < int16(cell.MinHeight()) {
			if weapon != nil && weapon.GroundBounce {
				p.Velocity.Y = numeric.Fixed(int64(-(p.Velocity.Y.Raw() >> 2)))
				bounce = true
				return
			}
			terrainContact = true
			return
		}
		sea := int16(terrain.SeaLevel)
		if int16(p.Pos.Y.Raw()>>16) < sea {
			if weapon != nil && !weapon.WaterWeapon {
				// Opaque liquid mode ends the ladder here. This is distinct from
				// central impact's water retirement: a direct unit was already
				// returned above and is still allowed to impact [06 §8.2].
				if opaqueLiquid {
					return
				}
				isWaterTerrain = true
				return
			}
		}
	}
	return
}

// contactUnitInCell is steps 3 and 4 of the contact ladder [06 §8.1]: the two
// occupancy words of the ONE plot cell under the projectile's post-motion
// point, ground word first and air word second [03 §2.2].
//
// There is no radius. A unit is a candidate iff its pool index IS the value in
// one of those two words, so the XY gate is exactly the footprint rectangle the
// occupancy stamper wrote [04 R-COLL-01 §4] — up to footprintX × footprintZ
// cells, never a disc. Nothing here computes a planar distance, builds a
// candidate list, or prefers a nearer unit; the first word whose occupant
// passes both gates impacts and returns [06 R-DMG-01 §7].
//
// The owner test compares the unit's owner byte with the projectile's side
// byte, so an ALLIED unit standing on the cell is a valid contact and only the
// shooter's own side is exempt; a shooter-less record carries the neutral side
// byte, which differs from every player slot and therefore contacts anyone
// [06 R-DMG-01 §7][06 §6.5].
//
// The vertical band is asymmetric because the two words hold different
// classes: the ground word holds occupants whose extent runs base-to-top, so
// its test is `point.Y < unit.Y + modelTop` with no lower bound; the air word
// holds the flying class in an altitude band, so its test is
// `unit.Y <= point.Y <= unit.Y + modelTop`, both ends inclusive. All compares
// are signed 32-bit on full 16.16 values, and modelTop is the definition's
// 16.16 model-top walk, floored at zero [06 §8.1][06 R-DMG-01 §7].
func contactUnitInCell(p *Projectile, w *units.World, terrain *world.Terrain, cx, cz int32) pool.Handle {
	if p == nil || w == nil || terrain == nil {
		return 0
	}
	cell := terrain.PlotAt(cx, cz)
	if cell == nil {
		return 0
	}
	py := int32(p.Pos.Y.Raw())
	if u := contactCandidate(w, p, cell.OccupantA()); u != nil {
		lower, upper := contactBand(u)
		if CollisionSlotYGate(py, lower, upper, 0) {
			return u.Handle
		}
	}
	if u := contactCandidate(w, p, cell.OccupantB()); u != nil {
		lower, upper := contactBand(u)
		if CollisionSlotYGate(py, lower, upper, 1) {
			return u.Handle
		}
	}
	return 0
}

// contactCandidate resolves one occupancy word to the live unit it names and
// applies the owner-differs gate [06 §8.1] steps 3 and 4. A zero word is the
// free sentinel; a word naming a freed slot resolves to nil.
func contactCandidate(w *units.World, p *Projectile, word int16) *units.Unit {
	if word <= 0 {
		return nil
	}
	u := w.Unit(pool.Handle(word))
	if u == nil || u.Def == nil {
		return nil
	}
	if u.Owner == p.ShooterSide {
		return nil
	}
	return u
}

// contactBand returns the slot band's two 16.16 bounds for a unit: its own
// current height and that height plus the definition's full 16.16 model top,
// added as a signed 32-bit value the way retail forms it [06 R-DMG-01 §7].
func contactBand(u *units.Unit) (lower, upper int32) {
	lower = int32(u.Y.Raw())
	top := u.Def.ModelTopFixed
	if top < 0 {
		top = 0 // the walk is floored at zero [06 R-DMG-01 §7]
	}
	return lower, lower + top
}

// impactProjectile is the live central-impact boundary. The direct recipient is
// a collision result, not the projectile's retained guidance reference.
func impactProjectile(s *Service, h pool.Handle, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, wind Vec3, simRNG *rng.Simulation, directUnit pool.Handle) {
	// Cycle cut. The interceptor sweep in handleProjectileImpact deliberately
	// re-reads the live pool, and the pre-sweep retirement below is what
	// normally stops a sweep from selecting its own exploder again — but a
	// `noexplode` weapon is NOT retired there [06 §13.2] C28, so two records
	// whose weapons are both `interceptor` and `noexplode` and which lie in
	// each other's unhalved area of effect sweep one another without end and
	// exhaust the stack.
	//
	// This admits a record only once per impact stack, which cuts exactly those
	// cycles: a re-entry of a record whose own impact is still running below us
	// on this stack. It is neither a depth cap nor a visited set — a record
	// that has already impacted and returned is still eligible for a later
	// sibling sweep — so the visit set, the event order and the RNG draw order
	// are unchanged for every case that does not re-enter. A case that DOES
	// re-enter needs the re-entered record to still be alive while a sweep runs
	// over it, which needs its weapon to be both `interceptor` (to sweep at
	// all) and `noexplode` (to have survived its own impact): the same pair the
	// runaway recursion needs. No stock weapon authors both — stock has four
	// `interceptor` weapons, all `noexplode=0`, and two `noexplode` weapons,
	// neither an interceptor — so no stock impact reaches this guard [06 §11.2].
	if s != nil && h != 0 {
		if !s.beginImpact(h) {
			return
		}
		defer s.endImpact()
	}
	// Central impact sets the ordinary dead bit before effects and the
	// interceptor sweep. That makes a nested interceptor scan observe this
	// exploder as retired, rather than recursively selecting it again.
	if weapon != nil && NoExplodeRetirement(weapon.NoExplode, true, false, false) {
		s.MarkDead(h)
	}
	handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, wind, simRNG, directUnit)
}

// StackImpactRecord is the nonpooled shape the death handler passes to the
// central impact path. The two point fields, null identities, and owner-side
// byte are the complete initialized stack record [06 §12.2][06 R-WPN-02 §5].
// Velocity and state fields do not belong here: the central path never reads
// them for this record shape.
type StackImpactRecord struct {
	Weapon      *content.WeaponDef
	Point       Vec3
	SecondPoint Vec3
	Shooter     pool.Handle
	ShooterSide uint8
	DirectUnit  pool.Handle
}

// ImpactStackRecord runs central impact for an unpooled stack record. It does
// not apply pooled-record retirement; all presentation and damage branches are
// shared with ordinary projectile impact [06 §12.2][06 R-WFX-01 §§2–3].
func (s *Service) ImpactStackRecord(record StackImpactRecord, w *units.World, terrain *world.Terrain, catalog *content.Catalog, tick uint32) {
	if record.Weapon == nil {
		return
	}
	p := Projectile{
		Pos:         record.Point,
		StartPos:    record.SecondPoint,
		Shooter:     record.Shooter,
		ShooterSide: record.ShooterSide,
		TargetUnit:  record.DirectUnit,
	}
	handleProjectileImpact(s, 0, &p, record.Weapon, w, terrain, nil, nil, catalog, tick, Vec3{}, nil, record.DirectUnit)
}

func handleProjectileImpact(s *Service, h pool.Handle, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, wind Vec3, simRNG *rng.Simulation, directUnit pool.Handle) {
	if weapon == nil {
		return
	}
	hasDirectTarget := directUnit != 0
	isWaterTerrain := impactCellIsWater(terrain, p.Pos)
	if s != nil && s.OpaqueLiquidMode && isWaterTerrain && !hasDirectTarget {
		if h != 0 {
			s.MarkDead(h)
		}
		return
	}
	// Impact presentation is part of the concrete projectile path. Keep the
	// researched order visible here: shake, impact sound, end smoke or
	// explosion, then the impact event and authoritative damage [06 §13.2].
	if weapon.ShakeMagnitude != 0 || weapon.ShakeDuration != 0 {
		s.emitEvent(Event{Kind: EventShake, Tick: tick, Source: p.Shooter, Position: p.Pos, Magnitude: weapon.ShakeMagnitude, Duration: weapon.ShakeDuration})
	}
	if hasDirectTarget || !isWaterTerrain {
		if weapon.SoundHit != "" {
			s.emitEvent(Event{Kind: EventHitSound, Tick: tick, Source: p.Shooter, Position: p.Pos, Sound: weapon.SoundHit})
		}
	} else if weapon.SoundWater != "" {
		s.emitEvent(Event{Kind: EventWaterSound, Tick: tick, Source: p.Shooter, Position: p.Pos, Sound: weapon.SoundWater})
	}
	// One predicate selects the arm, and the end-smoke flag is tested INSIDE the
	// land/direct arm, where the end puff replaces the explosion art outright
	// [06 §13.2][06 R-WFX-01 §2]. "Land branch" in §13.2 names that arm — the
	// same hit-sound arm selected just above — not the cell class: a direct hit
	// on a unit standing in a water cell takes the land/direct arm, so an
	// end-smoke weapon emits the end puff and no explosion art there, and a
	// weapon without the flag takes the LAND art holder over water.
	isWaterExplosion := isWaterTerrain && !hasDirectTarget
	if weapon.EndSmoke && !isWaterExplosion {
		s.emitEvent(Event{Kind: EventEndSmoke, Tick: tick, Source: p.Shooter, Position: p.Pos})
	} else {
		bank, graphic := impactArt(weapon, isWaterExplosion, terrain)
		// The explosion-pool allocation does not depend on the art. Every
		// impact allocates a record with calculated table 0 as its secondary
		// cursor, and a weapon whose art holder is null — a misspelled
		// `explodeas`, or weapon record 0 — still shows the disc
		// [06 R-WFX-01 §2]. The observable retail result of a null holder on
		// land is a 24-tick calculated flash plus a smoke puff: a presentation
		// event, not nothing. Gating the event on the art meant an impact with
		// no art produced nothing at all.
		{
			kind := EventExplosion
			if isWaterExplosion {
				kind = EventWaterExplosion
			}
			// Smoke carries the weapon's start-smoke flag: the land/water
			// impact effect variants each append a strip-9 smoke object
			// under that second weapon flag [R-STRIP-01 §1 strip 9].
			s.emitEvent(Event{
				Kind: kind, Tick: tick, Source: p.Shooter, Position: p.Pos,
				Graphic: graphic, Bank: bank,
				HasBlastProfile: true, BlastAreaOfEffect: weapon.AreaOfEffect, BlastDamage: weapon.DamageDefault,
				// "the central impact passes (point, land or water holder, 0,
				// waterCell) — so EVERY projectile impact, land or water, draws
				// calculated table 0 under its art" [06 R-WFX-01 §2].
				HasCalculatedFlash: true, CalculatedTable: impactFlashTable,
			})
		}
	}
	s.emitEvent(Event{Kind: EventProjectileImpact, Tick: tick, Source: p.Shooter, Target: directUnit, Position: p.Pos})
	var feedback impactFeedback
	if s.rules().DetonationBroadcast(s, p, weapon) {
		feedback = applyProjectileDamage(s, p, weapon, w, terrain, tick, directUnit)
	}
	directFeedback := directUnit != 0 && weapon.AreaOfEffect <= 16
	if directFeedback {
		feedback.publish(w)
	}
	// Projectile victims are swept after ordinary area recipients, before tail
	// compaction, and each takes this same central selector [06 §11.2].
	if weapon.Interceptor && catalog != nil {
		// This scan deliberately observes the live pool on each iteration.
		// A nested central impact can retire a later victim or append another
		// record, both of which affect the remainder of this sweep [06 §11.2].
		for i := 0; i < s.Count(); i++ {
			victim := pool.Handle(i + 1)
			if victim == h || !s.Alive(victim) || !ProjectileInInterceptorBlast(s.Records[i].Pos, p.Pos, weapon.AreaOfEffect) {
				continue
			}
			vw, ok := catalog.WeaponByID(s.Records[i].WeaponID)
			if !ok || vw == nil {
				s.MarkDead(victim)
				continue
			}
			impactProjectile(s, victim, &s.Records[i], vw, w, terrain, featSvc, econ, catalog, tick, wind, simRNG, 0)
		}
	}
	if !directFeedback {
		feedback.publish(w) // after nested interceptor impacts [06 §9.4]
	}
	_ = wind
}

// MapWeaponMarker reports whether presentation should draw this projectile's
// minimap or strategic-map marker. CP-WPN-5 deliberately uses the exact same
// damage-0, attacker-less and authored-tag predicate as detonation; true means
// show the marker. A nil or unbound service answers Strict 3.1 and shows it.
func (s *Service) MapWeaponMarker(p *Projectile, weapon *content.WeaponDef) bool {
	return s.rules().DetonationBroadcast(s, p, weapon)
}

func applyProjectileDamage(service *Service, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, tick uint32, directUnit pool.Handle) impactFeedback {
	if w == nil || weapon == nil {
		return impactFeedback{}
	}
	// Gate 1 of the central impact routine [06 §9.1][06 R-DMG-01 §9]: the
	// damage gate is a property of the PROJECTILE's own side, not of the
	// victim, and it skips damage ONLY for an occupied row whose control byte
	// is 3. An unoccupied row passes — including the never-occupied eleventh
	// row selected by a meteor's neutral side byte. A death record instead
	// carries its dying owner's side and must pass that owner's routing gate.
	// When it does skip, the camera shake, the impact sound and the impact art
	// of [06 §9.1] steps 4 and 5 have already been emitted by the caller and
	// are unaffected.
	if !service.DamageRoutingAdmitted(p.ShooterSide) {
		return impactFeedback{}
	}
	feedback := impactFeedback{shooter: p.Shooter}
	if directUnit != 0 && weapon.AreaOfEffect <= 16 {
		victim := w.Unit(directUnit)
		if victim != nil {
			feedback.add(int32(uint16(applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick))), p.ShooterSide == victim.Owner)
		}
		return feedback
	}
	radius := BlastRadius(weapon.AreaOfEffect)
	if radius <= 0 {
		if directUnit != 0 {
			if victim := w.Unit(directUnit); victim != nil {
				feedback.add(int32(uint16(applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick))), p.ShooterSide == victim.Owner)
			}
		}
		return feedback
	}
	// Shared area splash: EnumerateArea→DistanceToBox→Falloff→ApplyDamage [06 §9.3]
	// Extracted to ExplodeWeaponAt for death DoExplosion reuse [06 §12.1] C22–C25 (I1, I2)
	return service.explodeWeaponAt(w, terrain, weapon, p.Pos, p.Shooter, p.ShooterSide, tick, p.Velocity)
}

// impactCellIsWater uses the contacted plot's neighbourhood maximum byte, the
// same classification central impact and the crossing effect share [06 §9.1].
func impactCellIsWater(terrain *world.Terrain, pos Vec3) bool {
	if terrain == nil {
		return false
	}
	cx, cz := world.WorldToCell(pos.X), world.WorldToCell(pos.Z)
	cell := terrain.PlotAt(cx, cz)
	return cell != nil && cell.MaxHeight() < terrain.SeaLevel
}

// emitWaterCrossing is presentation-only: a live projectile crossing down
// through sea can show water art but never sound, damage, or retires [06 §7.3].
func emitWaterCrossing(s *Service, h pool.Handle, p *Projectile, weapon *content.WeaponDef, terrain *world.Terrain, tick uint32, preY int16) {
	if s == nil || p == nil || weapon == nil || terrain == nil || s.OpaqueLiquidMode {
		return
	}
	postY := int16(p.Pos.Y.Raw() >> 16)
	sea := int16(terrain.SeaLevel)
	if preY <= sea || postY > sea || !impactCellIsWater(terrain, p.Pos) {
		return
	}
	bank, graphic := impactArt(weapon, true, terrain)
	s.emitEvent(Event{Kind: EventWaterExplosion, Tick: tick, Source: p.Shooter, Target: h, Position: p.Pos, Graphic: graphic, Bank: bank, HasCalculatedFlash: true, CalculatedTable: impactFlashTable})
}

// impactFeedback accumulates signed nominal amounts before packet narrowing
// or recipient scaling. Sums and the doubled friendly total wrap at 32 bits;
// only the direct-hit caller narrows its amount to uint16 [06 §9.4].
type impactFeedback struct {
	shooter         pool.Handle
	enemy, friendly int32
}

func (f *impactFeedback) add(amount int32, friendly bool) {
	if friendly {
		f.friendly += amount
	} else {
		f.enemy += amount
	}
}

func (f impactFeedback) publish(w *units.World) {
	if w == nil || f.shooter == 0 {
		return
	}
	if shooter := w.RawUnitRecord(f.shooter); shooter != nil {
		if f.enemy > f.friendly*2 {
			shooter.Pending |= 0x4000
		} else {
			shooter.Pending |= 0x2000
		}
	}
}

// ExplodeWeaponAt applies area damage without central-impact presentation.
// The feature burn-weapon producer uses this entry; pooled and stack impacts
// reach the same area walk after their effects [05 R-FEAT-01 §11][06 §9.3].
// Each admitted hit completes before the next candidate is discovered, so
// later cells observe earlier replacements [06 §9.3][05 R-FEAT-01 §5].
func (s *Service) ExplodeWeaponAt(w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, tick uint32) {
	shooterSide := NeutralSide
	if attacker := w.Unit(shooter); attacker != nil {
		shooterSide = attacker.Owner
	}
	feedback := s.explodeWeaponAt(w, terrain, weapon, impact, shooter, shooterSide, tick, Vec3{})
	feedback.publish(w)
}

// explodeWeaponAt is the shared area recipient walk. The central impact
// record carries the shooter-side byte separately from the shooter identity,
// so a stack death record can route with its dying owner's side while retaining
// its null shooter provenance [06 §12.2][06 §9.1].
func (s *Service) explodeWeaponAt(w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, shooterSide uint8, tick uint32, observedVelocity Vec3) impactFeedback {
	if w == nil || weapon == nil {
		return impactFeedback{}
	}
	feedback := impactFeedback{shooter: shooter}
	radius := BlastRadius(weapon.AreaOfEffect) // [06 §9.3] unsigned area>>1
	if radius <= 0 {
		return feedback
	}
	// Community's generation bracket is saved and restored so a nested blast's
	// higher generation cannot replace its caller's active generation. The
	// off-map bucket pass is part of every blast and precedes the tile walk
	// [CP-DMG-1][CP-ENV-1].
	savedAreaGeneration := s.beginCommunityArea()
	defer s.endCommunityArea(savedAreaGeneration)
	s.communityOffMapSplash(&feedback, w, terrain, weapon, impact, shooter, shooterSide, tick, observedVelocity, radius)
	var mapW, mapH int32
	if terrain != nil {
		mapW = terrain.CellW
		mapH = terrain.CellH
	}
	// The feature walk is skipped entirely by `unitsonly` [06 §9.3][05
	// R-FEAT-01 §8], and needs the feature runtime the session installs.
	featureWalk := !weapon.UnitsOnly && s != nil && s.Features != nil
	var featDedup FeatureDedup
	// The twenty-entry unit memory of [06 §9.3] spans the whole sweep, not one
	// cell: a unit standing on nine cells of the blast rectangle is a candidate
	// nine times and must be damaged once.
	var unitDedup UnitDedup
	EnumerateArea(impact, radius, mapW, mapH, func(cx, cz int32) {
		// "Within each cell the order is unit slot zero, unit slot one, then
		// the feature/terrain candidate" [06 §9.3]. The two unit slots are the
		// plot cell's two occupancy words — the ground plane first and the air
		// plane second [03 §2.2][04 R-COLL-01 §4] — exactly as the projectile
		// contact ladder reads them [06 R-DMG-01 §7].
		//
		// The candidate set is therefore the footprint rectangle the occupancy
		// stamper wrote, not the units whose CENTRE falls in the cell. Before
		// this reader the sweep rebuilt the whole live-unit slice per cell and
		// admitted a unit only when its centre cell matched, so a blast landing
		// inside a large unit's footprint but outside the rectangle its centre
		// sits in did nothing at all, and the cell ordering, the ground-before-
		// air ordering and the bounded deduplication were all unobservable.
		s.rules().AreaVictims(AreaVictimQuery{Service: s, World: w, Terrain: terrain, Tick: tick, CellX: cx, CellZ: cz}, func(h pool.Handle) {
			// A unit candidate must be nonzero and must NOT be the record's
			// shooter: the shooter is unconditionally excluded from every
			// blast, and that exclusion is the whole of retail's
			// self-damage policy [06 §9.3][06 R-DMG-01 §9]. There is no
			// `noselfdamage` key and no owner or alliance test here — a
			// shooter's own OTHER units take full damage, and the shooter
			// itself still takes full damage from a different record's
			// blast.
			//
			// A null shooter matches nobody [06 R-DMG-01 §9], which is what
			// makes a meteor or a death explosion damage every side alike.
			//
			// Before this reader existed the shooter enumerated itself,
			// took its own splash, and had its last-damage provenance
			// overwritten with its own owner below — so a commander that
			// died inside its own blast credited the kill to itself instead
			// of to the player whose shot actually killed it [06 §12.1].
			if shooter != 0 && h == shooter {
				return
			}
			// "Unit deduplication happens BEFORE the radius test, against a
			// memory of at most 20 unit pointers … a candidate encountered
			// when the memory is full is still processed but not remembered,
			// so a later occurrence is processed again. An out-of-radius
			// first sighting therefore consumes a memory entry" [06 §9.3].
			if unitDedup.SeenUnit(h) {
				return
			}
			u := w.Unit(h)
			if u == nil || !u.Alive || u.Dying {
				return // a word naming a freed slot names no candidate
			}
			// "lo = unit.pos.axis + definition.boundsMin.axis; hi =
			// unit.pos.axis + definition.boundsMax.axis" [06 §9.3]: the
			// box is the definition's whole bounding record translated by
			// the unit's own position, on all three axes. That record is
			// footprint-derived in X and Z and the model-top walk in Y over
			// a minimum Y of zero — the walk is the only bound retail takes
			// from model geometry [02 R-CAT-01 §7] — and BoundingExtents is
			// exactly that record.
			//
			// The Y bound used to be a flat sixteen world units above the
			// unit's position, which is no retail quantity at all: a tall
			// target took nothing from an impact inside its own body, and a
			// flat one took damage from an impact above it.
			boundsMin, boundsMax := u.Def.BoundingExtents()
			min := Vec3{
				X: u.X.Add(numeric.Fixed(boundsMin[0])),
				Y: u.Y.Add(numeric.Fixed(boundsMin[1])),
				Z: u.Z.Add(numeric.Fixed(boundsMin[2])),
			}
			max := Vec3{
				X: u.X.Add(numeric.Fixed(boundsMax[0])),
				Y: u.Y.Add(numeric.Fixed(boundsMax[1])),
				Z: u.Z.Add(numeric.Fixed(boundsMax[2])),
			}
			uv := UnitForArea{Handle: u.Handle, Pos: Vec3{X: u.X, Y: u.Y, Z: u.Z}, Min: min, Max: max}
			dist := DistanceToBox(impact, uv) // integer [06 §9.3]
			if dist >= radius {
				return // strict < radius [06 §9.3]
			}
			falloff := float32(1)
			if dist != 0 {
				falloff = Falloff(float32(dist), float32(radius), float32(weapon.EdgeEffectiveness)) // [06 §9.3] float32
			}
			p := &Projectile{Pos: impact, Shooter: shooter, ShooterSide: shooterSide, Velocity: observedVelocity}
			feedback.add(applyDamageToUnit(s, u, p, weapon, falloff, dist, w, tick), shooterSide == u.Owner)
		})
		// Discover the feature after both unit hits have completed, before
		// advancing to the next cell [06 §9.3].
		if !featureWalk {
			return
		}
		cand, ok := s.Features.AreaCandidateAt(int(cx), int(cz))
		if !ok {
			return
		}
		// The distance is measured to the candidate's reference point with the
		// same truncate-and-narrow form units use, and accepted on the same
		// strict `< R` [06 §9.3][06 R-WPN-04 §3].
		fdist := DistanceToBox(impact, UnitForArea{
			Pos: Vec3{X: cand.X, Y: cand.Y, Z: cand.Z},
			Min: Vec3{X: cand.X, Y: cand.Y, Z: cand.Z},
			Max: Vec3{X: cand.X, Y: cand.Y, Z: cand.Z},
		})
		if fdist >= radius {
			return
		}
		// Unlike the unit walk, the distance is tested BEFORE deduplication
		// [06 §9.3], so an out-of-radius first sighting never consumes one of
		// the sixty-four ANCHOR entries — the memory holds anchors, not covered
		// cells, which is what makes one blast damage a multi-cell footprint
		// once rather than once per cell it covers. A blast covering more than
		// 64 distinct anchors stops remembering, and the 65th onward can be hit
		// once per covered cell [05 R-FEAT-01 §8].
		if featDedup.SeenFeature(int32(cand.CX), int32(cand.CZ)) {
			return
		}
		// Features receive the full default damage, without unit falloff or
		// veterancy; ignition precedes accumulation [05 R-FEAT-01 §8].
		s.Features.Ignite(cand.CX, cand.CZ, weapon.Firestarter, weapon.DamageDefault)
	})
	if terrain == nil || mapW == 0 {
		// Preserve the terrain-free fixture path's planar acceptance.
		for _, u := range w.Iter() { // deterministic [I1]
			if u == nil || !u.Alive || u.Dying || (shooter != 0 && u.Handle == shooter) {
				continue
			}
			dx := impact.X.Int() - u.X.Int()
			dz := impact.Z.Int() - u.Z.Int()
			dist2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			if dist2 >= int64(radius)*int64(radius) {
				continue
			}
			p := &Projectile{Pos: impact, Shooter: shooter, ShooterSide: shooterSide, Velocity: observedVelocity}
			feedback.add(applyDamageToUnit(s, u, p, weapon, 1, 0, w, tick), shooterSide == u.Owner)
		}
	}
	return feedback
}

// ParalyzeTaskPush is the seam a kind-2 packet reaches the victim's primary
// command list through [06 §10]: "the engine then resolves the task type by the
// authored alias `paralyze` and inspects only the HEAD of the victim's primary
// command list. If the head already carries that task type, the packet's
// unsigned 16-bit amount is added to the head's 32-bit accumulated credit — a
// plain add … Otherwise a task object is allocated, constructed with the credit
// as its parameter, and PREPENDED."
//
// The task itself is a row of internal/orders, which imports this package, so
// the mechanism cannot be called from here directly; that package's own
// initializer installs the entry point (orders.PushParalyzeCredit). The
// installer is static — the value does not vary with the session, and the row
// it drives reaches everything session-scoped through the victim's own queue
// binding — so this is a wiring table, not shared simulation state (I6).
//
// With nothing installed a paralyzer hit keeps the preliminary side effects of
// [06 §10] and pushes no task, which is what a fixture composing internal/combat
// alone gets.
var ParalyzeTaskPush func(victim *units.Unit, credit uint32, tick uint32)

// pushParalyzeTask is the packet side of [06 §10]. The stun's whole mechanism —
// the release verb on all three slots, the unconditional target clear, the
// goal-payload release, the wait arm and the stunned mark — belongs to the
// task's first visit, not to this site [06 R-DMG-01 §11].
func pushParalyzeTask(victim *units.Unit, credit uint32, tick uint32) {
	if victim == nil || ParalyzeTaskPush == nil {
		return
	}
	ParalyzeTaskPush(victim, credit, tick)
}

// AcceptDamage is combat's common receiver for locally delivered packets. It
// receives a producer-selected nominal, applies only the recipient side of
// C20, and leaves finalization to the later unit/death visit [06 §9.1][06 §9.2].
func (s *Service) AcceptDamage(w *units.World, tick uint32, in DamageInput) DamageResult {
	if s == nil || w == nil || in.Victim == 0 {
		return DamageResult{}
	}
	victim := w.Unit(in.Victim)
	if victim == nil || !victim.Alive || victim.Dying {
		return DamageResult{}
	}

	if in.Kind == KindHeal {
		// The heal arm reads its low word unsigned and stores the result as a
		// signed 16-bit health word before any damage-side effects [06 §9.1].
		amount := uint16(in.Nominal)
		victim.Health = ApplyHealing(victim.Health, victim.MaxHealth, amount)
		return DamageResult{Accepted: true, Amount: amount}
	}

	damageModifier := int32(65536)
	if victim.Def != nil {
		damageModifier = victim.Def.DamageModifier
	}
	level := s.VeteranLevel(victim.Def, victim.Kills)
	if level > 25 {
		level = 25
	}
	amount := scaleAcceptedAmount(in.Nominal, int32(level), UnitArmored(victim), damageModifier)
	result := DamageResult{Accepted: true, Amount: amount}

	// These effects are deliberately before provenance rewriting: reaction
	// observes the prior packet state [06 §9.1][06 R-WPN-04 §2].
	SetDamageFlash(victim)
	s.emitEvent(Event{Kind: EventDamageFlash, Tick: tick, Source: in.Attacker, Target: victim.Handle, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}, Duration: DamageFlashTicks})
	s.rules().ObserveDanger(s, victim, w.Unit(in.Attacker), tick)
	if amount != 0 && in.Kind != KindNoReaction {
		s.rules().ObserveImpact(s, victim, w.Unit(in.Attacker), in, tick)
	}
	if in.Kind != KindNoReaction {
		s.ReactToDamage(w, victim, w.Unit(in.Attacker), tick)
	}
	victim.LastDamageCause = in.Kind
	if in.Attacker != 0 {
		victim.EngagementTarget = in.Attacker
		if rawAttacker := w.RawUnitRecord(in.Attacker); rawAttacker != nil {
			victim.LastDamageSide = rawAttacker.Owner
			if s.DamageActivity != nil {
				s.DamageActivity(victim, rawAttacker, tick)
			}
		}
	}

	if in.Kind == KindParalyzer {
		// Reaction runs above and can alter the victim's state through its
		// session seams, so the paralyze arm repeats the packet acceptance
		// state test immediately before task admission [06 §10].
		if !victim.Alive || victim.Dying {
			return result
		}
		if s.DeathLatchAdmitted(victim.Owner) && (victim.Def == nil || !victim.Def.ImmuneToParalyzer) {
			pushParalyzeTask(victim, uint32(amount), tick)
		}
		return result
	}

	victim.Health = ApplyDamage(victim.Health, amount)
	if victim.Health <= 0 && s.DeathLatchAdmitted(victim.Owner) {
		// Preserve the modular health and stored provenance for the later
		// death packet. A null damage attacker leaves the prior link intact
		// [06 §9.1]; the death packet reads that stored link [06 §12.1].
		w.DestroyBy(victim.Handle, units.DeathCauseFromKind(in.Kind), victim.EngagementTarget)
		result.DeathLatched = true
		if s.deathNotified == nil {
			s.deathNotified = make(map[pool.Handle]*units.Unit)
		}
		if s.deathNotified[victim.Handle] != victim {
			s.deathNotified[victim.Handle] = victim
			s.emitEvent(Event{Kind: EventUnitKilled, Tick: tick, Source: in.Attacker, Target: victim.Handle, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}})
		}
		return result
	}
	if victim.Health <= 0 {
		victim.Health = 0 // absent or remote controller continues without latching
	}
	if in.Kind == KindOrdinary {
		if bridge := s.callbackBridgeForUnit(victim); bridge != nil {
			bridge.HitByWeapon(in.Direction)
			bridge.TakeDamage(cob.TakeDamagePercent(victim.Health, victim.MaxHealth))
		}
	}
	return result
}

// weaponDamageNominal keeps the weapon-side half of C20 separate from the
// accepted-packet receiver. Fixed producers pass their established nominal to
// AcceptDamage and never construct a dummy weapon [06 §9.2].
func (s *Service) weaponDamageNominal(weapon *content.WeaponDef, victim *units.Unit, rawAttacker *units.Unit, falloff float32) int32 {
	if weapon == nil || victim == nil {
		return 0
	}
	name := ""
	if victim.Def != nil {
		name = victim.Def.UnitName
	}
	level := uint32(0)
	if rawAttacker != nil {
		level = s.VeteranLevel(rawAttacker.Def, rawAttacker.Kills)
	}
	return weaponNominal(SelectBaseDamage(weapon, name), falloff, int32(level), rawAttacker != nil, s != nil && s.doubleShot, s != nil && s.halfShot)
}

func applyDamageToUnit(service *Service, victim *units.Unit, p *Projectile, weapon *content.WeaponDef, falloff float32, distance int32, w *units.World, tick uint32) int32 {
	if service == nil || victim == nil || p == nil || weapon == nil || w == nil {
		return 0
	}
	cause := KindOrdinary
	if weapon.Paralyzer {
		cause = KindParalyzer
	}
	nominal := service.weaponDamageNominal(weapon, victim, w.RawUnitRecord(p.Shooter), falloff)
	service.AcceptDamage(w, tick, DamageInput{
		Victim: victim.Handle, Attacker: p.Shooter,
		Nominal:   nominal,
		Direction: hitDirectionByte(p, victim), Kind: cause,
		ImpactVelocityX: p.Velocity.X, ImpactVelocityZ: p.Velocity.Z,
	})
	_ = distance
	return nominal
}

// impactArt selects the explosion-art holder one impact draws from
// [06 R-WFX-01 §1], returning the GAF bank name and the entry name inside it.
// An empty entry means the holder is null and the impact draws no art.
//
// A weapon has two holders, not three. The land holder is
// `explosiongaf`/`explosionart`. The single water-or-lava holder is filled
// from `waterexplosiongaf`/`waterexplosionart` on an ordinary map and from
// `lavaexplosiongaf`/`lavaexplosionart` on a `lavaworld` map — the other pair
// is never consulted. There is no per-impact lava test: the water arm is the
// same "below sea level" predicate everywhere, and a lava world shows lava art
// because the holder was filled from the lava keys.
//
// **Both keys of a pair are required.** A pair with only one key present is
// not an error and not a fallback: the holder stays null and the impact draws
// nothing. Stock content authors a lava pair on most weapons but not all, so
// on a lava world the weapons without one correctly show no splash at all.
//
// Divergence, deliberate and recorded: retail chooses which pair fills the
// water-or-lava holder at CATALOG PARSE, from the session's `lavaworld` value,
// and the parser binds a GAF entry pointer there and then. Nanolathe compiles
// its weapon catalog without a map, so the same choice is made here, per
// impact, from the terrain the impact happened on. The two are observationally
// identical inside one battle — which is the scope over which retail's holder
// is also fixed — and this form additionally survives the process compiling
// one catalog for several maps, which retail's does not [06 R-WFX-01 §1].
func impactArt(weapon *content.WeaponDef, water bool, terrain *world.Terrain) (bank, entry string) {
	if weapon == nil {
		return "", ""
	}
	if !water {
		return completeArtPair(weapon.ExplosionGaf, weapon.ExplosionArt)
	}
	if terrain != nil && terrain.LavaWorld {
		return completeArtPair(weapon.LavaExplosionGaf, weapon.LavaExplosionArt)
	}
	return completeArtPair(weapon.WaterExplosionGaf, weapon.WaterExplosionArt)
}

// impactFlashTable is the calculated table every central impact passes
// [06 R-WFX-01 §2]. Table 1 is built and never drawn by any caller; table 2
// belongs to the `explode` opcode's bitmap bits.
const impactFlashTable uint8 = 0

// completeArtPair applies the both-or-nothing rule of [06 R-WFX-01 §1].
func completeArtPair(bank, entry string) (string, string) {
	if bank == "" || entry == "" {
		return "", ""
	}
	return bank, entry
}

// interceptorRescanPort builds the FirePorts.InterceptorRescan closure, or nil
// when this unit's slot is not an `interceptor` weapon [06 §11.2].
//
// nil is the gate: an ordinary vertical launch — a nuclear missile — performs
// no rescan and must not be refused a shot by one. Only the four
// interceptor-flagged weapons in the retail corpus (I14) get a non-nil port,
// and all four are `vlaunch`, which is why the vertical-launch executor is the
// executor that owns the rescan [06 §4.4].
func interceptorRescanPort(svc *Service, u *units.Unit, weapon *content.WeaponDef, catalog *content.Catalog) func(int, *Slot) pool.Handle {
	if svc == nil || u == nil || catalog == nil || weapon == nil || !weapon.Interceptor {
		// nil, not a closure that answers zero: TryFire reads a zero return as
		// "the rescan found nothing", which refuses the shot [06 §11.2]. A
		// nuclear missile is `vlaunch` and not `interceptor`, so a closure here
		// would silently disarm every nuke silo in the game.
		return nil
	}
	return func(_ int, cSlot *Slot) pool.Handle {
		if cSlot == nil || cSlot.Weapon == nil || !cSlot.Weapon.Interceptor {
			return 0
		}
		// The executor's own slot view carries the ammunition byte and the
		// coverage scalar the scan needs; interceptorScanCandidate takes the
		// units-side record, so the two fields are lifted across rather than
		// re-read from the live slot.
		probe := units.Slot{Weapon: cSlot.Weapon, Ammo: cSlot.Ammo}
		h, _, ok := interceptorScanCandidate(svc, u, &probe, catalog)
		if !ok {
			return 0
		}
		return h
	}
}
