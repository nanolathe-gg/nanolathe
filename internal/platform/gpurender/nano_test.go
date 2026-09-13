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

// Enhanced presentation: translation/scale must preserve physical falloff,
// and repeated spray particles must share the bounded source budget.
func TestNanoLightClustersAndProjection(t *testing.T) {
	var reference [3]float32
	for _, scale := range []float32{1, 1.5, 2} {
		r := &Renderer{w: 640, h: 480}
		for i := 161; i <= 167; i++ {
			r.displayPalette[i] = [4]byte{80, 255, 30, 255}
		}
		var list drawlist.List
		for i := 0; i < 20; i++ {
			list.RecordFill(drawlist.Fill{Nano: true, Rect: drawlist.Rect{X: int32(80*scale) + 7, Y: int32(60*scale) + 9, W: int32(2 * scale), H: int32(2 * scale)}, Index: 163, WorldHeight: 20 * scale, LightingScale: scale})
		}
		r.prepareBattleLighting(&list)
		if len(r.lighting.lights) != 1 {
			t.Fatalf("spray did not cluster: %d lights", len(r.lighting.lights))
		}
		s := r.lighting.near(80*scale+7, 70*scale+9, 10*scale)
		got := s.irradiance(80*scale+7, 70*scale+9, 0, [3]float32{0, 0, 1}, false)
		if scale == 1 {
			reference = got
		}
		for j := range got {
			if d := got[j] - reference[j]; d < -0.00001 || d > 0.00001 {
				t.Fatalf("zoom changed light: %v vs %v", got, reference)
			}
		}
		if got[1] <= got[0] || got[0] <= got[2] {
			t.Fatalf("lost palette tint: %v", got)
		}
		clone := list.Clone()
		list.Reset()
		r.prepareBattleLighting(&list)
		if len(r.lighting.lights) != 0 {
			t.Fatal("ended spray retained light")
		}
		r.prepareBattleLighting(&clone)
		if len(r.lighting.lights) != 1 {
			t.Fatal("clone lost spray")
		}
	}
}

func TestNanoSourceClassificationAndRecordedClip(t *testing.T) {
	r := &Renderer{w: 64, h: 64}
	r.displayPalette[163] = [4]byte{0, 255, 0, 255}
	var list drawlist.List
	f := drawlist.Fill{Rect: drawlist.Rect{X: 90, Y: 20, W: 2, H: 2}, Index: 163, Clip: drawlist.Rect{W: 128, H: 128}}
	list.RecordFill(f) // An ordinary green UI fill must never emit.
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 0 {
		t.Fatal("unclassified green fill emitted")
	}
	f.Nano = true
	list.RecordFill(f) // Visible when the recorded viewport exceeds the device.
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 1 {
		t.Fatal("recorded clip ignored")
	}
	list.Reset()
	f.Rect.X = 128
	list.RecordFill(f)
	r.prepareBattleLighting(&list)
	if len(r.lighting.lights) != 0 {
		t.Fatal("offscreen particle emitted")
	}
}

func checkNanoDevicePixels() error {
	pal := fixturePalette()
	for i := 161; i <= 167; i++ {
		pal.Base[i] = [4]byte{60, 255, 30, 255}
	}
	const w, h = 240, 140
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 25})
	for i, normal := range [][3]float32{{1, 0, 0}, {-1, 0, 0}} {
		f := directFace(0, 0, 18, 18, 95, 10, 10)
		f.Normal = normal
		list.RecordModel(drawlist.Model{Geometry: directSubject(62, 48+int32(i)*38, 18, 18, f)})
	}
	for i := 0; i < 20; i++ {
		list.RecordFill(drawlist.Fill{Nano: true, Rect: drawlist.Rect{X: 100 + int32(i%5)*2, Y: 60 + int32(i/5)*2, W: 2, H: 2}, Index: 163, WorldHeight: 12, LightingScale: 1})
	}
	list.RecordWorld(drawlist.WorldSpace{}) // resolve glow before the interface
	list.RecordExpand()
	read := func(light, glow bool) []byte {
		r.SetBattleLighting(light)
		r.SetGlow(glow)
		out := r.Execute(&list, w, h)
		p := make([]byte, w*h*4)
		out.ReadPixels(p)
		return p
	}
	off, lit, glow := read(false, false), read(true, false), read(true, true)
	at := func(p []byte, x, y int) []byte { return p[(y*w+x)*4 : (y*w+x)*4+4] }
	if a, b := at(lit, 70, 56), at(off, 70, 56); a[1] <= b[1]+5 || a[1] <= a[0] {
		return fmt.Errorf("nano did not light facing armour: %v -> %v", b, a)
	}
	if !bytes.Equal(at(lit, 70, 95), at(off, 70, 95)) {
		return fmt.Errorf("nano lit back-facing armour")
	}
	if at(glow, 105, 72)[1] <= at(lit, 105, 72)[1] {
		return fmt.Errorf("nano halo absent beside particles")
	}
	if !bytes.Equal(off, read(false, false)) {
		return fmt.Errorf("disabled nano effects persisted")
	}
	return checkNanoShimmerDevicePixels()
}

// Enhanced emission must not turn the short looping particle ramp into a
// factory-wide flash. Replay order and palette replacement must remain stateless.
func TestNanoLightDoesNotFollowParticleShimmer(t *testing.T) {
	r := &Renderer{w: 320, h: 240}
	for i := 161; i <= 167; i++ {
		r.displayPalette[i] = [4]byte{byte((i - 160) * 8), byte((i - 160) * 32), 0, 255}
	}
	var reference battleLight
	for step, index := range []uint8{161, 162, 163, 164, 165, 166, 167, 161, 167, 163} {
		var list drawlist.List
		for i := 0; i < 10; i++ {
			list.RecordFill(drawlist.Fill{Nano: true, Rect: drawlist.Rect{X: 100 + int32(i), Y: 80, W: 2, H: 2}, Index: index, WorldHeight: 16})
		}
		r.prepareBattleLighting(&list)
		if len(r.lighting.lights) != 1 {
			t.Fatalf("step %d: missing spray", step)
		}
		got := r.lighting.lights[0]
		if step == 0 {
			reference = got
		}
		if got != reference {
			t.Fatalf("particle ramp changed broad light at index %d: %v -> %v", index, reference, got)
		}
	}
	if reference.color[1] <= reference.color[0] || reference.color[0] <= 0 {
		t.Fatalf("lost palette hue: %v", reference.color)
	}
	before := nanoLightColor(&r.displayPalette)
	for i := 161; i <= 167; i++ {
		r.displayPalette[i][0], r.displayPalette[i][1] = r.displayPalette[i][1], r.displayPalette[i][0]
	}
	after := nanoLightColor(&r.displayPalette)
	if before[0] != after[1] || before[1] != after[0] {
		t.Fatalf("palette replacement ignored: %v -> %v", before, after)
	}
}

// Sweep a synchronized spray through its complete palette cycle. Only the
// two-pixel cores may change: nearby armour and terrain must hold their light.
func checkNanoShimmerDevicePixels() error {
	pal := fixturePalette()
	for i := 161; i <= 167; i++ {
		pal.Base[i] = [4]byte{byte((i - 160) * 8), byte((i - 160) * 32), 0, 255}
	}
	const w, h = 240, 140
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	r.SetGlow(false)
	var reference []byte
	terrain := groundFixtureTerrain(40)
	for index := uint8(161); index <= 167; index++ {
		var list drawlist.List
		list.RecordClear()
		list.RecordTerrain(drawlist.Terrain{Terrain: terrain, Cam: &camera.Camera{}, DstW: w, DstH: h, Scale: camera.ViewScaleNative})
		face := directFace(0, 0, 18, 18, 95, 10, 10)
		face.Normal = [3]float32{1, 0, 0}
		list.RecordModel(drawlist.Model{Geometry: directSubject(62, 48, 18, 18, face)})
		for i := 0; i < 10; i++ {
			list.RecordFill(drawlist.Fill{Nano: true, Rect: drawlist.Rect{X: 100 + int32(i%5)*2, Y: 60 + int32(i/5)*2, W: 2, H: 2}, Index: index, WorldHeight: 12})
		}
		list.RecordExpand()
		out := r.Execute(&list, w, h)
		pixels := make([]byte, w*h*4)
		out.ReadPixels(pixels)
		if reference == nil {
			reference = pixels
		}
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if x >= 100 && x < 110 && y >= 60 && y < 64 {
					continue
				}
				at := (y*w + x) * 4
				if !bytes.Equal(reference[at:at+4], pixels[at:at+4]) {
					return fmt.Errorf("nano ramp %d flashed receiver at %d,%d", index, x, y)
				}
			}
		}
		if pixels[(60*w+100)*4+1] != pal.Base[index][1] {
			return fmt.Errorf("nano core stopped following its palette ramp")
		}
		if pixels[(80*w+104)*4+1] <= 40 {
			return fmt.Errorf("nano terrain light absent")
		}
		if dir := os.Getenv("NANOLATHE_NANO_SHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ramp-%d.png", index)))
			if err != nil {
				return err
			}
			err = png.Encode(f, &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, h)})
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

// Sparse spray energy changes with admitted particle count, never with the
// palette phase. There is no threshold that toggles a whole cluster to full
// brightness, and removing the final particle must remove its light immediately.
func TestNanoSparseLightTracksCountWithoutThreshold(t *testing.T) {
	r := &Renderer{w: 320, h: 240}
	for i := 161; i <= 167; i++ {
		r.displayPalette[i] = [4]byte{0, byte((i - 160) * 32), 0, 255}
	}
	var perParticle float32
	for _, count := range []int{1, 2, 3, 2, 1, 0} {
		var list drawlist.List
		for i := 0; i < count; i++ {
			list.RecordFill(drawlist.Fill{Nano: true, Rect: drawlist.Rect{X: 100, Y: 80, W: 2, H: 2}, Index: uint8(161 + i), WorldHeight: 16})
		}
		r.prepareBattleLighting(&list)
		if count == 0 {
			if len(r.lighting.lights) != 0 {
				t.Fatal("expired sparse spray retained light")
			}
			continue
		}
		if len(r.lighting.lights) != 1 {
			t.Fatalf("%d particles toggled the cluster off", count)
		}
		energy := r.lighting.lights[0].color[1]
		if perParticle == 0 {
			perParticle = energy
		}
		want := perParticle * float32(count)
		if delta := energy - want; delta < -0.000001 || delta > 0.000001 {
			t.Fatalf("sparse spray jumped to %g energy; want %g for %d particles", energy, want, count)
		}
	}
}
