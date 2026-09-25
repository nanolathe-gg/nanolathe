package gpurender

import (
	"image"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// Retained packed vertices (docs/DESIGN_GPU_RENDERER.md §22, the model lane):
// the executor's biggest CPU term was appendDirectFace re-deriving, for every
// cached-lane face of every presented frame, what the recorder had already
// proved unchanged — the winding test, the centroid, the texture slot, the
// parameter entry, the glint, the finish, the shade lanes and the n packed
// vertices. The recorder names an unchanged cached lane with a reusable
// ModelCacheKey (§13.12): an equal key means literally the same retained
// faces. So the lane keeps, per key, the packed vertex array and the packed
// parameter block of that lane, in a frame that does not know where on the
// atlas the subject lands:
//
//   - the vertices' DstX/DstY are relative to the subject's frame origin (the
//     atlas texel of the raster's local (0,0)), which is what the cold path
//     adds to every corner; every other lane depends on the raster, the
//     texture slots (fixed until ResetSources drops the store with the lane)
//     and the finish switches (a change clears the store);
//   - the parameter block is subject-local by construction (model_quads.go),
//     and a face's parameter index is kept relative to the block;
//   - the verdict entry and the battle light are per frame and are written
//     into the replayed vertices, the light per face from the retained
//     centroid and normal through the same arithmetic the cold path uses.
//
// The store is a bounded LRU keyed on the cache key. A key's first sighting
// primes a stub, its second captures the cold append, and every later one
// replays: a subject that rebuilds every frame (a turning turret, a walking
// kbot) therefore costs a stub and nothing else. The replay is byte-for-byte
// what the cold path would have produced — the integer-valued lanes are
// exact in binary32 and the light is the same function of the same operands
// — so the pixels are the cold path's (verified by the device fixture and the
// battle capture).
//
// What stays cold: a packet with a group delta or a water reflection this
// frame (the reflection batch is placement-bound), the solo cargo pass, the
// fallback, and a subject whose parameter block would not fit the image this
// frame. An outline or a live lane does not: both are appended cold AFTER the
// cached lane in retail's order [03 R-REN-03A §4], the capture closes before
// either is appended, and the replay puts the cached lane's vertices,
// parameter block and runs back exactly where the cold append would, so the
// per-frame lanes that follow land on the same batch either way. The one
// thing they touch is the doubled raster's slot box, whose even-rounded
// corner is the lane's frame origin: the entry keeps the box of the cached
// faces alone and the replay unions this frame's live and outline corners
// into it (slotFor), which is the box the cold path measures.
//
// The WARM path. A key that never returns — a turning unit, whose cached lane
// rebuilds every presented frame under retail's orientation threshold
// [03 §5.2] and so takes a new revision each time — pays the cold append on
// every sighting, and the store above can do nothing for it: its packed
// vertices are a function of the pose. But most of what the cold append
// derives per face is not. Reading how the recorder builds a face
// (internal/client, collectDrawPolysLaneProjected): the texture frame, the
// flat colour, the shade switch, the material and the corner count come from
// the model and its primitive, and the texel coordinates are the texture's own
// dimensions in corner order — none of them moves with the pose. What does:
// the corners and their keys, the SHD rows, the corner heights and the
// lighting normal, and everything the cold append derives from those (the
// winding test and its culling, the parameter entry, the centroid, the battle
// light, the glint and the finish response, the shade lanes). So a second
// index, keyed on (Body, Lane) across revisions, keeps the pose-independent
// products of the last cold append: per face, in the order the cold lane
// appends them, the run image the face binds and its texture-derived lanes
// (modelFaceTex). A key miss whose body hits appends WARM: the same core the
// cold lane runs (appendFaceCore), fed this frame's corners, keys, rows,
// heights and normals, with the texture resolution, the page/standalone run
// routing and the texel bounds answered by the body entry. The body entry is
// used only when the raster's face list has the same length and, face by
// face, the same corner count and the same texture frame as the recorded
// one — a different build state, damage variant or animated texture frame
// fails that in one pointer compare per face — and when the texture page the
// routing was decided against is still the page. Anything else is cold, and
// the cold append re-records the body.
const (
	// modelRetainCap bounds the store's entries, stubs included. A 1080p
	// battle frame has about four hundred subjects, each on up to four
	// half-pixel keys (§17.3).
	modelRetainCap = 2048
	// modelRetainBodyCap bounds the body index: one entry per retained
	// object's lane, whatever its revisions, so a frame's subjects and their
	// projected shadows fit with room for what scrolled off.
	modelRetainBodyCap = 1024
)

// modelRetainStore is the lane's retained-lane store: the map for lookup,
// an intrusive most-recently-used list for eviction, and a free list so a
// steady state of stubs allocates nothing. The map is only ever looked up by
// key, never ranged into output order [I1].
type modelRetainStore struct {
	entries    map[drawlist.ModelCacheKey]*modelRetained
	head, tail *modelRetained
	count      int
	free       *modelRetained
	// switches are the finish switches the entries were captured under.
	metalGlint, materials bool
	// capture is the entry the cold append is filling, or nil; quadBase and
	// vertBase are where its parameter block and vertices begin, and stats
	// the accounting before the append.
	capture  *modelRetained
	quadBase int
	vertBase int
	stats    ModelStats
	// The body index (the file comment's warm path): its own bounded LRU
	// keyed on (Body, Lane), and the entry the cold append is recording.
	bodies             map[modelBodyKey]*modelRetainedBody
	bodyHead, bodyTail *modelRetainedBody
	bodyCount          int
	bodyFree           *modelRetainedBody
	body               *modelRetainedBody
}

// modelBodyKey names one retained object's lane across its revisions.
type modelBodyKey struct {
	body uint64
	lane drawlist.ModelCacheLane
}

// modelBodyFace is one face of a body entry, in the order the cold lane
// appends it (the shared page's faces first, then each standalone
// texture's): which face of the raster it is, the proof it is still that
// face, the run image it binds and its texture-derived lanes.
type modelBodyFace struct {
	index   int32
	n       int32
	texture *formats.GAFFrame
	img     *ebiten.Image
	tex     modelFaceTex
}

// modelRetainedBody is one body entry: the raster the faces describe (the
// doubled lane or the native one), the texture page the run routing was
// decided against, and the faces.
type modelRetainedBody struct {
	key        modelBodyKey
	prev, next *modelRetainedBody
	doubled    bool
	page       *ebiten.Image
	faces      []modelBodyFace
	// ready is set once the cold append that records the entry has closed;
	// an entry abandoned mid-append never answers a warm lookup.
	ready bool
}

// matches reports whether b describes faces: the same raster, the same page
// for the run routing, and face by face the same corner count and texture
// frame — the topology the recorder authors from the model, independent of
// its pose.
func (b *modelRetainedBody) matches(faces []drawlist.ModelFace, doubled bool, page *ebiten.Image) bool {
	if !b.ready || b.doubled != doubled || b.page != page || len(b.faces) != len(faces) {
		return false
	}
	for i := range b.faces {
		bf := &b.faces[i]
		f := &faces[bf.index]
		if int32(len(f.Vertices)) != bf.n || f.Texture != bf.texture {
			return false
		}
	}
	return true
}

// modelRetainedSeg is one run of a retained lane's faces bound to one texture
// image: the faces on the shared page (and the flat ones) first, then each
// standalone texture's, as appendDirectLane orders them.
type modelRetainedSeg struct {
	img    *ebiten.Image
	f0, f1 int32
	verts  int32
}

// modelRetainedFace is what the replay needs per face beyond its vertices:
// the corner count, the parameter index within the block (zero for a face
// that interpolates linearly), and the battle light's centroid and normal.
type modelRetainedFace struct {
	n, quad   int32
	cx, cy, h float32
	normal    [3]float32
}

// modelRetained is one retained lane. Before capture it is a stub: the key,
// its list links and primed.
type modelRetained struct {
	key        drawlist.ModelCacheKey
	prev, next *modelRetained
	// primed is set on the first sighting; the second captures.
	primed   bool
	captured bool
	// The packet shape the capture saw, re-checked on every hit: an equal key
	// promises equal faces, and this is the cheap proof the promise held.
	width, height, originX, originY int32
	faces                           int
	hasSupersample, doubled         bool
	// slot is the doubled lane's box over its cached faces alone
	// (retainedSlotBounds), a pass over every retained corner the replay does
	// not repeat; slotFor adds the per-frame lanes.
	slot image.Rectangle
	segs []modelRetainedSeg
	// verts are the packed vertices in the frame-relative form described in
	// the file comment; recs are their faces in order.
	verts []ebiten.Vertex
	recs  []modelRetainedFace
	// params is the packed parameter block and quads its entry count.
	params []byte
	quads  int
	// culled and material are the accounting the cold append made.
	culled, material int
}

// matches reports whether g is the packet shape e was captured from.
func (e *modelRetained) matches(g *drawlist.ModelGeometry) bool {
	if e.width != g.Width || e.height != g.Height || e.originX != g.OriginX || e.originY != g.OriginY {
		return false
	}
	if e.hasSupersample != (g.Supersample != nil) {
		return false
	}
	raster := g
	if e.doubled {
		raster = g.Supersample
	}
	return e.faces == len(raster.Faces)
}

// retainedSlotBounds is modelSlotBounds over the packet's box and its cached
// faces alone: the part of the doubled raster's slot box an equal key keeps.
func retainedSlotBounds(g *drawlist.ModelGeometry) image.Rectangle {
	return unionFaceCorners(modelLocalBounds(g), g.Faces)
}

// unionFaceCorners grows b to hold every corner of faces, as modelSlotBounds
// counts them (the corner and the texel past it).
func unionFaceCorners(b image.Rectangle, faces []drawlist.ModelFace) image.Rectangle {
	x0, y0, x1, y1 := int32(b.Min.X), int32(b.Min.Y), int32(b.Max.X), int32(b.Max.Y)
	for i := range faces {
		for _, v := range faces[i].Vertices {
			x0, y0 = min(x0, v.X), min(y0, v.Y)
			x1, y1 = max(x1, v.X+1), max(y1, v.Y+1)
		}
	}
	return image.Rect(int(x0), int(y0), int(x1), int(y1))
}

// slotFor is the doubled raster's slot box this frame: the retained box of
// the cached faces plus the corners of the per-frame lanes ss carries, which
// is exactly modelSlotBounds(ss) for a packet of the captured shape.
func (e *modelRetained) slotFor(ss *drawlist.ModelGeometry) image.Rectangle {
	slot := e.slot
	if ss == nil {
		return slot
	}
	if len(ss.LiveFaces) != 0 {
		slot = unionFaceCorners(slot, ss.LiveFaces)
	}
	if len(ss.Outline) != 0 {
		slot = unionFaceCorners(slot, ss.Outline)
	}
	return slot
}

// retainedEntry is the store entry a packet's cached lane is replayed from or
// captured into, and the body entry its warm append may take, either nil for
// a packet that cannot use it this frame, the cold ones counted by reason
// (ModelStats). A key's first sighting inserts a stub. A reflecting packet
// gets no entry — the replay cannot rebuild the placement-bound reflection
// batch — but keeps its body: the warm append runs per face and reflects
// each one as the cold append does. The third result says the packet is one
// the body index serves at all, so a cold append records it.
func (r *Renderer) retainedEntry(g *drawlist.ModelGeometry, keyDelta int32, group *drawlist.ModelGeometry, reflecting bool) (e *modelRetained, body *modelRetainedBody, keyed bool) {
	d := &r.modelDirect
	s := &r.modelStats
	switch {
	case !g.Cache.Reusable():
		s.DirectColdNoKey++
		if len(g.Outline) != 0 {
			s.DirectColdNoKeyOutline++
		}
		if len(g.LiveFaces) != 0 || g.Supersample != nil && len(g.Supersample.LiveFaces) != 0 {
			s.DirectColdNoKeyLive++
		}
		return nil, nil, false
	case d.soloPass || keyDelta != 0 || group != nil:
		s.DirectColdGroup++
		return nil, nil, false
	}
	body = d.retain.lookupBody(g.Cache)
	if reflecting {
		return nil, body, true
	}
	e = d.retain.lookup(g.Cache)
	if e == nil {
		var evicted bool
		e, evicted = d.retain.insert(g.Cache)
		if evicted {
			s.DirectRetainEvicted++
		}
	}
	return e, body, true
}

// countRetainMiss counts why a packet with an entry is not replayed this
// frame: a captured entry the parameter image cannot take, a captured entry
// whose packet shape changed, or the stub of a first sighting. A packet
// appended warm is not cold and is counted by the warm append; a primed
// entry being captured is counted by the capture itself.
func (s *ModelStats) countRetainMiss(e *modelRetained, hit, fits, warm bool) {
	switch {
	case e == nil:
	case hit && !fits:
		s.DirectColdParams++
	case hit:
	case warm:
	case e.captured:
		s.DirectColdShape++
	case !e.primed:
		s.DirectColdPrimed++
	}
}

// setSwitches records the finish switches; a change drops every entry.
func (s *modelRetainStore) setSwitches(metalGlint, materials bool) {
	if s.metalGlint == metalGlint && s.materials == materials {
		return
	}
	s.metalGlint, s.materials = metalGlint, materials
	s.clear()
}

// clear drops every entry, keeping their storage on the free list.
func (s *modelRetainStore) clear() {
	for e := s.head; e != nil; {
		next := e.next
		s.release(e)
		e = next
	}
	s.head, s.tail, s.count = nil, nil, 0
	if s.entries == nil {
		s.entries = make(map[drawlist.ModelCacheKey]*modelRetained)
	} else {
		clear(s.entries)
	}
	for b := s.bodyHead; b != nil; {
		next := b.next
		s.releaseBody(b)
		b = next
	}
	s.bodyHead, s.bodyTail, s.bodyCount, s.body = nil, nil, 0, nil
	if s.bodies == nil {
		s.bodies = make(map[modelBodyKey]*modelRetainedBody)
	} else {
		clear(s.bodies)
	}
}

// lookupBody returns the body entry for key's lane, most recently used, or
// nil.
func (s *modelRetainStore) lookupBody(key drawlist.ModelCacheKey) *modelRetainedBody {
	b := s.bodies[modelBodyKey{key.Body, key.Lane}]
	if b == nil {
		return nil
	}
	if b != s.bodyHead {
		s.unlinkBody(b)
		s.pushFrontBody(b)
	}
	return b
}

// beginBody arms the store to record the cold cached-lane append that
// follows into key's body entry, inserting one (evicting the least recently
// used when the index is full) or emptying the one it has.
func (s *modelRetainStore) beginBody(key drawlist.ModelCacheKey, doubled bool) {
	b := s.lookupBody(key)
	if b == nil {
		if s.bodies == nil {
			s.bodies = make(map[modelBodyKey]*modelRetainedBody)
		}
		if s.bodyCount >= modelRetainBodyCap {
			old := s.bodyTail
			s.unlinkBody(old)
			delete(s.bodies, old.key)
			s.bodyCount--
			s.releaseBody(old)
		}
		b = s.bodyFree
		if b != nil {
			s.bodyFree = b.next
			b.next = nil
		} else {
			b = &modelRetainedBody{}
		}
		b.key = modelBodyKey{key.Body, key.Lane}
		s.bodies[b.key] = b
		s.pushFrontBody(b)
		s.bodyCount++
	}
	b.doubled, b.page, b.ready = doubled, nil, false
	b.faces = b.faces[:0]
	s.body = b
}

// endBody closes the recording; the entry answers warm lookups from now on.
func (s *modelRetainStore) endBody() {
	if b := s.body; b != nil {
		b.ready = true
	}
	s.body = nil
}

// releaseBody resets a body entry and puts its storage on the free list.
func (s *modelRetainStore) releaseBody(b *modelRetainedBody) {
	faces := b.faces[:0]
	*b = modelRetainedBody{faces: faces}
	b.next = s.bodyFree
	s.bodyFree = b
}

func (s *modelRetainStore) unlinkBody(b *modelRetainedBody) {
	if b.prev != nil {
		b.prev.next = b.next
	} else {
		s.bodyHead = b.next
	}
	if b.next != nil {
		b.next.prev = b.prev
	} else {
		s.bodyTail = b.prev
	}
	b.prev, b.next = nil, nil
}

func (s *modelRetainStore) pushFrontBody(b *modelRetainedBody) {
	b.prev, b.next = nil, s.bodyHead
	if s.bodyHead != nil {
		s.bodyHead.prev = b
	}
	s.bodyHead = b
	if s.bodyTail == nil {
		s.bodyTail = b
	}
}

// lookup returns the entry for key, most recently used, or nil.
func (s *modelRetainStore) lookup(key drawlist.ModelCacheKey) *modelRetained {
	e := s.entries[key]
	if e == nil {
		return nil
	}
	if e != s.head {
		s.unlink(e)
		s.pushFront(e)
	}
	return e
}

// insert adds a stub for key at the front, evicting the least recently used
// entry when the store is full, which the second result reports.
func (s *modelRetainStore) insert(key drawlist.ModelCacheKey) (*modelRetained, bool) {
	if s.entries == nil {
		s.entries = make(map[drawlist.ModelCacheKey]*modelRetained)
	}
	evicted := false
	if s.count >= modelRetainCap {
		old := s.tail
		s.unlink(old)
		delete(s.entries, old.key)
		s.count--
		s.release(old)
		evicted = true
	}
	e := s.free
	if e != nil {
		s.free = e.next
		e.next = nil
	} else {
		e = &modelRetained{}
	}
	e.key = key
	s.entries[key] = e
	s.pushFront(e)
	s.count++
	return e, evicted
}

// release resets an entry and puts its storage on the free list.
func (s *modelRetainStore) release(e *modelRetained) {
	segs, verts, recs, params := e.segs[:0], e.verts[:0], e.recs[:0], e.params[:0]
	*e = modelRetained{segs: segs, verts: verts, recs: recs, params: params}
	e.next = s.free
	s.free = e
}

func (s *modelRetainStore) unlink(e *modelRetained) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		s.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		s.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

func (s *modelRetainStore) pushFront(e *modelRetained) {
	e.prev, e.next = nil, s.head
	if s.head != nil {
		s.head.prev = e
	}
	s.head = e
	if s.tail == nil {
		s.tail = e
	}
}

// beginCapture arms the store to record the cold append that follows.
func (s *modelRetainStore) beginCapture(e *modelRetained, quadBase, vertBase int, stats ModelStats) {
	e.captured = false
	e.segs, e.recs = e.segs[:0], e.recs[:0]
	s.capture, s.quadBase, s.vertBase, s.stats = e, quadBase, vertBase, stats
}

// captureFace records one face the cold append just packed: its texture
// binding (a new segment when it changes), its corner count, its parameter
// index within the block, and its light centroid.
func (s *modelRetainStore) captureFace(f *drawlist.ModelFace, img *ebiten.Image, n, quad int) {
	e := s.capture
	if len(e.segs) == 0 || e.segs[len(e.segs)-1].img != img {
		e.segs = append(e.segs, modelRetainedSeg{img: img, f0: int32(len(e.recs))})
	}
	rec := modelRetainedFace{n: int32(n), normal: f.Normal}
	if quad != 0 {
		rec.quad = int32(quad - s.quadBase)
	}
	if f.Normal != ([3]float32{}) {
		rec.cx, rec.cy, rec.h = modelFaceCentre(f)
	}
	e.recs = append(e.recs, rec)
	seg := &e.segs[len(e.segs)-1]
	seg.f1 = int32(len(e.recs))
	seg.verts += int32(n)
}

// finishCapture copies the cold append's output into the entry in
// frame-relative form and marks it replayable. A capture the replay could not
// reproduce exactly — the parameter image filled up during it, or a segment
// larger than a run — is abandoned, and the key stays primed for another try.
func (s *modelRetainStore) finishCapture(d *modelDirectLane, g *drawlist.ModelGeometry, doubled bool, ox, oy float32, stats ModelStats) {
	e := s.capture
	s.capture = nil
	src := d.verts[s.vertBase:]
	// A face the image refused a quad is packed linearly, which a replay
	// with room would not reproduce; once no quad fits, one may have been
	// refused, so the capture is abandoned.
	if d.params.count+modelQuadSlots > modelDirectParamCap || d.noQuads || len(src) > schedRunVertexLimit/2 {
		return
	}
	e.verts = append(e.verts[:0], src...)
	vi := 0
	for i := range e.recs {
		rec := &e.recs[i]
		for k := vi; k < vi+int(rec.n); k++ {
			v := &e.verts[k]
			v.DstX -= ox
			v.DstY -= oy
			if rec.quad != 0 {
				v.ColorB = float32(rec.quad)
			}
			v.Custom2, v.Custom3 = 0, 0
		}
		vi += int(rec.n)
	}
	if vi != len(e.verts) {
		return
	}
	e.quads = d.params.count - s.quadBase
	e.params = append(e.params[:0], d.params.buf[s.quadBase*modelQuadBytes:d.params.count*modelQuadBytes]...)
	e.culled = stats.DirectCulled - s.stats.DirectCulled
	e.material = stats.MaterialFaces - s.stats.MaterialFaces
	e.faces = len(g.Faces)
	e.width, e.height, e.originX, e.originY = g.Width, g.Height, g.OriginX, g.OriginY
	e.hasSupersample, e.doubled = g.Supersample != nil, doubled
	if doubled {
		e.faces = len(g.Supersample.Faces)
		e.slot = retainedSlotBounds(g.Supersample)
	}
	e.captured = true
}

// replayRetained appends a retained lane at this frame's placement: the
// parameter block, then each segment's faces into the run bound to its image,
// each vertex shifted by the frame origin and given the frame's entry and its
// face's battle light. The caller has checked that the block fits. g is the
// packet being replayed, read only for the accounting of the per-frame lanes
// the caller appends after the lane.
func (r *Renderer) replayRetained(e *modelRetained, g *drawlist.ModelGeometry, ox, oy float32, entry int, shadow bool) {
	d := &r.modelDirect
	if len(g.LiveFaces) != 0 || g.Supersample != nil && len(g.Supersample.LiveFaces) != 0 {
		r.modelStats.DirectRetainedLive++
	}
	if len(g.Outline) != 0 {
		r.modelStats.DirectRetainedOutline++
	}
	first := 0
	if e.quads > 0 {
		first = d.params.appendBlock(e.params, e.quads)
	}
	lit := !shadow && d.lightSources.count > 0
	custom2 := float32(entry)
	vi := 0
	for si := range e.segs {
		seg := &e.segs[si]
		run := d.colourRun([2]*ebiten.Image{seg.img, r.tables.atlas}, int(seg.verts))
		for fi := seg.f0; fi < seg.f1; fi++ {
			rec := &e.recs[fi]
			n := int(rec.n)
			base := uint32(len(d.verts)) - uint32(run.vOff)
			d.verts = append(d.verts, e.verts[vi:vi+n]...)
			dst := d.verts[len(d.verts)-n:]
			lighting := float32(0)
			if lit && rec.normal != ([3]float32{}) {
				lighting = d.lightAt(rec.cx, rec.cy, rec.h, rec.normal)
				if lighting > 0 {
					r.modelStats.LitModelFaces++
				}
			}
			quad := float32(0)
			if rec.quad != 0 {
				quad = float32(first - 1 + int(rec.quad))
			}
			for k := range dst {
				v := &dst[k]
				v.DstX += ox
				v.DstY += oy
				if rec.quad != 0 {
					v.ColorB = quad
				}
				v.Custom2, v.Custom3 = custom2, lighting
			}
			for i := 1; i+1 < n; i++ {
				d.idx = append(d.idx, base, base+uint32(i), base+uint32(i+1))
			}
			run.vLen += int32(n)
			run.iLen += int32(3 * (n - 2))
			vi += n
		}
	}
	r.modelStats.DirectFaces += len(e.recs)
	r.modelStats.DirectCulled += e.culled
	r.modelStats.MaterialFaces += e.material
	r.modelStats.DirectRetained++
}
