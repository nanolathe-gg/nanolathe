package hud

// The queue overlay is deliberately a pure snapshot consumer.  The retail
// walker reads live order records, but the presentation boundary publishes a
// complete immutable queue [04 §3][07 §9].  This adapter therefore returns
// draw instructions and never retains a pointer into simulation state.

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// QueueOverlayMask is the five-bit helper mask used by retail's descriptor
// walker [07 §9][R-P0-11 §3].  There is no separate runtime writer for the mask
// byte: the runtime table is built from the static records whose census is
// queueDescriptors below.
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
	QueuePrimitiveLabel
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
	// IconCursor is the order descriptor's icon byte, which indexes the same
	// twenty-two-slot cursor handle array the software pointer uses: the icon
	// helper blits `cursorHandles[iconByte]`'s current frame at the order's
	// anchor [R-P0-11 §3][07 §8].  It is nonzero on every icon primitive,
	// because an icon byte of zero is exactly the "no icon" encoding.
	IconCursor uint8
	// Text is populated only for QueuePrimitiveLabel. A is that label's
	// screen-space pen and Color is its known GUI semantic colour.
	Text string
}

// RangeWeapon is one weapon slot's immutable inputs to the queued-order range
// overlay. The integration adapter resolves the authored definition and
// normalizes its stored field widths; Enabled remains the separately published
// live slot bit [06 R-WPN-05 §3][07 R-P0-11 §3].
type RangeWeapon struct {
	Enabled      bool
	Range        int32
	Coverage     int32
	AreaOfEffect int32
}

// RangeSet is the fixed, presentation-only input to retail's compact and
// labelled queued-order range helpers. The integration adapter resolves these
// values from the immutable catalog by UnitView.DefName, while the live slot bits
// come from the committed UnitView; no definition pointer crosses the frame
// boundary [07 R-P0-11 §3][I6].
//
// The unit radii and ExplosionAreaOfEffect are already normalized to their
// retail consumer widths: the six sensor fields are signed 16-bit, the build,
// maneuver, kamikaze, attack-run and explosion fields unsigned 16-bit, and
// weapon Range/Coverage retain their authored dwords [02 "Unit record"]
// [02 "Weapon record"].
type RangeSet struct {
	MinCloak int32
	Sight    int32
	Radar    int32
	Sonar    int32
	RadarJam int32
	SonarJam int32

	BuildDistance    int32
	Maneuver         int32
	KamikazeDistance int32
	AttackRunLength  int32

	Kamikaze              bool
	HasMover              bool
	ExplosionResolved     bool
	ExplosionAreaOfEffect int32
	Weapons               [3]RangeWeapon
}

// QueueOverlayOptions supplies the camera/content facts not carried by a
// generic frame.  A missing callback suppresses only the affected helper;
// it never invents a footprint, range, circle radius, or GAF frame.
//
// TrackedUnit, PageUnit and HoveredUnit are the walker's first three
// privileged sources [R-P0-11 §3].  All three are presentation-owned pointer
// and camera state — the follow camera's tracked slot [07 R-CAM-01 §12], the
// command page's subject, and the same hover word the footer's first hover
// source reads [07 R-HUD-03 §1] — so none of them crosses the publication
// boundary and none may be reconstructed from the live pool [I6].  PageUnit
// defaults to the committed `CommandPage.Builder` when it is left zero.
type QueueOverlayOptions struct {
	Tick        uint32
	ShiftHeld   bool
	LocalOwner  uint8
	TrackedUnit pool.Handle
	PageUnit    pool.Handle
	HoveredUnit pool.Handle
	Project     func(x, y, z numeric.Fixed) QueuePoint
	BuildRect   func(frame.OrderView) (QueueRect, bool)
	Circle      func(frame.OrderView) (int32, bool)
	// Icon resolves the animated cursor-GAF frame index for one icon byte.
	// The client owns the artwork, so it owns both the frame count and the
	// ticks-per-frame the index is formed from [R-P0-11 §3].
	Icon func(cursorIndex uint8, tick uint32) (int32, bool)
	// ShowRanges selects the labelled range branch and the extra attack-icon
	// rings. It does not bypass Shift or the descriptor's range/icon bits.
	ShowRanges bool
	// Range resolves the immutable authored inputs for one committed unit. A
	// false result suppresses range helpers for that unit rather than inventing
	// definition data.
	Range func(frame.UnitView) (RangeSet, bool)
	// GroundHeight supplies the presentation terrain sample used to lift each
	// range-ring endpoint. A missing callback suppresses range geometry.
	GroundHeight func(x, z numeric.Fixed) numeric.Fixed
	// Builder reports whether a unit's definition carries the builder
	// capability.  It gates the marker-only fallback alone [R-P0-11 §3]; a nil
	// callback therefore suppresses that fallback rather than admitting every
	// local unit.
	Builder func(frame.UnitView) bool
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
	// The walker's privileged sources, in the order [R-P0-11 §3] lists them:
	// the follow camera's tracked unit, the unit whose command page is open,
	// the hovered unit id, and every selected unit.  All four get the full
	// five-bit mask `0x1F`; every other local unit gets marker-only `1`.
	//
	// The earlier reading here gave a selected unit only marker+dash and
	// reserved the full mask for the hovered one.  §3 corrects both halves:
	// "single-selected" was misleading — *every* selected unit gets the full
	// mask — and the tracked and command-page units are privileged too.
	page := opt.PageUnit
	if page == 0 {
		page = f.CommandPage.Builder
	}
	privileged := [3]pool.Handle{opt.TrackedUnit, page, opt.HoveredUnit}

	// The marker-only fallback runs only when a builder context exists,
	// "defined precisely as: at least one of (1), (2), (3) resolves to a live
	// unit whose definition carries the builder capability" [R-P0-11 §3].  Note
	// the asymmetry the section spells out: the four privileged units draw
	// their queues whether or not anything is a builder.  The previous reading
	// accepted any live command-page unit, and otherwise any selected unit
	// holding a build order — neither is one of the three sources, and the
	// selection is not consulted by this test at all.
	hasBuilder := false
	if opt.Builder != nil {
		for _, h := range privileged {
			if h == 0 {
				continue
			}
			if v, ok := units[h]; ok && opt.Builder(v) {
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
		isPrivileged := isSelected
		for _, h := range privileged {
			if h != 0 && h == q.Unit {
				isPrivileged = true
				break
			}
		}
		mask := QueueOverlayMask(QueueMarkerMask)
		if isPrivileged {
			mask = QueueMarkerMask | QueueDashMask | QueueCircleMask | QueueIconMask | QueueRangeMask
		} else if !hasBuilder {
			continue
		}
		u := units[q.Unit]
		var ranges RangeSet
		rangesResolved, rangesKnown := false, false
		resolveRanges := func() (RangeSet, bool) {
			if !rangesResolved {
				rangesResolved = true
				if isPrivileged && opt.Range != nil {
					ranges, rangesKnown = opt.Range(u)
				}
			}
			return ranges, rangesKnown
		}
		// The per-unit dispatcher seeds the running anchor from the unit's own
		// position and each helper advances it, so a queue's first dash segment
		// runs from the unit to its first order [R-P0-11 §3].
		prevWorld := QueueWorldPoint{X: u.X, Y: u.Y, Z: u.Z}
		prev := opt.Project(prevWorld.X, prevWorld.Y, prevWorld.Z)
		rangeDrawn := false
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
				// The bit-8 helper is also the anchor getter, and the bit-2
				// helper's first act is to call it, so an order kind that sets
				// bit 2 without bit 8 still runs the icon helper.  Whether an
				// icon appears is decided by the descriptor's icon byte alone,
				// and it is drawn before the chain that needed the anchor
				// [R-P0-11 §3 "The dash chain's artwork, and the anchor getter
				// that doubles as the icon"].
				icon, iconByte := queueOrderIcon(order.Kind)
				runIconHelper := iconByte && icon != 0 &&
					orderMask&(QueueDashMask|QueueIconMask) != 0
				iconHandled := false
				emitIcon := func() {
					if !runIconHelper || iconHandled {
						return
					}
					iconHandled = true
					centerWorld := world[len(world)-1]
					center := points[len(points)-1]
					if opt.ShowRanges && (icon == 1 || icon == 2) {
						if set, ok := resolveRanges(); ok {
							base := QueuePrimitive{Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Center: center}
							out = appendAttackRanges(out, base, centerWorld, set, opt)
						}
					}
					if opt.Icon == nil {
						return
					}
					frameIndex, ok := opt.Icon(icon, opt.Tick)
					if !ok {
						return
					}
					// The anchor is the order's own point — the target's
					// position for a targeted node, the node's stored position
					// otherwise — which is the last of this order's points.
					out = append(out, QueuePrimitive{Kind: QueuePrimitiveIcon, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Center: center, IconFrame: frameIndex, IconKnown: true, IconCursor: icon})
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
					emitIcon()
					for i, p := range points {
						// The dash chain is a sprite chain, not a line: the
						// instruction keeps the world segment and the order's age
						// and leaves placement to DashSprites, so an integration
						// cannot substitute a guessed solid line [R-P0-11 §3].
						out = append(out, QueuePrimitive{Kind: QueuePrimitiveDash, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, A: prev, B: p, WorldA: prevWorld, WorldB: world[i], DashAge: age(opt.Tick, order.CreationTick)})
						prev, prevWorld = p, world[i]
					}
				} else if orderMask&QueueIconMask != 0 {
					// The icon helper is also the anchor getter, and it is the
					// ONLY other helper that resolves and advances the running
					// point [R-P0-11 §3 "The dash chain's artwork, and the anchor
					// getter that doubles as the icon"]. An order whose mask sets
					// neither bit 2 nor bit 8 never calls it in retail, so the
					// running anchor must stay put. The idle-queue refill's
					// Standby/Standby_Mine head record is the reachable case:
					// mask 0x10 (range rings only) and "no target, goal, or
					// parameters" [04 "The idle-queue refill from
					// `defaultmissiontype`"] — advancing the anchor to that
					// record's zero-valued goal made the next queued order's dash
					// chain run from world origin instead of from wherever the
					// anchor actually was.
					prev, prevWorld = points[len(points)-1], world[len(world)-1]
				}
				if orderMask&QueueCircleMask != 0 && opt.Circle != nil {
					if radius, ok := opt.Circle(order); ok && radius > 0 {
						center := points[len(points)-1]
						for _, chord := range circle15(center, radius) {
							out = append(out, QueuePrimitive{Kind: QueuePrimitiveCircle, Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Color: compactRangeColor, ColorKnown: true, A: chord[0], B: chord[1], Center: center, Radius: radius})
						}
					}
				}
				if orderMask&QueueIconMask != 0 {
					emitIcon()
				}
				if orderMask&QueueRangeMask != 0 && !rangeDrawn {
					// The one-shot latch belongs to the per-unit descriptor walk: the
					// first range-bit node consumes it even when the integration has no
					// range data, and later nodes never run the helper [R-P0-11 §3].
					rangeDrawn = true
					if set, ok := resolveRanges(); ok {
						base := QueuePrimitive{Unit: q.Unit, List: uint8(list), Index: order.Index, OrderKind: order.Kind, Mask: orderMask, Selected: isSelected, Center: opt.Project(u.X, u.Y, u.Z)}
						out = appendUnitRanges(out, base, QueueWorldPoint{X: u.X, Y: u.Y, Z: u.Z}, u.Cloaked, set, opt)
					}
				}
			}
		}
	}
	return out
}

const (
	compactCloakColor = uint8(15)
	compactRangeColor = uint8(12)
	detailRangeColor  = uint8(14)
	detailEvenColor   = uint8(12)
	detailOddColor    = uint8(4)
	rangeLabelColor   = uint8(15)
)

func appendUnitRanges(out []QueuePrimitive, base QueuePrimitive, center QueueWorldPoint, cloaked bool, set RangeSet, opt QueueOverlayOptions) []QueuePrimitive {
	if !opt.ShowRanges {
		if set.MinCloak != 0 && cloaked {
			out = appendRangeRing(out, base, center, set.MinCloak, compactCloakColor, "", 0, opt)
		}
		if set.Kamikaze && set.ExplosionResolved {
			half := set.ExplosionAreaOfEffect >> 1
			pulse := int32((int64(opt.Tick%60) * int64(half) * 2) / 60)
			if pulse < 8 {
				pulse = 8
			}
			if pulse >= half {
				pulse = half
			}
			out = appendRangeRing(out, base, center, pulse, compactRangeColor, "", 0, opt)
			radius := set.Sight
			if set.HasMover {
				radius = set.KamikazeDistance
			}
			out = appendRangeRing(out, base, center, radius, compactRangeColor, "", 0, opt)
		}
		return out
	}

	ordinal := 0
	unitRadii := [...]struct {
		radius int32
		label  string
	}{
		{set.MinCloak, "mincloak"},
		{set.Sight, "sight"},
		{set.Radar, "radar"},
		{set.Sonar, "sonar"},
		{set.RadarJam, "radarjam"},
		{set.SonarJam, "sonarjam"},
		{set.BuildDistance, "build distance"},
		{set.Maneuver, "maneuver"},
		{set.KamikazeDistance, "kamikazedistance"},
	}
	for _, item := range unitRadii {
		if item.radius == 0 {
			continue
		}
		out = appendRangeRing(out, base, center, item.radius, detailRangeColor, item.label, ordinal, opt)
		ordinal++
	}

	color := detailEvenColor
	if opt.Tick&1 != 0 {
		color = detailOddColor
	}
	for slot := range set.Weapons {
		enabled := set.Weapons[slot].Enabled
		if slot == 2 {
			// Retail's third authored-range branch tests slot one's enabled bit,
			// while still reading slot three's weapon and radius. Preserve that
			// observable asymmetry [07 R-P0-11 §3].
			enabled = set.Weapons[0].Enabled
		}
		if enabled && set.Weapons[slot].Range != 0 {
			out = appendRangeRing(out, base, center, set.Weapons[slot].Range, color, fmt.Sprintf("weapon%d range", slot+1), slot, opt)
		}
	}
	return out
}

func appendAttackRanges(out []QueuePrimitive, base QueuePrimitive, center QueueWorldPoint, set RangeSet, opt QueueOverlayOptions) []QueuePrimitive {
	color := detailEvenColor
	if opt.Tick&1 != 0 {
		color = detailOddColor
	}
	for slot, weapon := range set.Weapons {
		if !weapon.Enabled {
			continue
		}
		if weapon.AreaOfEffect != 0 {
			out = appendRangeRing(out, base, center, weapon.AreaOfEffect, color, fmt.Sprintf("weapon %d: area of effect", slot), 0, opt)
		}
		if weapon.Coverage != 0 {
			out = appendRangeRing(out, base, center, weapon.Coverage, color, fmt.Sprintf("weapon %d: coverage", slot), 1, opt)
		}
	}
	if set.AttackRunLength != 0 {
		out = appendRangeRing(out, base, center, set.AttackRunLength, color, "attack length", 2, opt)
	}
	return out
}

// appendRangeRing emits the shared range helper's adaptive, terrain-conforming
// chord walk and then its optional FNT label [07 R-P0-11 §3].
func appendRangeRing(out []QueuePrimitive, base QueuePrimitive, center QueueWorldPoint, radius int32, color uint8, label string, labelOrdinal int, opt QueueOverlayOptions) []QueuePrimitive {
	if radius == 0 || opt.Project == nil || opt.GroundHeight == nil {
		return out
	}
	chords := rangeChordCount(radius)
	if chords < 1 {
		// Divergence (I11 bounds rejection): retail divides by this chord count
		// and a malformed input can produce a nonpositive count. Reject that draw
		// at the host boundary rather than panicking the application.
		return out
	}
	if chords > maxRangeRingChords {
		// The same host bounds rejection at the other end, not a retail
		// contract: radius is a raw authored dword, the walk appends one
		// primitive per chord, and a modded or corrupted range would append
		// billions. The cap is the angle domain itself — a chord count above
		// the number of representable angles cannot produce another distinct
		// point, and the divisor below would reach zero — so no authored range
		// that draws a ring at all is drawn differently.
		chords = maxRangeRingChords
	}
	step := int32(65536 / chords)
	angle := int32(0)
	a := rangeRingPoint(center, numeric.Angle(uint16(angle)), radius, opt)
	last := a
	labelAt := QueuePoint{}
	for chord := int32(0); chord <= chords; chord++ {
		angle += step
		b := rangeRingPoint(center, numeric.Angle(uint16(angle)), radius, opt)
		line := base
		line.Kind = QueuePrimitiveCircle
		line.Color, line.ColorKnown = color, true
		line.A, line.B = a, b
		line.Radius = radius
		out = append(out, line)
		if chord == int32(labelOrdinal*3) {
			labelAt = b
		}
		a, last = b, b
	}
	if label != "" {
		if labelAt.X == 0 && labelAt.Y == 0 {
			labelAt = last
		}
		text := base
		text.Kind = QueuePrimitiveLabel
		text.Color, text.ColorKnown = rangeLabelColor, true
		text.A = QueuePoint{X: labelAt.X, Y: labelAt.Y + 4}
		text.Text = label
		out = append(out, text)
	}
	return out
}

// maxRangeRingChords bounds the ring walk at one chord per representable
// angle. Host bound, not a retail contract: see appendRangeRing.
const maxRangeRingChords = int32(65536)

func rangeChordCount(radius int32) int32 {
	if radius <= 0 {
		return 0
	}
	// Retail uses this stored approximation of 2pi, not math.Pi*2. This is a
	// presentation-only transient and retains the full authored dword domain;
	// a fixed rational approximation would drift near integer boundaries.
	return int32(float64(radius) * 6.28318530717958 * 0.125)
}

func rangeRingPoint(center QueueWorldPoint, angle numeric.Angle, radius int32, opt QueueOverlayOptions) QueuePoint {
	// The shared trig helpers consume an int32 16.16 magnitude; the shift and
	// coordinate additions therefore retain the original dword wrap.
	magnitude := radius << 16
	dx := numeric.MulRound(numeric.Sin(angle), magnitude)
	dz := numeric.MulRound(numeric.Cos(angle), magnitude)
	x := numeric.Fixed(int32(int64(int32(center.X)) + int64(dx)))
	z := numeric.Fixed(int32(int64(int32(center.Z)) + int64(dz)))

	centerWhole := int32(int16(uint16(uint64(center.Y) >> 16)))
	groundWhole := int32(opt.GroundHeight(x, z) >> 16)
	whole := centerWhole
	if groundWhole > whole {
		whole = groundWhole
	}
	// The terrain result replaces the high word while the center's fractional
	// low word survives. Projection consumes that signed high word.
	y := numeric.Fixed(int32(uint32(uint16(whole))<<16 | uint32(uint16(center.Y))))
	return opt.Project(x, y, z)
}

// queueDescriptor is one order descriptor's two overlay bytes: the draw-mask
// word and the icon byte [R-P0-11 §3].
type queueDescriptor struct {
	mask QueueOverlayMask
	icon uint8
}

// queueDescriptors is the per-kind census of the order-descriptor table's two
// overlay bytes, transcribed from [04 §3.1]'s sixty-seven-record table (the
// reject sentinel's empty name excluded).
//
// [04 §3.1] names them "a small class parameter" and "an acknowledgement
// group index"; [R-P0-11 §3] identifies both readers: the class parameter is
// the overlay's **draw-mask word** and the acknowledgement group is the
// **icon byte** that indexes the cursor handle array. Its independent
// transcription of the ground-state and VTOL static tables agrees with
// [04 §3.1] on all forty-four shared rows, which is what closes the
// identification; [04 §3.1] now records it in place, and
// `orders.Descriptor.Class` cites the same section.
//
// This replaces a five-row table whose default carried its own open-question
// marker about the "runtime descriptor mask writer": there is no
// separate writer, the runtime table is built from these static records.
//
// The five bits dispatch marker (1), dash (2), circle (4), icon (8) and range
// rings (16) [R-P0-11 §3]. An icon byte of 0 is the "no icon" encoding rather
// than cursor slot 0 — MOBILEBUILD and VTOL_MOBILEBUILD are the only records
// carrying it, which is why a queued build site shows the marker and the
// `pathicon` chain but no order icon. A mask of 0 draws nothing however
// privileged the unit; no stock record sets the circle bit.
//
// The same two bytes live on `internal/orders`' descriptor table as `Class`
// and `AckGroup`; `TestQueueDescriptorCensusMatchesOrderTable` pins the two
// together rather than making presentation import the order package.
var queueDescriptors = map[string]queueDescriptor{
	"Activate":           {0x00, 19},
	"AirStrike":          {0x08, 2},
	"AirToAir":           {0x08, 1},
	"AirToGround":        {0x08, 1},
	"AirToGroundHover":   {0x08, 1},
	"Attack_Chase":       {0x08, 1},
	"Attack_Kamikaze":    {0x08, 1},
	"Attack_NoMove":      {0x08, 1},
	"AttackSpecial":      {0x08, 1},
	"AttackUType":        {0x00, 19},
	"BeCarried":          {0x00, 19},
	"BuildingBuild":      {0x00, 19},
	"BuildWeapon":        {0x00, 19},
	"Capture":            {0x08, 4},
	"Cloak_Off":          {0x00, 19},
	"Cloak_On":           {0x00, 19},
	"Deactivate":         {0x00, 19},
	"Follow_Ground":      {0x12, 5},
	"GetBuilt":           {0x00, 19},
	"Ground_Pickup":      {0x08, 12},
	"Ground_Unload":      {0x08, 13},
	"Guard_NoMove":       {0x00, 19},
	"HelpBuild":          {0x18, 6},
	"MakeSelectable":     {0x00, 19},
	"MobileBuild":        {0x13, 0},
	"Move_Ground":        {0x12, 14},
	"Paralyze":           {0x00, 19},
	"Park":               {0x00, 14},
	"Patrol":             {0x12, 7},
	"QMove":              {0x02, 14},
	"QPatrol":            {0x02, 7},
	"Reclaim":            {0x12, 11},
	"ReclaimUnit":        {0x12, 11},
	"RepairPatrol":       {0x12, 7},
	"RepairUnit":         {0x12, 6},
	"RepairUnitNoMove":   {0x18, 6},
	"Resurrect":          {0x12, 11},
	"SelfDestruct":       {0x00, 19},
	"SelfDestructFG":     {0x00, 19},
	"SelfRepair":         {0x00, 19},
	"Standby":            {0x10, 15},
	"Standby_Mine":       {0x10, 15},
	"Standing_FireOrder": {0x00, 19},
	"Standing_MoveOrder": {0x00, 19},
	"Stop":               {0x00, 19},
	"Suppress":           {0x08, 1},
	"Teleport":           {0x08, 9},
	"VTOL_Evade":         {0x00, 19},
	"VTOL_Follow":        {0x02, 5},
	"VTOL_GetRepaired":   {0x00, 19},
	"VTOL_HelpBuild":     {0x08, 6},
	"VTOL_LandIfCan":     {0x00, 19},
	"VTOL_Landing":       {0x08, 14},
	"VTOL_MobileBuild":   {0x03, 0},
	"VTOL_Move":          {0x02, 14},
	"VTOL_Patrol":        {0x02, 7},
	"VTOL_Pickup":        {0x08, 8},
	"VTOL_Reclaim":       {0x02, 11},
	"VTOL_ReclaimUnit":   {0x02, 11},
	"VTOL_RepairPatrol":  {0x02, 7},
	"VTOL_RepairUnit":    {0x02, 6},
	"VTOL_SeekAttack":    {0x00, 19},
	"VTOL_SeekGuard":     {0x00, 19},
	"VTOL_Standby":       {0x00, 15},
	"VTOL_Unload":        {0x08, 9},
	"Wait":               {0x00, 19},
	"WaitForAttack":      {0x00, 19},
}

func queueOrderMask(kind string) QueueOverlayMask {
	return queueDescriptors[kind].mask
}

// queueOrderIcon returns the descriptor's icon byte and whether the kind has a
// census row at all.  A row with icon 0 is a kind that deliberately draws no
// icon [R-P0-11 §3].
func queueOrderIcon(kind string) (uint8, bool) {
	d, ok := queueDescriptors[kind]
	return d.icon, ok
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
	distance := numeric.ISqrt64(dx*dx + dy*dy + dz*dz)
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

func age(now, born uint32) uint32 {
	if now < born {
		return 0
	}
	return now - born
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
