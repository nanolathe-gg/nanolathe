package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

// The carrier staging image [R-REN-03A §4].
//
// Retail does not blit a carrier and then paint its children over the top.
// Presentation of a unit that has attached children takes one of two paths,
// chosen on whether the carrier's own composition image has a key plane:
//
//   - **No key plane.** The cached image is blitted, and then every live piece
//     and every attached child is rasterized straight to the framebuffer in
//     painter order. Nothing resolves per pixel because there is no plane to
//     resolve against.
//   - **Key plane present.** A staging image is prepared whose box is the union
//     of the carrier's own box with the boxes of all its attached children,
//     offset by each child's world position relative to the carrier. The cached
//     image is copied into it, both planes. Each child is then composed into its
//     own two-plane image and composited into the staging image with the key
//     test, at the child's pixel offset and with the child's world-height
//     difference added to every key it contributes. The waterline and digger
//     passes run over the staging image, and the staging image is blitted once.
//
// The consequence is per-pixel occlusion between a carrier and its cargo: a
// transport's hull can stand in front of the unit it is carrying, and a
// factory's plate in front of the nanoframe on it, because the two resolve
// against one height plane rather than by draw order.

// stagingChild is one attached child ready to be composited: its finished
// composition, the key offset that puts its heights on the carrier's scale, and
// the nanoframe overdraw that still has to reach the framebuffer after the
// staging image is blitted.
type stagingChild struct {
	model    composedModel
	keyDelta int32
	reveal   *presentationrender.NanoframeReveal
	outline  uint8
}

// composeCarrier presents one unit together with its attached children.
//
// It returns whether the CARRIER itself was drawn, which is what the caller
// uses to decide selection chrome; a child that fails to compose is skipped
// exactly as it would be on its own.
func (c *Client) composeCarrier(v frame.UnitView, sx, sy int32, children []frame.UnitView) bool {
	if c == nil {
		return false
	}
	if len(children) == 0 {
		return c.drawUnitModel(v, sx, sy)
	}
	carrier, ok := c.composeUnitModel(v)
	if !ok {
		// The carrier has no resolvable model of its own. Its children are
		// still real units and still present, each on its own.
		for i := range children {
			c.drawChildModel(children[i])
		}
		return false
	}
	reveal, outline := c.unitNanoframeReveal(v)
	if carrier.image.height == nil {
		// No key plane: the cached image is blitted and each child is
		// rasterized straight to the framebuffer after it, in painter order
		// [R-REN-03A §4].
		c.finishModel(carrier, nil, reveal, outline)
		for i := range children {
			c.drawChildModel(children[i])
		}
		return true
	}
	// Key plane present: compose each child into its own image, then composite
	// the lot into one staging image and blit that [R-REN-03A §4].
	staged := make([]stagingChild, 0, len(children))
	for i := range children {
		child, ok := c.composeChildModel(children[i])
		if !ok {
			continue
		}
		reveal, outline := c.unitNanoframeReveal(children[i])
		staged = append(staged, stagingChild{
			model:   child,
			reveal:  reveal,
			outline: outline,
			// "the child's world-height difference added to every key it
			// contributes": a child's keys are relative to its own origin, and
			// this is what puts them on the carrier's scale. Both images carry
			// the same key bias, so the bias cancels and only the world
			// difference remains [R-REN-03A §2][R-REN-03A §4].
			keyDelta: int32(int64(children[i].Y)>>16) - int32(int64(v.Y)>>16),
		})
	}
	if len(staged) == 0 {
		c.finishModel(carrier, nil, reveal, outline)
		return true
	}
	staging := newStagingImage(carrier.image, staged)
	for i := range staged {
		staging.compositeChild(staged[i].model.image, staged[i].keyDelta)
	}
	c.finishModel(carrier, staging, reveal, outline)
	// A child's nanoframe outline is an overdraw on the framebuffer, not part
	// of its composition image [03 §5.2], so it has to follow the staging blit
	// that would otherwise cover it. Its trace is resolved here for the same
	// reason: the pixels it describes are only final now.
	for i := range staged {
		c.finishStagedChild(staged[i])
	}
	return true
}

// finishStagedChild runs the parts of a child's presentation that belong after
// the staging image reaches the framebuffer: the nanoframe outline overdraw and
// the parity trace resolve.
//
// TODO(question): whether retail's nanoframe outline is an overdraw on the
// framebuffer or a pass over the composition image. It decides whether a
// carrier's geometry can occlude the wireframe of the product on its plate.
// This client draws the outline in framebuffer space [03 §5.2], so under the
// staging path it must follow the blit or be covered by it — which is what
// this does. If the outline turns out to belong to the composition image, it
// moves inside composeModel and the key test occludes it like any other pixel.
// The decider is a writer trace of the outline pass's target image.
func (c *Client) finishStagedChild(child stagingChild) {
	if c == nil || child.model.image == nil {
		return
	}
	if child.reveal != nil {
		c.drawModelOutline(child.model.draw, child.outline, child.model.raster)
	}
	if child.model.raster != nil && child.model.raster.trace != nil {
		child.model.raster.trace.resolve(child.model.raster, c.indexed, c.width, c.height)
		child.model.raster.trace.emit(c.rendererTraceSink, c.rendererTraceFilter)
	}
}

// composeUnitModel builds one unit's draw record and composes it, without
// committing. It is drawUnitModel's first half.
func (c *Client) composeUnitModel(v frame.UnitView) (composedModel, bool) {
	draw, ok := c.unitDrawFor(v)
	if !ok {
		return composedModel{}, false
	}
	reveal, _ := c.unitNanoframeReveal(v)
	return c.composeModel(draw, v.Owner, unitPresentationID(v), modelCursorUnit, reveal)
}

// composeChildModel composes one attached child for the staging path.
//
// The child is given a key plane whether or not its own definition authors
// one: [R-REN-03A §4] step 2 composes each attached child into "its own
// two-plane image" before compositing it, and a child with no key plane would
// contribute no heights for the staging key test to resolve against.
func (c *Client) composeChildModel(v frame.UnitView) (composedModel, bool) {
	if c == nil || c.cam == nil {
		return composedModel{}, false
	}
	draw, ok := c.unitDrawFor(v)
	if !ok {
		return composedModel{}, false
	}
	draw.KeyPlane = true
	reveal, _ := c.unitNanoframeReveal(v)
	m, ok := c.composeModel(draw, v.Owner, unitPresentationID(v), modelCursorUnit, reveal)
	if !ok {
		return composedModel{}, false
	}
	// A carried child's own shadow is still its own subject's business, and it
	// is blitted to the framebuffer rather than into the staging image
	// [R-REN-03D §1].
	c.drawModelShadow(draw, m.image)
	return m, true
}

// drawChildModel presents one attached child on its own, which is the painter
// path of [R-REN-03A §4] and the fallback whenever no staging image is built.
func (c *Client) drawChildModel(v frame.UnitView) {
	if c == nil || c.cam == nil {
		return
	}
	sx, sy := c.cam.WorldToScreen(v.X, v.Y, v.Z)
	c.drawUnitModel(v, sx-camera.OriginX, sy-camera.OriginY)
}

// newStagingImage allocates the staging image and copies the carrier's cached
// image into it, both planes [R-REN-03A §4].
//
// The box is the union of the carrier's own box with every child's, taken in
// framebuffer space: each image already records where its model origin lands on
// screen, so "offset by the child's world position relative to the parent" is
// the offset those anchors already carry. Uniting the projected rectangles is
// that statement without a second copy of the projection.
func newStagingImage(body *modelTarget, children []stagingChild) *modelTarget {
	if body == nil {
		return nil
	}
	left, top := body.screenX(0), body.screenY(0)
	right, bottom := body.screenX(int32(body.width)-1), body.screenY(int32(body.heightPx)-1)
	for i := range children {
		ch := children[i].model.image
		if ch == nil || ch.width == 0 || ch.heightPx == 0 {
			continue
		}
		if l := ch.screenX(0); l < left {
			left = l
		}
		if t := ch.screenY(0); t < top {
			top = t
		}
		if r := ch.screenX(int32(ch.width) - 1); r > right {
			right = r
		}
		if b := ch.screenY(int32(ch.heightPx) - 1); b > bottom {
			bottom = b
		}
	}
	// The staging image keeps the carrier's anchor, so its origin is whatever
	// puts the union's top-left corner at image pixel (0,0).
	originX := body.anchorX - left
	originY := body.anchorY - top
	staging := newModelImage(int(right-left+1), int(bottom-top+1), originX, originY, body.anchorX, body.anchorY, body.height != nil, 1)
	staging.copyFrom(body)
	return staging
}

// copyFrom copies one image's covered pixels into this one, both planes, at the
// screen position each already carries. It is the "cached image is copied or
// re-blitted into it" step of [R-REN-03A §4]: an unconditional copy, with no
// key test, because the staging image is empty underneath.
func (t *modelTarget) copyFrom(src *modelTarget) {
	if t == nil || src == nil {
		return
	}
	for sy := 0; sy < src.heightPx; sy++ {
		iy := t.imageY(src.screenY(int32(sy)))
		if iy < 0 || iy >= int32(t.heightPx) {
			continue
		}
		row := int(iy) * t.width
		base := sy * src.width
		for sx := 0; sx < src.width; sx++ {
			si := base + sx
			if !src.covered[si] {
				continue
			}
			ix := t.imageX(src.screenX(int32(sx)))
			if ix < 0 || ix >= int32(t.width) {
				continue
			}
			di := row + int(ix)
			t.color[di], t.covered[di] = src.color[si], true
			if t.height != nil && src.height != nil {
				t.height[di] = src.height[si]
			}
		}
	}
}

// compositeChild composites one child's finished image into the staging image
// with the key test [R-REN-03A §4] step 2.
//
// A child pixel is written when it is not the child image's transparent index
// and `stagingKey <= childKey + heightDelta`. That is the same comparison the
// span writers apply within one image, which is why it is the image's own
// admission and not a second rule: what makes it a cross-unit test is the
// height delta, which puts the child's keys on the carrier's scale.
//
// TODO(question): whether retail's key plane wraps or saturates when a child's
// shifted key leaves the byte range. The comparison here is made in int32 and
// only the stored byte is clamped, so a child far above its carrier stays in
// front rather than wrapping behind it. Stock cargo and factory products sit
// within a few tens of world units of their carrier, so no reachable
// configuration distinguishes the two; the decider is the plane's own store
// width in the composition path.
func (t *modelTarget) compositeChild(child *modelTarget, keyDelta int32) {
	if t == nil || child == nil {
		return
	}
	for cy := 0; cy < child.heightPx; cy++ {
		iy := t.imageY(child.screenY(int32(cy)))
		if iy < 0 || iy >= int32(t.heightPx) {
			continue
		}
		row := int(iy) * t.width
		base := cy * child.width
		for cx := 0; cx < child.width; cx++ {
			ci := base + cx
			if !child.covered[ci] {
				continue // the child image's own transparent index
			}
			ix := t.imageX(child.screenX(int32(cx)))
			if ix < 0 || ix >= int32(t.width) {
				continue
			}
			di := row + int(ix)
			if t.height == nil {
				t.color[di], t.covered[di] = child.color[ci], true
				continue
			}
			shifted := keyDelta
			if child.height != nil {
				shifted += int32(child.height[ci])
			}
			if int32(t.height[di]) > shifted {
				continue
			}
			t.height[di] = clampKeyByte(shifted)
			t.color[di], t.covered[di] = child.color[ci], true
		}
	}
}

// clampKeyByte narrows a shifted key to the plane's byte store. See the
// TODO(question) on compositeChild: the clamp is this client's choice at a
// boundary stock content does not reach, not a traced retail behaviour.
func clampKeyByte(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// unitDrawFor builds one unit's draw record: the piece states, the authored
// gates the composer reads, and the nanoframe inputs. It is the part of
// drawUnitModel that precedes composition.
func (c *Client) unitDrawFor(v frame.UnitView) (*presentationrender.UnitDraw, bool) {
	if c == nil || c.cam == nil {
		return nil, false
	}
	m := c.unitModelFor(v.Model)
	if m == nil || m.compiled == nil {
		return nil, false
	}
	states := c.modelStates(m, v.Pieces)
	draw := presentationrender.BuildUnitDraw(m.compiled, states, v.Heading, v.Pitch, v.Bank, v, c.orientationCache(unitPresentationID(v)))
	// BMcode=0 is the structure class [R-RND-02A]; a unit under construction
	// always gets the height plane because the nanoframe reveal reads it
	// [R-REN-03A §2].
	draw.Structure = !v.BMCode
	draw.KeyPlane = v.ZBuffer || v.BuildRemaining > 0
	draw.CastsShadow = c.castsModelShadow(v.NoShadow, v.CanHover, v.Floater)
	draw.GroundY = c.groundHeightUnder(v.X, v.Z)
	draw.DiggerClip = v.Digger
	if v.Digger {
		// The Digger key bias is applied to every vertex, so the clip
		// threshold and the composed keys stay on one scale [R-REN-03A §8].
		draw.KeyPlane = true
	}
	return draw, true
}
