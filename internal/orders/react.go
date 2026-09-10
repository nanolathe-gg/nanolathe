package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The order-side seam of the damage-intake reaction routine [06 §9.1] step 4,
// closed at [06 R-WPN-04 §2]. The routine itself lives in internal/combat,
// which cannot import this package (internal/orders imports internal/combat),
// so the session binds these four functions onto the combat service at
// composition and the routine calls them through function values.
//
// Nothing in this file is called from inside internal/orders. Each function is
// one part of the routine and owns only what an order record can answer.

// observerNotice is the event code the damage-intake observer notice delivers
// [06 R-WPN-04 §2 part 1]. Every observer notification calls the first entry of
// the listed node's handler with an event code, and that entry ORs its argument
// into the record's pending word, so an event code IS a pending bit: unit
// removal delivers 0x8, cloak 0x10000, and the damage-intake notice 0x10 — the
// producer [04 R-ORD-01 §6] could not locate, closed by [04 R-MOV-03 §7].
const observerNotice uint32 = 0x10

// staticSlotKeeper is bit 16 of a descriptor's static gate mask: a record
// carrying it is exempt from the record destructor's "return all three slots"
// step [04 R-UNIT-06 §5 part 3]. The rows that carry it in this build's table
// are the two activation rows, the two cloak toggles, the two standing-order
// rows and `BuildingBuild` — none of which ever takes a weapon slot.
const staticSlotKeeper uint32 = 1 << 16

// staticTargetObserver is static gate-mask bit 9. A record constructed without
// a target unit has it cleared, and the record constructor unlinks the observer
// node again when it is clear, so a record whose descriptor does not carry
// 0x200 never observes its target [04 §3.1][04 R-MOV-03 §7].
const staticTargetObserver uint32 = 0x200

// staticUnderAttackSilent is static gate-mask bit 7. The damage dispatcher's
// under-attack notice reads it on the victim's FRONT PRIMARY order and stays
// silent while it is set; the three descriptors that carry it statically are
// `Attack_NoMove`, `Attack_Chase` and `AttackSpecial`, so a unit that is
// already attacking never announces `Under Attack`
// [04 §3.1 "Retained-opaque static gate-mask bits"][06 R-WPN-04 §2 part 4].
const staticUnderAttackSilent uint32 = 0x80

// ObserverNotice is part 1 of the reaction routine: the victim's observer list
// is walked from its head and each node carrying a handler receives event code
// 16 [06 R-WPN-04 §2 part 1]. Order tasks install themselves as the handler and
// every task type handles the code identically — the handler ORs it into the
// pending word — so the whole notice is "every order record observing the
// victim wakes with pending bit 0x10" [04 R-MOV-03 §7].
//
// Nanolathe keeps no per-unit observer list: the record's observed unit is its
// Target and the pending word is the OWNING unit's (the pump reads
// `(record.Satisfied | owner.Pending) & record.DynamicGate`, [04 §3.3]), so the
// same set is reached by walking the world in pool order (I1) and testing each
// record's target. Records whose descriptor lacks the target-observer bit are
// skipped, which is the "never observes its target" clause above.
func ObserverNotice(w *units.World, victim *units.Unit) {
	if w == nil || victim == nil || victim.Handle == 0 {
		return
	}
	for _, u := range w.Iter() { // pool slot ascending (I1)
		if u == nil || !u.Alive {
			continue
		}
		q := QueueOfUnit(u)
		if q == nil {
			continue
		}
		if queueObserves(q.Primary(), victim.Handle) || queueObserves(q.Secondary(), victim.Handle) {
			u.Pending |= observerNotice
		}
	}
}

// queueObserves reports whether any record of one segment observes victim.
func queueObserves(segment []*Node, victim pool.Handle) bool {
	for _, n := range segment {
		if n == nil {
			continue
		}
		if n.Target != victim {
			continue
		}
		if n.StaticGate&staticTargetObserver == 0 {
			continue // the node was never linked onto the target [04 R-MOV-03 §7]
		}
		return true
	}
	return false
}

// FrontPrimaryGateMask returns the static gate-mask word of a unit's front
// primary order, and zero when it has none — the read part 4 of the reaction
// routine makes before it decides whether to request the `Under Attack`
// message [06 R-WPN-04 §2 part 4].
func FrontPrimaryGateMask(u *units.Unit) uint32 {
	q := QueueOfUnit(u)
	if q == nil {
		return 0
	}
	primary := q.Primary()
	if len(primary) == 0 || primary[0] == nil {
		return 0
	}
	return primary[0].StaticGate
}

// UnderAttackSilenced reports whether the victim's front primary order silences
// the under-attack notice: bit 7 of that record's gate mask
// [06 R-WPN-04 §2 part 4].
func UnderAttackSilenced(u *units.Unit) bool {
	return FrontPrimaryGateMask(u)&staticUnderAttackSilent != 0
}

// staticStandbyInterruptible is static gate-mask bit 17 (0x20000), the
// "interruptible bit" of [08 R-AI-01 §11] and [04 R-STANCE-01 §3], numbered
// by [04 §3.1] (RWU-19-39). Exactly three descriptors carry it — `Standby`,
// `Standby_Mine` and `VTOL_Standby` — and its only reader is the reaction
// site below, which tests it on the victim's head order's static-mask copy.
// It is a descriptor property, not per-record state: "interruptible" means
// "standing by".
const staticStandbyInterruptible uint32 = 1 << 17

// RetaliationOrder is part 3's ORDER branch — the first bullet of
// [08 R-AI-01 §11], restated in stance terms at [04 R-STANCE-01 §3]: when the
// victim has no current order, or its current order's static gate-mask copy
// carries bit 17 (the standby interruptible bit [04 §3.1]), and the attacker's
// type is absent from both the victim definition's no-chase bitset and its
// primary-slot bad-target bitset [06 §3.2], the victim is given an attack order
// against the attacker through the ordinary order service. That service is the
// shared auto-engage issuer with `force = 0`, so a hold-fire or hold-position
// victim gets no counter-order.
//
// Retail evaluates one more admission between the category test and the
// issuer: the slot admission predicate for slot 0 against the attacker
// [08 R-AI-01 §11]. That predicate needs the world, visibility and terrain
// services (combat.SlotAcquisitionAdmits) which this order-side seam does not
// hold, so the combat-side reaction routine that binds this function owns it
// and now applies it immediately before this call (WU-19-128). All three
// admissions are pure predicates ANDed together, so evaluating that one first
// is observationally identical to retail's order.
//
// It reports whether a record was inserted; the caller falls through to the
// per-slot offer when it did not.
func RetaliationOrder(victim, attacker *units.Unit) bool {
	if victim == nil || attacker == nil || victim == attacker {
		return false
	}
	q := QueueOfUnit(victim)
	if q == nil {
		return false
	}
	if primary := q.Primary(); len(primary) != 0 {
		// The head order must be a standby to be interrupted [04 §3.1]; a
		// unit moving, patrolling, building or attacking is only ever
		// offered the attacker slot by slot.
		if primary[0] == nil || primary[0].StaticGate&staticStandbyInterruptible == 0 {
			return false
		}
	}
	if !categoryAdmitsChase(victim.Def, attacker.Def) {
		return false
	}
	return autoEngage(victim, attacker, false) // the retaliation site passes force = 0 [04 R-STANCE-01 §3]
}

// categoryAdmitsChase is the second half of the order branch's admission: the
// attacker's type must be absent from BOTH the victim definition's no-chase
// bitset and its PRIMARY slot's bad-target bitset (`wpri_badTargetCategory`)
// [08 R-AI-01 §11][06 §3.2]. Of the three per-slot bad-target bitsets the
// definition authors, the order branch reads only slot 0's (RWU-19-39); the
// per-slot offer reads each slot's own bitset, but only against the slot's
// existing target, never against the attacker.
func categoryAdmitsChase(victim, attacker *content.UnitDef) bool {
	if victim == nil || attacker == nil {
		return false
	}
	cat := attacker.DefinitionMask()
	if cat.Intersects(victim.NoChaseCategoryMask) {
		return false
	}
	return !cat.Intersects(victim.BadTargetCategoryWPRIMask)
}

// StopCurrentOrder is the second half of the construction throttle of
// [08 R-AI-01 §11]: after writing the owning player's manager throttle
// deadline, the executable "clears the damaged unit's current order through the
// ordinary stop path". That path is the `Stop` command's ordinary issue — purge
// the unprotected records, drop the leading auto operations, then push the
// `Stop` record, whose own row clears the three weapon-slot targets and lands a
// stopped aircraft [04 R-ORD-01 §2].
func StopCurrentOrder(u *units.Unit, tick uint32) {
	if u == nil {
		return
	}
	id := rowStop
	if id == 0 {
		return
	}
	q := QueueOfUnit(u)
	if q == nil {
		return
	}
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	q.Push(id, NewNodeForOrder(id, 0, 0, 0, 0, tick, u.Handle, false))
}
