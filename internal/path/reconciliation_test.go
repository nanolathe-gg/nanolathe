package path

import "testing"

func TestReconciliationDirectRayAndRoute(t *testing.T) {
	cfg := SearchConfig{Start: Cell{}, Goal: PointGoal(Cell{4, 0}, 0), PassableValue: func(Cell) uint8 { return 3 }, Scale: 65536, FootPrintX: 2, FootPrintZ: 4}
	r := RunSearch(cfg)
	if r.Status != 0 || len(r.Points) == 0 || r.Notified != StatusAlreadySatisfied {
		t.Fatalf("direct route status=%#x notified=%#x pops=%d seeded=%v points=%v", r.Status, r.Notified, r.Popped, r.Seeded, r.Points)
	}
	if r.Points[0] != (Point{16, 32}) || r.Points[len(r.Points)-1] != (Point{80, 32}) {
		t.Fatalf("direct route status=%#x notify=%#x pop=%d endpoints=%v", r.Status, r.Notified, r.Popped, r.Points)
	}
}

func TestReconciliationRayChargeIsExposed(t *testing.T) {
	r := RunSearch(SearchConfig{Goal: PointGoal(Cell{4, 0}, 0), PassableValue: func(Cell) uint8 { return 3 }})
	if r.SetupSteps == 0 {
		t.Fatal("ray setup must expose its charged step count")
	}
}

func TestReconciliationWallFollowSides(t *testing.T) {
	for _, tc := range []struct {
		name, direction string
		target          Cell
		wallX, side     int32
	}{
		{name: "clockwise-east", direction: "east", target: Cell{8, 0}, wallX: 2, side: -1},
		{name: "counter-clockwise-west", direction: "west", target: Cell{-8, 0}, wallX: -2, side: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var touched []Cell
			passable := func(c Cell) bool {
				return !(c.X == tc.wallX && c.Z >= -2 && c.Z <= 2)
			}
			r := walkRay(Cell{}, tc.target, func(c Cell) uint8 {
				if passable(c) {
					return 3
				}
				return 0
			}, PointGoal(tc.target, 0), 65536, func(c Cell, _ uint8, _ bool) bool {
				touched = append(touched, c)
				return false
			})
			if !r.connects {
				t.Fatalf("%s wall-follow did not rejoin target side: %+v", tc.direction, r)
			}
			foundSide := false
			for _, c := range touched {
				if c.Z*tc.side > 0 {
					foundSide = true
					break
				}
			}
			if !foundSide {
				t.Fatalf("%s wall-follow did not visit its side, touched=%v", tc.direction, touched)
			}
		})
	}
}

func TestReconciliationRayCycleTerminates(t *testing.T) {
	goal := PointGoal(Cell{100, 0}, 0)
	passable := func(c Cell) uint8 {
		if c.X == 0 && c.Z == 0 {
			return 3
		}
		return 0
	}
	r := walkRay(Cell{}, Cell{100, 0}, passable, goal, 65536, nil)
	if r.connects || r.best == 0 {
		t.Fatalf("cycle result best=%d connects=%v", r.best, r.connects)
	}
}

// rayTouch records one cell the ray marked and the direction byte it wrote.
type rayTouch struct {
	c Cell
	d uint8
}

func recordRay(start, target Cell, passable func(Cell) bool) (rayResult, []rayTouch) {
	var touched []rayTouch
	r := walkRay(start, target, func(c Cell) uint8 {
		if passable(c) {
			return 3
		}
		return 0
	}, PointGoal(target, 0), 65536, func(c Cell, d uint8, _ bool) bool {
		touched = append(touched, rayTouch{c, d})
		return false
	})
	return r, touched
}

// TestRayWallFollowProtocol locks the traced two-cursor protocol on one
// authored wall [04 R-PATH-01 §15]: the upper cursor re-probes the blocked
// cardinal and then starts two sectors back from each success; the lower
// cursor steps in the opposite sector of its stored probe and writes that
// stored probe as its direction byte; the scheduler is charged once per
// cursor pair, not per probe; and the rejoin cell resumes the greedy walk.
func TestRayWallFollowProtocol(t *testing.T) {
	r, touched := recordRay(Cell{}, Cell{8, 0}, func(c Cell) bool {
		return !(c.X == 2 && c.Z >= -2 && c.Z <= 2)
	})
	if !r.connects || r.best != 0 {
		t.Fatalf("ray did not reach the target: %+v", r)
	}
	want := []rayTouch{
		{Cell{1, 0}, DirE},   // greedy
		{Cell{1, -1}, DirN},  // upper: E, NE blocked, N
		{Cell{1, 1}, DirN},   // lower: stored N, actual S
		{Cell{1, -2}, DirN},  // upper starts N-2=E again, rotates to N
		{Cell{1, 2}, DirN},   // lower: stored W, NW blocked, N
		{Cell{2, -3}, DirNE}, // upper: E blocked, NE
		{Cell{2, 3}, DirNW},  // lower: stored W blocked, NW (actual SE)
		{Cell{3, -2}, DirSE}, // upper: NE-2 = SE
		{Cell{3, 2}, DirSW},  // lower: stored NW+2 = SW (actual NE)
		{Cell{3, -1}, DirS},  // upper: SW blocked, S
		{Cell{3, 1}, DirS},   // lower: stored SE blocked, S (actual N)
		{Cell{3, 0}, DirS},   // upper: W, SW blocked, S — on the leg: rejoin
		{Cell{4, 0}, DirE}, {Cell{5, 0}, DirE}, {Cell{6, 0}, DirE}, {Cell{7, 0}, DirE}, {Cell{8, 0}, DirE},
	}
	if len(touched) != len(want) {
		t.Fatalf("touched %d cells %v, want %d %v", len(touched), touched, len(want), want)
	}
	for i := range want {
		if touched[i] != want[i] {
			t.Fatalf("touch %d = %+v, want %+v (all: %v)", i, touched[i], want[i], touched)
		}
	}
	// Two greedy charges to the wall, six cursor pairs to the rejoin, five
	// greedy steps to the target and the final charge that observes it.
	if r.steps != 2+6+5+1 {
		t.Fatalf("charged %d steps, want 14 [04 R-PATH-01 §15] step 1", r.steps)
	}
}

// TestRayCursorsMeetEndsTheRay locks the meet test [04 R-PATH-01 §15] steps 3
// and 8: in a two-cell dead end the upper cursor walks back onto the lower
// cursor's cell facing along its last step, and the ray returns its threshold
// rather than looping, having charged one step per cursor pair.
func TestRayCursorsMeetEndsTheRay(t *testing.T) {
	r, touched := recordRay(Cell{}, Cell{5, 0}, func(c Cell) bool {
		return c == Cell{} || c == Cell{1, 0}
	})
	if r.connects || r.best == 0 {
		t.Fatalf("dead end must return a nonzero threshold: %+v", r)
	}
	want := []rayTouch{{Cell{1, 0}, DirE}, {Cell{0, 0}, DirW}, {Cell{0, 0}, DirE}}
	if len(touched) != len(want) {
		t.Fatalf("touched %v, want %v", touched, want)
	}
	for i := range want {
		if touched[i] != want[i] {
			t.Fatalf("touch %d = %+v, want %+v", i, touched[i], want[i])
		}
	}
	if r.steps != 4 {
		t.Fatalf("charged %d steps, want 4 (two greedy, two cursor pairs)", r.steps)
	}
}

func TestReconciliationBudgetContinuation(t *testing.T) {
	cfg := SearchConfig{Start: Cell{}, Goal: PointGoal(Cell{120, 0}, 0), PassableValue: func(Cell) uint8 { return 3 }, Scale: 65536}
	one := RunSearch(cfg)
	s := NewSession(cfg)
	if _, _, done := s.Resume(7); done || s.Popped() != 7 {
		t.Fatalf("first slice done=%v pops=%d", done, s.Popped())
	}
	points, status, done := s.Resume(1 << 20)
	if !done || status != one.Status || len(points) != len(one.Points) {
		t.Fatalf("continuation done=%v status=%#x points=%d one=%d", done, status, len(points), len(one.Points))
	}
}

func TestReconciliationRingKeeps64Points(t *testing.T) {
	ns := NewNodeStore(65536)
	start := ns.Alloc(Cell{}, 0, 0, 0, DirN)
	parent := start
	cell := Cell{}
	for i := 1; i <= 80; i++ {
		dir := DirE
		if i&1 == 0 {
			dir = DirS
		}
		cell.X += dirDelta[dir].X
		cell.Z += dirDelta[dir].Z
		parent = ns.Alloc(cell, int32(i), 0, parent, dir)
	}
	points := reconstructRoute(Cell{}, ns.Get(parent).Cell, ns, Point{})
	if len(points) != 64 {
		t.Fatalf("ring count=%d want 64", len(points))
	}
}
