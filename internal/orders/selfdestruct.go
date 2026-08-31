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

import (
	"strconv"
	"strings"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

const (
	// selfDestructCountField is the remaining count's 3-bit field inside p2.
	// The authored `selfdestructcountdown` is a 3-bit field ([02 "Unit record"]:
	// absent -> 5, present -> its decimal value masked to 3 bits), and the row
	// stores the remaining count back into p2 on every step.
	selfDestructCountField uint32 = 0x7

	// selfDestructInitialised marks p2 as initialised. [04 R-ORD-01 §2] says
	// only that "p2's high nibble marks initialisation".
	//
	// TODO(question): the exact marker value is not established — the row names
	// the nibble, not the bit pattern. Any nonzero value in p2's high nibble is
	// behaviourally identical for this handler, because the only reads of p2 are
	// this marker test and the 3-bit count, so the low bit of that nibble is
	// used here. A trace of the handler's marker store and test would settle it.
	selfDestructInitialised uint32 = 1 << 28
	selfDestructMarkerMask  uint32 = 0xf << 28

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
	return uint32(v) & selfDestructCountField
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
//
// TODO(T25): the row's status emissions — kinds 17..22 (`five` .. `zero`) while
// counting and kind 23 (`Self destruct terminated`) on cancellation, the latter
// suppressed for a unit carrying auto flag 14 — have no surface in this
// package. The status emitter of [04 R-ORD-01 §1] belongs to the presentation
// side and this build reaches it only from the session, not from an order
// handler (the same gap `Stop`'s caption clear records). Placeholder: the
// simulation half of every step runs and the announcement is a no-op.
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
				n.Param2 = (n.Param2 &^ selfDestructCountField) | (count - 1)
			}
			if count == 0 {
				armDeadline(n, tick, drawBelow(u, 15)) // the one draw of the timeline [04 R-SPEC-01 §13]
			} else {
				armDeadline(n, tick, selfDestructStep)
			}
			n.DynamicGate |= gateCancelBit
			return Code(1) // *advance* [04 R-ORD-01 §2]
		}
		return Code(5) // cancelled: *complete* without damage [04 R-ORD-01 §2]
	}
	applySelfDestructDamage(u)
	return Code(5) // *complete* [04 R-ORD-01 §2]
}

// applySelfDestructDamage applies the row's 30000 damage to the unit itself
// through the standard damage funnel [04 R-SPEC-01 §1][06 §9.2]: the
// armored-state reduction is skipped because it applies only to amounts
// strictly below 30000, and the defender veterancy scale applies as for any
// packet, so the amount is 30000 at zero kills and never less than 24000 at the
// top kill tier. The attacker term is not taken — the row's arithmetic is the
// defender factor alone.
//
// The death latch is the two death fields the world's own destroy entry writes
// [04 §2.3][04 §2.4]: the unit stays visible to later phases and is retired by
// the next phase-2 slot finalizer, which fires the death hooks and resolves
// `selfdestructas` for cause 3 [R-DMG-01 §5]. A handler cannot reach the world
// to call that entry, and the entry writes nothing else.
//
// TODO(T25): the funnel's two global double/half gates [06 §9.2] step 4 are
// session state with no surface here. Placeholder: the ungated case, which is
// the only one a stock session runs.
func applySelfDestructDamage(u *units.Unit) {
	if u == nil || u.Def == nil {
		return
	}
	amount := combat.ComputeScaledAmount(
		selfDestructDamage,
		1, // no area falloff: the amount is applied directly to the unit itself
		0, // attacker veterancy is not taken [04 R-SPEC-01 §1]
		u.Kills,
		u.Def.ArmoredState,
		u.Def.DamageModifier,
		false, false, false,
	)
	u.Health = combat.ApplyDamage(u.Health, amount)
	if u.Health <= 0 && !u.Dying {
		u.Dying = true
		u.DeathCause = units.DeathSelfDestruct // damage cause 3 [06 §12.1]
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
