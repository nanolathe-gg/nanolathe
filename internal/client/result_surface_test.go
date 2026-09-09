package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

func TestUIBlitFrameSourceRectScaledUsesInclusiveInterior(t *testing.T) {
	frame := &formats.GAFFrame{
		Width:       4,
		Height:      2,
		Pixels:      []byte{1, 2, 3, 4, 9, 11, 12, 13},
		Transparent: make([]bool, 8),
	}
	c := &Client{width: 3, height: 1, indexed: make([]byte, 3)}
	c.UIBlitFrameSourceRectScaledClipped(frame, 1, 1, 3, 1, 0, 0, 3, 1, 0, 0, 3, 1)
	c.replayForTest()

	for x, want := range []byte{11, 12, 13} {
		if got := c.indexed[x]; got != want {
			t.Fatalf("destination pixel %d = %d, want %d", x, got, want)
		}
	}
}
