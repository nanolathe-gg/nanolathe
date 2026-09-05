package client

// The indexed rasterizer the model draw writes into: the screen polygon, the
// span walk with its interpolated attribute lanes, the key-plane admission
// test and the supersample resolve [03 R-RAST-01].

import (
	"github.com/nanolathe/nanolathe/formats"
)

// spanU..spanRow index the attributes the two-chain edge walk carries beside
// the edge X. Retail promotes each of them to signed 16.16 when the walk
// starts, steps it down the chain edges, and steps it again across the span
// [R-RAST-01 §1] steps 3-5. The SHD row rides the same interpolation as the
// key, chosen per vertex from the smoothed normal [R-RAST-01 §5].
const (
	spanU = iota
	spanV
	spanKey
	spanRow
	spanAttrs
)

// spanEdgeBias is what retail adds to a chain edge's starting 16.16 X: the
// largest 16.16 fraction, one step short of a whole unit. Followed by the
// arithmetic shift down it is a ceiling for every non-integer edge position and
// the identity for an integer one, which is the section's whole pixel-coverage
// rule [R-RAST-01 §1] steps 3 and 5. Attributes carry no such bias.
const spanEdgeBias int64 = (1 << 16) - 1

// faceIdentity is the per-face identity the parity trace records beside each
// candidate pixel. It carries no geometry: the two-chain walk of
// [R-RAST-01 §1] owns every corner, and this is only what a trace event needs
// to name the face a candidate came from.
//
// It was the retained triangle record until the barycentric filler and its
// fan triangulation were deleted; the geometry lanes went with them.
type faceIdentity struct {
	color      uint8
	frame      *formats.GAFFrame
	useSHD     bool // textured texels pass through PALETTE.SHD [R-RND-02A]
	candidate  uint32
	piece      int
	primitive  int
	texture    string
	frameIndex int
	frameState RendererValueState
}

// spanEdge is one row of a chain's edge table: the pixel X the chain reaches on
// that row, and the attributes still in 16.16 [R-RAST-01 §1] step 3.
//
// The table carries no "written" mark and is never cleared between faces,
// exactly as retail's is not. Each chain is an index path from the top corner
// to the bottom corner, so every row level between the two is crossed by at
// least one descending edge of each chain, and the fill reads only those rows;
// a row a folded chain crosses twice holds the later edge's values. That is
// why retail's tables can be uninitialised scratch [R-RAST-01 §1] step 3.
type spanEdge struct {
	x int32
	a [spanAttrs]int64
}

// screenPoly is one authored primitive as retail's scan converter consumes it:
// the projected corners in **authored index order** — never re-ordered, never
// fan-triangulated — with one integer attribute lane per corner.
//
// Index order is load-bearing. The walk treats the chain of decreasing indices
// from the topmost corner as the left edge and the chain of increasing indices
// as the right edge, so the vertex order is what decides which faces paint at
// all [R-RAST-01 §1] steps 3, 4 and 7.
type screenPoly struct {
	x, y []int32
	// oddHeight records the low bit of each corner's model-relative whole-unit
	// height. The supersampled shear is (2*z) - y rather than 2*(z - (y>>1)),
	// which is one pixel lower exactly when that bit is set [R-REN-03A §6].
	oddHeight []bool
	// attr holds the per-corner lanes the walk interpolates, indexed by
	// spanU..spanRow. u and v are texel coordinates, not normalised: retail's
	// quad mapper defaults the four corners to (0,0) (w-1,0) (w-1,h-1) (0,h-1)
	// in vertex-index order [R-RAST-01 §1].
	attr [spanAttrs][]int32

	color      uint8
	frame      *formats.GAFFrame
	useSHD     bool // textured texels pass through PALETTE.SHD [R-RND-02A]
	candidate  uint32
	piece      int
	primitive  int
	texture    string
	frameIndex int
	frameState RendererValueState
}

// newScreenPoly allocates one face's corner lanes out of a single backing
// array, so a model with a few hundred primitives costs a few hundred
// allocations rather than a few thousand.
func newScreenPoly(n int) screenPoly {
	buf := make([]int32, n*(2+spanAttrs))
	p := screenPoly{
		x:         buf[0:n:n],
		y:         buf[n : 2*n : 2*n],
		oddHeight: make([]bool, n),
	}
	for k := 0; k < spanAttrs; k++ {
		lo := (2 + k) * n
		p.attr[k] = buf[lo : lo+n : lo+n]
	}
	return p
}

// traceFace adapts a face to the per-candidate trace helpers, which read only
// the face's identity fields and never its corners.
func (p *screenPoly) traceFace() *faceIdentity {
	return &faceIdentity{
		color: p.color, frame: p.frame, useSHD: p.useSHD,
		candidate: p.candidate, piece: p.piece, primitive: p.primitive,
		texture: p.texture, frameIndex: p.frameIndex, frameState: p.frameState,
	}
}

// spanByte narrows an interpolated 16.16 attribute the way the span writers'
// key test does: an arithmetic shift down, then the low byte [R-RAST-01 §1]
// step 6.
func spanByte(v int64) uint8 { return uint8((v >> 16) & 0xFF) }

// modelTarget is the retail per-unit composition image [R-REN-03A §1]. It is
// sized to the model's own projected extent plus a two-pixel margin, not to
// the framebuffer, and it records where the model's own (0,0) sits inside it
// so the finished image can be blitted at the unit's screen anchor.
//
// The colour plane is prefilled with transparentIndex and the final blit skips
// that index, so uncovered pixels leave terrain and earlier world passes
// untouched. The height plane is the per-pixel depth buffer of
// [R-REN-03A §2]; it is nil when the unit's definition authors no ZBuffer,
// and every writer then falls through to unconditional painter-order writes,
// exactly as retail's span writers do with a null key plane.
type modelTarget struct {
	color, height    []uint8
	covered          []bool
	width, heightPx  int   // image dimensions
	originX, originY int32 // image pixel holding the model's own (0,0)
	anchorX, anchorY int32 // framebuffer pixel holding the model's own (0,0)
	transparent      uint8
	// scale is 1, or 2 while the structure anti-alias supersample is active
	// [R-REN-03A §6]. It is descriptive: the caller has already multiplied the
	// dimensions and origin.
	scale  int32
	trace  *rendererTrace
	winner []int
	tick   uint32
	// scanLeft and scanRight are the two chains' edge tables [R-RAST-01 §1]
	// steps 3-4, retained across the faces of one composition so the walk
	// allocates once per image rather than once per primitive.
	scanLeft, scanRight []spanEdge
}

// transparentModelIndex is the composition image's background. Retail prefills
// the colour plane with it and records it in the image header as the index the
// final blit skips; it is also what the anti-alias downscale blends against at
// the silhouette, which is where the red/purple fringe comes from
// [R-REN-03A §1][R-REN-03A §7].
const transparentModelIndex uint8 = 1

// modelTargetMargin is retail's two-pixel border on every side of the measured
// extent [R-REN-03A §1].
const modelTargetMargin int32 = 2

func newModelTarget(width, height int) *modelTarget {
	return newModelImage(width, height, 0, 0, 0, 0, true, 1)
}

// newModelImage allocates a composition image. keyPlane follows the unit's
// ZBuffer authoring [R-REN-03A §2].
func newModelImage(width, height int, originX, originY, anchorX, anchorY int32, keyPlane bool, scale int32) *modelTarget {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	n := width * height
	t := &modelTarget{
		color: make([]uint8, n), covered: make([]bool, n),
		width: width, heightPx: height,
		originX: originX, originY: originY,
		anchorX: anchorX, anchorY: anchorY,
		transparent: transparentModelIndex,
		scale:       scale,
	}
	if keyPlane {
		t.height = make([]uint8, n)
	}
	for i := range t.color {
		t.color[i] = t.transparent
	}
	return t
}

// screenX and screenY map an image pixel back to the framebuffer. The image
// carries the model's (0,0) at (originX, originY) and that point lands on the
// framebuffer at (anchorX, anchorY) [R-REN-03A §1].
func (t *modelTarget) screenX(ix int32) int32 { return t.anchorX + ix - t.originX }
func (t *modelTarget) screenY(iy int32) int32 { return t.anchorY + iy - t.originY }

// imageX and imageY are the inverse, used to record framebuffer-space
// diagnostics against the image the model was rasterized into.
func (t *modelTarget) imageX(sx int32) int32 { return sx - t.anchorX + t.originX }
func (t *modelTarget) imageY(sy int32) int32 { return sy - t.anchorY + t.originY }

// admit applies the height-key test. With no key plane every candidate is
// admitted and composition is pure painter order [R-REN-03A §2].
func (t *modelTarget) admit(idx int, key uint8) bool {
	if t.height == nil {
		return true
	}
	if t.height[idx] > key {
		return false
	}
	t.height[idx] = key
	return true
}

func (t *modelTarget) write(idx int, color uint8, present bool) {
	if !present {
		// Nanoframe erase writes the image background, which the final blit
		// skips [03 §5.2][R-REN-03A §1].
		t.color[idx], t.covered[idx] = t.transparent, false
		return
	}
	t.color[idx], t.covered[idx] = color, true
}

func (t *modelTarget) storedKey(idx int) uint8 {
	if t == nil || t.height == nil || idx < 0 || idx >= len(t.height) {
		return 0
	}
	return t.height[idx]
}

// polyScan is retail's two-chain scanline edge walk over one face
// [R-RAST-01 §1] steps 1-5, in the composition-image variant: the bounds are
// the image's own, L = 0, T = 0, R = width-1, B = height-1.
//
// It replaces a fan triangulation with a barycentric point-in-triangle test.
// The two are not equivalent, and the difference is the whole point of the
// section. Retail never looks at a face as a whole: from the topmost corner it
// walks decreasing indices as the *left* chain and increasing indices as the
// *right* chain, and each row is painted only where the right chain is still
// strictly right of the left one. That per-scanline comparison is simultaneously
// the back-face cull — a ring that projects counter-clockwise with screen Y
// increasing downward yields an empty span on every row (step 7) — and the rule
// for a folded projection, which paints exactly the rows above the crossing and
// nothing at or below it.
//
// span is called once per painted row with the span already clamped to the
// image and the attribute accumulator positioned at its first pixel, together
// with the per-pixel step. Left and top are inclusive, right and bottom
// exclusive: the walk never writes the image's last row or last column, which
// the two-pixel margin of [R-REN-03A §1] makes harmless.
func (t *modelTarget) polyScan(p *screenPoly, span func(row, xl, xr int32, a, da *[spanAttrs]int64)) {
	n := len(p.x)
	if n < 3 || t == nil || t.width <= 0 || t.heightPx <= 0 {
		// A two-corner flat primitive makes both chains the same single edge,
		// so xr == xl on every row and nothing is drawn [R-RAST-01 §1] step 7.
		return
	}
	const boundLeft, boundTop = int32(0), int32(0)
	boundRight, boundBottom := int32(t.width)-1, int32(t.heightPx)-1

	// Step 1. The top and bottom indices are the FIRST corners attaining their
	// extreme, under a strict comparison; a tie keeps the earlier index.
	minY, maxY, minX, maxX := p.y[0], p.y[0], p.x[0], p.x[0]
	topIdx, botIdx := 0, 0
	for i := 1; i < n; i++ {
		if p.y[i] < minY {
			minY, topIdx = p.y[i], i
		}
		if p.y[i] > maxY {
			maxY, botIdx = p.y[i], i
		}
		if p.x[i] < minX {
			minX = p.x[i]
		}
		if p.x[i] > maxX {
			maxX = p.x[i]
		}
	}
	// Step 2. Reject against the bounds, then clip the row range. The fill is
	// exclusive of yEnd, so the bottom row is never written.
	if maxX < boundLeft || minX > boundRight || maxY < boundTop || minY > boundBottom {
		return
	}
	yStart, yEnd := minY, maxY
	if yStart < boundTop {
		yStart = boundTop
	}
	if yEnd > boundBottom {
		yEnd = boundBottom
	}
	if yStart >= yEnd {
		return
	}
	rows := int(yEnd - yStart)
	if cap(t.scanLeft) < rows || cap(t.scanRight) < rows {
		t.scanLeft = make([]spanEdge, rows)
		t.scanRight = make([]spanEdge, rows)
	}
	leftTab, rightTab := t.scanLeft[:rows], t.scanRight[:rows]

	// Steps 3 and 4. One walk serves both chains; only the index direction
	// differs. An edge contributes only when it descends — horizontal edges and
	// edges that go up are skipped outright — and both chains index the table
	// from yStart, so an edge clipped at T lands on the row an unclipped one
	// would have.
	walk := func(step int, tab []spanEdge) {
		cur := topIdx
		for guard := 0; guard <= n; guard++ {
			next := cur + step
			if next < 0 {
				next = n - 1
			} else if next >= n {
				next = 0
			}
			if p.y[next] > p.y[cur] {
				dy := int64(p.y[next] - p.y[cur])
				// The edge X is the only biased quantity: one fraction short of
				// a whole unit, followed by the arithmetic shift, is a ceiling
				// for a fractional position and the identity for a whole one,
				// which is what makes two faces sharing an edge neither overlap
				// nor leave a gap.
				x := int64(p.x[cur])<<16 + spanEdgeBias
				xStep := (int64(p.x[next]-p.x[cur]) << 16) / dy
				var a, aStep [spanAttrs]int64
				for k := 0; k < spanAttrs; k++ {
					a[k] = int64(p.attr[k][cur]) << 16
					aStep[k] = (int64(p.attr[k][next]-p.attr[k][cur]) << 16) / dy
				}
				row := p.y[cur]
				if row < boundTop {
					d := int64(boundTop - row)
					x += xStep * d
					for k := range a {
						a[k] += aStep[k] * d
					}
					row = boundTop
				}
				stop := p.y[next]
				if stop > boundBottom {
					stop = boundBottom
				}
				for ; row < stop; row++ {
					if i := int(row - yStart); i >= 0 && i < rows {
						tab[i].x, tab[i].a = int32(x>>16), a
					}
					x += xStep
					for k := range a {
						a[k] += aStep[k]
					}
				}
			}
			if next == botIdx {
				return
			}
			cur = next
		}
	}
	walk(-1, leftTab)  // step 3: decreasing indices are the left chain
	walk(+1, rightTab) // step 4: increasing indices are the right chain

	// Step 5. The composition span writers run only where xr > xl. The
	// per-pixel attribute step divides by the UNCLAMPED width, and only then is
	// the span clamped into the image.
	for r := yStart; r < yEnd; r++ {
		i := int(r - yStart)
		l, rt := &leftTab[i], &rightTab[i]
		if rt.x <= l.x {
			continue // step 7's cull, evaluated per scanline
		}
		width := int64(rt.x - l.x)
		var a, da [spanAttrs]int64
		for k := 0; k < spanAttrs; k++ {
			a[k] = l.a[k]
			da[k] = (rt.a[k] - l.a[k]) / width
		}
		xl, xr := l.x, rt.x
		if xl < boundLeft {
			d := int64(boundLeft - xl)
			for k := range a {
				a[k] += da[k] * d
			}
			xl = boundLeft
		}
		if xr > boundRight {
			xr = boundRight
		}
		if xr <= xl {
			continue
		}
		span(r, xl, xr, &a, &da)
	}
}

// resolveSupersample is retail's 2:1 resolve of an anti-aliased structure
// image [R-REN-03A §6]. The colour plane goes through three ALP lookups per
// output pixel — the two horizontal pairs first, then the two results — and
// the key plane is nearest-sampled from the top-left of each block with no
// blending at all.
//
// The filter does not exclude the image background from the blend, and that is
// deliberate: it is the retail defect that puts a one-pixel border of reds and
// dusty purples around every anti-aliased building. ALP's diagonal is exact
// identity, so a block wholly outside the model still resolves to the
// background index and stays transparent [R-REN-03A §7].
func (t *modelTarget) resolveSupersample(dst *modelTarget, alp *[65536]byte) {
	if t == nil || dst == nil || alp == nil {
		return
	}
	for y := 0; y < dst.heightPx; y++ {
		srcTop, srcBottom := 2*y*t.width, (2*y+1)*t.width
		if srcBottom+t.width > len(t.color) {
			break
		}
		for x := 0; x < dst.width; x++ {
			if 2*x+1 >= t.width {
				break
			}
			top := alp[int(t.color[srcTop+2*x])*256+int(t.color[srcTop+2*x+1])]
			bottom := alp[int(t.color[srcBottom+2*x])*256+int(t.color[srcBottom+2*x+1])]
			out := alp[int(top)*256+int(bottom)]
			i := y*dst.width + x
			dst.color[i] = out
			dst.covered[i] = out != dst.transparent
			if dst.height != nil && t.height != nil {
				dst.height[i] = t.height[srcTop+2*x]
			}
		}
	}
}

// commit blits the finished image into the framebuffer, skipping the
// background index [R-REN-03A §1].
func (t *modelTarget) commit(dst []uint8, width, height int) {
	if t == nil || width <= 0 || height <= 0 {
		return
	}
	for iy := 0; iy < t.heightPx; iy++ {
		sy := t.screenY(int32(iy))
		if sy < 0 || sy >= int32(height) {
			continue
		}
		row := int(sy) * width
		src := iy * t.width
		for ix := 0; ix < t.width; ix++ {
			i := src + ix
			if !t.covered[i] {
				continue
			}
			sx := t.screenX(int32(ix))
			if sx < 0 || sx >= int32(width) {
				continue
			}
			dst[row+int(sx)] = t.color[i]
		}
	}
}

// spanShadeRow narrows an interpolated SHD row. The rows the model path carries
// are already 0..31 by construction (`trunc(dot * 5.0) & 0x1F`), so the clamp
// only guards the table lookup [R-RAST-01 §5][03 §2.4.1].
func spanShadeRow(v int64) int {
	r := int(v >> 16)
	if r < 0 {
		return 0
	}
	if r > 31 {
		return 31
	}
	return r
}
