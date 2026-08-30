package visibility

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Fixtures for the authored-asset raster (REVIEW.md WU-R2-7).

func flatTerrain(cells int32, height uint8) *world.Terrain {
	plot := make([]world.PlotCell, cells*cells)
	for i := range plot {
		plot[i].SetHeight(height)
	}
	return &world.Terrain{CellW: cells, CellH: cells, Plot: plot}
}

// coveredTiles counts the tiles carrying a player's history bit.
func coveredTiles(s *Service, p PlayerID) int {
	n := 0
	bit := cellBit(p)
	for _, v := range s.wordMask {
		if v&bit != 0 {
			n++
		}
	}
	return n
}

// TestSpriteRadiusIsIndexPlusFive is the F-5 regression guard. The -5 in
// floor(radius/32)-5 selects a SHAPE; shape k covers radius k+5. Reading the
// index as the radius shrank every unit's sight by five tiles [03 §3.2].
func TestSpriteRadiusIsIndexPlusFive(t *testing.T) {
	terrain := &world.Terrain{CellW: 256, CellH: 256}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)

	// armfav's shipped sightdistance is 310: floor(310/32) = 9, index 4,
	// which is the 19x19 frame — a radius of nine tiles, not four.
	if got := s.spriteShapeIndex(310); got != 4 {
		t.Fatalf("shape index for sight 310 = %d want 4", got)
	}
	shape := s.shapes.Shape(4)
	if shape.W != 19 || shape.H != 19 {
		t.Fatalf("shape 4 is %dx%d, want 19x19 (radius index+5)", shape.W, shape.H)
	}
	s.Publish(0, 60, 60, 0, 310)
	if got := coveredTiles(s, 0); got != 19*19 {
		t.Fatalf("sight 310 covered %d tiles, want %d; the index is being used "+
			"as a radius", got, 19*19)
	}
}

// TestSpriteMaskSkipsTransparentCells locks C3: only opaque mask bytes touch
// the grids.
func TestSpriteMaskSkipsTransparentCells(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64}
	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	// One 3x3 shape with a single opaque cell at its centre.
	sh := &content.SightShapes{Shapes: []content.SightShape{{
		W: 3, H: 3, AnchorX: 1, AnchorY: 1,
		Opaque: []bool{false, false, false, false, true, false, false, false, false},
	}}}
	s.SetShapes(sh)
	s.Publish(0, 10, 10, 0, 0)
	if got := coveredTiles(s, 0); got != 1 {
		t.Fatalf("covered %d tiles, want 1 — transparent cells must be skipped", got)
	}
}

// TestSpriteMaskClipsAtEdges locks C3's unsigned clipping: a shape hanging off
// the west and north edges must not wrap into the far border.
func TestSpriteMaskClipsAtEdges(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64} // 32x32 tiles
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.Publish(0, 0, 0, 0, 160) // index 0, an 11x11 frame anchored at its centre

	// Only the quarter inside the map is covered: 6x6.
	if got := coveredTiles(s, 0); got != 36 {
		t.Fatalf("covered %d tiles at the corner, want 36", got)
	}
	// Nothing on the opposite edges.
	for x := int32(0); x < s.W; x++ {
		if s.wordMask[(s.H-1)*s.W+x] != 0 {
			t.Fatalf("coverage wrapped to the south edge at x=%d", x)
		}
	}
}

// TestRayUsesAuthoredSpokes is the F-12 regression guard: the spokes come from
// the LOS.TDF table, not from a hard-coded direction set.
func TestRayUsesAuthoredSpokes(t *testing.T) {
	terrain := flatTerrain(64, 10)
	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s.SetRayTables(&content.LOSTables{
		// Two declared tables: radius 32 is group 1, which walks TABLE 0
		// under the one-based accessor skew [03 R-COMP-02 §1].
		NumTables: 2,
		Tables: []content.LOSTable{{
			TableNum: 0, NumLines: 1,
			// One line: two steps due north.
			Lines: [][]int32{{2, 0, 1, 0, 2}},
		}, {
			TableNum: 1, NumLines: 1,
			Lines: [][]int32{{3, 0, 1, 0, 2, 0, 3}},
		}},
	})
	// The observer stands above the ground it is looking across. An observer at
	// exactly terrain height ties with flat ground at every step beyond the
	// first and sees nothing — correct under C5's strict rule, but degenerate.
	s.Publish(0, 10, 10, 20, 32)

	// One authored line becomes four spokes under the quadrant rotation, so
	// the origin plus two steps in each of the four cardinal directions.
	if got := coveredTiles(s, 0); got != 1+4*2 {
		t.Fatalf("covered %d tiles, want %d (origin + four rotations of a "+
			"two-step line)", got, 1+4*2)
	}
	// With no table bound nothing publishes — no fallback circle.
	s2 := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s2.Publish(0, 10, 10, 20, 32)
	if got := coveredTiles(s2, 0); got != 0 {
		t.Fatalf("terrain-ray with no authored table covered %d tiles; it must "+
			"not fall back to a synthesized shape", got)
	}
}

// TestRayStrictTieNeverAdmits locks C5: admission is strictly greater, so an
// exact tie fails. A ridge of equal height beyond the first step must occlude.
//
// The words are installed directly rather than authored as heights: the LOS
// height table is built by a height-dependent scatter [R-P0-18-B], and this
// test is about the horizon comparison, not that builder.
func TestRayStrictTieNeverAdmits(t *testing.T) {
	// A 3-step spoke due north over terrain that rises to a plateau: the first
	// step establishes a horizon that the equal-height steps behind it tie
	// with, and a tie never admits.
	terrain := flatTerrain(64, 0)
	terrain.SetLOSHeightWord(10, 11, 20, 20) // first step: a wall
	terrain.SetLOSHeightWord(10, 12, 20, 20) // exactly as tall — ties, hidden
	terrain.SetLOSHeightWord(10, 13, 20, 20)

	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s.SetRayTables(&content.LOSTables{
		// Radius 32 is group 1, which walks TABLE 0 [03 R-COMP-02 §1]; a
		// second declared table makes group 1 reachable.
		NumTables: 2,
		Tables: []content.LOSTable{{
			TableNum: 0, NumLines: 1,
			Lines: [][]int32{{3, 0, 1, 0, 2, 0, 3}},
		}, {
			TableNum: 1, NumLines: 1, Lines: [][]int32{{1, 0, 1}},
		}},
	})
	s.Publish(0, 10, 10, 0, 32)

	idx := func(x, z int32) int { return int(z*s.W + x) }
	bit := cellBit(0)
	if s.wordMask[idx(10, 11)]&bit == 0 {
		t.Fatal("the first step above the observer must be admitted")
	}
	if s.wordMask[idx(10, 12)]&bit != 0 {
		t.Fatal("an exact slope tie must not admit [C5]")
	}
	if s.wordMask[idx(10, 13)]&bit != 0 {
		t.Fatal("a step behind the horizon must not admit")
	}
}

// TestRayExactEqualityTie locks the equality edge of C5's admission compare:
// `retainedNum × stepDist == candidateDiff × retainedDen` is NOT admitted.
// Horizon 20 at distance 1 meets a difference of 40 at distance 2 — both
// sides are exactly 40 — and a step beyond it (difference 41) admits.
func TestRayExactEqualityTie(t *testing.T) {
	terrain := flatTerrain(64, 0)
	terrain.SetLOSHeightWord(10, 11, 20, 20) // horizon (20 @ distance 1)
	terrain.SetLOSHeightWord(10, 12, 40, 40) // 20*2 == 40*1 — exact equality
	terrain.SetLOSHeightWord(10, 13, 61, 61) // 20*3 < 61*1 — strictly greater
	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s.SetRayTables(&content.LOSTables{
		// Radius 32 is group 1, which walks TABLE 0 [03 R-COMP-02 §1]; a
		// second declared table makes group 1 reachable.
		NumTables: 2,
		Tables: []content.LOSTable{{
			TableNum: 0, NumLines: 1,
			Lines: [][]int32{{3, 0, 1, 0, 2, 0, 3}},
		}, {
			TableNum: 1, NumLines: 1, Lines: [][]int32{{1, 0, 1}},
		}},
	})
	s.Publish(0, 10, 10, 0, 32)

	idx := func(x, z int32) int { return int(z*s.W + x) }
	bit := cellBit(0)
	if s.wordMask[idx(10, 11)]&bit == 0 {
		t.Fatal("first step must be admitted")
	}
	if s.wordMask[idx(10, 12)]&bit != 0 {
		t.Fatal("exact equality (20·2 == 40·1) must not admit [C5]")
	}
	if s.wordMask[idx(10, 13)]&bit == 0 {
		t.Fatal("strictly-greater difference past the tie must admit")
	}
}

// TestRayHighByteGatesHorizonUpdate locks the second half of C5: the high byte
// is tested separately and decides whether the retained pair advances. With a
// single-byte terrain word that test is algebraically dead.
func TestRayHighByteGatesHorizonUpdate(t *testing.T) {
	terrain := flatTerrain(64, 0)
	// A tile whose two bytes disagree: low 0 lets sight through, high 40 raises
	// the horizon behind it.
	terrain.SetLOSHeightWord(10, 11, 0, 40)

	lo, hi := terrain.LOSHeightWord(10, 11)
	if lo != 0 || hi != 40 {
		t.Fatalf("installed word = (%d,%d), want (0,40)", lo, hi)
	}

	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s.SetRayTables(&content.LOSTables{
		// Radius 32 is group 1, which walks TABLE 0 [03 R-COMP-02 §1]; a
		// second declared table makes group 1 reachable.
		NumTables: 2,
		Tables: []content.LOSTable{{
			TableNum: 0, NumLines: 1,
			Lines: [][]int32{{2, 0, 1, 0, 2}},
		}, {
			TableNum: 1, NumLines: 1, Lines: [][]int32{{1, 0, 1}},
		}},
	})
	s.Publish(0, 10, 10, 0, 32)

	bit := cellBit(0)
	if s.wordMask[int(11*s.W+10)]&bit == 0 {
		t.Fatal("the mixed tile's LOW byte should admit it")
	}
	if s.wordMask[int(12*s.W+10)]&bit != 0 {
		t.Fatal("the mixed tile's HIGH byte should have raised the horizon and " +
			"occluded the flat tile behind it")
	}
}

// TestRefreshThrottle locks C6: nothing recomputes unless the tile moved or the
// height byte moved by more than five.
func TestRefreshThrottle(t *testing.T) {
	terrain := flatTerrain(128, 0)
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetRayTables(&content.LOSTables{
		NumTables: 1,
		Tables:    []content.LOSTable{{TableNum: 1, NumLines: 1, Lines: [][]int32{{1, 0, 1}}}},
	})
	s.SetMode(ModeHistoryEnabled | ModeCurrentEnabled | ModeTerrainRay)

	ob := Observer{Owner: 0, CX: 20, CZ: 20, HeightByte: 100, Radius: 32}
	s.Refresh(1, ob)
	first := s.byteGrids[0][int(20*s.W+20)]
	if first == 0 {
		t.Fatal("first refresh published nothing")
	}
	// Same tile, height within five: throttled, so the refcount does not move.
	ob.HeightByte = 104
	s.Refresh(1, ob)
	if got := s.byteGrids[0][int(20*s.W+20)]; got != first {
		t.Fatalf("refcount moved to %d on a throttled refresh (was %d)", got, first)
	}
	// Height moved by more than five: republishes, so the old footprint is
	// removed and the new one added — the refcount lands back where it was.
	ob.HeightByte = 120
	s.Refresh(1, ob)
	if got := s.byteGrids[0][int(20*s.W+20)]; got != first {
		t.Fatalf("refcount %d after a real refresh; the old footprint was not "+
			"removed before the new one published", got)
	}
	// Moving off the tile leaves the old cell's refcount at zero.
	ob.CX = 40
	s.Refresh(1, ob)
	if got := s.byteGrids[0][int(20*s.W+20)]; got != 0 {
		t.Fatalf("old cell refcount %d after the observer moved away", got)
	}
}

// TestRayTableSkew locks the one-based table accessor of [03 R-COMP-02 §1]:
// group g = clamp(floor(radius/32), 0, numtables-1) walks TABLE g-1, so the
// top declared table is unreachable and group 0 walks no line at all (the
// retail record there is unknown; only the origin is admitted).
func TestRayTableSkew(t *testing.T) {
	terrain := flatTerrain(64, 10)
	tables := &content.LOSTables{
		NumTables: 3,
		Tables: []content.LOSTable{
			{TableNum: 0, NumLines: 1, Lines: [][]int32{{1, 0, 1}}},
			{TableNum: 1, NumLines: 1, Lines: [][]int32{{2, 0, 1, 0, 2}}},
			{TableNum: 2, NumLines: 1, Lines: [][]int32{{5, 0, 1, 0, 2, 0, 3, 0, 4, 0, 5}}},
		},
	}
	cases := []struct {
		radius int32
		want   int
	}{
		{16, 1},        // group 0: origin only
		{32, 1 + 4*1},  // group 1 -> TABLE 0
		{64, 1 + 4*2},  // group 2 -> TABLE 1
		{200, 1 + 4*2}, // clamped to group 2 -> TABLE 1; TABLE 2 unreachable
	}
	for _, c := range cases {
		s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
		s.SetRayTables(tables)
		s.Publish(0, 10, 10, 20, c.radius)
		if got := coveredTiles(s, 0); got != c.want {
			t.Fatalf("radius %d covered %d tiles, want %d", c.radius, got, c.want)
		}
	}
}
