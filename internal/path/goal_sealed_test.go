package path

import "testing"

// The worst failure of Modern unreachable moves is a sealed verdict for a goal
// the search could reach. This compares the probe with an exhaustive
// reachability flood over the search's own step rule (eight neighbours, a
// diagonal checks only its destination [04 §7.1]) on deterministic
// pseudo-random maps with random density, long walls and point radii.
// Nanolathe Modern policy (docs/DESIGN_MOVEMENT_PATH.md "Modern unreachable
// moves"), not a retail claim.
func TestProbeGoalSealedNeverSealsAReachableGoal(t *testing.T) {
	const w, h = 24, 20
	bounds := Rect{Max: Cell{X: w - 1, Z: h - 1}}
	state := uint32(12345)
	next := func() uint32 { state = state*1664525 + 1013904223; return state >> 8 }
	trials := 40000
	if testing.Short() {
		trials = 4000
	}
	var scratch []Cell
	sealed, reachable, unreachable := 0, 0, 0
	for trial := 0; trial < trials; trial++ {
		var grid [w * h]bool
		density := 20 + next()%45
		for i := range grid {
			grid[i] = next()%100 >= density
		}
		// Some maps get long straight walls, which build pockets and seals.
		for k := next() % 4; k > 0; k-- {
			x, z := int32(next()%w), int32(next()%h)
			horizontal := next()%2 == 0
			for l := int32(0); l < int32(4+next()%16); l++ {
				cx, cz := x, z+l
				if horizontal {
					cx, cz = x+l, z
				}
				if cx < w && cz < h {
					grid[cz*w+cx] = false
				}
			}
		}
		pass := func(c Cell) uint8 {
			if !InBounds(c, bounds) || !grid[c.Z*w+c.X] {
				return 0
			}
			return 3
		}
		start := Cell{X: int32(next() % w), Z: int32(next() % h)}
		goalCell := Cell{X: int32(next() % w), Z: int32(next() % h)}
		radius := int32(0)
		if trial%5 == 0 {
			radius = int32(next() % 60)
		}
		if pass(start) == 0 {
			continue
		}
		goal := PointGoal(goalCell, radius)
		var p GoalSealedProbe
		p, scratch = ProbeGoalSealed(start, goal, true, bounds, pass, scratch)
		var seen [w * h]bool
		queue := []Cell{start}
		seen[start.Z*w+start.X] = true
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			for d := 0; d < 8; d++ {
				n := Cell{c.X + dirDelta[d].X, c.Z + dirDelta[d].Z}
				if pass(n) == 0 || seen[n.Z*w+n.X] {
					continue
				}
				seen[n.Z*w+n.X] = true
				queue = append(queue, n)
			}
		}
		reach := false
		for _, g := range goal.Enumerate(nil) {
			if InBounds(g, bounds) && seen[g.Z*w+g.X] {
				reach = true
			}
		}
		if p.Sealed {
			sealed++
			if reach {
				t.Fatalf("trial %d: sealed verdict for a reachable goal %v from %v (radius %d)", trial, goalCell, start, radius)
			}
		}
		if reach {
			reachable++
		} else {
			unreachable++
		}
	}
	if sealed < trials/200 || reachable < trials/200 {
		t.Fatalf("weak corpus: %d sealed, %d reachable", sealed, reachable)
	}
	t.Logf("%d sealed verdicts for %d unreachable goals; %d reachable goals, none sealed", sealed, unreachable, reachable)
}

// sealedBox is a 24x16 map whose goal sits inside a closed ring; ringValue is
// what the ring reads as, so the same geometry can be walled or unexplored.
func sealedBox(ringValue uint8) func(Cell) uint8 {
	return func(c Cell) uint8 {
		if c.X < 0 || c.Z < 0 || c.X >= 24 || c.Z >= 16 {
			return 0
		}
		onRing := c.X >= 12 && c.X <= 20 && c.Z >= 4 && c.Z <= 12 && (c.X == 12 || c.X == 20 || c.Z == 4 || c.Z == 12)
		if onRing {
			return ringValue
		}
		return 3
	}
}

// A closed ring around the goal seals it; the same ring read as unexplored
// (the search's value 2) is passable, so fog never produces a sealed verdict
// [04 R-PATH-01 §2]; and an opened ring connects. The frontier is a cell on
// the traced boundary, never the start when the ring was struck.
func TestProbeGoalSealedSealsOnlyAClosedKnownRing(t *testing.T) {
	bounds := Rect{Max: Cell{X: 23, Z: 15}}
	start, goal := Cell{X: 3, Z: 8}, PointGoal(Cell{X: 16, Z: 8}, 4)
	walled, _ := ProbeGoalSealed(start, goal, true, bounds, sealedBox(0), nil)
	if !walled.Sealed || walled.Steps == 0 {
		t.Fatalf("closed ring: %+v, want sealed", walled)
	}
	if walled.Frontier == start || walled.Frontier.X != 11 {
		t.Fatalf("closed ring frontier %v, want a cell beside the ring's west face", walled.Frontier)
	}
	if fog, _ := ProbeGoalSealed(start, goal, true, bounds, sealedBox(2), nil); fog.Sealed {
		t.Fatalf("unexplored ring sealed the goal: %+v", fog)
	}
	gap := sealedBox(0)
	open := func(c Cell) uint8 {
		if c.X == 12 && c.Z == 8 {
			return 3
		}
		return gap(c)
	}
	if reach, _ := ProbeGoalSealed(start, goal, true, bounds, open, nil); reach.Sealed {
		t.Fatalf("opened ring sealed the goal: %+v", reach)
	}
	// A satisfied or blocked start is never a verdict.
	if p, _ := ProbeGoalSealed(Cell{X: 16, Z: 8}, goal, true, bounds, sealedBox(0), nil); p.Sealed {
		t.Fatal("a satisfied start answered sealed")
	}
	if p, _ := ProbeGoalSealed(Cell{X: 12, Z: 8}, goal, true, bounds, sealedBox(0), nil); p.Sealed {
		t.Fatal("a blocked start answered sealed")
	}
}

// A warmed caller's probe does not allocate: the goal cells reuse scratch.
func TestProbeGoalSealedReusesScratch(t *testing.T) {
	bounds := Rect{Max: Cell{X: 23, Z: 15}}
	start, goal := Cell{X: 3, Z: 8}, PointGoal(Cell{X: 16, Z: 8}, 4)
	pass := sealedBox(0)
	_, scratch := ProbeGoalSealed(start, goal, true, bounds, pass, nil)
	if n := testing.AllocsPerRun(20, func() {
		_, scratch = ProbeGoalSealed(start, goal, true, bounds, pass, scratch)
	}); n != 0 {
		t.Fatalf("warmed probe allocated %v times", n)
	}
}

// RouteEndCell inverts the published point representation.
func TestRouteEndCellInvertsWorldPoint(t *testing.T) {
	for _, foot := range []Point{{X: 1, Z: 1}, {X: 2, Z: 3}, {X: 4, Z: 4}} {
		for _, c := range []Cell{{X: 0, Z: 0}, {X: 17, Z: 5}, {X: 255, Z: 511}} {
			got, ok := RouteEndCell([]Point{{}, worldPoint(c, foot)}, foot.X, foot.Z)
			if !ok || got != c {
				t.Fatalf("footprint %v cell %v: got %v, %v", foot, c, got, ok)
			}
		}
	}
	if _, ok := RouteEndCell(nil, 1, 1); ok {
		t.Fatal("an empty route has an end cell")
	}
}
