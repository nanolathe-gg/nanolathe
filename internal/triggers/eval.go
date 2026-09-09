package triggers

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// PollContext supplies session-owned state to the trigger dispatch slots.
// Mission trigger owner predicates are the literal slots 0 and 1; the two
// owner fields remain for source compatibility and are not consulted by the
// canonical kind-1 evaluator [08 R-TRIG-01 §3].
type PollContext struct {
	Tick  uint32
	World *units.World

	LocalOwner uint8
	EnemyOwner uint8
	// NotificationOwner carries the pre-transfer owner at the capture slot.
	// Removal and creation notifications leave the valid flag clear and read
	// the subject record directly [08 R-TRIG-01 §7].
	NotificationOwner      uint8
	NotificationOwnerValid bool

	// MissionArmed is the kind-1 mission object's armed flag. The spawner
	// clears it when the mission has no authored unit records [08 R-TRIG-01 §6].
	MissionArmed bool
	// StampedCell returns the committed footprint anchor cell. Boundary
	// triggers read this stamp rather than deriving a cell from the unit centre
	// [08 R-TRIG-01 §3].
	StampedCell func(*units.Unit) (x, z int16, ok bool)
	// Deproject converts authored projected map pixels to 16.16 world
	// coordinates on the first MoveUnitToRadius poll [08 R-TRIG-01 §5].
	Deproject func(x, z int32) (worldX, worldY, worldZ int32)
	// IsCommander resolves the unit owner's side commander name through the
	// authoritative side table [08 R-TRIG-01 §3].
	IsCommander func(*units.Unit) bool
	// Celebrate publishes the authored "Victory Condition" sound cue. A nil
	// callback is the established missing-alias silence [08 R-TRIG-01 §8].
	Celebrate func()
}

// NotifyEvent identifies the notification slot being driven [08 R-TRIG-01 §2].
type NotifyEvent uint8

const (
	NotifyUnitDied NotifyEvent = iota
	NotifyUnitCaptured
	NotifyUnitCreated
)

// cargoSelectableStatus is bit 30 of the unit status word, a static mirror of
// the carrier definition's `isairbase` flag: internal/units seeds it at unit
// creation and nothing writes it afterward [04 R-UNIT-06 §3]. The carrier
// clause below reads it off the carrier's own Flags word, never the cargo's.
const cargoSelectableStatus uint32 = 0x40000000

func forEachOccupied(w *units.World, fn func(*units.Unit) bool) {
	if w == nil || fn == nil {
		return
	}
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if !fn(u) {
			return
		}
	}
}

func (t *Trigger) matchesType(u *units.Unit, wildcard bool) bool {
	if t == nil || u == nil || u.Def == nil {
		return false
	}
	if wildcard && t.Type == "" {
		return true
	}
	return strings.EqualFold(u.Def.UnitName, t.Type)
}

func notificationOwner(c PollContext, u *units.Unit) uint8 {
	if c.NotificationOwnerValid {
		return c.NotificationOwner
	}
	return u.Owner
}

func (t *Trigger) complete(c PollContext, celebrate bool) {
	t.Completed = true
	if !celebrate || t.Celebrated {
		return
	}
	if c.Celebrate != nil {
		c.Celebrate()
	}
	t.Celebrated = true
}

func (t *Trigger) celebratePredicate(c PollContext, satisfied bool) {
	if !satisfied || t.Celebrated {
		return
	}
	if c.Celebrate != nil {
		c.Celebrate()
	}
	t.Celebrated = true
}

func eligibleMissionUnit(c PollContext, u *units.Unit) bool {
	if u == nil || !u.Alive || u.Flags&units.ClassifierEligibleStatus == 0 || u.Remaining != 0 {
		return false
	}
	if u.Attachment.Carrier == 0 {
		return true
	}
	if c.World == nil {
		return false
	}
	carrier := c.World.Unit(u.Attachment.Carrier)
	return carrier != nil && carrier.Flags&cargoSelectableStatus != 0
}

func stampedBoundary(c PollContext, u *units.Unit, xAxis bool) (int32, bool) {
	if c.StampedCell == nil {
		return 0, false
	}
	x, z, ok := c.StampedCell(u)
	if !ok {
		return 0, false
	}
	if xAxis {
		return int32(x), true
	}
	return int32(z), true
}

func withinBoundary(cell, threshold int32) bool {
	d := cell - threshold
	if d < 0 {
		d = -d
	}
	return d < 3
}

// wrappedDelta performs the authoritative signed 32-bit coordinate
// subtraction before widening for the 64-bit square [08 R-TRIG-01 §5].
func wrappedDelta(position int64, center int32) int64 {
	return int64(int32(position) - center)
}

// Poll dispatches the tick slot. The caller owns the local-player 30-tick
// cadence; Poll owns only the condition body [08 R-TRIG-01 §2, §4].
func (t *Trigger) Poll(c PollContext) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case KindDestroyAllUnits:
		done := c.World == nil || c.World.LiveCountForPlayer(1) == 0
		t.celebratePredicate(c, done)
		return done // this predicate never stores Satisfied [08 R-TRIG-01 §4].

	case KindBuildUnitType:
		if t.Completed {
			return true
		}
		forEachOccupied(c.World, func(u *units.Unit) bool {
			if u.Owner == 0 && u.Remaining == 0 && t.matchesType(u, false) {
				t.complete(c, true)
				return false
			}
			return true
		})
		return t.Completed

	case KindUnitTypePassesX, KindUnitTypePassesZ:
		if t.Completed {
			return true
		}
		xAxis := t.Kind == KindUnitTypePassesX
		forEachOccupied(c.World, func(u *units.Unit) bool {
			if u.Owner != 0 || !t.matchesType(u, true) {
				return true
			}
			cell, ok := stampedBoundary(c, u, xAxis)
			if ok && withinBoundary(cell, t.Args[0]) {
				t.complete(c, true)
				return false
			}
			return true
		})
		return t.Completed

	case KindAnyUnitPassesX, KindAnyUnitPassesZ:
		if t.Completed {
			return true
		}
		xAxis := t.Kind == KindAnyUnitPassesX
		forEachOccupied(c.World, func(u *units.Unit) bool {
			if u.Owner != 1 {
				return true
			}
			cell, ok := stampedBoundary(c, u, xAxis)
			if ok && withinBoundary(cell, t.Args[0]) {
				t.complete(c, false)
				return false
			}
			return true
		})
		return t.Completed

	case KindAllUnitsKilled:
		t.Completed = true
		forEachOccupied(c.World, func(u *units.Unit) bool {
			if u.Owner == 0 && eligibleMissionUnit(c, u) {
				t.Completed = false
				return false
			}
			return true
		})
		return t.Completed // recomputed every poll [08 R-TRIG-01 §4].

	case KindMoveUnitToRadius:
		if t.Completed {
			return true
		}
		if !t.CenterReady {
			if c.Deproject == nil {
				return false
			}
			t.CenterX, t.CenterY, t.CenterZ = c.Deproject(t.Args[0], t.Args[1])
			t.CenterReady = true
		}
		// The constructed record stores radius<<16 in a signed 32-bit word.
		// A wrapped-negative radius produces an empty partition range.
		radius := int64(int32(uint32(t.Args[2]) << 16))
		if radius < 0 {
			return false
		}
		radiusSquared := (radius * radius) >> 32
		forEachOccupied(c.World, func(u *units.Unit) bool {
			if u.Owner != 0 || !t.matchesType(u, true) || !eligibleMissionUnit(c, u) {
				return true
			}
			dx := wrappedDelta(u.X.Raw(), t.CenterX)
			dz := wrappedDelta(u.Z.Raw(), t.CenterZ)
			if ((dx*dx)>>32)+((dz*dz)>>32) <= radiusSquared {
				t.complete(c, true)
			}
			return true // retail continues through the remaining partition tiles.
		})
		return t.Completed

	case KindVictoryTimerRunsOut, KindDeathTimerRunsOut:
		return uint32(t.Args[0]) <= c.Tick // timers never store Satisfied or celebrate.

	case KindKillEnemyCommander, KindKillAllMobileUnits, KindCaptureUnitType,
		KindKillAllOfType, KindKillUnitType, KindCommanderKilled,
		KindAllUnitsKilledOfType, KindUnitTypeKilled:
		return t.Completed // notification-driven conditions are no-op polls.
	default:
		return false
	}
}

func countMatching(c PollContext, owner int, mobile bool, t *Trigger, bothPrimaryOwners bool) int {
	count := 0
	forEachOccupied(c.World, func(u *units.Unit) bool {
		if bothPrimaryOwners {
			if u.Owner != 0 && u.Owner != 1 {
				return true
			}
		} else if int(u.Owner) != owner {
			return true
		}
		if mobile && (u.Def == nil || u.Def.BMCode != 1) {
			return true
		}
		if t != nil && !t.matchesType(u, false) {
			return true
		}
		count++
		return count < 2
	})
	return count
}

// Notify dispatches a unit notification while the subject is still occupied.
// Capture supplies the pre-transfer owner explicitly; removal reads it from
// the still-owned record [08 R-TRIG-01 §7].
func (t *Trigger) Notify(c PollContext, ev NotifyEvent, u *units.Unit) bool {
	if t == nil || u == nil || u.Def == nil || ev == NotifyUnitCreated {
		return t != nil && t.Completed
	}
	owner := notificationOwner(c, u)
	switch t.Kind {
	case KindKillEnemyCommander:
		if !t.Completed && ev == NotifyUnitDied && owner == 1 && c.IsCommander != nil && c.IsCommander(u) {
			t.complete(c, true)
		}
	case KindKillAllMobileUnits:
		if !t.Completed && ev == NotifyUnitDied && owner == 1 && u.Def.BMCode == 1 && countMatching(c, 1, true, nil, false) < 2 {
			t.complete(c, true)
		}
	case KindCaptureUnitType:
		if !t.Completed && ev == NotifyUnitCaptured && owner == 1 && t.matchesType(u, false) {
			t.complete(c, true)
		}
	case KindKillAllOfType:
		if !t.Completed && ev == NotifyUnitDied && owner == 1 && t.matchesType(u, false) && countMatching(c, 1, false, t, false) < 2 {
			t.complete(c, true)
		}
	case KindKillUnitType:
		if !t.Completed && ev == NotifyUnitDied && owner == 1 && t.matchesType(u, false) && t.Args[0] > 0 {
			t.Args[0]--
			if t.Args[0] < 1 {
				t.complete(c, true)
			}
		}
	case KindCommanderKilled:
		if !t.Completed && ev == NotifyUnitDied && owner == 0 && c.IsCommander != nil && c.IsCommander(u) {
			t.complete(c, false)
		}
	case KindAllUnitsKilledOfType:
		if !t.Completed && ev == NotifyUnitDied && t.matchesType(u, false) && countMatching(c, 0, false, t, true) < 2 {
			t.complete(c, false)
		}
	case KindUnitTypeKilled:
		if ev == NotifyUnitDied && t.matchesType(u, false) {
			t.Args[0]--
			if t.Args[0] < 1 {
				t.complete(c, false)
			}
		}
	}
	return t.Completed
}

// Evaluate evaluates detached trigger queues. Production mission polling uses
// EvaluateOwned so an empty queue receives a persistent default record. This
// value-slice form retains the historical detached fallback for isolated
// condition consumers that cannot install a record on their owner [08
// "Default triggers"] [08 R-TRIG-01 §6].
func Evaluate(victory, defeat []*Trigger, c PollContext) (victoryDone, defeatDone bool) {
	if !c.MissionArmed {
		return false, false
	}
	if len(victory) == 0 {
		victory = []*Trigger{DefaultVictory()}
	}
	if len(defeat) == 0 {
		defeat = []*Trigger{DefaultDefeat()}
	}
	return evaluateQueues(victory, defeat, c)
}

// EvaluateOwned evaluates a mission's trigger queues. Its empty-queue fallback
// appends defaults to the supplied owner before polling, preserving each
// record's satisfied and celebrated state for subsequent polls and saves [08
// "Default triggers"] [08 R-TRIG-01 §8].
func EvaluateOwned(victory, defeat *[]*Trigger, c PollContext) (victoryDone, defeatDone bool) {
	if victory == nil || defeat == nil {
		return false, false
	}
	*victory, *defeat = EnsureDefaults(*victory, *defeat)
	if !c.MissionArmed {
		return false, false
	}
	return evaluateQueues(*victory, *defeat, c)
}

func evaluateQueues(victory, defeat []*Trigger, c PollContext) (victoryDone, defeatDone bool) {
	victoryDone = true
	for _, t := range victory {
		if t == nil || !t.Poll(c) {
			victoryDone = false
			break
		}
	}
	if victoryDone {
		return true, false
	}
	for _, t := range defeat {
		if t != nil && t.Poll(c) {
			return false, true
		}
	}
	return false, false
}

// NotifyAll drives a gameplay event through victory then defeat records in
// builder order. Notifications run for every session kind [08 R-TRIG-01 §1, §7].
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
