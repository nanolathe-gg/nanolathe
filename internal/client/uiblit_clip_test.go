package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
)

// uiBlitClippedReference is UIBlitClipped as it read before the clip was
// hoisted out of the per-pixel loops: every destination pixel tested against
// the clip rectangle, every source pixel fetched through GAFFrame.At.
//
// The rewrite has to reach exactly these pixels. The parts that can silently
// disagree are the corners — a frame placed so that only part of it is inside
// the clip, a clip rectangle that starts off-surface, and a frame whose Pixels
// or Transparent array is shorter than its declared Width x Height, where At
// reports the missing tail as absent rather than reading past the end.
func uiBlitClippedReference(c *Client, f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil {
		return
	}
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	for row := 0; row < int(f.Height); row++ {
		py := y + row
		if py < minY || py >= maxY {
			continue
		}
		for col := 0; col < int(f.Width); col++ {
			px := x + col
			if px < minX || px >= maxX {
				continue
			}
			b, ok := f.At(col, row)
			if !ok {
				continue
			}
			c.indexed[py*c.width+px] = b
		}
	}
}

// TestUIBlitClippedMatchesPerPixelWalk drives both forms over the same
// placements and clip rectangles and requires the surfaces to be equal.
func TestUIBlitClippedMatchesPerPixelWalk(t *testing.T) {
	frames := map[string]*formats.GAFFrame{
		"opaque":      uiTestFrame(11, 7, 0),
		"transparent": uiTestFrame(11, 7, 3),
		"short":       uiTestShortFrame(11, 7, 40),
		"tall":        uiTestFrame(3, 29, 2),
		"wide":        uiTestFrame(37, 2, 5),
		"one":         uiTestFrame(1, 1, 0),
	}
	placements := [][2]int{{0, 0}, {5, 5}, {-4, -3}, {-20, 4}, {60, 60}, {58, 30}, {63, 63}, {-1, 61}}
	clips := [][4]int{
		{0, 0, 64, 64}, {4, 4, 20, 20}, {-8, -8, 20, 20}, {50, 50, 40, 40},
		{0, 0, 0, 0}, {10, 10, 1, 1}, {-30, -30, 10, 10}, {32, 0, 32, 64},
	}
	for name, f := range frames {
		for _, p := range placements {
			for _, clip := range clips {
				want := newUIBlitClient(t)
				got := newUIBlitClient(t)
				uiBlitClippedReference(want, f, p[0], p[1], clip[0], clip[1], clip[2], clip[3])
				got.UIBlitClipped(f, p[0], p[1], clip[0], clip[1], clip[2], clip[3])
				got.replayForTest()
				for i := range want.indexed {
					if want.indexed[i] != got.indexed[i] {
						t.Fatalf("frame %s at (%d,%d) clip %v: pixel (%d,%d) = %d, per-pixel walk wrote %d",
							name, p[0], p[1], clip, i%want.width, i/want.width, got.indexed[i], want.indexed[i])
					}
				}
			}
		}
	}
}

// newUIBlitClient builds a 64x64 surface prefilled with a varying index, so a
// blit that wrote a pixel it should have skipped -- or skipped one it should
// have written -- shows up rather than blending into a constant background.
func newUIBlitClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	for i := range c.indexed {
		c.indexed[i] = uint8(i%199) + 1
	}
	return c
}

// uiTestFrame builds a frame whose pixel values vary and where every
// transparentEvery-th pixel is transparent. transparentEvery of zero makes the
// frame fully opaque.
func uiTestFrame(w, h, transparentEvery int) *formats.GAFFrame {
	f := &formats.GAFFrame{
		Width: uint16(w), Height: uint16(h),
		Pixels:      make([]byte, w*h),
		Transparent: make([]bool, w*h),
	}
	for i := range f.Pixels {
		f.Pixels[i] = uint8(200 + i%50)
		if transparentEvery > 0 && i%transparentEvery == 0 {
			f.Transparent[i] = true
		}
	}
	return f
}

// uiTestShortFrame declares a Width x Height the pixel arrays do not cover.
// GAFFrame.At reports every index past the end as absent, so the tail must not
// be drawn -- and must not panic.
func uiTestShortFrame(w, h, have int) *formats.GAFFrame {
	f := uiTestFrame(w, h, 4)
	f.Pixels = f.Pixels[:have]
	f.Transparent = f.Transparent[:have]
	return f
}

// TestUIBlitClippedShortArraysStopAtTheData pins the truncation directly: a
// frame that declares more pixels than it carries draws its available prefix
// and nothing past it. The rewritten blitter computes that limit per row
// instead of per pixel, which is where an off-by-one would live.
func TestUIBlitClippedShortArraysStopAtTheData(t *testing.T) {
	// Three full rows of four plus two pixels of the fourth row.
	f := uiTestShortFrame(4, 6, 14)
	c := newUIBlitClient(t)
	base := append([]uint8(nil), c.indexed...)
	c.UIBlitClipped(f, 10, 10, 0, 0, 64, 64)
	c.replayForTest()
	for row := 0; row < 6; row++ {
		for col := 0; col < 4; col++ {
			i := row*4 + col
			at := (10+row)*c.width + 10 + col
			b, ok := f.At(col, row)
			want := base[at]
			if ok {
				want = b
			}
			if i >= 14 && c.indexed[at] != base[at] {
				t.Fatalf("pixel %d is past the frame's data and must be untouched, got %d want %d", i, c.indexed[at], base[at])
			}
			if c.indexed[at] != want {
				t.Fatalf("pixel (%d,%d) = %d, want %d", col, row, c.indexed[at], want)
			}
		}
	}
}
