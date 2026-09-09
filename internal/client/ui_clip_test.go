package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/palette"
)

func TestUIBlitClippedConfinesGAFToWindowSurface(t *testing.T) {
	const pixel = byte(73)
	frame := &formats.GAFFrame{
		Width:       4,
		Height:      4,
		Pixels:      []byte{pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel, pixel},
		Transparent: make([]bool, 16),
	}
	c := &Client{width: 8, height: 8, indexed: make([]byte, 64)}
	c.UIBlitClipped(frame, 2, 2, 3, 3, 2, 2)
	c.replayForTest()

	for y := 0; y < c.height; y++ {
		for x := 0; x < c.width; x++ {
			want := byte(0)
			if x >= 3 && x < 5 && y >= 3 && y < 5 {
				want = pixel
			}
			if got := c.indexed[y*c.width+x]; got != want {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, got, want)
			}
		}
	}
}

func TestPushUIClipConfinesRecordedUIPrimitives(t *testing.T) {
	frame := &formats.GAFFrame{
		Width:       4,
		Height:      4,
		Pixels:      []byte{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7},
		Transparent: make([]bool, 16),
	}
	t.Run("nested image and lit image", func(t *testing.T) {
		c := &Client{width: 8, height: 8, indexed: make([]byte, 64)}
		outer := c.PushUIClip(2, 2, 4, 4)
		inner := c.PushUIClip(3, 3, 2, 2)
		c.UIBlit(frame, 2, 2)
		inner()
		outer()
		c.replayForTest()
		assertRectPixels(t, c, 3, 3, 2, 2, 7)

		lit := &palette.Tables{}
		lit.Light[1*256+7] = 9
		c = &Client{width: 8, height: 8, indexed: make([]byte, 64)}
		restore := c.PushUIClip(3, 3, 2, 2)
		c.UIBlitLit(frame, 2, 2, lit, 1)
		restore()
		c.replayForTest()
		assertRectPixels(t, c, 3, 3, 2, 2, 9)
	})

	t.Run("text and fill", func(t *testing.T) {
		fnt := &formats.FNT{Height: 1, Baseline: 0, Glyphs: [256]*formats.FNTGlyph{
			'A': {Width: 1, Height: 1, Bits: []byte{0x80}},
		}}
		c := &Client{width: 8, height: 8, indexed: make([]byte, 64), fnt: fnt}
		restore := c.PushUIClip(3, 3, 2, 2)
		c.UITextWidth(fnt, "AAAA", 2, 3, 4, 4)
		c.UIFillRect(2, 4, 4, 2, 6)
		restore()
		c.replayForTest()
		if got := c.indexed[3*c.width+3]; got != 4 {
			t.Fatalf("clipped text pixel = %d, want 4", got)
		}
		if got := c.indexed[3*c.width+2]; got != 0 {
			t.Fatalf("text escaped clip = %d, want 0", got)
		}
		if got := c.indexed[4*c.width+3]; got != 6 {
			t.Fatalf("clipped fill pixel = %d, want 6", got)
		}
		if got := c.indexed[5*c.width+2]; got != 0 {
			t.Fatalf("fill escaped clip = %d, want 0", got)
		}
	})

	t.Run("outline preserves original edges", func(t *testing.T) {
		c := &Client{width: 8, height: 8, indexed: make([]byte, 64)}
		restore := c.PushUIClip(3, 3, 2, 2)
		c.UIFrameRect(2, 2, 5, 5, 8)
		restore()
		c.replayForTest()
		for i, pixel := range c.indexed {
			if pixel != 0 {
				t.Fatalf("outline manufactured clipped edge at pixel %d = %d", i, pixel)
			}
		}
	})

	t.Run("scaled indexed surface preserves source placement", func(t *testing.T) {
		c := &Client{width: 8, height: 8, indexed: make([]byte, 64)}
		restore := c.PushUIClip(2, 3, 2, 1)
		c.UIBlitIndexed([]byte{11, 22, 33}, 3, 1, 0, 3, 6, 1)
		restore()
		c.replayForTest()
		for x := 0; x < c.width; x++ {
			want := byte(0)
			if x == 2 || x == 3 {
				want = 22
			}
			if got := c.indexed[3*c.width+x]; got != want {
				t.Fatalf("surface pixel (%d,3) = %d, want %d", x, got, want)
			}
		}
	})
}

func assertRectPixels(t *testing.T, c *Client, x, y, w, h int, want byte) {
	t.Helper()
	for py := 0; py < c.height; py++ {
		for px := 0; px < c.width; px++ {
			got, expected := c.indexed[py*c.width+px], byte(0)
			if px >= x && px < x+w && py >= y && py < y+h {
				expected = want
			}
			if got != expected {
				t.Fatalf("pixel (%d,%d) = %d, want %d", px, py, got, expected)
			}
		}
	}
}
