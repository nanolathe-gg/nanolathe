package combat

import (
	"encoding/binary"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Packet is the nine-byte damage packet [06 §9.1].
//
// Layout per [06 §9.1]: builder tag (1) + victim id (2) + shooter id (2) +
// amount (2) + armor/direction (1) + kind (1) = 9 bytes. Go struct uses named
// fields (I13); byte offsets are identity, not layout, except where serialized
// for wire/save boundaries (I13 exception).
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

// PacketSize is the wire size [06 §9.1].
const PacketSize = 9 // [06 §9.1] nine-byte damage packet

// MarshalPacket serializes p into 9 bytes little-endian [06 §9.1] (I13 exception).
// Wire order: builder(1) | victim(2) | attacker(2) | amount(2) | direction(1) | kind(1) [06 §9.1].
func MarshalPacket(p Packet) [PacketSize]byte {
	var b [PacketSize]byte
	b[0] = p.Builder
	binary.LittleEndian.PutUint16(b[1:3], p.Victim)
	binary.LittleEndian.PutUint16(b[3:5], p.Attacker)
	binary.LittleEndian.PutUint16(b[5:7], p.Amount)
	b[7] = p.Direction
	b[8] = p.Kind
	return b
}

// UnmarshalPacket deserializes 9 bytes [06 §9.1].
func UnmarshalPacket(b [PacketSize]byte) Packet {
	return Packet{
		Builder:   b[0],
		Victim:    binary.LittleEndian.Uint16(b[1:3]),
		Attacker:  binary.LittleEndian.Uint16(b[3:5]),
		Amount:    binary.LittleEndian.Uint16(b[5:7]),
		Direction: b[7],
		Kind:      b[8],
	}
}

// MarshalBytes is a slice variant that validates length.
func MarshalBytes(p Packet) []byte {
	b := MarshalPacket(p)
	out := make([]byte, PacketSize)
	copy(out, b[:])
	return out
}

// UnmarshalBytes validates length 9 [06 §9.1].
func UnmarshalBytes(b []byte) (Packet, bool) {
	if len(b) != PacketSize {
		return Packet{}, false
	}
	var arr [PacketSize]byte
	copy(arr[:], b)
	return UnmarshalPacket(arr), true
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
// The absent-row branch is the one an earlier reading inverted ("the row must
// exist"). Under that reading every meteor, death explosion and routed burn
// weapon — records that carry the neutral side byte or a zeroed one and no
// shooter — would have been harmless, contradicting §12.1's "the explosion
// damages every side alike" [06 R-DMG-01 §9]. Such a record damages, and can
// kill, every unit of every side within its radius, and credits nobody.
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

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// Bit7 (0x80) signed byte <0 doubles damage; bit8 (0x100) halves.
// Order is *2 then /2, so both set nets to *1 [P1-07 §2.5] [06 §9.2] step4.
const (
	GlobalDoubleMask = 0x80  // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	GlobalHalfMask   = 0x100 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
)

// ApplyGlobalGates applies the double/half gates in retail order *2 then /2
// from the raw 0x37f2f word [P1-07 §2.5] [06 §9.2] step4.
func ApplyGlobalGates(amount int32, rawFlags int) int32 {
	if rawFlags&GlobalDoubleMask != 0 {
		amount *= 2 // [P1-07 §2.5] signed <0 branch
	}
	if rawFlags&GlobalHalfMask != 0 {
		amount /= 2 // [P1-07 §2.5] bit8
	}
	return amount
}

// IsDamagePacketKind reports whether kind is one of the four damage packet
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func IsDamagePacketKind(k uint8) bool {
	return k == KindOrdinary || k == KindParalyzer || k == KindHeal || k == KindNoReaction // [P1-07 §2.6]
}

// IsDeathCause reports whether cause is one of the death causes 3..11 that
// are dispatched through the death handler's cause producer table [P1-07 §2.6]
// [06 §12.1]. Causes 12..15 have no local producer [06 §12.1].
func IsDeathCause(c uint8) bool {
	return c >= 3 && c <= 11 // [P1-07 §2.6] [06 §12.1] 3 self-destruct .. 11 water damage
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

// veteranTier computes tier = min(floor(unsigned kills/5),5) [06 §4.2], [06 §9.2].
func veteranTier(kills int32) int32 {
	tier := int32(uint32(kills) / 5) // unsigned division, floor [06 §9.2], [06 §4.2]
	if tier > 5 {
		tier = 5
	}
	return tier
}

// Blast radius helpers [06 §9.3].

// BlastRadius returns authoritative blast radius in world units: unsigned
// authored area value shifted right once [06 §9.3] C26.
func BlastRadius(authoredArea int32) int32 {
	return int32(uint32(authoredArea) >> 1) // [06 §9.3] unsigned shift
}

// BroadPhaseRadiusCells returns (radius/16)+1 terrain cells around impact
// cell for the rectangular broad phase [06 §9.3] C26.
func BroadPhaseRadiusCells(radius int32) int32 {
	if radius < 0 {
		radius = 0
	}
	return radius/16 + 1 // [06 §9.3]
}

// Falloff computes area falloff for accepted nonzero distance d and radius R [06 §9.3] C26.
//
//	(1 - edgeEffectiveness) * (d/R -1)^2 + edgeEffectiveness
//
// Zero distance is exactly one. Executable does not clamp authored edge
// effectiveness [06 §9.3].
func Falloff(d, r float32, edgeEffectiveness float32) float32 {
	if r <= 0 {
		return 1
	}
	if d == 0 {
		return 1 // [06 §9.3] The zero-distance value is exactly one
	}
	x := d/r - 1
	return (1-edgeEffectiveness)*x*x + edgeEffectiveness // [06 §9.3]
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
// Global double/half gates multiply/adjust before health application [06 §9.2]
// — exact configuration aliases not recovered; bool gates preserve order.
// Amount modulo and health subtraction ordering preserves low-32-bit wrap
// [06 §9.2] per C20.
func ComputeScaledAmount(baseDamage int32, falloff float32, attackerKills int32, defenderKills int32, isArmored bool, damageModifier int32, isHealing bool, globalDouble bool, globalHalf bool) uint16 {
	// Step 1 already applied: baseDamage is selected override or default [06 §9.2] C19.
	// Step 2: multiply by area falloff, truncate toward zero [01 §8] (I3) [06 §9.2].
	// Base damage is int32; falloff is single-precision float truncated.
	// Go's int32(float32) truncates toward zero per [01 §8] I3.
	amount := int32(float32(baseDamage) * falloff) // truncate toward zero [01 §8] [06 §9.2] step 2

	// Step 3: apply attacker veterancy 6% per tier, tier=min(kills/5,5), truncate [06 §9.2] [01 §8].
	tierA := veteranTier(attackerKills)
	// Multiply by (100+6*tier)/100 truncating toward zero.
	amount = int32((int64(amount) * int64(100+6*tierA)) / 100) // truncate integer percentage [01 §8] [06 §9.2] step 3

	// Step 4: apply recovered global double/half gates [06 §9.2] step 4.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// for half, in order *2 then /2 so both set nets *1 [P1-07 §2.5][06 §9.2].
	if globalDouble {
		amount = amount * 2 // [06 §9.2] global double [P1-07 §2.5] 0x37f2f bit7
	}
	if globalHalf {
		amount = amount / 2 // truncate toward zero [01 §8] [06 §9.2] global half [P1-07 §2.5] 0x37f2f bit8
	}

	if isHealing {
		// Healing bypasses steps 5 and 6 [06 §9.2] C20.
		return uint16(amount) // pack low 16 bits modulo 65,536 [06 §9.2] step 7
	}

	// Step 5: if target is in armored state and incoming amount <30000, apply fixed-point damage modifier [06 §9.2] step 5.
	// The modifier is definition's fixed-point scale >>16 (damageModifier is 16.16, 65536 =1.0) [02 "Unit record"].
	if isArmored && amount < 30000 {
		// Apply as (amount * modifier) >>16 with truncation toward zero [01 §8].
		// modifier is Fixed 16.16; Go division truncates toward zero; shift of positive matches trunc.
		// Use int64 to avoid overflow.
		amount = int32((int64(amount) * int64(damageModifier)) >> 16) // [06 §9.2] step 5
	}

	// Step 6: apply defender veterancy ((25-tier)*4)/100, truncate [06 §9.2] step 6.
	tierD := veteranTier(defenderKills)
	defFactor := (25 - tierD) * 4                            // (25-tier)*4
	amount = int32((int64(amount) * int64(defFactor)) / 100) // truncate [01 §8] [06 §9.2] step 6

	// Step 7: pack low 16 bits into packet, modulo 65,536 [06 §9.2].
	return uint16(amount) // modulo 65,536 [06 §9.2] step 7
}

// ComputePacket builds a Packet for a projectile recipient using the C20 order
// [06 §9.2] C20, C21. It selects base damage via override lookup, applies steps
// 2-7, and fills packet fields [06 §9.2] C21.
func ComputePacket(weapon *content.WeaponDef, targetUnitName string, falloff float32, attacker pool.Handle, victim pool.Handle, attackerKills int32, defenderKills int32, isArmored bool, damageModifier int32, isHealing bool, globalDouble bool, globalHalf bool, direction uint8, kind uint8) Packet {
	base := SelectBaseDamage(weapon, targetUnitName)                                                                                        // [06 §9.2] step 1
	amt := ComputeScaledAmount(base, falloff, attackerKills, defenderKills, isArmored, damageModifier, isHealing, globalDouble, globalHalf) // [06 §9.2] steps 2-7
	return Packet{
		Victim:    uint16(victim),   // 0=null, no generation [06 §5.1] (I5) C18
		Attacker:  uint16(attacker), // 0=null, no validation [06 §5.1] C18
		Amount:    amt,              // modulo amount [06 §9.2] C20 step 7
		Direction: direction,        // one-byte direction [06 §9.2] C21
		Kind:      kind,             // kind byte [06 §9.2] C21
	}
}

// ValidatePacketTarget reports whether victim acceptance requires alive bit and
// clear dead latch [06 §9.1]. Stale-id reuse is accepted: nonzero id converts
// directly by slot arithmetic with no liveness probe for attacker, and victim
// acceptance requires alive+clear dead latch so reused slot accepts stale packet
// [06 §5.1] C18. Attacker receives NO validation [06 §9.1] C18.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// (alive&~0x4000) [P1-07 §2.6] [06 §9.1]. Kinds 1/2/0xA/0xB are the damage kinds
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func ValidatePacketTarget(victim pool.Handle, isAlive func(pool.Handle) bool, isDeadLatch func(pool.Handle) bool) bool {
	if victim == 0 {
		return false // 0=null [06 §9.1] C18
	}
	if isAlive != nil && !isAlive(victim) {
		return false // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
	if isDeadLatch != nil && isDeadLatch(victim) {
		return false // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	}
	return true
}

// ValidatePacketKind reports whether kind is admissible for the damage intake
// per [P1-07 §2.6] — damage kinds 1/2/0xA/0xB are the packet handlers at
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// This separates the two enums that share the packet byte position [06 §12.1].
func ValidatePacketKind(kind uint8) bool {
	return IsDamagePacketKind(kind) // [P1-07 §2.6] 1,2,10,11
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
	// StopCurrentOrder clears the damaged unit's current order through the
	// ordinary stop path, the throttle's second half [08 R-AI-01 §11].
	StopCurrentOrder func(victim *units.Unit, tick uint32)
	// RetaliationOrder is the retaliation's order branch: the shared auto-engage
	// issuer with force = 0, behind the front-order and category admissions
	// [08 R-AI-01 §11][04 R-STANCE-01 §3]. It reports whether a record was
	// inserted; the per-slot offer runs only when it was not.
	RetaliationOrder func(victim, attacker *units.Unit) bool
	// SlotAcquisitionAdmits is the §3.1 acquisition physical gate for one of the
	// victim's weapon slots against one candidate. The damage path does not
	// carry the visibility, terrain, ledger and catalog operands that gate
	// needs, so the session supplies it bound to them [06 §3.1].
	SlotAcquisitionAdmits func(victim *units.Unit, slotIdx int, candidate *units.Unit) bool
	// UnderAttackSilenced reads bit 7 of the gate-mask word of the victim's
	// front primary order [06 R-WPN-04 §2 part 4].
	UnderAttackSilenced func(victim *units.Unit) bool
	// UnderAttackNotice requests the interface message of kind 2. The helper
	// applies its own selection/ownership/liveness gates [06 R-WPN-04 §2 part 4].
	UnderAttackNotice func(victim *units.Unit)
}

// The slot autonomy bit is units.OrderControlInhibit. [04 R-UNIT-06 §5 part 3]
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
// tick + 30 + draw, then clears the damaged unit's current order through the
// ordinary stop path. It is computer-players-only, and the draw is the only
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
	if r.StopCurrentOrder != nil {
		r.StopCurrentOrder(victim, tick)
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
	if r.RetaliationOrder != nil && r.RetaliationOrder(victim, attacker) {
		return // an order was issued; the offer does not also run [08 R-AI-01 §11]
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
		if slot == nil || !slot.IsPopulated() {
			continue
		}
		// "for each slot whose armed and tracking bits are set" — the tracking
		// half is the control byte's autonomy bit [04 R-UNIT-06 §5 part 3], and
		// the test is load bearing: a slot an attack order currently holds must
		// not be handed the attacker, and becomes eligible again the moment the
		// record destructor returns it. It was skipped while §1 left the bit's
		// writers open; §5 closes them.
		if slot.OrderControl&units.OrderControlInhibit == 0 {
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

// ApplyHealing performs the early healing path [06 §9.1]: adds packet's
// unsigned 16-bit amount to signed current health in 32-bit arithmetic,
// compares against maximum health as unsigned, clamps when required. Healing
// produces no flash/reaction/attacker assignment/callbacks [06 §9.1].
func ApplyHealing(currentHealth int32, maxHealth int32, amount uint16) int32 {
	// Add unsigned 16-bit amount to signed current health in 32-bit arithmetic [06 §9.1].
	newHealth := int32(int64(currentHealth) + int64(amount)) // 32-bit add
	// Compare against maximum health as unsigned, clamp when required [06 §9.1].
	if uint32(newHealth) > uint32(maxHealth) {
		newHealth = maxHealth
	}
	return newHealth
}

// DispatchHealingPacket is the central kind-10 intake. It resolves the victim
// from the authoritative world and applies the same alive/death-latch gates as
// ordinary damage intake before applying early healing [06 §9.1].
func (s *Service) DispatchHealingPacket(w *units.World, packet Packet) bool {
	if s == nil || w == nil || !ValidatePacketKind(packet.Kind) || packet.Victim == 0 {
		return false
	}
	victim := w.Unit(pool.Handle(packet.Victim))
	if victim == nil || !victim.Alive || victim.Dying {
		return false
	}
	switch packet.Kind {
	case KindHeal:
		if victim.Def == nil {
			return false
		}
		victim.Health = ApplyHealing(victim.Health, victim.Def.MaxDamage, packet.Amount)
		return true
	default:
		return false
	}
}

// ApplySelfDestructDamage sends the fixed self-damage packet through the
// ordinary health/reaction funnel. The packet's 30000 amount is at the strict
// armor-bypass boundary; defender veterancy still applies, and a lethal result
// is marked with cause 3 so the normal death finalizer performs callbacks and
// death effects [08 R-SKIR-01 §3][06 §9.1][06 §12.1].
func (s *Service) ApplySelfDestructDamage(w *units.World, target pool.Handle, tick uint32) bool {
	if s == nil || w == nil || target == 0 {
		return false
	}
	victim := w.Unit(target)
	if victim == nil || !victim.Alive || victim.Dying {
		return false
	}
	// The armor gate reads the runtime posture only [06 R-DMG-01 §8]; the FBI
	// `armoredstate` flag has no reader in retail. (At 30000 the strict
	// `amount < 30000` guard closes the gate anyway, but the operand must
	// still be the right one.)
	armored := UnitArmored(victim)
	damageModifier := int32(65536)
	if victim.Def != nil {
		damageModifier = victim.Def.DamageModifier
	}
	amount := ComputeScaledAmount(30000, 1, victim.Kills, victim.Kills, armored, damageModifier, false, false, false)
	victim.LastDamageSide = victim.Owner
	victim.LastDamageCause = uint8(CauseSelfDestruct)
	victim.Health = ApplyDamage(victim.Health, amount)
	packet := &Projectile{Shooter: target}
	if victim.Health <= 0 {
		w.Destroy(target, units.DeathSelfDestruct)
		if s.deathNotified == nil {
			s.deathNotified = make(map[pool.Handle]*units.Unit)
		}
		if s.deathNotified[target] != victim {
			s.deathNotified[target] = victim
			s.emitEvent(Event{Kind: EventUnitKilled, Tick: tick, Source: target, Target: target, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}})
		}
		return true
	}
	if bridge := s.callbackBridgeForUnit(victim); bridge != nil {
		bridge.HitByWeapon(uint8(packet.Yaw.Raw() >> 8))
		bridge.TakeDamage(cob.HealthPercent(victim.Health, victim.MaxHealth))
	}
	return true
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

// HealthPercentWithFault computes health*100/maxHealth with retail DIV fault
// on zero max [P1-07 §2.7] [06 §9.1]. Stock MaxDamage is always >0, so the fault
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// the pool record already incremented (if ballistic) — count not rolled back
// [P1-07 §4][GAP T5] I11. We reproduce as panic (Go divide fault).
func HealthPercentWithFault(health, maxHealth int32) int32 {
	if maxHealth == 0 {
		panic("combat: zero maxHealth divide fault [P1-07 §2.7][GAP T5]") // retail #DE [P1-07 §4]
	}
	v := (int64(health) * 100) / int64(maxHealth) // unsigned DIV for positive domain [04 §5.1]
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return int32(v)
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
// TickWaterDamage is the deterministic per-tick applicator (I1).

// IsWaterDamageTick reports whether global tick is a water-damage tick [04 §9.2].
func IsWaterDamageTick(tick uint32) bool {
	return tick%30 == 0 // [04 §9.2] once per tick when globalTick % 30 == 0
}

// IsInWaterForDamage reports whether Y is at or below sea-level byte [04 §9.2].
// Signed integer height versus sea-level byte. Uses trunc towards zero via Fixed.Int() [01 §8] (I3).
// TODO(question): signed integer height conversion uses trunc toward zero vs floor unresolved; using trunc via Int() as the __ftol path [01 §8]. Which integer width retail uses (high word of 16.16) matches trunc for positive heights and differs for negative fractions; not observable on stock maps with non-negative sea-level byte.
func IsInWaterForDamage(y numeric.Fixed, seaLevel uint8) bool {
	return int32(y.Int()) <= int32(seaLevel) // [04 §9.2] signed integer height at or below sea-level byte
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
		// TODO(question): sea-level source when terrain unavailable. Placeholder: treat as not in water when terrain nil, so caller must supply terrain for water test. Deterministic, no map range.
		return false
	}
	return IsInWaterForDamage(u.Y, terrain.SeaLevel) // [04 §9.2] signed integer height at or below sea-level byte
}

// ComputeWaterDamageScaledAmount computes the scaled water-damage amount for
// victim per [04 §9.2][06 §9.2] C20.
// It is ComputeScaledAmount with falloff 1.0 (non-AOE multiplier [04 §9.2]),
// attacker 0 (null), and non-heal path so armor/veteran (defender) reductions apply.
// isArmored is the victim's runtime armored posture — bit 1 of its first state
// byte, nothing else [06 R-DMG-01 §8]; damageModifier is def.DamageModifier
// 16.16; defenderKills is the victim's credited-kill counter.
func ComputeWaterDamageScaledAmount(baseDamage int32, defenderKills int32, isArmored bool, damageModifier int32) uint16 {
	return ComputeScaledAmount(baseDamage, float32(1), 0, defenderKills, isArmored, damageModifier, false, false, false) // [04 §9.2] multiplier 1.0 for non-AOE, [06 §9.2] steps 5-6 defender vet reduction
}

// WaterDamagePacketForTest builds the would-be water-damage packet for inspection per [04 §9.2][06 §9.1].
// It is kind 0xB (KindNoReaction, 11) with null attacker (0) and the scaled amount from ComputeWaterDamageScaledAmount [04 §9.2][06 §9.1].
func WaterDamagePacketForTest(victim pool.Handle, baseDamage int32, defenderKills int32, isArmored bool, damageModifier int32) Packet {
	amt := ComputeWaterDamageScaledAmount(baseDamage, defenderKills, isArmored, damageModifier) // [04 §9.2][06 §9.2]
	return Packet{
		Victim:    uint16(victim), // 0=null sentinel not used here [06 §9.1]
		Attacker:  0,              // null attacker [04 §9.2][06 §12.1] cause 11 attacker null
		Amount:    amt,            // modulo amount [06 §9.2] step 7
		Direction: 0,              // no direction for water damage [04 §9.2] (blast distance zero)
		Kind:      KindNoReaction, // type 0xB [04 §9.2][06 §9.1] == 11 skip-reaction [06 §9.1]
	}
}

// TickWaterDamage applies retail water damage for one global tick [04 §9.2][06 §12.1] cause 11.
// It is the authoritative per-tick water-damage sweep evaluated per unit before movement [04 §9.2].
// Deterministic iteration: players 0..9 ascending, slots ascending within each player's slice (I1) [01 §4.4][04 §9.2].
// No map range, no float64 outside I2 allowlist, no time.Now, no per-entity RNG (I1,I2,I4).
// Returns the number of units damaged this tick.
// getControlByte reads the owning player slot's control byte [05 R-SHARE-01 §1]:
// 1 a locally controlled human, 2 a computer player, 3 a remote peer, and
// ControlByteAbsent (0) for a slot with no record. The sweep's block runs only
// for 1 or 2 [04 R-MOV-03 §1][04 §9.2].
//
// Correction (2026-09-01, [06 R-DMG-01 §8]): this parameter was documented as
// "0 empty, 1 human host, 2 human join, 3 computer/AI" and the gate was skipped
// entirely when the accessor was nil. Both were wrong. 2 is the computer player
// and 3 the remote peer, so the old table exempted every computer-owned unit
// from water damage; and a nil accessor now fails closed — an unoccupied row is
// not "eligible". (Unlike gate 1, which an unoccupied row PASSES
// [06 R-DMG-01 §9], this gate admits only 1 and 2, so the sweep's block is the
// one place where "no record" and "remote peer" behave the same.)
func TickWaterDamage(tick uint32, w *units.World, terrain *world.Terrain, waterDoesDamage, waterDamage int32, getControlByte func(owner uint8) uint8) int {
	if !IsWaterDamageTick(tick) { // [04 §9.2] cadence
		return 0
	}
	if waterDoesDamage == 0 || waterDamage == 0 { // [04 §9.2] both mission fields nonzero
		return 0
	}
	if w == nil {
		return 0
	}
	// Sea-level byte from terrain header [03 §2.2] C9, used via IsInWaterForDamage [04 §9.2].
	// If terrain nil, no unit can be determined in-water, so no damage (see IsWaterDamageEligible TODO).
	applied := 0
	// Deterministic traversal: players 0..9 ascending, slots ascending (I1) [01 §4.4][04 §9.2].
	// Use VisitActiveSlots which already enforces that order [01 §4.4].
	w.VisitActiveSlots(func(v units.SlotVisit) {
		u := v.Unit
		if u == nil {
			return
		}
		// Control-byte gate [04 R-MOV-03 §1][04 §9.2][06 R-DMG-01 §8] — the
		// sweep's block runs only for an owner whose control byte is 1 or 2.
		// An absent accessor reads every slot as having no record, which
		// rejects: the byte must be read, never assumed.
		ctrl := ControlByteAbsent
		if getControlByte != nil {
			ctrl = getControlByte(u.Owner)
		}
		if ctrl != ControlByteHuman && ctrl != ControlByteComputer {
			return // [04 §9.2] control byte 1 or 2 only
		}
		if !IsWaterDamageEligible(u, terrain) { // [04 §9.2] canhover exclusion and Y <= seaLevel
			return
		}
		// Funnel arithmetic per [04 §9.2][06 §9.2] C20 via ComputeScaledAmount with falloff 1.0 [04 §9.2] non-AOE multiplier.
		// The armor gate is bit 1 of the victim's first runtime state byte —
		// the COB port 20 posture — and nothing else [06 R-DMG-01 §8]. The
		// definition's `armoredstate` flag has no reader in retail.
		isArmored := UnitArmored(u)
		damageMod := int32(65536) // 1.0 [02 "Unit record"] default
		if u.Def != nil {
			damageMod = u.Def.DamageModifier
		}
		scaled := ComputeWaterDamageScaledAmount(waterDamage, u.Kills, isArmored, damageMod) // [04 §9.2][06 §9.2] veteran victim REDUCED
		// Apply through standard damage funnel without callbacks [04 §9.2] type 0xB skips HitByWeapon/TakeDamage and feature effects.
		// Use 16-bit modular subtraction per [06 §9.1] ApplyDamage, then death latch via world.Destroy for lethal [04 §9.2][06 §12.1] cause 11 normal death-pending.
		// No packet construction needed for health path, but kind is 11 for citation.
		_ = KindNoReaction // 0xB [04 §9.2][06 §9.1]
		u.LastDamageCause = uint8(CauseWaterDamage)
		// Water has no attacking unit; its side byte remains the prior known
		// source. Cause 11 is sufficient to clear the repair-patrol cause-5 gate.
		newHealth := ApplyDamage(u.Health, scaled) // [06 §9.1] exact 16-bit modular subtraction
		u.Health = newHealth
		if newHealth <= 0 {
			// Lethal 0xB sets normal death-pending state [04 §9.2][06 §12.1] — same latch as ordinary, no callbacks.
			// Use the generic DeathKilled cause for units layer (cause 11 maps to that latch in combat death.go CauseWaterDamage).
			w.Destroy(u.Handle, units.DeathKilled) // [04 §2.4] marks Dying, firing OnDeath exactly once; slot freed at FinalizeDeath
		}
		applied++
	})
	return applied
}
