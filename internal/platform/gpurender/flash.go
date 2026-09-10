package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The lit-disc families as quads (docs/DESIGN_GPU_RENDERER.md §13.11).
//
// An explosion's calculated disc and an effect's ground halo are brightenings of
// the pixels already composed under them: every covered pixel is folded through
// one PALETTE.LHT row [03 §4.3.1][03 R-FX-01 §4]. The classic recording lane
// carries that as one lit point per covered SCREEN pixel, which at about two
// hundred live effects is a million points a frame — the executor's whole
// scaling term. The modern lane carries each disc as one command instead, and
// this file draws it as one quad.
//
// Both discs are row-family commands: they hand the device the same scale
// fragment `FillLitRect` and the lit point plane hand it, under the same blend,
// so they compose with the trails and the fills without a phase between them
// (§13.11 "The same-stream rule") and the brightening per row is the point
// plane's own arithmetic, not a second derivation of it.
//
// # The flash: an intensity atlas
//
// A generated disc frame is immutable for the battle [06 R-WFX-01 §2], so it is
// packed once, on first use, into one RGBA8 atlas and sampled from there for
// ever after. A texel carries exactly what the lit point plane's texels carry —
// the row family's high lane as a 24-bit fixed-point triple, built from the same
// pointLaneBytes table (points.go) — so the flash quad and the point plane run
// the same fragment op and cannot drift from each other. An uncovered texel is
// zero, whose lane is zero, whose fragment is (1,1,1,0): the blend forms dst × 1
// and writes the destination back unchanged, so the disc's transparent ring
// costs nothing and needs no key test.
//
// The disc is magnified by the view scale through the sampler rather than by the
// recorder's per-source-pixel loop. At the native and detail scales that is the
// same pixel set the loop covers — the span of source pixel c is
// [Project(c-Offset), Project(c-Offset+1)), which is c and 2c exactly — and at
// the 1.5× step the loop's alternating one- and two-wide columns become nearest
// sample columns instead (§13.11 "Divergences").
//
// # The halo: a fragment test
//
// A halo has no texture: it is one row applied inside a radius. The quad carries
// the pixel's offset from the disc centre in its per-corner custom lanes, so the
// fragment recovers the integer (dx, dy) the byte writer tested and runs the
// same dx² + dy² ≤ r² comparison [03 §4.3.1].

const (
	// flashAtlasWidth is the atlas page width in texels. The widest generated
	// frame is 200 [06 R-WFX-01 §2], so every frame fits a page.
	flashAtlasWidth = 1024
	// flashAtlasHeight is the page height. All three generated tables together
	// are 391,606 texels — twelve frames of side 64 down to 20, fifteen of 128
	// down by 7, fifteen of 200 down by 11 — and shelving them in the order the
	// battle first draws them needs about seven hundred rows of this width, so
	// one page always serves a battle and the packer never grows.
	flashAtlasHeight = 1024
	// flashAtlasPad is the border of zero texels reserved around every packed
	// frame, for the reason sceneAtlasPad exists (atlas.go): a magnified quad's
	// interpolated source coordinate can floor one texel past the frame's far
	// edge, and that texel must be the identity lane rather than the next disc's
	// ramp.
	flashAtlasPad = 1
)

// flashKey is a generated frame's identity: the table it belongs to and its
// index inside it, both already clamped by the recorder [06 R-WFX-01 §2].
type flashKey struct {
	table, frame int32
}

// flashRegion is one frame's placement: the page coordinates of its first INNER
// texel, so the reserved border stays outside every recorded source rectangle.
type flashRegion struct {
	x, y int32
}

// flashDiscAtlas owns the page, its staging bytes and the shelf packer. Frames
// are never freed — a frame is keyed by an identity that lives for the battle —
// so the packer needs no free list. The map is keyed by identity and never
// ranged in a way that reaches output, so it introduces no ordering [I1].
type flashDiscAtlas struct {
	img     *ebiten.Image
	buf     []byte
	regions map[flashKey]flashRegion
	// shelfX is the next free column of the open shelf, shelfY its top row and
	// shelfH its fixed height; next is the row a new shelf opens at.
	shelfX, shelfY, shelfH int
	next                   int
	// dirtyY0 and dirtyY1 bound the rows written since the last upload, so a
	// frame that packs nothing new hands the device nothing.
	dirtyY0, dirtyY1 int
}

// region returns the frame's placement, packing it on first use. It reports
// false only when the page cannot hold the frame, which the size arithmetic
// above makes unreachable for the three generated tables.
func (a *flashDiscAtlas) region(f drawlist.Flash) (flashRegion, bool) {
	side := int(f.Side)
	if side <= 0 || len(f.Rows) < side*side {
		return flashRegion{}, false
	}
	key := flashKey{f.Table, f.Frame}
	if r, ok := a.regions[key]; ok {
		return r, true
	}
	x, y, ok := a.alloc(side, side)
	if !ok {
		return flashRegion{}, false
	}
	a.write(x, y, side, f.Rows)
	if a.regions == nil {
		a.regions = make(map[flashKey]flashRegion)
	}
	r := flashRegion{int32(x), int32(y)}
	a.regions[key] = r
	return r, true
}

// alloc reserves a w×h region with its border and returns the origin of its
// first inner texel, creating the page on first use.
func (a *flashDiscAtlas) alloc(w, h int) (int, int, bool) {
	need := w + 2*flashAtlasPad
	tall := h + 2*flashAtlasPad
	if need > flashAtlasWidth {
		return 0, 0, false
	}
	if a.img == nil {
		a.img = ebiten.NewImage(flashAtlasWidth, flashAtlasHeight)
		a.buf = make([]byte, flashAtlasWidth*flashAtlasHeight*4)
	}
	if a.shelfH == 0 || a.shelfX+need > flashAtlasWidth || tall > a.shelfH {
		a.shelfX, a.shelfY, a.shelfH = 0, a.next, tall
		a.next += tall
	}
	if a.shelfY+tall > flashAtlasHeight {
		return 0, 0, false
	}
	x := a.shelfX + flashAtlasPad
	y := a.shelfY + flashAtlasPad
	a.shelfX += need
	return x, y, true
}

// write stores one frame's lanes and zeroes its border. rows is the frame's
// source texels row-major, each an LHT row or drawlist.FlashTransparentRow.
func (a *flashDiscAtlas) write(x, y, side int, rows []uint8) {
	for row := -flashAtlasPad; row < side+flashAtlasPad; row++ {
		off := ((y+row)*flashAtlasWidth + x - flashAtlasPad) * 4
		span := (side + 2*flashAtlasPad) * 4
		clear(a.buf[off : off+span])
		if row < 0 || row >= side {
			continue
		}
		off += flashAtlasPad * 4
		for _, r := range rows[row*side : row*side+side] {
			if r != drawlist.FlashTransparentRow {
				lane := &pointLaneBytes[r&31]
				a.buf[off+0], a.buf[off+1], a.buf[off+2], a.buf[off+3] = lane[0], lane[1], lane[2], 255
			}
			off += 4
		}
	}
	y0, y1 := y-flashAtlasPad, y+side+flashAtlasPad
	if a.dirtyY1 <= a.dirtyY0 || y0 < a.dirtyY0 {
		a.dirtyY0 = y0
	}
	if y1 > a.dirtyY1 {
		a.dirtyY1 = y1
	}
}

// flush hands the device the rows packed since the last flush. It runs just
// before a segment is submitted, so the texels a compiled quad samples are on
// the device before the draw that reads them; Ebitengine's queue keeps the two
// in order (points.go flush).
func (a *flashDiscAtlas) flush() {
	if a.img == nil || a.dirtyY1 <= a.dirtyY0 {
		return
	}
	y0, y1 := maxInt(a.dirtyY0, 0), minInt(a.dirtyY1, flashAtlasHeight)
	if y1 > y0 {
		rect := image.Rect(0, y0, flashAtlasWidth, y1)
		a.img.SubImage(rect).(*ebiten.Image).WritePixels(a.buf[y0*flashAtlasWidth*4 : y1*flashAtlasWidth*4])
	}
	a.dirtyY0, a.dirtyY1 = 0, 0
}

// Flash draws one calculated explosion disc as a single magnified quad over the
// disc atlas (docs/DESIGN_GPU_RENDERER.md §13.11).
func (r *Renderer) Flash(f drawlist.Flash) {
	if r == nil || r.surfaces[0] == nil || r.sceneDest == nil || r.tables.atlas == nil {
		return
	}
	// The projected extent is the union of the per-source-pixel spans the point
	// lane walks: source pixel c covers [Project(c-Offset), Project(c-Offset+1))
	// (DESIGN_GPU_RENDERER §14.2).
	x0 := f.X + f.Scale.Project(-f.Offset)
	x1 := f.X + f.Scale.Project(f.Side-f.Offset)
	y0 := f.Y + f.Scale.Project(-f.Offset)
	y1 := f.Y + f.Scale.Project(f.Side-f.Offset)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	cx0, cy0, cx1, cy1 := clipLitDisc(int(x0), int(y0), int(x1), int(y1), f.Clip, r.clipW(), r.clipH())
	if cx0 >= cx1 || cy0 >= cy1 {
		return
	}
	region, ok := r.sched.flash.region(f)
	if !ok {
		return
	}
	if !r.sched.beginBlended(schedDest, cx0, cy0, cx1, cy1,
		[4]*ebiten.Image{0: r.sched.flash.img, 1: r.tables.atlas},
		nil, blendScaleDestination, schedReadNone) {
		return
	}
	// The clip takes a sub-rectangle of the projected extent, so the source
	// endpoints are the same linear map evaluated at the clipped destination
	// edges: the sample a fragment lands on is the one it would have landed on
	// unclipped.
	side := float32(f.Side)
	kx, ky := side/float32(x1-x0), side/float32(y1-y0)
	sx0 := float32(region.x) + float32(cx0-int(x0))*kx
	sx1 := float32(region.x) + float32(cx1-int(x0))*kx
	sy0 := float32(region.y) + float32(cy0-int(y0))*ky
	sy1 := float32(region.y) + float32(cy1-int(y0))*ky
	r.sched.quad(schedDest,
		float32(cx0), float32(cy0), float32(cx1), float32(cy1),
		sx0, sy0, sx1, sy1,
		[4]float32{}, [4]float32{0, 0, 0, destOpLaneAtlas})
	r.modelStats.Flashes++
}

// Halo draws one flat LHT ground disc as a single quad whose fragment runs the
// byte writer's own inside-the-circle test (docs/DESIGN_GPU_RENDERER.md §13.11)
// [03 §4.3.1].
func (r *Renderer) Halo(h drawlist.Halo) {
	if r == nil || r.surfaces[0] == nil || r.sceneDest == nil || r.tables.atlas == nil || h.Radius <= 0 {
		return
	}
	// The byte writer walks dx and dy over [-Radius, Radius] inclusive, so the
	// covered extent is 2·Radius+1 pixels on a side.
	x0, y0 := int(h.X-h.Radius), int(h.Y-h.Radius)
	x1, y1 := int(h.X+h.Radius)+1, int(h.Y+h.Radius)+1
	cx0, cy0, cx1, cy1 := clipLitDisc(x0, y0, x1, y1, h.Clip, r.clipW(), r.clipH())
	if cx0 >= cx1 || cy0 >= cy1 {
		return
	}
	if !r.sched.beginBlended(schedDest, cx0, cy0, cx1, cy1,
		[4]*ebiten.Image{1: r.tables.atlas}, nil, blendScaleDestination, schedReadNone) {
		return
	}
	low, high := rowScaleLanes(lightScale(clampLHTRow(int(h.Row))))
	// The per-corner lanes are the corner's offset from the disc centre, so the
	// fragment's own interpolated value at a pixel centre is (px + 0.5 − cx),
	// whose floor is the integer dx the byte writer tested. r² rides the third
	// lane; the quad carries no source, like the trail marks.
	lx0, ly0 := float32(cx0-int(h.X)), float32(cy0-int(h.Y))
	lx1, ly1 := float32(cx1-int(h.X)), float32(cy1-int(h.Y))
	r2 := float32(h.Radius) * float32(h.Radius)
	r.sched.quadCorners(schedDest,
		[4]float32{float32(cx0), float32(cx1), float32(cx0), float32(cx1)},
		[4]float32{float32(cy0), float32(cy0), float32(cy1), float32(cy1)},
		[4]float32{low, high, 0, 0},
		[4][4]float32{
			{lx0, ly0, r2, destOpHalo},
			{lx1, ly0, r2, destOpHalo},
			{lx0, ly1, r2, destOpHalo},
			{lx1, ly1, r2, destOpHalo},
		})
	r.modelStats.Flashes++
}

// clipLitDisc intersects a lit disc's projected extent with the recorder's gate
// — the recording extent intersected with the terrain rectangle, which is what
// the point lane applies per pixel — and with the executor's own extent.
func clipLitDisc(x0, y0, x1, y1 int, clip drawlist.Rect, w, h int) (int, int, int, int) {
	x0 = maxInt(maxInt(x0, int(clip.X)), 0)
	y0 = maxInt(maxInt(y0, int(clip.Y)), 0)
	x1 = minInt(minInt(x1, int(clip.X)+int(clip.W)), w)
	y1 = minInt(minInt(y1, int(clip.Y)+int(clip.H)), h)
	return x0, y0, x1, y1
}
