// Package hud — queued build-site marker geometry [07 §9].
package hud

// BuildMarkerSweepTicks is the length of the build-site marker's sweep in
// simulation ticks [07 §9]. Retail clamps the order's age into 0..10 and
// interpolates the four lines across the footprint over that span, so the
// animation lasts a third of a second at 30 Hz and then holds.
const BuildMarkerSweepTicks = 10

// MarkerSegment is one axis-aligned marker line in screen pixels [07 §9].
// Horizontal segments have Y0 == Y1, vertical ones X0 == X1.
type MarkerSegment struct {
	X0, Y0, X1, Y1 int32
	Color          uint8 // logical palette index, before the logical→physical map
}

// Build-site marker logical colors [07 §9]. Retail indexes the logical palette
// map with a pair per selection state: the outer, one-pixel-outset line and the
// inner line on it.
const (
	MarkerOuterSelected   = 3
	MarkerInnerSelected   = 10
	MarkerOuterUnselected = 1
	MarkerInnerUnselected = 9
)

// Placement ghost logical colors [07 §9]. Retail picks between them with a
// single conditional on the site-validity bit; both outlines of the ghost take
// the same one, so validity reads as a color change rather than a shape change.
const (
	GhostColorLegal   = 10
	GhostColorIllegal = 4
)

// BuildMarkerSegments returns the eight lines retail draws for one queued build
// order whose footprint projects to the screen rectangle (left, top, right,
// bottom) [07 §9].
//
// age is the order's age in simulation ticks; it is clamped into
// 0..BuildMarkerSweepTicks. The offsets are
//
//	dx = (right - left) * age / 10
//	dy = (bottom - top) * age / 10
//
// and the four lines sit at left+dx, right-dx, top+dy and bottom-dy. At age 0
// they lie on the footprint's own edges; over the sweep the two verticals cross
// each other and land on the opposite sides, as do the two horizontals, so the
// marker closes inward and comes to rest as the same rectangle it started from.
//
// Each line is drawn twice: once in the outer color, one pixel outside the
// rectangle on the perpendicular axis and one pixel back along its own, and
// once in the inner color flush with the rectangle. That is what gives the
// sweeping lines their dark edge over bright terrain.
func BuildMarkerSegments(left, top, right, bottom int32, age int, selected bool) []MarkerSegment {
	if age < 0 {
		age = 0
	}
	if age > BuildMarkerSweepTicks {
		age = BuildMarkerSweepTicks
	}
	dx := (right - left) * int32(age) / BuildMarkerSweepTicks
	dy := (bottom - top) * int32(age) / BuildMarkerSweepTicks

	outer, inner := uint8(MarkerOuterUnselected), uint8(MarkerInnerUnselected)
	if selected {
		outer, inner = MarkerOuterSelected, MarkerInnerSelected
	}
	return []MarkerSegment{
		{left + dx - 1, top - 1, left + dx - 1, bottom + 1, outer},
		{right - dx + 1, top - 1, right - dx + 1, bottom + 1, outer},
		{left - 1, top + dy - 1, right + 1, top + dy - 1, outer},
		{left - 1, bottom - dy + 1, right + 1, bottom - dy + 1, outer},
		{left + dx, top, left + dx, bottom, inner},
		{right - dx, top, right - dx, bottom, inner},
		{left, top + dy, right, top + dy, inner},
		{left, bottom - dy, right, bottom - dy, inner},
	}
}
