package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// tileWorld returns a 16.16 world coordinate that projects to coverage tile t.
// One tile is 32 map pixels and one map pixel is 65,536 world units [03 §2.1].
func tileWorld(tile int64) numeric.Fixed {
	return numeric.Fixed(tile * 32 * 65536)
}

// fixtureShapes builds a shape table with the same geometry as the shipped
// visibility-mask GAF — ten frames of side 11, 13 … 29 anchored at the centre,
// so shape k covers a radius of k+5 tiles [03 §3.2]. Frames are solid, which
// makes coverage easy to reason about in a fixture; the real opacity pattern is
// locked against the install in content.TestSightShapesFromInstall.
func fixtureShapes() *content.SightShapes {
	sh := &content.SightShapes{}
	for k := int32(0); k < 10; k++ {
		side := 11 + 2*k
		s := content.SightShape{
			W: side, H: side,
			AnchorX: side / 2, AnchorY: side / 2,
			Opaque: make([]bool, side*side),
		}
		for i := range s.Opaque {
			s.Opaque[i] = true
		}
		sh.Shapes = append(sh.Shapes, s)
	}
	return sh
}

// newTestService builds a Service with the fixture shape table bound.
func newTestService(t *world.Terrain, mode Mode) *Service {
	s := New(t, mode)
	s.SetShapes(fixtureShapes())
	return s
}

func TestGridDimensions(t *testing.T) {
	// 128x128-cell map yields 64x64 uint16 grid and 8192 bytes [PLAN_05 Tests].
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	w, h := s.GridDimensions()
	if w != 64 || h != 64 {
		t.Fatalf("grid dimensions %d x %d, want 64 x 64", w, h)
	}
	if len(s.wordMask) != 64*64 {
		t.Fatalf("word length %d, want %d", len(s.wordMask), 64*64)
	}
	if len(s.WordMask())*2 != 8192 {
		t.Fatalf("bytes %d, want 8192", len(s.WordMask())*2)
	}
}

func TestAllyNotOred(t *testing.T) {
	// 128x128 cells -> 64x64 coverage tiles. Player 1 publishes; players 0 and 2
	// are uninvolved. Viewer 0 must not see a unit owned by 2 standing inside
	// player 1's footprint [03 §3.2] C9.
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(1, 20, 20, 0, 320)

	inFootprint := Target{Owner: 2, X: tileWorld(20), Z: tileWorld(20)}
	if s.IsVisible(0, inFootprint) {
		t.Fatalf("ally vision OR'd: viewer 0 saw a unit only player 1 covers")
	}
	if !s.IsVisible(1, inFootprint) {
		t.Fatalf("player 1 published this tile and must see into it")
	}
	// Owner bypass needs no coverage at all.
	if !s.IsVisible(1, Target{Owner: 1, X: tileWorld(60), Z: tileWorld(60)}) {
		t.Fatalf("owner bypass failed")
	}
}

// TestProjectionUsesPixelComponents locks C8 step 4: the projection narrows the
// 16.16 coordinate to its map-pixel component BEFORE shifting by five. Shifting
// the raw 16.16 value puts every real unit outside the grid bounds.
func TestProjectionUsesPixelComponents(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(0, 40, 40, 0, 320)

	// A unit forty tiles from the origin, which is an ordinary position.
	target := Target{Owner: 1, X: tileWorld(40), Z: tileWorld(40)}
	if !s.IsVisible(0, target) {
		t.Fatalf("unit inside the published footprint reported not visible; "+
			"projection is probably shifting 16.16 instead of pixels (u=%d v=%d)",
			pixel(target.X)>>5, pixel(target.Z)>>5)
	}
	// Far outside the footprint it is not visible.
	if s.IsVisible(0, Target{Owner: 1, X: tileWorld(5), Z: tileWorld(5)}) {
		t.Fatalf("unit well outside the footprint reported visible")
	}
}

// TestHullSamplesAccumulate locks C8 step 5. Coverage exists on exactly one
// tile, the one reached only by the third sample — which carries the east
// offset forward. An implementation that offsets each sample independently
// from the centre never projects onto that tile.
func TestHullSamplesAccumulate(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	// Set one tile by hand so the fixture does not depend on the raster shape.
	s.setWordBit(int(2*s.W+2), 0)
	s.incByteGrid(int(2*s.W+2), 0)

	const px = 65536 // one map pixel in world units
	target := Target{
		Owner:   1,
		X:       0, // centre projects to tile (0,0)
		Z:       0,
		XExtent: numeric.Fixed(64 * px), // east  -> tile (2,0)
		ZExtent: numeric.Fixed(64 * px), // north -> tile (2,2) only if X is carried
	}
	if !s.IsVisible(0, target) {
		t.Fatalf("north sample did not carry the east offset: the four hull " +
			"samples must accumulate, not offset independently from the centre")
	}
	// With no Z extent the same target reaches only tiles (0,0) and (2,0).
	flat := target
	flat.ZExtent = 0
	if s.IsVisible(0, flat) {
		t.Fatalf("target with no Z extent reached the (2,2) tile")
	}
}

func TestPredicateOrder(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128, SeaLevel: 20}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(0, 10, 10, 0, 320)
	at := func(o PlayerID) Target {
		return Target{Owner: o, X: tileWorld(10), Z: tileWorld(10), Y: numeric.Fixed(40 * 65536)}
	}

	// Owner bypass precedes the cloak test [C8.1 before C8.2].
	own := at(0)
	own.Hidden = true
	if !s.IsVisible(0, own) {
		t.Fatalf("cloaked own-unit should be visible via owner bypass")
	}
	// Cloaked enemy is not visible [C8.2].
	enemy := at(1)
	enemy.Hidden = true
	if s.IsVisible(0, enemy) {
		t.Fatalf("cloaked enemy should not be visible")
	}
	// A visible enemy on the same tile is the control.
	if !s.IsVisible(0, at(1)) {
		t.Fatalf("uncloaked enemy on a covered tile should be visible")
	}
}

// TestSeaLevelIsHeaderByte locks C8 step 3 against the sea-level byte rather
// than zero [03 §2.2] C9. A unit at world height 10 is above zero and below a
// sea level of 20, so the two readings disagree on it.
func TestSeaLevelIsHeaderByte(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128, SeaLevel: 20}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(0, 10, 10, 0, 320)

	// Z compensates the half-height shear so the projection stays on the tile.
	submerged := Target{
		Owner: 1,
		X:     tileWorld(10),
		Y:     numeric.Fixed(10 * 65536),
		Z:     tileWorld(10) + numeric.Fixed(5*65536),
	}
	if s.IsVisible(0, submerged) {
		t.Fatalf("unit below the sea-level byte should not be visible without 0x200")
	}
	exempt := submerged
	exempt.Status = underwaterExempt
	if !s.IsVisible(0, exempt) {
		t.Fatalf("unit below sea level with 0x200 should be visible")
	}
	// Above sea level needs no exemption.
	afloat := Target{
		Owner: 1,
		X:     tileWorld(10),
		Y:     numeric.Fixed(30 * 65536),
		Z:     tileWorld(10) + numeric.Fixed(15*65536),
	}
	if !s.IsVisible(0, afloat) {
		t.Fatalf("unit above the sea-level byte should be visible")
	}
}

func TestFogLocalOnly(t *testing.T) {
	terrain := &world.Terrain{CellW: 32, CellH: 32}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.mode |= ModeFogCacheValid
	// Remote player publish should not dirty fog [C15]
	s.Publish(1, 8, 8, 0, 320)
	if !s.FogCacheValid() {
		t.Fatalf("remote player publish should not clear fog-cache-valid")
	}
	// Local publish should dirty
	s.Publish(0, 8, 8, 0, 320)
	if s.FogCacheValid() {
		t.Fatalf("local player publish should clear fog-cache-valid")
	}
	// Rebuild fog should validate
	s.RebuildFog(0, 0)
	if !s.FogCacheValid() {
		t.Fatalf("RebuildFog should set valid")
	}
}
