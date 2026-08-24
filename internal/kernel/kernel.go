// Package kernel implements the deterministic twelve-phase tick graph.
//
// The order and tick increment position are retail behavior [01 §4.4]:
// each sub-tick increments the global tick before phase 1, then runs
// phases 1..12 in fixed numeric order. Registration order within a
// phase is registration call order and is part of behavior — the single
// registration site is internal/session (PLAN_14 WU-14-2) [GAP T15].
package kernel

// Phase identifies one of the twelve sub-tick phases [01 §4.4].
type Phase int

const (
	PhaseNetwork              Phase = iota // 1 multiplayer frame/network drain (no-op single-player)
	PhaseUnitsScripts                      // 2 unit and script/COB updates
	PhaseProjectiles                       // 3 projectile integration and collision opportunities
	PhaseEffectsFeatureMotion              // 4 general effects/features motion and compaction pre-pass
	PhaseOrdersPathEconomy                 // 5 orders, path/economy, and occupancy work
	PhaseFeatureLifecycle                  // 6 feature lifecycle and reclaim/death processing
	PhaseSequences                         // 7 sequence/effect-strip advancement
	PhaseWindJitter                        // 8 wind jitter/randomized interval update
	PhaseWindField                         // 9 wind-field update
	PhaseLedgerCleanup                     // 10 ledger/death cleanup
	PhaseBarrier                           // 11 ten-object vtable-backed barrier pass — TODO(T23): consumers unidentified
	PhaseCadenceFlip                       // 12 every-eight-sub-tick cadence flip
	PhaseCount
)

// phaseName returns a stable diagnostic name for p. It is not sim input.
var phaseNames = [PhaseCount]string{
	PhaseNetwork:              "network",
	PhaseUnitsScripts:         "units-scripts",
	PhaseProjectiles:          "projectiles",
	PhaseEffectsFeatureMotion: "effects-feature-motion",
	PhaseOrdersPathEconomy:    "orders-path-economy",
	PhaseFeatureLifecycle:     "feature-lifecycle",
	PhaseSequences:            "sequences",
	PhaseWindJitter:           "wind-jitter",
	PhaseWindField:            "wind-field",
	PhaseLedgerCleanup:        "ledger-cleanup",
	PhaseBarrier:              "barrier",
	PhaseCadenceFlip:          "cadence-flip",
}

func (p Phase) String() string {
	if p < 0 || p >= PhaseCount {
		return "unknown"
	}
	return phaseNames[p]
}

type entry struct {
	name string
	fn   func(tick uint32)
}

// Kernel runs the twelve-phase tick graph in fixed order every sub-tick.
//
// Zero value is ready to use. Register all phases before the first SubTick;
// registration after ticking has started is allowed but the order remains
// registration call order within each phase, which is part of deterministic
// behavior [01 §4.4][GAP T15].
type Kernel struct {
	tick   uint32
	phases [PhaseCount][]entry
}

// Tick returns the current global tick. It is incremented before phase 1
// of each sub-tick [01 §4.4] (C6).
func (k *Kernel) Tick() uint32 { return k.tick }

// Register appends fn to phase p. Call order within p is preserved and is
// part of behavior — registration happens in one place (internal/session),
// not scattered through init() [01 §4.4][GAP T15].
//
// p must be in [0, PhaseCount), fn must be non-nil. Violations panic
// because they are programming errors, not runtime failures.
func (k *Kernel) Register(p Phase, name string, fn func(tick uint32)) {
	if p < 0 || p >= PhaseCount {
		panic("kernel: phase out of range")
	}
	if fn == nil {
		panic("kernel: nil func")
	}
	k.phases[p] = append(k.phases[p], entry{name: name, fn: fn})
}

// SubTick increments the global tick before phase 1 and then executes
// phases 1..12 in numeric order; within each phase callbacks run in
// registration order [01 §4.4] (C6, C7).
func (k *Kernel) SubTick() {
	// C6: global tick increments before phase 1 of each sub-tick [01 §4.4].
	k.tick++
	// C7: phase order is the twelve entries above, in that order, every sub-tick.
	for p := PhaseNetwork; p < PhaseCount; p++ {
		for _, e := range k.phases[p] {
			e.fn(k.tick)
		}
	}
}

// Run executes SubTick ticksToRun times. The clock budget already clamps
// the count to 0..5 [01 §4.2] (C1); values outside that range are clamped
// defensively and excess is dropped, not queued.
func (k *Kernel) Run(ticksToRun int) {
	if ticksToRun < 0 {
		ticksToRun = 0
	}
	if ticksToRun > 5 {
		ticksToRun = 5
	}
	for i := 0; i < ticksToRun; i++ {
		k.SubTick()
	}
}
