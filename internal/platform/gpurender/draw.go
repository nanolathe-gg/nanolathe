package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The destination-independent 2D primitive families for the modern executor:
// solid and outline fills, the Bresenham line, and the plain point batch
// (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). Each writes a physical palette index
// straight into the indexed offscreen, so the stored bytes match the classic byte
// writers exactly. Every write is an axis-aligned integer quad, which rasterizes
// to exactly the classic rectangle's pixels; a single-pixel write is a 1×1 quad.
// Lines and outline frames are reduced to the exact pixel set the classic integer
// algorithms produce and drawn as 1×1 quads, because a GPU-native line or thin
// quad would not match the integer Bresenham / per-edge clip [03 §5.4][R-SEL-02A].
//
// None of them draws directly: each compiles into the scheduler's opaque batch
// under the scene shader's constant-index op (§11.2), so a whole phase's fills,
// lines, points, sprites, glyphs and terrain leave as one device draw.

// Fill replays one indexed rectangle. This unit implements the four
// destination-independent styles — Solid, Outline, SolidInclusive and
// FrameInclusive — reproducing the classic fillIndexedRect, frameIndexedRect,
// fillRectInclusive and drawIndexedFrameInclusive clip semantics exactly. The
// destination-reading LitRect/ShadeRect styles compile into the destination
// batch in deststage.go [03 §4.3.1][03 R-COMP-02 §5].
func (r *Renderer) Fill(f drawlist.Fill) {
	if r == nil || r.surfaces[0] == nil {
		return
	}
	switch f.Style {
	case drawlist.FillSolid:
		// fillIndexedRect: exclusive extent, clamped to the framebuffer.
		r.fillSolidExclusive(int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), f.Index)
	case drawlist.FillSolidInclusive:
		// fillRectInclusive: recover the inclusive bounds right=X+W-1, bottom=Y+H-1,
		// then apply the reject/clamp/re-check clip the byte writer uses
		// [03 R-FX-01 §6][R-P0-19-P].
		r.fillSolidInclusive(f.Rect.X, f.Rect.Y, f.Rect.X+f.Rect.W-1, f.Rect.Y+f.Rect.H-1, f.Index)
	case drawlist.FillOutline:
		// frameIndexedRect: an inclusive one-pixel frame r=(X,Y,X+W-1,Y+H-1)
		// clipped to the whole framebuffer [R-SEL-02A].
		r.drawFrameInclusive(
			int(f.Rect.X), int(f.Rect.Y), int(f.Rect.X+f.Rect.W-1), int(f.Rect.Y+f.Rect.H-1),
			0, 0, r.clipW()-1, r.clipH()-1, f.Index)
	case drawlist.FillFrameInclusive:
		// drawIndexedFrameInclusive: inclusive frame clipped independently per edge
		// against the record's Clip [R-SEL-02A].
		r.drawFrameInclusive(
			int(f.Rect.X), int(f.Rect.Y), int(f.Rect.X+f.Rect.W-1), int(f.Rect.Y+f.Rect.H-1),
			int(f.Clip.X), int(f.Clip.Y), int(f.Clip.X+f.Clip.W-1), int(f.Clip.Y+f.Clip.H-1), f.Index)
	case drawlist.FillLitRect:
		// uiLightRectRaw: rewrite each pixel as LightLookup(level, dst) through one
		// LHT row; destination-reading, so it joins the destination batch
		// [03 §4.3.1].
		r.drawLitRect(int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), int(f.Level))
	case drawlist.FillShadeRect:
		// uiShadeRectRaw: rewrite each pixel through the signed fade table (SHD for a
		// negative level, else LHT); destination-reading [03 R-COMP-02 §5].
		r.drawShadeRect(int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), int(f.Level))
	}
}

// beginSolid opens one constant-index command covering the clipped rectangle. It
// reports false when the rectangle covers nothing.
func (r *Renderer) beginSolid(x0, y0, x1, y1 int) bool {
	if r.scene2D == nil {
		return false
	}
	return r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{})
}

// appendSolidQuad appends one axis-aligned quad covering [dx0,dx1)×[dy0,dy1) as
// two triangles, every vertex carrying the physical palette index in the red
// colour lane and the constant-index op in Custom3 (C-G4).
func (r *Renderer) appendSolidQuad(dx0, dy0, dx1, dy1 float32, idx uint8) {
	r.sched.quad(schedOpaque, dx0, dy0, dx1, dy1, 0, 0, 0, 0,
		[4]float32{float32(idx), 0, 0, 0}, [4]float32{0, 0, 0, sceneOpSolid})
}

// fillSolidExclusive reproduces internal/client fillIndexedRect: clamp the
// exclusive extent [x,x+w)×[y,y+h) to the framebuffer and fill it with idx.
func (r *Renderer) fillSolidExclusive(x, y, w, h int, idx uint8) {
	x0, y0 := x, y
	x1, y1 := x+w, y+h
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > r.clipW() {
		x1 = r.clipW()
	}
	if y1 > r.clipH() {
		y1 = r.clipH()
	}
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.beginSolid(x0, y0, x1, y1) {
		return
	}
	r.appendSolidQuad(float32(x0), float32(y0), float32(x1), float32(y1), idx)
}

// fillSolidInclusive reproduces internal/client fillRectInclusive: reject when
// the inclusive rectangle lies wholly off the framebuffer, clamp each inclusive
// edge, re-check non-empty, then fill [left..right]×[top..bottom] [R-P0-19-P].
func (r *Renderer) fillSolidInclusive(left, top, right, bottom int32, idx uint8) {
	clipRight, clipBottom := int32(r.clipW())-1, int32(r.clipH())-1
	if right < 0 || left > clipRight || bottom < 0 || top > clipBottom {
		return
	}
	if left < 0 {
		left = 0
	}
	if top < 0 {
		top = 0
	}
	if right > clipRight {
		right = clipRight
	}
	if bottom > clipBottom {
		bottom = clipBottom
	}
	if left > right || top > bottom {
		return
	}
	if !r.beginSolid(int(left), int(top), int(right)+1, int(bottom)+1) {
		return
	}
	// Inclusive [left..right] covers the exclusive quad [left, right+1).
	r.appendSolidQuad(float32(left), float32(top), float32(right+1), float32(bottom+1), idx)
}

// drawFrameInclusive reproduces internal/client drawIndexedFrameInclusive: a
// one-pixel inclusive frame whose four edges are each clipped independently
// against the inclusive clip rectangle, then written as the exact pixel set. It
// draws that pixel set as 1×1 quads so the covered pixels match the byte writer
// including its corner and degenerate-edge behaviour [R-SEL-02A].
func (r *Renderer) drawFrameInclusive(rMinX, rMinY, rMaxX, rMaxY, clipMinX, clipMinY, clipMaxX, clipMaxY int, idx uint8) {
	if rMinX > rMaxX || rMinY > rMaxY || clipMinX > clipMaxX || clipMinY > clipMaxY {
		return
	}
	// The command's screen rectangle is the frame's clipped bounding box; the
	// per-pixel writes below stay inside it.
	bx0 := maxInt(maxInt(rMinX, clipMinX), 0)
	by0 := maxInt(maxInt(rMinY, clipMinY), 0)
	bx1 := minInt(minInt(rMaxX, clipMaxX)+1, r.clipW())
	by1 := minInt(minInt(rMaxY, clipMaxY)+1, r.clipH())
	if !r.beginSolid(bx0, by0, bx1, by1) {
		return
	}
	write := func(x, y int) {
		if x < 0 || y < 0 || x >= r.clipW() || y >= r.clipH() {
			return
		}
		if x < clipMinX || x > clipMaxX || y < clipMinY || y > clipMaxY {
			return
		}
		r.appendSolidQuad(float32(x), float32(y), float32(x+1), float32(y+1), idx)
	}
	horizontal := func(y int) {
		if y < clipMinY || y > clipMaxY {
			return
		}
		left, right := maxInt(rMinX, clipMinX), minInt(rMaxX, clipMaxX)
		for x := left; x <= right; x++ {
			write(x, y)
		}
	}
	vertical := func(x int) {
		if x < clipMinX || x > clipMaxX {
			return
		}
		top, bottom := maxInt(rMinY, clipMinY), minInt(rMaxY, clipMaxY)
		for y := top; y <= bottom; y++ {
			write(x, y)
		}
	}
	horizontal(rMinY)
	if rMaxY != rMinY {
		horizontal(rMaxY)
	}
	vertical(rMinX)
	if rMaxX != rMinX {
		vertical(rMaxX)
	}
}

// Line replays one indexed line. A GPU-native line does not match the classic
// integer Bresenham, so this walks the identical Bresenham sequence and draws
// each point as a 1×1 quad; points off the framebuffer no-op, exactly as the
// byte writer's per-point clip skips them [03 §5.4].
func (r *Renderer) Line(l drawlist.Line) {
	if r == nil || r.surfaces[0] == nil {
		return
	}
	if l.Emissive {
		// A beam or lightning stroke is a light source for the glow layer (§19).
		r.glowLine(l)
	}
	bx0 := maxInt(minInt(int(l.X0), int(l.X1)), 0)
	by0 := maxInt(minInt(int(l.Y0), int(l.Y1)), 0)
	bx1 := minInt(maxInt(int(l.X0), int(l.X1))+1, r.clipW())
	by1 := minInt(maxInt(int(l.Y0), int(l.Y1))+1, r.clipH())
	if !r.beginSolid(bx0, by0, bx1, by1) {
		return
	}
	x0, y0, x1, y1 := l.X0, l.Y0, l.X1, l.Y1
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	sx := int32(1)
	if x0 > x1 {
		sx = -1
	}
	dy := y1 - y0
	if dy < 0 {
		dy = -dy
	}
	sy := int32(1)
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		if x0 >= 0 && x0 < int32(r.clipW()) && y0 >= 0 && y0 < int32(r.clipH()) {
			r.appendSolidQuad(float32(x0), float32(y0), float32(x0+1), float32(y0+1), l.Index)
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// Points replays one batch of single-pixel writes. The destination-independent
// PointPlain kind draws each point as a 1×1 quad carrying its own physical index,
// in array order so a later point overwrites an earlier one at the same pixel,
// matching the classic in-order write [03 §5.5]. The destination-reading PointLit
// kind compiles into the destination batch in deststage.go.
func (r *Renderer) Points(p drawlist.Points) {
	if r == nil || r.surfaces[0] == nil {
		return
	}
	if p.Kind == drawlist.PointLit {
		// PointLit folds each destination pixel through the point's own LHT row; it
		// is destination-reading [03 §4.3.1].
		r.drawLitPoints(p.Points)
		return
	}
	if len(p.Points) == 0 {
		return
	}
	x0, y0, x1, y1, ok := r.pointBounds(p.Points)
	if !ok || !r.beginSolid(x0, y0, x1, y1) {
		return
	}
	for _, pt := range p.Points {
		x, y := int(pt.X), int(pt.Y)
		if x < 0 || y < 0 || x >= r.clipW() || y >= r.clipH() {
			continue
		}
		r.appendSolidQuad(float32(x), float32(y), float32(x+1), float32(y+1), pt.Index)
	}
}

// pointBounds returns the framebuffer-clipped bounding rectangle of the visible
// points, the command rectangle the scheduler tests against the phase grid.
func (r *Renderer) pointBounds(points []drawlist.Point) (x0, y0, x1, y1 int, ok bool) {
	for _, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if x < 0 || y < 0 || x >= r.clipW() || y >= r.clipH() {
			continue
		}
		if !ok {
			x0, y0, x1, y1, ok = x, y, x+1, y+1, true
			continue
		}
		x0, y0 = minInt(x0, x), minInt(y0, y)
		x1, y1 = maxInt(x1, x+1), maxInt(y1, y+1)
	}
	return x0, y0, x1, y1, ok
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
