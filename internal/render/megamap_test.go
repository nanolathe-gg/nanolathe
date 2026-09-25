package render

import "testing"

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
	ComposeMegamapFog(dst, picture, 2, 2, word, current, 2, 2, 0, 0, &gray)
	if dst[0] != MegamapFogBlack || dst[1] != 2^0x80 || dst[2] != 3 || dst[3] != 4 {
		t.Fatalf("fog = %v", dst)
	}
	// Sea level shifts the rows up by sea/20, clamped at zero.
	ComposeMegamapFog(dst, picture, 2, 2, word, current, 2, 2, 0, 40, &gray)
	if dst[2] != MegamapFogBlack || dst[3] != 4^0x80 {
		t.Fatalf("sea-offset fog = %v", dst)
	}
}
