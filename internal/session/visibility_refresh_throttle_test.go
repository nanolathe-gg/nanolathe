package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// Regression for the session's bypass of the visibility refresh throttle
// [03 R-VIS-01 §2] "Terrain-ray branch": the refresh condition is
//
//	storedTileX != tileX or storedTileZ != tileZ or abs(storedByte - emitter) > 5
//
// compared against the LAST PUBLISHED byte. A session-side cache keyed on cell
// and radius alone can never satisfy the third disjunct, so an observer that
// climbs without leaving its coverage tile never re-rasterizes.

// terrainRayFixture is visibilityFixture switched to mode bit 2 with one
// authored two-step spoke. The raster expands the line into four quadrants, so
// the +Z spoke walks (cx, cz+1) then (cx, cz+2).
//
// numtables is 2 so a sight distance in [32,64) selects group 1, which walks
// TABLE index 0 [03 R-COMP-02 §1].
func terrainRayFixture(t *testing.T) *Session {
	t.Helper()
	s := visibilityFixture(t, true)
	s.Vis.SetMode(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay)
	line := []int32{2, 0, 1, 0, 2}
	s.Vis.SetRayTables(&content.LOSTables{
		NumTables: 2,
		Tables: []content.LOSTable{
			{TableNum: 1, NumLines: 1, Lines: [][]int32{line}},
			{TableNum: 2, NumLines: 1, Lines: [][]int32{line}},
		},
	})
	// Authored LOS words, ours: a flat field with one ridge at (10,11) whose
	// MINIMUM byte is 36 and one far tile at (10,12) whose MAXIMUM byte is 50.
	// The horizon rule then admits the far tile iff 2*(36 - E) < 50 - E, that
	// is iff the emitter E exceeds 22 — between the two heights this test
	// drives the observer through.
	w, h := s.Vis.GridDimensions()
	for vz := int32(0); vz < h; vz++ {
		for vx := int32(0); vx < w; vx++ {
			s.World.SetLOSHeightWord(vx, vz, 0, 0)
		}
	}
	s.World.SetLOSHeightWord(10, 11, 0, 36)
	s.World.SetLOSHeightWord(10, 12, 50, 0)
	return s
}

// rayObserver places the climbing observer of the regression at X=320, Z=340
// with a sight distance in group 1. Its coverage tile is (10,10) for every
// emitter this test uses, which is exactly why the session cache hid the bug.
func rayObserver(t *testing.T, s *Session) *units.Unit {
	t.Helper()
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	def.SightDistance = 40
	def.ModelTop = 0
	return placeObserver(t, s, def, 0, 320, 20, 340)
}

func setHeight(u *units.Unit, y int32) { u.Y = worldUnits(y) }

// TestRayRefreshIgnoresHeightDeltaOfFive locks the throttle's strictness: five
// is not "more than five", so an emitter that moves by exactly five inside its
// coverage tile publishes nothing [03 R-VIS-01 §2].
func TestRayRefreshIgnoresHeightDeltaOfFive(t *testing.T) {
	s := terrainRayFixture(t)
	u := rayObserver(t, s)

	publishVisibilityForAll(s)
	if cx, cz := observerCell(s, u, heightByteAt(u, seaLevelFor(s))); cx != 10 || cz != 10 {
		t.Fatalf("observer cell = (%d,%d), want (10,10)", cx, cz)
	}
	if coveredCell(s, 0, 10, 12) {
		t.Fatal("the occluded far tile is covered at the starting emitter")
	}

	setHeight(u, 25) // emitter 20 -> 25, a delta of exactly five
	stampPlayerSlice(s, 0)
	if coveredCell(s, 0, 10, 12) {
		t.Fatal("a five-unit climb refreshed; the throttle's compare is strictly greater [03 R-VIS-01 §2]")
	}
}

// TestRayRefreshFollowsHeightDeltaOfSix is the finding itself: the coverage
// tile never changes, so the session's own cell/radius key skipped the refresh
// and the far tile stayed dark forever.
func TestRayRefreshFollowsHeightDeltaOfSix(t *testing.T) {
	s := terrainRayFixture(t)
	u := rayObserver(t, s)
	publishVisibilityForAll(s)

	setHeight(u, 26) // emitter 20 -> 26
	if cx, cz := observerCell(s, u, heightByteAt(u, seaLevelFor(s))); cx != 10 || cz != 10 {
		t.Fatalf("observer cell moved to (%d,%d); the case requires an unchanged cell", cx, cz)
	}
	stampPlayerSlice(s, 0)
	if !coveredCell(s, 0, 10, 12) {
		t.Fatal("a six-unit climb inside one coverage tile did not refresh [03 R-VIS-01 §2]")
	}
}

// TestRayRefreshAccumulatesOneUnitClimbs is why comparing consecutive sweeps is
// not a fix: six one-unit steps each look like a delta of one, but the throttle
// compares against the LAST PUBLISHED byte, so the sixth must publish.
func TestRayRefreshAccumulatesOneUnitClimbs(t *testing.T) {
	s := terrainRayFixture(t)
	u := rayObserver(t, s)
	publishVisibilityForAll(s)

	for y := int32(21); y <= 25; y++ {
		setHeight(u, y)
		stampPlayerSlice(s, 0)
		if coveredCell(s, 0, 10, 12) {
			t.Fatalf("published at emitter %d, only five above the last publication", y)
		}
	}
	setHeight(u, 26)
	stampPlayerSlice(s, 0)
	if !coveredCell(s, 0, 10, 12) {
		t.Fatal("six accumulated one-unit climbs never republished [03 R-VIS-01 §2]")
	}
}

// TestRayRefreshStillFollowsHorizontalMoves guards the other direction: letting
// the service own the throttle must not cost the cell-change refresh.
func TestRayRefreshStillFollowsHorizontalMoves(t *testing.T) {
	s := terrainRayFixture(t)
	u := rayObserver(t, s)
	setHeight(u, 26)
	publishVisibilityForAll(s)
	if !coveredCell(s, 0, 10, 12) {
		t.Fatal("fixture precondition: the far tile should be covered at emitter 26")
	}

	u.X = worldUnits(360) // coverage tile 10 -> 11
	stampPlayerSlice(s, 0)
	if coveredCell(s, 0, 10, 12) {
		t.Fatal("the old footprint was not removed after a horizontal move")
	}
	if !coveredCell(s, 0, 11, 12) {
		t.Fatal("the new footprint was not published after a horizontal move")
	}
}
