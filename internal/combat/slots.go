// Package combat implements weapon slots and targeting per [06].
//
// WU-09-1 owns slots.go (C1, C9) and target.go ([06 §3]).
// Other combat work units own pool.go, fire.go, aim.go, motion.go, etc.
// Do not import or mutate pool.Projectiles allocation state here; pool.go owns it.
package combat

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// NumSlots is the fixed retail slot count [06 §1.2].
const NumSlots = 3 // [06 §1.2] primary, secondary, tertiary

// Slot container layout per P0-10 [06 §1.2] [06 §4.1].
//
// Retail's executable stores slot state inside the unit record as three
// contiguous slot records, roughly 24 bytes of logical state each (a 28-byte
// stride in the executable image) [06 §1.2]. Go stores named fields, not
// packed bytes (I13); flag bit values below are the citable contract, not a
// byte offset.
//
// Each slot record holds: a flags byte carrying the armed/has-target bit
// 0x02, the Aim-request latch bit 0x01, and the tracking bit 0x10; a
// resolved weapon definition pointer; a signed reload countdown in ticks; a
// stockpile remainder byte; desired yaw and pitch; and an encoded target — a
// unit slot index when the sentinel value -0x8000 is present, otherwise a
// ground point whose world X/Z words later resolve to height through the
// terrain query [06 §1.2]. The unit record separately carries a firing
// status word and an out-of-range status byte.
//
// Determinism: slots visited 0..2 asc [06 §1.2] C1 (I1).
const (
	SlotStride                 = 0x1C
	SlotBaseOffset             = 0x1F
	SlotLogicalSize            = 24
	FlagArmed            uint8 = 0x02    // hasTarget [06 §1.2] P0-10
	FlagAimLatch         uint8 = 0x01    // Aim-request latch [06 §3.3] P0-10 (OR 0x01 immediately after Aim dispatch)
	FlagTracking         uint8 = 0x10    // tracking [06 §1.2]
	TargetSentinelUnit   int16 = -0x8000 // 0x8000 sentinel unit latch [06 §1.2] P0-10
	TargetSentinelGround int16 = 0       // any != -0x8000 is ground
)

// Slot is one weapon slot per unit [06 §1.2] C1 (I13).
//
// Retail offsets above are exact; Go stores named fields (I13).
// Determinism: slots visited numeric order 0..2 [06 §1.2] C1 (I1).
type Slot struct {
	// Weapon is the resolved weapon definition for this slot [06 §1.2] C1 (I13).
	Weapon *content.WeaponDef // [06 §1.2] P0-10

	// Reload is the countdown ticks until the slot can fire again [06 §1.2] [06 §4.1] C1.
	// Retail's field is a signed 16-bit tick counter [06 §1.2] P0-10.
	Reload int32 // s16 logical, int32 for Go

	// Flags is the slot flags byte: 0x02 armed, 0x01 Aim latch, 0x10 tracking [06 §1.2] P0-10.
	Flags uint8

	// DesiredYaw is the desired yaw, TA angle units 0..65535 [06 §1.2] P0-10.
	DesiredYaw uint16

	// DesiredPitch is the desired pitch, TA angle units [06 §1.2] P0-10.
	DesiredPitch uint16

	// AutoTarget is the automatic-targeting enable bit [06 §1.2] (I13).
	// Retained for compat; maps to Flags & FlagTracking.
	AutoTarget bool // [06 §1.2] (I13) -> Flags 0x10

	// Aim is the asynchronous Aim handshake for this slot [GAP T15] C9/C16 [06 §3.3] (I13).
	// Aim.IssueBit mirrors Flags & 0x01 latch; Aim.Ready granted only on nonzero return [GAP T15] C9/C16.
	Aim cob.AimSlot // [GAP T15] C9 [06 §3.3] (I13)

	// Target is the current encoded target state for this slot [06 §1.2] (I13).
	// Encoding: a unit slot index when the sentinel value -0x8000 is present
	// for the target mode, otherwise a ground point whose world x/z words
	// resolve to height via the terrain query [06 §1.2] P0-10.
	Target Target // [06 §1.2] (I13)

	// Ammo is the stockpile remainder, an unsigned byte in retail [06 §1.2] P0-10 [06 §11.1].
	Ammo int32 // u8 logical, int32 for Go

	// MuzzlePiece is the muzzle piece identity queried synchronously before initialization so burst clones can re-query [06 §4.1] C3 (I13).
	MuzzlePiece int32 // [06 §4.1] (I13)

	// DistanceWord is the slot's distance word, written once by the slot
	// initializer at unit construction (units.WriteSlotDistanceWords) and never
	// again. The ballistic creator divides it by the weapon velocity to form
	// `T0` [06 §6.4] (RWU-19-39); it is a per-unit constant, not a flight
	// distance to the current target.
	DistanceWord int32 // [06 §6.4] [06 R-WPN-05 §3]

	// PendingReload is the reload value computed after a successful spawner return
	// but before it is committed to Reload [06 §4.2] C6/C7 (I13).
	PendingReload int32 // [06 §4.2] (I13)
}

// IsArmed reports whether the slot has the armed/hasTarget flag 0x02 [06 §1.2] P0-10.
func (s *Slot) IsArmed() bool { return s != nil && s.Flags&FlagArmed != 0 }

// IsTracking reports the tracking flag 0x10 [06 §1.2] P0-10.
func (s *Slot) IsTracking() bool { return s != nil && s.Flags&FlagTracking != 0 }

// LatchUnitTarget installs a unit target preserving the Aim latch and stale yaw/pitch [06 §1.2] P0-10.
// Every latch writer preserves 0x01 (no AND 0xFE); replacement while Aim outstanding keeps stale yaw for one shot.
func (s *Slot) LatchUnitTarget(handle pool.Handle) {
	if s == nil {
		return
	}
	savedYaw := s.DesiredYaw
	savedPitch := s.DesiredPitch
	savedIssue := s.Aim.IssueBit
	savedReady := s.Aim.Ready
	savedFlags := s.Flags & FlagAimLatch
	s.Target = Target{Kind: TargetUnit, Unit: handle}
	s.Flags |= FlagArmed
	// Preserve latch and stale angles [06 §1.2] P0-10
	s.DesiredYaw = savedYaw
	s.DesiredPitch = savedPitch
	s.Aim.IssueBit = savedIssue
	s.Aim.Ready = savedReady
	s.Flags |= savedFlags
	if s.AutoTarget {
		s.Flags |= FlagTracking
	}
}

// InstallUnitTarget installs a unit target by pool handle preserving Aim latch [06 §1.2] P0-10.
func (s *Slot) InstallUnitTarget(h pool.Handle) {
	if s == nil {
		return
	}
	savedYaw := s.DesiredYaw
	savedPitch := s.DesiredPitch
	savedIssue := s.Aim.IssueBit
	savedReady := s.Aim.Ready
	savedFlags := s.Flags & FlagAimLatch
	s.Target = Target{Kind: TargetUnit, Unit: h}
	s.Flags |= FlagArmed
	s.DesiredYaw = savedYaw
	s.DesiredPitch = savedPitch
	s.Aim.IssueBit = savedIssue
	s.Aim.Ready = savedReady
	s.Flags |= savedFlags
}

// SetTargetUnit installs a unit target preserving Aim latch and yaw/pitch [06 §1.2] P0-10.
// The unit-latch encoding is the target-mode sentinel value -0x8000 [06 §1.2].
func (s *Slot) SetTargetUnit(handle pool.Handle) {
	if s == nil {
		return
	}
	savedYaw := s.DesiredYaw
	savedPitch := s.DesiredPitch
	savedIssue := s.Aim.IssueBit
	savedReady := s.Aim.Ready
	savedLatch := s.Flags & FlagAimLatch
	s.Target = Target{Kind: TargetUnit, Unit: handle}
	s.Flags |= FlagArmed
	s.Aim.IssueBit = savedIssue
	s.Aim.Ready = savedReady
	s.Flags = (s.Flags &^ FlagAimLatch) | savedLatch
	s.DesiredYaw = savedYaw
	s.DesiredPitch = savedPitch
}

// SetTargetGround installs a ground point target preserving Aim latch [06 §1.2] P0-10.
// Ground encoding: world x/z words, distinct from the unit-latch sentinel [06 §1.2].
func (s *Slot) SetTargetGround(x, z numeric.Fixed) {
	if s == nil {
		return
	}
	savedYaw := s.DesiredYaw
	savedPitch := s.DesiredPitch
	savedIssue := s.Aim.IssueBit
	savedReady := s.Aim.Ready
	savedLatch := s.Flags & FlagAimLatch
	s.Target = Target{Kind: TargetPoint, X: x, Z: z}
	s.Flags |= FlagArmed
	s.Aim.IssueBit = savedIssue
	s.Aim.Ready = savedReady
	s.Flags = (s.Flags &^ FlagAimLatch) | savedLatch
	s.DesiredYaw = savedYaw
	s.DesiredPitch = savedPitch
}

// ClearStaleTarget clears the target and Aim latch on stale/dead resolution [06 §1.2] P0-10.
// The target-point resolver rewrites a unit target's slot words to the empty
// encoding and starts the deferred TargetCleared callback whenever the
// target's definition index has gone to zero (a freed slot) [06 R-WPN-04 §1].
func (s *Slot) ClearStaleTarget() {
	if s == nil {
		return
	}
	s.Target = Target{Kind: TargetNone}
	s.Aim.IssueBit = false
	s.Aim.Ready = false
	s.Flags &^= FlagAimLatch
	// The armed flag 0x02 is cleared separately, on the STOP order path, not here [06 §1.2]; stale resolution only clears the target words and Aim latch.
}

// DecrementReload decrements a nonzero reload countdown before target resolve [06 §4.1] C1.
// Returns true if a decrement occurred.
// Order: this is the first step of the per-slot pipeline per [06 §4.1] (I10).
func (s *Slot) DecrementReload() bool {
	if s == nil || s.Reload <= 0 {
		return false
	}
	s.Reload-- // [06 §4.1] decrement nonzero reload, signed 16-bit logical field P0-10
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
	return s.Aim.CanFire() // [GAP T15] C9/C16: Ready only on nonzero return, no timeout P0-10
}

// StartAim arms the Aim-request latch for an Aim* dispatch [GAP T15] [06 §3.3] C9.
// Caller must have cleared prior aim state and stored commanded angles before this call [04 §5.3].
// The latch is set immediately after the Aim callback is dispatched [06 §3.3] P0-10.
func (s *Slot) StartAim() {
	if s == nil {
		return
	}
	s.Aim.StartAim() // [GAP T15] sets IssueBit, Ready stays false until completion receiver [06 §3.3] P0-10
	s.Flags |= FlagAimLatch
}

// CompleteAim is the Aim* completion receiver for this slot [GAP T15] C16/C9 [06 §3.3].
// It forwards the popped script return value to the cob handshake: a zero delivery has no effect
// while any nonzero delivery marks the weapon aim-ready [GAP T15] C16. Returns current aim-ready state.
// Absent script, exhausted pool (delivery 0), or zero return leave weapon permanently unable to fire [GAP T15] C16.
// No timeout writer exists P0-10 (NEGATIVE-BOUNDED).
func (s *Slot) CompleteAim(returnValue int32) bool {
	if s == nil {
		return false
	}
	got := s.Aim.CompleteAim(returnValue) // [GAP T15] C16/C9, 0 vs nonzero P0-10
	if returnValue != 0 {
		s.Flags |= FlagAimLatch // keep latch on nonzero? Actually latch already set; Ready set.
	}
	return got
}

// aimRequirement reports the per-family readiness rule [06 §3.3]: turret
// families need the Aim issue latch AND a nonzero result; vertical-launch
// needs only the result; LOS/self-propelled and dropped need neither.
// TODO(question): guidance/tracks/cruise composability beyond turret/vlaunch
// gating remains open per [06 §3.3] missing/unknown.
func aimRequirement(w *content.WeaponDef) (needLatch, needResult bool) {
	if w == nil {
		return false, false
	}
	if w.Turret {
		return true, true // [06 §3.3] P0-10 turret needs latch+nonzero+drift
	}
	if w.VLaunch {
		return false, true // [06 §3.3] P0-10 vertical needs result only
	}
	return false, false
}

// RequiresAim reports whether this slot's weapon family requires an aim result per [06 §3.3].
func (s *Slot) RequiresAim() bool {
	if s == nil {
		return false
	}
	_, needResult := aimRequirement(s.Weapon)
	return needResult
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
func ComputeStoredReload(health, maxHealth int32, kills int32, authoredReload int32) int32 {
	// Tier: min(floor(unsigned kills/5),5) [06 §4.2] C7
	tier := int32(uint32(kills) / 5) // unsigned division, floor [06 §4.2]
	if tier > 5 {
		tier = 5 // clamp [06 §4.2]
	}
	// veteranReload = floor((100-6*tier)*authoredReload/100) trunc toward zero [01 §8] I3 [06 §4.2]
	veteranReload := int32((int64(100-6*tier) * int64(authoredReload)) / 100) // trunc toward zero [01 §8]

	// healthFactor = 120 - floor(20*health/maxHealth) [06 §4.2] C7
	// Zero maxHealth raises DIV fault after pool reservation not rolled back [P1-07 §2.7][GAP T5] I11.
	// Stock MaxDamage is always >0, so fault is malformed-only; we reproduce as panic like retail #DE.
	if maxHealth == 0 {
		panic("combat: zero maxHealth divide fault in reload healthFactor [P1-07 §2.7][GAP T5]") // retail #DE [P1-07 §2.7]
	}
	if maxHealth < 0 {
		// Negative max wraps as large unsigned; treat as fault path same as zero for determinism
		panic("combat: negative maxHealth divide fault [P1-07 §2.7]")
	}
	// For ordinary positive health values, floor vs trunc agree; use trunc toward zero per I3 [01 §8].
	// Negative health is preserved modular via 16-bit wrap in damage path; for reload the signed
	// health is used directly and trunc toward zero is the retail IDIV [P1-07 §2.7].
	healthFactor := int32(120 - (int64(20)*int64(health))/int64(maxHealth)) // trunc toward zero [01 §8] [06 §4.2] [P1-07 §2.7]

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
	StepAimDispatch                        // Aim* dispatch / readiness wait [GAP T15] C9 [06 §3.3] P0-10; latch is set immediately after dispatch
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
}

// TickSlot runs the established per-slot pipeline for slot idx in fixed order [06 §4.1] C1 P0-10.
// It returns true if charge/fire completed (spawner succeeded, reload stored, debit performed) [06 §4.2] C6.
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
		slot.Aim.Ready = false
		slot.Flags &^= FlagAimLatch
		// Keep Target as None for Go; caller may re-latch next tick. Emitting
		// the TargetCleared callback itself is a presentation/COB concern
		// outside this package; here we only clear the slot state.
		return false
	}

	// --- Step: optional Aim* dispatch [GAP T15] C9/C16 [06 §3.3] P0-10 ---
	// Family readiness is a spawner-side decision, but the DISPATCH belongs
	// here: a family that needs an aim result which is not yet ready issues
	// its Aim* (once — the issue bit latches) and the slot waits for the
	// asynchronous completion this visit. The latch is set immediately after
	// dispatch. The ballistic no-solution sentinel suppresses Aim dispatch P0-10.
	needLatch, needResult := aimRequirement(slot.Weapon)
	if needResult && !slot.Aim.Ready {
		if spy != nil {
			spy.Record(StepAimDispatch)
		}
		if !slot.Aim.IssueBit {
			dispatched := true
			if env.DispatchAim != nil {
				dispatched = env.DispatchAim(idx, slot)
			}
			if dispatched {
				// OR 0x01 immediately after dispatch [06 §3.3] P0-10
				slot.Aim.StartAim()
				slot.Flags |= FlagAimLatch
			} else {
				// Ballistic sentinel 0x8000 suppressed Aim [06 §3.3] P0-10 - don't set latch, fall through to admission which will fail via ballistic gate.
				// Return false waiting? Actually without latch, turret will also fail needLatch gate below. But to avoid infinite wait, we fall through.
				// For turret ballistic with no solution, we should not wait for Ready that will never come; we go to admission which will reject.
				// So don't return here; continue to admission which will reject.
				goto admission
			}
		}
		return false // fire waits for the asynchronous nonzero return, no timeout P0-10
	}
	if needLatch && !slot.Aim.IssueBit {
		// A turret whose issue latch was cleared (e.g. by TargetCleared)
		// cannot fire even with a stale ready result [06 §3.3] P0-10.
		if spy != nil && needResult == false {
			// Still record AimDispatch? Already handled above for needResult case.
		}
		return false
	}

admission:
	// --- Step: range/medium/ballistic admission [06 §3.3] P0-10 ---
	// Shot-time physical admission tests squared planar range first; water vs non-water diverges; ballistic requires solution [06 §3.3].
	// Coverage vs engagement distinction is established: coverage drives overlay only, not ordinary fire radius [06 §3.3].
	// Shot-gate never tests radar/cloak/jammer per P0-10 NEGATIVE-BOUNDED.
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
			economy.ImmediateDebit(env.Player, eCost, mCost)
		}
	}
	// On successful allocation for turret families, Ready and latch are cleared per [06 §3.3] P0-10?
	// Turret success clears latch; vertical preserves? For now keep Ready as is; firing does not automatically clear latch in this model - caller may clear.
	return true
}

// TickUnitSlots runs the per-unit slot pipeline for all three slots in numeric order [06 §1.2] C1 (I1) P0-10.
// It is the unit-level wrapper that visits slots 0..NumSlots-1 ascending and calls TickSlot for each.
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
