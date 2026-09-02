// Package orders — the transport order handlers [04 §10.2][04 R-AIR-01 §9].
//
// There are two load executors and two unload executors, split by carrier
// locomotion rather than by order family name [04 R-AIR-01 §9]:
//
//	VTOL_Pickup / VTOL_Unload      the air pair of [04 §10.2]. Every command
//	                               their phase tables queue is an air path
//	                               marker of [04 R-AIR-01 §4], which is
//	                               internal/movement's family, so the legs live
//	                               there and reach the pump through the runner
//	                               seam of vtolair.go — the same arrangement
//	                               the seven other pump-driven air executors
//	                               already use.
//	Ground_Pickup / Ground_Unload  a separate machine that never moves the
//	                               cargo itself: it fires a COB callback and
//	                               waits for the SCRIPT to perform the
//	                               attachment or the drop through the COB
//	                               transport opcodes [04 R-COB-03 §5]. Its
//	                               whole body is here, because it installs
//	                               ground goal handles rather than air markers.
//
//	BeCarried                      the cargo's own two-phase carried wait
//	                               [04 R-ORD-01 §2].
//
// The two families are selected by order identity at command resolution and
// never both run for one record [04 R-AIR-01 §9].
package orders

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Verbatim retail diagnostics [04 §10.2][04 R-AIR-01 §9]. Only the ground
// pair's are here; the air pair's belong to its legs. The two size-gate
// messages differ by exactly one word and that difference is the contract:
// `too large` for the ground carrier, `too heavy` for the air one.
const (
	groundTransportFailedMessage = "Transport mission failed"
	groundTransportLargeMessage  = "Unit is too large to transport"
	groundUnloadSuboptimalText   = "Unloading process is proceeding non-optimally"
)

// Status kinds this family raises, from [04 R-ORD-01 §1]'s table. Kinds 12
// (`load`) and 13 (`unload`) are the two notification event codes the ground
// pair emits — [04 R-AIR-01 §9] phase 2 of each — and both carry no default
// text in that table, which is what "no text payload" means at the emitter.
const (
	statusLoadEvent   uint8 = 12
	statusUnloadEvent uint8 = 13
)

// transportEntryInterrupt is the satisfied bit `0x8` both ground executors
// test at entry [04 R-AIR-01 §9]. It is `pendTargetRemoved` of
// [04 R-ORD-01 §0], already named by combat.go.
const transportEntryInterrupt = pendTargetRemoved

// groundTransportAttempts is the attempt counter's terminal value: phase 4 of
// `Ground_Pickup` and phase 2 of `Ground_Unload` both give up at 3
// [04 R-AIR-01 §9].
const groundTransportAttempts = 3

// groundTransportApproachGate is the `0xE8` both ground executors write when
// they install their approach goal handle [04 R-AIR-01 §9]: the three movement
// outcomes plus the target-removed interrupt.
const groundTransportApproachGate uint32 = 0xE8

// lookupTarget resolves target handle via per-queue Lookup [P0-I16].
func lookupTarget(carrier *units.Unit, target pool.Handle) *units.Unit {
	if carrier == nil || target == 0 {
		return nil
	}
	if q := QueueForUnit(carrier); q != nil {
		if binding := q.Binding(); binding != nil && binding.Lookup != nil {
			return binding.Lookup(target)
		}
	}
	return nil
}

// transportFootprintX is the target's cached footprint-X word both size gates
// read, compared SIGNED against the carrier definition's `transportsize` byte
// zero-extended. [04 R-AIR-01 §9] re-verified both operands and recorded that
// the earlier "a byte of the target's definition against a word of the
// carrier's runtime state" reading is inverted.
func transportFootprintX(target *units.Unit) int16 {
	if target == nil || target.Def == nil {
		return 0
	}
	return int16(target.Def.FootprintX)
}

// transportSizeByte is the carrier side of that comparison.
func transportSizeByte(carrier *units.Unit) int32 {
	if carrier == nil || carrier.Def == nil {
		return 0
	}
	return int32(uint8(carrier.Def.TransportSize))
}

// ---------------------------------------------------------------------------
// VTOL_Pickup, VTOL_Unload [04 §10.2]
// ---------------------------------------------------------------------------

// airTransportHandler routes the air pair to its legs. Both executors' whole
// bodies — the four entry gates, the phase tables, the markers, the callbacks
// and the two event codes — are `legVTOLPickup` and `legVTOLUnload` in
// internal/movement, for the reason the package comment gives.
//
// This replaces a pair of handlers that ran the phase tables here with every
// side effect stubbed: each phase advanced and left a `TODO(question)` reading
// "not simulated beyond phase advance", so an ordered Atlas attached its cargo
// on the spot without ever flying to it, and an unload dropped a unit wherever
// the record's goal said with no validator and no descent. What made that
// unfixable in place is the air marker family: internal/orders cannot import
// internal/movement, and every command §10.2's tables queue is one of those
// markers.
func airTransportHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	return airHandOff(u, n, satisfied, tick)
}

// ---------------------------------------------------------------------------
// Ground_Pickup [04 R-AIR-01 §9]
// ---------------------------------------------------------------------------

// groundPickupHandler is the ground carrier's load executor. It never moves
// the cargo: phase 2 fires `TransportPickup` on the CARRIER's script and phase
// 4 waits for that script to perform the attachment through the COB transport
// opcodes [04 R-COB-03 §5].
//
//	Entry: a null target, or a satisfied bit 0x8, emits status cue slot 7
//	`Transport mission failed` and returns 8; a phase above 5 returns 7.
//
//	0     a live mover (else 7) and the carrier definition's `canload` bit
//	      (else 7); the size gate, whose failure is slot 7
//	      `Unit is too large to transport` and 8; otherwise the caption
//	      `Loading unit` (slot 5).                                        -> 1
//	1, 3  the shared short-move helper.                              -> 1 or 2
//	2     asynchronous one-argument `TransportPickup` on the carrier's script
//	      with cell 0 = the cargo's stable unit identity; notification event
//	      12; increment the attempt counter; deadline tick + 15.          -> 1
//	4     the target now has a carrier -> 5; the attempt counter has reached 3
//	      -> 9; else a ground goal handle at the target's current position with
//	      radius parameter 0, gate = 0xE8.                          -> 1, 5 or 9
//	5     clear the goal payload.                                          -> 0
func groundPickupHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	target := lookupTarget(u, n.Target)
	if n.Target == 0 || target == nil || satisfied&transportEntryInterrupt != 0 {
		workStatus(u, statusCant, groundTransportFailedMessage)
		return 8 // *abandon* [04 R-AIR-01 §9]
	}
	switch n.Phase {
	case 0:
		if u.Def == nil || !u.Def.CanMove || !u.Def.CanLoad {
			return 7 // *cancel-all*: no live mover, or no `canload` [04 R-AIR-01 §9]
		}
		if int32(transportFootprintX(target)) > transportSizeByte(u) {
			workStatus(u, statusCant, groundTransportLargeMessage)
			return 8
		}
		workStatus(u, statusOK, "Loading unit")
		return 1
	case 1, 3:
		return groundTransportShortMove(n)
	case 2:
		// Arity 1, cell 0 = the cargo's stable unit identity (its pool slot
		// id), fillers 0, wake flag set; the engine emits notification event 12
		// right after [04 R-UNIT-06 §3].
		if bridge := callbackBridgeFor(u); bridge != nil {
			bridge.DeferredWake("TransportPickup", []int32{int32(n.Target)}, nil)
		}
		workStatus(u, statusLoadEvent, "")
		n.Param2++
		n.Deadline = int32(tick + 15)
		n.DynamicGate |= gateDeadline // the deadline setter's bit [04 R-ORD-01 §1]
		return 1
	case 4:
		if target.Attachment.Carrier != 0 {
			return 5 // the script did the attach [04 R-AIR-01 §9]
		}
		if n.Param2 >= groundTransportAttempts {
			return 9 // *retry* [04 R-AIR-01 §9]
		}
		installGroundGoal(u, n, target.X, target.Y, target.Z, 0)
		n.DynamicGate = groundTransportApproachGate
		return 1
	case 5:
		releaseGoal(u, n)
		return 0 // *restart* [04 R-AIR-01 §9]
	default:
		return 7 // a phase above 5 [04 R-AIR-01 §9]
	}
}

// ---------------------------------------------------------------------------
// Ground_Unload [04 R-AIR-01 §9]
// ---------------------------------------------------------------------------

// groundUnloadHandler is the ground carrier's unload executor, the twin of the
// above: phase 0 fires `TransportDrop` and phase 2 waits for the script to have
// released the cargo.
//
//	Entry: a satisfied bit 0x8 emits status cue slot 7
//	`Unloading process is proceeding non-optimally` and returns 8.
//
//	0  a live mover and `canload`; bind the record's target handle to the
//	   carrier's cargo-list head and return 5 if that head is null; the caption
//	   `Unloading`; asynchronous one-argument `TransportDrop` on the carrier's
//	   script with cell 0 = the cargo's identity and cell 1 = the packed drop
//	   point; increment the attempt counter; deadline tick + 15.           -> 1
//	1  the shared short-move helper.                                 -> 1 or 2
//	2  notification event 13 and 5 as soon as the cargo's carrier reference is
//	   no longer this carrier; 9 once the attempt counter reaches 3; else a
//	   ground goal handle at the record's goal with the hover radius parameter,
//	   gate = 0xE8.                                                 -> 1, 5 or 9
//	3  return 0.
func groundUnloadHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if satisfied&transportEntryInterrupt != 0 {
		workStatus(u, statusCant, groundUnloadSuboptimalText)
		return 8 // *abandon* [04 R-AIR-01 §9]
	}
	switch n.Phase {
	case 0:
		if u.Def == nil || !u.Def.CanMove || !u.Def.CanLoad {
			return 7
		}
		if len(u.Attachment.Cargo) == 0 {
			return 5 // a null cargo-list head completes at once [04 R-AIR-01 §9]
		}
		// The list is LIFO, so the head is the most recently attached cargo
		// [04 R-UNIT-06 §3].
		n.Target = u.Attachment.Cargo[0]
		workStatus(u, statusOK, "Unloading")
		if bridge := callbackBridgeFor(u); bridge != nil {
			// Cell 0 = the cargo's identity, cell 1 = the packed drop point;
			// the position cell is physically present even though the arity
			// byte says one argument [04 R-UNIT-06 §3].
			bridge.DeferredWake("TransportDrop", []int32{int32(n.Target), packedDropPoint(n)}, nil)
		}
		n.Param2++
		n.Deadline = int32(tick + 15)
		n.DynamicGate |= gateDeadline
		return 1
	case 1:
		return groundTransportShortMove(n)
	case 2:
		if cargo := lookupTarget(u, n.Target); cargo == nil || cargo.Attachment.Carrier != u.Handle {
			workStatus(u, statusUnloadEvent, "")
			return 5 // the script did the drop [04 R-AIR-01 §9]
		}
		if n.Param2 >= groundTransportAttempts {
			return 9
		}
		installGroundGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, groundUnloadRadius(u))
		n.DynamicGate = groundTransportApproachGate
		return 1
	case 3:
		return 0 // *restart* [04 R-AIR-01 §9]
	default:
		return 7
	}
}

// packedDropPoint is `TransportDrop`'s cell 1 [04 R-UNIT-06 §3]: "the
// destination X truncated to whole world units in the high half, destination Z
// integer part in the low half". Both halves are the 16.16 goal's high word.
func packedDropPoint(n *Node) int32 {
	x := int32(int64(n.GoalX) >> 16)
	z := int32(int64(n.GoalZ) >> 16)
	return int32(uint32(uint16(x))<<16 | uint32(uint16(z)))
}

// groundUnloadRadius is phase 2's goal-handle radius parameter
// [04 R-AIR-01 §9]: `trunc(carrierModelZExtentInteger · 1.5)` when the carrier
// definition has `canhover` set, and 0 when it does not.
//
// TODO(question): the compiled definition carries the model's total height
// (`ModelTop`, the max-Y dword's high half [04 R-UNIT-06 §3]) but no model Z
// extent, and no format or spec section names one. Placeholder: 0 for every
// carrier, which is the non-hover arm — a hovercraft therefore approaches its
// own drop point exactly rather than standing off by one and a half hull
// lengths. What would settle it: which model-bounds word the definition loader
// stores beside the max-Y one, in `research/formats/3do.md` terms.
func groundUnloadRadius(u *units.Unit) int32 {
	_ = u
	return 0
}

// groundTransportShortMove is the helper phases 1 and 3 of `Ground_Pickup` and
// phase 1 of `Ground_Unload` share [04 R-AIR-01 §9]: "when the unit's
// movement-state byte has bit 0x2 set, write gate 0x8 | 0x4 and return 2;
// otherwise return 1".
//
// TODO(question): §9 names the tested word only as "the unit's movement-state
// byte", and no section maps it onto a field this build has. `Node.MoveState`
// is the movement scheduler's published enumeration (0 none, 1 en route, 2
// arrived, 3 blocked [P0-I03]), not a bit field, so testing bit 0x2 on it would
// answer a different question. Placeholder: take the "otherwise" arm, so the
// phase advances and the machine reaches its callback rather than parking on a
// gate whose raising condition is unknown. What would settle it: which of the
// mover's state words that byte is, in the terms [04 R-MOV-01 §8] uses for the
// committed mover-mode pair.
func groundTransportShortMove(n *Node) Code {
	_ = n
	return 1
}

// installGroundGoal installs one point payload through the session-owned
// movement adapter [04 R-ORD-01 §1]. A queue with no movement service bound is
// a fixture, and leaves the payload alone.
func installGroundGoal(u *units.Unit, n *Node, x, y, z numeric.Fixed, radius int32) {
	b := bindingOfUnit(u)
	if b == nil || b.Movement == nil || b.Movement.InstallPoint == nil {
		return
	}
	b.Movement.InstallPoint(PointGoalRequest{Owner: u.Handle, Node: n, X: x, Y: y, Z: z, Radius: radius})
}

// releaseGoal is the payload release of [04 R-ORD-01 §1]'s four goal
// installers.
func releaseGoal(u *units.Unit, n *Node) {
	b := bindingOfUnit(u)
	if b == nil || b.Movement == nil || b.Movement.Release == nil {
		return
	}
	b.Movement.Release(n)
}

// ---------------------------------------------------------------------------
// BeCarried [04 R-ORD-01 §2]
// ---------------------------------------------------------------------------

// beCarriedHandler is the carried unit's own record:
//
//	If the unit's carrier link is null → complete. Phase 0: release all slots;
//	advance. Phase 1: deadline 10; hold. Other: cancel-all. A carried unit
//	therefore re-checks its carrier link every ~10 ticks.
//
// It draws no RNG. The deadline is measured from the handler's tick argument
// (WU-18-7 retired the by-name `beCarriedHandlerAtTick` special case the pump
// used to reach this body with).
func beCarriedHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	_ = satisfied
	if u == nil || n == nil {
		return 5
	}
	if u.Attachment.Carrier == 0 {
		return 5 // the carrier link is null [04 R-ORD-01 §2]
	}
	switch n.Phase {
	case 0:
		for i := 0; i < units.NumSlots; i++ {
			if slot := u.SlotAt(i); slot != nil {
				slot.Target = units.Target{Kind: units.TargetNone}
			}
		}
		return 1
	case 1:
		n.DynamicGate = gateDeadline
		n.Deadline = int32(tick + 10)
		return 2 // *hold* [04 R-ORD-01 §2]
	default:
		return 7 // *cancel-all* [04 R-ORD-01 §2]
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// transportHandlers is the family's registration list, a slice so that source
// order is registration order (I1).
var transportHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"Ground_Pickup", groundPickupHandler},
	{"Ground_Unload", groundUnloadHandler},
	{"VTOL_Pickup", airTransportHandler},
	{"VTOL_Unload", airTransportHandler},
	{"BeCarried", beCarriedHandler},
}

func ensureTransportHandlers() {
	if len(table) == 0 {
		return
	}
	for _, entry := range transportHandlers {
		id := Lookup(entry.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = entry.handler
		}
	}
}

func init() { ensureTransportHandlers() }
