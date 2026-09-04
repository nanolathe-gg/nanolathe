package hud

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/input"
)

// NotHandled is the result ParseButtonLatch returns when the button name
// matches none of the chain's eleven tests. Retail writes no latch and plays
// no cue in that case — the click falls through to whatever the panel
// handler tries next [07 §9 "Corrected and completed"]. It is the latch
// byte's zero value, which the dispatcher table never assigns to a matched
// arm, so `!NotHandled.IsValid()` also holds.
const NotHandled input.Latch = 0

// ParseButtonLatch parses a GUI order-button name into the armed latch byte
// via the retail button parse chain [07 §9] with gate gating [GAP T22].
//
// Chain order is **MOVE → STOP → ATTACK → BLAST → DEFEND → REPAIR → PATROL →
// RECLAIM → CAPTURE → UNLOAD → LOAD** [07 §9 "Corrected and completed"].
// MOVE is the first test, not a trailing default, and there is no default at
// all: a name matching none of the eleven returns NotHandled. The name is
// matched case-insensitively by substring containment. Writing the parsed
// value occurs only when gate != 0; otherwise the latch is forced to normal
// (1) [07 §9]. There is no `PICKUP` compare in retail — only `LOAD` — so a
// button literally named `PICKUP` (and nothing else in the chain) is
// NotHandled, not Pickup.
//
// Latches 0xB (TELEPORT) and 0xE (MOBILEBUILD) are armed by paths other than
// the button chain [07 §9] — MOBILEBUILD by the battle-HUD build-button handler
// — so this parser correctly never produces them. That is the section's own
// division of labour, not a gap in this function.
func ParseButtonLatch(name string, gate uint32) input.Latch {
	// Gate zero forces normal/idle [07 §9].
	if gate == 0 {
		return input.LatchNormal // [GAP T22] 1
	}
	upper := strings.ToUpper(name)
	// MOVE is the first test, not a default [07 §9] 2.
	if strings.Contains(upper, "MOVE") {
		return input.LatchMove // 2
	}
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
	// LOAD [07 §9][GAP T22] 6 — retail has no PICKUP compare.
	if strings.Contains(upper, "LOAD") {
		return input.LatchPickup // 6
	}
	// No predicate matched: not handled, no latch write, no cue [07 §9].
	return NotHandled
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
