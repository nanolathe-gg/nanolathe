package formats

import (
	"encoding/binary"
	"testing"
)

func TestGAFSubframeCountUsesLowByteAndPreservesHighByte(t *testing.T) {
	const entryOffset = 16
	const entrySize = 40
	const refOffset = entryOffset + entrySize
	const parentOffset = refOffset + 8
	const parentSize = 24
	const tableOffset = parentOffset + parentSize
	const subCount = 300 // effective low-byte count 44; high byte selects ALP.
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
	if f.SubframeCount != 44 || len(f.Subframes) != 44 || f.AlternateBlitter != 1 {
		t.Fatalf("subframe decode = raw count %d children %d alternate %d", f.SubframeCount, len(f.Subframes), f.AlternateBlitter)
	}
	if got, opaque := f.At(0, 0); !opaque || got != 7 {
		t.Fatalf("ordinary compatibility raster = %d,%v, want 7,true", got, opaque)
	}
	if len(f.PlainPixels) != 1 || len(f.PlainTransparent) != 1 {
		t.Fatalf("plain raster lengths = %d,%d, want 1,1", len(f.PlainPixels), len(f.PlainTransparent))
	}
}

func TestGAFCompositePlainRasterSkipsAlternateChild(t *testing.T) {
	data, err := EncodeGAF([]GAFWriteEntry{{Name: "alternate", Frames: []GAFWriteFrame{{
		Width: 1, Height: 1,
		Subframes: []GAFWriteFrame{
			{Width: 1, Height: 1, Pixels: []byte{5}},
			{Width: 1, Height: 1, Pixels: []byte{7}, AlternateBlitter: 1},
		},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	if got, opaque := g.Entries[0].Frames[0].Frame.At(0, 0); !opaque || got != 5 {
		t.Fatalf("plain raster = %d,%v, want ordinary child 5 without ALP child", got, opaque)
	}
}

func TestGAFRawFramePreservesHighByteWithoutChildren(t *testing.T) {
	const entryOffset = 16
	const refOffset = entryOffset + 40
	const frameOffset = refOffset + 8
	const pixelOffset = frameOffset + 24
	data := make([]byte, pixelOffset+1)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], entryOffset)
	binary.LittleEndian.PutUint16(data[entryOffset:], 1)
	copy(data[entryOffset+8:], "rawflag")
	binary.LittleEndian.PutUint32(data[refOffset:], frameOffset)
	binary.LittleEndian.PutUint16(data[frameOffset:], 1)
	binary.LittleEndian.PutUint16(data[frameOffset+2:], 1)
	data[frameOffset+11] = 0x9a // low count remains zero: raw frame.
	binary.LittleEndian.PutUint32(data[frameOffset+16:], pixelOffset)
	data[pixelOffset] = 7
	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	f := g.Entries[0].Frames[0].Frame
	if f.SubframeCount != 0 || len(f.Subframes) != 0 || f.AlternateBlitter != 0x9a {
		t.Fatalf("raw high-byte frame = raw count %d children %d alternate %#x", f.SubframeCount, len(f.Subframes), f.AlternateBlitter)
	}
	if got, opaque := f.At(0, 0); !opaque || got != 7 {
		t.Fatalf("raw high-byte frame pixel = %d,%v", got, opaque)
	}
}

func TestGAFEntryCountIsSignedLowWord(t *testing.T) {
	data := make([]byte, 12)
	// A negative signed low word suppresses the table walk even with high bits.
	binary.LittleEndian.PutUint32(data[4:], 0x7fff8000)
	g, err := LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Entries) != 0 {
		t.Fatalf("negative low-word entry count loaded %d entries", len(g.Entries))
	}
}

func TestGAFFindUsesFirstASCIIFoldedMatch(t *testing.T) {
	g := &GAF{Entries: []GAFEntry{{Name: "First"}, {Name: "fIrSt"}, {Name: "\xc1"}}}
	got, ok := g.Find("FIRST")
	if !ok || got != &g.Entries[0] {
		t.Fatalf("first ASCII match = %p,%v, want first entry", got, ok)
	}
	if got, ok := g.Find("\xe1"); ok || got != nil {
		t.Fatalf("high-byte lookup folded unexpectedly: %p,%v", got, ok)
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

// TestGAFRLEOddWidthRowsDecodeExactly locks the row decoder against a width
// that is odd and not a multiple of a run's maximum length. 129 is the width
// of the retail interface side panels (`anims/ARMINT.GAF` PANELSIDE and
// PANELSIDE2), whose rows are encoded almost entirely as maximal 64-byte
// literal runs: 64 + 64 + 1 exactly fills the row, so a run-length or
// remaining-width error scrambles every row after the first and produces
// per-pixel noise rather than a load failure [fmt gaf "RLE pixels"].
//
// The three encodings below are the whole retail command vocabulary at the
// awkward width: maximal literal runs that land exactly on the row end, a
// maximal repeat run followed by a one-pixel remainder, and a skip run that
// offsets everything after it. Row 3 is the zero-payload row the format
// defines as fully transparent.
//
// WU-17-12 note: a decode of PANELSIDE that yields high-entropy, near-black
// indexes is **correct**, not a defect. That art is a dithered panel texture
// drawn from the darkest entry of many palette ramps; see the caveat in
// [fmt gaf "Unknowns and caveats"] before "fixing" this path.
func TestGAFRLEOddWidthRowsDecodeExactly(t *testing.T) {
	const width, height = 129, 4
	const entryOffset = 16
	const refOffset = entryOffset + 40
	const frameOffset = refOffset + 8
	const dataOffset = frameOffset + 24

	// Row 0: literal 64, literal 64, literal 1. Every pixel differs from its
	// neighbours so a one-pixel stride slip cannot pass.
	literals := make([]byte, width)
	for i := range literals {
		literals[i] = byte(11 + i*7) // 11, 18, 25, ... wraps, never constant
	}
	row0 := []byte{0xFC}
	row0 = append(row0, literals[:64]...)
	row0 = append(row0, 0xFC)
	row0 = append(row0, literals[64:128]...)
	row0 = append(row0, 0x00, literals[128])

	// Row 1: repeat 64 × 0x21, repeat 64 × 0x22, repeat 1 × 0x23.
	row1 := []byte{0xFE, 0x21, 0xFE, 0x22, 0x02, 0x23}

	// Row 2: skip 63, literal 64, repeat 2 × 0x44.
	row2 := []byte{0x7F, 0xFC}
	row2 = append(row2, literals[:64]...)
	row2 = append(row2, 0x06, 0x44)

	// Row 3: zero payload — fully transparent.
	rows := [][]byte{row0, row1, row2, nil}

	size := dataOffset
	for _, r := range rows {
		size += 2 + len(r)
	}
	data := make([]byte, size)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[12:], entryOffset)
	binary.LittleEndian.PutUint16(data[entryOffset:], 1)
	copy(data[entryOffset+8:], "panelwidth")
	binary.LittleEndian.PutUint32(data[refOffset:], frameOffset)
	binary.LittleEndian.PutUint16(data[frameOffset:], width)
	binary.LittleEndian.PutUint16(data[frameOffset+2:], height)
	data[frameOffset+8] = 9 // color key, ignored on the RLE path
	data[frameOffset+9] = 1 // compressed
	binary.LittleEndian.PutUint32(data[frameOffset+16:], dataOffset)
	pos := dataOffset
	for _, r := range rows {
		binary.LittleEndian.PutUint16(data[pos:], uint16(len(r)))
		pos += 2
		pos += copy(data[pos:], r)
	}

	gaf, err := LoadGAF(data)
	if err != nil {
		t.Fatalf("LoadGAF: %v", err)
	}
	frame := gaf.Entries[0].Frames[0].Frame
	if frame.Width != width || frame.Height != height {
		t.Fatalf("frame = %dx%d, want %dx%d", frame.Width, frame.Height, width, height)
	}

	for x := 0; x < width; x++ {
		got, opaque := frame.At(x, 0)
		if !opaque || got != literals[x] {
			t.Fatalf("row 0 x=%d = %d,opaque=%v, want %d,true", x, got, opaque, literals[x])
		}
	}
	for x := 0; x < width; x++ {
		want := byte(0x21)
		switch {
		case x == 128:
			want = 0x23
		case x >= 64:
			want = 0x22
		}
		got, opaque := frame.At(x, 1)
		if !opaque || got != want {
			t.Fatalf("row 1 x=%d = %d,opaque=%v, want %d,true", x, got, opaque, want)
		}
	}
	for x := 0; x < 63; x++ {
		if _, opaque := frame.At(x, 2); opaque {
			t.Fatalf("row 2 x=%d should be a skip pixel", x)
		}
	}
	for x := 63; x < 127; x++ {
		got, opaque := frame.At(x, 2)
		if !opaque || got != literals[x-63] {
			t.Fatalf("row 2 x=%d = %d,opaque=%v, want %d,true", x, got, opaque, literals[x-63])
		}
	}
	for x := 127; x < width; x++ {
		if got, opaque := frame.At(x, 2); !opaque || got != 0x44 {
			t.Fatalf("row 2 x=%d = %d,opaque=%v, want 68,true", x, got, opaque)
		}
	}
	for x := 0; x < width; x++ {
		if _, opaque := frame.At(x, 3); opaque {
			t.Fatalf("row 3 x=%d should be transparent (zero payload)", x)
		}
	}
}
