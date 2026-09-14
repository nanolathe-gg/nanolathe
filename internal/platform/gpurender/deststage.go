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
// The order they impose is unchanged. A command of this class is placed one
// whole phase after every earlier command of the class it overlaps in ANOTHER
// blend stream, and may share a phase with an earlier one of its own stream,
// because a fixed-function blend reads and writes the attachment in primitive
// order and a phase's batch is drawn in record order (§13.11). Either way an
// overlapping later command composites over the earlier command's result,
// exactly as the byte writers do when they read c.indexed in record order
// (§11.2 "The scheduler")[03 R-COMP-01 §2].

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

// drawRawGlyph uses the existing destination-light stream: source selects the
// LHT row and mode replaces the authored frame key [03 R-FONT-01 §6]. The
// modern executor uses its established true-colour LHT approximation, as it
// does for calculated flashes (DESIGN_GPU_RENDERER §13.3).
func (r *Renderer) drawRawGlyph(f *formats.GAFFrame, x, y int, mode byte, clipX, clipY, clipW, clipH int) {
	var points [256]drawlist.Point
	n := 0
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.clipW()), minInt(clipY+clipH, r.clipH())
	for row := maxInt(0, minY-y); row < minInt(int(f.Height), maxY-y); row++ {
		for col := maxInt(0, minX-x); col < minInt(int(f.Width), maxX-x); col++ {
			i := row*int(f.Width) + col
			if i >= len(f.Pixels) {
				continue
			}
			b := f.Pixels[i]
			if b == mode || b >= 32 {
				continue // host safety: unsafe source rows are suppressed, never clamped
			}
			points[n] = drawlist.Point{X: int32(x + col), Y: int32(y + row), Index: b}
			n++
			if n == len(points) {
				r.drawLitPoints(points[:n])
				n = 0
			}
		}
	}
	r.drawLitPoints(points[:n])
}

// drawTint reproduces tintedBlitAnchor: it subtracts the frame's authored anchor
// offsets, clips to the framebuffer, and for every opaque source texel composites
// the ALP builder's floor((src + dst)/2) over the destination
// (docs/DESIGN_GPU_RENDERER.md §2.3, §13.3)[03 §4.3.4][03 R-COMP-01 §2]
// [03 R-FX-02 §2]. The family's gate is "the palette is installed": with no
// palette it draws nothing, exactly as tintedBlitAnchor returns on a nil palette
// [03 R-COMP-01 §2].
func (r *Renderer) drawTint(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	r.drawTintLight(f, x, y, clipX, clipY, clipW, clipH, nil, 0)
}

// drawTintLight preserves the strip blend and ordering while modulating only
// explicitly classified smoke with nearby visible explosion light (§23).
func (r *Renderer) drawTintLight(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int, lights *subjectLights, height float32) {
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
	batch := &r.sched.phases[r.sched.curPhase].batch[schedDest]
	before := len(batch.verts)
	r.sched.quad(schedDest,
		float32(dx0), float32(dy0), float32(dx1), float32(dy1),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{}, [4]float32{0, 0, 0, destOpTint})
	if lights != nil && lights.count > 0 {
		// quad transforms destination coordinates, so evaluate the four RECORD
		// corners explicitly instead of reading back the transformed vertices.
		xs := [4]float32{float32(dx0), float32(dx1), float32(dx0), float32(dx1)}
		ys := [4]float32{float32(dy0), float32(dy0), float32(dy1), float32(dy1)}
		lit := false
		for i := range 4 {
			rgb := lights.irradiance(xs[i], ys[i], height, [3]float32{}, true)
			v := &batch.verts[before+i]
			v.ColorR, v.ColorG, v.ColorB, v.Custom0 = rgb[0], rgb[1], rgb[2], 1
			lit = lit || rgb != [3]float32{}
		}
		if lit {
			r.modelStats.LitSmokeSprites++
		}
	}
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
	ox, oy := float64(r.sched.txx(0)), float64(r.sched.txy(0))
	if resample {
		r.sched.worldOn = false
		defer func() { r.sched.worldOn = true }()
	}
	// The batch arrives as contiguous horizontal runs — a disc is recorded row by
	// row — and one run is placed ONCE, not once per pixel: its pixels are
	// distinct, so the phase they must all take is the maximum of their
	// individual answers and stamping them with it leaves the same tag state
	// (schedule.go placePointSpan).
	//
	// The runs are collected rather than emitted, because what a run's geometry
	// should be depends on the whole batch: runs that landed in one phase are
	// pairwise disjoint and become ONE quad over the lit point plane, and only
	// what the plane cannot serve falls back to a quad per stretch of equal LHT
	// row (points.go). Placement still happens here, in record order, because the
	// point phase table is stamped as it goes.
	runs, rows := r.pointRuns[:0], r.pointRows[:0]
	active := false
	runX0, runY, runOff := 0, 0, 0
	flush := func() {
		if !active {
			return
		}
		runs = append(runs, litPointRun{
			x0:    int32(runX0),
			y:     int32(runY),
			off:   int32(runOff),
			n:     int32(len(rows) - runOff),
			phase: r.sched.placePointSpan(runX0, runX0+len(rows)-runOff, runY),
		})
		runOff, active = len(rows), false
	}
	for _, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if resample {
			sx, sy := int(math.Floor(float64(x)*k)), int(math.Floor(float64(y)*k))
			if ox != 0 || oy != 0 {
				// The first framebuffer centre covered by this translated record
				// texel. Keep legacy zero-offset selection byte-exact.
				sx = int(math.Ceil(float64(x)*k + ox - 0.5))
				sy = int(math.Ceil(float64(y)*k + oy - 0.5))
			}
			// The record point nearest sampling chooses for this screen pixel's
			// centre; every other record point that lands here is dropped.
			if x != int(math.Floor((float64(sx)+0.5-ox)/k)) || y != int(math.Floor((float64(sy)+0.5-oy)/k)) {
				continue
			}
			if sx < 0 || sy < 0 || sx >= r.w || sy >= r.h {
				continue
			}
			x, y = sx, sy
		} else if x < 0 || y < 0 || x >= r.clipW() || y >= r.clipH() {
			continue
		}
		// A discontinuity closes the run before the next placement, so the runs
		// are placed in record order.
		if active && (y != runY || x != runX0+len(rows)-runOff) {
			flush()
		}
		if !active {
			active, runX0, runY = true, x, y
		}
		rows = append(rows, uint8(clampLHTRow(int(pt.Index))))
	}
	flush()
	r.emitPointRuns(runs, rows, imgs)
	r.pointRuns, r.pointRows = runs[:0], rows[:0]
}

// litPointRun is one placed run of a lit point batch: its screen position, the
// slice of the batch's row arena it covers, and the phase every one of its pixels
// took.
type litPointRun struct {
	x0, y  int32
	off, n int32
	phase  int32
}

// litPointGroup accumulates the runs of one phase: their bounding box and their
// covered pixel count, which is what decides between the plane and the quads, and
// whether the plane took it.
type litPointGroup struct {
	phase          int32
	x0, y0, x1, y1 int32
	pixels         int32
	planed         bool
}

// emitPointRuns turns a placed batch into geometry: one quad over the lit point
// plane for every phase group dense enough to be worth a region, and one quad per
// stretch of equal LHT row for everything else.
//
// A batch is grouped by phase through an index over the phase NUMBER rather than a
// scan of the groups: a heavy battle frame's discs overlap each other and each
// other's earlier phases, so one batch of a late frame can spread over dozens of
// phases and a scan would be the cost the grouping is meant to remove.
//
// The groups are emitted before the fallback runs. Order between them is free:
// every pixel of one phase belongs to exactly one run, and two runs of one phase
// are disjoint, because a repeated pixel is placed a phase later by construction.
func (r *Renderer) emitPointRuns(runs []litPointRun, rows []uint8, imgs [4]*ebiten.Image) {
	if len(runs) == 0 {
		return
	}
	maxPhase := int32(0)
	for i := range runs {
		r.modelStats.PointPixels += int(runs[i].n)
		if runs[i].phase > maxPhase {
			maxPhase = runs[i].phase
		}
	}
	index := r.pointGroupIdx
	if cap(index) < int(maxPhase)+1 {
		index = make([]int32, maxPhase+1)
	}
	index = index[:maxPhase+1]
	for i := range index {
		index[i] = -1
	}
	groups := r.pointGroups[:0]
	for i := range runs {
		run := &runs[i]
		g := index[run.phase]
		if g < 0 {
			g = int32(len(groups))
			index[run.phase] = g
			groups = append(groups, litPointGroup{phase: run.phase,
				x0: run.x0, y0: run.y, x1: run.x0 + run.n, y1: run.y + 1})
		}
		b := &groups[g]
		b.x0, b.y0 = min(b.x0, run.x0), min(b.y0, run.y)
		b.x1, b.y1 = max(b.x1, run.x0+run.n), max(b.y1, run.y+1)
		b.pixels += run.n
	}
	planed := false
	for k := range groups {
		if r.emitPointPlaneGroup(&groups[k], runs, rows) {
			groups[k].planed = true
			planed = true
		}
	}
	// A plane quad opens a run of its own (its source image is the plane), so the
	// run cache the quad path keeps cannot be trusted across it.
	if planed {
		r.sched.ptRunPhase = -1
	}
	for i := range runs {
		run := &runs[i]
		if !groups[index[run.phase]].planed {
			r.emitPointRunQuads(run, rows[run.off:run.off+run.n], imgs)
		}
	}
	r.pointGroups, r.pointGroupIdx = groups[:0], index[:0]
}

// emitPointPlaneGroup writes one phase group into the lit point plane and commits
// it as a single quad, or reports false when the group is too sparse, too small or
// larger than the atlas can serve.
func (r *Renderer) emitPointPlaneGroup(g *litPointGroup, runs []litPointRun, rows []uint8) bool {
	w, h := int(g.x1-g.x0), int(g.y1-g.y0)
	if int(g.pixels) < pointPlaneMinPixels || w <= 0 || h <= 0 {
		return false
	}
	if w*h > int(g.pixels)*pointPlaneDensity {
		return false
	}
	rx, ry, ok := r.pointPlane.alloc(w, h)
	if !ok {
		return false
	}
	r.pointPlane.clearRegion(rx, ry, w, h)
	for i := range runs {
		run := &runs[i]
		if run.phase != g.phase {
			continue
		}
		r.pointPlane.write(rx, ry, int(run.x0-g.x0), int(run.y-g.y0), rows[run.off:run.off+run.n])
	}
	s := &r.sched
	p := s.phaseAt(g.phase)
	s.growDest(p, int(g.x0), int(g.y0), int(g.x1), int(g.y1))
	p.batch[schedDest].selectRun([4]*ebiten.Image{0: r.pointPlane.img, 1: r.tables.atlas},
		nil, blendScaleDestination, schedReadNone)
	s.curPhase, s.curClass = g.phase, schedDest
	r.modelStats.PointQuads++
	r.modelStats.PointPlanes++
	s.quad(schedDest,
		float32(g.x0), float32(g.y0), float32(g.x1), float32(g.y1),
		float32(rx), float32(ry), float32(rx+w), float32(ry+h),
		[4]float32{}, [4]float32{0, 0, 0, destOpLaneAtlas})
	return true
}

// emitPointRunQuads is the geometry path for a run the plane did not serve: one
// quad per maximal stretch of the run that shares an LHT row, all in the run's
// phase. The pixels of a run are distinct, so a stretch's single quad brightens
// each of them exactly once, which is what the per-pixel writes did
// [03 R-FX-01 §4].
func (r *Renderer) emitPointRunQuads(run *litPointRun, rows []uint8, imgs [4]*ebiten.Image) {
	if len(rows) == 0 {
		return
	}
	s := &r.sched
	x0, y := int(run.x0), int(run.y)
	p := s.phaseAt(run.phase)
	s.growDest(p, x0, y, x0+len(rows), y+1)
	if run.phase != s.ptRunPhase {
		p.batch[schedDest].selectRun(imgs, nil, blendScaleDestination, schedReadNone)
		s.ptRunPhase = run.phase
	}
	s.curPhase, s.curClass = run.phase, schedDest
	for i := 0; i < len(rows); {
		j := i + 1
		for j < len(rows) && rows[j] == rows[i] {
			j++
		}
		r.modelStats.PointQuads++
		r.appendDestTableQuad(float32(x0+i), float32(y), float32(x0+j), float32(y+1), lightScale(int(rows[i])))
		i = j
	}
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
