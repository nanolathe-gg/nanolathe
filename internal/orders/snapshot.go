package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	Owner               pool.Handle
	Target              pool.Handle
	GoalX, GoalY, GoalZ numeric.Fixed
	// DescriptorID names the row. The descriptor's Name and StateLabel used to
	// be copied in beside it, which put two more pointers in every element of
	// an array rebuilt for every unit every tick — pointers the collector then
	// had to scan. They are a constant function of this id: a reader resolves
	// them with DescriptorFor at the point of use, exactly as the frame's
	// order view now does.
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
	BuildFacing            units.StructureFacing
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
	return SnapshotQueueInto(SnapshotQueue{}, q, unit, route)
}

// SnapshotQueueInto is SnapshotQueueOf with caller-owned destination storage.
// The publication boundary calls it once per unit per tick, so allocating both
// segment arrays and every node's route each time was a third of everything
// the simulation allocated (docs/SIM_BENCHMARK.md). Pass back the value a
// previous call returned and its slices are refilled in place.
//
// The returned value aliases dst's arrays, so a caller must consume it before
// the next call — which the publication boundary does: it copies straight into
// the committed frame's own reused storage.
func SnapshotQueueInto(dst SnapshotQueue, q *Queue, unit pool.Handle, route RouteProvider) SnapshotQueue {
	out := SnapshotQueue{Unit: unit, Primary: dst.Primary[:0], Secondary: dst.Secondary[:0]}
	if q == nil {
		return out
	}
	out.Primary, out.PrimaryTruncated = snapshotList(out.Primary, q.primary, 0, route)
	out.Secondary, out.SecondaryTruncated = snapshotList(out.Secondary, q.secondary, 1, route)
	return out
}

func snapshotList(dst []SnapshotNode, src []*Node, list uint8, route RouteProvider) ([]SnapshotNode, bool) {
	dst = dst[:0]
	if len(src) == 0 {
		return dst, false
	}
	n := len(src)
	truncated := false
	if n > MaxSnapshotOrdersPerList {
		n = MaxSnapshotOrdersPerList
		truncated = true
	}
	if cap(dst) < n {
		grown := make([]SnapshotNode, n)
		copy(grown, dst[:cap(dst)])
		dst = grown
	}
	dst = dst[:n]
	for i := 0; i < n; i++ {
		// Keep the element's route storage across the rewrite; everything
		// else is overwritten unconditionally below.
		reusedRoute := dst[i].Route[:0]
		node := src[i]
		if node == nil {
			dst[i] = SnapshotNode{List: list, Index: uint16(i), Route: reusedRoute}
			continue
		}
		dst[i] = SnapshotNode{
			Owner: node.Owner, Target: node.Target,
			GoalX: node.GoalX, GoalY: node.GoalY, GoalZ: node.GoalZ,
			DescriptorID: int32(node.ID), Phase: node.Phase,
			MoveState: node.MoveState, List: list, Index: uint16(i),
			CreationTick: node.CreationTick, Flags: node.Flags,
			DynamicGate: node.DynamicGate, Deadline: node.Deadline,
			Satisfied: node.Satisfied, PathStatus: node.PathStatus,
			Param1: node.Param1, Param2: node.Param2, Param3: node.Param3,
			BuildProduct: node.BuildDefKey, BuildCount: node.Param2, BuildFacing: node.BuildFacing,
			Route: reusedRoute,
		}
		if route != nil {
			dst[i].Route = cloneRouteInto(reusedRoute, route(node))
			dst[i].RouteTruncated = len(dst[i].Route) > MaxSnapshotRoutePoints
			if dst[i].RouteTruncated {
				dst[i].Route = dst[i].Route[:MaxSnapshotRoutePoints]
			}
		}
	}
	return dst, truncated
}

// cloneRouteInto copies src into dst's storage, growing it when it is too
// small. An empty source keeps the (emptied) destination rather than returning
// nil, so the element's buffer survives to the next publication.
func cloneRouteInto(dst, src []SnapshotRoutePoint) []SnapshotRoutePoint {
	if cap(dst) < len(src) {
		dst = make([]SnapshotRoutePoint, len(src))
	}
	dst = dst[:len(src)]
	copy(dst, src)
	return dst
}
