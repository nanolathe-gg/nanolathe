package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
)

// fogFillReference paints the fog operations the way the composer's three
// direct fill kinds did before they were rewritten to walk a sliced row: one
// bounds-checked store per pixel, addressed from the row base, with the checker
// selecting columns by testing every one of them.
//
// It exists so the rewritten fills can be compared against the arithmetic they
// replaced rather than against a hand-copied expectation, which is the part
// that is easy to regress silently: the solid fill's inclusive/exclusive
// column range, the gray remap's per-pixel table read, and the checker's
// (x+y+parity)&1 phase.
func fogFillReference(c *Client, ops []render.FogOp, parity int32) {
	w, h := c.width, c.height
	for _, op := range ops {
		x0, y0, x1, y1 := op.ScreenX0, op.ScreenY0, op.ScreenX1, op.ScreenY1
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > int32(w) {
			x1 = int32(w)
		}
		if y1 > int32(h) {
			y1 = int32(h)
		}
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		switch op.Kind {
		case render.FogKindSolidDark:
			for py := y0; py < y1; py++ {
				base := int(py)*w + int(x0)
				for px := x0; px < x1; px++ {
					c.indexed[base+int(px-x0)] = render.FogDarkPaletteIndex
				}
			}
		case render.FogKindGrayRemap:
			for py := y0; py < y1; py++ {
				base := int(py)*w + int(x0)
				for px := x0; px < x1; px++ {
					i := base + int(px-x0)
					c.indexed[i] = c.pal.Gray[c.indexed[i]]
				}
			}
		case render.FogKindPatterned:
			for py := y0; py < y1; py++ {
				base := int(py)*w + int(x0)
				for px := x0; px < x1; px++ {
					if (px+py+parity)&1 != 1 {
						continue
					}
					c.indexed[base+int(px-x0)] = render.FogDarkPaletteIndex
				}
			}
		}
	}
}

// fogFillRewritten drives the composer's own three writers over the same
// operations the reference walked, applying the identical clip the fog
// composite applies before it dispatches on the operation kind.
func fogFillRewritten(c *Client, ops []render.FogOp, parity int32) {
	w, h := c.width, c.height
	for _, op := range ops {
		x0, y0, x1, y1 := op.ScreenX0, op.ScreenY0, op.ScreenX1, op.ScreenY1
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > int32(w) {
			x1 = int32(w)
		}
		if y1 > int32(h) {
			y1 = int32(h)
		}
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		switch op.Kind {
		case render.FogKindSolidDark:
			c.fogFillSolid(x0, y0, x1, y1)
		case render.FogKindGrayRemap:
			c.fogFillGray(x0, y0, x1, y1)
		case render.FogKindPatterned:
			c.fogFillChecker(x0, y0, x1, y1, parity)
		}
	}
}

// TestFogDirectFillsMatchPerPixelWalk locks the three direct fog fills against
// the per-pixel walk they replaced [03 §3.3][R-RR16-A §2]. The rewrite is a
// bounds-check hoist and must be byte-identical; the checker phase in
// particular is easy to invert, and no --shot scene exercises the dithered
// option or the gray remap, so the contract is asserted here instead.
func TestFogDirectFillsMatchPerPixelWalk(t *testing.T) {
	kinds := []render.FogKind{render.FogKindSolidDark, render.FogKindGrayRemap, render.FogKindPatterned}
	// Odd and even rectangle origins and widths, rectangles that overhang each
	// edge, and a degenerate one: the checker phase and the clamp both depend
	// on the parity and sign of the clipped range, not the authored one.
	rects := [][4]int32{
		{0, 0, 32, 32}, {1, 0, 33, 32}, {0, 1, 32, 33}, {7, 5, 40, 38},
		{-9, -3, 23, 29}, {50, 44, 90, 84}, {63, 63, 64, 64}, {10, 10, 10, 20},
	}
	for _, parity := range []int32{0, 1} {
		for _, kind := range kinds {
			var ops []render.FogOp
			for _, r := range rects {
				ops = append(ops, render.FogOp{Kind: kind, ScreenX0: r[0], ScreenY0: r[1], ScreenX1: r[2], ScreenY1: r[3]})
			}
			want := newFogFillClient(t)
			got := newFogFillClient(t)
			fogFillReference(want, ops, parity)
			fogFillRewritten(got, ops, parity)
			for i := range want.indexed {
				if want.indexed[i] != got.indexed[i] {
					t.Fatalf("kind %d parity %d: pixel %d (x=%d y=%d) = %d, per-pixel walk wrote %d",
						kind, parity, i, i%want.width, i/want.width, got.indexed[i], want.indexed[i])
				}
			}
		}
	}
}

// newFogFillClient builds a 64x64 surface whose starting indices vary per pixel
// and whose GRAY TABLE is an inversion, so a remap that silently skipped a
// pixel or read the wrong entry shows up as a difference rather than as two
// equal constants.
func newFogFillClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Options{Width: 64, Height: 64})
	if err != nil {
		t.Fatalf("New client: %v", err)
	}
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Base[i] = [4]byte{byte(i), byte(i), byte(i), 0}
		tables.Gray[i] = byte(255 - i) // invert so a remap is observable
	}
	c.SetPalette(tables)
	for i := range c.indexed {
		c.indexed[i] = uint8(i % 251)
	}
	return c
}

// TestFogPatternedPhaseFollowsScreenColumn pins the checker to absolute screen
// coordinates rather than to the offset inside the clipped rectangle. Retail
// selects (x+y+parity)&1 == 1 in screen space, so two adjacent fog cells share
// one continuous checker; deriving the phase from the row slice's own index
// would restart it at every cell boundary and print visible seams.
func TestFogPatternedPhaseFollowsScreenColumn(t *testing.T) {
	c := newFogFillClient(t)
	for i := range c.indexed {
		c.indexed[i] = 200
	}
	// Two abutting cells, the second starting on an odd column.
	ops := []render.FogOp{
		{Kind: render.FogKindPatterned, ScreenX0: 0, ScreenY0: 0, ScreenX1: 5, ScreenY1: 1},
		{Kind: render.FogKindPatterned, ScreenX0: 5, ScreenY0: 0, ScreenX1: 10, ScreenY1: 1},
	}
	fogFillRewritten(c, ops, 0)
	for px := 0; px < 10; px++ {
		want := uint8(200)
		if (px+0+0)&1 == 1 {
			want = render.FogDarkPaletteIndex
		}
		if got := c.indexed[px]; got != want {
			t.Fatalf("column %d across the cell seam = %d, want %d", px, got, want)
		}
	}
}

// TestFogFillsDriveTheComposer checks the three writers this file exercises are
// the ones the composite reaches: a never-seen grid must leave the viewport
// solid dark after drawFog, so a rewrite that stopped painting is caught here
// [03 §3.3].
func TestFogFillsDriveTheComposer(t *testing.T) {
	c := newTestClient(t)
	dark := make([]byte, 32*32)
	for i := range dark {
		dark[i] = 15
	}
	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Fog:       frame.FogView{Valid: true, W: 32, H: 32, Ch0: dark, Ch1: make([]byte, 32*32)},
	}
	clearIndexed(c)
	for i := range c.indexed {
		c.indexed[i] = 77
	}
	c.drawFog(cur)
	// The grid's first cell starts half a tile in and is then rebased off the
	// retail viewport origin, so the covered area begins at (16, 16)
	// [03 §2.5][03 §3.3]: (20, 20) is inside it and (10, 10) is not.
	if got := c.indexed[20*c.width+20]; got != render.FogDarkPaletteIndex {
		t.Fatalf("never-seen viewport pixel = %d, want the solid-dark index %d", got, render.FogDarkPaletteIndex)
	}
	if got := c.indexed[10*c.width+10]; got != 77 {
		t.Fatalf("pixel outside the fog grid = %d, want it untouched at 77", got)
	}
}
