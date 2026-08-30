package path

import "testing"

func TestReconciliationDirectRayAndRoute(t *testing.T) {
	cfg := SearchConfig{Start: Cell{}, Goal: PointGoal(Cell{4, 0}, 0), PassableValue: func(Cell) uint8 { return 3 }, Scale: 65536, FootPrintX: 2, FootPrintZ: 4}
	r := Search(cfg)
	if r.Status != 0 || len(r.Points) == 0 || r.Notified != StatusAlreadySatisfied {
		t.Fatalf("direct route status=%#x notified=%#x pops=%d seeded=%v points=%v", r.Status, r.Notified, r.Popped, r.Seeded, r.Points)
	}
	if r.Points[0] != (Point{16, 32}) || r.Points[len(r.Points)-1] != (Point{80, 32}) {
		t.Fatalf("direct route status=%#x notify=%#x pop=%d endpoints=%v", r.Status, r.Notified, r.Popped, r.Points)
	}
}

func TestReconciliationRayChargeIsExposed(t *testing.T) {
	r := Search(SearchConfig{Goal: PointGoal(Cell{4, 0}, 0), PassableValue: func(Cell) uint8 { return 3 }})
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

func TestReconciliationRayNonStartRepeatContinues(t *testing.T) {
	pos := Cell{1, 0}
	probe := DirE
	state := raySideState{departed: true}
	result := rayResult{}
	passable := func(Cell) uint8 { return 3 }
	mark := func(Cell, uint8) bool { return false }
	if !raySideStep(&pos, &probe, false, Cell{}, DirE, &state, passable, &result, mark) {
		t.Fatal("first side step unexpectedly terminated")
	}
	// Repeating a non-start state is not the permitted cycle stop. Restore the
	// state to prove it is evaluated again rather than rejected by a generic
	// visited-state map.
	pos, probe = Cell{1, 0}, DirE
	if !raySideStep(&pos, &probe, false, Cell{}, DirE, &state, passable, &result, mark) {
		t.Fatal("repeated non-start side state must continue")
	}
}

func TestReconciliationLowerRaySideReturnsToOwnOrigin(t *testing.T) {
	origin := Cell{1, 1}
	pos := origin
	probe := DirNW
	state := raySideState{}
	result := rayResult{}
	passable := func(c Cell) uint8 {
		if c == origin || c == (Cell{0, 0}) {
			return 3
		}
		return 0
	}
	mark := func(Cell, uint8) bool { return false }

	// With the lower cursor's successful-step probe rewritten in the wrong
	// direction, this exact two-cell fixture repeats a non-origin state
	// forever. The established transition returns to the side's own origin
	// state, which is the only permitted repeat termination [04 R-PATH-01 §5].
	terminated := false
	for range 16 {
		if !raySideStep(&pos, &probe, true, origin, DirNW, &state, passable, &result, mark) {
			terminated = true
			break
		}
	}
	if !terminated {
		t.Fatal("lower side did not return to its origin state within 16 probes")
	}
	if !state.departed || pos != origin || probe != DirNW {
		t.Fatalf("lower side terminated outside its origin state: pos=%v probe=%d state=%+v", pos, probe, state)
	}
	if result.steps != 12 {
		t.Fatalf("lower side charged %d probes, want 12 [04 R-PATH-01 §5]", result.steps)
	}
}

func TestReconciliationBudgetContinuation(t *testing.T) {
	cfg := SearchConfig{Start: Cell{}, Goal: PointGoal(Cell{120, 0}, 0), PassableValue: func(Cell) uint8 { return 3 }, Scale: 65536}
	one := Search(cfg)
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
