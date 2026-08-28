package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/world"
)

func applyMinimapIntentForTest(cam *camera.Camera, layout camera.Minimap, dst hud.Rect, playW, playH, mouseX, mouseY int32, inside bool, drag *bool) bool {
	if cam == nil {
		return false
	}
	viewport := hud.Rect{X1: camera.OriginX, Y1: camera.OriginY, X2: camera.OriginX + cam.ViewW - 1, Y2: camera.OriginY + cam.ViewH - 1}
	dragging := drag != nil && *drag
	intent, ok := MinimapCameraIntent(cam.X, cam.Z, layout, dst, viewport, playW, playH, mouseX, mouseY, inside, dragging)
	if ok {
		cam.X, cam.Z = intent.X, intent.Z
		cam.Clamp()
	}
	return ok
}

func TestMinimapCameraIntentInsideAndDrag(t *testing.T) { // [07 §10] two-branch lens
	playW, playH := int32(608), int32(352)
	m := camera.LayoutMinimap(640, 480)
	hudRect := hud.Rect{X1: 0, Y1: 0, X2: 126, Y2: 126}
	cam := &camera.Camera{X: 100, Z: 50, ViewW: 64, ViewH: 64, MapW: playW, MapH: playH}
	// Inside branch: click at center of minimap
	isInside := true
	var drag bool
	mouseX := int32(60)
	mouseY := int32(60)
	ok := applyMinimapIntentForTest(cam, m, hudRect, playW, playH, mouseX, mouseY, isInside, &drag)
	if !ok {
		t.Fatalf("applyMinimapIntentForTest inside should be consumed")
	}
	// After inside click, camera origin is the world point corresponding to the
	// display point's converted canvas coordinate, then clamped [03 §3.11].
	canvasX, canvasY, ok := m.DisplayToCanvas(mouseX, mouseY, hudRect.X1, hudRect.Y1, hudRect.X2-hudRect.X1+1, hudRect.Y2-hudRect.Y1+1)
	if !ok {
		t.Fatal("inside lens display point did not convert to canvas")
	}
	wx, wz := m.ToWorldPlay(canvasX, canvasY, playW, playH)
	wantX := wx
	// Need to recompute expected with clampAxis order [07 §10]
	// clampAxis: maximum = mapSize - viewSize; if camera<0->0 else if >maximum->maximum
	maxX := playW - 64
	if wantX < 0 {
		wantX = 0
	} else if wantX > maxX {
		wantX = maxX
	}
	maxZ := playH - 64
	wantZ := wz
	if wantZ < 0 {
		wantZ = 0
	} else if wantZ > maxZ {
		wantZ = maxZ
	}
	if cam.X != wantX || cam.Z != wantZ {
		t.Fatalf("inside lens camera want %d,%d got %d,%d wx,wz %d,%d", wantX, wantZ, cam.X, cam.Z, wx, wz)
	}
	// Drag branch: outside-or-drag-latch => cam + (mouse - viewport origin)
	// clamp [07 §10]. The viewport origin is (128,32), so this pointer is
	// clamped to that origin and the camera target remains its direct origin.
	cam2 := &camera.Camera{X: 10, Z: 20, ViewW: 64, ViewH: 64, MapW: playW, MapH: playH}
	drag = true // latch set
	isInside = false
	mouseX = 20
	mouseY = 30
	ok = applyMinimapIntentForTest(cam2, m, hudRect, playW, playH, mouseX, mouseY, isInside, &drag)
	if !ok {
		t.Fatalf("drag latch should be consumed even when outside")
	}
	wantX2 := int32(10)
	wantZ2 := int32(20)
	// clamp per [07 §10] C3
	if wantX2 < 0 {
		wantX2 = 0
	} else if wantX2 > maxX {
		wantX2 = maxX
	}
	if wantZ2 < 0 {
		wantZ2 = 0
	} else if wantZ2 > maxZ {
		wantZ2 = maxZ
	}
	if cam2.X != wantX2 || cam2.Z != wantZ2 {
		t.Fatalf("drag branch camera want %d,%d got %d,%d", wantX2, wantZ2, cam2.X, cam2.Z)
	}
	// Outside without drag should not be consumed
	cam3 := &camera.Camera{X: 0, Z: 0, ViewW: 64, ViewH: 64, MapW: playW, MapH: playH}
	drag = false
	ok = applyMinimapIntentForTest(cam3, m, hudRect, playW, playH, 5, 5, false, &drag)
	if ok {
		t.Fatalf("outside without drag should not be consumed")
	}
	if cam3.X != 0 || cam3.Z != 0 {
		t.Fatalf("outside without drag should not move camera")
	}
}

func TestMinimapCameraIntentClampOrder(t *testing.T) { // [07 §10] C3 clampAxis order
	playW, playH := int32(100), int32(100)
	m := camera.Minimap{W: 126, H: 126, PadX: 0, PadY: 0}
	hudRect := hud.Rect{X1: 0, Y1: 0, X2: 126, Y2: 126}
	// view larger than map -> maximum = map - view negative; the established
	// ordered clamp returns the negative maximum for a nonnegative target.
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 200, ViewH: 200, MapW: playW, MapH: playH}
	// inside click at 0,0 => wx 0; the ordered clamp sees target 0 above the
	// negative maximum and returns that maximum.
	ok := applyMinimapIntentForTest(cam, m, hudRect, playW, playH, 0, 0, true, nil)
	if !ok {
		t.Fatalf("inside should be consumed")
	}
	if cam.X != -100 || cam.Z != -100 {
		t.Fatalf("clamp order negative-maximum: camera -100,-100 want -100,-100 got %d,%d", cam.X, cam.Z)
	}
	// Click far edge with large view: newCam positive but > maximum (negative maximum) -> clamp to maximum negative
	cam2 := &camera.Camera{X: 0, Z: 0, ViewW: 200, ViewH: 200, MapW: playW, MapH: playH}
	dragClamp := true
	if !applyMinimapIntentForTest(cam2, m, hudRect, playW, playH, 126, 126, false, &dragClamp) {
		t.Fatalf("drag clamp case should be consumed")
	}
	// maximum = 100-200 = -100, camera computed maybe > -100? For click at 126 -> wx ~100, newCam = 0? Let's just ensure clamp doesn't panic and stays within 0..maximum logic: if camera positive > maximum negative, it should clamp to maximum (-100) per clampAxis? But first check camera<0 ->0, else if >maximum. Since maximum negative, a positive camera (e.g., 50) is > maximum (-100), so it clamps to -100. That's the ordered form.
	// Our impl does that.
	if cam2.X != -100 || cam2.Z != -100 {
		t.Fatalf("positive target must clamp to negative maximum: got %d,%d want -100,-100", cam2.X, cam2.Z)
	}
}

func TestMinimapDisplayDrawClickRelationship(t *testing.T) {
	playW, playH := int32(608), int32(352)
	layout := camera.LayoutMinimap(playW, playH)
	dst := hud.Rect{X1: 200, Y1: 100, X2: 325, Y2: 225}
	canvasX, canvasY := layout.PadX+layout.W/2, layout.PadY+layout.H/2
	displayX, displayY, ok := layout.CanvasToDisplay(canvasX, canvasY, dst.X1, dst.Y1, dst.X2-dst.X1+1, dst.Y2-dst.Y1+1)
	if !ok {
		t.Fatal("canonical canvas point did not project to display")
	}
	clickX, clickY, ok := layout.DisplayToCanvas(displayX, displayY, dst.X1, dst.Y1, dst.X2-dst.X1+1, dst.Y2-dst.Y1+1)
	if !ok || !layout.HitTest(clickX, clickY) {
		t.Fatalf("draw/click transform left canonical radar rectangle: %d,%d", clickX, clickY)
	}
	cam := &camera.Camera{ViewW: 64, ViewH: 64, MapW: playW, MapH: playH}
	if !applyMinimapIntentForTest(cam, layout, dst, playW, playH, displayX, displayY, true, nil) {
		t.Fatal("canonical minimap click was not consumed")
	}
	wantX, wantZ := layout.ToWorldPlay(clickX, clickY, playW, playH)
	cam.Clamp()
	if cam.X != wantX || cam.Z != wantZ {
		t.Fatalf("click camera mismatch: got %d,%d want %d,%d", cam.X, cam.Z, wantX, wantZ)
	}
}

func TestDrawMinimapLayoutUsesLetterboxAndMarker(t *testing.T) {
	playW, playH := int32(608), int32(352)
	layout := camera.LayoutMinimap(playW, playH)
	dst := hud.Rect{X1: 10, Y1: 20, X2: 135, Y2: 145}
	c := &Client{width: 160, height: 170, indexed: make([]uint8, 160*170)}
	surf := &render.RadarSurface{W: 1, H: 1, Pitch: 4, Bits: []byte{7}}
	markerX, markerY := layout.PadX+layout.W/2, layout.PadY+layout.H/2
	c.DrawMinimapLayout(surf, dst, layout, 2, markerX, markerY, 9)
	dx, dy, ok := layout.CanvasToDisplay(markerX, markerY, dst.X1, dst.Y1, 126, 126)
	if !ok || c.indexed[int(dy)*c.width+int(dx)] != 9 {
		t.Fatalf("marker did not land in canonical display transform at %d,%d", dx, dy)
	}
	// Letterbox bars are untouched by the picture blit, while the interior is
	// populated from the same authored surface [07 §10].
	barX, barY := dst.X1, dst.Y1
	if layout.PadX == 0 {
		barX++
	} else {
		barX += layout.PadX / 2
	}
	if layout.PadY > 0 && c.indexed[int(barY)*c.width+int(barX)] != 0 {
		t.Fatalf("letterbox bar was written at %d,%d", barX, barY)
	}
}

func TestDrawMinimapLayoutClipsEveryMarkerArmToFittedRect(t *testing.T) {
	tests := []struct {
		name    string
		playW   int32
		playH   int32
		topLeft bool
	}{
		// Wide maps have vertical letterbox bars. At the top-left fitted pixel,
		// only the crossing and inward arms may be written.
		{name: "wide top-left", playW: 640, playH: 480, topLeft: true},
		{name: "wide bottom-right", playW: 640, playH: 480},
		// Tall maps exercise the corresponding horizontal bars.
		{name: "tall top-left", playW: 480, playH: 640, topLeft: true},
		{name: "tall bottom-right", playW: 480, playH: 640},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			layout := camera.LayoutMinimap(tc.playW, tc.playH)
			// Place the center at each fitted edge, not at a canvas edge. This
			// catches a marker that clips to the 126-pixel canvas but not radar.
			centerX, centerY := layout.Right(), layout.Bottom()
			if tc.topLeft {
				centerX, centerY = layout.PadX, layout.PadY
			}
			c := &Client{width: 126, height: 126, indexed: make([]uint8, 126*126)}
			surf := &render.RadarSurface{W: 1, H: 1, Pitch: 4, Bits: []byte{7}}
			c.DrawMinimapLayout(surf, hud.Rect{X1: 0, Y1: 0, X2: 125, Y2: 125}, layout, 2, centerX, centerY, 9)
			for y := int32(0); y < 126; y++ {
				for x := int32(0); x < 126; x++ {
					got := c.indexed[int(y)*126+int(x)]
					if got != 9 {
						continue
					}
					canvasX, canvasY, ok := layout.DisplayToCanvas(x, y, 0, 0, 126, 126)
					if !ok || !layout.HitTest(canvasX, canvasY) {
						t.Fatalf("marker pixel at display %d,%d mapped outside fitted rect to %d,%d", x, y, canvasX, canvasY)
					}
				}
			}
			found := false
			for _, v := range c.indexed {
				if v == 9 {
					found = true
					break
				}
			}
			if !found {
				t.Fatal("fitted marker crossing was not drawn")
			}
		})
	}
}

func TestDrawMinimapLayoutMarkerModeOffDoesNotDraw(t *testing.T) {
	layout := camera.LayoutMinimap(640, 480)
	c := &Client{width: 126, height: 126, indexed: make([]uint8, 126*126)}
	surf := &render.RadarSurface{W: 1, H: 1, Pitch: 4, Bits: []byte{7}}
	c.DrawMinimapLayout(surf, hud.Rect{X1: 0, Y1: 0, X2: 125, Y2: 125}, layout, 0, layout.PadX+layout.W/2, layout.PadY+layout.H/2, 9)
	for _, v := range c.indexed {
		if v == 9 {
			t.Fatal("marker mode 0 drew viewport marker")
		}
	}
}

func TestMinimapPlaySizeForMinimap(t *testing.T) {
	ter := &world.Terrain{CellW: 64, CellH: 64, PlayRight: 992, PlayBottom: 896}
	w, h := PlaySizeForMinimap(ter)
	if w != 992 || h != 896 {
		t.Fatalf("PlaySizeForMinimap want 992,896 got %d,%d", w, h)
	}
}
