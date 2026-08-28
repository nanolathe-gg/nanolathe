package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

func viewportTerrain(height func(int, int) uint8, sea uint8) *world.Terrain {
	const cells = 64
	attrs := make([]formats.TNTAttribute, cells*cells)
	for z := 0; z < cells; z++ {
		for x := 0; x < cells; x++ {
			attrs[z*cells+x] = formats.TNTAttribute{Height: height(x, z), Feature: world.PlotFeatureNone}
		}
	}
	return &world.Terrain{CellW: cells, CellH: cells, Plot: world.ExpandPlot(attrs, cells, cells), SeaLevel: sea}
}
func TestViewportTransformRoundTripFlat(t *testing.T) {
	ter := viewportTerrain(func(int, int) uint8 { return 40 }, 0)
	worldPoints := []struct {
		x, z int32
	}{
		{8, 8}, {24, 24}, // centers of adjacent odd/even-indexed cells [03 §2.1]
		{0, 0}, {16, 16}, {160, 200}, {500, 300}, {900, 700}, {1023, 1023},
	}
	for i, pan := range []struct{ x, z int32 }{{0, 0}, {64, 48}, {256, 160}} {
		cam := &camera.Camera{X: pan.x, Z: pan.z, ViewW: 512, ViewH: 416, MapW: 1024, MapH: 1024}
		tr := NewViewportTransform(cam, ter, 640, 480)
		for _, point := range worldPoints {
			wx := numeric.Fixed(int64(point.x) << 16)
			wz := numeric.Fixed(int64(point.z) << 16)
			vp := tr.OrderTargetToViewport(wx, numeric.Fixed(40<<16), wz)
			if !tr.ViewportContains(vp) {
				continue // this point is outside this camera pan
			}
			gx, gy, gz, ok := tr.OrderTargetFromViewport(vp)
			if !ok {
				t.Fatalf("pan %d point %v: projected world point became HUD", i, point)
			}
			if got := int32(gx >> 16); got != point.x {
				t.Errorf("pan %d point %v: x=%d, want %d", i, point, got, point.x)
			}
			if got := int32(gy >> 16); got != 40 {
				t.Errorf("pan %d point %v: y=%d, want 40", i, point, got)
			}
			projected := tr.OrderTargetToViewport(gx, gy, gz)
			if dx, dy := projected.X-vp.X, projected.Y-vp.Y; dx < -1 || dx > 1 || dy < -1 || dy > 1 {
				t.Errorf("pan %d point %v: projected residue=(%d,%d)", i, point, dx, dy)
			}
		}
	}
}

func TestViewportTransformZoomProjectionRoundTrip(t *testing.T) {
	ter := viewportTerrain(func(int, int) uint8 { return 40 }, 0)
	cam := &camera.Camera{X: 128, Z: 96, ViewW: 512, ViewH: 416, MapW: 1024, MapH: 1024}
	// Point chosen to stay inside the drawn-chrome viewport (129,32..639,447) at all
	// zoom levels: distance from cam (128,96) is 72 world pixels, so at scale 4
	// the screen offset is 288, well inside the 511-wide viewport [C-1][F-P1-008].
	point := struct{ x, y, z numeric.Fixed }{numeric.Fixed(200 << 16), numeric.Fixed(40 << 16), numeric.Fixed(200 << 16)}
	for _, scale := range []float32{0.25, 0.5, 1, 2, 4} {
		cam.Scale = scale // presentation-only zoom [F-P1-008]
		tr := NewViewportTransform(cam, ter, 640, 480)
		beam := tr.WorldToBeam(point.x, point.y, point.z)
		if got := tr.ViewportToBeam(tr.BeamToViewport(beam)); got != beam {
			t.Errorf("scale %v: beam round trip=%+v, want %+v", scale, got, beam)
		}
		viewport := tr.WorldToViewport(point.x, point.y, point.z)
		if !tr.ViewportContains(viewport) {
			t.Fatalf("scale %v: known point %+v left viewport at %+v", scale, point, viewport)
		}
		gotX, gotY, gotZ, ok := tr.ViewportToWorldOnTerrain(viewport)
		if !ok {
			t.Fatalf("scale %v: projected point was classified as HUD", scale)
		}
		const tolerance = int64(2 << 16) // two map pixels cover integer/zoom truncation [03 §2.1][07 §8]
		if d := int64(gotX - point.x); d < -tolerance || d > tolerance {
			t.Errorf("scale %v: x error=%d, tolerance=%d", scale, d, tolerance)
		}
		if d := int64(gotZ - point.z); d < -tolerance || d > tolerance {
			t.Errorf("scale %v: z error=%d, tolerance=%d", scale, d, tolerance)
		}
		if gotY != point.y {
			t.Errorf("scale %v: y=%d, want authored height %d", scale, gotY>>16, point.y>>16)
		}
	}
}

func TestViewportTransformRoundTripSlopeAndHeight(t *testing.T) {
	ter := viewportTerrain(func(x, z int) uint8 { return uint8((x + 2*z) % 64) }, 20)
	cam := &camera.Camera{X: 160, Z: 96, ViewW: 512, ViewH: 416, MapW: 1024, MapH: 1024}
	tr := NewViewportTransform(cam, ter, 640, 480)
	for _, point := range []struct{ x, z int32 }{{176, 176}, {320, 240}, {640, 480}} {
		wx, wz := numeric.Fixed(int64(point.x)<<16), numeric.Fixed(int64(point.z)<<16)
		height := ter.HeightAt(wx, wz)
		vp := tr.WorldToViewport(wx, height, wz)
		if !tr.ViewportContains(vp) {
			continue
		}
		gx, gy, gz, ok := tr.ViewportToWorldOnTerrain(vp)
		if !ok {
			t.Fatalf("point %v was rejected as HUD", point)
		}
		if int32(gy>>16) < int32(ter.SeaLevel) {
			t.Errorf("point %v: picked below sea level: %d", point, gy>>16)
		}
		projected := tr.WorldToViewport(gx, gy, gz)
		if dy := projected.Y - vp.Y; dy < -4 || dy > 4 {
			t.Errorf("point %v: sloped projection residue=%d", point, dy)
		}
	}
}

func TestViewportLogicalCursorCoordinates(t *testing.T) {
	tr := NewViewportTransform(nil, nil, 0, 0)
	if tr.LogicalWidth != 640 || tr.LogicalHeight != 480 {
		t.Fatalf("defaults=(%d,%d), want (640,480)", tr.LogicalWidth, tr.LogicalHeight)
	}
	// Battle viewport is the drawn-chrome region [129,32]..[639,447] [07 §6][07 §8] C-3.
	if tr.Viewport != (ViewportRect{Left: 129, Top: 32, Right: 639, Bottom: 447}) {
		t.Fatalf("battle viewport=%+v, want drawn-chrome 511x416 rectangle at 129,32", tr.Viewport)
	}
	if tr.ViewportToHUDRegion(Point{X: 639, Y: 447}) != RegionWorld {
		t.Fatal("last battle pixel was classified as HUD")
	}
	if tr.ViewportToHUDRegion(Point{X: 640, Y: 447}) != RegionHUD || tr.ViewportToHUDRegion(Point{X: 129, Y: 448}) != RegionHUD {
		t.Fatal("HUD boundary reached the world region")
	}
}

func TestHUDClicksNeverReachWorld(t *testing.T) {
	tr := NewViewportTransform(nil, viewportTerrain(func(int, int) uint8 { return 10 }, 0), 640, 480)
	// Viewport is 129,32..639,447; points outside that are HUD [C-3][07 §8].
	for _, p := range []Point{{X: 128, Y: 100}, {X: 10, Y: 31}, {X: 640, Y: 100}, {X: 639, Y: 448}, {X: -1, Y: 10}} {
		if _, _, _, ok := tr.OrderTargetFromViewport(p); ok {
			t.Fatalf("HUD point %+v produced an order target", p)
		}
	}
}

func TestViewportRectToWorldSortsEndpoints(t *testing.T) {
	tr := NewViewportTransform(&camera.Camera{}, nil, 640, 480)
	// Use a rect fully inside the drawn-chrome viewport 129,32..639,447 [C-3].
	// World = viewport - (129,32) + cam (cam 0) [03 §2.5][07 §8].
	rect := ViewportRect{Left: 329, Top: 332, Right: 149, Bottom: 132}
	minX, minZ, maxX, maxZ, ok := tr.ViewportRectToWorld(rect)
	if !ok {
		t.Fatal("in-viewport selection was rejected")
	}
	if minX > maxX || minZ > maxZ {
		t.Fatalf("selection was not sorted: (%d,%d)..(%d,%d)", minX, minZ, maxX, maxZ)
	}
	// 149,132 => world 20,100 ; 329,332 => world 200,300 after subtracting chrome origin 129,32.
	if minX != numeric.Fixed(20<<16) || minZ != numeric.Fixed(100<<16) || maxX != numeric.Fixed(200<<16) || maxZ != numeric.Fixed(300<<16) {
		t.Fatalf("selection world bounds=(%d,%d)..(%d,%d) want 20,100..200,300", minX, minZ, maxX, maxZ)
	}
}

func TestViewportNegativeProjectionUsesArithmeticShift(t *testing.T) {
	tr := NewViewportTransform(&camera.Camera{}, nil, 640, 480)
	beam := tr.WorldToBeam(numeric.Fixed(-1), 0, numeric.Fixed(-1))
	if beam != (Point{X: 127, Y: 31}) {
		t.Fatalf("negative beam point=%+v, want (127,31)", beam)
	}
	// With drawn-chrome viewport at 129,32, beam 127 maps to 128 (=127-128+129) [C-3].
	if got := tr.BeamToViewport(beam); got != (Point{X: 128, Y: 31}) {
		t.Fatalf("negative viewport point=%+v, want (128,31)", got)
	}
}

func TestViewportSurfaceProjectionSharesBeamOrigin(t *testing.T) {
	cam := &camera.Camera{X: 40, Z: 24, ViewW: 512, ViewH: 416, MapW: 1024, MapH: 1024}
	v := NewViewportTransform(cam, nil, 640, 480)
	beam := v.WorldToBeam(numeric.Fixed(40<<16), 0, numeric.Fixed(24<<16))
	surface := v.WorldToSurface(numeric.Fixed(40<<16), 0, numeric.Fixed(24<<16))
	if beam != (Point{X: camera.OriginX, Y: camera.OriginY}) {
		t.Fatalf("beam origin=%+v", beam)
	}
	if surface != (Point{}) {
		t.Fatalf("surface origin=%+v", surface)
	}
	if got := v.SurfaceToBeam(surface); got != beam {
		t.Fatalf("surface/beam round trip=%+v want %+v", got, beam)
	}
}
