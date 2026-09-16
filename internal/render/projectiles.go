package render

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Rendertype IDs per [03 §5.4] C6.
const (
	RenderTypeBeam              = 0 // [03 §5.4] line from current endpoint to tail
	RenderTypeBaseSpriteModel   = 1 // [03 §5.4] base sprite plus model
	RenderTypeGlobalGAF         = 2 // [03 §5.4] fixed global GAF aborting whole renderer on failed admission
	RenderTypeBaseModelDistinct = 3 // [03 §5.4] base plus model distinct orientation path
	RenderTypeSelectorGAF       = 4 // [03 §5.4] selector picks one of five GAF sequences; -1 suppresses
	RenderTypeLifetimeGAF       = 5 // [03 §5.4] lifetime-scaled frame
	RenderTypeRecordOrientation = 6 // [03 §5.4] orientation stored verbatim in record
	RenderTypeSegmented         = 7 // [03 §5.4] randomized segmented lines with 0x50000 denominator
)

// RendertypeCount is the dispatch table size [03 §5.4] C6.
const RendertypeCount = 8 // [03 §5.4] eight cases 0..7

// BeamStroke is one one-pixel Bresenham line stroke [03 §5.4] C7.
type BeamStroke struct {
	X0, Y0 int32
	X1, Y1 int32
	Color  int32 // authored logical color; mapped by the client [06 R-WFX-01 §4]
}

// BeamStrokes returns the primary stroke and, when color2 is nonzero, its
// offset secondary stroke first. Endpoint sorting and the strict major-axis
// comparison follow [06 R-WFX-01 §4]; color presence is tested before mapping.
func BeamStrokes(headScreen, tailScreen [2]int32, color, color2 int32) []BeamStroke {
	primary := BeamStroke{X0: headScreen[0], Y0: headScreen[1], X1: tailScreen[0], Y1: tailScreen[1], Color: color}
	if color2 == 0 {
		return []BeamStroke{primary}
	}
	dx, dy := int64(primary.X1)-int64(primary.X0), int64(primary.Y1)-int64(primary.Y0)
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	horizontal := dx > dy // Equal spans use the vertical-major adjustment.
	if horizontal && primary.X0 > primary.X1 || !horizontal && primary.Y0 > primary.Y1 {
		primary.X0, primary.X1 = primary.X1, primary.X0
		primary.Y0, primary.Y1 = primary.Y1, primary.Y0
	}
	secondary := primary
	secondary.Color = color2
	if horizontal {
		secondary.Y0--
		secondary.Y1--
	} else {
		secondary.X0--
		secondary.X1++
	}
	return []BeamStroke{secondary, primary}
}

// Trail smoke is not emitted here. The projectile phase owns it: the smoke
// trail flag, alive records only, never burst parents, before expiry and past
// the strict next-trail deadline, with the deadline advanced ADDITIVELY by the
// smoke delay [06 §13.2][R-STRIP-01 §1 strip 9]. A presentation-side copy of
// that gate stood here with no caller, and its deadline comparison had drifted
// non-strict; the producer in internal/combat is the only one.

// SegmentCount computes the number of segments for rendertype 7 per [06 R-WFX-01 §4].
// Retail first truncates the raw 16.16 endpoint distance, then forms the
// 16.16 count (distance<<16)/0x50000.  It does not divide the floating-point
// distance directly by 0x50000: that loses the required intermediate boundary.
// Established: the span is map-space (world units), not projected screen
// space — [03 R-FX-01 §2] closes this explicitly ("whole world units per
// segment, in map space"), matching the world-space Fixed raw math below.
func SegmentCount(head, tail combat.Vec3) int { // [06 R-WFX-01 §4]
	dx := float64(int64(head.X) - int64(tail.X)) // Fixed raw delta [I2]
	dy := float64(int64(head.Y) - int64(tail.Y))
	dz := float64(int64(head.Z) - int64(tail.Z))
	distance := int64(numeric.TruncateFloat64ToLow32(math.Hypot(math.Hypot(dx, dy), dz)))
	if distance <= 0 {
		return 0
	}
	nFixed := (distance << 16) / 0x50000 // 16.16, signed division [06 R-WFX-01 §4]
	n := int(nFixed >> 16)
	if n <= 0 {
		n = 0
	}
	return n
}

// CRTRandomSource is the presentation copy of the CRT stream the segmented
// jitter draws from. Presentation never touches the simulation stream [I4].
type CRTRandomSource interface {
	Rand() int32
}

// SegmentedJitter applies integer per-axis jitter of rand()*11/0x8000 -5
// [03 §5.4] to X, height (Y), and Z. It draws exactly three CRT values per point
// preserving deterministic call order per [03 §5.4] and I4.
// Presentation only (I6); never uses simulation RNG.
func SegmentedJitter(crt CRTRandomSource, x, y, z numeric.Fixed) (numeric.Fixed, numeric.Fixed, numeric.Fixed) { // [03 §5.4] [I4] (I6)
	if crt == nil {
		return x, y, z
	}
	// [03 §5.4] rand()*11/0x8000 -5 ; CRT Rand returns 0..0x7FFF [01 §7.2]
	jx := int32(crt.Rand()*11/0x8000 - 5) // [03 §5.4]
	jy := int32(crt.Rand()*11/0x8000 - 5)
	jz := int32(crt.Rand()*11/0x8000 - 5)
	// Established: the jitter is whole world units added to the point's high
	// word [03 R-FX-01 §2][06 R-WFX-01 §4] — not a separate screen-pixel scale.
	// A raw 16.16 Fixed value's high word is its integer part, so adding an
	// integer to that word is exactly *65536 on the raw value.
	xj := x + numeric.Fixed(int64(jx)*65536)
	yj := y + numeric.Fixed(int64(jy)*65536)
	zj := z + numeric.Fixed(int64(jz)*65536)
	return xj, yj, zj
}

// SegmentedBeamPoints generates one type-7 pass from the retail tail endpoint
// to the current point. It returns that fixed start point followed by n
// jittered generated endpoints, spending exactly three CRT draws per generated
// point. A zero segment count draws nothing [06 R-WFX-01 §4].
func SegmentedBeamPoints(head, tail combat.Vec3, crt CRTRandomSource) []combat.Vec3 { // [06 R-WFX-01 §4] [I4] (I6)
	n := SegmentCount(head, tail)
	if n == 0 {
		return nil
	}
	pts := make([]combat.Vec3, 0, n+1)
	pts = append(pts, tail)

	dx := int64(head.X) - int64(tail.X)
	dy := int64(head.Y) - int64(tail.Y)
	dz := int64(head.Z) - int64(tail.Z)
	distance := int64(numeric.TruncateFloat64ToLow32(math.Hypot(math.Hypot(float64(dx), float64(dy)), float64(dz))))
	nFixed := (distance << 16) / 0x50000
	stepX := (dx << 16) / nFixed
	stepY := (dy << 16) / nFixed
	stepZ := (dz << 16) / nFixed
	point := tail
	for i := 0; i < n; i++ {
		point.X += numeric.Fixed(stepX)
		point.Y += numeric.Fixed(stepY)
		point.Z += numeric.Fixed(stepZ)
		jx, jy, jz := SegmentedJitter(crt, point.X, point.Y, point.Z)
		pts = append(pts, combat.Vec3{X: jx, Y: jy, Z: jz})
	}
	return pts
}
