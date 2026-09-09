package hud

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/world"
)

func TestMinimapHUDHitTestInclusive(t *testing.T) { // retail's minimap hit test is Rect-inclusive [07 §10][03 §3.11]
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	if !h.HitTest(0, 0) {
		t.Fatalf("HitTest top-left inclusive want true")
	}
	if !h.HitTest(126, 126) {
		t.Fatalf("HitTest bottom-right inclusive want true fallback 0,0,126,126")
	}
	if h.HitTest(127, 0) || h.HitTest(0, 127) {
		t.Fatalf("HitTest outside want false")
	}
	// Also compare to camera minimap HitTest semantics: both inclusive
	m := camera.LayoutMinimap(640, 480)
	camHit := m.HitTest(m.PadX, m.PadY)
	h2 := NewMinimapHUD(Anchors{}, Rect{X1: m.PadX, Y1: m.PadY, X2: m.Right(), Y2: m.Bottom()})
	if !h2.HitTest(m.PadX, m.PadY) != !camHit {
		t.Fatalf("hud HitTest mismatch camera HitTest")
	}
	if !h2.HitTest(m.Right(), m.Bottom()) {
		t.Fatalf("hud HitTest bottom-right want true")
	}
	if h2.HitTest(m.PadX-1, m.PadY) {
		t.Fatalf("hud HitTest left-1 want false")
	}
}

func TestMinimapHUDRequiresAuthoredRect(t *testing.T) {
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	if h.Rect != (Rect{X1: 0, Y1: 0, X2: 126, Y2: 126}) {
		t.Fatalf("explicit authored rect was not retained: %+v", h.Rect)
	}
	if h.BlinkCountdown != 7 {
		t.Fatalf("BlinkCountdown want 7 got %d [03 §3.6]", h.BlinkCountdown)
	}
	// The 30 side anchors do not contain a minimap record.
	var a Anchors
	a[0] = Rect{X1: 10, Y1: 10, X2: 20, Y2: 20}
	h2 := NewMinimapHUD(a, Rect{})
	if h2.Rect != (Rect{}) {
		t.Fatalf("unresolved rail anchor must remain empty: %+v", h2.Rect)
	}
}

func TestMinimapHUDWorldToMinimapRoundTrip(t *testing.T) { // world<->minimap conversion truncates within allowX/Y, as in camera tests [07 §10]
	cases := []struct {
		mapW, mapH   int32
		playW, playH int32
	}{
		{640, 480, camera.PlayRight(640), camera.PlayBottom(480)},
		{1024, 1024, 992, 896},
		{480, 640, 448, 512},
	}
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 126, Y2: 126})
	for _, c := range cases {
		m := camera.LayoutMinimap(c.mapW, c.mapH)
		pts := []struct{ wx, wz int32 }{
			{0, 0},
			{c.playW - 1, c.playH - 1},
			{c.playW / 2, c.playH / 2},
			{c.playW / 4, c.playH / 3},
			{100, 200},
		}
		for _, p := range pts {
			rx, ry := h.WorldToMinimap(p.wx, p.wz, c.playW, c.playH, m)
			// Must be inside hud rect's radar area when world inside play? For fallback full square, rx 0..126 inclusive may be slightly out of m's pad but inside hud.
			wx2, wz2 := h.MinimapToWorld(rx, ry, c.playW, c.playH, m)
			dx := wx2 - p.wx
			if dx < 0 {
				dx = -dx
			}
			dz := wz2 - p.wz
			if dz < 0 {
				dz = -dz
			}
			allowX := c.playW/m.W + 1
			if m.W == 0 {
				allowX = 1
			}
			allowZ := c.playH/m.H + 1
			if m.H == 0 {
				allowZ = 1
			}
			if dx > allowX || dz > allowZ {
				t.Fatalf("world->minimap->world trunc error >allow (%d,%d): map %dx%d play %dx%d world (%d,%d) -> minimap (%d,%d) -> world (%d,%d) delta %d,%d", allowX, allowZ, c.mapW, c.mapH, c.playW, c.playH, p.wx, p.wz, rx, ry, wx2, wz2, dx, dz)
			}
			// Also test MinimapToWorld matches camera RadarToWorld
			wx3, wz3 := m.RadarToWorld(rx, ry, c.playW, c.playH)
			if wx3 != wx2 || wz3 != wz2 {
				t.Fatalf("MinimapToWorld mismatch RadarToWorld: %d,%d vs %d,%d", wx3, wz3, wx2, wz2)
			}
		}
	}
}

// TestMinimapHUDViewportRect locks the traced camera-to-radar rectangle of
// [03 R-MM-01 §1]. The arithmetic is written out here rather than recomputed
// from the function under test, because a constant or a division order is
// exactly what regresses silently.
func TestMinimapHUDViewportRect(t *testing.T) {
	// A 64x64-cell map: PlayRight = 64*16-32, PlayBottom = 64*16-128 [03 §3.4].
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	if m.W != 126 || m.H != 113 || m.PadX != 0 || m.PadY != 6 {
		t.Fatalf("layout = %+v, want W126 H113 PadX0 PadY6", m)
	}
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 125, Y2: 125})
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}

	// cameraX = 0 + OriginX = 128, cameraZ = 0 + OriginY = 32; the game
	// viewport is 512x416 map pixels [03 §4.1].
	//   left   = 0 + 128*126/992  = 16
	//   top    = 6 +  32*113/896  = 6 + 4 = 10
	//   right  = 16 - 1 + 512*126/992 = 15 + 65 = 80
	//   bottom = 10 - 1 + 416*113/896 = 9 + 52 = 61
	got, ok := h.ViewportRect(cam, m, playW, playH)
	if !ok {
		t.Fatal("viewport rectangle must exist for a valid camera and lens")
	}
	if want := (Rect{X1: 16, Y1: 10, X2: 80, Y2: 61}); got != want {
		t.Fatalf("ViewportRect = %+v, want %+v", got, want)
	}

	// The rectangle tracks the camera, and its extents do not change with it.
	moved := &camera.Camera{X: 200, Z: 100, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	got2, ok := h.ViewportRect(moved, m, playW, playH)
	if !ok {
		t.Fatal("moved camera produced no rectangle")
	}
	if got2.X2-got2.X1 != got.X2-got.X1 || got2.Y2-got2.Y1 != got.Y2-got.Y1 {
		t.Fatalf("rectangle size changed with the camera: %+v then %+v", got, got2)
	}
	if got2.X1 <= got.X1 || got2.Y1 <= got.Y1 {
		t.Fatalf("rectangle did not follow the camera: %+v then %+v", got, got2)
	}

	// A camera origin of -OriginX/-OriginY is the map's own corner, and the
	// signed truncating divides must land the rectangle exactly on the
	// letterbox origin rather than wrapping [03 R-MM-01 §1].
	corner := &camera.Camera{X: -camera.OriginX, Z: -camera.OriginY, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	got3, ok := h.ViewportRect(corner, m, playW, playH)
	if !ok {
		t.Fatal("map-corner camera produced no rectangle")
	}
	if got3.X1 != m.PadX || got3.Y1 != m.PadY {
		t.Fatalf("map-corner rectangle = %+v, want its top-left at the letterbox origin %d,%d", got3, m.PadX, m.PadY)
	}

	// No camera, no lens, no play area: nothing is drawn rather than a
	// placeholder rectangle.
	if _, ok := h.ViewportRect(nil, m, playW, playH); ok {
		t.Fatal("a nil camera must not produce a rectangle")
	}
	if _, ok := h.ViewportRect(cam, camera.Minimap{}, playW, playH); ok {
		t.Fatal("an empty lens must not produce a rectangle")
	}
}

func TestMinimapHUDViewportRectUsesDisplayDestination(t *testing.T) {
	// A real presentation destination is not necessarily the 126-pixel canvas:
	// this catches returning canvas coordinates when the HUD is offset and
	// scaled. A 252-pixel destination is exactly 2x the canvas.
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	h := NewMinimapHUD(Anchors{}, Rect{X1: 200, Y1: 100, X2: 451, Y2: 351})
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	got, ok := h.ViewportRect(cam, m, playW, playH)
	if !ok {
		t.Fatal("scaled destination produced no rectangle")
	}
	// Canvas (16,10)-(80,61) at 2x from origin (200,100).
	if want := (Rect{X1: 232, Y1: 120, X2: 360, Y2: 222}); got != want {
		t.Fatalf("ViewportRect = %+v, want %+v", got, want)
	}
}

// TestMinimapViewportRectMatchesTheMethod keeps the destination-explicit entry
// point and the MinimapHUD method on one implementation.
func TestMinimapViewportRectMatchesTheMethod(t *testing.T) {
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	dst := Rect{X1: 3, Y1: 7, X2: 128, Y2: 132}
	h := NewMinimapHUD(Anchors{}, dst)
	cam := &camera.Camera{X: 64, Z: 96, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	a, okA := h.ViewportRect(cam, m, playW, playH)
	b, okB := MinimapViewportRect(cam, m, playW, playH, dst)
	if okA != okB || a != b {
		t.Fatalf("method %+v/%v and function %+v/%v disagree", a, okA, b, okB)
	}
}

func TestMinimapHUDDirtyBlink(t *testing.T) {
	h := NewMinimapHUD(Anchors{}, Rect{X1: 0, Y1: 0, X2: 10, Y2: 10})
	// DirtyBlink bits: bit0 blink, bit1 FINAL dirty, bit2 MAPPED dirty [03 §3.6]
	h.DirtyBlink = 0x0004 // MAPPED dirty
	if h.DirtyBlink&0x0004 == 0 {
		t.Fatalf("MAPPED dirty bit not set")
	}
	h.DirtyBlink |= 0x0002 // FINAL dirty
	h.DirtyBlink ^= 0x0001 // blink toggle every 8 frames
	if h.DirtyBlink == 0 {
		t.Fatalf("DirtyBlink bits")
	}
}

func TestPlaySizeForMinimap(t *testing.T) { // Wpix-32, Hpix-128 [03 §3.4]
	ter := &world.Terrain{CellW: 64, CellH: 64}
	ter.PlayRight = 64*16 - 32
	ter.PlayBottom = 64*16 - 128
	w, h := PlaySizeForMinimap(ter)
	if w != ter.PlayRight || h != ter.PlayBottom {
		t.Fatalf("PlaySizeForMinimap want %d,%d got %d,%d", ter.PlayRight, ter.PlayBottom, w, h)
	}
	// Missing map extents are not reconstructed from raw dimensions.
	ter2 := &world.Terrain{CellW: 32, CellH: 32}
	w2, h2 := PlaySizeForMinimap(ter2)
	if w2 != 0 || h2 != 0 {
		t.Fatalf("PlaySizeForMinimap missing extents want 0,0 got %d,%d", w2, h2)
	}
}

// TestMinimapViewportRectOriginFollowsTheCameraAtTheDetailScale locks the
// detail view's minimap rectangle to the camera's own battle-view origin
// (DESIGN_GPU_RENDERER §14.2). The leading chrome inset is 128 framebuffer
// pixels at either scale, but at scale 2 those pixels cover 64 world pixels,
// so an open-coded `cam.X + camera.OriginX` would place the rectangle 64 world
// pixels east and 16 south of where the view actually starts — and its size,
// which already comes from BattleView, would disagree with its origin.
func TestMinimapViewportRectOriginFollowsTheCameraAtTheDetailScale(t *testing.T) {
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	dst := Rect{X1: 0, Y1: 0, X2: 125, Y2: 125}
	for _, scale := range []int32{1, 2} {
		cam := &camera.Camera{X: 200, Z: 100, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH, Scale: scale}
		got, ok := MinimapViewportRect(cam, m, playW, playH, dst)
		if !ok {
			t.Fatalf("scale %d produced no rectangle", scale)
		}
		originX, originZ := cam.BattleViewOrigin()
		wantX := m.PadX + int32(int64(originX)*int64(m.W)/int64(playW))
		wantY := m.PadY + int32(int64(originZ)*int64(m.H)/int64(playH))
		if got.X1 != wantX || got.Y1 != wantY {
			t.Errorf("scale %d rectangle origin = (%d,%d), want the battle view origin's (%d,%d)",
				scale, got.X1, got.Y1, wantX, wantY)
		}
	}
	// The detail view shows half as much world, so its rectangle is smaller.
	native, _ := MinimapViewportRect(&camera.Camera{X: 200, Z: 100, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}, m, playW, playH, dst)
	detail, _ := MinimapViewportRect(&camera.Camera{X: 200, Z: 100, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH, Scale: 2}, m, playW, playH, dst)
	if detail.X2-detail.X1 >= native.X2-native.X1 || detail.Y2-detail.Y1 >= native.Y2-native.Y1 {
		t.Errorf("detail rectangle %+v is not smaller than the native one %+v", detail, native)
	}
}
