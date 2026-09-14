package client

import (
	"sync/atomic"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// modelBodySerials numbers retained body objects for the modern executor's
// persistent slots (docs/DESIGN_GPU_RENDERER.md §13.12). A slot's identity is
// the triple (serial, lane, revision), so a body dropped and recreated under the same
// presentation identity — which InvalidateModelImages does to every body at
// once — can never be mistaken for a raster the executor still holds.
//
// Only equality of the pair is ever read, never its magnitude or its order, so
// the counter's values reach no output and no comparison between frames;
// sharing one counter across clients is what keeps it correct without a
// per-client field, and the atomic covers a body created off the recording
// goroutine [I1].
var modelBodySerials atomic.Uint64

// takeSerial gives a body its permanent serial the first time it stores
// geometry. Bodies are created empty in several places — stage one pre-creates
// one per job so its workers never insert (record_parallel.go) — and an empty
// body has no raster to identify, so numbering them at the store is both
// sufficient and the one point every retained lane passes through.
func (b *cachedModelBody) takeSerial() {
	if b.serial == 0 {
		b.serial = modelBodySerials.Add(1)
	}
}

// InvalidateModelImages drops the shared model image products. Retail clears
// arena owner references and lets ordinary draws rebuild; retained orientation
// and piece pose are unchanged [07 R-CAM-01 §6][03 §2.4.1].
func (c *Client) InvalidateModelImages() {
	clear(c.cachedModelBodies)
}

// cachedModelBody is the persistent, unfinalized cached-piece composition.
// Waterline, Digger and child staging operate on a fresh frame-local copy, as
// their inputs may change while the cached local body remains valid
// [03 R-REN-03A §4].
type cachedModelBody struct {
	image            *modelTarget
	geometry         *drawlist.ModelGeometry
	model            string
	cacheRevision    uint64
	validityRevision uint64
	structure        bool
	construction     float32
	// Published team selection chooses authored LOGOS pixels/faces and is part
	// of both retained cache identities [03 R-RAST-01 §3][I6].
	teamColor teamColor
	// These are Nanolathe's retained-image memoization inputs. They keep a
	// cached physical-index raster from crossing a presentation setting or
	// palette installation; they are not retail's script-driven validity word.
	shaded, supersampled bool
	// geometrySupersampled is the geometry lane's own gate (supersampleGeometry),
	// which is not the classic image's structure-only one.
	geometrySupersampled bool
	scale                camera.ViewScale
	palette              *palette.Tables
	// store is where geometry points when the retained packet was copied into
	// this body's own arenas rather than freshly allocated.
	store cachedGeometryStore
	// serial and geometryRevision are the modern executor's slot identity
	// (docs/DESIGN_GPU_RENDERER.md §13.12): serial is unique to this body object
	// and geometryRevision changes on every store of new cached-lane geometry, so
	// an unchanged pair means the retained faces this frame rebases are literally
	// the ones the executor already rasterized.
	serial           uint64
	geometryRevision uint64
	// The retained shadow lane (§13.12 "Shadows — contract P4"). It is stored
	// beside the body but gated on its own inputs, because the projection reads
	// the whole current pose while the body's cached lane is frozen between
	// orientation rebuilds: the two lanes genuinely do not change together.
	shadowStore    cachedGeometryStore
	shadow         *drawlist.ModelGeometry
	shadowInputs   cachedShadowInputs
	shadowPose     []model.PieceState
	shadowRevision uint64
	shadowValid    bool
	// A 3DO feature's retained lane (featureGeometry) is gated on these two
	// rather than on the unit fields above: a feature publishes no cache
	// revisions, no construction fraction and no orientation cache, and its
	// pose is the corpse's folded orientation triple, compared whole.
	featureInputs featureBodyInputs
	featurePose   []model.PieceState
	// featureSeenTick is the committed tick the feature was last recorded on;
	// pruneCachedModelBodies drops a feature body unseen for featureRetainTicks.
	featureSeenTick uint32
}

// featureRetainTicks is how long a 3DO feature's retained lane outlives its
// last recording — one simulated second — so a wreck that scrolls out of view
// and back inside it is rebased, not re-projected, and one that is gone or
// stays out of view frees its arenas. Presentation retention only.
const featureRetainTicks = 30

// featureBodyID is the cachedModelBodies key of a 3DO feature's retained body:
// its publication identity when it has one, else its map position, because one
// feature stands at a cell and a feature never moves off its anchor while it
// stands [05 "Feature instance and terrain cell"] (a sinking feature keeps
// its X and Z). Unit and feature publication identities are separate
// counters, so the feature's is marked in the top bit, and a position key in
// the top two, to keep the three from ever naming one entry;
// pruneCachedModelBodies ages the marked entries out. Sharing a key between
// two features that stood at one cell in turn is harmless: the retained lane
// is a pure function of the inputs featureGeometry compares, so a different
// corpse re-projects and an identical one reuses faces that are identical.
func featureBodyID(id uint64, x, z numeric.Fixed) uint64 {
	if id != 0 {
		return id | featureBodyMark
	}
	return uint64(uint32(x.Raw()))<<32 | uint64(uint32(z.Raw())) | featureBodyMark | 1<<62
}

// featureBodyMark is the top bit every feature body key carries.
const featureBodyMark = uint64(1) << 63

// featureBodyInputs are the inputs of a 3DO feature's projection other than
// its pose (featureGeometry): the model, the team colour of its LOGOS faces,
// the presentation inputs the unit lane memoizes on, and the texture index
// generation its resolution table was built under.
type featureBodyInputs struct {
	model     string
	teamColor teamColor
	inputs    cachedBodyInputs
	texGen    uint64
}

func (c *Client) featureBodyInputs(draw *presentationrender.UnitDraw, selector teamColor) featureBodyInputs {
	in := featureBodyInputs{teamColor: selector, inputs: c.cachedBodyInputs(draw)}
	if c != nil {
		in.texGen = c.texGen
	}
	if draw != nil && draw.Model != nil {
		in.model = draw.Model.Name
	}
	return in
}

// modelHasAnimatedTexture reports whether any primitive of m resolves to an
// animated texture sequence, whose frame the per-face walk advances by the
// feature's cursor: such a model is projected every frame rather than
// retained. The per-model table answers when it exists; otherwise the
// primitives are resolved here, once per rebuild decision.
func (c *Client) modelHasAnimatedTexture(m *model.Model) bool {
	if c == nil || m == nil {
		return false
	}
	if refs := c.modelTexRefs(m); refs != nil {
		for i := range refs.refs {
			for j := range refs.refs[i] {
				if refs.ok[i][j] && refs.refs[i][j].kind == texAnimated {
					return true
				}
			}
		}
		return false
	}
	for i := range m.Pieces {
		for _, pr := range m.Pieces[i].Primitives {
			if ref, ok := c.resolveModelTexture(pr.TextureName); ok && ref.kind == texAnimated {
				return true
			}
		}
	}
	return false
}

// replaceFeatureGeometry retains a 3DO feature's projected faces beside its
// identity, as replaceCachedGeometry does a unit's cached lane: new faces are
// a new revision, so the executor never replays a lane the recorder has
// re-projected (§13.12).
func (c *Client) replaceFeatureGeometry(key uint64, draw *presentationrender.UnitDraw, in featureBodyInputs, geometry *drawlist.ModelGeometry) *cachedModelBody {
	if c.cachedModelBodies == nil {
		c.cachedModelBodies = make(map[uint64]*cachedModelBody)
	}
	body := c.cachedModelBodies[key]
	if body == nil {
		body = &cachedModelBody{}
		c.cachedModelBodies[key] = body
	}
	body.geometry = body.retainGeometry(geometry)
	body.takeSerial()
	body.geometryRevision++
	body.model, body.structure, body.teamColor = in.model, draw.Structure, in.teamColor
	body.shaded, body.scale, body.palette = in.inputs.shaded, in.inputs.scale, in.inputs.palette
	body.geometrySupersampled = in.inputs.geometrySupersampled
	body.featureInputs = in
	body.featurePose = append(body.featurePose[:0], draw.PieceStates...)
	return body
}

// cacheKey is the identity a rebased packet carries for the modern executor's
// persistent slots. A body with no stored geometry has none.
func (b *cachedModelBody) cacheKey(halfX, halfY int32) drawlist.ModelCacheKey {
	if b == nil || b.geometry == nil || b.geometryRevision == 0 {
		return drawlist.ModelCacheKey{}
	}
	return drawlist.ModelCacheKey{Body: b.serial, Revision: b.geometryRevision, HalfX: halfX, HalfY: halfY}
}

// shadowCacheKey is cacheKey for the retained shadow projection. It shares the
// body's serial and is separated from it by the lane alone, so the two rasters
// of one subject can never answer to each other's slot.
func (b *cachedModelBody) shadowCacheKey(halfX, halfY int32) drawlist.ModelCacheKey {
	if b == nil || b.shadow == nil || b.shadowRevision == 0 {
		return drawlist.ModelCacheKey{}
	}
	return drawlist.ModelCacheKey{
		Body: b.serial, Revision: b.shadowRevision,
		HalfX: halfX, HalfY: halfY, Lane: drawlist.ModelCacheLaneShadow,
	}
}

// cachedGeometryStore is one subject's retained cached-lane packet and the face
// and vertex arenas it addresses. The packet the recorder hands over is frame
// scratch, so it has to be copied out; copying it into arenas the body keeps
// means a subject that rebuilds every frame — which is every mobile subject
// under Enhanced interpolation, because its blended pose genuinely moves
// (docs/DESIGN_GPU_RENDERER.md §13.5) — allocates nothing after its first
// rebuild. ModelGeometry.Clone allocated a packet, four slices and one vertex
// slice per face on every one of those rebuilds, which was the largest single
// item in the recorder's frame.
type cachedGeometryStore struct {
	g                drawlist.ModelGeometry
	faces            []drawlist.ModelFace
	vertices         []drawlist.ModelVertex
	supersample      drawlist.ModelGeometry
	supersampleFaces []drawlist.ModelFace
	supersampleVerts []drawlist.ModelVertex
}

// retainGeometry copies src into this body's arenas and returns the retained
// packet. The cached lane is always a plain face packet with an optional
// supersample of the same shape — that is what borrowModelPacket produces — so
// anything carrying a shadow, children, an outline, a live lane or a reveal
// keeps the general deep copy rather than being silently flattened.
func (b *cachedModelBody) retainGeometry(src *drawlist.ModelGeometry) *drawlist.ModelGeometry {
	return b.store.retain(src)
}

// retain copies src into this store's arenas and returns the retained packet.
// Both retained lanes are plain face packets with an optional supersample of the
// same shape, so anything else keeps the general deep copy.
func (s *cachedGeometryStore) retain(src *drawlist.ModelGeometry) *drawlist.ModelGeometry {
	if src == nil {
		return nil
	}
	if !plainCachedPacket(src) || src.Supersample != nil && !plainCachedPacket(src.Supersample) {
		return src.Clone()
	}
	s.g = *src
	s.faces, s.vertices = copyModelFaces(s.faces, s.vertices, src.Faces, 0, 0)
	s.g.Faces = s.faces
	s.g.LiveFaces, s.g.Outline, s.g.Children = nil, nil, nil
	s.g.Reveal, s.g.Shadow, s.g.Supersample = nil, nil, nil
	if src.Supersample == nil {
		return &s.g
	}
	s.supersample = *src.Supersample
	s.supersampleFaces, s.supersampleVerts =
		copyModelFaces(s.supersampleFaces, s.supersampleVerts, src.Supersample.Faces, 0, 0)
	s.supersample.Faces = s.supersampleFaces
	s.supersample.LiveFaces, s.supersample.Outline, s.supersample.Children = nil, nil, nil
	s.supersample.Reveal, s.supersample.Shadow, s.supersample.Supersample = nil, nil, nil
	s.g.Supersample = &s.supersample
	return &s.g
}

// plainCachedPacket reports whether a packet is faces and placement only, which
// is the whole of what the cached lane retains.
func plainCachedPacket(g *drawlist.ModelGeometry) bool {
	return g.Shadow == nil && g.Reveal == nil && len(g.Children) == 0 &&
		len(g.Outline) == 0 && len(g.LiveFaces) == 0
}

type cachedBodyInputs struct {
	shaded, supersampled bool
	geometrySupersampled bool
	scale                camera.ViewScale
	palette              *palette.Tables
}

func (c *Client) cachedBodyInputs(draw *presentationrender.UnitDraw) cachedBodyInputs {
	if c == nil {
		return cachedBodyInputs{scale: camera.ViewScaleNative}
	}
	input := cachedBodyInputs{palette: c.pal, scale: camera.ViewScaleNative}
	if c.cam != nil {
		input.scale = c.cam.EffectiveScale()
	}
	input.supersampled = c.supersampleModel(draw != nil && draw.Structure)
	input.geometrySupersampled = c.supersampleGeometry()
	if draw != nil {
		for _, piece := range draw.Pieces {
			for _, primitive := range piece.Primitives {
				if primitive.ShadeRow != presentationrender.NoShadeRow {
					input.shaded = true
					return input
				}
			}
		}
	}
	return input
}

// cachedShadowInputs are the retained shadow projection's inputs other than the
// pose (docs/DESIGN_GPU_RENDERER.md §13.12 "Shadows — contract P4").
//
// Established by reading collectShadowPolys and modelShadowGeometry, not
// assumed. The projection walks draw.Pieces, whose local corners are the piece
// transforms applied to the model's authored vertices — WorldVertices carry
// worldPos and shadowLocalVertex subtracts it back off exactly, so the subject's
// position is not an input at all — scales each corner by the model scale
// (scaleModelLocal), and derives its box from those corners. Everything else the
// packet carries is placement, which the rebase supplies per frame.
type cachedShadowInputs struct {
	model string
	scale camera.ViewScale
	// casts folds draw.CastsShadow and the palette the projection requires.
	casts bool
	// doubled is whether the packet carries the §17 supersample lane. Its
	// half-pixel offset is not here: the retained lane is projected without one
	// and the rebase adds this frame's, which the slot key names instead.
	doubled bool
}

func (c *Client) cachedShadowInputs(draw *presentationrender.UnitDraw) cachedShadowInputs {
	in := cachedShadowInputs{scale: camera.ViewScaleNative}
	if c == nil {
		return in
	}
	in.scale, in.doubled = c.modelScale(), c.supersampleGeometry()
	if draw != nil {
		in.casts = draw.CastsShadow && c.pal != nil
		if draw.Model != nil {
			in.model = draw.Model.Name
		}
	}
	return in
}

// samePieceStates reports whether two poses are the same folded piece state. The
// shadow projection is a pure function of the model, this pose and the model
// scale, so an equal pose means an equal projection; comparing len(pieces) small
// structs is what replaces the per-frame walk, transform and winding test.
func samePieceStates(a, b []model.PieceState) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// retainedShadowGeometry is this frame's shadow packet for a subject that has a
// retained body. The projection is reused whenever its own inputs are unchanged,
// and the packet the frame records is the retained one rebased onto this frame's
// placement, carrying the identity the modern executor keys a persistent shadow
// slot on (§13.12 "Shadows — contract P4").
//
// A subject with no retained body — no presentation identity, no scratch arena,
// the direct projected route — keeps the per-frame projection and an empty key.
func (c *Client) retainedShadowGeometry(body *cachedModelBody, draw *presentationrender.UnitDraw) *drawlist.ModelGeometry {
	if c == nil || body == nil || !c.modelScratch.active || !shadowPoseComplete(draw) {
		return c.modelShadowGeometry(draw)
	}
	if in := c.cachedShadowInputs(draw); !body.shadowValid || body.shadowInputs != in ||
		!samePieceStates(body.shadowPose, drawPieceStates(draw)) {
		c.replaceCachedShadow(body, draw, in)
	}
	if body.shadow == nil {
		return nil
	}
	src := body.shadow
	ax, ay, hx, hy := c.shadowPlacement(draw)
	g := c.borrowRebasedModelGeometry(src, src.Width, src.Height, src.OriginX, src.OriginY, ax, ay, hx, hy)
	if g == nil {
		return nil
	}
	g.Cache = body.shadowCacheKey(hx, hy)
	return g
}

// silhouetteShadowGeometry is the Digger and mobile shadow: retail copies the
// finished body image, flattens it to index 0 and erases it at and below the
// buried or submerged key, then blits it at the shadow's placement
// [03 R-REN-03D §1]. The packet carries the body's box and the shadow anchor
// and no faces; the executor reads the body's own raster (cached lane, live
// lane and staged children, as the classic copy of the finished image holds
// them) at that placement (docs/DESIGN_GPU_RENDERER.md §22). Nothing here is
// projected or retained, so an animated mobile's shadow costs the recorder a
// packet header.
func (c *Client) silhouetteShadowGeometry(body *drawlist.ModelGeometry, draw *presentationrender.UnitDraw) *drawlist.ModelGeometry {
	if c == nil || body == nil || draw == nil || !draw.CastsShadow || c.pal == nil {
		return nil
	}
	ax, ay, _, _ := c.shadowPlacement(draw)
	var g *drawlist.ModelGeometry
	if c.modelScratch.active {
		g = &c.borrowPacketScratch().g
	} else {
		g = &drawlist.ModelGeometry{}
	}
	// The slot's face arena stays with the slot: a packet filled here next
	// frame would otherwise grow it again from nothing.
	faces := g.Faces[:0]
	*g = drawlist.ModelGeometry{
		Eligible: true, KeyPlane: true, Silhouette: true,
		Width: body.Width, Height: body.Height, OriginX: body.OriginX, OriginY: body.OriginY,
		AnchorX: ax, AnchorY: ay, Scale: body.Scale,
	}
	g.Faces = faces
	if draw.DiggerClip {
		// The buried half casts no shadow: the Digger key bias puts the model
		// origin at 125 and the comparison is inclusive [R-REN-03D §1].
		g.SilhouetteClip = uint8(diggerEraseThreshold)
	} else if threshold, submerged := waterlineThreshold(c.seaLevel(), draw.WorldPos[1], false); submerged {
		// Only the above-water part of a submerged mobile casts its silhouette.
		g.SilhouetteClip = threshold
	}
	return g
}

// replaceCachedShadow projects the shadow and retains it. The stored lane
// carries no placement at all: the anchor follows the subject's ground point
// frame by frame and the doubled lane's half-pixel offset follows with it, so
// both are supplied by the rebase, exactly as they are for the retained body.
func (c *Client) replaceCachedShadow(body *cachedModelBody, draw *presentationrender.UnitDraw, in cachedShadowInputs) {
	body.shadow = body.shadowStore.retain(c.shadowGeometryAt(draw, 0, 0, 0, 0))
	body.shadowInputs, body.shadowValid = in, true
	body.shadowPose = append(body.shadowPose[:0], drawPieceStates(draw)...)
	// A new projection is a new raster: the executor keys a persistent slot on
	// this revision and must not keep the one it already holds (§13.12).
	body.takeSerial()
	body.shadowRevision++
}

// shadowPoseComplete reports whether this draw's pose is the one its piece
// draws were built from. Keying the projection on the pose alone is sound
// because BuildUnitDrawInto derives every piece's transform, its hidden verdict
// and therefore its world corners from exactly that slice — the world corners
// also carry the subject's position, which the projection subtracts back off
// exactly, so position is not an input. A draw assembled some other way is
// outside that derivation and keeps the per-frame projection.
func shadowPoseComplete(draw *presentationrender.UnitDraw) bool {
	return draw != nil && draw.Model != nil &&
		len(draw.PieceStates) == len(draw.Model.Pieces) &&
		len(draw.Pieces) == len(draw.Model.Pieces)
}

func drawPieceStates(draw *presentationrender.UnitDraw) []model.PieceState {
	if draw == nil {
		return nil
	}
	return draw.PieceStates
}

func cloneModelTarget(src *modelTarget) *modelTarget {
	if src == nil {
		return nil
	}
	out := &modelTarget{
		color: append([]uint8(nil), src.color...), height: append([]uint8(nil), src.height...),
		covered: append([]bool(nil), src.covered...), width: src.width, heightPx: src.heightPx,
		originX: src.originX, originY: src.originY, anchorX: src.anchorX, anchorY: src.anchorY,
		transparent: src.transparent, scale: src.scale,
	}
	if src.height == nil {
		out.height = nil
	}
	return out
}

func (c *Client) cachedBody(id uint64) *cachedModelBody {
	if c.cachedModelBodies == nil {
		c.cachedModelBodies = make(map[uint64]*cachedModelBody)
	}
	return c.cachedModelBodies[id]
}

// pruneCachedModelBodies bounds presentation retention to the committed live
// unit set. InstanceID is the publication identity, rather than a retail pool
// slot: the producer changes it when a slot receives a replacement object.
// Keeping both body and orientation entries on that identity prevents a
// departed subject from lending either to a later subject [I6].
//
// A 3DO feature's body (featureBodyID) is pruned by age instead: the
// committed feature set is thousands of sprites a frame, and a per-frame set
// over it was a measurable allocation, while the retained lane is a pure
// function of inputs the next recording compares, so a stale entry can never
// draw a departed feature — it can only cost its arenas until it ages out.
func (c *Client) pruneCachedModelBodies(cur *frame.Frame) {
	if c == nil {
		return
	}
	if cur == nil {
		clear(c.cachedModelBodies)
		clear(c.modelOrientation)
		return
	}
	live := make(map[uint64]struct{}, len(cur.Units))
	for i := range cur.Units {
		if id := unitPresentationID(cur.Units[i]); id != 0 {
			live[id] = struct{}{}
		}
	}
	for id, body := range c.cachedModelBodies {
		if id&featureBodyMark != 0 {
			if cur.Tick > body.featureSeenTick+featureRetainTicks {
				delete(c.cachedModelBodies, id)
			}
			continue
		}
		if _, ok := live[id]; !ok {
			delete(c.cachedModelBodies, id)
		}
	}
	for id := range c.modelOrientation {
		if _, ok := live[id]; !ok {
			delete(c.modelOrientation, id)
		}
	}
}

func (c *Client) replaceCachedBody(id uint64, v frame.UnitView, draw *presentationrender.UnitDraw, image *modelTarget) {
	if c == nil || draw == nil || image == nil {
		return
	}
	if c.cachedModelBodies == nil {
		c.cachedModelBodies = make(map[uint64]*cachedModelBody)
	}
	inputs := c.cachedBodyInputs(draw)
	c.cachedModelBodies[id] = &cachedModelBody{
		image: cloneModelTarget(image), model: draw.Model.Name,
		cacheRevision: v.CacheRevision, validityRevision: v.CacheValidityRevision,
		structure: draw.Structure, construction: v.BuildRemaining, teamColor: unitTeamColor(v),
		shaded: inputs.shaded, supersampled: inputs.supersampled, scale: inputs.scale, palette: inputs.palette,
	}
}

// replaceCachedGeometry retains only the cached piece lane. Unlike the classic
// image, this is immutable native input; current live pieces are added to a
// frame-owned packet by unitGeometryPair.
func (c *Client) replaceCachedGeometry(id uint64, v frame.UnitView, draw *presentationrender.UnitDraw, geometry *drawlist.ModelGeometry) {
	if c == nil || draw == nil || geometry == nil {
		return
	}
	if c.cachedModelBodies == nil {
		c.cachedModelBodies = make(map[uint64]*cachedModelBody)
	}
	inputs := c.cachedBodyInputs(draw)
	body := c.cachedModelBodies[id]
	if body == nil {
		body = &cachedModelBody{}
		c.cachedModelBodies[id] = body
	}
	// Geometry-only recording has just established a new validity/settings
	// identity for the cached lane. A retained classic image from the prior
	// identity cannot inherit that tag: the next classic consumer must rebuild
	// its own physical-index plane.
	body.image = nil
	body.geometry = body.retainGeometry(geometry)
	// New retained faces are a new raster: the modern executor keys a persistent
	// slot on this pair and must not keep the one it already holds (§13.12).
	body.takeSerial()
	body.geometryRevision++
	body.model, body.cacheRevision, body.validityRevision = draw.Model.Name, v.CacheRevision, v.CacheValidityRevision
	body.structure, body.construction, body.teamColor = draw.Structure, v.BuildRemaining, unitTeamColor(v)
	body.shaded, body.supersampled, body.scale, body.palette = inputs.shaded, inputs.supersampled, inputs.scale, inputs.palette
	body.geometrySupersampled = inputs.geometrySupersampled
}

func (c *Client) cachedGeometryMustRebuild(body *cachedModelBody, v frame.UnitView, draw *presentationrender.UnitDraw, orient *presentationrender.OrientationCache) bool {
	if body == nil || body.geometry == nil || draw == nil || draw.Model == nil || body.model != draw.Model.Name || body.geometry.KeyPlane != draw.KeyPlane {
		return true
	}
	if body.validityRevision != v.CacheValidityRevision || body.structure && body.construction != v.BuildRemaining {
		return true
	}
	if body.teamColor != unitTeamColor(v) {
		return true
	}
	inputs := c.cachedBodyInputs(draw)
	if body.shaded != inputs.shaded || body.geometrySupersampled != inputs.geometrySupersampled || body.scale != inputs.scale || body.palette != inputs.palette {
		return true
	}
	return orient == nil || orient.Model != draw.Model.Name || orient.NeedsRebuild(v.Heading, v.Pitch, v.Bank)
}

func (c *Client) cachedBodyMustRebuild(body *cachedModelBody, v frame.UnitView, draw *presentationrender.UnitDraw, orient *presentationrender.OrientationCache) bool {
	if body == nil || body.image == nil || draw == nil || draw.Model == nil || body.model != draw.Model.Name {
		return true
	}
	if body.validityRevision != v.CacheValidityRevision {
		return true
	}
	if body.structure && body.construction != v.BuildRemaining {
		return true
	}
	if body.teamColor != unitTeamColor(v) {
		return true
	}
	inputs := c.cachedBodyInputs(draw)
	if body.shaded != inputs.shaded || body.supersampled != inputs.supersampled || body.scale != inputs.scale || body.palette != inputs.palette {
		return true
	}
	return orient == nil || orient.Model != draw.Model.Name || orient.NeedsRebuild(v.Heading, v.Pitch, v.Bank)
}

// cachedBodyImage is a fresh, frame-owned copy with the current placement.
// The retained plane is model-local and position never invalidates it.
func (c *Client) cachedBodyImage(body *cachedModelBody, draw *presentationrender.UnitDraw) *modelTarget {
	if c == nil || body == nil || body.image == nil || draw == nil {
		return nil
	}
	anchorX, anchorY := c.modelAnchor(draw)
	out := c.borrowModelImage(body.image.width, body.image.heightPx, body.image.originX, body.image.originY, anchorX, anchorY, body.image.height != nil, body.image.scale)
	// The retained plane is model-local. Copying through screen coordinates
	// would bake the prior frame's placement into a moving unit, so copy its
	// pixels at matching local offsets and assign the current anchor above.
	copy(out.color, body.image.color)
	copy(out.covered, body.image.covered)
	if out.height != nil && body.image.height != nil {
		copy(out.height, body.image.height)
	}
	return out
}
