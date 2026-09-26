package aikit

import (
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The analysis's faster forms must give exactly the answers of the plain
// ones they replaced: the spot list of a full sort (AnalyzeMap selects from a
// heap over precomputed footprint sums) and Reach.Dist's nearest anchor from
// a full window scan (it walks outward and stops early).

// refSpots is the plain spot search: every local maximum, sorted.
func refSpots(t *world.Terrain, footX, footZ, ownX, ownZ int32) [][2]int32 {
	w, h := t.CellW, t.CellH
	metal := func(x, z int32) int32 { return int32(t.Plot[z*w+x].Metal()) }
	sum := func(x, z int32) int32 {
		var s int32
		for j := z; j < z+footZ; j++ {
			for i := x; i < x+footX; i++ {
				s += metal(i, j)
			}
		}
		return s
	}
	type cand struct {
		x, z, s int32
		d       int64
	}
	var cands []cand
	var best int32
	for z := int32(1); z+footZ < h-1; z++ {
		for x := int32(1); x+footX < w-1; x++ {
			s := sum(x, z)
			if s <= 0 {
				continue
			}
			if s < sum(x-1, z) || s < sum(x+1, z) || s < sum(x, z-1) || s < sum(x, z+1) ||
				s < sum(x-1, z-1) || s < sum(x+1, z+1) || s < sum(x-1, z+1) || s < sum(x+1, z-1) {
				continue
			}
			if s == sum(x-1, z) && s == sum(x, z-1) && (x%footX != 0 || z%footZ != 0) {
				continue
			}
			dx, dz := int64(x*16+footX*8-ownX), int64(z*16+footZ*8-ownZ)
			cands = append(cands, cand{x, z, s, dx*dx + dz*dz})
			if s > best {
				best = s
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.s != b.s {
			return a.s > b.s
		}
		if a.d != b.d {
			return a.d < b.d
		}
		if a.z != b.z {
			return a.z < b.z
		}
		return a.x < b.x
	})
	var out [][2]int32
	for _, c := range cands {
		if len(out) >= maxSpots || c.s < best/6 {
			break
		}
		overlap := false
		for _, o := range out {
			if absI32(o[0]-c.x) < footX+1 && absI32(o[1]-c.z) < footZ+1 {
				overlap = true
				break
			}
		}
		if !overlap {
			out = append(out, [2]int32{c.x, c.z})
		}
	}
	return out
}

func testRand(seed uint32) func(n int32) int32 {
	return func(n int32) int32 {
		seed = seed*1664525 + 1013904223
		return int32((seed >> 8) % uint32(n))
	}
}

func TestAnalyzeMapSpotsMatchFullSort(t *testing.T) {
	for _, c := range []struct {
		name  string
		metal func(x, z int32, rnd func(int32) int32) uint8
	}{
		{"patches", func(x, z int32, rnd func(int32) int32) uint8 {
			if rnd(40) == 0 {
				return uint8(rnd(200))
			}
			return 0
		}},
		{"metal map", func(x, z int32, rnd func(int32) int32) uint8 { return uint8(20 + rnd(2)) }},
		{"plateau", func(x, z int32, rnd func(int32) int32) uint8 { return 17 }},
	} {
		rnd := testRand(7)
		ter := &world.Terrain{CellW: 150, CellH: 110, SeaLevel: 30, Plot: make([]world.PlotCell, 150*110)}
		for z := int32(0); z < ter.CellH; z++ {
			for x := int32(0); x < ter.CellW; x++ {
				p := &ter.Plot[z*ter.CellW+x]
				p.SetMetal(c.metal(x, z, rnd))
				p.SetHeight(uint8(rnd(60)))
				p.SetFeature(world.PlotFeatureNone)
			}
		}
		for _, foot := range [][2]int32{{2, 2}, {3, 3}, {2, 3}} {
			m := AnalyzeMap(ter, nil, foot[0], foot[1], 700, 900)
			want := refSpots(ter, foot[0], foot[1], 700, 900)
			if len(m.Spots) != len(want) {
				t.Fatalf("%s %v: %d spots, want %d", c.name, foot, len(m.Spots), len(want))
			}
			for i, s := range m.Spots {
				if s.CellX != want[i][0] || s.CellZ != want[i][1] {
					t.Fatalf("%s %v: spot %d at (%d, %d), want (%d, %d)", c.name, foot, i, s.CellX, s.CellZ, want[i][0], want[i][1])
				}
			}
		}
	}
}

func TestReachDistMatchesFullScan(t *testing.T) {
	rnd := testRand(11)
	w, h := int32(120), int32(90)
	m := &MapInfo{CellW: w, CellH: h, SeaLevel: 50}
	m.cellLo = make([]uint8, w*h)
	m.cellHi = make([]uint8, w*h)
	m.cellVoid = make([]bool, w*h)
	for i := range m.cellLo {
		v := uint8(60)
		if rnd(3) == 0 {
			v = 10 // scattered water breaks the land into many regions
		}
		m.cellLo[i], m.cellHi[i] = v, v
	}
	for _, foot := range [][2]int32{{1, 1}, {2, 2}, {3, 2}} {
		r := m.Reach(MoveClass{MinDepth: -10000, MaxDepth: 0, MaxSlope: 10, MaxWaterSlope: 255, FootX: foot[0], FootZ: foot[1]})
		full := func(region, x, z, radius int32) int32 {
			best := int64(-1)
			lim := int64(radius) * int64(radius)
			for cz := int32(0); cz < h; cz++ {
				for cx := int32(0); cx < w; cx++ {
					if int32(r.label[cz*w+cx]) != region {
						continue
					}
					d := Dist2(cx*16+foot[0]*8, cz*16+foot[1]*8, x, z)
					if d <= lim && (best < 0 || d < best) {
						best = d
					}
				}
			}
			if best < 0 {
				return -1
			}
			return int32(ISqrt64(best))
		}
		for i := 0; i < 3000; i++ {
			x, z := rnd(w*16+400)-200, rnd(h*16+400)-200
			region := 1 + rnd(r.Regions()+1)
			radius := rnd(1200)
			if got, want := r.Dist(region, x, z, radius), full(region, x, z, radius); got != want {
				t.Fatalf("foot %v: Dist(%d, %d, %d, %d) = %d, want %d", foot, region, x, z, radius, got, want)
			}
		}
	}
}
