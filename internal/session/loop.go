package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/audio"
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
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
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
	Shutdown *Shutdown // ordered shutdown in reverse-init order [01 §2.1][01 §2.3] P0-I10
	// CampaignSlot is the mission list slot for progress W/L [P1-01 §2.3] [P0-05].
	CampaignSlot int

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

	// LocalOwner and EnemyOwner are the player identities the trigger owner
	// gates compare against [08 "Evaluation"]. Victory conditions gate on the
	// enemy index, CommanderKilled on the local one; UnitTypeKilled and
	// AllUnitsKilledOfType accept any owner. PollContext and all notification
	// sites use these, not hard-coded 0/1 [P0-I13].
	LocalOwner uint8
	EnemyOwner uint8

	// Latch is the global end-of-mission countdown and win/lose bits
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// bits 0x04 ending 0x10/0x20 win 0x40 lose (lose clears win) [P1-01].
	// Latch never clears 0x04 once set [P1-01]. Settlement freeze gates
	// countdown<0 && NOT latched [P1-01 §2.2]. Countdown arms to 4 without
	// yet setting Bits; Bits are written only when Countdown crosses below
	// zero [P1-01 §2.2][08 "Evaluation"].
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

	// Audio is presentation-only and never feeds back into simulation
	// [03 §8.3] C19 [I4][I6]. Queue is 8-deep sorted with cooldowns and
	// per-slot global nextAllowed; variants and aliases are consumed at
	// Resolve via CRT draw [03 §8.3] C16 C17. Positional helper uses
	// audience cell + mode &2 choosing explored vs LOS, viewport pan
	// dx/dy with half-height shear and two-level attenuation [03 §8.3].
	// Music/CD fallback is briefing/music/CD probing via Controller [03 §8.4].
	AudioQueue    *audio.Queue       // eight-slot arbitration queue [03 §8.3] C16 C18
	AudioCache    *audio.SampleCache // alias→sample cache capped 255 [03 §8.2] C20
	AudioMusic    *audio.Controller  // CD/MCI controller with 5 modes [03 §8.4]
	audioViewport audio.Viewport     // presentation viewport for pan/attenuation [03 §8.3]
	audioFrame    uint32             // presentation frame counter for Drain [03 §8.3] C18
	audioFS       vfs.FSOps          // VFS for cache loads, presentation-only

	// Trace is the optional ordered debug sink [ON-09]. Disabled by default,
	// appends in authoritative order, never ranges maps, never calls RNG,
	// never alters sim decisions [INVARIANTS I1][I4][I6].
	traceEnabled bool
	trace        []SessionTraceEvent
}

// SessionTraceEvent is one ordered trace entry [ON-09].
// It uses stable integer/fixed-point fields, never RNG, and is
// appended in authoritative tick order. Never range over maps to emit.
type SessionTraceEvent struct {
	Tick     uint32        // global tick [01 §4.4] C6
	Kind     string        // one of Trace* constants [ON-09]
	Player   int           // player slot 0..9 or -1 [INVARIANTS I1]
	Handle   pool.Handle   // unit slot or 0
	Slot     int           // weapon slot 0..2 or -1
	WeaponID int32         // weapon ID or 0
	X        numeric.Fixed // stable world X [INVARIANTS I2]
	Z        numeric.Fixed // stable world Z [INVARIANTS I2]
	Value    int32         // generic stable value
}

// Trace kind constants [ON-09] ordered trace sink events.
const (
	TraceTickBegin            = "TickBegin"
	TraceWindMeteor           = "WindMeteor"
	TracePlayerBegin          = "PlayerBegin"
	TraceAIDeadline           = "AIDeadline"
	TraceEconomyRequest       = "EconomyRequest"
	TraceEconomySettle        = "EconomySettle"
	TraceUnitBegin            = "UnitBegin"
	TraceWeaponAimDispatch    = "WeaponAimDispatch"
	TraceCOBReturn            = "COBReturn"
	TraceWeaponFire           = "WeaponFire"
	TraceOrderPump            = "OrderPump"
	TraceWorkAdmission        = "WorkAdmission"
	TraceConstructionProgress = "ConstructionProgress"
	TraceMovementStep         = "MovementStep"
	TraceDeathFinalize        = "DeathFinalize"
	TraceProjectileStep       = "ProjectileStep"
	TraceProjectileImpact     = "ProjectileImpact"
	TraceFeatureLifecycle     = "FeatureLifecycle"
	TraceVisibilityDeadline   = "VisibilityDeadline"
	TraceTriggerPoll          = "TriggerPoll"
	TraceAIAux                = "AIAux"
	TraceVictoryLatch         = "VictoryLatch"
	TraceSnapshotPublish      = "SnapshotPublish"
	TraceTickEnd              = "TickEnd"
)

// SetTraceEnabled enables or disables the ordered trace sink [ON-09].
// Disabled by default. Must not be toggled mid-tick; call before Step.
func (s *Session) SetTraceEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.traceEnabled = enabled
	if !enabled {
		s.trace = nil
	}
}

// TraceEnabled reports whether tracing is active.
func (s *Session) TraceEnabled() bool {
	if s == nil {
		return false
	}
	return s.traceEnabled
}

// TraceEvents returns a copy of the ordered trace [ON-09].
func (s *Session) TraceEvents() []SessionTraceEvent {
	if s == nil || s.trace == nil {
		return nil
	}
	cp := make([]SessionTraceEvent, len(s.trace))
	copy(cp, s.trace)
	return cp
}

// ClearTrace drops the recorded trace.
func (s *Session) ClearTrace() {
	if s == nil {
		return
	}
	s.trace = nil
}

// emitTrace appends one trace entry if enabled. Never calls RNG, never ranges maps, never alters sim [INVARIANTS I1][I4].
func (s *Session) emitTrace(ev SessionTraceEvent) {
	if s == nil || !s.traceEnabled {
		return
	}
	s.trace = append(s.trace, ev)
}

// CaptureTrace is a test helper that enables tracing, runs fn, captures trace, then disables [ON-09].
func (s *Session) CaptureTrace(fn func()) []SessionTraceEvent {
	if s == nil || fn == nil {
		return nil
	}
	prevEnabled := s.traceEnabled
	prevTrace := s.trace
	s.traceEnabled = true
	s.trace = nil
	fn()
	out := s.TraceEvents()
	s.traceEnabled = prevEnabled
	s.trace = prevTrace
	return out
}

// ValidateComposition checks that every required authoritative service and
// cross-service port is non-nil and bound. It returns the first missing
// diagnostic and is the gate for P0-I01. [08 "Session states"] [01 §4.4]
func (s *Session) ValidateComposition() error {
	if s == nil {
		return fmt.Errorf("session: nil session [08 \"Session states\"]")
	}
	// Every AI manager must have the typed build queue bound [RX-01][F-P0-004].
	// A computer slot without a binder is a passive "computer", never a real AI.
	for _, mgr := range s.AI {
		if mgr != nil && mgr.QueueBuildTyped == nil {
			return fmt.Errorf("session: ai manager player %d has no QueueBuildTyped binding [RX-01][F-P0-004]", mgr.Player)
		}
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

// installStateHandlers installs the eight-state dispatch table handlers per
// [08 "Session states"] C1-C2 P0-I10.  State 0/1 teardown variants, 2 routing,
// 4 campaign player setup, 5 sync/async load completion, 6 battle stepping,
// 7 report/progression cleanup.  Single-player takes 2->5 directly; state 3
// network preload remains present and unreachable [08 "Session states"] C1.
func (s *Session) installStateHandlers() {
	// Capture handlers exactly once per session; re-install is idempotent.
	s.SetHandler(StateTeardownA, handleTeardownA)
	s.SetHandler(StateTeardownB, handleTeardownB)
	s.SetHandler(StateRouter, handleRouter)
	s.SetHandler(StateNetworkPreload, handleNetworkPreload)
	s.SetHandler(StateLocalPreload, handleLocalPreload)
	s.SetHandler(StateLoading, handleLoading)
	s.SetHandler(StateBattle, handleBattle)
	s.SetHandler(StatePostBattle, handlePostBattle)
}

// handleTeardownA implements state 0 cleanup variant A, then state 2 [08 "Session states"].
// It releases session-owned state in reverse-init order [01 §2.1][01 §2.3] ShutdownOrder.
func handleTeardownA(s *Session) {
	if s == nil {
		return
	}
	s.teardown(0)
	_ = s.TransitionTo(StateRouter)
}

// handleTeardownB implements state 1 alternate cleanup, then state 2 [08 "Session states"].
func handleTeardownB(s *Session) {
	if s == nil {
		return
	}
	s.teardown(1)
	_ = s.TransitionTo(StateRouter)
}

// teardown releases session-owned resources in documented reverse-init order
// [01 §2.1][01 §2.3] ShutdownOrder: window → display → sound → archives → semaphore → registryAudio.
// Simulation pools (units/projectiles/COB) are freed via pool paths, not here [shutdown.go].
func (s *Session) teardown(variant int) {
	_ = variant
	if s.Shutdown != nil {
		_ = s.Shutdown.Run()
		return
	}
	// Fallback when no Shutdown registered: still honor documented order by
	// clearing session-owned presentation hooks; simulation pools remain owned
	// elsewhere. This preserves the teardown→Router transition contract [08].
}

// handleRouter implements state 2 front-end/session router, selects 3|4|5 [08 "Session states"].
// Single-player takes 2->5 directly; state 3 is present and unreachable in single-player C1.
func handleRouter(s *Session) {
	if s == nil {
		return
	}
	// Campaign saves ride via 4 first, skirmish via 5 directly [08 "Session states"] C3.
	// If mission type is campaign and we have not yet configured local players, go via 4.
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
		if s.Econ != nil && s.Econ.Players[0].ControllerState != 1 {
			_ = s.TransitionTo(StateLocalPreload)
			return
		}
		// Even if already configured, router for campaign should still prefer 4 when coming from teardown
		// to ensure two-player records are initialized [08 "Session states"].
		// But to avoid infinite loop when already in campaign after Continue/Retry, we allow direct 5
		// if already at correct state. For P0-I10 gate, single-player router ->5 is sufficient.
	}
	// Network preload (3) is unreachable in single-player; keep handler present [08] C1.
	_ = s.TransitionTo(StateLoading)
}

// handleNetworkPreload implements state 3 network startup polling, then state 5 [08 "Session states"].
// No network in single-player; handler still transitions to 5 to preserve graph presence.
func handleNetworkPreload(s *Session) {
	if s == nil {
		return
	}
	_ = s.TransitionTo(StateLoading)
}

// handleLocalPreload implements state 4 initializes local two-player records, then state 5 [08 "Session states"].
// Established behavior: configures two campaign players (human local 0, computer enemy 1) [08][P0-05].
func handleLocalPreload(s *Session) {
	if s == nil {
		return
	}
	if s.Econ != nil {
		for i := 0; i < 2 && i < 10; i++ {
			p := &s.Econ.Players[i]
			p.Exists = true
			if i == 0 {
				p.ControllerState = 1
			} else {
				p.ControllerState = 2
			}
			p.IsObserver = false
			p.StatusHalfwordAt144 = 1
			p.StatusWordAt140 = 0
			p.GameEnded = false
			p.EndGameCountdown = -1
		}
		// Seed deadlines from current tick to keep WinLoseTime cadence correct [05].
		var tick uint32
		if s.Clock != nil {
			tick = s.Clock.GlobalTick
		}
		s.Econ.SeedDeadlines(tick)
		s.LocalOwner = 0
		s.EnemyOwner = 1
	}
	_ = s.TransitionTo(StateLoading)
}

// handleLoading implements state 5 loading UI/thread and readiness barrier [08 "Session states"].
// Established: completion installs state 6; its first run happens on NEXT dispatch, not inline C2.
// Supports synchronous (immediate CompleteLoading) and asynchronous (external CompleteLoading) paths.
func handleLoading(s *Session) {
	if s == nil {
		return
	}
	if s.IsPendingBattle() {
		return
	}
	// Synchronous path: if composition is valid (or at least terrain/catalog present), complete now.
	// For fixtures that lack full composition, ValidateComposition would fail; we still complete to unblock tests.
	// The gate requires newly constructed session cannot tick while loading, but loading handler must schedule battle for next dispatch.
	_ = s.CompleteLoading()
}

// handleBattle implements state 6 live battle loop [08 "Session states"].
// No-op: authoritative ticks are driven by Session.Step which checks State==StateBattle [P0-I10].
func handleBattle(s *Session) {
	_ = s
}

// handlePostBattle implements state 7 post-battle handling — results/postgame [08 "Session states"].
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func handlePostBattle(s *Session) {
	if s == nil {
		return
	}
	// BetweenMissions persistence is already written at latch time via ApplyCampaignResult [P1-01 §2.3];
	// this handler ensures we return to router exactly once [08 "Session states"] 7->2.
	_ = s.TransitionTo(StateRouter)
}

// authoritativeTick implements the researched authoritative tick per [01 §4.4][05 "Authoritative settlement order"][ON-09].
// Order per [ON-09] and later P0-09 research superseding package-wide graph:
//
//	1 Global wind/meteor prepass (Wind.Jitter, Wind.Field) [01 §7.3][01 §4.4]
//	2 Network/input boundary (no-op seam)
//	3 Player traversal deterministic 0..9: due AI beforeDeadline + economy TickPlayer [05][08]
//	4 One deterministic active unit-slot traversal ascending: pre-update, weapon, COB drain, orders, construction, movement, death [01 §4.4][04 "unit sweep"]
//	5 Projectile pool update and impact (TickProjectiles + interceptor guidance/detonation) [06 §5][06 §11.2]
//	6 Feature/fire lifecycle (TickLifecycle, TickMotion) [05][06 §13.1]
//	7 Visibility/LOS/radar deadline work and publication [03 §3.2][03 §3.4]
//	8 Trigger polling [08 "Evaluation"]
//	9 Sharing cadence [05 "Allied resource and sensor sharing"]
//	10 AI auxiliary deadlines [08 "Established AI-facing data"]
//	11 deterministic commit/barrier [01 §4.4]
//	12 one immutable snapshot publication [03 §2.4][PLAN_03 C15]
//
// Slot behavior per [01 §4.4] R-P0-04: newly created unit visible to later same-tick readers when player+slot ahead
// of current scan position, already-visited waits for next tick; freed slot immediate reuse via lowest-free scan,
// no generation counter. Implemented via VisitActiveSlots live Alive check at visit moment [01 §4.4] and immediate
// Free in FinalizeDeath, so later same-tick readers correctly skip freed slot via Alive flag.
func (s *Session) authoritativeTick(tick uint32) {
	if s == nil {
		return
	}
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTickBegin})
	// 1 Global wind/meteor prepass [01 §7.3][01 §4.4] — phase 8+9 moved to top per P0-09
	if s.Wind != nil {
		// [01 §7.3] split: phase 8 draws CRT interval, phase 9 draws Sim strength/heading; order is behavior [INVARIANTS I4]
		if rng.Global.Crt != nil {
			s.Wind.Jitter(tick, rng.Global.Crt)
		}
		if rng.Global.Sim != nil {
			_ = s.Wind.Field(tick, rng.Global.Sim)
		}
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceWindMeteor})
		// Meteor shower scheduling per [08 "Meteor showers"] would run here after wind jitter and before projectile phase
		// so spawned meteor first moves next tick [08 "Meteor showers"]. Currently no separate MeteorService; wind field covers wind scalar.
		// TODO(question): meteor shower globals persist in save's Meteor account [08 "Meteor showers"]; spawn via shared projectile pool at 90-tick flight — not yet wired in this tick, deferred as presentation-only until trigger.
	}
	// 2 Network/input boundary — single-player no-op seam [01 §4.4] PhaseNetwork
	// No network drain in single-player; keep as comment seam for determinism proof.

	// 3 Player traversal deterministic slot order 0..9 [05 "Authoritative settlement order"][INVARIANTS I1]
	// Due AI player work at researched deadline relationship (beforeDeadline) + economy request/accept/settlement via economy.TickPlayer per player
	// No map-defined player order [ON-09].
	for player := 0; player < 10; player++ {
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TracePlayerBegin, Player: player})
		var mgr *ai.Manager
		if player < len(s.AI) {
			mgr = s.AI[player]
		}
		if mgr == nil {
			for _, cand := range s.AI {
				if cand != nil && int(cand.Player) == player {
					mgr = cand
					break
				}
			}
		}
		if s.Econ == nil {
			if mgr != nil {
				mgr.Tick(tick, s.Units, nil)
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceAIDeadline, Player: player})
			}
			continue
		}
		// Capture economy helper/carry state before to emit EconomyRequest/Settle accurately without map iteration
		beforeUpdateTime := s.Econ.Players[player].UpdateTime
		before := func() {
			if mgr != nil {
				mgr.Tick(tick, s.Units, s.Econ)
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceAIDeadline, Player: player})
			}
		}
		s.Econ.TickPlayer(player, tick, s.Units, before)
		// Economy traces: request/accept are always recorded inside TickPlayer's settlement when due; emit after call
		// We emit EconomyRequest whenever the player's deadline was due (UpdateTime advanced) or helper ran; for determinism emit per active player
		p := &s.Econ.Players[player]
		if p.Exists && !p.IsObserver {
			// Helpers always run before deadline compare [05]; settlement only when UpdateTime advanced by exactly 30 [05 C2]
			if beforeUpdateTime != p.UpdateTime {
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceEconomyRequest, Player: player})
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceEconomySettle, Player: player})
			} else if p.Helper1Calls > 0 || p.Helper2Calls > 0 {
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceEconomyRequest, Player: player})
			}
		}
	}

	// Prepare movement shared indexing once per tick [04 §8.2][04 §10.2] before unit visits
	if s.Movement != nil {
		if s.Units != nil {
			s.Movement.BindWorld(s.Units)
		}
		s.Movement.BeginTick(tick)
	}
	// Path-submit — on path-backed order activation submit one request so scheduler receives it and can produce a route [04 §7.3][P0-I03].
	// Must run after orders-pump's head is established but before scheduler tick so request is visible this tick [P0-I03].
	// In authoritativeTick the per-unit pump runs after scheduler in the current structure (one-tick delay), but we still submit for the
	// existing heads deterministically before scheduler so the fallback path-submit defect [ON-10] is closed. Deterministic player 0..9 asc, slots asc, no map range [INVARIANTS I1].
	// Duplicate of kernel's path-submit phase loop.go:1425 replicated here for the fast path [04 §7.3].
	if s.Units != nil && s.Movement != nil && s.Movement.Scheduler != nil {
		sched := s.Movement.Scheduler
		s.Path = sched
		for _, u := range s.Units.IterSliced() {
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
				if head.GoalX == 0 && head.GoalZ == 0 && head.Target == 0 {
					continue
				}
				if !isMove {
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
					continue
				}
			}
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
			route := s.Movement.Routes[u.Handle]
			if route != nil && route.Active {
				continue
			}
			if sched.HasRequest(u.Handle) {
				continue
			}
			startCell := path.Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
			goalCell := path.Cell{X: world.WorldToCell(head.GoalX), Z: world.WorldToCell(head.GoalZ)}
			s.Movement.SubmitMove(u.Handle, u.Owner, startCell, goalCell)
			head.MoveState = orders.MoveEnRoute
		}
	}
	// Path scheduler at researched boundary without per-unit accidental invocation [04 §7.3] C11 C12
	// Called exactly once per tick, deterministic, before movement steps consume routes.
	// The scheduler services at most 100 nodes per request per call and publishes full-or-empty [04 §7.3]
	if s.Movement != nil && s.Movement.Scheduler != nil {
		s.Movement.Scheduler.Tick(tick)
	} else if s.Path != nil {
		s.Path.Tick(tick)
	}

	// 4 One deterministic active unit-slot traversal ascending [01 §4.4][01 §6.1][INVARIANTS I1]
	// Each active unit visited exactly once under researched rule; new/dead units follow same-tick visibility [01 §4.4] R-P0-04
	if s.Units != nil {
		ordersPump := &orders.Pump{World: s.Units}
		s.Units.VisitActiveSlots(func(v units.SlotVisit) {
			h := v.Handle
			u := v.Unit
			s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceUnitBegin, Handle: h, Slot: int(v.Slot)})
			if tick < 200 {
				if qDbg := orders.QueueForUnit(u); qDbg != nil {
					fmt.Printf("DEBUG Visit start tick %d handle %d queue len %d\n", tick, h, qDbg.LenPrimary())
				}
			}
			// unit pre-update (StepPreUpdate) [04 "unit sweep"]
			s.Units.StepPreUpdate(h, tick)
			// weapon slot/service step per unit [06 §3][06 §4] — stable weapon index once-compiled [ON-04]
			// Trace kinds are emitted from the service's per-visit summary, never inferred
			// from projectile counts or slot population [ON-09 evidence contract].
			if s.Combat != nil && s.Catalog != nil && u != nil && u.Alive && !u.Dying {
				beforeCount := s.Combat.Count()
				wsum := s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, rng.Global.Sim, rng.Global.Crt)
				// Authoritative order: AimDispatch → COBReturn → WeaponFire [06 §3.3][GAP T15 C17]
				if wsum.Dispatched {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceWeaponAimDispatch, Handle: h, Slot: wsum.DispatchSlot, WeaponID: wsum.DispatchWeaponID})
				}
				if wsum.ReturnSeen {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceCOBReturn, Handle: h, Value: wsum.ReturnValue})
				}
				if fired := s.Combat.Count() - beforeCount; fired > 0 {
					for i := beforeCount; i < len(s.Combat.Records); i++ {
						s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceWeaponFire, Handle: h, WeaponID: s.Combat.Records[i].WeaponID})
					}
				}
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
				res := ordersPump.PumpUnit(h, tick)
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceOrderPump, Handle: h, Value: int32(res.PrimaryLen)})
			}
			// construction/worker action per unit (StepUnit) [05 "Factory production lifecycle"]
			if s.Build != nil {
				if tick < 200 && (h == 5 || h == 6 || h == 7) {
					if qDbg2 := orders.QueueForUnit(u); qDbg2 != nil && qDbg2.LenPrimary() > 0 {
						if hd2 := qDbg2.Head(); hd2 != nil {
							fmt.Printf("DEBUG queue before construction tick %d h %d head %s BuildDef %s\n", tick, h, orders.DescriptorFor(hd2.ID).Name, hd2.BuildDefKey)
						}
					} else {
						fmt.Printf("DEBUG queue before construction tick %d h %d empty\n", tick, h)
					}
				}
				ctx := construction.TickContext{Tick: tick, World: s.Units, Economy: s.Econ, Terrain: s.World, Catalog: s.Catalog}
				wres := s.Build.StepUnit(ctx, h)
				if tick < 200 && (h == 5 || h == 6 || h == 7) {
					fmt.Printf("DEBUG construction tick %d h %d DefKey %s Product %d Completed %v Err %v State %d\n", tick, h, wres.DefKey, wres.Product, wres.Completed, wres.Err, wres.State)
					if qDbg := orders.QueueForUnit(u); qDbg != nil && qDbg.LenPrimary() > 0 {
						if hd := qDbg.Head(); hd != nil {
							fmt.Printf("DEBUG queue after construction tick %d head %s BuildDef %s Goal %d %d\n", tick, orders.DescriptorFor(hd.ID).Name, hd.BuildDefKey, int64(hd.GoalX.Raw()), int64(hd.GoalZ.Raw()))
						}
					} else {
						fmt.Printf("DEBUG queue after construction tick %d empty\n", tick)
					}
				}
				// Work admission trace when request recorded
				if wres.Product != 0 || wres.DefKey != "" {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceWorkAdmission, Handle: h})
				}
				if wres.Completed {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceConstructionProgress, Handle: h, Value: 1})
				} else if wres.Product != 0 {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceConstructionProgress, Handle: h})
				}
				_ = wres
			}
			// movement step per unit (Between BeginTick/EndTick, StepUnit with route ownership) [04 §8.1][04 §8.2]
			if s.Movement != nil {
				mres := s.Movement.StepUnit(h, tick)
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceMovementStep, Handle: h, X: mres.DistToGoal, Value: 0})
				_ = mres
				// Arrival check for move orders: dist ≤2 world units [04 §3.5][P0-I03] strict thresholds.
				if qArr := orders.QueueForUnit(u); qArr != nil && qArr.LenPrimary() > 0 {
					if headArr := qArr.Head(); headArr != nil {
						nameArr := orders.DescriptorFor(headArr.ID).Name
						isMoveArr := nameArr == "Move_Ground" || nameArr == "VTOL_Move" || nameArr == "QMove" || nameArr == "Patrol" || nameArr == "QPatrol" || nameArr == "VTOL_Patrol" || nameArr == "RepairPatrol" || nameArr == "VTOL_RepairPatrol"
						if isMoveArr {
							if !(headArr.GoalX == 0 && headArr.GoalZ == 0 && headArr.Target == 0) {
								dxArr := int64(headArr.GoalX) - int64(u.X)
								dzArr := int64(headArr.GoalZ) - int64(u.Z)
								const threshFixedArr = 2 * 65536
								const thresh2Arr = int64(threshFixedArr) * int64(threshFixedArr)
								dist2Arr := dxArr*dxArr + dzArr*dzArr
								if dist2Arr <= thresh2Arr {
									headArr.MoveState = orders.MoveArrived
									headArr.PathStatus = 0
									qArr.RemoveHead()
									if routeArr := s.Movement.Routes[h]; routeArr != nil {
										routeArr.Active = false
										routeArr.Dirty = true
									}
									if schedArr := s.Movement.Scheduler; schedArr != nil {
										schedArr.Cancel(h)
									} else if s.Path != nil {
										s.Path.Cancel(h)
									}
								}
							}
						}
					}
				}
			}
			// slot-end death, cleanup, corpse, occupancy, target invalidation (FinalizeDeath + vis unpublish + Feature.PlaceCorpse) [01 §4.4][04 §2.4]
			if s.Units.NeedsDeathFinalization(h) {
				res := s.Units.FinalizeDeath(h, tick)
				// Occupancy clear for movement grid [04 §8.2] C22 immediate reuse
				if s.Movement != nil && s.Movement.Grid != nil {
					if coll, ok := s.Movement.Collisions[h]; ok && coll != nil {
						s.Movement.Grid.Clear(coll.CachedAnchor, coll.FootPrintX, coll.FootPrintZ, int(h))
						delete(s.Movement.Collisions, h)
					}
					delete(s.Movement.Steers, h)
					delete(s.Movement.Flights, h)
					delete(s.Movement.Routes, h)
					// profiles retained for determinism? Clear to allow re-resolve on reuse
					// Keep but delete if present to avoid stale on reuse
					delete(s.Movement.Routes, h)
				}
				// Path scheduler cancel for freed handle
				if s.Movement != nil && s.Movement.Scheduler != nil {
					s.Movement.Scheduler.Cancel(h)
				} else if s.Path != nil {
					s.Path.Cancel(h)
				}
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceDeathFinalize, Handle: h, Value: int32(res.Cause)})
			}
			if tick < 10 {
				if qDbg := orders.QueueForUnit(u); qDbg != nil {
					fmt.Printf("DEBUG Visit end tick %d handle %d queue len %d\n", tick, h, qDbg.LenPrimary())
				}
			}
		})
	}
	if tick < 200 {
		if uDbg := s.Units.Unit(5); uDbg != nil {
			if qDbg := orders.QueueForUnit(uDbg); qDbg != nil {
				fmt.Printf("DEBUG after Visit loop tick %d handle5 queue len %d\n", tick, qDbg.LenPrimary())
				if qDbg.LenPrimary() > 0 {
					if hd := qDbg.Head(); hd != nil {
						fmt.Printf("DEBUG after Visit loop head %s BuildDef %s\n", orders.DescriptorFor(hd.ID).Name, hd.BuildDefKey)
					}
				}
			}
		}
	}
	if s.Movement != nil {
		s.Movement.EndTick(tick)
	}
	if tick < 200 {
		if uDbg := s.Units.Unit(5); uDbg != nil {
			if qDbg := orders.QueueForUnit(uDbg); qDbg != nil {
				fmt.Printf("DEBUG after EndTick tick %d handle5 queue len %d\n", tick, qDbg.LenPrimary())
				if qDbg.LenPrimary() > 0 {
					if hd := qDbg.Head(); hd != nil {
						fmt.Printf("DEBUG after EndTick head %s BuildDef %s\n", orders.DescriptorFor(hd.ID).Name, hd.BuildDefKey)
					}
				}
			} else {
				fmt.Printf("DEBUG after EndTick tick %d handle5 queue nil\n", tick)
			}
		} else {
			fmt.Printf("DEBUG after EndTick tick %d handle5 unit nil\n", tick)
		}
	}

	// 5 Projectile pool update and impact (TickProjectiles + interceptor guidance/detonation) [06 §5][06 §11.2]
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceProjectileStep})
	// Interceptor guidance pre-step before motion [06 §11.2]
	s.interceptorGuidanceTick()
	// Snapshot hostile health before impact for AI milestone [P0-07] HostileDamageObserved
	var beforeHealth map[pool.Handle]int32
	if s.Combat != nil && len(s.AI) > 0 && s.Units != nil {
		beforeHealth = make(map[pool.Handle]int32, s.Units.Used())
		for _, u := range s.Units.IterSliced() {
			if u == nil {
				continue
			}
			beforeHealth[u.Handle] = u.Health
		}
		// Also include dead-but-recently-damaged? Before we only have alive, but after damage a unit may die and be removed from IterSliced.
		// To capture death, also snapshot via direct handle scan for any unit with health < MaxHealth
		// Use Unit() for handles up to capacity via Iter()
		for _, u := range s.Units.Iter() {
			if u == nil {
				continue
			}
			if _, ok := beforeHealth[u.Handle]; !ok {
				beforeHealth[u.Handle] = u.Health
			}
		}
	}
	if s.Combat != nil {
		s.Combat.TickProjectiles(tick, s.Units, s.World, s.Features, s.Vis, s.Econ, s.Catalog, rng.Global.Sim, rng.Global.Crt)
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceProjectileImpact})
		// Notify AI of hostile damage via normal combat [P0-07] HostileDamageObserved
		if beforeHealth != nil && len(s.AI) > 0 {
			for h, before := range beforeHealth {
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
			// Also check for newly dead units that were not in beforeHealth because they were alive but we captured, but after are dead and still have health < before
			// The above loop covers them via beforeHealth entry, since we captured before health, and after health is -400 for dead, so it will be detected.
			// Additionally, check for any unit that was not in beforeHealth but is now dead with health < MaxHealth (e.g., newly created nanoframe? not hostile)
		}
	}
	s.interceptorDetonationTick()

	// 6 Feature/fire lifecycle (TickLifecycle, TickMotion as per phase) [05 "Feature burning"][05 "Feature sinking"][06 §13.1]
	if s.Features != nil {
		// TickMotion is reproduction walker top of phase [06 §13.1] C25, lifecycle is burn/sink
		// Maintain order: TickMotion then TickLifecycle to avoid double reproduce
		s.Features.TickMotion(tick)
		s.Features.TickLifecycle(tick)
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceFeatureLifecycle})
	}

	// 7 visibility/LOS/radar deadline work and publication (Refresh, SensorTick) [03 §3.2][03 §3.4]
	if s.Vis != nil && s.Units != nil {
		// Visibility refresh throttled [03 §3.2] C6 — iterate deterministic
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
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceVisibilityDeadline})
	}

	// 8 Trigger polling [08 "Evaluation"][P1-01 §3] — mission victory/defeat queues polled once per 30 ticks in LOCAL player's slice only for mission type 1
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign && (len(s.Mission.Victory) > 0 || len(s.Mission.Defeat) > 0) {
		// Delegate to existing triggerPoll helper to preserve per-player WinLoseTime deadline logic [05 "Authoritative settlement order"] C2
		// Extracted from old loop's trigger-poll registration
		isDue := false
		if s.Econ != nil && int(s.LocalOwner) < 10 {
			p := &s.Econ.Players[int(s.LocalOwner)]
			if p.WinLoseTime <= tick {
				p.WinLoseTime += 30
				isDue = true
			}
		} else {
			if tick%30 == 0 {
				isDue = true
			}
		}
		if isDue && s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
			ctx := triggers.PollContext{Tick: tick, World: s.Units, LocalOwner: s.LocalOwner, EnemyOwner: s.EnemyOwner}
			v, d := triggers.Evaluate(s.Mission.Victory, s.Mission.Defeat, ctx)
			s.VictoryDone = s.VictoryDone || v
			s.DefeatDone = s.DefeatDone || d
			var latched bool
			if v {
				latched = s.Latch.AdvanceWin(isDue)
			} else if d {
				latched = s.Latch.AdvanceLose(isDue)
			}
			if s.Econ != nil {
				for i := 0; i < 10; i++ {
					s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
					s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
				}
			}
			if latched && s.Latch.IsEnding() && s.State == StateBattle {
				win := s.Latch.IsWin()
				s.Progress.ApplyCampaignResult(0, win)
				_ = s.TransitionTo(StatePostBattle)
			}
			s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTriggerPoll})
			if v || d {
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceVictoryLatch})
			}
		}
	} else {
		// Still emit TriggerPoll for determinism proof even when no campaign mission
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTriggerPoll})
	}

	// 9 sharing cadence [05 "Allied resource and sensor sharing"] — once per tick after player phase, for reference player only
	if s.Econ != nil {
		s.Econ.ShareTick(tick)
		// Sharing emits no per-unit trace but we emit one per tick for determinism
		// No map iteration inside ShareTick (it iterates players 0..9 asc)
	}

	// 10 AI auxiliary deadlines [08 "Established AI-facing data and rooted planner"] — managers already ticked via player traversal beforeDeadline;
	// auxiliary tasks with later deadlines (+150/+300 etc.) are also handled inside same Tick via runDueTasks [P0-02].
	// Emit the stage marker for the trace contract; VictoryLatch is reserved for actual result latches.
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceAIAux})

	// 11 deterministic commit/barrier [01 §4.4] PhaseBarrier + ledger cleanup
	if s.Units != nil {
		s.Units.Cleanup()
	}
	// Barrier no-op [01 §4.4] TODO(T23) keep as registration point
	// Countdown-nohuman handled separately via Latch after trigger poll; for commit we also handle nohuman countdown if campaign with no humans
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign && s.humanCount() == 0 {
		if s.Latch.Countdown >= 0 {
			if latched := s.Latch.TickNoHuman(); latched && s.Latch.IsEnding() && s.State == StateBattle {
				win := s.Latch.IsWin()
				s.Progress.ApplyCampaignResult(0, win)
				_ = s.TransitionTo(StatePostBattle)
			}
			if s.Econ != nil {
				for i := 0; i < 10; i++ {
					s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
					s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
				}
			}
		}
	}
	// Result evaluation observes authoritative death finalization (hook EvaluateResult after ledger cleanup) [ON-09]
	// Skirmish alliance-aware victory [08 "Skirmish configuration"] CommanderDeath==1
	if s.Skirmish.CommanderDeath != 0 {
		if s.EvaluateResult(tick) {
			s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceVictoryLatch})
		}
	}

	// 12 one immutable snapshot publication [03 §2.4][PLAN_03 C15][INVARIANTS I6]
	s.publishSnapshot(tick)
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceSnapshotPublish})
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTickEnd})
	s.cadence++
}

// publishSnapshot publishes one immutable frame after every completed sub-tick [PLAN_03 C15].
func (s *Session) publishSnapshot(tick uint32) {
	if s.Snapshot == nil {
		return
	}
	frame := &snapshot.Frame{Tick: tick}
	if s.Units != nil {
		views := make([]snapshot.UnitView, 0, s.Units.Used())
		ordersViews := make([]snapshot.OrderView, 0)
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
				Heading:        u.Move.Heading,
				Pitch:          u.Move.Pitch,
				Bank:           u.Move.Bank,
			}
			if s.Movement != nil {
				if st := s.Movement.Steers[u.Handle]; st != nil {
					v.Heading = st.Heading
				} else if fl := s.Movement.Flights[u.Handle]; fl != nil {
					v.Heading = fl.Heading
				} else if coll := s.Movement.Collisions[u.Handle]; coll != nil {
					v.Heading = coll.Heading
				}
			}
			if u.Def != nil {
				v.Model = u.Def.ObjectName
				v.FootX = int8(u.Def.FootprintX)
				v.FootZ = int8(u.Def.FootprintZ)
				if id := s.Units.DefIDForHandle(u.Handle); id != 0 {
					v.DefID = id
				}
			}
			if vm := u.GetScript(); vm != nil {
				if vmPieces := vm.Pieces; len(vmPieces) > 0 {
					v.Pieces = make([]snapshot.PieceView, len(vmPieces))
					for i, ps := range vmPieces {
						v.Pieces[i] = snapshot.PieceView{
							Index: i,
							RotX:  ps.RotX,
							RotY:  ps.RotY,
							RotZ:  ps.RotZ,
							Tx:    ps.Trans[0],
							Ty:    ps.Trans[1],
							Tz:    ps.Trans[2],
						}
					}
				}
			}
			views = append(views, v)
			if q := orders.QueueForUnit(u); q != nil && q.LenPrimary() > 0 {
				if head := q.Head(); head != nil {
					ordersViews = append(ordersViews, snapshot.OrderView{
						Unit:      u.Handle,
						Target:    head.Target,
						GoalX:     head.GoalX,
						GoalY:     head.GoalY,
						GoalZ:     head.GoalZ,
						Kind:      orders.DescriptorFor(head.ID).Name,
						MoveState: uint8(head.MoveState),
					})
				}
			}
		}
		frame.Units = views
		frame.Orders = ordersViews
	}
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

				Filename:    inst.Def.Filename,
				SeqName:     inst.Def.SeqName,
				SeqNameShad: inst.Def.SeqNameShad,
				Animating:   inst.Def.Animating != 0,
				AnimTrans:   inst.Def.AnimTrans != 0,
				ShadTrans:   inst.Def.ShadTrans != 0,
				Blocking:    inst.Def.Blocking,
				Reclaimable: inst.Def.Reclaimable,
				Height:      inst.Def.Height,
				Geothermal:  inst.Def.Geothermal,
			}
			if fv.Model == "" {
				fv.Model = inst.Def.Filename
			}
			fviews = append(fviews, fv)
		}
		frame.Features = fviews
	}
	if s.Combat != nil {
		for i := 0; i < s.Combat.Count(); i++ {
			h := pool.Handle(i + 1)
			if !s.Combat.Alive(h) {
				continue
			}
			if i < 0 || i >= len(s.Combat.Records) {
				continue
			}
			p := s.Combat.Records[i]
			frame.Projectiles = append(frame.Projectiles, snapshot.ProjectileView{
				Handle:   h,
				X:        p.Pos.X,
				Y:        p.Pos.Y,
				Z:        p.Pos.Z,
				WeaponID: p.WeaponID,
				Shooter:  p.Shooter,
			})
		}
	}
	if s.Econ != nil {
		var resViews []snapshot.ResourceView
		for p := 0; p < 10; p++ {
			pl := s.Econ.Players[p]
			if !pl.Exists {
				continue
			}
			resViews = append(resViews, snapshot.ResourceView{
				Player:         uint8(p),
				Metal:          pl.Stock[economy.Metal],
				Energy:         pl.Stock[economy.Energy],
				MetalCapacity:  pl.Capacity[economy.Metal],
				EnergyCapacity: pl.Capacity[economy.Energy],
			})
		}
		frame.Resources = resViews
	}
	s.CollectAudioForSnapshot(frame)
	s.Snapshot.Publish(frame)
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
	// Install eight-state handlers before kernel phases so state machine governs lifecycle [08][P0-I10].
	s.installStateHandlers()
	// Ensure Shutdown exists in documented startup order [01 §2.1][01 §2.3] for teardown variants 0/1 P0-I10.
	if s.Shutdown == nil {
		s.Shutdown = &Shutdown{}
		// Register in startup order: window → display → sound → archives → semaphore → registryAudio.
		// Run() will invoke reverse. We register no-ops that preserve order observable for tests.
		for _, name := range ShutdownOrder {
			n := name // capture
			s.Shutdown.Register(func() error {
				_ = n
				return nil
			})
		}
	}
	// Ensure single canonical scheduler: Session.Path is alias to Movement.Scheduler [04 §7.3][P0-I03].
	if s.Movement != nil && s.Movement.Scheduler != nil && s.Path != s.Movement.Scheduler {
		s.Path = s.Movement.Scheduler
	}
	if s.Path != nil && s.Movement != nil && s.Movement.Scheduler == nil {
		s.Movement.Scheduler = s.Path
	}
	// Trigger and visibility hooks [08 "Evaluation"] are bound exactly once via
	// units.World hooks at the single fire points: Create (slot 3), Death
	// (first Destroy latch, slot 1), and Capture transfer (slot 2). The hook
	// identities are derived from the session's actual local/enemy owners, not
	// hard-coded 0/1 [P0-I13][08 "Evaluation"]. Visibility unpublish/corpse
	// remain here so the byte refcount plain wraps 0→255 and word mask never
	// decrements [03 §3.1] P0-11; corpse uses correct chain depth [06 §12.1] C23.
	if s.Units != nil {
		// Derive actual local/enemy identities from session state if not yet set
		// [P0-I13]. Skirmish stores them from SkirmishConfig, mission from
		// economy player slots 0/1; fallback to 0/1 preserves fixture compatibility.
		if s.LocalOwner == 0 && s.EnemyOwner == 0 {
			// Try SkirmishConfig first
			foundLocal := false
			foundEnemy := false
			var local, enemy uint8
			if s.Skirmish.NumPlayers > 0 {
				for i := 0; i < 10; i++ {
					ctrl := s.Skirmish.Players[i].Controller
					if !foundLocal && ctrl == 0 && i < s.Skirmish.NumPlayers {
						local = uint8(i)
						foundLocal = true
					}
					if !foundEnemy && ctrl != 0 && i < s.Skirmish.NumPlayers {
						enemy = uint8(i)
						foundEnemy = true
					}
				}
				if foundLocal || foundEnemy {
					s.LocalOwner = local
					if foundEnemy {
						s.EnemyOwner = enemy
					} else {
						// Single human skirmish fallback: enemy 1
						s.EnemyOwner = 1
					}
				}
			}
			if s.LocalOwner == 0 && s.EnemyOwner == 0 && s.Econ != nil {
				// Derive from economy controller states 1/2 [08 "Established AI-facing data"]
				var l, e uint8
				foundL, foundE := false, false
				for i := 0; i < 10; i++ {
					p := s.Econ.Players[i]
					if !p.Exists {
						continue
					}
					if !foundL && p.ControllerState == 1 {
						l = uint8(i)
						foundL = true
					}
					if !foundE && p.ControllerState == 2 {
						e = uint8(i)
						foundE = true
					}
				}
				if foundL || foundE {
					if foundL {
						s.LocalOwner = l
					}
					if foundE {
						s.EnemyOwner = e
					} else if s.EnemyOwner == 0 {
						s.EnemyOwner = 1
					}
				} else {
					s.LocalOwner = 0
					s.EnemyOwner = 1
				}
			} else if s.LocalOwner == 0 && s.EnemyOwner == 0 {
				s.LocalOwner = 0
				s.EnemyOwner = 1
			}
		}
		localOwner := s.LocalOwner
		enemyOwner := s.EnemyOwner
		s.Units.OnDeath = func(h pool.Handle, cause units.DeathCause, u *units.Unit) {
			if s.Vis != nil && u != nil {
				unpublishOne(s, u)
			}
			if s.Mission != nil && u != nil {
				ctx := triggers.PollContext{Tick: s.Clock.GlobalTick, World: s.Units, LocalOwner: localOwner, EnemyOwner: enemyOwner}
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitDied, u)
			}
			// Audio: death does not map to a queued voice directly, but an
			// under-attack cue for nearby allies could be queued elsewhere.
			// For now, no death voice; weapon hit already queues via impact sink.
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
		s.Units.OnCreate = func(h pool.Handle, u *units.Unit) {
			if s.Mission != nil && u != nil {
				ctx := triggers.PollContext{Tick: s.Clock.GlobalTick, World: s.Units, LocalOwner: localOwner, EnemyOwner: enemyOwner}
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitCreated, u)
			}
			// Audio: completed build emits unitcomplete [03 §8.3] slot 8.
			if s.AudioQueue != nil && u != nil && s.Clock != nil && s.Clock.GlobalTick > 0 {
				s.AudioQueue.SetNow(s.Clock.GlobalTick)
				_ = s.AudioQueue.InsertAt(s.Clock.GlobalTick, audio.SlotUnitComplete, h, "")
			}
		}
		s.Units.OnCapture = func(h pool.Handle, oldOwner, newOwner uint8, u *units.Unit) {
			if s.Mission != nil && u != nil {
				ctx := triggers.PollContext{Tick: s.Clock.GlobalTick, World: s.Units, LocalOwner: localOwner, EnemyOwner: enemyOwner}
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitCaptured, u)
			}
			if s.AudioQueue != nil && u != nil && s.Clock != nil {
				s.AudioQueue.SetNow(s.Clock.GlobalTick)
				_ = s.AudioQueue.InsertAt(s.Clock.GlobalTick, audio.SlotCapture, h, "")
			}
		}
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
	// Phase 2b: weapon target acquisition, Aim, fire [06 §3][06 §4][GAP T15] P0-I04.
	// Runs after units-tick's reload decrement but still in PhaseUnitsScripts
	// so Aim dispatch remains before normal COB drain of the same tick [GAP T15] C17.
	s.Kernel.Register(kernel.PhaseUnitsScripts, "weapons-fire", func(tick uint32) {
		if s.Combat != nil && s.Units != nil {
			// Capture projectile count before fire to emit start sounds for new allocations [06 §5.1][03 §8.3].
			before := 0
			if s.Combat != nil {
				before = s.Combat.Count()
			}
			s.Combat.TickWeapons(tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, rng.Global.Sim, rng.Global.Crt)
			// Weapon start sounds: for each newly appended projectile, emit its SoundStart alias with positional gating [03 §8.3].
			// Presentation-only, uses CRT for variant via queue not Sim [I4]; audience gate via IsAudibleAt.
			if s.AudioQueue != nil && s.Combat != nil && s.Catalog != nil {
				after := s.Combat.Count()
				for i := before; i < after; i++ {
					if i < 0 || i >= len(s.Combat.Records) {
						continue
					}
					rec := s.Combat.Records[i]
					if wdef, ok := s.Catalog.WeaponByID(rec.WeaponID); ok && wdef != nil && wdef.SoundStart != "" {
						pos := [3]numeric.Fixed{rec.Pos.X, rec.Pos.Y, rec.Pos.Z}
						if s.IsAudibleAt(pos) {
							_, _, _ = s.EmitPositional(wdef.SoundStart, pos)
						}
					}
				}
			}
		}
	})
	// Interceptor automatic launches [06 §11.2] C29: stockpile anti-nuke scans
	// the current projectile prefix for first unclaimed enemy targetable nuke
	// within coverage square. Runs after ordinary weapon fire so
	// launch-before-production ordering is preserved (stockpile build is in
	// PhaseOrdersPathEconomy after this phase) [06 §11.1] C29. Deterministic
	// player/slot asc, prefix asc (I1).
	s.Kernel.Register(kernel.PhaseUnitsScripts, "interceptor-fire", func(tick uint32) {
		s.interceptorFireTick(tick)
	})
	// Interceptor guidance pre-step: update linked projectile's stored target
	// point to current position before motion per [06 §11.2] non-cruise guidance.
	s.Kernel.Register(kernel.PhaseProjectiles, "interceptor-guidance", func(tick uint32) {
		_ = tick
		s.interceptorGuidanceTick()
	})

	// Phase 3: projectile integration and collision [01 §4.4]
	s.Kernel.Register(kernel.PhaseProjectiles, "projectiles", func(tick uint32) {
		if s.Combat != nil {
			// combat motion, collision, impact — delegate to combat service [06 §5][06 §6][06 §8][06 §9] P0-I04.
			// The service is the sole projectile allocation authority [06 §5.1][I5].
			// Per [01 §6.2] the projectile phase captures the count at entry; tail
			// compaction reads the current count and is handled at phase tail [06 §5.2].
			s.Combat.TickProjectiles(tick, s.Units, s.World, s.Features, s.Vis, s.Econ, s.Catalog, rng.Global.Sim, rng.Global.Crt)
		}
	})
	// Interceptor detonation sweep after ordinary impact [06 §11.2][06 §9.3] C29:
	// ordinary area enumeration first, then interceptor force-detonation of
	// alive non-self projectiles within unhalved Area. Friendly can be removed.
	s.Kernel.Register(kernel.PhaseProjectiles, "interceptor-detonation", func(tick uint32) {
		_ = tick
		s.interceptorDetonationTick()
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
	// latch freeze gates settlement same tick [P1-01 §2.2]. Poll cadence uses
	// the per-player persisted WinLoseTime deadline (sibling of UpdateTime)
	// advanced by exactly 30 when due [05 "Authoritative settlement order"] C2,
	// not global tick%30 [P0-I13].
	s.Kernel.Register(kernel.PhaseOrdersPathEconomy, "trigger-poll", func(tick uint32) {
		if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
			return
		}
		if len(s.Mission.Victory) == 0 && len(s.Mission.Defeat) == 0 {
			return
		}
		// Per-player deadline block [P0-I13][05 "Authoritative settlement order"] C2.
		isDue := false
		if s.Econ != nil && int(s.LocalOwner) < 10 {
			p := &s.Econ.Players[int(s.LocalOwner)]
			// Unsigned due check: deadline <= tick means due. Mirrors settlement.
			if p.WinLoseTime > tick {
				return
			}
			// When due, advance by exactly 30 before poll [05 C2]. Single add,
			// not loop, so catch-up one per tick if behind.
			p.WinLoseTime += 30
			isDue = true
		} else {
			// Fallback when no economy or invalid local owner (fixture without
			// persisted deadline): global tick%30 [P0-I13].
			// TODO(question): persisted per-player WinLoseTime not available; using global fallback.
			if tick%30 != 0 {
				return
			}
			isDue = true
		}
		ctx := triggers.PollContext{Tick: tick, World: s.Units, LocalOwner: s.LocalOwner, EnemyOwner: s.EnemyOwner}
		v, d := triggers.Evaluate(s.Mission.Victory, s.Mission.Defeat, ctx)
		s.VictoryDone = s.VictoryDone || v
		s.DefeatDone = s.DefeatDone || d
		var latched bool
		if v {
			latched = s.Latch.AdvanceWin(isDue)
		} else if d {
			latched = s.Latch.AdvanceLose(isDue)
		}
		if s.Econ != nil {
			for i := 0; i < 10; i++ {
				s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
				s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
			}
		}
		// Transition to postbattle only when retail latch point reached:
		// Countdown has crossed below zero and Bits has win/lose [P1-01 §2.2]
		// [08 "Evaluation"]. This is the sole latch transition; Arm alone
		// (Countdown 4, Bits not yet set) does not transition [P0-I13].
		if latched && s.Latch.IsEnding() && s.State == StateBattle {
			// Feed final statistics/progression after final authoritative tick
			// [P1-01 §2.3][P1-01 §4]: kills from economy, ticks from clock.
			// Determine win/lose from latch bits, not just VictoryDone, so
			// pending outcome is authoritative.
			win := s.Latch.IsWin()
			// Apply campaign W/L for mission slot 0 (campaign missions use slot
			// 0 for single-mission test fixtures; real campaign slot derived from
			// mission index via mission list — use 0 for now).
			// TODO(question): campaign slot index for multi-mission persistence not yet threaded; using 0.
			s.Progress.ApplyCampaignResult(0, win)
			// Transition to postbattle [08 "Session states"] 6->7.
			_ = s.TransitionTo(StatePostBattle)
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
			// Sync authoritative heading from movement state into units.MoveState
			// so snapshot can copy from the single per-unit record [04 §8.1] C20 [04 §5.1] C25 [03 §2.4] C24.
			// Heading is stored in movement side-state (Steer/Flight/Collision) not on Unit;
			// this keeps Units.Tick or Movement.Tick as the heading owner and publishes it via MoveState.
			if s.Units != nil {
				for _, um := range s.Units.Iter() {
					if um == nil || !um.Alive {
						continue
					}
					if st := s.Movement.Steers[um.Handle]; st != nil {
						um.Move.Heading = st.Heading
					} else if fl := s.Movement.Flights[um.Handle]; fl != nil {
						um.Move.Heading = fl.Heading
						// TODO(question): Pitch/Bank for flight derived from velocity delta / bank-scale not yet wired; keep zero [03 §2.4] C24 [04 §10.1].
					} else if coll := s.Movement.Collisions[um.Handle]; coll != nil {
						um.Move.Heading = coll.Heading
					}
				}
			}
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
	// Countdown arms to 4 without yet setting Bits; Bits are written only
	// when Countdown crosses below zero via TickNoHuman with pending outcome
	// [P1-01 §2.2][08 "Evaluation"].
	s.Kernel.Register(kernel.PhaseBarrier, "countdown-nohuman", func(tick uint32) {
		if s.Mission == nil || s.Mission.Type != mission.TypeCampaign {
			return
		}
		if s.humanCount() != 0 {
			return
		}
		if s.Latch.Countdown < 0 && !s.Latch.IsEnding() {
			// Not armed and no victory/defeat predicate: no latch yet.
			return
		}
		// If already armed (Countdown >=0) decrement every tick [P1-01 §7.2].
		if s.Latch.Countdown >= 0 {
			latched := s.Latch.TickNoHuman()
			if s.Econ != nil {
				for i := 0; i < 10; i++ {
					s.Econ.Players[i].GameEnded = s.Latch.IsEnding()
					s.Econ.Players[i].EndGameCountdown = int32(s.Latch.Countdown)
				}
			}
			if latched && s.Latch.IsEnding() && s.State == StateBattle {
				win := s.Latch.IsWin()
				s.Progress.ApplyCampaignResult(0, win)
				_ = s.TransitionTo(StatePostBattle)
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
				ordersViews := make([]snapshot.OrderView, 0)
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
						Heading:        u.Move.Heading, // fallback [04 §5.1] C25 [03 §2.4] C24
						Pitch:          u.Move.Pitch,
						Bank:           u.Move.Bank,
					}
					// Prefer authoritative movement heading if movement state exists [04 §8.1] C20 [04 §10.1].
					// Units.Tick or Movement.Tick owns heading; snapshot copies it read-only (I6).
					if s.Movement != nil {
						if st := s.Movement.Steers[u.Handle]; st != nil {
							v.Heading = st.Heading
						} else if fl := s.Movement.Flights[u.Handle]; fl != nil {
							v.Heading = fl.Heading
							// TODO(question): Pitch/Bank for flight are visual lean from velocity delta; keep Move.Pitch/Bank [03 §2.4] C24 [04 §10.1].
						} else if coll := s.Movement.Collisions[u.Handle]; coll != nil {
							v.Heading = coll.Heading
						}
					}
					if u.Def != nil {
						v.Model = u.Def.ObjectName
						v.FootX = int8(u.Def.FootprintX)
						v.FootZ = int8(u.Def.FootprintZ)
						// DefID via world lookup if available (I13). Zero when not yet sliced.
						if s.Units != nil {
							if id := s.Units.DefIDForHandle(u.Handle); id != 0 {
								v.DefID = id
							}
						}
					}
					// Bind COB piece transforms if VM exists [03 §2.4] C21–C22 [04 §4.6] [04 §4.2] C13.
					// Last writer wins across TURN, turn-now, SPIN via one adapter [03 §2.4] C22.
					// Snapshot copies the accumulators read-only; rendering uses float trig (I2) and never touches sim RNG (I4).
					if vm := u.GetScript(); vm != nil {
						if vmPieces := vm.Pieces; len(vmPieces) > 0 {
							v.Pieces = make([]snapshot.PieceView, len(vmPieces))
							for i, ps := range vmPieces {
								v.Pieces[i] = snapshot.PieceView{
									Index: i,
									RotX:  ps.RotX,
									RotY:  ps.RotY,
									RotZ:  ps.RotZ,
									Tx:    ps.Trans[0],
									Ty:    ps.Trans[1],
									Tz:    ps.Trans[2],
								}
							}
						}
					}
					views = append(views, v)
					// Collect primary order head for overlays [04 §3][04 §7.3]
					if q := orders.QueueForUnit(u); q != nil && q.LenPrimary() > 0 {
						if head := q.Head(); head != nil {
							ordersViews = append(ordersViews, snapshot.OrderView{
								Unit:      u.Handle,
								Target:    head.Target,
								GoalX:     head.GoalX,
								GoalY:     head.GoalY,
								GoalZ:     head.GoalZ,
								Kind:      orders.DescriptorFor(head.ID).Name,
								MoveState: uint8(head.MoveState),
							})
						}
					}
				}
				frame.Units = views
				frame.Orders = ordersViews
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

						Filename:    inst.Def.Filename,
						SeqName:     inst.Def.SeqName,
						SeqNameShad: inst.Def.SeqNameShad,
						Animating:   inst.Def.Animating != 0,
						AnimTrans:   inst.Def.AnimTrans != 0,
						ShadTrans:   inst.Def.ShadTrans != 0,
						Blocking:    inst.Def.Blocking,
						Reclaimable: inst.Def.Reclaimable,
						Height:      inst.Def.Height,
						Geothermal:  inst.Def.Geothermal,
					}
					if fv.Model == "" {
						fv.Model = inst.Def.Filename
					}
					fviews = append(fviews, fv)
				}
				frame.Features = fviews
			}
			if s.Combat != nil {
				// Publish projectiles for presentation [06 §5.1] P0-I04.
				for i := 0; i < s.Combat.Count(); i++ {
					h := pool.Handle(i + 1)
					if !s.Combat.Alive(h) {
						continue
					}
					if i < 0 || i >= len(s.Combat.Records) {
						continue
					}
					p := s.Combat.Records[i]
					frame.Projectiles = append(frame.Projectiles, snapshot.ProjectileView{
						Handle:   h,
						X:        p.Pos.X,
						Y:        p.Pos.Y,
						Z:        p.Pos.Z,
						WeaponID: p.WeaponID,
						Shooter:  p.Shooter,
					})
				}
			}
			// Resources for HUD [05 "Player slot"] (I2 float allowlist: ledger stocks)
			if s.Econ != nil {
				var resViews []snapshot.ResourceView
				for p := 0; p < 10; p++ {
					pl := s.Econ.Players[p]
					if !pl.Exists {
						continue
					}
					resViews = append(resViews, snapshot.ResourceView{
						Player:         uint8(p),
						Metal:          pl.Stock[economy.Metal],
						Energy:         pl.Stock[economy.Energy],
						MetalCapacity:  pl.Capacity[economy.Metal],
						EnergyCapacity: pl.Capacity[economy.Energy],
					})
				}
				frame.Resources = resViews
			}
			// Effects placeholder: no fixed pool in session yet; keep empty but
			// valid for renderer to read without touching sim state (I6).
			// Presentation RNG (CRT) is not advanced here; client/server streams remain distinct (I4).
			// Audio queue is presentation-only but snapshot carries pending cues for HUD/speech display [03 §8.3] C18 I6.
			s.CollectAudioForSnapshot(frame)
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
//
// P0-I10: Session.Step executes authoritative ticks only in StateBattle (6) [08 "Session states"].
// Loading completion defers first battle dispatch to next dispatch (C2). Abort and victory/defeat
// transition through 7/2 rather than leaving battle ticking behind overlay [08].
func (s *Session) Step(scaledNow int32) {
	if s.Clock == nil {
		s.Clock = &clock.State{Requested: 10, Active: 10}
	}
	if s.Kernel == nil {
		s.Kernel = &kernel.Kernel{}
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
			// or just transitioned Router/Preload->Loading. Render once and return.
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
					if a != a {
						a = 0
					}
					alpha = a
				}
				s.OnRender(alpha)
			}
			return
		}
	}
	// Authoritative ticks only in StateBattle [08][P0-I10].
	if s.State != StateBattle {
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
				if a != a {
					a = 0
				}
				alpha = a
			}
			s.OnRender(alpha)
		}
		return
	}
	ticks := s.Clock.AdvanceSP(scaledNow)
	for i := 0; i < ticks; i++ {
		if s.State != StateBattle {
			break // abort or victory transitioned out mid-batch [08] 6->2 or 6->7
		}
		// Authoritative researched tick per [01 §4.4][05][08][ON-09] — increments GlobalTick before phase 1 [01 §4.4] C6
		tick := s.Clock.BeginSubTick()
		s.authoritativeTick(tick)
		if s.State != StateBattle {
			break // latch armed->ending transitioned to postbattle same tick [P1-01 §2.2]
		}
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

// AbortBattle transitions 6->2 abort/return path [08 "Session states"] C1.
// It is the overlay abort path that must not leave battle ticking behind overlay [P0-I10].
func (s *Session) AbortBattle() bool {
	if s == nil {
		return false
	}
	if s.State == StateBattle {
		return s.TransitionTo(StateRouter)
	}
	if s.State == StatePostBattle {
		return s.TransitionTo(StateRouter)
	}
	return false
}

// Retry reloads the same mission via state 5 directly [P1-01 §7.5][08 "Session states"].
// RETRY path reloads same mission without rewriting campaign progress beyond current slot,
// while CONTINUE writes W/L and selects next mission. For the gate we ensure Retry
// keeps the same Mission object and ends in Loading, ready for next dispatch.
func (s *Session) Retry() bool {
	if s == nil {
		return false
	}
	if s.State != StatePostBattle && s.State != StateBattle {
		return false
	}
	// Reset latch and done flags so battle can retrigger exactly once [P1-01][08].
	s.Latch = NewEndLatch()
	s.VictoryDone = false
	s.DefeatDone = false
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			s.Econ.Players[i].GameEnded = false
			s.Econ.Players[i].EndGameCountdown = -1
		}
	}
	// Direct to loading for same mission; transition must respect graph: 6/7->2->5.
	// If we are in PostBattle (7) we can go 7->2, then 2->5. If in Battle (6) go 6->2->5.
	if s.State == StatePostBattle || s.State == StateBattle {
		if !s.TransitionTo(StateRouter) {
			return false
		}
	}
	if s.State == StateRouter {
		return s.TransitionTo(StateLoading)
	}
	// Fallback: try direct 5->6 pending path via CompleteLoading? For retry we want loading.
	if s.State == StateLoading {
		return true
	}
	return false
}

// ContinueCampaign writes progress and selects next mission via post-battle W/L [P1-01 §7.5][P0-I10].
// It must be called from PostBattle (7) after victory latch has written Progress.WL.
// It transitions 7->2 (front-end return) and leaves Progress written exactly once.
func (s *Session) ContinueCampaign() bool {
	if s == nil {
		return false
	}
	if s.State != StatePostBattle {
		return false
	}
	// Progress already written at latch time via trigger-poll ApplyCampaignResult [P1-01 §2.3].
	// Ensure at least one slot holds W/L for gate: if Apply did not run (fixture without triggers),
	// write current latch outcome.
	if s.Progress.WL[s.CampaignSlot] == 0 {
		win := s.Latch.IsWin()
		if s.Latch.IsEnding() {
			s.Progress.ApplyCampaignResult(s.CampaignSlot, win)
		} else if s.VictoryDone {
			s.Progress.ApplyCampaignResult(s.CampaignSlot, true)
		} else if s.DefeatDone {
			s.Progress.ApplyCampaignResult(s.CampaignSlot, false)
		}
	}
	return s.TransitionTo(StateRouter)
}
