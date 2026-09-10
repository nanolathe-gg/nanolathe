package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
)

// The lit point plane (docs/DESIGN_GPU_RENDERER.md §13.8).
//
// A flash disc contributes one lit point per covered pixel [03 R-FX-01 §4], and a
// 1080p battle frame covers a median 179,000 pixels that way. As four vertices
// and six indices each, that is thirty megabytes of vertex traffic per frame —
// written once by the executor and copied again by Ebitengine into its command
// queue — and it was the single largest cost of a presented frame, larger than
// the whole recorder.
//
// The plane replaces the geometry with a texture. A group of points that share a
// phase is written into a rectangle of one per-frame RGBA8 atlas, one texel per
// covered screen pixel, and committed as ONE quad over the group's bounding box.
// Four bytes per pixel replace two hundred and sixteen.
//
// # Why the covered and uncovered texels both come out exact
//
// The row families composite by the scale blend: with the fragment carrying
// (min(k,1), min(k,1), min(k,1), max(k−1,0)) the device forms
// dst × (src.rgb + src.a) = dst × k (§13.3). For every LHT row k = 1 + row/30 is
// at least one, so min(k,1) is exactly 1 and the whole of the per-pixel
// information is the high lane max(k−1,0).
//
// That lane is a multiple of 2⁻²³. The CPU forms it as fl(1 + fl(row/30)) − 1,
// and a sum whose value lies in [1,2) is a multiple of 2⁻²³, so subtracting one
// leaves a multiple of 2⁻²³; the rows the §13.3 clamp caps at 2 leave exactly 1.
// The lane times 2²³ is therefore an integer below 2²⁴ and fits three bytes with
// nothing lost. The shader reassembles it as r·65536 + g·256 + b — every term and
// every partial sum an integer below 2²⁴, so exact in binary32 whatever order a
// driver associates them in — and scales by 2⁻²³, which is exact because it is a
// power of two. The lane the fragment carries is bit-for-bit the lane the vertex
// carried, so the composite is byte-identical rather than merely close.
//
// An uncovered texel is zero, so its lane is zero and its fragment is
// (1,1,1,0): the blend forms dst × 1 and writes the destination back unchanged.
// That is what lets one quad cover a whole bounding box, including the pixels
// between the disc's spokes and the pixels other commands of the same phase own.
//
// # Why one quad over the box cannot reorder anything
//
// Placement is unchanged: every point is still placed by its own pixel through
// placePointSpan, and a group is the points that landed in ONE phase. Two points
// of one group can therefore never be the same pixel — a repeat is placed a phase
// later by construction — so the group's texels are pairwise distinct and one
// quad brightens each of them exactly once, as the per-pixel writes did. The
// quad's box may reach pixels of other commands of the same phase, but the plane
// is identity there, and the box is exactly the union rectangle the per-pixel
// placements already grew the phase's destination rectangle to.
const (
	// pointPlaneWidth is the atlas width. A group wider than this falls back to
	// per-pixel quads; a 1080p frame's widest disc is far below it.
	pointPlaneWidth = 2048
	// pointPlaneMaxHeight caps the atlas at thirty-two megabytes of staging
	// bytes. A frame that wants more puts the remainder through the quad path and
	// the next frame's atlas grows no further.
	pointPlaneMaxHeight = 4096
	// pointPlaneDensity is the break-even the group test uses: a plane region
	// costs about eight bytes per texel of its box (cleared, then uploaded) and
	// the quads it replaces about four hundred bytes per covered pixel (written
	// by the executor, copied by Ebitengine), so a box up to about fifty times
	// the covered pixel count is still cheaper as a plane. Below that density the
	// group keeps the quad path.
	pointPlaneDensity = 50
	// pointPlaneMinPixels keeps a handful of stray points off the atlas, where
	// the region, the sub-image upload and the quad would cost more than the
	// geometry.
	pointPlaneMinPixels = 24
)

// pointPlaneShelfHeights are the shelf height classes. A region opens or joins the
// shelf of the smallest class that holds it, so a rare tall region cannot make a
// whole shelf of ordinary discs as tall as itself: one open shelf shared by every
// height wasted about five times the area the regions needed. A region taller than
// the largest class gets a shelf of its own exact height.
var pointPlaneShelfHeights = [...]int{32, 64, 128, 256, 512, 1024}

// pointShelf is one open shelf of the atlas: its top row, its fixed height and the
// next free column.
type pointShelf struct {
	x, y, h int
	open    bool
}

// pointLaneBytes is the high lane of every LHT row, as the 24-bit fixed-point
// triple the plane stores and the shader reassembles. It is built once from the
// same rowScaleLanes/lightScale the quad path uses, so the two paths cannot
// drift.
var pointLaneBytes = buildPointLaneBytes()

func buildPointLaneBytes() [32][3]byte {
	var out [32][3]byte
	for row := 0; row < 32; row++ {
		_, high := rowScaleLanes(lightScale(row))
		n := uint32(float64(high) * (1 << 23))
		out[row] = [3]byte{byte(n >> 16), byte(n >> 8), byte(n)}
	}
	return out
}

// pointPlane is the per-frame atlas: one RGBA8 image, its staging bytes, and the
// shelf allocator. Everything is retained across frames, so a steady-state frame
// allocates only the sub-image an upload needs (§11.2 "Allocation policy").
type pointPlane struct {
	img  *ebiten.Image
	buf  []byte
	w, h int
	// shelves is the open shelf of each height class and next the atlas row a new
	// shelf opens at. next advances even when a shelf does not fit, so want ends
	// the frame holding the height the frame would have needed and the next frame
	// grows to it.
	shelves [len(pointPlaneShelfHeights)]pointShelf
	next    int
	want    int
	// dirtyY0 and dirtyY1 bound the rows written since the last upload, so a
	// segment hands the device only what changed.
	dirtyY0, dirtyY1 int
}

// resetFrame opens a new frame: it grows the atlas to what the previous frame
// wanted and rewinds the allocator. Growing here rather than mid-frame keeps every
// region a compiled quad refers to on the image that quad was bound to.
func (p *pointPlane) resetFrame() {
	if p.want > p.h {
		h := p.h
		if h < pointPlaneShelfHeights[0] {
			h = pointPlaneShelfHeights[0]
		}
		for h < p.want && h < pointPlaneMaxHeight {
			h *= 2
		}
		if h > pointPlaneMaxHeight {
			h = pointPlaneMaxHeight
		}
		if h != p.h {
			if p.img != nil {
				p.img.Deallocate()
			}
			p.img = ebiten.NewImage(pointPlaneWidth, h)
			p.buf = make([]byte, pointPlaneWidth*h*4)
			p.w, p.h = pointPlaneWidth, h
		}
	}
	p.shelves = [len(pointPlaneShelfHeights)]pointShelf{}
	p.next, p.want = 0, 0
	p.dirtyY0, p.dirtyY1 = 0, 0
}

// alloc reserves a w×h region and returns its origin. It reports false when the
// atlas cannot serve the region, which sends the group down the quad path.
func (p *pointPlane) alloc(w, h int) (int, int, bool) {
	if w <= 0 || h <= 0 || w > pointPlaneWidth {
		return 0, 0, false
	}
	class, shelfH := len(pointPlaneShelfHeights)-1, h
	for i, ch := range pointPlaneShelfHeights {
		if h <= ch {
			class, shelfH = i, ch
			break
		}
	}
	s := &p.shelves[class]
	if !s.open || s.x+w > pointPlaneWidth || h > s.h {
		*s = pointShelf{x: 0, y: p.next, h: shelfH, open: true}
		p.next += shelfH
		if p.next > p.want {
			p.want = p.next
		}
	}
	if p.img == nil || s.y+h > p.h {
		return 0, 0, false
	}
	x := s.x
	s.x += w
	return x, s.y, true
}

// clearRegion zeroes a reserved region, so every texel a group does not cover
// carries the identity lane, and records the rows the next upload has to carry.
func (p *pointPlane) clearRegion(rx, ry, w, h int) {
	for row := 0; row < h; row++ {
		off := ((ry+row)*p.w + rx) * 4
		clear(p.buf[off : off+w*4])
	}
	if p.dirtyY1 == 0 || ry < p.dirtyY0 {
		p.dirtyY0 = ry
	}
	if ry+h > p.dirtyY1 {
		p.dirtyY1 = ry + h
	}
}

// write stores one run's lanes into a region. rx, ry is the region origin and
// x0, y the run's screen position relative to the group's box.
func (p *pointPlane) write(rx, ry, x0, y int, rows []uint8) {
	off := ((ry+y)*p.w + rx + x0) * 4
	for _, row := range rows {
		lane := &pointLaneBytes[row&31]
		p.buf[off+0], p.buf[off+1], p.buf[off+2], p.buf[off+3] = lane[0], lane[1], lane[2], 255
		off += 4
	}
}

// flush hands the device the rows written since the last flush. It runs just
// before a segment is submitted, so the texels a compiled quad samples are on the
// device before the draw that reads them; Ebitengine's queue keeps the two in
// order.
func (p *pointPlane) flush() {
	if p.dirtyY1 <= p.dirtyY0 || p.img == nil {
		return
	}
	y0, y1 := p.dirtyY0, minInt(p.dirtyY1, p.h)
	if y1 > y0 {
		rect := image.Rect(0, y0, p.w, y1)
		p.img.SubImage(rect).(*ebiten.Image).WritePixels(p.buf[y0*p.w*4 : y1*p.w*4])
	}
	p.dirtyY0, p.dirtyY1 = 0, 0
}
