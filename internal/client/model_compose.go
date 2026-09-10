package client

// Composing one model into an indexed image: the primitive dispatch, the
// per-face paint test, the collected polygons and their extent [03 §2.4]
// [03 R-REN-03A].

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

type modelPrimitiveMode uint8

const (
	modelPrimitiveSkip modelPrimitiveMode = iota
	modelPrimitiveFlat
	modelPrimitiveTexture
)

// modelPrimitiveDispatch reproduces the retail per-primitive dispatch: bit 0
// of the authored IsColored field selects the flat polygon filler, which takes
// an explicit vertex count and draws any arity, and only when that bit is
// clear does the renderer require a quad before it binds a texture
// [R-REN-03A §5][fmt 3do].
//
// Bit 0 is also what makes editor garbage in IsColored harmless: across the
// 608 stock models it is set on exactly the 6,598 primitives with no texture
// name and clear on exactly the 43,845 with one, so the garbage values that
// co-occur with a texture are all even [R-REN-03A §5]. An untextured face with
// the bit clear is the format's "clear" primitive — it reaches the quad mapper
// with no texture bound and the mapper draws nothing [fmt 3do].
func modelPrimitiveDispatch(pr *presentationrender.PrimitiveDraw, resolved bool) modelPrimitiveMode {
	if pr.IsColored&1 != 0 {
		return modelPrimitiveFlat // flat filler, any vertex count [R-REN-03A §5]
	}
	if len(pr.VertexIndices) != 4 {
		return modelPrimitiveSkip // textured faces are quads only [R-REN-03A §5]
	}
	if pr.TextureName == "" {
		// The format's "clear" primitive: it reaches the quad mapper with no
		// texture bound, and the mapper's null guard draws nothing [fmt 3do].
		return modelPrimitiveSkip
	}
	if !resolved {
		// A texture miss is rewritten to the gray placeholder at load; it is
		// already a quad by the test above [03 §2.4.1].
		return modelPrimitiveFlat
	}
	return modelPrimitiveTexture
}

// modelFacePaints applies retail's winding cull to one authored primitive
// [R-RAST-01 §1] step 7.
//
// Retail has no normal test, no signed-area test and no backface flag. What
// removes a back face is the scan converter itself: from the polygon's topmost
// corner it walks decreasing indices as the *left* edge chain and increasing
// indices as the *right* chain, and a span writer runs only where `xr > xl`.
// A convex face whose projected corners run clockwise in index order (screen Y
// increasing downward) therefore paints, and a counter-clockwise one produces
// an empty span on every row and paints nothing. The doubled signed area of
// the projected corner ring is exactly the sign of that comparison for a
// convex face, and it is positive for a clockwise ring in a Y-down frame.
//
// This is why it matters here rather than as a micro-optimisation. Authored
// 3DO order is counter-clockwise seen from outside the piece (measured: 2,029
// pieces outward against 90 inward across the 608 stock models, [fmt 3do]
// "Unknowns and caveats"), and the composition projection negates the
// model-relative Z ([R-RAST-01 §2]), a reflection that reverses winding — so
// outward faces arrive clockwise and paint while the inward faces behind them
// are dropped. Drawing both sides let an interior face that shares a plane
// with the skin win the height key's equal-key tie-break, which is drawn later
// wins ([R-REN-03A §2]): on `ARMCK` the team-coloured inner face of each
// shoulder flap is coplanar with the grey ribbed outer face and index-ordered
// after it, so the whole flap came out solid team colour.
//
// The corners are the projection the face is actually rasterized with, taken
// before the presentation zoom of scaleModelLocal, so the test is evaluated on
// exactly the quantity retail's chains compare. The shadow pass passes its own
// quarter-shear projection for the same reason.
//
// The body raster no longer needs this predicate: polyScan performs the real
// per-scanline comparison, so a folded projection paints exactly the rows where
// its right chain is still right of its left one instead of being dropped
// whole. What remains here is the convex summary of the same comparison, used
// by the two passes that do not run a span writer at all — the nanoframe
// wireframe, which draws edges rather than spans, and the shadow
// rasterization's own quarter-shear pass.
func modelFacePaints(vertices [][3]numeric.Fixed, indices []uint16, origin [3]numeric.Fixed) bool {
	return facePaints(vertices, indices, origin, modelLocalVertex)
}

// facePaints is modelFacePaints over an arbitrary vertex projection, so the
// body and shadow passes share one cull evaluated on their own corners.
func facePaints(vertices [][3]numeric.Fixed, indices []uint16, origin [3]numeric.Fixed, project func(v, origin [3]numeric.Fixed) (int32, int32, int32)) bool {
	n := len(indices)
	if n < 3 {
		// A two-corner flat primitive makes both chains the same single edge,
		// so `xr == xl` on every row and nothing is drawn [R-RAST-01 §1] step 7.
		return false
	}
	for _, vi := range indices {
		if int(vi) >= len(vertices) {
			return false // malformed primitive suppresses the whole face [fmt 3do]
		}
	}
	var area int64
	px, py, _ := project(vertices[indices[n-1]], origin)
	for _, vi := range indices {
		x, y, _ := project(vertices[vi], origin)
		area += int64(px)*int64(y) - int64(x)*int64(py)
		px, py = x, y
	}
	return area > 0
}

// teamColor is the per-owner colour byte that selects a LOGOS frame. It is not
// the player slot: ownership remains a separate input for waterline and other
// presentation predicates [R-RAST-01 §3][R-REN-03A §4].
type teamColor struct {
	index uint8
	known bool
}

func unitTeamColor(v frame.UnitView) teamColor {
	return teamColor{index: v.OwnerColor, known: v.OwnerColorKnown}
}

// teamTextureFrame applies the LOGOS selector directly. A missing player
// colour, an index beyond the entry, or an absent frame is a missing texture,
// so the textured quad contributes no pixels; no slot or modulo fallback is
// part of this lookup [R-RAST-01 §3].
func teamTextureFrame(ref texRef, selector teamColor) *formats.GAFFrame {
	if !selector.known || ref.entry == nil || int(selector.index) >= len(ref.entry.Frames) {
		return nil
	}
	return ref.entry.Frames[selector.index].Frame
}

// collectDrawPolys adapts canonical render records to the indexed framebuffer.
// It owns no hierarchy math: units, features, and projectiles all pass through
// render.UnitDraw [03 §2.4][03 §5.2]. One authored primitive becomes one face
// at its authored arity and corner order; there is no fan triangulation because
// retail's scan converter derives its chains from the index ring. The winding
// cull remains inside that scan conversion [R-RAST-01 §1].
//
// The selector is deliberately distinct from owner: model geometry does not
// need ownership, and keeping the values separate prevents a player slot from
// becoming a colour.
func (c *Client) collectDrawPolys(draw *presentationrender.UnitDraw, selector teamColor, id uint64, kind uint8) []screenPoly {
	return c.collectDrawPolysLane(draw, selector, id, kind, presentationrender.PieceLaneAll)
}

// collectDrawPolysLane resolves only the requested cached/live body lane.
// The direct live path chooses its own target and projection; this helper only
// preserves the published piece order and material resolution [03 R-REN-03A §4].
func (c *Client) collectDrawPolysLane(draw *presentationrender.UnitDraw, selector teamColor, id uint64, kind uint8, lane presentationrender.PieceLane) []screenPoly {
	return c.collectDrawPolysLaneProjected(draw, selector, id, kind, lane, false)
}

// collectDrawPolysLaneProjected keeps the cache-lane material walk shared while
// selecting either the local cached projection or direct live projection.
func (c *Client) collectDrawPolysLaneProjected(draw *presentationrender.UnitDraw, selector teamColor, id uint64, kind uint8, lane presentationrender.PieceLane, direct bool) []screenPoly {
	if c == nil || c.cam == nil || draw == nil || draw.Model == nil {
		return nil
	}
	faces, corners := 0, 0
	for i := range draw.Pieces {
		prims := draw.Pieces[i].Primitives
		faces += len(prims)
		for j := range prims {
			corners += len(prims[j].VertexIndices)
		}
	}
	scratch := c.borrowPolys(faces, corners)

	// Retail walks the piece list last-to-first. Because the height-key test
	// admits equal keys, draw order is the tie-break, so this direction is what
	// makes piece 0 win every tie against every later piece — reversing it
	// lifts a factory's build plate through its roof and lets an opened solar
	// collector's panels swallow the column they intersect [R-REN-03A §3].
	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		piece := &draw.Pieces[pi]
		if !lane.Includes(piece.DontCache, draw.UnderConstruction) {
			continue
		}
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		for pri := range piece.Primitives {
			pr := &piece.Primitives[pri]
			if draw.Model.Pieces[pi].Selection && pri == 0 {
				continue // selection plate is retained by the model but never rasterized [03 §2.4.1]
			}
			n := len(pr.VertexIndices)
			if n < 3 {
				continue
			}
			ref, textured := c.resolveModelTexture(pr.TextureName)
			mode := modelPrimitiveDispatch(pr, textured)
			if mode == modelPrimitiveSkip {
				continue
			}
			color := uint8(pr.ColorIndex & 0xff)
			if pr.TextureName != "" && !textured {
				color = 0xd1 // unresolved authored texture is the established flat miss [03 §2.4.1]
			}
			var texFrame *formats.GAFFrame
			if mode == modelPrimitiveTexture {
				switch ref.kind {
				case texAnimated:
					if c.modelTextures != nil {
						texFrame = c.modelTextures.animatedFrame(draw.Model, pi, pri, ref)
						if texFrame == nil {
							continue
						}
						break
					}
					if kind == modelCursorFeature && id == 0 {
						// Same publication gap as the sprite path above:
						// FeatureView carries no stable identity, so an
						// animated model texture would share cursor zero
						// across every feature. Suppressed, not shared.
						continue
					}
					texFrame = c.modelAnimatedFrameAt(ref, kind, id, piece.SourceIndex, pri)
				case texTeam:
					if kind == modelCursorProjectile {
						// The standalone effect renderer has no player-colour
						// branch. It reads the model binder's ordinary cursor,
						// which remains at its initial frame zero for a LOGOS
						// entry because those cursors are not advanced [R-COMP-02 §6].
						if ref.entry != nil && len(ref.entry.Frames) > 0 {
							texFrame = ref.entry.Frames[0].Frame
						}
					} else {
						texFrame = teamTextureFrame(ref, selector)
					}
				default:
					texFrame = ref.frame
				}
				if texFrame == nil {
					continue
				}
			}
			// modelPrimitiveDispatch has already rejected any textured face
			// that is not a quad: retail's quad mapper is hard-wired to four
			// corners and there is no textured n-gon path [R-REN-03A §5].

			valid := true
			for _, vi := range pr.VertexIndices {
				if int(vi) >= len(piece.WorldVertices) {
					valid = false
					break
				}
			}
			if !valid {
				continue // malformed primitive suppresses the whole face [fmt 3do]
			}
			poly := scratch.next(n)
			poly.color, poly.frame = color, texFrame
			// The live-piece invocation is the separate unshaded renderer entry.
			// A BMcode=0 body may have prepared SHD rows for its cached half, but
			// its DontCache pieces still bypass SHD here [R-RND-02A][03 R-REN-03A §4].
			poly.useSHD = lane != presentationrender.PieceLaneLive && pr.ShadeRow != presentationrender.NoShadeRow
			poly.candidate, poly.piece, poly.primitive, poly.texture = uint32(len(scratch.polys)-1), piece.SourceIndex, pri, pr.TextureName
			if texFrame != nil && c.rendererTraceSink != nil {
				poly.frameState = RendererValueAvailable
				if ref.kind == texStatic {
					poly.frameIndex = 0
				} else if ref.entry != nil {
					for fi := range ref.entry.Frames {
						if ref.entry.Frames[fi].Frame == texFrame {
							poly.frameIndex = fi
							break
						}
					}
				}
			}
			for corner, vi := range pr.VertexIndices {
				v := piece.WorldVertices[vi]
				// Local cached projection floors model-relative coordinates before
				// placement. Direct live projection adds the world offset first;
				// the two differ by a pixel for fractional coordinates
				// [03 R-RAST-01 §2].
				var lx, ly, ry int32
				if direct {
					lx, ly = c.modelDirectVertex(v, draw.WorldPos)
					ry = int32(v[1].Floor())
				} else {
					lx, ly, ry = modelLocalVertex(v, draw.WorldPos)
					lx, ly = c.scaleModelLocal(lx, ly)
				}
				poly.x[corner], poly.y[corner] = lx, ly
				poly.oddHeight[corner] = ry&1 != 0
				poly.attr[spanKey][corner] = modelHeightKey(v[1].Sub(draw.WorldPos[1]), draw.DiggerClip)
				if poly.useSHD {
					// The DONT_SHADE pin is the one retail value this lane can
					// hold before a corner's own row is known [R-RAST-01 §5].
					row := int32(presentationrender.SHDIdentityRow)
					if corner < len(pr.ShadeRows) {
						row = int32(pr.ShadeRows[corner])
					}
					poly.attr[spanRow][corner] = row
				}
			}
			if texFrame != nil {
				// Retail's quad mapper defaults the four corners to
				// (0,0) (w-1,0) (w-1,h-1) (0,h-1) in vertex-index order, with
				// the clamp built into the default rather than applied after
				// sampling [R-RAST-01 §1][R-REN-03A §5].
				u := [4]int32{0, int32(texFrame.Width) - 1, int32(texFrame.Width) - 1, 0}
				vv := [4]int32{0, 0, int32(texFrame.Height) - 1, int32(texFrame.Height) - 1}
				for corner := 0; corner < n && corner < 4; corner++ {
					poly.attr[spanU][corner], poly.attr[spanV][corner] = u[corner], vv[corner]
				}
			}
		}
	}
	return scratch.polys
}

// modelDirectVertex projects a live piece straight into the framebuffer. The
// model-space Z component is mirrored when it becomes a world point, and the
// camera helper then floors the complete 16.16 sums [03 R-RAST-01 §2].
func (c *Client) modelDirectVertex(v [3]numeric.Fixed, world [3]numeric.Fixed) (int32, int32) {
	if c == nil || c.cam == nil {
		return 0, 0
	}
	localZ := v[2].Sub(world[2])
	worldZ := world[2].Sub(localZ)
	sx, sy := c.cam.WorldToScreen(v[0], v[1], worldZ)
	return sx - camera.OriginX, sy - camera.OriginY
}

func (c *Client) resolveModelTexture(name string) (texRef, bool) {
	if c == nil {
		return texRef{}, false
	}
	return resolveTextureRef(c.texIndex, c.logoIndex, c.modelNameKey(name))
}

// modelExtent measures the composition image from the collected faces: retail
// walks every visible piece's vertices, tracks the min and max of the projected
// offsets with the extrema seeded at zero so the box always contains the model
// origin, then adds a two-pixel margin on every side [R-REN-03A §1].
func modelExtent(polys []screenPoly) (width, height int, originX, originY int32) {
	var minX, minY, maxX, maxY int32 // seeded at the model origin, not at a vertex
	for i := range polys {
		xs, ys := polys[i].x, polys[i].y
		for k := range xs {
			x, y := xs[k], ys[k]
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	originX = modelTargetMargin - minX
	originY = modelTargetMargin - minY
	return int(maxX - minX + 2*modelTargetMargin), int(maxY - minY + 2*modelTargetMargin), originX, originY
}

// placeFaces moves every corner into image space. At the supersample scale the
// shear is `(2z) - y` rather than `2*(z - (y>>1))`, which is one pixel lower
// exactly when the corner's model-relative height is odd [R-REN-03A §6].
func placeFaces(polys []screenPoly, originX, originY, scale int32) {
	for i := range polys {
		for k := range polys[i].x {
			if scale == 2 {
				polys[i].x[k] = 2 * (polys[i].x[k] + originX)
				polys[i].y[k] = 2*(polys[i].y[k]+originY) - boolToInt32(polys[i].oddHeight[k])
				continue
			}
			polys[i].x[k] += originX
			polys[i].y[k] += originY
		}
	}
}

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// supersampleModel reports whether this subject takes retail's structure
// anti-aliasing: the Anti_Alias display option on, and the unit's authored
// class bit saying structure. Mobile units are rasterized at 1x whatever the
// option says, which is why buildings read smoother than units in retail
// [R-REN-03A §6].
func (c *Client) supersampleModel(structure bool) bool {
	return c != nil && c.antiAlias && structure && c.pal != nil
}

// composedModel is one finished composition image and the bookkeeping the
// caller still needs after it: the raster the model actually went into (the
// doubled scratch while anti-aliasing, which is what the outline and the trace
// read) and the draw record itself.
type composedModel struct {
	image      *modelTarget
	raster     *modelTarget
	draw       *presentationrender.UnitDraw
	geometry   *drawlist.ModelGeometry
	directLane presentationrender.PieceLane
	direct     bool
}

// composeDirectLiveModel rasterizes one live lane onto a native framebuffer
// target. It has no key plane; the shared exclusive fill limits leave the
// final framebuffer row and column untouched [03 R-RAST-01 §1/§2].
func (c *Client) composeDirectLiveModel(draw *presentationrender.UnitDraw, selector teamColor, id uint64, kind uint8, lane presentationrender.PieceLane) (composedModel, bool) {
	polys := c.collectDrawPolysLaneProjected(draw, selector, id, kind, lane, true)
	if len(polys) == 0 {
		return composedModel{}, false
	}
	recW, recH := c.recordExtent()
	target := c.borrowModelImage(recW, recH, 0, 0, 0, 0, false, 1)
	for i := range polys {
		if polys[i].frame != nil {
			c.blitTexturedPolyTarget(target, &polys[i], polys[i].frame, nil, id)
			continue
		}
		c.fillPolyTarget(target, &polys[i], polys[i].color, nil, id)
	}
	return composedModel{image: target, raster: target, draw: draw, direct: true, directLane: lane}, true
}

// composeDirectDebrisModel is the bounded classic counterpart of the direct
// standalone model call. Projection happens before rebasing, so fractional
// world coordinates follow the direct truncation path; the resulting target
// covers only its clipped projected-vertex box and replays at scale one
// [03 R-COMP-02 §6][03 R-RAST-01 §2].
func (c *Client) composeDirectDebrisModel(draw *presentationrender.UnitDraw, selector teamColor, id uint64) (composedModel, bool) {
	if !c.directModelOriginVisible(draw) {
		return composedModel{}, false
	}
	polys := c.collectDrawPolysLaneProjected(draw, selector, id, modelCursorDebris, presentationrender.PieceLaneAll, true)
	recW, recH := c.recordExtent()
	minX, minY, maxX, maxY, ok := directProjectedBounds(polys, int32(recW), int32(recH))
	if !ok {
		return composedModel{}, false
	}
	placeFaces(polys, -minX, -minY, 1)
	target := c.borrowModelImage(int(maxX-minX+1), int(maxY-minY+1), -minX, -minY, 0, 0, false, 1)
	// Direct polygon coordinates were already in framebuffer space, so Original
	// detail mode must not scale the committed image a second time.
	target.blit = camera.ViewScaleNative
	for i := range polys {
		if polys[i].frame != nil {
			c.blitTexturedPolyTarget(target, &polys[i], polys[i].frame, nil, id)
			continue
		}
		c.fillPolyTarget(target, &polys[i], polys[i].color, nil, id)
	}
	return composedModel{image: target, raster: target, draw: draw, direct: true, directLane: presentationrender.PieceLaneAll}, true
}

func (c *Client) directModelOriginVisible(draw *presentationrender.UnitDraw) bool {
	if c == nil || c.cam == nil || draw == nil {
		return false
	}
	recW, recH := c.recordExtent()
	if recW <= 0 || recH <= 0 {
		return false
	}
	x, y := c.modelAnchor(draw)
	return x >= 0 && x <= int32(recW) && y >= 0 && y <= int32(recH)
}

func directProjectedBounds(polys []screenPoly, width, height int32) (minX, minY, maxX, maxY int32, ok bool) {
	if width <= 0 || height <= 0 {
		return 0, 0, 0, 0, false
	}
	first := true
	for i := range polys {
		for j := range polys[i].x {
			x, y := polys[i].x[j], polys[i].y[j]
			if first {
				minX, maxX, minY, maxY, first = x, x, y, y, false
				continue
			}
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if first || maxX < 0 || maxY < 0 || minX >= width || minY >= height {
		return 0, 0, 0, 0, false
	}
	if minX < 0 {
		minX = 0
	}
	if minY < 0 {
		minY = 0
	}
	if maxX >= width {
		maxX = width - 1
	}
	if maxY >= height {
		maxY = height - 1
	}
	return minX, minY, maxX, maxY, true
}

// composeModel composes one unit into its own image and returns it
// UNCOMMITTED. Traversal is performed by internal/render; this function only
// resolves authored textures and rasterizes [03 §2.4][03 §2.4.1]
// [R-REN-03A §1]. A non-nil reveal composes the unit exactly as a finished one
// and then recolours it band by band, which is the nanoframe [03 §5.2].
//
// It is separate from drawModel because a carrier's finished image has to
// exist before its children can be composited into it: [R-REN-03A §4]'s
// staging path copies the cached image into a staging image, composites each
// child there with the key test, and blits once at the end. A composer that
// blitted as it finished could never be a staging source.
func (c *Client) composeModel(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) (composedModel, bool) {
	return c.composeModelLane(draw, owner, selector, id, kind, reveal, outline, presentationrender.PieceLaneAll, true)
}

// composeModelLane builds one local cached/live image. The composition bounds
// always measure every visible piece, even when the selected lane rasterizes a
// subset; otherwise a live door could escape the cached body's staging box
// [03 R-REN-03A §1][03 R-REN-03A §4]. finalPasses applies waterline and Digger
// only to the per-frame subject image, never the retained body source.
func (c *Client) composeModelLane(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8, lane presentationrender.PieceLane, finalPasses bool) (composedModel, bool) {
	all := c.collectDrawPolys(draw, selector, id, kind)
	if len(all) == 0 {
		return composedModel{}, false
	}
	// The collector borrows one scratch slice: measure before a lane walk
	// replaces its contents [03 R-REN-03A §1].
	anchorX, anchorY := c.modelAnchor(draw)
	width, height, originX, originY := modelExtent(all)
	polys := all
	if lane != presentationrender.PieceLaneAll {
		polys = c.collectDrawPolysLane(draw, selector, id, kind, lane)
	}
	var geometry *drawlist.ModelGeometry
	if c.recordModelGeometry && len(polys) != 0 {
		// The outer packet describes native output. An optional doubled body
		// projection below supplies the GPU resolve; neither packet inherits
		// pixels from the classic composition.
		native := c.cloneModelPolys(polys)
		placeFaces(native, originX, originY, 1)
		geometry = modelGeometryPacketAt(native, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, draw.KeyPlane, drawlist.ModelFallbackNone)
		c.configureModelGeometry(geometry, draw, owner, kind, reveal, outline)
		if lane != presentationrender.PieceLaneLive {
			geometry.Supersample = c.modelSupersampleGeometry(polys, draw, width, height, originX, originY)
		}
		if geometry.Supersample != nil {
			geometry.Supersample.Reveal = geometry.Reveal
		}
	}

	scale := int32(1)
	if lane != presentationrender.PieceLaneLive && c.supersampleModel(draw.Structure) {
		scale = 2
	}
	placeFaces(polys, originX, originY, scale)

	// The image the unit is blitted from is always 1x; when anti-aliasing is
	// active the model is rasterized into a doubled scratch first and resolved
	// into it [R-REN-03A §6].
	keyPlane := draw.KeyPlane
	target := c.borrowModelImage(width, height, originX, originY, anchorX, anchorY, keyPlane, 1)
	raster := target
	if scale == 2 {
		raster = c.borrowModelImage(2*width, 2*height, 2*originX, 2*originY, anchorX, anchorY, keyPlane, 2)
	}
	c.attachModelTrace(raster, id)

	for i := range polys {
		if polys[i].frame != nil {
			c.blitTexturedPolyTarget(raster, &polys[i], polys[i].frame, reveal, id)
			continue
		}
		c.fillPolyTarget(raster, &polys[i], polys[i].color, reveal, id)
	}
	if scale == 2 {
		raster.resolveSupersample(target, &c.pal.Alpha)
	}
	if finalPasses && reveal != nil {
		// The nanoframe outline is a pass over the composition image, not an
		// overdraw on the framebuffer: retail runs the reveal and the outline
		// over the image it is about to composite from — the unit's own image
		// after the cached body is copied in, a carried child's own image
		// before it is composited into the carrier's staging image — so the
		// outline is key-tested there and the digger erase, the waterline
		// pass and a carrier's geometry all act on it like any other pixel
		// [R-COMP-01 §3]. It follows the anti-alias resolve because that is
		// the image the reveal reads, at 1x.
		c.outlineModelInto(target, raster, draw, outline)
	}
	// The waterline runs before the Digger erase, and both before the blit
	// [R-WATER-01 §2]. It acts on the image the reveal and the outline have
	// just written into, so a nanoframe rising under water is tinted like any
	// other submerged geometry [R-COMP-01 §3].
	if finalPasses {
		c.waterlinePass(target, draw, owner, kind)
	}
	if finalPasses && draw.DiggerClip {
		// A Digger definition raises every key by 75; erasing at or below 125
		// therefore removes exactly the geometry at or below the model origin,
		// which is the buried half of a pop-up defence [R-REN-03A §8].
		target.eraseAtOrBelow(uint8(diggerEraseThreshold))
	}
	return composedModel{image: target, raster: raster, draw: draw, geometry: geometry}, true
}

// waterlinePass is retail's underwater presentation, both arms of it
// [R-REN-03A §8][R-WATER-01 §2][R-RAST-01 §4].
//
// With `t = seaLevel - hi16(unitY)` positive, part of the subject sits below
// the water surface, and every pixel whose height key is at or below
// `t + 50 [+75 for a Digger]` is that part. What happens to those pixels is an
// ownership question, not a geometry one:
//
//   - a submerged subject the viewer neither owns nor holds on sonar is
//     ERASED below the surface — an enemy submarine simply is not drawn;
//   - a subject the viewer owns, or has on sonar, is RECOLOURED through the
//     BLUE TABLE, which is why your own submarine reads as a blue hull instead
//     of vanishing, and why a submerged nanoframe goes up blue.
//
// A 3DO feature takes the tint always: the feature-backed pseudo-unit sets the
// contact bit permanently at construction, so a wreck under water is tinted,
// never cut [R-RAST-01 §4, §6].
//
// The pass needs a key plane to have anything to compare, so a ZBuffer=0
// definition is never cut at the waterline and never tinted — retail's own
// no-key-plane present skips the waterline and digger passes outright
// [R-RAST-01 §4].
//
// This runs on the subject's own composition image rather than on a carrier's
// staging image, which is where the section places it. The two differ only for
// a carrier: each carried child is tinted at its own depth instead of at the
// carrier's. The Digger erase beside it already sits the same way, and moving
// either is a staging-path change, not a waterline one.
func (c *Client) waterlinePass(target *modelTarget, draw *presentationrender.UnitDraw, owner, kind uint8) {
	if c == nil || target == nil || draw == nil || target.height == nil {
		return
	}
	threshold, submerged := waterlineThreshold(c.seaLevel(), draw.WorldPos[1], draw.DiggerClip)
	if !submerged {
		return
	}
	if !c.waterlineTints(draw, owner, kind) {
		target.eraseAtOrBelow(threshold)
		return
	}
	if c.pal == nil {
		return
	}
	target.tintAtOrBelow(threshold, &c.pal.Blue)
}

// revealModelImage applies the current reveal to a copied composition, after
// cached supersampling and before later live/child composition [03 R-COMP-01 §3].
func (c *Client) revealModelImage(target *modelTarget, draw *presentationrender.UnitDraw, reveal *presentationrender.NanoframeReveal, outline uint8) {
	if target == nil || reveal == nil {
		return
	}
	for i, color := range target.color {
		if color == target.transparent {
			continue
		}
		color, present := nanoframeVerdict(*reveal, target.storedKey(i), color)
		target.write(i, color, present)
	}
	c.outlineModelInto(target, target, draw, outline)
}

// finalizeModelImage runs after the subject's live pieces and attached children
// have joined the staging image [03 R-REN-03A §4/§9].
func (c *Client) finalizeModelImage(target *modelTarget, draw *presentationrender.UnitDraw, owner, kind uint8) {
	if target == nil {
		return
	}
	c.waterlinePass(target, draw, owner, kind)
	if draw.DiggerClip {
		target.eraseAtOrBelow(uint8(diggerEraseThreshold))
	}
}

// waterlineTints resolves the erase-versus-tint bit of [R-RAST-01 §4]: the
// sonar-contact bit the sensor phase publishes, OR the subject belonging to the
// viewing player. The two are genuinely separate — the sensor sweep skips the
// viewer's own units when it sets the contact bit, so ownership is not implied
// by it. A 3DO feature carries the bit permanently [R-RAST-01 §6].
func (c *Client) waterlineTints(draw *presentationrender.UnitDraw, owner, kind uint8) bool {
	if kind == modelCursorFeature {
		return true
	}
	if draw.SonarContact {
		return true
	}
	cur := c.buffer.Current()
	return cur != nil && cur.ViewingPlayer < 10 && owner == cur.ViewingPlayer
}

// seaLevel is the committed map sea level in world units. It is the map
// header's byte scaled to 16.16 and published on the visibility view, never
// read from the mutable terrain [03 §2.2][I6]. Without a committed frame there
// is no map and no waterline.
func (c *Client) seaLevel() numeric.Fixed {
	if c == nil {
		return 0
	}
	cur := c.buffer.Current()
	if cur == nil {
		return 0
	}
	return cur.Visibility.SeaLevel
}

// pendingModelCommit is the recorder-local description used to freeze one
// drawlist.Model classic packet. It names the shadow/body/observer sequence;
// emitModel copies every pixel operand before this temporary value goes away
// (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5).
type pendingModelCommit struct {
	m    composedModel
	blit *modelTarget
	// shadow, body and trace select which of the three commit steps classicSink
	// runs, in that order (WU-1.8). An ordinary unit or carrier records all three
	// in one command; a carrier's staged child records its framebuffer shadow as a
	// shadow-only command before the carrier, and its parity trace as a trace-only
	// command after, so each direct write moves to its own list position and the
	// recording pass touches c.indexed nowhere [C-G1].
	shadow bool
	body   bool
	trace  bool
}

// finishModel is everything that happens once a composed image is final: the
// shadow, the one blit, and the trace emit. The nanoframe outline is not here:
// it is part of the composed image [R-COMP-01 §3].
//
// blit is the image actually put on screen. It is the composed image for an
// ordinary unit and the staging image for a carrier, which is the one place
// the two differ [R-REN-03A §4].
//
// The two writes into c.indexed and the trace that follows them are recorded as
// one drawlist.Model command that runs all three commit steps — shadow, body
// blit, trace — when the frame is replayed once through the classic sink
// (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) finishModel(m composedModel, blit *modelTarget) {
	if m.image == nil {
		return // a subject with no composition image records nothing
	}
	if blit == nil {
		blit = m.image
	}
	c.emitModel(pendingModelCommit{m: m, blit: blit, shadow: true, body: true, trace: true})
}

// drawModel composes one unit and blits it once. It is composeModel followed
// by finishModel with no staging image, which is every subject that carries
// nothing [03 §2.4][R-REN-03A §1].
func (c *Client) drawModel(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) bool {
	if c.geometryOnlyModels {
		return c.recordModelGeometryOnly(draw, owner, selector, id, kind, reveal, outline)
	}
	m, ok := c.composeModel(draw, owner, selector, id, kind, reveal, outline)
	if !ok {
		return false
	}
	c.finishModel(m, nil)
	return true
}

// recordModelGeometryOnly records a native-scale GPU subject without creating
// any CPU composition storage. Per-subject passes are carried as immutable
// geometry metadata [DESIGN_GPU_RENDERER.md §9–§10].
func (c *Client) recordModelGeometryOnly(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) bool {
	g := c.prepareModelGeometry(draw, owner, selector, id, kind, reveal, outline)
	if g == nil {
		return false
	}
	c.list.RecordModel(drawlist.Model{Geometry: g})
	return true
}

// modelAnchor is the framebuffer pixel the composition image's recorded origin
// lands on. Position enters the model path here and nowhere else
// [03 §5.2][R-REN-03A §1].
func (c *Client) modelAnchor(draw *presentationrender.UnitDraw) (int32, int32) {
	if c == nil || c.cam == nil || draw == nil {
		return 0, 0
	}
	sx, sy := c.cam.WorldToScreen(draw.WorldPos[0], draw.WorldPos[1], draw.WorldPos[2])
	return sx - camera.OriginX, sy - camera.OriginY
}
