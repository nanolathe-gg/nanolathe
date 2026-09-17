// Weapon slots and targeting per [06].
//
// WU-09-1 owns slots.go (C1, C9) and target.go ([06 §3]).
// Other combat work units own pool.go, fire.go, aim.go, motion.go, etc.
// Do not import or mutate pool.Projectiles allocation state here; pool.go owns it.

package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// NumSlots is the fixed retail slot count [06 §1.2].
const NumSlots = 3 // [06 §1.2] primary, secondary, tertiary

// Slot flags are logical state; visits use numeric slot order [06 §1.2].
const FlagAimLatch uint8 = 0x01 // Aim-request latch [06 §3.3]

// Slot is one weapon slot per unit [06 §1.2] C1 (I13).
//
// Go stores named logical fields [I13].
// Determinism: slots visited numeric order 0..2 [06 §1.2] C1 (I1).
type Slot struct {
	// Weapon is the resolved weapon definition for this slot [06 §1.2] C1 (I13).
	Weapon *content.WeaponDef // [06 §1.2] P0-10

	// Reload is the countdown ticks until the slot can fire again [06 §1.2] [06 §4.1] C1.
	// Retail's field is a signed 16-bit tick counter [06 §1.2] P0-10.
	Reload int32 // s16 logical, int32 for Go

	// Flags is the slot flags byte: 0x02 armed, 0x01 Aim latch, 0x10 tracking [06 §1.2] P0-10.
	Flags uint8

	// DesiredYaw uses retail numbering: relative at turret Aim, absolute after
	// its muzzle query. Failed attempts retain the rewritten value plus spread
	// [06 R-WPN-05 §4][06 R-WPN-03 §4].
	DesiredYaw uint16

	// DesiredPitch is the desired pitch, TA angle units [06 §1.2] P0-10.
	DesiredPitch uint16

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

	// DistanceWord is initialized at unit construction and restored from saves
	// [08 R-SAVE-WEAPON-01]. The ballistic creator divides it by weapon velocity to form
	// `T0` [06 §6.4] (RWU-19-39); it is a per-unit constant, not a flight
	// distance to the current target.
	DistanceWord int32 // [06 §6.4] [06 R-WPN-05 §3]

	// PendingReload is the reload value computed after a successful spawner return
	// but before it is committed to Reload [06 §4.2] C6/C7 (I13).
	PendingReload int32 // [06 §4.2] (I13)
}

// DecrementReload decrements a nonzero signed-16 reload countdown before target resolve [06 §4.1] C1.
// Returns true if a decrement occurred.
// Order: this is the first step of the per-slot pipeline per [06 §4.1] (I10).
func (s *Slot) DecrementReload() bool {
	if s == nil || s.Reload == 0 {
		return false
	}
	s.Reload = int32(int16(s.Reload - 1)) // [06 §4.1] signed-16 decrement, including negative values
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

// aimRequirement reports the per-executor readiness rule [06 §3.3]: turret
// needs the Aim issue latch AND a nonzero result; vertical-launch needs only
// the result; line-of-sight/self-propelled and dropped need neither.
//
// The ladder below is complete, and there is nothing to compose it with. Which
// executor a weapon uses "is decided once, at catalog compile time, by the first
// matching flag in this order: `turret`; else `vlaunch`; else `lineofsight` or
// `selfprop`; else `dropped`; else none" [06 §3.3], and §3.3 then enumerates the
// readiness rule for each of those four executors and for none. `guidance`,
// `tracks` and `cruise` are not executor selectors at all — they are motion-
// phase flags, read by the family dispatch and guidance of [06 §6.2] and
// [06 §6.6], which §3.3 says explicitly "is not the same as" this ordering and
// must be reproduced independently. So a weapon's guidance flags cannot change
// what its aim gate demands, at any combination.
//
// Correction (WU-19-154): an open-question marker here said "guidance/tracks/cruise
// composability beyond turret/vlaunch gating remains open per [06 §3.3]
// missing/unknown". §3.3's missing list carries no such item, and the
// composability it asked about does not exist — the two ladders never meet.
func aimRequirement(w *content.WeaponDef) (needLatch, needResult bool) {
	if w == nil {
		return false, false
	}
	if w.Turret {
		return true, true // [06 §3.3] turret: latch + nonzero result, then the drift gate
	}
	if w.VLaunch {
		return false, true // [06 §3.3] vertical-launch: the result only, latch not tested
	}
	// Line-of-sight/self-propelled and dropped "gate on neither field"
	// [06 §3.3]. The fifth rung — a weapon matching no executor flag, which
	// "can never fire" — is not a readiness question and is not decided here.
	return false, false
}

// ComputeStoredReload implements the integer-truncated reload computation [06 §4.2] C7 (I3) [01 §8].
//
// `authoredReload` is the weapon record's reload word, which every reader in
// retail reads zero-extended — this recomputation, the maximum-reload
// notification, the stockpile production visit and the interface percentage —
// so it arrives in 0..65535 and is never negative [06 §4.2][06 §11.1]. The
// signed 16-bit word in this path is the caller's store of the RESULT into the
// slot, which is a different field.
//
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
	tier := veteranTier(kills) // shared unsigned stored-word reader [06 §4.2]
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
