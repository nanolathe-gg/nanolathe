package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

// TestMinimapViewportRect locks the traced camera-to-radar rectangle of
// [03 R-MM-01 §1]. The arithmetic is written out here rather than recomputed
// from the function under test, because a constant or a division order is
// exactly what regresses silently.
func TestMinimapViewportRect(t *testing.T) {
	// A 64x64-cell map: PlayRight = 64*16-32, PlayBottom = 64*16-128 [03 §3.4].
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	if m.W != 126 || m.H != 113 || m.PadX != 0 || m.PadY != 6 {
		t.Fatalf("layout = %+v, want W126 H113 PadX0 PadY6", m)
	}
	dst := Rect{X1: 0, Y1: 0, X2: 125, Y2: 125}
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}

	// cameraX = 0 + OriginX = 128, cameraZ = 0 + OriginY = 32; the game
	// viewport is 512x416 map pixels [03 §4.1].
	//   left   = 0 + 128*126/992  = 16
	//   top    = 6 +  32*113/896  = 6 + 4 = 10
	//   right  = 16 - 1 + 512*126/992 = 15 + 65 = 80
	//   bottom = 10 - 1 + 416*113/896 = 9 + 52 = 61
	got, ok := MinimapViewportRect(cam, m, playW, playH, dst)
	if !ok {
		t.Fatal("viewport rectangle must exist for a valid camera and lens")
	}
	if want := (Rect{X1: 16, Y1: 10, X2: 80, Y2: 61}); got != want {
		t.Fatalf("ViewportRect = %+v, want %+v", got, want)
	}

	// The rectangle tracks the camera, and its extents do not change with it.
	moved := &camera.Camera{X: 200, Z: 100, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	got2, ok := MinimapViewportRect(moved, m, playW, playH, dst)
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
	got3, ok := MinimapViewportRect(corner, m, playW, playH, dst)
	if !ok {
		t.Fatal("map-corner camera produced no rectangle")
	}
	if got3.X1 != m.PadX || got3.Y1 != m.PadY {
		t.Fatalf("map-corner rectangle = %+v, want its top-left at the letterbox origin %d,%d", got3, m.PadX, m.PadY)
	}

	// No camera, no lens, no play area: nothing is drawn rather than a
	// placeholder rectangle.
	if _, ok := MinimapViewportRect(nil, m, playW, playH, dst); ok {
		t.Fatal("a nil camera must not produce a rectangle")
	}
	if _, ok := MinimapViewportRect(cam, camera.Minimap{}, playW, playH, dst); ok {
		t.Fatal("an empty lens must not produce a rectangle")
	}
}

func TestMinimapViewportRectUsesDisplayDestination(t *testing.T) {
	// A real presentation destination is not necessarily the 126-pixel canvas:
	// this catches returning canvas coordinates when the HUD is offset and
	// scaled. A 252-pixel destination is exactly 2x the canvas.
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	dst := Rect{X1: 200, Y1: 100, X2: 451, Y2: 351}
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	got, ok := MinimapViewportRect(cam, m, playW, playH, dst)
	if !ok {
		t.Fatal("scaled destination produced no rectangle")
	}
	// Canvas (16,10)-(80,61) at 2x from origin (200,100).
	if want := (Rect{X1: 232, Y1: 120, X2: 360, Y2: 222}); got != want {
		t.Fatalf("ViewportRect = %+v, want %+v", got, want)
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
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
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
	detail, _ := MinimapViewportRect(&camera.Camera{X: 200, Z: 100, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH, Scale: camera.ViewScaleDetail}, m, playW, playH, dst)
	if detail.X2-detail.X1 >= native.X2-native.X1 || detail.Y2-detail.Y1 >= native.Y2-native.Y1 {
		t.Errorf("detail rectangle %+v is not smaller than the native one %+v", detail, native)
	}
}
