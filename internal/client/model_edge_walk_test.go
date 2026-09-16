package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

// walkPoly builds one face straight from projected corners, bypassing the
// model traversal. The tests below are about the scan converter itself
// [R-RAST-01 §1], so the geometry is stated in image pixels.
func walkPoly(corners [][2]int32, keys []int32) screenPoly {
	p := newScreenPoly(len(corners))
	for i, c := range corners {
		p.x[i], p.y[i] = c[0], c[1]
		if i < len(keys) {
			p.attr[spanKey][i] = keys[i]
		}
	}
	return p
}

// paintedRows renders one face into a fresh image and returns, per row, the
// covered pixel columns.
func paintedRows(t *testing.T, w, h int, p *screenPoly, color uint8) map[int32][]int32 {
	t.Helper()
	c := &Client{width: w, height: h, indexed: make([]uint8, w*h)}
	img := newModelImage(w, h, 0, 0, 0, 0, true, 1)
	c.fillPolyTarget(img, p, color)
	out := map[int32][]int32{}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if img.covered[y*w+x] {
				out[int32(y)] = append(out[int32(y)], int32(x))
			}
		}
	}
	return out
}

// TestFoldedProjectionPaintsOnlyTheRowsAboveTheCrossing is the contract the
// two-chain walk exists for [R-RAST-01 §1] step 7: "a face that folds paints
// only the rows where its right chain is still to the right of its left chain".
//
// The fixture is a five-corner ring whose left chain runs straight down the
// column x = 20 while its right chain bulges right, crosses back through that
// column at row 10, and stays left of it for the rest of the descent. The
// whole-polygon signed-area test this replaced would have dropped the face
// outright; the walk paints the rows above the crossing and nothing at or below
// it. That boundary — the crossing row itself paints nothing, because the
// comparison is strict — is the part that regresses silently.
func TestFoldedProjectionPaintsOnlyTheRowsAboveTheCrossing(t *testing.T) {
	// Corner 0 is the top; index 3 is the bottom. Walking down decreasing
	// indices (0 → 4 → 3) is the left chain, increasing (0 → 1 → 2 → 3) the
	// right one.
	p := walkPoly([][2]int32{
		{20, 0},  // 0: top, both chains start here
		{30, 5},  // 1: right chain bulges right
		{10, 15}, // 2: and crosses back past the left chain at row 10
		{20, 20}, // 3: bottom, both chains end here
		{20, 10}, // 4: left chain, straight down x = 20
	}, nil)
	rows := paintedRows(t, 40, 32, &p, 77)

	for r := int32(1); r <= 9; r++ {
		if len(rows[r]) == 0 {
			t.Fatalf("row %d above the crossing painted nothing; the right chain is still right of the left there", r)
		}
		if rows[r][0] != 20 {
			t.Fatalf("row %d starts at column %d, want the left chain's 20", r, rows[r][0])
		}
	}
	for r := int32(10); r < 32; r++ {
		if len(rows[r]) != 0 {
			t.Fatalf("row %d at or below the crossing painted columns %v; the right chain is no longer right of the left", r, rows[r])
		}
	}
	// Row 0 is the shared apex: both chains are at column 20, so the span is
	// empty there too. Left is inclusive, right exclusive.
	if len(rows[0]) != 0 {
		t.Fatalf("apex row painted columns %v, want an empty span", rows[0])
	}
}

// TestCounterClockwiseRingPaintsNothingPerScanline is step 7 stated on the
// walk rather than on the retired predicate: the ring reversed produces
// `xr <= xl` on every row.
func TestCounterClockwiseRingPaintsNothingPerScanline(t *testing.T) {
	forward := walkPoly([][2]int32{{6, 4}, {24, 4}, {24, 22}, {6, 22}}, nil)
	if len(paintedRows(t, 32, 32, &forward, 77)) == 0 {
		t.Fatal("clockwise ring painted nothing")
	}
	reversed := walkPoly([][2]int32{{6, 22}, {24, 22}, {24, 4}, {6, 4}}, nil)
	if rows := paintedRows(t, 32, 32, &reversed, 77); len(rows) != 0 {
		t.Fatalf("counter-clockwise ring painted %d rows", len(rows))
	}
}

// TestTwoCornerPrimitivePaintsNothing is the corollary [R-RAST-01 §1] step 7
// draws from the asset census: both chains are the same single edge, so
// `xr == xl` on every row.
func TestTwoCornerPrimitivePaintsNothing(t *testing.T) {
	p := walkPoly([][2]int32{{6, 4}, {24, 22}}, nil)
	if rows := paintedRows(t, 32, 32, &p, 77); len(rows) != 0 {
		t.Fatalf("two-corner primitive painted %d rows", len(rows))
	}
}

// TestEdgeWalkKeyIsTheChainInterpolation locks the key arithmetic the walk
// performs: the key is promoted by `<< 16` with no bias, stepped down the chain
// edge by `((next - cur) << 16) / dy` and across the span by the difference over
// the UNCLAMPED width, and narrowed at the pixel by an arithmetic shift and a
// byte mask [R-RAST-01 §1] steps 3, 5 and 6.
//
// The fixture is a right triangle whose left chain holds key 60 all the way
// down and whose right chain climbs from 60 to 100, so the span on each row
// interpolates between a constant and a known value. Asserting the composed key
// plane rather than a helper's return value keeps the test on the production
// path.
func TestEdgeWalkKeyIsTheChainInterpolation(t *testing.T) {
	const w, h = 24, 24
	// 0 is the top, 2 the bottom; left chain 0 → 2 is the column x = 4.
	p := walkPoly([][2]int32{{4, 0}, {20, 0}, {4, 16}}, []int32{60, 100, 60})
	c := &Client{width: w, height: h, indexed: make([]uint8, w*h)}
	img := newModelImage(w, h, 0, 0, 0, 0, true, 1)
	c.fillPolyTarget(img, &p, 77)

	// Row 0: the left chain sits at x = 4 with key 60, the right chain at the
	// ceiling of 20 with key 100. The per-pixel step is (100-60)<<16 / 16.
	const row = 0
	xl, xr := int32(4), int32(20)
	step := ((int64(100) - 60) << 16) / int64(xr-xl)
	acc := int64(60) << 16
	for x := xl; x < xr; x++ {
		want := uint8((acc >> 16) & 0xFF)
		if got := img.height[row*w+int(x)]; got != want {
			t.Fatalf("row %d column %d key = %d, want %d", row, x, got, want)
		}
		acc += step
	}
	// The span is exclusive of the right chain's column: the last pixel of a
	// span never reaches the right corner's value.
	if img.covered[row*w+int(xr)] {
		t.Fatalf("column %d was painted; the span is [xl, xr)", xr)
	}
}

// TestEdgeWalkInterpolatesTheShadeRowLikeTheKey locks [R-RAST-01 §5]: the SHD
// row is chosen per vertex and interpolated along the edges and across the span
// exactly like the key, so a face whose corners carry different rows shades
// across rather than in one step.
func TestEdgeWalkInterpolatesTheShadeRowLikeTheKey(t *testing.T) {
	const w, h = 24, 24
	// A one-texel opaque texture: every pixel samples the same source index, so
	// any variation across the span comes from the interpolated row alone.
	const source = uint8(9)
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{source}, Transparent: []bool{false}}
	// A shade table whose every row maps the source index to a different
	// value, so the composed byte names the row that produced it.
	pal := &palette.Tables{}
	for row := 0; row < 32; row++ {
		for i := 0; i < 256; i++ {
			pal.Shade[row][i] = uint8((i + row) & 0xFF)
		}
	}
	c := &Client{width: w, height: h, indexed: make([]uint8, w*h), pal: pal}

	p := walkPoly([][2]int32{{4, 0}, {20, 0}, {4, 16}}, []int32{60, 60, 60})
	p.useSHD, p.frame = true, texture
	p.attr[spanRow][0], p.attr[spanRow][1], p.attr[spanRow][2] = 0, 31, 0

	img := newModelImage(w, h, 0, 0, 0, 0, true, 1)
	c.blitTexturedPolyTarget(img, &p, texture)

	first := img.color[0*w+4]
	varied := false
	for x := int32(5); x < 20; x++ {
		if img.color[0*w+int(x)] != first {
			varied = true
			break
		}
	}
	if !varied {
		t.Fatal("the SHD row did not vary across the span; it must interpolate like the key [R-RAST-01 §5]")
	}
	// Row 0 column 4 takes the left corner's row 0 exactly.
	if want := pal.Shade[0][source]; first != want {
		t.Fatalf("span start shaded to %d, want SHD row 0 = %d", first, want)
	}
	// One shy of the right chain the row has climbed but never reached 31.
	last := img.color[0*w+19]
	if last == pal.Shade[31][source] {
		t.Fatal("the last pixel of the span reached the right corner's row; the interpolation never gets there")
	}
	if last == first {
		t.Fatal("the last pixel of the span kept the left corner's row")
	}
}

// TestNanoframeRevealReadsTheComposedKey keeps the construction reveal on the same
// admission and the same interpolated key as a finished body [03 §5.2].
func TestNanoframeRevealReadsTheComposedKey(t *testing.T) {
	const w, h = 24, 24
	c := &Client{width: w, height: h, indexed: make([]uint8, w*h)}

	keep := presentationRevealKeep()
	low := walkPoly([][2]int32{{4, 0}, {20, 0}, {4, 16}}, []int32{50, 50, 50})
	high := walkPoly([][2]int32{{4, 0}, {20, 0}, {4, 16}}, []int32{70, 70, 70})
	img := newModelImage(w, h, 0, 0, 0, 0, true, 1)
	c.fillPolyTarget(img, &high, 22)
	c.fillPolyTarget(img, &low, 11)
	c.revealModelImage(img, nil, &keep, 0)
	if got := img.color[1*w+5]; got != 22 {
		t.Fatalf("reveal pixel = %d, want the higher face 22", got)
	}

	erase := presentationrender.NanoframeReveal{
		Line: 60, Floor: 56,
		Below: presentationrender.NanoframeKeep,
		Band:  presentationrender.NanoframeKeep,
		Above: presentationrender.NanoframeErase,
	}
	img = newModelImage(w, h, 0, 0, 0, 0, true, 1)
	c.fillPolyTarget(img, &low, 11)
	c.fillPolyTarget(img, &high, 22)
	c.revealModelImage(img, nil, &erase, 0)
	if img.covered[1*w+5] {
		t.Fatal("an erased face left a covered pixel behind")
	}
}
