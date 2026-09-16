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

// dragBuildCells starts at the press anchor and stops before a footprint step
// would pass the release anchor. Grid rows walk X first, then Z, in drag order.
func dragBuildCells(start, end dragPoint, footX, footZ int32, grid bool) []dragPoint {
	if footX <= 0 || footZ <= 0 {
		return nil
	}
	dx, dz := int64(end.x)-int64(start.x), int64(end.z)-int64(start.z)
	ax, az := numeric.Abs(dx), numeric.Abs(dz)
	fx, fz := int64(footX), int64(footZ)
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
	// Cross multiplication compares spans measured in their own footprints,
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
