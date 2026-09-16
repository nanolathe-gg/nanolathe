// The BuildWeapon stockpile handler [06 §11].

package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// noWeaponRecord is weapon record 0, the `[noweapon]` sentinel every empty
// weapon slot points at [06 R-DMG-01 §5]: reload time 0, both per-shot costs 0,
// no `stockpile` flag. Nothing about it is authored — it is the zero record —
// so it is built here rather than looked up.
var noWeaponRecord content.WeaponDef

// StockpileSlotAcceptsBuildWeapon is the ENQUEUE guard for a BUILDWEAPON node
// [06 R-WPN-05 §2]. The handler itself has no such test, and on a slot holding
// weapon record 0 the queue runs away: every round completes free in a single
// visit, and if the node outlives the visit — a count large enough to reach the
// 199 gate — the build page's percentage `progress·100 / reloadtime` divides by
// zero with no guard, so hovering the unit faults.
//
// Refusing to enqueue such a node is the one behavior that is both safe and
// indistinguishable from retail on every shipped case: stock content puts a
// `MAKENUKE`/`MAKEANTI` button only on units whose named slot holds a
// `stockpile` weapon, so no shipped click can produce a node this refuses.
//
// The three producers — the HUD order alias, the mission `Bw` verb and the
// network decoders (§11.1) — are expected to gate on this before pushing.
func StockpileSlotAcceptsBuildWeapon(u *units.Unit, slotIdx int) bool {
	if u == nil || slotIdx < 0 || slotIdx >= units.NumSlots {
		return false
	}
	s := u.SlotAt(slotIdx)
	if s == nil || s.Weapon == nil {
		return false // weapon record 0: the unarmed slot
	}
	return s.Weapon.Stockpile
}

// BuildWeaponHandler is the secondary-queue handler for BUILDWEAPON per [06 §11]
// C29 and [04 §3.1] 0x40000. It advances the linked stockpile slot via
// combat.TickStockpile's contract, performing truncated cumulative cost deltas,
// retry scheduling, slot-byte capping at 200, and queue-count decrement. It is
// registered onto the sorted descriptor table in init() after the table is built.
//
// Encoding: Param1 SlotIdx, Param2 remainingCount, Param3 progress.
// Progress is per-node 0..BuildTime step 5 capped [06 §11.1]. Ammo is the
// slot's byte-sized completed-round remainder [06 §1.2] [06 §11.1].
//
// Launch-before-production is preserved by phase order: weapon firing
// (PhaseUnitsScripts) runs before the orders pump (PhaseOrdersPathEconomy),
// so a round completed here cannot launch until the next tick [06 §11.1] C29.
func buildWeaponHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	_ = satisfied
	if u == nil || n == nil {
		return Code(5)
	}
	// The node's slot index is used VERBATIM to select the weapon slot; the
	// handler tests neither that the slot's weapon carries `stockpile` nor that
	// it is a real weapon [06 R-WPN-05 §2]. The slot search this used to do —
	// "no stockpile weapon at Param1, so find the first slot that has one" —
	// and its 30-tick wait were both invented; they moved a node onto a slot
	// the producer never named. Enqueue is where a bad node is refused; see
	// StockpileSlotAcceptsBuildWeapon.
	slotIdx := int(n.Param1)
	var slot *units.Slot
	if slotIdx >= 0 && slotIdx < units.NumSlots {
		slot = u.SlotAt(slotIdx)
	}
	if slot == nil {
		// Retail's own answer here is a build-type of three or greater indexing
		// past the unit record with no bounds check [06 §11.1] — undefined
		// memory, not a behavior to clone. The bounded stand-in is the handler's
		// own blocked cadence: hold with a deadline, which is what every other
		// hold in this body does, so the record neither spins the pump nor
		// corrupts a slot. Our enqueue guard refuses such a node in the first
		// place, so this is reachable only from a malformed save.
		n.DynamicGate = 1
		n.Deadline = int32(tick + combat.StockpileRetryBlocked)
		return Code(2)
	}
	// An unarmed slot points at weapon record 0, the `[noweapon]` sentinel:
	// reload 0, both per-shot costs 0, never a null [06 R-WPN-05 §2]
	// [06 R-DMG-01 §5]. Our slots carry a nil weapon pointer for that, so the
	// sentinel is supplied here rather than special-cased downstream — its
	// rounds then complete free in one visit, which is what retail does.
	weapon := slot.Weapon
	if weapon == nil {
		weapon = &noWeaponRecord
	}
	// Count and progress from node.
	count := int32(n.Param2)
	progress := int32(n.Param3)
	if count <= 0 {
		// No remaining requested count: remove node [06 §11.1].
		return Code(5)
	}
	// Bridge to combat helper: combat.StockpileEntry + combat.Slot are the
	// canonical logic owners per I5; we translate the units.Slot's Ammo field
	// into a combat.Slot, run TickStockpile, then copy back.
	cs := &combat.Slot{Weapon: weapon, Ammo: slot.Ammo}
	ce := &combat.StockpileEntry{Weapon: weapon, Count: count, Progress: progress, SlotIdx: int32(slotIdx)}
	// Admit func: record via economy.UnitBuckets and return true if both
	// carries non-positive (accepted), false if rejected, but always record
	// requested amounts so the two-stage settlement retains fractional carry
	// across cancels [05][06 §11.1][P1-09 §4]. Per-queue economy only [RS-P0-018][INVARIANTS I1].
	admit := func(e, m float32) bool {
		q := QueueForUnit(u)
		if q == nil {
			return false
		}
		binding := q.Binding()
		if binding == nil || binding.Economy == nil {
			// Stockpile work is admitted only through the owning session's
			// economy ledger. An unbound queue cannot make progress: silently
			// treating it as an accepted request bypasses two-resource carry
			// admission and changes the retail queue lifecycle [05 "Two-stage
			// settlement algorithm"][06 §11.1].
			return false
		}
		buckets := binding.Economy.UnitBuckets(u.Handle)
		if buckets == nil {
			return false
		}
		// Snapshot carries before admission to decide accepted vs rejected.
		energyCarry := (*buckets)[economy.Energy].Carry
		metalCarry := (*buckets)[economy.Metal].Carry
		wasAccepted := energyCarry <= 0 && metalCarry <= 0
		economy.AdmitTwoResource(buckets, e, m)
		return wasAccepted
	}
	nextTick, _, completedRounds := combat.TickStockpile(ce, cs, tick, admit)
	// Copy the byte back. It neither underflows nor passes 200 through the
	// engine's own paths [06 R-WPN-05 §2], so there is nothing to clamp here.
	slot.Ammo = cs.Ammo
	n.Param2 = uint32(ce.Count)
	n.Param3 = uint32(ce.Progress)
	_ = completedRounds
	// If all queued counts consumed, unlink the node.
	if ce.Count <= 0 {
		return Code(5)
	}
	// With work remaining, the helper has reached an actual hold: rejected
	// admission, accepted incomplete work, or the full-slot gate. Completion
	// itself restarts without a delay [06 R-WPN-05 §2]. Preserve the absolute
	// deadline even when it wraps to zero [04 §3.3].
	n.Deadline = int32(nextTick)
	n.DynamicGate = 1
	return Code(2) // hold preserves this deadline; code 3 would replace it
}

// There is no stockpile census here. The completed-round byte lives on the
// slot and the outstanding count lives on the secondary `BuildWeapon` node's
// own operands [06 §11.1] C29; a reader wanting the pair reads those two
// places, which is what the publication boundary does, rather than a second
// walk that could disagree with the handler about which slot a node names.

// ensure BuildWeapon handler is registered after the descriptor table is built.
func init() {
	if len(Table()) == 0 {
		return
	}
	id := rowBuildWeapon
	if id == 0 {
		return
	}
	// The table is sorted; assign handler for BuildWeapon secondary queue.
	if int(id) < len(table) {
		table[int(id)].Handler = buildWeaponHandler
	}
}
