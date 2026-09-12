package gpurender

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Enhanced presentation: translation/scale must preserve physical falloff,
// and repeated spray particles must share the bounded source budget.
func TestNanoLightClustersAndProjection(t *testing.T) {
	var reference [3]float32
	for _, scale := range []float32{1, 1.5, 2} {
		r := &Renderer{w: 640, h: 480}
		r.displayPalette[163] = [4]byte{80, 255, 30, 255}
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
	pal.Base[163] = [4]byte{60, 255, 30, 255}
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
	return nil
}
