package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestWakeAdmissionWaterVisibilityAndMissingArt(t *testing.T) {
	in := WakeInput{MinX: 0, MinZ: 0, MaxX: numeric.Fixed(3 << 16), MaxZ: numeric.Fixed(2 << 16), Water: true, Visible: true, Graphic: "wake"}
	w, ok := BuildWakeRect(in)
	if !ok || w.Graphic != "wake" {
		t.Fatalf("wake admission = %+v, %v", w, ok)
	}
	for _, bad := range []WakeInput{{Water: false, Visible: true, MinX: 0, MaxX: 1, MaxZ: 1}, {Water: true, Visible: false, MinX: 0, MaxX: 1, MaxZ: 1}} {
		if _, ok := BuildWakeRect(bad); ok {
			t.Fatal("non-water or hidden wake admitted")
		}
	}
	// A missing optional graphic is admitted as metadata but rasterizes to no
	// pixels; no compatibility fallback is synthesized [03 §5.7][I9].
	w.Graphic = ""
	called := false
	RasterizeWake(w, func(int32, int32, uint8) { called = true })
	if called {
		t.Fatal("missing wake art produced pixels")
	}
}

func TestWakeRasterizeRowMajorAndClip(t *testing.T) {
	w := WakeRect{MinX: 0, MinZ: 0, MaxX: numeric.Fixed(3 << 16), MaxZ: numeric.Fixed(2 << 16), Graphic: "wake", Palette: 6}
	w, ok := ClipWakeRect(w, numeric.Fixed(1<<16), 0, numeric.Fixed(3<<16), numeric.Fixed(2<<16))
	if !ok || w.MinX != numeric.Fixed(1<<16) {
		t.Fatalf("clip = %+v, %v", w, ok)
	}
	var got [][2]int32
	RasterizeWake(w, func(x, z int32, p uint8) {
		if p != 6 {
			t.Fatalf("palette = %d, want 6", p)
		}
		got = append(got, [2]int32{x, z})
	})
	want := [][2]int32{{1, 0}, {2, 0}, {1, 1}, {2, 1}}
	if len(got) != len(want) {
		t.Fatalf("raster count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("raster[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
