package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestScorchShaderCompiles(t *testing.T) {
	shader, err := newScorchShader()
	if err != nil {
		t.Fatal(err)
	}
	shader.Deallocate()
}

// These lock the authored experiment's compositing contract (GPU design §29),
// rather than claiming that the cosmetic parameters reproduce retail behavior.
func checkScorchDevicePixels() error {
	const w, h = 224, 160
	pal := fixturePalette()
	for i := 64; i < 80; i++ {
		pal.Base[i] = [4]byte{byte(28 + (i-64)/2), byte(51 + i - 64), byte(64 + i - 64), 255}
	}
	for i := 120; i < 144; i++ {
		pal.Base[i] = [4]byte{byte(73 + i - 120), byte(77 + i - 120), byte(52 + (i-120)/2), 255}
	}
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	if r.scorchShader == nil {
		return fmt.Errorf("renderer did not compile its scorch shader")
	}
	ter := waterFixtureTerrain()
	ter.TileSet = make([][1024]byte, 2)
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			grain := ((x*17 + y*43) ^ (x * y * 7)) & 15
			ter.TileSet[0][y*32+x] = byte(64 + grain/3 + (x+y)%5)
			ter.TileSet[1][y*32+x] = byte(120 + grain + (x/4+y/5)%8)
		}
	}
	for i := range ter.TileIndices {
		if i%8 >= 4 {
			ter.TileIndices[i] = 1
		}
	}
	makeList := func(age float32, zoom camera.Zoom) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		rw, rh := int32(w), int32(h)
		if zoom != camera.ZoomUnit {
			rw, rh = w*4/3+1, h*4/3+1
		}
		l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: zoom, Step: camera.ViewScaleNative, RecordW: rw, RecordH: rh})
		l.RecordTerrain(drawlist.Terrain{Terrain: ter, OriginX: 16, OriginY: 16, DstW: rw, DstH: rh, Scale: camera.ViewScaleNative, Water: drawlist.WaterSurface{Enabled: true, Tick: 30}})
		x := float32(162)
		l.RecordScorchMarks(drawlist.ScorchMarks{Marks: []drawlist.ScorchMark{
			{X: x, Y: 76, Radius: 29, Age: age, Variant: 13},
			// A partly clipped mark checks that geometry and mask use the same zoom.
			{X: float32(rw) - 3, Y: 114, Radius: 24, Age: age, Variant: 29},
			{X: -90, Y: 76, Radius: 12, Age: age},
		}})
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: int32(x) - 3, Y: 71, W: 6, H: 10}, Index: 211})
		l.RecordWorld(drawlist.WorldSpace{})
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 8, H: h}, Index: 230})
		l.RecordExpand()
		return l
	}

	read := func(l *drawlist.List, enabled bool) ([]byte, ModelStats) {
		r.SetScorch(enabled)
		p := make([]byte, w*h*4)
		r.Execute(l, w, h).ReadPixels(p)
		return p, r.ModelStats()
	}
	difference := func(a, b []byte) int {
		sum := 0
		for i := range a {
			d := int(a[i]) - int(b[i])
			if d < 0 {
				d = -d
			}
			sum += d
		}
		return sum
	}
	for _, waterEnabled := range []bool{true, false} {
		// Clearing source caches exercises mask preparation without earlier water draws.
		r.ResetSources()
		r.SetWaterEffects(waterEnabled)
		for _, zoom := range []camera.Zoom{camera.ZoomUnit, camera.ZoomUnit * 3 / 4} {
			early := makeList(8, zoom)
			base, bs := read(&early, false)
			hot, hs := read(&early, true)
			if bytes.Equal(base, hot) || hs.ScorchQuads != 2 {
				return fmt.Errorf("scorch absent or clipping failed at zoom %v water %v: %+v", zoom, waterEnabled, hs)
			}
			again, _ := read(&early, true)
			clone := early.Clone()
			replay, _ := read(&clone, true)
			if !bytes.Equal(hot, again) || !bytes.Equal(hot, replay) {
				return fmt.Errorf("paused or cloned scorch replay changed pixels")
			}
			warmList := makeList(60, zoom)
			warm, _ := read(&warmList, true)
			if bytes.Equal(hot, warm) {
				return fmt.Errorf("scorch warm center did not cool")
			}
			// These are real GPU readbacks: the cooled mark's complete image delta
			// must decrease through the shared fade, ending at the disabled image.
			previous := -1
			for _, age := range []float32{drawlist.ScorchFadeStartTicks, 270, 420, 449, drawlist.ScorchLifeTicks} {
				l := makeList(age, zoom)
				pixels, stats := read(&l, true)
				delta := difference(base, pixels)
				if previous >= 0 && delta >= previous && previous != 0 {
					return fmt.Errorf("scorch failed to fade at age %v: delta %d after %d", age, delta, previous)
				}
				if age == drawlist.ScorchFadeStartTicks && delta == 0 {
					return fmt.Errorf("cooled scorch absent before fade")
				}
				if age == drawlist.ScorchLifeTicks && (!bytes.Equal(base, pixels) || stats.ScorchQuads != 0 || stats.DeviceDraws != bs.DeviceDraws || stats.SubmittedVertices != bs.SubmittedVertices) {
					return fmt.Errorf("expired scorch retained pixels or submissions: %+v versus %+v", stats, bs)
				}
				previous = delta
			}
			// Full baseline equality across the other surface, HUD, opaque object and
			// outside the envelope verifies the under-object order and local clipping.
			scale := float32(zoom) / float32(camera.ZoomUnit)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					px, py := float32(x)/scale, float32(y)/scale
					outside := py < 44 || py > 139 || (px < 162-43 && px < float32(w)/scale-39)
					object := px >= 160 && px < 164 && py >= 72 && py < 80
					if x < 8 || outside || px+16 < 100 || object {
						i := (y*w + x) * 4
						if !bytes.Equal(base[i:i+4], hot[i:i+4]) {
							return fmt.Errorf("scorch crossed local/mask/object clip at %d,%d zoom %v", x, y, zoom)
						}
					}
				}
			}
		}
	}
	// An otherwise valid mark over water must have no output, including with
	// coastal animation disabled. The dry channel alone controls admission.
	wet := makeList(8, camera.ZoomUnit)
	wet.Reset()
	wet.RecordClear()
	wet.RecordTerrain(drawlist.Terrain{Terrain: ter, OriginX: 16, OriginY: 16, DstW: w, DstH: h, Scale: camera.ViewScaleNative})
	wet.RecordScorchMarks(drawlist.ScorchMarks{Marks: []drawlist.ScorchMark{{X: 60, Y: 76, Radius: 29, Age: 8}}})
	wet.RecordExpand()
	base, _ := read(&wet, false)
	masked, _ := read(&wet, true)
	if !bytes.Equal(base, masked) {
		return fmt.Errorf("scorch painted water")
	}
	// Replay a deliberately oversized public batch: device submission stays
	// bounded even when a caller does not use the client's ring.
	var crowded drawlist.List
	crowded.RecordClear()
	crowded.RecordTerrain(drawlist.Terrain{Terrain: ter, OriginX: 16, OriginY: 16, DstW: w, DstH: h, Scale: camera.ViewScaleNative})
	marks := make([]drawlist.ScorchMark, drawlist.ScorchMarkLimit+20)
	for i := range marks {
		marks[i] = drawlist.ScorchMark{X: 162, Y: 76, Radius: 8, Age: 90}
	}
	crowded.RecordScorchMarks(drawlist.ScorchMarks{Marks: marks})
	crowded.RecordExpand()
	_, budget := read(&crowded, true)
	if budget.ScorchQuads != drawlist.ScorchMarkLimit {
		return fmt.Errorf("scorch submission budget = %d", budget.ScorchQuads)
	}
	r.ResetSources()
	var detached drawlist.List
	detached.RecordClear()
	detached.RecordScorchMarks(drawlist.ScorchMarks{Marks: marks[:1]})
	detached.RecordExpand()
	detachedBase, _ := read(&detached, false)
	detachedPixels, detachedStats := read(&detached, true)
	if !bytes.Equal(detachedBase, detachedPixels) || detachedStats.ScorchQuads != 0 {
		return fmt.Errorf("scorch survived terrain source removal")
	}
	if dir := os.Getenv("NANOLATHE_SCORCH_SHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for _, age := range []float32{0, 8, 30, 60, 90, 270, 420, 449, 450} {
			l := makeList(age, camera.ZoomUnit)
			pix, _ := read(&l, true)
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("scorch-age-%03.0f.png", age)))
			if err != nil {
				return err
			}
			err = png.Encode(f, &image.RGBA{Pix: pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}
