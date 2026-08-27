package session

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/orders"
)

// appendUnitView reuses the destination unit and nested piece storage left by
// Frame.Reset. The source value is copied only after its destination slot's
// retained pieces have been saved. A frame without reserved capacity still
// grows (append), so a session published before any Frame.Reserve call works.
func appendUnitView(dst []frame.UnitView, src frame.UnitView) []frame.UnitView {
	i := len(dst)
	if i < cap(dst) {
		dst = dst[:i+1]
	} else {
		dst = append(dst, frame.UnitView{})
	}
	pieces := dst[i].Pieces
	dst[i] = src
	dst[i].Pieces = pieces[:0]
	return dst
}

func appendOrderQueueView(dst []frame.OrderQueueView, src orders.SnapshotQueue, cat *content.Catalog) []frame.OrderQueueView {
	i := len(dst)
	if i < cap(dst) {
		dst = dst[:i+1]
	} else {
		dst = append(dst, frame.OrderQueueView{})
	}
	primary, secondary := dst[i].Primary, dst[i].Secondary
	dst[i] = frame.OrderQueueView{
		Unit: src.Unit, PrimaryTruncated: src.PrimaryTruncated, SecondaryTruncated: src.SecondaryTruncated,
		Primary:   snapshotOrderViewsInto(primary, src.Primary, cat),
		Secondary: snapshotOrderViewsInto(secondary, src.Secondary, cat),
	}
	return dst
}

func snapshotOrderViewsInto(dst []frame.OrderView, src []orders.SnapshotNode, cat *content.Catalog) []frame.OrderView {
	if cap(dst) < len(src) {
		dst = make([]frame.OrderView, 0, len(src))
	}
	dst = dst[:0]
	for _, n := range src {
		i := len(dst)
		dst = dst[:i+1]
		route := dst[i].Route
		var footX, footZ int8
		if cat != nil && n.BuildProduct != "" {
			if def, ok := cat.Unit(n.BuildProduct); ok && def != nil {
				footX, footZ = int8(def.FootprintX), int8(def.FootprintZ)
			}
		}
		dst[i] = frame.OrderView{
			Unit: n.Owner, Target: n.Target,
			GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ,
			Kind: n.Kind, StateLabel: n.State, MoveState: n.MoveState,
			List: n.List, Index: n.Index, DescriptorID: n.DescriptorID,
			Phase: n.Phase, CreationTick: n.CreationTick, Flags: n.Flags,
			DynamicGate: n.DynamicGate, Deadline: n.Deadline,
			Satisfied: n.Satisfied, PathStatus: n.PathStatus,
			Param1: n.Param1, Param2: n.Param2, Param3: n.Param3,
			BuildProduct: n.BuildProduct, BuildCount: n.BuildCount,
			FootX: footX, FootZ: footZ, RouteTruncated: n.RouteTruncated,
			Route: route[:0],
		}
		for _, p := range n.Route {
			dst[i].Route = append(dst[i].Route, frame.RoutePoint{X: p.X, Y: p.Y, Z: p.Z, Flags: p.Flags})
		}
	}
	return dst
}

func copyBytesInto(dst, src []uint8) []uint8 {
	if cap(dst) < len(src) {
		dst = make([]uint8, 0, len(src))
	}
	dst = dst[:len(src)]
	copy(dst, src)
	return dst
}

func copyWordsInto(dst, src []uint16) []uint16 {
	if cap(dst) < len(src) {
		dst = make([]uint16, 0, len(src))
	}
	dst = dst[:len(src)]
	copy(dst, src)
	return dst
}

func copyIntsInto(dst, src []int) []int {
	if cap(dst) < len(src) {
		dst = make([]int, 0, len(src))
	}
	dst = dst[:len(src)]
	copy(dst, src)
	return dst
}

func copyScoresInto(dst []frame.ResultScore, src []frame.ResultScore) []frame.ResultScore {
	if cap(dst) < len(src) {
		dst = make([]frame.ResultScore, 0, len(src))
	}
	dst = dst[:len(src)]
	copy(dst, src)
	return dst
}
