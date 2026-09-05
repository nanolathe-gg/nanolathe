package orders

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// SnapshotRoutePoint is a value-only copy of one authoritative route point.
// The orders package deliberately does not depend on movement: the caller
// supplies route points through RouteProvider at the publication boundary.
type SnapshotRoutePoint struct {
	X, Y, Z numeric.Fixed
	Flags   uint8
}

// SnapshotNode is the complete presentation copy of an order node. It keeps
// the descriptor payload intact so the client can render the queue without
// dereferencing an authoritative node or reconstructing state from a head.
// Param1..3 and BuildCount are retained even where a descriptor's meaning is
// unresolved; presentation must not invent icons or draw masks.
type SnapshotNode struct {
	Owner                  pool.Handle
	Target                 pool.Handle
	GoalX, GoalY, GoalZ    numeric.Fixed
	Kind                   string
	State                  string
	DescriptorID           int32
	Phase                  uint8
	MoveState              uint8
	List                   uint8
	Index                  uint16
	CreationTick           uint32
	Flags                  uint32
	DynamicGate            uint32
	Deadline               int32
	Satisfied              uint32
	PathStatus             uint32
	Param1, Param2, Param3 uint32
	BuildProduct           string
	BuildCount             uint32
	Route                  []SnapshotRoutePoint
	RouteTruncated         bool
}

// SnapshotQueue is a bounded, immutable-at-publication copy of both retail
// queue segments. The producer order is preserved; no map or descriptor sort
// occurs here [04 §3.2–3.4][07 §9].
type SnapshotQueue struct {
	Unit               pool.Handle
	Primary            []SnapshotNode
	Secondary          []SnapshotNode
	PrimaryTruncated   bool
	SecondaryTruncated bool
}

const (
	// These are presentation bounds only. Retail queue storage remains dynamic;
	// the bound protects the snapshot hand-off from malformed input [SC17].
	MaxSnapshotOrdersPerList = 4096 // records copied per segment
	MaxSnapshotRoutePoints   = 4096 // route points copied per record
)

// RouteProvider returns the currently authoritative route for node. It may
// return nil when no route is established for the node. Snapshot copies all
// returned points before returning, so neither the provider nor its route can
// alias the published value.
type RouteProvider func(node *Node) []SnapshotRoutePoint

// SnapshotQueueOf copies both queue chains, including descriptor payload,
// build count, and any route supplied by the authoritative movement owner.
// The queue itself remains untouched. Truncation is explicit and deterministic
// when a presentation bound is exceeded.
func SnapshotQueueOf(q *Queue, unit pool.Handle, route RouteProvider) SnapshotQueue {
	out := SnapshotQueue{Unit: unit}
	if q == nil {
		return out
	}
	out.Primary, out.PrimaryTruncated = snapshotList(q.primary, 0, route)
	out.Secondary, out.SecondaryTruncated = snapshotList(q.secondary, 1, route)
	return out
}

func snapshotList(src []*Node, list uint8, route RouteProvider) ([]SnapshotNode, bool) {
	if len(src) == 0 {
		return nil, false
	}
	n := len(src)
	truncated := false
	if n > MaxSnapshotOrdersPerList {
		n = MaxSnapshotOrdersPerList
		truncated = true
	}
	dst := make([]SnapshotNode, n)
	for i := 0; i < n; i++ {
		node := src[i]
		if node == nil {
			dst[i].List = list
			dst[i].Index = uint16(i)
			continue
		}
		d := DescriptorFor(node.ID)
		dst[i] = SnapshotNode{
			Owner: node.Owner, Target: node.Target,
			GoalX: node.GoalX, GoalY: node.GoalY, GoalZ: node.GoalZ,
			Kind: d.Name, State: d.StateLabel,
			DescriptorID: int32(node.ID), Phase: node.Phase,
			MoveState: node.MoveState, List: list, Index: uint16(i),
			CreationTick: node.CreationTick, Flags: node.Flags,
			DynamicGate: node.DynamicGate, Deadline: node.Deadline,
			Satisfied: node.Satisfied, PathStatus: node.PathStatus,
			Param1: node.Param1, Param2: node.Param2, Param3: node.Param3,
			BuildProduct: node.BuildDefKey, BuildCount: node.Param2,
		}
		if route != nil {
			dst[i].Route = cloneRoute(route(node))
			dst[i].RouteTruncated = len(dst[i].Route) > MaxSnapshotRoutePoints
			if dst[i].RouteTruncated {
				dst[i].Route = dst[i].Route[:MaxSnapshotRoutePoints]
			}
		}
	}
	return dst, truncated
}

func cloneRoute(src []SnapshotRoutePoint) []SnapshotRoutePoint {
	if len(src) == 0 {
		return nil
	}
	dst := make([]SnapshotRoutePoint, len(src))
	copy(dst, src)
	return dst
}
