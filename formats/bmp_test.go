package formats

import (
	"encoding/binary"
	"image/color"
	"testing"
)

func TestBMPDecodesFourBitPalettePixels(t *testing.T) {
	const (
		paletteOffset = 14 + 40
		pixelOffset   = paletteOffset + 16*4
	)
	data := make([]byte, pixelOffset+4)
	copy(data[0:2], "BM")
	binary.LittleEndian.PutUint32(data[10:14], pixelOffset)
	binary.LittleEndian.PutUint32(data[14:18], 40)
	binary.LittleEndian.PutUint32(data[18:22], 3)
	binary.LittleEndian.PutUint32(data[22:26], 1)
	binary.LittleEndian.PutUint16(data[26:28], 1)
	binary.LittleEndian.PutUint16(data[28:30], 4)
	binary.LittleEndian.PutUint32(data[46:50], 16)
	// Palette entries are stored as blue, green, red, reserved.
	data[paletteOffset+1*4+2] = 255
	data[paletteOffset+2*4+1] = 255
	data[paletteOffset+15*4] = 255
	data[pixelOffset] = 0x12
	data[pixelOffset+1] = 0xf0

	image, err := LoadBMP(data)
	if err != nil {
		t.Fatal(err)
	}
	for x, want := range []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}} {
		if got := image.At(x, 0); got != want {
			t.Errorf("pixel %d = %#v, want %#v", x, got, want)
		}
	}
}
