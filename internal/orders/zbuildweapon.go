// Package orders — BuildWeapon stockpile handler per [06 §11] C29.
//
// WU-09-9 owns this file. It connects the secondary BUILDWEAPON queue nodes to
// slot state, resource admission, UI counts, launch-before-production ordering,
// and save state. The stockpile weapon builds as a stockpiled missile: cost is
// paid over time via TickStockpile's truncated cumulative deltas, count increments
// on completion, launch consumes one stock, and interceptor reserves incoming
// nuke via the combat stockpile helpers.
//
// Queue encoding on Node (86-byte retail identity [04 §3.2] I13, [06 §11.1]):
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// BuildTime is Weapon.ReloadTime [06 §11.1] (compile-time reload*30).
//
// Determinism: stable iteration, no map iteration (I1), fixed-point 16.16 (I2),
// truncation toward zero (I3), pool is sole authority (I5) but not used here.
package orders

import (
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/units"
)

// secondaryTick returns the per-queue tick for the handler's unit [RS-P0-018].
// It reads Queue.SecondaryTick when available, else 0 (fixtures without pump).
func secondaryTick(u *units.Unit) uint32 {
	if q := QueueForUnit(u); q != nil {
		return q.SecondaryTick
	}
	return 0
}

// BuildWeaponHandler is the secondary-queue handler for BUILDWEAPON per [06 §11]
// C29 and [04 §3.1] 0x40000. It advances the linked stockpile slot via
// combat.TickStockpile's contract, performing truncated cumulative cost deltas,
// retry scheduling, slot-byte capping at 200, and queue-count decrement. It is
// registered onto the sorted descriptor table in init() after the table is built.
//
// Encoding: Param1 SlotIdx, Param2 remainingCount, Param3 progress.
// Progress is per-node 0..BuildTime step 5 capped [06 §11.1]. Ammo is the
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//
// Launch-before-production is preserved by phase order: weapon firing
// (PhaseUnitsScripts) runs before the orders pump (PhaseOrdersPathEconomy),
// so a round completed here cannot launch until the next tick [06 §11.1] C29.
func buildWeaponHandler(u *units.Unit, n *Node, satisfied uint32) Code {
	_ = satisfied
	if u == nil || n == nil {
		return Code(5)
	}
	// Resolve slotIdx and weapon.
	slotIdx := int(n.Param1)
	var slot *units.Slot
	if slotIdx >= 0 && slotIdx < units.NumSlots {
		s := u.SlotAt(slotIdx)
		if s != nil && s.Weapon != nil && s.Weapon.Stockpile {
			slot = s
		}
	}
	if slot == nil {
		// Fallback: first stockpile weapon slot [06 §11.1] (stockpile queue
		// keeps distinct signed count and byte remainder per weapon slot; when
		// Param1 is zero-initialized the first stockpile slot is the intended
		// target – battle.go and bw initial-mission path leave Param1 zero).
		for i := 0; i < units.NumSlots; i++ {
			s := u.SlotAt(i)
			if s != nil && s.Weapon != nil && s.Weapon.Stockpile {
				slot = s
				slotIdx = i
				n.Param1 = uint32(i)
				break
			}
		}
	}
	if slot == nil {
		// No stockpile weapon on this unit: keep node pending (do not unlink)
		// so dummy test queues with non-stockpile units are not spuriously
		// removed by the stockpile handler. Real stray nodes will simply wait
		// [05] and be cleaned via cancel.
		n.DynamicGate = 1
		n.Deadline = int32(secondaryTick(u) + 30)
		return Code(2)
	}
	weapon := slot.Weapon
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
		if binding == nil || binding.StockpileEconomy == nil {
			// Stockpile work is admitted only through the owning session's
			// economy ledger. An unbound queue cannot make progress: silently
			// treating it as an accepted request bypasses two-resource carry
			// admission and changes the retail queue lifecycle [05 "Two-stage
			// settlement algorithm"][06 §11.1].
			return false
		}
		buckets := binding.StockpileEconomy.UnitBuckets(u.Handle)
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
	nextTick, _, completedRounds := combat.TickStockpile(ce, cs, secondaryTick(u), admit)
	// Copy back slot ammo (TickStockpile wraps 255->0) and node fields.
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
	n.Deadline = int32(secondaryTick(u) + combat.StockpileRetryAccepted)
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
			ammo[i] = s.Ammo // TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
		idx := int(n.Param1)
		// Fallback: when Param1 is zero but weapon is at different slot, the
		// handler's fallback would have mapped it; for UI we sum under the
		// resolved slot when Param1 is in range and that slot is stockpile.
		if idx < 0 || idx >= units.NumSlots {
			// Find first stockpile slot for unmapped node.
			for j := 0; j < units.NumSlots; j++ {
				if s := u.SlotAt(j); s != nil && s.Weapon != nil && s.Weapon.Stockpile {
					idx = j
					break
				}
			}
		}
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
