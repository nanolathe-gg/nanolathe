package gpurender

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The attached-unit staging area (docs/DESIGN_GPU_RENDERER.md §11.2 "Models",
// §11.5 "Model slot passes", §13.3).
//
// A carrier or a factory composes its children over its own finished plane with
// the full signed shifted-key comparison and the wrapped byte store
// [03 R-REN-03A §4]. That merge reads the plane the previous merge produced, so
// it cannot be done in place: each child copies the accumulated plane into the
// other image and merges into that copy.
//
// Composing one group at a time over a single shared staging pair costs three
// device destination switches per group — into the staging image, into its
// scratch, and back into the composite — and forces a scheduler barrier, because
// the next group would otherwise overwrite the plane the previous group's commit
// still samples. On a battle frame with four to eight factories building, that is
// twelve to twenty-four of the frame's passes, against the whole rest of the
// composite's nine, which is what stood between the executor and §13.4's budget
// of twelve.
//
// So the frame's groups share one staging ATLAS: every group gets a disjoint
// region of the same pair of images, the composition runs before Replay, and the
// work is ordered by DESTINATION rather than by group — every group's background
// and parent in one pass, then every group's first child, then every group's
// second child, and so on. The whole frame's staging is then 1 + (the largest
// child count) passes however many groups there are, and the commits become
// ordinary opaque quads in the frame's own batches, with no barrier.
//
// The merge order inside a group is untouched: pass k merges child k of every
// group, so each group still sees its children in record order, each over the
// plane the previous one left [03 R-REN-03A §4]. Groups the atlas cannot serve —
// a parent or child that overflowed the slot atlas, a child at another raster
// scale than its parent, or a box that does not fit — fall back to the
// one-group-at-a-time path in models.go, which is unchanged.
//
// A group composes at its parent's raster scale, on index planes, exactly as a
// single subject rasterizes: a supersampled carrier's children merge on its
// doubled plane, and one coverage resolve at the end turns the composed group
// into colour on the out plane the commit samples (DESIGN_GPU_RENDERER §17).

// modelStageMaxSide bounds one staging image. A group's box is a subject's
// composition box unioned with its children's, so a handful of them fit easily;
// a frame that needed more sends the remainder through the fallback path.
const (
	modelStageMaxSide  = 2048
	modelStageGrowStep = 256
)

// modelStageChild is one child's merge: the finished slot it samples and where
// it lands inside its group's region.
type modelStageChild struct {
	slot     modelSlot
	dst      image.Rectangle // region-local destination of the merge quad
	keyDelta int32
}

// modelStageGroup is one batched group. children indexes the atlas' shared child
// store, so a steady-state frame allocates no per-group slice.
type modelStageGroup struct {
	region       image.Rectangle // the group's area on both index planes, at scale
	native       image.Rectangle // the resolved group's area on the out plane
	bounds       image.Rectangle // the group's world bounds, the commit's destination
	parent       modelSlot
	parentOffset image.Point // at scale
	scale        int
	childOff     int
	childCount   int
	// plane is the index plane holding the finished composition once
	// composeModelStage has run: which of the two it is depends on the group's
	// own child count. out is the resolved plane the commit samples.
	plane        *ebiten.Image
	out          *ebiten.Image
	waterline    drawlist.ModelWaterline
	waterlineKey uint8
	digger       bool
	diggerKey    uint8
}

// modelStageAtlas owns the frame's staging planes and its shelf allocator. Every
// slice and the index map are reused across frames (§11.2 "Allocation policy").
// The map is keyed by geometry pointer and is only ever looked up, never ranged,
// so it introduces no ordering [I1].
type modelStageAtlas struct {
	a, b, out   *ebiten.Image
	w, h        int
	x, y, rowH  int
	usedW       int
	usedH       int
	groups      []modelStageGroup
	children    []modelStageChild
	index       map[*drawlist.ModelGeometry]int
	maxChildren int
}

func (s *modelStageAtlas) reset() {
	s.x, s.y, s.rowH = 0, 0, 0
	s.usedW, s.usedH = 0, 0
	s.groups = s.groups[:0]
	s.children = s.children[:0]
	s.maxChildren = 0
	if s.index == nil {
		s.index = make(map[*drawlist.ModelGeometry]int)
	} else {
		clear(s.index)
	}
}

// alloc reserves one shelf-packed region for a group's box.
func (s *modelStageAtlas) alloc(w, h int) (image.Rectangle, bool) {
	if w <= 0 || h <= 0 || w > modelStageMaxSide || h > modelStageMaxSide {
		return image.Rectangle{}, false
	}
	if s.x+w > modelStageMaxSide {
		s.y, s.x, s.rowH = s.y+s.rowH, 0, 0
	}
	if s.y+h > modelStageMaxSide {
		return image.Rectangle{}, false
	}
	rect := image.Rect(s.x, s.y, s.x+w, s.y+h)
	s.x += w
	if h > s.rowH {
		s.rowH = h
	}
	s.usedW, s.usedH = maxInt(s.usedW, rect.Max.X), maxInt(s.usedH, rect.Max.Y)
	return rect, true
}

// ensure allocates the staging pair at the size this frame needs. The pair grows
// and is reused; it is never reallocated per group.
func (s *modelStageAtlas) ensure() bool {
	if s.usedW == 0 || s.usedH == 0 {
		return false
	}
	w := minInt(ceilTo(s.usedW, modelStageGrowStep), modelStageMaxSide)
	h := minInt(ceilTo(s.usedH, modelStageGrowStep), modelStageMaxSide)
	if s.a != nil && s.w >= w && s.h >= h {
		return true
	}
	w, h = maxInt(w, s.w), maxInt(h, s.h)
	if s.a != nil {
		s.a.Deallocate()
		s.b.Deallocate()
		s.out.Deallocate()
	}
	s.a, s.b, s.out = ebiten.NewImage(w, h), ebiten.NewImage(w, h), ebiten.NewImage(w, h)
	s.w, s.h = w, h
	return true
}

// mergeableChild is the group merge's admission: a child carries its own key
// plane and is not itself a group [03 R-REN-03A §4]. Anything else is an explicit
// omission, counted where the group commits.
func mergeableChild(cg *drawlist.ModelGeometry) bool {
	return cg != nil && cg.KeyPlane && len(cg.Children) == 0
}

// modelGroupBounds is the world rectangle a group composes over: the parent's
// box unioned with every eligible child's.
func modelGroupBounds(g *drawlist.ModelGeometry) image.Rectangle {
	b := modelWorldBounds(g)
	for _, child := range g.Children {
		if cg := child.Geometry; cg != nil && cg.Eligible && mergeableChild(cg) {
			b = b.Union(modelWorldBounds(cg))
		}
	}
	return b
}

// prepareModelGroups reserves a staging region for every attached-unit group the
// atlas can serve. It runs after the slot pages are rasterized and before Replay,
// and it touches no device: the drawing is composeModelStage's.
//
// A group is batched only when its parent and every merging child hold an
// ordinary slot. An overflowing subject is rasterized into the shared fallback
// pages at commit time, which is exactly what the one-group-at-a-time path in
// models.go exists for.
func (r *Renderer) prepareModelGroups(l *drawlist.List) {
	s := &r.modelGroups
	s.reset()
	if l == nil || r.modelChild == nil {
		return
	}
	l.VisitModels(func(m drawlist.Model) {
		g := m.Geometry
		if g == nil || !g.Eligible || m.ShadowOnly || len(g.Children) == 0 {
			return
		}
		if _, held := s.index[g]; held {
			return
		}
		parent, ok := r.batchedSlot(g)
		if !ok {
			return
		}
		b := modelGroupBounds(g)
		scale := parent.scale
		region, ok := s.alloc(scale*b.Dx(), scale*b.Dy())
		if !ok {
			return
		}
		off := len(s.children)
		for _, child := range g.Children {
			cg := child.Geometry
			if !mergeableChild(cg) {
				continue
			}
			slot, ok := r.batchedSlot(cg)
			if !ok || slot.scale != scale {
				// This child would be rasterized through the fallback pages at
				// commit time, or cannot join the parent's plane, so the whole
				// group keeps the fallback path and its pixels are decided there.
				s.children = s.children[:off]
				return
			}
			s.children = append(s.children, modelStageChild{
				slot:     slot,
				dst:      scaleRect(modelWorldBounds(cg).Sub(b.Min), scale),
				keyDelta: child.KeyDelta,
			})
		}
		n := len(s.children) - off
		s.index[g] = len(s.groups)
		s.groups = append(s.groups, modelStageGroup{
			region:       region,
			native:       image.Rect(region.Min.X, region.Min.Y, region.Min.X+b.Dx(), region.Min.Y+b.Dy()),
			bounds:       b,
			parent:       parent,
			parentOffset: modelWorldBounds(g).Min.Sub(b.Min).Mul(scale),
			scale:        scale,
			childOff:     off,
			childCount:   n,
			waterline:    g.Waterline, waterlineKey: g.WaterlineKey,
			digger: g.Digger, diggerKey: g.DiggerKey,
		})
		if n > s.maxChildren {
			s.maxChildren = n
		}
	})
}

// batchedSlot returns a subject's reserved slot when it is an ordinary one. An
// overflowing subject has no plane until its commit rasterizes it, so it can
// never be part of the batched staging.
func (r *Renderer) batchedSlot(g *drawlist.ModelGeometry) (modelSlot, bool) {
	slot, ok := r.modelAtlas.slots[g]
	if !ok || slot.overflow || slot.page == nil || slot.page.img == nil {
		return modelSlot{}, false
	}
	return slot, true
}

// composeModelStage draws every batched group's composition, ordered by
// destination: one pass writes every group's background and parent, pass k
// after it merges child k of every group, the clips follow, and one last pass
// resolves every group onto the out plane (§17). Regions are pairwise disjoint,
// so batching by child index preserves each group's own record order
// [03 R-REN-03A §4].
func (r *Renderer) composeModelStage() {
	s := &r.modelGroups
	if len(s.groups) == 0 || !s.ensure() {
		return
	}
	// The staging atlas is its own stage, so its switches are counted frame-wide
	// (ModelStats.Passes) and not against the slot stage's own budget. The slot
	// stage's destination is no longer current once this stage has drawn, so its
	// tracker is cleared: an overflowing subject rasterized later still counts the
	// switch back to its page.
	defer func() { r.modelAtlas.lastDst = nil }()
	// Pass one: the composition background and the parent plane. Index 1 is the
	// composition transparent index [03 R-REN-03A §1–§2].
	r.beginPass(s.a)
	for i := range s.groups {
		grp := &s.groups[i]
		grp.plane = s.a
		sub := s.a.RecyclableSubImage(grp.region)
		sub.Fill(color.RGBA{R: 1, A: 255})
		r.modelStageOp.Blend = ebiten.BlendCopy
		r.modelStageOp.GeoM.Reset()
		r.modelStageOp.GeoM.Translate(
			float64(grp.region.Min.X+grp.parentOffset.X),
			float64(grp.region.Min.Y+grp.parentOffset.Y))
		parentImg := grp.parent.rasterImage()
		sub.DrawImage(parentImg, &r.modelStageOp)
		recycleImage(parentImg)
		sub.Recycle()
		r.modelStats.Draws += 2
	}
	// Pass k+2: child k of every group that has one, into the other image.
	src, dst := s.a, s.b
	for k := 0; k < s.maxChildren; k++ {
		drew := false
		for i := range s.groups {
			grp := &s.groups[i]
			if k >= grp.childCount {
				continue
			}
			if !drew {
				r.beginPass(dst)
				drew = true
			}
			child := &s.children[grp.childOff+k]
			// The merge writes only the child's rectangle, so the rest of the
			// accumulated plane is carried over by this copy.
			dstSub := dst.RecyclableSubImage(grp.region)
			srcSub := src.RecyclableSubImage(grp.region)
			r.modelStageOp.Blend = ebiten.BlendCopy
			r.modelStageOp.GeoM.Reset()
			r.modelStageOp.GeoM.Translate(float64(grp.region.Min.X), float64(grp.region.Min.Y))
			dstSub.DrawImage(srcSub, &r.modelStageOp)
			r.modelStats.Draws++
			r.appendModelQuad(child.dst.Add(grp.region.Min), child.slot.raster,
				[4]float32{}, [4]float32{float32(child.keyDelta), 0, 0, 0})
			childImg := child.slot.rasterImage()
			r.modelDraw(dstSub, r.modelChild, ebiten.BlendCopy, childImg, srcSub, nil, nil)
			recycleImage(childImg)
			dstSub.Recycle()
			srcSub.Recycle()
			grp.plane = dst
		}
		src, dst = dst, src
	}
	// Carrier waterline and Digger run after its live lane and every child
	// merge. The group plane contains both colour and key, so the same native
	// clip shader applies without a CPU staging image [03 R-REN-03A §4].
	for i := range s.groups {
		grp := &s.groups[i]
		if grp.waterline == drawlist.ModelWaterlineNone && !grp.digger {
			continue
		}
		dst := s.a
		if grp.plane == dst {
			dst = s.b
		}
		srcSub := grp.plane.RecyclableSubImage(grp.region)
		dstSub := dst.RecyclableSubImage(grp.region)
		r.beginPass(dst)
		r.appendModelQuad(grp.region, grp.region,
			[4]float32{float32(grp.waterline), float32(grp.waterlineKey), boolFloat(grp.digger), float32(grp.diggerKey)}, [4]float32{})
		r.modelDraw(dstSub, r.modelClip, ebiten.BlendCopy, srcSub, r.tables.blue, nil, nil)
		srcSub.Recycle()
		dstSub.Recycle()
		grp.plane = dst
	}
	// The resolve: every group's finished index plane becomes colour on the out
	// plane, one draw per source plane into one destination (§17).
	if r.modelResolve == nil || r.tables.atlas == nil {
		return
	}
	r.beginPass(s.out)
	for _, plane := range [2]*ebiten.Image{s.a, s.b} {
		r.resetModelQuads()
		for i := range s.groups {
			grp := &s.groups[i]
			if grp.plane != plane {
				continue
			}
			r.appendModelQuad(grp.native, grp.region, [4]float32{float32(grp.scale), 0, 0, 0}, [4]float32{})
			grp.out = s.out
		}
		r.modelDraw(s.out, r.modelResolve, ebiten.BlendCopy, plane, r.tables.atlas, nil, nil)
	}
}

// batchedGroup returns the finished staging plane of a group composeModelStage
// handled, if it handled this one.
func (r *Renderer) batchedGroup(g *drawlist.ModelGeometry) (*modelStageGroup, bool) {
	i, ok := r.modelGroups.index[g]
	if !ok || i >= len(r.modelGroups.groups) {
		return nil, false
	}
	grp := &r.modelGroups.groups[i]
	if grp.plane == nil || grp.out == nil {
		return nil, false
	}
	return grp, true
}
