package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// worldUnits converts whole world units to the 16.16 fixed representation.
func worldUnits(n int32) numeric.Fixed { return numeric.Fixed(int64(n) << 16) }

// singleCellSightFixture is visibilityFixture with the authored sight table
// replaced by ONE opaque cell anchored at its own origin. The raster then
// covers exactly the observer cell it is handed, so a projection error shows
// up as a whole-cell shift instead of being smeared across a disc.
func singleCellSightFixture(t *testing.T) *Session {
	t.Helper()
	s := visibilityFixture(t, true)
	shapes := &content.SightShapes{}
	shapes.Shapes = []content.SightShape{{W: 1, H: 1, AnchorX: 0, AnchorY: 0, Opaque: []bool{true}}}
	s.Vis.SetShapes(shapes)
	return s
}

// placeObserver drops one live unit at an exact world position. Create's own
// placement is not the subject here; the projection of a known position is.
func placeObserver(t *testing.T, s *Session, def *content.UnitDef, owner uint8, x, y, z int32) *units.Unit {
	t.Helper()
	h, err := s.Units.Create(def, owner, worldUnits(x), worldUnits(y), worldUnits(z))
	if err != nil {
		t.Fatalf("create observer: %v", err)
	}
	u := s.Units.Unit(h)
	if u == nil {
		t.Fatal("observer record missing after create")
	}
	u.X, u.Y, u.Z = worldUnits(x), worldUnits(y), worldUnits(z)
	return u
}

func coveredCell(s *Session, owner visibility.PlayerID, cx, cz int32) bool {
	w, h := s.Vis.GridDimensions()
	if uint32(cx) >= uint32(w) || uint32(cz) >= uint32(h) {
		return false
	}
	g := s.Vis.ByteGrid(owner)
	return g != nil && g[cz*w+cx] != 0
}

// TestCircularSightUsesSpriteMaskProjection locks [03 R-VIS-01 §2]
// "Sprite-mask branch": with mode bit 2 clear the observer cell is
// floorDiv(worldX, 2^21) and floorDiv(worldZ, 2^21) − floorDiv(worldY_high, 64)
// — two independent floors, and NO model top. The regression is the review's
// case: observer X=320 Y=64 Z=384 with a model top of 64 covers row 11. Sharing
// the ray branch's (worldZ_high − emitter/2) shear puts it on row 10, which is
// authoritative sight moved by a whole cell, not a fog artifact.
func TestCircularSightUsesSpriteMaskProjection(t *testing.T) {
	s := singleCellSightFixture(t)
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	def.ModelTop = 64
	u := placeObserver(t, s, def, 0, 320, 64, 384)

	cx, cz := observerCell(s, u, heightByteAt(u, seaLevelFor(s)))
	if cx != 10 || cz != 11 {
		t.Fatalf("Circular observer cell = (%d,%d), want (10,11) [03 R-VIS-01 §2]", cx, cz)
	}

	publishVisibilityForAll(s)
	if !coveredCell(s, 0, 10, 11) {
		t.Fatal("row 11 not covered: the Circular raster did not use the sprite-mask projection")
	}
	if coveredCell(s, 0, 10, 10) {
		t.Fatal("row 10 covered: the terrain-ray beam shear leaked into Circular mode")
	}
}

// TestCircularSightIgnoresModelTop locks the sprite branch's second divergence
// from the ray branch [03 R-VIS-01 §2]: the model top does not enter the
// Circular projection at all, so two definitions differing only in model top
// project to the same cell.
func TestCircularSightIgnoresModelTop(t *testing.T) {
	s := singleCellSightFixture(t)
	def := s.Catalog.Units[content.CanonicalKey("armcom")]

	def.ModelTop = 0
	flat := placeObserver(t, s, def, 0, 320, 64, 384)
	flatX, flatZ := observerCell(s, flat, heightByteAt(flat, seaLevelFor(s)))

	def.ModelTop = 200
	tall := placeObserver(t, s, def, 0, 320, 64, 384)
	tallEmitter := heightByteAt(tall, seaLevelFor(s))
	tallX, tallZ := observerCell(s, tall, tallEmitter)

	if flatX != tallX || flatZ != tallZ {
		t.Fatalf("model top changed the Circular cell: (%d,%d) vs (%d,%d)", flatX, flatZ, tallX, tallZ)
	}
	// The ray branch is the converse: there the model top is part of the
	// emitter and does move the cell. If the two agree, the branches were
	// merged again.
	if _, rayZ := observerTile(tall, tallEmitter); rayZ == tallZ {
		t.Fatalf("terrain-ray shear stopped reacting to the model top (both rows %d)", rayZ)
	}
}

// TestCircularSightTakesTwoIndependentFloors locks the arithmetic the review
// called out as separately wrong: floorDiv(Z,32) − floorDiv(Y,64) is NOT the
// floor of the combined expression (Z − Y/2)/32. These positions are chosen so
// the two disagree, including at a negative height word.
func TestCircularSightTakesTwoIndependentFloors(t *testing.T) {
	s := singleCellSightFixture(t)
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	def.ModelTop = 0

	cases := []struct {
		name        string
		x, y, z     int32
		wantX       int32
		wantZ       int32
		combinedZ   int32 // what one combined floor would have produced
		differsFrom bool
	}{
		// Z=64 (tile 2), Y=32 (floorDiv 64 -> 0): independent floors keep row 2.
		// One combined floor of (64 - 16)/32 = 1 would drop a row.
		{"sub-64 height keeps the row", 320, 32, 64, 10, 2, 1, true},
		// Z=95 (tile 2), Y=64 (floorDiv 64 -> 1): independent floors give 1.
		// Combined (95 - 32)/32 = 1 agrees here; the case pins the boundary.
		{"exact 64 boundary", 320, 64, 95, 10, 1, 1, false},
		// Z=128 (tile 4), Y=127 (floorDiv 64 -> 1): independent give 3;
		// combined (128 - 63)/32 = 2.
		{"just below the next 64 step", 320, 127, 128, 10, 3, 2, true},
		// Y below sea level is raised to (SeaLevel+1) first, so a negative
		// authored height cannot subtract a row.
		{"sea-level raise precedes the floor", 320, -400, 128, 10, 4, 4, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := placeObserver(t, s, def, 0, tc.x, tc.y, tc.z)
			cx, cz := spriteObserverTile(u, seaLevelFor(s))
			if cx != tc.wantX || cz != tc.wantZ {
				t.Fatalf("cell = (%d,%d), want (%d,%d) [03 R-VIS-01 §2]", cx, cz, tc.wantX, tc.wantZ)
			}
			if tc.differsFrom && cz == tc.combinedZ {
				t.Fatalf("independent floors collapsed onto the combined floor %d", tc.combinedZ)
			}
		})
	}
}

// TestTerrainRayGeometryUnchanged locks the other half of the fix: with mode
// bit 2 SET the observer cell is still the single-floor beam shear including
// the model top [03 R-VIS-01 §2] "Terrain-ray branch".
func TestTerrainRayGeometryUnchanged(t *testing.T) {
	s := singleCellSightFixture(t)
	s.Vis.SetMode(s.Vis.Mode() | visibility.ModeTerrainRay)
	def := s.Catalog.Units[content.CanonicalKey("armcom")]
	def.ModelTop = 64
	u := placeObserver(t, s, def, 0, 320, 64, 384)

	emitter := heightByteAt(u, seaLevelFor(s))
	if emitter != 128 {
		t.Fatalf("emitter = %d, want 128 (raised Y plus model top)", emitter)
	}
	cx, cz := observerCell(s, u, emitter)
	if cx != 10 || cz != 10 {
		t.Fatalf("terrain-ray observer cell = (%d,%d), want (10,10)", cx, cz)
	}
}
