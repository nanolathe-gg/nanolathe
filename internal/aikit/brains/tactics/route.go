package tactics

import "github.com/nanolathe-gg/nanolathe/internal/aikit"

// router is a small Dijkstra search over the sector grid. Cell costs come
// from a caller-filled slice; the step between 8-neighbours costs the mean
// of the two cells, times 10 straight or 14 diagonal. All buffers are
// reused, so a search allocates nothing once warmed up.
type router struct {
	w, h int32
	cost []int32 // per sector, filled by the caller before search
	dist []int32
	prev []int32
	heap []int64 // (distance << 20) | node
	path []int32 // start … goal after search
}

func (r *router) init(w, h int32) {
	n := int(w * h)
	r.w, r.h = w, h
	r.cost = make([]int32, n)
	r.dist = make([]int32, n)
	r.prev = make([]int32, n)
	r.heap = make([]int64, 0, n)
	r.path = make([]int32, 0, 64)
}

const routeInf = int32(1 << 30)

func (r *router) push(d int32, node int32) {
	r.heap = append(r.heap, int64(d)<<20|int64(node))
	i := len(r.heap) - 1
	for i > 0 {
		p := (i - 1) / 2
		if r.heap[p] <= r.heap[i] {
			break
		}
		r.heap[p], r.heap[i] = r.heap[i], r.heap[p]
		i = p
	}
}

func (r *router) pop() (int32, int32) {
	top := r.heap[0]
	last := len(r.heap) - 1
	r.heap[0] = r.heap[last]
	r.heap = r.heap[:last]
	i := 0
	for {
		l, rt := 2*i+1, 2*i+2
		m := i
		if l < last && r.heap[l] < r.heap[m] {
			m = l
		}
		if rt < last && r.heap[rt] < r.heap[m] {
			m = rt
		}
		if m == i {
			break
		}
		r.heap[m], r.heap[i] = r.heap[i], r.heap[m]
		i = m
	}
	return int32(top >> 20), int32(top & (1<<20 - 1))
}

var nbrDX = [8]int32{1, -1, 0, 0, 1, 1, -1, -1}
var nbrDZ = [8]int32{0, 0, 1, -1, 1, -1, 1, -1}

// search finds the cheapest sector path from `from` to `to` and leaves it
// in r.path. It returns the path cost, or routeInf when unreachable.
func (r *router) search(from, to int32) int32 {
	for i := range r.dist {
		r.dist[i] = routeInf
		r.prev[i] = -1
	}
	r.heap = r.heap[:0]
	r.path = r.path[:0]
	r.dist[from] = 0
	r.push(0, from)
	for len(r.heap) > 0 {
		d, node := r.pop()
		if d > r.dist[node] {
			continue
		}
		if node == to {
			break
		}
		x, z := node%r.w, node/r.w
		for k := 0; k < 8; k++ {
			nx, nz := x+nbrDX[k], z+nbrDZ[k]
			if nx < 0 || nz < 0 || nx >= r.w || nz >= r.h {
				continue
			}
			nb := nz*r.w + nx
			step := (r.cost[node] + r.cost[nb]) / 2
			if k < 4 {
				step *= 10
			} else {
				step *= 14
			}
			nd := d + step
			if nd < r.dist[nb] {
				r.dist[nb] = nd
				r.prev[nb] = node
				r.push(nd, nb)
			}
		}
	}
	if r.dist[to] >= routeInf {
		return routeInf
	}
	for n := to; n >= 0; n = r.prev[n] {
		r.path = append(r.path, n)
		if n == from {
			break
		}
	}
	for i, j := 0, len(r.path)-1; i < j; i, j = i+1, j-1 {
		r.path[i], r.path[j] = r.path[j], r.path[i]
	}
	return r.dist[to]
}

// lineCost is the cost of walking the straight sector line from a to b,
// in the same units as search.
func (r *router) lineCost(m *aikit.MapInfo, ax, az, bx, bz int32) int32 {
	d := aikit.Dist(ax, az, bx, bz)
	n := d/aikit.SectorWorld + 1
	var sum int64
	for i := int32(0); i <= n; i++ {
		x := ax + int32(int64(bx-ax)*int64(i)/int64(n))
		z := az + int32(int64(bz-az)*int64(i)/int64(n))
		sum += int64(r.cost[m.Sector(x, z)])
	}
	// Per-sector steps cost ×10 in search; the samples are one sector apart.
	sum = sum * 10 * int64(n) / int64(n+1)
	if sum > int64(routeInf) {
		return routeInf
	}
	return int32(sum)
}

// maxWaypoints bounds a route's queued moves (each is one player action).
const maxWaypoints = 3

// waypoints compresses r.path into at most maxWaypoints turning points,
// skipping the start. A turning point is kept where the path's heading
// changes after a run of at least minRun sectors. ok(sector) filters points
// (known ground); a rejected point is replaced by the nearest accepted
// sector earlier on the path. Every turn is appended to dst before an even
// subset is kept, so dst may grow past maxWaypoints (and reallocate): use
// the returned slice.
func (r *router) waypoints(m *aikit.MapInfo, dst [][2]int32, known []uint8) [][2]int32 {
	dst = dst[:0]
	p := r.path
	if len(p) < 3 {
		return dst
	}
	const minRun = 3
	last := 0
	pdx, pdz := int32(0), int32(0)
	for i := 1; i < len(p); i++ {
		dx := p[i]%r.w - p[i-1]%r.w
		dz := p[i]/r.w - p[i-1]/r.w
		if i > 1 && (dx != pdx || dz != pdz) && i-1-last >= minRun && i-1 < len(p)-minRun {
			// Turn at p[i-1]: keep it, pulled back to known ground.
			k := i - 1
			for k > last && known[p[k]] == 0 {
				k--
			}
			if k > last {
				x, z := m.SectorCentre(p[k])
				dst = append(dst, [2]int32{x, z})
				last = k
			}
		}
		pdx, pdz = dx, dz
	}
	// Too many turns: keep an even subset.
	if len(dst) > maxWaypoints {
		n := len(dst)
		for i := 0; i < maxWaypoints; i++ {
			dst[i] = dst[(i+1)*n/(maxWaypoints+1)]
		}
		dst = dst[:maxWaypoints]
	}
	return dst
}
