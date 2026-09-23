package gpurender

import (
	"cmp"
	"fmt"
	"image"
	"slices"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// The model lane (docs/DESIGN_GPU_RENDERER.md §22): the modern executor's one
// model path.
//
// Its predecessor, the slot-atlas model stage, reproduced retail's per-unit
// composition image exactly through a key pass, a body pass, reveal, outline,
// clipping, a coverage resolve and a residency table, and was the executor's
// largest CPU term. The lane keeps what retail's picture needs and drops the
// rest:
//
//   - Every subject of the frame, and its shadow, gets a region of a per-frame
//     2× atlas page, packed by shelf, no residency; an attached-unit group
//     composes in one region (construction children finish separately before
//     merging, model_group.go), and a carried child that casts a shadow composes
//     a second time in a region of its own, because a shadow is cut from its
//     subject's own finished image [03 R-REN-03D §1]. Faces are
//     fan-triangulated straight from the packet's projected corners: the
//     recorder's doubled lane when the packet carries one (half-pixel offset
//     included, §17.3), the native corners doubled here otherwise.
//
//   - A key pass writes each face's height key into a KEY plane under a max
//     blend, so a texel holds the highest key drawn there. The colour pass
//     draws a face's texel only where its own key is not below the stored one:
//     retail's `stored ≤ incoming` admission [03 R-REN-03A §2], faces in
//     recorded order so a tie goes to the later-drawn face as retail's does.
//     Both passes draw one vertex batch, and a four-corner face's key is the
//     span writer's two-chain mapping in both, so the passes never disagree
//     about a texel's key.
//
//   - The nanoframe reveal, the outline endpoints, the waterline tint and the
//     Digger erase are per-texel verdicts on the height key
//     [03 §5.2][03 R-COMP-01 §3][03 R-WATER-01 §2][03 R-REN-03A §8], so the
//     colour pass evaluates them at the fragment from the face's own key,
//     which is the stored key wherever the face wins. A carried child's own
//     verdicts read its own key, before the group delta; the carrier's
//     waterline and Digger clip then read the shifted key, as the staging
//     image's passes do [03 R-REN-03A §4].
//
//   - Textures sample through the same two-chain span mapping the slot stage
//     used for textured quads (§11.2 "Textured quads without strips"); the SHD
//     lookup becomes the scale the table was generated from (§13.2).
//
//   - The body commit into the composite is one quad per subject through the
//     scheduler, in record order, whose fragment box-resolves the four 2×
//     texels under a pixel: §17's coverage resolve without its passes, for
//     models only. The shadow commit resolves the silhouette the same way and
//     punches the body's wholly covered blocks, as the slot stage's did.
//
//   - A subject whose cached lane the recorder proves unchanged (a reusable
//     ModelCacheKey) keeps its PACKED vertices and parameter entries across
//     frames (model_retain.go): the parameter corners are subject-local, so
//     the retained arrays depend only on the raster, the texture slots and
//     the finish switches, and a later frame rewrites just the atlas
//     placement, the verdict entry and the per-face battle light. A subject
//     whose lane did change (a turning unit: a new revision of the same body)
//     appends WARM: every face is packed from this frame's corners through
//     the cold path's own core, with what its texture alone decides — the
//     slot, the run it binds, its texel bounds — answered by the body entry
//     the last cold append recorded.
//
// A frame whose subjects overflow every page draws the rest straight onto the
// composite at native scale in painter order (drawModelDirectFallback): no key,
// no supersample, never a dropped subject.
const (
	// The atlas pages: 4096 × 4096 texels each, two planes a page, allocated
	// on demand and retained. A 1080p battle frame packs its four hundred
	// subjects and their shadows into about 1,300 rows of the first page
	// tallest-first; the 2× detail view reaches about 4,000, so a second page
	// opens when the first is full rather than sending a large detail-view
	// battle to the fallback. A page is 128 MiB of device memory.
	modelDirectAtlasW   = 4096
	modelDirectAtlasH   = 4096
	modelDirectMaxPages = 2
	// modelDirectMargin is the atlas margin around a region in 2× texels. The
	// body's biased corners stay inside the subject's box; the margin keeps a
	// neighbour's texels out of the commit's two-by-two resolve.
	modelDirectMargin = 2
	// modelDirectParamMaxRows bounds the lane's parameter image: 1024 texels a
	// row, twelve per entry, holding every mapped four-corner face and the
	// per-subject verdict entries. The image grows to what a frame uses; a
	// frame past the bound draws its remaining faces linearly.
	modelDirectParamMaxRows = 2048
	modelDirectParamCap     = modelDirectParamMaxRows * modelQuadParamWidth / modelQuadTexels
	// modelDirectFatten is how far the water reflection pushes a face corner
	// from its centroid, in atlas texels (water_reflections.go). The body
	// raster no longer fattens: see modelDirectSampleBias.
	modelDirectFatten = 1.0
	// modelDirectSampleBias moves every body corner half a texel right and
	// down on the raster it is drawn into. The span writer covers the texel
	// whose top-left corner (col, row) lies inside the ring — left and top
	// inclusive, right and bottom exclusive, so faces that share an edge
	// neither overlap nor leave a gap [03 R-RAST-01 §1] — while the device
	// samples texel centres. The bias makes the device's centre test the span
	// writer's corner test, so a face covers exactly the texels whose two-chain
	// mapping (model_shaders.go) has a span. Pushing the far corners a whole
	// texel instead, as the lane once did, covered one texel row and column
	// past the ring, where the mapping has no span and falls back to the top
	// corner's lanes: a flat line of one texel under and right of every
	// model's silhouette.
	modelDirectSampleBias = 0.5
)

// Face modes in Custom0 of the colour pass and the fallback. A mapped mode
// takes its key (and shade, and for a textured face its texel) from the
// parameter image's two-chain mapping of the face's four corners; the others
// interpolate their lanes linearly. An outline endpoint is one native pixel
// and is key-tested once, against the pixel's nearest-sampled key, as
// retail's 1× outline pass is [03 R-COMP-01 §3]. modelDirectLive is added to
// a face drawn after the reveal — outline endpoints and the live lane — which
// the reveal therefore never rewrites.
const (
	modelDirectFlat          = 0
	modelDirectTextured      = 1
	modelDirectQuad          = 2
	modelDirectQuadShade     = 3
	modelDirectShadow        = 4
	modelDirectFlatQuad      = 5
	modelDirectFlatQuadShade = 6
	modelDirectOutline       = 7
	modelDirectLive          = 8
)

// modelDirectRegion is one subject's place on the atlas this frame: the page
// and the 2× texel of its world-bounds origin.
type modelDirectRegion struct {
	x, y int32
	page int32
	// bounds is the world rectangle the region covers: the subject's, or a
	// group's union.
	bounds image.Rectangle
	ok     bool
}

// modelDirectRun is one colour-pass draw: the page it draws into, the
// texture and table bindings, and its span. The page's key plane and the
// parameter image are bound when the run is drawn.
type modelDirectRun struct {
	imgs             [2]*ebiten.Image
	page             int32
	vOff, vLen, iOff int32
	iLen             int32
}

// modelDirectPage is one atlas page: its two planes and the rows this frame's
// regions reached on it.
type modelDirectPage struct {
	key, colour *ebiten.Image
	usedRows    int32
}

// modelDirectLane is the renderer's lane state: the pages, the per-frame batch
// and the scratch, all retained across frames.
type modelDirectLane struct {
	pages                 [modelDirectMaxPages]modelDirectPage
	page                  int32
	packX, packY, packRow int32
	regions               map[*drawlist.ModelGeometry]modelDirectRegion
	// solo holds the region of a carried child composed a second time on its
	// own, so its shadow is cut from the child's own finished image rather than
	// from the group's texels. An entry is present for every carried child that
	// casts a shadow, invalid when the atlas could not hold it.
	solo map[*drawlist.ModelGeometry]modelDirectRegion
	// soloPass is set while that second composition is appended: the commit
	// reads that image for coverage and keys alone, so its faces take the
	// coverage-only path and skip everything a colour needs.
	soloPass bool
	// soloOutline is the outline walk the group composition made of the packet
	// the solo pass is about to repeat, so the rows are walked once. Valid only
	// between those two appends of soloOutlineFor.
	soloOutline    []modelGPUFace
	soloOutlineFor *drawlist.ModelGeometry
	// soloSlot is the same reuse for the doubled lane's box (slotBoundsFor).
	soloSlot    image.Rectangle
	soloSlotFor *drawlist.ModelGeometry

	keyShader, colourShader *ebiten.Shader
	groups                  modelGroupMergeLane
	groupReflection         modelDirectRegion
	shaderErr               error

	// params is the lane's parameter image: mapped faces and subject
	// verdicts, twelve texels an entry, one-based (model_quads.go).
	params modelQuadParams

	runs  []modelDirectRun
	verts []ebiten.Vertex
	idx   []uint32

	order []modelDirectFace
	// keyDelta is the group child delta added to the keys being appended.
	keyDelta                                int32
	lightSources                            subjectLights
	lightX, lightY, lightScale, lightHeight float32

	// pending collects the frame's packets before placement, so they can be
	// packed tallest first; record order is the commits' business.
	pending    []modelDirectPending
	standalone []int
	doubled    [4]drawlist.ModelVertex
	opts       ebiten.DrawTrianglesShaderOptions

	// retain holds the packed vertices of subjects whose cached lane the
	// recorder proved unchanged (model_retain.go).
	retain modelRetainStore
	// noQuads is set for a subject whose frame origin the verdict entry cannot
	// carry, so its four-corner faces interpolate linearly rather than map
	// through a frame the fragment cannot recover.
	noQuads bool
	// fallbackOpacity is the fallback batch's body opacity: one, or the ALP
	// half-colour's half for a cloaked subject [03 R-COMP-01 §2].
	fallbackOpacity float32
}

// modelDirectFace is one face's place in the fallback's painter order.
type modelDirectFace struct {
	// key is the face's mean height key in 1/256 units, with a carried child's
	// delta applied: the sort key over the whole group.
	key int64
	// index names the face: non-negative in Faces, ^index in LiveFaces.
	index int32
	// child names the packet the face belongs to: -1 the carrier, else its
	// index in Children.
	child int32
}

// initModelDirect compiles the lane's two atlas passes.
func (r *Renderer) initModelDirect() error {
	d := &r.modelDirect
	d.keyShader, d.shaderErr = ebiten.NewShader([]byte(modelDirectKeyShaderSource()))
	if d.shaderErr == nil {
		d.colourShader, d.shaderErr = ebiten.NewShader([]byte(modelDirectColourShaderSource()))
	}
	if d.shaderErr == nil {
		d.groups.shader, d.shaderErr = ebiten.NewShader([]byte(modelGroupMergeShaderSource))
	}
	return d.shaderErr
}

// modelDirectEligible reports whether the lane draws g: the passes compiled,
// a palette installed and the packet eligible.
func (r *Renderer) modelDirectEligible(g *drawlist.ModelGeometry) bool {
	d := &r.modelDirect
	return d.keyShader != nil && d.colourShader != nil && r.scene2D != nil &&
		r.tables.atlas != nil && g != nil && g.Eligible
}

// ensurePage allocates one page's planes once.
func (d *modelDirectLane) ensurePage(i int32) {
	pg := &d.pages[i]
	if pg.key != nil {
		return
	}
	opts := &ebiten.NewImageOptions{Unmanaged: true}
	pg.key = ebiten.NewImageWithOptions(image.Rect(0, 0, modelDirectAtlasW, modelDirectAtlasH), opts)
	pg.colour = ebiten.NewImageWithOptions(image.Rect(0, 0, modelDirectAtlasW, modelDirectAtlasH), opts)
}

// resetFrame starts a frame: the packer, the regions and the batch.
func (d *modelDirectLane) resetFrame() {
	d.page, d.packX, d.packY, d.packRow = 0, 0, 0, 0
	for i := range d.pages {
		d.pages[i].usedRows = 0
	}
	if d.regions == nil {
		d.regions = make(map[*drawlist.ModelGeometry]modelDirectRegion)
	} else {
		clear(d.regions)
	}
	if d.solo == nil {
		d.solo = make(map[*drawlist.ModelGeometry]modelDirectRegion)
	} else {
		clear(d.solo)
	}
	d.soloPass = false
	d.groupReflection = modelDirectRegion{}
	d.groups.merges = d.groups.merges[:0]
	// The per-packet reuse holds slices and boxes of this frame's packets only.
	d.soloOutline, d.soloOutlineFor, d.soloSlotFor = nil, nil, nil
	d.runs, d.verts, d.idx = d.runs[:0], d.verts[:0], d.idx[:0]
	d.params.reset()
}

// alloc places one region of w × h texels on the shelf packer, at an even
// origin so a native pixel's 2×2 block never straddles two regions. A region
// the current page cannot hold opens the next page; past the last page it is
// refused.
func (d *modelDirectLane) alloc(w, h int32) (x, y, page int32, ok bool) {
	w, h = (w+1)&^1, (h+1)&^1
	if w > modelDirectAtlasW || h > modelDirectAtlasH {
		return 0, 0, 0, false
	}
	for {
		if d.packX+w > modelDirectAtlasW {
			d.packX = 0
			d.packY += d.packRow
			d.packRow = 0
		}
		if d.packY+h <= modelDirectAtlasH {
			break
		}
		if d.page+1 >= modelDirectMaxPages {
			return 0, 0, 0, false
		}
		d.page++
		d.packX, d.packY, d.packRow = 0, 0, 0
	}
	x, y = d.packX, d.packY
	d.packX += w
	if h > d.packRow {
		d.packRow = h
	}
	if pg := &d.pages[d.page]; y+h > pg.usedRows {
		pg.usedRows = y + h
	}
	return x, y, d.page, true
}

// addPacked appends one packed parameter entry and returns its one-based
// index, or zero when the image is full.
func (q *modelQuadParams) addPacked(packed *[modelQuadBytes]byte) int {
	if q.count >= modelDirectParamCap {
		return 0
	}
	need := (q.count + 1) * modelQuadBytes
	if cap(q.buf) < need {
		q.buf = slices.Grow(q.buf, need-len(q.buf))
	}
	q.buf = q.buf[:need]
	copy(q.buf[q.count*modelQuadBytes:], packed[:])
	q.count++
	return q.count
}

// subjectVerdicts describes a subject's reveal and clipping to the parameter
// image and returns the entry index, or zero for a subject with none. reveal
// is the raster's reveal; g carries the subject's waterline and Digger
// [03 R-REN-03A §8]; delta is a carried child's key delta, so the fragment can
// recover the child's own key for its own verdicts; group is the carrier
// whose waterline and Digger clip the whole staging image, children included
// [03 R-REN-03A §4], or nil.
//
// The entry also carries the subject's FRAME: the atlas texel of the raster's
// local (0,0), (ox, oy), which is what a mapped face's fragment subtracts from
// its own position before evaluating the subject-local parameters
// (model_quads.go). Every non-shadow subject therefore has an entry; one that
// carries no verdict at all is returned NEGATED, so the fragment can find the
// frame through its magnitude and skip the verdicts on its sign.
//
// The entry's lanes, two 16-bit values a texel: (line, floor), (below+2,
// band+2), (above+2, waterline mode), (waterline key, digger), (digger key,
// has reveal), (delta + 32768, group clip: 0 none, else waterline mode + 1),
// (group waterline key, group digger), (group digger key, 0), then the frame
// (ox + 32768, oy + 32768).
func (d *modelDirectLane) subjectVerdicts(reveal *drawlist.ModelReveal, g *drawlist.ModelGeometry, delta int32, group *drawlist.ModelGeometry, ox, oy float32) int {
	groupClip := group != nil && (group.Waterline != drawlist.ModelWaterlineNone || group.Digger)
	verdicts := reveal != nil || g.Waterline != drawlist.ModelWaterlineNone || g.Digger || groupClip
	var packed [modelQuadBytes]byte
	hasReveal := 0
	if v := reveal; v != nil {
		hasReveal = 1
		putQuadLane(packed[0:], int(v.Line), int(v.Floor))
		putQuadLane(packed[4:], int(v.Below)+2, int(v.Band)+2)
		putQuadLane(packed[8:], int(v.Above)+2, int(g.Waterline))
	} else {
		putQuadLane(packed[8:], 0, int(g.Waterline))
	}
	digger := 0
	if g.Digger {
		digger = 1
	}
	putQuadLane(packed[12:], int(g.WaterlineKey), digger)
	putQuadLane(packed[16:], int(g.DiggerKey), hasReveal)
	if delta < -modelQuadKeyBias || delta >= modelQuadKeyBias {
		// A delta the lane cannot carry: no stock child is anywhere near it,
		// and the child's own verdicts then read the shifted key rather than
		// losing the subject.
		delta = 0
	}
	if groupClip {
		groupDigger := 0
		if group.Digger {
			groupDigger = 1
		}
		putQuadLane(packed[20:], int(delta)+modelQuadKeyBias, int(group.Waterline)+1)
		putQuadLane(packed[24:], int(group.WaterlineKey), groupDigger)
		putQuadLane(packed[28:], int(group.DiggerKey), 0)
	} else {
		putQuadLane(packed[20:], int(delta)+modelQuadKeyBias, 0)
	}
	fx, fy := int(ox)+modelQuadKeyBias, int(oy)+modelQuadKeyBias
	d.noQuads = !fitsQuadLane(fx) || !fitsQuadLane(fy)
	if !d.noQuads {
		putQuadLane(packed[32:], fx, fy)
	}
	entry := d.params.addPacked(&packed)
	if entry == 0 {
		// The image is full: no face of this subject maps either, because the
		// per-face admission tests the same capacity.
		d.noQuads = true
	}
	if !verdicts {
		return -entry
	}
	return entry
}

// prepareModelDirect runs before Replay: it places every eligible subject and
// shadow on the atlas, builds the batch, and draws both planes of every page
// used, so Replay only compiles the commit quads (§22).
func (r *Renderer) prepareModelDirect(l *drawlist.List) {
	d := &r.modelDirect
	d.resetFrame()
	// The retained lanes bake the finish switches into their colour lane, so
	// a change drops every entry rather than drawing last frame's finish.
	d.retain.setSwitches(r.metalGlint, r.materialsEnabled)
	if r.tables.atlas == nil {
		return
	}
	// Subjects are packed tallest first, which is what keeps a shelf packer's
	// waste down; the commits keep record order regardless. A carried child's
	// shadow-only command places the shadow alone; the body's region is set
	// when the carrier is placed.
	d.pending = d.pending[:0]
	l.VisitModels(func(m drawlist.Model) {
		g := m.Geometry
		if !r.modelDirectEligible(g) {
			return
		}
		if m.ShadowOnly {
			if g.Shadow != nil && !g.Shadow.Silhouette {
				d.pending = append(d.pending, modelDirectPending{g: g.Shadow, shadow: true, height: modelWorldBounds(g.Shadow).Dy()})
			}
			return
		}
		bounds := modelWorldBounds(g)
		if len(g.Children) != 0 {
			bounds = modelGroupBounds(g)
		}
		d.pending = append(d.pending, modelDirectPending{g: g, height: bounds.Dy()})
	})
	slices.SortStableFunc(d.pending, func(a, b modelDirectPending) int { return cmp.Compare(b.height, a.height) })
	for _, p := range d.pending {
		if _, seen := d.regions[p.g]; seen {
			continue
		}
		if p.shadow {
			r.placeModelDirectShadow(p.g)
		} else {
			r.placeModelDirect(p.g)
		}
	}
	rows, pages := 0, 0
	for i := range d.pages {
		if d.pages[i].usedRows > 0 {
			rows += int(d.pages[i].usedRows)
			pages++
		}
	}
	r.modelStats.DirectAtlasRows, r.modelStats.DirectPages = rows, pages
	if rows == 0 {
		return
	}
	d.params.upload()
	fill := r.placeholderImage()
	params := d.params.img
	if params == nil {
		params = fill
	}
	for p := range d.pages {
		pg := &d.pages[p]
		if pg.usedRows == 0 {
			continue
		}
		d.ensurePage(int32(p))
		used := image.Rect(0, 0, modelDirectAtlasW, int(pg.usedRows))
		// The key plane: every face's key under a max blend, so a texel holds
		// the highest key drawn there. The runs are the colour batch's own —
		// the key shader reads positions, the key lane and, for a mapped face,
		// the parameter image — so the vertices are built once. Shadow faces
		// write keys nobody reads; their regions are their own.
		r.beginPass(pg.key)
		pg.key.SubImage(used).(*ebiten.Image).Clear()
		d.opts.Images = [4]*ebiten.Image{fill, fill, fill, params}
		d.opts.Blend = ebiten.Blend{
			BlendFactorSourceRGB: ebiten.BlendFactorOne, BlendFactorSourceAlpha: ebiten.BlendFactorOne,
			BlendFactorDestinationRGB: ebiten.BlendFactorOne, BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
			BlendOperationRGB: ebiten.BlendOperationMax, BlendOperationAlpha: ebiten.BlendOperationMax,
		}
		for i := range d.runs {
			run := &d.runs[i]
			if run.iLen == 0 || int(run.page) != p {
				continue
			}
			r.recordSubmission(int(run.vLen), int(run.iLen))
			pg.key.DrawTrianglesShader32(deviceVertexSpan(d.verts, int(run.vOff), int(run.vLen)), d.idx[run.iOff:run.iOff+run.iLen], d.keyShader, &d.opts)
			r.frameDraws++
		}
		// The colour plane: faces that pass the key test, at their texel, with
		// the reveal and clipping verdicts applied; shadow silhouettes in their
		// index.
		r.beginPass(pg.colour)
		pg.colour.SubImage(used).(*ebiten.Image).Clear()
		d.opts.Blend = ebiten.BlendSourceOver
		for i := range d.runs {
			run := &d.runs[i]
			if run.iLen == 0 || int(run.page) != p {
				continue
			}
			d.opts.Images = [4]*ebiten.Image{run.imgs[0], run.imgs[1], pg.key, params}
			for j := range 2 {
				if d.opts.Images[j] == nil {
					d.opts.Images[j] = fill
				}
			}
			r.recordSubmission(int(run.vLen), int(run.iLen))
			pg.colour.DrawTrianglesShader32(deviceVertexSpan(d.verts, int(run.vOff), int(run.vLen)), d.idx[run.iOff:run.iOff+run.iLen], d.colourShader, &d.opts)
			r.frameDraws++
		}
		r.modelStats.DirectPasses += 2
	}
	r.mergeModelGroups()
}

// assignGroupRegions places one subject's regions and nothing else, so the
// packer's assignment is decided in one place and is testable without a device:
//
//   - the group region, over the union of the carrier's bounds and its
//     mergeable children's, which the whole group composes into;
//   - each mergeable child's entry, a sub-rectangle of that group region —
//     the child's texels within it, the carrier's beside and beneath them;
//     construction groups instead allocate each child independently and merge
//     only finished visible pixels afterwards (model_group.go);
//   - a region of its own for each carried child that casts a shadow, because
//     a shadow is cut from its subject's FINISHED image [03 R-REN-03D §1] and
//     the group region's texels at the child's box are the carrier's as much as
//     the child's. Carried units are rare, so the extra image is bounded.
//
// The returned region is invalid when the atlas could not hold the group; a
// child's solo region is invalid when it could not hold that.
func (d *modelDirectLane) assignGroupRegions(g *drawlist.ModelGeometry) modelDirectRegion {
	bounds := modelWorldBounds(g)
	if len(g.Children) != 0 {
		bounds = modelGroupBounds(g)
	}
	region, _ := d.allocRegion(bounds)
	d.regions[g] = region
	if !region.ok {
		return region
	}
	separate := modelGroupNeedsMerge(g)
	for _, child := range g.Children {
		cg := child.Geometry
		if !mergeableChild(cg) {
			continue
		}
		cb := modelWorldBounds(cg)
		d.regions[cg] = modelDirectRegion{
			x: region.x + 2*int32(cb.Min.X-bounds.Min.X), y: region.y + 2*int32(cb.Min.Y-bounds.Min.Y),
			page: region.page, bounds: cb, ok: true,
		}
		if separate {
			childRegion, ok := d.allocRegion(cb)
			if !ok {
				// Keep the existing whole-group overflow fallback; never submit
				// an incomplete staged factory as a successful body.
				d.regions[g] = modelDirectRegion{}
				for _, pending := range g.Children {
					if mergeableChild(pending.Geometry) {
						d.regions[pending.Geometry] = modelDirectRegion{}
						if pending.Geometry.Shadow != nil {
							d.solo[pending.Geometry] = modelDirectRegion{}
						}
					}
				}
				return modelDirectRegion{}
			}
			d.regions[cg] = childRegion
		}
		if cg.Shadow == nil {
			continue
		}
		// The solo image is the same 2× region every subject gets: both shadow
		// commits resolve the four texels under a pixel and count their share
		// (destOpModelSilhouetteShadow, destOpModelDirectShadow), so a 1× image
		// would be read as though it were doubled.
		solo, _ := d.allocRegion(cb)
		d.solo[cg] = solo
	}
	return region
}

// shadowSource is the region a subject's shadow is cut from: the solo region of
// a carried child that casts one, else the subject's own. The second result is
// false when there is no usable image — a subject the atlas could not hold, or
// a carried child whose solo region it could not hold — and the shadow is then
// omitted rather than cut from texels that are not the subject's.
func (d *modelDirectLane) shadowSource(g *drawlist.ModelGeometry) (modelDirectRegion, bool) {
	if solo, carried := d.solo[g]; carried {
		return solo, solo.ok
	}
	region, placed := d.regions[g]
	return region, placed && region.ok
}

// placeModelDirect allocates one subject's regions — its own, its group's and
// its shadow's — and appends their faces to the batch. An attached-unit group
// composes in one region over the union of its bounds: the carrier's faces,
// then each mergeable child's with its signed height delta added to the keys,
// saturating at the byte's range where retail would wrap [03 R-REN-03A §4]. A
// carried child that casts a shadow composes a second time in a region of its
// own, with its own keys and its own verdicts and no carrier clip — the image
// the classic composer's child pass produces, and the one its shadow is cut
// from [03 R-REN-03A §4][03 R-REN-03D §1]. A subject the atlas cannot hold is
// recorded with an invalid region and takes the fallback at commit time; its
// shadow is then omitted, as the slot stage omitted a shadow it could not
// place. Construction groups finish each child in isolation before merging
// its visible pixels (model_group.go).
func (r *Renderer) placeModelDirect(g *drawlist.ModelGeometry) {
	d := &r.modelDirect
	region := d.assignGroupRegions(g)
	if !region.ok {
		r.modelStats.DirectOverflow++
		return
	}
	r.modelStats.DirectSubjects++
	r.appendPacket(g, region, false, 0, nil)
	separate := modelGroupNeedsMerge(g)
	mergeStart := len(d.groups.merges)
	needsKey := g.ReflectWater || (g.Shadow != nil && g.Shadow.Silhouette)
	for _, child := range g.Children {
		cg := child.Geometry
		if !mergeableChild(cg) {
			r.modelStats.Skipped++
			continue
		}
		r.modelStats.DirectSubjects++
		childRegion := region
		if separate {
			childRegion = d.regions[cg]
			d.groups.merges = append(d.groups.merges, modelGroupMerge{parent: region, child: childRegion, key: true})
			d.groupReflection = region
		}
		needsKey = needsKey || cg.ReflectWater
		r.appendPacket(cg, childRegion, false, child.KeyDelta, g)
		d.groupReflection = modelDirectRegion{}
		solo, carried := d.solo[cg]
		if !carried {
			continue
		}
		if !solo.ok {
			r.modelStats.DirectOverflow++
			continue
		}
		r.modelStats.DirectCargoImages++
		d.soloPass = true
		r.appendPacket(cg, solo, false, 0, nil)
		d.soloPass = false
	}
	if len(d.groups.merges) > mergeStart && !needsKey {
		// A final child's key has no remaining reader: body commits and
		// projected shadows use colour only. Keep it for silhouettes and
		// water reflections, and for every earlier child's admission.
		d.groups.merges[len(d.groups.merges)-1].key = false
	}
	if g.Shadow != nil && !g.Shadow.Silhouette {
		r.placeModelDirectShadow(g.Shadow)
	}
}

// placeModelDirectShadow places one shadow packet in a region of its own.
func (r *Renderer) placeModelDirectShadow(sg *drawlist.ModelGeometry) {
	d := &r.modelDirect
	if _, seen := d.regions[sg]; seen {
		return
	}
	region, ok := d.allocRegion(modelWorldBounds(sg))
	d.regions[sg] = region
	if !ok {
		r.modelStats.DirectOverflow++
		return
	}
	r.modelStats.DirectShadows++
	r.appendPacket(sg, region, true, 0, nil)
}

// allocRegion places a region for a world rectangle: twice its size plus the
// margin that keeps neighbouring regions apart.
func (d *modelDirectLane) allocRegion(bounds image.Rectangle) (modelDirectRegion, bool) {
	if bounds.Empty() {
		return modelDirectRegion{}, false
	}
	w := int32(bounds.Dx())*2 + 2*modelDirectMargin
	h := int32(bounds.Dy())*2 + 2*modelDirectMargin
	rx, ry, page, ok := d.alloc(w, h)
	if !ok {
		return modelDirectRegion{}, false
	}
	return modelDirectRegion{x: rx + modelDirectMargin, y: ry + modelDirectMargin, page: page, bounds: bounds, ok: true}, true
}

// appendPacket appends one packet's lanes into a region, in retail's order:
// the cached faces, the outline endpoints, then the live faces
// [03 R-REN-03A §4]. keyDelta is added to every key and group is the carrier
// whose clip applies (a group child), else nil.
//
// A packet the recorder marked reusable (§13.12) and that composes alone —
// no group delta, no solo pass, no water reflection — takes its cached lane
// from the retained store when the store holds it (model_retain.go): the
// packed vertices are replayed at this frame's placement, and only the
// verdict entry and the battle light are made anew. The outline and the live
// lane are per-frame and are appended cold after the cached lane whether it
// was replayed or not, exactly as they follow a cold cached lane. The first
// sighting of a key primes a stub, the second captures the cold append's
// output, and every later one replays it, so a subject that rebuilds every
// frame never pays for a copy nobody reads. A key miss whose body the index
// holds — the same object under a new revision — appends the lane warm
// (appendWarmLane), captured all the same when the key is primed; a cold
// append records the body for the next revision.
func (r *Renderer) appendPacket(g *drawlist.ModelGeometry, region modelDirectRegion, shadow bool, keyDelta int32, group *drawlist.ModelGeometry) {
	d := &r.modelDirect
	// Atlas texel of the raster's local (0,0) and the scale the corners take.
	// A doubled lane's local box, rounded down to an even corner, lands on the
	// packet's own world-bounds origin so its two-by-two blocks line up with
	// the native pixels the commit resolves [03 R-REN-03A §7]; native corners
	// are doubled about that origin instead.
	wb := modelWorldBounds(g)
	rx := region.x + 2*int32(wb.Min.X-region.bounds.Min.X)
	ry := region.y + 2*int32(wb.Min.Y-region.bounds.Min.Y)
	raster, scale := g, float32(2)
	local := modelLocalBounds(g)
	nox := float32(rx) - float32(local.Min.X)*2
	noy := float32(ry) - float32(local.Min.Y)*2
	ox, oy := nox, noy
	reflecting := !shadow && g.ReflectWater && !r.reflections.disabled && !r.water.disabled
	e, body, keyed := r.retainedEntry(g, keyDelta, group, reflecting)
	hit := e != nil && e.captured && e.matches(g)
	if hit {
		if e.doubled {
			raster, scale = g.Supersample, 1
			slot := e.slotFor(g.Supersample)
			ox = float32(rx) - float32(slot.Min.X&^1)
			oy = float32(ry) - float32(slot.Min.Y&^1)
		}
	} else if ss := g.Supersample; ss != nil {
		ssLocal := d.slotBoundsFor(ss)
		if !ssLocal.Empty() {
			raster, scale = ss, 1
			ox = float32(rx) - float32(ssLocal.Min.X&^1)
			oy = float32(ry) - float32(ssLocal.Min.Y&^1)
		}
	}
	// The warm append needs the body entry to describe this raster's faces
	// exactly; otherwise the lane is cold and, for a keyed packet, records the
	// body anew. A reflecting packet is cold only when it is not warm.
	warm := !hit && body != nil && body.matches(raster.Faces, raster == g.Supersample, r.texturePage())
	if !warm {
		body = nil
		if reflecting && keyed {
			r.modelStats.DirectColdReflect++
		}
	}
	entry := 0
	d.noQuads = false
	if !shadow {
		// The reveal rides the raster (a doubled lane carries its own copy);
		// the waterline and Digger ride the packet, which is the one the
		// recorder configures; the frame is where this raster lands.
		entry = d.subjectVerdicts(raster.Reveal, g, keyDelta, group, ox, oy)
	}
	mode := 0
	if shadow {
		mode = modelDirectShadow
	}
	d.keyDelta = keyDelta
	d.lightSources = subjectLights{}
	if !shadow && !d.soloPass {
		// A solo image's colour is never read, so it carries no light, and the
		// world geometry it repeats reflects once, not twice.
		d.lightSources = r.lighting.near(float32(wb.Min.X+wb.Max.X)*0.5, float32(wb.Min.Y+wb.Max.Y)*0.5, float32(wb.Dx()+wb.Dy())*0.5)
		d.lightX = float32(region.bounds.Min.X) + (ox-float32(region.x))*0.5
		d.lightY = float32(region.bounds.Min.Y) + (oy-float32(region.y))*0.5
		d.lightScale, d.lightHeight = scale*0.5, g.WorldHeight
		r.reflections.active, r.reflections.region = g, region
	}
	fits := hit && !d.noQuads && d.params.count+e.quads <= modelDirectParamCap
	r.modelStats.countRetainMiss(e, hit, fits, warm)
	switch {
	case fits:
		r.replayRetained(e, g, ox, oy, entry, shadow)
	case e != nil && e.primed:
		d.retain.beginCapture(e, d.params.count, len(d.verts), r.modelStats)
		r.appendCachedLane(g, raster, body, keyed, ox, oy, scale, mode, entry)
		d.retain.finishCapture(d, g, raster == g.Supersample, ox, oy, r.modelStats)
		if e.captured {
			r.modelStats.DirectCaptured++
		} else if !warm {
			r.modelStats.DirectColdParams++
		}
	default:
		if e != nil {
			e.primed = true
		}
		r.appendCachedLane(g, raster, body, keyed, ox, oy, scale, mode, entry)
	}
	if !shadow && len(g.Outline) != 0 {
		// The outline endpoints are one native pixel each, from the native
		// packet's rows drawn at 2× about the native origin: retail walks the
		// rows of the 1× image after the anti-alias resolve [03 R-COMP-01 §3],
		// so an endpoint is a whole pixel. The doubled lane's own rows would
		// put an endpoint at a doubled column, straddling two pixels' blocks
		// and resolving to half an endpoint in each. A solo image reuses the
		// walk the group composition just made of the same packet — the frame
		// arena hands out slices that stay valid and distinct for the whole
		// frame — instead of walking those rows a second time.
		faces := d.soloOutline
		if !d.soloPass || d.soloOutlineFor != g {
			faces = r.prepareModelOutline(g, false)
			d.soloOutline, d.soloOutlineFor = faces, g
		}
		for _, f := range faces {
			r.appendDirectGPUFace(f, nox, noy, 2, entry)
		}
	}
	r.appendDirectLane(raster.LiveFaces, raster, ox, oy, scale, mode|modelDirectLive, entry)
	d.keyDelta = 0
	r.reflections.active = nil
}

// slotBoundsFor is modelSlotBounds with the group composition's own walk reused
// by the solo pass that repeats the same packet an instant later: the doubled
// lane's box costs a pass over every corner of the packet, and the two appends
// ask for the same one. Only the solo pass reads the memo, and only the
// composition that precedes it writes one, so it never outlives its packet.
func (d *modelDirectLane) slotBoundsFor(ss *drawlist.ModelGeometry) image.Rectangle {
	if d.soloPass {
		if d.soloSlotFor == ss {
			return d.soloSlot
		}
		return modelSlotBounds(ss)
	}
	b := modelSlotBounds(ss)
	d.soloSlotFor, d.soloSlot = ss, b
	return b
}

// modelDirectShiftKey is a face corner's key lane: the packet's key plus the
// group delta, saturated to the byte's range when a delta applies.
func modelDirectShiftKey(key, delta int32) int32 {
	if delta == 0 {
		return key
	}
	k := key + delta
	if k < 0 {
		k = 0
	}
	if k > 255 {
		k = 255
	}
	return k
}

// laneKey is a corner's key lane with the group delta applied.
func (d *modelDirectLane) laneKey(key int32) float32 {
	return float32(modelDirectShiftKey(key, d.keyDelta))
}

// texturePage is the shared texture page, or the placeholder before any
// texture is resident: flat faces bind it too, so they share the textured
// faces' runs.
func (r *Renderer) texturePage() *ebiten.Image {
	if page := r.textureAtlas.page; page != nil {
		return page
	}
	return r.placeholderImage()
}

// appendCachedLane appends a packet's cached lane: warm from body when the
// store offered one (model_retain.go), else cold, recording the body of a
// keyed packet for the next revision's warm append. The body recording
// closes here, before the per-frame lanes that follow it.
func (r *Renderer) appendCachedLane(g, raster *drawlist.ModelGeometry, body *modelRetainedBody, keyed bool, ox, oy, scale float32, mode, entry int) {
	d := &r.modelDirect
	if body != nil {
		r.appendWarmLane(raster.Faces, body, ox, oy, scale, mode, entry)
		r.modelStats.DirectWarm++
		return
	}
	if keyed {
		d.retain.beginBody(g.Cache, raster == g.Supersample)
	}
	r.appendDirectLane(raster.Faces, raster, ox, oy, scale, mode, entry)
	d.retain.endBody()
}

// appendWarmLane appends one cached lane from this frame's faces and the
// body entry's texture products: the faces in the order the cold lane
// appends them, each into the run bound to the image the entry recorded,
// through the core the cold lane runs. The entry has been matched against
// faces (modelRetainedBody.matches), so every recorded face is there with
// the corner count and texture the entry describes.
func (r *Renderer) appendWarmLane(faces []drawlist.ModelFace, body *modelRetainedBody, ox, oy, scale float32, mode, entry int) {
	d := &r.modelDirect
	for i := range body.faces {
		bf := &body.faces[i]
		run := d.colourRun([2]*ebiten.Image{bf.img, r.tables.atlas}, 8)
		r.appendFaceCore(&faces[bf.index], bf.tex, ox, oy, scale, mode, entry, run)
	}
}

// appendDirectLane appends one face list to the batch, faces on the shared
// texture page (or flat) first and faces on standalone textures after, each
// group in its own run. A body recording in progress takes every face in
// this order with the image its run binds.
func (r *Renderer) appendDirectLane(faces []drawlist.ModelFace, g *drawlist.ModelGeometry, ox, oy, scale float32, mode, entry int) {
	d := &r.modelDirect
	page := r.texturePage()
	if b := d.retain.body; b != nil {
		b.page = page
	}
	d.standalone = d.standalone[:0]
	for i := range faces {
		f := &faces[i]
		slot := r.modelTextureFor(f.Texture)
		if f.Texture != nil && slot.img != nil && slot.img != page {
			d.standalone = append(d.standalone, i)
			continue
		}
		r.appendLaneFace(f, i, slot, page, ox, oy, scale, mode, entry, d.colourRun([2]*ebiten.Image{page, r.tables.atlas}, 8))
	}
	for _, i := range d.standalone {
		f := &faces[i]
		slot := r.modelTextureFor(f.Texture)
		r.appendLaneFace(f, i, slot, slot.img, ox, oy, scale, mode, entry, d.colourRun([2]*ebiten.Image{slot.img, r.tables.atlas}, 8))
	}
}

// appendLaneFace appends face index of the lane through the full path, or
// through the coverage-only path while a carried child's solo image is being
// composed; img is the image its run binds.
func (r *Renderer) appendLaneFace(f *drawlist.ModelFace, index int, slot modelTextureSlot, img *ebiten.Image, ox, oy, s float32, mode, entry int, run *modelDirectRun) {
	d := &r.modelDirect
	if d.soloPass {
		r.appendSoloFace(f, slot, ox, oy, s, mode, entry, run)
		return
	}
	tex := modelFaceTexFor(f, slot)
	if b := d.retain.body; b != nil {
		b.faces = append(b.faces, modelBodyFace{index: int32(index), n: int32(len(f.Vertices)), texture: f.Texture, img: img, tex: tex})
	}
	r.appendFaceCore(f, tex, ox, oy, s, mode, entry, run)
}

// modelFaceTex is what a face's texture alone decides, which is what the
// body index keeps across a subject's revisions (model_retain.go): the slot
// origin its corners' texel coordinates are offset by, and the ColorA/ColorB
// lanes in both forms — the slot for a mapped face, the face's texel bounds
// for a linear textured one, which the fragment clamps to so that a lane
// interpolated to a covered texel's edge never reaches one texel past the
// authored ring into a neighbouring texture on the page. The bounds are the texture's
// own dimensions in corner order, as the recorder authors them.
type modelFaceTex struct {
	x, y       int32
	quadA      float32
	linA, linB float32
}

// modelFaceTexFor resolves a face's texture products from its slot.
func modelFaceTexFor(f *drawlist.ModelFace, slot modelTextureSlot) modelFaceTex {
	t := modelFaceTex{x: int32(slot.x), y: int32(slot.y), quadA: float32(slot.x*4096 + slot.y)}
	if f.Texture != nil && len(f.Vertices) != 0 {
		u0, v0, u1, v1 := f.Vertices[0].U, f.Vertices[0].V, f.Vertices[0].U, f.Vertices[0].V
		for _, v := range f.Vertices[1:] {
			u0, v0 = min(u0, v.U), min(v0, v.V)
			u1, v1 = max(u1, v.U), max(v1, v.V)
		}
		u1, v1 = max(u1-1, u0), max(v1-1, v0)
		t.linA = float32((int32(slot.x)+u0)*4096 + int32(slot.y) + v0)
		t.linB = float32((int32(slot.x)+u1)*4096 + int32(slot.y) + v1)
	}
	return t
}

// colourRun returns the open colour run for imgs on the current page, opening
// one when the last run binds something else, draws into another page or
// cannot take need more vertices.
func (d *modelDirectLane) colourRun(imgs [2]*ebiten.Image, need int) *modelDirectRun {
	if n := len(d.runs); n > 0 {
		run := &d.runs[n-1]
		if run.imgs[0] == imgs[0] && run.imgs[1] == imgs[1] && run.page == d.page && int(run.vLen)+need <= schedRunVertexLimit {
			return run
		}
	}
	d.runs = append(d.runs, modelDirectRun{imgs: imgs, page: d.page, vOff: int32(len(d.verts)), iOff: int32(len(d.idx))})
	return &d.runs[len(d.runs)-1]
}

// appendSoloFace appends one face of a carried child's solo image: the image
// the shadow commits read for COVERAGE, and for KEYS when a clip applies
// [03 R-REN-03D §1]. Its colour is never sampled — the silhouette composites
// the half-colour of index 0 — so the face carries only what decides whether a
// texel is covered: its biased corners, its key lane, its flat index (index 1
// is the composition transparent one and leaves a hole, as it does in the group
// image) or its texture's texel, and the subject's verdict entry, whose reveal
// and clip erases are part of the finished image. It skips the shade row, the
// battle light, the glint, the finish and the reflection, all of which only
// reach a colour.
//
// It also skips the parameter entry, so a four-corner face's key interpolates
// linearly here rather than through the span writer's two-chain mapping. Both
// atlas passes read the same lane, so they still agree about a texel; the
// departure is confined to where a SLOPED quad's key crosses a silhouette clip
// key, which can move that boundary by a texel of the shadow's edge.
func (r *Renderer) appendSoloFace(f *drawlist.ModelFace, slot modelTextureSlot, ox, oy, s float32, mode, entry int, run *modelDirectRun) {
	d := &r.modelDirect
	n := len(f.Vertices)
	if n < 3 {
		return
	}
	// The ring's doubled signed area, summed edge by edge from the closing
	// edge (last corner to first) onward.
	var area int64
	a := &f.Vertices[n-1]
	for i := range f.Vertices {
		b := &f.Vertices[i]
		area += int64(a.X)*int64(b.Y) - int64(a.Y)*int64(b.X)
		a = b
	}
	if area <= 0 {
		r.modelStats.DirectCulled++
		return
	}
	// A textured face keeps the linear texel path, clamped to its own authored
	// bounds, because a transparent texel is a hole in the finished image and so
	// a hole in its shadow.
	colorA, colorB := float32(0), float32(0)
	if f.Texture != nil {
		mode |= modelDirectTextured
		u0, v0, u1, v1 := f.Vertices[0].U, f.Vertices[0].V, f.Vertices[0].U, f.Vertices[0].V
		for _, v := range f.Vertices[1:] {
			u0, v0 = min(u0, v.U), min(v0, v.V)
			u1, v1 = max(u1, v.U), max(v1, v.V)
		}
		u1, v1 = max(u1-1, u0), max(v1-1, v0)
		colorA = float32((int32(slot.x)+u0)*4096 + int32(slot.y) + v0)
		colorB = float32((int32(slot.x)+u1)*4096 + int32(slot.y) + v1)
	}
	base := uint32(len(d.verts)) - uint32(run.vOff)
	for _, v := range f.Vertices {
		d.verts = append(d.verts, ebiten.Vertex{
			DstX: ox + modelSamplePos(v.X, s), DstY: oy + modelSamplePos(v.Y, s),
			SrcX: float32(slot.x + int(v.U)), SrcY: float32(slot.y + int(v.V)),
			ColorR: 1, ColorG: float32(f.Color), ColorB: colorB, ColorA: colorA,
			Custom0: float32(mode), Custom1: d.laneKey(v.Key), Custom2: float32(entry), Custom3: 0,
		})
	}
	for i := 1; i+1 < n; i++ {
		d.idx = append(d.idx, base, base+uint32(i), base+uint32(i+1))
	}
	run.vLen += int32(n)
	run.iLen += int32(3 * (n - 2))
	r.modelStats.DirectFaces++
}

// appendDirectFace fan-triangulates one face at scale s about the origin
// (ox, oy) into run (the atlas batch) or, when run is nil, into the fallback
// batch, resolving its texture products first (the cold path).
func (r *Renderer) appendDirectFace(f *drawlist.ModelFace, slot modelTextureSlot, ox, oy, s float32, mode, entry int, run *modelDirectRun) {
	r.appendFaceCore(f, modelFaceTexFor(f, slot), ox, oy, s, mode, entry, run)
}

// appendFaceCore is the pose-dependent whole of a face's append, shared by
// the cold and the warm lanes so the two produce the same bytes by
// construction: the winding test, the parameter entry, the centroid, the
// reflection, the battle light, the glint and the finish (both from the
// world normal), the shade lanes and the packed corners, with tex the
// texture's part. mode is the face's Custom0 lane and entry the subject's
// verdict entry. A ring whose signed area is not positive is a back face or
// degenerate and is culled, as the slot stage's triangulator culled it.
func (r *Renderer) appendFaceCore(f *drawlist.ModelFace, tex modelFaceTex, ox, oy, s float32, mode, entry int, run *modelDirectRun) {
	d := &r.modelDirect
	n := len(f.Vertices)
	if n < 3 {
		return
	}
	// The ring's doubled signed area, summed edge by edge from the closing
	// edge (last corner to first) onward.
	var area int64
	a := &f.Vertices[n-1]
	for i := range f.Vertices {
		b := &f.Vertices[i]
		area += int64(a.X)*int64(b.Y) - int64(a.Y)*int64(b.X)
		a = b
	}
	if area <= 0 {
		r.modelStats.DirectCulled++
		return
	}
	shadow := mode&7 == modelDirectShadow
	// A four-corner face is described to the parameter image in the subject's
	// own frame at the atlas scale (model_quads.go) and mapped per fragment,
	// flat or textured, so its key is the span writer's two-chain
	// interpolation rather than the device's per-triangle one; anything else
	// interpolates its lanes linearly.
	quad := 0
	if n == 4 && run != nil && !shadow && !d.noQuads && d.params.count < modelDirectParamCap {
		// At scale one (the recorder's own doubled lane) the corners are the
		// packet's: the scaled copy would be exact and is not made.
		corners := f.Vertices
		if s != 1 {
			for i, v := range f.Vertices {
				d.doubled[i] = v
				d.doubled[i].X = int32(float32(v.X) * s)
				d.doubled[i].Y = int32(float32(v.Y) * s)
			}
			corners = d.doubled[:]
		}
		quad = d.params.add(corners, modelQuadLocalBias, modelQuadLocalBias, d.keyDelta)
	}
	if !shadow {
		switch {
		case f.Texture != nil && quad != 0:
			mode = (mode &^ 7) | modelDirectQuad
			if f.Shaded {
				mode = (mode &^ 7) | modelDirectQuadShade
			}
		case f.Texture != nil:
			mode |= modelDirectTextured
		case quad != 0:
			mode = (mode &^ 7) | modelDirectFlatQuad
			if f.Shaded {
				mode = (mode &^ 7) | modelDirectFlatQuadShade
			}
		}
	}
	if run != nil && !shadow && r.reflections.active != nil {
		// The centroid and the fattening feed the reflection alone, so a
		// subject that does not reflect this frame skips them.
		var cx, cy float32
		for _, v := range f.Vertices {
			cx += float32(v.X)
			cy += float32(v.Y)
		}
		cx /= float32(n)
		cy /= float32(n)
		r.reflectModelFace(f, ox, oy, s, cx, cy, float32(modelDirectFatten), quad, run.page)
	}
	base := uint32(len(d.verts))
	if run != nil {
		base -= uint32(run.vOff)
	}
	// A mapped face carries its texture slot in ColorA and its entry in
	// ColorB; a linear textured face carries its texel bounds there instead,
	// absolute on the page.
	colorA, colorB := tex.quadA, float32(quad)
	if quad == 0 && f.Texture != nil {
		colorA, colorB = tex.linA, tex.linB
	}
	lighting := float32(0)
	if !shadow {
		lighting = r.modelFaceLight(f)
	}
	if lighting > 0 {
		r.modelStats.LitModelFaces++
	}
	custom2, custom3 := float32(entry), lighting
	colorG := float32(f.Color)
	if r.metalGlint && !shadow {
		colorG = metalGlintColor(f.Color, metalFaceGlint(f.Normal))
	}
	if !shadow {
		colorG = r.modelFinishColor(f, colorG)
	}
	if run == nil {
		custom2, custom3 = lighting, sceneOpModelDirect
	}
	// The face's corners and its fan are written in place after one growth
	// each, rather than appended corner by corner.
	vi := len(d.verts)
	d.verts = slices.Grow(d.verts, n)[:vi+n]
	dst := d.verts[vi : vi+n]
	shaded := f.Shaded && !shadow
	for i := range f.Vertices {
		v := &f.Vertices[i]
		k := float32(1)
		if shaded {
			k = shadeScale(int(v.Shade))
			if k > rowScaleMax {
				k = rowScaleMax
			}
		}
		// The atlas batch's key lane is the fallback batch's opacity: the
		// fallback fragment tests no key and the cloaked half-colour needs a
		// lane [03 R-COMP-01 §2].
		custom1 := d.laneKey(v.Key)
		if run == nil {
			custom1 = d.fallbackOpacity
		}
		dst[i] = ebiten.Vertex{
			DstX: ox + modelSamplePos(v.X, s), DstY: oy + modelSamplePos(v.Y, s),
			SrcX: float32(tex.x + v.U), SrcY: float32(tex.y + v.V),
			ColorR: k, ColorG: colorG, ColorB: colorB,
			ColorA:  colorA,
			Custom0: float32(mode), Custom1: custom1, Custom2: custom2, Custom3: custom3,
		}
	}
	ii := len(d.idx)
	d.idx = slices.Grow(d.idx, 3*(n-2))[:ii+3*(n-2)]
	fan := d.idx[ii:]
	for i := 1; i+1 < n; i++ {
		fan[0], fan[1], fan[2] = base, base+uint32(i), base+uint32(i+1)
		fan = fan[3:]
	}
	if run != nil {
		run.vLen += int32(n)
		run.iLen += int32(3 * (n - 2))
		if d.retain.capture != nil {
			d.retain.captureFace(f, run.imgs[0], n, quad)
		}
	}
	r.modelStats.DirectFaces++
}

// appendDirectGPUFace appends one prepared outline endpoint quad: a flat,
// keyed face drawn after the reveal, at scale s about (ox, oy), unfattened so
// the endpoint stays one native pixel.
func (r *Renderer) appendDirectGPUFace(f modelGPUFace, ox, oy, s float32, entry int) {
	d := &r.modelDirect
	n := len(f.Vertices)
	if n < 3 {
		return
	}
	run := d.colourRun([2]*ebiten.Image{r.texturePage(), r.tables.atlas}, 8)
	base := uint32(len(d.verts)) - uint32(run.vOff)
	for _, v := range f.Vertices {
		d.verts = append(d.verts, ebiten.Vertex{
			DstX: ox + v.X*s, DstY: oy + v.Y*s,
			ColorR: 1, ColorG: float32(f.Color),
			Custom0: modelDirectOutline | modelDirectLive, Custom1: d.laneKey(int32(v.Key)), Custom2: float32(entry), Custom3: 0,
		})
	}
	for i := 1; i+1 < n; i++ {
		d.idx = append(d.idx, base, base+uint32(i), base+uint32(i+1))
	}
	run.vLen += int32(n)
	run.iLen += int32(3 * (n - 2))
	r.modelStats.DirectFaces++
}

// modelSamplePos is a corner coordinate scaled about the origin and biased
// onto the span writer's sampling grid (modelDirectSampleBias). The bias is in
// the texels of the raster being drawn, whatever the scale, because the
// two-chain mapping evaluates the scaled corners at that raster's texels.
func modelSamplePos(v int32, s float32) float32 {
	return float32(v)*s + modelDirectSampleBias
}

// fattenBy scales a corner coordinate about the origin and pushes a corner on
// the far side of the face centroid fat texels further, and nothing on the
// near side. Only the water reflection's geometry uses it now.
func fattenBy(v, c, s, fat float32) float32 {
	v *= s
	c *= s
	if v > c {
		return v + fat
	}
	return v
}

// commitModelDirect compiles one subject's commit into the frame's opaque
// batch: a quad over its clipped world rectangle whose fragment box-resolves
// the atlas (sceneOpModelDirectCommit). A subject without a region takes the
// fallback.
func (r *Renderer) commitModelDirect(g *drawlist.ModelGeometry) {
	d := &r.modelDirect
	region, ok := d.regions[g]
	if !ok || !region.ok {
		r.drawModelDirectFallback(g)
		return
	}
	b := region.bounds
	x0, y0 := max(b.Min.X, 0), max(b.Min.Y, 0)
	x1, y1 := min(b.Max.X, r.clipW()), min(b.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{2: d.pages[region.page].colour}) {
		return
	}
	// Source coordinates are atlas texels: the region origin plus twice the
	// pixel's offset from the world-bounds origin, so a fragment at a pixel
	// centre lands one texel into its 2×2 block and the fragment floors back.
	sx0 := float32(region.x) + 2*float32(x0-b.Min.X)
	sy0 := float32(region.y) + 2*float32(y0-b.Min.Y)
	cloak := float32(0)
	if g.Cloaked {
		cloak = 1
	}
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+2*float32(x1-x0), sy0+2*float32(y1-y0),
		[4]float32{g.WreckEmission[0], g.WreckEmission[1], g.WreckEmission[2], 0}, [4]float32{cloak, 0, 0, sceneOpModelDirectCommit})
}

// commitModelDirectShadow compiles one subject's shadow commit: a
// destination command over the shadow's clipped world rectangle whose fragment
// resolves the silhouette from the shadow's page (source 3) and punches the
// body's wholly covered blocks read from the body's page (source 2)
// (destOpModelDirectShadow). The body image is the subject's own — a carried
// child's solo region, not the group's texels — so the punch tests the
// subject's coverage and nothing else. A shadow without a region is omitted, as
// the slot stage omitted one it could not place.
func (r *Renderer) commitModelDirectShadow(g *drawlist.ModelGeometry) {
	d := &r.modelDirect
	sg := g.Shadow
	shadow, ok := d.regions[sg]
	body, bok := d.shadowSource(g)
	if !ok || !shadow.ok || !bok || r.sceneDest == nil {
		r.modelStats.ShadowsOmitted++
		return
	}
	sb, bb := shadow.bounds, body.bounds
	x0, y0 := max(sb.Min.X, 0), max(sb.Min.Y, 0)
	x1, y1 := min(sb.Max.X, r.clipW()), min(sb.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		r.modelStats.Shadows++
		return
	}
	if !r.sched.begin(schedDest, x0, y0, x1, y1, [4]*ebiten.Image{1: r.tables.atlas, 2: d.pages[body.page].colour, 3: d.pages[shadow.page].colour}) {
		return
	}
	// A pixel's shadow block is the region origin plus twice its offset from
	// the shadow bounds; its body block is the same pixel's block in the body
	// region, which is this constant vector away in atlas texels.
	sx0 := float32(shadow.x) + 2*float32(x0-sb.Min.X)
	sy0 := float32(shadow.y) + 2*float32(y0-sb.Min.Y)
	kx := float32(body.x-shadow.x) + 2*float32(sb.Min.X-bb.Min.X)
	ky := float32(body.y-shadow.y) + 2*float32(sb.Min.Y-bb.Min.Y)
	r.sched.quad(schedDest,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+2*float32(x1-x0), sy0+2*float32(y1-y0),
		[4]float32{float32(body.x), float32(body.y), float32(body.x) + 2*float32(bb.Dx()), float32(body.y) + 2*float32(bb.Dy())},
		[4]float32{kx, ky, 0, destOpModelDirectShadow})
	r.modelStats.Shadows++
}

// commitModelSilhouetteShadow compiles a Digger or mobile subject's shadow:
// a destination command over the body's box at the shadow's placement whose
// fragment resolves the body's own colour texels as the silhouette, erased at
// and below the packet's clip key read from the body's key page (slot 2, bound
// only when a clip applies), and
// composites the ALP half-colour of index 0 (destOpModelSilhouetteShadow)
// [03 R-REN-03D §1, §4]. The body's region is the source, so the shadow costs
// no region and no faces of its own; a body without a region (the fallback)
// casts none. For a carried child that source is the child's solo region: the
// silhouette is the child's finished image, as the classic composer's copy is,
// and the clip key then compares against the child's OWN keys rather than the
// group plane's shifted ones [03 R-REN-03A §4][03 R-REN-03D §1].
func (r *Renderer) commitModelSilhouetteShadow(g *drawlist.ModelGeometry) {
	d := &r.modelDirect
	sg := g.Shadow
	body, bok := d.shadowSource(g)
	if !bok || r.sceneDest == nil {
		r.modelStats.ShadowsOmitted++
		return
	}
	if r.commitAircraftShadow(g, body) {
		r.modelStats.Shadows++
		r.modelStats.Silhouettes++
		return
	}
	sb := modelWorldBounds(sg)
	x0, y0 := max(sb.Min.X, 0), max(sb.Min.Y, 0)
	x1, y1 := min(sb.Max.X, r.clipW()), min(sb.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		r.modelStats.Shadows++
		r.modelStats.Silhouettes++
		return
	}
	// The colour page rides slots 2 and 3, the projected shadow commit's own
	// binding, so the two kinds of shadow share a run; only a clipped
	// silhouette (a Digger, a submerged mobile) binds the key page in slot 2.
	pg := &d.pages[body.page]
	slot2 := pg.colour
	if sg.SilhouetteClip != 0 {
		slot2 = pg.key
	}
	if !r.sched.begin(schedDest, x0, y0, x1, y1, [4]*ebiten.Image{1: r.tables.atlas, 2: slot2, 3: pg.colour}) {
		return
	}
	// The shadow's local space is the body's: a pixel's block is the body's
	// texel origin plus twice the pixel's offset from the shadow bounds.
	wb := modelWorldBounds(g)
	rx := body.x + 2*int32(wb.Min.X-body.bounds.Min.X)
	ry := body.y + 2*int32(wb.Min.Y-body.bounds.Min.Y)
	sx0 := float32(rx) + 2*float32(x0-sb.Min.X)
	sy0 := float32(ry) + 2*float32(y0-sb.Min.Y)
	r.sched.quad(schedDest,
		float32(x0), float32(y0), float32(x1), float32(y1),
		sx0, sy0, sx0+2*float32(x1-x0), sy0+2*float32(y1-y0),
		[4]float32{}, [4]float32{0, 0, float32(sg.SilhouetteClip), destOpModelSilhouetteShadow})
	r.modelStats.Shadows++
	r.modelStats.Silhouettes++
}

// drawModelDirectFallback draws a subject's faces straight onto the composite
// at native scale in painter order — mean key ascending, recorded order on
// ties, no key test, no supersample — for a subject the atlas could not hold.
// An attached-unit group draws as one: every mergeable child's faces join the
// carrier's in that one order, each at its own anchor and with its key delta
// applied, so an overflowed transport still carries its cargo
// [03 R-REN-03A §4].
//
// What this path does NOT reproduce, all of it for want of a key plane to put
// the verdict on or a lane in the fragment to carry it:
//
//   - the nanoframe reveal, the waterline tint and the Digger erase
//     [03 §5.2][03 R-WATER-01 §2][03 R-REN-03A §8], which are per-texel
//     verdicts on the stored key;
//   - the subject's shadow, which commitModelDirect's callers omit with no
//     region to cut it from.
//
// A cloaked subject draws the ALP half-colour of its faces, source-over, as
// the commit does for the group as a whole [03 R-COMP-01 §2]: the carrier's
// blend covers its cargo, as a staged carrier's does.
//
// A frame reaches here only when both atlas pages are full, which neither
// battle view does (§22.2).
func (r *Renderer) drawModelDirectFallback(g *drawlist.ModelGeometry) {
	d := &r.modelDirect
	b := modelGroupBounds(g)
	x0, y0 := max(b.Min.X, 0), max(b.Min.Y, 0)
	x1, y1 := min(b.Max.X, r.clipW()), min(b.Max.Y, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	d.fallbackOpacity = 1
	if g.Cloaked {
		d.fallbackOpacity = 0.5
	}
	r.modelStats.Skipped += d.groupPainterOrder(g)
	d.lightSources = r.lighting.near(float32(b.Min.X+b.Max.X)*0.5, float32(b.Min.Y+b.Max.Y)*0.5, float32(b.Dx()+b.Dy())*0.5)
	d.lightX, d.lightY, d.lightScale, d.lightHeight = float32(g.AnchorX-g.OriginX), float32(g.AnchorY-g.OriginY), 1, g.WorldHeight
	page := r.texturePage()
	d.verts, d.idx = d.verts[:0], d.idx[:0]
	d.standalone = d.standalone[:0]
	for oi, of := range d.order {
		owner := modelDirectFaceOwner(g, of.child)
		f := modelDirectFaceAt(owner, of.index)
		slot := r.modelTextureFor(f.Texture)
		if f.Texture != nil && slot.img != nil && slot.img != page {
			d.standalone = append(d.standalone, oi)
			continue
		}
		ox, oy := modelDirectFaceOrigin(owner)
		r.appendDirectFace(f, slot, ox, oy, 1, 0, 0, nil)
	}
	if len(d.idx) > 0 {
		if r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{1: r.tables.atlas, 2: page}) {
			r.sched.tris(schedOpaque, d.verts, d.idx)
		}
	}
	for _, oi := range d.standalone {
		owner := modelDirectFaceOwner(g, d.order[oi].child)
		f := modelDirectFaceAt(owner, d.order[oi].index)
		slot := r.modelTextureFor(f.Texture)
		d.verts, d.idx = d.verts[:0], d.idx[:0]
		ox, oy := modelDirectFaceOrigin(owner)
		r.appendDirectFace(f, slot, ox, oy, 1, 0, 0, nil)
		if len(d.idx) == 0 {
			continue
		}
		if r.sched.begin(schedOpaque, x0, y0, x1, y1, [4]*ebiten.Image{1: r.tables.atlas, 2: slot.img}) {
			r.sched.tris(schedOpaque, d.verts, d.idx)
		}
	}
	d.verts, d.idx = d.verts[:0], d.idx[:0]
}

// groupPainterOrder fills the lane's order with every face a fallback group
// draws — the carrier's cached and live lanes, then each mergeable child's —
// sorted by mean height key ascending, stably, so faces of equal key keep the
// order they were recorded in. A child's key delta is applied to its corners as
// the group region's composition applies it, saturating at the byte's range
// [03 R-REN-03A §4], so the cargo sorts against the carrier where its keys
// actually put it. It returns the children the lane cannot merge, which the
// caller counts as skipped.
func (d *modelDirectLane) groupPainterOrder(g *drawlist.ModelGeometry) (skipped int) {
	d.order = d.order[:0]
	collect := func(faces []drawlist.ModelFace, live bool, child, delta int32) {
		for i := range faces {
			n := len(faces[i].Vertices)
			if n < 3 {
				continue
			}
			var sum int64
			for _, v := range faces[i].Vertices {
				sum += int64(modelDirectShiftKey(v.Key, delta))
			}
			index := int32(i)
			if live {
				index = ^index
			}
			d.order = append(d.order, modelDirectFace{key: sum * 256 / int64(n), index: index, child: child})
		}
	}
	collect(g.Faces, false, -1, 0)
	collect(g.LiveFaces, true, -1, 0)
	for ci, child := range g.Children {
		cg := child.Geometry
		if !mergeableChild(cg) {
			skipped++
			continue
		}
		collect(cg.Faces, false, int32(ci), child.KeyDelta)
		collect(cg.LiveFaces, true, int32(ci), child.KeyDelta)
	}
	slices.SortStableFunc(d.order, func(a, b modelDirectFace) int { return cmp.Compare(a.key, b.key) })
	return skipped
}

// modelDirectFaceOwner is the packet a fallback face belongs to: the carrier,
// or one of its carried children.
func modelDirectFaceOwner(g *drawlist.ModelGeometry, child int32) *drawlist.ModelGeometry {
	if child < 0 {
		return g
	}
	return g.Children[child].Geometry
}

// modelDirectFaceOrigin is where a packet's local corners land on the
// framebuffer: each child carries its own anchor.
func modelDirectFaceOrigin(g *drawlist.ModelGeometry) (float32, float32) {
	return float32(g.AnchorX - g.OriginX), float32(g.AnchorY - g.OriginY)
}

func modelDirectFaceAt(g *drawlist.ModelGeometry, index int32) *drawlist.ModelFace {
	if index < 0 {
		return &g.LiveFaces[^index]
	}
	return &g.Faces[index]
}

// modelDirectMappedSource is the mode test shared by both passes: a mapped
// mode's key comes from the parameter image's two-chain mapping.
func modelDirectMappedSource() string {
	return `
func modelDirectMapped(mode int) bool {
	return mode == ` + fmt.Sprint(modelDirectQuad) + ` || mode == ` + fmt.Sprint(modelDirectQuadShade) + ` ||
		mode == ` + fmt.Sprint(modelDirectFlatQuad) + ` || mode == ` + fmt.Sprint(modelDirectFlatQuadShade) + `
}
`
}

// modelDirectKeyShaderSource is the key pass: the face's height key — the
// two-chain mapping's for a mapped face, the vertex lane's otherwise —
// narrowed to a byte as the span writers narrow it, in red, under a max
// blend. Source 3 is the parameter image; a mapped face's Custom2 names the
// subject's entry, whose magnitude locates the frame its parameters are in.
func modelDirectKeyShaderSource() string {
	return `//kage:unit pixels

package main
` + modelQuadMapperSource + modelDirectMappedSource() + `
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	mode := int(custom.x + 0.5)
	if mode >= ` + fmt.Sprint(modelDirectLive) + ` {
		mode -= ` + fmt.Sprint(modelDirectLive) + `
	}
	key := floor(custom.y)
	if modelDirectMapped(mode) {
		// The parameters are subject-local: the subject's frame, read from its
		// verdict entry (either sign), shifts the fragment into them.
		frame := modelQuadFrame(abs(floor(custom.z + 0.5)))
		key = floor(modelQuadLanes(color.b, floor(dstPos.xy-imageDstOrigin())-frame).z)
	}
	key = key - floor(key/256.0)*256.0
	return vec4(key/255.0, 0.0, 0.0, 1.0)
}
`
}

// modelDirectColourShaderSource is the colour pass over the atlas. Source 0 is
// the texture page (or a standalone texture), 1 the table atlas, 2 the key
// plane, 3 the parameter image, whose corners are subject-local and read
// through the frame the subject's entry carries. A fragment whose key is
// below the stored one is another face's pixel. The reveal rewrites a cached face's index by its
// key band [03 §5.2]; the waterline and Digger clip erase or tint at and
// below their keys [03 R-WATER-01 §2][03 R-REN-03A §8], the subject's own on
// its own key and then the carrier's on the group key, every verdict on the
// pixel's nearest-sampled key; the composition transparent index 1 is
// dropped last.
func modelDirectColourShaderSource() string {
	return `//kage:unit pixels

package main

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0
const blueRow = ` + fmt.Sprint(tableRowBlue) + `.0
` + modelQuadMapperSource + modelDirectMappedSource() + battleLightShaderSource + metalGlintShaderSource + modelFinishShaderSource + `
func palAt(idx float) vec3 {
	return imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb
}

func blueAt(idx float) float {
	return floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, blueRow+0.5)).r*255.0 + 0.5)
}

// clipIndex is one waterline-and-Digger clip: erase (mode 1) or blue-tint
// (mode 2) at and below the waterline key, then erase at and below the
// Digger key.
func clipIndex(idx float, key float, wl float, wlKey float, digger float, diggerKey float) float {
	if wl > 0.5 && idx != 1.0 && key <= wlKey {
		if wl == 1.0 {
			idx = 1.0
		} else {
			idx = blueAt(idx)
		}
	}
	if digger > 0.5 && key <= diggerKey {
		idx = 1.0
	}
	return idx
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	d := floor(dstPos.xy - imageDstOrigin())
	mode := int(custom.x + 0.5)
	live := mode >= ` + fmt.Sprint(modelDirectLive) + `
	if live {
		mode -= ` + fmt.Sprint(modelDirectLive) + `
	}
	key := floor(custom.y)
	mapped := modelDirectMapped(mode)
	// The subject's verdict entry: negated when it carries the frame alone.
	entry := floor(custom.z + 0.5)
	var lanes vec4
	if mapped {
		lanes = modelQuadLanes(color.b, d-modelQuadFrame(abs(entry)))
		key = floor(lanes.z)
	}
	key = key - floor(key/256.0)*256.0
	// The pixel's block origin: the top-left texel, whose stored key is the
	// key retail's 1× image holds after the 2:1 resolve samples it
	// [03 R-REN-03A §6].
	block := floor(d/2.0) * 2.0
	if mode != ` + fmt.Sprint(modelDirectShadow) + ` {
		at := d
		if mode == ` + fmt.Sprint(modelDirectOutline) + ` {
			// An endpoint is tested once for its whole pixel.
			at = block
		}
		stored := floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+at+vec2(0.5, 0.5)).r*255.0 + 0.5)
		if key < stored {
			return vec4(0.0)
		}
	}
	encoded := floor(color.g + 0.5)
	glint := mod(floor(encoded/256.0), 256.0)
	finish := floor(encoded/65536.0)
	idx := mod(encoded, 256.0)
	k := color.r
	if mode == ` + fmt.Sprint(modelDirectShadow) + ` {
		// A shadow silhouette: its index, no key test, no verdicts.
	} else if mode == ` + fmt.Sprint(modelDirectQuad) + ` || mode == ` + fmt.Sprint(modelDirectQuadShade) + ` {
		uv := floor(lanes.xy + vec2(1.0/65536.0, 1.0/65536.0))
		slot := vec2(floor(color.a/4096.0), color.a-floor(color.a/4096.0)*4096.0)
		idx = floor(imageSrc0At(imageSrc0Origin()+slot+uv+vec2(0.5, 0.5)).r*255.0 + 0.5)
		if mode == ` + fmt.Sprint(modelDirectQuadShade) + ` {
			k = min(0.06875*floor(lanes.w), ` + fmt.Sprint(rowScaleMax) + `)
		}
	} else if mode == ` + fmt.Sprint(modelDirectFlatQuadShade) + ` {
		k = min(0.06875*floor(lanes.w), ` + fmt.Sprint(rowScaleMax) + `)
	} else if mode == ` + fmt.Sprint(modelDirectTextured) + ` {
		lo := vec2(floor(color.a/4096.0), color.a-floor(color.a/4096.0)*4096.0)
		hi := vec2(floor(color.b/4096.0), color.b-floor(color.b/4096.0)*4096.0)
		t := clamp(floor(srcPos-imageSrc0Origin()), lo, hi)
		idx = floor(imageSrc0At(imageSrc0Origin()+t+vec2(0.5, 0.5)).r*255.0 + 0.5)
	}
	if entry > 0.5 && mode != ` + fmt.Sprint(modelDirectShadow) + ` {
		base := (entry-1.0)*12.0
		a := modelQuadTexel(base)
		b := modelQuadTexel(base+1.0)
		c := modelQuadTexel(base+2.0)
		e := modelQuadTexel(base+3.0)
		f := modelQuadTexel(base+4.0)
		g := modelQuadTexel(base+5.0)
		// The verdicts read the pixel's key, not the texel's, so all four
		// texels under a pixel take one verdict and a band one key wide
		// resolves to whole pixels rather than half-covered ones.
		vkey := floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+block+vec2(0.5, 0.5)).r*255.0 + 0.5)
		// The subject's own verdicts read its own key: a carried child's
		// lane carries the group delta, which is taken back off here.
		own := vkey - (modelQuadU16(g.r, g.g) - 32768.0)
		hasReveal := modelQuadU16(f.b, f.a)
		if !live && hasReveal > 0.5 {
			// The nanoframe reveal: below the floor, in the band, or at and
			// above the line, each a verdict — erase (-2), keep (-1) or an
			// index — stored offset by two.
			verdict := modelQuadU16(b.b, b.a) - 2.0
			if own < modelQuadU16(a.b, a.a) {
				verdict = modelQuadU16(b.r, b.g) - 2.0
			} else if own >= modelQuadU16(a.r, a.g) {
				verdict = modelQuadU16(c.r, c.g) - 2.0
			}
			if verdict == -2.0 {
				idx = 1.0
			} else if verdict != -1.0 {
				// A replaced index is a physical colour written as is: the
				// slot stage rewrote the plane after shading (§11.5).
				idx = verdict
				k = 1.0
				glint = 0.0
				finish = 0.0
			}
		}
		idx = clipIndex(idx, own, modelQuadU16(c.b, c.a), modelQuadU16(e.r, e.g), modelQuadU16(e.b, e.a), modelQuadU16(f.r, f.g))
		gclip := modelQuadU16(g.b, g.a)
		if gclip > 0.5 {
			// The carrier's clip over the staging image, on the shifted key.
			h := modelQuadTexel(base+6.0)
			i := modelQuadTexel(base+7.0)
			idx = clipIndex(idx, vkey, gclip-1.0, modelQuadU16(h.r, h.g), modelQuadU16(h.b, h.a), modelQuadU16(i.r, i.g))
		}
	}
	if idx == 1.0 {
		return vec4(0.0)
	}
	albedo := palAt(idx)
	return vec4(modelFinish(albedo, metalGlint(albedo, battleLit(albedo, k, custom.w), glint), finish), 1.0)
}
`
}

// modelDirectPending is one packet awaiting placement: a body (with its
// shadow) or a carried child's shadow alone, with the height the packer sorts
// by.
type modelDirectPending struct {
	g      *drawlist.ModelGeometry
	shadow bool
	height int
}
