package author

// GAF writer — layout per research/formats/gaf.md. Frames are written raw
// (compressed = 0) with the retail colour key 9, so palette index 9 is the
// transparent pixel and every other index is opaque.

// GAFFrame is one raw frame.
type GAFFrame struct {
	Width, Height    int
	XOffset, YOffset int16
	Pixels           []byte // Width×Height palette indexes, row-major
	// Hold is the frame-reference duration word (whole simulation ticks).
	Hold uint32
}

// GAFEntry is a named sequence of frames.
type GAFEntry struct {
	Name   string
	Frames []GAFFrame
}

// GAFColorKey is the transparent index used by every retail raw frame.
const GAFColorKey = 9

// NewFrame returns a frame filled with the colour key (fully transparent).
func NewFrame(w, h int, xoff, yoff int16, hold uint32) GAFFrame {
	px := make([]byte, w*h)
	for i := range px {
		px[i] = GAFColorKey
	}
	return GAFFrame{Width: w, Height: h, XOffset: xoff, YOffset: yoff, Pixels: px, Hold: hold}
}

// Set writes one pixel; out-of-range coordinates are ignored.
func (f *GAFFrame) Set(x, y int, index byte) {
	if x < 0 || y < 0 || x >= f.Width || y >= f.Height {
		return
	}
	f.Pixels[y*f.Width+x] = index
}

// Fill writes a rectangle [x0,x1)×[y0,y1).
func (f *GAFFrame) Fill(x0, y0, x1, y1 int, index byte) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			f.Set(x, y, index)
		}
	}
}

// GAFBytes serialises a GAF with the retail version word 0x00010100 and
// entry loop word 1 ([fmt gaf] "Entry header").
func GAFBytes(entries []GAFEntry) []byte {
	var b buf
	b.u32(0x00010100)           // version
	b.u32(uint32(len(entries))) // entry_count
	b.u32(0)                    // unknown, 0 in all retail files
	entryPtrs := b.off()        // u32 entry_offsets[count]
	for range entries {
		b.u32(0)
	}
	for ei, e := range entries {
		check(len(e.Name) < 32, "gaf: entry name too long: %q", e.Name)
		b.patchU32(entryPtrs+uint32(ei)*4, b.off())
		b.u16(uint16(len(e.Frames))) // frame_count
		b.u16(1)                     // unknown1: loop byte, 1 in every retail entry
		b.u32(0)                     // unknown2
		var name [32]byte
		copy(name[:], e.Name)
		b.Write(name[:])
		refs := b.off() // frame references: {u32 frame header offset, u32 hold}
		for _, f := range e.Frames {
			b.u32(0)
			b.u32(f.Hold)
		}
		for fi, f := range e.Frames {
			check(len(f.Pixels) == f.Width*f.Height, "gaf: frame pixel count")
			check(f.Width > 0 && f.Height > 0, "gaf: frame must be non-empty")
			b.patchU32(refs+uint32(fi)*8, b.off())
			b.u16(uint16(f.Width))  // +0 width
			b.u16(uint16(f.Height)) // +2 height
			b.i16(f.XOffset)        // +4 x_offset
			b.i16(f.YOffset)        // +6 y_offset
			b.u8(GAFColorKey)       // +8 color_key
			b.u8(0)                 // +9 compressed = 0 (raw)
			b.u16(0)                // +10 subframe_count
			b.u32(0)                // +12 unknown2
			dataPtr := b.off()      // +16 data_offset
			b.u32(0)
			b.u32(0) // +20 unknown3 (ignored by the engine)
			b.patchU32(dataPtr, b.off())
			b.Write(f.Pixels)
		}
	}
	return b.Bytes()
}
