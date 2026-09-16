// Test-only weapon-slot fixtures [06 §4.1] C1 [06 §4.2] C7.
//
// These used to live in slots.go. Nothing the engine ships calls them: the live
// per-slot entry is Service.StepWeaponsForUnit (DESIGN_WEAPONS_PROJECTILES
// §2.1), and the intermediate reload helpers restate steps ComputeStoredReload
// performs inline. They stay here so a test can drive one slot's gates in
// isolation and observe the order through the spy, without the fixture sitting
// in production source. A pipeline behavior change belongs in
// StepWeaponsForUnit first, and this must be kept in step with it.

package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
)

// VeteranReloadForTest exposes the intermediate veteran reload for testing C7 truncation vectors.
// Not for gameplay; tests pin the truncation order per [06 §4.2] C7.
func VeteranReloadForTest(kills int32, authoredReload int32) int32 {
	tier := veteranTier(kills)
	return int32((int64(100-6*tier) * int64(authoredReload)) / 100) // trunc [01 §8] [06 §4.2]
}

// HealthFactorForTest exposes the intermediate health factor for testing C7.
// See ComputeStoredReload for malformed TODOs.
func HealthFactorForTest(health, maxHealth int32) int32 {
	if maxHealth == 0 {
		panic("combat: zero maxHealth divide fault in HealthFactorForTest [P1-07 §2.7]")
	}
	if maxHealth < 0 {
		panic("combat: negative maxHealth divide fault [P1-07 §2.7]")
	}
	return int32(120 - (int64(20)*int64(health))/int64(maxHealth)) // [06 §4.2] trunc [01 §8] [P1-07 §2.7]
}

// ---------------------------------------------------------------------------
// Per-slot pipeline order per [06 §4.1] C1 (I7, I10) P0-10
// ---------------------------------------------------------------------------

// PipelineStep enumerates the established per-slot pipeline order [06 §4.1] C1 [06 §1.2] P0-10.
// Order is: decrement reload → resolve/validate the current target → optional
// Aim* dispatch (direct or ballistic solver, per weapon family) → the
// range/medium/ballistic shot-admission gate [06 R-WPN-05 §1] → spawner →
// store reload → debit.
// Family readiness is a SPAWNER-side decision [06 §3.3]: turret needs the
// issue latch AND a nonzero result; vertical-launch needs only the result;
// LOS/self-propelled and dropped need neither.
type PipelineStep int

const (
	StepDecrement      PipelineStep = iota // decrement nonzero reload [06 §4.1] P0-10
	StepTargetValidate                     // target validation / resolve [06 §3.1] [06 §3.2][06 R-WPN-04 §1] P0-10
	StepAimDispatch                        // Aim* dispatch [GAP T15] C9 [06 §3.3] P0-10; latch is set immediately after dispatch
	StepAdmission                          // the range/medium/ballistic shot-admission gate [06 §3.3][06 R-WPN-05 §1] P0-10
	StepSpawner                            // family spawner (allocation, Fire*/RockUnit per [06 §4.1] C2) P0-10; pool-count increment precedes the divide that can fault [GAP T5]
	StepStoreReload                        // store reload and ammo per [06 §4.2] C7/C6 (int trunc order [06 §4.2])
	StepDebit                              // debit resources per [06 §4.2] C6 (both-or-neither)
)

// String returns a debug name for the step.
func (p PipelineStep) String() string {
	switch p {
	case StepDecrement:
		return "Decrement"
	case StepTargetValidate:
		return "Target"
	case StepAimDispatch:
		return "AimDispatch"
	case StepAdmission:
		return "Admission"
	case StepSpawner:
		return "Spawner"
	case StepStoreReload:
		return "StoreReload"
	case StepDebit:
		return "Debit"
	default:
		return "Unknown"
	}
}

// PipelineSpy records the order of pipeline steps visited for one slot tick.
// Tests assert spy sequences to lock the established order [06 §4.1] C1 per I10.
type PipelineSpy struct {
	Steps []PipelineStep
}

// Record appends a step.
func (s *PipelineSpy) Record(step PipelineStep) {
	if s == nil {
		return
	}
	s.Steps = append(s.Steps, step)
}

// PipelineEnv supplies the physical and resource gates for pipeline execution.
// Each field is a predicate for that step; nil gates default to pass/true for minimal tests.
// Call order is behavior; env is consulted in fixed pipeline order [06 §4.1] (I4) and iteration is slots asc [06 §1.2] (I1).
type PipelineEnv struct {
	ValidateTarget func(slotIdx int, t Target) bool // [06 §3.1] [06 §3.2][06 R-WPN-04 §1] target validation; false clears the Aim latch and starts TargetCleared P0-10
	CheckAdmission func(slotIdx int, s *Slot) bool  // [06 §3.3][06 R-WPN-05 §1] shot-admission gate P0-10; never tests radar/cloak/jammer (NEGATIVE-BOUNDED); water/sea, toair mover-mode, ballistic-solution and range tests
	TryFire        func(slotIdx int, s *Slot) bool  // family spawner + allocation per [06 §4.1]; returns true on successful spawner return [06 §4.2] C6; pool-count increment precedes the divide that can fault [GAP T5] P0-10
	// DispatchAim starts the slot's Aim* script for this visit [GAP T15]
	// C9/C16 [06 §3.3] P0-10. Returns true if dispatched (the ballistic
	// no-solution sentinel suppresses). The latch is set immediately after
	// dispatch.
	DispatchAim func(slotIdx int, s *Slot) bool

	// Player is the firing unit's owner, debited at StepDebit [06 §4.2] C6.
	// The pipeline is the sole owner of that payment: the spawner prechecks
	// the two costs but mutates nothing, so a shot cannot be paid for twice.
	// A nil player pays nothing, which is what a shot with no economy behind
	// it (a fixture, a neutral feature) does.
	Player *economy.Player
	// Buckets is the FIRING UNIT's economy subrecord, the one the direct
	// two-resource payment credits its `requested` accumulator to
	// [05 R-ECO-01 §7]: the helper reaches through the subrecord's owner
	// pointer for the live stock, but the request stays on the subrecord. A nil
	// Buckets falls back to the player mirror.
	Buckets *[2]economy.Bucket
}

// TickSlot runs the established per-slot pipeline for slot idx in fixed order [06 §4.1] C1 P0-10.
// It returns true if charge/fire completed (spawner succeeded, reload stored, debit performed) [06 §4.2] C6.
//
// TEST-ONLY. It has no production caller: the live entry is Service.StepWeaponsForUnit,
// which the session's slot visit calls (DESIGN_WEAPONS_PROJECTILES §2.1). This helper exists so a
// test can drive one slot's gates in isolation and observe the order through the spy. A pipeline
// behavior change belongs in StepWeaponsForUnit first, and this must be kept in step with it.
// Pipeline order for test observability (spy) is recorded as:
//
//	StepDecrement → StepTargetValidate → StepAimDispatch → StepAdmission → StepSpawner → StepStoreReload → StepDebit
//
// Gates that fail short-circuit later steps but do not reorder earlier visits [06 §4.1].
// Reload decrement occurs even when later gates fail [06 §1.2] [06 §4.1] P0-10.
// Aim-ready gate consults RequiresAim() + IsAimReady() per [GAP T15] C9 [06 §3.3] P0-10; the latch is set immediately after dispatch, completion only on explicit nonzero return, no timeout.
// Target validation via ValidateTarget is the target-point resolver's dead-target path [06 R-WPN-04 §1]; false clears the Aim latch and the target words.
// On spawner success, reload is computed via ComputeStoredReload and stored, then debit is considered performed [06 §4.2] C6/C7.
// Stockpile weapons skip reload store per [06 §4.2] C7 — caller must indicate via Weapon.Stockpile.
// Determinism: caller must iterate slots 0..NumSlots-1 ascending [06 §1.2] C1 (I1); this helper does one slot and records in spy in pipeline order (I10).
func TickSlot(slot *Slot, idx int, tick uint32, spy *PipelineSpy, env PipelineEnv, health, maxHealth, kills int32) bool {
	_ = tick // tick is authoritative time for future expiry/deadline use; reload decrement is countdown, not tick compare, per [06 §1.2] [06 §4.1]. Kept for API symmetry with units tick.
	if slot == nil || !slot.IsPopulated() {
		return false
	}
	// --- Step: decrement nonzero reload [06 §4.1] [06 §1.2] P0-10 ---
	if spy != nil {
		spy.Record(StepDecrement)
	}
	slot.DecrementReload() // [06 §4.1] decrement nonzero before target resolve P0-10

	// --- Step: target validation [06 §3.1] [06 §3.2][06 R-WPN-04 §1] P0-10 ---
	if spy != nil {
		spy.Record(StepTargetValidate)
	}
	if env.ValidateTarget != nil && !env.ValidateTarget(idx, slot.Target) {
		// Stale/dead target: the resolver clears the Aim latch and rewrites
		// the target to the empty encoding, then starts the deferred
		// TargetCleared callback [06 §1.2][06 R-WPN-04 §1] P0-10.
		// Every writer preserves the latch on replacement, but stale
		// resolution DOES clear it.
		slot.Aim.IssueBit = false
		slot.Flags &^= FlagAimLatch
		// Keep Target as None for Go; caller may re-latch next tick. Emitting
		// the TargetCleared callback itself is a presentation/COB concern
		// outside this package; here we only clear the slot state.
		return false
	}

	// Aim dispatch is independent of reload, but only a clear issue latch
	// requests a new callback. Readiness gates the executor after admission
	// and costs, never the outer slot path [06 R-P0-07].
	needLatch, needResult := aimRequirement(slot.Weapon)
	if needResult && !slot.Aim.IssueBit && (slot.Weapon.Turret || !slot.Weapon.Stockpile || slot.Ammo != 0) {
		if spy != nil {
			spy.Record(StepAimDispatch)
		}
		dispatched := true
		if env.DispatchAim != nil {
			dispatched = env.DispatchAim(idx, slot)
		}
		if dispatched {
			slot.Aim.StartAim()
			slot.Flags |= FlagAimLatch
		}
	}

	// --- Step: range/medium/ballistic admission [06 §3.3] P0-10 ---
	// Shot-time physical admission tests squared planar range first; water vs non-water diverges; ballistic requires solution [06 §3.3].
	// Coverage vs engagement distinction is established: coverage drives overlay only, not ordinary fire radius [06 §3.3].
	// Shot-gate never tests radar/cloak/jammer per P0-10 NEGATIVE-BOUNDED.
	if spy != nil {
		spy.Record(StepAdmission)
	}
	// Also gate on a reload word still nonzero after decrement: only exact zero admits.
	if slot.Reload != 0 {
		return false
	}
	if env.CheckAdmission != nil && !env.CheckAdmission(idx, slot) {
		return false
	}

	// Both costs are prechecked before the spawner, so a shot that cannot be
	// paid for never reserves a projectile and never runs the callbacks
	// [06 §4.2] C6. A stockpile launch is gated on a completed round instead
	// [06 §11.1]. The precheck reads; only StepDebit writes.
	if slot.Weapon.Stockpile {
		if slot.Ammo <= 0 {
			return false
		}
	} else if env.Player != nil {
		eCost := float32(slot.Weapon.EnergyPerShot)
		mCost := float32(slot.Weapon.MetalPerShot)
		if eCost != 0 || mCost != 0 {
			if env.Player.Stock[economy.Energy] < eCost || env.Player.Stock[economy.Metal] < mCost {
				return false
			}
		}
	}

	if (needLatch && !slot.Aim.IssueBit) || (needResult && !slot.Aim.Ready) {
		return false
	}

	// --- Step: family spawner (allocation, sounds, Fire*/RockUnit) [06 §4.1] C2 P0-10 ---
	// The spawner is TryFire in fire.go, wired through env so this package's
	// pipeline does not need the caller's COB, presentation and RNG ports.
	// vel0 #DE after inc: pool count inc before divide, not rolled back P0-10 [GAP T5].
	if spy != nil {
		spy.Record(StepSpawner)
	}
	if env.TryFire != nil && !env.TryFire(idx, slot) {
		return false // spawner failure: no reload store, no debit, no Fire/RockUnit per [06 §4.1] C4 [06 §4.2] C6
	}
	if env.TryFire == nil {
		// No env means no spawner to succeed; treat as not fired for pipeline fixture unless caller explicitly passes TryFire.
		return false
	}

	// --- Step: store reload and ammo [06 §4.2] C6/C7 ---
	// Reload integer-truncation order per C7's citation [06 §4.2] where pipeline touches it (I10).
	// Stockpile launch does not write reload [06 §4.2] C7.
	if spy != nil {
		spy.Record(StepStoreReload)
	}
	if !slot.Weapon.Stockpile {
		stored := ComputeStoredReload(health, maxHealth, kills, slot.Weapon.ReloadTime) // [06 §4.2] C7 trunc order [01 §8] I3
		stored = int32(int16(stored))                                                   // signed-16 slot store [06 §4.2]
		slot.Reload = stored                                                            // store reload, signed 16-bit logical field P0-10
		slot.PendingReload = stored                                                     // latch for diagnostics (I13)
	}
	// Stockpile launch decrements ammunition and writes no reload [06 §4.2] C7.
	// Retail's stockpile remainder is an unsigned byte, decremented after
	// spawner success [06 §1.2] P0-10.
	if slot.Weapon.Stockpile && slot.Ammo > 0 {
		slot.Ammo-- // [06 §11.1] byte-sized completed rounds decrement P0-10
	}

	// --- Step: debit resources (both-or-neither) [06 §4.2] C6 ---
	if spy != nil {
		spy.Record(StepDebit)
	}
	// Debit happens only after a successful spawner return; both costs were
	// prechecked by the spawner, and this recheck-and-debit pays both or
	// neither [05 "Direct two-resource payment"] C11 [06 §4.2] C6.
	// A stockpile launch performs no per-launch debit — its metal and energy
	// were spent building the round [06 §4.2] C6.
	if !slot.Weapon.Stockpile && env.Player != nil {
		eCost := float32(slot.Weapon.EnergyPerShot)
		mCost := float32(slot.Weapon.MetalPerShot)
		if eCost != 0 || mCost != 0 {
			economy.ImmediateDebit(env.Player, env.Buckets, eCost, mCost)
		}
	}
	// On successful allocation for turret families, Ready and latch are cleared per [06 §3.3] P0-10?
	// Turret success clears latch; vertical preserves? For now keep Ready as is; firing does not automatically clear latch in this model - caller may clear.
	return true
}

// TickUnitSlots runs the per-unit slot pipeline for all three slots in numeric order [06 §1.2] C1 (I1) P0-10.
// It is the unit-level wrapper that visits slots 0..NumSlots-1 ascending and calls TickSlot for each.
// TEST-ONLY, like TickSlot above: no production caller, and the live per-unit entry is
// Service.StepWeaponsForUnit (DESIGN_WEAPONS_PROJECTILES §2.1).
// Determinism: iteration order is fixed ascending (I1); no map iteration.
// Returns the count of slots that fired this tick.
func TickUnitSlots(slots *[NumSlots]Slot, tick uint32, spy *PipelineSpy, env PipelineEnv, health, maxHealth, kills int32) int {
	if slots == nil {
		return 0
	}
	fired := 0
	// Iterate slots in numeric order [06 §1.2] C1 (I1) P0-10.
	for idx := 0; idx < NumSlots; idx++ {
		slot := &slots[idx]
		if TickSlot(slot, idx, tick, spy, env, health, maxHealth, kills) {
			fired++
		}
	}
	return fired
}
