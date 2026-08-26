package formats

import "testing"

// TestRawFrameColorKey locks the raw-path transparency rule: retail's blitter
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// 9 is that key in every retail frame, so the mask families (anims/fog.gaf,
// fogtiles.gaf, vismasks.gaf) decode to shape, not to solid rectangles.
// RLE frames carry their own skip runs and must ignore the key.
func TestRawFrameColorKey(t *testing.T) {
	// One entry, one 2x2 raw frame: 0, 9, 9, 0 with key 9.
	// Layout: 12-byte file header, entry offset table, 40-byte entry header
	// (frame count u16, u16, u32, name[32]), 8-byte frame refs, 24-byte frame
	// headers, then pixels.
	const entryOff = 16
	const refOff = entryOff + 40
	const frameOff = refOff + 8
	const pixelOff = frameOff + 24
	data := make([]byte, pixelOff+4)
	le := func(off, v, n int) {
		for i := 0; i < n; i++ {
			data[off+i] = byte(v >> (8 * i))
		}
	}
	le(0, 1, 4)         // version
	le(4, 1, 4)         // entry count
	le(8, 0, 4)         // unknown
	le(12, entryOff, 4) // entry offset table
	le(entryOff, 1, 2)  // frame count
	copy(data[entryOff+8:], "mask")
	le(refOff, frameOff, 4) // frame reference: header offset
	le(refOff+4, 0, 4)      // frame reference: value
	le(frameOff+0, 2, 2)    // width
	le(frameOff+2, 2, 2)    // height
	le(frameOff+4, 0, 2)    // x offset
	le(frameOff+6, 0, 2)    // y offset
	data[frameOff+8] = 9    // +8 color key
	data[frameOff+9] = 0    // +9 uncompressed
	le(frameOff+10, 0, 2)   // subframe count
	le(frameOff+16, pixelOff, 4)
	copy(data[pixelOff:], []byte{0, 9, 9, 0})

	gaf, err := LoadGAF(data)
	if err != nil {
		t.Fatalf("LoadGAF: %v", err)
	}
	frame := gaf.Entries[0].Frames[0].Frame
	if frame.ColorKey != 9 {
		t.Fatalf("ColorKey = %d, want 9", frame.ColorKey)
	}
	want := []bool{false, true, true, false}
	for i, w := range want {
		if frame.Transparent[i] != w {
			t.Fatalf("pixel %d transparent = %v, want %v", i, frame.Transparent[i], w)
		}
	}
	// Pixels stay lossless: the key is recorded in Transparent, not erased.
	if frame.Pixels[1] != 9 {
		t.Fatalf("key pixel was overwritten in Pixels: %d", frame.Pixels[1])
	}
}
