package client

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// TestBlitFogGAFFrameOffsets locks the retail anchor rule: dest = cell origin -
// frame XOffset/YOffset before clipping [03 §3.3][fmt gaf]. The art is keyed:
// retail's raw blit skips
// every source pixel equal to the frame's ColorKey (9 in every retail frame) and
// the fog clouds themselves are index 0 [fmt gaf][R-RR16-A §3].
func TestBlitFogGAFFrameOffsets(t *testing.T) {
	c, err := New(Options{Width: 64, Height: 64})
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
			c, err := New(Options{Width: 64, Height: 64})
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
	c, err := New(Options{Width: 64, Height: 64})
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

// Scrolling past a screen edge must crop the world-anchored art, retaining
// its signed frame offset [03 §3.3][R-RR16-A §3]. Exercise the complete sink:
// the blitter alone already clips correctly.
func TestFogScrollClipsWithoutMovingArt(t *testing.T) {
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		for _, kind := range []render.FogKind{render.FogKindGAFCh0, render.FogKindGAFCh1} {
			for _, patterned := range []bool{false, true} {
				t.Run(fmt.Sprintf("scale%d/kind%d/patterned%t", scale, kind, patterned), func(t *testing.T) {
					c, err := New(Options{Width: 128, Height: 128})
					if err != nil {
						t.Fatal(err)
					}
					tables := &palette.Tables{}
					tables.Gray[200] = 100
					c.SetPalette(tables)
					frame := fogMaskFrame(16, 16, -16, -16)
					entry := &formats.GAFEntry{Frames: []formats.GAFFrameRef{{Frame: frame}}}
					c.fogGAF = &formats.GAF{}
					c.fogGray[0], c.fogBlack[0] = entry, entry
					var baseline []byte
					// Multiples of four keep the checker phase identical at both scales.
					for _, pan := range [][2]int32{{0, 0}, {0, 4}, {0, 16}, {0, 28}, {4, 0}, {16, 0}, {28, 0}, {12, 12}} {
						cam := &camera.Camera{X: 16 + pan[0], Z: 16 + pan[1], Scale: scale}
						c.SetCamera(cam)
						for i := range c.indexed {
							c.indexed[i] = 200
						}
						x0, y0, x1, y1 := render.FogScreenRect(cam, 0, 0)
						classicSink{c: c}.Fog(drawlist.Fog{Ops: []render.FogOp{{ScreenX0: x0, ScreenY0: y0, ScreenX1: x1, ScreenY1: y1, Kind: kind, Variant: 0, Frame: 0, Patterned: patterned, Scale: scale}}})
						if baseline == nil {
							baseline = append([]byte(nil), c.indexed...)
							continue
						}
						dx, dy := int(scale.Project(pan[0])), int(scale.Project(pan[1]))
						for y := 0; y < 128-dy; y++ {
							for x := 0; x < 128-dx; x++ {
								if got, want := c.indexed[y*128+x], baseline[(y+dy)*128+x+dx]; got != want {
									t.Fatalf("pan %v pixel (%d,%d) = %d, want cropped world pixel %d", pan, x, y, got, want)
								}
							}
						}
					}
				})
			}
		}
	}
}
