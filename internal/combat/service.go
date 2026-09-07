// This file: the per-unit weapon service and the projectile phase.

package combat

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
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

func isAllied(owner, other uint8, econ *economy.Service) bool {
	if owner == other {
		return true
	}
	if econ == nil {
		return false
	}
	if int(owner) >= 10 || int(other) >= 10 {
		return false
	}
	if econ.Players[owner].Allies[other] {
		return true
	}
	if econ.Players[other].Allies[owner] {
		return true
	}
	return false
}

func isHostile(shooter *units.Unit, cand *units.Unit, econ *economy.Service) bool {
	if shooter == nil || cand == nil {
		return false
	}
	if shooter.Owner == cand.Owner {
		return false
	}
	if isAllied(shooter.Owner, cand.Owner, econ) {
		return false
	}
	return true
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
	ReturnSeen       bool  // an explicit COB return was consumed this visit [06 §3.3]
	ReturnValue      int32 // value of the last consumed return (0 or nonzero)
	Drained          bool  // combat performed the visit's synchronous vm.Drain(1) [04 §4.2][GAP T15 C17]
	Fired            int   // projectiles created via TryFire this visit [06 §4]
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

// StepWeaponsForUnit runs the per-unit weapon pipeline for one unit visit ON-04 [06 §3.3][06 §4][04 §5.3][GAP T15].
// It is the authoritative per-unit step; the session owns the deterministic
// pool-order loop and invokes this method directly [06 §1.2] C1 (I1).
// Dependencies are the session's world, visibility, terrain, economy, catalog,
// simulation RNG, and CRT RNG services.
// RS-08: exactly one VM drain per unit visit at the normal window [04 §4.2][04 §4.6][GAP T15 C17] (I7):
// queue all TargetCleared/Aim callbacks in slot order 0..2, drain once delta 1 (eight threads then one piece pass),
// collect actual explicit Aim returns, then apply post-drain fire permissions in slot order without second drain.
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
	// The autonomous target scan's PER-UNIT admission [06 §3.2]. It is a
	// separate pass in retail — one per player per tick, run from that player's
	// manager immediately after its AI task dispatch — and this build folds it
	// into the unit's weapon visit, so its preconditions are read here and
	// carried into the two halves of the scan below (the retention drops and
	// the re-acquisition). It gates NOTHING else: the reload decrement, the Aim
	// handshake, the shot-time gates and the firing of a target an order
	// installed all run for a unit the scan does not visit this tick.
	scanning := s.autonomousScanVisitsUnit(u, tick, w)
	// The per-side target registry's rebuild used to be reached from here, and
	// is not any more: RebuildTargetRegistryIfDue is called once per slot from
	// the per-player phase, at the position [06 §3.1] gives it — after that
	// slot's manager tick and before its per-unit visits — so that the lists,
	// the census and the one bound-30 draw share the single cadence gate they
	// share in retail. See RebuildTargetRegistryIfDue.
	// --- Phase: pre-drain callback scheduling (TargetCleared + Aim) in slot order 0..2 [GAP T15] ---
	type slotPrep struct {
		needLatch    bool
		needResult   bool
		weapon       *content.WeaponDef
		tgtPos       Vec3
		tgtHandle    pool.Handle
		desiredYaw   uint16
		desiredPitch uint16
		suppressAim  bool
	}
	var preps [NumSlots]*slotPrep
	var queuedTargetCleared bool
	// The ordinary target clear [06 §3.2]: drop the target, drop the Aim
	// handshake and its latch, and queue TargetCleared when the slot was
	// actually pointing somewhere. Both the autonomous scan's retention drops
	// and the interceptor scan's miss go through it — [06 §11.2] says an
	// interceptor miss "clears the slot target through the ordinary path", so
	// it has to be one body rather than two that drift apart.
	//
	// Bit 1 is the slot's ENABLED bit, whose one writer is the slot
	// initializer [06 R-WPN-05 §3]; a lost target does not disable the slot.
	// This used to clear it, reading it as an armed/has-target flag.
	clearSlotTarget := func(slot *units.Slot, idx int) {
		clearedHeading := slot.DesiredYaw != 0
		clearedPitch := slot.DesiredPitch != 0x8000
		slot.Target = units.Target{Kind: units.TargetNone}
		slot.Aim.IssueBit = false
		slot.Aim.Ready = false
		slot.Flags &^= 0x01
		if s.pendingAims != nil {
			delete(s.pendingAims, pendingKey{Unit: u.Handle, Slot: idx})
		}
		if (clearedHeading || clearedPitch) && bridge != nil {
			bridge.TargetCleared(int32(idx))
			queuedTargetCleared = true
		}
	}
	for idx := 0; idx < NumSlots; idx++ {
		slot := u.SlotAt(idx)
		if slot == nil || !slot.IsPopulated() {
			continue
		}
		// The slot visit's first step: decrement a nonzero reload countdown.
		// It happens for every populated slot, before the target is resolved
		// and before any later gate can skip the visit, so a weapon that is
		// out of range or waiting on Aim still recovers its shot
		// [06 §1.2][06 §4.1]. Only after the decrement can the reload-zero
		// fire-time pipeline below admit the visit, which is why a one-tick
		// reloadtime fires on the tick after the decrement, never the same
		// tick [06 §4.1].
		if slot.Reload > 0 {
			slot.Reload--
		}
		// The control byte's bit 4 is AUTONOMY [06 R-WPN-05 §3]
		// [04 R-UNIT-06 §5 part 3]: set means the slot belongs to autonomous
		// acquisition, clear means an order currently holds it. It is not a
		// firing gate — a slot an attack order took still shoots the target
		// that order installed — so it gates only the two halves of the
		// autonomous scan [06 §3.2]: the scan's retention drops below, and the
		// re-acquisition after them. Every other reader of the bit already
		// tests it (the retaliation offer, the guards); this site claimed in a
		// comment that the bit "gates the AUTONOMOUS SCAN below" and then never
		// read it.
		//
		// Reading it as a *suppression* gate on the whole slot visit, which
		// this file did before, was a total regression: the order-record
		// removal cleanup walk sets the bit on every assigned slot on EVERY
		// removal [04 R-ORDER-02 §2] — a player's own non-queued right-click
		// included — so one order silenced every weapon that unit owned.
		//
		// `scanning` is the per-unit half of the same admission [06 §3.2]: a
		// unit the round-robin cursor does not reach this tick, or that is not
		// fully built, or whose standing-fire field is not fire-at-will, runs
		// neither half of the scan. Retention is inside the scan ("the scan
		// first tries to retain"), so it waits for the unit's next visit too.
		autonomous := scanning && slot.Flags&units.SlotFlagAutonomous != 0
		// The AUTOMATIC INTERCEPTOR SCAN [06 §11.2]. It "runs from the same
		// per-slot position in the autonomous scan that ordinary acquisition
		// runs from (§3.2) and is chosen by the slot weapon's interceptor
		// flag", so it stands here, at that position, as the other arm of the
		// same branch rather than as an extra pass somewhere else.
		//
		// It has no retention half. Retention's three drops [06 §3.2] are all
		// tests on a target UNIT (allied now, bad-target mask, paralyzer versus
		// the stunned mark) and an interceptor slot's target is a POINT, so
		// there is nothing for them to test; the scan simply runs again and its
		// own hit or miss decides. A miss "clears the slot target through the
		// ordinary path", which is the shared clear above.
		//
		// The scan's own gates — nonzero ammunition, differing owner side,
		// `targetable`, the coverage square on the candidate's stored aim
		// point, unclaimed — live in interceptorScanCandidate. The slot store
		// is the candidate's CURRENT position, not the aim point it was
		// matched on [06 §11.2].
		//
		// Everything below this branch is unchanged for an interceptor slot:
		// the shot-time gate, the muzzle query, the vertical-launch executor
		// and the stockpile fire gate all run exactly as they do for any other
		// weapon [06 §11.1].
		if slot.Weapon != nil && slot.Weapon.Interceptor {
			if autonomous {
				if _, candPos, found := interceptorScanCandidate(s, u, slot, catalog); found {
					// A hit installs a POINT target from the candidate's
					// current position [06 §11.2]. The Aim handshake and the
					// stored angles are left alone: the vertical-launch
					// executor runs no drift gate [06 §3.3].
					slot.Target = units.Target{Kind: units.TargetGround, X: candPos.X, Z: candPos.Z}
				} else {
					clearSlotTarget(slot, idx)
				}
			}
			if slot.Target.Kind == units.TargetNone {
				continue // nothing to shoot at [06 §11.2]
			}
		} else if slot.Target.Kind == units.TargetUnit && slot.Target.Unit != 0 {
			tu := w.Unit(slot.Target.Unit)
			// Stale/dead resolution belongs to the slot pipeline's own target
			// resolution and runs for every slot, autonomous or not: [06 §3.2]
			// lists "stale/dead resolution" among TargetCleared's producers
			// beside the scanner's own failure.
			drop := tu == nil || !tu.Alive || tu.Dying
			if !drop && autonomous {
				// The autonomous scan tries to RETAIN first, and [06 §3.2]
				// gives retention exactly three drops, in this order: the
				// target's owning player is now allied to the scanning player;
				// the target's definition index is in the slot's bad-target
				// mask (retention is stricter than acquisition, which merely
				// buckets such a candidate as fallback, [06 §3.1]); or the
				// slot's weapon is a paralyzer and the target already carries
				// the stunned mark — one of the exactly two readers of that
				// mark [06 R-DMG-01 §11], and the reason a paralyzer does not
				// spend its shots re-stunning a unit that is already down. An
				// ordinary weapon ignores the mark entirely.
				switch {
				case isAllied(u.Owner, tu.Owner, econ):
					drop = true
				case !IsPreferredCategoryMask(tu.Def.DefinitionMask(), badMaskForSlot(u.Def, idx)):
					drop = true
				case slot.Weapon != nil && slot.Weapon.Paralyzer && tu.Stunned:
					drop = true
				}
			}
			if drop {
				clearSlotTarget(slot, idx)
				continue
			}
		}
		// The autonomous scan's per-slot command-fire clause [06 §3.2]: a slot
		// only acquires when the owning player's controller type is 2
		// (computer) or the weapon is not `commandfire`. The consequence is a
		// contract, not a nicety — a human player's units never acquire
		// autonomously with a command-fire weapon. Without this reader the
		// commander's disintegrator hunted and fired on its own, which is also
		// how a human commander ended up standing in its own blast.
		//
		// The gate suppresses only the ACQUISITION. A target the manual path
		// installed on a command-fire slot — `AttackSpecial` resolves command
		// code 3, sets p1 = 2 and the resolved attack handler binds slot 2
		// [04 R-ORD-01 §2][04 R-ORD-01 §3] — is left in place and falls through
		// to the ordinary shot-time gates below, because forced/manual
		// installation bypasses the autonomous lists and nothing else
		// [06 §3.2].
		// The trigger is target absence alone. It used to read `|| bit 1
		// clear` as "not armed"; bit 1 is the enabled bit and is set for the
		// whole life of every populated slot [06 R-WPN-05 §3]. The autonomy
		// bit is the scan's other per-slot clause [06 §3.2], so a slot an
		// order holds re-acquires nothing.
		if slot.Target.Kind == units.TargetNone {
			if !autonomous || !AutonomousScanAdmitsSlot(slot.Weapon, s.PlayerControlByteFor(u.Owner)) {
				if slot.Target.Kind == units.TargetNone {
					continue // nothing installed and nothing to acquire [06 §3.2]
				}
			} else if acquired, ok := s.acquireTargetForSlot(u, slot, idx, w, vis, terrain, simRNG, econ, catalog); ok {
				savedYaw := slot.DesiredYaw
				savedPitch := slot.DesiredPitch
				savedIssue := slot.Aim.IssueBit
				savedReady := slot.Aim.Ready
				savedFlags := slot.Flags & 0x01
				slot.Target = units.Target{Kind: units.TargetUnit, Unit: acquired}
				slot.DesiredYaw = savedYaw
				slot.DesiredPitch = savedPitch
				slot.Aim.IssueBit = savedIssue
				slot.Aim.Ready = savedReady
				slot.Flags = (slot.Flags &^ 0x01) | savedFlags
			} else {
				continue
			}
		}
		if slot.Target.Kind == units.TargetNone {
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
			tgtPos = PreFireLeadPoint(u, tu, slot, weapon, UnitTargetPoint(tu))
		} else {
			tgtPos = Vec3{X: slot.Target.X, Y: PointTargetHeight(terrain, slot.Target.X, slot.Target.Z), Z: slot.Target.Z}
		}
		// The AIM ORIGIN: the `AimFrom[k]` query seeded −1, falling back to
		// `Query[k]` seeded 0 only on the −1 sentinel [06 §3.4][R-P0-07]. It
		// is the point the yaw and pitch are solved FROM [06 §3.3], and it is
		// not the muzzle: the fire-time executors spawn the projectile from
		// the forced `Query[k]` piece alone [06 §4.1], which tryFireForSlot
		// queries afresh, so the result here stays local and never lands in
		// the slot's MuzzlePiece word. Storing it there — which this site
		// used to do — made every weapon spawn at its aim-origin piece (a
		// Peewee's shoulder, a tank's turret) instead of its barrel flare.
		//
		// The seed distinction is load-bearing [R-P0-07]: with a script but
		// neither entry the piece is 0, the root, and the locator answers
		// the root's composed offset. Only a unit with no script at all
		// takes the bare-position path.
		aimPiece := int32(-1)
		if bridge != nil {
			aimPiece = bridge.AimPiece(cob.WeaponSlot(idx)).QueryValue()
		}
		muzzlePos, muzzleOK := muzzleWorldPosResolved(u, aimPiece)
		if !muzzleOK {
			continue // no binding to compose through; a synthetic fixture
		}
		dx := tgtPos.X.Sub(muzzlePos.X)
		dy := tgtPos.Y.Sub(muzzlePos.Y)
		dz := tgtPos.Z.Sub(muzzlePos.Z)
		var desiredYaw uint16
		var desiredPitch uint16
		if weapon.Ballistic {
			var grav numeric.Fixed
			if terrain != nil {
				grav = terrain.Gravity
			}
			vel := numeric.Fixed(int64(weapon.WeaponVelocity))
			if vel.Raw() == 0 {
				desiredYaw = uint16(YawFromDelta(dx, dz))
				desiredPitch = 0x8000
				slot.DesiredYaw = desiredYaw
				slot.DesiredPitch = desiredPitch
				preps[idx] = &slotPrep{weapon: weapon, tgtPos: tgtPos, tgtHandle: tgtHandle, desiredYaw: desiredYaw, desiredPitch: desiredPitch, suppressAim: true}
				continue
			}
			pitch, ok := BallisticSolve(dx, dy, dz, vel, grav, weapon.MinBarrelAngle)
			if !ok {
				desiredYaw = uint16(YawFromDelta(dx, dz))
				desiredPitch = 0x8000
				slot.DesiredYaw = desiredYaw
				slot.DesiredPitch = desiredPitch
				preps[idx] = &slotPrep{weapon: weapon, tgtPos: tgtPos, tgtHandle: tgtHandle, desiredYaw: desiredYaw, desiredPitch: desiredPitch, suppressAim: true}
				continue
			}
			// The aim-time solve runs from the AimFrom/Query muzzle piece
			// [06 §3.3]; the shot-time gate's own ballistic clause re-solves
			// from the SHOOTER's position [06 R-WPN-05 §9] clause 3, so its
			// result is not cached here.
			desiredPitch = pitch
			desiredYaw = uint16(YawFromDelta(dx, dz))
		} else {
			desiredYaw = uint16(YawFromDelta(dx, dz))
			desiredPitch = uint16(PitchFromDelta(dx, dy, dz))
		}
		// The convention marker that used to stand here is retired by
		// [06 R-WPN-05 §4], which settles both halves it could not reconcile.
		// Retail's bearing IS atan2q(muzzle - target), and retail's yaw
		// denotes the direction (-sin a, -cos a), so the two negations cancel
		// and the bearing points from the muzzle toward the target — the same
		// direction our target-minus-muzzle solve produces. What differs is
		// only the numbering: this build builds velocity from +sin/+cos, so
		// its absolute yaw is exactly half a turn from retail's
		// (atan2(-x, -z) = atan2(x, z) + 0x8000). Projectiles fly correctly
		// because both sign flips cancel; every value shared with a
		// retail-authored script, or compared against a unit heading (which
		// this build already carries in retail's convention, -sin/-cos
		// [04 R-MOV-01 §4]), must carry the shift. See retailYawFromGo and
		// aimYawForScript below.
		needLatch, needResult := aimRequirement(weapon)
		suppress := weapon.Ballistic && desiredPitch == 0x8000
		if suppress && weapon.Turret {
			// The turret executor's aim geometry yielded no solution: raise
			// "could not fire" and clear the Aim latch [06 R-WPN-05 §6].
			// Nothing here reads the bit back — it is the attack handlers'
			// disengage trigger.
			u.Pending |= units.PendingCouldNotFire
			slot.Aim.IssueBit = false
			slot.Flags &^= units.SlotFlagAimLatch
		}
		// The turret executor writes the solved angles into the slot only when
		// it dispatches Aim, i.e. only while the Aim-request latch is clear
		// [06 §3.3]. Those stored angles are what the drift gate later measures
		// the freshly re-solved pair against, so it reads how far the target
		// has moved in angle since the Aim request went out [06 R-WPN-03 §2] —
		// rewriting them every visit would make the gate compare a value with
		// itself and pass unconditionally. Every other executor writes the
		// absolute angles at fire time instead, which the per-visit write here
		// reproduces.
		//
		// What is stored is the RELATIVE yaw — the same value handed to Aim* —
		// and the ABSOLUTE pitch, which is what the save persists
		// [06 R-WPN-05 §4] [08 R-SAVE-WEAPON-01]. Both halves of the drift
		// comparison are then relative, so its error carries however far the
		// hull turned while the Aim was outstanding; storing the absolute
		// bearing and comparing absolute against absolute loses that term.
		// Every other executor writes absolute angles at fire time, so they
		// keep this build's own absolute yaw.
		storedYaw := desiredYaw
		if weapon.Turret {
			storedYaw = aimYawForScript(desiredYaw, u.Move.Heading)
		}
		if !weapon.Turret || !slot.Aim.IssueBit {
			slot.DesiredYaw = storedYaw
			slot.DesiredPitch = desiredPitch
		}
		preps[idx] = &slotPrep{weapon: weapon, tgtPos: tgtPos, tgtHandle: tgtHandle, desiredYaw: desiredYaw, desiredPitch: desiredPitch, needLatch: needLatch, needResult: needResult, suppressAim: suppress}
		// Aim dispatch is gated on the latch being clear and on nothing else
		// [06 §3.3]. The aim-ready word is a separate latch that a nonzero
		// completion sets: a drift-gate failure clears the request latch and
		// leaves the ready word alone [06 R-WPN-03 §2], and the slot must
		// re-dispatch on its next visit for that recovery to happen at all.
		if needResult && !suppress {
			if !slot.Aim.IssueBit {
				weaponID := weapon.ID
				if bridge == nil {
					slot.Aim.IssueBit = true
				} else {
					key := pendingKey{Unit: u.Handle, Slot: idx}
					// The first argument of Aim* is the RELATIVE yaw —
					// (bearing - heading) mod 65536, zero meaning dead ahead —
					// and the second is the absolute pitch
					// [06 R-WPN-05 §4]. The authored scripts assume that;
					// handing them the un-shifted absolute yaw is right only
					// for a unit whose heading is 0x8000.
					result := bridge.Aim(cob.WeaponSlot(idx), aimYawForScript(desiredYaw, u.Move.Heading), desiredPitch, func(ret cob.CallbackReturn) {
						if s.pendingAims != nil {
							delete(s.pendingAims, key)
						}
						sum.ReturnSeen = true
						sum.ReturnValue = ret.Value
						if ret.Explicit && ret.Value != 0 {
							slot.Aim.Ready = true
						}
					})
					slot.Aim.IssueBit = true
					slot.Flags |= 0x01
					if result.Started {
						if s.pendingAims == nil {
							s.pendingAims = make(map[pendingKey]pendingAim)
						}
						s.pendingAims[key] = pendingAim{ThreadIdx: result.Thread, DispatchedTick: tick}
						if !sum.Dispatched {
							sum.Dispatched = true
							sum.DispatchSlot = idx
							sum.DispatchWeaponID = weaponID
						}
					} else {
					}
				}
			}
		}
	}
	hasPending := false
	for idx := 0; idx < NumSlots; idx++ {
		if s.pendingAims != nil {
			if _, ok := s.pendingAims[pendingKey{Unit: u.Handle, Slot: idx}]; ok {
				hasPending = true
				break
			}
		}
	}
	shouldDrain := sum.Dispatched || hasPending || queuedTargetCleared
	if shouldDrain && bridge != nil {
		bridge.Drain(1)
		sum.Drained = true
	}
	for idx := 0; idx < NumSlots; idx++ {
		pre := preps[idx]
		slot := u.SlotAt(idx)
		if slot == nil || !slot.IsPopulated() || pre == nil {
			continue
		}
		weapon := pre.weapon
		needLatch, needResult := pre.needLatch, pre.needResult
		key := pendingKey{Unit: u.Handle, Slot: idx}
		if _, ok := s.pendingAims[key]; ok {
			continue
		} else {
			if needResult && slot.Aim.IssueBit && !slot.Aim.Ready {
				continue
			}
		}
		if needLatch && !slot.Aim.IssueBit {
			continue
		}
		if needResult && !slot.Aim.Ready {
			continue
		}
		if slot.Reload > 0 {
			continue
		}
		if !checkAdmission(u, weapon, pre.tgtPos, terrain) {
			// A failed shot-time gate sets the shooter's "could not fire"
			// status and clears no latch [06 §3.3]. Clearing the aim issue
			// bit and the aim result here — which this path used to do for
			// turret weapons — discarded a completed handshake every time a
			// moving target stepped briefly out of range, so the turret had
			// to re-run the whole Aim* turn before it could shoot again and
			// in practice never caught up with a mover.
			//
			// The status bit is bit 12 of the unit's order-event word
			// [06 R-WPN-05 §6]. The reload was already zero above, so a shot
			// really was attempted. The weapon layer never reads the bit back;
			// it is the attack handlers' disengage signal.
			u.Pending |= units.PendingCouldNotFire
			continue
		}
		if weapon.Stockpile {
			if slot.Ammo <= 0 {
				continue
			}
		} else if econ != nil {
			eCost := float32(weapon.EnergyPerShot)
			mCost := float32(weapon.MetalPerShot)
			if eCost != 0 || mCost != 0 {
				p := &econ.Players[u.Owner]
				if p.Stock[economy.Energy] < eCost || p.Stock[economy.Metal] < mCost {
					continue
				}
			}
		}
		// The angular-drift gate, in the unit phase at fire time and before the
		// muzzle query [06 §3.3] [06 R-WPN-03 §1]. Which executor a weapon uses
		// is decided by the first matching flag in the order turret, vlaunch,
		// lineofsight-or-selfprop, dropped [06 §3.3], and only two of them gate:
		//
		//   turret — the slot's stored angles (written when Aim was dispatched)
		//     against the pair just re-solved from current geometry, so the
		//     error is the target's angular drift since the request went out;
		//   line-of-sight/self-propelled — the just-solved absolute direction
		//     against the unit's own heading and pitch, so a fixed-forward
		//     weapon fires only when the target sits ahead within the gate.
		//
		// Vertical-launch and dropped do not call the gate at all.
		//
		// The turret pair is RELATIVE on both sides [06 R-WPN-05 §4]: the
		// stored yaw is the relative one written when Aim was dispatched, and
		// the executor re-solves the relative yaw from the CURRENT muzzle,
		// target and heading. With the target still, a unit that turned by d
		// since the dispatch reads a yaw error of -d, so a turret whose script
		// has already finished turning must re-aim when its hull turns further
		// than the tolerance. Only after the gate passes does the executor add
		// the current heading back (relative -> absolute) for the spread and
		// the creator.
		switch {
		case weapon.Turret:
			wantRelYaw := aimYawForScript(pre.desiredYaw, u.Move.Heading)
			if !DriftGatePass(weapon, unitStationary(u), slot.DesiredYaw, wantRelYaw, slot.DesiredPitch, pre.desiredPitch) {
				// Failure clears the Aim-issued latch and returns failure
				// without touching the reload timer, the aim-ready word or the
				// RNG, so the next slot visit re-solves and re-dispatches Aim
				// [06 R-WPN-03 §2].
				slot.Aim.IssueBit = false
				slot.Flags &^= 0x01
				if s.pendingAims != nil {
					delete(s.pendingAims, pendingKey{Unit: u.Handle, Slot: idx})
				}
				continue
			}
			// Relative -> absolute, before the spread of [06 §4.4] and the
			// creator [06 R-WPN-05 §4]. The result is this build's own
			// absolute convention, which is what the projectile arithmetic and
			// the ballistic creator consume.
			slot.DesiredYaw = retailYawFromGo(slot.DesiredYaw + u.Move.Heading)
		case weapon.VLaunch:
			// no gate [06 §3.3]
		case weapon.LineOfSight || weapon.SelfProp:
			// This executor owns no latch: it simply returns failure, leaving
			// the absolute angles it just wrote in the slot [06 R-WPN-03 §2].
			//
			// The solved yaw is compared against the unit's own HEADING, so it
			// has to be expressed in the heading's convention — retail's
			// [06 R-WPN-05 §4]. The un-shifted comparison this used to make
			// was half a turn out and could never come inside a gate of 150,
			// 2,000, or an authored `tolerance`, so a fixed-forward weapon
			// only ever passed with its target behind it.
			if !DriftGatePass(weapon, unitStationary(u), retailYawFromGo(pre.desiredYaw), u.Move.Heading, pre.desiredPitch, u.Move.Pitch) {
				continue
			}
		}
		if !tryFireForSlot(u, slot, idx, tick, terrain, simRNG, s, w, catalog, pre.tgtPos) {
			continue
		}
		if needResult || needLatch {
			slot.Aim.IssueBit = false
			slot.Aim.Ready = false
			slot.Flags &^= 0x01
			if s.pendingAims != nil {
				delete(s.pendingAims, pendingKey{Unit: u.Handle, Slot: idx})
			}
		}
		if !weapon.Stockpile {
			stored := ComputeStoredReload(u.Health, u.MaxHealth, u.Kills, weapon.ReloadTime)
			slot.Reload = stored
		}
		if weapon.Stockpile && slot.Ammo > 0 {
			slot.Ammo--
			if slot.Ammo < 0 {
				slot.Ammo = 0
			}
		}
		if !weapon.Stockpile && econ != nil {
			eCost := float32(weapon.EnergyPerShot)
			mCost := float32(weapon.MetalPerShot)
			if eCost != 0 || mCost != 0 {
				// The direct two-resource payment credits the SHOOTER's
				// subrecord, not the player mirror: the helper reaches through
				// the subrecord's owner pointer only for the live stock
				// [05 R-ECO-01 §7]. Pass totals are identical either way — the
				// settlement fold sums the subrecords into the mirror.
				economy.ImmediateDebit(&econ.Players[u.Owner], econ.UnitBuckets(u.Handle), eCost, mCost)
			}
		}
		sum.Fired++
	}
	return sum
}

// TickWeapons runs the integrated per-unit weapon pipeline [06 §3][06 §4][04 §5.3][GAP T15].
// Compatibility wrapper: loops over units in deterministic order and delegates to StepWeaponsForUnit ON-04.
// Documented non-authoritative: authoritative behavior is per-unit StepWeaponsForUnit.
func (s *Service) TickWeapons(tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil || w == nil {
		return
	}
	// DET-01: no global fallback; session must inject simRNG.
	if simRNG == nil {
		// nil means no draw (return 0 without advancing) — production always injects.
	}
	var unitList []*units.Unit
	if w.IsSliced() {
		unitList = w.IterSliced()
		_ = unitList
	} else {
		unitList = w.Iter()
		_ = unitList
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
		for _, u := range unitList {
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

// autonomousScanCursor is the persistent per-player cursor of [06 §3.2]: the
// scan "advanc[es] a persistent cursor through the owning player's unit vector
// and wrap[s] to its beginning at the end". That vector is the player's whole
// FIXED RECORD SLICE of the unit array — `perPlayerLimit` consecutive records,
// bounded once at session entry and never resized, free records included — and
// not a compacted list of its live units
// [06 §3.2 "The budget word is the per-player unit limit"][05 R-SHARE-01 §7].
//
// The window is therefore a range of record INDICES, known before the pass
// starts rather than measured by it: this tick admits the `span` consecutive
// indices starting at the player's cursor, wrapping at the end of the slice,
// and the cursor advances by `span` every tick. A live unit is visited exactly
// when its own record index falls in that window, so a free record inside the
// window still spends budget by simply not being anybody — which is retail's
// "nonzero definition index" clause read from the other side. A player owning
// few units spends most of its budget on empty records and its units are
// revisited on the same fixed `ceil(perPlayerLimit / span)` period as a player
// owning many.
//
// Correction (WU-19-154): the previous model divided the GLOBAL LIVE COUNT and
// reconstructed the cursor from the order units arrived in the session's single
// unit sweep, measuring the "vector length" as the number of live units the
// previous pass saw. Both halves were the superseded reading of [06 §3.2],
// which its own correction paragraph names; the marker that carried them said
// closing it needed accessors internal/units does not expose, and that was
// false — `World.MaxDefs` is the per-player slice width (the name predates
// WU-19-118 sizing the pool from the unit limit and misdescribes it),
// `World.SliceForPlayer` gives the slice base, and `Unit.Handle` is the record
// index.
type autonomousScanCursor struct {
	tick   uint32
	primed bool
	span   int // this tick's budget, `perPlayerLimit/30 + 1`
	limit  int // the per-player record slice length; 0 for an unsliced fixture pool
	cursor [combatPlayerSlots]int
}

// beginTick rolls every player's cursor forward onto a new tick, wrapping it
// against the fixed slice length [06 §3.2]. perPlayerLimit is the session's
// per-player unit limit, which is also the slice length.
func (c *autonomousScanCursor) beginTick(tick uint32, perPlayerLimit int) {
	if c.primed && c.tick == tick {
		return
	}
	if perPlayerLimit < 0 {
		perPlayerLimit = 0
	}
	if c.primed {
		for p := range c.cursor {
			if c.limit > 0 {
				c.cursor[p] = (c.cursor[p] + c.span) % c.limit
			} else {
				c.cursor[p] = 0
			}
		}
	}
	c.limit = perPlayerLimit
	// `word / 30 + 1` [06 §3.2] — the dividend is truncated to sixteen bits
	// before the divide, which is retail's storage width for the limit
	// [05 R-SHARE-01 §7].
	c.span = int(uint16(perPlayerLimit))/autonomousScanDivisor + 1
	c.tick = tick
	c.primed = true
}

// visits reports whether this tick's window covers the record the unit occupies
// [06 §3.2]. record is the unit's index within its owner's fixed slice; a
// negative index means the pool is not sliced (a fixture world), where there is
// no record array to walk and every unit is admitted.
func (c *autonomousScanCursor) visits(owner uint8, record int) bool {
	p := int(owner)
	if p < 0 || p >= combatPlayerSlots {
		return false
	}
	count := c.limit
	if count <= 0 || record < 0 || c.span >= count {
		// Unsliced fixture pool, or a budget that covers the whole slice: every
		// record is visited every tick.
		return true
	}
	rel := record - c.cursor[p]
	if rel < 0 {
		rel += count // the window wraps to the beginning of the slice
	}
	return rel < c.span
}

// autonomousScanVisitsUnit is the autonomous scan's per-unit admission
// [06 §3.2]: the visited unit must have a nonzero definition index, a
// remaining-build-fraction of exactly zero, the ARMED status bit set, and its
// two-bit stance field equal to the fire-at-will value — read in that order off
// the one runtime status word.
//
// The cursor window is tested first, because the budget is spent on record
// slots rather than on units that pass.
func (s *Service) autonomousScanVisitsUnit(u *units.Unit, tick uint32, w *units.World) bool {
	if s == nil || u == nil {
		return false
	}
	// The slice width is the session's per-player unit limit [05 R-SHARE-01 §7];
	// MaxDefs is its accessor under a name that predates WU-19-118 sizing the
	// pool from the limit rather than from the catalog. record is the unit's
	// index inside its owner's slice, and stays -1 for an unsliced fixture pool.
	record := -1
	if start, _, ok := w.SliceForPlayer(int(u.Owner)); ok {
		record = int(u.Handle) - start
	}
	s.scanCursor.beginTick(tick, w.MaxDefs())
	if !s.scanCursor.visits(u.Owner, record) {
		return false
	}
	if u.Def == nil {
		return false // "a nonzero definition index"
	}
	if u.Remaining != 0 {
		return false // "a remaining-build-fraction of exactly zero"
	}
	// The third clause, now named [06 §3.2 "The third clause is the armed
	// bit"]. The previous text here was an open-question marker saying that "one high
	// status bit set" did "not name the bit, and no other section identifies a
	// status bit this scan reads", and left the clause unmodelled so the gate
	// scanned a superset. That was incomplete rather than wrong: the scan reads
	// the same runtime status word this line's stance field lives in, and the
	// bit it tests is the ARMED bit — the second of the two high bits the
	// classifier census names, set once at creation when the definition
	// resolved at least one of its three weapon slots [08 "Classifier
	// eligibility, destinations, and order"]. The sense is SET: a clear bit
	// ends the unit's visit before any slot is looked at.
	if u.Flags&units.ArmedStatus == 0 {
		return false
	}
	return u.Flags>>units.StandingFireShift&units.StandingFieldMask == stanceFireAtWill
}

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

	// seen is this tick's view of the sensor phase's seen bit, indexed by unit
	// handle. It is refreshed at most once per tick and only on a tick that
	// rebuilds at least one side's registry.
	seen       []bool
	seenTick   uint32
	seenPrimed bool
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

// refreshSeen rebuilds the per-handle seen-bit view from the sensor phase's
// last completed pass [03 §3.4][R-VIS-01 §4].
//
// The bit is recomputed every tick from the LOCAL player's point of view only,
// so every side's secondary list is built from the local observer's sensors —
// "in single player that is the human's view, and a computer opponent's
// fallback acquisition therefore inherits it" [06 §3.1]. Reading it here rather
// than recomputing it keeps the four producers (own/allied, radar, sonar, line
// of sight) and the two jam clears in the one phase that owns them.
func (r *targetRegistry) refreshSeen(tick uint32, vis *visibility.Service) {
	if r == nil {
		return
	}
	if r.seenPrimed && r.seenTick == tick {
		return
	}
	r.seenPrimed = true
	r.seenTick = tick
	for i := range r.seen {
		r.seen[i] = false
	}
	if vis == nil {
		return
	}
	for _, in := range vis.SensorInputs() { // a slice, in the sensor phase's order (I1)
		if in.Status&visibility.SeenBit == 0 {
			continue
		}
		h := int(in.ID)
		if h < 0 {
			continue
		}
		if h >= len(r.seen) {
			grown := make([]bool, h+1)
			copy(grown, r.seen)
			r.seen = grown
		}
		r.seen[h] = true
	}
}

// seenBit reports the last sensor pass's seen bit for one unit handle.
func (r *targetRegistry) seenBit(h pool.Handle) bool {
	if r == nil || int(h) < 0 || int(h) >= len(r.seen) {
		return false
	}
	return r.seen[h]
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
	r.refreshSeen(tick, vis)
	gate := false
	pri := r.primary[p][:0] // both lists are cleared at every rebuild [06 §3.1]
	sec := r.secondary[p][:0]
	for _, u := range w.Iter() {
		if u == nil || !u.Alive || u.Dying {
			continue // alive bit set, death latch clear [06 §3.1]
		}
		if u.Owner == owner {
			if unitOpensTargetingUpgradeGate(u, owner) {
				gate = true // the constant 1, not a count [06 §3.1]
			}
			continue // an own unit is never a candidate for its owner's lists
		}
		if isAllied(owner, u.Owner, econ) {
			continue // neither hostile nor own: skipped entirely [06 §3.1]
		}
		if u.Flags&units.ImmunityStatus == 0 && directlyVisibleAtRebuild(owner, u, vis) {
			pri = append(pri, u.Handle) // unit-array order [06 §3.1] (I1)
		}
		if r.seenBit(u.Handle) {
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
	return vis.IsVisible(visibility.PlayerID(owner), visibilityTarget(cand, sensorStatus(vis, cand.Handle)))
}

// sensorStatus reads the completed sensor phase's runtime word for this pool
// handle. The sonar bit is authored by that phase and can be cleared by its
// sonar-jam callback; alliance membership is not a substitute [06 §3.1]
// [03 R-VIS-01 §4][03 R-VIS-01 §5].
func sensorStatus(vis *visibility.Service, h pool.Handle) uint32 {
	if vis == nil {
		return 0
	}
	if status, ok := vis.SensorStatus(uint16(h)); ok {
		return status
	}
	return 0
}

// visibilityTarget forms the direct-visibility probe from the definition's
// bounding record: start at min X/max Y/min Z, then carry the three spans
// through the visibility predicate's four probes [06 §3.1][03 §3.2].
func visibilityTarget(cand *units.Unit, status uint32) visibility.Target {
	if cand == nil {
		return visibility.Target{}
	}
	min, max := cand.Def.BoundingExtents()
	return visibility.Target{
		Owner:   visibility.PlayerID(cand.Owner),
		X:       cand.X.Add(numeric.Fixed(min[0])),
		Y:       cand.Y.Add(numeric.Fixed(max[1])),
		Z:       cand.Z.Add(numeric.Fixed(min[2])),
		XExtent: numeric.Fixed(max[0] - min[0]),
		YExtent: numeric.Fixed(max[1] - min[1]),
		ZExtent: numeric.Fixed(max[2] - min[2]),
		Hidden:  isCloakedUnit(cand),
		Status:  status,
	}
}

func (s *Service) acquireTargetForSlot(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, econ *economy.Service, catalogs ...*content.Catalog) (pool.Handle, bool) {
	return s.acquireTargetForSlotRange(u, slot, idx, w, vis, terrain, simRNG, econ, -1, catalogs...)
}

// acquireTargetForSlotRange is the non-mutating form used by order-facing
// acquisition. A nonnegative range overrides only the query's range operand;
// the compiled WeaponDef remains immutable [02 "Weapon record"][06 §3.2].
func (s *Service) acquireTargetForSlotRange(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, econ *economy.Service, rangeLimit int32, catalogs ...*content.Catalog) (pool.Handle, bool) {
	if u == nil || w == nil || slot == nil || slot.Weapon == nil {
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
	candidates := s.primaryCandidates(u, w, seaLevel, vis, econ, catalog)
	acq := slotAcquisition(u, slot, idx, w, vis, terrain, simRNG, catalog, seaLevel, rangeLimit)
	// The registry's secondary-list gate and its secondary list [06 §3.1].
	// Both belong to the SCANNING PLAYER — the registry is per side and is the
	// same for every slot of every unit that player owns — so they are read
	// here, by owner, and never from the shooter's own definition.
	acq.HasUpgrade = s.targetingUpgradeGateFor(u.Owner)
	if acq.HasUpgrade {
		acq.Secondary = s.secondaryCandidates(u, w, seaLevel, vis, econ, catalog)
	}
	h, ok := AcquireTarget(candidates, acq)
	return h, ok
}

// primaryCandidates materializes the scanning player's PRIMARY list into the
// per-attempt filter's candidate array [06 §3.1].
//
// "Automatic acquisition never scans the unit array": the array walked here is
// the registry's list, filed at the last rebuild with the direct-visibility
// predicate applied there, and the filter over it is thin — "no visibility,
// category, sensor, medium, alliance or range test happens at this point". This
// build's filter is liveness plus hostility; the distance test, the physical
// gate and the category split are AcquireTarget's, in that section's order.
//
// Hostility is re-tested rather than trusted from the rebuild because an
// alliance declared since then would otherwise leave a now-allied unit
// shootable for up to thirty ticks, and the alliance row is read live
// everywhere else in this package. Liveness must be re-tested: the list is up
// to thirty ticks stale and can name units that have died, and a handle the
// pool has since reused names a different unit (I5).
//
// The one thing NOT re-tested is visibility. A unit that has become visible
// since the rebuild is not on this list and cannot be acquired until the next
// one — "an acquisition can therefore see a list up to thirty ticks stale" —
// and a listed unit that has since gone dark stays acquirable for the rest of
// the window.
func (s *Service) primaryCandidates(u *units.Unit, w *units.World, seaLevel numeric.Fixed, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog) []Candidate {
	if s == nil || u == nil || w == nil {
		return nil
	}
	list := s.targets.primaryList(u.Owner)
	if len(list) == 0 {
		return nil
	}
	out := make([]Candidate, 0, len(list))
	for _, h := range list {
		cand := w.Unit(h)
		if cand == nil || cand.Handle == u.Handle {
			continue
		}
		if !cand.Alive || cand.Dying {
			continue // alive bit set, death latch clear [06 §3.1]
		}
		if !isHostile(u, cand, econ) {
			continue
		}
		out = append(out, acquisitionCandidate(u, cand, seaLevel, sensorStatus(vis, cand.Handle), catalog))
	}
	return out
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
func (s *Service) secondaryCandidates(u *units.Unit, w *units.World, seaLevel numeric.Fixed, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog) []Candidate {
	if s == nil || u == nil || w == nil {
		return nil
	}
	list := s.targets.secondaryList(u.Owner)
	if len(list) == 0 {
		return nil
	}
	out := make([]Candidate, 0, len(list))
	for _, h := range list {
		cand := w.Unit(h)
		if cand == nil || cand.Handle == u.Handle {
			continue
		}
		if !cand.Alive || cand.Dying {
			continue // alive bit set, death latch clear [06 §3.1]
		}
		out = append(out, acquisitionCandidate(u, cand, seaLevel, sensorStatus(vis, cand.Handle), catalog))
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
		Floater:   candGate.Floater,
		CanHover:  candGate.CanHover,
		// The stunned mark travels with the candidate; only a paralyzer slot
		// reads it [06 §3.2] check 5 [06 R-DMG-01 §11].
		Stunned: cand.Stunned,
	}
}

// slotAcquisition builds one weapon slot's acquisition-time gate set. A
// nonnegative rangeLimit overrides only the query's range operand [06 §3.1].
func slotAcquisition(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, catalog *content.Catalog, seaLevel numeric.Fixed, rangeLimit int32) Acquisition {
	weapon := slot.Weapon
	queryRange := weapon.Range
	if rangeLimit >= 0 {
		queryRange = rangeLimit
	}
	acq := Acquisition{
		ShooterX: u.X,
		ShooterZ: u.Z,
		ShooterY: u.Y,
		// The shooter half of the non-water height clause carries the model
		// top-height word too [06 §3.1][06 R-WPN-05 §1] clause 2.
		ShooterModelTop: modelTop(u),
		SeaLevel:        seaLevel,
		Range:           queryRange,
		BadTargetMask:   badMaskForSlot(u.Def, idx),
		MaskResolved:    catalog != nil && u.Def != nil,
		WaterWeapon:     weapon.WaterWeapon,
		ToAir:           weapon.ToAirWeapon,
		Ballistic:       weapon.Ballistic,
		// A paralyzer slot rejects candidates that already carry the stunned
		// mark [06 §3.2] check 5; every other weapon ignores it.
		Paralyzer: weapon.Paralyzer,
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
			return vis.IsVisible(visibility.PlayerID(u.Owner), visibilityTarget(candUnit, sensorStatus(vis, candUnit.Handle)))
		}
	}
	if weapon.Ballistic {
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
// It is exported because the damage path does not carry the visibility,
// terrain, ledger and catalog operands the gate needs; the session binds it
// into ReactionSeams.SlotAcquisitionAdmits with those in hand. It draws no RNG:
// the gate is the primary-list hostility and visibility predicates plus the
// physical admission, never the scoring pass (I4).
func SlotAcquisitionAdmits(u *units.Unit, idx int, cand *units.Unit, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog) bool {
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
	if !isHostile(u, cand, econ) {
		return false
	}
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
	c := acquisitionCandidate(u, cand, seaLevel, sensorStatus(vis, cand.Handle), catalog)
	acq := slotAcquisition(u, slot, idx, w, vis, terrain, nil, catalog, seaLevel, -1)
	return IsValidAcquisitionCandidate(c, acq)
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
func checkAdmission(u *units.Unit, weapon *content.WeaponDef, tgtPos Vec3, terrain *world.Terrain) bool {
	return shotTimeAdmits(u, weapon, tgtPos.X, tgtPos.Y, tgtPos.Z, terrain)
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
	muzzlePieceFn := func(slotIdx int) int32 {
		if bridge == nil {
			return -1
		}
		return bridge.QueryWeapon(cob.WeaponSlot(slotIdx)).QueryValue()
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
	cSlot := Slot{
		Weapon:       weapon,
		Reload:       slot.Reload,
		Flags:        slot.Flags,
		DesiredYaw:   slot.DesiredYaw,
		DesiredPitch: slot.DesiredPitch,
		Ammo:         slot.Ammo,
		MuzzlePiece:  slot.MuzzlePiece,
		// The `T0` divisor the ballistic creator reads. It is copied, never
		// written back: the word has exactly one writer, the slot initializer
		// at unit construction [06 R-WPN-05 §3][06 §6.4].
		DistanceWord: slot.DistanceWord,
		Aim:          slot.Aim,
		Target:       tgt,
	}
	_, ok := TryFire(svc, &cSlot, idx, tgt, tick, ports)
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
	slot := u.SlotAt(slotIdx)
	if slot == nil {
		return
	}
	// RockUnit's recoil direction is the slot's stored yaw minus the heading,
	// in retail's convention [06 R-WPN-05 §3][06 R-WPN-05 §5]. By this point
	// the stored yaw is absolute and carries the accuracy draw — which for an
	// ordinary-family weapon is the ONLY thing the draw reaches.
	rel := int16(retailYawFromGo(slot.DesiredYaw) - u.Move.Heading)
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

// TickProjectiles is the projectile phase [06 §5]. It advances burst anchors,
// integrates each motion family, resolves the collision ladder and its impact,
// and compacts the pool at the tail. The active span is captured at entry —
// before the burst advance, not after it — so records appended during the pass,
// burst clones included, wait for the next tick [06 §4.3] [06 §5.1] [I1]. The
// tail compactor is the exception: it reads the CURRENT count and so does
// include them [06 §5.1].
func (s *Service) TickProjectiles(tick uint32, w *units.World, terrain *world.Terrain, windState *world.Wind, featSvc *features.Service, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil {
		return
	}
	// The phase's active span is captured ONCE, before anything in the phase
	// runs, and it bounds BOTH halves of the pass. Retail's phase is a single
	// ascending walk over that captured count in which each record takes
	// either the burst branch or the motion branch; splitting it into a burst
	// pass and a motion pass is only sound while both walk the same span. A
	// clone appended by the burst advance therefore lands beyond the span and
	// does not move until the next tick — including the zero-interval case,
	// where the root emits a clone during its own creation tick and the
	// captured span still holds that clone still `[06 §4.3]` `[06 §5.1]`
	// `[01 §6.2]` C11 [I1]. Re-reading the count after the burst advance
	// stepped every clone one tick early, which shifted each pellet's whole
	// flight — and its impact — a tick ahead of the contract.
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
		var res AdvanceResult
		switch MotionFamilyForWeapon(weapon) {
		case MotionDirect:
			res = AdvanceDirect(p, weapon, tick)
		case MotionBallistic:
			res = AdvanceBallistic(p, weapon, tick, windVec, gravity)
		case MotionDropped:
			res = AdvanceDropped(p, weapon, tick, windVec, gravity)
		case MotionMeteor:
			res = AdvanceMeteor(p, weapon, tick)
		case MotionSelfProp:
			res = AdvanceSelfProp(p, weapon, tick, gravity, seaLevel, guidance)
		default:
			res = AdvanceRetire
		}
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

func checkCollision(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, opaqueLiquid bool) (hitUnit pool.Handle, hitFeature *features.Instance, isWaterTerrain bool, isOffMap bool, terrainContact bool, bounce bool) {
	if terrain != nil {
		cx := world.WorldToCell(p.Pos.X)
		cz := world.WorldToCell(p.Pos.Z)
		if cx < 0 || cz < 0 || cx >= terrain.CellW || cz >= terrain.CellH {
			isOffMap = true
			return
		}
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
		if hit := contactUnitInCell(p, w, terrain, cx, cz); hit != 0 {
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
	if weapon.EndSmoke && !isWaterTerrain {
		s.emitEvent(Event{Kind: EventEndSmoke, Tick: tick, Source: p.Shooter, Position: p.Pos})
	} else {
		isWaterExplosion := isWaterTerrain && !hasDirectTarget
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
				Graphic: graphic, Bank: bank, Smoke: weapon.StartSmoke,
				// "the central impact passes (point, land or water holder, 0,
				// waterCell) — so EVERY projectile impact, land or water, draws
				// calculated table 0 under its art" [06 R-WFX-01 §2].
				HasCalculatedFlash: true, CalculatedTable: impactFlashTable,
			})
		}
	}
	s.emitEvent(Event{Kind: EventProjectileImpact, Tick: tick, Source: p.Shooter, Target: directUnit, Position: p.Pos})
	applyProjectileDamage(s, p, weapon, w, terrain, tick, directUnit)
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
	_ = wind
}

func applyProjectileDamage(service *Service, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, tick uint32, directUnit pool.Handle) {
	if w == nil || weapon == nil {
		return
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
		return
	}
	if directUnit != 0 && weapon.AreaOfEffect <= 16 {
		victim := w.Unit(directUnit)
		if victim != nil {
			applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick)
		}
		return
	}
	radius := BlastRadius(weapon.AreaOfEffect)
	if radius <= 0 {
		if directUnit != 0 {
			if victim := w.Unit(directUnit); victim != nil {
				applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick)
			}
		}
		return
	}
	// Shared area splash: EnumerateArea→DistanceToBox→Falloff→ApplyDamage [06 §9.3]
	// Extracted to ExplodeWeaponAt for death DoExplosion reuse [06 §12.1] C22–C25 (I1, I2)
	service.explodeWeaponAt(w, terrain, weapon, p.Pos, p.Shooter, p.ShooterSide, tick)
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
	s.emitEvent(Event{Kind: EventWaterExplosion, Tick: tick, Source: p.Shooter, Target: h, Position: p.Pos, Graphic: graphic, Bank: bank, HasCalculatedFlash: true, CalculatedTable: impactFlashTable, Smoke: weapon.StartSmoke})
}

// ExplodeWeaponAt is the shared authoritative area-damage entry point for
// projectile splash and death explosions [06 §9.3][06 §12.1] C22–C25.
// It performs EnumerateArea → DistanceToBox (the definition's bounding record
// translated by the unit position) → Falloff (float32) →
// SelectBaseDamage → ComputeScaledAmount → ApplyDamage→Destroy exactly as TickProjectiles
// does, deterministically (pool asc via Iter, I1) and without new float64 sites
// (I2 allowlist: Falloff float32 only). Collect-then-apply avoids double-processing
// victims when nested deaths chain-explode [01 §4.4].
func (s *Service) ExplodeWeaponAt(w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, tick uint32) {
	shooterSide := NeutralSide
	if attacker := w.Unit(shooter); attacker != nil {
		shooterSide = attacker.Owner
	}
	s.explodeWeaponAt(w, terrain, weapon, impact, shooter, shooterSide, tick)
}

// explodeWeaponAt is the shared area recipient walk. The central impact
// record carries the shooter-side byte separately from the shooter identity,
// so a stack death record can route with its dying owner's side while retaining
// its null shooter provenance [06 §12.2][06 §9.1].
func (s *Service) explodeWeaponAt(w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, shooterSide uint8, tick uint32) {
	if w == nil || weapon == nil {
		return
	}
	radius := BlastRadius(weapon.AreaOfEffect) // [06 §9.3] unsigned area>>1
	if radius <= 0 {
		return
	}
	var mapW, mapH int32
	if terrain != nil {
		mapW = terrain.CellW
		mapH = terrain.CellH
	}
	type victim struct {
		h       pool.Handle
		u       *units.Unit
		dist    int32
		falloff float32
		// feature marks a recipient that is a FEATURE anchor rather than a
		// unit; fcx/fcz are then the anchor cell [05 R-FEAT-01 §8].
		feature  bool
		fcx, fcz int
	}
	var victims []victim
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
		if cell := terrain.PlotAt(cx, cz); cell != nil {
			for _, word := range [2]int16{cell.OccupantA(), cell.OccupantB()} {
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
				if word <= 0 {
					continue // the free sentinel; slot zero is never an occupant [I5]
				}
				h := pool.Handle(word)
				if shooter != 0 && h == shooter {
					continue
				}
				// "Unit deduplication happens BEFORE the radius test, against a
				// memory of at most 20 unit pointers … a candidate encountered
				// when the memory is full is still processed but not remembered,
				// so a later occurrence is processed again. An out-of-radius
				// first sighting therefore consumes a memory entry" [06 §9.3].
				if unitDedup.SeenUnit(h) {
					continue
				}
				u := w.Unit(h)
				if u == nil || !u.Alive || u.Dying {
					continue // a word naming a freed slot names no candidate
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
					continue // strict < radius [06 §9.3]
				}
				falloff := float32(1)
				if dist != 0 {
					falloff = Falloff(float32(dist), float32(radius), float32(weapon.EdgeEffectiveness)) // [06 §9.3] float32
				}
				victims = append(victims, victim{h: u.Handle, u: u, dist: dist, falloff: falloff})
			}
		}
		// "Within each cell the order is unit slot zero, unit slot one, then
		// the feature/terrain candidate" [06 §9.3]. The feature therefore joins
		// the SAME ordered recipient list, after this cell's units and before
		// the next cell's, which is what fixes the position of an ignition's
		// simulation draw relative to the unit damage around it (I4).
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
		victims = append(victims, victim{feature: true, fcx: cand.CX, fcz: cand.CZ, dist: fdist})
	})
	if terrain == nil || mapW == 0 {
		// Fallback when no terrain map (mirrors TickProjectiles fallback) — planar dist2 check, no float64
		if len(victims) == 0 {
			for _, u := range w.Iter() { // deterministic (I1)
				if u == nil || !u.Alive || u.Dying {
					continue
				}
				if shooter != 0 && u.Handle == shooter {
					continue // the shooter is excluded from every blast [06 §9.3]
				}
				dx := impact.X.Int() - u.X.Int()
				dz := impact.Z.Int() - u.Z.Int()
				dist2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
				if dist2 >= int64(radius)*int64(radius) {
					continue
				}
				victims = append(victims, victim{h: u.Handle, u: u, dist: 0, falloff: 1})
			}
		}
	}
	// Apply deterministically in collected order (EnumerateArea rows Z asc, cols X asc, Iter pool asc) [I1]
	for _, vi := range victims {
		if vi.feature {
			// The feature damage entry [06 §13.1][05 R-FEAT-01 §8]: the
			// weapon's authored DEFAULT damage word exactly — no area falloff,
			// no armour table, no veterancy, no global double/half gate — and
			// the firestarter byte, whose only reader is the ignition test and
			// which carries no roll of its own. Ignition takes precedence: a
			// flammable feature hit by a firestarter weapon never accumulates
			// damage on that hit. Service.Ignite is that whole cascade.
			//
			// Step 1's global settings bit is not modelled: its only writer
			// sets it unconditionally at startup and nothing clears it, so the
			// gate is always open in retail [05 R-FEAT-01 §8 step 1].
			s.Features.Ignite(vi.fcx, vi.fcz, weapon.Firestarter, weapon.DamageDefault)
			continue
		}
		cand := vi.u
		if cand == nil || !cand.Alive || cand.Dying {
			continue
		}
		p := &Projectile{Pos: impact, Shooter: shooter, ShooterSide: shooterSide} // synthetic projectile for damage pipeline [06 §9.1]
		applyDamageToUnit(s, cand, p, weapon, vi.falloff, vi.dist, w, tick)
	}
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
	amount := scaleAcceptedAmount(in.Nominal, victim.Kills, UnitArmored(victim), damageModifier)
	result := DamageResult{Accepted: true, Amount: amount}

	// These effects are deliberately before provenance rewriting: reaction
	// observes the prior packet state [06 §9.1][06 R-WPN-04 §2].
	SetDamageFlash(victim)
	s.emitEvent(Event{Kind: EventDamageFlash, Tick: tick, Source: in.Attacker, Target: victim.Handle, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}, Duration: DamageFlashTicks})
	if in.Kind != KindNoReaction {
		s.ReactToDamage(w, victim, w.Unit(in.Attacker), tick)
	}
	victim.LastDamageCause = in.Kind
	if in.Attacker != 0 {
		victim.EngagementTarget = in.Attacker
		if rawAttacker := w.RawUnitRecord(in.Attacker); rawAttacker != nil {
			victim.LastDamageSide = rawAttacker.Owner
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
		// Preserve the modular signed word for the later severity calculation.
		w.DestroyBy(victim.Handle, units.DeathCauseFromKind(in.Kind), in.Attacker)
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
func weaponDamageNominal(weapon *content.WeaponDef, victim *units.Unit, rawAttacker *units.Unit, falloff float32) int32 {
	if weapon == nil || victim == nil {
		return 0
	}
	name := ""
	if victim.Def != nil {
		name = victim.Def.UnitName
	}
	attackerKills := int32(0)
	if rawAttacker != nil {
		attackerKills = rawAttacker.Kills
	}
	return weaponNominal(SelectBaseDamage(weapon, name), falloff, attackerKills, false, false)
}

func applyDamageToUnit(service *Service, victim *units.Unit, p *Projectile, weapon *content.WeaponDef, falloff float32, distance int32, w *units.World, tick uint32) {
	if service == nil || victim == nil || p == nil || weapon == nil || w == nil {
		return
	}
	cause := KindOrdinary
	if weapon.Paralyzer {
		cause = KindParalyzer
	}
	service.AcceptDamage(w, tick, DamageInput{
		Victim: victim.Handle, Attacker: p.Shooter,
		Nominal:   weaponDamageNominal(weapon, victim, w.RawUnitRecord(p.Shooter), falloff),
		Direction: hitDirectionByte(p, victim), Kind: cause,
	})
	_ = distance
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
