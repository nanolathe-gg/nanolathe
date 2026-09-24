package survival

// Regions labels the 4-connected passable components of one movement class
// over the map's cells (DESIGN_SURVIVAL §4.2, §6.6). Label 0 is impassable;
// components are numbered from 1 in row-major discovery order, so labels are a
// pure function of the passability predicate.
type Regions struct {
	W, H  int32
	label []int32
	sizes []int32 // sizes[id] for id ≥ 1; sizes[0] unused
}

// Label flood-fills every passable cell.
func Label(w, h int32, passable func(x, z int32) bool) Regions {
	r := Regions{W: w, H: h}
	if w <= 0 || h <= 0 {
		return r
	}
	r.label = make([]int32, int(w)*int(h))
	r.sizes = []int32{0}
	var stack []int32
	for z := int32(0); z < h; z++ {
		for x := int32(0); x < w; x++ {
			i := z*w + x
			if r.label[i] != 0 || !passable(x, z) {
				continue
			}
			id := int32(len(r.sizes))
			r.sizes = append(r.sizes, 0)
			r.label[i] = id
			stack = append(stack[:0], i)
			for len(stack) > 0 {
				c := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				r.sizes[id]++
				cx, cz := c%w, c/w
				for _, d := range [4][2]int32{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					nx, nz := cx+d[0], cz+d[1]
					if nx < 0 || nz < 0 || nx >= w || nz >= h {
						continue
					}
					ni := nz*w + nx
					if r.label[ni] != 0 || !passable(nx, nz) {
						continue
					}
					r.label[ni] = id
					stack = append(stack, ni)
				}
			}
		}
	}
	return r
}

// At is the component at a cell, 0 when impassable or outside the map.
func (r Regions) At(x, z int32) int32 {
	if x < 0 || z < 0 || x >= r.W || z >= r.H || r.label == nil {
		return 0
	}
	return r.label[z*r.W+x]
}

// Largest is the component with the most cells, the lowest id on a tie; 0 when
// nothing is passable.
func (r Regions) Largest() int32 {
	best := int32(0)
	for id := int32(1); id < int32(len(r.sizes)); id++ {
		if best == 0 || r.sizes[id] > r.sizes[best] {
			best = id
		}
	}
	return best
}

// Nearest returns the cell of component id nearest (x, z) by squared distance,
// ties by row then column, searching outward ring by ring over the map.
func (r Regions) Nearest(id, x, z int32) (int32, int32, bool) {
	maxR := r.W
	if r.H > maxR {
		maxR = r.H
	}
	return r.NearestWithin(id, x, z, maxR)
}

// NearestWithin is Nearest limited to rings of at most maxR cells.
func (r Regions) NearestWithin(id, x, z, maxR int32) (int32, int32, bool) {
	if id == 0 || r.label == nil {
		return 0, 0, false
	}
	bestD := int64(-1)
	var bx, bz int32
	for rad := int32(0); rad <= maxR; rad++ {
		// Any cell found at ring rad has distance ≥ rad²; once the best found
		// is below the next ring's minimum, stop.
		if bestD >= 0 && bestD < int64(rad)*int64(rad) {
			break
		}
		for cz := z - rad; cz <= z+rad; cz++ {
			for cx := x - rad; cx <= x+rad; cx++ {
				if cx != x-rad && cx != x+rad && cz != z-rad && cz != z+rad {
					continue
				}
				if r.At(cx, cz) != id {
					continue
				}
				dx, dz := int64(cx-x), int64(cz-z)
				d := dx*dx + dz*dz
				if bestD < 0 || d < bestD || (d == bestD && (cz < bz || (cz == bz && cx < bx))) {
					bestD, bx, bz = d, cx, cz
				}
			}
		}
	}
	return bx, bz, bestD >= 0
}
