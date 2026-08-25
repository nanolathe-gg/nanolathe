package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Session is the canonical full Session per PLAN_14 Public API [08 "Session states"].
// State/dispatch fields (State, handlers, pendingBattle) are shared with state.go's
// eight-state machine C1-C4; the remaining fields are the authoritative simulation
// services owned centrally by this package C5.
// Go allows methods in any file, but the struct is defined once here.
type Session struct {
	State         State
	handlers      [8]func(*Session)
	pendingBattle bool

	Clock    *clock.State
	Kernel   *kernel.Kernel
	Catalog  *content.Catalog
	World    *world.Terrain
	Units    *units.World
	Vis      *visibility.Service
	Path     *path.Scheduler
	Econ     *economy.Service
	Build    *construction.Service
	Features *features.Service
	Movement *movement.System // Gate-5 integration: ground steering/routes [PLAN_14 C5 movement integration]
	Combat   *combat.Service
	AI       []*ai.Manager
	Mission  *mission.Mission
	Snapshot *snapshot.Buffer

	// Skirmish retains the lobby/setup values that selected this battle. The
	// placement and spawn paths consume Location and per-slot resources now;
	// the remaining round rules stay available to visibility/endgame wiring
	// without being silently replaced by map-global defaults.
	Skirmish SkirmishConfig

	// VictoryDone / DefeatDone latch the mission end conditions [08
	// "Evaluation"]. They are set by the trigger poll site below and are
	// one-way: a completed condition stays completed.
	VictoryDone bool
	DefeatDone  bool

	// Latch is the global end-of-mission countdown and win/lose bits
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// bits 0x04 ending 0x10/0x20 win 0x40 lose (lose clears win) [P1-01].
	// Latch never clears 0x04 once set [P1-01]. Settlement freeze gates
	// countdown<0 && NOT latched [P1-01 §2.2].
	Latch EndLatch

	// Progress holds campaign W/L and BetweenMissions persistence
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	Progress BankProgress

	Wind *world.Wind

	cadence uint32

	// OnRender is called exactly once per Step after the sub-tick batch, with
	// alpha from the final snapshot pair. It is presentation-only; sim never
	// reads it [PLAN_03 C15][PLAN_14 C6]. Tests set this to count renders.
	OnRender func(alpha float32)

	// Visibility sensor state [03 §3.4] P0-11: per-unit status bits (0x100 seen, 0x300 friendly, 0x1000 decloak)
	// and decloak deadlines tick+90, plus presentation-only jammer/radar surfaces.
	visStatus      map[int]uint32
	visDecloak     map[int]uint32
	sensorSurfaces *sensorSurfacesImpl
}

// ValidateComposition checks that every required authoritative service and
// cross-service port is non-nil and bound. It returns the first missing
// diagnostic and is the gate for P0-I01. [08 "Session states"] [01 §4.4]
func (s *Session) ValidateComposition() error {
	if s == nil {
		return fmt.Errorf("session: nil session [08 \"Session states\"]")
	}
	if s.Clock == nil {
		return fmt.Errorf("session: missing Clock [01 §4.4]")
	}
	if s.Kernel == nil {
		return fmt.Errorf("session: missing Kernel [01 §4.4]")
	}
	if s.Catalog == nil {
		return fmt.Errorf("session: missing Catalog [02 §5]")
	}
	if s.World == nil {
		return fmt.Errorf("session: missing World [03 §2.2]")
	}
	if s.Units == nil {
		return fmt.Errorf("session: missing Units [01 §6.1]")
	}
	if !s.Units.IsSliced() {
		return fmt.Errorf("session: Units not sliced retail [P0-16] [01 §6.1]")
	}
	if s.Econ == nil {
		return fmt.Errorf("session: missing Econ [05 \"Authoritative settlement order\"]")
	}
	if s.Features == nil {
		return fmt.Errorf("session: missing Features [05 \"Feature instance and terrain cell\"]")
	}
	if s.Features.Terrain != s.World {
		return fmt.Errorf("session: Features.Terrain mismatch [05]")
	}
	if s.Vis == nil {
		return fmt.Errorf("session: missing Vis [03 §3.2]")
	}
	if w, h := s.Vis.GridDimensions(); w == 0 || h == 0 {
		return fmt.Errorf("session: Vis zero dimensions [03 §3.1]")
	}
	if w, h := s.Vis.GridDimensions(); w != s.World.CellW/2 || h != s.World.CellH/2 {
		return fmt.Errorf("session: Vis dimensions %dx%d != terrain %dx%d/2 [03 §3.1]", w, h, s.World.CellW, s.World.CellH)
	}
	if s.Movement == nil {
		return fmt.Errorf("session: missing Movement [04 §8.1]")
	}
	if s.Movement.Terrain != s.World {
		return fmt.Errorf("session: Movement.Terrain mismatch [04 §8.1]")
	}
	if s.Movement.Classes == nil {
		return fmt.Errorf("session: Movement.Classes not bound [02 \"Movement class record\"]")
	}
	if s.Movement.Scheduler == nil {
		return fmt.Errorf("session: Movement.Scheduler nil [04 §7.3]")
	}
	if s.Path == nil {
		return fmt.Errorf("session: missing Path [04 §7.3]")
	}
	if s.Path != s.Movement.Scheduler {
		return fmt.Errorf("session: Path != Movement.Scheduler [04 §7.3]")
	}
	if s.Build == nil {
		return fmt.Errorf("session: missing Build [05 \"Factory production lifecycle\"]")
	}
	if s.Combat == nil {
		return fmt.Errorf("session: missing Combat [06 §5.1]")
	}
	if s.Mission == nil {
		return fmt.Errorf("session: missing Mission [08 \"Mission type dispatch\"]")
	}
	if s.Snapshot == nil {
		return fmt.Errorf("session: missing Snapshot [03 §2.4]")
	}
	if s.Wind == nil {
		return fmt.Errorf("session: missing Wind [01 §7.3]")
	}
	if s.AI == nil {
		return fmt.Errorf("session: missing AI slice [08 \"Established AI-facing data\"]")
	}
	return nil
}

// humanCount returns the number of human players (ControllerState==1)
// among economy slots [P1-01 §7.2] for the no-human post-loop path.
func (s *Session) humanCount() int {
	if s.Econ == nil {
		return 0
	}
	n := 0
	for i := 0; i < 10; i++ {
		p := &s.Econ.Players[i]
		if p.Exists && !p.IsObserver && p.ControllerState == 1 {
			n++
		}
	}
	return n
}

// activePlayerCount returns the number of active players (Exists && !IsObserver) [03 §3.4] P0-11.
// The sensor phase runs only when more than one player is active (activePlayers>1 via CMP 1 JBE skip).
func (s *Session) activePlayerCount() int {
	if s.Econ == nil {
		return 0
	}
	n := 0
	for i := 0; i < 10; i++ {
		p := &s.Econ.Players[i]
		if p.Exists && !p.IsObserver {
			n++
		}
	}
	return n
}

// IsVisible is the canonical gameplay LOS predicate [03 §3.2] C8 P0-11.
// It wraps visibility.Service.IsVisible with the session's local player and sea-level handling.
// Owner bypass, cloak, underwater (Y <= water), and no-allied-OR are preserved [03 §3.2] C9.
func (s *Session) IsVisible(viewer visibility.PlayerID, t visibility.Target) bool {
	if s.Vis == nil {
		return false
	}
	return s.Vis.IsVisible(viewer, t)
}

// IsUnitVisible reports whether target unit is visible to viewer via the canonical predicate [03 §3.2] C8.
// It builds a Target from the target unit's authoritative position, hull extents (zero for now),
// and sensor status (friendly/underwater/decloak bits).
func (s *Session) IsUnitVisible(viewer int, target *units.Unit) bool {
	if s == nil || s.Vis == nil || target == nil {
		return false
	}
	vid := visibility.PlayerID(viewer)
	tid := int(target.Handle)
	var status uint32
	if s.visStatus != nil {
		status = s.visStatus[tid]
	}
	hidden := (target.Flags & 0x04) != 0
	if !hidden && target.Def != nil && target.Def.InitCloaked {
		hidden = true
	}
	// Underwater exemption is stored as FriendlyMask 0x200 via sensor phase; we include it if present.
	t := visibility.Target{
		Owner:  visibility.PlayerID(target.Owner),
		X:      target.X,
		Y:      target.Y,
		Z:      target.Z,
		Hidden: hidden,
		Status: status,
	}
	return s.Vis.IsVisible(vid, t)
}

// RegisterAll centralizes subsystem registration in kernel phase order
// with a comment naming each phase (I7, PLAN_03 C7, [01 §4.4]).
// No package registers itself from init().
func (s *Session) RegisterAll() {
	if s.Kernel == nil {
		s.Kernel = &kernel.Kernel{}
	}
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	// Ensure single canonical scheduler: Session.Path is alias to Movement.Scheduler [04 §7.3][P0-I03].
	if s.Movement != nil && s.Movement.Scheduler != nil && s.Path != s.Movement.Scheduler {
		s.Path = s.Movement.Scheduler
	}
	if s.Path != nil && s.Movement != nil && s.Movement.Scheduler == nil {
		s.Movement.Scheduler = s.Path
	}
	// Death notifications feed the mission trigger queues exactly once
	// [08 "Evaluation"]: units.World fires the hook at the first Destroy
	// latch, which is the single fire point. Visibility unpublish is also handled here so the byte refcount plain
	// wraps 0→255 and word mask never decrements [03 §3.1] P0-11. The same hook also routes death into corpse placement via the features service [05 "Feature instance and terrain cell"][06 §13] C23.
	if s.Units != nil {
		prevHook := s.Units.OnDeath
		// Wrap or create hook to handle visibility unpublish, trigger notify, and corpse.
		s.Units.OnDeath = func(h pool.Handle, cause units.DeathCause, u *units.Unit) {
			if s.Vis != nil && u != nil {
				unpublishOne(s, u)
			}
			if s.Mission != nil && u != nil {
				ctx := triggers.PollContext{Tick: s.Clock.GlobalTick, World: s.Units, LocalOwner: 0, EnemyOwner: 1}
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitDied, u)
			} else if prevHook != nil {
				// If no mission, still delegate to previous hook if it was trigger hook (should not happen, but preserve)
			}
			if prevHook != nil && s.Mission == nil {
				// For non-mission path, prevHook may have been nil; avoid double notify.
				// If prevHook existed before our wrap, call it (it may be another visibility hook).
				// But we already handled unpublish and trigger; just ensure we don't lose it.
				// Detect if prevHook is our own earlier wrap by not calling again if s.Mission != nil (we already notified).
				if s.Mission == nil {
					prevHook(h, cause, u)
				}
			} else if prevHook != nil && s.Mission != nil {
				// If we wrapped an existing hook that was not trigger, we already did trigger; no need to call prevHook again
				// (it would have been visibility-only). Keep idempotent.
			}
			// Route into corpse feature with correct chain depth low nibble [06 §12.1] C23 [04 §5.1] [P0-I06].
			if s.Features != nil && u != nil && u.Def != nil && u.Def.Corpse != "" && s.World != nil {
				var depth uint8 = 0
				switch cause {
				case units.DeathKilled:
					depth = 1
				case units.DeathSelfDestruct:
					depth = 1
				case units.DeathReclaimed:
					depth = 0
				default:
					if u.Health <= 0 {
						depth = 1
					}
				}
				if s.Catalog != nil && s.Catalog.Features != nil {
					if corpseDef := features.CorpseDefFor(u.Def, s.Catalog.Features, depth); corpseDef != nil && depth != 0 {
						_ = s.Features.PlaceCorpse(u.X, u.Z, corpseDef, u.Def.IsFeature)
					} else if depth == 1 {
						if corpseDef := features.CorpseDefFor(u.Def, map[string]*content.FeatureDef{}, depth); corpseDef == nil {
							if d, ok := s.Catalog.Features[content.CanonicalKey(u.Def.Corpse)]; ok {
								s.Features.PlaceCorpse(u.X, u.Z, d, u.Def.IsFeature)
							}
						}
					}
				} else {
					for _, d := range s.World.FeatureDefs {
						if d != nil && d.CanonicalKey == content.CanonicalKey(u.Def.Corpse) {
							if depth != 0 {
								s.Features.PlaceCorpse(u.X, u.Z, d, u.Def.IsFeature)
							}
							break
						}
					}
				}
				_ = h
			}
		}
		// If there was a previous hook that we wrapped, ensure we preserve its behavior for non-visibility cases
		// (the above already handled trigger; for the case where prevHook was set before RegisterAll, we merged).
		_ = prevHook
	}

	// Phase 1: network drain — single-player no-op [01 §4.4]
	s.Kernel.Register(kernel.PhaseNetwork, "network-drain", func(tick uint32) {
		// single-player: no network drain [01 §4.4]
	})

	// Phase 2: unit and script/COB updates [01 §4.4]
	s.Kernel.Register(kernel.PhaseUnitsScripts, "units-tick", func(tick uint32) {
		if s.Units != nil {
			s.Units.Tick(tick)
		}
	})

	// Phase 3: projectile integration and collision [01 §4.4]
	s.Kernel.Register(kernel.PhaseProjectiles, "projectiles", func(tick uint32) {
		if s.Combat != nil {
			// combat motion, collision, impact — delegate to combat service.
			// The service is the sole projectile allocation authority [06 §5.1][I5].
			// Per [01 §6.2] the projectile phase captures the count at entry; tail
			// compaction reads the current count and is handled at phase tail.
			// Placeholder: no per-tick motion until WU-09-5; keep stub deterministic.
			s.Combat.ForEachAliveInEntrySpan(func(h pool.Handle, p *combat.Projectile) {
				// stub: projectile motion handled by combat/motion.go when wired
				_ = h
				_ = p
				_ = tick
			})
		}
	})

	// Phase 4: effects/features motion, compaction pre-pass [01 §4.4]
	s.Kernel.Register(kernel.PhaseEffectsFeatureMotion, "features-motion", func(tick uint32) {
		// Phase 4 is motion/prepass: reproduction walker is top of phase [06 §13.1] C25.
		// Lifecycle (burning, sinking) belongs in phase 6 per [01 §4.4] P0-I06.
		if s.Features != nil {
			s.Features.TickMotion(tick)
		}
	})

	// Phase 5: orders, path/economy, occupancy [01 §4.4]
	// Phase 5 is shared, and the order inside it matters [PLAN_03]. Both economy
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// that loop and calls economy.TickPlayer with AI as beforeDeadline [PLAN_11 C11]
	// so AI runs after per-tick helpers but before the deadline compare without an
	// economy↔ai import cycle.

	// Phase 5a: orders pump [04 §3.3]
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "orders-pump", func(tick uint32) {
		if s.Units == nil {
			return
		}
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			q := orders.QueueForUnit(u)
			if q != nil {
				q.Pump(u, tick)
			}
		}
	})

	// Phase 5a.6: construction/factory work [05 "Factory production lifecycle"][P0-I05].
	// Must run in the orders/build window BEFORE movement integration [04 §1.3][I7][P0-I05]:
	// orders-pump → construction work → movement. Construction exclusively owns
	// Remaining, health, resource admission, and completion [05].
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "construction-pump", func(tick uint32) {
		if s.Build == nil || s.Units == nil {
			return
		}
		// Deterministic iteration: player 0..9, slots asc, same as PumpAll [I1][05].
		// Use PumpAll when available to ensure lowest-slot wins for multi-builder
		// cooperation [05 "Construction arithmetic"].
		s.Build.PumpAll(tick)
	})

	// Phase 5a.5: path-submit — on path-backed order activation submit one
	// request so scheduler receives it and can produce a route [04 §7.3][P0-I03].
	// Must run after orders-pump (so the head is the newly activated order) and
	// before path-scheduler (so the request is visible this tick) [P0-I03].
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "path-submit", func(tick uint32) {
		if s.Units == nil || s.Movement == nil || s.Movement.Scheduler == nil {
			return
		}
		sched := s.Movement.Scheduler
		// Also keep alias coherent [P0-I03]
		s.Path = sched
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil || q.LenPrimary() == 0 {
				continue
			}
			head := q.Head()
			if head == nil {
				continue
			}
			name := orders.DescriptorFor(head.ID).Name
			isMove := name == "Move_Ground" || name == "VTOL_Move" || name == "QMove" || name == "Patrol" || name == "QPatrol" || name == "VTOL_Patrol" || name == "RepairPatrol" || name == "VTOL_RepairPatrol"
			if !isMove {
				// Also treat any order carrying a Goal when descriptor is move-class: generic fallback.
				if head.GoalX == 0 && head.GoalZ == 0 && head.Target == 0 {
					continue
				}
				if !isMove {
					// For non-move orders with a target unit, update goal to target's current pos and handle stale target.
					if head.Target != 0 {
						var tgt *units.Unit
						if q.Lookup != nil {
							tgt = q.Lookup(head.Target)
						}
						if tgt == nil && s.Units != nil {
							tgt = s.Units.Unit(head.Target)
						}
						if tgt == nil || !tgt.Alive {
							// Stale target: abandon the order [04 §3.5] missing target → Code 5
							head.MoveState = orders.MoveBlocked
							head.PathStatus = uint32(path.StatusRejected)
							// Remove head next time pump sees it? Do immediate removal via queue to avoid stuck head.
							// But we cannot remove while iterating pump's ordering? Do it now for move-like orders.
							// For generic attack/assist, let handler decide; for now just mark blocked and let pump handle.
							continue
						}
						// Target moved: update goal and invalidate route so repath occurs [P0-I03].
						if tgt.X != head.GoalX || tgt.Z != head.GoalZ {
							head.GoalX = tgt.X
							head.GoalY = tgt.Y
							head.GoalZ = tgt.Z
							if route := s.Movement.Routes[u.Handle]; route != nil && route.Active {
								route.Active = false
								route.Dirty = true
							}
							sched.Cancel(u.Handle)
						}
					}
					continue
				}
			}
			// Handle stale target for move-with-target (rare): if target set, use target's pos.
			if head.Target != 0 {
				var tgt *units.Unit
				if q.Lookup != nil {
					tgt = q.Lookup(head.Target)
				}
				if tgt == nil && s.Units != nil {
					tgt = s.Units.Unit(head.Target)
				}
				if tgt == nil || !tgt.Alive {
					head.MoveState = orders.MoveBlocked
					head.PathStatus = uint32(path.StatusRejected)
					q.RemoveHead()
					if route := s.Movement.Routes[u.Handle]; route != nil {
						route.Active = false
					}
					sched.Cancel(u.Handle)
					continue
				}
				if tgt.X != head.GoalX || tgt.Z != head.GoalZ {
					head.GoalX = tgt.X
					head.GoalY = tgt.Y
					head.GoalZ = tgt.Z
					if route := s.Movement.Routes[u.Handle]; route != nil && route.Active {
						route.Active = false
						route.Dirty = true
					}
					sched.Cancel(u.Handle)
				}
			}
			// If route already active for this goal, keep it; otherwise submit if no pending request.
			route := s.Movement.Routes[u.Handle]
			if route != nil && route.Active {
				// Check if route already covers goal: compare goal cell to last waypoint cell bias-adjusted?
				// For now if route active we assume it covers current goal; repath only on goal change above.
				continue
			}
			if sched.HasRequest(u.Handle) {
				continue
			}
			// No active route and no pending request: submit one PointGoal radius 0 [04 §7.2] C8 [P0-I03].
			startCell := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
			goalCell := path.Cell{X: world.WorldToCell(head.GoalX), Z: world.WorldToCell(head.GoalZ)}
			// Out-of-bounds goal is handled by search as rejected [04 §7.2] C10 → publish empty; keep order for retry.
			s.Movement.SubmitMove(u.Handle, u.Owner, startCell, goalCell)
			head.MoveState = orders.MoveEnRoute
		}
	})

	// Phase 5b: path scheduler [04 §7.3] — must be before movement-integrate [P0-I03].
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "path-scheduler", func(tick uint32) {
		var sched *path.Scheduler
		if s.Movement != nil && s.Movement.Scheduler != nil {
			sched = s.Movement.Scheduler
		} else {
			sched = s.Path
		}
		if sched != nil {
			sched.Tick(tick)
		}
		// Publish route status back to active order node [P0-I03].
		if s.Units == nil || s.Movement == nil {
			return
		}
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil || q.LenPrimary() == 0 {
				continue
			}
			head := q.Head()
			if head == nil {
				continue
			}
			name := orders.DescriptorFor(head.ID).Name
			isMove := name == "Move_Ground" || name == "VTOL_Move" || name == "QMove" || name == "Patrol" || name == "QPatrol"
			if !isMove {
				continue
			}
			route := s.Movement.Routes[u.Handle]
			if route != nil && route.Active {
				head.MoveState = orders.MoveEnRoute
				head.PathStatus = 0 // success
			} else if sched != nil && sched.HasRequest(u.Handle) {
				head.MoveState = orders.MoveEnRoute
			} else {
				// No active route and no pending: check if scheduler just published empty (rejected)
				// We treat as blocked; path-submit will retry on next opportunity [P0-I03].
				if head.MoveState != orders.MoveBlocked {
					// Only mark blocked if we attempted and got no route; keep en route otherwise to avoid flip.
				}
			}
		}
	})

	// Phase 5c: trigger evaluation before economy settlement [P1-01 §3]
	// Victory is an AND, defeat an OR, victory first; polled once per
	// 30 ticks in LOCAL player's slice only for mission type 1 [08
	// "Evaluation"][P1-01 §3]. This must run before settlement so the
	// latch freeze gates settlement same tick [P1-01 §2.2].
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "trigger-poll", func(tick uint32) {
		if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
			return
		}
		if len(s.Mission.Victory) == 0 && len(s.Mission.Defeat) == 0 {
			return
		}
		if tick%30 != 0 {
			return
		}
		ctx := triggers.PollContext{Tick: tick, World: s.Units, LocalOwner: 0, EnemyOwner: 1}
		v, d := triggers.Evaluate(s.Mission.Victory, s.Mission.Defeat, ctx)
		s.VictoryDone = s.VictoryDone || v
		s.DefeatDone = s.DefeatDone || d
		isDue := tick%30 == 0
		if v {
			s.Latch.AdvanceWin(isDue)
		} else if d {
			s.Latch.AdvanceLose(isDue)
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
	})

	// Phase 5d: economy + AI coordinator (session-owned player loop) [05 "Authoritative settlement order"][08 "Established AI-facing data and rooted planner"][PLAN_11 C11]
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "economy-ai-coordinator", func(tick uint32) {
		// Ensure latch propagation even when trigger poll didn't run (e.g., no triggers or not due)
		// so coordinator's settlement gate sees latest latch.
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		s.coordinatePlayers(tick)
		if s.Econ != nil {
			s.Econ.ShareTick(tick)
		}
	})

	// Phase 5d: movement integration / occupancy commit [04 §8.1][04 §8.2]
	// Scheduler tick runs before this in phase 5b [P0-I03]; route following
	// consumes the published points here. Completion, stale target, blocked and
	// repath handling publish status back to the order node [P0-I03][04 §7].
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "movement-integrate", func(tick uint32) {
		if s.Movement != nil {
			s.Movement.Tick(tick, s.Units)
		}
		if s.Units == nil || s.Movement == nil {
			return
		}
		var sched *path.Scheduler
		if s.Movement != nil {
			sched = s.Movement.Scheduler
		} else {
			sched = s.Path
		}
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil || q.LenPrimary() == 0 {
				continue
			}
			head := q.Head()
			if head == nil {
				continue
			}
			name := orders.DescriptorFor(head.ID).Name
			isMove := name == "Move_Ground" || name == "VTOL_Move" || name == "QMove" || name == "Patrol" || name == "QPatrol"
			if !isMove {
				continue
			}
			// Target movement / stale target handling [P0-I03][04 §3.5].
			if head.Target != 0 {
				var tgt *units.Unit
				if q.Lookup != nil {
					tgt = q.Lookup(head.Target)
				}
				if tgt == nil && s.Units != nil {
					tgt = s.Units.Unit(head.Target)
				}
				if tgt == nil || !tgt.Alive {
					head.MoveState = orders.MoveBlocked
					head.PathStatus = uint32(path.StatusRejected)
					q.RemoveHead()
					if route := s.Movement.Routes[u.Handle]; route != nil {
						route.Active = false
						route.Dirty = true
					}
					if sched != nil {
						sched.Cancel(u.Handle)
					}
					continue
				}
				if tgt.X != head.GoalX || tgt.Z != head.GoalZ {
					head.GoalX = tgt.X
					head.GoalY = tgt.Y
					head.GoalZ = tgt.Z
					if route := s.Movement.Routes[u.Handle]; route != nil && route.Active {
						route.Active = false
						route.Dirty = true
					}
					if sched != nil {
						sched.Cancel(u.Handle)
					}
					head.MoveState = orders.MoveEnRoute
					continue
				}
			}
			// Arrival check: dist ≤2 world units [04 §3.5][P0-I03] strict thresholds.
			dx := int64(head.GoalX) - int64(u.X)
			dz := int64(head.GoalZ) - int64(u.Z)
			// Skip completion for zero goal (should not happen for move, but guard).
			if head.GoalX == 0 && head.GoalZ == 0 && head.Target == 0 {
				continue
			}
			dist2 := dx*dx + dz*dz
			const threshFixed = 2 * 65536 // 2 world units
			const thresh2 = int64(threshFixed) * int64(threshFixed)
			if dist2 <= thresh2 {
				head.MoveState = orders.MoveArrived
				head.PathStatus = 0
				q.RemoveHead()
				if route := s.Movement.Routes[u.Handle]; route != nil {
					route.Active = false
					route.Dirty = true
				}
				if sched != nil {
					sched.Cancel(u.Handle)
				}
				continue
			}
			// Blocked route: no active route and no pending request but not arrived.
			// Mark blocked and let path-submit retry on its cadence (30+rand) [P0-I03].
			route := s.Movement.Routes[u.Handle]
			pending := false
			if sched != nil {
				pending = sched.HasRequest(u.Handle)
			}
			if (route == nil || !route.Active) && !pending {
				// If we previously tried and got rejected, the scheduler would have
				// published empty and cleared pending. Detect by checking that goal
				// is not satisfied but no route exists: treat as blocked.
				// Set status so HUD could show blocked; actual retry happens via path-submit.
				if head.MoveState != orders.MoveBlocked {
					head.MoveState = orders.MoveBlocked
					head.PathStatus = uint32(path.StatusRejected)
				}
			} else if route != nil && route.Active {
				head.MoveState = orders.MoveEnRoute
			}
		}
	})

	// Visibility refresh on movement — throttled [03 §3.2] C6. Refresh internally throttles to cell/2 or height delta >5.
	// TODO(question): exact retail refresh threshold is cell/2 and height >5 [03 §3.2] C6; we publish via Refresh which implements that,
	// but if the movement tick does not cross the threshold, coverage correctly stays. At least updates when cell/2 changes.
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "visibility-refresh", func(tick uint32) {
		if s.Vis == nil || s.Units == nil {
			return
		}
		for _, u := range s.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			cx := world.WorldToCell(u.X) / 2
			cz := world.WorldToCell(u.Z) / 2
			hb := heightByteFor(u)
			r := radiusFor(u)
			s.Vis.Refresh(visibility.ObserverID(u.Handle), visibility.Observer{Owner: visibility.PlayerID(u.Owner), CX: cx, CZ: cz, HeightByte: hb, Radius: r})
		}
		_ = tick
	})

	// Phase 6: feature lifecycle, reclaim/death [01 §4.4]
	s.Kernel.Register(kernel.PhaseFeatureLifecycle, "feature-lifecycle", func(tick uint32) {
		// Phase 6 is lifecycle: burning (smoke via CRT, spread via sim), successor
		// replacement and sinking [05 "Feature burning"][05 "Feature sinking and water interaction"][06 §13.1] P0-I06.
		if s.Features != nil {
			s.Features.TickLifecycle(tick)
		}
	})

	// Phase 7: sequence/effect-strip advancement and visibility/sensor phase [01 §4.4][03 §3.4] C11 C12 P0-11
	// Sensor phase runs only when more than one player is active (activePlayers>1 via CMP 1 JBE skip) [03 §3.4] P0-11.
	// It runs after movement so next tick's acquisition sees fresh positions, and before wind/effects.
	s.Kernel.Register(kernel.PhaseSequences, "visibility-sensors", func(tick uint32) {
		if s.Vis == nil || s.Units == nil {
			return
		}
		active := s.activePlayerCount()
		if active <= 1 {
			return // sensor gate [03 §3.4] P0-11
		}
		// Build sensor units deterministically: players 0..9 asc, slots asc [I1].
		// Status and decloak deadline are session-owned maps so mutations persist [03 §3.4] P0-11.
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
			// Skirmish ally groups when available [GAP T14]; otherwise only same owner.
			if s.Skirmish.NumPlayers > 0 {
				if int(a) < 10 && int(b) < 10 {
					return s.Skirmish.Players[a].AllyGroup == s.Skirmish.Players[b].AllyGroup
				}
			}
			// Fallback: check Econ alliance via controller? For now same owner only.
			return false
		}
		s.Vis.SensorTick(tick, active, allied, sensorUnits)
		// Write back mutated status/deadlines.
		for _, h := range holders {
			s.visStatus[h.handle] = *h.statusPtr
			s.visDecloak[h.handle] = *h.deadPtr
		}
	})
	s.Kernel.Register(kernel.PhaseSequences, "sequences", func(tick uint32) {
		// sequence/effect-strip advancement — driven by tick, renderer only reads [01 §4.4]
		// TODO(T25): effect strips not yet wired; presentation-only stub.
		_ = tick
	})

	// Phase 8: wind jitter / randomized interval [01 §4.4][01 §7.3]
	s.Kernel.Register(kernel.PhaseWindJitter, "wind-jitter", func(tick uint32) {
		if s.Wind != nil {
			if rng.Global.Crt != nil {
				s.Wind.Jitter(tick, rng.Global.Crt)
			}
		}
	})

	// Phase 9: wind-field update [01 §4.4][01 §7.3]
	s.Kernel.Register(kernel.PhaseWindField, "wind-field", func(tick uint32) {
		if s.Wind != nil {
			if rng.Global.Sim != nil {
				s.Wind.Field(tick, rng.Global.Sim)
			}
		}
	})

	// Phase 10: ledger/death cleanup [01 §4.4]
	s.Kernel.Register(kernel.PhaseLedgerCleanup, "ledger-cleanup", func(tick uint32) {
		// economy cleanup already via settlement; slot-end death handling [04 §2.4] C2
		if s.Units != nil {
			s.Units.Cleanup()
		}
		_ = tick
	})

	// Phase 11: ten-object vtable barrier — TODO(T23): consumers unidentified [01 §4.4]
	s.Kernel.Register(kernel.PhaseBarrier, "barrier", func(tick uint32) {
		// no-op barrier [01 §4.4] TODO(T23)
		_ = tick
	})

	// Post-loop no-human countdown site at 0x4655E8 when humanCount==0
	// [P1-01 §7.2]: decrements every tick, not per 30, so latch in 5
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// humanCount==0 [P1-01 §7.2]. Settlement still gated by same latch.
	s.Kernel.Register(kernel.PhaseBarrier, "countdown-nohuman", func(tick uint32) {
		if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
			return
		}
		if s.humanCount() != 0 {
			return
		}
		if s.Latch.Countdown < 0 && !s.Latch.IsEnding() {
			// Not armed and no victory/defeat predicate: no latch yet.
			// Retail post-loop site still arms to 4 when <0 before decrement
			// on next predicate? We only decrement if already armed or if
			// predicate would have armed. Keep armed check here.
			return
		}
		// If already armed (Countdown >=0) decrement every tick [P1-01 §7.2].
		if s.Latch.Countdown >= 0 {
			s.Latch.TickNoHuman()
			if s.Latch.Countdown < 0 {
				s.Latch.Bits |= LatchBitEnding
			}
			if s.Econ != nil {
				for i := 0; i < 10; i++ {
					s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
					s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
				}
			}
		}
	})

	// Phase 12: every-eight-sub-tick cadence flip [01 §4.4]
	s.Kernel.Register(kernel.PhaseCadenceFlip, "cadence-flip", func(tick uint32) {
		s.cadence++
		// snapshot publish after phase 12 of every completed sub-tick [PLAN_03 C15][PLAN_14 C6]
		if s.Snapshot != nil {
			frame := &snapshot.Frame{Tick: tick}
			if s.Units != nil {
				views := make([]snapshot.UnitView, 0, s.Units.Used())
				for _, u := range s.Units.Iter() {
					if u == nil || !u.Alive {
						continue
					}
					v := snapshot.UnitView{
						Slot:           u.Handle,
						Owner:          u.Owner,
						X:              u.X,
						Y:              u.Y,
						Z:              u.Z,
						Health:         u.Health,
						MaxHealth:      u.MaxHealth,
						BuildRemaining: u.Remaining,
						Flags:          u.Flags,
					}
					if u.Def != nil {
						v.Model = u.Def.ObjectName
						v.FootX = int8(u.Def.FootprintX)
						v.FootZ = int8(u.Def.FootprintZ)
					}
					views = append(views, v)
				}
				frame.Units = views
			}
			// Fog cache presentation copy [03 §3.3] C13.
			if s.Vis != nil {
				s.Vis.RebuildFog(0, 0)
				if fc := s.Vis.Fog(); fc != nil {
					w, h := fc.Dimensions()
					ch0, ch1 := fc.Channels()
					frame.Fog.W = w
					frame.Fog.H = h
					frame.Fog.Ch0 = ch0
					frame.Fog.Ch1 = ch1
					frame.Fog.Valid = fc.IsValid()
				}
			}
			if s.Features != nil {
				// Publish feature views in deterministic order (sorted keys) [I1][05 "Feature instance and terrain cell"] [P0-I06].
				insts := s.Features.Instances()
				fviews := make([]snapshot.FeatureView, 0, len(insts))
				for _, inst := range insts {
					if inst == nil || inst.Def == nil {
						continue
					}
					fv := snapshot.FeatureView{
						CX:        int32(inst.CX),
						CZ:        int32(inst.CZ),
						X:         inst.X,
						Y:         inst.Y,
						Z:         inst.Z,
						DefName:   inst.Def.CanonicalKey,
						Model:     inst.Def.Object,
						Health:    inst.Health,
						MaxHealth: inst.MaxHealth,
						IsBurning: inst.IsBurning,
						IsSinking: inst.IsSinking,
						BurnTicks: inst.BurnTicks,
						FootX:     int8(inst.FootprintX),
						FootZ:     int8(inst.FootprintZ),
					}
					if fv.Model == "" {
						fv.Model = inst.Def.Filename
					}
					fviews = append(fviews, fv)
				}
				frame.Features = fviews
			}
			s.Snapshot.Publish(frame)
		}
	})
}

// coordinatePlayers is the session-owned per-player loop that supplies AI
// callbacks to economy.TickPlayer per PLAN_11 C11 [05 "Authoritative settlement order"].
// It iterates players 0..9 ascending (I1), and for each player calls
// Econ.TickPlayer with a beforeDeadline closure that dispatches the AI manager
// after per-tick helpers but before the settlement deadline compare. The
// identification of AI as that auxiliary helper is supported inference from the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func (s *Session) coordinatePlayers(tick uint32) {
	if s.Econ == nil {
		// No economy: still dispatch AI directly per player in ascending order
		// so headless tests that construct managers without economy still tick.
		// This path is not the retail shape but keeps tests deterministic.
		for i := 0; i < 10; i++ {
			var m *ai.Manager
			if i < len(s.AI) {
				m = s.AI[i]
			}
			if m == nil {
				for _, cand := range s.AI {
					if cand != nil && int(cand.Player) == i {
						m = cand
						break
					}
				}
			}
			if m != nil {
				m.Tick(tick, s.Units, nil)
			}
		}
		return
	}
	for player := 0; player < 10; player++ {
		p := player
		var mgr *ai.Manager
		if p < len(s.AI) {
			mgr = s.AI[p]
		}
		if mgr == nil {
			for _, cand := range s.AI {
				if cand != nil && int(cand.Player) == p {
					mgr = cand
					break
				}
			}
		}
		before := func() {
			if mgr != nil {
				// supported inference: AI is the auxiliary player-level update [08][PLAN_11 C11]
				mgr.Tick(tick, s.Units, s.Econ)
			}
		}
		s.Econ.TickPlayer(p, tick, s.Units, before)
	}
}

// Step is the single-player tick loop per PLAN_14 C6-C7 [01 §4.2][01 §4.3][01 §4.4].
// It reads the time source (caller supplies scaledNow = floor(GetTickCount*30/1000) [01 §4.1]),
// calls clock.AdvanceSP(scaledNow) which short-circuits behind the SP pause gate
// (anchor stalls, unpause yields one capped burst ≤5) [01 §4.3] C7,
// runs 0..5 sub-ticks publishing after phase 12 of each completed sub-tick, and
// renders once with alpha from the final snapshot pair. Rendering never runs
// between sub-ticks of the same batch [PLAN_03 C15].
func (s *Session) Step(scaledNow int32) {
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	if s.Kernel == nil {
		s.Kernel = &kernel.Kernel{}
	}
	ticks := s.Clock.AdvanceSP(scaledNow)
	for i := 0; i < ticks; i++ {
		s.Kernel.SubTick(s.Clock)
	}
	// Render once with alpha from final snapshot pair [PLAN_03 C15][PLAN_03 C16][I6].
	// Alpha is presentation-only and computed in the client as
	// clamp((nowNanos - tickStartNanos)/tickPeriodNanos,0,1); here we use the clock's
	// fractional carry as a stable placeholder in [0,1) so tests can observe a value
	// without coupling to wall-clock. Sim never reads this value.
	if s.OnRender != nil {
		var alpha float32
		if s.Clock != nil {
			a := s.Clock.Carry
			if a < 0 {
				a = 0
			}
			if a > 1 {
				a = 1
			}
			if a != a { // NaN
				a = 0
			}
			alpha = a
		}
		s.OnRender(alpha)
	}
}
