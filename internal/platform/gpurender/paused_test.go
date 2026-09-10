package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Registered in the existing main-thread device loop. Compare the whole-frame
// executor with world reuse across a moving/removed foreground and a modal's
// destination-reading shade. The fixture world includes models and shadows.
func checkPausedCompositePixels() error {
	pal := fixturePalette()
	r, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	world := fixtureModelList()
	img := r.Execute(&world, 80, 48)
	background := ebiten.NewImage(80, 48)
	defer background.Deallocate()
	background.DrawImage(img, &ebiten.DrawImageOptions{Blend: ebiten.BlendCopy})
	want, got := make([]byte, 80*48*4), make([]byte, 80*48*4)
	for _, x := range []int32{4, 25, -1} {
		appendForeground := func(l *drawlist.List) {
			if x >= 0 {
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: x, Y: 2, W: 8, H: 12}, Index: 50})
			}
			l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: 2, Y: 3, W: 65, H: 30}, Style: drawlist.FillShadeRect, Level: -12, Pal: &pal})
			l.RecordExpand()
		}
		full := fixtureModelList()
		appendForeground(&full)
		r.Execute(&full, 80, 48).ReadPixels(want)
		var overlay drawlist.List
		appendForeground(&overlay)
		r.ExecuteOver(&overlay, background, 80, 48).ReadPixels(got)
		if !bytes.Equal(got, want) {
			return fmt.Errorf("paused composite at foreground x=%d differs from full-frame replay", x)
		}
		if x == 25 {
			if path := os.Getenv("NANOLATHE_PAUSED_FIXTURE_PNG"); path != "" {
				f, err := os.Create(path)
				if err != nil {
					return err
				}
				err = png.Encode(f, &image.RGBA{Pix: got, Stride: 80 * 4, Rect: image.Rect(0, 0, 80, 48)})
				closeErr := f.Close()
				if err != nil {
					return err
				}
				if closeErr != nil {
					return closeErr
				}
			}
		}
	}
	return nil
}

func TestPausedCompositeDevicePixels(t *testing.T) {
	if os.Getenv("NANOLATHE_GPU_DEVICE_TEST") != "1" {
		t.Skip("set NANOLATHE_GPU_DEVICE_TEST=1 for real-device paused composition")
	}
	if deviceFixtureResult != nil {
		t.Fatal(deviceFixtureResult)
	}
}
