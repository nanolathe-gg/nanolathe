package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_UNITS_ORDERS_COB "Modern crowded arrival".
// These limits are prototype tuning, not retail arrival constants.
const crowdedArrivalRadius = 96
const crowdedArrivalDwell uint32 = 90

type crowdedArrivalState struct {
	active          bool
	since, lastTick uint32
	x, z            int32
	goalX, goalZ    numeric.Fixed
}

// CrowdedMoveArrival is called by the ordinary movement visit, including while
// its route is inactive. It never creates a queue or changes a Strict record.
func CrowdedMoveArrival(u *units.Unit, n *Node, tick uint32) bool {
	return rulesOfUnit(u).CrowdedMoveArrival(u, n, tick)
}

func (*ModernRules) CrowdedMoveArrival(u *units.Unit, n *Node, tick uint32) bool {
	if n == nil {
		return false
	}
	q := QueueOfUnit(u)
	if !plainTerminalGroundMove(q, u, n) || u.Move.Speed != 0 {
		n.crowdedArrival = crowdedArrivalState{}
		return false
	}
	// Unsigned subtraction keeps even opposite extreme restored coordinates
	// outside the bound without overflowing a signed difference or square.
	delta := func(a, b numeric.Fixed) uint64 {
		if a < b {
			return uint64(b) - uint64(a)
		}
		return uint64(a) - uint64(b)
	}
	dx, dz := delta(u.X, n.GoalX), delta(u.Z, n.GoalZ)
	radius := uint64(crowdedArrivalRadius * 65536)
	if dx > radius || dz > radius || dx*dx+dz*dz > radius*radius {
		n.crowdedArrival = crowdedArrivalState{}
		return false
	}
	b := q.Binding()
	if b == nil || b.Movement == nil || b.Movement.CrowdedMoveBlocked == nil {
		n.crowdedArrival = crowdedArrivalState{}
		return false
	}
	x, z, blocked := b.Movement.CrowdedMoveBlocked(u, n)
	if !blocked {
		n.crowdedArrival = crowdedArrivalState{}
		return false
	}
	state := &n.crowdedArrival
	if !state.active || state.x != x || state.z != z || state.goalX != n.GoalX || state.goalZ != n.GoalZ || tick-state.lastTick > 1 {
		*state = crowdedArrivalState{active: true, since: tick, lastTick: tick, x: x, z: z, goalX: n.GoalX, goalZ: n.GoalZ}
		return false
	}
	state.lastTick = tick
	return tick-state.since >= crowdedArrivalDwell
}

// plainTerminalGroundMove is the record eligibility the Modern move-completion
// policies share: a sole primary Move_Ground with no target, not produced by
// automatic work and not a danger response or return, on a live, complete,
// unstunned, uncarried ground mover. A move with successors already leaves
// the queue on its first failure through the pump's code 9 [04 §3.3]; Patrol,
// Guard, build and repair approaches keep their own failure handling.
func plainTerminalGroundMove(q *Queue, u *units.Unit, n *Node) bool {
	return q != nil && u != nil && n != nil && u.Def != nil && u.Alive && !u.Dying && !u.Stunned && u.Remaining == 0 && u.Attachment.Carrier == 0 &&
		!u.Def.CanFly && u.Def.BMCode == 1 && u.Def.CanMove &&
		len(q.primary) == 1 && q.primary[0] == n && n.ID == rowMoveGround && n.Target == 0 &&
		!n.automaticWork && n != q.danger.response && n != q.danger.returnMove
}
