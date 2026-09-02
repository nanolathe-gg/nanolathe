package orders

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
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

// RetaliationOrder is part 3's ORDER branch — the first bullet of
// [08 R-AI-01 §11], restated in stance terms at [04 R-STANCE-01 §3]: when the
// victim has no current order (or its current order's gate mask carries the
// interruptible bit) and the attacker's type is absent from both the victim
// definition's no-chase and bad-target category bitsets [06 §3.2], the victim
// is given an attack order against the attacker through the ordinary order
// service. That service is the shared auto-engage issuer with `force = 0`, so
// a hold-fire or hold-position victim gets no counter-order.
//
// It reports whether a record was inserted; the caller falls through to the
// per-slot offer when it did not.
//
// TODO(question): the "interruptible bit" of [08 R-AI-01 §11] and
// [04 R-STANCE-01 §3] is named in both sections but numbered in neither, and
// [04 §3.1]'s static-mask census lists no bit with that meaning among the ones
// it has located (bit 2 purge-survivor, bit 7 under-attack silence, 9 target,
// 10 goal, 18 rear segment, 20 build-site class; bits 1, 3-6, 8, 11, 16, 17, 19
// and 24 have no located consumer). Placeholder: only the "no current order"
// arm admits, which never issues a counter-order the retail engine would not
// also issue, and leaves the per-slot offer — the branch that produces return
// fire for a unit sitting in `Standby` — as the reachable one. Decider: a trace
// of the reaction site's gate-mask test naming which static bit it reads.
func RetaliationOrder(victim, attacker *units.Unit) bool {
	if victim == nil || attacker == nil || victim == attacker {
		return false
	}
	if q := QueueOfUnit(victim); q == nil || len(q.Primary()) != 0 {
		return false // no front order is the only admitted arm; see TODO above
	}
	if !categoryAdmitsChase(victim.Def, attacker.Def) {
		return false
	}
	return autoEngage(victim, attacker)
}

// categoryAdmitsChase is the second half of the order branch's admission: the
// attacker's type must be absent from BOTH the victim definition's no-chase and
// bad-target category bitsets [08 R-AI-01 §11][06 §3.2].
//
// TODO(question): [08 R-AI-01 §11] writes "bad-target category bitsets" in the
// singular sense of one per definition, but [02 "Unit record"] authors three —
// `wpri_`, `wsec_` and `wspe_badTargetCategory`, one per weapon slot — and no
// unit-level key. Placeholder: the primary slot's mask, because the order
// branch resolves command code 3, whose every weapon test reads weapon slot 0
// [04 R-ORD-02 §1]. Decider: a trace of which of the three masks the reaction
// site loads.
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
	id := Lookup("Stop")
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
