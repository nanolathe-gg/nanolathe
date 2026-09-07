package session

import (
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
)

// stepAuthoritativePhases runs only the twelve authoritative phase calls for
// callers that already own the tick number. Sharing and result handling are
// explicit in stepOneSubTick below, while publication remains a separate
// boundary after every completed sub-tick [DET-02][01 §4.4][I6].
func (s *Session) stepAuthoritativePhases(tick uint32) {
	if s == nil {
		return
	}
	// The phase order below is the authoritative retail sequence [01 §4.4].
	// Keep these calls in this order: their pool visibility, side effects, and RNG
	// draw order are observable. Each phase carries a status: implemented,
	// proven no-op for this state, or research-blocked with the marker naming
	// what is unknown. The global tick is already incremented before phase 1
	// via BeginSubTick [01 §4.4].
	s.phaseNetwork(tick)          // 1  implemented (single-player command drain)
	s.phaseUnits(tick)            // 2  implemented
	s.phaseProjectiles(tick)      // 3  implemented
	s.phaseEffects(tick)          // 4  implemented
	s.phaseOrders(tick)           // 5  implemented (path scheduler, per-player work, visibility stamps [R-CORE-01 §4.4.1])
	s.phaseFeatureLifecycle(tick) // 6  implemented
	s.phaseSequences(tick)        // 7  model-texture playback metadata [R-CRD-005 §1]
	s.phaseWind(tick)             // 8  implemented — complete scheduled redraw [01 §7.3] DET-03
	s.phaseMeteorShower(tick)     // 9  implemented — meteor shower [R-CORE-01 §4.4.1]
	s.phaseCameraShake(tick)      // 10 implemented — shake driver [R-CORE-01 §4.4.1] DET-04
	s.phaseObjectSweeps(tick)     // 11 implemented — ten effect-strip sweeps [R-CORE-01 §4.4.1][R-STRIP-01]
	s.phaseCadenceFlip(tick)      // 12 radar blink cadence [R-CORE-03][CRD-008]

	// The registry ends at phase 12. Per-sub-tick work follows in
	// stepOneSubTick so the phase graph cannot accidentally absorb outer work.
	// [01 §4.4]
}

// stepOneSubTick runs one complete authoritative sub-tick: phases 1..12,
// sharing/flush, and per-sub-tick result handling. Publication is owned by
// Session.Step immediately after this boundary [01 §4.4][03 §2.4][I6].
func (s *Session) stepOneSubTick(tick uint32) {
	if s == nil {
		return
	}
	s.setParityTraceTick(tick)
	s.stepAuthoritativePhases(tick)

	// Sharing is the transport tail after phase 12 [01 §4.4].
	s.stepSharingPhase(tick)

	// Result/mission evaluation remains a per-subtick session hook. It is not
	// part of the once-per-pump executor tail [01 §4.4].
	s.stepResultPhase(tick)
}

// Phase registry — one named method per phase 1..12 called in §4.4 order
// [01 §4.4] DET-02. There is exactly one call site per phase and one
// implementation per phase; legacy wrappers delegate to these methods.

func (s *Session) phaseNetwork(tick uint32) {
	s.applyHumanCommands(tick)
	s.recordPhase("phase1-network", tick)
}

func (s *Session) phaseUnits(tick uint32) {
	s.stepUnitPhase(tick)
	s.recordPhase("phase2-units", tick)
}

func (s *Session) phaseProjectiles(tick uint32) {
	s.stepProjectilePhase(tick)
	s.recordPhase("phase3-projectiles", tick)
}

func (s *Session) phaseEffects(tick uint32) {
	s.stepEffectPhase(tick)
	s.recordPhase("phase4-effects", tick)
}

// phaseOrders is phase 5 [01 §4.4][R-CORE-01 §4.4.1]: the path scheduler runs
// first, then per player 0..9 ascending the per-player orders/work, then that
// player's unit slice is swept stamping visibility coverage per in-game unit
// (dirty-checked) — the visibility publication seam lives INSIDE phase 5
// [R-CORE-01 §4.4.1] DET-06. The sensor/deadline pass runs inside the LOCAL
// viewing player's iteration, after that player's stamp sweep [R-SENSOR-01].
func (s *Session) phaseOrders(tick uint32) {
	s.stepPlayerPhase(tick)
	s.recordPhase("phase5-orders", tick)
}

func (s *Session) phaseFeatureLifecycle(tick uint32) {
	s.stepFeatureLifecyclePhase(tick)
	s.recordPhase("phase6-feature", tick)
}

// phaseSequences is phase 7 [01 §4.4]. The session owns only this narrow
// cadence seam; the client owns the registered model-texture cursors and
// advances their presentation metadata. Feature, projectile, effect, and UI
// cursor families use their own established paths [R-CRD-005 §1][I6].
func (s *Session) phaseSequences(tick uint32) {
	if s.phase7 != nil {
		s.phase7.StepPhase7()
	}
	s.recordPhase("phase7-sequences", tick)
}

// phaseWind is phase 8 [01 §4.4][01 §7.3] — the COMPLETE scheduled wind redraw
// DET-03: when due (strict gate), one CRT interval draw, then sim strength,
// then sim heading only when strength is nonzero, then vectors, scalar, and
// change flag. Battle entry zeroes the deadline and consumes no draws; the
// first chain fires here at tick 1 [R-CORE-02].
func (s *Session) phaseWind(tick uint32) {
	if s.Wind != nil {
		s.Wind.Jitter(tick, s.CrtRNG(), s.SimRNG())
	}
	s.recordPhase("phase8-wind", tick)
}

// phaseMeteorShower is phase 9 [01 §4.4][R-CORE-01 §4.4.1] — the meteor-shower
// strike scheduler (the old "9b" meteor work and the earlier "wind-field"
// label both resolve to this phase). Four CRT scheduling draws on every due
// evaluation even when disabled, then per hit a radius and an angle draw;
// zero sim draws [06 §6.5].
func (s *Session) phaseMeteorShower(tick uint32) {
	if !s.Meteor.Initialized {
		s.initMeteor()
	}
	s.tickMeteor(tick)
	s.recordPhase("phase9-meteor", tick)
}

// phaseCameraShake is phase 10 [01 §4.4][R-CORE-01 §4.4.1] — camera/scroll
// position update plus the authoritative shake driver DET-04. The scroll step
// toward the presentation camera target stays presentation-owned; the shake
// driver advances here with exactly two CRT draws per active tick and
// publishes the offset on the committed frame. Requests arrive during this
// tick from the impact dispatcher via the session's shake state.
func (s *Session) phaseCameraShake(tick uint32) {
	s.tickShake(tick)
	s.recordPhase("phase10-shake", tick)
}

// phaseObjectSweeps is phase 11 [01 §4.4][R-CORE-01 §4.4.1] — the ten
// effect-strip update sweeps over the strip table allocated at battle entry
// (see strips.go). Per strip ascending and per object in insertion order, a
// removal verdict is evaluated BEFORE the update virtual — a positive verdict
// destroys (destructor with argument 1) and removes the object with stable
// left compaction, a zero verdict runs the update work and keeps the object;
// a terminal condition created during an update is noticed only on the next
// invocation. Empty strips touch no globals and the dispatcher consumes no
// random draws; object-internal draws all come from the CRT presentation
// stream [R-STRIP-01 §2][R-STRIP-01 §3]. The simulation Park–Miller stream is
// never touched by phase 11.
func (s *Session) phaseObjectSweeps(tick uint32) {
	if s.strips != nil && s.strips.anyObjects() {
		s.strips.sweep(tick, s)
	}
	s.recordPhase("phase11-objects", tick)
}

// phaseCadenceFlip is phase 12 [01 §4.4]. The countdown is initialized at
// battle entry and is intentionally independent of the global tick label.
// A positive countdown decrements; zero reloads to seven and toggles only the
// phase bit. This state is transient presentation cadence and consumes no RNG
// [R-CORE-03][CRD-008].
func (s *Session) phaseCadenceFlip(tick uint32) {
	if s.radarBlinkCountdown > 0 {
		s.radarBlinkCountdown--
	} else {
		s.radarBlinkCountdown = 7
		s.radarBlinkPhase ^= 1
	}
	s.recordPhase("phase12-cadence", tick)
}

// stepUnitPhase is phase 2 of the authoritative tick [01 §4.4].
func (s *Session) finalizePhase2Death(h pool.Handle, tick uint32) {
	if s == nil || s.Units == nil || !s.Units.NeedsDeathFinalization(h) {
		return
	}
	// The owner is read BEFORE the finalizer runs: FinalizeDeath frees the pool
	// slot and drops the record, so the victim's owner byte is unreachable
	// afterwards. It is only used to address the player row whose live count
	// the finalizer decrements [08 R-CAMP-01 §9].
	owner, liveBefore := -1, 0
	if u := s.Units.Unit(h); u != nil {
		owner = int(u.Owner)
		liveBefore = s.Units.LiveCountForPlayer(owner)
	}
	if result := s.Units.FinalizeDeath(h, tick); !result.Freed {
		return
	}
	// The elimination branch of the central death handler: the finalizer has
	// just decremented the owner's live-unit count, and the handler's last act
	// is to test it against zero and, in a skirmish, spend one CRT draw on the
	// announcement line [08 R-CAMP-01 §9][01 §7.5]. It runs here — inside phase
	// 2, at the death — because the draw's position in the CRT stream is the
	// contract, and it runs BEFORE the commander-rule transitions below because
	// retail reaches the commander game-over path from the death preamble only
	// after the central handler has returned [06 §12.1].
	// "Reached zero" is the decrement's own arrival at zero, which is why the
	// count is sampled on both sides of the finalizer: a row already at zero
	// takes no decrement there and must not announce a second time.
	if owner >= 0 && liveBefore > 0 && s.Units.LiveCountForPlayer(owner) == 0 {
		s.announceElimination(owner, tick)
	}
	// Commander rule transitions run only after FinalizeDeath has filed the
	// owner's live-count decrement and released the slot [08 R-SKIR-01 §3].
	s.processPendingCommanderDeaths(tick)
	// The slot finalizer is the sole gameplay teardown point for a running
	// battle: remove occupancy/path state only after the unit has been freed.
	// [01 §4.4][04 §2.4]
	if s.Movement != nil {
		s.Movement.ForgetUnit(h)
		if s.Movement.Scheduler != nil {
			s.Movement.CancelPathRequest(h)
		}
	}
}

// sweepPlayerGate is "The player gate" of [04 R-MOV-03 §1] as corrected by
// [04 R-MOV-03 §10], read on the row that owns the unit being visited. The
// sweep visits player slots 0..9 in order and processes a slot only when its
// leading occupancy word is nonzero, its control byte is 1, 2 or 3, and its
// ally-group byte is not 10. Inside a visit a second, narrower test
// admits one block — retail's water damage, self-repair, the two order pumps,
// the mover tick and the post-move correction — for an owner of control byte
// 1 or 2 only; it is re-evaluated per unit from the OWNER record, which is why
// it is a function of the unit's owner byte rather than of the slice being
// swept.
//
// visit is the outer gate, work the inner one. The control byte is the player
// row's, never the unit's own owner byte — that byte is the slot number
// [06 R-DMG-01 §8]. In single player every occupied row is 1 or 2, so both
// gates pass for every live unit; the byte is still read rather than assumed,
// which is exactly what [06 R-DMG-01 §8] requires of an implementation.
//
// A row past the ten records, or one whose occupancy word is zero, is not swept
// at all.
//
// There is NO elimination term. [04 R-MOV-03 §1]'s third clause named the wrong
// byte and invented a state; [04 R-MOV-03 §10] establishes that the byte the
// sweep loads is the row's ally-group byte — the byte the row constructor seeds
// with 10 ([05 "Player slot"]) and the byte the alliance rows are indexed by
// ([05 R-SHARE-01 §1]) — tested against 10. A row whose player has lost every
// unit is still swept, and trivially owns nothing to visit; the 10 test can
// only exclude a row that was never seated. The derived
// economy.PlayerEliminated term this gate used to carry is removed: it was the
// invented state.
//
// Nanolathe keeps no separate per-slot ally-group byte on the player row —
// its alliance rows are indexed by the slot number itself
// (economy.Service.DeclaresAlliance), so the byte's value here IS `owner`,
// which for rows 0..9 is never 10. That is retail's arithmetic, not an
// approximation: the seat-setup writer stores the slot's own index into the
// byte in every session kind, the only other writer (the multiplayer
// battleroom's renumbering pass) stores the row index or 10, and no
// single-player path can make the byte differ from the row index
// [04 R-MOV-03 §11] (Established; this used to be the Supported inference of
// [04 R-MOV-03 §10]). The clause is inert in any battle, as §10 says.
//
// A session with no economy service at all has no player table to read, which
// is not a state retail can be in: the battle block allocates the table before
// any unit exists. That is an unwired composition rather than a control byte,
// so it opens both gates instead of silently emptying the sweep.
// neverSeatedAllyGroup is the value the player-row constructor seeds the
// ally-group byte with, and which the movement sweep's third clause excludes
// [04 R-MOV-03 §10][05 "Player slot"].
const neverSeatedAllyGroup uint8 = 10

func (s *Session) sweepPlayerGate(owner uint8) (visit, work bool) {
	if s == nil {
		return false, false
	}
	if s.Econ == nil {
		return true, true
	}
	if int(owner) >= len(s.Econ.Players) {
		return false, false
	}
	p := &s.Econ.Players[owner]
	if !p.Exists {
		return false, false // the leading occupancy word [04 R-MOV-03 §10]
	}
	if owner == neverSeatedAllyGroup {
		return false, false // the ally-group byte is not 10 [04 R-MOV-03 §10]
	}
	switch p.ControllerState {
	case combat.ControlByteHuman, combat.ControlByteComputer:
		return true, true
	case combat.ControlByteRemote:
		return true, false
	}
	return false, false
}

// stepWaterDamage is the first act of step 9 of the per-unit visit
// [04 R-MOV-03 §1] — the mission water-damage packet of [04 §9.2].
//
// The caller owns the block's control-byte 1-or-2 gate (`work`, which reads the
// OWNER row's byte, [06 R-DMG-01 §8]). Every other gate is here, in the order
// the research states them:
//
//   - the step's own `tick mod 30 == 0` cadence — the same number as step 8's
//     health-percentage roll but a separate test;
//   - both mission words nonzero. Ten of the 275 stock OTAs author
//     `waterdoesdamage=1`; most of the rest author a `waterdamage` amount with
//     the flag at zero, which is inert;
//   - the unit's signed 16-bit height word at or below the map's sea-level
//     byte, and the `canhover` exemption. `floater` and `amphibious` are NOT
//     immunity at this call site [04 §9.2]. Both live in
//     combat.IsWaterDamageEligible.
//
// The amount is the shared funnel's, so it is combat's: falloff 1.0 (the stored
// blast distance is zero, so no AOE falloff), the victim's armored posture bit,
// its definition damage modifier, and the defender veterancy reduction — a
// veteran drowns SLOWER [04 §9.2][06 §9.2]. The packet is kind 0xB, which emits
// neither `HitByWeapon` nor `TakeDamage`, so no COB callback runs here; a lethal
// result sets the ordinary death-pending state that step 10 finalizes at the end
// of this same visit.
//
// This composes the per-unit act from combat's exported predicates rather than
// calling combat.TickWaterDamage, which states the same contract as its own
// whole-world sweep over players 0..9. Retail applies water damage inside the
// visit, between the unit's script drain and its order pumps — running a sweep
// per visit would be both quadratic and in the wrong order.
//
// The act that follows it in the same block is stepHealTimeSelfRepair below
// [04 R-SPEC-01 §4].
func (s *Session) stepWaterDamage(u *units.Unit, tick uint32) {
	if s == nil || u == nil || s.World == nil {
		return
	}
	if !combat.IsWaterDamageTick(tick) { // [04 §9.2] cadence
		return
	}
	// Both mission words must be nonzero [04 §9.2]; they reach the session on
	// the terrain record beside the sea-level byte they are tested against.
	if s.World.WaterDoesDamage == 0 || s.World.WaterDamage == 0 {
		return
	}
	if !combat.IsWaterDamageEligible(u, s.World) { // canhover exemption and height <= sea level [04 §9.2]
		return
	}
	damageMod := int32(65536) // 1.0 [02 "Unit record"] default
	if u.Def != nil {
		damageMod = u.Def.DamageModifier
	}
	amount := combat.ComputeWaterDamageScaledAmount(s.World.WaterDamage, u.Kills, combat.UnitArmored(u), damageMod)
	u.LastDamageCause = uint8(combat.CauseWaterDamage) // [06 §12.1] cause 11
	u.Health = combat.ApplyDamage(u.Health, amount)    // [06 §9.1] 16-bit modular subtraction
	if u.Health <= 0 && s.Units != nil {
		// The owner's control byte is 1 or 2 by the caller's gate, which is
		// also gate 2 of [06 §9.1] step 6, so the latch is admitted. Water has
		// no attacker: the recorded-attacker link is written null by the plain
		// Destroy arm [04 R-UNIT-06 §5].
		s.Units.Destroy(u.Handle, units.DeathKilled) // [04 §2.4] marks Dying; step 10 finalizes it
	}
}

// stepHealTimeSelfRepair is the `healtime` self-repair act, the second act of
// step 9 of the per-unit visit: retail runs it in the general unit update
// immediately after the water-damage test above and before the same pass's
// cloak settlement [04 R-SPEC-01 §4][04 R-MOV-03 §1].
//
// It is the ONLY reader of the definition's `healtime` word anywhere in retail
// [04 R-SPEC-01 §4]. It is not an order, not a state and not a capability: a
// definition that authors the word heals itself, wherever it is and whatever
// it is doing.
//
// The gates, all of them [05 R-WORK-01 §3, "`healtime`, the only consumer"]:
//
//   - the definition's `healtime` is non-zero;
//   - `(unsigned)health < (unsigned)maxdamage` — the UNSIGNED compare, so a
//     unit whose health went negative (an overkill, or the drowning packet the
//     act above may just have applied) reads as a very large value and is
//     refused rather than healed;
//   - `tick & 7 == 0`, an eight-tick cadence of its own, unrelated to the
//     water step's thirty;
//   - the owner is an ordinary or a computer player — the caller's `work` gate,
//     the block's control-byte 1-or-2 test [06 R-DMG-01 §8].
//
// The work itself is the SHARED REPAIR HELPER, called with this unit as both
// the builder and the target: the unit bills itself and heals itself. The
// quantum is construction.HealQuantum, and the helper's two terms are each
// clamped to exactly one whenever positive, so the observable effect for every
// definition that authors the word is one health point and one energy unit per
// eight ticks — a hundred and twenty-five hit points and a hundred and
// twenty-five energy per minute [05 R-WORK-01 §3].
//
// It DOES cost resources. The energy goes through the ordinary one-resource
// admission against the unit's own buckets, so a player whose energy carry has
// gone positive — a stall — stops self-healing until the stall clears; the
// heal and the charge are refused together, never one without the other.
//
// It stops when the unit reaches full health: the gate above closes and the
// helper's own signed entry compare closes behind it. There is no completion
// cue, no order to advance and no state to leave — the act simply stops firing.
//
// It draws no random number [05 R-WORK-01 §3, "repair's randomness"].
func (s *Session) stepHealTimeSelfRepair(u *units.Unit, tick uint32) {
	if s == nil || u == nil || u.Def == nil || s.Build == nil {
		return
	}
	if u.Def.HealTime == 0 {
		return
	}
	// The unsigned compare, which is also what keeps a unit the water packet
	// just killed out of the helper.
	if uint32(u.Health) >= uint32(u.Def.MaxDamage) {
		return
	}
	if tick&7 != 0 {
		return
	}
	// Builder and target are the same unit. Repair owns the signed entry
	// compare, the two clamped terms, the one-resource energy admission against
	// the BUILDER's buckets and the kind-10 heal packet [05 R-WORK-01 §3].
	s.Build.Repair(u, u, construction.HealQuantum(u.Def.HealTime))
}

func (s *Session) stepUnitPhase(tick uint32) {
	// Begin movement's per-tick occupancy transaction for the phase-2 unit sweep.
	if s.Movement != nil {
		if s.Units != nil {
			s.Movement.BindWorld(s.Units)
		}
		s.Movement.BeginTick(tick)
	}
	// 2 one deterministic active unit-slot traversal ascending [01 §4.4][01 §6.1][INVARIANTS I1]
	// Each active unit visited exactly once under researched rule; new/dead units follow same-tick visibility [01 §4.4] R-P0-04
	if s.Units != nil {
		ordersPump := &orders.Pump{World: s.Units}
		s.Units.VisitActiveSlots(func(v units.SlotVisit) {
			h := v.Handle
			u := v.Unit
			// The player gate of [04 R-MOV-03 §1], read per unit from the
			// owner record. A slot the sweep does not admit is not visited at
			// all — not even for its counter, its pre-update or its death
			// mark, all of which sit inside the visit the gate refuses.
			var owner uint8
			if u != nil {
				owner = u.Owner
			}
			visit, work := s.sweepPlayerGate(owner)
			if !visit {
				return
			}
			// unit pre-update (StepPreUpdate) [04 R-MOV-03 §1]
			s.Units.StepPreUpdate(h, tick)
			// weapon slot/service step per unit [06 §3][06 §4] — stable weapon index once-compiled [ON-04].
			// Step 3 of [04 R-MOV-03 §1] runs "for an owner of controller 1 or
			// 2 only"; the COB drain of step 4 is unconditional, so the else
			// arm below still owns it for a remote-peer owner.
			if work && s.Combat != nil && s.Catalog != nil && u != nil && u.Alive {
				wsum := s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
				// Exactly-one synchronous COB drain per unit visit [04 §4.2][04 §4.6][GAP T15 C17].
				// Combat drains only inside the Aim handshake; when it did not, this visit owns
				// the drain so pending threads progress even without weapons or on non-turret arms.
				if !wsum.Drained {
					if vm := u.GetScript(); vm != nil {
						vm.Drain(1)
					}
				}
			} else if u != nil {
				// No weapon service for this unit: the visit still owns the COB drain [04 §4.2]
				if vm := u.GetScript(); vm != nil {
					vm.Drain(1)
				}
			}
			// Step 9 of [04 R-MOV-03 §1] opens with the water-damage packet
			// of [04 §9.2], BEFORE the two order pumps and the mover tick
			// that close the same block — so a unit that drowns this tick
			// takes the damage on the position its own mover left it at last
			// tick, and its death mark is read by step 10 below.
			//
			// `work` is already the block's control-byte 1-or-2 gate
			// ([06 R-DMG-01 §8]); the step's other gates — the tick%30
			// cadence, the mission's two words, the height test and the
			// `canhover` exemption — live in stepWaterDamage.
			if work {
				s.stepWaterDamage(u, tick)
				// The second act of the same step: `healtime` self-repair,
				// which retail runs immediately after the water-damage test
				// and before the pass's cloak settlement [04 R-SPEC-01 §4].
				// Its own gates — the eight-tick cadence, the non-zero
				// `healtime` and the unsigned health test — live in the act.
				s.stepHealTimeSelfRepair(u, tick)
			}
			// order resolve/pump per unit (PumpUnit) [04 §3.3]. Producers bind
			// queues when they create them, while existing queues are bound
			// immediately before this first use.
			//
			// A unit that has never carried an order has no queue at all, and
			// the pump returns straight back out when it finds none. That used
			// to be harmless, because an empty queue had no work. It is not
			// harmless now: the idle refill of [04 §3.3] is precisely the work
			// an empty queue owes, and a factory-fresh aircraft that has never
			// been given an order is exactly the unit that needs its
			// `defaultmissiontype` record in order to come home and land.
			//
			// The queue is materialised only when the definition authors a
			// default mission, which is the same precondition IdleRefillMission
			// tests first, so a unit that could never refill still never gets an
			// empty queue built for it. The binding has to be in place before
			// the pump runs: the refill reads the owner's controller state
			// through it.
			// The two order pumps are inside the control-byte 1-or-2 block of
			// [04 R-MOV-03 §1] step 9, together with the mover tick and the
			// post-move correction below.
			if work && ordersPump != nil {
				if orders.QueueOfUnit(u) == nil && u != nil && u.Def != nil && u.Def.DefaultMissionType != "" {
					orders.QueueForUnit(u)
				}
				if orders.QueueOfUnit(u) != nil {
					s.bindExistingOrderQueue(u)
				}
				ordersPump.PumpUnit(h, tick)
			}
			// Pumping can advance the primary head in this same visit.  Reconcile
			// the activation boundary immediately so a stale request/route cannot
			// be consumed by movement for the successor order.  The scheduler has
			// already run for this tick; the replacement is therefore serviced on
			// its next normal scheduler turn, without a second scheduler call.
			// It reconciles what the pump above just did with the mover below,
			// so it belongs to the same gated block.
			if work && s.Movement != nil {
				qActive := orders.QueueOfUnit(u)
				var active *orders.Node
				if qActive != nil {
					active = qActive.Head()
				}
				if active != nil {
					activeName := orders.DescriptorFor(active.ID).Name
					// Park joins the move family: its phase 0 installs the
					// rectangle goal that carries a no-rally factory product off
					// its pad [04 R-ORD-01 §2][04 R-FAC-02 §4].
					activeMove := activeName == "Move_Ground" || activeName == "VTOL_Move" || activeName == "QMove" || activeName == "Patrol" || activeName == "QPatrol" || activeName == "VTOL_Patrol" || activeName == "RepairPatrol" || activeName == "VTOL_RepairPatrol" || activeName == "Park"
					// The ground work family installs a movement goal and then
					// waits behind gate 0xE0/0xE8 for the follower's outcome
					// [04 R-ORD-01 §5]. Those records need the mover as much as
					// the move family does; excluding them from this boundary
					// deactivated the mover the moment a work record reached the
					// head, so an out-of-reach assistant, repairer or captor
					// never took a step and its approach gate was never
					// satisfied by anything.
					activeWork := activeName == "HelpBuild" || activeName == "RepairUnit" || activeName == "Capture" || activeName == "Reclaim" || activeName == "Resurrect"
					// Beyond the two named families, ANY record that currently
					// owns this mover's installed ground goal needs the mover.
					// The four installers of [04 R-ORD-01 §1] bind the payload
					// to the record they install for and Release drops it, so
					// owning one is the record's own statement that it is
					// waiting on a movement outcome — a stronger and narrower
					// test than membership of a name list.
					//
					// `Attack_Chase` is why this is here. Its phase 2 installs a
					// point or banded goal sized from the weapon's engagement
					// distance and then waits behind gate `0x148E8`/`0x100E8`
					// for the verdict [04 R-ORD-01 §3], the same shape as the
					// work family above; being on neither list meant the goal
					// was installed and never activated, and an explicitly
					// ordered attacker never took a step toward its target.
					// The goal-position rewrite below stays keyed on
					// `activeMove`, because a chase goal is deliberately NOT the
					// target's own position.
					activeGoal := s.Movement.HasGroundGoal(h, active)
					isWalk := false
					if s.Build != nil && (activeName == "MobileBuild" || activeName == "VTOL_MobileBuild") && s.Build.NeedsWalk(u, active) {
						isWalk = true
					}
					if isWalk {
						// A mobile builder that still needs to walk — because its
						// record has not yet retired its approach phase, or
						// because its own footprint still covers the site — keeps
						// its goal installed and its mover activated
						// [04 R-PATH-01 §13][04 R-COLL-01 §2].
						//
						// Corrected (WU-19-225). The row's abandon arm used to
						// stand here, reading a wake this boundary had itself
						// delivered through a seam of its own. It belongs to the
						// row's phase 1, which the order pump above now
						// dispatches on gate `0xE0`: status 7 `I can't reach the
						// construction site` and result code 8 are emitted from
						// internal/construction's registered handler, so by the
						// time this boundary runs the record is already unlinked
						// and `active` is its successor [04 R-ORD-01 §5]
						// [05 R-WORK-01 §13][04 §3.3].
						s.Build.EnsureWalkPublic(u, active)
						active.MoveState = orders.MoveEnRoute
					} else if activeMove || activeWork || activeGoal {
						if activeMove && active.Target != 0 {
							var target *units.Unit
							if binding := qActive.Binding(); binding != nil && binding.Lookup != nil {
								target = binding.Lookup(active.Target)
							}
							if target == nil && s.Units != nil {
								target = s.Units.Unit(active.Target)
							}
							if target == nil || !target.Alive {
								rejected := qActive.RemoveHead()
								if rejected != nil {
									rejected.MoveState = orders.MoveBlocked
									rejected.PathStatus = uint32(path.StatusRejected)
								}
								s.Movement.DeactivateMove(h)
							} else {
								active.GoalX, active.GoalY, active.GoalZ = target.X, target.Y, target.Z
							}
						}
						if qActive.Head() == active && s.World != nil {
							// Submission, search, and publication own goal rejection. A
							// session-level cell precheck incorrectly discards commands whose
							// reachable acceptance area lies beside a blocked goal cell [04
							// R-PATH-01 §4][04 R-PATH-01 §7].
							s.Movement.ActivateMove(u, active)
							active.MoveState = orders.MoveEnRoute
						}
					} else {
						s.Movement.DeactivateMove(h)
					}
				} else {
					s.Movement.DeactivateMove(h)
				}
			}
			// construction/worker action per unit (StepUnit) [05 "Factory production lifecycle"]
			// Construction work is order-driven: [04 §3.5] places its operation
			// records on the unit's queue. StepUnit's queue-less branch otherwise
			// only called its lazy queue accessor and returned an empty result, so
			// skipping that call preserves construction cadence while avoiding an
			// observable empty allocation for units with no construction order.
			// Construction work is this build's owner of the order pump's
			// construction records, so it is gated with the pump that feeds it.
			if work && s.Build != nil && orders.QueueOfUnit(u) != nil {
				ctx := construction.TickContext{Tick: tick, World: s.Units, Economy: s.Econ, Terrain: s.World, Catalog: s.Catalog}
				wres := s.Build.StepUnit(ctx, h)
				if wres.Completed {
					// Completed product joins the world: movement state + visibility
					// publish for the new unit [RX-05][01 §6.1][03 §3].
					if wres.Product != 0 {
						s.CompleteUnit(wres.Product)
					}
				}
			}
			// movement step per unit (Between BeginTick/EndTick, StepUnit with route ownership) [04 §8.1][04 §8.2].
			// The mover tick and the post-move correction close the control-byte
			// 1-or-2 block of [04 R-MOV-03 §1] step 9.
			if work && s.Movement != nil {
				mres := s.Movement.StepUnit(h, tick)
				_ = mres
				// [R-P0-01] Move_Ground-family completion arrives through the orders pump
				// result path only (satisfied 0x20 → code 5); invented ≤2wu distance
				// completion deleted. Route pruning (≤25 whole units) and
				// localSteeringThreshold remain separate and never complete the order.
				// Failed publications do not consume the order. The follower keeps
				// polling at its 60-tick cadence with no retry ceiling [04
				// R-MOV-01 §7].
				// Session does not inspect path-failure diagnostics or submit a second
				// request; movement's follower state is the sole recovery owner
				// [04 R-PATH-01 §6–§8].
			}
			// Slot-end death, cleanup, corpse, occupancy, target invalidation
			// [01 §4.4][04 §2.4]. This is the only gameplay finalizer in battle.
			s.finalizePhase2Death(h, tick)
		})
	}
	if s.Movement != nil {
		s.Movement.EndTick(tick)
	}
}

// stepProjectilePhase is phase 3 of the authoritative tick [01 §4.4][06 §5].
func (s *Session) stepProjectilePhase(tick uint32) {
	// 3 projectile integration and collision + pool compactor [01 §4.4][06 §5][06 §11.2]
	// Interceptor guidance pre-step before motion [06 §11.2]
	//
	// The two steps below are the post-launch halves of [06 §11.2] C29 —
	// guidance retargets a live interceptor at its linked candidate, the
	// detonation sweep clears projectiles inside the blast — and both are
	// reached only for a projectile whose weapon carries `interceptor`.
	//
	// The TODO(T25) that stood here said nothing in this build LAUNCHED one, so
	// neither step could run. WU-19-234 closed the launch half in
	// internal/combat: the automatic interceptor scan now runs from its
	// per-slot position in the autonomous scan and installs the point target,
	// and the vertical-launch executor's fire-time rescan is bound and supplies
	// the matched-projectile link the vertical creator retains [06 §11.2]
	// [06 §4.4] [06 §6.6]. The chain is exercised end to end, on retail
	// content, by TestRetailAntiNukeIntercept: an ARM Protector holding one
	// stockpiled round engages a nuclear missile aimed at the ground it stands
	// on, and the missile does not arrive.
	if s.Combat != nil {
		// [06 §6.4] plumb world wind vectors into ballistic/dropped drift
		s.Combat.TickProjectiles(tick, s.Units, s.World, s.Wind, s.Features, s.Vis, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
	}
}

// stepEffectPhase is phase 4 of the authoritative tick [01 §4.4].
func (s *Session) stepEffectPhase(tick uint32) {
	// 4 general effects and feature motion [01 §4.4]. The effect pool is
	// advanced once at this phase boundary. Admission consumes the current
	// ordered presentation window; publication only snapshots the resulting
	// pool [03 §1][01 §4.4].
	var presentationEvents []frame.Event
	if s.publication != nil && s.publication.events != nil {
		presentationEvents = s.publication.events.StagingEvents()
	}
	if s.publication != nil && s.publication.effects != nil {
		s.publication.effects.Advance(tick, presentationEvents)
	}
	if s.Features != nil {
		s.Features.TickMotion(tick)
	}
}

// stepPlayerPhase is phase 5 of the authoritative tick [01 §4.4][05
// "Authoritative settlement order"][R-CORE-01 §4.4.1].
// DET-06: the path scheduler runs FIRST, then per player 0..9 ascending the
// per-player orders/work followed by that player's visibility stamp sweep
// ([R-CORE-01 §4.4.1] "Visibility publication seam" — the publication lives
// INSIDE phase 5; the earlier post-phase-12 pass is removed). The sensor
// deadline pass runs inside the LOCAL viewing player's iteration, after that
// player's stamp sweep [R-SENSOR-01] — see tickPlayers; it is not a separate
// post-loop pass.
func (s *Session) stepPlayerPhase(tick uint32) {
	// Path scheduler first [R-CORE-01 §4.4.1]. Requests submitted by the live
	// unit sweep are serviced here at the phase-5 path boundary; an already
	// published route is consumed exactly once.
	if s.Movement != nil && s.Movement.Scheduler != nil {
		s.Movement.Scheduler.Tick(tick)
	} else if s.Path != nil {
		s.Path.Tick(tick)
	}
	// Per player 0..9 ascending: orders/work, then that player's stamp sweep
	// [R-CORE-01 §4.4.1]. No map-defined player order [ON-09] (I1). The
	// sensor/deadline pass runs inside the local viewing player's iteration
	// [R-SENSOR-01].
	s.tickPlayers(tick)
}

// stepFeatureLifecyclePhase is phase 6 of the authoritative tick [01 §4.4].
func (s *Session) stepFeatureLifecyclePhase(tick uint32) {
	// 6 feature lifecycle and reclaim or death processing (burn, wind probes, successor hops; reclaim credits become visible at the next settlement) [01 §4.4][05 "Feature burning"][05 "Feature sinking and water interaction"][06 §13.1]
	if s.Features != nil {
		s.Features.TickLifecycle(tick)
	}
}

// stepSharingPhase is the transport tail after phase 12 [01 §4.4].
func (s *Session) stepSharingPhase(tick uint32) {
	// Sharing cadence is the transport tail after phase 12 [01 §4.4][05
	// "Allied resource and sensor sharing"].
	if s.Econ != nil {
		// The unit world carries the candidate scan's elimination counters
		// [05 R-SHARE-01 §3].
		s.Econ.ShareTick(tick, s.Units)
		// No map iteration inside ShareTick (it iterates players 0..9 asc)
	}
}

// stepResultPhase is the per-sub-tick tail of the skirmish result path. It
// settles commander-death transitions that a composition seam filed outside
// phase-2 finalization, and nothing else: the end predicates, the shared
// countdown and the end latch all belong to the local slot's settlement due and
// run from endConditionBlock [08 R-TRIG-01 §6] "The due tick is the settlement
// deadline".
//
// The owner sweep stays per sub-tick because retail runs it from the
// kill-record handler, at the death, not at the next due [08 R-SKIR-01 §3]
// "Trigger site". finalizePhase2Death is the ordinary caller and the pending
// flag makes this one idempotent. No presentation mirror is needed here: every
// Result field presentation reads — Ended, Countdown, the score rows — is
// written on a due, so a per-sub-tick refresh would copy unchanged values.
// Gameplay unit retirement is intentionally absent: phase-2 slot visitation
// owns that decision [01 §4.4].
func (s *Session) stepResultPhase(tick uint32) {
	// The "no human left playing" countdown step is Established as
	// kind-3-only, and needs no mission-type mapping to exclude: both
	// live-player counters are called exclusively from the multiplayer branch
	// of the end-condition block — "Kinds 1 and 2 never call either counter, so
	// a single-player engine needs neither" [08 R-SESS-01 §1]. Campaign and
	// skirmish are the only kinds this engine runs, so the site is absent by
	// contract rather than deferred.
	//
	// The gate below is the commander-death rule word, reached through the
	// mission type: a campaign battle's OTA load writes commander death = 0
	// [08 R-SKIR-01 §4], and rule 0 "skips the sweep entirely"
	// [08 R-SKIR-01 §3], so only a skirmish can have a pending sweep to run.
	if s.Mission != nil && s.Mission.Type == mission.TypeSkirmish && s.Skirmish.NumPlayers > 0 {
		s.processPendingCommanderDeaths(tick)
	}
}

func (s *Session) initMeteor() {
	if s == nil || s.Meteor.Initialized {
		return
	}
	s.Meteor.Initialized = true
	var weaponName string
	var radius int32
	var density, duration, interval float64
	// Try global header first [P1-02 §2.1] (campaign/skirmish global).
	if s.Mission != nil && s.Mission.OTA != nil && s.Mission.OTA.Global != nil {
		// Use mission globals census [P1-02]
		if mg := mission.DecodeMissionGlobals(s.Mission.OTA.Global); mg != nil {
			weaponName = mg.MeteorWeapon
			radius = mg.MeteorRadius
			density = mg.MeteorDensity
			duration = mg.MeteorDuration
			interval = mg.MeteorInterval
		}
	}
	// Fallback to per-schema meteor from MapHeader when global missing or weapon empty but schema carries it [02 "Map files"] [fmt ota].
	if weaponName == "" && s.Mission != nil && s.Catalog != nil && s.Mission.TerrainKey != "" && s.Mission.Schema.Name != "" {
		if mh, ok := s.Catalog.Maps[content.CanonicalKey(s.Mission.TerrainKey)]; ok && mh != nil {
			for _, sch := range mh.Schemas {
				if sch.Name == s.Mission.Schema.Name {
					if sch.MeteorWeapon != "" {
						weaponName = sch.MeteorWeapon
					}
					if radius == 0 && sch.MeteorRadius != 0 {
						radius = sch.MeteorRadius
					}
					if density == 0 && sch.MeteorDensity != 0 {
						density = sch.MeteorDensity
					}
					if duration == 0 && sch.MeteorDuration != 0 {
						duration = sch.MeteorDuration
					}
					if interval == 0 && sch.MeteorInterval != 0 {
						interval = sch.MeteorInterval
					}
					break
				}
			}
		}
	}
	var defaults *content.MeteorDefaults
	if s.Catalog != nil {
		defaults = s.Catalog.Meteor
	}
	effRadius := combat.EffectiveMeteorRadius(radius, defaults)
	effDensity := combat.EffectiveMeteorDensity(density, defaults)
	effDuration := combat.EffectiveMeteorDuration(duration, defaults)
	effInterval := combat.EffectiveMeteorInterval(interval, defaults)
	s.Meteor.WeaponName = weaponName
	s.Meteor.Radius = effRadius
	s.Meteor.Density = effDensity
	s.Meteor.DurationTicks = combat.MeteorDurationTicks(effDuration)
	s.Meteor.IntervalTicks = combat.MeteorIntervalTicks(effInterval)
	s.Meteor.PerHitDelay = combat.MeteorDelay(effDensity)
	// Resolve weapon: empty disables, unresolved or non-meteor falls back to ID 0 [06 §6.5]
	if s.Catalog != nil && s.Catalog.Weapons != nil {
		s.Meteor.Weapon = combat.ResolveMeteorWeapon(weaponName, s.Catalog.Weapons)
	}
	s.Meteor.Enabled = combat.IsMeteorEnabled(weaponName)
	if s.Meteor.Weapon == nil {
		// If weapon name resolves to nil (empty disables), ensure disabled even if helper would fallback
		if weaponName == "" {
			s.Meteor.Enabled = false
		}
	} else {
		// Weapon resolved non-nil implies enabled when name non-empty per [08 "Meteor showers"]; keep Enabled as IsMeteorEnabled
	}
	if !s.Meteor.Enabled {
		return
	}
	// Seed first storm to per-hit delay [02 Meteor scheduler]
	// When delay 0, NextStrike 0 means immediate storm at tick 0.
	if s.Meteor.PerHitDelay < 0 {
		s.Meteor.PerHitDelay = 0
	}
	s.Meteor.NextStrike = uint32(s.Meteor.PerHitDelay)
	s.Meteor.NextHit = 0
	s.Meteor.StrikeEnds = 0
	s.Meteor.Active = false
	s.Meteor.OriginX = 0
	s.Meteor.OriginZ = 0
	s.Meteor.TargetX = 0
	s.Meteor.TargetZ = 0
}

// tickMeteor implements the phase-9 shower scheduler per [R-CORE-01 §4.4.1]
// [08 "Meteor showers"] [02 Meteor scheduler] [06 §6.5].
// DET-03 audit: the four scheduling draws are consumed ONLY on due
// evaluations — the earlier code drew them every sub-tick and, when the storm
// was disabled, never advanced the deadline, so "due" was every tick. The
// corrected body arms the strike window on every due evaluation (even when
// disabled, so the deadline advances) and spends the per-hit radius/angle
// draws only for actual spawns [R-CORE-01 §4.4.1][06 §6.5].
// Arming: strikeEnd = tick + durationTicks (authored seconds → ticks at load),
// nextStrike = strikeEnd + intervalTicks, per-hit spacing trunc(30/density);
// the spacing is also the initial next-strike written at battle entry.
// The next-strike comparison is non-strict (due when tick >= nextStrike).
// Storms run after the projectile phase so spawns move next tick [06 §6.5].
func (s *Session) tickMeteor(tick uint32) {
	if s == nil {
		return
	}
	if !s.Meteor.Initialized {
		s.initMeteor()
		if !s.Meteor.Initialized {
			return
		}
	}
	if s.World == nil {
		// No map-independent geometry source: leave state and stream untouched.
		return
	}
	crt := s.CrtRNG()
	if crt == nil {
		return
	}
	if !s.Meteor.Active {
		// Non-strict next-strike comparison [R-CORE-01 §4.4.1].
		if tick < s.Meteor.NextStrike {
			return
		}
		// Due evaluation: four scheduling draws, consumed even when the storm
		// is disabled [06 §6.5][R-CORE-01 §4.4.1]. Target = one draw scaled by
		// map depth then one by map width; origin = target plus a depth-axis
		// offset (draw*10)/0x8000−15 and a width-axis offset (draw*30)/0x8000−15.
		mapW, mapH := s.World.CellW, s.World.CellH
		s.Meteor.TargetX, s.Meteor.TargetZ, s.Meteor.OriginX, s.Meteor.OriginZ = combat.MeteorSchedule(crt, mapW, mapH)
		s.Meteor.Active = true
		s.Meteor.StrikeEnds = tick + uint32(s.Meteor.DurationTicks)                // trunc(duration*30) at load
		s.Meteor.NextStrike = s.Meteor.StrikeEnds + uint32(s.Meteor.IntervalTicks) // trunc(interval*30) at load
		s.Meteor.NextHit = tick                                                    // first hit on the opening tick
	}
	if tick > s.Meteor.StrikeEnds {
		s.Meteor.Active = false
		return
	}
	if tick < s.Meteor.NextHit {
		return
	}
	// Per-hit spacing trunc(30/density); a zero spacing attempts a spawn on
	// every storm tick (density 31+) so keep a one-tick floor for the timer.
	if s.Meteor.PerHitDelay > 0 {
		s.Meteor.NextHit = tick + uint32(s.Meteor.PerHitDelay)
	} else {
		s.Meteor.NextHit = tick + 1
	}
	// Spawn gate: a disabled storm (no weapon) spends no per-hit draws —
	// "six draws per METEOR" counts only meteors actually created [06 §6.5].
	if s.Combat != nil && s.Meteor.Weapon != nil {
		// Spawn uses the stored storm target/origin plus two lateral CRT draws
		// (radius then angle) inside SpawnMeteor [06 §6.5]. Pool-full drops
		// silently after the hit timer advanced; no retry [06 §6.5]. Meteor
		// enters at 1350 wu altitude, −15 wu/tick vertical, horizontal
		// trunc(((target−origin)<<20)/90) per axis.
		_, _ = combat.SpawnMeteor(s.Combat, crt, tick, s.Meteor.Weapon, s.Meteor.TargetX, s.Meteor.TargetZ, s.Meteor.OriginX, s.Meteor.OriginZ, s.Meteor.Radius)
	}
}

// tickPlayers is stage 5 of the authoritative tick: the per-player orders,
// path, economy and occupancy pass.
//
// Invariant: players are traversed 0..9 ascending with no map-defined order
// [INVARIANTS I1], and for each player the AI coordinator runs inside
// economy.TickPlayer's beforeDeadline hook — after the per-tick helpers and
// before the settlement deadline compare
// [05 "Authoritative settlement order"][PLAN_11 C11]. That AI is the
// auxiliary player-level update is a supported inference [08 "Established
// AI-facing data and rooted planner"]. This is the only per-player settlement
// loop in the package; a second one with a different hook position would be a
// second settlement order.
//
// The sensor/deadline pass executes inside the LOCAL viewing player's
// iteration, after that player's stamp sweep [R-SENSOR-01]: because the loop
// walks players ascending, it runs after the local player's visibility stamps
// but before every higher-indexed player's stamps within the same tick.
// The campaign end-condition poll no longer rides this before-hook: it runs
// inside the settlement deadline block, after the local slot's UpdateTime
// advance and before that slot's settlement gate chain, through
// economy.Service.EndCondition [08 R-TRIG-01 §6].
//
// Residual delta from [R-SENSOR-01]'s exact retail ordering: the per-tick
// minimap contacts pass and mapped-minimap rebuild are presentation-side and
// have no sim counterpart here. No economy after-hook is required — the
// position after stampPlayerSlice is session-owned loop body. The pass runs
// once per tick keyed to the local viewing slot; SensorTick owns the
// player-count gate.
func (s *Session) tickPlayers(tick uint32) {
	if s == nil || s.Econ == nil {
		return
	}
	localPlayer, hasLocalPlayer := s.triggerLocalPlayer()
	if hasLocalPlayer {
		s.LocalOwner = uint8(localPlayer)
	}
	// The end-condition block is bound onto the ledger, not passed per call,
	// because it fires from inside the settlement deadline block rather than
	// at the before-hook position [08 R-TRIG-01 §6]. Binding here rather than
	// at service wiring keeps every session that ticks — including the ones
	// tests construct directly — on the one settlement order.
	if s.Econ.EndCondition == nil {
		s.Econ.EndCondition = s.endConditionBlock
	}
	for player := 0; player < 10; player++ {
		mgr := s.AI[player] // direct player-indexed access per RS-02 [08] I1
		// Bind per-session RNG for isolation [RS-06][I4] — ensure manager uses session's stream, not shared global.
		if mgr != nil && mgr.RNG == nil {
			mgr.RNG = s.SimRNG()
		}
		// Bind the combat half of the one 30-tick routine [06 §3.1]. The
		// per-side target registry rebuild and the strategic refresh are one
		// retail routine on one object per player slot; this build splits them
		// across internal/combat (the candidate lists and the secondary-list
		// gate) and internal/ai (the census, the centroid and the single
		// bound-30 draw) because neither package may import the other. The
		// session is the only place that sees both, so it binds the seam here,
		// beside the RNG binding, so a session a test constructs directly is
		// wired the same way the composed one is.
		//
		// The result is retail's shape: one gate per slot, both halves rebuilt
		// on the same tick, one draw per due, at the per-player phase position
		// [06 §3.1 "Which slots draw"]. Before WU-19-126 the combat half ran
		// from the weapons step on its own cadence word and the two clocks
		// could drift apart by up to thirty ticks.
		if mgr != nil && !mgr.Strategic.TargetRegistryRebuildBound() {
			mgr.Strategic.BindTargetRegistryRebuild(s.rebuildTargetRegistryForSlot)
		}
		before := func() {
			if mgr != nil {
				mgr.Tick(tick, s.Units, s.Econ)
			}
		}
		s.Econ.TickPlayer(player, tick, s.Units, before)
		// Per-player visibility stamp sweep, after that player's orders/work
		// [R-CORE-01 §4.4.1] DET-06: dirty-checked, per in-game unit, slots
		// ascending within the player's slice.
		stampPlayerSlice(s, player)
		// [R-SENSOR-01]: sensor/deadline work in the LOCAL viewing player's
		// iteration only, immediately after that player's stamp sweep (called
		// unconditionally; SensorTick owns the player-count gate).
		if hasLocalPlayer && player == localPlayer {
			s.stepSensorPhase(tick)
		}
	}
}

// rebuildTargetRegistryForSlot is the session end of the seam bound onto every
// manager's strategic state in tickPlayers. The strategic refresh's cadence
// gate calls it on a due, before the census and before the bound-30 draw, so
// the candidate lists and the counters of the one retail routine are rebuilt
// on the same tick from the same gate [06 §3.1][08 R-AI-01 §16].
//
// It takes no random draw of its own; the routine's single draw stays where it
// is, at the end of the refresh (I4).
func (s *Session) rebuildTargetRegistryForSlot(tick uint32, player uint8) {
	if s == nil || s.Combat == nil {
		return
	}
	s.Combat.RebuildTargetRegistryIfDue(tick, player, s.Units, s.Vis, s.World, s.Econ)
}

// endConditionBlock is the economy ledger's EndCondition seam: it runs inside
// the settlement deadline block of every due slot, and forwards the local
// slot's due to the end-condition poll [08 R-TRIG-01 §6]. The local-slot test
// lives here because the ledger has no notion of which slot is local.
//
// Both session kinds hang off this one due, in retail's order: kind 1 polls the
// authored or injected victory/defeat queues, kinds 2/3 run the two predicates.
// The two calls are mutually exclusive on the mission type, so the block does
// exactly one of them per due [08 R-TRIG-01 §6].
func (s *Session) endConditionBlock(player int, tick uint32) {
	if s == nil {
		return
	}
	if local, ok := s.triggerLocalPlayer(); !ok || local != player {
		return
	}
	s.pollMissionTriggers(tick)
	if s.Mission != nil && s.Mission.Type == mission.TypeSkirmish && s.Skirmish.NumPlayers > 0 {
		s.EvaluateResult(tick)
	}
}

// pollMissionTriggers is the local player's end-condition block [08
// R-TRIG-01 §6]. It carries no deadline of its own: it is reached from the
// local slot's settlement deadline block, after that slot's UpdateTime has
// advanced by 30 and before its settlement gate chain, so the poll runs
// exactly once per settlement due and a load resumes it on the saved
// UpdateTime phase. The sibling WinLoseTime word is seeded at battle init and
// persisted, and no gameplay site reads or advances it. Only kind 1 polls the
// authored or injected queues.
func (s *Session) pollMissionTriggers(tick uint32) {
	if s == nil || s.Mission == nil || s.Mission.Type != mission.TypeCampaign || s.Econ == nil {
		return
	}
	localPlayer, ok := s.triggerLocalPlayer()
	if !ok {
		return
	}
	s.LocalOwner = uint8(localPlayer)

	v, d := triggers.EvaluateOwned(&s.Mission.Victory, &s.Mission.Defeat, s.missionTriggerContext(tick))
	s.VictoryDone = s.VictoryDone || v
	s.DefeatDone = s.DefeatDone || d
	var latched bool
	if v {
		latched = s.Latch.AdvanceWin(true)
	} else if d {
		latched = s.Latch.AdvanceLose(true)
	}
	for i := 0; i < 10; i++ {
		s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
		s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
	}
	if latched && s.Latch.IsEnding() && s.State == StateBattle {
		win := s.Latch.IsWin()
		s.Progress.ApplyCampaignResult(s.CampaignSlot, win)
		if !s.result.Ended {
			kind := "defeat"
			if win {
				kind = "victory"
			}
			var winners, losers []int
			if win {
				winners = []int{localPlayer}
				losers = []int{1}
			} else {
				winners = []int{1}
				losers = []int{localPlayer}
			}
			scores := s.collectScores(winners[0], false)
			reason := "campaign"
			if win && len(s.Mission.Victory) > 0 {
				reason = "victory_trigger"
			} else if !win && len(s.Mission.Defeat) > 0 {
				reason = "defeat_trigger"
			}
			s.result = Result{Ended: true, Draw: false, Kind: kind, WinnerTeam: winners[0], Winners: winners, Losers: losers, Reason: reason, Tick: tick, ArmedTick: tick, Countdown: s.Latch.Countdown, Scores: scores, ColumnMaxima: resultColumnMaxima(scores)}
		}
		_ = s.TransitionTo(StatePostBattle)
	}
}

// Step is the single-player tick loop per PLAN_14 C6-C7 [01 §4.2][01 §4.3][01 §4.4].
// It reads the time source (caller supplies scaledNow = floor(GetTickCount*30/1000) [01 §4.1]),
// calls clock.AdvanceSP(scaledNow) which short-circuits behind the SP pause gate
// (anchor stalls, unpause yields one capped burst ≤5) [01 §4.3] C7,
// runs 0..5 sub-ticks publishing after phase 12 of each completed sub-tick.
// Presentation consumes the resulting committed frame outside Session; it is
// not a session callback [01 §4.4][03 §1].
//
// P0-I10: Session.Step executes authoritative ticks only in StateBattle (6) [08 "Session states"].
// Loading completion defers first battle dispatch to next dispatch (C2). Abort and victory/defeat
// transition through 7/2 rather than leaving battle ticking behind overlay [08].
func (s *Session) Step(scaledNow int32) {
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	// State machine governs lifecycle [08 "Session states"] P0-I10.
	// If not in battle or pendingBattle, drive one state dispatch and do not tick this frame.
	// This makes newly constructed loading sessions unable to tick and ensures
	// state-5 completion schedules state-6 for the next dispatch (C2).
	if s.State != StateBattle || s.IsPendingBattle() {
		wasPending := s.IsPendingBattle()
		// Drive one dispatch. For non-battle states this runs the state's handler
		// which transitions via TransitionTo or CompleteLoading. For pendingBattle,
		// this runs the battle handler's first run [08] C2.
		s.Advance()
		// If we just cleared pendingBattle (was pending, now battle not pending),
		// we have just run the battle handler's first dispatch; now we can tick
		// authoritative simulation in this same Step call. This still respects C2
		// because battle handler did not tick inline—it ran on this next dispatch,
		// and ticks follow after it.
		if wasPending && s.State == StateBattle && !s.IsPendingBattle() {
			// fall through to ticking
		} else {
			// Not yet ready to tick: still not in battle, or just became pending,
			// or just transitioned Router/Preload->Loading. There is no presentation
			// callback at this authoritative boundary [01 §4.4][03 §1].
			return
		}
	}
	// Authoritative ticks only in StateBattle [08][P0-I10].
	if s.State != StateBattle {
		return
	}
	ticks := s.Clock.AdvanceSP(scaledNow)
	for i := 0; i < ticks; i++ {
		if s.State != StateBattle {
			break // abort or victory transitioned out mid-batch [08] 6->2 or 6->7
		}
		// BeginSubTick increments GlobalTick before phase 1 [01 §4.4] C6.
		tick := s.Clock.BeginSubTick()
		s.stepOneSubTick(tick)
		// Publication is outside the phase registry and follows sharing/result
		// work for every completed sub-tick [01 §4.4][03 §2.4][I6].
		s.publishSnapshot(tick)
		s.recordPublication(tick)
		if s.State != StateBattle {
			break // latch armed->ending transitioned to postbattle same tick [P1-01 §2.2]
		}
	}
	// The executor tail is once per host pump after the whole catch-up batch,
	// including a zero-runnable pump. It cannot interpose between a phase-12
	// result and that tick's publication [01 §4.4][01 R-PLAT-02 §7].
	s.runRetailPostLoopTail(s.Clock.GlobalTick)
}
