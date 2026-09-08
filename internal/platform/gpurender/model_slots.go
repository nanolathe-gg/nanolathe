package gpurender

import (
	"image"
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The modern executor rasterizes every model subject of one frame into a
// per-frame slot atlas and submits one batched draw per stage, replacing the
// per-subject scratch surfaces, the body image cache and its page packer
// [DESIGN_GPU_RENDERER.md §11.2]. Slots are disjoint, so the maximum-byte-key
// pass stays subject-local [03 R-REN-03A §2] and cross-subject painter order
// remains the commit order Replay issues [03 R-RAST-01 §7].
//
// These are renderer payload bounds, not retail constants. Page height grows to
// what one frame needs; a frame that still does not fit falls back to the
// per-subject route below and is counted, never dropped.
const (
	modelPageWidth     = 2048
	modelPageMaxHeight = 2048
	modelPageGrowStep  = 256
	modelNativePages   = 2
)

// modelPage is one slot atlas page. img holds the composed subject planes —
// index in red, the key stored at that texel in green, body coverage in blue —
// key holds the maximum-byte-key plane the body pass reads, and post is the
// scratch the reveal and clipping passes write before their region is copied
// back. scale 2 pages carry the doubled structure raster of [03 R-REN-03A §6].
type modelPage struct {
	img, key, post *ebiten.Image
	w, h           int
	scale          int

	x, y, rowH   int
	maxH         int
	usedW, usedH int
	needPost     bool
	subjects     []modelPageSubject
}

// modelPageSubject is one subject's raster on one page: where its local origin
// lands, the prepared faces and outline endpoints, and the processed-pass
// parameters that used to be per-draw uniforms.
type modelPageSubject struct {
	origin  image.Point
	rect    image.Rectangle
	faces   []preparedModelFace
	outline []modelGPUFace
	reveal  *drawlist.ModelReveal

	keyed        bool
	waterline    drawlist.ModelWaterline
	waterlineKey uint8
	digger       bool
	diggerKey    uint8
}

func (s *modelPageSubject) clips() bool {
	return s.keyed && (s.waterline != drawlist.ModelWaterlineNone || s.digger)
}

// modelSlot locates one reserved subject. box is the page region of the
// declared composition box — the rectangle a commit samples — while rect is the
// whole reserved region, which also contains any projected vertex the producer's
// box does not, so one subject can never write into its neighbour's slot.
type modelSlot struct {
	page     *modelPage
	rect     image.Rectangle
	box      image.Rectangle
	overflow bool
}

func (s modelSlot) image() *ebiten.Image {
	if s.page == nil || s.page.img == nil {
		return nil
	}
	return s.page.img.SubImage(s.box).(*ebiten.Image)
}

// modelSlotAtlas owns the frame's pages. overflow pages are the per-subject
// fallback route: one subject at a time, rasterized at commit time through the
// same batched passes.
type modelSlotAtlas struct {
	native           [modelNativePages]modelPage
	super            modelPage
	overflow         [2]modelPage
	overflowSuper    modelPage
	slots            map[*drawlist.ModelGeometry]modelSlot
	resolves         []modelResolveJob
	overflowResolves []modelResolveJob
	// quads is the frame's textured-quad parameter store, read by the key and
	// colour passes as source 3 (docs/DESIGN_GPU_RENDERER.md §11.2 "Textured
	// quads without strips").
	quads modelQuadParams
}

// modelResolveJob is one doubled slot resolving into its native slot.
type modelResolveJob struct {
	src, dst modelSlot
	keyed    bool
}

func ceilTo(v, a int) int {
	if a <= 1 {
		return v
	}
	return (v + a - 1) / a * a
}

func (p *modelPage) reset(scale, maxH int) {
	p.scale, p.maxH = scale, maxH
	if p.maxH <= 0 || p.maxH > modelPageMaxHeight {
		p.maxH = modelPageMaxHeight
	}
	p.x, p.y, p.rowH = 0, 0, 0
	p.usedW, p.usedH = 0, 0
	p.needPost = false
	p.subjects = p.subjects[:0]
}

// alloc reserves one shelf-packed region. Doubled pages align every slot to an
// even origin so a resolve's two-by-two blocks line up with the native pixel it
// produces [03 R-REN-03A §7].
func (p *modelPage) alloc(w, h int) (image.Rectangle, bool) {
	if w <= 0 || h <= 0 {
		return image.Rectangle{}, false
	}
	w, h = ceilTo(w, p.scale), ceilTo(h, p.scale)
	if w > modelPageWidth || h > p.maxH {
		return image.Rectangle{}, false
	}
	if p.x+w > modelPageWidth {
		p.y, p.x, p.rowH = p.y+p.rowH, 0, 0
	}
	if p.y+h > p.maxH {
		return image.Rectangle{}, false
	}
	rect := image.Rect(p.x, p.y, p.x+w, p.y+h)
	p.x += w
	if h > p.rowH {
		p.rowH = h
	}
	p.usedW, p.usedH = maxInt(p.usedW, rect.Max.X), maxInt(p.usedH, rect.Max.Y)
	return rect, true
}

// ensure allocates the page's planes at the height this frame needs. Pages grow
// and are reused; they are never reallocated per subject.
func (p *modelPage) ensure() {
	if p.usedH == 0 {
		return
	}
	w, h := modelPageWidth, minInt(ceilTo(p.usedH, modelPageGrowStep), modelPageMaxHeight)
	if p.img != nil && p.w >= w && p.h >= h {
		return
	}
	h = maxInt(h, p.h)
	for _, img := range []*ebiten.Image{p.img, p.key, p.post} {
		if img != nil {
			img.Deallocate()
		}
	}
	p.img, p.key, p.post = ebiten.NewImage(w, h), ebiten.NewImage(w, h), nil
	p.w, p.h = w, h
}

func (p *modelPage) ensurePost() {
	if p.post == nil && p.img != nil {
		p.post = ebiten.NewImage(p.w, p.h)
	}
}

// modelSlotBounds is the composition box widened to contain every projected
// vertex. The producer's box already includes the visible pieces with a two
// pixel margin [03 R-REN-03A §1]; the union keeps a packet whose authored
// corners fall outside it from writing into an adjacent slot.
func modelSlotBounds(g *drawlist.ModelGeometry) image.Rectangle {
	b := modelLocalBounds(g)
	if g.Width <= 0 || g.Height <= 0 {
		return b
	}
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.Outline} {
		for i := range faces {
			for _, v := range faces[i].Vertices {
				b = b.Union(image.Rect(int(v.X), int(v.Y), int(v.X)+1, int(v.Y)+1))
			}
		}
	}
	return b
}

// prepareModelSlots reserves one slot per eligible subject of the frame and
// rasterizes every page before Replay commits any of them. Preparation visits
// the same subjects the commit path consumes, in record order.
func (r *Renderer) prepareModelSlots(l *drawlist.List) {
	a := &r.modelAtlas
	if a.slots == nil {
		a.slots = make(map[*drawlist.ModelGeometry]modelSlot)
	} else {
		clear(a.slots)
	}
	// modelPageLimit is a test hook: a small limit exhausts the shared pages
	// so the per-subject fallback route runs against real geometry.
	for i := range a.native {
		a.native[i].reset(1, r.modelPageLimit)
	}
	a.super.reset(2, r.modelPageLimit)
	a.resolves = a.resolves[:0]
	a.quads.reset()
	l.VisitModels(func(m drawlist.Model) {
		g := m.Geometry
		if g == nil || !g.Eligible {
			return
		}
		r.reserveModelSlot(g)
		if g.Shadow != nil && r.modelShadowCommit != nil && r.tables.alpha != nil {
			r.reserveModelSlot(g.Shadow)
		}
		if m.ShadowOnly {
			return
		}
		for _, child := range g.Children {
			if cg := child.Geometry; cg != nil && cg.KeyPlane && len(cg.Children) == 0 {
				r.reserveModelSlot(cg)
			}
		}
	})
	r.flushModelPages()
}

// reserveModelSlot places one subject on the atlas. An unsupported packet is
// left out of the slot map entirely, so the commit path reports the same
// explicit omission it reported before (§9).
func (r *Renderer) reserveModelSlot(g *drawlist.ModelGeometry) {
	a := &r.modelAtlas
	if g == nil || !g.Eligible {
		return
	}
	if _, ok := a.slots[g]; ok {
		return
	}
	if !r.modelGeometryConfigSupported(g) || !r.modelFacesSupported(g) {
		return
	}
	if ss := g.Supersample; ss != nil && !r.modelFacesSupported(ss) {
		return
	}
	slot, ok := r.placeModelSubject(g, nil, &a.super, &a.resolves)
	if !ok {
		// The frame does not fit; this subject takes the per-subject route at
		// commit time rather than disappearing from the frame.
		a.slots[g] = modelSlot{overflow: true}
		r.modelStats.SlotOverflows++
		return
	}
	a.slots[g] = slot
}

// placeModelSubject reserves the native slot, the doubled slot a structure
// resolve needs, and appends the page subjects that rasterize them. native nil
// selects the shared atlas pages.
func (r *Renderer) placeModelSubject(g *drawlist.ModelGeometry, native, doubled *modelPage, resolves *[]modelResolveJob) (modelSlot, bool) {
	local := modelSlotBounds(g)
	if local.Empty() {
		return modelSlot{}, false
	}
	rect, page, ok := r.allocModelSlot(native, local.Dx(), local.Dy())
	if !ok {
		return modelSlot{}, false
	}
	origin := rect.Min.Sub(local.Min)
	slot := modelSlot{page: page, rect: rect, box: modelLocalBounds(g).Add(origin)}
	sub := modelPageSubject{
		origin: origin, rect: rect, keyed: g.KeyPlane,
		waterline: g.Waterline, waterlineKey: g.WaterlineKey, digger: g.Digger, diggerKey: g.DiggerKey,
	}
	sub.outline = r.prepareModelOutline(g)
	if ss := g.Supersample; ss != nil {
		// The doubled body and its reveal rasterize on the 2x page; the native
		// slot receives the resolve, then the native outline and clipping
		// passes [03 R-REN-03A §6–§7].
		ssLocal := modelSlotBounds(ss)
		if ssLocal.Empty() {
			return modelSlot{}, false
		}
		// A doubled slot is placed at an even page origin; rounding the local
		// box down to an even corner keeps the doubled body's own origin even
		// too, so each resolved native pixel reads the two-by-two block the
		// classic resolve reads [03 R-REN-03A §7].
		ssLocal.Min.X -= ssLocal.Min.X & 1
		ssLocal.Min.Y -= ssLocal.Min.Y & 1
		ssRect, ssPage, ok := r.allocModelSlot(doubled, ssLocal.Dx(), ssLocal.Dy())
		if !ok {
			return modelSlot{}, false
		}
		ssOrigin := ssRect.Min.Sub(ssLocal.Min)
		ssSub := modelPageSubject{origin: ssOrigin, rect: ssRect, keyed: ss.KeyPlane, reveal: ss.Reveal}
		ssSub.faces = r.prepareModelFaces(ss, ssOrigin)
		if ssSub.reveal != nil {
			ssPage.needPost = true
		}
		ssPage.subjects = append(ssPage.subjects, ssSub)
		*resolves = append(*resolves, modelResolveJob{
			src:   modelSlot{page: ssPage, rect: ssRect, box: modelLocalBounds(ss).Add(ssOrigin)},
			dst:   slot,
			keyed: g.KeyPlane,
		})
		r.modelStats.StructureResolves++
		r.modelStats.RasterPixels += ssRect.Dx() * ssRect.Dy()
	} else {
		sub.faces = r.prepareModelFaces(g, origin)
		sub.reveal = g.Reveal
	}
	if sub.reveal != nil || sub.clips() {
		page.needPost = true
	}
	page.subjects = append(page.subjects, sub)
	r.modelStats.RasterPixels += rect.Dx() * rect.Dy()
	return slot, true
}

// allocModelSlot places one region on the given page, or on the first shared
// native page with room when page is nil.
func (r *Renderer) allocModelSlot(page *modelPage, w, h int) (image.Rectangle, *modelPage, bool) {
	if page != nil {
		rect, ok := page.alloc(w, h)
		return rect, page, ok
	}
	for i := range r.modelAtlas.native {
		p := &r.modelAtlas.native[i]
		if rect, ok := p.alloc(w, h); ok {
			return rect, p, true
		}
	}
	return image.Rectangle{}, nil, false
}

// prepareModelFaces prepares one subject's faces. origin is where the subject's
// local (0,0) lands on its page, so a textured quad's parameters describe the
// page pixels its fragment shader compares against.
func (r *Renderer) prepareModelFaces(g *drawlist.ModelGeometry, origin image.Point) []preparedModelFace {
	out := r.modelPrep.prepared.take(len(g.Faces))
	for i := range g.Faces {
		out[i] = r.prepareModelFace(g.Faces[i], origin)
		out[i].tex = r.modelTextureFor(g.Faces[i].Texture)
	}
	return out
}

// flushModelPages rasterizes the whole frame. The doubled page and its resolves
// run first, so a resolved structure's native slot is complete before its
// outline and clipping passes read it.
func (r *Renderer) flushModelPages() {
	a := &r.modelAtlas
	pages := [modelNativePages + 1]*modelPage{&a.super}
	for i := range a.native {
		pages[i+1] = &a.native[i]
	}
	for _, p := range pages {
		p.ensure()
		if p.needPost {
			p.ensurePost()
		}
		r.clearModelPage(p)
	}
	if len(a.super.subjects) != 0 {
		r.drawModelPage(&a.super, false)
	}
	r.drawModelResolves(a.resolves)
	for i := range a.native {
		if len(a.native[i].subjects) != 0 {
			r.drawModelPage(&a.native[i], true)
		}
	}
	for i := range a.native {
		if a.native[i].usedH > 0 {
			r.modelStats.SlotPages++
		}
	}
	if a.super.usedH > 0 {
		r.modelStats.SlotPages++
	}
}

func (r *Renderer) clearModelPage(p *modelPage) {
	if p.img == nil || p.usedH == 0 {
		return
	}
	rect := image.Rect(0, 0, p.usedW, p.usedH)
	// Index 1 is the composition transparent index and 0 the cleared key
	// [03 R-REN-03A §1–§2]. One clear per page replaces one per subject.
	p.img.SubImage(rect).(*ebiten.Image).Fill(color.RGBA{R: 1, A: 255})
	p.key.SubImage(rect).(*ebiten.Image).Fill(index0Color)
	r.modelStats.Draws += 2
	r.modelStats.RasterDraws += 2
}

// drawModelPage submits one page's stages: the maximum-key pass, the colour
// pass, the reveal, then — natively — the outline and the waterline/Digger
// clipping. Every stage is one draw for every subject on the page.
func (r *Renderer) drawModelPage(p *modelPage, native bool) {
	if p.img == nil {
		return
	}
	r.drawModelPageFaces(p, true, false)
	r.drawModelPageFaces(p, false, false)
	r.drawModelProcessed(p, r.modelReveal, nil, func(s *modelPageSubject) (bool, [4]float32, [4]float32) {
		if s.reveal == nil {
			return false, [4]float32{}, [4]float32{}
		}
		v := s.reveal
		return true, [4]float32{float32(v.Line), float32(v.Floor), 0, 0},
			[4]float32{float32(v.Below), float32(v.Band), float32(v.Above), 0}
	})
	if !native {
		return
	}
	r.drawModelPageFaces(p, true, true)
	r.drawModelPageFaces(p, false, true)
	r.drawModelProcessed(p, r.modelClip, r.tables.blue, func(s *modelPageSubject) (bool, [4]float32, [4]float32) {
		if !s.clips() {
			return false, [4]float32{}, [4]float32{}
		}
		return true, [4]float32{float32(s.waterline), float32(s.waterlineKey), boolFloat(s.digger), float32(s.diggerKey)}, [4]float32{}
	})
}

// drawModelPageFaces batches every prepared face of every subject on the page
// into one device draw, split only where the texture page changes or the 16-bit
// index domain fills. Source face and scanline order is preserved across a
// split, so an equal-key tie still goes to the later face [03 R-REN-03A §3].
func (r *Renderer) drawModelPageFaces(p *modelPage, keyPass, outline bool) {
	if keyPass && (r.modelKey == nil || p.key == nil) || !keyPass && r.modelBody == nil {
		return
	}
	// Both passes evaluate the textured quad mapping from the frame's parameter
	// image, so the key a fragment writes and the key its colour pass compares
	// come from one arithmetic (§11.2 "Textured quads without strips").
	r.modelAtlas.quads.upload()
	quads := r.modelAtlas.quads.img
	r.resetGeometry()
	var texPage *ebiten.Image
	flush := func() {
		if len(r.idx) == 0 {
			return
		}
		if keyPass {
			r.rasterDraw(p.key, r.modelKey, modelKeyBlend, p.img, nil, nil, quads)
			return
		}
		r.rasterDraw(p.img, r.modelBody, ebiten.BlendSourceOver, p.key, r.tables.shade, texPage, quads)
	}
	emit := func(v []modelGPUVertex, idx []uint16, slot modelTextureSlot, textured bool, flat uint8, shaded, keyed bool, origin image.Point, quad int) {
		if !keyPass && slot.img != nil && texPage != nil && texPage != slot.img {
			flush()
		}
		if slot.img != nil {
			texPage = slot.img
		}
		if len(r.verts)+len(v) > quadBatchVertexLimit {
			flush()
		}
		base := uint16(len(r.verts))
		dx, dy := float32(origin.X), float32(origin.Y)
		for _, q := range v {
			vertex := ebiten.Vertex{
				DstX: dx + q.X, DstY: dy + q.Y,
				SrcX: float32(slot.x), SrcY: float32(slot.y),
				ColorR: q.Key, ColorG: q.Shade, ColorB: float32(flat), ColorA: boolFloat(keyed),
				Custom0: q.U, Custom1: q.V, Custom3: boolFloat(shaded),
			}
			if textured {
				vertex.Custom2 = float32(slot.w*4096 + slot.h)
				if slot.w >= 4096 || slot.h >= 4096 {
					// Too large to pack; the fragment reads the frame's own
					// image from its origin instead.
					vertex.Custom2 = -1
				}
				// The flat colour lane is dead on a textured face, so a mapped
				// quad rides it as a one-based parameter index. A face whose
				// texture slot did not resolve keeps the flat fallback the
				// shader's untextured branch already writes.
				if quad != 0 && vertex.Custom2 != 0 {
					vertex.ColorB = float32(quad)
				}
			}
			r.verts = append(r.verts, vertex)
		}
		for _, i := range idx {
			r.idx = append(r.idx, base+i)
		}
	}
	for i := range p.subjects {
		s := &p.subjects[i]
		if keyPass && !s.keyed {
			continue
		}
		if outline {
			for _, o := range s.outline {
				emit(o.Vertices, modelQuadIndices, modelTextureSlot{}, false, o.Color, false, s.keyed, s.origin, 0)
			}
			continue
		}
		// The texture page is resolved once per source face at preparation
		// time; every strip of one face shares it.
		for i := range s.faces {
			f := &s.faces[i]
			textured := f.face.Texture != nil
			for _, strip := range f.strips {
				emit(strip.Vertices, modelQuadIndices, f.tex, textured, strip.Color, strip.Shaded, s.keyed, s.origin, 0)
			}
			if len(f.indices) != 0 {
				emit(f.vertices, f.indices, f.tex, textured, f.face.Color, f.face.Shaded, s.keyed, s.origin, f.quad)
			}
		}
	}
	flush()
}

var modelQuadIndices = []uint16{0, 1, 2, 0, 2, 3}

var modelKeyBlend = ebiten.Blend{BlendOperationRGB: ebiten.BlendOperationMax, BlendOperationAlpha: ebiten.BlendOperationMax}

// drawModelProcessed runs one destination-reading stage over every subject that
// asks for it. The stage reads the page and writes the page's scratch plane, so
// one batched draw covers the frame; the affected regions are then copied back.
func (r *Renderer) drawModelProcessed(p *modelPage, shader *ebiten.Shader, table *ebiten.Image, params func(*modelPageSubject) (bool, [4]float32, [4]float32)) {
	if shader == nil || r.modelCopy == nil || p.img == nil || p.post == nil {
		return
	}
	r.resetGeometry()
	// The stage reads the page and writes its scratch plane, then the same
	// quads copy the processed regions back. Two draws cover the whole frame.
	flush := func() {
		if len(r.idx) == 0 {
			return
		}
		verts, idx := len(r.verts), len(r.idx)
		r.rasterDraw(p.post, shader, ebiten.BlendCopy, p.img, table, nil, nil)
		r.verts, r.idx = r.verts[:verts], r.idx[:idx]
		r.rasterDraw(p.img, r.modelCopy, ebiten.BlendCopy, p.post, nil, nil, nil)
	}
	for i := range p.subjects {
		s := &p.subjects[i]
		on, col, custom := params(s)
		if !on {
			continue
		}
		if !r.quadBatchHasRoom() {
			flush()
		}
		r.appendModelQuad(s.rect, s.rect, col, custom)
	}
	flush()
}

// drawModelResolves resolves every doubled slot into its native slot: one draw
// for the composed plane and one for the key plane the outline pass reads
// [03 R-REN-03A §6–§7].
func (r *Renderer) drawModelResolves(jobs []modelResolveJob) {
	if len(jobs) == 0 || r.modelResolve == nil || r.tables.alpha == nil {
		return
	}
	for pass := 0; pass < 2; pass++ {
		r.resetGeometry()
		key := pass == 1
		var dst, src *modelPage
		for _, j := range jobs {
			if key && !j.keyed {
				continue
			}
			if j.src.page == nil || j.dst.page == nil {
				continue
			}
			if dst != nil && dst != j.dst.page || src != nil && src != j.src.page || !r.quadBatchHasRoom() {
				r.flushModelResolve(dst, src, key)
			}
			dst, src = j.dst.page, j.src.page
			r.appendModelQuad(j.dst.box, j.src.box, [4]float32{boolFloat(key), 0, 0, 0}, [4]float32{})
		}
		r.flushModelResolve(dst, src, key)
	}
}

func (r *Renderer) flushModelResolve(dst, src *modelPage, key bool) {
	if dst == nil || src == nil || len(r.idx) == 0 {
		return
	}
	target := dst.img
	if key {
		target = dst.key
	}
	r.rasterDraw(target, r.modelResolve, ebiten.BlendCopy, src.img, r.tables.alpha, nil, nil)
}

// appendModelQuad appends one destination/source rectangle pair carrying the
// stage's parameters on its vertices.
func (r *Renderer) appendModelQuad(dst, src image.Rectangle, col, custom [4]float32) {
	base := uint16(len(r.verts))
	corners := [4][4]float32{
		{float32(dst.Min.X), float32(dst.Min.Y), float32(src.Min.X), float32(src.Min.Y)},
		{float32(dst.Max.X), float32(dst.Min.Y), float32(src.Max.X), float32(src.Min.Y)},
		{float32(dst.Max.X), float32(dst.Max.Y), float32(src.Max.X), float32(src.Max.Y)},
		{float32(dst.Min.X), float32(dst.Max.Y), float32(src.Min.X), float32(src.Max.Y)},
	}
	for _, c := range corners {
		r.verts = append(r.verts, ebiten.Vertex{
			DstX: c[0], DstY: c[1], SrcX: c[2], SrcY: c[3],
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3],
		})
	}
	r.idx = append(r.idx, base, base+1, base+2, base, base+2, base+3)
}

// modelDraw submits one model-family draw through the renderer's reused options
// value, so no steady-state frame allocates a draw option or uniform map
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"].
func (r *Renderer) modelDraw(dst *ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, src0, src1, src2, src3 *ebiten.Image) {
	if dst == nil || shader == nil || len(r.idx) == 0 {
		return
	}
	r.modelOpts.Blend = blend
	r.modelOpts.Images[0], r.modelOpts.Images[1] = src0, src1
	r.modelOpts.Images[2], r.modelOpts.Images[3] = src2, src3
	dst.DrawTrianglesShader(r.verts, r.idx, shader, &r.modelOpts)
	r.modelStats.Draws++
	r.resetGeometry()
}

// rasterDraw is a slot atlas pass: one draw for every subject on one page, so
// its count is a property of the frame's pages, not of the number of subjects.
func (r *Renderer) rasterDraw(dst *ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, src0, src1, src2, src3 *ebiten.Image) {
	if dst == nil || shader == nil || len(r.idx) == 0 {
		return
	}
	r.modelStats.RasterDraws++
	r.modelDraw(dst, shader, blend, src0, src1, src2, src3)
}

// rasterizeModelOverflow is the per-subject fallback for a frame whose subjects
// do not fit the atlas. It runs the same stages over a single-subject page at
// commit time, so an oversized frame costs more draws but loses no subject.
func (r *Renderer) rasterizeModelOverflow(g *drawlist.ModelGeometry, set int) (modelSlot, bool) {
	a := &r.modelAtlas
	native, doubled := &a.overflow[set], &a.overflowSuper
	native.reset(1, modelPageMaxHeight)
	doubled.reset(2, modelPageMaxHeight)
	a.overflowResolves = a.overflowResolves[:0]
	slot, ok := r.placeModelSubject(g, native, doubled, &a.overflowResolves)
	if !ok {
		return modelSlot{}, false
	}
	for _, p := range []*modelPage{doubled, native} {
		p.ensure()
		if p.needPost {
			p.ensurePost()
		}
		r.clearModelPage(p)
	}
	if len(doubled.subjects) != 0 {
		r.drawModelPage(doubled, false)
	}
	r.drawModelResolves(a.overflowResolves)
	r.drawModelPage(native, true)
	return slot, true
}
