// Test-only COB fixtures [04 §5.1] C26 [GAP T15] C17.
//
// None of this is shipped. The live damage funnel is internal/combat's
// Service.AcceptDamage, which owns the packet kinds, the health write and the
// HitByWeapon/TakeDamage pair on a real unit record; the shape below is this
// package's own arithmetic-only victim, written so a test can pin the C26
// ORDER without internal/cob importing internal/units (the import runs the
// other way). The same is true of the window order: the authoritative in-tick
// phase order lives in internal/session's phase graph, and the enumeration
// here is the list a test holds it against.

package cob

// ---------------------------------------------------------------------------
// C17 — same-tick windows scaffolding [GAP T15] (I7)
// ---------------------------------------------------------------------------

// WindowPhase enumerates the same-tick window order [GAP T15] C17 (I7):
// unit update (queues SetDirection/SetSpeed) → weapon update (queues
// TargetCleared, Aim*, Fire*, RockUnit) → normal COB drain (delta 1, eight
// thread slots then one piece pass) → orders/build work → movement integration
// (D+wake MoveRate, setSFXoccupy) → slot-end death handling. Deferred
// callbacks produced before the normal pass run in the same visit; those after
// normally wait, except a D+wake start does an all-slot delta-0 drain that can
// execute them earlier [GAP T15] C17.
type WindowPhase int

const (
	PhaseUnitUpdate     WindowPhase = 1 // queues SetDirection/SetSpeed [GAP T15] C17
	PhaseWeaponUpdate   WindowPhase = 2 // queues TargetCleared/Aim*/Fire*/RockUnit [GAP T15] C17
	PhaseNormalDrain    WindowPhase = 3 // normal COB drain delta 1, 8 slots then one piece pass [04 §4.6] C17
	PhaseOrdersBuild    WindowPhase = 4 // orders and build work [GAP T15] C17
	PhaseMovementIntegr WindowPhase = 5 // movement integration D+wake MoveRate/setSFXoccupy [GAP T15] C17
	PhaseSlotEndDeath   WindowPhase = 6 // slot-end death handling synchronous Killed query [GAP T15] C17
)

// TickWindowOrder returns the in-tick order as phase numbers [GAP T15] C17 (I7).
// Deterministic iteration: caller walks the returned slice in order (I1).
func TickWindowOrder() []WindowPhase {
	return []WindowPhase{
		PhaseUnitUpdate,
		PhaseWeaponUpdate,
		PhaseNormalDrain,
		PhaseOrdersBuild,
		PhaseMovementIntegr,
		PhaseSlotEndDeath,
	}
}

// ---------------------------------------------------------------------------
// C26 — normal-kind damage packet ordering [04 §5.1]
// ---------------------------------------------------------------------------

// DamageKind enumerates the packet kind byte. Only Normal participates in the
// HitByWeapon/TakeDamage pair; Heal and Paralyze skip it, and lethal against
// movement-category-1/2 sets death latch with no callbacks [04 §5.1] C26.
type DamageKind uint8

const (
	DamageKindNormal   DamageKind = 1  // ordinary damage, full callback pair path [04 §5.1] C26
	DamageKindParalyze DamageKind = 2  // paralyzer: flash etc but no HitByWeapon/TakeDamage, instead stun task [04 §5.1] C26
	DamageKindHeal     DamageKind = 10 // healing: early path adds unsigned amount clamped vs max, no callbacks [06 §9.1] [04 §5.1] C26
	DamageKindOther    DamageKind = 11 // kind 11 subtracts health but skips reaction/callbacks [06 §9.1]
)

// VictimState is the minimal per-unit health/liveness surface needed for the
// C26 damage funnel without importing internal/units (WU-06-7 may not add
// fields to units.Unit; the orchestrator decides the final placement, so this
// package defines its own test-only victim shape, and the units-owner mirrors
// these names) [04 §5.1] C26 (I13: the definition's armor word is not modeled
// here).
type VictimState struct {
	Health      int32 // current health, signed
	MaxHealth   int32 // definition maximum damage, >0
	Active      bool  // alive bit [04 §5.1] C26
	Dying       bool  // death latch already set (not yet final-cleaned) [04 §2.4]
	MovementCat int   // movement category for lethal latch short-circuit; 1/2 are the mobile classes [04 §5.1] C26
}

// DamageResult captures the synchronous health mutation plus the computed
// arguments for the two independent async starters [04 §5.1] C26. Either starter
// can fail separately due to pool exhaustion [04 §4.3] C14.
type DamageResult struct {
	HealthAfter int32    // after subtraction (clamped zero for stationary non-mobile path) [06 §9.1]
	HitArgs     [2]int32 // cos*400, sin*400 from dir byte shifted left 8 [04 §5.1] C26 C25
	TakeArg     int32    // post-hit percentage clamp(health*100/maxHealth,0,100) [04 §5.1] C26
	ShouldHit   bool     // whether HitByWeapon should be started (normal kind, active victim, not death-latch short-circuit)
	ShouldTake  bool     // whether TakeDamage should be started (same gates)
	DeathLatch  bool     // lethal against movement-cat 1/2 => latch set, no callbacks [04 §5.1] C26
	Dead        bool     // health dropped to <=0 (before clamp) and latch case examined
}

// ApplyNormalDamage implements the C26 ordering on a normal-kind packet against
// an active, not-yet-dying victim [04 §5.1] C26: health is subtracted FIRST,
// then HitByWeapon args are cos/sin*400 of dir byte shifted left 8, then
// TakeDamage arg is the post-hit percentage. Either starter can fail separately.
// Heal and paralyze kinds skip the pair entirely; lethal damage against a
// movement-category-1/2 victim sets the death latch and returns with NO
// callbacks. This helper performs the synchronous health mutation and computes
// the would-be arguments; the caller does the two VM.Start calls independently
// so each can fail [04 §4.3] C14.
//
// The 16-bit modular subtraction is modeled as plain int32 subtraction here;
// the wrap is noted but does not affect the clamp to zero for the stationary
// path in the bounded tests.
//
// Death latch: on a non-positive signed health result, the two mobile
// controller classes (movement categories 1 and 2) set the latch, preserve the
// modular health, and return immediately with no callbacks [06 §9.1] [04 §5.1]
// C26. Stationary units clamp health to zero and continue to callbacks unless
// also latched.
func ApplyNormalDamage(v *VictimState, kind DamageKind, amount int32, dirByte uint8) DamageResult {
	var res DamageResult
	if v == nil || !v.Active || v.Dying {
		return res // packet rejected: missing, non-live, or already death-marked [04 §5.1] C26
	}
	if kind == DamageKindHeal || kind == DamageKindParalyze {
		return res // skip pair entirely [04 §5.1] C26; heal's add path vs paralyze stun task are outside this helper
	}
	if kind == DamageKindOther {
		return res // kind 11 skips reaction and callbacks [06 §9.1]
	}
	// Only normal-kind reaches here in this helper; other normal-adjacent kinds
	// that also mutate health would be routed via separate funcs.

	// --- health subtracted FIRST [04 §5.1] C26 ---
	prevHealth := v.Health
	_ = prevHealth
	// Exact 16-bit modular subtraction [06 §9.1] [04 §5.1] — low 16 bits wrap modulo 65536.
	// Implemented as sign-extending 16-bit amount then subtract; we preserve the
	// raw wrap for latch test then clamp.
	lowAmount := int32(int16(amount))        // packet amount is signed 16-bit, wraps modulo [06 §9.1]
	newHealthModular := v.Health - lowAmount // modular at 16-bit then sign-extended? Keep int32 for latch compare.
	// Retail's stationary clamp after modular test: if non-positive and not
	// mobile 1/2, clamp to zero instead of preserving negative modular value
	// [06 §9.1]. We apply latch short-circuit first.

	// Lethal test: non-positive signed result [06 §9.1] [04 §5.1] C26.
	if newHealthModular <= 0 {
		res.Dead = true
		if v.MovementCat == 1 || v.MovementCat == 2 {
			// Lethal against movement-category-1/2 victim sets death latch and
			// returns with NO callbacks [04 §5.1] C26.
			//
			// This latch is NOT the unit death path, and must not be taken for
			// it. `units.World.DestroyBy` / `units.MarkDeath` write the three
			// death fields — the latch, the cause, and the recorded-attacker
			// link of [04 R-UNIT-06 §5] — and none of them is reachable here:
			// `VictimState` is this package's own arithmetic-only victim shape
			// with no handle and no world behind it, and `internal/units`
			// imports `internal/cob` (units/cob_binding.go), so the reverse
			// edge that would let this call the death entry does not exist and
			// cannot be added.
			//
			// There is also nothing to pass. [04 §5.1] C26 states this arm as
			// "sets the death latch and returns with NO callbacks" and gives
			// neither an attacker nor a death cause, and `ApplyNormalDamage`
			// takes no shooter argument — so there is not even a null to
			// thread. The production funnel that owns both is `internal/combat`,
			// whose kill site passes the packet's shooter to `DestroyBy`.
			v.Dying = true
			v.Health = newHealthModular // preserve modular health [06 §9.1]
			res.HealthAfter = v.Health
			res.DeathLatch = true
			// No callbacks.
			return res
		}
		// Stationary lethal would clamp health to zero and can continue to
		// normal callbacks unless cause-specific latch existed; we continue past
		// short-circuit to queue HitByWeapon/TakeDamage [06 §9.1] [04 §5.1] C26
		// but still mark dead for caller awareness.
		// For C26 test purposes, stationary lethal still emits callbacks.
		v.Health = 0 // clamp to zero [06 §9.1]
		res.HealthAfter = 0
	} else {
		v.Health = newHealthModular
		res.HealthAfter = v.Health
	}

	// On a non-latched normal victim, queue HitByWeapon then TakeDamage
	// independently with computed args [04 §5.1] C26. Either starter can fail
	// separately [04 §4.3] C14 (caller checks vm.Start result per callback).
	x, y := HitByWeaponArgs(dirByte) // [04 §5.1] C26 angle via 512-entry table [04 §5.1] C25
	res.HitArgs = [2]int32{x, y}
	res.TakeArg = TakeDamagePercent(v.Health, v.MaxHealth) // [04 §5.1] C26 unsigned clamp
	res.ShouldHit = true
	res.ShouldTake = true
	return res
}
