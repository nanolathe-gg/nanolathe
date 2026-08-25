package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
)

func TestUIBlitAnchorCancelsBattleCallSiteOffset(t *testing.T) {
	const (
		width   = 20
		height  = 20
		originX = 7
		originY = 11
		pixel   = byte(93)
	)
	frame := &formats.GAFFrame{
		Width:       1,
		Height:      1,
		XOffset:     -3,
		YOffset:     -4,
		Pixels:      []byte{pixel},
		Transparent: []bool{false},
	}
	c := &Client{
		width:   width,
		height:  height,
		indexed: make([]byte, width*height),
	}

	// TotalA's battle shell adds the authored offsets at the call site. Its raw
	// rasterizer subtracts them, leaving the decoded pixel at the final origin.
	c.UIBlitAnchor(frame, originX+int(frame.XOffset), originY+int(frame.YOffset))

	if got := c.indexed[originY*width+originX]; got != pixel {
		t.Fatalf("pixel at final origin = %d, want %d", got, pixel)
	}
	if oldX, oldY := originX+2*int(frame.XOffset), originY+2*int(frame.YOffset); c.indexed[oldY*width+oldX] == pixel {
		t.Fatalf("pixel retained the former double-offset placement at (%d,%d)", oldX, oldY)
	}
}
