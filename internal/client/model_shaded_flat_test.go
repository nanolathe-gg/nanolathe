package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

// shadeTestTables builds a palette whose SHD rows are trivially identifiable:
// row r maps colour c to c+r. Nothing in the raster depends on the table's
// contents, so a synthetic one makes the lookup visible.
func shadeTestTables() *palette.Tables {
	tab := &palette.Tables{}
	for row := 0; row < 32; row++ {
		for c := 0; c < 256; c++ {
			tab.Shade[row][c] = uint8((c + row) & 0xff)
		}
	}
	return tab
}

// shadedFlatFace is a flat face carrying one SHD row at every corner, so the
// interpolated row is that row across the whole span.
func shadedFlatFace(key uint8, row int32) screenPoly {
	k := int32(key)
	p := walkPoly([][2]int32{{0, 0}, {6, 0}, {0, 6}}, []int32{k, k, k})
	p.useSHD = true
	for i := range p.attr[spanRow] {
		p.attr[spanRow][i] = row
	}
	return p
}

// TestShadedFlatWriterResolvesThroughSHD locks the shaded flat span writer of
// [R-REN-03A §5]: it writes `SHD[row*256 + colour]`, not the raw colour byte.
// There are four writers, one per (shaded, unshaded) x (textured, flat), and
// the flat pair differs only in that lookup.
func TestShadedFlatWriterResolvesThroughSHD(t *testing.T) {
	const color uint8 = 100
	for _, row := range []int32{0, 7, 31} {
		c := &Client{width: 8, height: 8, indexed: make([]uint8, 64), pal: shadeTestTables()}
		img := newModelImage(c.width, c.height, 0, 0, 0, 0, true, 1)
		face := shadedFlatFace(70, row)
		c.fillPolyTarget(img, &face, color)
		want := uint8((int(color) + int(row)) & 0xff)
		if got := img.color[1*c.width+1]; got != want {
			t.Fatalf("row %d composed %d, want SHD[%d][%d] = %d", row, got, row, color, want)
		}
	}
}

// TestUnshadedFlatWriterEmitsTheRawColour is the other half of the pair: a
// primitive the unshaded piece renderer emitted carries no SHD row, and its
// flat writer must put the authored byte down untouched [R-REN-03A §5].
// Mobile units take that renderer, which is why their flat faces do not
// respond to the light [R-RND-02A].
func TestUnshadedFlatWriterEmitsTheRawColour(t *testing.T) {
	const color uint8 = 100
	c := &Client{width: 8, height: 8, indexed: make([]uint8, 64), pal: shadeTestTables()}
	img := newModelImage(c.width, c.height, 0, 0, 0, 0, true, 1)
	face := walkPoly([][2]int32{{0, 0}, {6, 0}, {0, 6}}, []int32{70, 70, 70})
	if face.useSHD {
		t.Fatal("fixture face must carry no SHD row")
	}
	c.fillPolyTarget(img, &face, color)
	if got := img.color[1*c.width+1]; got != color {
		t.Fatalf("unshaded flat writer composed %d, want the raw %d", got, color)
	}
}

// TestShadedFlatWriterWithNoPaletteKeepsTheColour locks the degenerate path: a
// client with no palette bound has no SHD table to read, and must compose the
// authored byte rather than index a nil table.
func TestShadedFlatWriterWithNoPaletteKeepsTheColour(t *testing.T) {
	const color uint8 = 100
	c := &Client{width: 8, height: 8, indexed: make([]uint8, 64)}
	img := newModelImage(c.width, c.height, 0, 0, 0, 0, true, 1)
	face := shadedFlatFace(70, 7)
	c.fillPolyTarget(img, &face, color)
	if got := img.color[1*c.width+1]; got != color {
		t.Fatalf("composed %d with no palette, want the raw %d", got, color)
	}
}

// TestShadedFlatRowMatchesTheTexturedRow locks that both shaded writers read
// the same interpolated row lane: a flat face and a textured face carrying the
// same corner rows resolve their byte through the same table entry
// [R-RAST-01 §5][R-REN-03A §5].
func TestShadedFlatRowMatchesTheTexturedRow(t *testing.T) {
	if presentationrender.NoShadeRow >= 0 {
		t.Fatal("NoShadeRow must stay the negative sentinel the writers test against")
	}
	const row int32 = 11
	const color uint8 = 100
	c := &Client{width: 8, height: 8, indexed: make([]uint8, 64), pal: shadeTestTables()}
	img := newModelImage(c.width, c.height, 0, 0, 0, 0, true, 1)
	face := shadedFlatFace(70, row)
	c.fillPolyTarget(img, &face, color)
	flat := img.color[1*c.width+1]
	if flat != c.pal.Shade[row][color] {
		t.Fatalf("flat writer composed %d, want the row-%d entry %d", flat, row, c.pal.Shade[row][color])
	}
}
