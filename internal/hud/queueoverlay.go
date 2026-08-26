package hud

// The queue overlay is deliberately a pure snapshot consumer.  The retail
// walker reads live order records, but the presentation boundary publishes a
// complete immutable queue [04 §3][07 §9].  This adapter therefore returns
// draw instructions and never retains a pointer into simulation state.

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// QueueOverlayMask is the five-bit helper mask used by retail's descriptor
// walker [07 §9][R-P0-11 §3].  The runtime writer for descriptor mask bytes is
// not recovered; only the established/support-inference mappings below are
// enabled.
type QueueOverlayMask uint32

const (
	QueueMarkerMask QueueOverlayMask = 1 << iota
	QueueDashMask
	QueueCircleMask
	QueueIconMask
	QueueRangeMask
)

// QueuePrimitiveKind identifies a presentation-only instruction.
type QueuePrimitiveKind uint8

const (
	QueuePrimitiveMarker QueuePrimitiveKind = iota + 1
	QueuePrimitiveDash
	QueuePrimitiveCircle
	QueuePrimitiveIcon
)

// QueuePoint is an integer screen-space point.  Integer points preserve the
// retail line primitive's truncation boundary [03 §2.5].
type QueuePoint struct{ X, Y int32 }

// QueueRect is a projected footprint rectangle.  The integration adapter
// supplies it because camera/view origin policy belongs to the client.
type QueueRect struct{ Left, Top, Right, Bottom int32 }

// QueuePrimitive is one immutable draw instruction.  Marker contains the
// eight lines from BuildMarkerSegments; Dash and Circle contain one segment.
// DashAge is intentionally retained rather than converted to a guessed
// procedural pattern: the authored GAF dash chain and ticks-per-frame are
// resolved by the client asset adapter [R-P0-11 §3].
type QueuePrimitive struct {
	Kind       QueuePrimitiveKind
	Unit       pool.Handle
	List       uint8
	Index      uint16
	OrderKind  string
	Mask       QueueOverlayMask
	Selected   bool
	Color      uint8
	ColorKnown bool
	A, B       QueuePoint
	Center     QueuePoint
	Radius     int32
	Segments   []MarkerSegment
	DashAge    uint32
	IconFrame  int32
	IconKnown  bool
}

// QueueOverlayOptions supplies the two camera/content facts not carried by a
// generic snapshot.  A missing callback suppresses only the affected helper;
// it never invents a footprint, range, circle radius, or GAF frame.
type QueueOverlayOptions struct {
	Tick        uint32
	ShiftHeld   bool
	LocalOwner  uint8
	HoveredUnit pool.Handle
	Project     func(x, y, z numeric.Fixed) QueuePoint
	BuildRect   func(snapshot.OrderView) (QueueRect, bool)
	Circle      func(snapshot.OrderView) (int32, bool)
	Icon        func(snapshot.OrderView, uint32) (int32, bool)
}

// QueueOverlay returns stable queue instructions while Shift is held.  The
// release path returns nil and performs no mutation, so it cannot alter an
// authoritative state or hash [07 §9][R-P0-11 §4].
func QueueOverlay(frame *snapshot.Frame, opt QueueOverlayOptions) []QueuePrimitive {
	if frame == nil || !opt.ShiftHeld || opt.Project == nil {
		return nil
	}

	units := make(map[pool.Handle]snapshot.UnitView, len(frame.Units))
	for _, u := range frame.Units {
		if (u.MaxHealth > 0 || u.Health > 0 || u.Slot != 0) && u.Owner == opt.LocalOwner {
			if u.Slot == 0 {
				continue
			}
			units[u.Slot] = u
		}
	}
	selected := make(map[pool.Handle]bool, len(frame.Selection.Handles))
	for _, h := range frame.Selection.Handles {
		selected[h] = true
	}
	// Queue overlays walk the local slice.  The marker-only path is only
	// meaningful when the immutable command page identifies a builder context
	// or a selected queue already contains a build order [R-P0-11 §3].
	// IsBuilding alone is insufficient: factories, mexes, and other fixed
	// structures are not necessarily builders.
	hasBuilder := false
	if frame.CommandPage.Builder != 0 {
		_, hasBuilder = units[frame.CommandPage.Builder]
	}
	if !hasBuilder {
		for h := range selected {
			if _, ok := units[h]; ok && hasBuildOrder(frame.OrderQueues, h) {
				hasBuilder = true
				break
			}
		}
	}

	var out []QueuePrimitive
	for _, q := range frame.OrderQueues {
		_, ok := units[q.Unit]
		if !ok {
			continue
		}
		isSelected := selected[q.Unit] || q.Unit == opt.HoveredUnit
		mask := QueueOverlayMask(QueueMarkerMask)
		if isSelected {
			mask = QueueMarkerMask | QueueDashMask | QueueCircleMask | QueueIconMask | QueueRangeMask
		} else if !hasBuilder {
			continue
		}
		u := units[q.Unit]
		prev := opt.Project(u.X, u.Y, u.Z)
		for list, orders := range [][]snapshot.OrderView{q.Primary, q.Secondary} {
			for _, order := range orders {
				orderMask := queueOrderMask(order.Kind) & mask
				if orderMask == 0 {
					continue
				}
				points := orderPoints(order, opt.Project)
				if len(points) == 0 {
					points = []QueuePoint{opt.Project(order.GoalX, order.GoalY, order.GoalZ)}
				}
				if orderMask&QueueDashMask != 0 {
					for _, p := range points {
						// The helper's GAF artwork and palette entry are not yet
						// carried by the immutable frame. Keep the exact creation
						// age in the instruction, but mark its raster color as
						// unresolved so an integration cannot draw a guessed solid
						// line in its place [R-P0-11 §3].
						out = append(out, QueuePrimitive{Kind: QueuePrimitiveDash, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, A: prev, B: p, DashAge: age(opt.Tick, order.CreationTick)})
						prev = p
					}
				} else {
					prev = points[len(points)-1]
				}
				if orderMask&QueueMarkerMask != 0 && order.BuildProduct != "" && opt.BuildRect != nil {
					if rect, ok := opt.BuildRect(order); ok {
						segments := BuildMarkerSegments(rect.Left, rect.Top, rect.Right, rect.Bottom, int(age(opt.Tick, order.CreationTick)), isSelected)
						out = append(out, QueuePrimitive{Kind: QueuePrimitiveMarker, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Color: segments[0].Color, ColorKnown: true, Segments: segments})
					}
				}
				if orderMask&QueueCircleMask != 0 && opt.Circle != nil {
					if radius, ok := opt.Circle(order); ok && radius > 0 {
						center := points[len(points)-1]
						for _, chord := range circle15(center, radius) {
							// The runtime color-index block is unresolved. Preserve
							// geometry only; the client must suppress this primitive
							// until authored color data is supplied [R-P0-11 §3].
							out = append(out, QueuePrimitive{Kind: QueuePrimitiveCircle, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, A: chord[0], B: chord[1], Center: center, Radius: radius})
						}
					}
				}
				if orderMask&QueueIconMask != 0 && opt.Icon != nil {
					if icon, ok := opt.Icon(order, opt.Tick); ok {
						center := points[len(points)-1]
						out = append(out, QueuePrimitive{Kind: QueuePrimitiveIcon, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Center: center, IconFrame: icon, IconKnown: true})
					}
				}
			}
		}
	}
	return out
}

func hasBuildOrder(queues []snapshot.OrderQueueView, unit pool.Handle) bool {
	for _, q := range queues {
		if q.Unit != unit {
			continue
		}
		for _, list := range [][]snapshot.OrderView{q.Primary, q.Secondary} {
			for _, o := range list {
				if o.BuildProduct != "" {
					return true
				}
			}
		}
	}
	return false
}

func queueOrderMask(kind string) QueueOverlayMask {
	switch kind {
	case "MobileBuild":
		return QueueMarkerMask | QueueDashMask | QueueRangeMask
	case "VTOL_MobileBuild":
		return QueueMarkerMask | QueueDashMask
	case "Move_Ground", "Patrol":
		return QueueDashMask | QueueRangeMask
	case "QMove", "QPatrol":
		return QueueDashMask
	default:
		// TODO(question): runtime descriptor mask writer and attack-family
		// helper assignments are unresolved [R-P0-11 §3]. Suppress them rather
		// than guess a line/icon/color.
		return 0
	}
}

func orderPoints(o snapshot.OrderView, project func(numeric.Fixed, numeric.Fixed, numeric.Fixed) QueuePoint) []QueuePoint {
	points := make([]QueuePoint, 0, len(o.Route)+1)
	for _, route := range o.Route {
		points = append(points, project(route.X, route.Y, route.Z))
	}
	goal := project(o.GoalX, o.GoalY, o.GoalZ)
	if len(points) == 0 || points[len(points)-1] != goal {
		points = append(points, goal)
	}
	return points
}

func age(now, born uint32) uint32 {
	if now < born {
		return 0
	}
	return now - born
}

// OrderCircleSegments emits the established fifteen-segment chord
// approximation. The radius is supplied by immutable authored data through
// QueueOverlayOptions; absent data suppresses this helper [07 §9][R-P0-11 §3].
// The integer unit-circle table keeps this presentation primitive out of the
// authoritative float domain [INVARIANTS I2].
func OrderCircleSegments(center QueuePoint, radius int32) [][2]QueuePoint {
	return circle15(center, radius)
}

func circle15(center QueuePoint, radius int32) [][2]QueuePoint {
	const n = 15
	const unit = int32(1024)
	// 24-degree steps, rounded once to the integer presentation grid.
	cosine := [...]int32{1024, 936, 685, 318, -107, -522, -828, -1011, -1011, -828, -522, -107, 318, 685, 936}
	sine := [...]int32{0, 416, 762, 974, 1018, 881, 601, 213, -213, -601, -881, -1018, -974, -762, -416}
	out := make([][2]QueuePoint, n)
	pts := make([]QueuePoint, n)
	for i := range pts {
		pts[i] = QueuePoint{center.X + radius*cosine[i]/unit, center.Y + radius*sine[i]/unit}
	}
	for i := range out {
		out[i] = [2]QueuePoint{pts[i], pts[(i+1)%n]}
	}
	return out
}
