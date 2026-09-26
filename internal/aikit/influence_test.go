package aikit

import "testing"

// refAddDisc is AddDisc with a square root per cell, the form it replaced.
func refAddDisc(g *Grid, x, z, radius, value int32) {
	if value == 0 {
		return
	}
	cx, cz := x/SectorWorld, z/SectorWorld
	r := radius / SectorWorld
	if r < 0 {
		r = 0
	}
	for sz := cz - r; sz <= cz+r; sz++ {
		if sz < 0 || sz >= g.H {
			continue
		}
		for sx := cx - r; sx <= cx+r; sx++ {
			if sx < 0 || sx >= g.W {
				continue
			}
			dx, dz := sx-cx, sz-cz
			d := int32(ISqrt64(int64(dx*dx + dz*dz)))
			if d > r {
				continue
			}
			v := value
			if r > 0 {
				v = int32(int64(value) * int64(r+1-d) / int64(r+1))
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

// AddDisc steps distances along each row instead of taking a square root
// per cell; every cell must come out as the per-cell form leaves it, for
// discs on and off the grid, of every size (past the share table too), with
// either sign and at the clamp.
func TestAddDiscMatchesPerCellForm(t *testing.T) {
	rnd := testRand(5)
	m := &MapInfo{SectorW: 37, SectorH: 23}
	got, want := NewGrid(m), NewGrid(m)
	for i := 0; i < 4000; i++ {
		x, z := rnd(8000)-1500, rnd(5000)-1500
		radius := rnd(3000) - 100
		if i%50 == 0 {
			radius = 64*SectorWorld + rnd(20000) // past the share table
		}
		value := rnd(4000) - 2000
		if i%97 == 0 {
			value = 1<<30 - rnd(3) // drives cells to the clamp
		}
		got.AddDisc(x, z, radius, value)
		refAddDisc(want, x, z, radius, value)
		for k := range want.V {
			if got.V[k] != want.V[k] {
				t.Fatalf("disc %d (%d, %d) r %d v %d: cell %d = %d, want %d", i, x, z, radius, value, k, got.V[k], want.V[k])
			}
		}
	}
}
