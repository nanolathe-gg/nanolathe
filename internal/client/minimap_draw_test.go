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

// TestDrawMinimapLayoutDrawsPictureOnlyLocks the corrected contract: the
// minimap carries its radar picture and nothing else. The three superseded
// tests here asserted a five-pixel camera cross at "marker mode" 2, its arm
// clipping, and its absence at mode 0. That figure is not a minimap feature at
// all — the world composer draws it over the game viewport at the ground
// resolver's world point, and only in film mode [03 §3.12].
func TestDrawMinimapLayoutDrawsPictureOnly(t *testing.T) {
	playW, playH := int32(608), int32(352)
	layout := camera.LayoutMinimap(playW, playH)
	dst := hud.Rect{X1: 10, Y1: 20, X2: 135, Y2: 145}
	c := &Client{width: 160, height: 170, indexed: make([]uint8, 160*170)}
	surf := &render.RadarSurface{W: 1, H: 1, Pitch: 4, Bits: []byte{7}}
	c.DrawMinimapLayout(surf, dst, layout)
	centerX, centerY := layout.PadX+layout.W/2, layout.PadY+layout.H/2
	dx, dy, ok := layout.CanvasToDisplay(centerX, centerY, dst.X1, dst.Y1, 126, 126)
	if !ok || c.indexed[int(dy)*c.width+int(dx)] != 7 {
		t.Fatalf("radar picture did not land in the canonical display transform at %d,%d", dx, dy)
	}
	// Nothing but the authored surface's own index may be written.
	for _, v := range c.indexed {
		if v != 0 && v != 7 {
			t.Fatalf("minimap wrote index %d, which is not the radar picture", v)
		}
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

func TestMinimapPlaySizeForMinimap(t *testing.T) {
	ter := &world.Terrain{CellW: 64, CellH: 64, PlayRight: 992, PlayBottom: 896}
	w, h := PlaySizeForMinimap(ter)
	if w != 992 || h != 896 {
		t.Fatalf("PlaySizeForMinimap want 992,896 got %d,%d", w, h)
	}
}

// TestMinimapMappedFollowsExploredMaskEachTick locks the explored-terrain
// contract of [03 §3.8] on the surface the client actually draws: a cell the
// local player has ever seen keeps its picture byte, tinted through the GUI
// remap once current sight leaves it, and only a never-explored cell takes the
// fog fill. The mapping word grid is an idempotent OR that is never
// decremented, so exploration is permanent [03 §3.2].
//
// It is the regression test for playtest defect PT3-13. The MAPPED composite
// used to run only while the allocation-time dirty bit stood, and nothing in
// this port could raise that bit again, so the surface froze at the first
// frame the HUD composited and no terrain explored afterwards ever appeared.
// Retail raises the same bit from the tail of every local LOS raster that
// changed a cell [03 §3.6 "Mapped-surface invalidation"], so the composite
// must follow the committed stores on every tick.
func TestMinimapMappedFollowsExploredMaskEachTick(t *testing.T) {
	const grid = 4
	const terrainIndex, dimIndex, fogIndex = byte(7), byte(3), byte(1)
	picture := &render.RadarSurface{W: grid, H: grid, Pitch: grid, Bits: make([]byte, grid*grid)}
	for i := range picture.Bits {
		picture.Bits[i] = terrainIndex
	}
	remap := make([]byte, 256)
	for i := range remap {
		remap[i] = byte(i)
	}
	remap[terrainIndex] = dimIndex
	svc := render.NewMinimapService(render.MinimapServiceConfig{
		Picture: picture, MapW: grid, MapH: grid, LocalSlot: 0,
		FogFill: fogIndex, GUIRemap: remap,
	})
	layout := camera.LayoutMinimap(1024, 1024)

	// stores builds the committed pair for one tick: every cell in an explored
	// column carries the local player's word bit, and every cell in a
	// currently-sighted column carries a nonzero byte refcount [03 §3.1].
	stores := func(explored, sighted []int) ([]uint16, []uint8) {
		word := make([]uint16, grid*grid)
		current := make([]uint8, grid*grid)
		for y := 0; y < grid; y++ {
			for _, x := range explored {
				word[y*grid+x] = 1 << 0
			}
			for _, x := range sighted {
				current[y*grid+x] = 1
			}
		}
		return word, current
	}
	compose := func(explored, sighted []int) *render.RadarSurface {
		t.Helper()
		word, current := stores(explored, sighted)
		svc.RebuildMapped(word, current)
		if !svc.RebuildFinal(layout, 1024, 1024, nil, nil, 0, 0, 0) {
			t.Fatal("FINAL rebuild produced no surface")
		}
		return svc.Final()
	}
	check := func(surf *render.RadarSurface, want [grid]byte, when string) {
		t.Helper()
		if surf == nil {
			t.Fatalf("%s: no surface", when)
		}
		for y := 0; y < grid; y++ {
			for x := 0; x < grid; x++ {
				if got := surf.Bits[y*grid+x]; got != want[x] {
					t.Fatalf("%s: cell %d,%d = %d, want %d", when, x, y, got, want[x])
				}
			}
		}
	}

	// Tick one: the observer sights column 0 only.
	check(compose([]int{0}, []int{0}),
		[grid]byte{terrainIndex, fogIndex, fogIndex, fogIndex}, "first sighting")

	// Tick two: the observer has moved to column 1. Column 0 is explored but no
	// longer sighted, so it must stay drawn through the remap rather than
	// revert to the fog fill; column 2 was never explored and stays filled.
	check(compose([]int{0, 1}, []int{1}),
		[grid]byte{dimIndex, terrainIndex, fogIndex, fogIndex}, "after the observer moved")

	// Tick three: nothing is sighted any more — a dead observer stops sweeping
	// and its already-mapped bits persist [03 §3.2].
	final := compose([]int{0, 1}, nil)
	check(final, [grid]byte{dimIndex, dimIndex, fogIndex, fogIndex}, "after the observer died")

	// The explored-but-fogged tint must survive the draw into the framebuffer,
	// not just the composite.
	c := &Client{width: camera.MinimapLongSide, height: camera.MinimapLongSide,
		indexed: make([]uint8, camera.MinimapLongSide*camera.MinimapLongSide)}
	dst := hud.Rect{X1: 0, Y1: 0, X2: camera.MinimapLongSide - 1, Y2: camera.MinimapLongSide - 1}
	c.DrawMinimapLayout(final, dst, layout)
	seen := map[uint8]bool{}
	for _, v := range c.indexed {
		seen[v] = true
	}
	for _, want := range []byte{dimIndex, fogIndex} {
		if !seen[want] {
			t.Fatalf("drawn minimap never wrote index %d; got %v", want, seen)
		}
	}
}
