package main

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"testing"
)

// The crop excludes padding without using its colour as a sentinel. The
// mapper's fixed-point corner steps and exclusive clip retain the black
// trailing canvas edges [07 R-FE-01 §5][03 R-RAST-01 §1].
func TestMapPreviewCropFitAndClear(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		width, height                          uint32
		usedW, usedH, left, top, right, bottom int
	}{
		{"tall", 6, 24, 3, 12, 4, 0, 7, 11},
		{"wide", 18, 12, 12, 3, 0, 4, 11, 7},
		{"square usable terrain", 10, 16, 12, 12, 0, 0, 11, 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raster := make([]byte, 12*12)
			for i := range raster {
				raster[i] = 100
			}
			for y := 0; y < tc.usedH; y++ {
				for x := 0; x < tc.usedW; x++ {
					raster[y*12+x] = 37
				}
			}
			d := &retailMapData{tnt: &formats.TNT{Width: tc.width, Height: tc.height, MinimapWidth: 12, MinimapHeight: 12, Minimap: raster}}
			pixels := retailMapPreviewCanvas(d.tnt, 12, 12)
			for y := 0; y < 12; y++ {
				for x := 0; x < 12; x++ {
					want := byte(0)
					if x >= tc.left && x < tc.right && y >= tc.top && y < tc.bottom {
						want = 37
					}
					if got := pixels[y*12+x]; got != want {
						t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestMapPreviewDividesCornerSpanBeforeStepping(t *testing.T) {
	raster := make([]byte, 9*9)
	for y := 0; y < 9; y++ {
		for x := 0; x < 9; x++ {
			raster[y*9+x] = byte(1 + y*9 + x)
		}
	}
	d := &retailMapData{tnt: &formats.TNT{Width: 10, Height: 16, MinimapWidth: 9, MinimapHeight: 9, Minimap: raster}}
	got := retailMapPreviewCanvas(d.tnt, 12, 12)
	// Eight source intervals over twelve destination intervals produce a
	// truncated 16.16 step: the third destination step is still source 1.
	if got[3] != 2 || got[3*12] != 10 {
		t.Fatalf("corner steps = %d/%d, want 2/10", got[3], got[3*12])
	}
}

// The surface painter begins at source (1,1), truncates its fixed step,
// and stops before the gadget's trailing edges [07 R-FE-01 §5].
func TestMapPreviewSurfaceInterior(t *testing.T) {
	raster := make([]byte, 16*16)
	for i := range raster {
		raster[i] = byte(i)
	}
	d := &retailMapData{tnt: &formats.TNT{Width: 10, Height: 16, MinimapWidth: 16, MinimapHeight: 16, Minimap: raster}}
	got := d.previewPixels(4, 4)
	want := []byte{51, 51, 55, 51, 51, 55, 115, 115, 119}
	if len(got) != len(want) {
		t.Fatalf("surface length %d, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("surface pixel %d = %d, want %d", i, got[i], v)
		}
	}
}
