package path

// Goal families [04 §7.2] C8 and enumeration [04 §7.4].
//
// Four families share the Goal interface (types.go): point/radius,
// annulus (stand-off), rectangle perimeter, and saved/restored.
//
// - Point/radius: h = max(18*max(|dx|,|dz|)+7*min(|dx|,|dz|)-R, 0)
//   where R is the raw authored radius; arrival is a squared-distance
//   test against a >>4-quantized radius [04 §7.2].
// - Annulus: same inflated octile with V-shaped zero band inside
//   [inner,outer] raw radii, rising as oct-outer outward and inner-oct
//   inward; arrival uses >>4-quantized squared radii [04 §7.2], [04 §7.4].
// - Rectangle perimeter: admissible 16*max+6*min outside, and inside
//   16*min(distance to each edge); goal cells exactly the border [04 §7.2].
// - Saved: h identically zero (Dijkstra), null start predicate [04 §7.2].
//
// The annulus radius-unit mismatch (raw authored radii in the heuristic
// clamp vs >>4-quantized squared radii in arrival) is REAL and reproduced,
// not fixed. Both sides are documented here with citations.
// TODO(question): annulus radius-unit mismatch [04 §7.4] — heuristic
// compares raw radii, arrival compares quantized radii; unresolved.

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

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// pointGoal implements the point/radius family [04 §7.2].
type pointGoal struct {
	center Cell
	radius int32 // raw authored radius, compared to oct [04 §7.2]
}

// PointGoal creates a point/radius goal [04 §7.2] C8.
// h = max(18*max(|dx|,|dz|)+7*min(|dx|,|dz|)-R, 0).
// Arrival (StartSatisfied) is a squared-distance test against a
// >>4-quantized radius [04 §7.2], [04 §7.4].
func PointGoal(center Cell, radius int32) Goal {
	return &pointGoal{center: center, radius: radius}
}

func (g *pointGoal) H(c Cell) int32 {
	dx := abs32(c.X - g.center.X)
	dz := abs32(c.Z - g.center.Z)
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
	// Arrival is a squared-distance test against >>4-quantized radius [04 §7.2].
	// TODO(question): radius-unit mismatch note — point arrival quantizes,
	// while point h clamps against raw radius [04 §7.2], [04 §7.4].
	q := g.radius >> 4 // [04 §7.2] >>4 quantization
	if q < 0 {
		q = 0
	}
	dx := start.X - g.center.X
	dz := start.Z - g.center.Z
	distSq := int64(dx)*int64(dx) + int64(dz)*int64(dz)
	qSq := int64(q) * int64(q)
	return distSq <= qSq
}

// annulusGoal implements the stand-off annulus family [04 §7.2].
type annulusGoal struct {
	center Cell
	inner  int32 // raw authored inner radius [04 §7.2], [04 §7.4]
	outer  int32 // raw authored outer radius
}

// AnnulusGoal creates an annulus (stand-off) goal [04 §7.2] C8.
// Same inflated octile as point, with V-shaped zero band inside
// [inner,outer] raw radii: h is zero inside, rises as oct-outer outward
// and inner-oct inward. Arrival uses >>4-quantized squared radii [04 §7.4].
// The radius-unit mismatch is reproduced, not fixed.
// TODO(question): annulus radius-unit mismatch (raw in h, quantized in arrival) [04 §7.4].
func AnnulusGoal(center Cell, inner, outer int32) Goal {
	return &annulusGoal{center: center, inner: inner, outer: outer}
}

func (g *annulusGoal) H(c Cell) int32 {
	// [04 §7.2] same inflated octile with V-shaped zero band.
	// h = 0 inside [inner,outer]; oct-outer outward; inner-oct inward.
	// Raw authored radii in this clamp [04 §7.4].
	dx := abs32(c.X - g.center.X)
	dz := abs32(c.Z - g.center.Z)
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
	// Arrival predicate uses >>4-quantized squared radii [04 §7.4].
	// Mismatch: h clamp above uses raw radii, this test uses quantized [04 §7.4] TODO(question).
	qInner := g.inner >> 4
	qOuter := g.outer >> 4
	if qInner < 0 {
		qInner = 0
	}
	if qOuter < 0 {
		qOuter = 0
	}
	dx := start.X - g.center.X
	dz := start.Z - g.center.Z
	distSq := int64(dx)*int64(dx) + int64(dz)*int64(dz)
	innerSq := int64(qInner) * int64(qInner)
	outerSq := int64(qOuter) * int64(qOuter)
	// Band is inclusive [innerSq, outerSq]; if inner>outer no cell satisfies (zero band empty).
	return distSq >= innerSq && distSq <= outerSq
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

// savedGoal implements restored-from-save goals [04 §7.2].
type savedGoal struct {
	cells []Cell
}

// SavedGoal creates a saved/restored-from-save goal [04 §7.2] C8.
// h identically zero, giving pure Dijkstra behavior [04 §7.2].
// Start predicate is null (always false) per [04 §7.2]; acceptance is
// decided solely by enumerated cells. The stored list is copied.
func SavedGoal(cells []Cell) Goal {
	cp := make([]Cell, len(cells))
	copy(cp, cells)
	return &savedGoal{cells: cp}
}

func (g *savedGoal) H(c Cell) int32 {
	// Identically zero [04 §7.2].
	return 0
}

func (g *savedGoal) Enumerate(out []Cell) []Cell {
	if out == nil {
		// Return a copy to preserve immutability.
		cp := make([]Cell, len(g.cells))
		copy(cp, g.cells)
		return cp
	}
	out = out[:0]
	out = append(out, g.cells...)
	return out
}

func (g *savedGoal) StartSatisfied(start Cell) bool {
	// Null start predicate [04 §7.2] — always false, even if start is listed.
	// Acceptance is via enumerated cells, not this early-exit [04 §7.2].
	return false
}
