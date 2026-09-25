package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Ring minimums compare strictly, and the radar-jammer ring reads the radar
// minimum [draw-engine-interface "Rings"].
func TestMegamapRingThresholds(t *testing.T) {
	th := MegamapRingThresholds{Radar: 100, Sonar: 50, SonarJam: 10, AntiNuke: 600}
	r, s, rj, sj := MegamapSensorRings(th, 100, 51, 101, 10)
	if r || !s || !rj || sj {
		t.Fatalf("sensor rings = %v %v %v %v", r, s, rj, sj)
	}
	if _, draw := MegamapInterceptorRing(th, 600); draw {
		t.Fatal("interceptor coverage equal to the minimum drew")
	}
	if radius, draw := MegamapInterceptorRing(th, 601); !draw || radius != 89 {
		t.Fatalf("interceptor radius = %d %v, want coverage-512", radius, draw)
	}
	if got := MegamapRingRadius(1000, 300, 1000); got != 300 {
		t.Fatalf("ring radius = %d", got)
	}
}

func TestBlitMegamapIconRecolorsAndShiftsAtLeftEdge(t *testing.T) {
	icon := &MegamapIcon{W: 3, H: 1, Pix: []byte{9, 34, 255}, Role: []MegamapPixelRole{MegamapPixelEmpty, MegamapPixelFill, MegamapPixelSelected}, Hover: 84}
	dst := make([]byte, 8)
	BlitMegamapIcon(dst, 8, 1, 8, icon, -2, 0, MegamapIconNormal, 227)
	// Left clipping moves the start to zero without skipping source pixels;
	// unselected art drops the selection ink.
	if dst[0] != 0 || dst[1] != 227 || dst[2] != 0 {
		t.Fatalf("normal = %v", dst[:3])
	}
	BlitMegamapIcon(dst, 8, 1, 8, icon, 0, 0, MegamapIconSelected, 227)
	if dst[2] != 255 {
		t.Fatalf("selected ink = %d", dst[2])
	}
	BlitMegamapIcon(dst, 8, 1, 8, icon, 4, 0, MegamapIconHovered, 227)
	if dst[6] != 84 {
		t.Fatalf("hover ink = %d", dst[6])
	}
	// The right edge clips at the four-aligned pitch.
	clear(dst)
	BlitMegamapIcon(dst, 8, 1, 4, icon, 2, 0, MegamapIconSelected, 227)
	if dst[3] != 227 || dst[4] != 0 {
		t.Fatalf("pitch clip = %v", dst)
	}
}

func TestComposeMegamapFogTable(t *testing.T) {
	var gray [256]byte
	for i := range gray {
		gray[i] = byte(i) ^ 0x80
	}
	picture := []byte{1, 2, 3, 4}
	dst := make([]byte, 4)
	// Two by two LOS cells: unmapped, mapped without LOS, mapped with LOS.
	word := []uint16{0, 1, 1, 1}
	current := []uint8{1, 0, 1, 1}
	ComposeMegamapFog(dst, picture, 2, 2, word, current, 2, 2, 2, 2, 0, 0, &gray)
	if dst[0] != MegamapFogBlack || dst[1] != 2^0x80 || dst[2] != 3 || dst[3] != 4 {
		t.Fatalf("fog = %v", dst)
	}
	// Sea level shifts the rows up by sea/20, clamped at zero.
	ComposeMegamapFog(dst, picture, 2, 2, word, current, 2, 2, 2, 2, 0, 40, &gray)
	if dst[2] != MegamapFogBlack || dst[3] != 4^0x80 {
		t.Fatalf("sea-offset fog = %v", dst)
	}
}

// The fog samples only the play-area span of the LOS grid, so a grid with an
// unmapped last column leaves a picture over a one-column span clear.
func TestComposeMegamapFogSamplesTheSpan(t *testing.T) {
	var gray [256]byte
	picture := []byte{7, 7}
	dst := make([]byte, 2)
	word := []uint16{1, 0}
	current := []uint8{1, 1}
	ComposeMegamapFog(dst, picture, 2, 1, word, current, 2, 1, 1, 1, 0, 0, &gray)
	if dst[0] != 7 || dst[1] != 7 {
		t.Fatalf("fog over a one-cell span = %v", dst)
	}
}

// The terrain picture point-samples raw tile indices: output column c reads
// source column trunc(c × extent/width) in single precision, and a picture
// larger than the extent magnifies rather than reading past it
// [draw-engine-interface "Terrain picture"] (host choice for the fractional
// step, DESIGN_INTERFACE_HUD_INPUT §3.15).
func TestBuildMegamapPicturePointSamples(t *testing.T) {
	// Two by two tiles; tile n holds byte 16n+column/8 in each row.
	terrain := &world.Terrain{CellW: 4, CellH: 4, TileIndices: []uint16{0, 1, 2, 3}, TileSet: make([][1024]byte, 4)}
	for n := range terrain.TileSet {
		for i := range terrain.TileSet[n] {
			terrain.TileSet[n][i] = byte(16*n + (i%32)/8)
		}
	}
	// A 64×32 extent (two tiles by one) sampled at width 3: step 21.333,
	// columns 0, 21, 42 — tile 0 bytes 0 and 2, tile 1 byte 1.
	if got := MegamapSampleSteps(64, 3); got[0] != 0 || got[1] != 21 || got[2] != 42 {
		t.Fatalf("steps = %v", got)
	}
	pic := BuildMegamapPicture(terrain, 64, 32, 3, 1)
	if pic[0] != 0 || pic[1] != 2 || pic[2] != 16+1 {
		t.Fatalf("picture = %v, want raw indices 0, 2, 17", pic)
	}
	// Magnified: 128 columns over a 64-pixel extent repeat each source
	// column twice and never leave it.
	if got := MegamapSampleSteps(64, 128); got[1] != 0 || got[2] != 1 || got[127] != 63 {
		t.Fatalf("magnified steps = %v", got[:4])
	}
}

// The selection box outline shows only while both extents exceed 8 pixels.
func TestMegamapBoxOutlineGate(t *testing.T) {
	if MegamapBoxOutlineShown(10, 10, 18, 30) || MegamapBoxOutlineShown(10, 10, 30, 2) {
		t.Fatal("an 8-pixel extent drew the box")
	}
	if !MegamapBoxOutlineShown(10, 10, 19, 1) {
		t.Fatal("9 by 9 pixels did not draw the box")
	}
}

// The placement ghost is the footprint centred on the point and moved back
// inside the image.
func TestMegamapFootprintRect(t *testing.T) {
	// 4×2 cells at half scale: 32×16 pixels centred on (50, 40).
	if x0, y0, x1, y1 := MegamapFootprintRect(50, 40, 4, 2, 0.5, 0.5, 200, 100); x0 != 34 || y0 != 32 || x1 != 65 || y1 != 47 {
		t.Fatalf("rect = %d,%d %d,%d", x0, y0, x1, y1)
	}
	// Crossing the right and top edges moves it back inside.
	if x0, y0, x1, y1 := MegamapFootprintRect(195, 2, 4, 2, 0.5, 0.5, 200, 100); x0 != 168 || y0 != 0 || x1 != 199 || y1 != 15 {
		t.Fatalf("edge rect = %d,%d %d,%d", x0, y0, x1, y1)
	}
}

// The projectile cell truncates toward zero with y/2 truncated first and
// passes a coordinate equal to the grid size; admission is owner/ally, then
// current sight, then Unmapped, then the mapping word
// [draw-engine-interface "Projectiles"].
func TestMegamapProjectileGate(t *testing.T) {
	if cx, cz, ok := MegamapProjectileCell(-31, 3, 95, 4, 4); !ok || cx != 0 || cz != 2 {
		t.Fatalf("cell = %d,%d %v; want 0, (95-1)/32 = 2", cx, cz, ok)
	}
	if _, _, ok := MegamapProjectileCell(128, 0, 128, 4, 4); !ok {
		t.Fatal("a coordinate equal to the grid size was rejected")
	}
	if _, _, ok := MegamapProjectileCell(160, 0, 0, 4, 4); ok {
		t.Fatal("a coordinate past the grid size passed")
	}
	if _, _, ok := MegamapProjectileCell(0, 0, -40, 4, 4); ok {
		t.Fatal("a negative cell passed")
	}
	s := MegamapProjectileSight{W: 2, H: 2, Current: []uint8{0, 1, 0, 0}, Mapped: []uint16{0, 2, 0, 0}, Viewer: 1}
	if !MegamapProjectileAdmitted(true, 0, 0, s) {
		t.Fatal("an allied projectile was rejected")
	}
	s.Mode = 3
	if MegamapProjectileAdmitted(false, 0, 0, s) || !MegamapProjectileAdmitted(false, 1, 0, s) {
		t.Fatal("current sight did not decide")
	}
	s.Mode = 1
	if !MegamapProjectileAdmitted(false, 0, 1, s) {
		t.Fatal("Unmapped did not admit everything")
	}
	s.Mode = 0
	if MegamapProjectileAdmitted(false, 0, 0, s) || !MegamapProjectileAdmitted(false, 1, 0, s) {
		t.Fatal("the mapping word did not decide")
	}
	// A cell equal to the width reads the next row's first cell; past the
	// grid is not read.
	if MegamapProjectileAdmitted(false, 2, 1, s) {
		t.Fatal("a cell past the grid was admitted")
	}
}
