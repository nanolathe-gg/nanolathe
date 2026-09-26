package tactics

import "github.com/nanolathe-gg/nanolathe/internal/aikit"

// addDisc adds value to g at (x, z) falling off linearly to zero at radius,
// exactly as aikit.Grid.AddDisc does (same cells, same integer rounding,
// same clamp), but reads each cell's falloff from a kernel computed once
// per radius instead of taking a square root per cell. The picture paints
// hundreds of discs every think; this is most of its cost.
func (a *Army) addDisc(g *aikit.Grid, x, z, radius, value int32) {
	if value == 0 {
		return
	}
	cx, cz := x/aikit.SectorWorld, z/aikit.SectorWorld
	r := radius / aikit.SectorWorld
	if r < 0 {
		r = 0
	}
	if r > maxKernel {
		g.AddDisc(x, z, radius, value) // rare (strategic weapons): no kernel kept
		return
	}
	k := a.kernel(r)
	side := 2*r + 1
	for sz := cz - r; sz <= cz+r; sz++ {
		if sz < 0 || sz >= g.H {
			continue
		}
		row := k[(sz-cz+r)*side:]
		for sx := cx - r; sx <= cx+r; sx++ {
			if sx < 0 || sx >= g.W {
				continue
			}
			f := row[sx-cx+r]
			if f < 0 {
				continue
			}
			v := value
			if r > 0 {
				v = int32(int64(value) * int64(f) / int64(r+1))
			}
			i := sz*g.W + sx
			s := int64(g.V[i]) + int64(v)
			if s > 1<<30 {
				s = 1 << 30
			} else if s < -(1 << 30) {
				s = -(1 << 30)
			}
			g.V[i] = int32(s)
		}
	}
}

// addHurt adds fresh damage at (x, z) to the hurt grid, a one-sector disc,
// listing the sector in hurtCells when it starts holding some.
func (a *Army) addHurt(x, z, value int32) {
	g := a.hurt
	cx, cz := x/aikit.SectorWorld, z/aikit.SectorWorld
	if cx < 0 || cz < 0 || cx >= g.W || cz >= g.H {
		return
	}
	i := cz*g.W + cx
	was := g.V[i]
	a.addDisc(g, x, z, 0, value)
	if was == 0 && g.V[i] != 0 {
		a.hurtCells = append(a.hurtCells, i)
	}
}

// maxKernel is the largest radius (sectors) given a cached kernel.
const maxKernel = 48

// kernel returns the falloff weights (r + 1 − distance, −1 outside the
// disc) of a disc of radius r sectors, row-major over the (2r+1)² square.
// Built on first use of each radius and kept.
func (a *Army) kernel(r int32) []int32 {
	for int32(len(a.kernels)) <= r {
		a.kernels = append(a.kernels, nil)
	}
	if k := a.kernels[r]; k != nil {
		return k
	}
	side := 2*r + 1
	k := make([]int32, side*side)
	for dz := -r; dz <= r; dz++ {
		for dx := -r; dx <= r; dx++ {
			d := int32(aikit.ISqrt64(int64(dx*dx + dz*dz)))
			f := r + 1 - d
			if d > r {
				f = -1
			}
			k[(dz+r)*side+(dx+r)] = f
		}
	}
	a.kernels[r] = k
	return k
}
