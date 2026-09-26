package aikit

// Grid is an integer map over the sector grid (SectorWorld units per cell).
// Brains use it for threat, value and control maps. All operations are
// integer and walk cells in row-major order.
type Grid struct {
	W, H int32
	V    []int32
}

// NewGrid allocates a grid matching the map's sectors.
func NewGrid(m *MapInfo) *Grid {
	g := &Grid{W: m.SectorW, H: m.SectorH}
	g.V = make([]int32, g.W*g.H)
	return g
}

// Clear zeroes every cell.
func (g *Grid) Clear() {
	for i := range g.V {
		g.V[i] = 0
	}
}

// At returns the value at a world point, 0 off the grid.
func (g *Grid) At(x, z int32) int32 {
	// Division truncates toward zero, so a point up to a sector west or
	// north of the map would otherwise read sector 0.
	if x < 0 || z < 0 {
		return 0
	}
	sx, sz := x/SectorWorld, z/SectorWorld
	if sx >= g.W || sz >= g.H {
		return 0
	}
	return g.V[sz*g.W+sx]
}

// AddDisc adds value at the centre sector falling off linearly to zero at
// radius (world units). Values are clamped to stay inside int32.
//
// A cell at whole-sector distance d (the integer square root of its squared
// offset) gets value·(r+1−d)/(r+1), and cells with d > r nothing. Along one
// row d only grows away from the centre column, so it is stepped rather than
// taking a square root per cell, and each distance's share is divided once
// per disc; the cells and their sums are exactly those of the per-cell form.
func (g *Grid) AddDisc(x, z, radius, value int32) {
	if value == 0 {
		return
	}
	cx, cz := x/SectorWorld, z/SectorWorld
	r := radius / SectorWorld
	if r < 0 {
		r = 0
	}
	// share[d] for the distances a disc this size reaches, when small.
	var buf [64]int32
	var share []int32
	if r < int32(len(buf)) {
		share = buf[:r+1]
		for d := int32(0); d <= r; d++ {
			share[d] = discShare(value, r, d)
		}
	}
	for sz := cz - r; sz <= cz+r; sz++ {
		if sz < 0 || sz >= g.H {
			continue
		}
		dz := int64(sz - cz)
		row := g.V[sz*g.W : (sz+1)*g.W]
		// Right half (dx ≥ 0), then left half (dx < 0), each walked away from
		// the centre column so d can be stepped.
		for side := int32(1); side >= -1; side -= 2 {
			dx := int32(0)
			if side < 0 {
				dx = 1
			}
			d := int64(absI32(int32(dz))) // isqrt(dz²) = |dz|, then grows with |dx|
			for ; dx <= r; dx++ {
				s := int64(dx)*int64(dx) + dz*dz
				for (d+1)*(d+1) <= s {
					d++
				}
				if d > int64(r) {
					break
				}
				sx := cx + side*dx
				if sx < 0 || sx >= g.W {
					if (side > 0 && sx >= g.W) || (side < 0 && sx < 0) {
						break // the rest of this half is off the grid
					}
					continue
				}
				var v int32
				if share != nil {
					v = share[d]
				} else {
					v = discShare(value, r, int32(d))
				}
				t := int64(row[sx]) + int64(v)
				if t > 1<<30 {
					t = 1 << 30
				} else if t < -(1 << 30) {
					t = -(1 << 30)
				}
				row[sx] = int32(t)
			}
		}
	}
}

// discShare is a disc's value at whole-sector distance d from its centre.
func discShare(value, r, d int32) int32 {
	if r <= 0 {
		return value
	}
	return int32(int64(value) * int64(r+1-d) / int64(r+1))
}

// SumDisc sums cells within radius of a world point.
func (g *Grid) SumDisc(x, z, radius int32) int64 {
	cx, cz := x/SectorWorld, z/SectorWorld
	r := radius / SectorWorld
	var s int64
	for sz := cz - r; sz <= cz+r; sz++ {
		if sz < 0 || sz >= g.H {
			continue
		}
		for sx := cx - r; sx <= cx+r; sx++ {
			if sx < 0 || sx >= g.W {
				continue
			}
			dx, dz := sx-cx, sz-cz
			if dx*dx+dz*dz > r*r {
				continue
			}
			s += int64(g.V[sz*g.W+sx])
		}
	}
	return s
}

// Snapshot copies the grid for an Explain.
func (g *Grid) Snapshot(name string) NamedGrid {
	v := make([]int32, len(g.V))
	copy(v, g.V)
	return NamedGrid{Name: name, W: g.W, H: g.H, Values: v}
}

// LineMax returns the largest cell value sampled along the segment from
// (ax, az) to (bx, bz), one sample per sector — a cheap "how dangerous is
// this route" query.
func (g *Grid) LineMax(ax, az, bx, bz int32) int32 {
	d := Dist(ax, az, bx, bz)
	n := d/SectorWorld + 1
	var best int32
	for i := int32(0); i <= n; i++ {
		x := ax + int32(int64(bx-ax)*int64(i)/int64(n))
		z := az + int32(int64(bz-az)*int64(i)/int64(n))
		if v := g.At(x, z); v > best {
			best = v
		}
	}
	return best
}
