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
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// stepAuthoritativePhases runs one complete authoritative tick for focused
// same-package tests and callers that already own the tick number. Step keeps
// the canonical sequence visible at its call site; this helper is deliberately
// only a phase-call wrapper, not a second implementation of any phase.
func (s *Session) stepAuthoritativePhases(tick uint32) {
	if s == nil {
		return
	}
	// The phase order below is the authoritative retail sequence [01 §4.4].
	// Keep these calls in this order: their pool visibility, side effects, and RNG
	// draw order are observable.
	_ = s.SimRNG()
	_ = s.CrtRNG()
	rng.Global.Sim = s.SimRNG()
	rng.Global.Crt = s.CrtRNG()

	// 1. network/input boundary (single-player drains due human commands).
	s.applyHumanCommands(tick)

	// 2. deterministic unit-slot sweep: unit update, weapons/COB, orders,
	// construction, movement, and slot-end death handling.
	s.stepUnitPhase(tick)

	// 3. projectiles: captured-span integration, collision, and detonation.
	s.stepProjectilePhase(tick)

	// 4. effects and feature motion.
	s.stepEffectPhase(tick)

	// 5. player orders/economy work and the phase-5 path boundary.
	s.stepPlayerPhase(tick)

	// 6. feature lifecycle and reclaim/death processing.
	s.stepFeatureLifecyclePhase(tick)

	// 7. sequence/effect-strip cursors. TODO(question): strip advancement is
	// presentation-owned in nanolathe [03 §1]; no sim state advances here.

	// 8/9. wind change, wind field, then meteor scheduling.
	s.stepWindAndMeteorPhase(tick)

	// 10. camera/scroll update. TODO(question): nanolathe's camera is
	// presentation-owned; no sim-side scroll target or shake state exists yet
	// [01 §4.4].

	// 11. ten object-list sweeps. TODO(question): the object family is not yet
	// identified [01 §4.4]; nothing registers lists yet.

	// 12. every-eight-sub-tick cadence flip. TODO(question): no consumer is
	// wired in nanolathe; the flip drives nothing yet.

	// Visibility/LOS and sensor refresh remain at this seam because their exact
	// relationship to the twelve phases is not established [03 §3.2][03 §3.4].
	// TODO(question): establish its placement relative to phase 12 and sharing.
	s.stepVisibilityPhase(tick)

	// Sharing is the transport tail after phase 12 [01 §4.4].
	s.stepSharingPhase(tick)

	// Post-loop cleanup and configured skirmish result evaluation.
	s.stepCleanupAndResultPhase(tick)

	// Synchronize the session streams back to the process-visible handles, then
	// publish exactly one committed frame for this completed sub-tick [I4][I6].
	if rng.Global.Sim != nil {
		*rng.Global.Sim = s.rngSim
	}
	if rng.Global.Crt != nil {
		*rng.Global.Crt = s.rngCrt
	}
	s.publishSnapshot(tick)
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
// "Authoritative settlement order"].
func (s *Session) stepPlayerPhase(tick uint32) {
	// 5 per-player orders, path, economy, and occupancy work [01 §4.4] — outer loop players 0..9 ascending; the AI coordinator tick (30-tick cadence, deadline-gated subtasks) runs before the settlement deadline compare and the nine-step settlement pass [05 "Authoritative settlement order"] [INVARIANTS I1].
	// Due AI player work at researched deadline relationship (beforeDeadline) + economy request/accept/settlement via economy.TickPlayer per player
	// No map-defined player order [ON-09].
	s.tickPlayers(tick)
	// Path requests are serviced once, at the phase-5 path boundary. Requests
	// submitted by the live unit sweep therefore publish routes for the next
	// unit sweep, while an already published route is consumed exactly once.
	if s.Movement != nil && s.Movement.Scheduler != nil {
		s.Movement.Scheduler.Tick(tick)
	} else if s.Path != nil {
		s.Path.Tick(tick)
	}
}

// stepFeatureLifecyclePhase is phase 6 of the authoritative tick [01 §4.4].
func (s *Session) stepFeatureLifecyclePhase(tick uint32) {
	// 6 feature lifecycle and reclaim or death processing (burn, wind probes, successor hops; reclaim credits become visible at the next settlement) [01 §4.4][05 "Feature burning"][05 "Feature sinking"][06 §13.1]
	if s.Features != nil {
		s.Features.TickLifecycle(tick)
	}
}

// stepWindAndMeteorPhase runs phases 8 and 9, preserving their separate RNG
// draws and the meteor scheduler's position after projectile integration [01
// §4.4][06 §6.5].
func (s *Session) stepWindAndMeteorPhase(tick uint32) {
	// 8/9 wind change and wind-field update [01 §4.4] — phase 8 draws the CRT interval and phase 9 the Sim strength/heading (order is behavior [I4]); projectiles, effects and features in earlier phases therefore read the previous tick's wind, as retail's phase order dictates. An earlier 'prepass at tick top' reading is superseded by the established order.
	if s.Wind != nil {
		// [01 §7.3] split: phase 8 draws CRT interval, phase 9 draws Sim strength/heading; order is behavior [INVARIANTS I4][RS-P0-018] per-session isolated
		s.Wind.Jitter(tick, s.CrtRNG())
		_ = s.Wind.Field(tick, s.SimRNG())
	}
	// Meteor scheduler initialization lazy: merge OTA meteor params with METEOR.TDF defaults [02 "Map files"] [08 "Meteor showers"] [06 §6.5].
	// Done once before first scheduling evaluation so scheduling draws start deterministically after wind.
	if !s.Meteor.Initialized {
		s.initMeteor()
	}

	// 9b Meteor scheduler after the wind phases so scheduling draws start deterministically after wind, and after the projectile phase so spawns move next tick [08 "Meteor showers"] [06 §6.5]. Cadence is interval+duration ticks per storm and per-hit delay trunc(30/density) [02 Meteor scheduler]; draws are CRT six per meteor (four scheduling even when disabled + two lateral) [06 §6.5] I4 with zero sim draws.
	// Cadence is interval+duration ticks per storm and per-hit delay trunc(30/density) [02 Meteor scheduler]; draws are CRT six per meteor (four scheduling even when disabled + two lateral) [06 §6.5] I4 with zero sim draws.
	s.tickMeteor(tick)
}

// stepVisibilityPhase refreshes LOS and the multi-player sensor state at the
// established seam after phase 12 [03 §3.2][03 §3.4].
func (s *Session) stepVisibilityPhase(tick uint32) {
	// Visibility/LOS/radar deadline work and publication is retained at this
	// seam because its exact relationship to the twelve runtime phases is not
	// established by the cited visibility contract [03 §3.2][03 §3.4].
	// TODO(question): establish the retail visibility placement relative to the
	// phase-12 cadence flip and sharing tail; the pass consumes no RNG here.
	if s.Vis != nil && s.Units != nil {
		// Visibility refresh throttled [03 §3.2] C6 — iterate deterministic
		for _, u := range s.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			hb := heightByteAt(u, seaLevelFor(s))
			cx, cz := observerTile(u, hb)
			r := radiusFor(u)
			s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz, HeightByte: hb, Radius: r})
		}
		// Sensor phase only when more than one player active [03 §3.4] P0-11
		active := s.activePlayerCount()
		if active > 1 {
			if s.visStatus == nil {
				s.visStatus = make(map[int]uint32)
			}
			if s.visDecloak == nil {
				s.visDecloak = make(map[int]uint32)
			}
			type holder struct {
				statusPtr *uint32
				deadPtr   *uint32
				handle    int
			}
			var holders []holder
			var sensorUnits []visibility.SensorUnit
			for _, u := range s.Units.IterSliced() {
				if u == nil || !u.Alive {
					continue
				}
				h := int(u.Handle)
				stVal := s.visStatus[h]
				dlVal := s.visDecloak[h]
				sp := new(uint32)
				*sp = stVal
				dp := new(uint32)
				*dp = dlVal
				holders = append(holders, holder{statusPtr: sp, deadPtr: dp, handle: h})
				hidden := (u.Flags & 0x04) != 0
				if !hidden && u.Def != nil && u.Def.InitCloaked {
					hidden = true
				}
				var rd, sd, rj, sj, mc int32
				if u.Def != nil {
					rd = u.Def.RadarDistance
					sd = u.Def.SonarDistance
					rj = u.Def.RadarDistanceJam
					sj = u.Def.SonarDistanceJam
					mc = u.Def.MinCloakDistance
				}
				sensorUnits = append(sensorUnits, visibility.SensorUnit{
					Owner:            visibility.PlayerID(u.Owner),
					Status:           sp,
					X:                u.X,
					Z:                u.Z,
					Y:                u.Y,
					Alive:            true,
					Hidden:           hidden,
					RadarDistance:    rd,
					SonarDistance:    sd,
					RadarJam:         rj,
					SonarJam:         sj,
					MinCloakDistance: mc,
					DecloakDeadline:  dp,
				})
			}
			allied := func(a, b visibility.PlayerID) bool {
				if a == b {
					return true
				}
				if s.Skirmish.NumPlayers > 0 {
					if int(a) < 10 && int(b) < 10 {
						return s.Skirmish.Players[a].AllyGroup == s.Skirmish.Players[b].AllyGroup
					}
				}
				return false
			}
			s.Vis.SensorTick(tick, active, allied, sensorUnits)
			for _, h := range holders {
				s.visStatus[h.handle] = *h.statusPtr
				s.visDecloak[h.handle] = *h.deadPtr
			}
		}
	}
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

// tickMeteor implements the shower scheduler per [08 "Meteor showers"] [02 Meteor scheduler] [06 §6.5].
// It runs after wind jitter and after projectile phase so spawns move next tick [08].
// Scheduling draws four CRT values every evaluation even when disabled (targetZ,X and origin offsets) [06 §6.5] I4;
// each active hit consumes two more for lateral radius/angle for six per meteor, zero sim draws [06 §6.5].
// Storms recur every interval+duration ticks with per-hit delay trunc(30/density), first hit on activation tick [02].
func (s *Session) tickMeteor(tick uint32) {
	if s == nil {
		return
	}
	crt := s.CrtRNG()
	if crt == nil {
		return
	}
	if !s.Meteor.Initialized {
		s.initMeteor()
		if !s.Meteor.Initialized {
			return
		}
	}
	if s.World == nil {
		return
	}
	mapW, mapH := s.World.CellW, s.World.CellH
	// Four scheduling-side draws consumed on every evaluation even when disabled [06 §6.5] I4.
	sampledTX, sampledTZ, sampledOX, sampledOZ := combat.MeteorSchedule(crt, mapW, mapH)
	if !s.Meteor.Enabled || s.Meteor.Weapon == nil {
		return
	}
	if !s.Meteor.Active {
		if tick < s.Meteor.NextStrike {
			return
		}
		s.Meteor.TargetX = sampledTX
		s.Meteor.TargetZ = sampledTZ
		s.Meteor.OriginX = sampledOX
		s.Meteor.OriginZ = sampledOZ
		s.Meteor.Active = true
		s.Meteor.StrikeEnds = tick + uint32(s.Meteor.DurationTicks)
		// Next storm at interval+duration per [02]; when duration zero, still interval.
		s.Meteor.NextStrike = s.Meteor.StrikeEnds + uint32(s.Meteor.IntervalTicks)
		s.Meteor.NextHit = tick
	}
	if tick > s.Meteor.StrikeEnds {
		s.Meteor.Active = false
		return
	}
	if tick < s.Meteor.NextHit {
		return
	}
	if s.Meteor.PerHitDelay > 0 {
		s.Meteor.NextHit = tick + uint32(s.Meteor.PerHitDelay)
	} else {
		s.Meteor.NextHit = tick + 1
	}
	if s.Combat != nil && s.Meteor.Weapon != nil {
		// Spawn uses stored storm target/origin plus two lateral CRT draws inside SpawnMeteor [06 §6.5].
		// Pool-full drops silently after advancing next hit, no retry [06 §6.5].
		// Meteor enters at 1350 wu altitude, 90-tick flight, fixed -15 vertical speed [06 §6.5].
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
		// The phase order below is the authoritative retail sequence [01 §4.4].
		// Keep these calls in this order: their pool visibility, side effects, and RNG
		// draw order are observable.
		_ = s.SimRNG()
		_ = s.CrtRNG()
		rng.Global.Sim = s.SimRNG()
		rng.Global.Crt = s.CrtRNG()

		// 1. network/input boundary (single-player drains due human commands).
		s.applyHumanCommands(tick)

		// 2. deterministic unit-slot sweep: unit update, weapons/COB, orders,
		// construction, movement, and slot-end death handling.
		s.stepUnitPhase(tick)

		// 3. projectiles: captured-span integration, collision, and detonation.
		s.stepProjectilePhase(tick)

		// 4. effects and feature motion.
		s.stepEffectPhase(tick)

		// 5. player orders/economy work and the phase-5 path boundary.
		s.stepPlayerPhase(tick)

		// 6. feature lifecycle and reclaim/death processing.
		s.stepFeatureLifecyclePhase(tick)

		// 7. sequence/effect-strip cursors. TODO(question): strip advancement is
		// presentation-owned in nanolathe [03 §1]; no sim state advances here.

		// 8/9. wind change, wind field, then meteor scheduling.
		s.stepWindAndMeteorPhase(tick)

		// 10. camera/scroll update. TODO(question): nanolathe's camera is
		// presentation-owned; no sim-side scroll target or shake state exists yet
		// [01 §4.4].

		// 11. ten object-list sweeps. TODO(question): the object family is not yet
		// identified [01 §4.4]; nothing registers lists yet.

		// 12. every-eight-sub-tick cadence flip. TODO(question): no consumer is
		// wired in nanolathe; the flip drives nothing yet.

		// Visibility/LOS and sensor refresh remain at this seam because their exact
		// relationship to the twelve phases is not established [03 §3.2][03 §3.4].
		// TODO(question): establish its placement relative to phase 12 and sharing.
		s.stepVisibilityPhase(tick)

		// Sharing is the transport tail after phase 12 [01 §4.4].
		s.stepSharingPhase(tick)

		// Post-loop cleanup and configured skirmish result evaluation.
		s.stepCleanupAndResultPhase(tick)

		// Synchronize the session streams back to the process-visible handles, then
		// publish exactly one committed frame for this completed sub-tick [I4][I6].
		if rng.Global.Sim != nil {
			*rng.Global.Sim = s.rngSim
		}
		if rng.Global.Crt != nil {
			*rng.Global.Crt = s.rngCrt
		}
		s.publishSnapshot(tick)
		if s.State != StateBattle {
			break // latch armed->ending transitioned to postbattle same tick [P1-01 §2.2]
		}
	}
}
