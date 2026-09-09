package gpurender

import (
	"image"
	"image/color"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The modern executor rasterizes every model subject of one frame into a
// per-frame slot atlas and submits the stage as a fixed handful of passes,
// replacing the per-subject scratch surfaces, the body image cache and its page
// packer [DESIGN_GPU_RENDERER.md §11.2, §11.5]. Slots are disjoint, so the
// maximum-byte-key pass stays subject-local [03 R-REN-03A §2] and cross-subject
// painter order remains the commit order Replay issues [03 R-RAST-01 §7].
//
// One page carries every subject of the frame, native and doubled alike, and
// the stage is ordered by destination rather than by subject or page: the device
// cost unit is the destination switch, because the driver ends its render pass —
// a whole attachment load and store — whenever the destination image changes
// [DESIGN_GPU_RENDERER.md §11.5]. Ordering by destination makes the switch count
// a property of which stages the frame needs, never of how many subjects or
// pages it holds.
//
// These are renderer payload bounds, not retail constants. Page height grows to
// what one frame needs; a frame that still does not fit falls back to the
// per-subject route below and is counted, never dropped.
const (
	modelPageWidth     = 2048
	modelPageMaxHeight = 2048
	modelPageGrowStep  = 256
	// One page now carries what the doubled page and both native pages carried
	// before it, so its row bound is two page heights rather than one: a loaded
	// battle at 1920x1080 reserves about 1,850 rows, and one page height would
	// leave almost nothing above that. The planes are still grown only to what a
	// frame asks for, so the bound is reached only by a frame that reserves it,
	// and a frame that does not fit still takes the per-subject fallback route.
	modelPageMaxRows = 2 * modelPageMaxHeight
	// Every slot is placed on an even page origin with even dimensions, so a
	// doubled slot's two-by-two blocks line up with the native pixels the
	// resolve produces [03 R-REN-03A §7]. Paying that alignment on native slots
	// too — at most one wasted row and column each — is what lets native and
	// doubled subjects share one page, which removes the doubled page's own
	// clear, key, colour and reveal passes from the stage.
	modelSlotAlign = 2
	// modelBatchVertexLimit bounds one device draw's vertex range. Indices are
	// uint32 (§11.5 "CPU"), so this is a payload bound rather than the 16-bit
	// index domain the earlier batches were split by.
	modelBatchVertexLimit = 1 << 20
)

// modelPage is one slot atlas page. img holds the composed subject planes —
// index in red, the key stored at that texel in green, body coverage in blue —
// key holds the maximum-byte-key plane the body pass reads, and post is the
// scratch the reveal and clipping stages write. Doubled subjects live on the
// same page as native ones, at even origins [03 R-REN-03A §6].
type modelPage struct {
	img, key, post *ebiten.Image
	w, h           int

	x, y, rowH   int
	maxH         int
	usedW, usedH int
	needPost     bool
	needClip     bool
	subjects     []modelPageSubject
}

// modelPageSubject is one subject's raster on the page: where its local origin
// lands, the prepared faces and outline endpoints, and the processed-pass
// parameters that used to be per-draw uniforms.
type modelPageSubject struct {
	origin  image.Point
	rect    image.Rectangle
	faces   []preparedModelFace
	outline []modelGPUFace
	reveal  *drawlist.ModelReveal

	keyed bool
	// doubled marks a subject whose finished plane a resolve reads. Its plane
	// must reach the post scratch even when it carries no reveal, because the
	// resolve reads post while it writes the page [03 R-REN-03A §6–§7].
	doubled      bool
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

// image is the page region a commit or a group merge samples. It comes from the
// recyclable pool: the box of a slot moves with the frame's packing, so the
// image's own sub-image cache would gain an entry per subject per frame and pay
// a full sweep of that cache once a tick
// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy", §11.5 "CPU"). The
// caller recycles it as soon as the draw that binds it has been issued.
func (s modelSlot) image() *ebiten.Image {
	if s.page == nil || s.page.img == nil {
		return nil
	}
	return s.page.img.RecyclableSubImage(s.box)
}

// recycleImage returns an image() result to the pool. A nil image is ignored, so
// callers do not have to test for the empty slot twice.
func recycleImage(img *ebiten.Image) {
	if img != nil {
		img.Recycle()
	}
}

// modelFaceRun is one device draw of the page's shared vertex list. Indices are
// relative to v0, so a run submits only the vertices it addresses. The list is
// built once per page per frame and read by both the key pass and the colour
// pass, which differ only in destination, shader, blend and — for the colour
// pass — the texture page a run samples (§11.5 "Model slot passes").
type modelFaceRun struct {
	v0, vn int
	i0, iN int
	tex    *ebiten.Image
	keyed  bool
}

// modelSlotAtlas owns the frame's page. overflow pages are the per-subject
// fallback route: one subject at a time, rasterized at commit time through the
// same ordered stages.
type modelSlotAtlas struct {
	page             modelPage
	overflow         [2]modelPage
	slots            map[*drawlist.ModelGeometry]modelSlot
	resolves         []modelResolveJob
	overflowResolves []modelResolveJob
	// passes counts the slot stage's device destination switches: the unit of
	// cost on a driver that opens a render pass whenever the destination image
	// changes [DESIGN_GPU_RENDERER.md §11.5]. lastDst is the destination the
	// previous slot-stage device call named.
	passes  int
	lastDst *ebiten.Image
	// verts/idx/runs are the page's face geometry, built once per page per
	// frame; quadVerts/quadIdx are the stage-quad geometry the reveal, resolve,
	// copy and clipping stages append to. They are separate stores because the
	// outline runs must survive the quad draws issued between the outline key
	// pass and the outline colour pass.
	verts     []ebiten.Vertex
	idx       []uint32
	runs      []modelFaceRun
	quadVerts []ebiten.Vertex
	quadIdx   []uint32
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

// modelPass records one slot-stage device call's destination. The Metal driver
// ends its render command encoder — a whole attachment load and store — whenever
// the destination image changes, so this counts the stage's real cost unit
// [DESIGN_GPU_RENDERER.md §11.5]. dst is always a whole plane, never a
// sub-image, so pointer identity is the destination identity.
func (r *Renderer) modelPass(dst *ebiten.Image) {
	a := &r.modelAtlas
	if a.lastDst != dst {
		a.passes++
		a.lastDst = dst
	}
	// The frame-wide count in ModelStats.Passes sees the same switch.
	r.beginPass(dst)
}

// modelStagePasses reports the destination switches the most recent frame's
// slot stage issued. Diagnostic only; it never reaches output state.
func (r *Renderer) modelStagePasses() int {
	if r == nil {
		return 0
	}
	return r.modelAtlas.passes
}

func ceilTo(v, a int) int {
	if a <= 1 {
		return v
	}
	return (v + a - 1) / a * a
}

func (p *modelPage) reset(maxH int) {
	p.maxH = maxH
	if p.maxH <= 0 || p.maxH > modelPageMaxRows {
		p.maxH = modelPageMaxRows
	}
	p.x, p.y, p.rowH = 0, 0, 0
	p.usedW, p.usedH = 0, 0
	p.needPost, p.needClip = false, false
	p.subjects = p.subjects[:0]
}

// alloc reserves one shelf-packed region on an even origin with even
// dimensions, so a doubled slot's two-by-two blocks line up with the native
// pixel the resolve produces [03 R-REN-03A §7].
func (p *modelPage) alloc(w, h int) (image.Rectangle, bool) {
	if w <= 0 || h <= 0 {
		return image.Rectangle{}, false
	}
	w, h = ceilTo(w, modelSlotAlign), ceilTo(h, modelSlotAlign)
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
	w, h := modelPageWidth, minInt(ceilTo(p.usedH, modelPageGrowStep), modelPageMaxRows)
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
	// The union is taken over the corner coordinates directly: a rectangle union
	// per vertex ran the empty-rectangle test on every corner of every face of
	// every subject of the frame (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation
	// policy"). The producer's box is never empty here, so the result is the same.
	x0, y0, x1, y1 := int32(b.Min.X), int32(b.Min.Y), int32(b.Max.X), int32(b.Max.Y)
	for _, faces := range [][]drawlist.ModelFace{g.Faces, g.Outline} {
		for i := range faces {
			for _, v := range faces[i].Vertices {
				x0, y0 = min(x0, v.X), min(y0, v.Y)
				x1, y1 = max(x1, v.X+1), max(y1, v.Y+1)
			}
		}
	}
	return image.Rect(int(x0), int(y0), int(x1), int(y1))
}

// prepareModelSlots reserves one slot per eligible subject of the frame and
// rasterizes the page before Replay commits any of them. Preparation visits the
// same subjects the commit path consumes, in record order.
func (r *Renderer) prepareModelSlots(l *drawlist.List) {
	a := &r.modelAtlas
	if a.slots == nil {
		a.slots = make(map[*drawlist.ModelGeometry]modelSlot)
	} else {
		clear(a.slots)
	}
	// modelPageLimit is a test hook: a small limit exhausts the shared page so
	// the per-subject fallback route runs against real geometry.
	a.page.reset(r.modelPageLimit)
	a.resolves = a.resolves[:0]
	a.passes, a.lastDst = 0, nil
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
	r.flushModelPage(&a.page, a.resolves)
	// SlotPages keeps its meaning: the page-sized areas this frame's raster
	// needed, now measured as bands of one page height on the shared page.
	r.modelStats.SlotPages += ceilTo(a.page.usedH, modelPageMaxHeight) / modelPageMaxHeight
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
	if !r.modelGeometryConfigSupported(g) {
		return
	}
	crosses, ok := r.modelFacesSupported(g)
	if !ok {
		return
	}
	var ssCrosses []bool
	if ss := g.Supersample; ss != nil {
		if ssCrosses, ok = r.modelFacesSupported(ss); !ok {
			return
		}
	}
	slot, ok := r.placeModelSubject(g, crosses, ssCrosses, &a.page, &a.resolves)
	if !ok {
		// The frame does not fit; this subject takes the per-subject route at
		// commit time rather than disappearing from the frame.
		a.slots[g] = modelSlot{overflow: true}
		r.modelStats.SlotOverflows++
		return
	}
	a.slots[g] = slot
}

// placeModelSubject reserves the native slot and the doubled slot a structure
// resolve needs, and appends the page subjects that rasterize them. Both land on
// the same page, so a doubled body costs no pass of its own.
func (r *Renderer) placeModelSubject(g *drawlist.ModelGeometry, crosses, ssCrosses []bool, page *modelPage, resolves *[]modelResolveJob) (modelSlot, bool) {
	local := modelSlotBounds(g)
	if local.Empty() {
		return modelSlot{}, false
	}
	rect, ok := page.alloc(local.Dx(), local.Dy())
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
		// The doubled body and its reveal rasterize into a doubled slot on the
		// same page; the native slot receives the resolve, then the native
		// outline and clipping passes [03 R-REN-03A §6–§7].
		ssLocal := modelSlotBounds(ss)
		if ssLocal.Empty() {
			return modelSlot{}, false
		}
		// Rounding the local box down to an even corner keeps the doubled
		// body's own origin even inside its evenly placed slot, so each
		// resolved native pixel reads the two-by-two block the classic resolve
		// reads [03 R-REN-03A §7].
		ssLocal.Min.X -= ssLocal.Min.X & 1
		ssLocal.Min.Y -= ssLocal.Min.Y & 1
		ssRect, ok := page.alloc(ssLocal.Dx(), ssLocal.Dy())
		if !ok {
			return modelSlot{}, false
		}
		ssOrigin := ssRect.Min.Sub(ssLocal.Min)
		ssSub := modelPageSubject{origin: ssOrigin, rect: ssRect, keyed: ss.KeyPlane, reveal: ss.Reveal, doubled: true}
		ssSub.faces = r.prepareModelFaces(ss, ssOrigin, ssCrosses)
		// The resolve reads the doubled plane out of the post scratch while it
		// writes the page, so every doubled subject reaches post whether or not
		// it carries a reveal.
		page.needPost = true
		page.subjects = append(page.subjects, ssSub)
		*resolves = append(*resolves, modelResolveJob{
			src:   modelSlot{page: page, rect: ssRect, box: modelLocalBounds(ss).Add(ssOrigin)},
			dst:   slot,
			keyed: g.KeyPlane,
		})
		r.modelStats.StructureResolves++
		r.modelStats.RasterPixels += ssRect.Dx() * ssRect.Dy()
	} else {
		sub.faces = r.prepareModelFaces(g, origin, crosses)
		sub.reveal = g.Reveal
	}
	if sub.reveal != nil {
		page.needPost = true
	}
	if sub.clips() {
		page.needPost, page.needClip = true, true
	}
	page.subjects = append(page.subjects, sub)
	r.modelStats.RasterPixels += rect.Dx() * rect.Dy()
	return slot, true
}

// prepareModelFaces prepares one subject's faces. origin is where the subject's
// local (0,0) lands on its page, so a textured quad's parameters describe the
// page pixels its fragment shader compares against. crosses is the admission's
// per-face self-intersection verdict; a nil slice makes each face compute its
// own, which is what the per-subject fallback route needs.
func (r *Renderer) prepareModelFaces(g *drawlist.ModelGeometry, origin image.Point, crosses []bool) []preparedModelFace {
	out := r.modelPrep.prepared.take(len(g.Faces))
	for i := range g.Faces {
		f := &g.Faces[i]
		x := false
		if i < len(crosses) {
			x = crosses[i]
		} else {
			x = polygonCrosses(f.Vertices)
		}
		r.prepareModelFace(&out[i], f, origin, x)
		out[i].tex = r.modelTextureFor(f.Texture)
	}
	return out
}

// flushModelPage rasterizes one page as a sequence ordered by destination, not
// by subject: every clear, then every key write, then every colour write, then
// the follow-ups, with two stages kept apart exactly where the researched
// semantics require it [DESIGN_GPU_RENDERER.md §11.5 "Model slot passes"].
//
// The order, and the reason each boundary exists:
//
//	img   the clear
//	key   the clear, then every subject's key faces
//	img   every subject's colour faces — reads the key plane the key faces
//	      wrote, and must not see the outline keys
//	post  the reveal, and every doubled plane a resolve reads — a fragment
//	      cannot read the image it writes, so the reveal is a ping-pong, and
//	      routing the doubled planes through the same scratch is what lets a
//	      resolve read one plane of the page while it writes another
//	key   the key-plane resolves, then the outline keys, which max-blend on top
//	      of the resolved key
//	img   the revealed regions copied back, the composed resolves, then the
//	      outline colours — outline colour compares against keys including the
//	      outline keys [§10], and a resolve must not overwrite the outline
//	post  the waterline/Digger clipping that follows colour [§10], after which
//	      the two planes swap, so the finished raster is the one img names
//
// That is at most eight destination switches for the whole stage whatever the
// frame holds; a stage the frame does not need is skipped entirely.
func (r *Renderer) flushModelPage(p *modelPage, resolves []modelResolveJob) {
	if p.usedH == 0 {
		return
	}
	p.ensure()
	if p.needPost {
		p.ensurePost()
	}
	if p.img == nil {
		return
	}
	// The stage's first device call always opens a pass: whatever ran before it
	// wrote somewhere else.
	r.modelAtlas.lastDst = nil
	r.clearModelPage(p)
	r.buildModelFaceRuns(p, false)
	r.drawModelKeyRuns(p)
	r.drawModelColourRuns(p)
	r.drawModelReveal(p)
	r.drawModelResolvePass(resolves, true)
	r.buildModelFaceRuns(p, true)
	r.drawModelKeyRuns(p)
	r.drawModelRevealCopyBack(p)
	r.drawModelResolvePass(resolves, false)
	r.drawModelColourRuns(p)
	r.drawModelClip(p)
}

func (r *Renderer) clearModelPage(p *modelPage) {
	if p.img == nil || p.usedH == 0 {
		return
	}
	rect := image.Rect(0, 0, p.usedW, p.usedH)
	// Index 1 is the composition transparent index and 0 the cleared key
	// [03 R-REN-03A §1–§2]. One clear per plane replaces one per subject. Both
	// sub-images live only for the call, so they are recycled rather than kept
	// in the page's own sub-image cache (§11.5 "CPU").
	r.modelPass(p.img)
	fillModelRegion(p.img, rect, color.RGBA{R: 1, A: 255})
	r.modelPass(p.key)
	fillModelRegion(p.key, rect, index0Color)
	r.modelStats.Draws += 2
	r.modelStats.RasterDraws += 2
}

func fillModelRegion(img *ebiten.Image, rect image.Rectangle, c color.RGBA) {
	sub := img.RecyclableSubImage(rect)
	sub.Fill(c)
	sub.Recycle()
}

// buildModelFaceRuns assembles one page's device geometry once per frame. The
// key pass and the colour pass carry identical vertices — they differ only in
// destination, shader, blend and the texture page a run samples — so the list is
// built once and read twice (§11.5 "Model slot passes"). Subjects owning a key
// plane are emitted first, so the key pass is exactly the leading runs instead
// of a second walk that skips keyless subjects.
//
// Source face and scanline order is preserved inside each subject, so an
// equal-key tie still goes to the later face [03 R-REN-03A §3]; subject order
// within a page is free because slots are disjoint.
func (r *Renderer) buildModelFaceRuns(p *modelPage, outline bool) {
	a := &r.modelAtlas
	a.verts, a.idx, a.runs = a.verts[:0], a.idx[:0], a.runs[:0]
	run := modelFaceRun{keyed: true}
	var texPage *ebiten.Image
	closeRun := func() {
		run.vn, run.iN = len(a.verts)-run.v0, len(a.idx)
		if run.iN > run.i0 {
			run.tex = texPage
			a.runs = append(a.runs, run)
		}
		run = modelFaceRun{v0: len(a.verts), i0: len(a.idx), keyed: run.keyed}
	}
	emit := func(v []modelGPUVertex, idx []uint16, slot modelTextureSlot, textured bool, flat uint8, shaded, keyed bool, origin image.Point, quad int) {
		// The colour pass samples one texture page per draw, so a face on
		// another page opens the next run; the key pass ignores the texture and
		// simply draws the same runs.
		if slot.img != nil && texPage != nil && texPage != slot.img {
			closeRun()
		}
		if slot.img != nil {
			texPage = slot.img
		}
		if len(a.verts)-run.v0+len(v) > modelBatchVertexLimit {
			closeRun()
		}
		base := uint32(len(a.verts) - run.v0)
		dx, dy := float32(origin.X), float32(origin.Y)
		// Every lane but the position and the source corner is constant over the
		// face, so it is resolved once here rather than per vertex; a battle frame
		// emits tens of thousands of these vertices
		// (docs/DESIGN_GPU_RENDERER.md §13 "CPU/allocation policy").
		sx, sy := float32(slot.x), float32(slot.y)
		colorB, custom2 := float32(flat), float32(0)
		if textured {
			custom2 = float32(slot.w*4096 + slot.h)
			if slot.w >= 4096 || slot.h >= 4096 {
				// Too large to pack; the fragment reads the frame's own
				// image from its origin instead.
				custom2 = -1
			}
			// The flat colour lane is dead on a textured face, so a mapped
			// quad rides it as a one-based parameter index. A face whose
			// texture slot did not resolve keeps the flat fallback the
			// shader's untextured branch already writes.
			if quad != 0 && custom2 != 0 {
				colorB = float32(quad)
			}
		}
		colorA, custom3 := boolFloat(keyed), boolFloat(shaded)
		// Both stores are grown once for the whole face and written in place, so
		// the per-element append bound check disappears.
		nv := len(a.verts)
		a.verts = slices.Grow(a.verts, len(v))[:nv+len(v)]
		out := a.verts[nv:]
		for i := range v {
			q := &v[i]
			out[i] = ebiten.Vertex{
				DstX: dx + q.X, DstY: dy + q.Y,
				SrcX: sx, SrcY: sy,
				ColorR: q.Key, ColorG: q.Shade, ColorB: colorB, ColorA: colorA,
				Custom0: q.U, Custom1: q.V, Custom2: custom2, Custom3: custom3,
			}
		}
		ni := len(a.idx)
		a.idx = slices.Grow(a.idx, len(idx))[:ni+len(idx)]
		into := a.idx[ni:]
		for i, ix := range idx {
			into[i] = base + uint32(ix)
		}
	}
	for _, keyed := range [2]bool{true, false} {
		if run.keyed != keyed {
			closeRun()
			run.keyed = keyed
		}
		for i := range p.subjects {
			s := &p.subjects[i]
			if s.keyed != keyed {
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
			for j := range s.faces {
				f := &s.faces[j]
				textured := f.face.Texture != nil
				for _, strip := range f.strips {
					emit(strip.Vertices, modelQuadIndices, f.tex, textured, strip.Color, strip.Shaded, s.keyed, s.origin, 0)
				}
				if len(f.indices) != 0 {
					emit(f.vertices, f.indices, f.tex, textured, f.face.Color, f.face.Shaded, s.keyed, s.origin, f.quad)
				}
			}
		}
	}
	closeRun()
	// Padding a submitted vertex slice needs capacity past the list's end.
	a.verts = reserveModelVertices(a.verts)
}

// drawModelKeyRuns writes the maximum-byte-key plane. Keyless subjects stay in
// painter order and own no key, so only the leading keyed runs draw
// [03 R-REN-03A §2].
func (r *Renderer) drawModelKeyRuns(p *modelPage) {
	a := &r.modelAtlas
	if r.modelKey == nil || p.key == nil || len(a.runs) == 0 {
		return
	}
	// Both passes evaluate the textured quad mapping from the frame's parameter
	// image, so the key a fragment writes and the key its colour pass compares
	// come from one arithmetic (§11.2 "Textured quads without strips").
	a.quads.upload()
	for i := range a.runs {
		if !a.runs[i].keyed {
			continue
		}
		r.drawModelRun(p.key, r.modelKey, modelKeyBlend, &a.runs[i], p.img, nil, nil, a.quads.img)
	}
}

// drawModelColourRuns writes the composed plane. Each fragment compares the
// interpolated key against the key stored at its own page texel.
func (r *Renderer) drawModelColourRuns(p *modelPage) {
	a := &r.modelAtlas
	if r.modelBody == nil || len(a.runs) == 0 {
		return
	}
	a.quads.upload()
	for i := range a.runs {
		r.drawModelRun(p.img, r.modelBody, ebiten.BlendSourceOver, &a.runs[i], p.key, r.tables.shade, a.runs[i].tex, a.quads.img)
	}
}

func (r *Renderer) drawModelRun(dst *ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, run *modelFaceRun, src0, src1, src2, src3 *ebiten.Image) {
	a := &r.modelAtlas
	if dst == nil || shader == nil || run.iN <= run.i0 {
		return
	}
	r.modelStats.RasterDraws++
	r.modelStats.Draws++
	r.modelPass(dst)
	r.modelOpts.Blend = blend
	r.modelOpts.Images[0], r.modelOpts.Images[1] = src0, src1
	r.modelOpts.Images[2], r.modelOpts.Images[3] = src2, src3
	dst.DrawTrianglesShader32(padModelVertices(a.verts, run.v0, run.vn), a.idx[run.i0:run.iN], shader, &r.modelOpts)
}

// drawModelReveal applies the nanoframe reveal, and carries every doubled plane
// a resolve reads, into the page's scratch. A fragment cannot read the image it
// writes, so the reveal is a ping-pong; routing the doubled planes through the
// same scratch is what lets a resolve read one plane of the page while it writes
// another [03 R-COMP-01 §3][03 R-REN-03A §6].
func (r *Renderer) drawModelReveal(p *modelPage) {
	if p.post == nil || r.modelCopy == nil {
		return
	}
	// A doubled subject with no reveal only has to reach the scratch unchanged.
	r.resetModelQuads()
	for i := range p.subjects {
		s := &p.subjects[i]
		if !s.doubled || s.reveal != nil {
			continue
		}
		if !r.modelQuadHasRoom() {
			r.rasterQuadDraw(p.post, r.modelCopy, ebiten.BlendCopy, p.img, nil)
		}
		r.appendModelQuad(s.rect, s.rect, [4]float32{}, [4]float32{})
	}
	r.rasterQuadDraw(p.post, r.modelCopy, ebiten.BlendCopy, p.img, nil)
	if r.modelReveal == nil {
		return
	}
	r.resetModelQuads()
	for i := range p.subjects {
		s := &p.subjects[i]
		if s.reveal == nil {
			continue
		}
		if !r.modelQuadHasRoom() {
			r.rasterQuadDraw(p.post, r.modelReveal, ebiten.BlendCopy, p.img, nil)
		}
		v := s.reveal
		r.appendModelQuad(s.rect, s.rect,
			[4]float32{float32(v.Line), float32(v.Floor), 0, 0},
			[4]float32{float32(v.Below), float32(v.Band), float32(v.Above), 0})
	}
	r.rasterQuadDraw(p.post, r.modelReveal, ebiten.BlendCopy, p.img, nil)
}

// drawModelRevealCopyBack returns each revealed native region to the page. A
// doubled subject needs no copy: its resolve reads the scratch plane and writes
// the native slot directly.
func (r *Renderer) drawModelRevealCopyBack(p *modelPage) {
	if p.post == nil || r.modelCopy == nil {
		return
	}
	r.resetModelQuads()
	for i := range p.subjects {
		s := &p.subjects[i]
		if s.reveal == nil || s.doubled {
			continue
		}
		if !r.modelQuadHasRoom() {
			r.rasterQuadDraw(p.img, r.modelCopy, ebiten.BlendCopy, p.post, nil)
		}
		r.appendModelQuad(s.rect, s.rect, [4]float32{}, [4]float32{})
	}
	r.rasterQuadDraw(p.img, r.modelCopy, ebiten.BlendCopy, p.post, nil)
}

// drawModelResolvePass resolves every doubled plane, reading the scratch the
// reveal stage wrote. key selects the key plane the outline pass reads, which is
// the top-left sample of each block [03 R-REN-03A §6–§7].
func (r *Renderer) drawModelResolvePass(jobs []modelResolveJob, key bool) {
	if len(jobs) == 0 || r.modelResolve == nil || r.tables.alpha == nil {
		return
	}
	var page *modelPage
	r.resetModelQuads()
	for _, j := range jobs {
		if key && !j.keyed || j.src.page == nil || j.dst.page == nil || j.dst.page.post == nil {
			continue
		}
		if page != nil && page != j.dst.page || !r.modelQuadHasRoom() {
			r.flushModelResolve(page, key)
		}
		page = j.dst.page
		r.appendModelQuad(j.dst.box, j.src.box, [4]float32{boolFloat(key), 0, 0, 0}, [4]float32{})
	}
	r.flushModelResolve(page, key)
}

func (r *Renderer) flushModelResolve(page *modelPage, key bool) {
	if page == nil {
		r.resetModelQuads()
		return
	}
	target := page.img
	if key {
		target = page.key
	}
	r.rasterQuadDraw(target, r.modelResolve, ebiten.BlendCopy, page.post, r.tables.alpha)
}

// drawModelClip applies the waterline and Digger clipping that follows colour
// [03 R-WATER-01 §2][03 R-REN-03A §8]. The stage copies the whole used page
// forward into the scratch first, so exchanging the two planes afterwards leaves
// the finished raster under the name every commit site reads. That replaces the
// copy back a per-region ping-pong would need with one destination switch; the
// two planes are otherwise interchangeable, and the commit sites read the page's
// img field after the whole stage has run.
func (r *Renderer) drawModelClip(p *modelPage) {
	if !p.needClip || p.post == nil || r.modelClip == nil || r.modelCopy == nil {
		return
	}
	used := image.Rect(0, 0, p.usedW, p.usedH)
	r.resetModelQuads()
	r.appendModelQuad(used, used, [4]float32{}, [4]float32{})
	r.rasterQuadDraw(p.post, r.modelCopy, ebiten.BlendCopy, p.img, nil)
	r.resetModelQuads()
	for i := range p.subjects {
		s := &p.subjects[i]
		if !s.clips() {
			continue
		}
		if !r.modelQuadHasRoom() {
			r.rasterQuadDraw(p.post, r.modelClip, ebiten.BlendCopy, p.img, r.tables.blue)
		}
		r.appendModelQuad(s.rect, s.rect,
			[4]float32{float32(s.waterline), float32(s.waterlineKey), boolFloat(s.digger), float32(s.diggerKey)},
			[4]float32{})
	}
	r.rasterQuadDraw(p.post, r.modelClip, ebiten.BlendCopy, p.img, r.tables.blue)
	p.img, p.post = p.post, p.img
}

var modelQuadIndices = []uint16{0, 1, 2, 0, 2, 3}

var modelKeyBlend = ebiten.Blend{BlendOperationRGB: ebiten.BlendOperationMax, BlendOperationAlpha: ebiten.BlendOperationMax}

// appendModelQuad appends one destination/source rectangle pair carrying the
// stage's parameters on its vertices.
func (r *Renderer) appendModelQuad(dst, src image.Rectangle, col, custom [4]float32) {
	a := &r.modelAtlas
	base := uint32(len(a.quadVerts))
	corners := [4][4]float32{
		{float32(dst.Min.X), float32(dst.Min.Y), float32(src.Min.X), float32(src.Min.Y)},
		{float32(dst.Max.X), float32(dst.Min.Y), float32(src.Max.X), float32(src.Min.Y)},
		{float32(dst.Max.X), float32(dst.Max.Y), float32(src.Max.X), float32(src.Max.Y)},
		{float32(dst.Min.X), float32(dst.Max.Y), float32(src.Min.X), float32(src.Max.Y)},
	}
	for _, c := range corners {
		a.quadVerts = append(a.quadVerts, ebiten.Vertex{
			DstX: c[0], DstY: c[1], SrcX: c[2], SrcY: c[3],
			ColorR: col[0], ColorG: col[1], ColorB: col[2], ColorA: col[3],
			Custom0: custom[0], Custom1: custom[1], Custom2: custom[2], Custom3: custom[3],
		})
	}
	a.quadIdx = append(a.quadIdx, base, base+1, base+2, base, base+2, base+3)
}

func (r *Renderer) resetModelQuads() {
	a := &r.modelAtlas
	a.quadVerts, a.quadIdx = a.quadVerts[:0], a.quadIdx[:0]
}

// modelQuadHasRoom reports whether another four-vertex quad fits the current
// stage batch.
func (r *Renderer) modelQuadHasRoom() bool {
	return len(r.modelAtlas.quadVerts) <= modelBatchVertexLimit-quadVertices
}

// modelDraw submits one model-family draw through the renderer's reused options
// value, so no steady-state frame allocates a draw option or uniform map
// [DESIGN_GPU_RENDERER.md §11.2 "Allocation policy"]. Indices are uint32, since
// Ebitengine converts a uint16 index slice into a freshly grown uint32 buffer on
// every call (§11.5 "CPU").
func (r *Renderer) modelDraw(dst *ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, src0, src1, src2, src3 *ebiten.Image) {
	a := &r.modelAtlas
	if dst != nil && shader != nil && len(a.quadIdx) != 0 {
		r.modelOpts.Blend = blend
		r.modelOpts.Images[0], r.modelOpts.Images[1] = src0, src1
		r.modelOpts.Images[2], r.modelOpts.Images[3] = src2, src3
		a.quadVerts = reserveModelVertices(a.quadVerts)
		dst.DrawTrianglesShader32(padModelVertices(a.quadVerts, 0, len(a.quadVerts)), a.quadIdx, shader, &r.modelOpts)
		r.modelStats.Draws++
	}
	r.resetModelQuads()
}

// rasterQuadDraw is one slot atlas stage draw: it covers every subject the stage
// applies to, so its count is a property of the frame's stages, not of the
// number of subjects.
func (r *Renderer) rasterQuadDraw(dst *ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend, src0, src1 *ebiten.Image) {
	if dst == nil || shader == nil || len(r.modelAtlas.quadIdx) == 0 {
		r.resetModelQuads()
		return
	}
	r.modelStats.RasterDraws++
	r.modelPass(dst)
	r.modelDraw(dst, shader, blend, src0, src1, nil, nil)
}

// rasterizeModelOverflow is the per-subject fallback for a frame whose subjects
// do not fit the atlas. It runs the same ordered stages over a single-subject
// page at commit time, so an oversized frame costs more passes but loses no
// subject.
func (r *Renderer) rasterizeModelOverflow(g *drawlist.ModelGeometry, set int) (modelSlot, bool) {
	a := &r.modelAtlas
	page := &a.overflow[set]
	page.reset(modelPageMaxHeight)
	a.overflowResolves = a.overflowResolves[:0]
	// The fallback route re-prepares the subject outside the admission pass, so
	// its faces recompute their own ring test.
	slot, ok := r.placeModelSubject(g, nil, nil, page, &a.overflowResolves)
	if !ok {
		return modelSlot{}, false
	}
	r.flushModelPage(page, a.overflowResolves)
	return slot, true
}
