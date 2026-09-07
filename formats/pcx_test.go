package formats

import (
	"encoding/binary"
	"testing"
)

func authoredPCX(width, height, stride uint16, pixels []byte, marker bool) []byte {
	data := make([]byte, 128, 128+len(pixels)+768)
	data[0], data[1] = 0x0a, 5
	binary.LittleEndian.PutUint16(data[8:], width-1)
	binary.LittleEndian.PutUint16(data[10:], height-1)
	binary.LittleEndian.PutUint16(data[66:], stride)
	data = append(data, pixels...)
	if marker {
		data = append(data, 0x0c)
	}
	for i := 0; i < 256; i++ {
		data = append(data, byte(i), byte(i+1), byte(i+2))
	}
	return data
}

func TestPCXRowsConsumeVisibleWidthNotAuthoredStride(t *testing.T) {
	pcx, err := LoadPCX(authoredPCX(2, 2, 4, []byte{1, 2, 3, 4, 5, 6, 7, 8}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(pcx.Pixels), string([]byte{1, 2, 3, 4}); got != want {
		t.Fatalf("pixels = %v, want %v", pcx.Pixels, []byte{1, 2, 3, 4})
	}
	if pcx.BytesPerLine != 4 || pcx.Palette[0].R != 0 || pcx.Palette[255].B != 1 {
		t.Fatalf("metadata/palette = stride %d palette %#v", pcx.BytesPerLine, pcx.Palette[255])
	}
}

func TestPCXRLEClampsToVisibleRowAndFailsSafelyAtEOF(t *testing.T) {
	pcx, err := LoadPCX(authoredPCX(2, 1, 1, []byte{0xc4, 9}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(pcx.Pixels), string([]byte{9, 9}); got != want {
		t.Fatalf("pixels = %v, want %v", pcx.Pixels, []byte{9, 9})
	}
	if _, err := LoadPCX(authoredPCX(2, 1, 2, []byte{0xc2}, false)); err == nil {
		t.Fatal("truncated RLE value was accepted")
	}
}
