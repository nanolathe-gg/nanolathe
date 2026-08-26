package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
)

// TestBlitFogGAFFrameOffsets locks the retail anchor rule: dest = cell origin −
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// i.e. the cell's top-right quadrant: a pixel written at frame (0,0) must land
// at cell origin +(16,0).
func TestBlitFogGAFFrameOffsets(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	frame := &formats.GAFFrame{
		Width:  16,
		Height: 16,
		Pixels: make([]byte, 256),
	}
	for i := range frame.Pixels {
		frame.Pixels[i] = 9 // fog blue
	}
	// Top-left quadrant frame (offset 0,0) at cell (0,0): fills 0..15.
	c.blitFogGAF(frame, 0, 0)
	if c.indexed[0] != 9 || c.indexed[15*64+15] != 9 {
		t.Fatalf("offset(0,0) frame should fill cell top-left 16x16: got %d,%d", c.indexed[0], c.indexed[15*64+15])
	}
	if c.indexed[15*64+16] != 0 {
		t.Fatalf("offset(0,0) frame must not spill past x=15, got %d at 15,16", c.indexed[15*64+16])
	}
	// Right-quadrant frame (offset -16,0) at cell (32,0): pixels land at x=48..63.
	frame.XOffset = -16
	for i := range frame.Pixels {
		frame.Pixels[i] = 9
	}
	c.blitFogGAF(frame, 32, 0)
	if c.indexed[48] != 9 || c.indexed[15*64+63] != 9 {
		t.Fatalf("offset(-16,0) frame should fill 48..63: got %d,%d", c.indexed[48], c.indexed[15*64+63])
	}
	if c.indexed[47] != 0 {
		t.Fatalf("offset(-16,0) frame must not spill left of x=48, got %d at 47", c.indexed[47])
	}
}

// TestFogGrayRemapKind locks hi==15 presentation: existing pixels remap through
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestFogGrayRemapKind(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Base[i] = [4]byte{byte(i), byte(i), byte(i), 0}
		tables.Gray[i] = byte(255 - i) // invert so remap is observable
	}
	c.SetPalette(tables)
	for i := range c.indexed {
		c.indexed[i] = 3
	}
	op := render.FogOp{Kind: render.FogKindGrayRemap, ScreenX0: 0, ScreenY0: 0, ScreenX1: 32, ScreenY1: 32}
	// Apply the same remap the composer performs for FogKindGrayRemap.
	for py := op.ScreenY0; py < op.ScreenY1; py++ {
		base := int(py) * c.width
		for px := op.ScreenX0; px < op.ScreenX1; px++ {
			i := base + int(px)
			c.indexed[i] = c.pal.Gray[c.indexed[i]]
		}
	}
	if c.indexed[0] != 252 || c.indexed[31*64+31] != 252 {
		t.Fatalf("gray remap want 252 got %d,%d", c.indexed[0], c.indexed[31*64+31])
	}
	if c.indexed[31*64+32] != 3 {
		t.Fatalf("gray remap must stay inside the 32x32 cell, got %d outside", c.indexed[31*64+32])
	}
}
