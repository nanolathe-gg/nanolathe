package client

import (
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

// cachedModelBody is the persistent, unfinalized cached-piece composition.
// Waterline, Digger and child staging operate on a fresh frame-local copy, as
// their inputs may change while the cached local body remains valid
// [03 R-REN-03A §4].
type cachedModelBody struct {
	image            *modelTarget
	model            string
	cacheRevision    uint64
	validityRevision uint64
	structure        bool
	construction     float32
	// These are Nanolathe's retained-image memoization inputs. They keep a
	// cached physical-index raster from crossing a presentation setting or
	// palette installation; they are not retail's script-driven validity word.
	shaded, supersampled bool
	scale                float32
	palette              *palette.Tables
}

type cachedBodyInputs struct {
	shaded, supersampled bool
	scale                float32
	palette              *palette.Tables
}

func (c *Client) cachedBodyInputs(draw *presentationrender.UnitDraw) cachedBodyInputs {
	if c == nil {
		return cachedBodyInputs{scale: 1}
	}
	input := cachedBodyInputs{palette: c.pal, scale: 1}
	if c.cam != nil {
		input.scale = c.cam.EffectiveScale()
	}
	input.supersampled = c.supersampleModel(draw != nil && draw.Structure)
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
		structure: draw.Structure, construction: v.BuildRemaining,
		shaded: inputs.shaded, supersampled: inputs.supersampled, scale: inputs.scale, palette: inputs.palette,
	}
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
