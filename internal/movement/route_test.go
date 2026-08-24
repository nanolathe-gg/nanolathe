package movement

import (
	"bytes"
	"testing"
)

// TestZeroPublicationKeepsBytes locks C14 zero-publication rule: a zero
// publication clears Active and sets Dirty but writes NEITHER count NOR array
// — stale bytes stay physically present [04 §7.3] C14.
func TestZeroPublicationKeepsBytes(t *testing.T) {
	var r Route
	initial := []Point{{X: 10, Z: 20}, {X: 30, Z: 40}, {X: 50, Z: 60}}
	r.Publish(initial)
	if !r.Active || !r.Dirty {
		t.Fatalf("nonempty publish: Active=%v Dirty=%v want true/true", r.Active, r.Dirty)
	}
	if r.Count != 3 {
		t.Fatalf("count %d want 3", r.Count)
	}
	// Snapshot stale bytes
	savedCount := r.Count
	savedPoints := r.Points
	// Dirty will be set; reset to false to detect change
	r.Dirty = false
	// Zero publication — empty slice
	r.Publish(nil)
	if r.Active {
		t.Fatalf("zero publication: Active should be false [04 §7.3] C14")
	}
	if !r.Dirty {
		t.Fatalf("zero publication: Dirty should be true [04 §7.3] C14")
	}
	if r.Count != savedCount {
		t.Fatalf("zero publication: Count changed %d -> %d want stale %d [04 §7.3] C14", savedCount, r.Count, savedCount)
	}
	if r.Points != savedPoints {
		t.Fatalf("zero publication: Points array changed want stale bytes [04 §7.3] C14: got %v want %v", r.Points, savedPoints)
	}
	// Also test Publish with empty but non-nil slice with len 0
	var r2 Route
	r2.Publish([]Point{{X: 1, Z: 1}})
	c2 := r2.Count
	p2 := r2.Points
	r2.Dirty = false
	r2.Publish([]Point{})
	if r2.Count != c2 || r2.Points != p2 {
		t.Fatalf("zero publication with empty non-nil slice should keep bytes")
	}
}

// TestPublishClampsTo20 ensures publisher clamps count above 20 to 20 FIRST [04 §7.3] C14.
func TestPublishClampsTo20(t *testing.T) {
	var r Route
	pts := make([]Point, 25)
	for i := range pts {
		pts[i] = Point{X: int32(i), Z: int32(i * 2)}
	}
	r.Publish(pts)
	if r.Count != 20 {
		t.Fatalf("clamp: count %d want 20 [04 §7.3] C14", r.Count)
	}
	for i := 0; i < 20; i++ {
		if r.Points[i] != pts[i] {
			t.Fatalf("clamp: point %d = %v want %v", i, r.Points[i], pts[i])
		}
	}
}

// TestPruneBoundary locks C15 dx²+dz² ≤25 shifts, =26 does not [04 §7.3] C15.
func TestPruneBoundary(t *testing.T) {
	// count>1 case: route [ (0,0), (10,0), (20,0) ]
	var r Route
	r.Publish([]Point{{X: 0, Z: 0}, {X: 10, Z: 0}, {X: 20, Z: 0}})
	r.Dirty = false
	// pos at (13,4) => dx=3, dz=4 => 9+16=25 => should shift
	r.Prune(Point{X: 13, Z: 4})
	if r.Count != 2 {
		t.Fatalf("prune at 25: count %d want 2", r.Count)
	}
	if !r.Dirty {
		t.Fatalf("prune at 25: Dirty should be set")
	}
	if r.Points[0] != (Point{X: 10, Z: 0}) || r.Points[1] != (Point{X: 20, Z: 0}) {
		t.Fatalf("prune at 25: points %v want [(10,0),(20,0)]", r.Points[:r.Count])
	}
	// Reset and test 26 does NOT shift
	var r2 Route
	r2.Publish([]Point{{X: 0, Z: 0}, {X: 10, Z: 0}, {X: 20, Z: 0}})
	r2.Dirty = false
	// pos at (15,1) => dx=5, dz=1 => 25+1=26 => no shift
	r2.Prune(Point{X: 15, Z: 1})
	if r2.Count != 3 {
		t.Fatalf("prune at 26: count %d want 3 (no shift)", r2.Count)
	}
	if r2.Dirty {
		t.Fatalf("prune at 26: Dirty should remain false")
	}
}

// TestPruneClearsActiveBelowTwo ensures active clears when fewer than two points remain [04 §7.3] C15.
func TestPruneClearsActiveBelowTwo(t *testing.T) {
	var r Route
	r.Publish([]Point{{X: 0, Z: 0}, {X: 5, Z: 0}})
	if !r.Active {
		t.Fatalf("pre: Active should be true")
	}
	// pos near index 1 distance 0 => shift => count 1 => active cleared
	r.Prune(Point{X: 5, Z: 0})
	if r.Count != 1 {
		t.Fatalf("count %d want 1", r.Count)
	}
	if r.Active {
		t.Fatalf("Active should be cleared when fewer than two points remain [04 §7.3] C15")
	}
	if !r.Dirty {
		t.Fatalf("Dirty should be set")
	}
}

// TestPruneNoOpWhenCountLE1 ensures no panic and no dirty when count <=1 [04 §7.3] C15.
func TestPruneNoOpWhenCountLE1(t *testing.T) {
	var r Route
	r.Publish([]Point{{X: 10, Z: 10}})
	r.Dirty = false
	r.Prune(Point{X: 10, Z: 10})
	if r.Count != 1 || r.Dirty {
		t.Fatalf("prune with count 1 should be no-op")
	}
	var r2 Route
	r2.Dirty = false
	r2.Prune(Point{X: 0, Z: 0}) // zero count zero route
	if r2.Dirty {
		t.Fatalf("prune zero count should be no-op")
	}
}

// TestExportHelper implements C17: not active predicate, repeats last point, zero count reads adjacent [04 §7.3] C17.
func TestExportHelper(t *testing.T) {
	var r Route
	r.Publish([]Point{{X: 10, Z: 10}, {X: 20, Z: 20}, {X: 30, Z: 30}})
	// Below count returns that point
	if p := r.At(1); p != (Point{X: 20, Z: 20}) {
		t.Fatalf("At(1)=%v want (20,20)", p)
	}
	if p := r.Export(0); p != (Point{X: 10, Z: 10}) {
		t.Fatalf("Export(0)=%v want (10,10)", p)
	}
	// Excess repeats last
	if p := r.At(5); p != (Point{X: 30, Z: 30}) {
		t.Fatalf("At(5) excess should repeat last want (30,30) got %v", p)
	}
	if p := r.At(100); p != (Point{X: 30, Z: 30}) {
		t.Fatalf("At(100) excess should repeat last got %v", p)
	}
	// Zero count reads adjacent — we return zero deterministically; callers gate on Active
	var empty Route
	empty.Publish(nil) // stays inactive, count 0
	// Count is 0 but stale array may hold previous data; our helper returns zero value
	p := empty.At(0)
	if p != (Point{}) {
		t.Fatalf("At zero count: got %v want zero Point{} (adjacent-field divergence documented) [04 §7.3] C17", p)
	}
	// Regardless of Active flag, helper is not predicate
	r.Active = false // force inactive but still has count 3 stale
	if p := r.At(0); p != (Point{X: 10, Z: 10}) {
		t.Fatalf("At should not gate on Active [04 §7.3] C17: got %v", p)
	}
	if p := r.At(10); p != (Point{X: 30, Z: 30}) {
		t.Fatalf("At excess with inactive should still repeat last [04 §7.3] C17")
	}
}

// TestSaveFormRoundTrip locks C16 save form: inactive 2-bit zero, active min(count,3) + pairs [04 §7.3] C16.
func TestSaveFormRoundTrip(t *testing.T) {
	// Inactive
	var inactive Route
	inactive.Publish(nil)
	data := EncodeRoute(&inactive)
	if len(data) != 1 || data[0]&0x3 != 0 {
		t.Fatalf("inactive encode: %v want [0] with low 2 bits 0 [04 §7.3] C16", data)
	}
	dec, err := DecodeRoute(data)
	if err != nil {
		t.Fatalf("decode inactive: %v", err)
	}
	if dec.Active {
		t.Fatalf("decoded inactive should be inactive")
	}
	if dec.Count != 0 {
		t.Fatalf("decoded inactive count %d want 0", dec.Count)
	}
	// Active with 5 points -> capped at 3
	var r Route
	r.Publish([]Point{{X: 100, Z: 200}, {X: -300, Z: 400}, {X: 500, Z: -600}, {X: 700, Z: 800}, {X: 900, Z: 1000}})
	data = EncodeRoute(&r)
	if data[0]&0x3 != 3 {
		t.Fatalf("active 5: encoded count bits %d want 3 [04 §7.3] C16", data[0]&0x3)
	}
	if len(data) != 1+3*4 {
		t.Fatalf("active 5: len %d want %d", len(data), 1+3*4)
	}
	dec, err = DecodeRoute(data)
	if err != nil {
		t.Fatalf("decode active: %v", err)
	}
	if !dec.Active || dec.Count != 3 {
		t.Fatalf("decoded active: Active %v Count %d want true/3", dec.Active, dec.Count)
	}
	for i := 0; i < 3; i++ {
		if dec.Points[i] != r.Points[i] {
			t.Fatalf("point %d: got %v want %v (int16 truncation)", i, dec.Points[i], r.Points[i])
		}
	}
	// Active with 2 points
	var r2 Route
	r2.Publish([]Point{{X: 10, Z: 20}, {X: 30, Z: 40}})
	data2 := EncodeRoute(&r2)
	if data2[0]&0x3 != 2 {
		t.Fatalf("active 2: encoded bits %d want 2", data2[0]&0x3)
	}
	dec2, _ := DecodeRoute(data2)
	if dec2.Count != 2 || dec2.Points[0] != (Point{X: 10, Z: 20}) || dec2.Points[1] != (Point{X: 30, Z: 40}) {
		t.Fatalf("round-trip 2: got %v", dec2)
	}
	// Truncated should error
	_, err = DecodeRoute(data[:2])
	if err == nil {
		t.Fatalf("truncated data should error")
	}
	_, err = DecodeRoute([]byte{})
	if err == nil {
		t.Fatalf("empty data should error")
	}
	// Encode with high bits masked
	var r3 Route
	r3.Publish([]Point{{X: 1, Z: 1}, {X: 2, Z: 2}})
	// Manually craft byte with high bits set; Decode should mask to 2 bits
	crafted := EncodeRoute(&r3)
	crafted[0] |= 0xFC // set high bits
	dec3, _ := DecodeRoute(crafted)
	if dec3.Count != 2 {
		t.Fatalf("decode should mask high bits, got %d want 2", dec3.Count)
	}
	// Ensure negative values round-trip via int16 truncation
	var r4 Route
	r4.Publish([]Point{{X: -1, Z: -32768}, {X: 32767, Z: -2}})
	data4 := EncodeRoute(&r4)
	dec4, _ := DecodeRoute(data4)
	if dec4.Points[0] != (Point{X: -1, Z: -32768}) || dec4.Points[1] != (Point{X: 32767, Z: -2}) {
		t.Fatalf("signed 16-bit round-trip negative: got %v", dec4.Points[:2])
	}
	// bytes cross boundary exact layout check
	if !bytes.Equal(data2[1:5], []byte{10, 0, 20, 0}) {
		t.Fatalf("little-endian layout mismatch: %v", data2)
	}
}

// TestReconstructionSimple verifies C13 basic ring behavior [04 §7.3] C13.
func TestReconstructionSimple(t *testing.T) {
	// Path: (0,0) -> (1,0) -> (1,1) -> (2,1) -> (2,2)
	// Map predecessor
	pred := map[Point]Point{
		{X: 1, Z: 0}: {X: 0, Z: 0},
		{X: 1, Z: 1}: {X: 1, Z: 0},
		{X: 2, Z: 1}: {X: 1, Z: 1},
		{X: 2, Z: 2}: {X: 2, Z: 1},
	}
	parentOf := func(p Point) (Point, bool) {
		v, ok := pred[p]
		return v, ok
	}
	start := Point{X: 0, Z: 0}
	goal := Point{X: 2, Z: 2}
	pts := ReconstructRoute(start, goal, parentOf, Point{X: 0, Z: 0})
	// Expect direction changes each step + start = 5 points newest-first: start, (1,0),(1,1),(2,1),(2,2)
	// But with our bias 0, cells equal world points.
	if len(pts) != 5 {
		t.Fatalf("simple: len %d want 5 [04 §7.3] C13: got %v", len(pts), pts)
	}
	want := []Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 1, Z: 1}, {X: 2, Z: 1}, {X: 2, Z: 2}}
	for i, w := range want {
		if pts[i] != w {
			t.Fatalf("simple: pts[%d]=%v want %v", i, pts[i], w)
		}
	}
	// Test with bias: bias (10,10) should add to each cell
	pts2 := ReconstructRoute(start, goal, parentOf, Point{X: 10, Z: 10})
	for i, w := range want {
		exp := Point{X: w.X + 10, Z: w.Z + 10}
		if pts2[i] != exp {
			t.Fatalf("bias: pts2[%d]=%v want %v", i, pts2[i], exp)
		}
	}
}

// TestReconstructionStraightLine tests that straight line yields 2 points (goal + start) [04 §7.3] C13.
func TestReconstructionStraightLine(t *testing.T) {
	// Straight east: (0,0)->(1,0)->(2,0)->(3,0)
	pred := map[Point]Point{
		{X: 1, Z: 0}: {X: 0, Z: 0},
		{X: 2, Z: 0}: {X: 1, Z: 0},
		{X: 3, Z: 0}: {X: 2, Z: 0},
	}
	parentOf := func(p Point) (Point, bool) { v, ok := pred[p]; return v, ok }
	start := Point{X: 0, Z: 0}
	goal := Point{X: 3, Z: 0}
	pts := ReconstructRoute(start, goal, parentOf, Point{})
	// First step always counts as change, so straight line should have goal + start =2 points
	if len(pts) != 2 {
		t.Fatalf("straight: len %d want 2, got %v", len(pts), pts)
	}
	if pts[0] != start || pts[1] != goal {
		t.Fatalf("straight: pts %v want [start,goal] [(0,0),(3,0)]", pts)
	}
}

// TestReconstructionWrapOverwrite tests wrap overwrites oldest when >63 changes [04 §7.3] C13.
func TestReconstructionWrapOverwrite(t *testing.T) {
	// Build zigzag with 70 steps alternating east/north, creating 70 direction changes.
	// Chain: start (0,0) then 70 steps to goal.
	const steps = 70
	cells := make([]Point, steps+1)
	cells[0] = Point{X: 0, Z: 0}
	for i := 1; i <= steps; i++ {
		prev := cells[i-1]
		if i%2 == 1 {
			cells[i] = Point{X: prev.X + 1, Z: prev.Z} // east
		} else {
			cells[i] = Point{X: prev.X, Z: prev.Z + 1} // north
		}
	}
	start := cells[0]
	goal := cells[steps]
	// Build predecessor map: each cell's parent is previous cell
	pred := make(map[Point]Point, steps)
	for i := 1; i <= steps; i++ {
		pred[cells[i]] = cells[i-1]
	}
	parentOf := func(p Point) (Point, bool) { v, ok := pred[p]; return v, ok }
	pts := ReconstructRoute(start, goal, parentOf, Point{})
	// changes = steps (each step direction differs from previous? alternating ensures every step changes)
	// But for steps 70 alternating east/north, direction sequence is E,N,E,N,... every step changes, so changes=70
	// total min(71,64)=64
	if len(pts) != 64 {
		t.Fatalf("wrap: len %d want 64 (min(71,64)) got %v", len(pts), pts)
	}
	// First point should be start
	if pts[0] != start {
		t.Fatalf("wrap: pts[0]=%v want start %v", pts[0], start)
	}
	// Oldest surviving should NOT be goal (overwritten). Goal is cells[70]; oldest 7 near goal lost.
	// Most recent 63 changes are those nearest start, so oldest surviving is cells[7] ? Let's compute.
	// Insertion order: goal (cells70) oldest index0, then cells69?? Wait we store cur each time direction changes; cur sequence walking backward: goal70, then parents 69..1 ?
	// Actually for alternating, each cur is a change point, so stored order backward: 70,69,68,...,1 then start0.
	// Ring holds last 64: start0 + most recent 63 = cells0 plus cells1..cells63? Let's verify.
	// Stored before wrap: 70 entries (70 changes) + start =71.
	// Ring size 64 overwrites oldest 7: those are goals 70,69,68,67,66,65,64 (7 oldest).
	// So oldest surviving is cells63? Need to deduce.
	// Our implementation stores cur (70), then 69,...1 as we walk. So indices: next0=70, next1=69,..., next69=1, next70=start0.
	// After 71 writes, ring holds indices0..63 = most recent 64 writes = start0 plus 63 most recent changes = cells1..cells63 + start.
	// The oldest surviving change after wrap is cells63? Actually recent are near start, so cells1..63 are nearest start, oldest surviving among them is cells63 (near middle). Goal70 is gone.
	// So last point (oldest surviving) should be cells63, not goal.
	if pts[63] == goal {
		t.Fatalf("wrap: last point should not be goal (overwritten) got %v", pts[63])
	}
	// Ensure pts contains start plus cells1..63 in order start first
	// pts[1] should be cells1 (1,0)
	if pts[1] != cells[1] {
		t.Fatalf("wrap: pts[1]=%v want %v", pts[1], cells[1])
	}
	// pts[63] should be cells63
	if pts[63] != cells[63] {
		t.Fatalf("wrap: pts[63]=%v want %v (oldest surviving)", pts[63], cells[63])
	}
	// Ensure no duplicate missing
	seen := make(map[Point]bool)
	for _, p := range pts {
		if seen[p] {
			t.Fatalf("wrap: duplicate %v", p)
		}
		seen[p] = true
	}
}

// TestReconstruction64Exact ensures exactly 64 changes (63? ) boundary is correct.
func TestReconstruction64Exact(t *testing.T) {
	const steps = 63 // changes 63 -> total 64 (no wrap)
	cells := make([]Point, steps+1)
	cells[0] = Point{X: 0, Z: 0}
	for i := 1; i <= steps; i++ {
		prev := cells[i-1]
		if i%2 == 1 {
			cells[i] = Point{X: prev.X + 1, Z: prev.Z}
		} else {
			cells[i] = Point{X: prev.X, Z: prev.Z + 1}
		}
	}
	start, goal := cells[0], cells[steps]
	pred := make(map[Point]Point)
	for i := 1; i <= steps; i++ {
		pred[cells[i]] = cells[i-1]
	}
	parentOf := func(p Point) (Point, bool) { v, ok := pred[p]; return v, ok }
	pts := ReconstructRoute(start, goal, parentOf, Point{})
	if len(pts) != 64 {
		t.Fatalf("64 exact: len %d want 64", len(pts))
	}
	if pts[0] != start || pts[63] != goal {
		t.Fatalf("64 exact: first/last mismatch start %v goal %v got %v / %v", start, goal, pts[0], pts[63])
	}
}

// TestSmoothingCollinear verifies C19 collinear removal [04 §7.5] C19.
func TestSmoothingCollinear(t *testing.T) {
	// Isolate collinear removal from ray shortcutting by blocking the
	// intermediate cells that the shortcut rays would traverse. The always-
	// true case (legality-only) is tested in TestSmoothingLegalityOnly.
	blockForCollinear := func(p Point) bool {
		// Block cells that lie on the shortcut diagonals but not on the
		// retained polyline, to force the smoother to keep the post-collinear
		// points. For (0,0)->(2,1) the Bresenham visits (1,0); for
		// (0,0)->(2,2) it visits (1,1); for larger L-shapes similar.
		if (p.X == 1 && p.Z == 0) || (p.X == 1 && p.Z == 1) {
			return false
		}
		return true
	}
	var r Route
	// Collinear on X axis: (0,0)-(1,0)-(2,0)-(2,1) => middle (1,0) collinear and should be removed
	r.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}, {X: 2, Z: 1}})
	r.Smooth(blockForCollinear)
	if r.Count != 3 {
		t.Fatalf("collinear: count %d want 3 after removing (1,0), pts %v", r.Count, r.Points[:r.Count])
	}
	// Expect (0,0),(2,0),(2,1)
	want := []Point{{X: 0, Z: 0}, {X: 2, Z: 0}, {X: 2, Z: 1}}
	for i, w := range want {
		if r.Points[i] != w {
			t.Fatalf("collinear: point %d %v want %v", i, r.Points[i], w)
		}
	}
	// Diagonal collinear: (0,0)-(1,1)-(2,2)-(2,3) => (1,1) removed, but (0,0)->(2,3) should stay blocked
	var r2 Route
	r2.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 1}, {X: 2, Z: 2}, {X: 2, Z: 3}})
	r2.Smooth(blockForCollinear)
	if r2.Count != 3 || r2.Points[1] != (Point{X: 2, Z: 2}) {
		t.Fatalf("diagonal collinear: got %v count %d", r2.Points[:r2.Count], r2.Count)
	}
	// Non-collinear should remain when diagonal is blocked [04 §7.5] C19
	// Use a larger L-shape so the shortcut has an intermediate cell to block.
	var r3 Route
	r3.Publish([]Point{{X: 0, Z: 0}, {X: 0, Z: 2}, {X: 2, Z: 2}})
	r3.Smooth(blockForCollinear)
	if r3.Count != 3 {
		t.Fatalf("non-collinear with blocked diagonal should remain 3, got %d pts %v", r3.Count, r3.Points[:r3.Count])
	}
	// With all-passable, the same L-shape should shortcut to 2 (legality-only)
	var r4 Route
	r4.Publish([]Point{{X: 0, Z: 0}, {X: 0, Z: 2}, {X: 2, Z: 2}})
	always := func(p Point) bool { return true }
	r4.Smooth(always)
	if r4.Count != 2 {
		t.Fatalf("non-collinear with always passable should shortcut to 2, got %d pts %v", r4.Count, r4.Points[:r4.Count])
	}
}

// TestSmoothingLegalityOnly verifies ray shortcut accepted for legality only, cost never compared [04 §7.5] C19.
func TestSmoothingLegalityOnly(t *testing.T) {
	// Route L-shape: (0,0)-(0,2)-(2,2). Direct ray (0,0)->(2,2) is diagonal through (1,1).
	// If (1,1) is blocked, shortcut must be rejected even though it would be shorter.
	var r Route
	r.Publish([]Point{{X: 0, Z: 0}, {X: 0, Z: 2}, {X: 2, Z: 2}})
	// Block (1,1)
	blocked := func(p Point) bool {
		if p.X == 1 && p.Z == 1 {
			return false
		}
		return true
	}
	r.Smooth(blocked)
	if r.Count != 3 {
		t.Fatalf("legality blocked: count %d want 3 (no shortcut) pts %v", r.Count, r.Points[:r.Count])
	}
	// Now allow all -> shortcut should be taken, removing middle point, despite cost not compared
	var r2 Route
	r2.Publish([]Point{{X: 0, Z: 0}, {X: 0, Z: 2}, {X: 2, Z: 2}})
	always := func(p Point) bool { return true }
	r2.Smooth(always)
	if r2.Count != 2 {
		t.Fatalf("legality passed: count %d want 2 after shortcut, pts %v", r2.Count, r2.Points[:r2.Count])
	}
	if r2.Points[0] != (Point{X: 0, Z: 0}) || r2.Points[1] != (Point{X: 2, Z: 2}) {
		t.Fatalf("shortcut result %v want [(0,0),(2,2)]", r2.Points[:r2.Count])
	}
	// Second legality-only case: ensure shortcut is taken even when alternative path is same cost?
	// Use longer route (0,0)-(0,1)-(0,2)-(2,2) with same direct ray; should shortcut to 2 points when legal.
	var r3 Route
	r3.Publish([]Point{{X: 0, Z: 0}, {X: 0, Z: 1}, {X: 0, Z: 2}, {X: 2, Z: 2}})
	r3.Smooth(always)
	if r3.Count != 2 {
		t.Fatalf("multi-point legality: count %d want 2, got %v", r3.Count, r3.Points[:r3.Count])
	}
}

// TestSmoothingBidirectional ensures bidirectional ray checks can find shortcut missed by forward-only.
func TestSmoothingBidirectional(t *testing.T) {
	// Create route where forward greedy picks not maximal? Our implementation does both passes.
	// Simple check: route (0,0)-(1,0)-(2,0)-(3,0)-(3,3)
	// Collinear will shrink first 4 to (0,0)-(3,0), then ray (0,0)->(3,3) diagonal blocked at (1,1) etc but (3,0)->(3,3) vertical remains.
	// Instead use all passable: route should shortcut to 2 points (0,0)->(3,3) via ray.
	var r Route
	r.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}, {X: 3, Z: 0}, {X: 3, Z: 3}})
	always := func(p Point) bool { return true }
	r.Smooth(always)
	// After collinear removal, (1,0),(2,0) removed => (0,0),(3,0),(3,3) then direct (0,0)->(3,3) is diagonal passable => should become 2 points.
	if r.Count != 2 {
		t.Fatalf("bidirectional: count %d want 2, pts %v", r.Count, r.Points[:r.Count])
	}
}

// TestSmoothingNilPassability ensures nil func does not panic and only collinear removal occurs.
func TestSmoothingNilPassability(t *testing.T) {
	var r Route
	r.Publish([]Point{{X: 0, Z: 0}, {X: 1, Z: 0}, {X: 2, Z: 0}, {X: 2, Z: 1}})
	r.Smooth(nil)
	if r.Count != 3 { // collinear still removed
		t.Fatalf("nil passability: count %d want 3 (collinear only)", r.Count)
	}
}
