package gpurender

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

const (
	featureDeviceWidth      = 80
	featureDeviceHeight     = 20
	featureDeviceBackground = byte(201)
	featureDeviceShadow     = byte(37)
	featureDeviceBody       = byte(41)
	featureDeviceShadowALP  = byte(88)
	featureDeviceBodyALP    = byte(99)
)

// Feature shadows reuse the existing keyed and ALP-tinted GPU stages. The
// producer records the final top-left, while drawTint accepts an anchor; both
// routes must therefore submit identical destination/source geometry despite
// nonzero authored offsets [03 §5.3.1][R-REN-03D §4].
func TestFeatureShadowRoutesPreserveAuthoredGeometry(t *testing.T) {
	p := &palette.Tables{}
	p.Alpha[37*256+201] = 88
	r, err := NewChecked(p, 16, 12)
	if err != nil {
		t.Fatalf("compile GPU shaders: %v", err)
	}
	f := &formats.GAFFrame{
		Width: 2, Height: 1, XOffset: 2, YOffset: 1,
		ColorKey: 9, Pixels: []byte{37, 9}, Transparent: []bool{false, true},
	}
	want := [4]ebitenVertexPoint{{3, 4, 0, 0}, {5, 4, 2, 0}, {3, 5, 0, 1}, {5, 5, 2, 1}}
	for _, tc := range []struct {
		name  string
		trans bool
	}{
		{name: "opaque keyed"},
		{name: "translucent ALP", trans: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r.Sprite(drawlist.Sprite{Frame: f, X: 3, Y: 4, Kind: drawlist.BlitFeatureShadow, Trans: tc.trans})
			if len(r.verts) != 4 {
				t.Fatalf("submitted %d vertices, want 4", len(r.verts))
			}
			for i, v := range r.verts {
				got := ebitenVertexPoint{v.DstX, v.DstY, v.SrcX, v.SrcY}
				if got != want[i] {
					t.Fatalf("vertex %d = %+v, want %+v", i, got, want[i])
				}
			}
		})
	}
}

type ebitenVertexPoint struct {
	dstX, dstY float32
	srcX, srcY float32
}

// checkFeatureShadowDevicePixels runs inside modelFixtureGame's opt-in
// main-thread Ebitengine loop. It verifies the authored feature commands after
// actual shader execution and readback. The first four blocks are static opaque
// shadow, static translucent shadow, static translucent body and an opaque live
// event shadow; the fifth stays empty to represent the producer's disabled
// feature-shadow gate. The producer tests above the GPU package lock that this
// gate remains independent when Shading is off [03 §5.3][03 R-RAST-01 §6]
// [R-REN-03D §4].
func checkFeatureShadowDevicePixels() error {
	var p palette.Tables
	for i := 0; i < 256; i++ {
		p.Base[i] = [4]byte{byte(i), byte(i), byte(i), 255}
	}
	p.Alpha[int(featureDeviceShadow)*256+int(featureDeviceBackground)] = featureDeviceShadowALP
	p.Alpha[int(featureDeviceBody)*256+int(featureDeviceBackground)] = featureDeviceBodyALP
	r, err := NewChecked(&p, featureDeviceWidth, featureDeviceHeight)
	if err != nil {
		return fmt.Errorf("compile feature-shadow fixture shaders: %w", err)
	}
	frame := func(index byte) *formats.GAFFrame {
		pixels := make([]byte, 8*8)
		transparent := make([]bool, 8*8)
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				i := y*8 + x
				pixels[i] = index
				transparent[i] = x == 7
			}
		}
		return &formats.GAFFrame{
			Width: 8, Height: 8, XOffset: 2, YOffset: 1,
			ColorKey: 9, Pixels: pixels, Transparent: transparent,
		}
	}
	shadow, body := frame(featureDeviceShadow), frame(featureDeviceBody)
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{
		Rect:  drawlist.Rect{W: featureDeviceWidth, H: featureDeviceHeight},
		Index: featureDeviceBackground,
		Style: drawlist.FillSolid,
	})
	// These are the exact records produced after the client has applied the
	// independent option and static/live dispatcher decisions.
	list.RecordSprite(drawlist.Sprite{Frame: shadow, X: 4, Y: 6, Kind: drawlist.BlitFeatureShadow})
	list.RecordSprite(drawlist.Sprite{Frame: shadow, X: 20, Y: 6, Kind: drawlist.BlitFeatureShadow, Trans: true})
	list.RecordSprite(drawlist.Sprite{Frame: body, X: 36, Y: 6, Kind: drawlist.BlitFeatureNormal, Trans: true})
	list.RecordSprite(drawlist.Sprite{Frame: shadow, X: 52, Y: 6, Kind: drawlist.BlitFeatureShadow})
	list.RecordExpand()
	img := r.Execute(&list, featureDeviceWidth, featureDeviceHeight)
	if img == nil {
		return fmt.Errorf("feature-shadow fixture renderer returned no image")
	}
	pixels := make([]byte, featureDeviceWidth*featureDeviceHeight*4)
	img.ReadPixels(pixels)
	check := func(name string, x, y int, want byte) error {
		i := (y*featureDeviceWidth + x) * 4
		if got := pixels[i]; got != want {
			return fmt.Errorf("%s at (%d,%d): red %d, want exact index %d", name, x, y, got, want)
		}
		if pixels[i+1] != want || pixels[i+2] != want || pixels[i+3] != 255 {
			return fmt.Errorf("%s at (%d,%d): RGBA %v, want grayscale index %d", name, x, y, pixels[i:i+4], want)
		}
		return nil
	}
	checks := []struct {
		name   string
		x, y   int
		wanted byte
	}{
		{"opaque keyed source", 4, 6, featureDeviceShadow},
		{"opaque keyed transparent skip", 11, 6, featureDeviceBackground},
		{"translucent shadow ALP", 20, 6, featureDeviceShadowALP},
		{"translucent shadow offset cancellation", 18, 5, featureDeviceBackground},
		{"translucent shadow transparent skip", 27, 6, featureDeviceBackground},
		{"static body ALP", 36, 6, featureDeviceBodyALP},
		{"live event opaque", 52, 6, featureDeviceShadow},
		{"feature option disabled omission", 68, 6, featureDeviceBackground},
	}
	for _, c := range checks {
		if err := check(c.name, c.x, c.y, c.wanted); err != nil {
			return err
		}
	}
	if path := os.Getenv("NANOLATHE_FEATURE_SHADOW_SHOT"); path != "" {
		if err := writeFeatureShadowDeviceShot(path, pixels); err != nil {
			return err
		}
	}
	return nil
}

func writeFeatureShadowDeviceShot(path string, pixels []byte) error {
	const scale = 8
	out := image.NewRGBA(image.Rect(0, 0, featureDeviceWidth*scale, featureDeviceHeight*scale))
	for y := 0; y < featureDeviceHeight; y++ {
		for x := 0; x < featureDeviceWidth; x++ {
			i := (y*featureDeviceWidth + x) * 4
			c := color.RGBA{R: pixels[i], G: pixels[i+1], B: pixels[i+2], A: pixels[i+3]}
			for yy := 0; yy < scale; yy++ {
				for xx := 0; xx < scale; xx++ {
					out.SetRGBA(x*scale+xx, y*scale+yy, c)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create feature-shadow device shot: %w", err)
	}
	if err := png.Encode(f, out); err != nil {
		f.Close()
		return fmt.Errorf("encode feature-shadow device shot: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close feature-shadow device shot: %w", err)
	}
	return nil
}
