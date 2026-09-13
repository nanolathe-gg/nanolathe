package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

func TestBlastWaveLifetimeAndScale(t *testing.T) {
	a, _, sa := blastShape(2, 64, 1)
	b, _, sb := blastShape(8, 64, 1)
	if b <= a || sb >= sa || sb <= 0 {
		t.Fatalf("radius/strength %v %v -> %v %v", a, sa, b, sb)
	}
	for _, age := range []float32{-1, 15, 100} {
		if _, _, s := blastShape(age, 64, 1); s != 0 {
			t.Fatalf("active at age %v", age)
		}
	}
	if _, _, s := blastShape(2, 63, 1); s != 0 {
		t.Fatal("small impact produced a wave")
	}
	r, w, s := blastShape(2, 64, 2)
	_, w1, _ := blastShape(2, 64, 1)
	if r != 2*a || w != 2*w1 || s != 2*sa {
		t.Fatal("record scale changed the profile")
	}
	if _, err := newDistortionShader(); err != nil {
		t.Fatal(err)
	}
}

func checkBlastDistortionDevicePixels() error {
	if err := captureDynamicBlastExamples(); err != nil {
		return err
	}
	const w, h = 320, 240
	pal := fixturePalette()
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	art := &formats.GAFFrame{Width: 64, Height: 64}
	makeList := func(age float32) drawlist.List {
		var l drawlist.List
		l.RecordClear()
		l.RecordWorld(drawlist.WorldSpace{Begin: true, Zoom: camera.ZoomUnit, Step: camera.ViewScaleNative, RecordW: w, RecordH: h})
		for y := 0; y < h; y += 8 {
			for x := 0; x < w; x += 8 {
				l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{X: int32(x), Y: int32(y), W: 8, H: 8}, Index: uint8(50 + ((x/8+y/8)%2)*120)})
			}
		}
		l.RecordLightSource(drawlist.Sprite{Frame: art, X: 160, Y: 120, LightingKind: drawlist.SpriteLightingExplosion, LightingScale: 1, BlastSize: 64, BlastAge: age, HasClip: true, Clip: drawlist.Rect{X: 24, Y: 24, W: 272, H: 192}})
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
	r.SetBlastDistortion(false)
	before := read(&l)
	r.SetBlastDistortion(true)
	after := read(&l)
	if bytes.Equal(before, after) || r.modelStats.BlastWaves != 1 {
		return fmt.Errorf("blast did not distort background")
	}
	if !bytes.Equal(after, read(&l)) {
		return fmt.Errorf("replay advanced blast age")
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x >= 24 && x < 296 && y >= 24 && y < 216 {
				continue
			}
			i := (y*w + x) * 4
			if !bytes.Equal(before[i:i+4], after[i:i+4]) {
				return fmt.Errorf("blast escaped world clip at %d,%d", x, y)
			}
		}
	}
	expired := makeList(15)
	if !bytes.Equal(before, read(&expired)) {
		return fmt.Errorf("expired blast left distortion")
	}
	if dir := os.Getenv("NANOLATHE_BLAST_SHOTS"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		for i := 0; i <= 30; i++ {
			seq := makeList(float32(i) / 2)
			pix := read(&seq)
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("wave-%02d.png", i)))
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

func TestBlastBudgetCullsBeforeSelectingAndKeepsOrder(t *testing.T) {
	var d worldDistortion
	for i := 0; i < blastLimit; i++ {
		d.candidates = append(d.candidates, blastWave{x: -1000, y: -1000, radius: 10, width: 5, strength: 10})
	}
	for i := 0; i < blastLimit; i++ {
		d.candidates = append(d.candidates, blastWave{x: float32(i + 1), y: 20, radius: 10, width: 5, strength: 1})
	}
	d.candidates = append(d.candidates, blastWave{x: 99, y: 20, radius: 10, width: 5, strength: 2})
	d.selectVisible(0.75, 100, 100)
	if d.count != blastLimit {
		t.Fatalf("visible count %d", d.count)
	}
	for i := 0; i < blastLimit-1; i++ {
		if d.waves[i].x != float32(i+1) {
			t.Fatalf("tie/order changed at %d: %+v", i, d.waves[i])
		}
	}
	if d.waves[blastLimit-1].x != 99 {
		t.Fatal("stronger late wave was not appended")
	}
}
