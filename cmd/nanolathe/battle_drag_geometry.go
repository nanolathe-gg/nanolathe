package main

import (
	"cmp"
	"math"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// dragPoint holds whole world coordinates, or footprint cells for build rows.
// This input geometry implements the modern policy in
// DESIGN_INTERFACE_HUD_INPUT §3.11; it is not retail behavior.
type dragPoint struct{ x, z int32 }

const dragBuildSiteLimit = 1024

// dragBuildGapMax bounds the wheel-chosen gap between build-drag sites, in
// footprint cells. It is a UI bound, not a retail constant.
const dragBuildGapMax = 16

// dragBuildCells starts at the press anchor and stops before a footprint step
// would pass the release anchor. Each step is the footprint plus gap free
// cells on its axis. Grid rows walk X first, then Z, in drag order.
func dragBuildCells(start, end dragPoint, footX, footZ, gap int32, grid bool) []dragPoint {
	if footX <= 0 || footZ <= 0 || gap < 0 {
		return nil
	}
	dx, dz := int64(end.x)-int64(start.x), int64(end.z)-int64(start.z)
	ax, az := numeric.Abs(dx), numeric.Abs(dz)
	fx, fz := int64(footX)+int64(gap), int64(footZ)+int64(gap)
	if grid {
		cols, rows := ax/fx+1, az/fz+1
		capacity := min(min(cols, dragBuildSiteLimit)*min(rows, dragBuildSiteLimit), dragBuildSiteLimit)
		points := make([]dragPoint, 0, capacity)
		for row := int64(0); row < rows && len(points) < dragBuildSiteLimit; row++ {
			for col := int64(0); col < cols && len(points) < dragBuildSiteLimit; col++ {
				points = append(points, dragPoint{
					x: int32(int64(start.x) + dragSigned(col*fx, dx)),
					z: int32(int64(start.z) + dragSigned(row*fz, dz)),
				})
			}
		}
		return points
	}
	// Cross multiplication compares spans measured in their own steps,
	// rather than preferring the longer unscaled coordinate delta.
	xMajor := ax*fz >= az*fx
	span, step, minor := ax, fx, az
	if !xMajor {
		span, step, minor = az, fz, ax
	}
	points := make([]dragPoint, 0, min(span/step+1, dragBuildSiteLimit))
	for travel := int64(0); travel <= span && len(points) < dragBuildSiteLimit; travel += step {
		offset := int64(0)
		if span != 0 {
			// Unsigned intermediates cover even opposite int32 endpoints. Exact
			// halves round toward the dragged endpoint, also on reverse drags.
			offset = int64((uint64(minor)*uint64(travel) + uint64(span)/2) / uint64(span))
		}
		x, z := travel, offset
		if !xMajor {
			x, z = offset, travel
		}
		points = append(points, dragPoint{
			x: int32(int64(start.x) + dragSigned(x, dx)),
			z: int32(int64(start.z) + dragSigned(z, dz)),
		})
	}
	return points
}

func dragSigned(magnitude, direction int64) int64 {
	if direction < 0 {
		return -magnitude
	}
	return magnitude
}

// dragSamplePath spaces destinations along the entire traced polyline. The
// floating intermediates are input geometry only; commands receive whole points
// per DESIGN_INTERFACE_HUD_INPUT §3.11. Rounding uses nearest, halves away from zero.
func dragSamplePath(path []dragPoint, count int) []dragPoint {
	if len(path) == 0 || count <= 0 {
		return nil
	}
	distance := make([]float64, len(path))
	for i := 1; i < len(path); i++ {
		dx := float64(path[i].x) - float64(path[i-1].x)
		dz := float64(path[i].z) - float64(path[i-1].z)
		distance[i] = distance[i-1] + math.Hypot(dx, dz)
	}
	total := distance[len(distance)-1]
	points := make([]dragPoint, count)
	segment := 1
	for i := range points {
		if total == 0 || (count > 1 && i == 0) {
			points[i] = path[0]
			continue
		}
		if count > 1 && i == count-1 {
			points[i] = path[len(path)-1]
			continue
		}
		target := total / 2
		if count > 1 {
			target = total * (float64(i) / float64(count-1))
		}
		for segment < len(path)-1 && distance[segment] <= target {
			segment++
		}
		from, to := path[segment-1], path[segment]
		t := (target - distance[segment-1]) / (distance[segment] - distance[segment-1])
		points[i] = dragPoint{
			x: int32(math.Round(float64(from.x) + t*(float64(to.x)-float64(from.x)))),
			z: int32(math.Round(float64(from.z) + t*(float64(to.z)-float64(from.z)))),
		}
	}
	return points
}

// dragAssignDestinations minimizes total straight-line travel for the group
// to within one world pixel.
// The modern input policy is DESIGN_INTERFACE_HUD_INPUT §3.11, not a retail
// rule. Assignment happens once on release; pathfinding still owns obstacles.
// With fewer destinations, only the actor prefix receives assignments.
func dragAssignDestinations(actors, destinations []dragPoint) []dragPoint {
	n, m := min(len(actors), len(destinations)), len(destinations)
	if n == 0 {
		return nil
	}
	// Canonical destination order makes equal-cost choices independent of which
	// end of the same line the player started drawing. Never mutate the preview.
	goals := slices.Clone(destinations)
	slices.SortFunc(goals, func(a, b dragPoint) int {
		if c := cmp.Compare(a.x, b.x); c != 0 {
			return c
		}
		return cmp.Compare(a.z, b.z)
	})
	// Dummy actors make the rectangular case square without charging for unused
	// destinations. Production formations have one destination per actor.
	costs := make([]float64, m*m)
	largest := 0.0
	for i, a := range actors[:n] {
		for j, g := range goals {
			cost := math.Hypot(float64(a.x)-float64(g.x), float64(a.z)-float64(g.z))
			costs[i*m+j] = cost
			largest = max(largest, cost)
		}
	}
	if m == 1 {
		return []dragPoint{goals[0]}
	}
	// Epsilon-scaled auction: a displaced actor bids again, so slot order does
	// not strand later units with distant leftovers. Warm prices let each finer
	// pass refine the previous matching instead of starting from scratch.
	// The final complete assignment costs at most m*epsilon above the optimum,
	// less than a single world pixel across the entire formation.
	prices := make([]float64, m)
	owner := make([]int, m)
	pending := make([]int, 0, m)
	precision := 1 / float64(m+1)
	for epsilon := max(largest/4, precision); ; epsilon = max(epsilon/4, precision) {
		for j := range owner {
			owner[j] = -1
		}
		pending = pending[:0]
		for i := m - 1; i >= 0; i-- {
			pending = append(pending, i)
		}
		for len(pending) > 0 {
			i := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			row := costs[i*m : (i+1)*m]
			best, second, destination := math.Inf(1), math.Inf(1), 0
			for j, cost := range row {
				value := cost + prices[j]
				if value < best || (value == best && owner[j] < 0 && owner[destination] >= 0) {
					best, second, destination = value, best, j
				} else if value < second {
					second = value
				}
			}
			prices[destination] += second - best + epsilon
			if displaced := owner[destination]; displaced >= 0 {
				pending = append(pending, displaced)
			}
			owner[destination] = i
		}
		if epsilon == precision {
			break
		}
		// A common price shift leaves every preference unchanged.
		floor := slices.Min(prices)
		for j := range prices {
			prices[j] -= floor
		}
	}
	assigned := make([]dragPoint, n)
	for j, i := range owner {
		if i < n {
			assigned[i] = goals[j]
		}
	}
	return assigned
}

// dragNearestChain is the order one worker visits an area's targets in
// (DESIGN_INTERFACE_HUD_INPUT §3.11, Nanolathe input policy): from start it
// takes the nearest remaining point, then the point nearest that one, and so
// on. Distances are exact squared whole-unit distances, with ties to the lower
// index, so the walk is the same as a full scan at every step. It returns
// indices into points. A uniform grid keeps a large forest drag well below
// quadratic work.
func dragNearestChain(start dragPoint, points []dragPoint) []int {
	n := len(points)
	order := make([]int, 0, n)
	if n == 0 {
		return order
	}
	minX, minZ, maxX, maxZ := points[0].x, points[0].z, points[0].x, points[0].z
	for _, p := range points[1:] {
		minX, maxX = min(minX, p.x), max(maxX, p.x)
		minZ, maxZ = min(minZ, p.z), max(maxZ, p.z)
	}
	// Cells of at least 64 world units, widened so a side has at most 256.
	span := max(int64(maxX)-int64(minX), int64(maxZ)-int64(minZ)) + 1
	cell := max(int64(64), (span+255)/256)
	cols := int((int64(maxX)-int64(minX))/cell) + 1
	rows := int((int64(maxZ)-int64(minZ))/cell) + 1
	cellOf := func(p dragPoint) (int, int) {
		return int((int64(p.x) - int64(minX)) / cell), int((int64(p.z) - int64(minZ)) / cell)
	}
	buckets := make([][]int32, cols*rows)
	where := make([]int32, n) // each point's position inside its bucket
	for i, p := range points {
		cx, cz := cellOf(p)
		b := cz*cols + cx
		where[i] = int32(len(buckets[b]))
		buckets[b] = append(buckets[b], int32(i))
	}
	remove := func(i int) {
		cx, cz := cellOf(points[i])
		b := cz*cols + cx
		list := buckets[b]
		last := list[len(list)-1]
		list[where[i]], where[last] = last, where[i]
		buckets[b] = list[:len(list)-1]
	}
	dist := func(a, b dragPoint) int64 {
		dx, dz := int64(a.x)-int64(b.x), int64(a.z)-int64(b.z)
		return dx*dx + dz*dz
	}
	best, bestD := -1, int64(0)
	consider := func(from dragPoint, i int) {
		if d := dist(from, points[i]); best < 0 || d < bestD || (d == bestD && i < best) {
			best, bestD = i, d
		}
	}
	// The worker may stand far outside the area, so its first pick scans all.
	for i := range points {
		consider(start, i)
	}
	for {
		order = append(order, best)
		remove(best)
		if len(order) == n {
			return order
		}
		from := points[best]
		cx, cz := cellOf(from)
		reach := max(cx, cols-1-cx, cz, rows-1-cz)
		best = -1
		for r := 0; r <= reach; r++ {
			// A point in ring r lies more than (r−1)·cell away on one axis, so
			// once the best is that close no farther ring can beat or tie it.
			if bound := int64(r-1) * cell; r > 0 && best >= 0 && bestD <= bound*bound {
				break
			}
			for z := cz - r; z <= cz+r; z++ {
				if z < 0 || z >= rows {
					continue
				}
				step := 2 * r // interior rows visit only the ring's two ends
				if z == cz-r || z == cz+r || r == 0 {
					step = 1
				}
				for x := cx - r; x <= cx+r; x += step {
					if x < 0 || x >= cols {
						continue
					}
					for _, i := range buckets[z*cols+x] {
						consider(from, int(i))
					}
				}
			}
		}
	}
}

// An area gesture's fan-out budget (DESIGN_INTERFACE_HUD_INPUT §3.11). A
// single-player gesture sends at most 16 commands and, unless its list alone
// is longer, at most 4,096 entries in all (about 144 KiB at 36 bytes an
// entry), which keeps replays bounded. Online the relay holds every seat's
// commands awaiting a tick together and closes the room past 64 commands or
// 256 KiB (DESIGN_MULTIPLAYER §16.5.1); no per-gesture budget can account for
// the other seats, so an online gesture sends one command, exactly the size it
// always was. These are UI budgets, not retail constants.
const (
	dragAreaCommandLimit = 16
	dragAreaEntryBudget  = 4096
)

// dragAreaGroups splits workers into at most limit groups (at least one) so
// that one copy of an entries-long list per group fits the entry budget. With
// room for everyone each worker is its own group, starting from its own
// position. Otherwise the workers are sorted along the axis on which they
// spread wider (then the other axis, then index) and cut into that many
// contiguous runs of near-equal size; a run starts from its middle worker.
// Groups list worker indices in ascending order.
func dragAreaGroups(workers []dragPoint, entries, limit int) (groups [][]int, starts []dragPoint) {
	n := len(workers)
	if n == 0 {
		return nil, nil
	}
	count := min(n, max(1, limit), max(1, dragAreaEntryBudget/max(entries, 1)))
	if count == n {
		for i, p := range workers {
			groups, starts = append(groups, []int{i}), append(starts, p)
		}
		return groups, starts
	}
	minX, maxX, minZ, maxZ := workers[0].x, workers[0].x, workers[0].z, workers[0].z
	for _, p := range workers[1:] {
		minX, maxX = min(minX, p.x), max(maxX, p.x)
		minZ, maxZ = min(minZ, p.z), max(maxZ, p.z)
	}
	alongX := int64(maxX)-int64(minX) >= int64(maxZ)-int64(minZ)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		pa, pb := workers[a], workers[b]
		if !alongX {
			pa, pb = dragPoint{pa.z, pa.x}, dragPoint{pb.z, pb.x}
		}
		if c := cmp.Compare(pa.x, pb.x); c != 0 {
			return c
		}
		if c := cmp.Compare(pa.z, pb.z); c != 0 {
			return c
		}
		return cmp.Compare(a, b)
	})
	for g := range count {
		run := order[g*n/count : (g+1)*n/count]
		starts = append(starts, workers[run[len(run)/2]])
		members := slices.Clone(run)
		slices.Sort(members)
		groups = append(groups, members)
	}
	return groups, starts
}
