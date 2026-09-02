package orders

// The remaining air executors: `VTOL_Evade`, `VTOL_SeekAttack`,
// `VTOL_SeekGuard`, `VTOL_GetRepaired`, and the four air-attack executors
// `AirStrike`, `AirToAir`, `AirToGround` and `AirToGroundHover`
// [04 R-AIR-01 §7][04 R-AIR-01 §8][04 R-ORD-02 §3].
//
// Only the ENTRY SEQUENCES live here. WU-18-4 settled the boundary while it
// implemented the ground combat family: every leg of these executors is built
// out of the unexported air path-marker family of [04 R-AIR-01 §4] — the point
// marker, the frozen terrain-relative marker, the follow-unit marker, the
// takeoff preamble, and `AirToAir`'s velocity-carrying payload — which is
// internal/movement's, and internal/orders cannot import internal/movement
// (the dependency runs the other way). The legs therefore live beside their
// siblings `VTOL_Move`, `VTOL_LandIfCan` and `VTOL_Standby` in
// internal/movement/airorders.go, and reach the pump through the runner seam
// below.
//
// `VTOL_GetRepaired` is the exception: [04 R-AIR-01 §7] makes it a pure record
// machine — a two-phase wait on the patient's own health — that installs no
// goal payload at all, so its whole body is here.
//
// Registration note. This family claims `AirStrike`, `AirToAir`, `AirToGround`
// and `AirToGroundHover` because table.go's installer list runs
// `ensureVTOLAirHandlers` before `ensureCombatHandlers`, and every installer
// assigns only where the descriptor's handler is still nil. That supersedes
// combat.go's `airAttackHandler`, which WU-18-4 wrote as an explicit
// placeholder saying the legs "belong beside their twins in internal/movement"
// — this is that placeholder being retired forward, not undone: its two shared
// pieces, `airEntry` and `airInterruptMask`, are what the four handlers below
// are built from.

import (
	"github.com/nanolathe/nanolathe/internal/units"
)

// pendNoRoute is the pending word's `0x40`: "an empty route is published while
// the unit is not at the goal — the cannot-get-there signal" [04 R-ORD-01 §0].
// It is the whole entry test of `VTOL_SeekAttack` and `VTOL_SeekGuard`
// [04 R-AIR-01 §7][04 R-ORD-02 §3].
const pendNoRoute uint32 = 0x40

// ---------------------------------------------------------------------------
// The runner seam
// ---------------------------------------------------------------------------

// AirLegRunner is the shape internal/movement registers so that the pump stays
// the sole dispatcher of an air record [04 §3.3] while the legs that build and
// install air path markers stay in the package that owns the marker family
// [04 R-AIR-01 §4].
//
// It reports `(code, true)` when it recognised the record and ran its leg, and
// `(0, false)` when it did not — no runner bound yet, a descriptor it does not
// drive, or a unit that belongs to a different world than the runner's.
type AirLegRunner func(u *units.Unit, n *Node, satisfied uint32, tick uint32) (Code, bool)

// airHandOff hands a record whose entry sequence did not end the order to the
// air executor legs, and returns the leg's own result code.
//
// With no runner bound — the movement system has not stepped this unit yet, or
// does not own it — the deadline setter of [04 R-ORD-01 §1]
// with `n = 1` (the one-tick hold `AirToAir` phase 0 and `VTOL_Standby` phase 0
// both arm) plus *hold*, so the record keeps its place at the head, is
// re-dispatched on the next tick, and never parks on a gate nothing can raise.
// Returning *complete* here instead would silently drop an order the player
// still owns; returning *hold* with no gate would spin the pump, because code 2
// restarts the walk from the head.
func airHandOff(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if q := QueueForUnit(u); q != nil && q.Binding() != nil && q.Binding().Movement != nil {
		if run := q.Binding().Movement.RunAir; run != nil {
			if code, handled := run(u, n, satisfied, tick); handled {
				return code
			}
		}
	}
	n.Deadline = int32(tick + 1)
	n.DynamicGate |= 1 // the deadline setter always ORs bit 0 [04 R-ORD-01 §1]
	return Code(2)
}

// ---------------------------------------------------------------------------
// VTOL_Evade [04 R-AIR-01 §8]
// ---------------------------------------------------------------------------

// vtolEvadeHandler is the random break of [04 R-AIR-01 §8]: "Entry returns 5 on
// a null target or when the satisfied set intersects `0x10008`."
//
// That is `airEntry` with mask `pendTargetGone` and nothing else: §8 names
// `VTOL_Evade` inside step 1's own `0x10008` list, alongside
// `AirToGroundHover`, so the evasion runs the same shared entry sequence the
// four attack executors do — including its per-visit cached-goal refresh and
// its maneuver leash — rather than a private two-arm test.
//
// The descriptor carries a zero static mask, so a `VTOL_Evade` record dispatches
// on sight: it is the order that proves the family reaches its legs at all.
func vtolEvadeHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if code, done := airEntry(u, n, satisfied, pendTargetGone); done {
		return code
	}
	return airHandOff(u, n, satisfied, tick)
}

// ---------------------------------------------------------------------------
// VTOL_SeekAttack, VTOL_SeekGuard [04 R-AIR-01 §7][04 R-ORD-02 §3]
// ---------------------------------------------------------------------------

// vtolSeekHandler is the entry both seek states share: "a satisfied goal-release
// bit `0x40` returns 5, and the off-map recovery of [R-AIR-01 §5] pre-empts"
// [04 R-AIR-01 §7]; [04 R-ORD-02 §3] repeats it verbatim for `VTOL_SeekGuard`.
//
// The off-map recovery is a marker leg, so it belongs to the runner and runs as
// the first thing the leg does, not here.
func vtolSeekHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if satisfied&pendNoRoute != 0 {
		return Code(5) // *complete* [04 R-AIR-01 §7]
	}
	return airHandOff(u, n, satisfied, tick)
}

// ---------------------------------------------------------------------------
// VTOL_GetRepaired [04 R-AIR-01 §7]
// ---------------------------------------------------------------------------

// vtolGetRepairedHandler is the two-phase wait an aircraft sitting on a repair
// pad runs [04 R-AIR-01 §7]:
//
//	With a null target it emits status cue slot 7 `Repair aborted.` and returns
//	8. Phase 0 returns 1 as soon as the unit's health has reached its
//	definition's `MaxDamage` (unsigned compare, `MaxDamage <= health`);
//	otherwise it sets the record's deadline to the current tick plus 30, ORs
//	`0x8` into the gate word, and returns 2. Phase 1 emits status cue slot 10
//	`Unit repaired` and returns 5.
//
// The health test is on the patient itself, not the pad: the order is the
// aircraft's own, and the pad's healing is the repairer's per-tick work. The
// comparison is unsigned and non-strict, so a unit already at full health
// advances out of phase 0 on its first visit and completes on its second.
//
// [04 R-AIR-01 §7] states exactly two phases and phase 1 completes, so no
// third phase is reachable; a record that somehow arrives at one is treated as
// phase 1 rather than given an invented arm.
func vtolGetRepairedHandler(u *units.Unit, n *Node, _ uint32, tick uint32) Code {
	if n.Target == 0 || targetOf(u, n) == nil {
		workStatus(u, statusCant, "Repair aborted.")
		return Code(8) // *abandon* [04 R-AIR-01 §7]
	}
	if n.Phase == 0 {
		if maxDamageOf(u) <= healthOf(u) {
			return Code(1) // *advance*: already whole [04 R-AIR-01 §7]
		}
		n.Deadline = int32(tick + 30)
		n.DynamicGate |= 1 | pendTargetRemoved // the deadline bit, plus `0x8`
		return Code(2)                         // *hold* [04 R-AIR-01 §7]
	}
	workStatus(u, statusRepair, "Unit repaired")
	return Code(5) // *complete* [04 R-AIR-01 §7]
}

// healthOf and maxDamageOf are the unsigned pair the health comparisons of
// [04 R-AIR-01 §7] and [04 R-AIR-01 §8] are written in. The definition's
// `MaxDamage` is the stated operand; a definition that carries none falls back
// to the instance's own maximum, which is how work.go already reads the pair.
func healthOf(u *units.Unit) uint32 {
	if u == nil || u.Health < 0 {
		return 0
	}
	return uint32(u.Health)
}

func maxDamageOf(u *units.Unit) uint32 {
	if u == nil {
		return 0
	}
	max := int32(0)
	if u.Def != nil {
		max = u.Def.MaxDamage
	}
	if max <= 0 {
		max = u.MaxHealth
	}
	if max < 0 {
		return 0
	}
	return uint32(max)
}

// ---------------------------------------------------------------------------
// AirStrike, AirToAir, AirToGround, AirToGroundHover [04 R-AIR-01 §8]
// ---------------------------------------------------------------------------

// airAttackExecutorHandler is the four air-attack executors' shared entry
// sequence [04 R-AIR-01 §8] followed by the hand-off to their legs. The entry
// sequence itself — the per-order interrupt mask, the per-visit cached-goal
// refresh, and the inclusive whole-world-unit maneuver leash — is WU-18-4's
// `airEntry`, reused rather than re-derived. Its step-1/step-2
// `VTOL_SeekAttack` replacements were closed by [04 R-AIR-01 §16], and the
// per-executor mask by the §8 addendum of 2026-09-02 (see `airInterruptMask`
// below).
func airAttackExecutorHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	if code, done := airEntry(u, n, satisfied, airInterruptMask(n.ID)); done {
		return code
	}
	return airHandOff(u, n, satisfied, tick)
}

// airInterruptMask is the shared-entry pending mask [04 R-AIR-01 §8], one
// immediate per executor: the two run executors test `0x1000A`, the standoff
// orbit and the dogfight test `0x10008`. `0x10008` is `pendTargetGone` — the
// target-removed and target-cloaked bits; the run executors' extra `0x2` is
// the cancel-current notification of [04 R-ORDER-02 §2], which only a record
// freed with gate bit 1 armed ever receives — and only `AirStrike` (`0xE2`)
// and `AirToGround` (`0x100EA`) arm that bit on their legs.
func airInterruptMask(id ID) uint32 {
	switch DescriptorFor(id).Name {
	case "AirStrike", "AirToGround":
		return pendTargetGone | gateCancelCurrent
	case "AirToGroundHover", "AirToAir":
		return pendTargetGone
	default:
		return pendTargetGone
	}
}

// ---------------------------------------------------------------------------
// VTOL_LandIfCan [04 R-AIR-01 §6]
// ---------------------------------------------------------------------------

// vtolLandIfCanHandler gives the landing machine a descriptor handler whose
// only job is to let the record finish.
//
// The machine itself is `execVTOLLandIfCan` in internal/movement, which the
// mover tick runs off the head record because it owns the air marker family.
// It has always reached touchdown; what it could not do is say so. With no
// handler here the record fell into the pump's `handlerlessButDriven` arm,
// which deliberately writes none of a driven record's fields — correct while
// the machine is running, and terminal once it stops, because the record then
// sat at the head forever and every later order queued behind it. `Stop` on an
// airborne aircraft landed it and jammed its queue; the next order the player
// gave never ran.
//
// The hand-off is the one the four air-attack executors already use, so the
// pump stays the sole dispatcher [04 §3.3] and the marker work stays in the
// package that owns markers [04 R-AIR-01 §4]. The runner's answer for this
// descriptor reads the executor's outcome rather than re-running it.
//
// `VTOL_Standby` deliberately keeps its place in `handlerlessButDriven`: it is
// a standing auto-op record with its own idle-refill lifecycle [04 §3.3], not a
// machine that finishes, and giving it a completion is a separate question.
func vtolLandIfCanHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	return airHandOff(u, n, satisfied, tick)
}

// ---------------------------------------------------------------------------
// VTOL_Landing [04 R-AIR-01 §6]
// ---------------------------------------------------------------------------

// vtolLandingHandler is the same arrangement for the pad-landing machine.
//
// `execVTOLLanding` in internal/movement is the seven-phase machine of
// [04 R-AIR-01 §6] — takeoff preamble, loiter bearing, the pad query, the
// approach and the descent — and the mover tick has always dispatched it off
// the head record. It never ran, because a placeholder descriptor handler
// completed the record on its first dispatch.
//
// That placeholder wrote the pad's X, Y and Z straight onto the aircraft and
// returned *complete*: an aircraft ordered to land teleported onto the pad in
// one tick, moving 320 world units against a MaxVelocity of 10, and never
// attached to a pad piece. It predated the machine it was shadowing. Routing
// the descriptor at the runner leaves one owner for the phase byte, and the
// runner's answer reads the executor's outcome rather than re-running it.
func vtolLandingHandler(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	return airHandOff(u, n, satisfied, tick)
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// vtolAirHandlers is the family's registration list, a slice so that source
// order is registration order (I1).
var vtolAirHandlers = []struct {
	name    string
	handler func(*units.Unit, *Node, uint32, uint32) Code
}{
	{"VTOL_LandIfCan", vtolLandIfCanHandler},
	{"VTOL_Landing", vtolLandingHandler},
	{"VTOL_Evade", vtolEvadeHandler},
	{"VTOL_SeekAttack", vtolSeekHandler},
	{"VTOL_SeekGuard", vtolSeekHandler},
	{"VTOL_GetRepaired", vtolGetRepairedHandler},
	{"AirStrike", airAttackExecutorHandler},
	{"AirToAir", airAttackExecutorHandler},
	{"AirToGround", airAttackExecutorHandler},
	{"AirToGroundHover", airAttackExecutorHandler},
}

// ensureVTOLAirHandlers installs this family onto the descriptor table. It is
// idempotent and assigns only where the descriptor still has no handler, and it
// tolerates a table that has not been built yet.
func ensureVTOLAirHandlers() {
	if len(table) == 0 {
		return
	}
	for _, entry := range vtolAirHandlers {
		id := Lookup(entry.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = entry.handler
		}
	}
}

func init() { ensureVTOLAirHandlers() }
