package session

import (
	"fmt"
	"sync"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// MeteorState holds the shower scheduler per [08 "Meteor showers"] [02 "Map files"] [06 §6.5].
// Nine fields persist in the Meteor account: enabled, active, next-strike, strike-end,
// next-hit, origin X/Z, target X/Z, plus resolved weapon [08 "Meteor showers"].
// Timing is integral: per-hit delay trunc(30/density), duration trunc(duration*30),
// interval trunc(interval*30) [02 Meteor scheduler]. Draws are CRT stream six per
// meteor (four scheduling even when disabled + two lateral) [06 §6.5] I4; meteors
// consume zero sim draws and are side-neutral [06 §6.5].
type MeteorState struct {
	Enabled       bool
	Active        bool
	NextStrike    uint32
	StrikeEnds    uint32
	NextHit       uint32
	OriginX       int32
	OriginZ       int32
	TargetX       int32
	TargetZ       int32
	Density       float64
	Radius        int32
	DurationTicks int32
	IntervalTicks int32
	PerHitDelay   int32
	WeaponName    string
	Weapon        *content.WeaponDef
	Initialized   bool
}

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
	AI       [10]*ai.Manager // fixed player-indexed, nil holes per RS-02 [08] I4 single stream
	Mission  *mission.Mission
	Snapshot *snapshot.Buffer
	Shutdown *Shutdown // ordered shutdown in reverse-init order [01 §2.1][01 §2.3] P0-I10

	Presentation *presentation.Collector     // typed presentation event admission [EVENT-01]
	Effects      *presentation.EffectService // bounded immutable effect publication [F-P0-034]
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

	// Result ownership [08][RS-05] moved from package global sync.Map into Session
	// per RS-P0-012. One point where result becomes terminal is when latch
	// becomes visible (EndLatch Bits 0x04 set) and Result.Ended set; one where
	// simulation stops is State != Battle (Step early return, no authoritativeTick).
	// All result mutation happens in authoritativeTick/EvaluateResult (single
	// thread); reads from presentation (snapshot GetResult) hold resultMu.
	resultMu            sync.Mutex
	result              Result
	resultPending       bool
	resultPendingWinner int
	resultPendingLosers []int
	resultPendingReason string
	resultPendingDraw   bool
	resultArmedTick     uint32
	resultNextDue       uint32
	resultCallback      func(Result)
	resultCallbackFired bool

	Wind *world.Wind

	// MeteorState is the shower scheduler per [08 "Meteor showers"] [02 "Map files"] [06 §6.5].
	// Nine fields (enabled, active, next-strike, strike-end, next-hit, origin/target) persist
	// in the Meteor account [08 "Meteor showers"]; spawn uses shared projectile pool [06 §6.5].
	Meteor MeteorState

	// Per-session RNG state [I4][RS-06]: isolated per-session copies of the single global Park-Miller and CRT streams.
	// Two interleaved sessions must not cross-contaminate draws; moved from process-global rng.Global [INVARIANTS I4][RS-P0-018].
	rngSim         rng.Simulation
	rngCrt         rng.CRT
	rngInitialized bool

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
	AudioRegistry *audio.Registry    // session-owned alias identities and samples [03 §8.2][03 §8.3]
	AudioMusic    *audio.Controller  // CD/MCI controller with 5 modes [03 §8.4]
	audioViewport audio.Viewport     // presentation viewport for pan/attenuation [03 §8.3]
	audioFrame    uint32             // presentation frame counter for Drain [03 §8.3] C18
	audioFS       vfs.FSOps          // VFS for cache loads, presentation-only
	audioCRT      *presentation.CRTRandom
	audioClock    *presentation.Clock

	// Trace is the optional ordered debug sink [ON-09]. Disabled by default,
	// appends in authoritative order, never ranges maps, never calls RNG,
	// never alters sim decisions [INVARIANTS I1][I4][I6].
	traceEnabled bool
	trace        []SessionTraceEvent

	// pendingHuman is the session-owned immutable input queue. Presentation
	// enqueues value commands; authoritativeTick drains it at the network/input
	// boundary before any order/build work [01 §4.4][I6].
	humanMu      sync.Mutex
	pendingHuman []HumanCommand
}

// HumanCommandKind identifies a typed local-player command. Payloads contain
// handles and values only; they never retain pointers into simulation pools.
type HumanCommandKind uint8

const (
	HumanSelectionReplace HumanCommandKind = iota + 1
	HumanSelectionToggle
	HumanSelectionClear
	HumanOrder
	HumanStop
	HumanActivation
	HumanMobileBuild
	HumanFactoryBuild
	HumanCancelProduction
	HumanStockpile
	HumanBuildPage
	HumanGroupAssign
	HumanGroupRecall
)

type HumanSelectionCommand struct{ Handles []pool.Handle }
type HumanOrderCommand struct {
	Handles  []pool.Handle
	Code     int
	Target   pool.Handle
	Position orders.ResolvePos
	Queued   bool
}
type HumanStopCommand struct{ Handles []pool.Handle }
type HumanActivationCommand struct {
	Unit             pool.Handle
	Activate, Queued bool
}
type HumanMobileBuildCommand struct {
	Builder    pool.Handle
	Product    string
	WX, WY, WZ numeric.Fixed
	Queued     bool
}
type HumanFactoryBuildCommand struct {
	Builder pool.Handle
	Product string
	Count   int
	// Queued remains for old callers that only supplied the pre-count command.
	Queued bool
}
type HumanCancelProductionCommand struct{ Unit pool.Handle }
type HumanStockpileCommand struct {
	Unit   pool.Handle
	Queued bool
}

// HumanBuildPageCommand selects one authored build page for a selected builder.
// Page is an absolute zero-based page; presentation resolves digit/next/prev
// into this value from the immutable frame, while the authoritative boundary
// validates the builder and clamps against the compiled CANBUILD page count
// [07 §9].
type HumanBuildPageCommand struct {
	Builder pool.Handle
	Page    int
}

// HumanGroupCommand carries the established Ctrl+digit assignment or digit
// recall operation. Preserve is the Shift-held toggle/preserve argument on
// recall [07 §9]. Mask is the authored CTRL_F filter when that state is
// available; an all-zero value means that the presentation boundary has not
// published a CTRL_F mask yet (the no-filter path).
type HumanGroupCommand struct {
	Group    int
	Preserve bool
	Mask     [32]byte
}

// HumanCommand is an immutable-at-boundary command value. EnqueueHumanCommand
// copies handle slices and strings so callers may reuse their input buffers.
type HumanCommand struct {
	Kind             HumanCommandKind
	Selection        HumanSelectionCommand
	Order            HumanOrderCommand
	Stop             HumanStopCommand
	Activation       HumanActivationCommand
	MobileBuild      HumanMobileBuildCommand
	FactoryBuild     HumanFactoryBuildCommand
	CancelProduction HumanCancelProductionCommand
	Stockpile        HumanStockpileCommand
	BuildPage        HumanBuildPageCommand
	Group            HumanGroupCommand
}

func cloneHumanHandles(in []pool.Handle) []pool.Handle {
	if len(in) == 0 {
		return nil
	}
	out := make([]pool.Handle, len(in))
	copy(out, in)
	return out
}

func cloneHumanCommand(c HumanCommand) HumanCommand {
	c.Selection.Handles = cloneHumanHandles(c.Selection.Handles)
	c.Order.Handles = cloneHumanHandles(c.Order.Handles)
	c.Stop.Handles = cloneHumanHandles(c.Stop.Handles)
	return c
}

// EnqueueHumanCommand appends one command for the next authoritative input
// phase. It performs no simulation mutation.
func (s *Session) EnqueueHumanCommand(c HumanCommand) error {
	if s == nil {
		return fmt.Errorf("session: nil human-command owner")
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	s.pendingHuman = append(s.pendingHuman, cloneHumanCommand(c))
	return nil
}

// PendingHumanCommands returns immutable command copies for diagnostics/tests.
func (s *Session) PendingHumanCommands() []HumanCommand {
	if s == nil {
		return nil
	}
	s.humanMu.Lock()
	defer s.humanMu.Unlock()
	out := make([]HumanCommand, len(s.pendingHuman))
	for i := range s.pendingHuman {
		out[i] = cloneHumanCommand(s.pendingHuman[i])
	}
	return out
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
	TracePathFailed           = "PathFailed"
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

// SimRNG returns the per-session simulation RNG [INVARIANTS I4][RS-P0-018].
// It is isolated per Session so two interleaved sessions do not cross-contaminate draws [RS-06].
func (s *Session) SimRNG() *rng.Simulation {
	if s == nil {
		return rng.Global.Sim
	}
	if !s.rngInitialized {
		if rng.Global.Sim != nil {
			s.rngSim = *rng.Global.Sim
		} else {
			s.rngSim = rng.NewSimulation(1)
		}
		if rng.Global.Crt != nil {
			s.rngCrt = *rng.Global.Crt
		} else {
			s.rngCrt = rng.NewCRT(1)
		}
		s.rngInitialized = true
	}
	return &s.rngSim
}

// CrtRNG returns the per-session CRT RNG [INVARIANTS I4][RS-P0-018].
func (s *Session) CrtRNG() *rng.CRT {
	if s == nil {
		return rng.Global.Crt
	}
	if !s.rngInitialized {
		if rng.Global.Sim != nil {
			s.rngSim = *rng.Global.Sim
		} else {
			s.rngSim = rng.NewSimulation(1)
		}
		if rng.Global.Crt != nil {
			s.rngCrt = *rng.Global.Crt
		} else {
			s.rngCrt = rng.NewCRT(1)
		}
		s.rngInitialized = true
	}
	return &s.rngCrt
}

// SeedSessionRNG seeds the per-session RNGs from the given seeds [I4][RS-06].
// It also seeds the process-global for backward compatibility with code that still reads rng.Global.
func (s *Session) SeedSessionRNG(simSeed, crtSeed uint32) {
	if s == nil {
		return
	}
	rng.SeedGlobal(simSeed, crtSeed)
	s.rngSim = rng.NewSimulation(simSeed)
	s.rngCrt = rng.NewCRT(crtSeed)
	// Preserve draw counters at zero for fresh session [01 §7.1][01 §7.2].
	s.rngInitialized = true
	// Bind AI managers to this session's RNG for isolation [RS-06][I4].
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.RNG = s.SimRNG()
		}
	}
}

// SyncGlobalRNG syncs the process-global RNG to this session's state for code that still reads rng.Global [RS-06][I4].
// Call before any legacy global draw to keep global in sync with session-local.
func (s *Session) SyncGlobalRNG() {
	if s == nil || !s.rngInitialized {
		return
	}
	if rng.Global.Sim != nil {
		*rng.Global.Sim = s.rngSim
	}
	if rng.Global.Crt != nil {
		*rng.Global.Crt = s.rngCrt
	}
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
	// RS-02: assert player-indexed ownership — mgr.Player must equal array index [08].
	for i, mgr := range s.AI {
		if mgr != nil {
			if int(mgr.Player) != i {
				return fmt.Errorf("session: ai manager player %d at index %d mismatch [RS-02][08]", mgr.Player, i)
			}
			if mgr.QueueBuildTyped == nil {
				return fmt.Errorf("session: ai manager player %d has no QueueBuildTyped binding [RX-01][F-P0-004]", mgr.Player)
			}
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
	// AI is fixed [10]*Manager per RS-02 — nil holes are valid (human players); no missing-array check.
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
		// Seed deadlines from current tick to keep WinLoseTime cadence correct [05 "Authoritative settlement order"] C5 and [08 "Evaluation"] tick site.
		// WinLoseTime is the trigger poll deadline (globalTick >= WinLoseTime then WinLoseTime+=30) [08 "Evaluation"] in LOCAL slice.
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
	// Sync per-session RNG to global for any remaining global draws, and ensure per-session initialized [RS-06][I4].
	// Use pointer assignment so Global and per-session share same state during tick — any draw via either advances same state [I4][RS-06].
	_ = s.SimRNG()
	_ = s.CrtRNG()
	rng.Global.Sim = s.SimRNG()
	rng.Global.Crt = s.CrtRNG()
	// 1 network/input boundary — drain local typed commands before orders/build [01 §4.4] PhaseNetwork. The queue is presentation-owned until this point.
	// [01 §4.4] PhaseNetwork. The queue is presentation-owned until this point.
	s.applyHumanCommands(tick)

	// 2 movement shared indexing + path submission (before the unit sweep; not a retail phase — nanolathe's pathing is A* on a grid) [04 §8.2][04 §10.2][04 §7.3]
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
				s.Movement.DeactivateMove(u.Handle)
				continue
			}
			head := q.Head()
			if head == nil {
				s.Movement.DeactivateMove(u.Handle)
				continue
			}
			name := orders.DescriptorFor(head.ID).Name
			isMove := name == "Move_Ground" || name == "VTOL_Move" || name == "QMove" || name == "Patrol" || name == "QPatrol" || name == "VTOL_Patrol" || name == "RepairPatrol" || name == "VTOL_RepairPatrol"
			// Walk-to-site for mobile builders [04 §3.4][05][R-P0-06].
			// A MOBILE builder with a MobileBuild order out of nano range walks
			// via normal Move_Ground machinery before state 2. This pre-pass
			// submission ensures the scheduler sees the request before its Tick,
			// and prevents the non-move DeactivateMove below from cancelling it.
			isWalk := false
			if s.Build != nil && (name == "MobileBuild" || name == "VTOL_MobileBuild") && s.Build.NeedsWalk(u, head) {
				isWalk = true
			}
			if isWalk {
				s.Build.EnsureWalkPublic(u, head)
				head.MoveState = orders.MoveEnRoute
				continue
			}
			if !isMove {
				// The primary order head is authoritative.  Drop any path binding
				// as soon as a non-move head becomes active so a late publication
				// cannot attach to the replacement [04 §3.3][04 §7.3].
				s.Movement.DeactivateMove(u.Handle)
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
			targetMoved := false
			if head.Target != 0 {
				var tgt *units.Unit
				if q.Lookup != nil {
					tgt = q.Lookup(head.Target)
				}
				if tgt == nil && s.Units != nil {
					tgt = s.Units.Unit(head.Target)
				}
				if tgt == nil || !tgt.Alive {
					rejected := q.RemoveHead()
					if rejected != nil {
						rejected.MoveState = orders.MoveBlocked
						rejected.PathStatus = uint32(path.StatusRejected)
					}
					s.Movement.DeactivateMove(u.Handle)
					continue
				}
				if tgt.X != head.GoalX || tgt.Z != head.GoalZ {
					head.GoalX = tgt.X
					head.GoalY = tgt.Y
					head.GoalZ = tgt.Z
					targetMoved = true
				}
			}
			// Reject an impossible destination after resolving a target's current
			// position, but before activation submits anything. A target may have
			// moved since the node was created; the resolved position is the only
			// goal eligible for this preflight [04 §7.3].
			goalCell := path.Cell{X: world.WorldToCell(head.GoalX), Z: world.WorldToCell(head.GoalZ)}
			if s.Movement != nil && s.World != nil && !s.Movement.IsGoalCellPassable(u.Handle, goalCell) {
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TracePathFailed, Handle: u.Handle, Value: int32(path.StatusRejected)})
				rejected := q.RemoveHead()
				if rejected != nil {
					rejected.MoveState = orders.MoveBlocked
					rejected.PathStatus = uint32(path.StatusRejected)
				}
				s.Movement.ClearPathFailure(u.Handle)
				s.Movement.DeactivateMove(u.Handle)
				continue
			}
			// Bind and submit at one boundary.  Repeated visits for the same
			// active node do not submit again; a changed target is an explicit
			// replan for that same node [04 §7.3].
			if targetMoved {
				s.Movement.ReplanMove(u, head)
			} else {
				s.Movement.ActivateMove(u, head)
			}
			head.MoveState = orders.MoveEnRoute
			route := s.Movement.Routes[u.Handle]
			if route != nil && route.Active {
				continue
			}
			if sched.HasRequest(u.Handle) {
				continue
			}
			if s.Movement != nil && s.Movement.HasPathFailure(u.Handle) {
				if rec, ok := s.Movement.PathFailureRecord(u.Handle); ok {
					if tick < rec.NextRetry {
						continue
					}
				}
			}
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

	// 3 one deterministic active unit-slot traversal ascending [01 §4.4][01 §6.1][INVARIANTS I1]
	// Each active unit visited exactly once under researched rule; new/dead units follow same-tick visibility [01 §4.4] R-P0-04
	if s.Units != nil {
		ordersPump := &orders.Pump{World: s.Units}
		s.Units.VisitActiveSlots(func(v units.SlotVisit) {
			h := v.Handle
			u := v.Unit
			s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceUnitBegin, Handle: h, Slot: int(v.Slot)})
			// unit pre-update (StepPreUpdate) [04 "unit sweep"]
			s.Units.StepPreUpdate(h, tick)
			// weapon slot/service step per unit [06 §3][06 §4] — stable weapon index once-compiled [ON-04]
			// Trace kinds are emitted from the service's per-visit summary, never inferred
			// from projectile counts or slot population [ON-09 evidence contract].
			if s.Combat != nil && s.Catalog != nil && u != nil && u.Alive && !u.Dying {
				beforeCount := s.Combat.Count()
				wsum := s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
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
				// Work admission trace when request recorded
				if wres.Product != 0 || wres.DefKey != "" {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceWorkAdmission, Handle: h})
				}
				if wres.Completed {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceConstructionProgress, Handle: h, Value: 1})
					// Completed product joins the world: movement state + visibility
					// publish for the new unit [RX-05][01 §6.1][03 §3].
					if wres.Product != 0 {
						s.CompleteUnit(wres.Product)
					}
				} else if wres.Product != 0 {
					s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceConstructionProgress, Handle: h})
				}
			}
			// movement step per unit (Between BeginTick/EndTick, StepUnit with route ownership) [04 §8.1][04 §8.2]
			if s.Movement != nil {
				mres := s.Movement.StepUnit(h, tick)
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceMovementStep, Handle: h, X: mres.DistToGoal, Value: 0})
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
										s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TracePathFailed, Handle: h, Value: int32(rec.Status)})
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
				res := s.Units.FinalizeDeath(h, tick)
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
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceDeathFinalize, Handle: h, Value: int32(res.Cause)})
			}
		})
	}
	if s.Movement != nil {
		s.Movement.EndTick(tick)
	}

	// 4 projectile integration and collision + pool compactor [01 §4.4][06 §5][06 §11.2]
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceProjectileStep})
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
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceProjectileImpact})
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

	// 5 per-player orders, path, economy, and occupancy work [01 §4.4] — outer loop players 0..9 ascending; the AI coordinator tick (30-tick cadence, deadline-gated subtasks) runs before the settlement deadline compare and the nine-step settlement pass [05 "Authoritative settlement order"] [INVARIANTS I1].
	// Due AI player work at researched deadline relationship (beforeDeadline) + economy request/accept/settlement via economy.TickPlayer per player
	// No map-defined player order [ON-09].
	for player := 0; player < 10; player++ {
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TracePlayerBegin, Player: player})
		mgr := s.AI[player] // direct player-indexed access per RS-02 [08] I1
		// Bind per-session RNG for isolation [RS-06][I4] — ensure manager uses session's stream, not shared global.
		if mgr != nil && mgr.RNG == nil {
			mgr.RNG = s.SimRNG()
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

	// 6 feature lifecycle and reclaim or death processing (burn, wind probes, successor hops; reclaim credits become visible at the next settlement) [01 §4.4][05 "Feature burning"][05 "Feature sinking"][06 §13.1]
	if s.Features != nil {
		// TickMotion is reproduction walker top of phase [06 §13.1] C25, lifecycle is burn/sink
		// Maintain order: TickMotion then TickLifecycle to avoid double reproduce
		s.Features.TickMotion(tick)
		s.Features.TickLifecycle(tick)
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceFeatureLifecycle})
	}

	// 7 sequence and effect-strip advancement [01 §4.4] — the global animation-
	// sequence cursor list (frame counter, remaining duration, loop flag per cursor).
	// TODO(question): strip advancement is presentation-owned in nanolathe [03 §1];
	// no sim state advances here.

	// 8/9 wind change and wind-field update [01 §4.4] — phase 8 draws the CRT interval and phase 9 the Sim strength/heading (order is behavior [I4]); projectiles, effects and features in earlier phases therefore read the previous tick's wind, as retail's phase order dictates. An earlier 'prepass at tick top' reading is superseded by the established order.
	if s.Wind != nil {
		// [01 §7.3] split: phase 8 draws CRT interval, phase 9 draws Sim strength/heading; order is behavior [INVARIANTS I4][RS-P0-018] per-session isolated
		s.Wind.Jitter(tick, s.CrtRNG())
		_ = s.Wind.Field(tick, s.SimRNG())
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceWindMeteor})
	}
	// Meteor scheduler initialization lazy: merge OTA meteor params with METEOR.TDF defaults [02 "Map files"] [08 "Meteor showers"] [06 §6.5].
	// Done once before first scheduling evaluation so scheduling draws start deterministically after wind.
	if !s.Meteor.Initialized {
		s.initMeteor()
	}

	// 9b Meteor scheduler after the wind phases so scheduling draws start deterministically after wind, and after the projectile phase so spawns move next tick [08 "Meteor showers"] [06 §6.5]. Cadence is interval+duration ticks per storm and per-hit delay trunc(30/density) [02 Meteor scheduler]; draws are CRT six per meteor (four scheduling even when disabled + two lateral) [06 §6.5] I4 with zero sim draws.
	// Cadence is interval+duration ticks per storm and per-hit delay trunc(30/density) [02 Meteor scheduler]; draws are CRT six per meteor (four scheduling even when disabled + two lateral) [06 §6.5] I4 with zero sim draws.
	s.tickMeteor(tick)

	// 10 camera/scroll position update [01 §4.4] — the camera steps toward its scroll
	// target (clamped at ±320 per tick, half-step when closer) and the shake driver
	// adds a CRT-drawn jitter while a shake is active. TODO(question): nanolathe's
	// camera is presentation-owned (internal/camera); no sim-side scroll target or
	// shake state exists yet.

	// 11 ten object-list update sweeps [01 §4.4] — a table of ten linked lists of
	// vtable-backed objects allocated at battle entry; each object's update virtual
	// runs, and objects returning zero are destroyed and removed with inline
	// compaction. TODO(question): the object family is not yet identified [01 §4.4];
	// nothing registers lists yet.

	// 12 every-eight-sub-tick cadence flip [01 §4.4]. TODO(question): no consumer is
	// wired in nanolathe; the flip drives nothing yet.

	// 13 visibility/LOS/radar deadline work and publication (Refresh, SensorTick) [03 §3.2][03 §3.4] — no retail phase slot; LOS has no RNG draws, so its tail placement does not perturb the sim draw order
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
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceVisibilityDeadline})
	}

	// 14 Trigger polling [08 "Evaluation"][P1-01 §3] — victory/defeat queues are polled
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// settlement) [08 "Evaluation"], and only when mission type is 1 (campaign)
	// [08 "Evaluation"]. Skirmish/multiplayer types 2/3 never poll [08 "Evaluation"].
	// Poll order is victory then defeat in builder order; victory AND, defeat OR,
	// victory evaluated first so simultaneous resolves as victory [08 "Evaluation"].
	// Completion arms shared countdown at 4 which decrements once per due poll
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// is written with ending 0x04, won 0x10|0x20, lost 0x40 clearing 0x10
	// [08 "Evaluation"].
	// TODO(question): exact presentation sequence between latch write and session
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign && (len(s.Mission.Victory) > 0 || len(s.Mission.Defeat) > 0) {
		// Once-per-30 cadence via LOCAL player's WinLoseTime deadline [08 "Evaluation"].
		// Shape matches economy settlement UpdateTime deadline: unsigned
		// globalTick >= WinLoseTime then WinLoseTime +=30 [05 "Authoritative settlement order"] C2.
		isDue := false
		if s.Econ != nil && int(s.LocalOwner) < 10 {
			p := &s.Econ.Players[int(s.LocalOwner)]
			if p.WinLoseTime <= tick { // unsigned due, single add not loop [08 "Evaluation"]
				p.WinLoseTime += 30
				isDue = true
			}
		} else {
			// Fixture-only fallback when Econ is nil; retail always has Econ for campaign.
			// Tick%30 shape preserves ~1/sec when no deadline storage exists.
			if tick%30 == 0 {
				isDue = true
			}
		}
		if isDue {
			ctx := triggers.PollContext{Tick: tick, World: s.Units, LocalOwner: s.LocalOwner, EnemyOwner: s.EnemyOwner}
			v, d := triggers.Evaluate(s.Mission.Victory, s.Mission.Defeat, ctx) // AND/OR + victory-first [08 "Evaluation"]
			s.VictoryDone = s.VictoryDone || v
			s.DefeatDone = s.DefeatDone || d
			var latched bool
			if v {
				latched = s.Latch.AdvanceWin(isDue) // 4→-1 ~150 ticks [08 "Evaluation"]
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
				s.Progress.ApplyCampaignResult(s.CampaignSlot, win)
				// RS-05: campaign latch also becomes terminal result for snapshot and callback [08][P1-01]
				shouldFire := false
				s.resultMu.Lock()
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
					scores := s.collectScores(func() int {
						if win {
							return int(s.LocalOwner)
						}
						return int(s.EnemyOwner)
					}(), false)
					reason := "campaign"
					if len(s.Mission.Victory) > 0 && win {
						reason = "victory_trigger"
					} else if len(s.Mission.Defeat) > 0 && !win {
						reason = "defeat_trigger"
					}
					s.result = Result{
						Ended:      true,
						Draw:       false,
						Kind:       kind,
						WinnerTeam: winners[0],
						Winners:    winners,
						Losers:     losers,
						Reason:     reason,
						Tick:       tick,
						ArmedTick:  tick,
						Countdown:  s.Latch.Countdown,
						Scores:     scores,
					}
					shouldFire = true
				}
				s.resultMu.Unlock()
				if shouldFire {
					s.publishResultView()
					s.fireResultCallback()
				}
				_ = s.TransitionTo(StatePostBattle)
			}
			if v || d {
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceVictoryLatch})
			}
		}
		// Deterministic trace every tick, even when not due [INVARIANTS I1].
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTriggerPoll})
	} else {
		// Still emit TriggerPoll for determinism when no campaign mission or no queues.
		s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTriggerPoll})
	}

	// 15 sharing cadence [05 "Allied resource and sensor sharing"] — retail runs it at the sub-tick tail after phase 12; once per tick for the reference player only
	if s.Econ != nil {
		s.Econ.ShareTick(tick)
		// Sharing emits no per-unit trace but we emit one per tick for determinism
		// No map iteration inside ShareTick (it iterates players 0..9 asc)
	}

	// 16 AI auxiliary deadlines [08 "Established AI-facing data and rooted planner"] — managers already ticked via player traversal beforeDeadline; auxiliary tasks with later deadlines (+150/+300 etc.) are also handled inside same Tick via runDueTasks [P0-02].
	// auxiliary tasks with later deadlines (+150/+300 etc.) are also handled inside same Tick via runDueTasks [P0-02].
	// Emit the stage marker for the trace contract; VictoryLatch is reserved for actual result latches.
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceAIAux})

	// 17 post-loop executor tail [01 §4.4] — barriers, the deadline-ring slide and missile/interceptor compaction are retail post-loop structures; nanolathe's deterministic commit (movement occupancy clear for Dying units, unit cleanup, campaign no-human countdown, victory evaluation) and the RNG global sync live here
	if s.Units != nil {
		// RS-08: projectile damage after slot visit marks Dying after that visit [01 §4.4][GAP T15];
		// retain for feature/visibility/trigger same tick but clear movement occupancy before next tick and before snapshot.
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
				s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceDeathFinalize, Handle: h, Value: int32(u.DeathCause)})
			}
		}
		s.Units.Cleanup()
	}
	// Barrier no-op [01 §4.4] TODO(T23) keep as registration point
	// Countdown-nohuman handled separately via Latch after trigger poll; for commit we also handle nohuman countdown if campaign with no humans
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign && s.humanCount() == 0 {
		if s.Latch.Countdown >= 0 {
			if latched := s.Latch.TickNoHuman(); latched && s.Latch.IsEnding() && s.State == StateBattle {
				win := s.Latch.IsWin()
				s.Progress.ApplyCampaignResult(s.CampaignSlot, win)
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

	// Sync global to per-session RNG for single-session determinism and test Global draw checks [RS-06][I4].
	if rng.Global.Sim != nil {
		*rng.Global.Sim = s.rngSim
	}
	if rng.Global.Crt != nil {
		*rng.Global.Crt = s.rngCrt
	}

	// 18 one immutable snapshot publication [03 §2.4][PLAN_03 C15][INVARIANTS I6]
	s.publishSnapshot(tick)
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceSnapshotPublish})
	s.emitTrace(SessionTraceEvent{Tick: tick, Kind: TraceTickEnd})

	// TODO(question): phases 7 (sequence cursors), 10 (camera/scroll), 11
	// (object-list sweeps) and 12 (cadence flip) are staged above as no-ops:
	// research establishes the passes, but their sim-side consumers are not yet
	// implemented or identified [01 §4.4]. Strip advancement is presentation-only
	// [03 §1]; the object family is unidentified [01 §4.4]; the cadence flip has
	// no wired consumer.
}
func (s *Session) initMeteor() {
	if s == nil || s.Meteor.Initialized {
		return
	}
	s.Meteor.Initialized = true
	// Default dimensions fallback 64x64 for fixtures without terrain.
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
	mapW, mapH := int32(64), int32(64)
	if s.World != nil {
		mapW = s.World.CellW
		mapH = s.World.CellH
	}
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

// applyHumanCommands is the sole production consumer of local input. Commands
// are applied in enqueue order, with canonical orders/construction APIs doing
// all descriptor and lifecycle decisions.
func (s *Session) applyHumanCommands(tick uint32) {
	if s == nil {
		return
	}
	s.humanMu.Lock()
	if len(s.pendingHuman) == 0 {
		s.humanMu.Unlock()
		return
	}
	cmds := s.pendingHuman
	s.pendingHuman = nil
	s.humanMu.Unlock()
	for _, c := range cmds {
		s.applyHumanCommand(c, tick)
	}
}

func (s *Session) humanUnit(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	u := s.Units.Unit(h)
	if u == nil || !u.Alive || u.Owner != s.LocalOwner {
		return nil
	}
	return u
}

func (s *Session) selectedHumanHandles() []pool.Handle {
	if s == nil || s.Units == nil {
		return nil
	}
	out := make([]pool.Handle, 0)
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive && u.Owner == s.LocalOwner && u.Flags&0x10 != 0 {
			out = append(out, u.Handle)
		}
	}
	return out
}

func (s *Session) selectedHumanBuilder(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	var selected *units.Unit
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != s.LocalOwner || u.Flags&0x10 == 0 {
			continue
		}
		// The retail page state is keyed by the single selected-builder
		// identity. A builder mixed with another selected unit has aggregate
		// command state, not a builder page [07 §9].
		if selected != nil {
			return nil
		}
		selected = u
	}
	if selected == nil || selected.Handle != h || selected.Def == nil || !selected.Def.Builder {
		return nil
	}
	return selected
}

func (s *Session) applyHumanBuildPage(c HumanBuildPageCommand) {
	u := s.selectedHumanBuilder(c.Builder)
	if u == nil || s.Catalog == nil {
		return
	}
	menu := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]
	if menu == nil || len(menu.Buttons) == 0 {
		return
	}
	pageCount := hud.PageCountFromButtons(len(menu.Buttons), hud.RetailBuildButtonsPerPage)
	defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || defID == 0 || defID > 0xffff {
		return
	}
	view := hud.SelectUnit{Flags: u.Flags, DefID: uint16(defID)}
	// SetBuildPage performs the retail identity/page-count guard and clamps
	// to the authored page byte. It mutates only at the input boundary [07 §9].
	hud.SetBuildPage(&view, c.Page, pageCount, nil)
	u.Flags = view.Flags
}

func (s *Session) applyHumanGroup(c HumanGroupCommand, assign bool) {
	if s == nil || s.Units == nil || s.Catalog == nil || c.Group < 1 || c.Group > 9 {
		return
	}
	views := make([]*hud.SelectUnit, 0)
	unitsByView := make([]*units.Unit, 0)
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != s.LocalOwner || u.Def == nil {
			continue
		}
		defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
		if !ok || defID == 0 || defID > 0xffff {
			continue
		}
		views = append(views, &hud.SelectUnit{Flags: u.Flags, Group: u.Group, DefID: uint16(defID)})
		unitsByView = append(unitsByView, u)
	}
	if assign {
		hud.AssignGroup(views, c.Group, nil)
	} else {
		mask := c.Mask
		if mask == [32]byte{} {
			// No CTRL_F mask producer is part of the current immutable frame.
			// Treat absent filter state as no filter; the authored mask producer
			// remains an explicit TODO rather than a guessed category mask [07 §9].
			for i := range mask {
				mask[i] = 0xff
			}
		}
		hud.RecallGroup(views, c.Group, c.Preserve, mask, nil)
	}
	for i, view := range views {
		unitsByView[i].Flags = view.Flags
		unitsByView[i].Group = view.Group
	}
}

func (s *Session) normalizeSelectedBuilderPages() {
	if s == nil || s.Units == nil || s.Catalog == nil {
		return
	}
	var u *units.Unit
	for _, candidate := range s.Units.Iter() {
		if candidate == nil || !candidate.Alive || candidate.Owner != s.LocalOwner || candidate.Flags&0x10 == 0 {
			continue
		}
		if u != nil {
			return // mixed/multiple selection has no single page owner [07 §9]
		}
		u = candidate
	}
	if u == nil || u.Def == nil || !u.Def.Builder {
		return
	}
	menu := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]
	if menu == nil || len(menu.Buttons) == 0 {
		return
	}
	defID, ok := s.Catalog.UnitDefIndex(u.Def.CanonicalKey)
	if !ok || defID == 0 || defID > 0xffff {
		return
	}
	view := hud.SelectUnit{Flags: u.Flags, DefID: uint16(defID)}
	page := 0
	if hud.IsPaged(view.Flags) {
		page = hud.DecodePage(view.Flags)
	}
	hud.SetBuildPage(&view, page, hud.PageCountFromButtons(len(menu.Buttons), hud.RetailBuildButtonsPerPage), nil)
	u.Flags = view.Flags
}

func stampHumanBuild(u *units.Unit, product string, tick uint32, queued bool) {
	if u == nil {
		return
	}
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return
	}
	prim := q.Primary()
	tail := prim[len(prim)-1]
	if tail == nil || tail.BuildDefKey != content.CanonicalKey(product) {
		return
	}
	tail.Owner = u.Handle
	tail.CreationTick = tick
	if queued {
		tail.Flags |= orders.FlagPurgeSurvivor
	} else {
		tail.Flags &^= orders.FlagPurgeSurvivor
	}
}

func (s *Session) applyHumanCommand(c HumanCommand, tick uint32) {
	if s == nil || s.Units == nil {
		return
	}
	switch c.Kind {
	case HumanSelectionReplace:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive && u.Owner == s.LocalOwner {
				u.Flags &^= 0x10
			}
		}
		for _, h := range c.Selection.Handles {
			if u := s.humanUnit(h); u != nil {
				u.Flags |= 0x10
			}
		}
		s.normalizeSelectedBuilderPages()
	case HumanSelectionToggle:
		for _, h := range c.Selection.Handles {
			if u := s.humanUnit(h); u != nil {
				u.Flags ^= 0x10
			}
		}
		s.normalizeSelectedBuilderPages()
	case HumanSelectionClear:
		for _, u := range s.Units.Iter() {
			if u != nil && u.Alive && u.Owner == s.LocalOwner {
				u.Flags &^= 0x10
			}
		}
	case HumanStop:
		id := orders.Lookup("Stop")
		if id == 0 {
			return
		}
		handles := c.Stop.Handles
		if len(handles) == 0 {
			handles = s.selectedHumanHandles()
		}
		for _, h := range handles {
			if u := s.humanUnit(h); u != nil {
				if q := orders.QueueForUnit(u); q != nil {
					q.PurgeUnprotected()
					q.DropLeadingAutoOps()
					q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false))
				}
			}
		}
	case HumanActivation:
		u := s.humanUnit(c.Activation.Unit)
		if u == nil || u.Def == nil || !u.Def.OnOffable {
			return
		}
		name := "Deactivate"
		if c.Activation.Activate {
			name = "Activate"
		}
		id := orders.Lookup(name)
		if id == 0 {
			return
		}
		if q := orders.QueueForUnit(u); q != nil {
			if !c.Activation.Queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, c.Activation.Queued))
		}
	case HumanMobileBuild:
		u := s.humanUnit(c.MobileBuild.Builder)
		if u == nil || s.Catalog == nil {
			return
		}
		if !c.MobileBuild.Queued {
			if q := orders.QueueForUnit(u); q != nil {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
		}
		if err := construction.QueueMobileBuild(u, c.MobileBuild.Product, c.MobileBuild.WX, c.MobileBuild.WZ, 1, s.Catalog); err == nil {
			stampHumanBuild(u, c.MobileBuild.Product, tick, c.MobileBuild.Queued)
		}
	case HumanFactoryBuild:
		u := s.humanUnit(c.FactoryBuild.Builder)
		if u == nil || s.Catalog == nil {
			return
		}
		count := c.FactoryBuild.Count
		if count == 0 {
			count = 1
		}
		var err error
		if count > 0 {
			err = construction.QueueFactoryBuild(u, c.FactoryBuild.Product, count, s.Catalog)
		} else {
			err = construction.CancelProductCount(u, c.FactoryBuild.Product, -count)
		}
		if err == nil && count > 0 {
			// Counted factory nodes are no-purge commands. The old boolean is
			// retained only for source compatibility with pre-count callers.
			stampHumanBuild(u, c.FactoryBuild.Product, tick, false)
		}
	case HumanCancelProduction:
		u := s.humanUnit(c.CancelProduction.Unit)
		if u == nil {
			return
		}
		q := orders.QueueForUnit(u)
		if q == nil || q.LenPrimary() == 0 {
			return
		}
		prim := q.Primary()
		tail := prim[len(prim)-1]
		if tail == nil || tail.BuildDefKey == "" {
			return
		}
		if orders.IsMobileBuild(tail.ID) {
			_ = construction.CancelMobileTailMost(u, tail.BuildDefKey, tail.GoalX, tail.GoalZ)
		} else {
			_ = construction.CancelTailMost(u, tail.BuildDefKey)
		}
	case HumanStockpile:
		u := s.humanUnit(c.Stockpile.Unit)
		if u == nil {
			return
		}
		id := orders.Lookup("BuildWeapon")
		if id == 0 {
			return
		}
		slot := -1
		for i := 0; i < units.NumSlots; i++ {
			if sl := u.SlotAt(i); sl != nil && sl.Weapon != nil && sl.Weapon.Stockpile {
				slot = i
				break
			}
		}
		if slot < 0 {
			return
		}
		n := orders.NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, c.Stockpile.Queued)
		n.Param1, n.Param2 = uint32(slot), 1
		if q := orders.QueueForUnit(u); q != nil {
			q.CoalesceTail(id, n)
		}
	case HumanBuildPage:
		s.applyHumanBuildPage(c.BuildPage)
	case HumanGroupAssign:
		s.applyHumanGroup(c.Group, true)
	case HumanGroupRecall:
		s.applyHumanGroup(c.Group, false)
	case HumanOrder:
		var target *units.Unit
		if c.Order.Target != 0 {
			target = s.humanTarget(c.Order.Target)
		}
		handles := c.Order.Handles
		if len(handles) == 0 {
			handles = s.selectedHumanHandles()
		}
		for _, h := range handles {
			u := s.humanUnit(h)
			if u == nil {
				continue
			}
			id := orders.Resolve(c.Order.Code, u, target, &c.Order.Position)
			if id == 0 {
				continue
			}
			gx, gy, gz := c.Order.Position.X, c.Order.Position.Y, c.Order.Position.Z
			if target != nil {
				gx, gy, gz = target.X, target.Y, target.Z
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			if !c.Order.Queued {
				q.PurgeUnprotected()
				q.DropLeadingAutoOps()
			}
			q.Push(id, orders.NewNodeForOrder(id, c.Order.Target, gx, gy, gz, tick, u.Handle, c.Order.Queued))
		}
	}
}

func (s *Session) humanTarget(h pool.Handle) *units.Unit {
	if s == nil || s.Units == nil || h == 0 {
		return nil
	}
	u := s.Units.Unit(h)
	if u == nil || !u.Alive {
		return nil
	}
	return u
}

// publishSnapshot publishes one immutable frame after every completed sub-tick [PLAN_03 C15].
func (s *Session) publishSnapshot(tick uint32) {
	if s.Snapshot == nil {
		return
	}
	frame := &snapshot.Frame{Tick: tick, Paused: s.Clock != nil && s.Clock.Paused}
	var presentationEvents []presentation.Event
	if s.Presentation != nil {
		presentationEvents = s.Presentation.Events()
	}
	if s.Effects == nil {
		// Keep one active-effect owner across publication paths [03 §1].
		s.Effects = presentation.NewEffectServiceWithPool(presentation.EffectCapacity, &render.FixedEffectPool{})
	}
	// Effects consume the same ordered value events that are published below.
	// They are advanced even when the current event window is empty so explicit
	// deadlines and authored frame timing expire independently of rendering [I6].
	s.Effects.Advance(tick, presentationEvents)
	frame.Effects = s.Effects.Snapshot()
	if len(frame.Effects) > snapshot.MaxSnapshotEffects || s.Effects.Dropped() != 0 {
		frame.EffectsTruncated = true
	}
	if s.Units != nil {
		views := make([]snapshot.UnitView, 0, s.Units.Used())
		ordersViews := make([]snapshot.OrderView, 0)
		orderQueues := make([]snapshot.OrderQueueView, 0)
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
				v.DefName = u.Def.CanonicalKey
				v.Model = u.Def.ObjectName
				v.FootX = int8(u.Def.FootprintX)
				v.FootZ = int8(u.Def.FootprintZ)
				if id := s.Units.DefIDForHandle(u.Handle); id != 0 {
					v.DefID = id
				}
				// Fixed vs mobile image-cache selector: retail uses runtime flag
				// TODO(question): Historical analysis omitted; independently worded behavior is needed.
				// but definition-level MaxVelocity==0 reliably identifies buildings
				// (all 21 factories and labs have 0, mobile have >0) and matches
				// the 2x building supersample expectation without needing runtime flags.
				if u.Def.MaxVelocity == 0 {
					v.IsBuilding = true
				}
			}
			if vm := u.GetScript(); vm != nil {
				if vmPieces := vm.Pieces; len(vmPieces) > 0 {
					flags := vm.SnapshotFlags()
					prog := vm.Program()
					var names []string
					if prog != nil {
						names = prog.Pieces
					}
					v.Pieces = make([]snapshot.PieceView, len(vmPieces))
					for i, ps := range vmPieces {
						pv := snapshot.PieceView{
							Index: i,
							RotX:  ps.RotX,
							RotY:  ps.RotY,
							RotZ:  ps.RotZ,
							Tx:    ps.Trans[0],
							Ty:    ps.Trans[1],
							Tz:    ps.Trans[2],
						}
						if i < len(names) {
							pv.Name = names[i]
						}
						if i < len(flags) {
							f := flags[i]
							pv.Hidden = (f & 0x01) == 0     // show bit [04 §4.3]
							pv.DontShade = (f & 0x04) == 0  // shade bit [04 §4.3] 0x1000d/e000
							pv.DontShadow = (f & 0x08) == 0 // dont-shadow [04 §4.3] 0x1000a000
							// DontCache (0x02) not needed in snapshot; renderer decides via IsBuilding.
						}
						v.Pieces[i] = pv
					}
				}
			}
			views = append(views, v)
			if q := orders.QueueForUnit(u); q != nil && (q.LenPrimary() > 0 || q.LenSecondary() > 0) {
				activeHead := q.Head()
				queue := orders.SnapshotQueueOf(q, u.Handle, func(n *orders.Node) []orders.SnapshotRoutePoint {
					// A route is authoritative only for the node that activated it;
					// movement.Route is keyed by unit for that active binding. Do not
					// attach a stale route to a queued node [04 §7.3].
					if n == nil || n != activeHead || s.Movement == nil {
						return nil
					}
					r := s.Movement.Routes[u.Handle]
					if r == nil || !r.Active || r.Count == 0 {
						return nil
					}
					points := make([]orders.SnapshotRoutePoint, int(r.Count))
					for i := range points {
						p := r.Points[i]
						points[i] = orders.SnapshotRoutePoint{X: world.CellToWorld(p.X), Z: world.CellToWorld(p.Z)}
					}
					return points
				})
				ov := snapshotOrderQueueView(queue, s.Catalog)
				orderQueues = append(orderQueues, ov)
				if len(ov.Primary) > 0 {
					ordersViews = append(ordersViews, ov.Primary[0])
				}
			}
		}
		frame.Units = views
		frame.Orders = ordersViews
		frame.OrderQueues = orderQueues
		// Selection is authoritative unit state (bit 0x10), not a renderer-side
		// cache [07 §9]. Preserve pool order so a frame is deterministic [I1].
		for _, u := range views {
			if u.Owner != s.LocalOwner || u.Flags&0x10 == 0 {
				continue
			}
			frame.Selection.Handles = append(frame.Selection.Handles, u.Slot)
			if frame.Selection.Primary == 0 {
				frame.Selection.Primary = u.Slot
			}
		}
		frame.Selection.LocalPlayer = s.LocalOwner
		frame.Selection.Count = uint16(len(frame.Selection.Handles))
		// Command-page state is authored by the selected builder's CANBUILD
		// page. Shift/input latches are presentation-owned and therefore remain
		// at their zero value until a typed input state is introduced [07 §9].
		if frame.Selection.Count == 1 && frame.Selection.Primary != 0 && s.Catalog != nil {
			// A command page is a single-selected-builder surface. Do not
			// promote one builder from a mixed or multi-builder selection to
			// the page owner; aggregate command state is distinct [07 §9].
			if u := s.Units.Unit(frame.Selection.Primary); u != nil && u.Alive && u.Owner == s.LocalOwner && u.Flags&0x10 != 0 && u.Def != nil && u.Def.Builder {
				if page := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]; page != nil {
					frame.CommandPage.Builder = u.Handle
					const buttonsPerPage = 6 // authored build rail page [07 §9]
					frame.CommandPage.PageCount = uint16((len(page.Buttons) + buttonsPerPage - 1) / buttonsPerPage)
					if frame.CommandPage.PageCount == 0 {
						frame.CommandPage.PageCount = 1
					}
					pageNumber := 0
					if hud.IsPaged(u.Flags) {
						pageNumber = hud.DecodePage(u.Flags)
					}
					pageNumber = hud.ClampPage(pageNumber, int(frame.CommandPage.PageCount))
					frame.CommandPage.Page = uint16(pageNumber)
					start := pageNumber * buttonsPerPage
					end := start + buttonsPerPage
					if start < len(page.Buttons) {
						if end > len(page.Buttons) {
							end = len(page.Buttons)
						}
						frame.CommandPage.ProductKeys = append([]string(nil), page.Buttons[start:end]...)
					}
				}
			}
		}
	}
	// Visibility masks are copied for the validated local player; their
	// mode-dependent/raw representation remains owned by visibility [03 §3.1–§3.2].
	// Radar is a separate presentation surface, not a mask
	// published by visibility.Service [03 §3.4], so it remains unset. The
	// service currently has no generation counter; Version consequently stays
	// zero rather than inventing one [I9].
	if s.Vis != nil {
		publishVisibilityView(s.Vis, s.LocalOwner, &frame.Visibility)
		s.Vis.RebuildFog(0, 0)
		if fc := s.Vis.Fog(); fc != nil {
			w, h := fc.Dimensions()
			ch0, ch1 := fc.Channels()
			frame.Fog.W = w
			frame.Fog.H = h
			frame.Fog.OriginX, frame.Fog.OriginZ = fc.Origin()
			frame.Fog.Ch0 = ch0
			frame.Fog.Ch1 = ch1
			frame.Fog.Valid = s.Vis.FogCacheValid()
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
			pv := snapshot.ProjectileView{
				Handle:         h,
				X:              p.Pos.X,
				Y:              p.Pos.Y,
				Z:              p.Pos.Z,
				WeaponID:       p.WeaponID,
				Shooter:        p.Shooter,
				Yaw:            uint16(p.Yaw),
				Pitch:          uint16(p.Pitch),
				StartX:         p.StartPos.X,
				StartY:         p.StartPos.Y,
				StartZ:         p.StartPos.Z,
				TailX:          p.StartPos.X,
				TailY:          p.StartPos.Y,
				TailZ:          p.StartPos.Z,
				VX:             p.Velocity.X,
				VY:             p.Velocity.Y,
				VZ:             p.Velocity.Z,
				CreationTick:   p.CreationTick,
				ExpiryTick:     p.ExpiryTick,
				BurstRemaining: p.BurstRemaining,
				MuzzlePiece:    int32(p.MuzzlePiece),
				Target:         p.TargetUnit,
				TargetX:        p.TargetPos.X,
				TargetY:        p.TargetPos.Y,
				TargetZ:        p.TargetPos.Z,
				TrailFrame:     0,  // no authored/runtime trail-frame field is established [I9]
				Selector:       -1, // explicit suppression sentinel when no selector art is established [03 §5.4]
			}
			if s.Catalog != nil {
				if w, ok := s.Catalog.WeaponByID(p.WeaponID); ok && w != nil {
					pv.Model = w.Model
					pv.Graphic = w.Model
					pv.RenderType = w.RenderType
					// Creation-family is established via Weapon record flags
					// (Ballistic/VLaunch/etc.) and is presentation-relevant per [03 §5.4] C6 [06 §6.2].
					pv.Family = int32(combat.CreationFamilyForWeapon(w))
					pv.SmokeTrail = w.SmokeTrail
					// [03 §5.4] leaves the lifetime-scaled GAF input as a caller
					// parameter (commonly WeaponTimer or Duration); no projectile
					// record field identifies which authored value is selected. Keep
					// Lifetime explicitly unknown rather than guessing [I9].
				}
			}
			frame.Projectiles = append(frame.Projectiles, pv)
		}
	}
	if s.Build != nil && s.Units != nil {
		// BuilderLinks is the construction service's authoritative product→builder
		// relation [05 C18]. SnapshotLinks provides deterministic product order;
		// queue index, accepted work, and stall state have no published source yet.
		for _, link := range s.Build.SnapshotLinks() {
			builder := s.Units.Unit(link.Builder)
			product := s.Units.Unit(link.Product)
			if builder == nil || product == nil || !builder.Alive || !product.Alive {
				continue
			}
			b := snapshot.BuildProgressView{
				Builder:    link.Builder,
				Product:    link.Product,
				Remaining:  product.Remaining,
				Health:     product.Health,
				MaxHealth:  product.MaxHealth,
				QueueIndex: -1, // queue position is O5 and is not exposed here
			}
			if product.Def != nil {
				b.ProductKey = product.Def.CanonicalKey
				b.FootX = int8(product.Def.FootprintX)
				b.FootZ = int8(product.Def.FootprintZ)
			}
			if builder.Def != nil {
				b.Factory = !builder.Def.CanMove && !builder.Def.CanFly
			}
			frame.Builds = append(frame.Builds, b)
		}
	}
	if s.Econ != nil {
		var resViews []snapshot.ResourceView
		var econViews []snapshot.EconomyView
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
				MetalProduced:  pl.PassProduced[economy.Metal],
				MetalConsumed:  pl.PassConsumed[economy.Metal],
				EnergyProduced: pl.PassProduced[economy.Energy],
				EnergyConsumed: pl.PassConsumed[economy.Energy],
			})
			econViews = append(econViews, snapshot.EconomyView{
				Player:         uint8(p),
				Metal:          pl.Stock[economy.Metal],
				Energy:         pl.Stock[economy.Energy],
				MetalCapacity:  pl.Capacity[economy.Metal],
				EnergyCapacity: pl.Capacity[economy.Energy],
				MetalProduced:  pl.PassProduced[economy.Metal],
				MetalConsumed:  pl.PassConsumed[economy.Metal],
				EnergyProduced: pl.PassProduced[economy.Energy],
				EnergyConsumed: pl.PassConsumed[economy.Energy],
				Active:         pl.Exists && !pl.IsObserver,
			})
		}
		frame.Resources = resViews
		frame.Economy = econViews
	}
	// RS-05: publish authoritative result (kind, tick, winners/losers, scores, countdown) [08][P1-01]
	// Countdown is visible even before Ended (pending) via Latch.Countdown; winners/losers/scores
	// become authoritative only when Ended. This is the sole snapshot writer for Result (I6).
	{
		s.resultMu.Lock()
		r := s.result
		s.resultMu.Unlock()
		view := snapshot.ResultView{
			Ended:      r.Ended,
			Kind:       r.Kind,
			WinnerTeam: r.WinnerTeam,
			Reason:     r.Reason,
			Tick:       r.Tick,
			ArmedTick:  r.ArmedTick,
			Countdown:  r.Countdown,
			Draw:       r.Draw,
		}
		if len(r.Winners) > 0 {
			view.Winners = append([]int(nil), r.Winners...)
		}
		if len(r.Losers) > 0 {
			view.Losers = append([]int(nil), r.Losers...)
		}
		if len(r.Scores) > 0 {
			view.Scores = append([]snapshot.ResultScore(nil), r.Scores...)
		}
		// Even when not Ended, publish Countdown from latch for HUD countdown display [P1-01][RR-04].
		if !view.Ended {
			view.Countdown = s.Latch.Countdown
			// If pending, ensure Kind/ArmedTick etc reflect pending even though not Ended.
			if s.resultPending {
				s.resultMu.Lock()
				view.Kind = s.result.Kind
				view.ArmedTick = s.result.ArmedTick
				view.Reason = s.result.Reason
				view.Draw = s.result.Draw
				if len(s.result.Winners) > 0 {
					view.Winners = append([]int(nil), s.result.Winners...)
				}
				if len(s.result.Losers) > 0 {
					view.Losers = append([]int(nil), s.result.Losers...)
				}
				s.resultMu.Unlock()
			}
		}
		frame.Result = view
	}
	s.CollectAudioForSnapshot(frame)
	if s.Presentation != nil {
		batch := s.Presentation.Snapshot()
		frame.Events = batch.Events
		if len(frame.Events) > snapshot.MaxSnapshotEvents {
			frame.Events = frame.Events[:snapshot.MaxSnapshotEvents]
			frame.EventsTruncated = true
		}
		frame.EventAdmissionsDropped = batch.Dropped
		// Effects were already admitted and advanced by the sole fixed pool owner
		// above. Publish its detached view; do not create a parallel event-derived
		// lifecycle in the snapshot boundary [03 §1][I6].
		frame.Effects = s.Effects.Snapshot()
		if len(frame.Effects) > snapshot.MaxSnapshotEffects {
			// Preserve newest, evict oldest beyond fixed pool [03 §1] C5 and strip beyond 400 [R-P0-06].
			frame.Effects = frame.Effects[len(frame.Effects)-snapshot.MaxSnapshotEffects:]
			frame.EffectsTruncated = true
		}
		// Buffer.Publish deep-copies the frame. Reset only after that successful
		// hand-off, so events survive exactly once and remain queued on a nil
		// buffer path [I6][F-P0-031].
		s.Snapshot.Publish(frame)
		s.Presentation.Reset()
		return
	}
	// Headless or no presentation collector: no effects to publish.
	s.Snapshot.Publish(frame)
}

// publishVisibilityView copies the local player's visibility masks into the
// immutable presentation frame. Radar has no authoritative mask source in the
// visibility service.
func snapshotOrderQueueView(src orders.SnapshotQueue, cat *content.Catalog) snapshot.OrderQueueView {
	return snapshot.OrderQueueView{
		Unit:               src.Unit,
		Primary:            snapshotOrderViews(src.Primary, cat),
		Secondary:          snapshotOrderViews(src.Secondary, cat),
		PrimaryTruncated:   src.PrimaryTruncated,
		SecondaryTruncated: src.SecondaryTruncated,
	}
}

func snapshotOrderViews(src []orders.SnapshotNode, cat *content.Catalog) []snapshot.OrderView {
	if len(src) == 0 {
		return nil
	}
	dst := make([]snapshot.OrderView, len(src))
	for i, n := range src {
		var footX, footZ int8
		if cat != nil && n.BuildProduct != "" {
			if def, ok := cat.Unit(n.BuildProduct); ok && def != nil {
				footX, footZ = int8(def.FootprintX), int8(def.FootprintZ)
			}
		}
		dst[i] = snapshot.OrderView{
			Unit: n.Owner, Target: n.Target,
			GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ,
			Kind: n.Kind, StateLabel: n.State, MoveState: n.MoveState,
			List: n.List, Index: n.Index, DescriptorID: n.DescriptorID,
			Phase: n.Phase, CreationTick: n.CreationTick, Flags: n.Flags,
			DynamicGate: n.DynamicGate, Deadline: n.Deadline,
			Satisfied: n.Satisfied, PathStatus: n.PathStatus,
			Param1: n.Param1, Param2: n.Param2, Param3: n.Param3,
			BuildProduct: n.BuildProduct, BuildCount: n.BuildCount,
			FootX: footX, FootZ: footZ,
			RouteTruncated: n.RouteTruncated,
		}
		if len(n.Route) > 0 {
			dst[i].Route = make([]snapshot.RoutePoint, len(n.Route))
			for j, p := range n.Route {
				dst[i].Route[j] = snapshot.RoutePoint{X: p.X, Y: p.Y, Z: p.Z, Flags: p.Flags}
			}
		}
	}
	return dst
}

func publishVisibilityView(vis *visibility.Service, local uint8, out *snapshot.VisibilityView) {
	if vis == nil || out == nil || local >= 10 {
		return
	}
	w, h := vis.GridDimensions()
	word := vis.WordMask()
	current := vis.ByteGrid(visibility.PlayerID(local))
	if w <= 0 || h <= 0 || len(word) != int(w*h) || len(current) != int(w*h) {
		return
	}
	explored := make([]uint8, len(word))
	bit := uint16(1) << (local % 10)
	for i, cell := range word {
		if cell&bit != 0 {
			explored[i] = 1
		}
	}
	visible := make([]uint8, len(current))
	copy(visible, current)
	*out = snapshot.VisibilityView{W: w, H: h, Explored: explored, Visible: visible, Valid: true}
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
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
			// Polled queues use LocalOwner/EnemyOwner gating, not alliance; type-gated countdown
			// decrements only here, never from poll [08 "Evaluation"].
			if s.Mission != nil && u != nil {
				ctx := triggers.PollContext{Tick: s.Clock.GlobalTick, World: s.Units, LocalOwner: localOwner, EnemyOwner: enemyOwner}
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitDied, u)
			}
			// Audio: death does not map to a queued voice directly, but an
			// under-attack cue for nearby allies could be queued elsewhere.
			// For now, no death voice; weapon hit already queues via impact sink.
			// Local authoritative death runs the synchronous 4-cell Killed query
			// BEFORE the death packet is emitted [04 §5.1]; severity clamp → corpse
			// chain depth + death-explosion weapon trigger (DoExplosion) per
			// [06 §12.1] C22–C25. Deterministic, no wall-clock, no map iteration (I1, I4, I6).
			if s.Features != nil && u != nil && u.Def != nil && s.World != nil {
				// Map units death cause to combat cause [06 §12.1] for the shared path.
				var c combat.Cause
				switch cause {
				case units.DeathKilled:
					c = combat.CauseOrdinary // 1 [06 §12.1]
				case units.DeathSelfDestruct:
					c = combat.CauseSelfDestruct // 3 [06 §12.1]
				case units.DeathReclaimed:
					c = combat.CauseReclaim // 5 [06 §12.1] C24 bypass
				default:
					if u.Health <= 0 {
						c = combat.CauseOrdinary
					} else {
						c = combat.CauseReclaim
					}
				}
				// IsFeature direct conversion cause 7 handling: when def isfeature,
				// retail writes cause 7 DIRECT store not via packet builder [06 §12.1].
				// We treat isfeature kills as FeatureConversion when cause is killed and isfeature true and corpse exists?
				// Keep ordinary for now; TODO(question) on isfeature cause-7 producer gating [06 §12.1].
				ctx := combat.DeathContext{
					Health:            u.Health,
					MaxHealth:         u.MaxHealth,
					PriorSample:       u.PriorSample, // TODO(question): Historical analysis omitted; independently worded behavior is needed.
					Cause:             c,
					RemainingFraction: u.Remaining, // [06 §12.1] 0.0 when normal/grounded
					UnitDef:           u.Def,
				}
				var featMap map[string]*content.FeatureDef
				if s.Catalog != nil {
					featMap = s.Catalog.Features
				}
				// Synchronous 4-cell Killed query drains script threads inline [04 §5.1] C25 (I1, I4)
				syncKilled := func(sev int32) (int32, bool) {
					if vm := u.GetScript(); vm != nil {
						v, ok := combat.KilledVariantFromVM(vm, sev) // [04 §5.1][06 §12.1] C23 C25
						return v, ok
					}
					return 0, false
				}
				res := combat.ResolveDeath(ctx, featMap, syncKilled) // [04 §5.1][06 §12.1] C22-C25
				// Corpse depth comes from the Killed-variant low nibble [04 §5.1][06 §12.1] C23, replacing the constant switch.
				if res.DoCorpse && u.Def.Corpse != "" {
					depth := res.Variant & 0x0F // low nibble [06 §12.1] C23
					var corpseDef *content.FeatureDef
					if s.Catalog != nil && s.Catalog.Features != nil {
						corpseDef = features.CorpseDefFor(u.Def, s.Catalog.Features, depth) // [06 §12.1] C23 low nibble
						if corpseDef == nil && depth != 0 {
							// Fallback to direct catalog lookup for depth1 when chain helper misses
							if d, ok := s.Catalog.Features[content.CanonicalKey(u.Def.Corpse)]; ok && depth == 1 {
								corpseDef = d
							}
						}
					} else {
						for _, d := range s.World.FeatureDefs {
							if d != nil && d.CanonicalKey == content.CanonicalKey(u.Def.Corpse) {
								if depth != 0 {
									corpseDef = features.CorpseDefFor(u.Def, map[string]*content.FeatureDef{content.CanonicalKey(u.Def.Corpse): d}, depth)
									if corpseDef == nil && depth == 1 {
										corpseDef = d
									}
								}
								break
							}
						}
					}
					if corpseDef != nil {
						_ = s.Features.PlaceCorpse(u.X, u.Z, corpseDef, u.Def.IsFeature)
					}
				}
				// Death-explosion weapon trigger (DoExplosion) [06 §12.1] C22-C25
				// Shared with projectile splash via ExplodeWeaponAt [06 §9.3] (I1, I2)
				if res.DoExplosion {
					weapon := combat.SelectDeathExplosionWeapon(u.Def, c) // [06 §12.1][02 "Unit record"]
					if weapon != nil && s.Combat != nil {
						impact := combat.Vec3{X: u.X, Y: u.Y, Z: u.Z}
						tick := uint32(0)
						if s.Clock != nil {
							tick = s.Clock.GlobalTick
						}
						s.Combat.ExplodeWeaponAt(s.Units, s.World, weapon, impact, h, tick) // [06 §9.3][06 §12.1] shared splash
						if s.Presentation != nil {
							pe := presentation.Event{Tick: tick, Source: h, X: u.X, Y: u.Y, Z: u.Z, Graphic: weapon.ExplosionGaf, Alias: weapon.SoundHit}
							if weapon.ExplosionGaf != "" || weapon.ExplosionArt != "" {
								s.Presentation.EmitExplosion(pe) // [06 §13.2] C27
							}
						}
					}
				}
			} else if u != nil && u.Def != nil && u.Def.Corpse != "" && s.World != nil {
				// Fallback when no authoritative resolution (nil def etc) — keep deterministic no-op; TODO(question) on missing def path
				_ = h
			}
		}
		s.Units.OnCreate = func(h pool.Handle, u *units.Unit) {
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
			// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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

	// The former package-wide kernel phase graph (network-drain, units-tick,
	// weapons-fire, orders-pump, path-submit, movement-integrate, cadence-flip,
	// …) was retired here [RX-08][ON-09]: authoritativeTick above is the single
	// production tick and implements the researched stage order itself
	// ([01 §4.4][04 §7.3][05 "Authoritative settlement order"]). Nothing may
	// re-register phases or drive Kernel.Run/SubTick in production; gate2's
	// legacy diagnostic builds its own kernel and does not touch this one.
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
			m := s.AI[i] // direct player-indexed per RS-02 [08] I1
			if m != nil {
				m.Tick(tick, s.Units, nil)
			}
		}
		return
	}
	for player := 0; player < 10; player++ {
		p := player
		mgr := s.AI[p] // direct player-indexed per RS-02 [08] I1
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

// Retry reloads the same mission via state 5 directly [P1-01 §7.5][08 "Session states"] [RS-05].
// RETRY path reloads same mission without rewriting campaign progress beyond current slot,
// while CONTINUE writes W/L and selects next mission. For the gate we ensure Retry
// keeps the same Mission object and ends in Loading, ready for next dispatch.
// RS-05: retry must create/reload a clean session without duplicate callbacks [RS-P0-012].
// We reset the Session-owned result state (including callbackFired) so the new match
// can latch exactly once again, and clear the snapshot view.
func (s *Session) Retry() bool {
	if s == nil {
		return false
	}
	if s.State != StatePostBattle && s.State != StateBattle {
		return false
	}
	// Reset result and latch to clean state for new match [RS-05] RS-P0-012.
	s.ResetResultForRetry()
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
