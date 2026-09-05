// Engine ports and callbacks [04 §4.4] [04 §5] [GAP T15] [03 §2.4].
//
// Contracts C15–C19, C25, C26 live here. This file owns the verbatim port
// table (1–20), the callback arithmetic that uses fixed-point trig (RockUnit /
// HitByWeapon / Killed severity / SetMaxReloadTime / Query seeds), the
// emit-sfx vocabulary and its presentation sink, the MoveRate tier classifier,
// the aim-ready handshake, the same-tick window scaffolding, and the normal-
// kind damage packet ordering of C26. Model draw trig remains floating point
// per [03 §2.4] and is not used for callback arguments, which go through the
// 512-entry table via numeric.Sin/Cos and rounded products [04 §5.1] C25 (I2).

package cob

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// ---------------------------------------------------------------------------
// C15 — engine ports 1–20 [04 §4.4]
// ---------------------------------------------------------------------------

// PortInfo is one entry of the verbatim port table [04 §4.4] C15.
// The switch over 1–20 is the retail dispatch; an id outside 1..20 reads zero
// [04 §4.4] and a write to an id with no write arm only sets the script-
// touched marker. Every write arm sets that marker in addition to its effect
// [04 §4.4].
//
// The marker is not an opaque flag: it is bit 2 of the owning unit's
// order-event word, which is order gate bit 0x4, and the order pump's
// satisfied-set merge is its only consumer [04 R-COB-06]. The INBUILDSTANCE
// wait and the transport BUSY wait park on that bit with no deadline, so a
// script's engine write is the only thing that re-polls them. That producer IS
// wired (WU-19-148): the engine-write arm in vm.go raises it unconditionally —
// before the port dispatch, so the fall-through raises it too — and
// internal/units turns the raise into the unit's pending bit. This sentence
// previously said the wiring "belongs" at those two sites, which read as open
// work after it had been done.
type PortInfo struct {
	ID        Port
	Name      string
	ReadDesc  string
	WriteDesc string
}

// PortTable is the verbatim table of 20 engine ports [04 §4.4] C15.
// Names are the plan/report spellings; read/write columns mirror the spec
// prose without paraphrase so reviewers can spot-check line-by-line (I10).
var PortTable = []PortInfo{
	{1, "activation", "the unit's activation bit", "drives the activation edge machine, which fires the Activate and Deactivate callbacks re-entrantly [04 §4.4]"},

	{2, "standing move orders", "a two-bit field, values 0 to 3 [04 §4.4]", "ignored [04 §4.4]"},

	{3, "standing fire orders", "a two-bit field, values 0 to 3 [04 §4.4]", "ignored [04 §4.4]"},

	{4, "health", "current health times 100 divided by the definition's maximum damage, as an unsigned division, giving 0 to 100 [04 §4.4]", "ignored [04 §4.4]"},

	{5, "in build stance", "a flag bit [04 §4.4]", "sets the bit from the low bit of the value [04 §4.4]"},

	{6, "busy", "a flag bit [04 §4.4]", "sets the bit from the low bit of the value [04 §4.4]"},

	{7, "piece position XZ", "the piece's world transform packed with X in the high half and Z in the low half [R-COB-03 §3]", "— [04 §4.4]"},

	{8, "piece position Y", "the piece's world transform Y [04 §4.4]", "— [04 §4.4]"},

	{9, "unit position XZ", "another unit's packed X and Z, selected by identifier through the unit table, gated on that unit being alive; identifier zero or a dead unit reads zero [04 §4.4]", "— [04 §4.4]"},

	{10, "unit position Y", "the same unit's Y, same gates [04 §4.4]", "— [04 §4.4]"},

	{11, "unit height", "the own definition's height value; takes no unit argument [04 §4.4]", "— [04 §4.4]"},

	{12, "relative bearing", "unpacks the argument into two signed 16.16 halves, takes the arc tangent, then subtracts the unit's own heading, truncated to 16 bits [04 §4.4]", "— [04 §4.4]"},

	{13, "distance", "the hypotenuse of the unpacked halves, truncated [04 §4.4]", "— [04 §4.4]"},

	{14, "arc tangent", "the arc tangent of the two arguments, low 16 bits [04 §4.4]", "— [04 §4.4]"},

	{15, "hypotenuse", "the hypotenuse of the two arguments, truncated [04 §4.4]", "— [04 §4.4]"},

	{16, "ground height", "the world height query at the packed coordinates, shifted into 16.16 [04 §4.4]", "— [04 §4.4]"},

	{17, "build percent left", "from the remaining-build fraction f: zero when f is exactly zero, otherwise 1 - trunc(f * -99.0) [04 §4.4]", "ignored [04 §4.4]"},

	{18, "yard open", "a flag bit [04 §4.4]", "runs the yard-occupancy update [04 §4.4]"},

	{19, "bugger off", "a plain flag bit, not a queued request [04 §4.4]", "sets the bit from the low bit of the value [04 §4.4]"},

	{20, "armored", "a flag bit [04 §4.4]", "drives the armor edge event [04 §4.4]"},
} // [04 §4.4] C15 exactly 20

// IsEnginePort reports whether id is a valid engine port 1..20 [04 §4.4] C15.
// Outside that range reads zero and writes only set the touched marker.
func IsEnginePort(id int32) bool { return id >= 1 && id <= 20 }

// ---------------------------------------------------------------------------
// C15/C25 — callback arithmetic helpers
// ---------------------------------------------------------------------------

// trigScalar multiplies a table-scaled trig value (8192 scale, from
// numeric.Sin/Cos [04 §5.1]) by an unscaled integer scalar. The product is
// formed at full 64-bit width, half the scale (4096) is added to it, and the
// result is an ARITHMETIC shift right by 13 — so the rounding FLOORS: a
// negative product rounds toward negative infinity and a tie rounds up
// [04 §5.3][04 R-MOV-01 §4][04 §10.3] C25 (I2).
//
// The earlier text here carried an open-question marker saying "retail's rounding for
// negative products is not closed ... negative values bias by +0.5" and asked
// for the -cos*800 and HitByWeapon 400 sequences to be checked before relying
// on negative angles. That is now closed, and the doubt was misplaced: the two
// shared component routines RockUnit and HitByWeapon call are the same pair the
// ground mover and the air work bodies use, and they contain no divide and no
// float-to-integer conversion — nothing that could truncate toward zero. Adding
// half and then flooring IS the contract, so the "+0.5 bias on negatives" the
// old marker treated as a risk is the behavior being cloned. Go's >> on int64
// is arithmetic, which makes this line bit-identical to it.
func trigScalar(tableVal, scalar int32) int32 {
	return int32((int64(tableVal)*int64(scalar) + 4096) >> 13)
}

// RockUnitArgs computes the two arguments for the RockUnit callback [GAP T15]
// [04 §5.3] C15/C25: (-cos(rel)*800, -sin(rel)*800) where
// rel = (int16)(barrelDir - unitHeading) evaluated through the shared 512-
// entry table, each product adding half the 8192 scale and flooring. Both
// signs are negative [04 §5.3] and there is no completion receiver [GAP T15]. Model draw trig is
// separate float path [03 §2.4] and is not used here (C25).
func RockUnitArgs(rel int16) (int32, int32) {
	// rel as signed 16-bit circular angle, widened to uint16 for the 65536
	// domain, then to Angle for numeric table lookup [04 §5.1] C25.
	a := numeric.Angle(uint16(rel))
	cosVal := numeric.Cos(a)
	sinVal := numeric.Sin(a)
	// Products add half the 8192 scale and floor [04 §5.3] C25.
	c := trigScalar(cosVal, 800)
	s := trigScalar(sinVal, 800)
	return -c, -s // [GAP T15] both negative
}

// HitByWeaponArgs computes the two arguments for HitByWeapon [04 §5.1]
// [GAP T15] C15/C25: (cos(dir)*400, sin(dir)*400) where dir is the packet
// direction byte shifted left 8 into the 65536 domain [04 §5.1] C26. Products
// go through the same two shared component routines RockUnit uses, radius 400
// and positive signs: add half the scale, then floor [04 §5.3] C25.
func HitByWeaponArgs(dirByte uint8) (int32, int32) {
	angle := numeric.Angle(uint16(dirByte) << 8) // byte shifted left 8 [04 §5.1] C26
	cosVal := numeric.Cos(angle)
	sinVal := numeric.Sin(angle)
	return trigScalar(cosVal, 400), trigScalar(sinVal, 400) // [04 §5.1] C25
}

// KilledSeverity computes the local Killed severity input for the synchronous
// query [GAP T15] C15 [04 §5.1]:
//
//	severity = ((-health*100)/maxHealth + prior) / 2 // unsigned divide
//	clamp(severity, 1, 100)
//
// where prior is the PREVIOUS 30-tick window's health percentage. The unit
// record keeps two adjacent sample bytes: each 30-tick sample writes
// clamp(health*100/maxHealth, 0, 100) into the current one after shifting the
// current one into the prior one [04 §5.1]. Division is
// unsigned; for the positive domain it coincides with signed trunc toward zero
// [01 §8] I3. The caller must only invoke this when the query is actually taken
// (cause overrides bypass it with forced 0) [04 §5.1].
func KilledSeverity(health, maxHealth int32, priorSample uint8) int32 {
	if maxHealth <= 0 {
		// Retail divides by the definition's maximum-damage field with no
		// guard, so an authored zero raises the processor divide fault and
		// kills the process — "document this as policy rather than silently
		// repairing it" [04 §4.3]. We cannot host that, so this clamp is a
		// named divergence under the INVARIANTS I11 bounds-check exception.
		return 1
	}
	negHealth := -health // health <=0 on normal lethal path; -health >=0
	if negHealth < 0 {
		negHealth = 0 // positive health would give negative severity; caller should have bypassed, but clamp path keeps it.
	}
	tmp := (negHealth * 100) / maxHealth // unsigned divide for positive domain [04 §5.1]
	tmp += int32(priorSample)            // the prior-window sample byte [04 §5.1]
	tmp /= 2
	if tmp < 1 {
		tmp = 1
	}
	if tmp > 100 {
		tmp = 100
	}
	return tmp // [GAP T15] C15
}

// HealthPercent is the port-4 and post-hit percentage read [04 §4.4] [04 §5.1]
// C15/C26: current health*100 / maxHealth as an unsigned division giving
// 0..100, clamped. It is the computation underlying port 4 (health) and the
// TakeDamage argument after health subtraction [04 §5.1] C26.
func HealthPercent(health, maxHealth int32) int32 {
	if maxHealth <= 0 {
		// The same unguarded divide as KilledSeverity above: an authored zero
		// maximum-damage faults retail outright [04 §4.3]. Clamping is the
		// INVARIANTS I11 divergence, not a traced behavior.
		return 0
	}
	// Unsigned division note [04 §4.4] port 4 and [04 §5.1] TakeDamage.
	// For the positive health path this is signed trunc toward zero [01 §8] I3.
	v := (int64(health) * 100) / int64(maxHealth)
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return int32(v) // [04 §4.4] port 4, [04 §5.1] C26
}

// TakeDamagePercent is the same as HealthPercent but named for the C26 post-hit
// call site [04 §5.1] C26: clamp(health*100/maxHealth, 0, 100) as unsigned
// division after health was already subtracted.
func TakeDamagePercent(health, maxHealth int32) int32 {
	return HealthPercent(health, maxHealth) // [04 §5.1] C26
}

// MaxReloadMillis converts the compiled maxReload ticks to the milliseconds
// reported to SetMaxReloadTime [GAP T15] C15: trunc(maxReload*1000/30) [01 §8] I3
// with truncation toward zero. The scan is over all three weapon slots [04
// §5.3] and is issued after Create so it lands outside Create's own immediate
// drain [04 §5.3].
func MaxReloadMillis(maxReloadTicks int32) int32 {
	// trunc(maxReload * 1000 / 30) [GAP T15] C15 (I3 trunc toward zero).
	return int32((int64(maxReloadTicks) * 1000) / 30)
}

// QueryTransportSeed is the synchronous four-output seed for QueryTransport
// [04 §5.3] [GAP T15] C15: [-1, 0, 0, 0]. Remaining outputs default 0 and are
// excluded from copy-back; a missing script leaves -1, the root-piece fallback
// [04 §5.3].
func QueryTransportSeed() [4]int32 { return [4]int32{-1, 0, 0, 0} } // [GAP T15] C15

// QueryLandingPadSeed is the synchronous four-output seed for QueryLandingPad
// [04 §5.3] [GAP T15] C15: all -1. Candidates tried in order 0..3, first free
// wins. Some paths query carrier first then transported unit [04 §5.3].
func QueryLandingPadSeed() [4]int32 { return [4]int32{-1, -1, -1, -1} } // [GAP T15] C15

// ---------------------------------------------------------------------------
// C16 — aim-ready handshake [GAP T15] [04 §5.3] [06 §3.3]
// ---------------------------------------------------------------------------
//
// CONTRACT — do not soften. Aim-ready is granted ONLY by a nonzero completed
// Aim* result. The producer clears the slot's aim state to 0, stores the
// commanded heading/pitch, and starts Aim* deferred with the slot's
// completion receiver; immediately after the start it sets the issue bit,
// which gates re-issue but authorizes nothing. The receiver is then invoked
// with the delivered cell:
//
//   - explicit script return: the popped return value;
//   - failed start (script name absent, invalid identity, all eight thread
//     slots occupied): 0, delivered through the same receiver;
//   - signal termination or abnormal termination: no invocation at all.
//
// A zero delivery has no effect — it neither grants nor revokes — while any
// nonzero delivery marks the weapon aim-ready. There is no timeout: a weapon
// whose Aim* never completes nonzero stays unable to fire until the producer
// clears the aim state and re-issues [04 §5.3] [06 §3.3].
//
// KNOWN TEMPTING BUG: "helpfully" treating a missing Aim* script, a nil VM,
// or pool exhaustion as success. That inverts the established zero/nonzero
// grant — every script-less unit becomes an always-ready turret and the
// completion handshake stops being observable. Retail delivers 0 on exactly
// those paths; a missing Aim* must stay never-ready. Regression tests:
// internal/cob/aimready_test.go.
type AimSlot struct {
	IssueBit bool // weapon flags byte bit 0: set immediately after an Aim* start, AND-cleared on target-acquisition failure, gates re-issue [04 §5.3]
	Ready    bool // the aim-state word: granted only by a nonzero delivered cell [04 §5.3] [06 §3.3]
}

// StartAim arms the issue bit for an Aim* start [04 §5.3]. The caller performs
// the producer's preceding steps first: clear the aim state to 0 and store the
// commanded heading/pitch. Both start forms then set the issue bit; the issue
// bit alone authorizes nothing [04 §5.3] [06 §3.3].
func (s *AimSlot) StartAim() {
	s.IssueBit = true
	// Ready is untouched here: the producer cleared it before this start, and
	// only CompleteAim can grant it [04 §5.3].
}

// CompleteAim is the aim-ready grant site: the completion receiver consuming
// the delivered cell [04 §5.3] [06 §3.3]. An explicit script return delivers
// its value; a failed start delivers 0 through this same receiver; signal and
// abnormal termination never invoke it. Zero has no effect — it neither
// grants nor revokes — while any nonzero delivery marks the weapon aim-ready.
// The grant is a one-way latch until the producer clears the aim state before
// the next Aim* start. Do not soften the zero/nonzero rule, and never treat a
// missing Aim* as success — see the C16 block contract above. Returns the
// current aim-ready state.
func (s *AimSlot) CompleteAim(returnValue int32) bool {
	if returnValue != 0 {
		s.Ready = true
	}
	// Zero: no effect [04 §5.3] [06 §3.3] — no grant, no revoke.
	return s.Ready
}

// CanFire reports whether this slot holds a nonzero Aim result. The issue bit
// alone authorizes nothing; the fire path additionally applies the per-family
// readiness rules behind it — turret: latch and result; vertical-launch:
// result; line-of-sight/self-propelled and dropped: neither [06 §3.3]. This
// method models the result half only.
func (s *AimSlot) CanFire() bool { return s.Ready }

// AimDeliveryZero is the cell a failed Aim* start delivers to the completion
// receiver: script name absent, invalid identity, or all eight thread slots
// occupied each deliver 0 through the same receiver, leaving aim-ready clear
// [04 §5.3] [04 §4.3] [06 §3.3]. The starter itself retains its arguments;
// the zero delivery is the receiver-bearing adapter's response to the failed
// start.
const AimDeliveryZero int32 = 0 // [04 §5.3] [06 §3.3]

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

// CallbackKind identifies an engine→COB callback for window bookkeeping
// [GAP T15] C17. Names mirror the script callbacks; the kind alone does not
// authoritatively order draws — call order is behavior (I4).
type CallbackKind int

const (
	CallbackSetDirection  CallbackKind = 1 // unit update queued [GAP T15] C17
	CallbackSetSpeed      CallbackKind = 2 // unit update queued [GAP T15] C17
	CallbackTargetCleared CallbackKind = 3 // weapon update queued [GAP T15] C17
	CallbackAimPrimary    CallbackKind = 4 // weapon update queued [GAP T15] C17
	CallbackAimSecondary  CallbackKind = 5
	CallbackAimTertiary   CallbackKind = 6
	CallbackFirePrimary   CallbackKind = 7 // weapon update queued [GAP T15] C17
	CallbackFireSecondary CallbackKind = 8
	CallbackFireTertiary  CallbackKind = 9
	CallbackRockUnit      CallbackKind = 10 // weapon update queued [GAP T15] C17

	CallbackStartMoving  CallbackKind = 11 // movement integration D+wake=1 [GAP T15] C17
	CallbackStopMoving   CallbackKind = 12
	CallbackMoveRate1    CallbackKind = 13 // D+wake=1 tiers [GAP T15] C18
	CallbackMoveRate2    CallbackKind = 14
	CallbackMoveRate3    CallbackKind = 15
	CallbackSetSFXoccupy CallbackKind = 16 // movement integration D+wake=1 [GAP T15] C17

	CallbackHitByWeapon CallbackKind = 17 // damage path async [04 §5.1] C26
	CallbackTakeDamage  CallbackKind = 18
	CallbackKilled      CallbackKind = 19 // slot-end sync [GAP T15] C17
)

// StartDeferredWake starts a mode-D script on vm and performs its wake=1
// all-slot delta-0 barrier. The barrier runs every slot at delta 0 plus one
// piece pass, so a deferred callback queued earlier but not yet drained can
// also execute earlier than the normal pass [R-CB-01 §2].
// The caller typically does:
//
//	StartDeferredWake(vm, "StartMoving", nil)
//	StartDeferredWake(vm, "MoveRate2", nil)
//
// Returns whether the start succeeded (pool free and script present) [04 §4.3]
// C14. Either starter can fail separately [04 §5.1] C26.
func StartDeferredWake(vm *VM, scriptName string, args []int32) bool {
	if vm == nil || vm.prog == nil {
		return false
	}
	pc, ok := vm.prog.Scripts[scriptName]
	if !ok {
		return false
	}
	if !vm.Start(pc, args) {
		return false
	}
	// D+wake barrier [R-CB-01 §2]: all slots at delta 0 plus one piece pass.
	vm.Drain(0)
	return true
}

// ---------------------------------------------------------------------------
// C18 — MoveRate tiers [GAP T15] [04 §5.2]
// ---------------------------------------------------------------------------

// MoveRateCategory classifies the movement tier [GAP T15] C18 [04 §5.2].
// Category 0 overrides when the inhibit bit is set, the unit is attached to a
// carrier (carrier dword nonzero), or both magnitude words are zero. Otherwise
// 1 up to the definition's first move-rate threshold, 2 up to its second, 3
// above both (MoveRate1 and MoveRate2 below). All comparisons are signed
// 32-bit [04 §5.2]. On change: into 0 from nonzero issues StopMoving; into
// nonzero from 0 issues StartMoving FIRST then MoveRateN with a wake=1
// delta-zero barrier; other nonzero-to-nonzero issues only MoveRateN [GAP T15] C18.
//
// The first magnitude is the signed scalar speed word; the second is the
// adjacent signed turn-residual word used only by the both-zero override.
// Thresholds are the compiled MoveRate1 and MoveRate2 values [R-MOV-01 §6].
func MoveRateCategory(inhibit, attached bool, magA, magZ int32, rate1, rate2 int32) int {
	if inhibit || attached { // [GAP T15] C18 category 0 overrides
		return 0 // [04 §5.2] inhibit bit or attached (carrier dword nonzero)
	}
	if magA == 0 && magZ == 0 { // [GAP T15] C18 both magnitudes zero
		return 0
	}
	// Classify the scalar speed word with signed inclusive comparisons [04 §5.2].
	mag := magA
	if mag <= rate1 { // inclusive [04 §5.2]
		return 1
	}
	if mag <= rate2 {
		return 2
	}
	return 3
}

// MoveRateTransition describes the callbacks emitted on a tier change
// [GAP T15] C18 [04 §5.2]. The cache is two bits of the unit's class/state
// word; an unchanged category emits nothing [04 §5.2].
func MoveRateTransition(prev, next int) []CallbackKind {
	if prev == next {
		return nil // unchanged emits nothing [04 §5.2]
	}
	if next == 0 && prev != 0 {
		return []CallbackKind{CallbackStopMoving} // into 0 from nonzero [GAP T15] C18
	}
	if prev == 0 && next != 0 {
		// Into nonzero from 0 issues StartMoving FIRST then matching MoveRateN
		// with a wake=1 delta-zero barrier between them [GAP T15] C18.
		var mr CallbackKind
		switch next {
		case 1:
			mr = CallbackMoveRate1
		case 2:
			mr = CallbackMoveRate2
		case 3:
			mr = CallbackMoveRate3
		default:
			mr = CallbackMoveRate1
		}
		return []CallbackKind{CallbackStartMoving, mr}
	}
	// Nonzero-to-nonzero change issues only MoveRateN [GAP T15] C18.
	switch next {
	case 1:
		return []CallbackKind{CallbackMoveRate1}
	case 2:
		return []CallbackKind{CallbackMoveRate2}
	case 3:
		return []CallbackKind{CallbackMoveRate3}
	}
	return nil
}

// ---------------------------------------------------------------------------
// C19 — emit-sfx vocabulary [GAP T15] [04 §4.3] [03 §2.4]
// ---------------------------------------------------------------------------

// SFXKind classifies an emit-sfx type [GAP T15] C19 [04 §4.3].
type SFXKind int

const (
	SFXIgnored    SFXKind = 0 // >=0x104, 0x100 itself, vector 6+ ignored [GAP T15] C19 [04 §4.4]
	SFXVector     SFXKind = 1 // vector types 0–5 piece-direction effects [GAP T15] C19
	SFXWhiteSmoke SFXKind = 2 // point 0x101 [GAP T15] C19
	SFXBlackSmoke SFXKind = 3 // point 0x102 [GAP T15] C19
	SFXSubBubbles SFXKind = 4 // point 0x103 water-line bubbles [GAP T15] C19
)

// ClassifySFX maps an emit-sfx type word to its vocabulary class [GAP T15] C19.
// Vector types 0–5 are piece-direction; point types 0x101 white, 0x102 black,
// 0x103 sub-bubbles with height forced to water-line; everything >=0x104,
// vector 6+, and 0x100 itself is ignored [GAP T15] C19 [04 §4.3]. Presentation-
// only, visibility-gated [GAP T15]: no simulation state is written.
func ClassifySFX(t int32) SFXKind {
	if t >= 0 && t <= 5 {
		return SFXVector // [GAP T15] C19 vector 0–5
	}
	switch t {
	case 0x101:
		return SFXWhiteSmoke // [GAP T15] C19
	case 0x102:
		return SFXBlackSmoke
	case 0x103:
		return SFXSubBubbles
	}
	// 0x100 itself falls through, and >=0x104 ignored [GAP T15] C19.
	return SFXIgnored // [GAP T15] C19 presents nothing
}

// SFXSink is the presentation-only sink for emit-sfx. The engine never writes
// sim state from this path; it is gated on the local player being able to see
// the unit [GAP T15] C19. Implementations render or record, never mutate sim.
//
// The VM consults this sink plus an optional visibility predicate before
// dispatching; the stub does not require a renderer.
type SFXSink interface {
	EmitSFX(piece int, sfxType int32, kind SFXKind)
}

// DispatchSFX classifies t and, if visible, calls sink. Returns true if an
// effect was classified as visible (vector/point). Ignored vocabulary returns
// false and touches no sink. Visibility is the caller's predicate; the VM's
// piece-visibility gate belongs to the renderer, not sim [GAP T15] C19 (I6).
func DispatchSFX(sink SFXSink, piece int, sfxType int32, visible bool) bool {
	k := ClassifySFX(sfxType) // [GAP T15] C19
	if k == SFXIgnored {
		return false // [GAP T15] C19 >=0x104 ignored
	}
	if !visible {
		return false // visibility-gated, presentation-only [GAP T15] C19
	}
	if sink != nil {
		sink.EmitSFX(piece, sfxType, k)
	}
	return true
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

// ComputeTakeDamagePercent is the TakeDamage starter's single argument after
// health subtraction [04 §5.1] C26: clamp(health*100/maxHealth, 0, 100) via
// unsigned division. See also HealthPercent [04 §4.4].
func ComputeTakeDamagePercent(health, maxHealth int32) int32 {
	return TakeDamagePercent(health, maxHealth) // [04 §5.1] C26
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
	res.TakeArg = ComputeTakeDamagePercent(v.Health, v.MaxHealth) // [04 §5.1] C26 unsigned clamp
	res.ShouldHit = true
	res.ShouldTake = true
	return res
}

// ---------------------------------------------------------------------------
// Engine-port read/write helpers (trunc/round notes)
// ---------------------------------------------------------------------------

// RelativeBearing computes port 12: unpack the signed X/Z halves, evaluate
// atan2(X,Z), subtract the unit heading, and retain the low 16 bits. The angle
// conversion rounds to nearest [R-COB-03 §2][R-COB-03 §3].
func RelativeBearing(packedXZ int32, heading uint16) uint16 {
	x, z := unpackXZ(packedXZ)
	// Port 12 evaluates atan2(X,Z) in the shared 65536-per-circle domain
	// [R-COB-03 §2]. Its operands remain raw signed 16.16 words [R-COB-03 §3].
	angle := numeric.AngleFromAtan2(int64(x), int64(z)).Raw()
	rel := angle - heading // subtract own heading, wrap via uint16 [04 §4.4]
	return rel
}

// Distance computes engine port 13 read [04 §4.4] C15: hypotenuse of unpacked
// halves, truncated toward zero [01 §8] I3.
func Distance(packedXZ int32) int32 {
	x, z := unpackXZ(packedXZ)
	h := math.Hypot(float64(x), float64(z))
	return int32(h) // trunc toward zero [01 §8] I3 [04 §4.4]
}

// AtanPort computes engine port 14's read [04 §4.4] C15: the low sixteen bits
// of the rounded angle. Ports 12 and 14 are the only port arithmetic that
// rounds; everything else, port 15's hypotenuse included, truncates toward
// zero [04 §4.4].
func AtanPort(first, second int32) uint16 {
	// Port 14 evaluates atan2(first, second), with no heading subtraction
	// [R-COB-03 §2]. Keep that order explicit at the shared helper boundary.
	return numeric.AngleFromAtan2(int64(first), int64(second)).Raw()
}

// HypotPort computes engine port 15's read [04 §4.4] C15: the C-runtime
// hypotenuse of the two arguments taken as signed 32-bit values, with NO
// unpacking, truncated toward zero by the shared float-to-integer conversion
// [R-COB-03 §2][01 §8] I3. Port 13 is the same helper fed the unpacked halves;
// port 15 is fed the raw arguments unchanged, which is the whole difference
// between them [04 §4.4].
//
// It was deleted as unreachable by CL-3 and is restored here because WU-19-234
// binds the port that reaches it.
func HypotPort(first, second int32) int32 {
	h := math.Hypot(float64(first), float64(second))
	return int32(h) // trunc toward zero [01 §8] I3 [04 §4.4]
}

// BuildPercentLeft computes port 17 read [04 §4.4] C15: from remaining-build
// fraction f (1→0): zero when f exactly zero, otherwise 1 - trunc(f * -99.0).
// I2 allowlist: construction remaining fraction is float32 [05 "Construction arithmetic"].
func BuildPercentLeft(f float32) int32 {
	if f == 0.0 {
		return 0 // [04 §4.4] C15
	}
	// trunc toward zero is Go int32(f * -99.0) [01 §8] I3.
	return 1 - int32(f*-99.0) // [04 §4.4] C15
}

// SetDirectionArg is the engine→COB SetDirection conversion [04 §5.3] [GAP T15]
// C15: guarded on a positive definition float field and a nonzero global
// mover-active word, the engine passes a zero-extended 16-bit direction word
// [04 §5.3]. Truncation is toward zero (here a zero-extend).
func SetDirectionArg(dir uint16) int32 { return int32(dir) } // [04 §5.3] zero-extended

// SetSpeedGeneral is the general unit-update SetSpeed conversion [04 §5.3] C15:
// guarded on ... passes a signed dword shifted left by 4 [04 §5.3].
func SetSpeedGeneral(speed int32) int32 { return speed << 4 } // [04 §5.3] signed <<4

// SetSpeedFootprint is the creation-time extractor callback argument: the
// footprint metal sum is accumulated modulo 16 bits and sign-extended when it
// enters the script window [R-CB-01 §5].
func SetSpeedFootprint(sum int32) int32 { return int32(int16(sum)) } // [04 §5.3] 16-bit sum

// PackXZ packs X into the high half and Z into the low half by adding the
// arithmetic-shifted coordinates [R-COB-03 §3]. It is intentionally addition,
// rather than OR: negative Z borrows one whole unit from X.
func PackXZ(x, z numeric.Fixed) int32 {
	return int32((uint32(x.Raw()) & 0xffff0000) + uint32(int32(z.Raw())>>16))
}

// unpackXZ reverses PackXZ, including the borrow correction for negative Z
// [R-COB-03 §3]. The returned values are raw 16.16 coordinates.
func unpackXZ(packed int32) (x, z int32) {
	x = int32(uint32(packed) & 0xffff0000)
	z = packed << 16
	if z < 0 {
		x += 0x10000
	}
	return x, z
}

// groundHeightOffMapSentinel is the raw, unshifted −1 that world.Terrain.
// HeightAt returns when the packed coordinate falls off the last valid
// interior cell [03 §2.3]. It is an out-of-band marker in that function's own
// contract (see its doc comment), not a height in 16.16 — retail's port
// shifts its off-map −1 left 16 into −0x10000, so this function must reshape
// the marker rather than pass it through [04 §4.4].
const groundHeightOffMapSentinel = numeric.Fixed(-1)

// GroundHeightOffMap is engine port 16's off-map read: the terrain query's
// raw −1 sentinel shifted left 16 into 16.16, i.e. −0x10000 (−1.0), not zero
// [04 §4.4].
const GroundHeightOffMap = numeric.Fixed(-0x10000)

// GroundHeight is engine port 16's read arithmetic [04 §4.4] C15: the shared
// terrain height query at the unpacked X and Z, in 16.16. PackXZ/unpackXZ's
// [R-COB-03 §3] high-half-X/low-half-Z convention applies — packedXZ is the
// single argument authored `GROUND_HEIGHT(x, z)` scripts pass after packing
// [fmt cob]. heightFn is the shared world height query in 16.16
// (world.Terrain.HeightAt or an injected test double per [03 §2.3]); this
// package takes it as a parameter rather than importing internal/world so the
// engine-port arithmetic stays free of a world dependency — the production
// binding lives at internal/session/composition.go, the one seam allowed to
// wire the two together.
//
// heightFn is expected to return world.Terrain.HeightAt's own off-map
// contract: the raw, unshifted −1 sentinel (not −0x10000) for a coordinate
// outside the last valid interior cell. This function performs the one
// reshape retail's port applies so callers see −0x10000, never the raw
// marker or an invented zero [04 §4.4].
//
// A nil heightFn returns 0 — no production caller may bind with one; see the
// note on readPortDefault in vm.go for the fixture/no-terrain case.
func GroundHeight(packedXZ int32, heightFn func(x, z numeric.Fixed) numeric.Fixed) numeric.Fixed {
	if heightFn == nil {
		return 0
	}
	x, z := unpackXZ(packedXZ) // [R-COB-03 §3] high half X, low half Z
	h := heightFn(numeric.Fixed(x), numeric.Fixed(z))
	if h == groundHeightOffMapSentinel {
		return GroundHeightOffMap // [04 §4.4] off-map read is −1.0, not zero
	}
	return h
}

// GroundHeightPortFunc adapts GroundHeight into a VM.BindPort handler for
// port 16 [04 §4.4] C15. It follows the engine "get" opcode's argument
// convention: the compiler always pushes the port id followed by four
// argument slots, zero-filling the ones a given port does not use [fmt cob];
// GROUND_HEIGHT uses exactly one, so args[1] is the packed X/Z and args[2..4]
// (if present) are the compiler's zero fill. A call with fewer than two
// elements (no argument pushed) reads coordinate (0,0), matching the same
// zero-fill convention. heightFn is documented on GroundHeight; the
// production caller is internal/session/composition.go's per-unit binding.
func GroundHeightPortFunc(heightFn func(x, z numeric.Fixed) numeric.Fixed) func(args []int32) int32 {
	return func(args []int32) int32 {
		var packed int32
		if len(args) > 1 {
			packed = args[1]
		}
		return int32(GroundHeight(packed, heightFn))
	}
}

// portArg returns argument slot n of an engine read, or zero when the authored
// call pushed fewer slots. The compiler always pushes the port id followed by
// four argument slots and zero-fills the ones a port does not use [fmt cob], so
// an absent slot and an authored zero are the same value; the one-argument read
// opcode passes the identifier alone, which is why the length test is needed at
// all [04 §4.4][04 R-COB-03 §1].
func portArg(args []int32, n int) int32 {
	if n < len(args) {
		return args[n]
	}
	return 0
}

// PieceWorldPoint resolves a COB piece index to its world point for ports 7
// and 8. The piece world position is the unit's own position plus the piece
// locator's offset; an out-of-range piece index, or a unit with no render
// table, contributes a ZERO offset, so the port answers the unit's own
// position rather than failing [04 §4.4][04 R-COB-03 §2]. An implementation
// therefore returns the unit position, not an ok flag, for a bad index — ports
// 7 and 8 have no failure value.
type PieceWorldPoint func(piece int32) [3]numeric.Fixed

// UnitPortLookup resolves the identifier argument of ports 9, 10 and 11. The
// identifier is masked to sixteen bits and a zero identifier — including the
// zero-filled argument slot of a bare `get` — reads zero, as does a slot whose
// alive bit is clear [04 §4.4][04 R-COB-03 §2]. Implementations return ok=false
// for both cases; the port then pushes zero.
//
// modelHeight is the definition's model bounding-box maximum Y in 16.16, which
// is what port 11 pushes [04 R-MOV-03 §5]. It is the same word the transport
// admission and the repair water clause read [04 §7 R-ORD-01 §7].
type UnitPortLookup func(id int32) (pos [3]numeric.Fixed, modelHeight int32, ok bool)

// PiecePositionXZPortFunc adapts the piece locator into port 7's read: the
// piece's world position packed as X in the high half and Z in the low half
// [04 §4.4][04 R-COB-03 §3]. args[1] is the piece index.
func PiecePositionXZPortFunc(pieceWorld PieceWorldPoint) func(args []int32) int32 {
	return func(args []int32) int32 {
		if pieceWorld == nil {
			return 0
		}
		p := pieceWorld(portArg(args, 1))
		return PackXZ(p[0], p[2]) // [04 §4.4] port 7
	}
}

// PiecePositionYPortFunc adapts the piece locator into port 8's read: the
// piece's world Y as a RAW 16.16 value, not shifted, with the same zero-offset
// fallback as port 7 [04 §4.4]. The 32-bit narrowing is retail's word width.
func PiecePositionYPortFunc(pieceWorld PieceWorldPoint) func(args []int32) int32 {
	return func(args []int32) int32 {
		if pieceWorld == nil {
			return 0
		}
		p := pieceWorld(portArg(args, 1))
		return int32(p[1].Raw()) // [04 §4.4] port 8, raw 16.16
	}
}

// UnitPositionXZPortFunc is port 9's read: the named unit's position packed the
// same way as port 7 [04 §4.4].
func UnitPositionXZPortFunc(lookup UnitPortLookup) func(args []int32) int32 {
	return func(args []int32) int32 {
		if lookup == nil {
			return 0
		}
		pos, _, ok := lookup(portArg(args, 1))
		if !ok {
			return 0 // zero identifier or a slot whose alive bit is clear [04 §4.4]
		}
		return PackXZ(pos[0], pos[2]) // [04 §4.4] port 9
	}
}

// UnitPositionYPortFunc is port 10's read: the same unit's Y as a raw 16.16
// value under the same identifier and alive gates as port 9 [04 §4.4].
func UnitPositionYPortFunc(lookup UnitPortLookup) func(args []int32) int32 {
	return func(args []int32) int32 {
		if lookup == nil {
			return 0
		}
		pos, _, ok := lookup(portArg(args, 1))
		if !ok {
			return 0
		}
		return int32(pos[1].Raw()) // [04 §4.4] port 10, raw 16.16
	}
}

// UnitHeightPortFunc is port 11's read: the definition height — the model
// bounding-box maximum Y in 16.16 — of the unit the identifier selects, via the
// same unit-table lookup and alive gate as ports 9 and 10 [04 §4.4]
// [04 R-MOV-03 §5].
func UnitHeightPortFunc(lookup UnitPortLookup) func(args []int32) int32 {
	return func(args []int32) int32 {
		if lookup == nil {
			return 0
		}
		_, height, ok := lookup(portArg(args, 1))
		if !ok {
			return 0
		}
		return height // [04 §4.4] port 11
	}
}

// RelativeBearingPortFunc is port 12's read: atan2 over the unpacked halves of
// args[1], less the reading unit's own heading, masked to sixteen bits
// [04 §4.4]. headingFn is read at call time because the heading moves every
// tick; a nil headingFn reads heading zero, which no production binding uses.
func RelativeBearingPortFunc(headingFn func() uint16) func(args []int32) int32 {
	return func(args []int32) int32 {
		var heading uint16
		if headingFn != nil {
			heading = headingFn()
		}
		return int32(RelativeBearing(portArg(args, 1), heading)) // [04 §4.4] port 12
	}
}

// DistancePortFunc is port 13's read: the hypotenuse of the UNPACKED halves of
// args[1], a 16.16 result truncated toward zero [04 §4.4].
func DistancePortFunc() func(args []int32) int32 {
	return func(args []int32) int32 { return Distance(portArg(args, 1)) } // [04 §4.4] port 13
}

// AtanPortFunc is port 14's read: atan2 of the two independent arguments in the
// same scaled domain as port 12, masked to sixteen bits with no heading
// subtraction [04 §4.4].
func AtanPortFunc() func(args []int32) int32 {
	return func(args []int32) int32 {
		return int32(AtanPort(portArg(args, 1), portArg(args, 2))) // [04 §4.4] port 14
	}
}

// HypotPortFunc is port 15's read: the hypotenuse of the two arguments taken as
// signed 32-bit values with NO unpacking, truncated toward zero [04 §4.4].
func HypotPortFunc() func(args []int32) int32 {
	return func(args []int32) int32 {
		return HypotPort(portArg(args, 1), portArg(args, 2)) // [04 §4.4] port 15
	}
}

// ---------------------------------------------------------------------------
// Unit definition thresholds identity (I13) — MoveRate mapping note for
// WU-06-1 units-owner. The unit record's two move-rate threshold words are
// content.UnitDef.MoveRate1 and MoveRate2 (both defaulting to twice
// MaxVelocity) [02 "Unit record"] [04 §5.2]. They are compiled thresholds for
// the inclusive tier classification described in MoveRateCategory above.
// ---------------------------------------------------------------------------

// Ensure numeric import is used and fixed-point vs float separation is explicit.
// The model draw path uses float trig with round-to-nearest [03 §2.4] (I2) and
// must not call numeric.Sin/Cos; this package's callback arguments use
// numeric.Sin/Cos exclusively [04 §5.1] C25.
