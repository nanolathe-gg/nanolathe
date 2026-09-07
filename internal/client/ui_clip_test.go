package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
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
