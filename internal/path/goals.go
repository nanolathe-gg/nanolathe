package path

// Goal families [04 §7.2] C8 and enumeration [04 §7.4].
//
// The path-facing families share the Goal interface (types.go): point/radius,
// annulus (stand-off), rectangle perimeter, and the serialized saved form.
//
// - Point/radius: h = max(18*max(|dx|,|dz|)+7*min(|dx|,|dz|)-R, 0)
//   where R is the raw authored radius; arrival is a squared-distance
//   test against a radius quantized by division by 16 toward zero [04 §7.2].
// - Annulus: same inflated octile with V-shaped zero band inside
//   [inner,outer] raw radii, rising as oct-outer outward and inner-oct
//   inward; arrival uses >>4-quantized squared radii [04 §7.2], [04 §7.4].
// - Rectangle perimeter: admissible 16*max+6*min outside, and inside
//   16*min(distance to each edge); goal cells exactly the border [04 §7.2].
// - Air work and air moving: h identically zero, empty enumeration, and a
//   false start predicate. These serialized air surfaces are not admitted to
//   the ground search [04 R-PATH-01 §9].
//
// The annulus radius-unit mismatch (raw authored radii in the heuristic
// clamp vs >>4-quantized squared radii in arrival) is REAL and reproduced,
// not fixed. Both sides are documented here with citations.
// The mismatch is not an open question: [04 §7.4] records it as "real and
// established as a dual-unit contract ... two unit systems coexist in one
// family and must be reproduced as-is, not 'fixed'". Reproducing it is the
// contract, so the sites below carry the citation and no marker.

import "github.com/nanolathe-gg/nanolathe/internal/sim/numeric"

// octInflated returns the inflated octile 18*max+7*min [04 §7.2] C8.
// a and b are non-negative distances (|dx|, |dz|).
func octInflated(a, b int32) int32 {
	maxv := a
	minv := b
	if b > a {
		maxv = b
		minv = a
	}
	return 18*maxv + 7*minv
}

// octAdmissible returns the admissible octile 16*max+6*min [04 §7.2] C8.
func octAdmissible(a, b int32) int32 {
	maxv := a
	minv := b
	if b > a {
		maxv = b
		minv = a
	}
	return 16*maxv + 6*minv
}

// pointGoal implements the point/radius family [04 §7.2].
type pointGoal struct {
	center   Cell
	radius   int32 // raw authored radius, compared to oct [04 §7.2]
	radiusSq int32 // saved arrival threshold in squared cells [08 R-SAVE-02 §10]
}

// PointGoal creates a point/radius goal [04 §7.2] C8.
// h = max(18*max(|dx|,|dz|)+7*min(|dx|,|dz|)-R, 0).
// Arrival (StartSatisfied) is a squared-distance test against a
// radius quantized by division by 16 toward zero [04 §7.2], [04 §7.4].
func PointGoal(center Cell, radius int32) Goal {
	return PointGoalRestored(center, radius, quantizedRadiusSquare(radius))
}

// PointGoalRestored creates a point goal from its saved radius and arrival
// threshold. The threshold is a separately stored field in a code-4 order
// payload, so a restore must not derive it again from radius [08 R-SAVE-02
// §10, §11].
func PointGoalRestored(center Cell, radius, radiusSq int32) Goal {
	return &pointGoal{center: center, radius: radius, radiusSq: radiusSq}
}

func (g *pointGoal) H(c Cell) int32 {
	dx := numeric.Abs(c.X - g.center.X)
	dz := numeric.Abs(c.Z - g.center.Z)
	oct := octInflated(dx, dz) // [04 §7.2] 18*max+7*min
	h := oct - g.radius        // clamp to zero within R [04 §7.2]
	if h < 0 {
		return 0
	}
	return h
}

func (g *pointGoal) Enumerate(out []Cell) []Cell {
	// Single packed center cell per [04 §7.4].
	if out == nil {
		return []Cell{g.center}
	}
	out = out[:0]
	out = append(out, g.center)
	return out
}

func (g *pointGoal) StartSatisfied(start Cell) bool {
	// Arrival is a squared-distance test against radius quantized by division by 16 toward zero [04 §7.2].
	// Point arrival is "a squared-distance test against a separately stored
	// quantized radius" while the heuristic clamps against the raw authored
	// radius [04 §7.2 "Point/radius goals"][04 §7.4]. The two units are the
	// established contract, not a defect.
	dx := start.X - g.center.X
	dz := start.Z - g.center.Z
	// Retail keeps both the threshold and the wrapped cell-domain product as
	// signed 32-bit values [04 R-PATH-01 §9].
	distSq := dx*dx + dz*dz
	return distSq <= g.radiusSq
}

// annulusGoal implements the stand-off annulus family [04 §7.2].
type annulusGoal struct {
	center  Cell
	inner   int32 // raw authored inner radius [04 §7.2], [04 §7.4]
	outer   int32 // raw authored outer radius
	innerSq int32
	outerSq int32
}

// AnnulusGoal creates an annulus (stand-off) goal [04 §7.2] C8.
// Same inflated octile as point, with V-shaped zero band inside
// [inner,outer] raw radii: h is zero inside, rises as oct-outer outward
// and inner-oct inward. Arrival uses >>4-quantized squared radii [04 §7.4].
// The radius-unit mismatch is reproduced, not fixed.
// The raw-in-h, quantized-in-arrival split is the established dual-unit
// contract of [04 §7.4], reproduced deliberately.
func AnnulusGoal(center Cell, inner, outer int32) Goal {
	return AnnulusGoalRestored(center, inner, outer, quantizedRadiusSquare(inner), quantizedRadiusSquare(outer))
}

// AnnulusGoalRestored creates an annulus goal from its saved raw radii and
// squared arrival-band bounds. The latter are independently serialized in a
// code-5 order payload and must be kept verbatim on restore [08 R-SAVE-02
// §10, §11].
func AnnulusGoalRestored(center Cell, inner, outer, innerSq, outerSq int32) Goal {
	return &annulusGoal{center: center, inner: inner, outer: outer, innerSq: innerSq, outerSq: outerSq}
}

func quantizedRadiusSquare(radius int32) int32 {
	// Division rounds toward zero and the 32-bit product is retained verbatim.
	q := radius / 16
	return q * q
}

func (g *annulusGoal) H(c Cell) int32 {
	// [04 §7.2] same inflated octile with V-shaped zero band.
	// h = 0 inside [inner,outer]; oct-outer outward; inner-oct inward.
	// Raw authored radii in this clamp [04 §7.4].
	dx := numeric.Abs(c.X - g.center.X)
	dz := numeric.Abs(c.Z - g.center.Z)
	oct := octInflated(dx, dz)
	if oct < g.inner {
		return g.inner - oct
	}
	if oct <= g.outer {
		return 0
	}
	return oct - g.outer
}

func (g *annulusGoal) Enumerate(out []Cell) []Cell {
	// Single cell biased along z by (inner+outer)/32 toward far side [04 §7.4].
	// Bias uses raw authored radii before quantization; integer division truncates toward zero.
	bias := (g.inner + g.outer) / 32 // [04 §7.4] arithmetic direct, intent inferred
	cell := Cell{X: g.center.X, Z: g.center.Z + bias}
	if out == nil {
		return []Cell{cell}
	}
	out = out[:0]
	out = append(out, cell)
	return out
}

func (g *annulusGoal) StartSatisfied(start Cell) bool {
	// Arrival predicate uses squared radii after division by 16 toward zero [04 §7.4].
	// The h clamp above uses raw radii and this test uses quantized ones: the
	// dual-unit contract [04 §7.4] requires exactly that.
	dx := start.X - g.center.X
	dz := start.Z - g.center.Z
	// Both comparisons, including a wrapped product, are signed [04
	// R-PATH-01 §9].
	distSq := dx*dx + dz*dz
	// Band is inclusive [innerSq, outerSq]; if inner>outer no cell satisfies (zero band empty).
	return distSq >= g.innerSq && distSq <= g.outerSq
}

// rectGoal implements the rectangle-perimeter family [04 §7.2].
type rectGoal struct {
	rect Rect // Min inclusive, Max inclusive [04 §7.1] via types.go
}

func normalizeRect(r Rect) Rect {
	minX := r.Min.X
	maxX := r.Max.X
	if minX > maxX {
		minX, maxX = maxX, minX
	}
	minZ := r.Min.Z
	maxZ := r.Max.Z
	if minZ > maxZ {
		minZ, maxZ = maxZ, minZ
	}
	return Rect{Min: Cell{X: minX, Z: minZ}, Max: Cell{X: maxX, Z: maxZ}}
}

// RectPerimeterGoal creates a rectangle-perimeter goal [04 §7.2] C8.
// Outside: admissible 16*max+6*min to the rectangle.
// Inside: 16 * min(distance to each edge) back out.
// Goal cells are exactly the border where h is 0 [04 §7.2].
func RectPerimeterGoal(r Rect) Goal {
	return &rectGoal{rect: normalizeRect(r)}
}

func (g *rectGoal) H(c Cell) int32 {
	r := g.rect
	// Compute dx/dz distance to rectangle for outside case.
	var dx, dz int32
	if c.X < r.Min.X {
		dx = r.Min.X - c.X
	} else if c.X > r.Max.X {
		dx = c.X - r.Max.X
	} else {
		dx = 0
	}
	if c.Z < r.Min.Z {
		dz = r.Min.Z - c.Z
	} else if c.Z > r.Max.Z {
		dz = c.Z - r.Max.Z
	} else {
		dz = 0
	}
	if dx == 0 && dz == 0 {
		// Inside including border [04 §7.2]: 16 * min(dist to each edge).
		d1 := c.X - r.Min.X
		d2 := r.Max.X - c.X
		d3 := c.Z - r.Min.Z
		d4 := r.Max.Z - c.Z
		m := d1
		if d2 < m {
			m = d2
		}
		if d3 < m {
			m = d3
		}
		if d4 < m {
			m = d4
		}
		// m is 0 on the border => h 0; interior => 16*m.
		return 16 * m // [04 §7.2] inside cost
	}
	// Outside: admissible octile 16*max+6*min [04 §7.2].
	return octAdmissible(dx, dz)
}

func (g *rectGoal) Enumerate(out []Cell) []Cell {
	r := g.rect
	// Count border cells: 2*width + 2*height -4, with degenerate handling.
	width := r.Max.X - r.Min.X + 1
	height := r.Max.Z - r.Min.Z + 1
	if width <= 0 || height <= 0 {
		if out == nil {
			return nil
		}
		return out[:0]
	}
	// Single cell case.
	if width == 1 && height == 1 {
		if out == nil {
			return []Cell{r.Min}
		}
		out = out[:0]
		out = append(out, r.Min)
		return out
	}
	if out != nil {
		out = out[:0]
	} else {
		// Preallocate exact border size.
		var n int32
		if width == 1 {
			n = height
		} else if height == 1 {
			n = width
		} else {
			n = 2*width + 2*height - 4
		}
		out = make([]Cell, 0, n)
	}
	// Deterministic order: top row, bottom row, then sides.
	// Top edge Z=Min.Z
	for x := r.Min.X; x <= r.Max.X; x++ {
		out = append(out, Cell{X: x, Z: r.Min.Z})
	}
	// Bottom edge if distinct.
	if r.Max.Z != r.Min.Z {
		for x := r.Min.X; x <= r.Max.X; x++ {
			out = append(out, Cell{X: x, Z: r.Max.Z})
		}
	}
	// Sides between top and bottom, excluding corners already emitted.
	for z := r.Min.Z + 1; z <= r.Max.Z-1; z++ {
		out = append(out, Cell{X: r.Min.X, Z: z})
		if r.Max.X != r.Min.X {
			out = append(out, Cell{X: r.Max.X, Z: z})
		}
	}
	return out
}

func (g *rectGoal) StartSatisfied(start Cell) bool {
	r := g.rect
	// Goal cells are exactly the border [04 §7.2]; arrival requires lying on that border.
	if start.X < r.Min.X || start.X > r.Max.X || start.Z < r.Min.Z || start.Z > r.Max.Z {
		return false
	}
	return start.X == r.Min.X || start.X == r.Max.X || start.Z == r.Min.Z || start.Z == r.Max.Z
}

// The two serialized air surfaces of [04 R-PATH-01 §9] — air work and air
// moving — have no ground-order producer anywhere, and a ground search they
// were handed could not finish: their heuristic is zero, they enumerate no
// cells and they never report the start satisfied. Nothing in this package
// constructs them, so no inert family object stands here pretending otherwise;
// a ground order that cannot name its family falls back to the point goal
// (internal/movement/goals.go).

// IsRectGoal reports whether g is a rectangle-perimeter goal and, if so,
// returns its rectangle [04 §7.2]. It is the one family predicate with a
// production reader: the follower's rectangle arm. Every other family question
// — which family is this, with which parameters — is answered by DescribeGoal,
// which the scheduler's diagnostics already call, so no per-family accessor
// exists beside it. No retail constant is invented by either.
func IsRectGoal(g Goal) (r Rect, ok bool) {
	if rg, ok2 := g.(*rectGoal); ok2 {
		return rg.rect, true
	}
	return Rect{}, false
}

// RetailGoalWords exposes the stored goal parameters in saved order. Radius
// squares are independent live fields and must not be recomputed [08 R-SAVE-02 §10].
func RetailGoalWords(goal Goal) (uint32, []uint32, bool) {
	pack := func(c Cell) uint32 { return uint32(uint16(c.X)) | uint32(uint16(c.Z))<<16 }
	switch g := goal.(type) {
	case *pointGoal:
		return 4, []uint32{pack(g.center), uint32(g.radius), uint32(g.radiusSq)}, true
	case *annulusGoal:
		return 5, []uint32{pack(g.center), uint32(g.inner), uint32(g.outer), uint32(g.innerSq), uint32(g.outerSq)}, true
	case *rectGoal:
		return 6, []uint32{uint32(g.rect.Min.X), uint32(g.rect.Max.X), uint32(g.rect.Min.Z), uint32(g.rect.Max.Z)}, true
	}
	return 0, nil, false
}
