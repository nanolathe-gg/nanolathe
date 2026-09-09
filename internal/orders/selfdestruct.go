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
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	// The decimal prefix wraps before the three-bit store [04 R-SPEC-01 §13].
	v := formats.ParseTDFInteger(def.SelfDestructCountdown)
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
	applySelfDestructDamage(u, tick)
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

// applySelfDestructDamage sends the fixed kind-3 packet to the common intake.
// Attacker identity is self; only defender scaling applies [04 R-ORD-01 §14]
// [06 §9.2]. The intake owns flash, reaction, provenance and death admission.
func applySelfDestructDamage(u *units.Unit, tick uint32) {
	if u == nil {
		return
	}
	if binding := bindingFor(u); binding != nil && binding.Damage != nil {
		binding.Damage(tick, combat.DamageInput{Victim: u.Handle, Attacker: u.Handle, Nominal: selfDestructDamage, Kind: uint8(combat.CauseSelfDestruct)})
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
