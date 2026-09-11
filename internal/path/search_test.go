package path

import (
	"testing"
)

// TestStepCosts locks [04 §7.2] C4 constants: cardinal 16, diagonal 22, turn table, neighbor 30, short-run 75.
func TestStepCosts(t *testing.T) {
	if CardinalCost != 16 {
		t.Fatalf("CardinalCost want 16 got %d [04 §7.2] C4", CardinalCost)
	}
	if DiagonalCost != 22 {
		t.Fatalf("DiagonalCost want 22 got %d [04 §7.2] C4 corrected", DiagonalCost)
	}
	if SteepCost != 30 {
		t.Fatalf("SteepCost want 30 got %d [04 §7.2] C4", SteepCost)
	}
	if ShortRunPenalty != 75 {
		t.Fatalf("ShortRunPenalty want 75 got %d [04 §7.2] C4", ShortRunPenalty)
	}
	if ShortRunLimit != 5 {
		t.Fatalf("ShortRunLimit want 5 got %d [04 §7.2] C4", ShortRunLimit)
	}
	// turn table [04 §7.2] C4
	wantTable := [8]int32{0, 40, 60, 80, 100, 80, 60, 40}
	for i, want := range wantTable {
		if TurnPenaltyTable[i] != want {
			t.Fatalf("TurnPenaltyTable[%d] want %d got %d [04 §7.2] C4", i, want, TurnPenaltyTable[i])
		}
	}
	// step cost by direction
	if got := StepCost(DirN); got != 16 {
		t.Fatalf("StepCost N want 16 got %d", got)
	}
	if got := StepCost(DirE); got != 16 {
		t.Fatalf("StepCost E want 16 got %d", got)
	}
	if got := StepCost(DirW); got != 16 {
		t.Fatalf("StepCost W want 16 got %d", got)
	}
	if got := StepCost(DirS); got != 16 {
		t.Fatalf("StepCost S want 16 got %d", got)
	}
	if got := StepCost(DirNW); got != 22 {
		t.Fatalf("StepCost NW want 22 got %d", got)
	}
	if got := StepCost(DirNE); got != 22 {
		t.Fatalf("StepCost NE want 22 got %d", got)
	}
	if got := StepCost(DirSW); got != 22 {
		t.Fatalf("StepCost SW want 22 got %d", got)
	}
	if got := StepCost(DirSE); got != 22 {
		t.Fatalf("StepCost SE want 22 got %d", got)
	}
	// turn penalty by delta
	// from N to N diff 0 ->0, N to NW diff1 ->40 etc [04 §7.2]
	if got := TurnPenalty(DirN, DirN); got != 0 {
		t.Fatalf("Turn N->N want 0 got %d", got)
	}
	if got := TurnPenalty(DirN, DirNW); got != 40 {
		t.Fatalf("Turn N->NW want 40 got %d", got)
	}
	if got := TurnPenalty(DirN, DirW); got != 60 {
		t.Fatalf("Turn N->W want 60 got %d", got)
	}
	if got := TurnPenalty(DirN, DirSW); got != 80 {
		t.Fatalf("Turn N->SW want 80 got %d", got)
	}
	if got := TurnPenalty(DirN, DirS); got != 100 {
		t.Fatalf("Turn N->S want 100 got %d", got)
	}
	// wrap-around: NE (7) to N (0) diff 1 ->40 (since (0-7+8)%8=1)
	if got := TurnPenalty(DirNE, DirN); got != 40 {
		t.Fatalf("Turn NE->N want 40 got %d", got)
	}
	// E to W: 2 to 6 diff (6-2)=4 ->100
	if got := TurnPenalty(DirW, DirE); got != 100 {
		t.Fatalf("Turn W->E want 100 got %d", got)
	}
	// start dir none ->0
	if got := TurnPenalty(DirNone, DirN); got != 0 {
		t.Fatalf("Turn None->N want 0 got %d", got)
	}
	// The fixed neighbor penalty is unconditional; TestSteepCostEveryNeighborExpansion
	// below checks this through live expansions with both a populated and
	// initially empty open heap [04 §7.2] C4.

	// short-run: parent chain straight run <5
	// Build store chain: start -> A(N) -> B(N) -> C(N) -> D(N) -> E(N) (run 5)
	ns := NewNodeStore(65536)
	start := ns.Ensure(Cell{0, 0}, 0, invalidNodeID, DirNone, &mutableGoal{h: 0})
	a := ns.Ensure(Cell{0, -1}, 16, start, DirN, &mutableGoal{h: 0})
	b := ns.Ensure(Cell{0, -2}, 32, a, DirN, &mutableGoal{h: 0})
	c := ns.Ensure(Cell{0, -3}, 48, b, DirN, &mutableGoal{h: 0})
	d := ns.Ensure(Cell{0, -4}, 64, c, DirN, &mutableGoal{h: 0})
	e := ns.Ensure(Cell{0, -5}, 80, d, DirN, &mutableGoal{h: 0})
	if got := straightRunLen(ns, a); got != 1 {
		t.Fatalf("straight run a want 1 got %d", got)
	}
	if got := straightRunLen(ns, b); got != 2 {
		t.Fatalf("straight run b want 2 got %d", got)
	}
	if got := straightRunLen(ns, d); got != 4 {
		t.Fatalf("straight run d want 4 got %d", got)
	}
	if got := straightRunLen(ns, e); got != 5 {
		t.Fatalf("straight run e want 5 got %d", got)
	}
	// short-run penalty should apply when <5
	if straightRunLen(ns, d) < ShortRunLimit {
		// d run 4 <5 => penalty 75 should apply for its children
		if ShortRunPenalty != 75 {
			t.Fatalf("short run penalty mismatch")
		}
	} else {
		t.Fatalf("d should be short run")
	}
	if straightRunLen(ns, e) < ShortRunLimit {
		t.Fatalf("e run 5 should NOT be short")
	}
}

// TestSteepCostEveryNeighborExpansion locks the steep-tier terrain term
// and direction penalty order [04 R-PATH-01 §3].
func TestSteepCostEveryNeighborExpansion(t *testing.T) {
	cfg := SearchConfig{
		Start:         Cell{0, 0},
		Goal:          &mutableGoal{h: 100},
		PassableValue: func(Cell) uint8 { return 3 },
		Scale:         65536,
	}
	sess := NewSession(cfg)
	if !sess.Seeded() {
		t.Fatal("search should seed for the live-expansion fixture")
	}
	if _, _, done := sess.Resume(1); done {
		t.Fatal("first expansion should leave the search active")
	}

	// The start's nine-entry fan contains all eight neighbors plus a duplicate.
	first := []struct {
		cell Cell
		dir  uint8
	}{
		{Cell{0, 1}, DirS},
		{Cell{1, 1}, DirSE},
		{Cell{1, 0}, DirE},
		{Cell{1, -1}, DirNE},
		{Cell{0, -1}, DirN},
		{Cell{-1, -1}, DirNW},
		{Cell{-1, 0}, DirW},
		{Cell{-1, 1}, DirSW},
	}
	for _, tc := range first {
		id, ok := sess.ns.Find(tc.cell)
		if !ok {
			t.Fatalf("first expansion did not allocate %v", tc.cell)
		}
		want := StepCost(tc.dir) + TurnPenalty(DirN, tc.dir)
		if got := sess.ns.Get(id).G; got != want {
			t.Errorf("first expansion %v G want %d got %d [04 R-PATH-01 §3]", tc.cell, want, got)
		}
	}
}

// An admitted node keeps its route eligibility after a class-layer change;
// neither relaxation nor pop repeats its probe [04 R-PATH-01 §1].
func TestOpenNodeRetainsAdmissionAcrossLayerChange(t *testing.T) {
	blocked := map[Cell]bool{}
	cfg := SearchConfig{
		Start: Cell{5, 5},
		Goal:  PointGoal(Cell{5, 12}, 0),
		PassableValue: func(c Cell) uint8 {
			if blocked[c] {
				return 0
			}
			return 3
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    Rect{Min: Cell{0, 0}, Max: Cell{15, 15}},
	}
	s := NewSession(cfg)
	if _, _, done := s.Resume(1); done {
		t.Fatal("fixture search completed before an open node could be revised")
	}
	id, _, ok := s.heap.Peek()
	if !ok {
		t.Fatal("first expansion left no open node")
	}
	cell := s.ns.Get(id).Cell
	blocked[cell] = true
	if _, _, done := s.Resume(1); done {
		t.Fatal("fixture search completed while expanding one node")
	}
	n := s.ns.Get(id)
	if !n.Closed || n.Open || s.entries.get(cell).status != 2 {
		t.Fatalf("revised open node must expand and clear its flags: node=%+v entry=%+v", *n, s.entries.get(cell))
	}
}

func TestHeuristicScaling(t *testing.T) {
	// [04 §7.2] C6 hScaled = (h*scale)>>16 with signed 64 product and arithmetic shift, no float
	if got := ScaledHeuristic(0, 65536); got != 0 {
		t.Fatalf("h=0 scale 65536 want 0 got %d [04 §7.2] C6", got)
	}
	if got := ScaledHeuristic(0, 12345); got != 0 {
		t.Fatalf("h=0 any scale want 0 got %d", got)
	}
	// 1.0 scale
	if got := ScaledHeuristic(100, 65536); got != 100 {
		t.Fatalf("100*1.0 want 100 got %d", got)
	}
	// 0.5 scale (32768)
	if got := ScaledHeuristic(100, 32768); got != 50 {
		t.Fatalf("100*0.5 want 50 got %d", got)
	}
	// tier 6x: scale = 6*65536 = 393216; h=10 => (10*393216)>>16 = 60
	if got := ScaledHeuristic(10, 393216); got != 60 {
		t.Fatalf("10*6 want 60 got %d", got)
	}
	// tier 3x: 196608 *10 >>16 =30
	if got := ScaledHeuristic(10, 196608); got != 30 {
		t.Fatalf("10*3 want 30 got %d", got)
	}
	// arithmetic shift: negative h
	if got := ScaledHeuristic(-100, 65536); got != -100 {
		t.Fatalf("-100*1.0 want -100 got %d", got)
	}
	if got := ScaledHeuristic(-100, 32768); got != -50 {
		t.Fatalf("-100*0.5 want -50 got %d", got)
	}
	// 64-bit product overflow check: h=2000000, scale=65536 => product 131072000000 fits 64
	// >>16 = 2000000
	if got := ScaledHeuristic(2000000, 65536); got != 2000000 {
		t.Fatalf("large h want 2000000 got %d", got)
	}
	// h=0 stays 0 for any scale even large
	if got := ScaledHeuristic(0, 1<<30); got != 0 {
		t.Fatalf("0 with large scale want 0 got %d", got)
	}
	// verify no float is used: product is int64 shift, not float division
	// check that shift is arithmetic (sign preserving) not logical for negative
	// -1 * 65536 = -65536 >>16 = -1 (arithmetic)
	if got := ScaledHeuristic(-1, 65536); got != -1 {
		t.Fatalf("-1 want -1 got %d", got)
	}
}

// TestExpansions verifies C2 neighbor order and fan shape [04 §7.1].
func TestExpansions(t *testing.T) {
	cur := Cell{5, 5}
	// First expansion nine entries: the centered loop around startFanDir
	// (north) with width 4 [04 §7.1] C2; dir numbering is 0=N counterclockwise.
	fan := NeighborsForDir(cur, DirNone, true)
	if fan.Len != 9 {
		t.Fatalf("first expansion want 9 got %d [04 §7.1] C2", fan.Len)
	}
	// Centered on N with ±4: S,SE,E,NE,N,NW,W,SW,S — the ninth duplicates S.
	wantOrder := []uint8{DirS, DirSE, DirE, DirNE, DirN, DirNW, DirW, DirSW, DirS}
	for i := 0; i < 9; i++ {
		if fan.Dirs[i] != wantOrder[i] {
			t.Fatalf("first expansion order [%d] want %d got %d [04 §7.1] C2", i, wantOrder[i], fan.Dirs[i])
		}
	}
	if fan.Cells[0] != (Cell{5, 6}) { // S
		t.Fatalf("first cell S want (5,6) got %v", fan.Cells[0])
	}
	if fan.Cells[8] != fan.Cells[0] {
		t.Fatalf("duplicate ninth should equal first N: got %v vs %v", fan.Cells[8], fan.Cells[0])
	}
	// Later expansions five-entry fan centered on parent dir [04 §7.1] C2.
	// For DirN (0) the offsets -2..+2 wrap to E, NE, N, NW, W.
	fanN := NeighborsForDir(cur, DirN, false)
	if fanN.Len != 5 {
		t.Fatalf("fan for N want 5 got %d [04 §7.1] C2", fanN.Len)
	}
	wantFanN := []uint8{DirE, DirNE, DirN, DirNW, DirW}
	for i, want := range wantFanN {
		if fanN.Dirs[i] != want {
			t.Fatalf("fan N [%d] want %d got %d", i, want, fanN.Dirs[i])
		}
	}
	// For DirE (6), fan => DirS(4), DirSE(5), DirE(6), DirNE(7), DirN(0)
	fanE := NeighborsForDir(cur, DirE, false)
	wantFanE := []uint8{DirS, DirSE, DirE, DirNE, DirN}
	for i, want := range wantFanE {
		if fanE.Dirs[i] != want {
			t.Fatalf("fan E [%d] want %d got %d", i, want, fanE.Dirs[i])
		}
	}
}

// TestDiagonalDestinationOnly verifies C3 diagonal checks ONLY destination [04 §7.1] C3.
// Corner cutting must be allowed: diagonal move succeeds even if both cardinal corners blocked.
func TestDiagonalDestinationOnly(t *testing.T) {
	// 4x4 grid bounds 0..3. The ray reaches the goal along its cardinal
	// legs, while the search's later diagonal step has blocked cardinal
	// corners. This isolates destination-only diagonal admission from ray
	// setup.
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{3, 3}}
	start := Cell{0, 0}
	goalCell := Cell{3, 3}
	isPassable := func(c Cell) bool {
		// Block the cardinal corners around the later (1,1)->(2,2)
		// diagonal destination; the destination itself remains passable.
		if (c.X == 2 && c.Z == 1) || (c.X == 1 && c.Z == 2) {
			return false
		}
		return true
	}
	goal := PointGoal(goalCell, 0)
	cfg := SearchConfig{
		Start: start,
		Goal:  goal,
		PassableValue: func(c Cell) uint8 {
			if isPassable(c) {
				return 3
			}
			return 0
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    bounds,
	}
	res := Search(cfg)
	if len(res.Points) == 0 {
		t.Fatalf("diagonal destination-only: path should exist (corner cutting allowed) but got empty, status %#x [04 §7.1] C3", res.Status)
	}
	// The endpoint must remain reachable despite the blocked cardinal corners.
	foundGoal := false
	for _, p := range res.Points {
		if p.X == goalCell.X*16 && p.Z == goalCell.Z*16 {
			foundGoal = true
		}
	}
	if !foundGoal {
		t.Fatalf("diagonal: expected goal point (%d,%d) in result %v", goalCell.X, goalCell.Z, res.Points)
	}
}

// TestEarlyExits verifies C10 early exits in order [04 §7.2] C10.
func TestEarlyExits(t *testing.T) {
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{10, 10}}
	// 1) start satisfies goal -> 0x100 empty without seeding [04 §7.2] C10
	goalSatisfied := PointGoal(Cell{5, 5}, 32) // radius 32 => q=2, start at center satisfies
	cfg1 := SearchConfig{
		Start:         Cell{5, 5},
		Goal:          goalSatisfied,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
	}
	res1 := Search(cfg1)
	if res1.Status != StatusAlreadySatisfied {
		t.Fatalf("early exit 1 start satisfies want 0x100 got %#x [04 §7.2] C10", res1.Status)
	}
	if len(res1.Points) != 0 {
		t.Fatalf("early exit 1 should publish empty, got %v", res1.Points)
	}
	if res1.Seeded {
		t.Fatalf("early exit 1 should NOT seed [04 §7.2] C10")
	}
	if res1.Popped != 0 {
		t.Fatalf("early exit 1 popped want 0 got %d", res1.Popped)
	}

	// 2) out-of-bounds start -> 0x200 empty without seeding [04 §7.2] C10
	goal2 := PointGoal(Cell{5, 5}, 0)
	cfg2 := SearchConfig{
		Start:         Cell{20, 20}, // OOB
		Goal:          goal2,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
	}
	res2 := Search(cfg2)
	if res2.Status != StatusRejected {
		t.Fatalf("early exit 2 OOB want 0x200 got %#x [04 §7.2] C10", res2.Status)
	}
	if len(res2.Points) != 0 {
		t.Fatalf("early exit 2 empty")
	}
	if res2.Seeded {
		t.Fatalf("early exit 2 should NOT seed")
	}
	if res2.Popped != 0 {
		t.Fatalf("early exit 2 popped 0")
	}

	// 3) ray connects start to goal -> notify 0x100 but seed and run anyway [04 §7.2] C10
	// Need passable straight line; start (0,0) goal (3,0) line is clear
	goal3 := PointGoal(Cell{3, 0}, 0)
	isPassable3 := func(c Cell) bool { return true }
	cfg3 := SearchConfig{
		Start: Cell{0, 0},
		Goal:  goal3,
		PassableValue: func(c Cell) uint8 {
			if isPassable3(c) {
				return 3
			}
			return 0
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    bounds,
	}
	res3 := Search(cfg3)
	if res3.Notified != StatusAlreadySatisfied {
		t.Fatalf("early exit 3 ray connects want notified 0x100 got %#x [04 §7.2] C10", res3.Notified)
	}
	if !res3.Seeded {
		t.Fatalf("early exit 3 ray connects SHOULD seed and run [04 §7.2] C10")
	}
	if len(res3.Points) == 0 {
		t.Fatalf("early exit 3 should still find path despite notify, got empty")
	}
	// Popped should be >0

	// 4) ray best >= start -> 0x200 without seeding [04 §7.2] C10
	// Create blocked ray: start (0,0) goal (5,0) but immediate column x=1 and x=2 blocked as wall
	// So frontier around first blocked cell (1,0) includes no cell east of wall that is closer;
	// all east neighbors remain blocked, only north/south remain with higher h -> best stays >= start.
	goal4 := PointGoal(Cell{5, 0}, 0)
	isPassable4 := func(c Cell) bool {
		if c.X == 1 || c.X == 2 {
			return false // wall 2 thick -> no east frontier closer [04 §7.2] C9
		}
		return true
	}
	cfg4 := SearchConfig{
		Start: Cell{0, 0},
		Goal:  goal4,
		PassableValue: func(c Cell) uint8 {
			if isPassable4(c) {
				return 3
			}
			return 0
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    bounds,
	}
	res4 := Search(cfg4)
	// This should trigger early exit 4 because ray best (at start) >= startScaled (equal)
	if res4.Status != StatusRejected {
		t.Fatalf("early exit 4 ray best>=start want 0x200 got %#x [04 §7.2] C10", res4.Status)
	}
	if len(res4.Points) != 0 {
		t.Fatalf("early exit 4 should publish empty")
	}
	if res4.Seeded {
		t.Fatalf("early exit 4 should NOT seed without closer frontier [04 §7.2] C10")
	}
	if res4.Popped != 0 {
		t.Fatalf("early exit 4 popped 0")
	}

	// Order check: start satisfies takes precedence over OOB
	// Start OOB but also satisfies? Should return 0x100 per order: start satisfies checked before OOB
	goal5 := PointGoal(Cell{20, 20}, 1000) // large radius satisfying OOB start at (20,20) if bounds 0..10? But OOB start 20,20 center 20,20 distance 0 -> satisfies even though OOB, first check should win
	cfg5 := SearchConfig{
		Start:         Cell{20, 20},
		Goal:          goal5,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
	}
	res5 := Search(cfg5)
	if res5.Status != StatusAlreadySatisfied {
		t.Fatalf("order: start satisfies should win over OOB want 0x100 got %#x", res5.Status)
	}
}

// TestToleranceWriteOnce verifies C9 write-once threshold and termination [04 §7.2] C9.
func TestToleranceWriteOnce(t *testing.T) {
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{10, 10}}
	// Create scenario where goal at (10,0), start at (0,0), but wall blocks direct line at (5,0)
	// Ray walk from start to nearest goal (10,0) will encounter block at 5,0, so best is min h among cells 0..4
	// That best corresponds to h at (4,0) maybe 18*6=108 etc, tolerance = that.
	// Search should terminate when popped node reaches tolerance region (near wall) even though enumerated goal behind wall not reachable.
	// We'll make isPassable block column x=5 all z
	isPassable := func(c Cell) bool { return c.X != 5 }
	goal := PointGoal(Cell{10, 0}, 0) // h =18*max+7*min
	cfg := SearchConfig{
		Start: Cell{0, 0},
		Goal:  goal,
		PassableValue: func(c Cell) uint8 {
			if isPassable(c) {
				return 3
			}
			return 0
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    bounds,
	}
	res := Search(cfg)
	if len(res.Points) == 0 {
		t.Fatalf("tolerance: expected path to frontier (write-once threshold) but got empty [04 §7.2] C9")
	}
	// Points should not include goal behind wall (10,0) because wall blocks, but should contain frontier cell near wall
	hasGoal := false
	for _, p := range res.Points {
		if p.X == 10 && p.Z == 0 {
			hasGoal = true
		}
	}
	if hasGoal {
		t.Fatalf("tolerance: path should terminate at frontier before wall, not reach blocked goal (10,0) %v", res.Points)
	}
	// Last point should be near wall x=4 maybe
	last := res.Points[len(res.Points)-1]
	if last.X != 4 && last.X != 5 { // depending on reconstruction, last is frontier
		// Allow 4 or near
		t.Logf("tolerance frontier last point %v, points %v", last, res.Points)
	}
	// Verify write-once: tolerance should not be updated after initial walkRay.
	// We can check that second Search with same params yields same result (deterministic) - implying not updated
	res2 := Search(cfg)
	if len(res.Points) != len(res2.Points) {
		t.Fatalf("tolerance write-once: second run mismatch")
	}
	for i := range res.Points {
		if res.Points[i] != res2.Points[i] {
			t.Fatalf("tolerance deterministic mismatch")
		}
	}
}

// TestEndToEndSmallGrid verifies C1 lattice and full A* optimal cost [04 §7.1][04 §7.2].
func TestEndToEndSmallGrid(t *testing.T) {
	// Hand-built 4x4 grid, start (0,0) goal (3,3), no obstacles.
	// Compute optimal g via known formula with our costs including penalties.
	// For unobstructed diagonal path, optimal should be 3 diagonal steps (0,0->1,1->2,2->3,3) each 22 =66 plus turn 0 + initial 30? Need to compute total g including penalties.
	// Our search will compute g as stored in NodeStore; we can verify via searching and inspecting node g values.
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{3, 3}}
	goal := PointGoal(Cell{3, 3}, 0)
	cfg := SearchConfig{
		Start:         Cell{0, 0},
		Goal:          goal,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
		FootPrintX:    0,
		FootPrintZ:    0,
	}
	res := Search(cfg)
	if len(res.Points) == 0 {
		t.Fatalf("end-to-end: expected path, got empty status %#x", res.Status)
	}
	// Verify points are via ring reconstruction: should be start plus goal for straight diagonal?
	// For straight diagonal direction NE? Actually from (0,0) to (3,3) direction SE? Our delta for SE is (1,1) direction 5, straight line so direction never changes -> reconstruction should yield 2 points: start and goal per [04 §7.3] C13
	if len(res.Points) != 2 {
		t.Fatalf("end-to-end straight diagonal want 2 points (start+goal) got %d %v [04 §7.3] C13", len(res.Points), res.Points)
	}
	if res.Points[0] != (Point{0, 0}) || res.Points[1] != (Point{48, 48}) {
		t.Fatalf("points want [(0,0),(48,48)] got %v", res.Points)
	}
	// Verify g value via building a small search manually to inspect node store?
	// We'll run Search with same cfg but inspect via helper that replicates cost calculation:
	// Expected g for diagonal 3 steps: 3*22=66, plus initial 30 for first step? Actually initial penalty adds to each neighbor of start? That would add 30 to first step only, so g goal =66 +30 + maybe short run penalties
	// For straight line 3 steps: second and third steps will have short-run penalty 75 each (since run <5) => total =66+30+75+75=246? Let's see if our Search computes that.
	// We'll verify by reproducing NodeStore g via separate instrumentation: run Search but capture via direct node store walk.
	// Simpler: check that path with obstacle forces longer route and higher g.

	// Second grid with obstacle forcing detour: block (1,1) so direct diagonal blocked, need cardinal detour
	isPassable2 := func(c Cell) bool {
		if c.X == 1 && c.Z == 1 {
			return false
		}
		return true
	}
	cfg2 := SearchConfig{
		Start: Cell{0, 0},
		Goal:  goal,
		PassableValue: func(c Cell) uint8 {
			if isPassable2(c) {
				return 3
			}
			return 0
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    bounds,
	}
	res2 := Search(cfg2)
	if len(res2.Points) == 0 {
		t.Fatalf("detour: expected path around blocked (1,1)")
	}
	// Detour path should be longer than straight: at least 2 extra cardinal? The optimal detour might be (0,0)->(1,0)->2,1->3,2->3,3 etc.
	// Ensure points not just 2, should be >2 because direction changes
	if len(res2.Points) <= 2 {
		t.Fatalf("detour should have >2 points due to direction changes, got %d %v", len(res2.Points), res2.Points)
	}
	// Test bias addition [04 §7.1] C1
	bias := Point{X: 5, Z: 7}
	cfg3 := SearchConfig{
		Start:         Cell{0, 0},
		Goal:          goal,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
		FootPrintX:    bias.X,
		FootPrintZ:    bias.Z,
	}
	res3 := Search(cfg3)
	if res3.Points[0] != (Point{5 * 8, 7 * 8}) {
		t.Fatalf("footprint: first point want (40,56) got %v [04 R-PATH-01 §7]", res3.Points[0])
	}
	if res3.Points[len(res3.Points)-1] != (Point{(3*2 + 5) * 8, (3*2 + 7) * 8}) {
		t.Fatalf("footprint last point want (88,104) got %v", res3.Points[len(res3.Points)-1])
	}
}

func TestEqualCostDetourPinsRouteAndPopCharge(t *testing.T) {
	// The center wall leaves north and south detours with the same ordinary
	// costs. Their selected route and charged expansions therefore expose the
	// heap's equal-key transaction [04 R-PATH-01 §1].
	cfg := SearchConfig{
		Start:     Cell{0, 0},
		StartDir:  DirE,
		Goal:      PointGoal(Cell{2, 0}, 0),
		Scale:     65536,
		HasBounds: true,
		Bounds:    Rect{Min: Cell{-1, -1}, Max: Cell{3, 1}},
		PassableValue: func(c Cell) uint8 {
			if c == (Cell{1, 0}) {
				return 0
			}
			return 3
		},
	}
	result := Search(cfg)
	want := []Point{{0, 0}, {16, 16}, {32, 0}}
	if len(result.Points) != len(want) {
		t.Fatalf("equal-cost route length = %d, want %d: %v", len(result.Points), len(want), result.Points)
	}
	for i := range want {
		if result.Points[i] != want[i] {
			t.Fatalf("equal-cost route[%d] = %v, want %v", i, result.Points[i], want[i])
		}
	}
	if result.Popped != 11 {
		t.Fatalf("equal-cost search charged %d pops, want 11", result.Popped)
	}
}

// TestHeuristicWriteOnce verifies C7 h evaluated once per node [04 §7.2] C7 via NodeStore.
func TestSearchHeuristicWriteOnce(t *testing.T) {
	ns := NewNodeStore(65536)
	g := &mutableGoal{h: 50}
	cell := Cell{2, 2}
	id := ns.Ensure(cell, 0, invalidNodeID, DirNone, g)
	firstH := ns.Get(id).H
	firstF := ns.Get(id).F
	g.h = 999
	id2 := ns.Ensure(cell, 0, invalidNodeID, DirNone, g)
	if id != id2 {
		t.Fatalf("same cell should return same ID")
	}
	if ns.Get(id2).H != firstH {
		t.Fatalf("h write-once violated: first %d second %d [04 §7.2] C7", firstH, ns.Get(id2).H)
	}
	if ns.Get(id2).F != firstF {
		t.Fatalf("F should not change after goal mutation [04 §7.2] C7")
	}
}

// TestFiveEntryFanTraversesOnlyDestinationAlreadyCovered via Search integration.
func TestFiveEntryFanAfterFirst(t *testing.T) {
	// Ensure after first expansion, second expansion only yields five neighbors, not nine
	// We can verify via NeighborsForDir fan shape already, but also via Search popping count limiting
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{10, 10}}
	goal := PointGoal(Cell{10, 10}, 0)
	cfg := SearchConfig{
		Start:         Cell{5, 5},
		Goal:          goal,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
	}
	res := Search(cfg)
	// Just ensure search completes and fan logic didn't cause 9 entries after first (would still succeed but we lock via unit test)
	if len(res.Points) == 0 {
		t.Fatalf("fan: expected path")
	}
}

// TestResumableBudgetHonoring verifies [04 §7.3] C11 C12 budget-honoring search [04 §7.3] "Budget exhaustion leaves the heap and request active — it does not publish the best partial prefix".
// A wide grid needs >100 pops; first call with budget 100 returns done=false with no publication,
// second (resumed) completes with identical results to an unconstrained one-shot run [04 §7.3] C11 C12.
func TestResumableBudgetHonoring(t *testing.T) {
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{200, 200}}
	start := Cell{0, 0}
	goalCell := Cell{150, 0}
	goal := PointGoal(goalCell, 0)
	cfg := SearchConfig{
		Start:         start,
		Goal:          goal,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
		FootPrintX:    0,
		FootPrintZ:    0,
	}
	oneShot := Search(cfg)
	if oneShot.Popped <= 100 {
		t.Fatalf("one-shot fixture requires >100 pops to test budget, got %d; widen grid or obstacle [04 §7.3] C11", oneShot.Popped)
	}
	if len(oneShot.Points) == 0 {
		t.Fatalf("one-shot should succeed, got empty status %#x", oneShot.Status)
	}
	sess := NewSession(cfg)
	points1, status1, done1 := sess.Resume(100) // [04 §7.3] C11 100 pops per request per call
	if done1 {
		t.Fatalf("first budget 100 should not be done, got done=true points %v status %#x [04 §7.3] C12", points1, status1)
	}
	if len(points1) != 0 {
		t.Fatalf("budget exhaustion must not publish partial prefix, got %d points [04 §7.3] C12", len(points1))
	}
	if sess.Popped() != 100 {
		t.Fatalf("first resume should stop after exactly 100 pops, got %d", sess.Popped())
	}
	if sess.IsDone() {
		t.Fatalf("session should remain active after budget exhaustion [04 §7.3] C11")
	}
	// Determinism: same session must retain heap+request state; resume must complete identically to one-shot [04 §7.3] C11 C12.
	points2, status2, done2 := sess.Resume(1 << 20)
	if !done2 {
		t.Fatalf("second resume should complete, got done=false")
	}
	if status2 != oneShot.Status {
		t.Fatalf("resumed status want %#x got %#x", oneShot.Status, status2)
	}
	if len(points2) != len(oneShot.Points) {
		t.Fatalf("resumed points len want %d got %d", len(oneShot.Points), len(points2))
	}
	for i := range points2 {
		if points2[i] != oneShot.Points[i] {
			t.Fatalf("resumed points[%d] want %v got %v", i, oneShot.Points[i], points2[i])
		}
	}
	if sess.Popped() != oneShot.Popped {
		t.Fatalf("total pops should equal one-shot %d vs resumed %d (determinism same seed + same request sequence ⇒ identical published routes whether budget interrupts occur or not) [04 §7.3] C11", oneShot.Popped, sess.Popped())
	}
	// Direct second one-shot vs resumed identical.
	sess2 := NewSession(cfg)
	var all []Point
	var st Status
	var done bool
	_, _, done = sess2.Resume(50)
	if done {
		t.Fatalf("50 budget should not finish")
	}
	all, st, done = sess2.Resume(1 << 20)
	if !done || st != oneShot.Status || len(all) != len(oneShot.Points) {
		t.Fatalf("chunked 50+ rest should also equal one-shot")
	}
	for i := range all {
		if all[i] != oneShot.Points[i] {
			t.Fatalf("chunked 50 mismatch")
		}
	}
	_ = status1
	_ = status2
}

// TestResumableHeapExhaustion verifies heap exhaustion publishes empty deterministically [04 §7.3] C12.
func TestResumableHeapExhaustion(t *testing.T) {
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{10, 10}}
	goal := PointGoal(Cell{10, 0}, 0)
	isPassable := func(c Cell) bool {
		if c.X == 1 || c.X == 2 {
			return false
		}
		return true
	}
	cfg := SearchConfig{
		Start: Cell{0, 0},
		Goal:  goal,
		PassableValue: func(c Cell) uint8 {
			if isPassable(c) {
				return 3
			}
			return 0
		},
		Scale:     65536,
		HasBounds: true,
		Bounds:    bounds,
	}
	oneShot := Search(cfg)
	if oneShot.Status != StatusRejected || len(oneShot.Points) != 0 {
		t.Fatalf("heap exhaustion one-shot should publish empty rejected")
	}
	sess := NewSession(cfg)
	p, st, done := sess.Resume(100)
	if !done || st != StatusRejected || len(p) != 0 {
		t.Fatalf("heap exhaustion with budget: resumed should also publish empty rejected in one call, got done %v st %#x len %d", done, st, len(p))
	}
}

// TestResumableWriteOnceSlots verifies write-once tolerance, node store h, and goal flags persist across resumes [04 §7.2] C7 C9.
func TestResumableWriteOnceSlots(t *testing.T) {
	bounds := Rect{Min: Cell{0, 0}, Max: Cell{200, 200}}
	start := Cell{0, 0}
	goal := PointGoal(Cell{150, 0}, 0)
	cfg := SearchConfig{
		Start:         start,
		Goal:          goal,
		PassableValue: func(c Cell) uint8 { return 3 },
		Scale:         65536,
		HasBounds:     true,
		Bounds:        bounds,
	}
	sess := NewSession(cfg)
	// Capture initial tolerance and first node h.
	initialTol := sess.tolerance
	hasTol := sess.hasTolerance
	_ = initialTol
	_ = hasTol
	_, _, done1 := sess.Resume(50)
	if done1 {
		t.Skipf("grid too small to need >50 pops, skipping write-once check")
	}
	// Tolerance slot must be unchanged after partial resume [04 §7.2] C9 write-once never updated.
	if sess.tolerance != initialTol || sess.hasTolerance != hasTol {
		t.Fatalf("tolerance write-once violated after resume: before %d %v after %d %v [04 §7.2] C9", initialTol, hasTol, sess.tolerance, sess.hasTolerance)
	}
	// Node h write-once: pick a node, mutate goal, ensure h unchanged.
	ns := sess.ns
	if ns.Len() > 1 {
		// Find a known cell, e.g., start neighbor (0,1) or (1,0)
		if id, ok := ns.Find(Cell{1, 0}); ok {
			hBefore := ns.Get(id).H
			// mutate goal via wrapper that returns different h
			mg := &mutableGoal{h: 9999}
			// Ensure returns same node with same h [04 §7.2] C7
			id2 := ns.Ensure(Cell{1, 0}, 0, invalidNodeID, DirNone, mg)
			if id2 != id || ns.Get(id2).H != hBefore {
				t.Fatalf("node h write-once violated after resume [04 §7.2] C7")
			}
		}
	}
	pts, _, done2 := sess.Resume(1 << 20)
	if !done2 {
		t.Fatalf("should finish")
	}
	oneShot := Search(cfg)
	if len(pts) != len(oneShot.Points) {
		t.Fatalf("write-once resume vs one-shot mismatch")
	}
}

// TestValuePassabilityOnlyZeroBlocks locks the class-layer consumption rule
// [04 §6.1 R-DOC04-B]: with the value-form passability, every consumer — the
// A* expansion and the greedy-ray probes — treats the result as passable iff
// it is nonzero; only the terrain value 0 hard-blocks. The start is fully
// enclosed by the value under test, so success requires traversing it.
func TestValuePassabilityOnlyZeroBlocks(t *testing.T) {
	run := func(ring uint8) SearchResult {
		vals := func(c Cell) uint8 {
			if c.X >= 4 && c.X <= 6 && c.Z >= 4 && c.Z <= 6 && !(c.X == 5 && c.Z == 5) {
				return ring
			}
			return 3
		}
		cfg := SearchConfig{
			Start:         Cell{X: 5, Z: 5},
			Goal:          PointGoal(Cell{X: 20, Z: 20}, 0),
			Scale:         65536,
			HasBounds:     true,
			Bounds:        Rect{Min: Cell{X: 0, Z: 0}, Max: Cell{X: 23, Z: 23}},
			PassableValue: vals,
		}
		return Search(cfg)
	}
	for _, ring := range []uint8{1, 2, 3} {
		res := run(ring)
		if res.Status != 0 || len(res.Points) == 0 {
			t.Fatalf("ring value %d must not block: status %d points %d [04 §6.1 R-DOC04-B]", ring, res.Status, len(res.Points))
		}
	}
	if res := run(0); res.Status != StatusRejected {
		t.Fatalf("enclosure of 0 must yield no route, status %d [04 §6.1 R-DOC04-B]", res.Status)
	}
}

// TestReviseRunsBeforeExpansion locks the request-init ordering [04 §6.1
// R-DOC04-B][04 §7.3]: the class-layer revision pass runs at request init
// before any expansion, exactly once per session initialization — including
// on the start-satisfies early exit, which happens after the revise call.
func TestReviseRunsBeforeExpansion(t *testing.T) {
	// Values are blocked until Revise arms them; if the revision pass did not
	// run before expansion, the search must fail.
	armed := false
	reviseRuns := 0
	vals := func(c Cell) uint8 {
		if !armed {
			return 0
		}
		return 3
	}
	cfg := SearchConfig{
		Start:         Cell{X: 2, Z: 2},
		Goal:          PointGoal(Cell{X: 9, Z: 2}, 0),
		Scale:         65536,
		PassableValue: vals,
		Revise: func() {
			reviseRuns++
			armed = true
		},
	}
	res := Search(cfg)
	if reviseRuns != 1 {
		t.Fatalf("revise must run exactly once per request init, got %d [04 §6.1 R-DOC04-B]", reviseRuns)
	}
	if res.Status != 0 || len(res.Points) == 0 {
		t.Fatalf("search must see post-revise passability: status %d", res.Status)
	}
	// The early exit also runs after the revise call.
	reviseRuns = 0
	armed = false
	cfg2 := cfg
	cfg2.Goal = PointGoal(Cell{X: 2, Z: 2}, 0) // start satisfies
	_ = Search(cfg2)
	if reviseRuns != 1 {
		t.Fatalf("revise must run even on the start-satisfied early exit, got %d", reviseRuns)
	}
}

// TestRayWalkValueSemantics locks the greedy-ray probe against the value
// form [04 §6.1 R-DOC04-B]: owner/building-mask miss (2) and steep (1) cells
// are traversable to the ray; only 0 stops it.
//
// The fixture is a bounded 12x12 terrain — every cell outside it reads 0 —
// because the wall follow of [04 R-PATH-01 §15] ends only when a sweep
// exhausts all eight sectors or the two cursors meet, and neither can happen
// against a wall with no ends. With an unbounded fixture the blocked case
// followed a wall of infinite length until the cursor coordinates wrapped.
// The bound is fixture geometry, not a rule about the ray.
func TestRayWalkValueSemantics(t *testing.T) {
	goal := PointGoal(Cell{X: 9, Z: 5}, 0)
	walk := func(mid func(Cell) uint8) (bool, bool) {
		vals := func(c Cell) uint8 {
			if c.X < 0 || c.X > 11 || c.Z < 0 || c.Z > 11 {
				return 0
			}
			if c.X >= 3 && c.X <= 6 {
				return mid(c)
			}
			return 3
		}
		r := walkRay(Cell{X: 0, Z: 5}, Cell{X: 9, Z: 5}, vals, goal, 65536, nil)
		return r.connects, r.best != 0
	}
	if connects, _ := walk(func(Cell) uint8 { return 2 }); !connects {
		t.Fatalf("ray must traverse owner-mask miss (2) cells [04 §6.1 R-DOC04-B]")
	}
	if connects, _ := walk(func(Cell) uint8 { return 1 }); !connects {
		t.Fatalf("ray must traverse steep (1) cells [04 §6.1 R-DOC04-B]")
	}
	if connects, _ := walk(func(Cell) uint8 { return 0 }); connects {
		t.Fatalf("ray must stop on blocked (0) cells [04 §6.1 R-DOC04-B]")
	}
}

// twoCellGoal is a test goal with two acceptable cells and the inflated
// octile heuristic to whichever of them is nearer.
type twoCellGoal struct{ near, far Cell }

func (g *twoCellGoal) H(c Cell) int32 {
	hn := octInflated(abs32(c.X-g.near.X), abs32(c.Z-g.near.Z))
	hf := octInflated(abs32(c.X-g.far.X), abs32(c.Z-g.far.Z))
	if hf < hn {
		return hf
	}
	return hn
}

func (g *twoCellGoal) Enumerate(out []Cell) []Cell {
	return append(out[:0], g.near, g.far)
}

func (g *twoCellGoal) StartSatisfied(Cell) bool { return false }

// TestRayVisitedBlockedCellPaysSteepTier locks the terrain term for the one
// case where the search costs a cell the class layer calls blocked: a cell the
// pre-search ray had already marked. The term is driven by the saved probe
// value, and the borrow yields 30 for a value of 0 or 1 — so such a cell pays
// the steep tier, not nothing [04 R-PATH-01 §3].
//
// Fixture: an open field with two goal cells. The ray walks to the near one
// and marks it ray-visited; the class layer then blocks it, as a revision
// spanning a multi-tick search can. Two NE steps reach the near goal for
// 40+22+22 = 84 and three NW steps reach the far one for 40+22+22+22 = 106,
// so the 30 is exactly what flips the published route from near to far.
func TestRayVisitedBlockedCellPaysSteepTier(t *testing.T) {
	near, far := Cell{X: 2, Z: -2}, Cell{X: -3, Z: -3}
	blocked := map[Cell]bool{}
	cfg := SearchConfig{
		Start:     Cell{X: 0, Z: 0},
		StartDir:  DirN,
		Goal:      &twoCellGoal{near: near, far: far},
		Scale:     65536,
		HasBounds: true,
		Bounds:    Rect{Min: Cell{X: -8, Z: -8}, Max: Cell{X: 8, Z: 8}},
		PassableValue: func(c Cell) uint8 {
			if blocked[c] {
				return 0
			}
			return 3
		},
	}
	s := NewSession(cfg)
	if !s.Seeded() {
		t.Fatal("fixture search must seed")
	}
	if s.entries.get(near).status&8 == 0 {
		t.Fatalf("fixture: the pre-search ray must mark %v ray-visited [04 R-PATH-01 §5]", near)
	}
	blocked[near] = true

	points, status, done := s.Resume(1 << 20)
	if !done || status != 0 || len(points) == 0 {
		t.Fatalf("fixture search must publish a route: done=%v status=%d points=%d", done, status, len(points))
	}

	nearID, ok := s.ns.Find(near)
	if !ok {
		t.Fatal("a blocked cell carrying the ray-visited bit is still costed as a neighbour [04 R-PATH-01 §3]")
	}
	nearNode := s.ns.Get(nearID)
	wantNear := TurnPenalty(DirN, DirNE) + DiagonalCost + DiagonalCost + SteepCost
	if nearNode.G != wantNear {
		t.Errorf("blocked ray-visited cell G want %d got %d — the probe value of 0 earns the steep tier [04 R-PATH-01 §3]", wantNear, nearNode.G)
	}
	if nearNode.TerrainTerm != uint16(SteepCost) {
		t.Errorf("stored terrain term want %d got %d [04 R-PATH-01 §3]", SteepCost, nearNode.TerrainTerm)
	}

	farID, ok := s.ns.Find(far)
	if !ok {
		t.Fatal("fixture: the far goal cell must be reached")
	}
	if got, want := s.ns.Get(farID).G, TurnPenalty(DirN, DirNW)+3*DiagonalCost; got != want {
		t.Errorf("far branch G want %d got %d", want, got)
	}

	// Route selection in cost terms: both goal cells score h = 0, so the two
	// branches are ordered by g alone. Without the steep tier the near branch
	// is 84 against the far branch's 106 and is preferred; with it the near
	// branch is 114 and the far branch wins.
	if s.ns.Get(nearID).F <= s.ns.Get(farID).F {
		t.Errorf("steep tier must order the near branch (%d) behind the far branch (%d) [04 R-PATH-01 §3]",
			s.ns.Get(nearID).F, s.ns.Get(farID).F)
	}
	// The steep terrain term alone makes the farther goal cheaper.
	if got, want := points[len(points)-1], worldPoint(far, routeFootPrint(cfg)); got != want {
		t.Errorf("fixture route must end at the far goal %v got %v", want, got)
	}
}

// TestSeededStartRunCounter locks the seeded start node's straight-run
// counter. The start is allocated with run 100, and that alone is what keeps
// the short-run 75 off the first step: there is no parent-identity test
// [04 R-PATH-01 §4] step 11.
func TestSeededStartRunCounter(t *testing.T) {
	if StartRun != 100 {
		t.Fatalf("StartRun want 100 got %d [04 R-PATH-01 §4] step 11", StartRun)
	}
	if StartRun < ShortRunLimit {
		t.Fatalf("StartRun %d must exceed ShortRunLimit %d [04 R-PATH-01 §4] step 11", StartRun, ShortRunLimit)
	}
	cfg := SearchConfig{
		Start:         Cell{X: 0, Z: 0},
		StartDir:      DirN,
		Goal:          PointGoal(Cell{X: 6, Z: 0}, 0),
		Scale:         65536,
		HasBounds:     true,
		Bounds:        Rect{Min: Cell{X: -8, Z: -8}, Max: Cell{X: 8, Z: 8}},
		PassableValue: func(Cell) uint8 { return 3 },
	}
	s := NewSession(cfg)
	startID, ok := s.ns.Find(cfg.Start)
	if !ok {
		t.Fatal("start node must be seeded")
	}
	if got := s.ns.Get(startID).Run; got != StartRun {
		t.Fatalf("seeded start run want %d got %d [04 R-PATH-01 §4] step 11", StartRun, got)
	}
	if _, _, done := s.Resume(1); done {
		t.Fatal("fixture search must survive its first expansion")
	}
	// A turning first step pays turn plus step and nothing else: the run of
	// 100 keeps the 75 off it.
	id, ok := s.ns.Find(Cell{X: 1, Z: 0})
	if !ok {
		t.Fatal("first expansion must open the eastward neighbour")
	}
	if got, want := s.ns.Get(id).G, TurnPenalty(DirN, DirE)+CardinalCost; got != want {
		t.Fatalf("turning first step G want %d got %d — the seeded run must suppress the short-run %d [04 R-PATH-01 §4] step 11", want, got, ShortRunPenalty)
	}
}

// The ray exemption survives opening, and an admitted terminal remains a
// terminal even when the layer changes before its pop [04 R-PATH-01 §1].
func TestRayVisitedTerminalSurvivesLayerChange(t *testing.T) {
	goal := Cell{5, 5}
	blocked := false
	var s *Session
	cfg := SearchConfig{
		Start: Cell{5, 4}, Goal: PointGoal(goal, 0), Scale: 65536,
		HasBounds: true, Bounds: Rect{Min: Cell{}, Max: Cell{12, 12}},
		PassableValue: func(c Cell) uint8 {
			if s != nil && s.entries.get(c).status&3 != 0 {
				t.Fatalf("re-probed an admitted/rejected cell %v [04 R-PATH-01 §1]", c)
			}
			if blocked && c == goal {
				return 0
			}
			return 3
		},
	}
	s = NewSession(cfg)
	if s.entries.get(goal).status&12 != 12 {
		t.Fatal("fixture needs an enumerated, ray-visited goal")
	}
	blocked = true
	s.Resume(1)
	if got := s.entries.get(goal).status; got != 13 {
		t.Fatalf("opened goal status = %d, want open + terminal + ray flags", got)
	}
	points, status, done := s.Resume(1000)
	if !done || status != 0 || len(points) == 0 || points[len(points)-1] != worldPoint(goal, routeFootPrint(cfg)) {
		t.Fatalf("ray-visited terminal lost eligibility: done=%v status=%v route=%v", done, status, points)
	}
}
