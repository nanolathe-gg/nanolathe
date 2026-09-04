// Package combat — integrated weapon and projectile pipeline per [06] P0-I04.
package combat

import (
	"sort"

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

func isUnderwaterUnit(u *units.Unit, seaLevel numeric.Fixed) bool {
	if u == nil {
		return false
	}
	return u.Y.Raw() < seaLevel.Raw()
}

func aimNameForSlot(slotIdx int) string {
	switch slotIdx {
	case 1:
		return "AimSecondary"
	case 2:
		return "AimTertiary"
	default:
		return "AimPrimary"
	}
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
		if slot.Target.Kind == units.TargetUnit && slot.Target.Unit != 0 {
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
				// Bit 1 is the slot's ENABLED bit, whose one writer is the
				// slot initializer [06 R-WPN-05 §3]; a lost target does not
				// disable the slot. This used to clear it here, reading it as
				// an armed/has-target flag.
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
			if tu := w.Unit(tgtHandle); tu != nil {
				tgtPos = Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}
			} else {
				continue
			}
		} else {
			tgtPos = Vec3{X: slot.Target.X, Y: PointTargetHeight(terrain, slot.Target.X, slot.Target.Z), Z: slot.Target.Z}
		}
		// Weapon piece selection is a synchronous Q path. AimFrom* uses -1 and
		// falls back to Query* with seed 0; SweetSpot is a separate Q query and
		// must not be conflated with the muzzle result [R-P0-07][04 §5.3].
		if bridge != nil {
			piece := bridge.AimPiece(cob.WeaponSlot(idx))
			if piece.Started && piece.QueryValue() >= 0 {
				slot.MuzzlePiece = piece.QueryValue()
			}
			_ = bridge.SweetSpot()
		}
		muzzlePos, muzzleOK := muzzleWorldPosResolved(u, slot.MuzzlePiece)
		if !muzzleOK {
			// A strict production binding resolves nonnegative queried pieces
			// through its model map; a negative query selects the unit-origin path.
			continue
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
					if _, present := bridge.VM.ScriptPC(aimNameForSlot(idx)); !present {
					}
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
		if !tryFireForSlot(u, slot, idx, tick, terrain, simRNG, s, w) {
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
				economy.ImmediateDebit(&econ.Players[u.Owner], eCost, mCost)
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
// TODO(T25): the dividend is wrong here, and correcting it is blocked on an
// accessor this package does not own. [06 §3.2 "The budget word is the
// per-player unit limit"] establishes that the sixteen-bit word the scan
// divides is the session's PER-PLAYER UNIT LIMIT — a setup constant, stock
// default 200 — and not a live unit count, and that the cursor walks that
// player's whole fixed record slice, free slots included, rather than a
// compacted vector of its live units. The budget is therefore constant for the
// session and every slot is revisited on a fixed period near thirty ticks.
// Placeholder: keep the live-count dividend and the compacted-vector cursor
// below, which agree with the corrected model whenever a player's live count is
// near the limit and drift wider as it falls. Settling it needs the per-player
// pool capacity and a free-slot-inclusive walk from internal/units, which this
// unit does not own; the numbers only ever change the phase and period of an
// acquisition, never whether one is legal.
const autonomousScanDivisor = 30

// combatPlayerSlots is the session's ten player slots [05 "Player slot"]. The
// cursor is indexed over that fixed range, never a map (I1).
const combatPlayerSlots = 10

// autonomousScanCursor is the persistent per-player cursor of [06 §3.2]: the
// scan "advanc[es] a persistent cursor through the owning player's unit vector
// and wrap[s] to its beginning at the end". Retail runs one pass per player per
// tick from the player's manager; this build reaches each unit through the
// session's single unit sweep instead, so the cursor is reconstructed from the
// order units arrive in: a unit's position in its owner's pass is its index in
// that vector, and the pass admits the `span` consecutive indices starting at
// the player's cursor.
//
// The vector's length is only known once a pass has walked it, so `count` is
// the length the PREVIOUS pass measured — which is also the length retail's
// cursor was last wrapped against. Before a player's first measured pass the
// length is unknown and every unit is admitted; the pass that measures it
// installs the window from the next tick.
type autonomousScanCursor struct {
	tick   uint32
	primed bool
	span   int // this tick's budget, `word/30 + 1` (see the TODO(T25) above)
	cursor [combatPlayerSlots]int
	seen   [combatPlayerSlots]int
	count  [combatPlayerSlots]int
}

// beginTick rolls every player's cursor forward onto a new tick, wrapping it
// against the vector length the pass that just finished measured [06 §3.2].
func (c *autonomousScanCursor) beginTick(tick uint32, globalLive int) {
	if c.primed && c.tick == tick {
		return
	}
	if c.primed {
		for p := range c.cursor {
			c.count[p] = c.seen[p]
			if c.count[p] > 0 {
				c.cursor[p] = (c.cursor[p] + c.span) % c.count[p]
			} else {
				c.cursor[p] = 0
			}
		}
	}
	for p := range c.seen {
		c.seen[p] = 0
	}
	if globalLive < 0 {
		globalLive = 0
	}
	// `word / 30 + 1` [06 §3.2] — the dividend is truncated to sixteen bits
	// before the divide, which is retail's storage width for it [08 "Counters"].
	// Which word it is stands corrected at autonomousScanDivisor's TODO(T25):
	// retail divides the per-player unit LIMIT, not this live count.
	c.span = int(uint16(globalLive))/autonomousScanDivisor + 1
	c.tick = tick
	c.primed = true
}

// visits consumes one entry of the owner's vector and reports whether this
// tick's window covers it [06 §3.2]. It is called once for every unit the
// weapon phase steps, whether or not that unit passes the scan's other
// preconditions, because retail's cursor advances over vector entries and only
// then tests the entry it landed on.
func (c *autonomousScanCursor) visits(owner uint8) bool {
	p := int(owner)
	if p < 0 || p >= combatPlayerSlots {
		return false
	}
	i := c.seen[p]
	c.seen[p]++
	count := c.count[p]
	if count <= 0 || c.span >= count {
		// Length not yet measured, or the budget covers the whole vector: every
		// entry is visited, which is what a small player owns anyway.
		return true
	}
	rel := i - c.cursor[p]
	if rel < 0 {
		rel += count // the window wraps to the beginning of the vector
	}
	return rel < c.span
}

// autonomousScanVisitsUnit is the autonomous scan's per-unit admission
// [06 §3.2]: the visited unit must have a nonzero definition index, a
// remaining-build-fraction of exactly zero, the ARMED status bit set, and its
// two-bit stance field equal to the fire-at-will value — read in that order off
// the one runtime status word.
//
// The cursor is consumed first and unconditionally, because the budget is spent
// on vector entries rather than on units that pass.
func (s *Service) autonomousScanVisitsUnit(u *units.Unit, tick uint32, w *units.World) bool {
	if s == nil || u == nil {
		return false
	}
	s.scanCursor.beginTick(tick, w.Used())
	if !s.scanCursor.visits(u.Owner) {
		return false
	}
	if u.Def == nil {
		return false // "a nonzero definition index"
	}
	if u.Remaining != 0 {
		return false // "a remaining-build-fraction of exactly zero"
	}
	// The third clause, now named [06 §3.2 "The third clause is the armed
	// bit"]. The previous text here was a TODO(question) saying that "one high
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
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
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
		if u.Flags&units.ImmunityStatus == 0 && directlyVisibleAtRebuild(owner, u, seaLevel, vis, econ) {
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
// It answers in the section's order: the candidate's owner is the observer,
// accept; the cloak bit is set, reject; the sonar bit is clear and the probe is
// below sea level, reject; otherwise sample the definition's footprint corners
// against the observer's visibility state. Acquisition.directlyVisible is the
// same predicate for a candidate record the caller has already built, and both
// read the same three candidate fields, so the two cannot drift.
//
// The observer is a player slot, not a shooter: the registry is per side, and
// "in word-mask mode every probe tests the local player's bit, not the
// observer's" is the visibility service's own business [03 §3.2].
func directlyVisibleAtRebuild(owner uint8, cand *units.Unit, seaLevel numeric.Fixed, vis *visibility.Service, econ *economy.Service) bool {
	if cand == nil {
		return false
	}
	if cand.Owner == owner {
		return true // own units are never hidden from their owner [06 §3.1]
	}
	cloaked := isCloakedUnit(cand)
	if cloaked {
		return false // the cloak bit is set — reject [06 §3.1]
	}
	// The underwater exemption: an undetected submerged unit is invisible
	// regardless of line of sight. The sonar status bit reaches this build as
	// the 0x200 alias the candidate record carries [06 §3.1][03 §3.4].
	sonar := isAllied(owner, cand.Owner, econ)
	if !sonar && isUnderwaterUnit(cand, seaLevel) {
		return false
	}
	if vis == nil {
		// Hostile list entry is visibility-gated; with no service bound the
		// predicate fails closed rather than filing every hostile unit.
		return false
	}
	var status uint32
	if sonar {
		status |= 0x200
	}
	return vis.IsVisible(visibility.PlayerID(owner), visibility.Target{
		Owner:  visibility.PlayerID(cand.Owner),
		X:      cand.X,
		Y:      cand.Y,
		Z:      cand.Z,
		Hidden: cloaked,
		Status: status,
	})
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
	candidates := s.primaryCandidates(u, w, seaLevel, econ, catalog)
	acq := slotAcquisition(u, slot, idx, w, vis, terrain, simRNG, catalog, seaLevel, rangeLimit)
	// The registry's secondary-list gate and its secondary list [06 §3.1].
	// Both belong to the SCANNING PLAYER — the registry is per side and is the
	// same for every slot of every unit that player owns — so they are read
	// here, by owner, and never from the shooter's own definition.
	acq.HasUpgrade = s.targetingUpgradeGateFor(u.Owner)
	if acq.HasUpgrade {
		acq.Secondary = s.secondaryCandidates(u, w, seaLevel, econ, catalog)
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
func (s *Service) primaryCandidates(u *units.Unit, w *units.World, seaLevel numeric.Fixed, econ *economy.Service, catalog *content.Catalog) []Candidate {
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
		out = append(out, acquisitionCandidate(u, cand, seaLevel, econ, catalog))
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
func (s *Service) secondaryCandidates(u *units.Unit, w *units.World, seaLevel numeric.Fixed, econ *economy.Service, catalog *content.Catalog) []Candidate {
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
		out = append(out, acquisitionCandidate(u, cand, seaLevel, econ, catalog))
	}
	return out
}

// acquisitionCandidate builds the §3.1 candidate record for one unit as seen by
// one shooter. Factored out of acquireTargetForSlotRange so the reaction
// routine's per-slot offer tests exactly the same predicate the autonomous scan
// does [06 §3.1][06 R-WPN-04 §2 part 3].
func acquisitionCandidate(u *units.Unit, cand *units.Unit, seaLevel numeric.Fixed, econ *economy.Service, catalog *content.Catalog) Candidate {
	var catMask content.CategoryMask
	if cand.Def != nil {
		catMask = cand.Def.DefinitionMask()
	}
	candGate := gateEndForUnit(cand)
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
		Underwater:           isUnderwaterUnit(cand, seaLevel),
		UnderwaterSeen:       isAllied(u.Owner, cand.Owner, econ),
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
			var status uint32
			if c.UnderwaterSeen {
				status |= 0x200
			}
			t := visibility.Target{
				Owner:  visibility.PlayerID(candUnit.Owner),
				X:      c.X,
				Y:      c.Y,
				Z:      c.Z,
				Hidden: c.Cloaked,
				Status: status,
			}
			return vis.IsVisible(visibility.PlayerID(u.Owner), t)
		}
	}
	if weapon.Ballistic {
		acq.BallisticFeasible = func(c Candidate) bool {
			muzzle, muzzleOK := muzzleWorldPosResolved(u, slot.MuzzlePiece)
			if !muzzleOK {
				return false
			}
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
	c := acquisitionCandidate(u, cand, seaLevel, econ, catalog)
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
			return Vec3{}, false
		}
		// Composed piece coordinates are model space, which is mirrored in Z
		// against world space: the model pass narrows a model-relative vertex
		// as hi16(-vz) while the unit's own position enters the blit
		// unnegated [03 R-RAST-01 §2]. Every other consumer that turns a
		// composed offset into a world point already subtracts — the build
		// plate and nano emitter queries [03 §5.5], the hover hull, and the
		// selection quad. This site added it, so a muzzle authored forward of
		// the unit's origin was reflected to the same distance behind it,
		// which moves both the spawn point and the aim delta the pitch and
		// yaw solvers are handed [06 §3.3].
		return Vec3{X: u.X.Add(origin[0]), Y: u.Y.Add(origin[1]), Z: u.Z.Sub(origin[2])}, true
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

func tryFireForSlot(u *units.Unit, slot *units.Slot, idx int, tick uint32, terrain *world.Terrain, simRNG *rng.Simulation, svc *Service, w *units.World) bool {
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
	origin, ok := muzzleWorldPosResolved(u, slot.MuzzlePiece)
	if !ok {
		return false
	}
	targetWorld := func(h pool.Handle) (Vec3, bool) {
		if tu := w.Unit(h); tu != nil {
			return Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}, true
		}
		return Vec3{}, false
	}
	muzzleWorld := func(piece int32) (Vec3, bool) {
		return muzzleWorldPosResolved(u, piece)
	}
	muzzlePieceFn := func(slotIdx int) int32 {
		if slot.MuzzlePiece >= 0 {
			return slot.MuzzlePiece
		}
		return -1
	}
	scriptAdapter := &fireScriptAdapter{bridge: svc.callbackBridgeForUnit(u), unit: u}
	// [06 §13.2] wire weapon-start events to same service sink installed at composition [06 §4.1] C2
	fireEvents := &combatFireEvents{svc: svc, tick: tick, shooter: u.Handle, pos: origin}
	ports := FirePorts{
		ShooterSide: uint8(u.Owner),
		// The shooter itself, so the fill can run its real-shooter branch in
		// place: the reference beside the side byte and the `tick + 600`
		// reveal stamp, both ahead of the start sound [06 §4.1]. This is the
		// only site that binds a real shooter to a fresh projectile record.
		Shooter:     u,
		Origin:      origin,
		MuzzlePiece: muzzlePieceFn,
		MuzzleWorld: muzzleWorld,
		TargetWorld: targetWorld,
		Gravity:     gravity,
		Script:      scriptAdapter,
		Events:      fireEvents,
		RNG:         simRNG,
		Spy:         nil,
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
	_, ok = TryFire(svc, &cSlot, idx, tgt, tick, ports)
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

func (s *Service) TickProjectiles(tick uint32, w *units.World, terrain *world.Terrain, windState *world.Wind, featSvc *features.Service, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil {
		return
	}
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
	var weaponByID map[int32]*content.WeaponDef
	if catalog != nil {
		if idx := catalog.WeaponIndex(); idx != nil {
			weaponByID = idx
		} else if catalog.Weapons != nil {
			// Fallback for fixtures without compiled index. [02 §5 R-CONTENT-02]:
			// the same-ID merge is last-wins — the record parser unconditionally
			// stores every field it parses, so a later section with the same ID
			// overwrites the earlier one. Keys are sorted for a deterministic
			// order (I1); the last same-ID weapon in that order wins.
			weaponByID = make(map[int32]*content.WeaponDef, len(catalog.Weapons))
			keys := make([]string, 0, len(catalog.Weapons))
			for k := range catalog.Weapons {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				wd := catalog.Weapons[k]
				if wd == nil {
					continue
				}
				weaponByID[wd.ID] = wd
			}
		}
	}
	s.AdvanceBursts(tick, simRNG, weaponByID, muzzleForBurst)
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
	entry := s.Count()
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		p := &s.Records[i]
		isDead := s.Slots.IsDead(h)
		if p.BurstRemaining > 0 {
			continue
		}
		var weapon *content.WeaponDef
		if catalog != nil {
			if w, ok := catalog.WeaponByID(p.WeaponID); ok {
				weapon = w
			}
		}
		if weapon == nil {
			if !isDead {
				s.MarkDead(h)
			}
			continue
		}
		if isDead {
			continue
		}
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
			res = AdvanceSelfProp(p, weapon, tick, gravity, seaLevel)
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
			if !weapon.BurnBlow && tick >= p.ExpiryTick {
				s.emitEvent(Event{Kind: EventTrailSmoke, Tick: tick, Source: p.Shooter, Target: h, Position: p.Pos})
			}
			s.MarkDead(h)
			continue
		}
		if res == AdvanceImpact {
			handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, false)
			s.MarkDead(h)
			continue
		}
		if res == AdvancePhaseTransition {
			continue
		}
		hitUnit, hitFeature, isWaterTerrain, isOffMap, bounce := checkCollision(p, weapon, w, terrain, featSvc)
		if bounce {
			continue
		}
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
		} else {
			if terrain != nil {
				cellX := world.WorldToCell(p.Pos.X)
				cellZ := world.WorldToCell(p.Pos.Z)
				if cellX >= 0 && cellZ >= 0 && cellX < terrain.CellW && cellZ < terrain.CellH {
					th := terrain.HeightAt(p.Pos.X, p.Pos.Z)
					if th.Raw() != -1 && p.Pos.Y.Raw() < th.Raw() {
						needImpact = true
					}
				}
			}
		}
		if needImpact {
			isWater := isWaterTerrain && directTarget == 0
			handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, isWater)
			if NoExplodeRetirement(weapon.NoExplode, true, isOffMap, false) {
				s.MarkDead(h)
			}
		}
		// Trail puffs: the smoke-trail flag plus smoke delay, ALIVE records
		// only, never burst parents, past the next-trail deadline. The
		// deadline update is ADDITIVE — the smoke delay is added — so a
		// zero delay emits on every eligible tick after the first
		// [06 §13.2][R-STRIP-01 §1 strip 9, the projectile phase's
		// trail-window branch]. A visit that just impacted marked the
		// record dead, which skips the puff.
		if weapon.SmokeTrail && p.BurstRemaining == 0 && !s.Slots.IsDead(h) && tick >= p.SmokeDeadline {
			s.emitEvent(Event{Kind: EventTrailSmoke, Tick: tick, Source: p.Shooter, Target: h, Position: p.Pos})
			p.SmokeDeadline += uint32(weapon.SmokeDelay)
		}
		_ = directTarget
		_ = hitFeature
	}
	s.Compact(nil)
}

func checkCollision(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service) (hitUnit pool.Handle, hitFeature *features.Instance, isWaterTerrain bool, isOffMap bool, bounce bool) {
	if terrain != nil {
		cx := world.WorldToCell(p.Pos.X)
		cz := world.WorldToCell(p.Pos.Z)
		if cx < 0 || cz < 0 || cx >= terrain.CellW || cz >= terrain.CellH {
			isOffMap = true
			return
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
		// Step 5 of the ladder, the units-only early return, has no reader
		// here: `WeaponDef.UnitsOnly` is compiled but nothing consults it, so a
		// `unitsonly` weapon that misses both unit slots still falls through to
		// feature, terrain, bounce and water below, where [06 §8.1] would have
		// it return. The gate only becomes expressible now that the unit slots
		// precede those steps; wiring it needs a "kept flying" result the
		// caller's own terrain fallback also honours, which is a change to
		// TickProjectiles' contract rather than to this ladder.
		cache := [2]int32{p.CacheCellX, p.CacheCellZ}
		suppressed := FeatureCacheSuppressed(&cache, int32(cx), int32(cz))
		p.CacheCellX, p.CacheCellZ = cache[0], cache[1]
		if suppressed {
		} else {
			if featSvc != nil {
				if inst := featSvc.InstanceAt(int(cx), int(cz)); inst != nil && inst.Def != nil {
					top := inst.Y.Add(numeric.Fixed(int64(16) * 65536))
					if p.Pos.Y.Raw() < top.Raw() {
						hitFeature = inst
						return
					}
				}
			} else if terrain != nil {
				if featIdx := terrain.Plot[int(cz)*int(terrain.CellW)+int(cx)].Feature(); featIdx < 0xFFFF && featIdx != 0xFFFE {
					base := terrain.CoarseHeightAt(cx, cz)
					top := base.Add(numeric.Fixed(int64(16) * 65536))
					if p.Pos.Y.Raw() < top.Raw() {
						hitFeature = &features.Instance{CX: int(cx), CZ: int(cz)}
						return
					}
				}
			}
		}
		th := terrain.HeightAt(p.Pos.X, p.Pos.Z)
		if th.Raw() != -1 && p.Pos.Y.Raw() < th.Raw() {
			if weapon != nil && weapon.GroundBounce {
				p.Velocity.Y = numeric.Fixed(int64(-(p.Velocity.Y.Raw() >> 2)))
				bounce = true
				return
			}
			return
		}
		sea := terrain.SeaLevelWorld()
		if p.Pos.Y.Raw() < sea.Raw() {
			if weapon != nil && !weapon.WaterWeapon {
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

func handleProjectileImpact(s *Service, h pool.Handle, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, wind Vec3, simRNG *rng.Simulation, isWaterTerrain bool) {
	if weapon == nil {
		return
	}
	hasDirectTarget := p.TargetUnit != 0
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
	s.emitEvent(Event{Kind: EventProjectileImpact, Tick: tick, Source: p.Shooter, Target: p.TargetUnit, Position: p.Pos})
	applyProjectileDamage(s, p, weapon, w, terrain, featSvc, econ, catalog, tick, simRNG, hasDirectTarget, isWaterTerrain, h)
	_ = wind
}

func applyProjectileDamage(service *Service, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, simRNG *rng.Simulation, hasDirectTarget bool, isWaterTerrain bool, handle pool.Handle) {
	if w == nil || weapon == nil {
		return
	}
	// Gate 1 of the central impact routine [06 §9.1][06 R-DMG-01 §9]: the
	// damage gate is a property of the PROJECTILE's own side, not of the
	// victim, and it skips damage ONLY for an occupied row whose control byte
	// is 3. An unoccupied row passes — including the never-occupied eleventh
	// row that a null-shooter record's neutral side byte selects, which is why
	// a meteor or a death explosion damages every side and credits nobody.
	// When it does skip, the camera shake, the impact sound and the impact art
	// of [06 §9.1] steps 4 and 5 have already been emitted by the caller and
	// are unaffected.
	if !service.DamageRoutingAdmitted(p.ShooterSide) {
		return
	}
	if hasDirectTarget && weapon.AreaOfEffect <= 16 && p.TargetUnit != 0 {
		victim := w.Unit(p.TargetUnit)
		if victim != nil {
			applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick)
		}
		if p.Shooter == 0 {
			return
		}
		return
	}
	radius := BlastRadius(weapon.AreaOfEffect)
	if radius <= 0 {
		if hasDirectTarget && p.TargetUnit != 0 {
			if victim := w.Unit(p.TargetUnit); victim != nil {
				applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick)
			}
		}
		return
	}
	// Shared area splash: EnumerateArea→DistanceToBox→Falloff→ApplyDamage [06 §9.3]
	// Extracted to ExplodeWeaponAt for death DoExplosion reuse [06 §12.1] C22–C25 (I1, I2)
	service.ExplodeWeaponAt(w, terrain, weapon, p.Pos, p.Shooter, tick)
	_ = featSvc
	_ = econ
	_ = catalog
	_ = simRNG
	_ = isWaterTerrain
	_ = handle
	return
}

// ExplodeWeaponAt is the shared authoritative area-damage entry point for
// projectile splash and death explosions [06 §9.3][06 §12.1] C22–C25.
// It performs EnumerateArea → DistanceToBox (fixed-point box, Y+16) → Falloff (float32) →
// SelectBaseDamage → ComputeScaledAmount → ApplyDamage→Destroy exactly as TickProjectiles
// does, deterministically (pool asc via Iter, I1) and without new float64 sites
// (I2 allowlist: Falloff float32 only). Collect-then-apply avoids double-processing
// victims when nested deaths chain-explode [01 §4.4].
func (s *Service) ExplodeWeaponAt(w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, tick uint32) {
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
	EnumerateArea(impact, radius, mapW, mapH, func(cx, cz int32) {
		for _, u := range w.Iter() { // deterministic pool asc (I1)
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			// A unit candidate must be nonzero and must NOT be the record's
			// shooter: the shooter is unconditionally excluded from every
			// blast, and that exclusion is the whole of retail's self-damage
			// policy [06 §9.3][06 R-DMG-01 §9]. There is no `noselfdamage` key
			// and no owner or alliance test here — a shooter's own OTHER units
			// take full damage, and the shooter itself still takes full damage
			// from a different record's blast.
			//
			// A null shooter matches nobody [06 R-DMG-01 §9], which is what
			// makes a meteor or a death explosion damage every side alike.
			//
			// Before this reader existed the shooter enumerated itself, took
			// its own splash, and had its last-damage provenance overwritten
			// with its own owner below — so a commander that died inside its
			// own blast credited the kill to itself instead of to the player
			// whose shot actually killed it [06 §12.1].
			if shooter != 0 && u.Handle == shooter {
				continue
			}
			ucx := world.WorldToCell(u.X)
			ucz := world.WorldToCell(u.Z)
			if ucx != cx || ucz != cz {
				continue
			}
			footX := int32(1)
			footZ := int32(1)
			if u.Def != nil {
				if u.Def.FootprintX > 0 {
					footX = u.Def.FootprintX
				}
				if u.Def.FootprintZ > 0 {
					footZ = u.Def.FootprintZ
				}
			}
			halfX := numeric.Fixed(int64(footX) * 1048576 / 2)
			halfZ := numeric.Fixed(int64(footZ) * 1048576 / 2)
			min := Vec3{X: u.X.Sub(halfX), Y: u.Y, Z: u.Z.Sub(halfZ)}
			max := Vec3{X: u.X.Add(halfX), Y: u.Y.Add(numeric.Fixed(int64(16) * 65536)), Z: u.Z.Add(halfZ)}
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
		p := &Projectile{Pos: impact, Shooter: shooter} // synthetic projectile for damage pipeline [06 §9.1]
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

func applyDamageToUnit(service *Service, victim *units.Unit, p *Projectile, weapon *content.WeaponDef, falloff float32, distance int32, w *units.World, tick uint32) {
	if victim == nil || weapon == nil || w == nil {
		return
	}
	shooter := w.Unit(p.Shooter)
	// [06 §9.1] step 4 runs three things in this order for every accepted
	// non-heal packet: the damage flash, then the reaction routine, then the
	// kind byte and the attacker fields. Keeping that order is what lets the
	// reaction's parts 3 and 4 read the PREVIOUS packet's provenance
	// [06 R-WPN-04 §2].
	//
	// The flash is one byte of the unit record written to 240, decremented as a
	// signed byte once per unit visit, whose only reader is the minimap
	// unit-dot pass: the dot is not drawn while it is nonzero, so a unit under
	// fire vanishes from the minimap for sixteen ticks after each hit
	// [06 R-WPN-04 §2]. It is presentation only, so it leaves here as an
	// ordered event on the combat sink rather than as authoritative state.
	service.emitEvent(Event{Kind: EventDamageFlash, Tick: tick, Source: p.Shooter, Target: victim.Handle, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}, Duration: DamageFlashTicks})
	service.ReactToDamage(w, victim, shooter, tick)
	// The provenance stamp of [06 §9.1] step 4 and [06 §12.1). The kind byte is
	// recorded on the victim UNCONDITIONALLY — "record the kind byte on the
	// victim; WHEN THE ATTACKER IS NONZERO store the attacker pointer and its
	// side snapshot" is two clauses, not one. Tying both to shooter presence
	// meant a unit killed outright by a null-shooter blast (a death explosion,
	// a meteor) reached the death finalizer with cause 0, so the credit switch
	// of [06 §12.1] filed neither the kill it must not file NOR the victim loss
	// it must.
	//
	// The credited side is always the damage-time snapshot — what the death
	// packet's attacker-side field and the session's kill credit read back
	// [06 §12.1]. For a real shooter it is the attacker unit's own owner byte,
	// not the record's side byte: the side byte is the damage gate's operand
	// and the friendly/enemy sum classifier [06 §9.1][06 §9.3].
	//
	// A shooter never stamps ITSELF here, because [06 §9.3] excludes the
	// record's shooter from its own blast enumeration before this site is
	// reached. Any later known weapon intake clears a stale reclaim bite
	// marker, including paralyzer packets that do not reduce health [06 §9.1].
	victim.LastDamageCause = uint8(CauseOrdinary)
	// The recorded-attacker link of [04 R-UNIT-06 §5 part 1], which §5 gives as
	// an implementation rule: on every damage application that passes the
	// dispatcher's gates, when the packet is not a heal and the attacker id is
	// nonzero, store the attacker and its owner byte — after the reaction
	// routine and before the paralyze and health arms, which is exactly here.
	//
	// The id is stored, not the pointer, and it is stored whether or not the
	// slot it names is still live: §5's writer table admits "the pool slot that
	// id names, whether or not it is live", and there is no per-tick clear and
	// no clear when the attacker dies. Readers check liveness themselves.
	//
	// This is the producer WU-19-35 recorded as missing for the guard's legs 1
	// and 2 [04 R-UNIT-06 §1]: the link is what the guard attacks and what it
	// points its free weapon slots at, so without it a guard never joins its
	// ward's fight. The side snapshot beside it was already written here.
	if p.Shooter != 0 && victim.Alive && !victim.Dying {
		victim.EngagementTarget = p.Shooter
	}
	if shooter != nil {
		victim.LastDamageSide = shooter.Owner
	} else {
		// A null-shooter record's side byte IS the neutral value 10 — the
		// never-occupied eleventh row [06 §9.1][06 §6.5] — and cause 1's full
		// credit path then files the victim's loss and credits nobody, because
		// every kill clause of [06 §12.1] requires an attacker side that is not
		// 10. Retail reaches the same state by leaving the field alone and
		// relying on the spawn seed of neutral 10 [06 R-WPN-04 §2]; this build's
		// unit records seed it to zero (internal/units, outside this unit's
		// files), which would credit player 0 for every meteor kill, so the
		// record's own side byte is written here instead. The two differ only
		// for a victim that had already taken a real hit before the
		// null-shooter one, where retail keeps the stale snapshot.
		victim.LastDamageSide = NeutralSide
	}
	if weapon.Paralyzer {
		// Stun eligibility is three tests in this order [06 §10]:
		//
		//  1. the alive bit set and the death latch clear, re-tested here;
		//  2. the victim's player record exists and its controller type is 1 or
		//     2 — the same predicate the death latch admits [06 R-DMG-01 §8];
		//  3. the definition does not carry `immunetoparalyzer`.
		//
		// A victim failing any of the three keeps the preliminary side effects
		// above and receives no stun task and no health damage.
		if !victim.Alive || victim.Dying {
			return
		}
		if !service.DeathLatchAdmitted(victim.Owner) {
			return
		}
		if victim.Def != nil && victim.Def.ImmuneToParalyzer {
			return
		}
		base := SelectBaseDamage(weapon, victim.Def.UnitName)
		attackerKills := int32(0)
		if shooter != nil {
			attackerKills = shooter.Kills
		}
		// "The incoming amount is scaled exactly as ordinary damage (§9.2),
		// including the armored-state modifier and defender veterancy, before
		// it becomes the stun credit" [06 §10]. The credit is the damage number
		// the weapon would have dealt, reinterpreted as ticks; only healing
		// bypasses those scales, and this branch never subtracts health on any
		// path. The armored pair used to be passed as (false, 0) here, which
		// gave an armored victim the unarmored stun.
		isArmored := UnitArmored(victim)
		damageMod := int32(65536)
		if victim.Def != nil {
			damageMod = victim.Def.DamageModifier
		}
		// ComputeScaledAmount already packs to the packet's unsigned 16-bit
		// amount field [06 §9.2] step 7, which is the width [06 §10] adds into
		// the task's 32-bit credit. A zero credit is a real value, not a floor:
		// the task's first visit sees credit 0, lowers the mark and completes.
		// The `if dur == 0 { dur = 1 }` that stood here was invented.
		credit := uint32(ComputeScaledAmount(base, falloff, attackerKills, victim.Kills, isArmored, damageMod, false, false, false))
		pushParalyzeTask(victim, credit, tick)
		return
	}
	base := SelectBaseDamage(weapon, victim.Def.UnitName)
	attackerKills := int32(0)
	if shooter := w.Unit(p.Shooter); shooter != nil {
		attackerKills = shooter.Kills
	}
	// The armor gate reads bit 1 of the victim's first runtime state byte — the
	// COB `set ARMORED` posture — and nothing else [06 R-DMG-01 §8]. The FBI
	// `armoredstate` key is parsed into a definition flag that retail never
	// reads; ORing it in here armored every unit that authored it.
	isArmored := UnitArmored(victim)
	damageMod := int32(65536)
	if victim.Def != nil {
		damageMod = victim.Def.DamageModifier
	}
	amt := ComputeScaledAmount(base, falloff, attackerKills, victim.Kills, isArmored, damageMod, false, false, false)
	newHealth := ApplyDamage(victim.Health, amt)
	victim.Health = newHealth
	if newHealth <= 0 && service.DeathLatchAdmitted(victim.Owner) {
		// Gate 2 [06 §9.1 step 6][06 R-DMG-01 §8]: only a victim owned by a
		// control byte of 1 or 2 latches death, and the modular health value
		// is PRESERVED, not clamped.
		//
		// The death handler's row in [04 R-UNIT-06 §5]'s writer table: the
		// recorded-attacker link takes the DEATH packet's attacker, always.
		// This packet is that packet, so the link ends as this shooter — and
		// as null for a shooterless killing blow (a meteor, the water gate),
		// which the dispatcher's own write above cannot express because its
		// row is conditional on a nonzero attacker id.
		w.DestroyBy(victim.Handle, units.DeathKilled, p.Shooter)
		if service != nil {
			if service.deathNotified == nil {
				service.deathNotified = make(map[pool.Handle]*units.Unit)
			}
			if service.deathNotified[victim.Handle] != victim {
				service.deathNotified[victim.Handle] = victim
				service.emitEvent(Event{Kind: EventUnitKilled, Tick: tick, Source: p.Shooter, Target: victim.Handle, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}})
			}
		}
	} else {
		if newHealth <= 0 {
			// No record, or a remote peer: health clamps to zero, the unit does
			// NOT die through this path, and the callbacks still run
			// [06 §9.1 step 6][06 R-DMG-01 §8].
			victim.Health = 0
		}
		dir := hitDirectionByte(p, victim)
		takeArg := cob.HealthPercent(victim.Health, victim.MaxHealth)
		if bridge := service.callbackBridgeForUnit(victim); bridge != nil {
			// The two damage callbacks are independent deferred starts and retain
			// their exact order after the HitByWeapon direction conversion
			// [04 §5.1]. The normal VM drain belongs to the session window.
			bridge.HitByWeapon(dir)
			bridge.TakeDamage(takeArg)
		}
	}
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
