package hud

// The queue overlay is deliberately a pure snapshot consumer.  The retail
// walker reads live order records, but the presentation boundary publishes a
// complete immutable queue [04 §3][07 §9].  This adapter therefore returns
// draw instructions and never retains a pointer into simulation state.

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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

// QueueWorldPoint is a world-space point in 16.16 fixed units.  The dash chain
// interpolates in world space and projects each sprite, so the segment endpoints
// have to survive the presentation boundary unprojected [R-P0-11 §3].
type QueueWorldPoint struct{ X, Y, Z numeric.Fixed }

// QueuePrimitive is one immutable draw instruction.  Marker contains the
// eight lines from BuildMarkerSegments; Dash and Circle contain one segment.
// A Dash carries its world-space endpoints and the order's age so the client
// asset adapter can place the authored GAF sprite chain with DashSprites; it is
// never a line to rasterize [R-P0-11 §3].
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
	WorldA     QueueWorldPoint
	WorldB     QueueWorldPoint
	Center     QueuePoint
	Radius     int32
	Segments   []MarkerSegment
	DashAge    uint32
	IconFrame  int32
	IconKnown  bool
}

// QueueOverlayOptions supplies the two camera/content facts not carried by a
// generic frame.  A missing callback suppresses only the affected helper;
// it never invents a footprint, range, circle radius, or GAF frame.
type QueueOverlayOptions struct {
	Tick        uint32
	ShiftHeld   bool
	LocalOwner  uint8
	HoveredUnit pool.Handle
	Project     func(x, y, z numeric.Fixed) QueuePoint
	BuildRect   func(frame.OrderView) (QueueRect, bool)
	Circle      func(frame.OrderView) (int32, bool)
	Icon        func(frame.OrderView, uint32) (int32, bool)
	Range       func(frame.UnitView) (int32, bool)
}

// QueueOverlay returns stable queue instructions while Shift is held.  The
// release path returns nil and performs no mutation, so it cannot alter an
// authoritative state or hash [07 §9][R-P0-11 §4].
func QueueOverlay(f *frame.Frame, opt QueueOverlayOptions) []QueuePrimitive {
	if f == nil || !opt.ShiftHeld || opt.Project == nil {
		return nil
	}

	units := make(map[pool.Handle]frame.UnitView, len(f.Units))
	for _, u := range f.Units {
		if (u.MaxHealth > 0 || u.Health > 0 || u.Slot != 0) && u.Owner == opt.LocalOwner {
			if u.Slot == 0 {
				continue
			}
			units[u.Slot] = u
		}
	}
	selected := make(map[pool.Handle]bool, len(f.Selection.Handles))
	for _, h := range f.Selection.Handles {
		selected[h] = true
	}
	// Queue overlays walk the local slice.  The marker-only path is only
	// meaningful when the immutable command page identifies a builder context
	// or a selected queue already contains a build order [R-P0-11 §3].
	// IsBuilding alone is insufficient: factories, mexes, and other fixed
	// structures are not necessarily builders.
	hasBuilder := false
	if f.CommandPage.Builder != 0 {
		_, hasBuilder = units[f.CommandPage.Builder]
	}
	if !hasBuilder {
		for h := range selected {
			if _, ok := units[h]; ok && hasBuildOrder(f.OrderQueues, h) {
				hasBuilder = true
				break
			}
		}
	}

	var out []QueuePrimitive
	for _, q := range f.OrderQueues {
		_, ok := units[q.Unit]
		if !ok {
			continue
		}
		isSelected := selected[q.Unit]
		isHovered := q.Unit == opt.HoveredUnit
		mask := QueueOverlayMask(QueueMarkerMask)
		if isHovered {
			mask = QueueMarkerMask | QueueDashMask | QueueCircleMask | QueueIconMask | QueueRangeMask
		} else if isSelected {
			// The selected queue remains a line/marker pass; hovering one of
			// those units upgrades only that queue to the full five-bit mask
			// [R-P0-11].
			mask = QueueMarkerMask | QueueDashMask
		} else if !hasBuilder {
			continue
		}
		u := units[q.Unit]
		// The per-unit dispatcher seeds the running anchor from the unit's own
		// position and each helper advances it, so a queue's first dash segment
		// runs from the unit to its first order [R-P0-11 §3].
		prevWorld := QueueWorldPoint{X: u.X, Y: u.Y, Z: u.Z}
		prev := opt.Project(prevWorld.X, prevWorld.Y, prevWorld.Z)
		for list, orders := range [][]frame.OrderView{q.Primary, q.Secondary} {
			for _, order := range orders {
				orderMask := queueOrderMask(order.Kind) & mask
				if orderMask == 0 {
					continue
				}
				world := orderWorldPoints(order)
				points := make([]QueuePoint, len(world))
				for i, w := range world {
					points[i] = opt.Project(w.X, w.Y, w.Z)
				}
				// Helpers run in draw-mask bit order: marker (1), dash (2),
				// circle (4), icon (8) [R-P0-11 §3].
				if orderMask&QueueMarkerMask != 0 && order.BuildProduct != "" && opt.BuildRect != nil {
					if rect, ok := opt.BuildRect(order); ok {
						segments := BuildMarkerSegments(rect.Left, rect.Top, rect.Right, rect.Bottom, int(age(opt.Tick, order.CreationTick)), isSelected)
						out = append(out, QueuePrimitive{Kind: QueuePrimitiveMarker, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Color: segments[0].Color, ColorKnown: true, Segments: segments})
					}
				}
				if orderMask&QueueDashMask != 0 {
					for i, p := range points {
						// The dash chain is a sprite chain, not a line: the
						// instruction keeps the world segment and the order's age
						// and leaves placement to DashSprites, so an integration
						// cannot substitute a guessed solid line [R-P0-11 §3].
						out = append(out, QueuePrimitive{Kind: QueuePrimitiveDash, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, A: prev, B: p, WorldA: prevWorld, WorldB: world[i], DashAge: age(opt.Tick, order.CreationTick)})
						prev, prevWorld = p, world[i]
					}
				} else {
					prev, prevWorld = points[len(points)-1], world[len(world)-1]
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
		if (isSelected || isHovered) && opt.Range != nil {
			if radius, ok := opt.Range(u); ok && radius > 0 {
				center := opt.Project(u.X, u.Y, u.Z)
				for _, chord := range circle15(center, radius) {
					out = append(out, QueuePrimitive{Kind: QueuePrimitiveCircle, Unit: q.Unit, OrderKind: "range", Mask: QueueRangeMask, Selected: isSelected, A: chord[0], B: chord[1], Center: center, Radius: radius})
				}
			}
		}
	}
	return out
}

func hasBuildOrder(queues []frame.OrderQueueView, unit pool.Handle) bool {
	for _, q := range queues {
		if q.Unit != unit {
			continue
		}
		for _, list := range [][]frame.OrderView{q.Primary, q.Secondary} {
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

func orderWorldPoints(o frame.OrderView) []QueueWorldPoint {
	points := make([]QueueWorldPoint, 0, len(o.Route)+1)
	for _, route := range o.Route {
		points = append(points, QueueWorldPoint{X: route.X, Y: route.Y, Z: route.Z})
	}
	goal := QueueWorldPoint{X: o.GoalX, Y: o.GoalY, Z: o.GoalZ}
	if len(points) == 0 || points[len(points)-1] != goal {
		points = append(points, goal)
	}
	return points
}

// Travelling-dash chain constants [R-P0-11 §3]. Retail advances a phase along
// the segment by three 16-unit cells per sprite, seeds that phase from the
// order's age wrapped at 30 ticks so the chain marches once a second, and skips
// a segment shorter than one world unit outright.
const (
	DashSpriteSpacing  = int64(3) << 20
	DashPhaseWrapTicks = 30
	DashMinSegment     = int64(1) << 16
)

// DashSprites places one travelling-dash segment's sprites [R-P0-11 §3].
//
// The segment length is the truncated 3-D distance between its world endpoints;
// under one world unit nothing is drawn. The phase starts at
// `((age mod 30) * 3 << 20) / 30`, advances DashSpriteSpacing per sprite, and
// each sprite sits at the 16.16 fraction `(phase << 16) / distance` along the
// segment. The first sprite takes frame `(age / ticksPerFrame) mod frameCount`
// and every later sprite in the chain takes the next frame, which is what makes
// a multi-frame chain read as motion along the line.
//
// age is the order's age in simulation ticks; the caller supplies the authored
// GAF entry's frame count and its ticks-per-frame. Absent artwork (frameCount
// zero) draws nothing rather than substituting a line.
func DashSprites(a, b QueueWorldPoint, age uint32, ticksPerFrame, frameCount int, emit func(frame int, x, y, z numeric.Fixed)) {
	if emit == nil || frameCount <= 0 {
		return
	}
	if ticksPerFrame < 1 {
		ticksPerFrame = 1
	}
	dx := int64(b.X) - int64(a.X)
	dy := int64(b.Y) - int64(a.Y)
	dz := int64(b.Z) - int64(a.Z)
	distance := isqrt64(dx*dx + dy*dy + dz*dz)
	if distance < DashMinSegment {
		return
	}
	phase := (int64(age%DashPhaseWrapTicks) * 3 << 20) / DashPhaseWrapTicks
	index := int(age/uint32(ticksPerFrame)) % frameCount
	for ; phase < distance; phase += DashSpriteSpacing {
		t := (phase << 16) / distance
		emit(index,
			numeric.Fixed(int64(a.X)+(dx*t>>16)),
			numeric.Fixed(int64(a.Y)+(dy*t>>16)),
			numeric.Fixed(int64(a.Z)+(dz*t>>16)))
		index = (index + 1) % frameCount
	}
}

// isqrt64 is floor(sqrt(v)) for a non-negative v. Retail truncates a hardware
// square root toward zero; an exact integer root reproduces that without
// bringing a float into the overlay path [INVARIANTS I2].
func isqrt64(v int64) int64 {
	if v <= 0 {
		return 0
	}
	r := int64(0)
	for bit := int64(1) << 62; bit != 0; bit >>= 2 {
		if v >= r+bit {
			v -= r + bit
			r = (r >> 1) + bit
		} else {
			r >>= 1
		}
	}
	return r
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
