package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The destination-reading families for the modern executor beyond fog: the
// translucent strip blit (BlitTinted), translucent feature bodies and shadows,
// the UI light/shade rects (FillLitRect/FillShadeRect) and the lit point batch
// (PointLit), plus the source-through-LHT strip blit (BlitLit), which reads no
// destination (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4, C-G7).
//
// Every destination-reading family runs over the phase snapshot: before a phase's
// destination batch writes the offscreen, the union rectangle of that batch is
// copied from the offscreen into destScratch, and the pass reads destScratch (the
// pre-batch destination) while writing the offscreen — the same read/write split
// fog uses for its gray layer (C-G7), so a destination-reading write never
// samples a pixel it just changed. The scheduler keeps a batch's rectangles
// pairwise disjoint and opens the next phase for an overlapping command, so an
// overlapping later command still reads the earlier command's writes, exactly as
// the byte writers do when they read c.indexed in record order
// (§11.2 "The scheduler")[03 R-COMP-01 §2].

// snapshotRect copies the offscreen's [x0,x1)×[y0,y1) rect into destScratch, so a
// following destination-reading pass reads the pre-batch destination there
// (C-G7). destScratch is full-surface and aligned 1:1 with the offscreen, so a
// screen pixel in the rect reads its own pre-batch index. The rect is clamped to
// the framebuffer. A plain image copy under BlendCopy reproduces the stored bytes
// exactly: sampling is nearest and no colour scale applies (C-G4).
func (r *Renderer) snapshotRect(x0, y0, x1, y1 int) {
	if r.destScratch == nil || r.offscreen == nil {
		return
	}
	x0, y0 = maxInt(x0, 0), maxInt(y0, 0)
	x1, y1 = minInt(x1, r.w), minInt(y1, r.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	rect := image.Rect(x0, y0, x1, y1)
	r.snapshotOpt.Blend = ebiten.BlendCopy
	r.snapshotOpt.GeoM.Reset()
	r.snapshotOpt.GeoM.Translate(float64(x0), float64(y0))
	r.destScratch.DrawImage(r.offscreen.SubImage(rect).(*ebiten.Image), &r.snapshotOpt)
	r.frameDraws++
}

// clampLHTRow clamps a light level to the LHT's 0..31 row range, matching
// palette.Tables.LightLookup's own clamp so the GPU row equals the byte writer's
// row exactly [03 §4.3.1].
func clampLHTRow(level int) int {
	if level < 0 {
		return 0
	}
	if level > 31 {
		return 31
	}
	return level
}

// drawLit reproduces uiBlitLitRaw: every opaque source texel is written as
// LightLookup(row, src) — the SOURCE index folded through one LHT row — and the
// transparent key is skipped (docs/DESIGN_GPU_RENDERER.md §2.3)[03 §4.3.1]. It
// reads no destination, so it joins the opaque batch; the geometry is the plain
// keyed blit's clip to the framebuffer at (x,y) with no anchor offset, exactly as
// uiBlitLitRaw walks x+col / y+row. row is the caller's LHT row clamped to 0..31.
func (r *Renderer) drawLit(f *formats.GAFFrame, x, y, row int) {
	if f == nil || r.scene2D == nil || r.tables.atlas == nil {
		return
	}
	e := r.sceneFrameFor(f)
	if !e.ok {
		return
	}
	fw, fh := int(f.Width), int(f.Height)
	col0, col1 := maxInt(0, -x), minInt(fw, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(fh, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	imgs := r.sceneImages(e)
	imgs[1] = r.tables.atlas
	if !r.sched.begin(schedOpaque, x+col0, y+row0, x+col1, y+row1, imgs) {
		return
	}
	r.sched.quad(schedOpaque,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{float32(tableRowLHT + row), 0, 0, 0},
		[4]float32{0, 0, 0, sceneOpLit})
}

// drawTint reproduces tintedBlitAnchor: it subtracts the frame's authored anchor
// offsets, clips to the framebuffer, and for every opaque source texel writes
// ALP[src*256 + dst] — a destination read (docs/DESIGN_GPU_RENDERER.md §2.3)
// [03 R-COMP-01 §2][03 R-FX-02 §2]. The family's gate is "ALP present": with no
// palette it draws nothing, exactly as tintedBlitAnchor returns on a nil palette
// [03 R-COMP-01 §2].
func (r *Renderer) drawTint(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil || r.sceneDest == nil || r.tables.atlas == nil || r.destScratch == nil {
		return
	}
	e := r.sceneFrameFor(f)
	if !e.ok {
		return
	}
	x -= int(f.XOffset)
	y -= int(f.YOffset)
	fw, fh := int(f.Width), int(f.Height)
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.w), minInt(clipY+clipH, r.h)
	col0, col1 := maxInt(0, minX-x), minInt(fw, maxX-x)
	row0, row1 := maxInt(0, minY-y), minInt(fh, maxY-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	dx0, dy0, dx1, dy1 := x+col0, y+row0, x+col1, y+row1
	imgs := r.sceneImages(e)
	imgs[1], imgs[2] = r.tables.atlas, r.destScratch
	if !r.sched.begin(schedDest, dx0, dy0, dx1, dy1, imgs) {
		return
	}
	r.sched.quad(schedDest,
		float32(dx0), float32(dy0), float32(dx1), float32(dy1),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{}, [4]float32{0, 0, 0, destOpTint})
}

// drawLitRect reproduces uiLightRectRaw: every pixel of the framebuffer-clipped
// rect is rewritten as LightLookup(level, dst) — a destination read through one
// LHT row (docs/DESIGN_GPU_RENDERER.md §2.3)[03 §4.3.1]. The level is clamped to
// the LHT's 0..31 row range as LightLookup does.
func (r *Renderer) drawLitRect(x, y, w, h, level int) {
	r.drawDestRect(x, y, w, h, tableRowLHT+clampLHTRow(level))
}

// drawShadeRect reproduces uiShadeRectRaw: every pixel of the framebuffer-clipped
// rect is rewritten through the signed fade table — SHD row level+32 (clamped at
// -32) for a negative level, else LHT row level — a destination read
// (docs/DESIGN_GPU_RENDERER.md §2.3)[03 R-COMP-02 §5]. The row selection and its
// clamps match uiShadeRectRaw exactly.
func (r *Renderer) drawShadeRect(x, y, w, h, level int) {
	useShade := level < 0
	row := level
	if useShade {
		if row < -32 {
			row = -32
		}
		row += 32
	}
	if row > 31 {
		row = 31
	}
	if row < 0 {
		row = 0
	}
	base := tableRowLHT
	if useShade {
		base = tableRowSHD
	}
	r.drawDestRect(x, y, w, h, base+row)
}

// drawDestRect is the shared body of the UI light/shade rects: it clips the rect
// to the framebuffer and rewrites every pixel as TABLE[row][dst] through the
// destination pass (docs/DESIGN_GPU_RENDERER.md §2.3). row is the absolute table
// atlas row and rides the vertex colour, constant across the quad. With no
// palette installed there is no table atlas and nothing is drawn, matching the
// byte writers' nil-palette return.
func (r *Renderer) drawDestRect(x, y, w, h, row int) {
	if r.sceneDest == nil || r.tables.atlas == nil || r.destScratch == nil || w <= 0 || h <= 0 {
		return
	}
	x0, y0 := maxInt(x, 0), maxInt(y, 0)
	x1, y1 := minInt(x+w, r.w), minInt(y+h, r.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.begin(schedDest, x0, y0, x1, y1, [4]*ebiten.Image{1: r.tables.atlas, 2: r.destScratch}) {
		return
	}
	r.appendDestTableQuad(float32(x0), float32(y0), float32(x1), float32(y1), row)
}

// drawLitPoints reproduces the PointLit branch of classicSink.Points: each point
// rewrites its destination pixel as LightLookup(pt.Index, dst) — a destination
// read through the point's own LHT row (docs/DESIGN_GPU_RENDERER.md §2.3)
// [03 §4.3.1][03 R-FX-01 §4]. Each point is placed on its own, so the batch tags
// the cells its points occupy rather than its bounding rectangle, and only a
// point that repeats a pixel this phase already wrote opens the next phase —
// that write must observe the earlier one, exactly as the byte writer's
// record-order chain does (§11.2 "The scheduler"). Points outside the
// framebuffer are skipped, matching the producers' own clip.
func (r *Renderer) drawLitPoints(points []drawlist.Point) {
	if r.sceneDest == nil || r.tables.atlas == nil || r.destScratch == nil || len(points) == 0 {
		return
	}
	imgs := [4]*ebiten.Image{1: r.tables.atlas, 2: r.destScratch}
	for _, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if x < 0 || y < 0 || x >= r.w || y >= r.h {
			continue
		}
		r.sched.beginPoint(x, y, imgs)
		r.appendDestTableQuad(float32(x), float32(y), float32(x+1), float32(y+1),
			tableRowLHT+clampLHTRow(int(pt.Index)))
	}
}

// appendDestTableQuad appends one axis-aligned quad covering [dx0,dx1)×[dy0,dy1)
// for the destination table op: the absolute table atlas row rides the red vertex
// lane and the fragment reads the phase snapshot under its own screen pixel
// (C-G4).
func (r *Renderer) appendDestTableQuad(dx0, dy0, dx1, dy1 float32, row int) {
	r.sched.quad(schedDest, dx0, dy0, dx1, dy1, 0, 0, 0, 0,
		[4]float32{float32(row), 0, 0, 0}, [4]float32{0, 0, 0, destOpTable})
}
