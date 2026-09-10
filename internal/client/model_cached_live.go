package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

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
	if src == nil {
		return nil
	}
	if !plainCachedPacket(src) || src.Supersample != nil && !plainCachedPacket(src.Supersample) {
		return src.Clone()
	}
	s := &b.store
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
	for id := range c.cachedModelBodies {
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
