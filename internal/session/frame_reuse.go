package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// reserveUnitView extends the destination by one element WITHOUT clearing it,
// so the caller can save the nested piece and cargo storage Frame.Reset left
// there and then write the view straight into the element. A frame without
// reserved capacity still grows (append), so a session published before any
// Frame.Reserve call works.
//
// The publication used to build a UnitView as a value and hand it here to be
// copied in. The struct is 256 bytes and the loop runs once per live unit per
// tick, so that copy was one of the largest single costs in the publication;
// writing through the element removes it. The element's contents are fully
// overwritten by the caller's struct assignment, exactly as the copy did.
func reserveUnitView(dst []frame.UnitView) []frame.UnitView {
	if i := len(dst); i < cap(dst) {
		return dst[:i+1]
	}
	return append(dst, frame.UnitView{})
}

// reserveFeatureView is the same reservation for the feature channel, whose
// view is 208 bytes and whose loop runs once per live feature per tick -- six
// thousand of them on the benchmark's map.
func reserveFeatureView(dst []frame.FeatureView) []frame.FeatureView {
	if i := len(dst); i < cap(dst) {
		return dst[:i+1]
	}
	return append(dst, frame.FeatureView{})
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
		// The row's displayed name and state label are a constant function of
		// its descriptor id, so the snapshot carries the id and this boundary
		// resolves the two strings. The frame's order view keeps both fields
		// and both keep their exact previous values [I6].
		descriptor := orders.DescriptorFor(orders.ID(n.DescriptorID))
		var footX, footZ int8
		if cat != nil && n.BuildProduct != "" {
			if def, ok := cat.Unit(n.BuildProduct); ok && def != nil {
				footX, footZ = int8(def.FootprintX), int8(def.FootprintZ)
				if n.BuildFacing&1 != 0 {
					footX, footZ = footZ, footX
				}
			}
		}
		dst[i] = frame.OrderView{
			Unit: n.Owner, Target: n.Target,
			GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ,
			Kind: descriptor.Name, StateLabel: descriptor.StateLabel, MoveState: n.MoveState,
			List: n.List, Index: n.Index, DescriptorID: n.DescriptorID,
			Phase: n.Phase, CreationTick: n.CreationTick, Flags: n.Flags,
			DynamicGate: n.DynamicGate, Deadline: n.Deadline,
			Satisfied: n.Satisfied, PathStatus: n.PathStatus,
			Param1: n.Param1, Param2: n.Param2, Param3: n.Param3,
			BuildProduct: n.BuildProduct, BuildCount: n.BuildCount,
			BuildFacing: uint8(n.BuildFacing),
			FootX:       footX, FootZ: footZ, RouteTruncated: n.RouteTruncated,
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
