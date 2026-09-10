package gpurender

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// It is diagnostic data for captures, never simulation state.
type ModelStats struct {
	// RasterPixels is the slot atlas area rasterized this frame; SlotPages the
	// pages that area needed; SlotOverflows the subjects a full atlas sent
	// through the per-subject fallback route. Draws is every model-family
	// device draw of the frame, and RasterDraws the slot atlas passes inside
	// it, which do not scale with the number of subjects
	// [DESIGN_GPU_RENDERER.md §11.2].
	// Phases is the phases this frame submitted and Passes the device
	// destination switches the executor issued: a switch is counted whenever the
	// destination image of a device call differs from the previous call's, which
	// is the unit of device cost Ebitengine's backends pay for
	// [DESIGN_GPU_RENDERER.md §11.5]. Passes covers the phase passes and their
	// read-surface copies, the fog draw inside them, the expansion, the
	// attached-unit staging draws and the model slot atlas stage.
	// PointPixels is the screen pixels the frame's lit point batches covered,
	// PointPlanes the lit point plane regions they committed through, and
	// PointQuads the device quads those pixels compiled into; Vertices is every
	// vertex the scheduler handed the device this frame. Together they say
	// whether the point layer is paying per pixel or per span, which is what the
	// executor's remaining CPU tracks [DESIGN_GPU_RENDERER.md §13.7].
	Phases, Passes                                             int
	PointPixels, PointQuads, PointPlanes, Vertices             int
	RasterPixels, SlotPages, SlotOverflows, Draws, RasterDraws int
	// The persistent slot accounting of docs/DESIGN_GPU_RENDERER.md §13.12.
	// SlotsReused is the subjects this frame took from a slot an earlier frame
	// rasterized, SlotsRasterized the subjects it placed and drew itself,
	// SlotEvictions the resident slots it retired to make room, and SlotsResident
	// the size of the residency table after it. RasterPixels and SlotPages above
	// now count only what was rasterized this frame, so they fall with the reuse
	// rate instead of restating the scene's size.
	SlotsReused, SlotsRasterized, SlotEvictions, SlotsResident  int
	GPU, Skipped, Shadows, ShadowsOmitted, StagedGroups, NoBody int
	UnsupportedGeometry, MissingTexture, UnsupportedFace        int
	FoldedFaces, FoldedStrips                                   int
	TexturedQuadFaces, TexturedQuadStrips                       int
	UntriangulatedFaces                                         int
	// Supersampled is the subjects that rasterized doubled and resolved with
	// fractional coverage this frame (DESIGN_GPU_RENDERER §17).
	Supersampled             int
	ComposedGroups           int
	RevealOrOutlineOmitted   int
	WaterlineOrDiggerOmitted int
	StagingCommandsOmitted   int
	// Flashes is the lit-disc quads this frame — explosion ground flashes and
	// light halos, one quad each, where the point lane spends one lit point per
	// covered screen pixel (DESIGN_GPU_RENDERER §13.11). Read it beside
	// PointPixels: together they say how much of the effect layer has left the
	// point path.
	Flashes int
}

func (r *Renderer) ModelStats() ModelStats {
	if r == nil {
		return ModelStats{}
	}
	return r.modelStats
}

// Model replays geometry or records an explicit omission. Modern mode never
// resolves Ref or substitutes CPU images [DESIGN_GPU_RENDERER.md §9–§10].
func (r *Renderer) Model(cmd drawlist.Model) {
	if r == nil || r.surfaces[0] == nil {
		return
	}
	if cmd.ShadowOmissions != 0 {
		r.modelStats.ShadowsOmitted += cmd.ShadowOmissions
	}
	if cmd.GroupOmission {
		r.modelStats.StagedGroups++
	}
	if cmd.Geometry != nil && cmd.Geometry.Fallback == drawlist.ModelFallbackNoBodyCommit {
		r.modelStats.NoBody++
		return
	}
	if cmd.ShadowOnly && cmd.Geometry != nil && cmd.Geometry.Shadow == nil {
		return
	}
	if g := cmd.Geometry; g != nil && g.Eligible && r.drawModelGeometry(g, cmd.ShadowOnly) {
		if !cmd.ShadowOnly {
			r.modelStats.GPU++
		}
		return
	}
	if cmd.Geometry != nil {
		if cmd.Geometry.Shadow != nil {
			r.modelStats.ShadowsOmitted++
		}
		switch cmd.Geometry.Fallback {
		case drawlist.ModelFallbackRevealOrOutline:
			r.modelStats.RevealOrOutlineOmitted++
		case drawlist.ModelFallbackWaterlineOrDigger:
			r.modelStats.WaterlineOrDiggerOmitted++
		case drawlist.ModelFallbackStaging:
			r.modelStats.StagingCommandsOmitted++
		case drawlist.ModelFallbackNoBodyCommit:
			r.modelStats.NoBody++
		}
	}
	r.modelStats.Skipped++
}

func (r *Renderer) modelGeometryConfigSupported(g *drawlist.ModelGeometry) bool {
	if r == nil || g == nil || g.Scale != 1 || len(g.Faces) == 0 && len(g.LiveFaces) == 0 || r.modelKey == nil || r.modelBody == nil || r.modelCommit == nil || r.modelCopy == nil || r.modelResolve == nil || r.tables.atlas == nil {
		return false
	}
	if g.Reveal != nil {
		// The reveal reads the key stored in the subject's slot, and a subject
		// under construction always owns a key plane [03 R-REN-03A §2]. A
		// keyless reveal packet is an explicit omission, never an invented key.
		if r.modelReveal == nil || !g.KeyPlane {
			return false
		}
	}
	if g.KeyPlane && (g.Waterline != drawlist.ModelWaterlineNone || g.Digger) && (r.modelClip == nil || g.Waterline == drawlist.ModelWaterlineBlue && r.tables.blue == nil) {
		return false
	}
	if len(g.Children) != 0 && (!g.KeyPlane || r.modelChild == nil) {
		return false
	}
	if ss := g.Supersample; ss != nil && (ss.Scale != 2 || ss.Width <= 0 || ss.Height <= 0 || ss.Reveal != nil && r.modelReveal == nil) {
		return false
	}
	return true
}

// modelFacesSupported admits one subject's faces and returns, for each of them,
// whether its ring crosses itself. Preparation reads that verdict rather than
// testing every ring a second time: the test is the largest single item in face
// preparation and the recorded geometry does not change within a frame
// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy"). The slice is frame
// scratch and is only meaningful when the subject is supported.
func (r *Renderer) modelFacesSupported(g *drawlist.ModelGeometry) ([]bool, bool) {
	r.modelStats.UnsupportedFace = -1
	crosses := r.modelPrep.crosses.take(len(g.Faces))
	for i := range g.Faces {
		f := &g.Faces[i]
		crosses[i] = polygonCrosses(f.Vertices)
		if crosses[i] {
			if modelSpanRows(f) == 0 {
				// The established winding/two-chain admission can retain a
				// projected ring with no positive row. It contributes no body
				// pixels, just like a back-facing ring, and is not unsupported.
				continue
			}
			if !modelFaceMaterialSupported(r, f) {
				r.modelStats.MissingTexture++
				return nil, false
			}
			continue
		}
		if len(f.Vertices) > 1<<16 {
			r.modelStats.UnsupportedGeometry++
			r.modelStats.UnsupportedFace = i
			return nil, false
		}
		if !modelFaceMaterialSupported(r, f) {
			r.modelStats.MissingTexture++
			return nil, false
		}
	}
	return crosses, true
}

func modelFaceMaterialSupported(r *Renderer, f *drawlist.ModelFace) bool {
	return r != nil && (!f.Shaded || r.tables.shade != nil) && (f.Texture == nil || r.gafImageFor(f.Texture) != nil)
}

// modelLocalBounds retains the producer's composition box. Authored device
// fixtures may omit dimensions; derive a box including outline endpoints then.
func modelLocalBounds(g *drawlist.ModelGeometry) image.Rectangle {
	if g.Width > 0 && g.Height > 0 {
		return image.Rect(0, 0, int(g.Width), int(g.Height))
	}
	var b image.Rectangle
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.LiveFaces, g.Outline} {
		for _, f := range faces {
			for _, v := range f.Vertices {
				b = b.Union(image.Rect(int(v.X), int(v.Y), int(v.X)+1, int(v.Y)+1))
			}
		}
	}
	return b
}

func modelWorldBounds(g *drawlist.ModelGeometry) image.Rectangle {
	return modelLocalBounds(g).Add(image.Pt(int(g.AnchorX-g.OriginX), int(g.AnchorY-g.OriginY)))
}

// attached-unit group [03 R-REN-03D §4][03 R-REN-03A §4].
func (r *Renderer) drawModelGeometry(g *drawlist.ModelGeometry, shadowOnly bool) bool {
	body, ok := r.modelSlotFor(g, 0)
	if !ok {
		return false
	}
	// The shadow commit reads and writes the destination, so the body commit
	// that follows it is an ordinary opaque write over a destination read and
	// takes the next phase. The scheduler had an exemption here, on the argument
	// that the shadow writes only pixels the body's own plane leaves uncovered
	// and the body only pixels that plane covers, so their order could not matter
	// [03 R-REN-03D §4–§5]. Measured against the battle capture the two sets are
	// not exactly complementary — a column of the silhouette at the body's edge
	// belongs to both — and drawing the body first drops the shadow there. The
	// sequential scheduler never exercised the exemption, because a body commit
	// almost always overlaps some other destination read of the shadow's phase
	// and opened the next phase anyway, so removing it restores that executor's
	// pixels exactly and keeps the byte writers' shadow-then-body order
	// [03 R-REN-03D §4].
	if g.Shadow != nil {
		shadow, ok := modelSlot{}, false
		if r.modelShadowCommit != nil && r.tables.alpha != nil {
			shadow, ok = r.modelSlotFor(g.Shadow, 1)
		}
		if ok {
			r.commitModelShadow(g.Shadow, shadow, g, body)
		} else {
			r.modelStats.ShadowsOmitted++
		}
	}
	if shadowOnly {
		return true
	}
	if len(g.Children) != 0 {
		// A group commits its composed staging plane, which carries the
		// children's pixels too; the shadow punched only the parent's coverage,
		// so the exemption does not hold and is not claimed.
		r.composeModelChildren(g, body)
	} else {
		r.commitModelSlot(body.resolved(), body.box, modelWorldBounds(g))
	}
	return true
}

// modelSlotFor returns the frame slot reserved for one subject. A subject the
// atlas could not fit is rasterized now through the per-subject fallback pages;
// an unsupported one has no slot and stays an explicit omission.
//
// The fallback pages are reused by the next overflowing subject, so rasterizing
// into them is a scheduler barrier: everything compiled so far — including an
// earlier overflow subject's commit — is submitted first, while its page still
// holds that subject (docs/DESIGN_GPU_RENDERER.md §11.2).
func (r *Renderer) modelSlotFor(g *drawlist.ModelGeometry, set int) (modelSlot, bool) {
	slot, ok := r.modelAtlas.slots[g]
	if !ok {
		return modelSlot{}, false
	}
	if slot.overflow {
		r.submitSchedule()
		return r.rasterizeModelOverflow(g, set)
	}
	return slot, slot.page != nil
}

// commitModelSlot compiles one resolved subject plane into the frame's opaque
// batch: a quad sampling the plane's premultiplied colour and coverage, skipping
// the uncovered texels and blending the partly covered ones by their coverage
// (docs/DESIGN_GPU_RENDERER.md §11.2, §17)[03 R-REN-03A §5]. src and dst are
// the same size, so the framebuffer clip shifts the source by the same amount and
// covers exactly the pixels the per-subject commit covered.
func (r *Renderer) commitModelSlot(page *ebiten.Image, src, dst image.Rectangle) {
	if page == nil || r.scene2D == nil {
		return
	}
	x0, y0 := maxInt(dst.Min.X, 0), maxInt(dst.Min.Y, 0)
	x1, y1 := minInt(dst.Max.X, r.clipW()), minInt(dst.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	sx0, sy0 := src.Min.X+(x0-dst.Min.X), src.Min.Y+(y0-dst.Min.Y)
	if !r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{2: page}) {
		return
	}
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(sx0), float32(sy0), float32(sx0+x1-x0), float32(sy0+y1-y0),
		[4]float32{}, [4]float32{0, 0, 0, sceneOpModelCommit})
}

// commitModelShadow composites the silhouette over the composite through the ALP
// builder's arithmetic, punching the body's coverage first so overlapping
// silhouette faces darken the ground once [03 R-REN-03D §4–§5][03 §4.3.4]. The
// punch reads the body plane, not the destination, so this commit takes no
// snapshot: it is an ordinary blend (docs/DESIGN_GPU_RENDERER.md §13.3). Both
// planes are the resolved ones (§17): the silhouette's coverage scales its
// darkening, and the punch removes only the texels the body covers whole, since
// the body's own blend restores the shadowed ground under a partly covered edge.
// The shadow plane rides source slot 3 and the body plane source slot 0; Custom0/1
// carries the body-page offset of a shadow texel and the colour lanes the body
// slot's page bounds, so a shadow texel outside the body slot reads as uncovered.
func (r *Renderer) commitModelShadow(sg *drawlist.ModelGeometry, shadow modelSlot, bg *drawlist.ModelGeometry, body modelSlot) {
	shadowPage, bodyPage := shadow.page, body.page
	if r.sceneDest == nil || r.tables.atlas == nil ||
		shadowPage == nil || shadowPage.post == nil || bodyPage == nil || bodyPage.post == nil {
		r.modelStats.ShadowsOmitted++
		return
	}
	b := modelWorldBounds(sg)
	x0, y0 := maxInt(b.Min.X, 0), maxInt(b.Min.Y, 0)
	x1, y1 := minInt(b.Max.X, r.clipW()), minInt(b.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		r.modelStats.Shadows++
		return
	}
	sx0 := shadow.box.Min.X + (x0 - b.Min.X)
	sy0 := shadow.box.Min.Y + (y0 - b.Min.Y)
	// The body plane is read at the same subject-local offset the per-subject
	// commit used: the shadow's world displacement from the body, rebased from
	// the shadow slot's page origin onto the body slot's.
	d := b.Min.Sub(modelWorldBounds(bg).Min)
	kx := body.box.Min.X - shadow.box.Min.X + d.X
	ky := body.box.Min.Y - shadow.box.Min.Y + d.Y
	if !r.sched.begin(schedDest, x0, y0, x1, y1, [4]*ebiten.Image{
		0: bodyPage.post, 1: r.tables.atlas, 3: shadowPage.post,
	}) {
		return
	}
	r.sched.quad(schedDest,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(sx0), float32(sy0), float32(sx0+x1-x0), float32(sy0+y1-y0),
		[4]float32{float32(body.box.Min.X), float32(body.box.Min.Y),
			float32(body.box.Max.X), float32(body.box.Max.Y)},
		[4]float32{float32(kx), float32(ky), 0, destOpShadowCommit})
	r.modelStats.Shadows++
}

// composeModelChildren commits an attached-unit group: each child is finished in
// its own slot and merged over the parent's plane with the full signed shifted-key
// comparison and the wrapped byte store [03 R-REN-03A §4].
//
// The frame's groups are normally composed together before Replay, on the shared
// staging atlas (model_stage.go), so this only accounts for the group's children
// and commits the finished plane — no staging draw and no barrier. A group the
// atlas could not serve is composed here instead, over the fallback staging pair
// shared by every such group, which makes it a scheduler barrier: the phases
// compiled so far are submitted first, while the previous fallback group's image
// still holds the plane its commit samples (docs/DESIGN_GPU_RENDERER.md §11.2).
func (r *Renderer) composeModelChildren(g *drawlist.ModelGeometry, parent modelSlot) {
	if grp, ok := r.batchedGroup(g); ok {
		// The staging atlas already merged this group; the omissions still have to
		// be counted here, where record order visits it.
		for _, child := range g.Children {
			if !mergeableChild(child.Geometry) {
				r.modelStats.Skipped++
				continue
			}
			r.modelStats.GPU++
		}
		r.commitModelSlot(grp.out, grp.native, grp.bounds)
		r.modelStats.ComposedGroups++
		return
	}
	// The group composes at the parent's raster scale and resolves once at the
	// end, so a supersampled carrier's children merge on its doubled index
	// plane and the whole group takes one coverage resolve (§17). A child at
	// another scale cannot join that plane and is an explicit omission.
	b := modelGroupBounds(g)
	scale := parent.scale
	r.submitSchedule()
	r.ensureModelStage(scale*b.Dx(), scale*b.Dy())
	if r.modelStage == nil {
		r.modelStats.Skipped++
		return
	}
	area := image.Rect(0, 0, scale*b.Dx(), scale*b.Dy())
	native := image.Rect(0, 0, b.Dx(), b.Dy())
	stage, scratch := r.modelStage.SubImage(area).(*ebiten.Image), r.modelStageScratch.SubImage(area).(*ebiten.Image)
	stageImg, scratchImg := r.modelStage, r.modelStageScratch
	r.beginPass(stageImg)
	stage.Fill(color.RGBA{R: 1, A: 255})
	d := modelWorldBounds(g).Min.Sub(b.Min).Mul(scale)
	r.modelStageOp.Blend = ebiten.BlendCopy
	r.modelStageOp.GeoM.Reset()
	r.modelStageOp.GeoM.Translate(float64(d.X), float64(d.Y))
	parentImg := parent.rasterImage()
	stage.DrawImage(parentImg, &r.modelStageOp)
	recycleImage(parentImg)
	for _, child := range g.Children {
		cg := child.Geometry
		if !mergeableChild(cg) {
			r.modelStats.Skipped++
			continue
		}
		slot, ok := r.modelSlotFor(cg, 1)
		if !ok || slot.scale != scale {
			r.modelStats.Skipped++
			continue
		}
		r.modelStageOp.GeoM.Reset()
		r.beginPass(scratchImg)
		scratch.DrawImage(stage, &r.modelStageOp)
		cb := scaleRect(modelWorldBounds(cg).Sub(b.Min), scale)
		r.appendModelQuad(cb, slot.raster, [4]float32{}, [4]float32{float32(child.KeyDelta), 0, 0, 0})
		childImg := slot.rasterImage()
		r.modelDraw(scratch, r.modelChild, ebiten.BlendCopy, childImg, stage, nil, nil)
		recycleImage(childImg)
		stage, scratch = scratch, stage
		stageImg, scratchImg = scratchImg, stageImg
		r.modelStats.GPU++
	}
	if g.Waterline != drawlist.ModelWaterlineNone || g.Digger {
		r.beginPass(scratchImg)
		r.appendModelQuad(area, area,
			[4]float32{float32(g.Waterline), float32(g.WaterlineKey), boolFloat(g.Digger), float32(g.DiggerKey)}, [4]float32{})
		r.modelDraw(scratch, r.modelClip, ebiten.BlendCopy, stage, r.tables.blue, nil, nil)
		stage = scratch
	}
	r.beginPass(r.modelStageOut)
	out := r.modelStageOut.SubImage(native).(*ebiten.Image)
	r.appendModelQuad(native, area, [4]float32{float32(scale), 0, 0, 0}, [4]float32{})
	r.modelDraw(out, r.modelResolve, ebiten.BlendCopy, stage, r.tables.atlas, nil, nil)
	r.commitModelSlot(r.modelStageOut, native, b)
	r.modelStats.ComposedGroups++
}

// scaleRect scales a rectangle about the origin by a whole factor.
func scaleRect(b image.Rectangle, scale int) image.Rectangle {
	return image.Rect(b.Min.X*scale, b.Min.Y*scale, b.Max.X*scale, b.Max.Y*scale)
}

func (r *Renderer) ensureModelStage(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	if r.modelStage != nil && r.modelStageW >= w && r.modelStageH >= h {
		return
	}
	if r.modelStage != nil {
		r.modelStage.Deallocate()
		r.modelStageScratch.Deallocate()
		r.modelStageOut.Deallocate()
	}
	r.modelStageW, r.modelStageH = maxInt(w, r.modelStageW), maxInt(h, r.modelStageH)
	r.modelStage = ebiten.NewImage(r.modelStageW, r.modelStageH)
	r.modelStageScratch = ebiten.NewImage(r.modelStageW, r.modelStageH)
	r.modelStageOut = ebiten.NewImage(r.modelStageW, r.modelStageH)
}
