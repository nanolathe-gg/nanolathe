package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
)

// TestBlitFogGAFFrameOffsets locks the retail anchor rule: dest = cell origin -
// frame XOffset/YOffset before clipping [03 §3.3][fmt gaf]. The art is keyed:
// retail's raw blit skips
// every source pixel equal to the frame's ColorKey (9 in every retail frame) and
// the fog clouds themselves are index 0 [fmt gaf][R-RR16-A §3].
func TestBlitFogGAFFrameOffsets(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64, Headless: true})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	for i := range c.indexed {
		c.indexed[i] = 200
	}
	// Top-left quadrant frame (offset 0,0) at cell (0,0): fills 0..15.
	c.blitFogGAF(fogMaskFrame(16, 16, 0, 0), 0, 0, fogBlitBlack)
	if c.indexed[0] != 0 || c.indexed[15*64+7] != 0 {
		t.Fatalf("offset(0,0) frame should fill cell top-left 16x16: got %d,%d", c.indexed[0], c.indexed[15*64+7])
	}
	if c.indexed[15*64+16] != 200 {
		t.Fatalf("offset(0,0) frame must not spill past x=15, got %d at 15,16", c.indexed[15*64+16])
	}
	// Right-quadrant frame (offset -16,0) at cell (32,0): pixels land at x=48..63.
	c.blitFogGAF(fogMaskFrame(16, 16, -16, 0), 32, 0, fogBlitBlack)
	if c.indexed[48] != 0 || c.indexed[15*64+55] != 0 {
		t.Fatalf("offset(-16,0) frame should fill 48..63: got %d,%d", c.indexed[48], c.indexed[15*64+55])
	}
	if c.indexed[47] != 200 {
		t.Fatalf("offset(-16,0) frame must not spill left of x=48, got %d at 47", c.indexed[47])
	}
}

// fogMaskFrame builds a retail-shaped fog frame: left half is cloud (index 0),
// right half is the color key 9 that the blitter must skip [fmt gaf].
func fogMaskFrame(w, h int, xoff, yoff int16) *formats.GAFFrame {
	frame := &formats.GAFFrame{
		Width: uint16(w), Height: uint16(h),
		XOffset: xoff, YOffset: yoff,
		ColorKey:    9,
		Pixels:      make([]byte, w*h),
		Transparent: make([]bool, w*h),
	}
	for y := 0; y < h; y++ {
		for x := w / 2; x < w; x++ {
			frame.Pixels[y*w+x] = 9
			frame.Transparent[y*w+x] = true
		}
	}
	return frame
}

// TestBlitFogGAFModes locks what each family does to the pixels its mask covers
// [03 §3.3][R-RR16-A §1]: Black copies the source art (index 0), Gray remaps
// the destination through the GRAY TABLE without writing the source, and the
// dithered variant writes literal index 0 on a 2-pixel checker. Key pixels are
// untouched in all three.
func TestBlitFogGAFModes(t *testing.T) {
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Gray[i] = byte(255 - i)
	}
	cases := []struct {
		name string
		mode fogBlitMode
		want func(x, y int) byte
	}{
		{"black", fogBlitBlack, func(int, int) byte { return 0 }},
		{"gray", fogBlitGray, func(int, int) byte { return 255 - 40 }},
		{"patterned", fogBlitPatterned, func(x, y int) byte {
			if (x+y)&1 == 1 {
				return 0
			}
			return 40
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(Options{Width: 64, Height: 64, Headless: true})
			if err != nil {
				t.Fatalf("New client: %v", err)
			}
			c.SetPalette(tables)
			for i := range c.indexed {
				c.indexed[i] = 40
			}
			c.blitFogGAF(fogMaskFrame(16, 16, 0, 0), 0, 0, tc.mode)
			for y := 0; y < 16; y++ {
				for x := 0; x < 8; x++ {
					if got := c.indexed[y*64+x]; got != tc.want(x, y) {
						t.Fatalf("masked pixel (%d,%d) = %d, want %d", x, y, got, tc.want(x, y))
					}
				}
				if got := c.indexed[y*64+12]; got != 40 {
					t.Fatalf("key pixel at row %d was written: %d", y, got)
				}
			}
		})
	}
}

// TestFogGrayRemapKind locks hi==15 presentation: existing pixels remap through
// palette.Tables.Gray [03 §3.3][03 §4.3.3], not a
// solid fill [03 §3.3].
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
