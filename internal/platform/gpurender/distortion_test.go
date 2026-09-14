package gpurender

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"
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
	r.setBlastDistortion(false)
	before := read(&l)
	r.setBlastDistortion(true)
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
	d.selectVisible(0.75, 100, 100, 0, 0)
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

// The refraction reads away from its own fragment, so its copy has to reach
// past the quad by the shader's displacement bound — and no further. A single
// blast used to copy the whole framebuffer (readcopy.go).
func TestBlastReadRegionCoversDisplacementAndNotTheFrame(t *testing.T) {
	r := &Renderer{w: 640, h: 480}
	r.distortion.candidates = []blastWave{{x: 320, y: 240, radius: 40, width: 12, strength: 5}}
	r.distortion.verts, r.distortion.indices = r.distortion.verts[:0], r.distortion.indices[:0]
	r.distortion.read.reset()
	r.appendBlastWaves()
	if len(r.distortion.verts) != 4 {
		t.Fatalf("expected one wave quad, got %d vertices", len(r.distortion.verts))
	}
	q := r.distortion.verts[0]
	e := r.distortion.verts[3]
	got := r.distortion.read
	// The quad itself must be inside the copy.
	if !got.any || float32(got.x0) > q.DstX || float32(got.y0) > q.DstY || float32(got.x1) < e.DstX || float32(got.y1) < e.DstY {
		t.Fatalf("read region %+v does not cover quad %v,%v..%v,%v", got, q.DstX, q.DstY, e.DstX, e.DstY)
	}
	// And so must the furthest texel the bilinear tap can reach.
	pad := float32(blastMaxOffset*5 + bilinearPad)
	if float32(got.x0) > q.DstX-pad || float32(got.x1) < e.DstX+pad {
		t.Fatalf("read region %+v clips the displaced samples (pad %v)", got, pad)
	}
	// It must still be a small part of the frame: the old pass copied all of it.
	if area := (got.x1 - got.x0) * (got.y1 - got.y0); area >= r.w*r.h/4 {
		t.Fatalf("one wave copied %d of %d pixels", area, r.w*r.h)
	}
}

// A plume belongs to tree_heat.go but shares this batch, so the read region is
// derived from the vertices it appended. Confirm that derivation tracks the
// quad and its own displacement bound.
func TestTreeHeatReadRegionFollowsAppendedPlumes(t *testing.T) {
	d := &worldDistortion{}
	const lane = 2
	for _, p := range [4][2]float32{{100, 50}, {140, 50}, {100, 90}, {140, 90}} {
		d.verts = append(d.verts, ebiten.Vertex{DstX: p[0], DstY: p[1],
			SrcX: p[0], SrcY: p[1] + 1 + lane,
			ColorR: 0, ColorG: 0, ColorB: 400, ColorA: 300})
	}
	d.addHeatRead(0)
	got := d.read
	padX := float32(treeHeatMaxOffsetX*lane + bilinearPad)
	padY := float32(treeHeatMaxOffsetY*lane + bilinearPad)
	if !got.any || float32(got.x0) > 100-padX || float32(got.x1) < 140+padX ||
		float32(got.y0) > 50-padY || float32(got.y1) < 90+padY {
		t.Fatalf("read region %+v misses the plume's displaced samples", got)
	}
	if got.x0 < 0 || got.y0 < 0 || got.x1 > 400 || got.y1 > 300 {
		t.Fatalf("read region %+v left the plume's own clip limits", got)
	}
}
