package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// helper to make a collision state with defaults.
func newMover(id int, x, z int32, vx, vz int32, anchor Cell, fx, fz int16, maxVel int32, heading uint16, mode uint8) *CollisionState {
	s := &CollisionState{
		ID:           id,
		X:            x,
		Z:            z,
		Y:            0,
		VX:           vx,
		VZ:           vz,
		Speed:        int32(80000), // arbitrary
		Heading:      heading,
		MaxVelocity:  maxVel,
		FootPrintX:   fx,
		FootPrintZ:   fz,
		Mode:         mode & 0x3,
		CachedAnchor: anchor,
		CachedMode:   mode & 0x3,
		OldAnchor:    anchor,
		Blocked:      false,
		Dirty:        false,
	}
	return s
}

func TestOccupancySweepClaimFirstBlocksLater(t *testing.T) { // [04 §8.2] C22
	// Two movers both want cell (1,0). First claims, second blocked.
	// Directly test grid Stamp claim order.
	_ = newMover(1, 0, 0, 0, 0, Cell{0, 0}, 1, 1, 65536, 0, 2)
	_ = newMover(2, 0, 32*65536, 0, 0, Cell{0, 2}, 1, 1, 65536, 0, 2)
	// Simpler: directly test grid Stamp claim order.
	grid2 := NewOccupancyGrid()
	if !grid2.Stamp(Cell{1, 0}, 1, 1, 1) {
		t.Fatalf("first stamp should succeed")
	}
	if grid2.Stamp(Cell{1, 0}, 1, 1, 2) {
		t.Fatalf("second stamp to same cell should be blocked [04 §8.2] C22 claim-first")
	}
}

func TestVacatedReuseSameSweep(t *testing.T) { // [04 §8.2] C22
	grid := NewOccupancyGrid()
	// A at (0,0) vacates to (1,0); B at (2,0) wants (0,0) — should succeed after A vacates.
	// Seed occupancy.
	if !grid.Stamp(Cell{0, 0}, 1, 1, 1) {
		t.Fatal("seed A")
	}
	if !grid.Stamp(Cell{2, 0}, 1, 1, 2) {
		t.Fatal("seed B")
	}
	// Simulate sweep order: A moves first.
	a := &CollisionState{ID: 1, X: 0, Z: 0, VX: int32(1 * worldUnitsPerCell), VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{0, 0}, CachedMode: 1, OldAnchor: Cell{0, 0}, MaxVelocity: 65536}
	b := &CollisionState{ID: 2, X: int32(2 * worldUnitsPerCell), Z: 0, VX: int32(-2 * worldUnitsPerCell), VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{2, 0}, CachedMode: 1, OldAnchor: Cell{2, 0}, MaxVelocity: 65536}
	states := []*CollisionState{a, b}
	// perCell checks occupancy excluding self's old (CommitOne will handle self ignore via grid.FootprintOccupied logic? But our perCell factory will use grid.IsOccupied check excluding id.)
	perCellFactory := func(s *CollisionState) func(Cell) bool {
		return func(c Cell) bool {
			// true means passable (not occupied by other)
			if occ, ok := grid.OccupantAt(c); ok && occ != s.ID {
				return false
			}
			return true
		}
	}
	// Need aggregate nil
	CommitSweep(states, grid, perCellFactory, nil)
	// After sweep, A should be at (1,0), B at (0,0)
	if occ, _ := grid.OccupantAt(Cell{1, 0}); occ != 1 {
		t.Fatalf("A should occupy (1,0) after vacated reuse, got %v at (1,0)", occ)
	}
	if occ, _ := grid.OccupantAt(Cell{0, 0}); occ != 2 {
		t.Fatalf("B should occupy vacated (0,0), got %v", occ)
	}
	if occ, ok := grid.OccupantAt(Cell{2, 0}); ok {
		t.Fatalf("old B cell (2,0) should be vacated, still occupied by %d", occ)
	}
}

func TestHeadOnSwapBlocks(t *testing.T) { // [04 §8.2] C22 head-on swaps block
	grid := NewOccupancyGrid()
	if !grid.Stamp(Cell{0, 0}, 1, 1, 1) {
		t.Fatal("seed A")
	}
	if !grid.Stamp(Cell{1, 0}, 1, 1, 2) {
		t.Fatal("seed B")
	}
	a := &CollisionState{ID: 1, X: 0, Z: 0, VX: int32(1 * worldUnitsPerCell), VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{0, 0}, CachedMode: 1, OldAnchor: Cell{0, 0}, MaxVelocity: 65536}
	b := &CollisionState{ID: 2, X: int32(1 * worldUnitsPerCell), Z: 0, VX: int32(-1 * worldUnitsPerCell), VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{1, 0}, CachedMode: 1, OldAnchor: Cell{1, 0}, MaxVelocity: 65536}
	states := []*CollisionState{a, b}
	perCellFactory := func(s *CollisionState) func(Cell) bool {
		return func(c Cell) bool {
			if occ, ok := grid.OccupantAt(c); ok && occ != s.ID {
				return false
			}
			return true
		}
	}
	CommitSweep(states, grid, perCellFactory, nil)
	// Both should remain blocked at old positions, no swap.
	if occ, _ := grid.OccupantAt(Cell{0, 0}); occ != 1 {
		t.Fatalf("head-on: (0,0) should stay 1, got %v", occ)
	}
	if occ, _ := grid.OccupantAt(Cell{1, 0}); occ != 2 {
		t.Fatalf("head-on: (1,0) should stay 2, got %v", occ)
	}
	if !a.Blocked || !b.Blocked {
		t.Fatalf("both should be blocked after head-on swap attempt, a.Blocked=%v b.Blocked=%v", a.Blocked, b.Blocked)
	}
	// Positions should be clamped but anchors unchanged (no occupancy stamp)
	if a.CachedAnchor != (Cell{0, 0}) {
		t.Fatalf("A cached anchor should remain old on blocked")
	}
	if b.CachedAnchor != (Cell{1, 0}) {
		t.Fatalf("B cached anchor should remain old")
	}
}

func TestSameCellFastPathSkipsValidator(t *testing.T) { // [04 §8.2] C23
	grid := NewOccupancyGrid()
	startX, startZ := world.PlacementCenter(5, 3, 1, 1)
	s := &CollisionState{
		ID:           1,
		X:            int32(startX),
		Z:            int32(startZ),
		VX:           int32(1000), // small creep within same cell
		VZ:           int32(-500),
		FootPrintX:   1,
		FootPrintZ:   1,
		Mode:         1,
		CachedAnchor: Cell{5, 3},
		CachedMode:   1,
		OldAnchor:    Cell{5, 3},
		MaxVelocity:  65536,
	}
	// Propose small move that stays in same cell (anchor unchanged)
	bx, bz := s.HalfBias()
	propX := s.X + s.VX
	propZ := s.Z + s.VZ
	propAnchor := QuantizedAnchor(propX, propZ, bx, bz)
	if propAnchor != s.CachedAnchor {
		t.Fatalf("test setup: expected same cell anchor %v got %v (bias %d,%d world %d,%d)", s.CachedAnchor, propAnchor, bx, bz, propX, propZ)
	}
	validatorCalls := 0
	perCell := func(c Cell) bool {
		validatorCalls++
		return true
	}
	aggregateCalls := 0
	aggregate := func() bool {
		aggregateCalls++
		return true
	}
	// Use CommitOne which internally decides fast path
	grid.Stamp(Cell{5, 3}, 1, 1, 1)
	revBefore := grid.Revision()
	fast, blocked := s.CommitOne(grid, s.Mode, perCell, aggregate)
	if !fast {
		t.Fatalf("expected fast path [04 §8.2] C23, got fast=%v blocked=%v", fast, blocked)
	}
	if blocked {
		t.Fatalf("fast path should not be blocked")
	}
	if validatorCalls != 0 {
		t.Fatalf("validator should not be called on fast path [04 §8.2] C23, called %d times", validatorCalls)
	}
	if aggregateCalls != 0 {
		t.Fatalf("aggregate should not be called on fast path")
	}
	if !s.Dirty {
		t.Fatalf("fast path must mark dirty [04 §8.2] C23")
	}
	if grid.Revision() != revBefore {
		t.Fatalf("fast path must NOT restamp occupancy [04 §8.2] C23, rev changed %d -> %d", revBefore, grid.Revision())
	}
	// Occupancy should still be at same cell
	if occ, _ := grid.OccupantAt(Cell{5, 3}); occ != 1 {
		t.Fatalf("occupancy should remain at old cell on fast path")
	}
}

func TestFastPathRequiresModeEquality(t *testing.T) { // [04 §8.2] C23
	// The committed mode is 2 (airborne) and the proposal below is mode 1
	// (grounded): the cell pair matches but the mode does not, so the fast
	// path is refused [04 R-COLL-01 §1][04 R-AIR-01 §3].
	s := &CollisionState{
		ID:           1,
		X:            0,
		Z:            0,
		VX:           100,
		VZ:           0,
		FootPrintX:   1,
		FootPrintZ:   1,
		Mode:         2,
		CachedAnchor: Cell{0, 0},
		CachedMode:   2,
		OldAnchor:    Cell{0, 0},
		MaxVelocity:  65536,
	}
	bx, bz := s.HalfBias()
	propX := s.X + s.VX
	propZ := s.Z + s.VZ
	propAnchor := QuantizedAnchor(propX, propZ, bx, bz)
	// same anchor but different mode should NOT be fast path
	calls := 0
	perCell := func(c Cell) bool { calls++; return true }
	fast, _ := s.CommitOne(nil, 1, perCell, nil) // proposed mode 1 differs
	if fast {
		t.Fatalf("same anchor but mode change must NOT take fast path [04 §8.2] C23")
	}
	if calls == 0 {
		t.Fatalf("mode-changing proposal must call validator [04 §8.2] C23")
	}
	_ = propAnchor
}

func TestBlockedNoFallback(t *testing.T) { // [04 §8.2] C24 no X-only/Z-only fallback
	grid := NewOccupancyGrid()
	if !grid.Stamp(Cell{0, 0}, 1, 1, 1) {
		t.Fatal("seed mover")
	}
	if !grid.Stamp(Cell{1, 0}, 1, 1, 99) {
		t.Fatal("seed blocker at destination")
	}
	s := &CollisionState{
		ID:           1,
		X:            0,
		Z:            0,
		VX:           int32(1 * worldUnitsPerCell),
		VZ:           0,
		Speed:        65536,
		Heading:      0, // north -> Z+
		MaxVelocity:  65536,
		FootPrintX:   1,
		FootPrintZ:   1,
		Mode:         1,
		CachedAnchor: Cell{0, 0},
		CachedMode:   1,
		OldAnchor:    Cell{0, 0},
	}
	calls := 0
	perCell := func(c Cell) bool {
		calls++
		// block at proposed anchor (1,0)
		if c == (Cell{1, 0}) {
			return false
		}
		return true
	}
	// Also would pass X-only (0,0 still? Actually X-only would be maybe (1,0) still blocked) but spec says no second call.
	fast, blocked := s.CommitOne(grid, s.Mode, perCell, nil)
	if fast {
		t.Fatalf("blocked should not be fast path")
	}
	if !blocked {
		t.Fatalf("expected blocked [04 §8.2] C24")
	}
	if calls != 1 {
		t.Fatalf("validator should be called exactly once, no X-only/Z-only fallback [04 §8.2] C24, got %d", calls)
	}
	// Ensure no second validator for fallback was attempted
}

func TestBlockedSpeedCapOnlyWhenHigher(t *testing.T) { // [04 §8.2] C24 cap at MaxVelocity/2 if higher
	// Case 1: higher -> caps
	s1 := &CollisionState{
		ID:           1,
		X:            int32(0),
		Z:            int32(0),
		VX:           0,
		VZ:           0,
		Speed:        100000, // > half
		Heading:      16384,  // east via sin? Heading 16384 = 90° (east)
		MaxVelocity:  80000,  // half =40000
		FootPrintX:   1,
		FootPrintZ:   1,
		Mode:         1,
		CachedAnchor: Cell{0, 0},
		CachedMode:   1,
		OldAnchor:    Cell{0, 0},
	}
	s1.ApplyBlocked()
	if s1.Speed != 40000 {
		t.Fatalf("speed higher than half should cap at MaxVelocity/2 [04 §8.2] C24, got %d want %d", s1.Speed, 40000)
	}
	// Case 2: lower -> unchanged
	s2 := &CollisionState{
		ID:           2,
		X:            0,
		Z:            0,
		Speed:        20000, // < half
		Heading:      0,
		MaxVelocity:  80000,
		FootPrintX:   1,
		FootPrintZ:   1,
		Mode:         1,
		CachedAnchor: Cell{0, 0},
		CachedMode:   1,
		OldAnchor:    Cell{0, 0},
	}
	before := s2.Speed
	s2.ApplyBlocked()
	if s2.Speed != before {
		t.Fatalf("speed lower than half should remain unchanged [04 §8.2] C24, got %d want %d", s2.Speed, before)
	}
	// Case 3: velocity recomputed at capped speed + heading [04 §8.2] C24
	s3 := &CollisionState{
		ID:           3,
		X:            0,
		Z:            0,
		Speed:        80000,
		Heading:      0, // -Z, up-screen [04 R-MOV-01 §4]
		MaxVelocity:  80000,
		FootPrintX:   1,
		FootPrintZ:   1,
		Mode:         1,
		CachedAnchor: Cell{0, 0},
		CachedMode:   1,
		OldAnchor:    Cell{0, 0},
	}
	s3.ApplyBlocked()
	// Speed capped to 40000; the blocked branch writes the SAME negated form as
	// the ordinary position step — vx = -sinq(heading, half),
	// vz = -cosq(heading, half) [04 R-COLL-01 §1] "The blocked branch",
	// [04 R-MOV-01 §4]. Heading 0 therefore gives VX ~0 and VZ ~-40000; the
	// mirror below carried the pre-correction signs.
	sin := numeric.Sin(numeric.Angle(s3.Heading))
	cos := numeric.Cos(numeric.Angle(s3.Heading))
	wantVX := -int32((int64(sin)*int64(s3.Speed) + 0x1000) >> 13)
	wantVZ := -int32((int64(cos)*int64(s3.Speed) + 0x1000) >> 13)
	if s3.VZ >= 0 {
		t.Fatalf("heading 0 must recompute a blocked velocity toward -Z [04 R-COLL-01 §1], got VZ=%d", s3.VZ)
	}
	if s3.VX != wantVX || s3.VZ != wantVZ {
		t.Fatalf("blocked must recompute velocity at capped speed+heading [04 R-COLL-01 §1], got (%d,%d) want (%d,%d)", s3.VX, s3.VZ, wantVX, wantVZ)
	}
}

func TestBlockedClampAndDirtyWithoutOccupancy(t *testing.T) { // [04 §8.2] C24 clamp 0x7FFFF, dirty, occupancy untouched
	grid := NewOccupancyGrid()
	if !grid.Stamp(Cell{5, 5}, 2, 2, 1) {
		t.Fatal("seed")
	}
	revBefore := grid.Revision()
	s := &CollisionState{
		ID:           1,
		X:            int32(5*worldUnitsPerCell + worldUnitsPerCell/2), // centre of footprint?
		Z:            int32(5*worldUnitsPerCell + worldUnitsPerCell/2),
		VX:           int32(5 * worldUnitsPerCell), // far beyond band
		VZ:           int32(-5 * worldUnitsPerCell),
		Speed:        80000,
		Heading:      0,
		MaxVelocity:  80000,
		FootPrintX:   2,
		FootPrintZ:   2,
		Mode:         1,
		CachedAnchor: Cell{5, 5},
		CachedMode:   1,
		OldAnchor:    Cell{5, 5},
		Dirty:        false,
	}
	// Force blocked via validator false
	perCell := func(c Cell) bool { return false }
	fast, blocked := s.CommitOne(grid, s.Mode, perCell, nil)
	if fast || !blocked {
		t.Fatalf("expected blocked")
	}
	if !s.Dirty {
		t.Fatalf("blocked must mark dirty [04 §8.2] C24")
	}
	if grid.Revision() != revBefore {
		t.Fatalf("blocked must NOT clear/restamp occupancy [04 §8.2] C24, rev %d -> %d", revBefore, grid.Revision())
	}
	if grid.Count() != 4 { // 2x2 =4 cells
		t.Fatalf("occupancy count should remain 4 after blocked, got %d", grid.Count())
	}
	// Clamp: centre = oldAnchor*cell + halfSpan; halfSpan = fx*cell/2 =1*cell
	halfSpanX := int64(s.FootPrintX) * worldUnitsPerCell / 2
	halfSpanZ := int64(s.FootPrintZ) * worldUnitsPerCell / 2
	centreX := int64(Cell{5, 5}.X)*worldUnitsPerCell + halfSpanX
	centreZ := int64(Cell{5, 5}.Z)*worldUnitsPerCell + halfSpanZ
	band := int64(blockedBand)
	minX := centreX - band
	maxX := centreX + band
	minZ := centreZ - band
	maxZ := centreZ + band
	if int64(s.X) < minX || int64(s.X) > maxX {
		t.Fatalf("blocked X not clamped to old footprint centre±0x7FFFF [04 §8.2] C24, got %d range [%d,%d] centre %d", s.X, minX, maxX, centreX)
	}
	if int64(s.Z) < minZ || int64(s.Z) > maxZ {
		t.Fatalf("blocked Z not clamped [04 §8.2] C24, got %d range [%d,%d]", s.Z, minZ, maxZ)
	}
	// Also check cached anchor NOT updated on blocked
	if s.CachedAnchor != (Cell{5, 5}) {
		t.Fatalf("blocked must not update cached anchor")
	}
}

func TestValidatorRowMajorShortCircuit(t *testing.T) { // [04 §8.2] C25 immediate return on rejecting per-cell
	anchor := Cell{10, 10}
	fx, fz := int16(2), int16(2)
	// row-major order: (10,10), (11,10), (10,11), (11,11)
	callOrder := []Cell{}
	perCell := func(c Cell) bool {
		callOrder = append(callOrder, c)
		// reject at third cell (10,11)
		if c == (Cell{10, 11}) {
			return false
		}
		return true
	}
	valid := ValidateFootprint(anchor, fx, fz, perCell, nil)
	if valid {
		t.Fatalf("should be invalid")
	}
	if len(callOrder) != 3 {
		t.Fatalf("row-major immediate return [04 §8.2] C25 expected 3 calls (up to rejecting cell), got %d: %v", len(callOrder), callOrder)
	}
	expected := []Cell{{10, 10}, {11, 10}, {10, 11}}
	for i, want := range expected {
		if callOrder[i] != want {
			t.Fatalf("call %d want %v got %v, row-major order violated [04 §8.2] C25", i, want, callOrder[i])
		}
	}
	// Test early reject at first cell
	callOrder = nil
	perCell2 := func(c Cell) bool {
		callOrder = append(callOrder, c)
		return false // first cell reject
	}
	valid = ValidateFootprint(anchor, fx, fz, perCell2, nil)
	if valid || len(callOrder) != 1 {
		t.Fatalf("immediate return on first rejecting cell [04 §8.2] C25, calls=%d", len(callOrder))
	}
}

func TestValidatorAggregateAfterScan(t *testing.T) { // [04 §8.2] C25 aggregate after scan
	anchor := Cell{0, 0}
	fx, fz := int16(1), int16(1)
	perCalls := 0
	perCell := func(c Cell) bool {
		perCalls++
		return true
	}
	aggCalls := 0
	aggregate := func() bool {
		aggCalls++
		return false // reject at aggregate gate
	}
	valid := ValidateFootprint(anchor, fx, fz, perCell, aggregate)
	if valid {
		t.Fatalf("aggregate false should reject")
	}
	if perCalls != 1 {
		t.Fatalf("perCell should be called for all cells before aggregate [04 §8.2] C25, perCalls=%d", perCalls)
	}
	if aggCalls != 1 {
		t.Fatalf("aggregate should be called once after scan [04 §8.2] C25")
	}
	// If perCell already rejected, aggregate must NOT be called
	perCalls = 0
	aggCalls = 0
	perCellFail := func(c Cell) bool {
		perCalls++
		return false
	}
	valid = ValidateFootprint(anchor, fx, fz, perCellFail, aggregate)
	if valid {
		t.Fatalf("perCell reject should stay rejected")
	}
	if perCalls != 1 || aggCalls != 0 {
		t.Fatalf("aggregate must not run if perCell already rejected [04 §8.2] C25, per=%d agg=%d", perCalls, aggCalls)
	}
}

func TestRevisionBumpOnDynamicBlock(t *testing.T) { // [04 §7.4] C18
	grid := NewOccupancyGrid()
	if grid.Revision() != 0 {
		t.Fatalf("initial rev 0")
	}
	grid.Bump()
	if grid.Revision() != 1 {
		t.Fatalf("Bump should increment [04 §7.4] C18")
	}
	// Block adds and bumps
	grid2 := NewOccupancyGrid()
	rev0 := grid2.Revision()
	grid2.Block(Cell{2, 2}, 1, 1, 1)
	if grid2.Revision() != rev0+1 {
		t.Fatalf("Block should bump revision [04 §7.4] C18")
	}
	grid2.Unblock(Cell{2, 2}, 1, 1, 1)
	if grid2.Revision() != rev0+2 {
		t.Fatalf("Unblock should bump revision [04 §7.4] C18, got %d want %d", grid2.Revision(), rev0+2)
	}
	// Stamp/Clear also bump via same path
	grid3 := NewOccupancyGrid()
	grid3.Stamp(Cell{0, 0}, 1, 1, 1)
	r1 := grid3.Revision()
	grid3.Clear(Cell{0, 0}, 1, 1, 1)
	if grid3.Revision() != r1+1 {
		t.Fatalf("Clear should bump [04 §7.4] C18")
	}
	// Failed stamp (occupied) must not bump
	grid4 := NewOccupancyGrid()
	grid4.Stamp(Cell{0, 0}, 1, 1, 1)
	rBefore := grid4.Revision()
	ok := grid4.Stamp(Cell{0, 0}, 1, 1, 2) // occupied by 1
	if ok {
		t.Fatalf("stamp should fail on occupied")
	}
	if grid4.Revision() != rBefore {
		t.Fatalf("failed stamp must not bump revision")
	}
}

func TestSweepOrderIsDeterministic(t *testing.T) { // [I1][04 §8.2] C22
	grid := NewOccupancyGrid()
	// Create states out of order
	s3 := &CollisionState{ID: 3, X: 0, Z: 0, VX: 0, VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{3, 0}, CachedMode: 1, OldAnchor: Cell{3, 0}, MaxVelocity: 65536}
	s1 := &CollisionState{ID: 1, X: 0, Z: 0, VX: 0, VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{1, 0}, CachedMode: 1, OldAnchor: Cell{1, 0}, MaxVelocity: 65536}
	s2 := &CollisionState{ID: 2, X: 0, Z: 0, VX: 0, VZ: 0, FootPrintX: 1, FootPrintZ: 1, Mode: 1, CachedAnchor: Cell{2, 0}, CachedMode: 1, OldAnchor: Cell{2, 0}, MaxVelocity: 65536}
	states := []*CollisionState{s3, s1, s2}
	// perCell that records order of commit attempts
	order := []int{}
	perCellFactory := func(s *CollisionState) func(Cell) bool {
		return func(c Cell) bool {
			order = append(order, s.ID)
			return true
		}
	}
	// But ValidateFootprint for each state will call perCell once? For 1x1 footprint each valid commit calls perCell once.
	// To observe order, we need to ensure perCell called. Use CommitSweep which will sort.
	// Need grid to have stamps? Not needed.
	CommitSweep(states, grid, perCellFactory, nil)
	// Expect order 1,2,3 regardless of input order
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("sweep order must be slot ascending deterministic [I1][04 §8.2] C22, got %v", order)
	}
}

func TestOccupancySortedDeterministic(t *testing.T) { // [I1]
	grid := NewOccupancyGrid()
	grid.Stamp(Cell{5, 3}, 1, 1, 3)
	grid.Stamp(Cell{1, 9}, 1, 1, 1)
	grid.Stamp(Cell{1, 2}, 1, 1, 2)
	sorted := grid.OccupiedCellsSorted()
	if len(sorted) != 3 {
		t.Fatalf("sorted count")
	}
	// X asc then Z asc: (1,2), (1,9), (5,3)
	if sorted[0] != (Cell{1, 2}) || sorted[1] != (Cell{1, 9}) || sorted[2] != (Cell{5, 3}) {
		t.Fatalf("deterministic sorted order [I1] got %v", sorted)
	}
}
