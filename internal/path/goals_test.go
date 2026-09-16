package path

import "testing"

func TestPointGoalHeuristic(t *testing.T) {
	// [04 §7.2] point/radius inflated octile 18*max+7*min, clamped.
	center := Cell{0, 0}
	g := PointGoal(center, 0)
	// Plan test vector: dx=10, dz=5 => 18*10+7*5=215.
	if got := g.H(Cell{10, 5}); got != 215 {
		t.Fatalf("point H(10,5) want 215 got %d", got)
	}
	if got := g.H(Cell{-10, 5}); got != 215 {
		t.Fatalf("point symmetry: H(-10,5) want 215 got %d", got)
	}
	if got := g.H(Cell{10, -5}); got != 215 {
		t.Fatalf("point symmetry: H(10,-5) want 215 got %d", got)
	}
	if got := g.H(Cell{0, 0}); got != 0 {
		t.Fatalf("point H at center want 0 got %d", got)
	}
	if got := g.H(Cell{1, 0}); got != 18 {
		t.Fatalf("H(1,0) want 18 got %d", got)
	}
	if got := g.H(Cell{1, 1}); got != 25 {
		t.Fatalf("H(1,1) want 25 got %d", got)
	}
	if got := g.H(Cell{5, 5}); got != 125 {
		t.Fatalf("H(5,5) want 125 got %d", got)
	}
	// Radius clamp: h = max(oct-R,0)
	g215 := PointGoal(center, 215)
	if got := g215.H(Cell{10, 5}); got != 0 {
		t.Fatalf("clamp at R=215: H(10,5) want 0 got %d", got)
	}
	g200 := PointGoal(center, 200)
	if got := g200.H(Cell{10, 5}); got != 15 {
		t.Fatalf("clamp at R=200: H(10,5) want 15 got %d", got)
	}
	g300 := PointGoal(center, 300)
	if got := g300.H(Cell{10, 5}); got != 0 {
		t.Fatalf("clamp at R=300: H(10,5) want 0 got %d", got)
	}
	gSmall := PointGoal(center, 50)
	if got := gSmall.H(Cell{2, 2}); got != 0 { // oct 50 => 50-50=0
		t.Fatalf("H(2,2) with R50 want 0 got %d", got)
	}
	if got := gSmall.H(Cell{3, 0}); got != 4 { // oct 54-50=4
		t.Fatalf("H(3,0) with R50 want 4 got %d", got)
	}
}

func TestAnnulusGoalHeuristic(t *testing.T) {
	// [04 §7.2] annulus V-shaped zero band with same inflated octile.
	center := Cell{0, 0}
	g := AnnulusGoal(center, 50, 100)
	// Hand-computed oct values and expected h:
	// oct = 18*max+7*min; h = inner-oct if oct<inner, 0 if in band, oct-outer if >outer.
	cases := []struct {
		cell Cell
		oct  int32
		h    int32
	}{
		{Cell{0, 0}, 0, 50},   // 50-0
		{Cell{1, 0}, 18, 32},  // 50-18
		{Cell{2, 0}, 36, 14},  // 50-36
		{Cell{2, 2}, 50, 0},   // exactly inner
		{Cell{3, 0}, 54, 0},   // inside band
		{Cell{4, 0}, 72, 0},   // inside
		{Cell{5, 0}, 90, 0},   // inside
		{Cell{6, 0}, 108, 8},  // 108-100
		{Cell{5, 5}, 125, 25}, // 125-100
		{Cell{7, 7}, 175, 75}, // 18*7+7*7=175 =>75
	}
	for _, tc := range cases {
		if got := g.H(tc.cell); got != tc.h {
			t.Fatalf("annulus H%v: oct %d want h %d got %d", tc.cell, tc.oct, tc.h, got)
		}
	}
	// Inward penalty rises as inner-oct.
	g2 := AnnulusGoal(center, 30, 60)
	if got := g2.H(Cell{0, 0}); got != 30 {
		t.Fatalf("annulus inner 30 H(0,0) want 30 got %d", got)
	}
	if got := g2.H(Cell{1, 0}); got != 12 { // 30-18
		t.Fatalf("annulus H(1,0) want 12 got %d", got)
	}
	if got := g2.H(Cell{2, 0}); got != 0 { // 36 inside
		t.Fatalf("annulus H(2,0) want 0 got %d", got)
	}
	if got := g2.H(Cell{5, 0}); got != 30 { // 90-60
		t.Fatalf("annulus H(5,0) want 30 got %d", got)
	}
}

func TestRestoredGoalsKeepSavedArrivalThresholds(t *testing.T) {
	center := Cell{}
	// The code-4 radius is zero, which a fresh point constructor turns into a
	// zero threshold. A saved threshold of four instead admits the cell two
	// cells away. It proves the restore constructor does not recompute it.
	point := PointGoalRestored(center, 0, 4)
	if !point.StartSatisfied(Cell{X: 2}) {
		t.Fatal("restored point goal discarded its saved squared threshold")
	}
	if PointGoal(center, 0).StartSatisfied(Cell{X: 2}) {
		t.Fatal("ordinary point constructor unexpectedly accepted the saved-only threshold")
	}

	// The code-5 raw radii still control the heuristic while the saved squared
	// band controls arrival. The intentionally inconsistent band accepts this
	// cell even though ordinary radii would not.
	annulus := AnnulusGoalRestored(center, 64, 128, 4, 9)
	if !annulus.StartSatisfied(Cell{X: 2}) {
		t.Fatal("restored annulus goal discarded its saved squared bounds")
	}
	if AnnulusGoal(center, 64, 128).StartSatisfied(Cell{X: 2}) {
		t.Fatal("ordinary annulus constructor unexpectedly accepted the saved-only band")
	}
	if got := annulus.H(center); got != 64 {
		t.Fatalf("restored annulus heuristic = %d, want raw inner radius 64", got)
	}
}

func TestRestoredGoalsUseSignedWrappedThresholds(t *testing.T) {
	center := Cell{}
	// The saved threshold is signed: zero distance is not within a negative
	// threshold, while a large cell delta wraps into the signed negative range.
	point := PointGoalRestored(center, 0, -1)
	if point.StartSatisfied(center) {
		t.Fatal("negative saved point threshold accepted zero distance")
	}
	if !point.StartSatisfied(Cell{X: 50000}) {
		t.Fatal("point arrival did not keep the signed wrapped cell product")
	}

	annulus := AnnulusGoalRestored(center, 0, 0, -1<<31, -1)
	if annulus.StartSatisfied(center) {
		t.Fatal("negative annulus bounds accepted zero distance")
	}
	if !annulus.StartSatisfied(Cell{X: 50000}) {
		t.Fatal("annulus arrival did not keep signed wrapped bounds")
	}
}

func TestGoalConstructorsKeepNegativeRadiusDivision(t *testing.T) {
	// Radius division truncates toward zero before the 32-bit square; it does
	// not clamp negative radii to zero [04 R-PATH-01 §9].
	if !PointGoal(Cell{}, -17).StartSatisfied(Cell{X: 1}) {
		t.Fatal("negative point radius was clamped before its stored square")
	}
	if !AnnulusGoal(Cell{}, -17, -17).StartSatisfied(Cell{X: 1}) {
		t.Fatal("negative annulus radii were clamped before their stored squares")
	}
}

func TestRectGoalHeuristic(t *testing.T) {
	// [04 §7.2] rect: admissible 16*max+6*min outside; inside 16*min(dist to edge).
	r := Rect{Min: Cell{0, 0}, Max: Cell{4, 4}}
	g := RectPerimeterGoal(r)
	// Outside cardinal.
	if got := g.H(Cell{6, 2}); got != 32 { // dx2
		t.Fatalf("rect outside (6,2) want 32 got %d", got)
	}
	if got := g.H(Cell{-2, 2}); got != 32 {
		t.Fatalf("rect outside (-2,2) want 32 got %d", got)
	}
	if got := g.H(Cell{2, 6}); got != 32 {
		t.Fatalf("rect outside (2,6) want 32 got %d", got)
	}
	if got := g.H(Cell{2, -2}); got != 32 {
		t.Fatalf("rect outside (2,-2) want 32 got %d", got)
	}
	// Outside diagonal corner.
	if got := g.H(Cell{6, 6}); got != 44 { // dx2 dz2 =>16*2+6*2=44
		t.Fatalf("rect outside (6,6) want 44 got %d", got)
	}
	if got := g.H(Cell{-2, -2}); got != 44 {
		t.Fatalf("rect outside (-2,-2) want 44 got %d", got)
	}
	if got := g.H(Cell{6, -2}); got != 44 {
		t.Fatalf("rect outside (6,-2) want 44 got %d", got)
	}
	// Outside mixed.
	if got := g.H(Cell{6, 5}); got != 38 { // dx2 dz1 =>32+6=38
		t.Fatalf("rect outside (6,5) want 38 got %d", got)
	}
	if got := g.H(Cell{5, 2}); got != 16 { // dx1
		t.Fatalf("rect outside (5,2) want 16 got %d", got)
	}
	// Inside.
	if got := g.H(Cell{2, 2}); got != 32 { // min dist 2 =>32
		t.Fatalf("rect inside (2,2) want 32 got %d", got)
	}
	if got := g.H(Cell{1, 2}); got != 16 { // min 1
		t.Fatalf("rect inside (1,2) want 16 got %d", got)
	}
	if got := g.H(Cell{1, 1}); got != 16 {
		t.Fatalf("rect inside (1,1) want 16 got %d", got)
	}
	if got := g.H(Cell{3, 3}); got != 16 { // dist to max 1
		t.Fatalf("rect inside (3,3) want 16 got %d", got)
	}
	// Border (goal cells) => 0.
	if got := g.H(Cell{0, 2}); got != 0 {
		t.Fatalf("rect border (0,2) want 0 got %d", got)
	}
	if got := g.H(Cell{4, 2}); got != 0 {
		t.Fatalf("rect border (4,2) want 0 got %d", got)
	}
	if got := g.H(Cell{2, 0}); got != 0 {
		t.Fatalf("rect border (2,0) want 0 got %d", got)
	}
	if got := g.H(Cell{2, 4}); got != 0 {
		t.Fatalf("rect border (2,4) want 0 got %d", got)
	}
	if got := g.H(Cell{0, 0}); got != 0 {
		t.Fatalf("rect border (0,0) want 0 got %d", got)
	}
	if got := g.H(Cell{4, 4}); got != 0 {
		t.Fatalf("rect border (4,4) want 0 got %d", got)
	}
	// Single-cell rect interior check.
	r1 := Rect{Min: Cell{5, 5}, Max: Cell{5, 5}}
	g1 := RectPerimeterGoal(r1)
	if got := g1.H(Cell{5, 5}); got != 0 {
		t.Fatalf("1x1 rect border H want 0 got %d", got)
	}
	if got := g1.H(Cell{6, 5}); got != 16 {
		t.Fatalf("1x1 rect outside (6,5) want 16 got %d", got)
	}
	if got := g1.H(Cell{6, 6}); got != 22 { // 16+6
		t.Fatalf("1x1 rect outside diag want 22 got %d", got)
	}
}

func TestGoalEnumerate(t *testing.T) {
	// Point: single center cell [04 §7.4].
	pg := PointGoal(Cell{5, 5}, 100)
	cells := pg.Enumerate(nil)
	if len(cells) != 1 || cells[0] != (Cell{5, 5}) {
		t.Fatalf("point enumerate want [(5,5)] got %v", cells)
	}
	// Reuse out buffer.
	buf := make([]Cell, 0, 4)
	buf = append(buf, Cell{99, 99})
	cells2 := pg.Enumerate(buf)
	if len(cells2) != 1 || cells2[0] != (Cell{5, 5}) {
		t.Fatalf("point enumerate with buf want [(5,5)] got %v", cells2)
	}

	// Annulus: single biased cell (inner+outer)/32 [04 §7.4].
	ag := AnnulusGoal(Cell{0, 0}, 32, 64)
	cells = ag.Enumerate(nil)
	bias := (int32(32) + int32(64)) / 32 // 3
	wantAnnulus := Cell{0, bias}
	if len(cells) != 1 || cells[0] != wantAnnulus {
		t.Fatalf("annulus enumerate want [%v] got %v", wantAnnulus, cells)
	}
	ag2 := AnnulusGoal(Cell{10, 10}, 100, 200)
	cells = ag2.Enumerate(nil)
	bias2 := int32((100 + 200) / 32) // 9
	want2 := Cell{10, 10 + bias2}
	if len(cells) != 1 || cells[0] != want2 {
		t.Fatalf("annulus enumerate2 want [%v] got %v", want2, cells)
	}

	// Rect perimeter: border cells [04 §7.2].
	r := Rect{Min: Cell{0, 0}, Max: Cell{4, 4}}
	rg := RectPerimeterGoal(r)
	cells = rg.Enumerate(nil)
	// width 5 height 5 => 2*5+2*5-4=16.
	if len(cells) != 16 {
		t.Fatalf("rect 5x5 enumerate want 16 got %d %v", len(cells), cells)
	}
	// Verify all border cells present and no interior.
	wantBorder := map[Cell]bool{}
	for x := int32(0); x <= 4; x++ {
		wantBorder[Cell{x, 0}] = true
		wantBorder[Cell{x, 4}] = true
	}
	for z := int32(1); z <= 3; z++ {
		wantBorder[Cell{0, z}] = true
		wantBorder[Cell{4, z}] = true
	}
	if len(wantBorder) != 16 {
		t.Fatalf("wantBorder size 16")
	}
	for _, c := range cells {
		if !wantBorder[c] {
			t.Fatalf("rect 5x5 enumerate unexpected %v", c)
		}
		delete(wantBorder, c)
	}
	if len(wantBorder) != 0 {
		t.Fatalf("rect 5x5 enumerate missing %v", wantBorder)
	}

	// Small rect counts.
	tests := []struct {
		rect Rect
		n    int
	}{
		{Rect{Cell{5, 5}, Cell{5, 5}}, 1},
		{Rect{Cell{0, 0}, Cell{1, 1}}, 4},
		{Rect{Cell{0, 0}, Cell{2, 2}}, 8},
		{Rect{Cell{0, 0}, Cell{1, 2}}, 6}, // width2 height3 => 4+6-4=6
		{Rect{Cell{0, 0}, Cell{2, 0}}, 3}, // width3 height1
		{Rect{Cell{0, 0}, Cell{0, 4}}, 5}, // width1 height5
	}
	for _, tc := range tests {
		g := RectPerimeterGoal(tc.rect)
		got := g.Enumerate(nil)
		if len(got) != tc.n {
			t.Fatalf("rect %v enumerate want %d got %d %v", tc.rect, tc.n, len(got), got)
		}
	}
}

func TestGoalStartSatisfied(t *testing.T) {
	// Point radius: squared-distance vs >>4-quantized radius [04 §7.2].
	pg := PointGoal(Cell{0, 0}, 32) // q=2 => qSq 4
	if !pg.StartSatisfied(Cell{0, 0}) {
		t.Fatalf("point start at center should satisfy")
	}
	if !pg.StartSatisfied(Cell{2, 0}) { // dist 4
		t.Fatalf("point (2,0) dist4 should satisfy qSq4")
	}
	if !pg.StartSatisfied(Cell{1, 1}) { // dist2
		t.Fatalf("point (1,1) dist2 should satisfy")
	}
	if pg.StartSatisfied(Cell{3, 0}) { // dist9
		t.Fatalf("point (3,0) dist9 should not satisfy")
	}
	if pg.StartSatisfied(Cell{2, 2}) { // dist8
		t.Fatalf("point (2,2) dist8 should not satisfy")
	}
	// Radius 0 => q0 => only center.
	pg0 := PointGoal(Cell{5, 5}, 0)
	if !pg0.StartSatisfied(Cell{5, 5}) {
		t.Fatalf("point R0 center should satisfy")
	}
	if pg0.StartSatisfied(Cell{6, 5}) {
		t.Fatalf("point R0 neighbor should not satisfy")
	}
	// Radius 15 => q0 => only center (15>>4=0)
	pg15 := PointGoal(Cell{0, 0}, 15)
	if !pg15.StartSatisfied(Cell{0, 0}) {
		t.Fatalf("R15 center should satisfy")
	}
	if pg15.StartSatisfied(Cell{1, 0}) {
		t.Fatalf("R15 (1,0) should not satisfy q0")
	}
	// Radius 16 => q1 => dist1
	pg16 := PointGoal(Cell{0, 0}, 16)
	if !pg16.StartSatisfied(Cell{1, 0}) {
		t.Fatalf("R16 (1,0) should satisfy q1")
	}
	if !pg16.StartSatisfied(Cell{0, 1}) {
		t.Fatalf("R16 (0,1) should satisfy")
	}
	if pg16.StartSatisfied(Cell{1, 1}) { // dist2 >1
		t.Fatalf("R16 (1,1) dist2 should not satisfy qSq1")
	}
	// Annulus: band [inner>>4, outer>>4] inclusive [04 §7.4] with mismatch.
	ag := AnnulusGoal(Cell{0, 0}, 32, 80) // qInner2 qOuter5 => 4..25
	if ag.StartSatisfied(Cell{0, 0}) {    // dist0 <4
		t.Fatalf("annulus center too close should not satisfy")
	}
	if ag.StartSatisfied(Cell{1, 0}) { // dist1 <4
		t.Fatalf("annulus (1,0) dist1 should not satisfy")
	}
	if !ag.StartSatisfied(Cell{2, 0}) { // dist4 lower bound
		t.Fatalf("annulus (2,0) dist4 should satisfy")
	}
	if !ag.StartSatisfied(Cell{3, 0}) { // dist9
		t.Fatalf("annulus (3,0) should satisfy")
	}
	if !ag.StartSatisfied(Cell{5, 0}) { // dist25 upper bound
		t.Fatalf("annulus (5,0) dist25 should satisfy")
	}
	if !ag.StartSatisfied(Cell{3, 4}) { // 9+16=25
		t.Fatalf("annulus (3,4) dist25 should satisfy")
	}
	if ag.StartSatisfied(Cell{6, 0}) { // dist36 >25
		t.Fatalf("annulus (6,0) dist36 should not satisfy")
	}
	if ag.StartSatisfied(Cell{1, 1}) { // dist2 <4
		t.Fatalf("annulus (1,1) dist2 should not satisfy")
	}
	// Rect: exactly border [04 §7.2].
	rg := RectPerimeterGoal(Rect{Cell{0, 0}, Cell{4, 4}})
	if !rg.StartSatisfied(Cell{0, 2}) {
		t.Fatalf("rect border (0,2) should satisfy")
	}
	if !rg.StartSatisfied(Cell{4, 2}) {
		t.Fatalf("rect border (4,2) should satisfy")
	}
	if !rg.StartSatisfied(Cell{2, 0}) {
		t.Fatalf("rect border (2,0) should satisfy")
	}
	if !rg.StartSatisfied(Cell{2, 4}) {
		t.Fatalf("rect border (2,4) should satisfy")
	}
	if !rg.StartSatisfied(Cell{0, 0}) {
		t.Fatalf("rect corner (0,0) should satisfy")
	}
	if rg.StartSatisfied(Cell{2, 2}) {
		t.Fatalf("rect interior (2,2) should not satisfy")
	}
	if rg.StartSatisfied(Cell{1, 1}) {
		t.Fatalf("rect interior (1,1) should not satisfy")
	}
	if rg.StartSatisfied(Cell{5, 2}) {
		t.Fatalf("rect outside (5,2) should not satisfy")
	}
	if rg.StartSatisfied(Cell{-1, 2}) {
		t.Fatalf("rect outside (-1,2) should not satisfy")
	}
	// 1x1 rect
	rg1 := RectPerimeterGoal(Rect{Cell{5, 5}, Cell{5, 5}})
	if !rg1.StartSatisfied(Cell{5, 5}) {
		t.Fatalf("1x1 rect center should satisfy")
	}
	if rg1.StartSatisfied(Cell{6, 5}) {
		t.Fatalf("1x1 rect neighbor should not satisfy")
	}
}

func TestRectGoalNormalization(t *testing.T) {
	// Rect with Min>Max should normalize.
	r := Rect{Min: Cell{4, 4}, Max: Cell{0, 0}}
	g := RectPerimeterGoal(r)
	if !g.StartSatisfied(Cell{0, 0}) {
		t.Fatalf("normalized rect should contain (0,0) border")
	}
	if !g.StartSatisfied(Cell{4, 4}) {
		t.Fatalf("normalized rect should contain (4,4) border")
	}
	if g.StartSatisfied(Cell{2, 2}) {
		t.Fatalf("normalized rect interior (2,2) should not satisfy")
	}
	cells := g.Enumerate(nil)
	if len(cells) != 16 {
		t.Fatalf("normalized 5x5 want 16 got %d", len(cells))
	}
}

func TestAnnulusBiasEnumeration(t *testing.T) {
	// Verify bias uses raw radii: (inner+outer)/32 truncated.
	cases := []struct {
		inner, outer int32
		bias         int32
	}{
		{32, 64, 3},
		{0, 0, 0},
		{100, 200, 9}, // 300/32=9
		{16, 16, 1},   // 32/32=1
		{15, 17, 1},   // 32/32=1
		{31, 33, 2},   // 64/32=2
	}
	for _, tc := range cases {
		g := AnnulusGoal(Cell{5, 5}, tc.inner, tc.outer)
		cells := g.Enumerate(nil)
		want := Cell{5, 5 + tc.bias}
		if cells[0] != want {
			t.Fatalf("annulus bias inner %d outer %d want %v got %v", tc.inner, tc.outer, want, cells[0])
		}
	}
}
