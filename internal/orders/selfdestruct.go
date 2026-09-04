package orders

// `SelfDestruct` and `SelfDestructFG` share one handler body
// [04 R-ORD-01 §2]; the two descriptors differ only in the segment they live
// on. `SelfDestruct` carries the rear-segment selection bit and therefore runs
// purely on its own deadline, with the secondary pump delivering an empty
// satisfied set [R-ORDER-02 §1]; `SelfDestructFG` — the `d` button's and the
// initial-mission interpreter's descriptor — sits on the front segment.
//
// The end-to-end timeline is [04 R-SPEC-01 §13]: visit 1 initialises the count
// from the definition and announces it, each further step falls by one every 30
// ticks, the `zero` step waits `RNG(15)`, and the visit after that applies
// 30000 self-damage with cause 3 and completes. Re-issuing the order while it
// counts sets the cancel-current bit; the next visit announces the termination
// and completes without damage.
//
// The two producers that spawn this record with p1 = 1 — `Attack_Kamikaze`'s
// arrival and `Standby_Mine`'s detonation — share combat.go's
// spawnImmediateSelfDestruct.
//
// Both spawn through the handler head insert, which goes to "the front of the
// segment the record's rear-segment flag selects" [04 R-ORD-01 §1], and
// `SelfDestruct`'s descriptor carries that flag (static bit 18, [04 §3.1]) —
// so the spawned record lands at the head of the REAR segment, which is what
// makes [04 R-ORD-01 §2]'s "the record lives on the rear segment" true for a
// spawned one as well as an issued one, and what keeps it from blocking the
// primary queue. `Queue.PushHead` inserts into the primary segment
// unconditionally, so the routing belongs at the spawn site: every handler
// spawn in this package goes through spawnAtSegmentHead (stop.go), which is the
// shape combat.go's self-destruct producer already inlines. `SelfDestructFG` —
// the `d` button's descriptor — carries no such flag and is issued, not
// spawned. A test in wu19168_test.go locks the segment.

import (
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

const (
	// selfDestructCountField is the remaining count's field inside p2. The
	// handler reads it as the low TWENTY-EIGHT bits — the whole word below the
	// marker nibble — not as the three bits the seed happens to fill
	// [04 R-ORD-01 §14]. The authored `selfdestructcountdown` is a 3-bit field
	// ([02 "Unit record"]: absent -> 5, present -> its decimal value masked to
	// 3 bits), so a stock session never sees a count above 7; the arithmetic is
	// still the row's.
	selfDestructCountField uint32 = 0x0fffffff

	// selfDestructInitialised is the marker the seed store ORs into p2 and the
	// value the initialisation test masks with: the WHOLE high nibble, all four
	// bits, never one of them [04 R-ORD-01 §14]. [04 R-ORD-01 §2] names the
	// nibble; §14 gives the pattern.
	selfDestructInitialised uint32 = 0xf0000000
	selfDestructMarkerMask  uint32 = 0xf0000000

	// selfDestructDefinitionField is the width of the DEFINITION's authored
	// countdown, which the seed store masks to three bits before ORing the
	// marker [02 "Unit record"][04 R-ORD-01 §14].
	selfDestructDefinitionField uint32 = 0x7

	// selfDestructDamage is the self-inflicted amount and selfDestructStep the
	// per-count wait, both from [04 R-ORD-01 §2] / [04 R-SPEC-01 §13].
	selfDestructDamage int32  = 30000
	selfDestructStep   uint32 = 30
)

// selfDestructCountdownField reads the definition's `selfdestructcountdown`
// [02 "Unit record"]: the key is read with the raw accessor so the record can
// tell an authored value from an absent one, an absent key yields 5, and an
// authored one yields its decimal value masked to three bits. There is no
// clamp: `0` stores as 0 (immediate), `8` as 0, `9` as 1, and 6 and 7 store as
// authored [04 R-SPEC-01 §13].
func selfDestructCountdownField(def *content.UnitDef) uint32 {
	if def == nil || !def.SelfDestructCountdownPresent {
		return 5
	}
	// A decimal accessor: a value that is not a number reads as zero.
	v, err := strconv.Atoi(strings.TrimSpace(def.SelfDestructCountdown))
	if err != nil {
		v = 0
	}
	return uint32(v) & selfDestructDefinitionField
}

// selfDestructHandler is the shared body of [04 R-ORD-01 §2]'s self-destruct
// row:
//
//	p2's high nibble marks initialisation: when clear, p2 = the definition's
//	selfdestructcountdown with the marker. If p1 = 0 and the countdown field is
//	nonzero: with the satisfied set lacking bit 1 (cancel-current), let n = the
//	remaining count; if n = 0 set p1 = 1 else store n - 1; emit status kind
//	22 - n; deadline RNG(15) when n was 0, else 30; gate |= 0x2; advance. With
//	bit 1 present (cancelled): if the unit lacks auto flag 14 emit status 23;
//	complete. Otherwise (countdown finished, or the definition has no
//	countdown): apply 30000 damage to itself with damage cause 3; complete.
//
// "The countdown field" in the branch test is the DEFINITION's field, not the
// remaining count: the counting arm is entered with a remaining count of zero
// on its last step, and [04 R-SPEC-01 §13] names the immediate case as "the
// field was 0 from the start".
//
// Gate bit 1 (value 2) has no bit writer at all — the cancel-current
// notification is delivered by the removal paths, which invoke the handler with
// mask 2 whenever the record being freed still has that bit armed
// [04 R-ORD-01 §0][R-ORDER-02 §2]. Arming it on every counting visit is what
// makes a re-issued or purged countdown announce its termination.
func selfDestructHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if u == nil || n == nil {
		return Code(5)
	}
	field := selfDestructCountdownField(u.Def)
	if n.Param2&selfDestructMarkerMask == 0 {
		n.Param2 = field | selfDestructInitialised
	}
	if n.Param1 == 0 && field != 0 {
		if satisfied&gateCancelBit == 0 {
			count := n.Param2 & selfDestructCountField
			if count == 0 {
				n.Param1 = 1
			} else {
				// The decrement REPLACES the word — count-1 with the marker
				// re-ORed — rather than merging into a narrow field
				// [04 R-ORD-01 §14].
				n.Param2 = (count - 1) | selfDestructInitialised
			}
			// The announce is a six-entry table of kinds {22..17} indexed by
			// the remaining count, which is 22 − count for every in-range one
			// [04 R-ORD-01 §14]. Counts 6 and 7 index past it — the Unknown
			// [04 R-ORD-01 §2] records.
			workStatus(u, uint8(22-count), "")
			if count == 0 {
				armDeadline(n, tick, drawBelow(u, 15)) // the one draw of the timeline [04 R-SPEC-01 §13]
			} else {
				armDeadline(n, tick, selfDestructStep)
			}
			n.DynamicGate |= gateCancelBit
			return Code(1) // *advance* [04 R-ORD-01 §2]
		}
		if u.Flags&0x4000 == 0 {
			workStatus(u, 23, "Self destruct terminated")
		}
		return Code(5) // cancelled: *complete* without damage [04 R-ORD-01 §2]
	}
	applySelfDestructDamage(u)
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

// applySelfDestructDamage applies the row's 30000 damage to the unit itself
// through the standard damage funnel [04 R-SPEC-01 §1][06 §9.2]: the
// armored-state reduction is skipped because it applies only to amounts
// strictly below 30000 — and the amount is still 30000 when step 5 tests it,
// because the defender veterancy of step 6 has not run yet — and the defender
// veterancy scale applies as for any
// packet, so the amount is 30000 at zero kills and never less than 24000 at the
// top kill tier. The attacker term is not taken — the row's arithmetic is the
// defender factor alone.
//
// The death latch is the three death fields the world's own destroy entry
// writes [04 §2.3][04 §2.4][04 R-UNIT-06 §5]: the unit stays visible to later
// phases and is retired by the next phase-2 slot finalizer, which fires the
// death hooks and resolves `selfdestructas` for cause 3 [R-DMG-01 §5]. A
// handler cannot reach the world to call that entry, so it calls that entry's
// own field writer, `units.MarkDeath`; the two must not drift, and before
// WU-19-49 this site latched two of the three fields.
//
// The third is the recorded-attacker link, whose death row takes the death
// packet's attacker [04 R-UNIT-06 §5]. Cause 3 applies its damage "to the unit
// itself", so that attacker is this unit and the link ends as its own handle —
// not as whoever shot it before it was told to self-destruct.
//
// The funnel's two global double/half gates [06 §9.2] are not on this path,
// which is why nothing here consults them. The row enters at the PACKET BUILDER
// — attacker and victim both this unit, amount 30000, kind 3 [04 R-ORD-01 §14]
// [06 R-WPN-05 §11] — and §9.2 applies those two bits in the per-recipient
// routine ABOVE the builder, alongside the area falloff and the attacker
// veterancy this row equally does not take. §9.2's own whole-image census
// independently finds no writer for either bit, so both are stock-inert as
// well; either fact alone settles the site.
func applySelfDestructDamage(u *units.Unit) {
	if u == nil || u.Def == nil {
		return
	}
	amount := combat.ComputeScaledAmount(
		selfDestructDamage,
		1, // no area falloff: the amount is applied directly to the unit itself
		0, // attacker veterancy is not taken [04 R-SPEC-01 §1]
		u.Kills,
		// The armor gate's operand is the RUNTIME armored posture — bit 1 of
		// the unit's first state byte, the `set ARMORED` port — never the FBI
		// `armoredstate` key, which is parsed into a definition flag no retail
		// path reads [06 R-DMG-01 §8]. Passing the definition flag here made
		// every unit authored `armoredstate=1` permanently armored at this
		// call site. It is inert at amount 30000, but it was the wrong operand.
		combat.UnitArmored(u),
		u.Def.DamageModifier,
		false, false, false,
	)
	u.LastDamageSide = u.Owner
	u.LastDamageCause = uint8(combat.CauseSelfDestruct)
	u.Health = combat.ApplyDamage(u.Health, amount)
	if u.Health <= 0 && !u.Dying {
		units.MarkDeath(u, units.DeathSelfDestruct, u.Handle) // damage cause 3 [06 §12.1]
	}
}

// ensureSelfDestructHandlers installs the pair onto the descriptor table. It is
// idempotent and tolerates an unbuilt table, like every installer in
// handlerInstallers.
func ensureSelfDestructHandlers() {
	if len(table) == 0 {
		return
	}
	for _, name := range []string{"SelfDestruct", "SelfDestructFG"} {
		id := Lookup(name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = selfDestructHandler
		}
	}
}

func init() { ensureSelfDestructHandlers() }
