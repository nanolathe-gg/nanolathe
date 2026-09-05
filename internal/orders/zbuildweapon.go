// The BuildWeapon stockpile handler [06 §11].

package orders

import (
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/units"
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
	// Otherwise keep node alive with custom retry deadline (5/10/300) per
	// [06 §11.1]. TickStockpile returns tick+retry; store it as absolute tick.
	// EVERY hold arms a deadline first: a hold returned with a clear gate would
	// leave the pump re-dispatching this head for the rest of the visit
	// [06 R-WPN-05 §2], which is why the fall-through below arms one too.
	if nextTick != 0 {
		n.Deadline = int32(nextTick)
		n.DynamicGate = 1
		// Return 2 to keep node with our custom deadline (code 3 would overwrite
		// with tick+30+rand15, which is not the stockpile cadence). Pump's
		// case 2 just keeps the node alive with existing DynamicGate/Deadline.
		return Code(2)
	}
	// No nextTick but count remains: keep node, schedule default 5-tick retry
	// so the pump does not spin. Use 5 as accepted-incomplete boundary.
	n.Deadline = int32(tick + combat.StockpileRetryAccepted)
	n.DynamicGate = 1
	return Code(2)
}

// StockpileCounts returns the UI-visible stockpile state for a unit: the
// completed-round byte per slot and the queued remaining count from the
// secondary BuildWeapon node that targets that slot [06 §11.1] C29. When no
// stockpile weapon exists on the slot, ammo is zero and queued is zero. When
// the unit has no secondary queue, queued is zero. The first return is ammo
// per slot [NumSlots]int32, the second is queued count per slot.
func StockpileCounts(u *units.Unit) ([units.NumSlots]int32, [units.NumSlots]int32) {
	var ammo [units.NumSlots]int32
	var queued [units.NumSlots]int32
	if u == nil {
		return ammo, queued
	}
	for i := 0; i < units.NumSlots; i++ {
		if s := u.SlotAt(i); s != nil {
			ammo[i] = s.Ammo // slot's byte-sized completed-round remainder [06 §1.2] [06 §11.1]
		}
	}
	q := QueueForUnit(u)
	if q == nil {
		return ammo, queued
	}
	for _, n := range q.Secondary() {
		if n == nil {
			continue
		}
		if DescriptorFor(n.ID).Name != "BuildWeapon" {
			continue
		}
		// The node's slot index is the node's own, verbatim, exactly as the
		// handler reads it [06 R-WPN-05 §2]. The search that used to stand here
		// mirrored the handler's invented fallback and reported a count under a
		// slot the producer never named; an out-of-range index belongs to no
		// slot and is shown under none.
		idx := int(n.Param1)
		if idx >= 0 && idx < units.NumSlots {
			queued[idx] += int32(n.Param2)
		}
	}
	return ammo, queued
}

// ensure BuildWeapon handler is registered after the descriptor table is built.
func init() {
	if len(Table()) == 0 {
		return
	}
	id := Lookup("BuildWeapon")
	if id == 0 {
		return
	}
	// The table is sorted; assign handler for BuildWeapon secondary queue.
	if int(id) < len(table) {
		table[int(id)].Handler = buildWeaponHandler
	}
}
