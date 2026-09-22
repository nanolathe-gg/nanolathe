package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// CommunityOrderDragReceipt is the immutable committed-frame identity of one
// primary order. The patch keeps a raw record pointer while dragging; across
// Nanolathe's command boundary the list position and immutable payload are the
// smallest equivalent identity already present in the order snapshot.
type CommunityOrderDragReceipt struct {
	Unit                pool.Handle
	Index               uint16
	DescriptorID        int32
	CreationTick        uint32
	Target              pool.Handle
	GoalX, GoalY, GoalZ numeric.Fixed
	BuildProduct        string
	BuildFacing         uint8
}

// CommunityOrderDragDestination is the cursor world point captured by the
// host. Build orders may replace it with the oriented placement centre and
// canonical site height through resolve.
type CommunityOrderDragDestination struct{ X, Y, Z numeric.Fixed }

// CommunityOrderDragResolve validates and, for builds, canonicalises a drag
// destination. It runs after an active head's movement payload is released,
// matching the extension's interruption-before-build-test order.
type CommunityOrderDragResolve func(*Node, CommunityOrderDragDestination) (CommunityOrderDragDestination, bool)

// DragCommunityOrder rewrites one still-identical queued record in place. It
// never removes or reorders records. A changed queue is refused, preventing a
// delayed host command from moving whichever record later occupies the same
// index [community patch engine behavior §5.11].
func DragCommunityOrder(q *Queue, receipt CommunityOrderDragReceipt, destination CommunityOrderDragDestination, resolve CommunityOrderDragResolve) bool {
	if q == nil || receipt.Unit == 0 || int(receipt.Index) >= len(q.primary) {
		return false
	}
	n := q.primary[receipt.Index]
	if !communityOrderDragMatch(n, receipt) || !CommunityOrderDraggable(n) {
		return false
	}
	if receipt.Index == 0 && q.binding != nil {
		// The extension stages the unit's current point and invokes the engine's
		// ground-move entry to interrupt the active head. Nanolathe's movement
		// port represents that interruption as Release: the bound adapter cancels
		// the path request, detaches and destroys the record goal, and clears the
		// route's active/repath state. Invalid build placement below puts the
		// receipt position back; accepted destinations replace it.
		if q.binding.Lookup != nil {
			if u := q.binding.Lookup(n.Owner); u != nil {
				n.GoalX, n.GoalY, n.GoalZ = u.X, u.Y, u.Z
			}
		}
		if q.binding.Movement != nil && q.binding.Movement.Release != nil {
			q.binding.Movement.Release(n)
		}
	}
	if resolve != nil {
		var ok bool
		destination, ok = resolve(n, destination)
		if !ok {
			n.GoalX, n.GoalY, n.GoalZ = receipt.GoalX, receipt.GoalY, receipt.GoalZ
			return false
		}
	}
	n.Phase = 0
	n.GoalX, n.GoalY, n.GoalZ = destination.X, destination.Y, destination.Z
	return true
}

func communityOrderDragMatch(n *Node, receipt CommunityOrderDragReceipt) bool {
	return n != nil && n.Owner == receipt.Unit && int32(n.ID) == receipt.DescriptorID &&
		n.CreationTick == receipt.CreationTick && n.Target == receipt.Target &&
		n.GoalX == receipt.GoalX && n.GoalY == receipt.GoalY && n.GoalZ == receipt.GoalZ &&
		n.BuildDefKey == receipt.BuildProduct && uint8(n.BuildFacing) == receipt.BuildFacing
}

// CommunityOrderDraggable is the source's targetless build/move, patrol and
// unload cursor set. Work and attack records are deliberately excluded even
// when they happen to carry a ground point.
func CommunityOrderDraggable(n *Node) bool {
	if n == nil || n.Target != 0 {
		return false
	}
	if IsMobileBuild(n.ID) && n.BuildDefKey != "" {
		return true
	}
	switch DescriptorFor(n.ID).Name {
	case "Move_Ground", "VTOL_Move", "QMove",
		"Patrol", "VTOL_Patrol", "QPatrol", "RepairPatrol", "VTOL_RepairPatrol",
		"Ground_Unload", "VTOL_Unload":
		return true
	default:
		return false
	}
}
