package session

import (
	"fmt"
	"sync"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
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

// publicationState is the session's compact committed-frame staging boundary.
// Simulation producers append typed events to events during a tick; effects
// advance once from that ordered window and publish.go copies both views into
// the public frame. Keeping these coupled prevents a second event/effect owner
// from entering the authoritative graph [01 §4.4][03 §1].
type publicationState struct {
	events  *frame.EventBuffer
	effects *render.EffectService
}

// Phase7Service is the narrow presentation-owned callback at the phase-7
// boundary. The session invokes it once per runnable sub-tick; the callback
// advances only model-texture metadata and never returns authoritative state
// [01 §4.4][R-CRD-005 §1][I6].
// effectFrameCountResolver is the seam the strip families read an effect
// entry's frame count through; see SetEffectEntryFrameCount.
type effectFrameCountResolver func(bank, entry string) (int, bool)

// featureSequenceResolver is the seam the FEATURE phase reads authored
// animation art through; see SetFeatureSequenceResolver. For a definition's
// GAF file and one of its named sequences it reports the geometry of the frame
// the cursor is on after `visit` visits — what the smoke jitter of
// [05 R-FEAT-01 §10] pass 3a scales its two CRT draws by — and the entry's
// whole lifetime in visits, the sum over its frames of max(delay, 1), which is
// when a die, reclaim or burn animation ends.
//
// It answers from the file's bytes alone, so it is identical in every run over
// the same install (I4), and a session with no resolver (every headless run)
// gets no geometry and no length, which leaves the transition on the immediate
// replacement it took before the animation records existed.
type featureSequenceResolver func(filename, sequence string, visit int32) (w, h, xoff, yoff, visits int32, ok bool)

type Phase7Service interface {
	StepPhase7()
}

func newPublicationState(events *frame.EventBuffer) *publicationState {
	if events == nil {
		events = frame.NewEventBuffer(frame.Limits{})
	}
	return &publicationState{
		events:  events,
		effects: render.NewEffectServiceWithPool(render.EffectCapacity, &render.FixedEffectPool{}),
	}
}

// ensurePublicationState initializes the one session-owned publication
// boundary without replacing an existing staged window or active-effect pool.
// Every construction path calls this helper before installing producers
// [01 §4.4][03 §1].
func (s *Session) ensurePublicationState() *publicationState {
	if s == nil {
		return nil
	}
	if s.publication == nil {
		s.publication = newPublicationState(nil)
	}
	return s.publication
}

// Session is the canonical full Session per PLAN_14 Public API [08 "Session states"].
// State/dispatch fields (State, pendingBattle) are shared with state.go's
// eight-state machine C1-C4; the remaining fields are the authoritative simulation
// services owned centrally by this package C5.
// Go allows methods in any file, but the struct is defined once here.
type Session struct {
	State         State
	pendingBattle bool

	// effectFrameCount resolves an effect entry's frame count for the strip
	// families; see SetEffectEntryFrameCount.
	effectFrameCount effectFrameCountResolver

	// featureSequence resolves a feature animation sequence for the feature
	// phase; see SetFeatureSequenceResolver.
	featureSequence featureSequenceResolver

	Clock    *clock.State
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
	Snapshot *frame.Buffer

	publication *publicationState // staged events and admitted effects at the committed-frame boundary [01 §4.4][03 §1]
	postLoop    *postLoopState    // once-per-pump executor tail; owned by the session goroutine [01 §4.4]
	phase7      Phase7Service     // presentation-owned model-texture cadence [R-CRD-005 §1][I6]
	// P28 parity tracing is nil/no-op until explicitly enabled. Selection is a
	// sorted handle list, not a map, so it cannot affect simulation iteration.
	parityTraceEnabled bool
	paritySelection    []pool.Handle
	parityCallbacks    []cob.LifecycleEvent
	parityTraceTick    uint32
	parityTraceLimit   int
	parityTraceDropped bool

	// Radar blink is presentation-owned state whose mutation is scheduled by
	// phase 12. It is deliberately absent from snapshots and save state
	// [R-CORE-03][CRD-008].
	radarBlinkCountdown int16
	radarBlinkPhase     uint16

	// strips is the ten effect-strip object family swept at phase 11. It is
	// allocated at battle entry (createAndBindServices) and destroyed with
	// every object at battle exit [R-CORE-01 §4.4.1]; producers append by
	// literal strip index [R-STRIP-01 §1].
	strips *stripTable
	// CampaignSlot is the mission list slot for progress W/L [P1-01 §2.3] [P0-05].
	CampaignSlot int
	// campaignPlayerSide is the campaign player-table side ordinal consumed by
	// canonical commander-trigger identity [08 R-TRIG-01 §3]. It has two
	// writers, both authoritative: campaign construction stamps the new-game
	// panel's two rows (applyCampaignPlayerTableSides, [08 R-CAMP-01 §3]) and a
	// retail restore takes each slot's `Side` item from its `Player%i` account
	// [08 "Player records"]. A row neither writer supplied stays unknown and the
	// identity fails closed; it is never inferred.
	campaignPlayerSide      [10]int8
	campaignPlayerSideKnown [10]bool
	// battleEntryTailDone prevents composition and fixture seams from invoking
	// the tick-zero prime or metal-vector snapshot twice [08 R-ENTRY-01 §8].
	battleEntryTailDone bool

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
	// [P1-01 §2.2]. It starts unarmed, arms at four, and publishes ending plus
	// the outcome bits when the countdown crosses below zero.
	// Latch never clears 0x04 once set [P1-01]. Settlement freeze gates
	// countdown<0 && NOT latched [P1-01 §2.2]. Countdown arms to 4 without
	// yet setting Bits; Bits are written only when Countdown crosses below
	// zero [P1-01 §2.2][08 "Evaluation"].
	Latch EndLatch

	// Progress holds campaign W/L and BetweenMissions persistence
	// [P1-01 §2.3] via the post-battle progression handler after latch.
	Progress BankProgress

	// Result ownership [08][RS-05] is session-local. The authoritative tick is
	// the sole writer; presentation receives a copy through the committed frame.
	// One point where result becomes terminal is when latch becomes visible
	// (Ended) and Result.Ended is set; simulation stops after State leaves Battle.
	result              Result
	resultPending       bool
	resultPendingWinner int
	resultPendingLosers []int
	resultPendingReason string
	resultPendingDraw   bool
	resultArmedTick     uint32
	resultNextDue       uint32

	// Commander death is observed by the unit finalizer, then settled once at
	// the owner boundary after the live counter has been decremented. Keeping
	// this fixed array preserves slot order and prevents a death hook from
	// recursively changing the owner while it is still being accounted
	// [08 R-SKIR-01 §3].
	pendingCommanderDeaths [10]bool
	// Deathmatch's shared five-due countdown is separate from the terminal
	// result latch: crossing below zero invokes respawn and does not end the
	// match [08 R-SKIR-01 §3][R-TRIG-01 §6].
	deathmatchCountdown int16
	deathmatchNextDue   uint32
	deathmatchActive    bool
	deathmatchAttempts  uint16
	deathmatchExhausted bool
	// deathsWithNoRecordedCause counts deaths that reached the finalizer with
	// no damage-kind byte. Retail's handler always has one [06 §12.1], so a
	// nonzero value names a producer this build has not wired. Diagnostics
	// only: written at the death boundary, read by DeathsWithNoRecordedCause,
	// never by the simulation.
	deathsWithNoRecordedCause int

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
	// RNGSimSeed and RNGCrtSeed record the exact pair selected at battle entry.
	// They are metadata, not additional random state; the streams themselves
	// remain the session-owned values above [R-CORE-02].
	RNGSimSeed uint32
	RNGCrtSeed uint32

	// Shake driver owned by authoritative phase 10 [R-CORE-01 §4.4.1] DET-04.
	// All CRT draws for shake are consumed here; the client only applies the
	// published offset on the committed frame.
	shakeActive    bool
	shakeDuration  int32
	shakeRemaining int32
	shakeAmpX      int32
	shakeAmpY      int32
	shakeOffsetX   int32
	shakeOffsetY   int32

	// Visibility stamp dirty-check keys per unit handle [R-CORE-01 §4.4.1]
	// DET-06: last-published stamp cell and sight range. Keyed access only,
	// never ranged (I1).
	visStamps map[int]visStamp

	// Phase trace for DET-02 verification [01 §4.4].
	phaseTrace        []string
	phaseTraceEnabled bool
	phaseDrawTrace    []PhaseDrawDelta

	// Visibility sensor state [03 §3.4] P0-11: per-unit status bits (0x100 seen, 0x300 friendly, 0x1000 decloak)
	// and decloak deadlines tick+90. Radar callback results are retained by
	// visibility.Service and published through Frame.Radar.
	visStatus  map[int]uint32
	visDecloak map[int]uint32

	// DebugDisplayMode is the world composer's debug display mode byte
	// [03 §3.12]. Its writers are now traced and there are exactly three: the
	// battle interface initializer zeroes it, film mode's `m` key cycles it
	// `0..4` wrapping at 5, and leaving film mode zeroes it again
	// [07 R-CAM-01 §9]. Mode 1 draws the terrain-grid wireframe and mode 2 the
	// five-pixel ground-pick crosshair; both are film-mode diagnostics and
	// neither is reachable without the developer password, so zero is the value
	// ordinary play holds throughout.
	//
	// It is published unchanged through the frame as Radar.MarkerMode, which is
	// a misnomer retained only because internal/frame is not this file's to
	// rename: nothing about this byte concerns the radar.
	DebugDisplayMode uint8

	// Audio is a reference to the concrete internal/audio owner. Queue/cache/
	// controller state and presentation draining live in that package; Session
	// only produces authoritative cues and supplies world-owned resolver data
	// [03 §8.2–§8.4] [I6].
	Audio *audio.Service

	// pendingHuman is the session-owned immutable input queue. Presentation
	// enqueues value commands; authoritativeTick drains it at the network/input
	// boundary before any order/build work [01 §4.4][I6].
	humanMu      sync.Mutex
	pendingHuman []HumanCommand
	// nextHumanSequence is assigned only while holding humanMu. It gives the
	// input boundary a total order independent of producer timing; commands
	// with one due tick are applied in this order [01 §4.4].
	nextHumanSequence uint64
}

// resetRadarBlink initializes the transient radar cadence at battle entry.
// The phase is kept as a bit so later presentation publication can consume a
// stable semantic value without exposing the countdown [R-CORE-03][CRD-008].
func (s *Session) resetRadarBlink() {
	if s == nil {
		return
	}
	s.radarBlinkCountdown = 7
	s.radarBlinkPhase = 0
}

// RadarBlinkPhase returns the transient phase-12 bit for presentation
// publication. It is read-only and does not expose the countdown
// [R-CORE-03][CRD-008].
func (s *Session) RadarBlinkPhase() uint8 {
	if s == nil {
		return 0
	}
	return uint8(s.radarBlinkPhase & 1)
}

// SetPhase7Service installs the presentation-owned phase-7 callback. The
// composition root may replace it when the active client changes; nil clears
// the callback. Session simulation state never reads or stores presentation
// pixels [R-CRD-005 §1][I6].
func (s *Session) SetPhase7Service(service Phase7Service) {
	if s != nil {
		s.phase7 = service
	}
}

// PhaseDrawDelta records per-phase RNG consumption for the phase trace test [01 §4.4][01 §7.1][01 §7.2].
type PhaseDrawDelta struct {
	Phase    string
	Tick     uint32
	SimDelta uint64
	CrtDelta uint64
}

// SimRNG returns the per-session simulation RNG [01 §7.1][INVARIANTS I4][RS-P0-018].
// It is isolated per Session so two interleaved sessions do not cross-contaminate draws [RS-06].
// DET-01: single authority — the session owns the battle's Park–Miller state
// for its lifetime; there is no copy from rng.Global, no sync back, and no
// reseed anywhere but here. Battle bootstrap seeds both streams fresh via
// SeedSessionRNG (retail: sim from the QPC sum XOR constant, forced odd; CRT
// from the time-of-day helper — so every draw before battle entry is wiped
// from the streams' state [R-CORE-02]). The zero-value initialization below
// exists only for bare fixture sessions; production constructors seed
// explicitly.
func (s *Session) SimRNG() *rng.Simulation {
	if s == nil {
		return nil
	}
	if !s.rngInitialized {
		s.rngSim = rng.NewSimulation(0)
		s.rngCrt = rng.NewCRT(0)
		s.rngInitialized = true
	}
	return &s.rngSim
}

// CrtRNG returns the per-session CRT RNG [01 §7.2][INVARIANTS I4][RS-P0-018].
// DET-01: single authority — per-session CRT state; no lazy global copy.
func (s *Session) CrtRNG() *rng.CRT {
	if s == nil {
		return nil
	}
	if !s.rngInitialized {
		s.rngSim = rng.NewSimulation(0)
		s.rngCrt = rng.NewCRT(0)
		s.rngInitialized = true
	}
	return &s.rngCrt
}

// SeedSessionRNG seeds both per-session streams fresh, wiping every draw made
// before it — the Nanolathe form of retail's reseed-wipes-history property at
// battle entry [R-CORE-02]. DET-01: explicit seeding only — it does not touch
// rng.Global. The session is the sole authority for its lifetime; the
// composition layer selects the pair and passes it here.
func (s *Session) SeedSessionRNG(simSeed, crtSeed uint32) {
	if s == nil {
		return
	}
	// Every battle entry starts at tick zero before setup-owned draws. This is
	// also used by save re-entry, whose RNG state is never restored [R-CORE-02].
	if s.Clock != nil {
		s.Clock.GlobalTick = 0
	}
	s.rngSim = rng.NewSimulation(simSeed)
	s.rngCrt = rng.NewCRT(crtSeed)
	s.RNGSimSeed = simSeed
	s.RNGCrtSeed = crtSeed
	// Fresh draw census for the battle [R-CORE-02].
	s.rngInitialized = true
	// Bind AI managers to this session's RNG for isolation [RS-06][I4].
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.RNG = s.SimRNG()
		}
	}
}

// EnablePhaseTrace enables ordered phase recording for the next sub-ticks [01 §4.4].
func (s *Session) EnablePhaseTrace() {
	if s != nil {
		s.phaseTraceEnabled = true
		s.phaseTrace = s.phaseTrace[:0]
		s.phaseDrawTrace = s.phaseDrawTrace[:0]
	}
}

// PhaseTrace returns the ordered phase list recorded since EnablePhaseTrace.
func (s *Session) PhaseTrace() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.phaseTrace))
	copy(out, s.phaseTrace)
	return out
}

// PhaseDrawDeltas returns per-phase RNG deltas.
func (s *Session) PhaseDrawDeltas() []PhaseDrawDelta {
	if s == nil {
		return nil
	}
	out := make([]PhaseDrawDelta, len(s.phaseDrawTrace))
	copy(out, s.phaseDrawTrace)
	return out
}

func (s *Session) recordPhase(name string, tick uint32) {
	if s == nil || !s.phaseTraceEnabled {
		return
	}
	s.phaseTrace = append(s.phaseTrace, name)
	// Record draw deltas relative to last entry? For now, record current draws and compute delta in test.
	// We store current totals; test computes delta via difference.
	var simD, crtD uint64
	if s.rngInitialized {
		simD = s.rngSim.Draws()
		crtD = s.rngCrt.Draws()
	}
	s.phaseDrawTrace = append(s.phaseDrawTrace, PhaseDrawDelta{Phase: name, Tick: tick, SimDelta: simD, CrtDelta: crtD})
}

// RequestShake requests screen shake from the authoritative impact dispatcher
// [R-CORE-01 §4.4.1] DET-04: one amplitude value applied to BOTH axes and one
// duration taken from the impacting weapon's definition. The earlier
// distinct-axes request form had no retail source and is removed.
//
// An options bit can make requests return untouched; which authored setting
// drives that bit has no established nanolathe mapping, so requests are
// always admitted for now.
// TODO(question): map the shake options bit (request gate) to a nanolathe
// setting; until traced, the gate defaults to enabled.
func (s *Session) RequestShake(magnitude, duration int32) {
	if s == nil {
		return
	}
	// If no shake is active the two amplitude accumulators are cleared
	// [R-CORE-01 §4.4.1].
	if !s.shakeActive {
		s.shakeAmpX = 0
		s.shakeAmpY = 0
	}
	// New duration = trunc((requested + current) / 2), signed truncation
	// toward zero [R-CORE-01 §4.4.1][01 §8]; remaining is set to it; the
	// amplitude accumulates; the active flag is set when the duration is
	// positive. There is no queue, maximum, or distance falloff.
	s.shakeDuration = (s.shakeDuration + duration) / 2
	s.shakeRemaining = s.shakeDuration
	s.shakeAmpX += magnitude
	s.shakeAmpY += magnitude
	s.shakeActive = s.shakeDuration > 0
}

// ShakeOffset returns the cumulative camera jitter offset produced by phase 10
// [R-CORE-01 §4.4.1].
func (s *Session) ShakeOffset() (int32, int32) {
	if s == nil {
		return 0, 0
	}
	return s.shakeOffsetX, s.shakeOffsetY
}

// ShakeState returns the full shake driver state for frame publication
// [R-CORE-01 §4.4.1].
func (s *Session) ShakeState() (active bool, duration, remaining, ampX, ampY, offX, offY int32) {
	if s == nil {
		return false, 0, 0, 0, 0, 0, 0
	}
	return s.shakeActive, s.shakeDuration, s.shakeRemaining, s.shakeAmpX, s.shakeAmpY, s.shakeOffsetX, s.shakeOffsetY
}

// tickShake advances the authoritative shake driver once per sub-tick at
// phase 10 [R-CORE-01 §4.4.1] DET-04. Each sub-tick with an active shake and
// a positive counter consumes EXACTLY TWO CRT draws (one per axis) and steps:
//
//	sx = amplitudeX * remaining / duration      (signed, truncating)
//	offsetX = rand() * sx / 0x8000 − sx / 2     (SIGNED truncating division —
//	                                            not a shift; the difference
//	                                            matters for odd negative sums)
//
// so the envelope decays linearly with the remaining counter. The tick after
// the counter reaches zero clears the active flag and consumes NO draws. The
// jitter is a permanent random walk accumulated into the published offset;
// the final view clamp that holds it inside the map runs at presentation when
// the offset is applied. The simulation stream is never touched (I4).
func (s *Session) tickShake(tick uint32) {
	_ = tick
	if s == nil || !s.shakeActive {
		return
	}
	// Expiry: the tick after the counter reaches zero clears the flag with no
	// draws [R-CORE-01 §4.4.1].
	if s.shakeRemaining <= 0 || s.shakeDuration == 0 {
		s.shakeActive = false
		return
	}
	crt := s.CrtRNG()
	if crt == nil {
		return
	}
	// sx = amplitudeX * remaining / duration (signed, truncating).
	sx := s.shakeAmpX * s.shakeRemaining / s.shakeDuration
	sy := s.shakeAmpY * s.shakeRemaining / s.shakeDuration
	// Exactly two CRT draws per active tick [R-CORE-01 §4.4.1] (I4).
	rx := int64(crt.Rand()) // 0..0x7FFF [01 §7.2]
	ry := int64(crt.Rand())
	// offsetX = rand()*sx/0x8000 − sx/2, both divisions SIGNED and truncating
	// toward zero [R-CORE-01 §4.4.1][01 §8] — Go's int64/int32 `/` is idiv,
	// not a shift.
	dx := int32(rx*int64(sx)/0x8000 - int64(sx)/2)
	dy := int32(ry*int64(sy)/0x8000 - int64(sy)/2)
	s.shakeOffsetX += dx
	s.shakeOffsetY += dy
	s.shakeRemaining--
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
	hidden := target.IsCloaked
	if target.Def != nil {
		// Authored stealth and init-cloak are gameplay predicate inputs. The
		// selection/presentation bits in Unit.Flags are unrelated and must not
		// stand in for cloak state [03 §3.2].
		hidden = hidden || target.Def.Stealth || target.Def.InitCloaked
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

// handleTeardownA implements state 0 cleanup variant A, then state 2 [08 "Session states"].
// Platform resources are owned and released by the command/platform edge; the
// authoritative session only advances the lifecycle state here [01 §2.3].
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

// teardown preserves the state-machine callback without owning platform
// resources. Window, display, sound, archive, semaphore, and registry cleanup
// belong to their concrete command/platform owners [01 §2.3]. Simulation pools
// remain owned by their respective services. The strip table is destroyed with
// every object at battle exit [R-CORE-01 §4.4.1]; a fresh battle entry
// allocates a new one.
func (s *Session) teardown(variant int) {
	_ = variant
	s.strips.release()
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
			p.GameEnded = false
			p.EndGameCountdown = -1
		}
		// WinLoseTime is the local mission-trigger deadline [08 R-TRIG-01 §6].
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
	// The loading barrier installs battle state; its first dispatch remains deferred
	// until the next Advance call [08 "Session states"] C2.
	_ = s.CompleteLoading()
}

// handleBattle implements state 6 live battle loop [08 "Session states"].
// No-op: authoritative ticks are driven by Session.Step which checks State==StateBattle [P0-I10].
func handleBattle(s *Session) {
	_ = s
}

// handlePostBattle implements state 7 post-battle handling — results/postgame [08 "Session states"].
// It performs report/progression cleanup through the post-battle handler after
// latch [P1-01 §2.3][P1-01 §2.4].
func handlePostBattle(s *Session) {
	if s == nil {
		return
	}
	// BetweenMissions persistence is already written at latch time via ApplyCampaignResult [P1-01 §2.3];
	// this handler ensures we return to router exactly once [08 "Session states"] 7->2.
	_ = s.TransitionTo(StateRouter)
}

// RegisterAll installs cross-service lifecycle hooks. State dispatch is a
// concrete switch in Advance; the authoritative tick is called directly by
// Step with no callback graph or secondary scheduler.
func (s *Session) RegisterAll() {
	// Direct Session literals used by loaders/tests still pass through the same
	// publication topology before any authoritative producer is installed.
	s.ensurePublicationState()
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
		// economy player slots and controller states [08 "Established AI-facing data"].
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
				if foundLocal {
					s.LocalOwner = local
				}
				if foundEnemy {
					s.EnemyOwner = enemy
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
				if foundL {
					s.LocalOwner = l
				}
				if foundE {
					s.EnemyOwner = e
				}
			}
		}
		s.Units.OnDeath = func(h pool.Handle, cause units.DeathCause, u *units.Unit) {
			// Statistics are filed at the same FinalizeDeath callback boundary as
			// the other victim teardown records [06 §12.1]. Packet-aware combat
			// paths may call RecordDeathStatistics with the stored attacker side.
			s.recordFinalizedDeathStatistics(cause, u)
			// Commander identity is owner/side data, not the broad authored
			// Commander convenience flag. The owner transition is deferred until
			// FinalizeDeath has decremented the live counter [08 R-SKIR-01 §3].
			if u != nil && s.isCommanderForOwner(u) {
				if s.Econ != nil && int(u.Owner) < len(s.Econ.Players) {
					s.Econ.Players[u.Owner].StorageBonusEnabled = false
				}
				if int(u.Owner) < len(s.pendingCommanderDeaths) {
					s.pendingCommanderDeaths[u.Owner] = true
				}
			}
			// Relocated (WU-19-26): the computer player's construction throttle
			// used to be armed from here, at death finalization. [08 R-AI-01
			// §11] arms it from DAMAGE to a `cancapture` unit — one reaction
			// site reached from the damage-application path, which also stops
			// the damaged unit where it stands — and that site now exists as
			// the reaction routine of [06 §9.1] step 4 (internal/combat/
			// damage.go, bound at bindDamageReaction). Death is a different
			// event with a different cadence: a commander taking fire suspends
			// commander-led construction for one to eleven seconds, re-armed by
			// every further hit, whether or not anything dies.
			//
			// A dying unit leaves its stored AI group record here, through the
			// direct writer's remove-sentinel form: swap-delete from the record
			// only, no destination, no RNG draw. This is the death-teardown
			// caller of the retail census, closing WU-19-1's reconciliation gap
			// at its real boundary instead of only at the next 30-entry sweep
			// [08 R-P0-04 §3 "The direct manager-group writer"]. Every
			// non-observer player carries a bound manager (initializeBattleAI),
			// so this runs regardless of controller state; a human-owned unit's
			// stored group is always zero (the classifier only runs under
			// controller 2), so the removal is a harmless no-op there.
			if u != nil && int(u.Owner) < len(s.AI) {
				if mgr := s.AI[u.Owner]; mgr != nil {
					mgr.OnUnitDeath(u)
				}
			}
			if s.Vis != nil && u != nil {
				unpublishOne(s, u)
				// The central death handler appends a 60-tick temporary sight
				// source for a locally owned victim under Circular/True LOS in
				// every session kind [08 R-SESS-01 §3].
				s.appendDeathEyeball(u)
			}
			// Notification slot driven at death finalization [08 "Evaluation"].
			// Polled queues use LocalOwner/EnemyOwner gating, not alliance; type-gated countdown
			// decrements only here, never from poll [08 "Evaluation"].
			if s.Mission != nil && u != nil {
				ctx := s.missionTriggerContext(s.Clock.GlobalTick)
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitDied, u)
			}
			// The last of the fixed teardown helpers [06 §12.1]: the
			// burst-anchor sweep of [06 §4.3] / [06 §5.2]. Every pool record
			// that is still a burst scheduler owned by the victim is retired
			// silently and compacted away inside the sweep's own walk, so the
			// pellets it had not yet emitted never launch. Pellets already in
			// flight are untouched — the sweep is not a general removal of the
			// victim's projectiles.
			if s.Combat != nil {
				s.Combat.SweepBurstAnchorsForShooter(h)
			}
			// The carrier/cargo half of the central death handler, in the
			// position [06 §12.1] gives it: after the fixed teardown helpers
			// and before the death explosion and the corpse. It "detaches the
			// victim from its carrier when it has one" and then walks the
			// victim's own cargo list, applying 30000 per cargo unit with the
			// attacker set to the victim's own killer — the recorded-attacker
			// link this handler wrote at death [04 R-UNIT-06 §5], which is what
			// the cascade's kill credit reads. The cargo's cause is 3 when the
			// carrier's kind nibble is 3 and 6 otherwise, so a dying transport
			// takes its passengers with it and a dying factory never leaves a
			// free-standing nanoframe on its pad [04 R-FAC-02 §3].
			//
			// Each cargo is only MARKED here; its own finalizer runs on the
			// next slot sweep, so this hook does not re-enter [04 §2.3].
			if s.Movement != nil && s.Units != nil && u != nil {
				s.Movement.HandleDeath(s.Units, h, u.EngagementTarget)
			}
			// Audio: death does not map to a queued voice directly, but an
			// under-attack cue for nearby allies could be queued elsewhere.
			// For now, no death voice; weapon hit already queues via impact sink.
			// Local authoritative death runs the synchronous 4-cell Killed query
			// BEFORE the death packet is emitted [04 §5.1]; severity clamp → corpse
			// chain depth + death-explosion weapon trigger (DoExplosion) per
			// [06 §12.1] C22–C25. Deterministic, no wall-clock, no map iteration (I1, I4, I6).
			if s.Features != nil && u != nil && u.Def != nil && s.World != nil {
				// The cause is the recorded damage-kind byte, and only that
				// [06 §12.1]; see deathCauseForResolution.
				c := s.deathCauseForResolution(u)
				// IsFeature direct conversion cause 7 handling: when def isfeature,
				// retail writes cause 7 DIRECT store not via packet builder [06 §12.1].
				// We treat isfeature kills as FeatureConversion when cause is killed and isfeature true and corpse exists?
				// Keep ordinary for now; TODO(question) on isfeature cause-7 producer gating [06 §12.1].
				ctx := combat.DeathContext{
					Health:            u.Health,
					MaxHealth:         u.MaxHealth,
					PriorSample:       u.PriorSample, // [04 §5.1] saved prior-byte sample
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
						// The death explosion record carries NO shooter
						// [06 R-WPN-02 §5]: it is a stack structure holding
						// the selected weapon, the victim's position as both
						// points, a null target, a null shooter and the
						// victim's owning-player byte — the null shooter is
						// load-bearing, since it forces the area path
						// regardless of areaofeffect and SUPPRESSES ATTACKER
						// VETERANCY. Passing the dying unit's own handle `h`
						// here (as an earlier revision did) makes it resolve
						// as a live shooter for the rest of this tick — Dying
						// is set but Alive is not cleared until FinalizeDeath,
						// which runs later — so the explosion's damage was
						// scaled by the dying unit's own veterancy and every
						// victim's provenance was stamped with the dying
						// unit's owner, crediting kills to the wrong side
						// [06 R-DMG-01 §9] (the null shooter is what makes a
						// meteor or a death explosion credit nobody).
						// ExplodeWeaponAt has no separate parameter for the
						// record's side byte (the victim's owning-player byte
						// per [06 R-WPN-02 §5], distinct from the shooter);
						// that byte only gates §9.1's routing test and §9.3's
						// friendly/enemy sum, neither of which this call path
						// exercises, so shooter 0 alone reproduces the
						// observable contract here.
						//
						// Retired (WU-19-26): this carried an open-question marker
						// saying applyDamageToUnit stamped LastDamageSide and
						// LastDamageCause together inside one shooter-presence
						// gate, so a unit killed outright by a null-shooter
						// blast reached the death finalizer with cause 0 and
						// was filed as neither a kill nor a loss. §9.1 step 4's
						// two clauses are now separate there: the kind byte is
						// recorded unconditionally, and the side snapshot takes
						// the record's neutral side byte 10 when there is no
						// attacker, so cause 1's full path files the victim's
						// loss and credits nobody [06 §12.1].
						s.Combat.ExplodeWeaponAt(s.Units, s.World, weapon, impact, 0, tick) // [06 R-WPN-02 §5][06 R-DMG-01 §9] null-shooter record, shared splash path [06 §9.3]
						if s.publication != nil && s.publication.events != nil {
							// The death explosion draws the weapon's LAND art:
							// both keys or nothing, the bank and the entry
							// carried separately [06 R-WFX-01 §1]. This used to
							// publish the bank name as the whole identity and
							// admit an event whenever EITHER key was authored,
							// so the view named a GAF file where a composer
							// expects an entry and could resolve nothing.
							if weapon.ExplosionGaf != "" && weapon.ExplosionArt != "" {
								pe := frame.Event{
									Tick: tick, Source: h, X: u.X, Y: u.Y, Z: u.Z,
									Graphic: weapon.ExplosionArt, AssetID: weapon.ExplosionGaf,
								}
								s.publication.events.EmitExplosion(pe) // [06 §13.2] C27
							}
						}
					}
				}
				// ORDER. The central handler runs the death explosion and only
				// THEN the corpse: "the death explosion (§12.2) ... ; then the
				// corpse (§12.2)" [06 §12.1 "the timeline of one weapon death, in
				// tick order" step 3]. The two blocks used to stand the other way
				// round, which was invisible while blasts could not touch features
				// and became visible the moment they could: the wreck was stamped
				// into its own blast and destroyed by it whenever the explode
				// weapon's default damage reached the corpse definition's capacity
				// [05 R-FEAT-01 §8], so an exploding unit left no wreck at all.
				//
				// And the order is the WHOLE of it [06 R-DMG-01 §10]: the two
				// calls are adjacent in the handler with no teardown, queue
				// drain or feature-phase work between them; the blast resolves
				// fully first — a null direct unit forces the area path, whose
				// feature phase damages and kills features inline — and only
				// then is the wreck stamped. The area path carries NO exclusion
				// keyed on the dying unit, its footprint or the corpse cell (the
				// shooter exclusion is for units, and the shooter is null here),
				// so a tree or an older wreck standing where this one lands
				// takes the weapon's full default damage and can die for it. Do
				// not add a corpse-cell exemption: it would spare bystanding
				// features that retail destroys. The wreck survives its own
				// unit's blast by order alone, which is what this sequence is.
				// The TODO(question) that asked whether anything else spared it
				// is answered: nothing does.
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
			} else if u != nil && u.Def != nil && u.Def.Corpse != "" && s.World != nil {
				// Fallback when no authoritative resolution (nil def etc) — keep deterministic no-op; TODO(question) on missing def path
				_ = h
			}
		}
		s.Units.OnCreate = func(h pool.Handle, u *units.Unit) {
			// Do not publish visibility at allocator return. A phase-2 factory
			// product is attached to its authored build piece later in the same
			// construction visit; phase 5 then stamps the owning slice from that
			// carried position in the same authoritative tick [04 R-FAC-02 §2]
			// [03 R-VIS-01 §2]. Publishing here would expose the pre-attachment
			// allocation position and add a second visibility owner.
			// Created notification is present but unused by shipped conditions [08
			// "Evaluation"]; it is still driven.
			if s.Mission != nil && u != nil {
				ctx := s.missionTriggerContext(s.Clock.GlobalTick)
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitCreated, u)
			}
			// Audio: completed build emits unitcomplete [03 §8.3] slot 8.
			if s.Audio != nil && u != nil && s.Clock != nil && s.Clock.GlobalTick > 0 {
				_ = s.Audio.Emit(s.Clock.GlobalTick, audio.SlotUnitComplete, h, "")
			}
		}
		s.Units.OnCapture = func(h pool.Handle, oldOwner, newOwner uint8, u *units.Unit) {
			// Capture/transfer notification is driven here [08 "Evaluation"];
			// type-gated CaptureUnitType decrements only here.
			if s.Mission != nil && u != nil {
				ctx := s.missionTriggerContext(s.Clock.GlobalTick)
				ctx.NotificationOwner = oldOwner
				ctx.NotificationOwnerValid = true
				triggers.NotifyAll(s.Mission.Victory, s.Mission.Defeat, ctx, triggers.NotifyUnitCaptured, u)
			}
			if s.Audio != nil && u != nil && s.Clock != nil {
				_ = s.Audio.Emit(s.Clock.GlobalTick, audio.SlotCapture, h, "")
			}
		}
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
// RS-05: retry must create/reload a clean session [RS-P0-012]. We reset the
// Session-owned result state so the new match can latch exactly once again,
// and clear the snapshot view.
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
	// Campaign progress is committed when the terminal latch becomes visible;
	// CONTINUE only follows the established post-battle transition [P1-01 §2.3]
	// [P1-01 §7.5].
	return s.TransitionTo(StateRouter)
}

// The debug display mode's writers [03 §3.12][07 R-CAM-01 §9]. Retail has
// exactly three and they are all here:
//
//   - the battle interface initializer stores zero (ResetDebugDisplayMode);
//   - film mode's `m` key increments the byte and wraps it to zero when the
//     increment reaches 5, so the cycle is 0,1,2,3,4,0 (CycleDebugDisplayMode);
//   - leaving film mode stores zero again (ResetDebugDisplayMode).
//
// Nanolathe implements neither developer mode nor film mode, so nothing calls
// the cycle yet and the byte holds zero for the whole of ordinary play — which
// is what retail does too: developer mode needs the six-word `+Now` password or
// the registry pair, and film mode needs developer mode, so none of it is
// reachable in a stock configuration [07 R-CAM-01 §9].

// ResetDebugDisplayMode is the battle-entry and film-mode-exit writer.
func (s *Session) ResetDebugDisplayMode() {
	if s != nil {
		s.DebugDisplayMode = 0
	}
}

// CycleDebugDisplayMode is film mode's `m` key: increment, and wrap to zero on
// reaching 5. The comparison is against the incremented value, so 4 is a valid
// mode and 5 is never observable.
func (s *Session) CycleDebugDisplayMode() {
	if s == nil {
		return
	}
	s.DebugDisplayMode++
	if s.DebugDisplayMode == 5 {
		s.DebugDisplayMode = 0
	}
}

// NewFrontEndCRT returns the pre-battle CRT stream the front end draws from
// before a battle exists — the briefing's wind and countdown values and the
// menu-side audio owner [08 R-CAMP-01 §2][01 §7.2].
//
// It lives here, beside SeedSessionRNG, because the session package is the one
// owner of retail stream construction: the front end must not stand up a
// substitute stream of its own [INVARIANTS I4][DET-01]. The stream this
// returns is the same CRT the battle continues from; battle entry reseeds it
// through SeedSessionRNG, which wipes every draw made before that point
// [R-CORE-02].
func NewFrontEndCRT(seed uint32) rng.CRT { return rng.NewCRT(seed) }
