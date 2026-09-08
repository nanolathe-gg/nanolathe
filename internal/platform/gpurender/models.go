package gpurender

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// It is diagnostic data for captures, never simulation state.
type ModelStats struct {
	// RasterPixels is the slot atlas area rasterized this frame; SlotPages the
	// pages that area needed; SlotOverflows the subjects a full atlas sent
	// through the per-subject fallback route. Draws is every model-family
	// device draw of the frame, and RasterDraws the slot atlas passes inside
	// it, which do not scale with the number of subjects
	// [DESIGN_GPU_RENDERER.md §11.2].
	RasterPixels, SlotPages, SlotOverflows, Draws, RasterDraws  int
	GPU, Skipped, Shadows, ShadowsOmitted, StagedGroups, NoBody int
	UnsupportedGeometry, MissingTexture, UnsupportedFace        int
	FoldedFaces, FoldedStrips                                   int
	TexturedQuadFaces, TexturedQuadStrips                       int
	UntriangulatedFaces                                         int
	StructureResolves                                           int
	ComposedGroups                                              int
	RevealOrOutlineOmitted                                      int
	WaterlineOrDiggerOmitted                                    int
	StagingCommandsOmitted                                      int
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
	if r == nil || r.offscreen == nil {
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
	if r == nil || g == nil || g.Scale != 1 || len(g.Faces) == 0 || r.modelKey == nil || r.modelBody == nil || r.modelCommit == nil || r.modelCopy == nil {
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
	if ss := g.Supersample; ss != nil && (ss.Scale != 2 || ss.Width <= 0 || ss.Height <= 0 || r.modelResolve == nil || r.tables.alpha == nil) {
		return false
	}
	return true
}

func (r *Renderer) modelFacesSupported(g *drawlist.ModelGeometry) bool {
	r.modelStats.UnsupportedFace = -1
	for i := range g.Faces {
		f := g.Faces[i]
		if polygonCrosses(f.Vertices) {
			if modelSpanRows(f) == 0 {
				// The established winding/two-chain admission can retain a
				// projected ring with no positive row. It contributes no body
				// pixels, just like a back-facing ring, and is not unsupported.
				continue
			}
			if !modelFaceMaterialSupported(r, f) {
				r.modelStats.MissingTexture++
				return false
			}
			continue
		}
		if len(f.Vertices) > 1<<16 {
			r.modelStats.UnsupportedGeometry++
			r.modelStats.UnsupportedFace = i
			return false
		}
		if !modelFaceMaterialSupported(r, f) {
			r.modelStats.MissingTexture++
			return false
		}
	}
	return true
}

func modelFaceMaterialSupported(r *Renderer, f drawlist.ModelFace) bool {
	return r != nil && (!f.Shaded || r.tables.shade != nil) && (f.Texture == nil || r.gafImageFor(f.Texture) != nil)
}

// modelLocalBounds retains the producer's composition box. Authored device
// fixtures may omit dimensions; derive a box including outline endpoints then.
func modelLocalBounds(g *drawlist.ModelGeometry) image.Rectangle {
	if g.Width > 0 && g.Height > 0 {
		return image.Rect(0, 0, int(g.Width), int(g.Height))
	}
	var b image.Rectangle
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.Outline} {
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
	// The shadow pass writes only pixels this body's own plane leaves uncovered,
	// and reads only the pixels it writes, so the body commit may join the
	// shadow's phase however their rectangles overlap [03 R-REN-03D §4–§5].
	exempt := schedNoOwner
	if g.Shadow != nil {
		shadow, ok := modelSlot{}, false
		if r.modelShadowCommit != nil && r.tables.alpha != nil {
			shadow, ok = r.modelSlotFor(g.Shadow, 1)
		}
		if ok {
			exempt = r.commitModelShadow(g.Shadow, shadow, g, body)
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
		if body.page != nil {
			r.commitModelSlot(body.page.img, body.box, modelWorldBounds(g), exempt)
		}
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

// commitModelSlot compiles one finished subject plane into the frame's opaque
// batch: a keyed quad sampling the plane, skipping the composition background
// index 1 (docs/DESIGN_GPU_RENDERER.md §11.2)[03 R-REN-03A §5]. src and dst are
// the same size, so the framebuffer clip shifts the source by the same amount and
// covers exactly the pixels the per-subject commit covered.
func (r *Renderer) commitModelSlot(page *ebiten.Image, src, dst image.Rectangle, exempt int32) {
	if page == nil || r.scene2D == nil {
		return
	}
	x0, y0 := maxInt(dst.Min.X, 0), maxInt(dst.Min.Y, 0)
	x1, y1 := minInt(dst.Max.X, r.w), minInt(dst.Max.Y, r.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	sx0, sy0 := src.Min.X+(x0-dst.Min.X), src.Min.Y+(y0-dst.Min.Y)
	if r.sched.beginExempt(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{2: page}, exempt) < 0 {
		return
	}
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(sx0), float32(sy0), float32(sx0+x1-x0), float32(sy0+y1-y0),
		[4]float32{}, [4]float32{0, 0, 0, sceneOpModelCommit})
}

// commitModelShadow blends the silhouette through ALP over the phase snapshot,
// punching the body's coverage first so overlapping silhouette faces darken the
// ground once [03 R-REN-03D §4–§5]. The shadow plane rides source slot 3 and the
// body plane source slot 0; Custom0/1 carries the body-page offset of a shadow
// texel and the colour lanes the body slot's page bounds, so a shadow texel
// outside the body slot reads as uncovered.
// It returns the shadow command's scheduler owner, which the paired body commit
// names as its exemption.
func (r *Renderer) commitModelShadow(sg *drawlist.ModelGeometry, shadow modelSlot, bg *drawlist.ModelGeometry, body modelSlot) int32 {
	shadowPage, bodyPage := shadow.page, body.page
	if r.sceneDest == nil || r.tables.atlas == nil || r.destScratch == nil ||
		shadowPage == nil || shadowPage.img == nil || bodyPage == nil || bodyPage.img == nil {
		r.modelStats.ShadowsOmitted++
		return schedNoOwner
	}
	b := modelWorldBounds(sg)
	x0, y0 := maxInt(b.Min.X, 0), maxInt(b.Min.Y, 0)
	x1, y1 := minInt(b.Max.X, r.w), minInt(b.Max.Y, r.h)
	if x0 >= x1 || y0 >= y1 {
		r.modelStats.Shadows++
		return schedNoOwner
	}
	sx0 := shadow.box.Min.X + (x0 - b.Min.X)
	sy0 := shadow.box.Min.Y + (y0 - b.Min.Y)
	// The body plane is read at the same subject-local offset the per-subject
	// commit used: the shadow's world displacement from the body, rebased from
	// the shadow slot's page origin onto the body slot's.
	d := b.Min.Sub(modelWorldBounds(bg).Min)
	kx := body.box.Min.X - shadow.box.Min.X + d.X
	ky := body.box.Min.Y - shadow.box.Min.Y + d.Y
	owner := r.sched.beginExempt(schedDest, x0, y0, x1, y1, [4]*ebiten.Image{
		0: bodyPage.img, 1: r.tables.atlas, 2: r.destScratch, 3: shadowPage.img,
	}, schedNoOwner)
	if owner < 0 {
		return schedNoOwner
	}
	r.sched.quad(schedDest,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(sx0), float32(sy0), float32(sx0+x1-x0), float32(sy0+y1-y0),
		[4]float32{float32(body.box.Min.X), float32(body.box.Min.Y),
			float32(body.box.Max.X), float32(body.box.Max.Y)},
		[4]float32{float32(kx), float32(ky), 0, destOpShadowCommit})
	r.modelStats.Shadows++
	return owner
}

// composeModelChildren merges an attached-unit group over a local staging pair.
// Each child is finished in its own slot, then merged with the full signed
// shifted-key comparison and the wrapped byte store [03 R-REN-03A §4].
//
// The staging pair is shared by every group of the frame, so composing one is a
// scheduler barrier: the phases compiled so far are submitted first, while the
// previous group's staging image still holds the plane its commit samples
// (docs/DESIGN_GPU_RENDERER.md §11.2). One barrier per group, and the group's own
// commit then joins the batch that follows it.
func (r *Renderer) composeModelChildren(g *drawlist.ModelGeometry, parent modelSlot) {
	b := modelWorldBounds(g)
	for _, child := range g.Children {
		if cg := child.Geometry; cg != nil && cg.Eligible && cg.KeyPlane && len(cg.Children) == 0 {
			b = b.Union(modelWorldBounds(cg))
		}
	}
	r.submitSchedule()
	r.ensureModelStage(b.Dx(), b.Dy())
	if r.modelStage == nil {
		r.modelStats.Skipped++
		return
	}
	area := image.Rect(0, 0, b.Dx(), b.Dy())
	stage, scratch := r.modelStage.SubImage(area).(*ebiten.Image), r.modelStageScratch.SubImage(area).(*ebiten.Image)
	stageImg, scratchImg := r.modelStage, r.modelStageScratch
	stage.Fill(color.RGBA{R: 1, A: 255})
	d := modelWorldBounds(g).Min.Sub(b.Min)
	r.modelStageOp.Blend = ebiten.BlendCopy
	r.modelStageOp.GeoM.Reset()
	r.modelStageOp.GeoM.Translate(float64(d.X), float64(d.Y))
	stage.DrawImage(parent.image(), &r.modelStageOp)
	for _, child := range g.Children {
		cg := child.Geometry
		if cg == nil || !cg.KeyPlane || len(cg.Children) != 0 {
			r.modelStats.Skipped++
			continue
		}
		slot, ok := r.modelSlotFor(cg, 1)
		if !ok {
			r.modelStats.Skipped++
			continue
		}
		r.modelStageOp.GeoM.Reset()
		scratch.DrawImage(stage, &r.modelStageOp)
		cb := modelWorldBounds(cg).Sub(b.Min)
		r.resetGeometry()
		r.appendModelQuad(cb, slot.box, [4]float32{}, [4]float32{float32(child.KeyDelta), 0, 0, 0})
		r.modelDraw(scratch, r.modelChild, ebiten.BlendCopy, slot.image(), stage, nil, nil)
		r.resetGeometry()
		stage, scratch = scratch, stage
		stageImg, scratchImg = scratchImg, stageImg
		r.modelStats.GPU++
	}
	r.commitModelSlot(stageImg, area, b, schedNoOwner)
	r.modelStats.ComposedGroups++
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
	}
	r.modelStageW, r.modelStageH = maxInt(w, r.modelStageW), maxInt(h, r.modelStageH)
	r.modelStage = ebiten.NewImage(r.modelStageW, r.modelStageH)
	r.modelStageScratch = ebiten.NewImage(r.modelStageW, r.modelStageH)
}
