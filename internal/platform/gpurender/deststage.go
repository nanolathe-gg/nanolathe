package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"math"
)

// The destination-compositing families for the modern executor beyond fog: the
// translucent strip blit (BlitTinted), translucent feature bodies and shadows,
// the UI light/shade rects (FillLitRect/FillShadeRect) and the lit point batch
// (PointLit), plus the source-through-LHT strip blit (BlitLit), which reads no
// destination (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4, C-G7, §13.3).
//
// None of them reads the destination any more. In the true-colour composite the
// destination-side tables are the arithmetic they were generated from
// [03 §4.3.4], so each family hands the device a fragment and a blend instead:
// the ALP families the premultiplied half-colour (PAL[src]/2, 1/2) under
// source-over, and the row families the scale k under the scale blend
// (§13.2, §13.3). The GPU reads the framebuffer for them, so there is no
// snapshot and no copy.
//
// The order they impose is unchanged, and so is the scheduler: a command of this
// class is still placed one whole phase after every earlier command of the class
// it overlaps, so an overlapping later command still composites over the earlier
// command's result, exactly as the byte writers do when they read c.indexed in
// record order (§11.2 "The scheduler")[03 R-COMP-01 §2].

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

// drawLit writes every opaque source texel as LightLookup(row, src), clipping
// after preserving its source offset from (x,y). The transparent key is skipped
// [03 §4.3.1]. It reads no destination, so it joins the opaque batch. row is the
// caller's LHT row clamped to 0..31.
func (r *Renderer) drawLit(f *formats.GAFFrame, x, y, row, clipX, clipY, clipW, clipH int) {
	if f == nil || r.scene2D == nil || r.tables.atlas == nil {
		return
	}
	e := r.sceneFrameFor(f)
	if !e.ok {
		return
	}
	fw, fh := int(f.Width), int(f.Height)
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.clipW()), minInt(clipY+clipH, r.clipH())
	col0, col1 := maxInt(0, minX-x), minInt(fw, maxX-x)
	row0, row1 := maxInt(0, minY-y), minInt(fh, maxY-y)
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
// offsets, clips to the framebuffer, and for every opaque source texel composites
// the ALP builder's floor((src + dst)/2) over the destination
// (docs/DESIGN_GPU_RENDERER.md §2.3, §13.3)[03 §4.3.4][03 R-COMP-01 §2]
// [03 R-FX-02 §2]. The family's gate is "the palette is installed": with no
// palette it draws nothing, exactly as tintedBlitAnchor returns on a nil palette
// [03 R-COMP-01 §2].
func (r *Renderer) drawTint(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil || r.sceneDest == nil || r.tables.atlas == nil {
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
	maxX, maxY := minInt(clipX+clipW, r.clipW()), minInt(clipY+clipH, r.clipH())
	col0, col1 := maxInt(0, minX-x), minInt(fw, maxX-x)
	row0, row1 := maxInt(0, minY-y), minInt(fh, maxY-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	dx0, dy0, dx1, dy1 := x+col0, y+row0, x+col1, y+row1
	imgs := r.sceneImages(e)
	imgs[1] = r.tables.atlas
	if !r.sched.begin(schedDest, dx0, dy0, dx1, dy1, imgs) {
		return
	}
	r.sched.quad(schedDest,
		float32(dx0), float32(dy0), float32(dx1), float32(dy1),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{}, [4]float32{0, 0, 0, destOpTint})
}

// drawLitRect reproduces uiLightRectRaw: every pixel of the framebuffer-clipped
// rect is brightened through one LHT row (docs/DESIGN_GPU_RENDERER.md §2.3)
// [03 §4.3.1]. The level is clamped to the LHT's 0..31 row range as LightLookup
// does, and the row becomes the scale its builder multiplied by (§13.3).
func (r *Renderer) drawLitRect(x, y, w, h, level int) {
	r.drawDestRect(x, y, w, h, lightScale(clampLHTRow(level)))
}

// drawShadeRect reproduces uiShadeRectRaw: every pixel of the framebuffer-clipped
// rect is taken through the signed fade table — SHD row level+32 (clamped at -32)
// for a negative level, else LHT row level (docs/DESIGN_GPU_RENDERER.md §2.3)
// [03 R-COMP-02 §5]. The row selection and its clamps match uiShadeRectRaw
// exactly; only the lookup becomes the row's own scale (§13.3).
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
	if useShade {
		r.drawDestRect(x, y, w, h, shadeScale(row))
		return
	}
	r.drawDestRect(x, y, w, h, lightScale(row))
}

// drawDestRect is the shared body of the UI light/shade rects: it clips the rect
// to the framebuffer and scales every pixel of it by k through the destination
// pass (docs/DESIGN_GPU_RENDERER.md §2.3, §13.3). k rides the vertex colour,
// constant across the quad. With no palette installed nothing is drawn, matching
// the byte writers' nil-palette return.
func (r *Renderer) drawDestRect(x, y, w, h int, k float32) {
	if r.sceneDest == nil || r.tables.atlas == nil || w <= 0 || h <= 0 {
		return
	}
	x0, y0 := maxInt(x, 0), maxInt(y, 0)
	x1, y1 := minInt(x+w, r.clipW()), minInt(y+h, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.beginBlended(schedDest, x0, y0, x1, y1,
		[4]*ebiten.Image{1: r.tables.atlas}, nil, blendScaleDestination, schedReadNone) {
		return
	}
	r.appendDestTableQuad(float32(x0), float32(y0), float32(x1), float32(y1), k)
}

// drawLitPoints reproduces the PointLit branch of classicSink.Points: each point
// brightens its destination pixel through the point's own LHT row
// (docs/DESIGN_GPU_RENDERER.md §2.3)[03 §4.3.1][03 R-FX-01 §4]. Each point is
// placed on its own, so the batch tags the cells its points occupy rather than
// its bounding rectangle, and a point that repeats a pixel an earlier point of
// this segment wrote is placed one phase after it — that write must observe the
// earlier one, exactly as the byte writer's record-order chain does
// (§11.2 "The scheduler"). Points outside the framebuffer are skipped, matching
// the producers' own clip.
func (r *Renderer) drawLitPoints(points []drawlist.Point) {
	if r.sceneDest == nil || r.tables.atlas == nil || len(points) == 0 {
		return
	}
	imgs := [4]*ebiten.Image{1: r.tables.atlas}
	// Under the world transform the point layer is RESAMPLED here rather than
	// scaled quad by quad (docs/DESIGN_GPU_RENDERER.md §16.3 "Lit points"). A
	// lit point reads the destination and brightens it, so it must land on each
	// screen pixel at most once: with the transform shrinking the record, two or
	// three record points fall on one screen pixel and the generic path brightened
	// it two or three times, which drew the explosion halo as a lattice of
	// over-lit pixels. Each screen pixel is instead lit by exactly the record
	// point that nearest sampling would choose for its centre — the same rule the
	// terrain and sprites follow — and the point is placed in screen pixels with
	// the transform held off. At a rest step the transform is disarmed and every
	// point takes the path it always took.
	resample := r.sched.worldOn
	k := float64(r.sched.worldScale)
	if resample {
		r.sched.worldOn = false
		defer func() { r.sched.worldOn = true }()
	}
	active := false
	spanX0, spanX1, spanY, spanRow := 0, 0, 0, 0
	var spanPhase int32
	flush := func() {
		if !active {
			return
		}
		phase, class := r.sched.curPhase, r.sched.curClass
		r.sched.curPhase, r.sched.curClass = spanPhase, schedDest
		r.appendDestTableQuad(float32(spanX0), float32(spanY), float32(spanX1), float32(spanY+1), lightScale(spanRow))
		r.sched.curPhase, r.sched.curClass = phase, class
		active = false
	}
	for _, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if resample {
			sx, sy := int(math.Floor(float64(x)*k)), int(math.Floor(float64(y)*k))
			// The record point nearest sampling chooses for this screen pixel's
			// centre; every other record point that lands here is dropped.
			if x != int(math.Floor((float64(sx)+0.5)/k)) || y != int(math.Floor((float64(sy)+0.5)/k)) {
				continue
			}
			if sx < 0 || sy < 0 || sx >= r.w || sy >= r.h {
				continue
			}
			x, y = sx, sy
		} else if x < 0 || y < 0 || x >= r.clipW() || y >= r.clipH() {
			continue
		}
		row := clampLHTRow(int(pt.Index))
		// Preserve record order without widening a scheduler command: a point
		// still owns and tags its own pixel. A discontinuity must flush before
		// the next placement so its geometry remains before that record.
		if active && (y != spanY || row != spanRow || x != spanX1) {
			flush()
		}
		r.sched.beginPoint(x, y, imgs)
		if active && r.sched.curPhase == spanPhase {
			spanX1++
			continue
		}
		// A repeated point can force a new phase even when its coordinates
		// happen to be adjacent to the current span. The earlier span remains
		// in its recorded phase and the new point starts another one.
		flush()
		active = true
		spanX0, spanX1, spanY, spanRow, spanPhase = x, x+1, y, row, r.sched.curPhase
	}
	flush()
}

// appendDestTableQuad appends one axis-aligned quad covering [dx0,dx1)×[dy0,dy1)
// for the row-family op. The scale k is split across the red and green vertex
// lanes; the fragment emits them and the scale blend multiplies the destination
// by their sum (§13.3).
func (r *Renderer) appendDestTableQuad(dx0, dy0, dx1, dy1, k float32) {
	low, high := rowScaleLanes(k)
	r.sched.quad(schedDest, dx0, dy0, dx1, dy1, 0, 0, 0, 0,
		[4]float32{low, high, 0, 0}, [4]float32{0, 0, 0, destOpTable})
}
