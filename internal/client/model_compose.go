package client

// Composing one model into an indexed image: the primitive dispatch, the
// per-face paint test, the collected polygons and their extent [03 §2.4]
// [03 R-REN-03A].

import (
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
func modelPrimitiveDispatch(pr presentationrender.PrimitiveDraw, resolved bool) modelPrimitiveMode {
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

// collectDrawPolys adapts canonical render records to the indexed framebuffer.
// It owns no hierarchy math: units, features, and projectiles all pass through
// render.UnitDraw [03 §2.4][03 §5.2].
//
// One authored primitive becomes one face, at its authored arity and in its
// authored corner order. There is no fan triangulation: retail's scan converter
// takes the vertex count as an argument and derives its two chains from the
// index order, so splitting a quad into triangles would change both the chains
// and the interpolation the section specifies [R-RAST-01 §1]. The winding cull
// is not applied here either — it is the span comparison inside the walk.
func (c *Client) collectDrawPolys(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8) []screenPoly {
	if c == nil || c.cam == nil || draw == nil || draw.Model == nil {
		return nil
	}
	var polys []screenPoly
	// Retail walks the piece list last-to-first. Because the height-key test
	// admits equal keys, draw order is the tie-break, so this direction is what
	// makes piece 0 win every tie against every later piece — reversing it
	// lifts a factory's build plate through its roof and lets an opened solar
	// collector's panels swallow the column they intersect [R-REN-03A §3].
	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		piece := draw.Pieces[pi]
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		for pri, pr := range piece.Primitives {
			if draw.Model.Pieces[pi].Selection && pri == 0 {
				continue // selection plate is retained by the model but never rasterized [03 §2.4.1]
			}
			n := len(pr.VertexIndices)
			if n < 3 {
				continue
			}
			ref, textured := resolveTextureRef(nil, c.texIndex, pr.TextureName)
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
					if kind == modelCursorFeature && id == 0 {
						// Same publication gap as the sprite path above:
						// FeatureView carries no stable identity, so an
						// animated model texture would share cursor zero
						// across every feature. Suppressed, not shared.
						continue
					}
					texFrame = c.modelAnimatedFrameAt(ref, kind, id, pi, pri)
				case texTeam:
					if ref.entry == nil {
						continue
					}
					oi := int(owner) % 10
					if oi >= 0 && oi < len(ref.entry.Frames) {
						texFrame = ref.entry.Frames[oi].Frame
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
			poly := newScreenPoly(n)
			poly.color, poly.frame = color, texFrame
			poly.useSHD = pr.ShadeRow != presentationrender.NoShadeRow
			poly.candidate, poly.piece, poly.primitive, poly.texture = uint32(len(polys)), pi, pri, pr.TextureName
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
				// Retail composes model-relative: the piece chain result is
				// narrowed once, the shear applied, and the unit's position
				// enters only at the final blit [03 §5.2][R-REN-03A §1].
				lx, ly, ry := modelLocalVertex(v, draw.WorldPos)
				lx, ly = c.scaleModelLocal(lx, ly)
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
			polys = append(polys, poly)
		}
	}
	return polys
}

// modelExtent measures the composition image from the collected faces: retail
// walks every visible piece's vertices, tracks the min and max of the projected
// offsets with the extrema seeded at zero so the box always contains the model
// origin, then adds a two-pixel margin on every side [R-REN-03A §1].
func modelExtent(polys []screenPoly) (width, height int, originX, originY int32) {
	var minX, minY, maxX, maxY int32 // seeded at the model origin, not at a vertex
	for i := range polys {
		for k := range polys[i].x {
			if polys[i].x[k] < minX {
				minX = polys[i].x[k]
			}
			if polys[i].x[k] > maxX {
				maxX = polys[i].x[k]
			}
			if polys[i].y[k] < minY {
				minY = polys[i].y[k]
			}
			if polys[i].y[k] > maxY {
				maxY = polys[i].y[k]
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
	image  *modelTarget
	raster *modelTarget
	draw   *presentationrender.UnitDraw
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
func (c *Client) composeModel(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) (composedModel, bool) {
	polys := c.collectDrawPolys(draw, owner, id, kind)
	if len(polys) == 0 {
		return composedModel{}, false
	}
	anchorX, anchorY := c.modelAnchor(draw)
	width, height, originX, originY := modelExtent(polys)

	scale := int32(1)
	if c.supersampleModel(draw.Structure) {
		scale = 2
	}
	placeFaces(polys, originX, originY, scale)

	// The image the unit is blitted from is always 1x; when anti-aliasing is
	// active the model is rasterized into a doubled scratch first and resolved
	// into it [R-REN-03A §6].
	keyPlane := draw.KeyPlane
	target := newModelImage(width, height, originX, originY, anchorX, anchorY, keyPlane, 1)
	raster := target
	if scale == 2 {
		raster = newModelImage(2*width, 2*height, 2*originX, 2*originY, anchorX, anchorY, keyPlane, 2)
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
	if reveal != nil {
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
	c.waterlinePass(target, draw, owner, kind)
	if draw.DiggerClip {
		// A Digger definition raises every key by 75; erasing at or below 125
		// therefore removes exactly the geometry at or below the model origin,
		// which is the buried half of a pop-up defence [R-REN-03A §8].
		target.eraseAtOrBelow(uint8(diggerEraseThreshold))
	}
	return composedModel{image: target, raster: raster, draw: draw}, true
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
	return cur != nil && cur.Selection.LocalPlayer < 10 && owner == cur.Selection.LocalPlayer
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

// pendingModelCommit is one composed subject held in the client-side table
// drawlist.Model.Ref indexes: everything the classic executor needs to run the
// two writes into the indexed surface that finish a model — the shadow and the
// body blit — plus the trace that reads the surface afterwards. Composition is
// already complete; this carries no geometry, only the finished images and the
// image actually blitted (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5).
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
func (c *Client) drawModel(draw *presentationrender.UnitDraw, owner uint8, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) bool {
	m, ok := c.composeModel(draw, owner, id, kind, reveal, outline)
	if !ok {
		return false
	}
	c.finishModel(m, nil)
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
