package gpurender

import (
	"math"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestMarkerShaderCompiles(t *testing.T) {
	if _, err := newMarkerShader(); err != nil {
		t.Fatal(err)
	}
}

func TestMarkerAtlasRejectsMalformedCoverage(t *testing.T) {
	for _, a := range []*drawlist.MarkerAtlas{nil, {}, {Width: math.MaxInt, Height: 1}, {Width: 1, Height: 1, Pixels: []byte{255, 0, 1, 0}}, {Width: 1, Height: 1, Pixels: []byte{255}}} {
		if validMarkerAtlas(a) {
			t.Fatalf("accepted malformed atlas %+v", a)
		}
	}
	if !validMarkerAtlas(&drawlist.MarkerAtlas{Width: 1, Height: 1, Pixels: []byte{80, 70, 60, 45}}) {
		t.Fatal("rejected disjoint coverage")
	}
}

func TestIconBoundsClipsWithoutStretching(t *testing.T) {
	m := drawlist.Marker{IconAtlas: &drawlist.MarkerAtlas{Width: 64, Height: 64}, IconRect: drawlist.Rect{X: 32, Y: 16, W: 32, H: 32}, X: 10, Y: 10, Size: 20, Alpha: 255, HasClip: true, Clip: drawlist.Rect{X: 5, Y: 3, W: 12, H: 15}}
	d, s, ok := iconBounds(&m, 100, 100)
	if !ok || d != [4]int{5, 3, 17, 18} {
		t.Fatalf("bounds=%v, valid=%v", d, ok)
	}
	want := [4]float32{40, 20.8, 59.2, 44.8}
	for i := range s {
		if math.Abs(float64(s[i]-want[i])) > 0.0001 {
			t.Fatalf("UV=%v want=%v", s, want)
		}
	}
	m.IconRect.X = math.MaxInt32
	if _, _, ok := iconBounds(&m, 100, 100); ok {
		t.Fatal("accepted overflowing rectangle")
	}
}

func TestMarkerSourceResetReleasesAtlas(t *testing.T) {
	img := &ebiten.Image{}
	a := &drawlist.MarkerAtlas{}
	r := &Renderer{markerAtlases: [4]markerAtlasUpload{{source: a, image: img, used: 1}}}
	count := 0
	r.resetSources(func(got *ebiten.Image) {
		if got != img {
			t.Fatal("unexpected image")
		}
		count++
	})
	if count != 1 || r.markerAtlases != [4]markerAtlasUpload{} {
		t.Fatal("atlas source lifetime leaked")
	}
}
