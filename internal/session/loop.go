package session

import (
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

	Wind *world.Wind

	cadence uint32

	// OnRender is called exactly once per Step after the sub-tick batch, with
	// alpha from the final snapshot pair. It is presentation-only; sim never
	// reads it [PLAN_03 C15][PLAN_14 C6]. Tests set this to count renders.
	OnRender func(alpha float32)
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
		// features motion / effect-strip pre-pass [05 "Feature burning"] [01 §4.4]
		// The full Tick does reproduction + burning + sinking; phase 4 is motion only.
		// Call Tick for now; phase 6 handles lifecycle separation when split.
		if s.Features != nil {
			// reproduction walker is top of phase [06 §13.1]; burning/sinking follow.
			// To avoid double Tick when both phases 4 and 6 call Tick, only phase 4
			// drives it today; phase 6 is stubbed.
			s.Features.Tick(tick)
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

	// Phase 5b: path scheduler [04 §7.3]
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "path-scheduler", func(tick uint32) {
		if s.Path != nil {
			s.Path.Tick(tick)
		}
	})

	// Phase 5c: economy + AI coordinator (session-owned player loop) [05 "Authoritative settlement order"][08 "Established AI-facing data and rooted planner"][PLAN_11 C11]
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "economy-ai-coordinator", func(tick uint32) {
		s.coordinatePlayers(tick)
		if s.Econ != nil {
			s.Econ.ShareTick(tick)
		}
	})

	// Phase 5d: movement integration / occupancy commit [04 §8.1][04 §8.2]
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "movement-integrate", func(tick uint32) {
		if s.Movement != nil {
			s.Movement.Tick(tick, s.Units)
		}
		// Movement integration is immediate MoveRate / setSFXoccupy [GAP T15] I7.
		// The movement.System (PLAN_07) is the composition root that the kernel's
		// movement window calls each tick. Session does not yet own a Movement
		// field per PLAN_14 snippet; keep nil-safe placeholder.
		// Stub: if a movement system were bound, it would be called here:
		// s.Movement.Tick(tick, s.Units)
		_ = tick
	})

	// Phase 6: feature lifecycle, reclaim/death [01 §4.4]
	s.Kernel.Register(kernel.PhaseFeatureLifecycle, "feature-lifecycle", func(tick uint32) {
		// feature lifecycle, reclaim, death processing [05 "Removal and successor replacement"][06 §13.1]
		// Currently driven in phase 4 Tick; keep no-op to preserve ordering for future split.
		_ = tick
	})

	// Phase 7: sequence/effect-strip advancement [01 §4.4]
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
					views = append(views, snapshot.UnitView{
						Slot:           u.Handle,
						Owner:          u.Owner,
						X:              u.X,
						Y:              u.Y,
						Z:              u.Z,
						Health:         u.Health,
						MaxHealth:      u.MaxHealth,
						BuildRemaining: u.Remaining,
						Flags:          u.Flags,
					})
				}
				frame.Units = views
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
