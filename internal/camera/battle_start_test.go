package camera

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The battle viewport is the framebuffer minus the chrome the viewport subrect
// excludes: left 128, top 32, bottom 32, so W-128 by H-64 [03 §4.1].
func TestBattleViewIsTheViewportSubrect(t *testing.T) {
	for _, tc := range []struct {
		name         string
		viewW, viewH int32
		wantW, wantH int32
	}{
		{"640x480", 640, 480, 512, 416}, // [03 §4.1] the only mode Nanolathe composes
		{"800x600", 800, 600, 672, 536}, // [03 §4.1]
		{"1024x768", 1024, 768, 896, 704},
		{"smaller than the chrome floors at zero", 64, 16, 0, 0},
	} {
		c := &Camera{ViewW: tc.viewW, ViewH: tc.viewH}
		gotW, gotH := c.BattleView()
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Errorf("%s: BattleView() = %dx%d, want %dx%d", tc.name, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}

// JumpToBattleViewCenter puts the target at the centre of what the player can
// see, which in this build's origin frame is the viewport inset plus half the
// viewport, not half the framebuffer [07 R-CAM-01 §12][03 §4.1].
func TestJumpToBattleViewCenterUsesTheViewportNotTheFramebuffer(t *testing.T) {
	c := &Camera{ViewW: 640, ViewH: 480, MapW: 4096, MapH: 4096}
	c.JumpToBattleViewCenter(1000, 900)
	// 1000 - 128 - 512/2 = 616 ; 900 - 32 - 416/2 = 660.
	if c.X != 616 || c.Z != 660 {
		t.Fatalf("origin = (%d,%d), want (616,660)", c.X, c.Z)
	}
	// The target must land on the viewport's centre pixel once the projection
	// takes the beam offset back out: OriginX + 512/2 = 384, OriginY + 416/2 = 240.
	if got := 1000 - c.X; got != 384 {
		t.Errorf("target screen X = %d, want 384 (viewport centre)", got)
	}
	if got := 900 - c.Z; got != 240 {
		t.Errorf("target screen Z = %d, want 240 (viewport centre)", got)
	}
	// Halving the framebuffer would have produced 680 on X — 64 pixels off — and
	// the same 660 on Z, which is why the Z axis alone hid the defect.
	if c.X == 1000-320 {
		t.Error("origin halves the framebuffer width instead of the viewport width")
	}
}

// A jump writes the origin outright and then clamps, exactly like every other
// origin writer [07 R-CAM-01 §12][07 §10].
//
// The bounds are the battle viewport's, in this build's origin frame: the floor
// is minus the viewport's leading inset and the maximum is
// mapSize - viewportSpan - leadingInset [07 §10][03 §4.1]. This test previously
// asserted a floor of 0 and a Z maximum of mapSize - framebuffer; both were the
// unconverted retail bounds and made the map's west, north and south margins
// unreachable (defect PT5-01).
func TestJumpClampsLikeEveryOtherOriginWriter(t *testing.T) {
	c := &Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	c.JumpTo(-50, 5000)
	// -50 is above the floor of -128, so it survives the clamp: the world's
	// column 0 sits 50 pixels right of the viewport's left edge.
	if c.X != -50 {
		t.Errorf("X = %d, want -50 (inside the [-128, ...] range)", c.X)
	}
	if c.Z != 1024-480+OriginY {
		t.Errorf("Z = %d, want %d (mapSize - viewportSpan - top inset)", c.Z, 1024-480+OriginY)
	}
	// At that Z maximum the map's last row is the viewport's last visible row.
	if last := c.Z + OriginY + 416 - 1; last != 1024-1 {
		t.Errorf("last visible row = %d, want %d", last, 1024-1)
	}

	// Below the floor clamps to the floor, and at the floor the map's first
	// column and row sit exactly on the viewport's leading edges.
	c = &Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	c.JumpTo(-1000, -1000)
	if c.X != -OriginX || c.Z != -OriginY {
		t.Fatalf("origin = (%d,%d), want (%d,%d)", c.X, c.Z, -OriginX, -OriginY)
	}
	if first := c.X + OriginX; first != 0 {
		t.Errorf("first visible column = %d, want 0", first)
	}

	// A target near the map edge clamps rather than centring.
	c = &Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	c.JumpToBattleViewCenter(1000, 1000)
	if c.X != 1024-512-OriginX || c.Z != 1024-416-OriginY {
		t.Fatalf("origin = (%d,%d), want (%d,%d)", c.X, c.Z, 1024-512-OriginX, 1024-416-OriginY)
	}
}

// Every map pixel of the playable area is reachable: at the clamp's floor the
// playable origin (0,0) is the viewport's first visible pixel, and at the clamp's
// maximum the playable extent's last pixel is its last visible pixel
// [07 §10][03 §4.1]. Great Divide's extents are the regression case: its only
// geothermal vent is anchored at map pixel (104,152), which the pre-2026-08-31
// floor of 0 could never bring on screen (defect PT5-01).
func TestEveryPlayablePixelIsReachable(t *testing.T) {
	// Great Divide: 160x256 cells, so 2560x4096 map pixels and playable
	// extents PlayRight = 2528, PlayBottom = 3968 [03 §3.4].
	playW, playH := world.PlayInsets(160, 256)
	if playW != 2528 || playH != 3968 {
		t.Fatalf("play extents = %dx%d, want 2528x3968", playW, playH)
	}
	c := NewFromTerrain(2560, 4096, playW, playH, 640, 480)

	c.JumpTo(-100000, -100000)
	if first := c.X + OriginX; first != 0 {
		t.Errorf("hard west: first visible column = %d, want 0", first)
	}
	if first := c.Z + OriginY; first != 0 {
		t.Errorf("hard north: first visible row = %d, want 0", first)
	}
	// The vent at map pixel (104,152) is inside the visible 512x416 rectangle
	// at the hard-west, hard-north origin.
	if vx := 104 - c.X; vx < OriginX || vx > 639 {
		t.Errorf("vent screen X = %d, want within [%d,639]", vx, OriginX)
	}
	if vz := 152 - c.Z; vz < OriginY || vz > 447 {
		t.Errorf("vent screen Z = %d, want within [%d,447]", vz, OriginY)
	}

	c.JumpTo(100000, 100000)
	if last := c.X + 639; last != playW-1 {
		t.Errorf("hard east: last visible column = %d, want %d", last, playW-1)
	}
	if last := c.Z + 447; last != playH-1 {
		t.Errorf("hard south: last visible row = %d, want %d", last, playH-1)
	}
}

// The halving is a truncating integer divide, as retail's is [07 R-CAM-01 §12].
func TestJumpToBattleViewCenterTruncatesTheHalving(t *testing.T) {
	// 641 - 128 = 513 wide, 481 - 64 = 417 tall: both halves truncate.
	c := &Camera{ViewW: 641, ViewH: 481, MapW: 8192, MapH: 8192}
	c.JumpToBattleViewCenter(1000, 1000)
	if c.X != 1000-128-256 {
		t.Errorf("X = %d, want %d (513/2 truncates to 256)", c.X, 1000-128-256)
	}
	if c.Z != 1000-32-208 {
		t.Errorf("Z = %d, want %d (417/2 truncates to 208)", c.Z, 1000-32-208)
	}
}
