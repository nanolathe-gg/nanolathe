package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// shadowSprite is a one-pixel authored stand-in for frame 0 of the shared
// `shadow` entry. Its placement offsets are zero so the test reads the pen
// position directly.
func shadowSprite(index byte) *formats.GAFFrame {
	return &formats.GAFFrame{Width: 1, Height: 1, ColorKey: 9, Pixels: []byte{index}, Transparent: []bool{false}}
}

// TestProjectileShadowUsesCachedFloorNotProjectileHeight locks the anchor of
// [03 §5.4]: the shared ground shadow projects at
// `(X − viewX + 128, (Z − floor/2) − viewZ + 32)` against the record's cached
// average floor height [06 §8.1], never against the projectile's own Y.
//
// The projectile is 200 world units up over ground whose cached floor is 40, so
// the two candidate anchors are 80 rows apart and cannot be confused.
func TestProjectileShadowUsesCachedFloorNotProjectileHeight(t *testing.T) {
	c, err := New(Options{Width: 320, Height: 240})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	c.cam = &camera.Camera{ViewW: 320, ViewH: 240, MapW: 4096, MapH: 4096}
	v := frame.ProjectileView{
		Handle:           1,
		X:                numeric.Fixed(100 << 16),
		Y:                numeric.Fixed(200 << 16),
		Z:                numeric.Fixed(100 << 16),
		FloorHeight:      40,
		FloorHeightValid: true,
	}
	if !c.drawProjectileShadow(shadowSprite(77), v) {
		t.Fatal("shadow was not drawn for a record with a cached floor height")
	}
	c.replayForTest()

	// The expected pen is the ordinary projection with the floor standing in
	// for the height: X = 100, row = 100 - (40>>1) = 80.
	wantX, wantY := int32(100), int32(100-20)
	if got := c.indexed[int(wantY)*c.width+int(wantX)]; got != 77 {
		t.Fatalf("shadow pixel at (%d,%d) = %d, want 77", wantX, wantY, got)
	}
	// The projectile's own height would have put it at row 100 - 100 = 0.
	if got := c.indexed[0*c.width+int(wantX)]; got == 77 {
		t.Fatal("shadow was anchored on the projectile's own Y; it must use the cached floor")
	}
}

// TestProjectileShadowSuppressedWithoutCachedFloor locks that a record whose
// point resolved to no plot cell draws no shadow: the collision gate retires an
// off-map point without sampling terrain, so there is no cached floor to anchor
// against and presentation must not invent one [06 §8.1][I9].
func TestProjectileShadowSuppressedWithoutCachedFloor(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	c.cam = &camera.Camera{ViewW: 64, ViewH: 64, MapW: 1024, MapH: 1024}
	v := frame.ProjectileView{Handle: 1, X: numeric.Fixed(10 << 16), Z: numeric.Fixed(10 << 16)}
	if c.drawProjectileShadow(shadowSprite(77), v) {
		t.Fatal("a record with no cached floor drew a shadow")
	}
	for i, px := range c.indexed {
		if px == 77 {
			t.Fatalf("shadow pixel written at %d despite no cached floor", i)
		}
	}
}
