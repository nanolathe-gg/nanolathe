package hud

import (
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// ParseButtonLatch parses a GUI order-button name into the armed latch byte
// via the retail button parse chain [07 §9] with gate gating [GAP T22].
//
// Chain order is STOP → ATTACK → BLAST → DEFEND → REPAIR → PATROL → RECLAIM
// → CAPTURE → UNLOAD → LOAD/PICKUP → MOVE [07 §9]. The name is matched
// case-insensitively by substring containment. Writing the parsed value
// occurs only when gate != 0; otherwise the latch is forced to normal (1)
// [07 §9]. Default when no predicate matches is MOVE (2) [07 §9].
//
// TODO(question) latch 0xB (TELEPORT) and 0xE (MOBILEBUILD) can be armed by
// paths other than the button chain [07 §9]; only the button path is wired
// here, so this parser never produces those values.
func ParseButtonLatch(name string, gate uint32) input.Latch {
	// Gate zero forces normal/idle [07 §9].
	if gate == 0 {
		return input.LatchNormal // [GAP T22] 1
	}
	upper := strings.ToUpper(name)
	// STOP → generic immediate table [07 §9][GAP T22] 1.
	if strings.Contains(upper, "STOP") {
		return input.LatchNormal // 1
	}
	// ATTACK [07 §9][GAP T22] 3.
	if strings.Contains(upper, "ATTACK") {
		return input.LatchAttack // 3
	}
	// BLAST (attack-special) [07 §9][GAP T22] 4.
	if strings.Contains(upper, "BLAST") {
		return input.LatchBlast // 4
	}
	// DEFEND → FOLLOW/GUARD [07 §9][GAP T22] 7.
	if strings.Contains(upper, "DEFEND") {
		return input.LatchFollow // 7
	}
	// REPAIR/HELPBUILD [07 §9][GAP T22] 8.
	if strings.Contains(upper, "REPAIR") {
		return input.LatchRepair // 8
	}
	// PATROL [07 §9][GAP T22] 9.
	if strings.Contains(upper, "PATROL") {
		return input.LatchPatrol // 9
	}
	// RECLAIM/RESURRECT [07 §9][GAP T22] 0xC.
	if strings.Contains(upper, "RECLAIM") {
		return input.LatchReclaim // 0xC
	}
	// CAPTURE [07 §9][GAP T22] 0xD.
	if strings.Contains(upper, "CAPTURE") {
		return input.LatchCapture // 0xD
	}
	// UNLOAD [07 §9][GAP T22] 5 — must be checked before LOAD because
	// UNLOAD contains LOAD as substring.
	if strings.Contains(upper, "UNLOAD") {
		return input.LatchUnload // 5
	}
	// LOAD/PICKUP alias [07 §9][GAP T22] 6.
	if strings.Contains(upper, "LOAD") || strings.Contains(upper, "PICKUP") {
		return input.LatchPickup // 6
	}
	// Default MOVE [07 §9][GAP T22] 2.
	return input.LatchMove // 2
}

// LatchToCode maps a latch byte to the orders resolver command code 1..14
// [GAP T22][07 §9]. The latch byte IS the dispatcher switch key [GAP T22]:
// normal 1, MOVE 2, ATTACK 3, BLAST 4, UNLOAD 5, PICKUP 6, FOLLOW 7, REPAIR 8,
// PATROL 9, TELEPORT 0xB (11), RECLAIM/RESURRECT 0xC (12), CAPTURE 0xD (13),
// MOBILEBUILD 0xE (14). 0xA and any other value return 0 (no dispatch).
func LatchToCode(l input.Latch) int {
	switch l {
	case input.LatchNormal:
		return 1 // [GAP T22]
	case input.LatchMove:
		return 2 // [GAP T22]
	case input.LatchAttack:
		return 3 // [GAP T22]
	case input.LatchBlast:
		return 4 // [GAP T22]
	case input.LatchUnload:
		return 5 // [GAP T22]
	case input.LatchPickup:
		return 6 // [GAP T22]
	case input.LatchFollow:
		return 7 // [GAP T22]
	case input.LatchRepair:
		return 8 // [GAP T22]
	case input.LatchPatrol:
		return 9 // [GAP T22]
	case input.LatchTeleport:
		return 11 // 0xB [GAP T22]
	case input.LatchReclaim:
		return 12 // 0xC [GAP T22]
	case input.LatchCapture:
		return 13 // 0xD [GAP T22]
	case input.LatchMobileBuild:
		return 14 // 0xE [GAP T22]
	default:
		return 0
	}
}

// Dispatch resolves a latched click for a single actor to an order ID via
// orders.Resolve [04 §3.4] wire-through only [07 §9][GAP T22]. The latch byte
// is translated to the resolver code 1..14 and forwarded; failed gates or an
// invalid latch return 0 (the reject sentinel) [04 §3.1].
func Dispatch(latch input.Latch, actor *units.Unit, target *units.Unit, pos *orders.ResolvePos) orders.ID {
	code := LatchToCode(latch)
	if code == 0 {
		return 0
	}
	return orders.Resolve(code, actor, target, pos)
}

// DispatchSelection resolves the latch for each selected unit stably in
// ascending Handle order per I1 [07 §9] and returns the per-unit order IDs
// aligned with the sorted order. Wire-through only; no new order semantics
// [GAP T22][04 §3.4].
func DispatchSelection(latch input.Latch, selected []*units.Unit, target *units.Unit, pos *orders.ResolvePos) []orders.ID {
	if len(selected) == 0 {
		return nil
	}
	// Stable ascending Handle order (I1) regardless of input order.
	sorted := make([]*units.Unit, len(selected))
	copy(sorted, selected)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i] == nil && sorted[j] == nil {
			return false
		}
		if sorted[i] == nil {
			return true
		}
		if sorted[j] == nil {
			return false
		}
		return sorted[i].Handle < sorted[j].Handle
	})
	code := LatchToCode(latch)
	if code == 0 {
		out := make([]orders.ID, len(sorted))
		return out
	}
	out := make([]orders.ID, len(sorted))
	for i, u := range sorted {
		out[i] = orders.Resolve(code, u, target, pos)
	}
	return out
}

// LatchIsValid is a convenience wrapper over input.Latch.IsValid [GAP T22].
func LatchIsValid(l input.Latch) bool { return l.IsValid() }
