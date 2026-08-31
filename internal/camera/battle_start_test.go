package camera

import "testing"

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
func TestJumpClampsLikeEveryOtherOriginWriter(t *testing.T) {
	c := &Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	c.JumpTo(-50, 5000)
	if c.X != 0 {
		t.Errorf("X = %d, want 0 (below-zero arm of the ordered clamp)", c.X)
	}
	if c.Z != 1024-480 {
		t.Errorf("Z = %d, want %d (mapSize - framebuffer)", c.Z, 1024-480)
	}

	// A target near the map edge clamps rather than centring, and the clamp
	// maximum is measured against the framebuffer because the origin is the
	// framebuffer's top-left corner.
	c = &Camera{ViewW: 640, ViewH: 480, MapW: 1024, MapH: 1024}
	c.JumpToBattleViewCenter(1000, 1000)
	if c.X != 1024-640 || c.Z != 1024-480 {
		t.Fatalf("origin = (%d,%d), want (%d,%d)", c.X, c.Z, 1024-640, 1024-480)
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
