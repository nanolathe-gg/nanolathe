package client

import (
	"encoding/binary"
	"testing"
)

// TestConvertIndexedToRGBAMatchesPerPixelStore locks the packed conversion
// against the one-store-per-pixel form it replaces [03 §4.3].
//
// The conversion is the only pass that touches every pixel of every presented
// frame, so it is written as two 64-bit stores over four source pixels. That
// makes two things regressable in a way no capture would show on a surface
// whose width happens to be a multiple of four: the byte order inside the
// packed word, and the tail that runs when the pixel count is not a multiple of
// four. Sizes here deliberately include all four remainders.
func TestConvertIndexedToRGBAMatchesPerPixelStore(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 15, 17, 64, 255, 640 * 480} {
		c := newConvertClient(t, n)
		want := make([]byte, len(c.rgba))
		// The reference: one 32-bit store per pixel, through the same table the
		// conversion builds.
		var lut [256]uint32
		for i := range lut {
			e := c.base[i]
			a := e[3]
			if a == 0 {
				a = 255
			}
			lut[i] = uint32(e[0]) | uint32(e[1])<<8 | uint32(e[2])<<16 | uint32(a)<<24
		}
		for i, idx := range c.indexed {
			binary.LittleEndian.PutUint32(want[i*4:i*4+4:i*4+4], lut[idx])
		}

		c.convertIndexedToRGBA()
		for i := range want {
			if c.rgba[i] != want[i] {
				t.Fatalf("%d pixels: byte %d (pixel %d component %d) = %d, per-pixel store wrote %d",
					n, i, i/4, i%4, c.rgba[i], want[i])
			}
		}
	}
}

// TestConvertIndexedToRGBAForcesOpaque keeps the alpha rule the packed table
// folds in: PALETTE.PAL's fourth byte is reserved zero, and a zero alpha is
// presented as fully opaque [03 §4.3].
func TestConvertIndexedToRGBAForcesOpaque(t *testing.T) {
	c := newConvertClient(t, 8)
	c.base[0] = [4]byte{1, 2, 3, 0}    // reserved zero -> opaque
	c.base[1] = [4]byte{4, 5, 6, 0x40} // authored alpha is kept as authored
	for i := range c.indexed {
		c.indexed[i] = uint8(i % 2)
	}
	c.convertIndexedToRGBA()
	if got := c.rgba[3]; got != 255 {
		t.Fatalf("reserved-zero alpha presented as %d, want 255", got)
	}
	if got := c.rgba[7]; got != 0x40 {
		t.Fatalf("authored alpha presented as %d, want 0x40", got)
	}
	if c.rgba[0] != 1 || c.rgba[1] != 2 || c.rgba[2] != 3 {
		t.Fatalf("component order is %d,%d,%d, want 1,2,3", c.rgba[0], c.rgba[1], c.rgba[2])
	}
}

// TestConvertIndexedToRGBARejectsMismatchedBuffers keeps the guard: a surface
// whose RGBA buffer does not match its indexed buffer is left alone rather than
// half-converted.
func TestConvertIndexedToRGBARejectsMismatchedBuffers(t *testing.T) {
	c := newConvertClient(t, 16)
	c.rgba = c.rgba[:len(c.rgba)-4]
	for i := range c.rgba {
		c.rgba[i] = 0xAB
	}
	c.convertIndexedToRGBA()
	for i := range c.rgba {
		if c.rgba[i] != 0xAB {
			t.Fatalf("mismatched buffers were converted at byte %d", i)
		}
	}
}

// newConvertClient builds a client with n pixels of varying index and a palette
// whose entries differ in every component, so a swapped byte or a skipped pixel
// is visible.
func newConvertClient(t *testing.T, n int) *Client {
	t.Helper()
	c, err := New(Options{Width: 8, Height: 8})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	c.indexed = make([]uint8, n)
	c.rgba = make([]byte, n*4)
	for i := range c.indexed {
		c.indexed[i] = uint8(i * 7 % 256)
	}
	for i := 0; i < 256; i++ {
		c.base[i] = [4]byte{byte(i), byte(255 - i), byte(i * 3 % 256), byte(i % 4)}
	}
	return c
}
