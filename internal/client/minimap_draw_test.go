package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// applyMinimapIntentForTest is the production wiring in miniature: the lens
// produces the clicked world point and the canonical camera writer recenters
// on it [07 R-CAM-01 §11].
func applyMinimapIntentForTest(cam *camera.Camera, layout camera.Minimap, dst hud.Rect, playW, playH, mouseX, mouseY int32) bool {
	if cam == nil {
		return false
	}
	wx, wz, ok := MinimapPointerWorld(layout, dst, playW, playH, mouseX, mouseY)
	if ok {
		cam.JumpToBattleViewCenter(wx, wz)
	}
	return ok
}

// TestMinimapPointerWorldRecentersOnTheClick locks the correction of
// [07 R-CAM-01 §11]: the clicked map point becomes the view *centre*, not the
// camera origin. The superseded reading — "the lens writes the projected world
// point directly as the camera origin" — is the pointer's world position, and
// implementing it as the camera write put the click at the view's top-left.
func TestMinimapPointerWorldRecentersOnTheClick(t *testing.T) {
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	dst := hud.Rect{X1: 0, Y1: 0, X2: 125, Y2: 125}
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}

	mouseX, mouseY := int32(60), int32(60)
	if !applyMinimapIntentForTest(cam, m, dst, playW, playH, mouseX, mouseY) {
		t.Fatal("a click inside the radar rectangle must be consumed")
	}
	wx, wz := m.ToWorldPlay(mouseX, mouseY, playW, playH)
	viewW, viewH := cam.BattleView()
	wantX, wantZ := wx-camera.OriginX-viewW/2, wz-camera.OriginY-viewH/2
	want := &camera.Camera{X: wantX, Z: wantZ, ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	want.Clamp()
	if cam.X != want.X || cam.Z != want.Z {
		t.Fatalf("recentred camera = %d,%d, want %d,%d (world point %d,%d)", cam.X, cam.Z, want.X, want.Z, wx, wz)
	}
	// The recenter must be a real subtraction: the direct-origin reading would
	// have landed the camera on the world point itself.
	if cam.X == wx && cam.Z == wz {
		t.Fatal("camera origin equals the clicked world point: the half-viewport recenter is missing")
	}
}

// TestMinimapPointerWorldIsTheLensConversion locks step 1 of the frame: the
// pointer's world position over the minimap has no half-viewport term
// [07 R-CAM-01 §11]. It is the point an order lands on.
func TestMinimapPointerWorldIsTheLensConversion(t *testing.T) {
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	dst := hud.Rect{X1: 200, Y1: 100, X2: 325, Y2: 225}
	mouseX, mouseY := int32(260), int32(160)
	wx, wz, ok := MinimapPointerWorld(m, dst, playW, playH, mouseX, mouseY)
	if !ok {
		t.Fatal("pointer inside the radar rectangle was not classified")
	}
	cx, cy, _ := m.DisplayToCanvas(mouseX, mouseY, dst.X1, dst.Y1, 126, 126)
	wantX, wantZ := m.ToWorldPlay(cx, cy, playW, playH)
	if wx != wantX || wz != wantZ {
		t.Fatalf("lens conversion = %d,%d, want %d,%d", wx, wz, wantX, wantZ)
	}
	// Outside the destination rectangle there is no minimap pointer at all.
	if _, _, ok := MinimapPointerWorld(m, dst, playW, playH, dst.X1-1, mouseY); ok {
		t.Fatal("a pointer outside the destination must not classify as minimap")
	}
	// The letterbox bars are inside the canvas but outside the fitted radar
	// rectangle, and only the fitted rectangle selects the lens [07 §10].
	tall := camera.LayoutMinimap(400, 1000)
	if tall.PadX <= 0 {
		t.Fatalf("expected a letterboxed layout, got %+v", tall)
	}
	if _, _, ok := MinimapPointerWorld(tall, dst, playW, playH, dst.X1, dst.Y1+60); ok {
		t.Fatal("a pointer on the letterbox bar must not classify as minimap")
	}
}

func TestMinimapCameraCaptureIntentClampsAnAlreadyCapturedPointer(t *testing.T) {
	playW, playH := int32(992), int32(896)
	m := camera.LayoutMinimap(playW, playH)
	dst := hud.Rect{X1: 200, Y1: 100, X2: 325, Y2: 225}
	if _, _, ok := MinimapPointerWorld(m, dst, playW, playH, dst.X2+40, dst.Y2+50); ok {
		t.Fatal("new camera latch admitted a pointer outside the radar")
	}
	got, ok := MinimapCameraCaptureIntent(m, dst, playW, playH, dst.X2+40, dst.Y2+50)
	if !ok {
		t.Fatal("captured camera pointer outside the radar was rejected")
	}
	cx := (dst.X2 + 40 - dst.X1) * camera.MinimapLongSide / 126
	cy := (dst.Y2 + 50 - dst.Y1) * camera.MinimapLongSide / 126
	wantX, wantZ := m.ToWorldPlay(cx, cy, playW, playH)
	if got.X != wantX || got.Z != wantZ {
		t.Fatalf("captured intent = %d,%d, want live-record lens %d,%d", got.X, got.Z, wantX, wantZ)
	}
}

// TestDrawMinimapViewportRectStrokesOneRectangleOutline locks [03 R-MM-01 §1]:
// the viewport marker is a one-pixel outline, never a fill, clipped to the
// destination.
func TestDrawMinimapViewportRectStrokesOneRectangleOutline(t *testing.T) {
	c := &Client{width: 64, height: 64, indexed: make([]uint8, 64*64)}
	dst := hud.Rect{X1: 0, Y1: 0, X2: 31, Y2: 31}
	c.DrawMinimapViewportRect(dst, hud.Rect{X1: 4, Y1: 6, X2: 12, Y2: 14}, 9)
	c.replayForTest()
	for y := int32(0); y < 64; y++ {
		for x := int32(0); x < 64; x++ {
			onEdge := (x >= 4 && x <= 12 && (y == 6 || y == 14)) || (y >= 6 && y <= 14 && (x == 4 || x == 12))
			got := c.indexed[int(y)*c.width+int(x)]
			if onEdge && got != 9 {
				t.Fatalf("edge pixel %d,%d = %d, want 9", x, y, got)
			}
			if !onEdge && got != 0 {
				t.Fatalf("pixel %d,%d = %d: the rectangle must be an outline, not a fill", x, y, got)
			}
		}
	}
	// Clipping is to the destination, not to the framebuffer.
	c2 := &Client{width: 64, height: 64, indexed: make([]uint8, 64*64)}
	c2.DrawMinimapViewportRect(dst, hud.Rect{X1: -10, Y1: -10, X2: 40, Y2: 40}, 9)
	c2.replayForTest()
	for y := int32(0); y < 64; y++ {
		for x := int32(0); x < 64; x++ {
			if c2.indexed[int(y)*c2.width+int(x)] != 0 {
				t.Fatalf("a rectangle straddling the destination wrote outside it at %d,%d", x, y)
			}
		}
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
	cam := &camera.Camera{ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	if !applyMinimapIntentForTest(cam, layout, dst, playW, playH, displayX, displayY) {
		t.Fatal("canonical minimap click was not consumed")
	}
	wx, wz := layout.ToWorldPlay(clickX, clickY, playW, playH)
	want := &camera.Camera{ViewW: 640, ViewH: 480, MapW: playW, MapH: playH}
	want.JumpToBattleViewCenter(wx, wz)
	if cam.X != want.X || cam.Z != want.Z {
		t.Fatalf("click camera mismatch: got %d,%d want %d,%d", cam.X, cam.Z, want.X, want.Z)
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
	c.replayForTest()
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
	c.replayForTest()
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
