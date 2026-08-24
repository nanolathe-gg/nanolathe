package triggers

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/units"
)

// Trigger evaluation [08 "Evaluation"] [PLAN_10 C16, C17].
//
// Triggers are vtable objects, not type-byte structs: each carries a poll slot
// and three separate notification slots (unit died, unit captured/transferred,
// unit created — the last present but unused by any shipped condition). The
// tick site calls only the poll; the notification slots are driven by the
// corresponding gameplay events.
//
// That separation is the contract, and collapsing it is what made a
// "kill 5 of type X" condition complete in five ticks with nothing killed: a
// countdown that advances from the poll advances from the passage of time.
// Poll never consumes an event, and a notification never runs the scans.

// PollContext is the input to the tick-driven poll slot [08 "Evaluation"].
//
// The poll-time scans walk the live unit world, so the world is the context —
// unlike the notification slots, which carry a single subject unit.
type PollContext struct {
	Tick  uint32
	World *units.World

	// LocalOwner and EnemyOwner are the player indices the owner gates compare
	// against [08 "Evaluation"]. Victory conditions gate on the enemy index and
	// CommanderKilled on the local one; UnitTypeKilled and AllUnitsKilledOfType
	// accept any owner. Single-player missions use 0 and 1.
	LocalOwner uint8
	EnemyOwner uint8
}

// NotifyEvent identifies which notification slot is being driven
// [08 "Evaluation"].
type NotifyEvent uint8

const (
	NotifyUnitDied     NotifyEvent = iota // slot 1
	NotifyUnitCaptured                    // slot 2 (capture/transfer)
	NotifyUnitCreated                     // slot 3 — present, unused by shipped conditions
)

// matchesType reports whether u satisfies the trigger's authored type token.
// An empty token and the literal ANYTYPE both match any type
// [08 "Victory and defeat triggers"] C14.
func (t *Trigger) matchesType(u *units.Unit) bool {
	if u == nil || u.Def == nil {
		return false
	}
	if t.Type == "" || IsANYTYPE(t.Type) {
		return true
	}
	return strings.EqualFold(u.Def.UnitName, t.Type)
}

// forEachLive visits live units in stable pool-slot order (I1). A nil world
// visits nothing, which leaves every scanning condition unsatisfied — the
// correct answer for a session that has not started.
func forEachLive(w *units.World, fn func(*units.Unit)) {
	if w == nil {
		return
	}
	for _, u := range w.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		fn(u)
	}
}

// Poll is the tick-driven slot. It mutates only the trigger's completed flag
// [08 "Evaluation"] C17 and returns the flag's new value. An already-completed
// trigger stays completed.
func (t *Trigger) Poll(c PollContext) bool {
	if t == nil {
		return false
	}
	if t.Completed {
		return true
	}
	switch t.Kind {

	// --- Flag-only conditions ------------------------------------------------

	case KindDestroyAllUnits:
		// The engine's body reads a counter whose only reference in the image
		// is that read — nothing writes it, so it holds its initial zero and
		// the condition is satisfied from the FIRST poll [08 "Evaluation"].
		//
		// This is not a bug to route around. Shipped campaign missions use
		// DestroyAllUnits as an AND-term beside a real BuildUnitType condition,
		// where a genuinely evaluated destroy-all would never let the mission
		// complete. It is also why the injected default victory condition
		// (C16) resolves rather than hanging.
		t.Completed = true
		return true

	case KindKillAllMobileUnits:
		// No live mobile unit belonging to the enemy remains.
		remaining := false
		forEachLive(c.World, func(u *units.Unit) {
			if u.Owner != c.EnemyOwner || u.Def == nil {
				return
			}
			if u.Def.CanMove {
				remaining = true
			}
		})
		if !remaining {
			t.Completed = true
		}
		return t.Completed

	case KindAllUnitsKilled:
		// Defeat: the local player has nothing left.
		remaining := false
		forEachLive(c.World, func(u *units.Unit) {
			if u.Owner == c.LocalOwner {
				remaining = true
			}
		})
		if !remaining {
			t.Completed = true
		}
		return t.Completed

	case KindKillEnemyCommander, KindCommanderKilled:
		// Absence scans over the owner this kind gates on: victory conditions
		// gate on the enemy index, CommanderKilled on the local one
		// [08 "Evaluation"].
		//
		// TODO(question): retail resolves commander identity through the
		// SIDEDATA commander-name table rather than the definition's commander
		// flag [08 "Evaluation"]. The flag is the same set for stock content;
		// swap the predicate when the side table is threaded through here.
		owner := c.EnemyOwner
		if t.Kind == KindCommanderKilled {
			owner = c.LocalOwner
		}
		alive := false
		forEachLive(c.World, func(u *units.Unit) {
			if u.Owner == owner && u.Def != nil && u.Def.Commander {
				alive = true
			}
		})
		if !alive {
			t.Completed = true
		}
		return t.Completed

	// --- Poll-time scans over live units -------------------------------------

	case KindBuildUnitType:
		// Satisfied when the local player holds at least the authored count of
		// completed units of the type [08 "Evaluation"].
		want := t.Args[0]
		if want < 1 {
			want = 1
		}
		var have int32
		forEachLive(c.World, func(u *units.Unit) {
			if u.Owner == c.LocalOwner && u.Remaining == 0 && t.matchesType(u) {
				have++
			}
		})
		if have >= want {
			t.Completed = true
		}
		return t.Completed

	case KindKillAllOfType, KindAllUnitsKilledOfType:
		// No live unit of the authored type remains. KillAllOfType is a victory
		// condition and gates on the enemy owner; AllUnitsKilledOfType is a
		// defeat condition and takes any owner [08 "Evaluation"].
		anyOwner := t.Kind == KindAllUnitsKilledOfType
		remaining := false
		forEachLive(c.World, func(u *units.Unit) {
			if !anyOwner && u.Owner != c.EnemyOwner {
				return
			}
			if t.matchesType(u) {
				remaining = true
			}
		})
		if !remaining {
			t.Completed = true
		}
		return t.Completed

	case KindUnitTypePassesX, KindUnitTypePassesZ, KindAnyUnitPassesX, KindAnyUnitPassesZ:
		// Boundary conditions scan live units and compare the signed world
		// coordinate against the stored threshold, satisfied when the absolute
		// difference is below three world units — a ±2 tolerance
		// [08 "Evaluation"] C17. The ANY variants take any type.
		isX := t.Kind == KindUnitTypePassesX || t.Kind == KindAnyUnitPassesX
		anyType := t.Kind == KindAnyUnitPassesX || t.Kind == KindAnyUnitPassesZ
		threshold := t.Args[0]
		forEachLive(c.World, func(u *units.Unit) {
			if t.Completed {
				return
			}
			if !anyType && !t.matchesType(u) {
				return
			}
			pos := worldPixel(u.Z)
			if isX {
				pos = worldPixel(u.X)
			}
			if withinBoundary(pos, threshold) {
				t.Completed = true
			}
		})
		return t.Completed

	case KindMoveUnitToRadius:
		// Type-gated scan against the authored centre and radius.
		//
		// TODO(question): the axis pair and the distance metric are not decoded
		// [08 "Evaluation"]. Implemented as a planar X/Z Euclidean test against
		// the squared radius, which is the only reading consistent with the
		// three authored integers being X, Z and radius.
		cx, cz, rad := t.Args[0], t.Args[1], t.Args[2]
		forEachLive(c.World, func(u *units.Unit) {
			if t.Completed || !t.matchesType(u) {
				return
			}
			dx := int64(worldPixel(u.X) - cx)
			dz := int64(worldPixel(u.Z) - cz)
			if dx*dx+dz*dz <= int64(rad)*int64(rad) {
				t.Completed = true
			}
		})
		return t.Completed

	// --- Timers --------------------------------------------------------------

	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		// Timers store seconds×30 as an absolute tick deadline via IMUL 30
		// TODO(question): Historical analysis omitted; independently worded behavior is needed.
		// "Evaluation"] C17 [P1-01 §2.1][P1-01 §4]. Comparison is >= (not >)
		// and uses signed int32 tick vs stored int ticks; seconds*30 stored
		// as int ticks and compared >= [P1-01].
		// TODO(question): signedness of deadline compare and overflow clamp
		// for large seconds remain TODO(question) [P1-01 §8].
		if int32(c.Tick) >= t.Args[0] {
			t.Completed = true
		}
		return t.Completed

	// --- Notification-driven conditions do nothing on a poll -----------------

	case KindKillUnitType, KindUnitTypeKilled, KindCaptureUnitType:
		return false

	default:
		return false
	}
}

// Notify drives one of the notification slots [08 "Evaluation"]. It mutates
// only the trigger's completed flag and its own countdown, and returns the
// completed flag's new value.
//
// The countdown conditions live here rather than in Poll: retail advances them
// from the unit-died and capture notifications, so they measure kills and
// captures, not elapsed time.
func (t *Trigger) Notify(c PollContext, ev NotifyEvent, u *units.Unit) bool {
	if t == nil {
		return false
	}
	if t.Completed {
		return true
	}
	if u == nil || u.Def == nil {
		return false
	}
	switch t.Kind {
	case KindKillUnitType, KindUnitTypeKilled:
		if ev != NotifyUnitDied || !t.matchesType(u) {
			return false
		}
		// KillUnitType is a victory condition and gates on the enemy owner;
		// UnitTypeKilled is a defeat condition and takes any owner
		// [08 "Evaluation"].
		if t.Kind == KindKillUnitType && u.Owner != c.EnemyOwner {
			return false
		}
		// While the subject's type matches, decrement the authored count and
		// complete once it reaches zero or below [08 "Evaluation"] C17.
		t.Args[0]--
		if t.Args[0] <= 0 {
			t.Completed = true
		}
		return t.Completed

	case KindCaptureUnitType:
		if ev != NotifyUnitCaptured || !t.matchesType(u) {
			return false
		}
		t.Args[0]--
		if t.Args[0] <= 0 {
			t.Completed = true
		}
		return t.Completed
	}
	return false
}

// worldPixel narrows a 16.16 world coordinate to the signed integer world
// units the authored thresholds are expressed in [08 "Evaluation"] C17.
func worldPixel(v interface{ Int() int64 }) int32 {
	return int32(v.Int())
}

// withinBoundary reports the ±2 tolerance of C17: satisfied when the absolute
// difference is strictly below three [08 "Evaluation"].
func withinBoundary(pos, threshold int32) bool {
	d := pos - threshold
	if d < 0 {
		d = -d
	}
	return d < 3
}

// Evaluate runs one tick of the victory and defeat queues [08 "Evaluation"].
//
// Victory is an AND across its queue, defeat is an OR, and victory is
// evaluated first so a tick on which both would fire resolves as a victory.
// The tick site calls this in the LOCAL player's once-per-30-tick slice and
// only for mission type 1 [08 "Evaluation"] — the caller owns that cadence,
// this function owns the combination.
//
// An empty victory queue does not win: the mission builder injects the default
// destroy-all-units condition when the mission authors none, and the default
// all-units-killed condition when it authors no defeat condition (C16).
func Evaluate(victory, defeat []*Trigger, c PollContext) (victoryDone, defeatDone bool) {
	for _, t := range victory {
		if t != nil {
			t.Poll(c)
		}
	}
	for _, t := range defeat {
		if t != nil {
			t.Poll(c)
		}
	}
	victoryDone = len(victory) > 0
	for _, t := range victory {
		if t == nil || !t.Completed {
			victoryDone = false
			break
		}
	}
	for _, t := range defeat {
		if t != nil && t.Completed {
			defeatDone = true
			break
		}
	}
	// Victory is evaluated first, so simultaneous resolves as a victory.
	if victoryDone {
		defeatDone = false
	}
	return victoryDone, defeatDone
}

// NotifyAll drives a gameplay event into both queues [08 "Evaluation"].
func NotifyAll(victory, defeat []*Trigger, c PollContext, ev NotifyEvent, u *units.Unit) {
	for _, t := range victory {
		if t != nil {
			t.Notify(c, ev, u)
		}
	}
	for _, t := range defeat {
		if t != nil {
			t.Notify(c, ev, u)
		}
	}
}
