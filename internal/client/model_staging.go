package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
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
// composition — reveal and nanoframe outline already applied to its own image
// [R-COMP-01 §3] — and the key offset that puts its heights on the carrier's
// scale.
type stagingChild struct {
	model    composedModel
	keyDelta int32
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
	if c.geometryOnlyModels && len(children) != 0 {
		return c.recordCarrierGeometry(v, children)
	}
	if len(children) == 0 {
		return c.drawUnitModel(v, sx, sy)
	}
	carrier, ok := c.composeUnitModelState(v, false, false)
	if !ok {
		// The carrier has no resolvable model of its own. Its children are
		// still real units and still present, each on its own.
		for i := range children {
			c.drawChildModel(children[i])
		}
		return false
	}
	if carrier.direct {
		live, drawn := c.composeDirectLiveModel(carrier.draw, unitTeamColor(v), unitPresentationID(v), modelCursorUnit, carrier.directLane)
		if drawn {
			c.finishModel(live, nil)
		}
		for i := range children {
			c.drawChildModel(children[i])
		}
		return drawn
	}
	if carrier.image == nil {
		return false
	}
	if carrier.image.height == nil {
		// No key plane: the cached image is blitted and each child is
		// rasterized straight to the framebuffer after it, in painter order.
		// The carrier's own live lane is part of that direct pass too; it is
		// separate from attached children but must still follow the cached body
		// [R-REN-03A §4].
		c.finishModel(carrier, nil)
		id := unitPresentationID(v)
		if live, ok := c.composeDirectLiveModel(carrier.draw, unitTeamColor(v), id, modelCursorUnit, presentationrender.PieceLaneLive); ok {
			c.emitModel(pendingModelCommit{m: live, blit: live.image, body: true, trace: true})
		}
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
		staged = append(staged, stagingChild{
			model: child,
			// "the child's world-height difference added to every key it
			// contributes": a child's keys are relative to its own origin, and
			// this is what puts them on the carrier's scale. Both images carry
			// the same key bias, so the bias cancels and only the world
			// difference remains [R-REN-03A §2][R-REN-03A §4].
			keyDelta: int32(int64(children[i].Y)>>16) - int32(int64(v.Y)>>16),
		})
	}
	if len(staged) == 0 {
		c.finalizeModelImage(carrier.image, carrier.draw, v.Owner, modelCursorUnit)
		c.finishModel(carrier, nil)
		return true
	}
	staging := stagingImage(carrier.image, staged, c.borrowModelImage)
	for i := range staged {
		staging.compositeChild(staged[i].model.image, staged[i].keyDelta)
		if carrier.geometry != nil && staged[i].model.geometry != nil {
			carrier.geometry.Children = append(carrier.geometry.Children, drawlist.ModelChild{Geometry: staged[i].model.geometry, KeyDelta: staged[i].keyDelta})
		}
	}
	c.finalizeModelImage(staging, carrier.draw, v.Owner, modelCursorUnit)
	c.finishModel(carrier, staging)
	// A child's parity trace is resolved only now: the pixels it describes are
	// final once the staging image is on the framebuffer.
	for i := range staged {
		c.finishStagedChild(staged[i])
	}
	return true
}

// recordCarrierGeometry follows the classic preparation and shadow/body commit
// order, retaining only geometry. Keyless or missing carriers leave children
// in the ordinary painter path [03 R-REN-03A §4].
func (c *Client) recordCarrierGeometry(v frame.UnitView, children []frame.UnitView) bool {
	carrier := c.unitGeometry(v, false)
	if carrier == nil || !carrier.KeyPlane {
		if carrier != nil {
			c.list.RecordModel(drawlist.Model{Geometry: carrier})
		}
		for _, child := range children {
			if g := c.unitGeometry(child, false); g != nil {
				c.list.RecordModel(drawlist.Model{Geometry: g})
			}
		}
		return carrier != nil
	}
	for _, child := range children {
		g := c.unitGeometry(child, true)
		if g == nil {
			continue
		}
		c.list.RecordModel(drawlist.Model{Geometry: g, ShadowOnly: true})
		delta := int32(int64(child.Y)>>16) - int32(int64(v.Y)>>16)
		carrier.Children = append(carrier.Children, drawlist.ModelChild{Geometry: g, KeyDelta: delta})
	}
	c.list.RecordModel(drawlist.Model{Geometry: carrier})
	return true
}

func (c *Client) unitGeometry(v frame.UnitView, forceKeyPlane bool) *drawlist.ModelGeometry {
	draw, ok := c.unitDrawFor(v)
	if !ok {
		return nil
	}
	if forceKeyPlane {
		draw.KeyPlane = true
	}
	reveal, outline := c.unitNanoframeReveal(v)
	return c.prepareModelGeometry(draw, v.Owner, unitTeamColor(v), unitPresentationID(v), modelCursorUnit, reveal, outline)
}

// finishStagedChild resolves and emits a composited child's parity trace after
// the staging image reaches the framebuffer.
//
// Nothing of the child is drawn here. Retail composes a carried child into its
// own two-plane image, runs the nanoframe reveal AND the outline over that
// image, and only then composites it into the carrier's staging image under
// the key test — so the carrier's geometry occludes the wireframe of a product
// on its plate wherever it occludes the product's body [R-COMP-01 §3]. The
// outline therefore lives in composeModel, and an overdraw here after the blit
// would put it in front of geometry retail puts it behind.
func (c *Client) finishStagedChild(child stagingChild) {
	if c == nil || child.model.image == nil {
		return
	}
	if child.model.raster != nil && child.model.raster.trace != nil {
		// The trace reads c.indexed after the staging body reaches the framebuffer,
		// so it is recorded as a trace-only model command after the carrier's own
		// command; replaying resolves it against the committed pixels exactly as the
		// inline resolve did, without a direct read during the recording pass
		// (WU-1.8).
		c.emitModel(pendingModelCommit{m: child.model, trace: true})
	}
}

// composeUnitModel builds one unit's draw record and composes it, without
// committing. It is drawUnitModel's first half.
func (c *Client) composeUnitModel(v frame.UnitView) (composedModel, bool) {
	return c.composeUnitModelState(v, false, true)
}

// A child forces a two-plane image; a carrier defers its water/digger pass
// until attached children have joined it [03 R-REN-03A §4].
func (c *Client) composeUnitModelState(v frame.UnitView, child, finalPasses bool) (composedModel, bool) {
	draw, ok := c.unitDrawFor(v)
	if !ok {
		return composedModel{}, false
	}
	if child {
		draw.KeyPlane = true
	}
	reveal, outline := c.unitNanoframeReveal(v)
	id := unitPresentationID(v)
	// Slot zero is the pool's null sentinel. Standalone preview/test poses may
	// intentionally use it without publication revisions, so they take the
	// non-retained all-piece adapter rather than pretending to be a live unit.
	if id == 0 || !c.modelScratch.active {
		m, ok := c.composeModelLane(draw, v.Owner, unitTeamColor(v), id, modelCursorUnit, reveal, outline, presentationrender.PieceLaneAll, false)
		if ok && reveal != nil {
			c.outlineModelInto(m.image, m.raster, draw, outline)
		}
		if ok && finalPasses {
			c.finalizeModelImage(m.image, draw, v.Owner, modelCursorUnit)
		}
		return m, ok
	}
	body := c.cachedBody(id)
	missing := body == nil || body.image == nil || body.cacheRevision != v.CacheRevision
	required := draw.Structure || draw.KeyPlane
	orient := c.orientationCache(id)
	if c.cachedBodyMustRebuild(body, v, draw, orient) || missing && required || draw.KeyPlane && body != nil && body.image != nil && body.image.height == nil {
		cached, built := c.composeModelLane(draw, v.Owner, unitTeamColor(v), id, modelCursorUnit, nil, 0, presentationrender.PieceLaneCached, false)
		if !built {
			return composedModel{}, false
		}
		c.replaceCachedBody(id, v, draw, cached.image)
		body = c.cachedBody(id)
		missing = false
		// A settings or script rebuild may still use the retained orientation.
		// Advance its reference only when this draw crossed the orientation gate;
		// otherwise the key would describe angles never rasterized [03 §5.2].
		if draw.NeedsRebuild {
			orient.UpdateKey(draw.Model.Name, v.Heading, v.Pitch, v.Bank)
		}
	}
	if missing || body == nil || body.image == nil {
		// A valid image-less mobile with no required key plane takes the direct
		// all-piece route. The direct framebuffer target is
		// installed by the no-key consumer; until then retain its full-pose body
		// rather than inventing a key plane [03 R-REN-03A §4].
		// Do not advance the orientation reference here: it belongs to an actual
		// cached-body rebuild, and changing it for a direct pose would lose a
		// sequence of sub-threshold turns [03 §5.2].
		return composedModel{draw: draw, direct: true, directLane: presentationrender.PieceLaneAll}, true
	}
	base := c.cachedBodyImage(body, draw)
	if base == nil {
		return composedModel{}, false
	}
	if base.height == nil {
		// Keyless cached/live presentation uses a direct framebuffer live pass.
		// It is selected by drawUnitModel after the cached body commits; return
		// the body here so carrier handling keeps its established fallback.
		return composedModel{image: base, raster: base, draw: draw}, true
	}
	if !child {
		c.revealModelImage(base, draw, reveal, outline)
	}
	// A keyed image stages live geometry against the copied cached plane. A
	// structure under construction keeps every piece in its cached lane and
	// skips this second pass [03 R-REN-03A §4].
	if !(draw.Structure && draw.UnderConstruction) {
		base = c.stageLivePieces(base, draw, unitTeamColor(v), id)

	}
	if child {
		c.revealModelImage(base, draw, reveal, outline)
	}
	if finalPasses {
		c.finalizeModelImage(base, draw, v.Owner, modelCursorUnit)
	}
	return composedModel{image: base, raster: base, draw: draw}, true
}

// stageLivePieces rasterizes into the union itself, at native scale. A live
// texel equal to the image key still writes color and height, erasing a cached
// color behind it; it must not be treated as a keyed child blit
// [03 R-REN-03A §4/§5].
func (c *Client) stageLivePieces(base *modelTarget, draw *presentationrender.UnitDraw, selector teamColor, id uint64) *modelTarget {
	polys := c.collectDrawPolysLane(draw, selector, id, modelCursorUnit, presentationrender.PieceLaneLive)
	if len(polys) == 0 {
		return base
	}
	w, h, ox, oy := modelExtent(polys)
	ax, ay := c.modelAnchor(draw)
	extent := modelTarget{width: w, heightPx: h, originX: ox, originY: oy, anchorX: ax, anchorY: ay}
	stage := stagingImage(base, []stagingChild{{model: composedModel{image: &extent}}}, c.borrowModelImage)
	placeFaces(polys, stage.originX, stage.originY, 1)
	for i := range polys {
		if polys[i].frame != nil {
			c.blitTexturedPolyTarget(stage, &polys[i], polys[i].frame, nil, id)
		} else {
			c.fillPolyTarget(stage, &polys[i], polys[i].color, nil, id)
		}
	}
	return stage
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
	m, ok := c.composeUnitModelState(v, true, true)
	if !ok {
		return composedModel{}, false
	}
	// A carried child's own shadow is still its own subject's business, and it
	// is blitted to the framebuffer rather than into the staging image
	// [R-REN-03D §1]. It is recorded as a shadow-only model command here, before
	// the carrier's own command, so replaying the frame writes the child shadows
	// under the carrier body in the order the direct blits ran and the recording
	// pass touches c.indexed nowhere (WU-1.8). The shadow reads the child's
	// finished image, which the staging composite only reads and never mutates, so
	// a deferred replay sees the same pixels the inline blit did.
	c.emitModel(pendingModelCommit{m: m, shadow: true})
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
	return stagingImage(body, children, newModelImage)
}

// Recording uses a distinct frame-owned image slot; standalone callers keep
// independently allocated storage. Both use the same union and composition.
func stagingImage(body *modelTarget, children []stagingChild, allocate func(int, int, int32, int32, int32, int32, bool, int32) *modelTarget) *modelTarget {
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
	staging := allocate(int(right-left+1), int(bottom-top+1), originX, originY, body.anchorX, body.anchorY, body.height != nil, 1)
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
// The comparison is made at full width — both stored bytes widened, the
// signed height delta added to the child's — and only the STORE narrows: the
// staging plane keeps the low byte of `childKey + heightDelta`, wrapping
// modulo 256 rather than saturating. Retail's composite forms the sum in a
// register and stores a byte add [R-REN-03A §4]. A child far enough above its
// carrier to leave the byte range therefore wins the comparison and then
// stores a small key, so later children and the digger/waterline passes see
// it as low; stock cargo and factory products sit within a few tens of world
// units of their carrier and never reach the boundary.
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
			if child.color[ci] == child.transparent {
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
			t.height[di] = wrapKeyByte(shifted)
			t.color[di], t.covered[di] = child.color[ci], true
		}
	}
}

// wrapKeyByte narrows a shifted key to the plane's byte store the way retail's
// byte add does: the low eight bits, wrapping [R-REN-03A §4]. This replaces a
// saturating clamp that was this client's own choice before the store width
// was traced.
func wrapKeyByte(v int32) uint8 {
	return uint8(v)
}

// unitDrawFor builds one unit's draw record: the piece states, the authored
// gates the composer reads, and the nanoframe inputs. It is the part of
// drawUnitModel that precedes composition.
func (c *Client) unitDrawFor(v frame.UnitView) (*presentationrender.UnitDraw, bool) {
	if c == nil || c.cam == nil {
		return nil, false
	}
	m := c.modelForUnit(v)
	if m == nil || m.compiled == nil {
		return nil, false
	}
	states := c.modelStates(m, v.Pieces)
	draw := presentationrender.BuildUnitDrawInto(m.compiled, states, v.Heading, v.Pitch, v.Bank, v, c.orientationCache(unitPresentationID(v)), c.borrowDrawScratch())
	// BMcode=0 is the structure class [R-RND-02A]; a unit under construction
	// always gets the height plane because the nanoframe reveal reads it
	// [R-REN-03A §2].
	draw.Structure = !v.BMCode
	draw.UnderConstruction = v.BuildRemaining > 0
	draw.KeyPlane = v.ZBuffer || v.BuildRemaining > 0
	// Digger selects its silhouette branch before the structure/mobile split;
	// even a structure-class Digger must pass the vehicle gates [R-REN-03D §1].
	draw.CastsShadow = c.castsModelShadow(v.NoShadow, v.CanHover, v.Floater, draw.Structure && !v.Digger)
	draw.GroundY = c.groundHeightUnder(v.X, v.Z)
	draw.DiggerClip = v.Digger
	if v.Digger {
		// The Digger key bias is applied to every vertex, so the clip
		// threshold and the composed keys stay on one scale [R-REN-03A §8].
		draw.KeyPlane = true
	}
	return draw, true
}
