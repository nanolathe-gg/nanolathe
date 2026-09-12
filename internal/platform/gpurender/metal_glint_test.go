package gpurender

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// These are prototype design checks, not claims about retail materials.
func TestMetalGlintFacingAndPacking(t *testing.T) {
	if metalFaceGlint([3]float32{}) != 0 || metalFaceGlint([3]float32{0, 0, -1}) != 0 {
		t.Fatal("missing or downward normal catches the overhead key")
	}
	if metalFaceGlint([3]float32{-0.35, -0.15, 0.9246621}) < 0.99 {
		t.Fatal("aligned panel misses the highlight")
	}
	for weight := 0; weight <= 255; weight++ {
		for index := 0; index <= 255; index++ {
			packed := int(metalGlintColor(byte(index), float32(weight)/255))
			if packed%256 != index || packed/256 != weight {
				t.Fatalf("glint weight %d corrupted palette index %d", weight, index)
			}
		}
	}
}

// Verify pixels on the real backend, including a full on/off round trip. This
// catches flat-colour packing corrupting palette lookup and mask regressions.
func checkMetalGlintDevicePixels() error {
	pal := fixturePalette()
	pal.Base[90] = [4]byte{120, 120, 120, 255}
	pal.Base[91] = [4]byte{180, 20, 20, 255}
	pal.Base[92] = [4]byte{15, 15, 15, 255}
	const w, h = 100, 50
	r, err := NewChecked(&pal, w, h)
	if err != nil {
		return err
	}
	var list drawlist.List
	list.RecordClear()
	list.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: w, H: h}, Index: 25})
	for i, index := range []byte{90, 91, 92, 1} {
		f := directFace(0, 0, 16, 16, index, 10, 10)
		f.Normal = [3]float32{-0.35, -0.15, 0.9246621}
		list.RecordModel(drawlist.Model{Geometry: directSubject(8+int32(i)*22, 10, 16, 16, f)})
	}
	list.RecordExpand()
	read := func(on bool) []byte {
		r.SetMetalGlint(on)
		out := r.Execute(&list, w, h)
		pixels := make([]byte, w*h*4)
		out.ReadPixels(pixels)
		return pixels
	}
	off, on, again := read(false), read(true), read(false)
	if !bytes.Equal(off, again) {
		return fmt.Errorf("glint off failed to restore original pixels")
	}
	for i := 0; i < 4; i++ {
		at := ((18 * w) + 16 + i*22) * 4
		if i == 0 {
			if on[at] < off[at]+40 {
				return fmt.Errorf("neutral panel has no glint: %v -> %v", off[at:at+4], on[at:at+4])
			}
		} else if !bytes.Equal(off[at:at+4], on[at:at+4]) {
			return fmt.Errorf("glint changed masked/keyed panel %d: %v -> %v", i, off[at:at+4], on[at:at+4])
		}
	}
	return nil
}
