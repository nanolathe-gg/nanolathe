package combat

import (
	"sort"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Packet is the nine-byte damage packet [06 §9.1].
//
// Retail's record is builder tag (1) + victim id (2) + shooter id (2) +
// amount (2) + armor/direction (1) + kind (1) [06 §9.1]. That is record
// identity, not Go layout: the fields below are named and the engine never
// serializes a packet, so no byte form exists here (I13).
// Ids are u16 with 0 = null and NO generation tags — stale-id reuse is
// accepted per [06 §5.1], [06 §9.1] (I5).
type Packet struct {
	Builder   uint8  // builder tag [06 §9.1] — semantic name unresolved
	Victim    uint16 // unsigned 16-bit victim id, 0=null, no generation token [06 §5.1], [06 §9.1] (I5) (I13)
	Attacker  uint16 // unsigned 16-bit shooter id, 0=null, no validation [06 §5.1], [06 §9.1] (I5)
	Amount    uint16 // signed 16-bit amount packed modulo 65,536 [06 §9.2] step 7 — low 16 bits after C20 pipeline
	Direction uint8  // one-byte armor/direction value [06 §9.1], [06 §9.2] C21 — HitByWeapon receives two 400-radius trig components derived from it [06 §9.1]
	Kind      uint8  // kind byte [06 §9.1], [06 §9.2] C21 — 1 ordinary, 2 paralyzer, 10 heal, 11 skip-reaction [06 §9.1] [06 §12.1]
}

// Player-slot control-byte identities [05 R-SHARE-01 §1]. The byte lives on the
// player slot record, not on the unit: a unit's own owner byte is the slot
// number, and the class is reached through that slot [06 R-DMG-01 §8].
//
// An UNOCCUPIED row is a distinct fourth reading, which is what
// ControlByteAbsent is: not a retail control-byte value, but the accessor's way
// of saying the row the side byte names is not occupied. Gate 1 PASSES an
// unoccupied row; gate 2 rejects it [06 R-DMG-01 §9].
const (
	ControlByteAbsent   uint8 = 0 // the row is not occupied [06 R-DMG-01 §9]
	ControlByteHuman    uint8 = 1 // locally controlled human [05 R-SHARE-01 §1]
	ControlByteComputer uint8 = 2 // computer player [05 R-SHARE-01 §1]
	ControlByteRemote   uint8 = 3 // remote peer [05 R-SHARE-01 §1]
)

// PlayerControlByteFor is the damage funnel's reader of the player row's
// control byte [06 R-DMG-01 §8]. The session binds it to the authoritative
// player record; it returns ControlByteAbsent for any row that is not occupied.
//
// Representation of the eleventh row. Retail's player table has ELEVEN rows:
// the battle-block allocator constructs row 10 — the row a null-shooter
// record's neutral side byte selects — and no writer ever occupies it, so the
// gate's occupancy read is the whole test for side 10 [06 R-DMG-01 §9]. That
// section states a bound check and a real never-occupied eleventh row are
// indistinguishable. This build takes the BOUND CHECK: the session's accessor
// returns ControlByteAbsent for any index past its ten player records, which is
// exactly what a constructed-but-never-occupied row 10 reads as.
//
// A nil accessor reads every row as unoccupied. That passes gate 1 (as retail's
// unoccupied row does) and rejects gate 2 (as retail's does), so it is not
// "assume eligible" in either direction.
func (s *Service) PlayerControlByteFor(owner uint8) uint8 {
	if s == nil || s.ControlByte == nil {
		return ControlByteAbsent
	}
	return s.ControlByte(owner)
}

// DamageRoutingAdmitted is gate 1 of [06 R-DMG-01 §8] item 1 as
// [06 R-DMG-01 §9] corrected it, read on the PROJECTILE's side byte. Retail is
// two reads on the selected row, entering the routing on the first success:
//
//	if row.occupied == 0 -> route damage    ; an unoccupied row PASSES
//	if row.control != 3  -> route damage    ; occupied, not a remote peer
//	otherwise            -> skip            ; an occupied remote-peer row only
//
// so `skip = side < 10 && slot[side].occupied && slot[side].control == 3`. The
// two reads collapse into one comparison here because the accessor already
// reports an unoccupied row as ControlByteAbsent, which is never
// ControlByteRemote.
//
// An absent row admits a meteor's neutral side. A death explosion instead
// carries the dying unit's owner side through this same test [06 §12.2].
// Shooter identity is independent: a null shooter suppresses attacker
// veterancy and does not overwrite the victim's preceding attacker snapshot.
//
// When it does skip, no damage is routed at all — the shake, the sound and the
// art of [06 §9.1] steps 4 and 5 have already happened.
func (s *Service) DamageRoutingAdmitted(shooterSide uint8) bool {
	return s.PlayerControlByteFor(shooterSide) != ControlByteRemote
}

// DeathLatchAdmitted is gate 2 of [06 R-DMG-01 §8], read on the VICTIM's owner
// byte: only control bytes 1 and 2 latch death on a non-positive health result
// (preserving the modular health value) and only they admit the paralyzer
// branch. Anything else — no record, or 3 — clamps health to zero and
// continues to the callbacks without dying through this path.
//
// It is the same test the per-player unit sweep applies to its water-damage,
// self-repair, order-pump and mover block [04 R-MOV-03 §1][04 §9.2].
func (s *Service) DeathLatchAdmitted(owner uint8) bool {
	b := s.PlayerControlByteFor(owner)
	return b == ControlByteHuman || b == ControlByteComputer
}

// UnitArmored is the packet builder's armor gate operand [06 R-DMG-01 §8]:
// bit 1 of the unit's first runtime state byte — the COB `set ARMORED` posture
// of port 20 [04 §4.4], zero at creation and written by nothing else.
//
// The FBI `armoredstate` key is a DIFFERENT thing that shares the name: it is
// parsed into a definition flag bit that has no reader anywhere in retail
// [R-DMG-01 §2]. ORing it in here would make every unit authored
// `armoredstate=1` permanently armored, which retail never does.
func UnitArmored(u *units.Unit) bool {
	return u != nil && u.Armored
}

// Kind constants [06 §9.1], [06 §12.1].
const (
	KindOrdinary   uint8 = 1  // ordinary weapon damage [06 §9.1]
	KindParalyzer  uint8 = 2  // paralyzer packet [06 §10]
	KindHeal       uint8 = 10 // heal (returns before kind-byte write & attacker snapshot) [06 §12.1]
	KindNoReaction uint8 = 11 // subtracts health but skips reaction/callbacks [06 §9.1]
)

// DamageInput is a locally delivered damage packet before defender scaling.
// Victim and Attacker are raw pool slots: the attacker is deliberately not
// validated, while zero remains the null attacker [06 §9.1] C18.
type DamageInput struct {
	// Modern can observe the incoming projectile at impact without learning
	// its unseen shooter's position. Zero means no horizontal motion cue.
	ImpactVelocityX, ImpactVelocityZ numeric.Fixed
	Victim, Attacker                 pool.Handle
	Nominal                          int32
	Direction                        uint8
	Kind                             uint8
}

// DamageResult reports the receiver's acceptance and packed post-defender
// amount. A zero amount is still an accepted packet [06 §9.1] C20.
type DamageResult struct {
	Accepted     bool
	Amount       uint16
	DeathLatched bool
}

// SelectBaseDamage selects the UnitName override or default damage [06 §9.2] C19.
//
// Weapons carry a sorted damage override table keyed by the target definition's
// exact UnitName string [06 §9.2] C19. Default damage is read as unsigned
// 16-bit; a matched override is signed 32-bit. Lookup is a case-insensitive
// binary search over the sorted table compiled in phase 2 (PLAN_02 C4)
// [06 §9.2]. It is not a category lookup.
func SelectBaseDamage(w *content.WeaponDef, targetUnitName string) int32 {
	if w == nil {
		return 0
	}
	if targetUnitName == "" {
		return int32(uint16(w.DamageDefault)) // unsigned 16-bit default [06 §9.2]
	}
	if len(w.Damage) == 0 {
		return int32(uint16(w.DamageDefault))
	}
	// Deterministic case-insensitive binary search over sorted keys (I1).
	// WeaponDef.DamageKeysSorted sorts case-insensitively, matching retail's
	// case-insensitive binary search [06 §9.2].
	keys := w.DamageKeysSorted()
	// Binary search with case-insensitive compare.
	i := sort.Search(len(keys), func(i int) bool {
		return compareCaseInsensitive(keys[i], targetUnitName) >= 0
	})
	if i < len(keys) && strings.EqualFold(keys[i], targetUnitName) { // [06 §9.2] case-insensitive
		// Matched override is signed 32-bit [06 §9.2].
		return w.Damage[keys[i]]
	}
	return int32(uint16(w.DamageDefault)) // unsigned 16-bit default [06 §9.2]
}

// compareCaseInsensitive returns -1,0,1 for case-insensitive compare [06 §9.2].
// It compares lowercased forms only; equality is lower-case equality, matching
// EqualFold semantics for the binary search. Tie-break by original case is
// irrelevant for equality, so it is not used here (search uses lower bound).
func compareCaseInsensitive(a, b string) int {
	la := strings.ToLower(a)
	lb := strings.ToLower(b)
	if la < lb {
		return -1
	}
	if la > lb {
		return 1
	}
	return 0
}

// Blast radius helpers [06 §9.3].

// StoredArea reads the weapon record's areaofeffect word the way every retail
// blast reader does: zero-extended from its 16-bit store
// [02 R-KEYS-01 §5][06 §9.3]. The content compiler already wraps the authored
// key to that width, so this is the identity for a compiled definition; it is
// kept as the single named reading so the two blast consumers — the area
// sweep's radius here and the interceptor blast of [06 R-WPN-05 §10] — cannot
// drift apart again, which is what a 32-bit shift on one side and a 16-bit
// mask on the other had already done.
func StoredArea(authoredArea int32) int32 {
	return int32(uint16(authoredArea))
}

// BlastRadius returns authoritative blast radius in world units: the unsigned
// 16-bit area word shifted right once [06 §9.3] C26.
func BlastRadius(authoredArea int32) int32 {
	return StoredArea(authoredArea) >> 1 // [06 §9.3] unsigned word, shifted once
}

// BroadPhaseRadiusCells returns (radius/16)+1 terrain cells around impact
// cell for the rectangular broad phase [06 §9.3] C26.
func BroadPhaseRadiusCells(radius int32) int32 {
	if radius < 0 {
		radius = 0
	}
	return radius/16 + 1 // [06 §9.3]
}

// communityOffMapDistance uses CP-ENV-1's saturating distance rather than
// retail area damage's signed-word wrap. An exact integer comparison first
// detects distances above the 32767-world-unit clamp; values inside that bound
// then use the retail area helper's double-precision root and whole-unit
// truncation [community patch engine behavior §5.7 CP-ENV-1].
func communityOffMapDistance(impact Vec3, u *units.Unit) int32 {
	if u == nil || u.Def == nil {
		return 32767
	}
	min, max := u.Def.BoundingExtents()
	// CP-ENV-1 adds each signed 32-bit stored position and extent at that same
	// width before widening the separation for its double-precision square.
	position := [3]int32{int32(u.X.Raw()), int32(u.Y.Raw()), int32(u.Z.Raw())}
	p := [3]int32{int32(impact.X.Raw()), int32(impact.Y.Raw()), int32(impact.Z.Raw())}
	lo := [3]int32{position[0] + min[0], position[1] + min[1], position[2] + min[2]}
	hi := [3]int32{position[0] + max[0], position[1] + max[1], position[2] + max[2]}
	const clampRaw = uint64(32767) * uint64(numeric.FractionOne)
	const clampSquared = clampRaw * clampRaw
	var sum uint64
	for axis := 0; axis < 3; axis++ {
		var delta uint64
		if p[axis] < lo[axis] {
			delta = uint64(int64(lo[axis]) - int64(p[axis]))
		} else if p[axis] > hi[axis] {
			delta = uint64(int64(p[axis]) - int64(hi[axis]))
		}
		if delta > clampRaw {
			return 32767
		}
		squared := delta * delta
		if sum > clampSquared-squared {
			return 32767
		}
		sum += squared
	}
	wrappedImpact := Vec3{X: numeric.Fixed(int64(p[0])), Y: numeric.Fixed(int64(p[1])), Z: numeric.Fixed(int64(p[2]))}
	return DistanceToBox(wrappedImpact, UnitForArea{
		Handle: u.Handle,
		Pos:    Vec3{X: numeric.Fixed(int64(position[0])), Y: numeric.Fixed(int64(position[1])), Z: numeric.Fixed(int64(position[2]))},
		Min: Vec3{
			X: numeric.Fixed(int64(lo[0])),
			Y: numeric.Fixed(int64(lo[1])),
			Z: numeric.Fixed(int64(lo[2])),
		},
		Max: Vec3{
			X: numeric.Fixed(int64(hi[0])),
			Y: numeric.Fixed(int64(hi[1])),
			Z: numeric.Fixed(int64(hi[2])),
		},
	})
}

// Falloff computes area falloff for accepted nonzero distance d and radius R [06 §9.3] C26.
//
//	f        = (float)d / (float)R - 1.0f
//	falloff  = (1.0f - edgeEffectiveness) * f * f + edgeEffectiveness
//
// Zero distance is exactly one. The executable does not clamp authored edge
// effectiveness in this path [06 §9.3].
//
// WIDTH AND ASSOCIATION. [06 §9.3] is explicit that the whole expression is
// "evaluated on the x87 stack and the result stored back as single precision" —
// one store, at the end — and re-tracing the recipient loop this session
// confirms it instruction by instruction: the distance and the radius are
// converted from their integers, the quotient, the subtraction of one, the
// square, the scaling by one-minus-edge and the final addition all stay on the
// stack, and a single single-precision store writes the falloff that is then
// passed by value to the amount scaler of [06 §9.2]. Nothing narrows in
// between.
//
// This used to be a chain of float32 operations, which rounds at every step
// where retail rounds once, and the error survives into the
// `trunc((double)base × falloff)` of [06 §9.2]: a radius-ten blast one world
// unit from the centre with no edge effectiveness has an exact falloff of 0.81,
// whose float32 store times a base of 100 truncates to 81, while the stepwise
// float32 chain lands on 0.80999994 and truncates to 80. The float64
// intermediates below are the x87 working precision, never stored (I2, the
// area-damage falloff row).
//
// The association is retail's too, and it is not the one the formula above
// reads as: the square is formed FIRST and then scaled by one-minus-edge —
// `f*f*(1-edge)`, not `(1-edge)*f*f`. The two differ in the last bit of the
// working-precision value, which can survive the narrowing.
//
// d and R reach here already converted from the integer distance and radius of
// [06 §9.3]; both are signed-16-bit-narrowed whole world units, so the float32
// parameters carry them exactly and the conversion loses nothing. The r <= 0
// arm has no retail counterpart and is unreachable from the enumerator, which
// accepts a recipient only on a strict d < R with a non-negative d.
func Falloff(d, r float32, edgeEffectiveness float32) float32 {
	if r <= 0 {
		return 1
	}
	if d == 0 {
		return 1 // [06 §9.3] The zero-distance value is exactly one
	}
	f := float64(d)/float64(r) - 1
	edge := float64(edgeEffectiveness)
	// The explicit conversion of the product is the rounding retail's x87
	// performs before the final add: three separate working-precision
	// roundings (the square, the scale by one-minus-edge, then the add), and
	// only then the one narrowing store. Without it a backend that has a fused
	// multiply-add — every arm64 build, and amd64 under GOAMD64=v3 — rounds
	// the scale and the add together and stores a different float32
	// [06 §9.3] (I2, the area-damage falloff row and its no-fusion rule).
	return float32(float64(f*f*(1-edge)) + edge) // [06 §9.3] one single-precision store
}

// ComputeScaledAmount implements the arithmetic order for a projectile
// recipient exactly as tabulated in [06 §9.2] C20.
//
// Order [06 §9.2] C20:
//
//  1. select UnitName override or default damage;
//  2. multiply by area falloff, truncate toward zero [01 §8] (I3);
//  3. apply attacker veterancy (6% per tier, tier=min(kills/5,5)), truncate integer percentage [01 §8];
//  4. apply recovered global double/half gates [06 §9.2];
//  5. if target is in armored state and incoming amount <30000, apply fixed-point damage modifier (definition scale >>16) [06 §9.2];
//  6. apply defender veterancy ((25-tier)*4)/100, truncate [06 §9.2];
//  7. pack low 16 bits modulo 65,536 [06 §9.2].
//
// Healing bypasses steps 5 and 6 [06 §9.2]. Paralyzer packets use ordinary
// incoming scaling before unsigned 16-bit duration credit is queued [06 §9.2].
// Global double/half gates multiply/adjust before health application [06 §9.2].
// The local DoubleShot and HalfShot commands independently toggle their
// battle-owned inputs [07 R-CAM-01 §6].
// Amount modulo and health subtraction ordering preserves low-32-bit wrap
// [06 §9.2] per C20.
// This standalone arithmetic entry models a present shooter; production passes
// explicit shooter presence into weaponNominal before the shared receiver.
func ComputeScaledAmount(baseDamage int32, falloff float32, attackerKills int32, defenderKills int32, isArmored bool, damageModifier int32, isHealing bool, globalDouble bool, globalHalf bool) uint16 {
	attackerLevel := int32(StrictRules{}.VeteranLevel(VeteranLevelRequest{Kills: uint16(attackerKills)}))
	amount := weaponNominal(baseDamage, falloff, attackerLevel, true, globalDouble, globalHalf)
	if isHealing {
		// Healing bypasses steps 5 and 6 [06 §9.2] C20.
		return uint16(amount) // pack low 16 bits modulo 65,536 [06 §9.2] step 7
	}
	defenderLevel := int32(StrictRules{}.VeteranLevel(VeteranLevelRequest{Kills: uint16(defenderKills)}))
	return scaleAcceptedAmount(amount, defenderLevel, isArmored, damageModifier)
}

// weaponNominal is the weapon-side half of C20. It intentionally knows
// nothing about the recipient: fixed producers have no weapon and enter the
// receiver below with their established nominal directly [06 §9.2].
func weaponNominal(baseDamage int32, falloff float32, attackerLevel int32, hasAttacker, globalDouble, globalHalf bool) int32 {
	// Step 1 already applied: baseDamage is selected override or default [06 §9.2] C19.
	// The base crosses the stored single-precision falloff boundary; only the
	// signed-64 truncation retains its low 32 bits [06 §9.2][01 R-DET-01 §1].
	amount := numeric.TruncateFloat64ToLow32(float64(baseDamage) * float64(falloff))

	// A null shooter skips this stage, including the tier-zero multiplication.
	// Percentage products wrap before the signed division [06 §9.2].
	if hasAttacker {
		amount = (amount * (100 + 6*attackerLevel)) / 100
	}

	// Step 4: apply recovered global double/half gates [06 §9.2] step 4.
	// Bits are the global options word's bit7 (0x80) for double and bit8
	// (0x100) for half, in order *2 then /2. Doubling wraps before division,
	// so both set need not recover the input [P1-07 §2.5][06 §9.2].
	if globalDouble {
		amount = amount * 2 // [06 §9.2] global double [P1-07 §2.5] options word bit7
	}
	if globalHalf {
		amount = amount / 2 // truncate toward zero [01 §8] [06 §9.2] global half [P1-07 §2.5] options word bit8
	}

	return amount
}

// scaleAcceptedAmount is C20's defender-side half: armor, defender veterancy,
// then the packet's low-word packing. It is shared by weapon and fixed packet
// producers, which keeps a fixed nominal from acquiring a fictitious weapon
// lookup [06 §9.2] [06 R-DMG-01 §8].
func scaleAcceptedAmount(amount, defenderLevel int32, isArmored bool, damageModifier int32) uint16 {

	// Step 5: if target is in armored state and incoming amount <30000, apply fixed-point damage modifier [06 §9.2] step 5.
	// The modifier is definition's fixed-point scale >>16 (damageModifier is 16.16, 65536 =1.0) [02 "Unit record"].
	if isArmored && amount < 30000 {
		// The full signed product uses an arithmetic right shift, including
		// its downward rounding for a negative product [06 §9.2].
		amount = int32((int64(amount) * int64(damageModifier)) >> 16) // [06 §9.2] step 5
	}

	// Step 6: apply defender veterancy ((25-tier)*4)/100, truncate [06 §9.2] step 6.
	defFactor := (25 - defenderLevel) * 4
	amount = (amount * defFactor) / 100 // wrap before signed division [06 §9.2]

	// Step 7: pack low 16 bits into packet, modulo 65,536 [06 §9.2].
	return uint16(amount) // modulo 65,536 [06 §9.2] step 7
}

// ReactionSeams binds the parts of the damage-intake reaction routine of
// [06 §9.1] step 4 that internal/combat cannot reach from inside itself. The
// order-side parts live in internal/orders, which imports this package, so they
// arrive as function values the session installs at composition; the alliance
// row, the computer player's manager and the interface message queue are
// session-owned for the same reason. A nil seam, or a nil member, makes that
// part a no-op — the routine's ORDER and GATES stay here regardless.
type ReactionSeams struct {
	// ObserverNotice is part 1: deliver event code 16 to every order record
	// observing the victim [06 R-WPN-04 §2 part 1][04 R-MOV-03 §7].
	ObserverNotice func(victim *units.Unit)
	// Allied reports the alliance relation the retaliation gate tests
	// ("the attacker is not allied") [08 R-AI-01 §11].
	Allied func(a, b uint8) bool
	// ArmConstructionThrottle writes the owning player's manager throttle
	// deadline — the one simulation draw of bound 300 in the whole damage-intake
	// path — for a computer player [08 R-AI-01 §11].
	ArmConstructionThrottle func(owner uint8, tick uint32)
	// PurgeOrdersOnDamage selectively removes unprotected primary orders;
	// it does not issue Stop [08 R-AI-01 §11][04 R-MOV-03 §6].
	PurgeOrdersOnDamage func(victim *units.Unit)
	// RetaliationOrder is the retaliation's order branch: the shared auto-engage
	// issuer with force = 0, behind the front-order and category admissions
	// [08 R-AI-01 §11][04 R-STANCE-01 §3]. It reports whether a record was
	// inserted; the per-slot offer runs only when it was not. The branch's
	// third admission — slot 0's acquisition predicate against the attacker —
	// is applied on THIS side, before the call, because the order side does not
	// hold that predicate's operands.
	RetaliationOrder func(victim, attacker *units.Unit) bool
	// SlotAcquisitionAdmits is the §3.1 acquisition physical gate for one of the
	// victim's weapon slots against one candidate. The damage path does not
	// carry the visibility, terrain, ledger and catalog operands that gate
	// needs, so the session supplies it bound to them [06 §3.1].
	//
	// The reaction routine calls it at TWO sites: once for slot 0 against the
	// attacker, ahead of the order branch's issuer [08 R-AI-01 §11], and once
	// per slot inside the per-slot offer [06 R-WPN-04 §2 part 3].
	SlotAcquisitionAdmits func(victim *units.Unit, slotIdx int, candidate *units.Unit) bool
	// UnderAttackSilenced reads bit 7 of the gate-mask word of the victim's
	// front primary order [06 R-WPN-04 §2 part 4].
	UnderAttackSilenced func(victim *units.Unit) bool
	// UnderAttackNotice requests the interface message of kind 2. The helper
	// applies its own selection/ownership/liveness gates [06 R-WPN-04 §2 part 4].
	UnderAttackNotice func(victim *units.Unit)
}

// The slot autonomy bit is units.SlotFlagAutonomous. [04 R-UNIT-06 §5 part 3]
// answers what this file's `slotTrackingFlag` constant recorded as unknown:
// [R-ORDER-02 §2]'s "slot control byte" and [08 R-SAVE-WEAPON-01]'s persisted
// slot-flag byte ARE one byte, whose bit 1 is *slot enabled* and whose bit 4 is
// the one [R-ORD-01 §7] called the "inhibit latch" and [06 §1.2] called
// "tracking". A build modelling them as two fields must collapse them to this
// one bit, so the separate constant is gone and every reader here reads the
// control byte.
//
// Set means the slot belongs to AUTONOMOUS acquisition; the two slot verbs are
// its only writers, and their names read inverted against their effect —
// *release* takes the slot for an order's own target (clearing the bit) and
// *inhibit* hands it back (setting it).

// DamageFlashByte is the value the damage dispatcher writes into the victim's
// minimap blink byte: retail stores 240. The Go field is signed, so it carries
// the same byte pattern as -16; the unit sweep interprets the byte over its
// full 240-visit lifetime [06 R-WPN-04 §2].
const DamageFlashByte int8 = -16

// SetDamageFlash arms the victim's minimap blink [06 R-WPN-04 §2]. Every packet
// the dispatcher accepts other than a heal writes it — paralyze included — and
// the write happens BEFORE the reaction routine, which is what [06 §9.1] step 4
// means by "set the damage flash; for every kind except 11 run
// reaction/wake/retarget". Every hit re-arms it outright; there is no maximum
// and no accumulation.
//
// The byte is authoritative unit state with no simulation reader: its one
// consumer is the minimap contacts pass, which the publication boundary feeds
// [03 §3.9]. The ordered EventDamageFlash cue is kept beside this write for the
// presentation layers that want the edge rather than the level.
func SetDamageFlash(victim *units.Unit) {
	if victim == nil {
		return
	}
	victim.BlinkSuppress = DamageFlashByte
}

// ReactToDamage is the damage-intake reaction routine of [06 §9.1] step 4,
// closed at [06 R-WPN-04 §2]. It runs for every accepted non-heal packet whose
// kind is not 11, AFTER the damage flash and BEFORE the kind byte and attacker
// fields are rewritten — which is why parts 3 and 4 still read the PREVIOUS
// packet's kind and attacker-side snapshot.
//
// Its four parts run in this order and no other:
//
//  1. the observer notice;
//  2. attacker validation — an attacker whose definition index is zero (a
//     freed slot) counts as no attacker for the rest of the routine;
//  3. the construction throttle, then the retaliation offer, exactly
//     [08 R-AI-01 §11];
//  4. the under-attack notice.
func (s *Service) ReactToDamage(w *units.World, victim, attacker *units.Unit, tick uint32) {
	if s == nil || victim == nil {
		return
	}
	r := s.Reaction
	if r != nil && r.ObserverNotice != nil {
		r.ObserverNotice(victim) // part 1 [06 R-WPN-04 §2]
	}
	if attacker != nil && attacker.Def == nil {
		attacker = nil // part 2: a freed slot is no attacker [06 R-WPN-04 §2]
	}
	s.reactionThrottle(victim, tick)           // part 3, first half [08 R-AI-01 §11]
	s.reactionRetaliation(w, victim, attacker) // part 3, second half
	s.reactionUnderAttackNotice(victim)        // part 4
}

// reactionThrottle is the construction throttle of [08 R-AI-01 §11]: when the
// damaged unit's definition has the authored `cancapture` flag, its owning
// player record exists and that player's control byte is 2, the engine draws
// RNG(300) and writes the owning player's manager throttle deadline to
// tick + 30 + draw, then selectively purges its primary orders. It is
// computer-players-only, and the draw is the only
// simulation draw anywhere in the damage-intake path.
//
// Retail arms it from DAMAGE to a `cancapture` unit. Before this routine
// existed the session armed it from death finalization instead, which is a
// different event with a different cadence.
func (s *Service) reactionThrottle(victim *units.Unit, tick uint32) {
	if s == nil || victim == nil || victim.Def == nil || !victim.Def.CanCapture {
		return
	}
	if s.PlayerControlByteFor(victim.Owner) != ControlByteComputer {
		return
	}
	r := s.Reaction
	if r == nil {
		return
	}
	if r.ArmConstructionThrottle != nil {
		r.ArmConstructionThrottle(victim.Owner, tick)
	}
	if r.PurgeOrdersOnDamage != nil {
		r.PurgeOrdersOnDamage(victim)
	}
}

// reactionRetaliation is the return-fire half of [08 R-AI-01 §11], which is not
// computer-player-specific: it is the engine's return fire and applies to a
// human player's units too.
//
// Its outer admission, all required: the attacker is known; the victim's owner
// has control byte 1 or 2; the victim's definition is ARMED (its derived flag,
// set unless all three resolved weapon slots are empty) or carries `kamikaze`;
// the victim is fully built; and the attacker is not allied.
//
// Then the order branch runs first ([04 R-STANCE-01 §3] restates it in stance
// terms), and only when it issued nothing does the per-slot offer run, gated on
// the victim's standing-fire field being non-zero.
func (s *Service) reactionRetaliation(w *units.World, victim, attacker *units.Unit) {
	if s == nil || victim == nil || attacker == nil || victim == attacker {
		return
	}
	// "a controller of type 1 or 2" — the same two values the death latch
	// admits [06 R-DMG-01 §8][08 R-AI-01 §11].
	if !s.DeathLatchAdmitted(victim.Owner) {
		return
	}
	armed := victim.Flags&units.ArmedStatus != 0 || (victim.Def != nil && victim.Def.Kamikaze)
	if !armed {
		return
	}
	if victim.Remaining != 0 {
		return // fully built only [08 R-AI-01 §11]
	}
	r := s.Reaction
	if r == nil {
		return
	}
	if r.Allied == nil || r.Allied(victim.Owner, attacker.Owner) {
		return // "the attacker is not allied"; with no alliance row, fail closed
	}
	// The order branch's last admission before the issuer: the slot admission
	// predicate evaluated for SLOT 0 against the attacker [08 R-AI-01 §11]
	// [06 §3.1] (RWU-19-39). It sits here rather than inside the order-side
	// seam because the predicate needs the world, visibility, terrain, ledger
	// and catalog operands internal/orders does not hold; the seam owns the
	// front-order and category halves and this side owns this one.
	//
	// Retail evaluates it after those two halves. Hoisting it ahead of them is
	// observationally identical: all three are pure predicates ANDed together,
	// and this one draws no RNG (see SlotAcquisitionAdmits), so neither the
	// outcome nor the simulation stream depends on the order (I4).
	//
	// A refusal skips ONLY the order branch. The "otherwise" arm of §11 still
	// runs: the standing-fire gate below and then the per-slot offer, which
	// re-tests each of the three slots on its own terms — so a victim whose
	// slot 0 cannot admit the attacker can still swing slot 1 or 2 onto it.
	//
	// An unbound predicate fails closed, the way the alliance row above does:
	// retail always evaluates it, so a build that cannot is not entitled to
	// issue the order.
	if r.SlotAcquisitionAdmits != nil && r.SlotAcquisitionAdmits(victim, 0, attacker) {
		if r.RetaliationOrder != nil && r.RetaliationOrder(victim, attacker) {
			return // an order was issued; the offer does not also run [08 R-AI-01 §11]
		}
	}
	if victim.Flags>>units.StandingFireShift&units.StandingFieldMask == 0 {
		return // the offer needs a non-zero standing-fire field [08 R-AI-01 §11]
	}
	s.offerAttackerToSlots(w, victim, attacker)
}

// offerAttackerToSlots is the per-slot offer, whose admission [06 R-WPN-04 §2
// part 3] states exactly: for each slot whose armed and tracking bits are set,
// the §3.1 acquisition physical gate accepts the attacker for that slot AND the
// weapon is not `commandfire`; the attacker is then installed through the
// unit-target setter (which preserves the Aim latch, [06 §3.2]) unless the
// slot's present target exists, passes the same gate, and is clear of the
// slot's bad-target set.
//
// The `commandfire` clause is why a human commander's disintegrator is never
// offered its attacker while its laser is: the same contract the autonomous
// scan carries at [06 §3.2].
func (s *Service) offerAttackerToSlots(w *units.World, victim, attacker *units.Unit) {
	r := s.Reaction
	if r == nil || r.SlotAcquisitionAdmits == nil {
		return
	}
	for idx := 0; idx < units.NumSlots; idx++ { // numeric slot order [06 §3.2]
		slot := victim.SlotAt(idx)
		// "for each slot whose armed and tracking bits are set" — the armed
		// half is the control byte's ENABLED bit (bit 1), which
		// [06 R-WPN-05 §3] names and [08 R-AI-01 §11] writes out as "each of
		// the victim's three weapon slots that is enabled and autonomous". The
		// offer used to test only that the slot held a resolved weapon. For a
		// unit built in this process the initializer sets the bit for exactly
		// the slots whose weapon link resolved, so the two agree; after a save
		// load the persisted byte is authoritative on its own
		// [08 R-SAVE-WEAPON-01], and a restored-disabled slot with a live
		// weapon link was still being handed the attacker.
		//
		// The resolved-weapon test stays beside it because the `commandfire`
		// clause below dereferences the link: it is this implementation's nil
		// guard, not a retail clause. The slot pipeline's own visits carry the
		// same pair.
		if slot == nil || !slot.IsEnabled() || !slot.IsPopulated() {
			continue
		}
		// The tracking half is the control byte's autonomy bit
		// [06 R-WPN-05 §3][04 R-UNIT-06 §5 part 3], and
		// the test is load bearing: a slot an attack order currently holds must
		// not be handed the attacker, and becomes eligible again the moment the
		// record destructor returns it. It was skipped while §1 left the bit's
		// writers open; §5 closes them.
		if slot.Flags&units.SlotFlagAutonomous == 0 {
			continue
		}
		if slot.Weapon.CommandFire {
			continue // "and the weapon is not `commandfire`" [06 R-WPN-04 §2]
		}
		if !r.SlotAcquisitionAdmits(victim, idx, attacker) {
			continue
		}
		if s.slotKeepsPresentTarget(w, victim, slot, idx) {
			continue
		}
		// The unit-target setter preserves the Aim latch and the asynchronous
		// Aim result and writes ONLY the target pair [06 §3.2]: "the unit-target
		// and point-target setters write only the target pair; they do not touch
		// the control byte" [04 R-UNIT-06 §5 part 3]. An `armed` OR stood here
		// with no retail counterpart — the setters write no flag byte at all.
		slot.Target = units.Target{Kind: units.TargetUnit, Unit: attacker.Handle}
		// Replacement also clears prior shot feedback from the owner; keeping
		// an existing target above must leave those events pending
		// [06 R-WPN-05 §6].
		victim.Pending &^= units.PendingSlotSetterClear
	}
}

// slotKeepsPresentTarget is the offer's "unless" clause: the slot's present
// target exists, passes the same acquisition gate, and is clear of the slot's
// bad-target set [06 R-WPN-04 §2 part 3].
func (s *Service) slotKeepsPresentTarget(w *units.World, victim *units.Unit, slot *units.Slot, idx int) bool {
	if w == nil || slot.Target.Kind != units.TargetUnit || slot.Target.Unit == 0 {
		return false
	}
	cur := w.Unit(slot.Target.Unit)
	if cur == nil || !cur.Alive || cur.Dying {
		return false
	}
	r := s.Reaction
	if r == nil || r.SlotAcquisitionAdmits == nil || !r.SlotAcquisitionAdmits(victim, idx, cur) {
		return false
	}
	return IsPreferredCategoryMask(cur.Def.DefinitionMask(), badMaskForSlot(victim.Def, idx))
}

// reactionUnderAttackNotice is part 4 [06 R-WPN-04 §2]: read the gate-mask word
// of the victim's front primary order (zero when it has none); when its bit 7
// is clear AND either the stored attacker-side snapshot differs from the
// victim's owner byte or the stored last damage kind is 1, request the
// interface message of kind 2 (`Under Attack`) for the victim.
//
// Because the routine runs before the field rewrite, the two stored values are
// the PREVIOUS packet's. The first hit on a fresh unit therefore always
// qualifies (its snapshot is seeded to the neutral value 10 at spawn), while
// the hit that follows an own-side non-weapon packet — a reclaim pulse, a cargo
// cascade — is silent once.
func (s *Service) reactionUnderAttackNotice(victim *units.Unit) {
	r := s.Reaction
	if r == nil || r.UnderAttackNotice == nil {
		return
	}
	if r.UnderAttackSilenced != nil && r.UnderAttackSilenced(victim) {
		return // bit 7 set: already attacking, or an aircraft in follow/guard
	}
	if victim.LastDamageSide == victim.Owner && victim.LastDamageCause != uint8(CauseOrdinary) {
		return
	}
	r.UnderAttackNotice(victim)
}

// ApplyHealing implements the early heal arm's signed-word read/store and
// unsigned maximum clamp [06 §9.1][06 R-DMG-01 §4]. No damage-side effects
// belong to this arm.
func ApplyHealing(currentHealth int32, maxHealth int32, amount uint16) int32 {
	h := int32(int16(currentHealth)) + int32(amount)
	if uint32(maxHealth) <= uint32(h) {
		h = maxHealth
	}
	return int32(int16(h))
}

// DispatchHealingPacket adapts the legacy wire entry to the common kind-10
// receiver. Acceptance, unsigned healing and the signed-word store therefore
// have one implementation [06 §9.1].
func (s *Service) DispatchHealingPacket(w *units.World, packet Packet) bool {
	if packet.Kind != KindHeal {
		return false
	}
	return s.AcceptDamage(w, 0, DamageInput{
		Victim: pool.Handle(packet.Victim), Attacker: pool.Handle(packet.Attacker),
		Nominal: int32(packet.Amount), Direction: packet.Direction, Kind: packet.Kind,
	}).Accepted
}

// ApplySelfDestructDamage sends the fixed self-damage packet through the
// ordinary health/reaction funnel. The packet's 30000 amount is at the strict
// armor-bypass boundary; defender veterancy still applies, and a lethal result
// is marked with cause 3 so the normal death finalizer performs callbacks and
// death effects [08 R-SKIR-01 §3][06 §9.1][06 §12.1].
func (s *Service) ApplySelfDestructDamage(w *units.World, target pool.Handle, tick uint32) bool {
	return s.AcceptDamage(w, tick, DamageInput{Victim: target, Attacker: target, Nominal: 30000, Kind: uint8(CauseSelfDestruct)}).Accepted
}

// ApplyDamage performs exact 16-bit modular subtraction from health [06 §9.1].
// The health word wraps modulo 65,536 and the result is read as SIGNED 16-bit
// (sign-extended for the caller): a non-positive signed result is what makes
// the mobile controller classes latch death while preserving this modular
// value [06 §9.1]. For ordinary health/amount pairs the wrap is unobservable;
// it only shows on extreme amounts, exactly as in retail.
func ApplyDamage(currentHealth int32, amount uint16) int32 {
	r := (currentHealth - int32(amount)) & 0xFFFF
	if r >= 0x8000 {
		r -= 0x10000
	}
	return r
}

// Water damage per [04 §9.2] established block ("canhover is bit 12" through
// veteran reduction) and [06 §9.1][06 §12.1] cause 11.
//
// The retail contract [04 §9.2]:
// - evaluated per unit before movement, once per tick when globalTick%30==0,
// - only when owning player's class is 1 or 2 and both mission fields
//   waterdoesdamage and waterdamage are nonzero,
// - and only when unit's signed integer height is at or below sea-level byte
//   and canhover is clear (bit 12). floater/amphibious are NOT additional immunity.
// - qualifying units receive amount = mission.waterdamage as damage type 0xB
//   (KindNoReaction, 11) with null attacker through standard damage funnel;
//   blast falloff with stored distance zero => multiplier 1.0 for non-AOE water
//   damage, type 0xB emits neither HitByWeapon nor TakeDamage and never enters
//   feature-class effect block. Lethal 0xB sets normal death-pending.
// - funnel arithmetic [06 §9.2] C20: definition-scaled armor reduction gated on
//   bit 1 of victim's instance armor byte (mask 0x02) with strictly-below-30000
//   amount guard, then veteran tier factor ((25 - tier)*amount*4)/100 with
//   tier = min(kills/5,5) — veteran victim REDUCED water damage — and each
//   credited kill increments killer's counter (none for water damage, attacker null) [04 §9.2][06 §12.1].
//
// This file owns the arithmetic and eligibility predicates; the sweep
// The session applies water damage during each eligible phase-2 unit visit (I1).

// IsWaterDamageTick reports whether global tick is a water-damage tick [04 §9.2].
func IsWaterDamageTick(tick uint32) bool {
	return tick%30 == 0 // [04 §9.2] once per tick when globalTick % 30 == 0
}

// IsInWaterForDamage reports whether Y is at or below the sea-level byte
// [04 §9.2] (refinement of 2026-09-02): the operand is the signed 16-bit HIGH
// WORD of the 16.16 Y read in place — an arithmetic narrowing, so a negative
// fraction floors (−0.5 reads −1), not the __ftol truncation — compared as a
// signed 16-bit value against the zero-extended byte, inclusive. This used to
// truncate through Fixed.Int(), which differs only for negative fractional Y.
func IsInWaterForDamage(y numeric.Fixed, seaLevel uint8) bool {
	return int16(y.Raw()>>16) <= int16(seaLevel) // [04 §9.2] high word <= sea-level byte
}

// IsWaterDamageEligible reports per-unit eligibility ignoring global tick and
// mission gates, per [04 §9.2] canhover exclusion and Y <= seaLevel.
// Read-only on units/world state (I6).
func IsWaterDamageEligible(u *units.Unit, terrain *world.Terrain) bool {
	if u == nil || !u.Alive || u.Dying {
		return false
	}
	if u.Def != nil && u.Def.CanHover {
		return false // [04 §9.2] canhover is bit 12 excludes from water damage
	}
	// floater and amphibious are NOT additional immunity [04 §9.2]
	if terrain == nil {
		// Retail reads one process-global byte loaded with the map [04 §9.2];
		// there is no "no terrain" state to clone. A nil terrain is a
		// build-side caller error, answered closed (not in water).
		return false
	}
	return IsInWaterForDamage(u.Y, terrain.SeaLevel) // [04 §9.2] signed integer height at or below sea-level byte
}
