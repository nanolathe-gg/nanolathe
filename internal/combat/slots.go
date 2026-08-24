// Package combat implements weapon slots and targeting per [06].
//
// WU-09-1 owns slots.go (C1, C9) and target.go ([06 §3]).
// Other combat work units own pool.go, fire.go, aim.go, motion.go, etc.
// Do not import or mutate pool.Projectiles allocation state here; pool.go owns it.
package combat

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
)

// NumSlots is the fixed retail slot count [06 §1.2].
const NumSlots = 3 // [06 §1.2] primary, secondary, tertiary

// Slot is one weapon slot per unit [06 §1.2] C1 (I13).
//
// Retail's executable stores slot state inside the 280-byte unit record
// alongside its resolved weapon definition pointer and encoded target state,
// reload countdown, auto-target enable, Aim-request latch and async result,
// and stockpile remainder. Exact byte offsets for individual slot fields are
// not established in the current census; roles and pipeline order are
// established per [06 §1.2] [06 §4.1] [06 §3.3] [GAP T15]. Go stores named
// fields, not packed bytes (I13). Offset comments below give role citations
// and TODO(question) where the numeric offset remains untraced.
//
// Determinism: slots are visited in numeric order 0..2 [06 §1.2] C1 (I1).
type Slot struct {
	// Weapon is the resolved weapon definition for this slot [06 §1.2] C1 (I13).
	// TODO(question): retail slot +offset for weapon pointer not traced; role established [06 §1.2].
	Weapon *content.WeaponDef // [06 §1.2] (I13)

	// Reload is the countdown ticks until the slot can fire again [06 §1.2] [06 §4.1] C1.
	// Nonzero values are decremented before target resolve on each weapon tick [06 §4.1].
	// Storage is integer ticks after compile-time conversion reloadtime*30 [02 "Weapon record"].
	// TODO(question): retail slot +offset for reload not traced; role and truncation order established [06 §4.2] C7.
	Reload int32 // [06 §1.2] [06 §4.1] [06 §4.2] (I13)

	// AutoTarget is the automatic-targeting enable bit [06 §1.2] (I13).
	// TODO(question): precise bit offset untraced [06 §1.2].
	AutoTarget bool // [06 §1.2] (I13)

	// Aim is the asynchronous Aim handshake for this slot [GAP T15] C9/C16 [06 §3.3] (I13).
	// Aim.IssueBit is the Aim-request latch set after Aim* dispatch [06 §3.3] [GAP T15].
	// Aim.Ready is granted only on a nonzero Aim* script return [GAP T15] C9/C16.
	// Integrate via cob.AimSlot surface [PLAN_06 C16] (I10).
	Aim cob.AimSlot // [GAP T15] C9 [06 §3.3] (I13)

	// Target is the current encoded target state for this slot [06 §1.2] (I13).
	// TODO(question): encoding of unit vs point not fully closed [06 §3.2]; coverage vs engagement distinction established [06 §3.3].
	Target Target // [06 §1.2] (I13)

	// Ammo is the stockpile remainder when applicable [06 §1.2] [06 §11.1] (I13).
	// TODO(question): retail slot size for completed rounds is byte-sized [06 §11.1]; using int32 until mapping closed.
	Ammo int32 // [06 §1.2] (I13)

	// MuzzlePiece is the muzzle piece identity queried synchronously before initialization so burst clones can re-query [06 §4.1] C3 (I13).
	// TODO(question): exact store offset untraced [06 §4.1].
	MuzzlePiece int32 // [06 §4.1] (I13)

	// PendingReload is the reload value computed after a successful spawner return
	// but before it is committed to Reload [06 §4.2] C6/C7 (I13).
	// TODO(question): precise retail latch offset untraced; used to stage C7 truncation order before store.
	PendingReload int32 // [06 §4.2] (I13)
}

// DecrementReload decrements a nonzero reload countdown before target resolve [06 §4.1] C1.
// Returns true if a decrement occurred.
// Order: this is the first step of the per-slot pipeline per [06 §4.1] (I10).
func (s *Slot) DecrementReload() bool {
	if s == nil || s.Reload <= 0 {
		return false
	}
	s.Reload-- // [06 §4.1] decrement nonzero reload
	return true
}

// IsPopulated reports whether the slot has a resolved weapon definition [06 §1.2] C1.
func (s *Slot) IsPopulated() bool { return s != nil && s.Weapon != nil }

// IsAimReady reports whether the slot is aim-ready per [GAP T15] C9 [06 §3.3].
// Aim-ready is granted only on a nonzero Aim* script return via cob.AimSlot [GAP T15] C16/C9.
// IssueBit alone authorizes nothing [GAP T15] [06 §3.3].
func (s *Slot) IsAimReady() bool {
	if s == nil {
		return false
	}
	return s.Aim.CanFire() // [GAP T15] C9/C16: Ready only on nonzero return
}

// StartAim arms the Aim-request latch for an Aim* dispatch [GAP T15] [06 §3.3] C9.
// Caller must have cleared prior aim state and stored commanded angles before this call [04 §5.3].
func (s *Slot) StartAim() {
	if s == nil {
		return
	}
	s.Aim.StartAim() // [GAP T15] sets IssueBit, Ready stays false until completion receiver
}

// CompleteAim is the Aim* completion receiver for this slot [GAP T15] C16/C9 [06 §3.3].
// It forwards the popped script return value to the cob handshake: a zero delivery has no effect
// while any nonzero delivery marks the weapon aim-ready [GAP T15] C16. Returns current aim-ready state.
// Absent script, exhausted pool (delivery 0), or zero return leave weapon permanently unable to fire [GAP T15] C16.
func (s *Slot) CompleteAim(returnValue int32) bool {
	if s == nil {
		return false
	}
	return s.Aim.CompleteAim(returnValue) // [GAP T15] C16/C9
}

// RequiresAim reports whether this slot's weapon family requires an aim-ready gate per [06 §3.3].
// Turret and vertical-launch families gate on aim-ready; non-turret LOS/self-propelled and dropped do not [06 §3.3].
// TODO(question): guidance/tracks/cruise composability beyond turret/vlaunch gating remains open per [06 §3.3] missing/unknown.
func (s *Slot) RequiresAim() bool {
	if s == nil || s.Weapon == nil {
		return false
	}
	// [06 §3.3] turret family requires both Aim-request latch and nonzero async result; vertical-launch requires async result.
	// We model "requires aim" as either turret or vlaunch flag requiring nonzero Ready.
	if s.Weapon.Turret || s.Weapon.VLaunch { // [06 §3.3]
		return true
	}
	return false
}

// ComputeStoredReload implements the integer-truncated reload computation [06 §4.2] C7 (I3) [01 §8].
// Order is exact per [06 §4.2]:
//
//	tier           = min(floor(unsigned kills/5), 5)           // unsigned [06 §4.2]
//	veteranReload  = floor((100-6*tier)*authoredReload/100)    // trunc toward zero [01 §8]
//	healthFactor   = 120 - floor(20*health/maxHealth)          // signed trunc toward zero [01 §8]
//	storedReload   = floor(healthFactor*veteranReload/100)     // trunc toward zero [01 §8]
//
// Stockpile launch does not write reload [06 §4.2] C7 — caller must skip the store path for stockpile weapons.
// Malformed states (zero maxHealth, negative health, overflow) are explicit unknowns per [06 §4.2] PLAN_09 Explicit unknowns.
// TODO(question): [06 §4.2] zero maximum health, negative health, and out-of-range state remain untraced; guarded placeholders below.
func ComputeStoredReload(health, maxHealth int32, kills int32, authoredReload int32) int32 {
	// Tier: min(floor(unsigned kills/5),5) [06 §4.2] C7
	tier := int32(uint32(kills) / 5) // unsigned division, floor [06 §4.2]
	if tier > 5 {
		tier = 5 // clamp [06 §4.2]
	}
	// veteranReload = floor((100-6*tier)*authoredReload/100) trunc toward zero [01 §8] I3 [06 §4.2]
	veteranReload := int32((int64(100-6*tier) * int64(authoredReload)) / 100) // trunc toward zero [01 §8]

	// healthFactor = 120 - floor(20*health/maxHealth) [06 §4.2] C7
	// TODO(question): [06 §4.2] zero maximum health, negative health, overflow outside ordinary state remain malformed-state unknowns.
	if maxHealth <= 0 {
		// TODO(question): [06 §4.2] zero maxHealth malformed-state untraced; returning veteranReload as neutral placeholder until probe proves exact fault/divide behavior.
		return veteranReload
	}
	// For ordinary positive health values, floor vs trunc agree; use trunc toward zero per I3 [01 §8].
	// Preserve signed health handling for negative case but clamp to TODO placeholder.
	h := health
	if h < 0 {
		// TODO(question): [06 §4.2] negative health outside ordinary state untraced; clamping to 0 as placeholder until retail trace proves signed divide handling.
		h = 0
	}
	healthFactor := int32(120 - (int64(20)*int64(h))/int64(maxHealth)) // trunc toward zero [01 §8] [06 §4.2]

	// storedReload = floor(healthFactor*veteranReload/100) trunc toward zero [01 §8] [06 §4.2]
	stored := int32((int64(healthFactor) * int64(veteranReload)) / 100) // trunc toward zero [01 §8]
	return stored                                                       // [06 §4.2] C7
}

// VeteranReloadForTest exposes the intermediate veteran reload for testing C7 truncation vectors.
// Not for gameplay; tests pin the truncation order per [06 §4.2] C7.
func VeteranReloadForTest(kills int32, authoredReload int32) int32 {
	tier := int32(uint32(kills) / 5)
	if tier > 5 {
		tier = 5
	}
	return int32((int64(100-6*tier) * int64(authoredReload)) / 100) // trunc [01 §8] [06 §4.2]
}

// HealthFactorForTest exposes the intermediate health factor for testing C7.
// See ComputeStoredReload for malformed TODOs.
func HealthFactorForTest(health, maxHealth int32) int32 {
	if maxHealth <= 0 {
		// TODO(question): zero maxHealth untraced [06 §4.2]
		return 120
	}
	h := health
	if h < 0 {
		// TODO(question): negative health untraced [06 §4.2]
		h = 0
	}
	return int32(120 - (int64(20)*int64(h))/int64(maxHealth)) // [06 §4.2] trunc [01 §8]
}

// ---------------------------------------------------------------------------
// Per-slot pipeline order per [06 §4.1] C1 (I7, I10)
// ---------------------------------------------------------------------------

// PipelineStep enumerates the established per-slot pipeline order [06 §4.1] C1 [06 §1.2].
// Order is: decrement reload → aim-ready check → target validation → charge/fire (admission → spawner → store reload → debit).
// Reload integer-truncation order per C7's citation [06 §4.2] is applied at the store-reload step where the pipeline touches it.
type PipelineStep int

const (
	StepDecrement      PipelineStep = iota // decrement nonzero reload [06 §4.1]
	StepAimReadyCheck                      // aim-ready gate [GAP T15] C9 [06 §3.3]
	StepTargetValidate                     // target validation / resolve [06 §3.1] [06 §3.2]
	StepAdmission                          // range/medium/ballistic admission [06 §3.3]
	StepSpawner                            // family spawner (allocation, Fire*/RockUnit per [06 §4.1] C2)
	StepStoreReload                        // store reload and ammo per [06 §4.2] C7/C6 (int trunc order [06 §4.2])
	StepDebit                              // debit resources per [06 §4.2] C6 (both-or-neither)
)

// String returns a debug name for the step.
func (p PipelineStep) String() string {
	switch p {
	case StepDecrement:
		return "Decrement"
	case StepAimReadyCheck:
		return "AimReady"
	case StepTargetValidate:
		return "Target"
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
	ValidateTarget func(slotIdx int, t Target) bool // [06 §3.1] [06 §3.2] target validation
	CheckAdmission func(slotIdx int, s *Slot) bool  // [06 §3.3] range/medium/ballistic admission; coverage vs engagement distinction is established — coverage drives overlay only [06 §3.3]
	TryFire        func(slotIdx int, s *Slot) bool  // family spawner + allocation per [06 §4.1]; returns true on successful spawner return [06 §4.2] C6
}

// TickSlot runs the established per-slot pipeline for slot idx in fixed order [06 §4.1] C1.
// It returns true if charge/fire completed (spawner succeeded, reload stored, debit performed) [06 §4.2] C6.
// Pipeline order for test observability (spy) is recorded as:
//
//	StepDecrement → StepAimReadyCheck → StepTargetValidate → StepAdmission → StepSpawner → StepStoreReload → StepDebit
//
// Gates that fail short-circuit later steps but do not reorder earlier visits [06 §4.1].
// Reload decrement occurs even when later gates fail [06 §1.2] [06 §4.1].
// Aim-ready gate consults RequiresAim() + IsAimReady() per [GAP T15] C9 [06 §3.3]; scope tick only decrements then checks.
// Target validation and admission are delegated to env; a nil env defaults to pass for that gate in tests.
// On spawner success, reload is computed via ComputeStoredReload and stored, then debit is considered performed [06 §4.2] C6/C7.
// Stockpile weapons skip reload store per [06 §4.2] C7 — caller must indicate via Weapon.Stockpile.
// Determinism: caller must iterate slots 0..NumSlots-1 ascending [06 §1.2] C1 (I1); this helper does one slot and records in spy in pipeline order (I10).
func TickSlot(slot *Slot, idx int, tick uint32, spy *PipelineSpy, env PipelineEnv, health, maxHealth, kills int32) bool {
	_ = tick // tick is authoritative time for future expiry/deadline use; reload decrement is countdown, not tick compare, per [06 §1.2] [06 §4.1]. Kept for API symmetry with units tick.
	if slot == nil || !slot.IsPopulated() {
		return false
	}
	// --- Step: decrement nonzero reload [06 §4.1] [06 §1.2] ---
	if spy != nil {
		spy.Record(StepDecrement)
	}
	slot.DecrementReload() // [06 §4.1] decrement nonzero before target resolve
	// When reload is still nonzero after decrement, the remaining gates still run but spawner will be gated by reload>0 check at admission/spawner.
	// For WU-09-1 pipeline order fixture, we model that as admission failing when reload>0 still; simplest is to let env decide.
	// To preserve observable order, we always proceed to aim-ready check next.

	// --- Step: aim-ready check [GAP T15] C9 [06 §3.3] ---
	if spy != nil {
		spy.Record(StepAimReadyCheck)
	}
	if slot.RequiresAim() && !slot.IsAimReady() {
		// Aim-ready required but not granted (zero return never fires) [GAP T15] C9.
		return false // short-circuit before target validation could still be observed? For pipeline order fixture we want the check recorded before returning.
	}

	// --- Step: target validation [06 §3.1] [06 §3.2] ---
	if spy != nil {
		spy.Record(StepTargetValidate)
	}
	if env.ValidateTarget != nil && !env.ValidateTarget(idx, slot.Target) {
		return false
	}

	// --- Step: range/medium/ballistic admission [06 §3.3] ---
	// Shot-time physical admission tests squared planar range first; water vs non-water diverge; ballistic requires solution [06 §3.3].
	// Coverage vs engagement distinction is established: coverage drives overlay only, not ordinary fire radius [06 §3.3].
	if spy != nil {
		spy.Record(StepAdmission)
	}
	// Also gate on reload still nonzero: if reload>0 after decrement, admission fails (not ready to fire).
	if slot.Reload > 0 {
		return false
	}
	if env.CheckAdmission != nil && !env.CheckAdmission(idx, slot) {
		return false
	}

	// --- Step: family spawner (allocation, sounds, Fire*/RockUnit) [06 §4.1] C2 ---
	// For WU-09-1 the spawner is stubbed via env.TryFire; real family dispatch lives in WU-09-3 fire.go.
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
	if slot.Weapon != nil && !slot.Weapon.Stockpile {
		stored := ComputeStoredReload(health, maxHealth, kills, slot.Weapon.ReloadTime) // [06 §4.2] C7 trunc order [01 §8] I3
		slot.Reload = stored                                                            // store reload [06 §4.2] C7
		slot.PendingReload = stored                                                     // latch for diagnostics (I13)
	} else if slot.Weapon != nil && slot.Weapon.Stockpile {
		// Stockpile launch decrements ammunition but performs no reload write per [06 §4.2] C7; handled below at debit/ammo step.
		// TODO(question): [06 §11.1] slot-byte mapping for completed rounds vs int32 remains untraced.
	}

	// --- Step: debit resources (both-or-neither) [06 §4.2] C6 ---
	if spy != nil {
		spy.Record(StepDebit)
	}
	// Debit happens only after successful spawner return; both costs prechecked then post-spawn helper rechecks and debits both or neither [06 §4.2] C6.
	// For stockpile, launch decrements ammunition and performs no per-launch debit [06 §4.2] C6 — modeled as Ammo-- if stockpile.
	if slot.Weapon != nil && slot.Weapon.Stockpile {
		if slot.Ammo > 0 {
			slot.Ammo-- // [06 §11.1] byte-sized completed rounds decrement
		}
	} else {
		// Non-stockpile debit would be performed by economy phase; we record the step.
	}
	return true
}

// TickUnitSlots runs the per-unit slot pipeline for all three slots in numeric order [06 §1.2] C1 (I1).
// It is the unit-level wrapper that visits slots 0..NumSlots-1 ascending and calls TickSlot for each.
// Determinism: iteration order is fixed ascending (I1); no map iteration.
// Returns the count of slots that fired this tick.
func TickUnitSlots(slots *[NumSlots]Slot, tick uint32, spy *PipelineSpy, env PipelineEnv, health, maxHealth, kills int32) int {
	if slots == nil {
		return 0
	}
	fired := 0
	// Iterate slots in numeric order [06 §1.2] C1 (I1).
	for idx := 0; idx < NumSlots; idx++ {
		slot := &slots[idx]
		if TickSlot(slot, idx, tick, spy, env, health, maxHealth, kills) {
			fired++
		}
	}
	return fired
}
