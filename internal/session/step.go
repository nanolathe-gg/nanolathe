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
	"github.com/nanolathe/nanolathe/internal/world"
)

// stepAuthoritativePhases runs one complete authoritative tick for focused
// same-package tests and callers that already own the tick number. Step calls
// this single path so there is no second implementation [DET-02][01 §4.4].
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
	s.phaseSequences(tick)        // 7  proven no-op for this state (sequence cursors are presentation-owned [03 §1])
	s.phaseWind(tick)             // 8  implemented — complete scheduled redraw [01 §7.3] DET-03
	s.phaseMeteorShower(tick)     // 9  implemented — meteor shower [R-CORE-01 §4.4.1]
	s.phaseCameraShake(tick)      // 10 implemented — shake driver [R-CORE-01 §4.4.1] DET-04
	s.phaseObjectSweeps(tick)     // 11 research-blocked TODO(R-CORE-01 §4.4.1) — family identified, not wired
	s.phaseCadenceFlip(tick)      // 12 research-blocked TODO(question) — flip mechanism unknown

	// Sharing is the transport tail after phase 12 [01 §4.4].
	s.stepSharingPhase(tick)

	// Post-loop cleanup and configured skirmish result evaluation.
	s.stepCleanupAndResultPhase(tick)

	s.publishSnapshot(tick)
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

// phaseSequences is phase 7 [01 §4.4] — sequence and effect-strip advancement:
// the global animation-sequence cursor list (frame counter, remaining
// duration, loop flag per cursor; each cursor advances with the same step used
// per-effect in phase 4); it is not a line-of-sight or occupancy scan. Proven
// no-op for this state: nanolathe's sequence cursors advance in the
// presentation layer [03 §1], so no sim state advances here. That
// presentation-owned note stands.
func (s *Session) phaseSequences(tick uint32) {
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
// effect-strip update sweeps. The family is IDENTIFIED as the ten effect
// strips of the rendering contract (doc 03 "Strip storage and lifecycle"):
// per strip ascending and per object in insertion order, a removal verdict is
// evaluated BEFORE the update virtual — positive destroys (destructor with
// argument 1) and removes with stable left compaction, zero runs the update
// virtual and keeps the object; empty strips touch no globals; no RNG.
// Research-blocked: nanolathe has no strip storage — the session's effect
// publication service (render.EffectService, publicationState.effects) is a
// fixed-capacity presentation pool of admitted event views, not the
// vtable-backed strip family, so the sweep cannot be force-fit onto it.
// TODO(R-CORE-01 §4.4.1): implement the ten-strip table (allocated at battle
// entry, producers append by literal strip index, oldest-first eviction above
// 400) and wire this sweep to it.
func (s *Session) phaseObjectSweeps(tick uint32) {
	s.recordPhase("phase11-objects", tick)
}

// phaseCadenceFlip is phase 12 [01 §4.4] — an every-eight-sub-tick cadence
// flip. Research-blocked: the exact flip mechanism (which cadence gate it
// drives and how the counter wraps) is not established; no consumer is wired
// in nanolathe. This is an explicit research-blocked registration point — the
// flip is NOT invented here.
// TODO(question): what does the every-eight-sub-tick cadence gate drive, and
// does the counter reset or wrap? A traced flip site would settle it.
func (s *Session) phaseCadenceFlip(tick uint32) {
	s.recordPhase("phase12-cadence", tick)
}

// stepUnitPhase is phase 2 of the authoritative tick [01 §4.4].
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
		// DET-01: inject session RNG for order jitter draws [04 §3.3][I4].
		orders.SetSimulationRNG(s.SimRNG())
		s.Units.VisitActiveSlots(func(v units.SlotVisit) {
			h := v.Handle
			u := v.Unit
			// unit pre-update (StepPreUpdate) [04 "unit sweep"]
			s.Units.StepPreUpdate(h, tick)
			// weapon slot/service step per unit [06 §3][06 §4] — stable weapon index once-compiled [ON-04]
			if s.Combat != nil && s.Catalog != nil && u != nil && u.Alive && !u.Dying {
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

			// order resolve/pump per unit (PumpUnit) [04 §3.3]
			if ordersPump != nil {
				ordersPump.PumpUnit(h, tick)
			}
			// Pumping can advance the primary head in this same visit.  Reconcile
			// the activation boundary immediately so a stale request/route cannot
			// be consumed by movement for the successor order.  The scheduler has
			// already run for this tick; the replacement is therefore serviced on
			// its next normal scheduler turn, without a second scheduler call.
			if s.Movement != nil {
				qActive := orders.QueueForUnit(u)
				active := qActive.Head()
				if active != nil {
					activeName := orders.DescriptorFor(active.ID).Name
					activeMove := activeName == "Move_Ground" || activeName == "VTOL_Move" || activeName == "QMove" || activeName == "Patrol" || activeName == "QPatrol" || activeName == "VTOL_Patrol" || activeName == "RepairPatrol" || activeName == "VTOL_RepairPatrol"
					isWalk := false
					if s.Build != nil && (activeName == "MobileBuild" || activeName == "VTOL_MobileBuild") && s.Build.NeedsWalk(u, active) {
						isWalk = true
					}
					if isWalk {
						s.Build.EnsureWalkPublic(u, active)
						active.MoveState = orders.MoveEnRoute
					} else if activeMove {
						if active.Target != 0 {
							var target *units.Unit
							if qActive.Lookup != nil {
								target = qActive.Lookup(active.Target)
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
							goalCell := path.Cell{X: world.WorldToCell(active.GoalX), Z: world.WorldToCell(active.GoalZ)}
							if !s.Movement.IsGoalCellPassable(h, goalCell) {
								rejected := qActive.RemoveHead()
								if rejected != nil {
									rejected.MoveState = orders.MoveBlocked
									rejected.PathStatus = uint32(path.StatusRejected)
								}
								s.Movement.DeactivateMove(h)
							} else {
								s.Movement.ActivateMove(u, active)
								active.MoveState = orders.MoveEnRoute
							}
						}
					} else {
						s.Movement.DeactivateMove(h)
					}
				} else {
					s.Movement.DeactivateMove(h)
				}
			}
			// construction/worker action per unit (StepUnit) [05 "Factory production lifecycle"]
			if s.Build != nil {
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
			// movement step per unit (Between BeginTick/EndTick, StepUnit with route ownership) [04 §8.1][04 §8.2]
			if s.Movement != nil {
				mres := s.Movement.StepUnit(h, tick)
				_ = mres
				// [R-P0-01] Move_Ground-family completion arrives through the orders pump
				// result path only (satisfied 0x20 → code 5); invented ≤2wu distance
				// completion deleted. Route pruning (≤25 whole units) and
				// localSteeringThreshold remain separate and never complete the order.
				if s.Movement.HasPathFailure(h) {
					if rec, ok := s.Movement.PathFailureRecord(h); ok {
						if qFail := orders.QueueForUnit(u); qFail != nil && qFail.LenPrimary() > 0 {
							headFail := qFail.Head()
							if headFail != nil {
								nameFail := orders.DescriptorFor(headFail.ID).Name
								isMoveFail := nameFail == "Move_Ground" || nameFail == "VTOL_Move" || nameFail == "QMove" || nameFail == "Patrol" || nameFail == "QPatrol" || nameFail == "VTOL_Patrol" || nameFail == "RepairPatrol" || nameFail == "VTOL_RepairPatrol"
								if isMoveFail {
									if rec.Retries >= 1 {
										headFail.MoveState = orders.MoveBlocked
										headFail.PathStatus = uint32(rec.Status)
										qFail.RemoveHead()
										if routeFail := s.Movement.Routes[h]; routeFail != nil {
											routeFail.Active = false
											routeFail.Dirty = true
										}
										if schedFail := s.Movement.Scheduler; schedFail != nil {
											schedFail.Cancel(h)
										} else if s.Path != nil {
											s.Path.Cancel(h)
										}
										s.Movement.ClearPathFailure(h)
									}
								} else {
									s.Movement.ClearPathFailure(h)
								}
							} else {
								s.Movement.ClearPathFailure(h)
							}
						} else {
							s.Movement.ClearPathFailure(h)
						}
					}
				}
			}
			// slot-end death, cleanup, corpse, occupancy, target invalidation (FinalizeDeath + vis unpublish + Feature.PlaceCorpse) [01 §4.4][04 §2.4]
			if s.Units.NeedsDeathFinalization(h) {
				s.Units.FinalizeDeath(h, tick)
				// Every per-handle movement contribution, including the grid
				// stamp, goes with the slot [04 §8.2] C22.
				if s.Movement != nil {
					s.Movement.ForgetUnit(h)
				}
				// Path scheduler cancel for freed handle
				if s.Movement != nil && s.Movement.Scheduler != nil {
					s.Movement.Scheduler.Cancel(h)
				} else if s.Path != nil {
					s.Path.Cancel(h)
				}
			}
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
	s.interceptorGuidanceTick()
	// Snapshot hostile health before impact for AI milestone [P0-07] HostileDamageObserved
	// Deterministic slot-ordered snapshot [INVARIANTS I1][RS-P0-014]: use slice in slot-ascending order, not map[Handle]int32 with random range iteration.
	var beforeHealth []struct {
		Handle pool.Handle
		Health int32
	}
	if s.Combat != nil && s.Units != nil {
		beforeHealth = make([]struct {
			Handle pool.Handle
			Health int32
		}, 0, s.Units.Used())
		// Collect in deterministic slot-ascending order via IterSliced (player 0..9 then slot asc) [INVARIANTS I1][01 §6.1].
		// This replaces the previous map[Handle]int32 which used random map iteration to notify AI [RS-P0-014].
		for _, u := range s.Units.IterSliced() {
			if u == nil {
				continue
			}
			beforeHealth = append(beforeHealth, struct {
				Handle pool.Handle
				Health int32
			}{Handle: u.Handle, Health: u.Health})
		}
	}
	if s.Combat != nil {
		// [06 §6.4] plumb world wind vectors into ballistic/dropped drift
		s.Combat.TickProjectiles(tick, s.Units, s.World, s.Wind, s.Features, s.Vis, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
		// Notify AI of hostile damage via normal combat [P0-07] HostileDamageObserved in deterministic slot order [RS-P0-014][INVARIANTS I1].
		if len(beforeHealth) > 0 {
			for _, snap := range beforeHealth {
				h := snap.Handle
				before := snap.Health
				u := s.Units.Unit(h)
				if u == nil {
					continue
				}
				if u.Health >= before {
					continue
				}
				// Health decreased: damage occurred
				for _, mgr := range s.AI {
					if mgr == nil {
						continue
					}
					mgr.ObserveHostileDamage(tick, h, s.Units)
				}
			}
		}
	}
	s.interceptorDetonationTick()
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
	// 6 feature lifecycle and reclaim or death processing (burn, wind probes, successor hops; reclaim credits become visible at the next settlement) [01 §4.4][05 "Feature burning"][05 "Feature sinking"][06 §13.1]
	if s.Features != nil {
		s.Features.TickLifecycle(tick)
	}
}

// stepWindAndMeteorPhase is retained for legacy tests that call it directly.
// It delegates to the registry's phase 8 (wind) and phase 9 (meteor shower)
// [01 §4.4][R-CORE-01 §4.4.1] DET-03; it is a wrapper, not a second
// implementation.
func (s *Session) stepWindAndMeteorPhase(tick uint32) {
	s.phaseWind(tick)
	s.phaseMeteorShower(tick)
}

// stepSharingPhase is the transport tail after phase 12 [01 §4.4].
func (s *Session) stepSharingPhase(tick uint32) {
	// Sharing cadence is the transport tail after phase 12 [01 §4.4][05
	// "Allied resource and sensor sharing"].
	if s.Econ != nil {
		s.Econ.ShareTick(tick)
		// No map iteration inside ShareTick (it iterates players 0..9 asc)
	}
}

// stepCleanupAndResultPhase runs post-loop cleanup and result evaluation.
func (s *Session) stepCleanupAndResultPhase(tick uint32) {
	// Post-loop executor tail [01 §4.4] — barriers, deadline-ring slide and
	// missile/interceptor compaction are retail post-loop structures; nanolathe's
	// deterministic commit and RNG synchronization live here.
	if s.Units != nil {
		// RS-08: projectile damage after slot visit marks Dying after that visit [01 §4.4][GAP T15];
		// retain for feature/visibility/trigger same tick but clear movement occupancy before next tick and before frame.
		if s.Movement != nil {
			for _, u := range s.Units.IterSliced() {
				if u == nil || !u.Dying {
					continue
				}
				h := u.Handle
				s.Movement.ForgetUnit(h)
				if s.Movement.Scheduler == nil && s.Path != nil {
					s.Path.Cancel(h)
				}
			}
		}
		s.Units.Cleanup()
	}
	// Barrier no-op [01 §4.4] TODO(T23) keep as registration point
	// TODO(question): the fast no-human countdown site is multiplayer-only in
	// retail, but this single-player Session has no established multiplayer
	// mission-type mapping. Keep it out of all current mission types until the
	// session dispatcher and its authoritative gate are identified [08
	// "Evaluation"].
	// Result evaluation observes authoritative death finalization (hook EvaluateResult after ledger cleanup) [ON-09]
	// Configured lobby skirmish alliance-aware result. EvaluateResult selects
	// commander-only or all-live-unit survival from the lobby rule [08
	// "Skirmish configuration"].
	if s.Mission != nil && s.Mission.Type == mission.TypeSkirmish && s.Skirmish.NumPlayers > 0 {
		s.EvaluateResult(tick)
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
// Residual delta from [R-SENSOR-01]'s exact retail ordering: nanolathe's
// 30-tick victory/defeat polling runs in the per-player before-hook (before
// that player's work) rather than after the stamp sweep, and the per-tick
// minimap contacts pass and mapped-minimap rebuild are presentation-side and
// have no sim counterpart here. No economy after-hook is required — the
// position after stampPlayerSlice is session-owned loop body. The pass runs
// once per tick keyed to the local viewing slot; SensorTick owns the
// player-count gate.
func (s *Session) tickPlayers(tick uint32) {
	if s == nil || s.Econ == nil {
		return
	}
	for player := 0; player < 10; player++ {
		mgr := s.AI[player] // direct player-indexed access per RS-02 [08] I1
		// Bind per-session RNG for isolation [RS-06][I4] — ensure manager uses session's stream, not shared global.
		if mgr != nil && mgr.RNG == nil {
			mgr.RNG = s.SimRNG()
		}
		before := func() {
			if mgr != nil {
				mgr.Tick(tick, s.Units, s.Econ)
			}
			if player == int(s.LocalOwner) {
				s.pollMissionTriggers(tick)
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
		if player == int(s.LocalOwner) {
			s.stepSensorPhase(tick)
		}
	}
}

// pollMissionTriggers is the local player's phase-5 trigger site [08
// "Evaluation"]. Its deadline advances once by 30 when due, so a late record
// catches up one invocation per tick rather than looping. Direct-OTA defeat
// polling is gated by the authored local-side commander identity; an
// unavailable side mapping leaves defeat polling disabled [08 "Evaluation"].
func (s *Session) pollMissionTriggers(tick uint32) {
	// A configured lobby skirmish owns its end rule through SkirmishConfig,
	// not through the OTA mission-trigger queues. Direct OTA type-2 sessions
	// have no lobby players and retain the per-player trigger path below [08
	// "Evaluation"][08 "Skirmish configuration"].
	if s != nil && s.Mission != nil && s.Mission.Type == mission.TypeSkirmish && s.Skirmish.NumPlayers > 0 {
		return
	}
	if s == nil || s.Mission == nil || (len(s.Mission.Victory) == 0 && len(s.Mission.Defeat) == 0) {
		return
	}
	isDue := false
	if !s.triggerDueValid {
		s.triggerDue = tick
		s.triggerDueValid = true
	}
	if s.triggerDue <= tick {
		s.triggerDue += 30
		isDue = true
	}
	if isDue {
		ctx := triggers.PollContext{Tick: tick, World: s.Units, LocalOwner: s.LocalOwner, EnemyOwner: s.EnemyOwner}
		allowDefeat := s.Mission.Type == mission.TypeCampaign
		defeatFirst := false
		if !allowDefeat {
			// Direct OTA polls the defeat queue only after the local commander
			// marker has cleared. The live-unit predicate is the existing
			// CommanderKilled/EvaluateResult authority [08 "Evaluation"].
			markerClear, known := s.localCommanderMarkerClear()
			if known {
				allowDefeat = markerClear
				defeatFirst = markerClear
			} else {
				// TODO(question): establish the local side's commander identity
				// before enabling direct-OTA defeat polling; without the authored
				// side mapping, the marker state is unknown [08 "Evaluation"].
				allowDefeat = false
				defeatFirst = false
			}
		}
		v, d := evaluateMissionQueues(s.Mission, ctx, allowDefeat, defeatFirst)
		s.VictoryDone = s.VictoryDone || v
		s.DefeatDone = s.DefeatDone || d
		var latched bool
		if v {
			latched = s.Latch.AdvanceWin(true)
		} else if d {
			latched = s.Latch.AdvanceLose(true)
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		if latched && s.Latch.IsEnding() && s.State == StateBattle {
			win := s.Latch.IsWin()
			if s.Mission.Type == mission.TypeCampaign {
				s.Progress.ApplyCampaignResult(s.CampaignSlot, win)
			}
			if !s.result.Ended {
				kind := "defeat"
				if win {
					kind = "victory"
				}
				var winners, losers []int
				if win {
					winners = []int{int(s.LocalOwner)}
					losers = []int{int(s.EnemyOwner)}
				} else {
					winners = []int{int(s.EnemyOwner)}
					losers = []int{int(s.LocalOwner)}
				}
				scores := s.collectScores(winners[0], false)
				reason := "campaign"
				if win && len(s.Mission.Victory) > 0 {
					reason = "victory_trigger"
				} else if !win && len(s.Mission.Defeat) > 0 {
					reason = "defeat_trigger"
				}
				s.result = Result{Ended: true, Draw: false, Kind: kind, WinnerTeam: winners[0], Winners: winners, Losers: losers, Reason: reason, Tick: tick, ArmedTick: tick, Countdown: s.Latch.Countdown, Scores: scores}
			}
			_ = s.TransitionTo(StatePostBattle)
		}
	}
}

// localCommanderMarkerClear reports the direct-OTA gate and whether the
// authored side mapping was available. A live, non-dying local commander keeps
// the marker set; once none remains, the defeat queue may be polled. The
// matching identity comes from Catalog.Sides, while the live predicate follows
// trigger and result evaluation [08 "Evaluation"].
func (s *Session) localCommanderMarkerClear() (clear, known bool) {
	if s == nil || s.Catalog == nil || int(s.LocalOwner) >= len(s.Skirmish.Players) {
		return false, false
	}
	sideIndex := s.Skirmish.Players[int(s.LocalOwner)].Side
	if sideIndex < 0 || sideIndex >= len(s.Catalog.Sides) || s.Catalog.Sides[sideIndex] == nil {
		return false, false
	}
	commanderKey := content.CanonicalKey(s.Catalog.Sides[sideIndex].Commander)
	if commanderKey == "" {
		return false, false
	}
	if s.Units == nil {
		return true, true
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !u.Alive || u.Dying || u.Owner != s.LocalOwner {
			continue
		}
		if u.Def == nil {
			continue
		}
		definitionKey := u.Def.CanonicalKey
		if definitionKey == "" {
			definitionKey = content.CanonicalKey(u.Def.UnitName)
		}
		if definitionKey == commanderKey {
			return false, true
		}
	}
	return true, true
}

// evaluateMissionQueues polls both trigger arrays in builder order and
// combines their completion flags. Campaign polls victory then defeat and
// gives victory precedence. Direct OTA polls defeat first only after the local
// commander marker clears; otherwise it polls victory alone [08 "Evaluation"].
func evaluateMissionQueues(m *mission.Mission, c triggers.PollContext, allowDefeat, defeatFirst bool) (victoryDone, defeatDone bool) {
	if m == nil {
		return false, false
	}
	if allowDefeat && defeatFirst {
		for _, t := range m.Defeat {
			if t != nil {
				t.Poll(c)
			}
		}
	}
	for _, t := range m.Victory {
		if t != nil {
			t.Poll(c)
		}
	}
	if allowDefeat && !defeatFirst {
		for _, t := range m.Defeat {
			if t != nil {
				t.Poll(c)
			}
		}
	}
	victoryDone = len(m.Victory) > 0
	for _, t := range m.Victory {
		if t == nil || !t.Completed {
			victoryDone = false
			break
		}
	}
	if allowDefeat {
		for _, t := range m.Defeat {
			if t != nil && t.Completed {
				defeatDone = true
				break
			}
		}
	}
	if defeatFirst && defeatDone {
		victoryDone = false
	} else if victoryDone {
		defeatDone = false
	}
	return victoryDone, defeatDone
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
		s.stepAuthoritativePhases(tick)
		if s.State != StateBattle {
			break // latch armed->ending transitioned to postbattle same tick [P1-01 §2.2]
		}
	}
}
