package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func checkTreeHeatDevicePixels() error {
	const w, h = 320, 240
	pal := fixturePalette()
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	art := &formats.GAFFrame{Width: 64, Height: 64, Pixels: make([]byte, 64*64)}
	makeList := func(age float32) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative, RecordW: w, RecordH: h})
		for y := 0; y < h; y += 8 {
			for x := 0; x < w; x += 8 {
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: int32(x), Y: int32(y), W: 8, H: 8}, Index: uint8(50 + ((x/8+y/8)%2)*120)})
			}
		}
		l.RecordSprite(drawlist.Sprite{Frame: art, X: 128, Y: 135, LightingScale: 1, HeatSource: age >= 0, HeatTime: age, HasClip: true, Clip: drawlist.Rect{X: 144, Y: 100, W: 152, H: 116}})
		l.RecordWorld(drawlist.WorldSpace{})
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: 20, H: h}, Index: 230})
		l.RecordExpand()
		return l
	}
	read := func(l *drawlist.List) []byte {
		pix := make([]byte, w*h*4)
		r.Execute(l, w, h).ReadPixels(pix)
		return pix
	}
	l := makeList(4)
	r.SetTreeHeat(false)
	before := read(&l)
	r.SetTreeHeat(true)
	after := read(&l)
	if bytes.Equal(before, after) || r.modelStats.HeatPlumes != 1 {
		return fmt.Errorf("heat did not distort background")
	}
	animated := makeList(9)
	if bytes.Equal(after, read(&animated)) {
		return fmt.Errorf("heat did not animate")
	}
	if !bytes.Equal(after, read(&l)) {
		return fmt.Errorf("replay advanced heat age")
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= 144 && x < 296 && y >= 100 && y < 216 {
				continue
			}
			i := (y*w + x) * 4
			if !bytes.Equal(before[i:i+4], after[i:i+4]) {
				return fmt.Errorf("heat escaped world clip at %d,%d", x, y)
			}
		}
	}
	expired := makeList(-1)
	if !bytes.Equal(before, read(&expired)) {
		return fmt.Errorf("expired heat left distortion")
	}
	if dir := os.Getenv("NANOLATHE_HEAT_SHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for i := 0; i < 60; i++ {
			seq := makeList(float32(i) / 2)
			pix := read(&seq)
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("heat-%02d.png", i)))
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

// The requested overlap policy is tree heat over blasts, sampled from the same
// original world. The GPU check also guards the one-copy/one-draw cost when
// both families are active (GPU design §27.2).
func checkCombinedDistortionDevicePixels() error {
	const w, h = 320, 240
	pal := fixturePalette()
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	art := &formats.GAFFrame{Width: 64, Height: 64, Pixels: make([]byte, 64*64)}
	var l drawlist.List
	l.RecordClear()
	l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative, RecordW: w, RecordH: h})
	for y := 0; y < h; y += 8 {
		for x := 0; x < w; x += 8 {
			l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: int32(x), Y: int32(y), W: 8, H: 8}, Index: uint8(50 + ((x/8+y/8)%2)*120)})
		}
	}
	// The small plume intersects the expanding ring's right-hand side.
	l.RecordSprite(drawlist.Sprite{Frame: art, X: 128, Y: 135, HeatSource: true, HeatTime: 4, LightingScale: 1})
	l.RecordLightSource(drawlist.Sprite{Frame: art, X: 110, Y: 120, LightingKind: drawlist.SpriteLightingExplosion, LightingScale: 1, BlastSize: 64, BlastAge: 4})
	l.RecordWorld(drawlist.WorldSpace{})
	l.RecordExpand()
	read := func(blast, heat bool) ([]byte, ModelStats) {
		r.SetBlastDistortion(blast)
		r.SetTreeHeat(heat)
		pix := make([]byte, w*h*4)
		r.Execute(&l, w, h).ReadPixels(pix)
		return pix, r.ModelStats()
	}
	// Populate source caches before comparing submission counts.
	read(true, true)
	base, none := read(false, false)
	blast, blastStats := read(true, false)
	heat, heatStats := read(false, true)
	both, bothStats := read(true, true)
	if bothStats.BlastWaves != 1 || bothStats.HeatPlumes != 1 {
		return fmt.Errorf("combined distortion omitted an effect: %+v", bothStats)
	}
	if bothStats.DeviceDraws != none.DeviceDraws+2 || bothStats.DeviceDraws != heatStats.DeviceDraws || bothStats.DeviceDraws != blastStats.DeviceDraws {
		return fmt.Errorf("distortion draws: none=%d blast=%d heat=%d both=%d", none.DeviceDraws, blastStats.DeviceDraws, heatStats.DeviceDraws, bothStats.DeviceDraws)
	}
	overlap, outsideBlast := 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			heatChanged := !bytes.Equal(base[i:i+4], heat[i:i+4])
			blastChanged := !bytes.Equal(base[i:i+4], blast[i:i+4])
			if heatChanged || (x >= 151 && x < 169 && y >= 105 && y < 150) {
				if !bytes.Equal(both[i:i+4], heat[i:i+4]) {
					return fmt.Errorf("tree heat lost priority at %d,%d", x, y)
				}
				if blastChanged {
					overlap++
				}
			}
			// Strictly outside the entire heat quad, even where its envelope discards.
			if x < 124 || x >= 196 || y < 83 || y >= 164 {
				if !bytes.Equal(both[i:i+4], blast[i:i+4]) {
					return fmt.Errorf("heat changed outside its bounds at %d,%d", x, y)
				}
				if blastChanged {
					outsideBlast++
				}
			}
		}
	}
	if overlap == 0 || outsideBlast == 0 {
		return fmt.Errorf("fixture missed overlap or separate blast pixels")
	}
	again, _ := read(true, true)
	if !bytes.Equal(both, again) {
		return fmt.Errorf("combined replay changed pixels")
	}
	if dir := os.Getenv("NANOLATHE_HEAT_SHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for _, shot := range []struct {
			name string
			pix  []byte
		}{{"blast", blast}, {"heat", heat}, {"combined", both}} {
			f, err := os.Create(filepath.Join(dir, shot.name+".png"))
			if err != nil {
				return err
			}
			err = png.Encode(f, &image.RGBA{Pix: shot.pix, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
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
