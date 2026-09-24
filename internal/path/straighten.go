package path

// Nanolathe Modern route straightening (DESIGN_MOVEMENT_PATH "Modern route
// straightening"). The retail search reconstructs a route from its turn cells
// [04 R-PATH-01 §7], and for a goal a little off the start's row or column its
// grid path can alternate between two parallel lines every few cells: a
// sawtooth of up to 64 turn points, of which the publisher keeps the first 20.
// A slow-turning unit crawls along such a route. StraightenKernel runs the
// retail search unchanged and, on the slice it finishes, drops each interior
// turn whose two neighbours share a row or a column and are joined by a
// passable straight line of at most straightenSpan cells. Diagonal approach
// legs are never shortcut, so a column still funnels into a choke as retail's
// route leads it.

// straightenSpan bounds one shortcut, in cells along its row or column.
// Nanolathe Modern policy tuning.
const straightenSpan = 16

// straightenCap is the retail reconstruction's turn-point cap; a longer result
// is published unchanged.
const straightenCap = 64

// StraightenKernel opens the retail search and straightens its sawtooth turns.
// It is zero size, so holding it in a Kernel never allocates.
type StraightenKernel struct{}

// NewSession opens a retail search for cfg behind the straightening pass.
func (StraightenKernel) NewSession(cfg SearchConfig) Search {
	return &straightenSearch{Search: NewSession(cfg), cfg: cfg}
}

type straightenSearch struct {
	Search
	cfg    SearchConfig
	probes int
	points [straightenCap]Point
	out    []Point
	done   bool
	status Status
}

// Popped includes one unit of work per cell the straightening probed, charged
// against the player's share like a heap pop [04 §7.3].
func (s *straightenSearch) Popped() int { return s.Search.Popped() + s.probes }

// Resume runs the retail search; on the slice it finishes, the route is
// straightened before it is returned, so publication happens on exactly the
// slice the retail search would publish on.
func (s *straightenSearch) Resume(budget int) ([]Point, Status, bool) {
	if s.done {
		return s.out, s.status, true
	}
	points, status, done := s.Search.Resume(budget)
	if !done {
		return points, status, done
	}
	s.done, s.status, s.out = true, status, points
	if len(points) < 3 || len(points) > straightenCap || s.cfg.PassableValue == nil {
		return points, status, true
	}
	n := copy(s.points[:], points)
	for i := 0; i+2 < n; {
		if s.straightLine(s.points[i], s.points[i+2]) {
			copy(s.points[i+1:n], s.points[i+2:n])
			n--
			continue
		}
		i++
	}
	s.out = s.points[:n]
	return s.out, status, true
}

// straightLine reports whether a and b, route points of this search's
// footprint, share a row or a column within straightenSpan cells and every
// anchor between them is passable.
func (s *straightenSearch) straightLine(a, b Point) bool {
	ca, aok := s.anchor(a)
	cb, bok := s.anchor(b)
	if !aok || !bok || ca.X != cb.X && ca.Z != cb.Z {
		return false
	}
	dx, dz := int32(1), int32(0)
	steps := cb.X - ca.X
	if ca.X == cb.X {
		dx, dz, steps = 0, 1, cb.Z-ca.Z
	}
	if steps < 0 {
		dx, dz, steps = -dx, -dz, -steps
	}
	if steps > straightenSpan {
		return false
	}
	for k := int32(1); k < steps; k++ {
		c := Cell{X: ca.X + dx*k, Z: ca.Z + dz*k}
		s.probes++
		if s.cfg.HasBounds && !InBounds(c, s.cfg.Bounds) || s.cfg.PassableValue(c) == 0 {
			return false
		}
	}
	return true
}

// anchor inverts the route's cell-to-world conversion [04 R-PATH-01 §7].
func (s *straightenSearch) anchor(p Point) (Cell, bool) {
	x, z := p.X-8*s.cfg.FootPrintX, p.Z-8*s.cfg.FootPrintZ
	if x%16 != 0 || z%16 != 0 {
		return Cell{}, false
	}
	return Cell{X: x / 16, Z: z / 16}, true
}
