package formats

import (
	"encoding/binary"
	"testing"
)

func TestGAFSubframeCountUsesCompleteU16(t *testing.T) {
	const entryOffset = 16
	const entrySize = 40
	const refOffset = entryOffset + entrySize
	const parentOffset = refOffset + 8
	const parentSize = 24
	const tableOffset = parentOffset + parentSize
	const subCount = 257 // high byte is significant
	const childOffset = tableOffset + subCount*4
	const pixelOffset = childOffset + 24
	data := make([]byte, pixelOffset+1)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], entryOffset)
	binary.LittleEndian.PutUint16(data[entryOffset:], 1)
	copy(data[entryOffset+8:], "composite")
	binary.LittleEndian.PutUint32(data[refOffset:], parentOffset)
	binary.LittleEndian.PutUint16(data[parentOffset:], 1)
	binary.LittleEndian.PutUint16(data[parentOffset+2:], 1)
	binary.LittleEndian.PutUint16(data[parentOffset+10:], subCount)
	binary.LittleEndian.PutUint32(data[parentOffset+16:], tableOffset)
	for i := 0; i < subCount; i++ {
		binary.LittleEndian.PutUint32(data[tableOffset+i*4:], childOffset)
	}
	binary.LittleEndian.PutUint16(data[childOffset:], 1)
	binary.LittleEndian.PutUint16(data[childOffset+2:], 1)
	binary.LittleEndian.PutUint32(data[childOffset+16:], pixelOffset)
	data[pixelOffset] = 7

	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	f := g.Entries[0].Frames[0].Frame
	if len(f.Subframes) != subCount || f.Pixels[0] != 7 || f.Transparent[0] {
		t.Fatalf("subframe decode = count %d pixel %d transparent %v", len(f.Subframes), f.Pixels[0], f.Transparent[0])
	}
}

func TestGAFRLERowsPreserveSkipAndOpaqueZero(t *testing.T) {
	const entryOffset = 16
	const refOffset = entryOffset + 40
	const frameOffset = refOffset + 8
	const dataOffset = frameOffset + 24
	// row 0: one transparent, then literal 4, 0; row 1: repeat 6 × 3.
	data := make([]byte, dataOffset+2+4+2+2)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], entryOffset)
	binary.LittleEndian.PutUint16(data[entryOffset:], 1)
	copy(data[entryOffset+8:], "rle")
	binary.LittleEndian.PutUint32(data[refOffset:], frameOffset)
	binary.LittleEndian.PutUint16(data[frameOffset:], 3)
	binary.LittleEndian.PutUint16(data[frameOffset+2:], 2)
	data[frameOffset+9] = 1
	binary.LittleEndian.PutUint32(data[frameOffset+16:], dataOffset)
	pos := dataOffset
	binary.LittleEndian.PutUint16(data[pos:], 4)
	pos += 2
	copy(data[pos:], []byte{3, 4, 4, 0})
	pos += 4
	binary.LittleEndian.PutUint16(data[pos:], 2)
	pos += 2
	copy(data[pos:], []byte{10, 6})
	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	f := g.Entries[0].Frames[0].Frame
	if _, opaque := f.At(0, 0); opaque {
		t.Fatal("RLE skip pixel became opaque")
	}
	if got, opaque := f.At(1, 0); !opaque || got != 4 {
		t.Fatalf("literal pixel = %d,%v", got, opaque)
	}
	if got, opaque := f.At(2, 0); !opaque || got != 0 {
		t.Fatalf("literal zero = %d,%v", got, opaque)
	}
	for x := 0; x < 3; x++ {
		if got, opaque := f.At(x, 1); !opaque || got != 6 {
			t.Fatalf("repeat pixel %d = %d,%v", x, got, opaque)
		}
	}
}
