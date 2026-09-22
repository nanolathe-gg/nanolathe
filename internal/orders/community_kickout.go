package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// MobileBuildBlockedVisitLimit is the blocked-site visit with a policy-owned
// inclusive wait limit. Retail passes 10; enabled CP-CON-1 passes 20. The
// comparison remains strictly greater, and every accepted visit increments
// once and waits exactly 30 ticks [04 R-ORD-01 §5]
// [community patch engine behavior §5.6].
func MobileBuildBlockedVisitLimit(n *Node, tick, limit uint32) (statusText string, code Code) {
	if limit == MobileBuildBlockedGiveUpAbove {
		return MobileBuildBlockedVisit(n, tick)
	}
	if n == nil {
		return MobileBuildBlockedText, 8
	}
	if n.Param3 > limit {
		return MobileBuildBlockedText, 8
	}
	first := n.Param3 == 0
	n.Param3++
	n.DynamicGate |= gateDeadline
	n.Deadline = int32(tick + MobileBuildBlockedWaitTicks)
	if first {
		return MobileBuildWaitingText, 2
	}
	return "", 2
}

// KickoutRewrite applies CP-CON-1's four queue branches. already is the
// service-owned destination-record match; the record table itself stays with
// construction so this package owns only its order list [community patch
// engine behavior §5.6].
func KickoutRewrite(u *units.Unit, x, y, z numeric.Fixed, tick uint32, already bool) bool {
	if u == nil {
		return false
	}
	q := QueueForUnit(u)
	if q == nil {
		return false
	}
	moveID := rowMoveGround
	if u.Def != nil && u.Def.CanFly {
		moveID = rowVTOLMove
	}
	if moveID == 0 {
		return false
	}
	move := NewNodeForOrder(moveID, 0, x, y, z, tick, u.Handle, false)
	old := q.Head()
	if old == nil {
		q.Push(moveID, move)
		return q.Head() != nil
	}

	// A fresh build stays in the queue and resumes after the move.
	if IsMobileBuild(old.ID) {
		target := lookupKickoutTarget(q, old.Target)
		if target == nil || target.Remaining == 1 {
			old.Phase = 0
			q.PushHead(moveID, move)
			return true
		}
	}

	// A position-less order is replaced and its successors are dropped.
	if old.GoalX == 0 && old.GoalY == 0 && old.GoalZ == 0 {
		q.CancelAll()
		q.Push(moveID, move)
		return q.Head() != nil
	}

	tail := append([]*Node(nil), q.primary[1:]...)
	q.primary = q.primary[:1]
	q.RemovePrimaryNode(old, false)
	if already {
		q.primary = []*Node{newNode(moveID, move)}
		q.primary = append(q.primary, tail...)
		return true
	}

	// Preserve a work target and position through a substitute behind the move.
	replacementID := old.ID
	if IsMobileBuild(old.ID) {
		replacementID = rowRepairUnit
		if u.Def != nil && u.Def.CanFly {
			replacementID = rowVTOLRepairUnit
		}
	}
	replacement := NewNodeForOrder(replacementID, old.Target, old.GoalX, old.GoalY, old.GoalZ, tick, u.Handle, false)
	q.primary = []*Node{newNode(moveID, move), newNode(replacementID, replacement)}
	q.primary = append(q.primary, tail...)
	return true
}

func lookupKickoutTarget(q *Queue, h pool.Handle) *units.Unit {
	if q == nil || h == 0 || q.binding == nil || q.binding.Lookup == nil {
		return nil
	}
	return q.binding.Lookup(h)
}
