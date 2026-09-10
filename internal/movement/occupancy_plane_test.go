package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The occupancy words of the plot cell are the store retail keeps mobile
// occupancy in [03 §2.2]: the ground word for mode-1 movers and the building
// class, the air word for mode-2 movers, nothing for modes 0 and 3
// [04 R-COLL-01 §4]. These tests assert the relationship between a mover's
// committed rectangle and those two words — they do not census the map.

// plotWords reports one cell's (ground, air) occupancy words.
func plotWords(t *testing.T, ter *world.Terrain, c Cell) (int16, int16) {
	t.Helper()
	cell := ter.PlotAt(c.X, c.Z)
	if cell == nil {
		t.Fatalf("cell %v is off the map", c)
	}
	return cell.OccupantA(), cell.OccupantB()
}

// assertRectangle checks that every cell of the rectangle carries want in the
// named plane and zero in the other, and that the ring of cells around the
// rectangle carries nothing of this identity at all.
func assertRectangle(t *testing.T, ter *world.Terrain, anchor Cell, fx, fz int32, plane Plane, want int16) {
	t.Helper()
	for dz := int32(-1); dz <= fz; dz++ {
		for dx := int32(-1); dx <= fx; dx++ {
			c := Cell{X: anchor.X + dx, Z: anchor.Z + dz}
			if ter.PlotAt(c.X, c.Z) == nil {
				continue
			}
			ground, air := plotWords(t, ter, c)
			inside := dx >= 0 && dx < fx && dz >= 0 && dz < fz
			mine, other := ground, air
			if plane == PlaneAir {
				mine, other = air, ground
			}
			switch {
			case inside && mine != want:
				t.Fatalf("cell %v of the rectangle holds %d in plane %d, want %d [04 R-COLL-01 §4]", c, mine, plane, want)
			case !inside && mine == want:
				t.Fatalf("cell %v outside the rectangle holds %d in plane %d [04 R-COLL-01 §4]", c, mine, plane)
			}
			if other == want {
				t.Fatalf("cell %v holds %d in the other plane; a mover writes one plane only [04 R-COLL-01 §4]", c, want)
			}
		}
	}
}

// TestGroundMoverStampsPlotGroundWordAcrossACommit locks the success branch's
// step (1) clear and step (4) stamp against the plot words [04 R-COLL-01 §1]:
// after a cross-cell commit the mover's identity is in the ground word of every
// cell of its new rectangle, in no cell outside it, and the cells of the old
// rectangle it left are zero again.
func TestGroundMoverStampsPlotGroundWordAcrossACommit(t *testing.T) {
	ter := syntheticFlat(24, 24)
	grid := NewOccupancyGrid()
	grid.AttachPlot(ter)

	const id = 7
	start := Cell{X: 6, Z: 6}
	// A 2x1 mover, so the rectangle is more than the anchor cell.
	x, z := world.PlacementCenter(start.X, start.Z, 2, 1)
	s := &CollisionState{
		ID: id, X: int32(x), Z: int32(z),
		FootPrintX: 2, FootPrintZ: 1,
		Mode: 1, CachedMode: 1,
		CachedAnchor: start, OldAnchor: start,
		MaxVelocity: 65536,
	}
	if !grid.StampPlane(PlaneGround, start, s.FootPrintX, s.FootPrintZ, id) {
		t.Fatal("the creation stamp did not take the rectangle")
	}
	s.StampedAnchor, s.StampedPlane, s.HasStamp = start, PlaneGround, true
	assertRectangle(t, ter, start, 2, 1, PlaneGround, id)

	// One whole cell east: a cross-cell proposal, so the validator runs and the
	// success branch clears and stamps [04 R-COLL-01 §1].
	s.VX = int32(worldUnitsPerCell)
	fast, blocked := s.CommitOne(grid, s.Mode, func(Cell) bool { return true }, nil)
	if fast || blocked {
		t.Fatalf("expected a validated cross-cell commit, got fast=%v blocked=%v", fast, blocked)
	}
	moved := Cell{X: start.X + 1, Z: start.Z}
	if s.CachedAnchor != moved {
		t.Fatalf("cached pair %v after the commit, want %v", s.CachedAnchor, moved)
	}
	assertRectangle(t, ter, moved, 2, 1, PlaneGround, id)
	if ground, _ := plotWords(t, ter, start); ground != 0 {
		t.Fatalf("the vacated cell %v still holds %d [04 R-COLL-01 §4]", start, ground)
	}
}

// TestSameCellCommitLeavesTheWordsAlone locks the fast path: "No validator, no
// clear, no stamp" [04 R-COLL-01 §1].
func TestSameCellCommitLeavesTheWordsAlone(t *testing.T) {
	ter := syntheticFlat(24, 24)
	grid := NewOccupancyGrid()
	grid.AttachPlot(ter)

	const id = 3
	anchor := Cell{X: 9, Z: 4}
	x, z := world.PlacementCenter(anchor.X, anchor.Z, 1, 1)
	s := &CollisionState{
		ID: id, X: int32(x), Z: int32(z),
		FootPrintX: 1, FootPrintZ: 1,
		Mode: 1, CachedMode: 1,
		CachedAnchor: anchor, OldAnchor: anchor,
		MaxVelocity: 65536,
		VX:          64, // a creep well inside the cell
	}
	grid.StampPlane(PlaneGround, anchor, 1, 1, id)
	s.StampedAnchor, s.StampedPlane, s.HasStamp = anchor, PlaneGround, true
	rev := grid.Revision()
	if fast, _ := s.CommitOne(grid, s.Mode, func(Cell) bool { return true }, nil); !fast {
		t.Fatal("a within-cell proposal must take the fast path [04 R-COLL-01 §1]")
	}
	if grid.Revision() != rev {
		t.Fatalf("the fast path rewrote occupancy: revision %d -> %d", rev, grid.Revision())
	}
	if ground, air := plotWords(t, ter, anchor); ground != id || air != 0 {
		t.Fatalf("cell %v holds (%d,%d) after the fast path, want (%d,0)", anchor, ground, air, id)
	}
}

// TestTakeoffMovesTheStampToTheAirWordAndLandingMovesItBack locks the mode
// rule of [04 R-COLL-01 §4] through the one mover-mode setter: mode 2 writes
// the air word and releases the ground word, mode 1 the reverse, and mode 0
// (attached) writes neither.
func TestTakeoffMovesTheStampToTheAirWordAndLandingMovesItBack(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	ter := sys.Terrain
	coll := handleRow(sys.Collisions, u.Handle)
	if coll == nil {
		t.Fatal("the fixture aircraft has no collision state")
	}
	anchor := coll.CachedAnchor
	id := int16(u.Handle)

	assertRectangle(t, ter, anchor, 1, 1, PlaneGround, id)

	if !sys.SetMoverMode(u, 2) {
		t.Fatal("the mode write to airborne was refused")
	}
	assertRectangle(t, ter, anchor, 1, 1, PlaneAir, id)
	if ground, _ := plotWords(t, ter, anchor); ground != 0 {
		t.Fatalf("an airborne mover still holds the ground word (%d) [04 R-COLL-01 §4]", ground)
	}

	// Mode 0 is the attached/carried mode: it stamps and clears nothing.
	if !sys.SetMoverMode(u, 0) {
		t.Fatal("the mode write to attached was refused")
	}
	if ground, air := plotWords(t, ter, anchor); ground != 0 || air != 0 {
		t.Fatalf("an attached mover holds (%d,%d); modes 0 and 3 write nothing [04 R-COLL-01 §4]", ground, air)
	}

	if !sys.SetMoverMode(u, 1) {
		t.Fatal("the mode write back to grounded was refused")
	}
	assertRectangle(t, ter, anchor, 1, 1, PlaneGround, id)
}

// TestAirborneStampFollowsTheAircraft locks that the air word is written at the
// aircraft's committed cell pair every tick, not at the cell it took off from:
// "every writer stamps at the unit's cached pair" [04 R-COLL-01 §4], and the
// flight commit rewrites that pair [04 R-COLL-01 §1].
func TestAirborneStampFollowsTheAircraft(t *testing.T) {
	sys, w, u := airFixture(t)
	ter := sys.Terrain
	id := int16(u.Handle)
	startCell := Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}

	pushAirOrder(t, u, "VTOL_Move", world.CellToWorld(24), world.CellToWorld(8))
	for tick := uint32(1); tick <= 240; tick++ {
		runMovementTick(sys, tick, w)
	}
	if u.Move.Mode&0x3 != 2 {
		t.Fatalf("mover mode=%d, want the airborne 2 [04 R-AIR-01 §6]", u.Move.Mode)
	}
	here := Cell{X: world.WorldToCell(u.X), Z: world.WorldToCell(u.Z)}
	if here == startCell {
		t.Fatal("the aircraft never left its start cell, so the test proves nothing")
	}
	if _, air := plotWords(t, ter, here); air != id {
		t.Fatalf("cell %v holds %d in the air word, want the flying aircraft %d [04 R-COLL-01 §4]", here, air, id)
	}
	if _, air := plotWords(t, ter, startCell); air == id {
		t.Fatalf("the take-off cell %v still holds the aircraft's air word [04 R-COLL-01 §4]", startCell)
	}
	if ground, _ := plotWords(t, ter, here); ground == id {
		t.Fatalf("cell %v holds the airborne aircraft in the GROUND word [04 R-COLL-01 §2]", here)
	}
}

// TestAirborneRectangleOffTheMapWritesNoCell locks the stamp's bounds test:
// "out of map → the unit is moved to the off-map sector bucket and no cell is
// written" [04 R-COLL-01 §4], with the bounds themselves the validator's steps
// 1–4, which exclude the last column and row [04 R-COLL-01 §2].
func TestAirborneRectangleOffTheMapWritesNoCell(t *testing.T) {
	ter := syntheticFlat(16, 16)
	grid := NewOccupancyGrid()
	grid.AttachPlot(ter)

	const id = 11
	for _, anchor := range []Cell{{X: -1, Z: 4}, {X: 4, Z: -1}, {X: 15, Z: 4}, {X: 4, Z: 15}} {
		if grid.RectOnMap(anchor, 1, 1) {
			t.Fatalf("rectangle at %v is not on the map [04 R-COLL-01 §2]", anchor)
		}
		if grid.StampPlane(PlaneAir, anchor, 1, 1, id) {
			t.Fatalf("an off-map rectangle at %v reported a held stamp", anchor)
		}
		if _, held := grid.OccupantAtPlane(PlaneAir, anchor); held {
			t.Fatalf("an off-map rectangle at %v wrote a cell [04 R-COLL-01 §4]", anchor)
		}
		if cell := ter.PlotAt(anchor.X, anchor.Z); cell != nil && cell.OccupantB() != 0 {
			t.Fatalf("an off-map rectangle at %v wrote the air word of %v", anchor, anchor)
		}
	}
	// The same rectangle one cell inside the boundary is written.
	inside := Cell{X: 14, Z: 4}
	if !grid.StampPlane(PlaneAir, inside, 1, 1, id) {
		t.Fatalf("the in-map rectangle at %v was refused", inside)
	}
	if _, air := plotWords(t, ter, inside); air != id {
		t.Fatalf("cell %v holds %d in the air word, want %d", inside, air, id)
	}
}

// TestOverlappingAirStampsKeepTheirOccupant locks the overlap protocol's
// non-state-3 branch: "the cell keeps occupant", and the stamp does not fail
// [04 R-COLL-01 §4]. Two aircraft over one cell is the ordinary case, since the
// validator never runs for a mode-2 proposal [04 R-COLL-01 §2].
func TestOverlappingAirStampsKeepTheirOccupant(t *testing.T) {
	ter := syntheticFlat(16, 16)
	grid := NewOccupancyGrid()
	grid.AttachPlot(ter)

	first, second := Cell{X: 5, Z: 5}, Cell{X: 6, Z: 5}
	if !grid.StampPlane(PlaneAir, first, 2, 1, 4) {
		t.Fatal("the first air stamp was refused")
	}
	// The second rectangle covers (6,5) — held by 4 — and (7,5), which is free.
	if grid.StampPlane(PlaneAir, second, 2, 1, 9) {
		t.Fatal("an overlapping stamp reported the whole rectangle held")
	}
	if _, air := plotWords(t, ter, Cell{X: 6, Z: 5}); air != 4 {
		t.Fatalf("the overlapped cell holds %d, want the occupant 4 [04 R-COLL-01 §4]", air)
	}
	if _, air := plotWords(t, ter, Cell{X: 7, Z: 5}); air != 9 {
		t.Fatalf("the free cell holds %d, want the intruder 9 [04 R-COLL-01 §4]", air)
	}
}
