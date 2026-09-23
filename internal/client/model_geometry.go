package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"

	"slices"
)

// modelGeometryPacketAt makes the durable P3 input from the already-resolved
// composition polygons. The composition walk has selected texture frames,
// converted every corner to the subject-local image coordinates and retained
// the load-fixed face order. Copying here makes the packet independent of the
// raster scratch and of the next recording [03 R-REN-03A §1–§3]. It takes the
// image geometry directly, so Modern recording never creates colour, coverage,
// or height planes.
func modelGeometryPacketAt(polys []screenPoly, width, height, originX, originY, anchorX, anchorY, scale int32, keyPlane bool, fallback drawlist.ModelFallbackReason) *drawlist.ModelGeometry {
	return fillModelPacket(&drawlist.ModelGeometry{}, nil, polys, width, height, originX, originY, anchorX, anchorY, scale, keyPlane, fallback, nil)
}

// doubledPlacement is the supersample projection applied while a packet is
// filled from UNPLACED polygons, so the doubled lane costs no copy of the
// polygon lanes (DESIGN_GPU_RENDERER §17): the corner lands at
// 2·(x + originX) + hx, 2·(y + originY) − odd + hy, with odd the corner's odd
// height correction in doubled-raster pixels [03 R-REN-03A §6]. shearX marks
// the shadow projection, whose quarter shear moves the doubled corner one
// raster pixel right as well as up when the second bit of the height is set —
// the doubled quarter shear is 2·(ry>>2) + ((ry>>1)&1) on both axes, exactly as
// the body's doubled half shear is 2·(ry>>1) + (ry&1) on Y alone [R-REN-03D §2].
// oddPx is one at the retail view and the view scale's own pixel at a magnified
// one, because the corner's scaled offset already lost the half the correction
// restores (scaleModelLocal).
type doubledPlacement struct {
	originX, originY, hx, hy, oddPx int32
	shearX                          bool
	// exact takes each corner's own doubled coordinates (screenPoly.x2, y2)
	// instead of doubling the native ones: the direct projection, whose
	// corners are projected one by one and carry no shared subject offset.
	exact bool
}

// doubledPlacement builds the placement for a lane doubled from the local
// projection at this frame's view scale.
func (c *Client) doubledPlacement(originX, originY, hx, hy int32, shearX bool) doubledPlacement {
	return doubledPlacement{originX: originX, originY: originY, hx: hx, hy: hy, oddPx: c.modelScale().Px(1), shearX: shearX}
}

func fillModelPacket(g *drawlist.ModelGeometry, vertices []drawlist.ModelVertex, polys []screenPoly, width, height, originX, originY, anchorX, anchorY, scale int32, keyPlane bool, fallback drawlist.ModelFallbackReason, place *doubledPlacement) *drawlist.ModelGeometry {
	*g = drawlist.ModelGeometry{
		Eligible: fallback == drawlist.ModelFallbackNone,
		Fallback: fallback,
		Faces:    resizeModelFaces(g.Faces, len(polys)),
		Width:    width, Height: height,
		OriginX: originX, OriginY: originY,
		AnchorX: anchorX, AnchorY: anchorY,
		Scale: scale, KeyPlane: keyPlane,
	}
	count := 0
	for i := range polys {
		count += len(polys[i].x)
	}
	vertices = resizeScratch(vertices, count)
	offset := 0
	for i := range polys {
		p := &polys[i]
		n := len(p.x)
		face := &g.Faces[i]
		face.Vertices = vertices[offset : offset+n : offset+n]
		face.Texture, face.Color, face.Shaded = p.frame, p.color, p.useSHD
		face.Normal = p.normal
		face.Material = p.material
		key, u, v, row := p.attr[spanKey], p.attr[spanU], p.attr[spanV], p.attr[spanRow]
		for j := 0; j < n; j++ {
			x, y := p.x[j], p.y[j]
			if place != nil && place.exact {
				x, y = 2*place.originX+p.x2[j], 2*place.originY+p.y2[j]
			} else if place != nil {
				odd := boolToInt32(p.oddHeight[j]) * place.oddPx
				x = 2*(x+place.originX) + place.hx
				if place.shearX {
					x += odd
				}
				y = 2*(y+place.originY) - odd + place.hy
			}
			face.Vertices[j] = drawlist.ModelVertex{
				X: x, Y: y, Key: key[j],
				U: u[j], V: v[j], Shade: uint8(row[j]),
			}
			if j < len(p.heights) {
				face.Vertices[j].Height = p.heights[j]
			}
		}
		offset += n
	}
	return g
}

// configureModelGeometry carries the same resolved reveal, outline and waterline
// decisions as the classic composer. These are presentation inputs, not pixels
// [03 R-COMP-01 §3][03 R-WATER-01 §2].
func (c *Client) configureModelGeometry(g *drawlist.ModelGeometry, draw *presentationrender.UnitDraw, owner, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) {
	c.configureModelGeometryFor(nil, g, draw, owner, kind, reveal, outline)
}

// configureModelGeometryFor is configureModelGeometry for a subject whose
// retained body is at hand, so the shadow lane can be kept beside the body
// rather than projected again every frame (docs/DESIGN_GPU_RENDERER.md §13.12
// "Shadows — contract P4"). A nil body keeps the per-frame projection.
func (c *Client) configureModelGeometryFor(body *cachedModelBody, g *drawlist.ModelGeometry, draw *presentationrender.UnitDraw, owner, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) {
	c.setModelLightingHeight(g, draw)
	if reveal != nil {
		g.Reveal = &drawlist.ModelReveal{Line: reveal.Line, Floor: reveal.Floor, Below: reveal.Below, Band: reveal.Band, Above: reveal.Above}
		g.Outline = c.modelOutlineGeometry(draw, g.OriginX, g.OriginY, outline, 1, 0, 0)
		if ss := g.Supersample; ss != nil {
			// The doubled raster carries the reveal, which is resolved with
			// the body; the outline endpoints are one native pixel each and
			// the executor draws them from the native rows above, so the
			// doubled lane carries none (DESIGN_GPU_RENDERER §22).
			ss.Reveal = g.Reveal
		}
	}
	if g.KeyPlane {
		if threshold, submerged := waterlineThreshold(c.seaLevel(), draw.WorldPos[1], draw.DiggerClip); submerged {
			g.Waterline, g.WaterlineKey = drawlist.ModelWaterlineErase, threshold
			if c.waterlineTints(draw, owner, kind) {
				g.Waterline = drawlist.ModelWaterlineBlue
			}
		}
		g.Digger, g.DiggerKey = draw.DiggerClip, uint8(diggerEraseThreshold)
	}
	if draw.DiggerClip || !draw.Structure {
		g.Shadow = c.silhouetteShadowGeometry(g, draw)
	} else {
		g.Shadow = c.retainedShadowGeometry(body, draw)
	}
}

// setModelLightingHeight refreshes placement metadata without rebuilding the
// retained faces. Doubled geometry carries the same physical height (§22.4).
func (c *Client) setModelLightingHeight(g *drawlist.ModelGeometry, draw *presentationrender.UnitDraw) {
	if g == nil || draw == nil {
		return
	}
	scale := float32(c.modelScale().Float())
	g.WorldHeight = float32(draw.WorldPos[1].Raw()) / 65536 * scale
	g.AircraftShadowHeight, g.AircraftShadowScale = 0, scale
	if c.enhanced && draw.Airborne {
		receiver := max(draw.GroundY, c.seaLevel())
		g.AircraftShadowHeight = max(0, float32(draw.WorldPos[1].Sub(receiver).Raw())/65536*scale)
	}
	g.ReflectWater = c.reflectionWaterAt(draw.WorldPos[0], draw.WorldPos[2])
	g.ReflectionSea = float32(c.seaLevel().Raw()) / 65536 * scale
	if g.Supersample != nil {
		g.Supersample.WorldHeight = g.WorldHeight
		g.Supersample.AircraftShadowHeight = g.AircraftShadowHeight
		g.Supersample.AircraftShadowScale = g.AircraftShadowScale
		g.Supersample.ReflectWater = g.ReflectWater
		g.Supersample.ReflectionSea = g.ReflectionSea
	}
}

// modelOutlineGeometry retains all valid rings, including primitives omitted by
// body material dispatch. The executor draws their two row endpoints with the
// subject key comparison, not a polygon border [03 R-COMP-01 §3].
//
// The ring list and its corners come from a borrowed scratch slot so a subject
// costs no allocation per frame: one face slice and one vertex arena grow to
// the frame's high-water mark and are rewound at the next reset. Corners are
// appended to the arena first and the faces are pointed at their spans
// afterwards, because an append that reallocates the arena would otherwise
// leave earlier faces addressing the old backing array. A ring that names a
// corner the piece does not have rewinds the arena and is dropped, exactly as
// the abandoned face was before.
//
// scale 2 is the supersampled outline: every corner at the doubled raster's
// coordinates with the subject's half-pixel offset, the same placement the
// doubled body takes (doubledPlacement).
func (c *Client) modelOutlineGeometry(draw *presentationrender.UnitDraw, originX, originY int32, color uint8, scale, hx, hy int32) []drawlist.ModelFace {
	s := c.borrowOutline()
	faces, verts, spans := s.faces[:0], s.verts[:0], s.spans[:0]
	oddPx := c.modelScale().Px(1)
	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		piece := &draw.Pieces[pi]
		for pri := range piece.Primitives {
			pr := &piece.Primitives[pri]
			if draw.Model.Pieces[pi].Selection && pri == 0 || len(pr.VertexIndices) < 2 {
				continue
			}
			start := len(verts)
			complete := true
			for _, vi := range pr.VertexIndices {
				if int(vi) >= len(piece.WorldVertices) {
					complete = false
					break
				}
				v := piece.WorldVertices[vi]
				x, y, ry := modelLocalVertex(v, draw.WorldPos)
				x, y = c.scaleModelLocal(x, y)
				if scale == 2 {
					x, y = 2*(x+originX)+hx, 2*(y+originY)-(ry&1)*oddPx+hy
				} else {
					x, y = x+originX, y+originY
				}
				verts = append(verts, drawlist.ModelVertex{X: x, Y: y, Key: modelHeightKey(v[1].Sub(draw.WorldPos[1]), draw.DiggerClip)})
			}
			if !complete || len(verts)-start < 2 {
				verts = verts[:start]
				continue
			}
			faces = append(faces, drawlist.ModelFace{Color: color})
			spans = append(spans, int32(start), int32(len(verts)))
		}
	}
	s.faces, s.verts, s.spans = faces, verts, spans
	// Faces beyond the new ring count can still point at a replaced vertex
	// array. The current rings are rebound below after append has finished.
	clear(faces[len(faces):cap(faces)])
	if len(faces) == 0 {
		return nil
	}
	for i := range faces {
		lo, hi := spans[2*i], spans[2*i+1]
		faces[i].Vertices = verts[lo:hi:hi]
	}
	return faces
}

// geometryForCommit preserves the recorded shadow/body separation. A staging
// body is represented by its carrier and ordered child packets, never a CPU
// composition image [03 R-REN-03A §4].
//
// The commit record is a value copy of the composed packet, not a deep clone.
// A subject can be committed more than once — a staged child records its shadow
// and its trace separately — and each command needs its own Shadow and
// eligibility fields, but the faces, vertices, outline, reveal and children
// underneath them are read-only and are already owned by the recorder's frame
// scratch, which the other model call sites record directly. The recorded list
// is same-frame use only and List.Clone deep-copies for a retained one, so
// sharing here costs nothing and saves a full copy of every face and vertex of
// every subject per frame (docs/DESIGN_GPU_RENDERER.md §11.5 "CPU").
func geometryForCommit(p pendingModelCommit) *drawlist.ModelGeometry {
	if p.m.geometry == nil {
		return nil
	}
	g := new(drawlist.ModelGeometry)
	*g = *p.m.geometry
	if !p.shadow {
		g.Shadow = nil
	}
	traceOnly := !p.body && !p.shadow
	missingBody := p.body && (p.blit == nil || p.blit != p.m.image && len(g.Children) == 0)
	if traceOnly || missingBody {
		g.Eligible = false
		g.Shadow = nil
		if !p.body {
			g.Fallback = drawlist.ModelFallbackNoBodyCommit
		} else {
			g.Fallback = drawlist.ModelFallbackStaging
		}
	}
	return g
}

// modelShadowGeometry shares the classic projection and anchor but never calls
// its software rasterizer. Mobile/Digger silhouettes retain the existing
// structure-projection approximation; classic uses the separate finished-body
// silhouette branches at buildModelShadow [03 R-REN-03D §1–§6].
func (c *Client) modelShadowGeometry(draw *presentationrender.UnitDraw) *drawlist.ModelGeometry {
	if c == nil || draw == nil {
		return nil
	}
	anchorX, anchorY, hx, hy := c.shadowPlacement(draw)
	return c.shadowGeometryAt(draw, anchorX, anchorY, hx, hy)
}

// shadowGeometryAt projects the shadow at one placement. The projection itself —
// the corners, the box and its origin — depends on nothing the placement carries,
// which is what lets the retained lane be built once at the zero placement and
// rebased per frame (§13.12 "Shadows — contract P4").
func (c *Client) shadowGeometryAt(draw *presentationrender.UnitDraw, anchorX, anchorY, hx, hy int32) *drawlist.ModelGeometry {
	if c == nil || draw == nil || !draw.CastsShadow || c.pal == nil {
		return nil
	}
	polys := c.collectShadowPolys(draw)
	if len(polys) == 0 {
		return nil
	}
	width, height, originX, originY, _ := c.projectedModelExtent(draw, presentationrender.PieceLaneAll, true)
	supersample := c.modelSupersampleGeometry(polys, true, width, height, c.doubledPlacement(originX, originY, hx, hy, true))
	placeFaces(polys, originX, originY, 1)
	g := c.borrowModelPacket(polys, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, true, drawlist.ModelFallbackNone)
	g.Supersample = supersample
	c.setModelLightingHeight(g, draw)
	return g
}

func (c *Client) prepareModelGeometry(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) *drawlist.ModelGeometry {
	if kind == modelCursorFeature {
		if g, retained := c.featureGeometry(draw, selector, id); retained {
			return g
		}
	}
	width, height, originX, originY, visible := c.projectedModelExtent(draw, presentationrender.PieceLaneAll, false)
	if !visible {
		return nil
	}
	anchorX, anchorY, hx, hy := c.modelPlacement(draw)
	polys := c.collectDrawPolys(draw, selector, id, kind)
	if len(polys) == 0 && reveal == nil {
		return nil
	}
	supersample := c.modelSupersampleGeometry(polys, draw.KeyPlane, width, height, c.doubledPlacement(originX, originY, hx, hy, false))
	placeFaces(polys, originX, originY, 1)
	g := c.borrowModelPacket(polys, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, draw.KeyPlane, drawlist.ModelFallbackNone)
	g.Supersample = supersample
	c.configureModelGeometry(g, draw, owner, kind, reveal, outline)
	return g
}

// featureGeometry is prepareModelGeometry for a 3DO feature — a wreck, or a
// map-authored model feature — with a publication identity: its faces are
// retained beside that identity and rebased onto this frame's placement, so
// the packet carries a cache key the executor's retained store can replay
// (docs/DESIGN_GPU_RENDERER.md §13.12, §22) and the recorder projects the
// model only when an input of the projection changes. The second result is
// false for a feature the retained path does not take, which keeps the
// per-frame projection: no frame arena, a draw whose pose does not describe
// every piece, or a model with an animated texture, whose frames the
// per-frame walk advances (a retained lane would freeze them). A feature
// without a published identity — which is every feature today — is retained
// under its map position (featureBodyID).
//
// The projection's inputs are exactly those of the unit path's cached lane,
// which this reuses: the model by name, the folded piece pose (which is what
// carries the corpse's orientation triple [03 R-RAST-01 §6]), the published
// team colour for LOGOS faces [03 R-RAST-01 §3], the shading option, the
// geometry supersample gate, the view scale, the palette tables, and the
// texture index generation the model's table was resolved under. The
// position is not one: every corner is model-local, and the placement, the
// half-pixel offset, the lighting height and the waterline are supplied by
// the rebase and configureModelGeometryFor each frame, exactly as for a
// unit. The rebased packet is therefore the packet the per-frame projection
// builds, corner for corner (verified by the battle capture).
func (c *Client) featureGeometry(draw *presentationrender.UnitDraw, selector teamColor, id uint64) (*drawlist.ModelGeometry, bool) {
	if draw == nil || draw.Model == nil || !c.modelScratch.active || !shadowPoseComplete(draw) || c.modelHasAnimatedTexture(draw.Model) {
		return nil, false
	}
	key := featureBodyID(id, draw.WorldPos[0], draw.WorldPos[2])
	body := c.cachedBody(key)
	in := c.featureBodyInputs(draw, selector)
	if body == nil || body.geometry == nil || body.featureInputs != in || !slices.Equal(body.featurePose, draw.PieceStates) {
		w, h, ox, oy, visible := c.projectedModelExtent(draw, presentationrender.PieceLaneAll, false)
		if !visible {
			return nil, true
		}
		polys := c.collectDrawPolys(draw, selector, id, modelCursorFeature)
		if len(polys) == 0 {
			return nil, true
		}
		ax, ay := c.modelAnchor(draw)
		// As for a unit, the retained doubled lane carries no half-pixel
		// offset; the rebase adds this frame's. The lane is projected straight
		// into the body's retained arenas.
		base := c.cachedBodyFor(key).store.fill(c, polys, w, h, ox, oy, ax, ay, draw.KeyPlane, c.doubledPlacement(ox, oy, 0, 0, false))
		body = c.replaceFeatureGeometry(key, draw, in, base)
	}
	body.featureSeenTick = c.frameTick
	src := body.geometry
	ax, ay, hx, hy := c.modelPlacement(draw)
	g := c.rebaseRetained(&body.store, src, src.Width, src.Height, src.OriginX, src.OriginY, ax, ay, hx, hy)
	if g == nil {
		return nil, false
	}
	g.Cache = body.cacheKey(hx, hy)
	c.configureModelGeometryFor(body, g, draw, 0, modelCursorFeature, nil, 0)
	return g, true
}

// unitGeometryPair records the native counterpart of the retained classic
// body. The cached lane keeps the orientation used when it was recorded; the
// live lane is rebuilt from this frame's script pose and is rebased into the
// cached body's current union box [03 R-REN-03A §4].
func (c *Client) unitGeometryPair(v frame.UnitView, forceKeyPlane bool) (arrivalBody, arrivalLive *drawlist.ModelGeometry) {
	// Tag only outgoing frame packets, after retained geometry has been copied.
	defer func() {
		c.applyArrivalHeat(arrivalBody, v)
		c.applyArrivalHeat(arrivalLive, v)
	}()
	// Stage one may already have built this pair on a worker's own arena
	// (docs/DESIGN_GPU_RENDERER.md §13.9). The slot is handed over for exactly
	// one call — a carrier's own pair is the first unitGeometryPair of its
	// present in both branches — and its identity is checked, so a slot that
	// does not belong to this subject falls through and is rebuilt here.
	if pre := c.pendingPair; pre != nil {
		c.pendingPair = nil
		if !forceKeyPlane && pre.id == unitPresentationID(v) {
			return pre.body, pre.live
		}
	}
	// The pieces' geometry is built below only for the lanes this frame reads
	// (materializeUnitDraw).
	draw, ok := c.unitDrawDeferred(v)
	if !ok {
		return nil, nil
	}
	if forceKeyPlane {
		draw.KeyPlane = true
	}
	id := unitPresentationID(v)
	reveal, outline := c.unitNanoframeReveal(v)
	if id == 0 || !c.modelScratch.active {
		draw.Materialize(presentationrender.PieceLaneAll)
		g := c.prepareModelGeometry(draw, v.Owner, unitTeamColor(v), id, modelCursorUnit, reveal, outline)
		if g != nil {
			g.Cloaked = v.Cloaked || c.developer.Mode != 0
		}
		return g, nil
	}
	orient := c.orientationCache(id)
	body := c.cachedBody(id)
	// A cache or shade script setter writes its flag unconditionally and
	// discards the composition-image reference on every call, without clearing
	// the cached-body validity state [03 R-COMP-01 §4][04 R-MOV-03 §4]. The
	// retained lane is that discarded reference, and the cached/live membership
	// it was built from is exactly what such a write changes, so a discard has
	// to be read here or a piece that leaves the live lane is drawn by neither.
	// Discarding is separate from invalidating: a structure or a required key
	// plane rebuilds, while a mobile subject without one draws every visible
	// piece directly [03 R-REN-03A §4]. This is the same three-term gate the
	// classic composer applies at composeUnitModelState.
	missing := body == nil || body.geometry == nil || body.cacheRevision != v.CacheRevision
	required := draw.Structure || draw.KeyPlane
	rebuild := c.cachedGeometryMustRebuild(body, v, draw, orient) || missing && required
	c.materializeUnitDraw(draw, body, rebuild || missing, reveal != nil)
	if rebuild {
		w, h, ox, oy, visible := c.projectedModelExtent(draw, presentationrender.PieceLaneAll, false)
		if !visible {
			return c.directUnitGeometry(draw, unitTeamColor(v), id, presentationrender.PieceLaneAll), nil
		}
		cached := c.collectDrawPolysLane(draw, unitTeamColor(v), id, modelCursorUnit, presentationrender.PieceLaneCached)
		if len(cached) == 0 && !draw.KeyPlane {
			return c.directUnitGeometry(draw, unitTeamColor(v), id, presentationrender.PieceLaneAll), nil
		}
		ax, ay := c.modelAnchor(draw)
		// The half-pixel offset follows the subject's position frame by frame
		// and is added when the lane is rebased into this frame's packet; the
		// lane is projected straight into the body's retained arenas with this
		// frame's offset already applied, which the store records so a later
		// rebase adds only the difference. A keyed subject whose pieces are
		// all live still retains an (empty) doubled lane, so its live faces
		// have a doubled raster to join.
		_, _, hx, hy := c.modelPlacement(draw)
		store := &c.cachedBodyFor(id).store
		base := store.fill(c, cached, w, h, ox, oy, ax, ay, draw.KeyPlane, c.doubledPlacement(ox, oy, hx, hy, false))
		c.replaceCachedGeometry(id, v, draw, base)
		missing = false
		if draw.NeedsRebuild {
			orient.UpdateKey(draw.Model.Name, v.Heading, v.Pitch, v.Bank)
		}
		body = c.cachedBody(id)
	}
	if missing || body == nil || body.geometry == nil {
		return c.directUnitGeometry(draw, unitTeamColor(v), id, presentationrender.PieceLaneAll), nil
	}
	if !draw.HasFaces() {
		return nil, nil
	}
	// The box is the commit rectangle: the retained lane's envelope, which
	// already holds every cached face this packet rebases, plus whatever the
	// live pieces reach this frame. Measuring the current pose of the cached
	// pieces too would project every face again for a box the retained
	// raster cannot fill.
	// Only a keyed packet carries a local live lane: the one-plane branch
	// below drops it and draws its live pieces through the direct projection,
	// which walks the same primitives, so it is not collected for that branch.
	var live []screenPoly
	if !draw.UnderConstruction && body.geometry.KeyPlane {
		live = c.collectDrawPolysLane(draw, unitTeamColor(v), id, modelCursorUnit, presentationrender.PieceLaneLive)
	}
	w, h, ox, oy, _ := c.projectedModelExtent(draw, presentationrender.PieceLaneLive, false)
	w, h, ox, oy = retainedModelExtent(&body.store, body.geometry, w, h, ox, oy)
	ax, ay, hx, hy := c.modelPlacement(draw)
	g := c.rebaseRetained(&body.store, body.geometry, int32(w), int32(h), ox, oy, ax, ay, hx, hy)
	if g == nil {
		return nil, nil
	}
	// The rebased packet is the retained raster placed for this frame, so it
	// carries the identity a modern executor keys its retained cached lane on
	// (docs/DESIGN_GPU_RENDERER.md §13.12). The key names the CACHED faces
	// alone: the reveal, the outline and the live lane below are rebuilt every
	// frame, and the executor draws them per frame after the cached lane in
	// retail's order whether or not it replayed that lane [03 R-REN-03A §4],
	// so none of them disqualifies the packet. A nanoframe's cached lane
	// rebuilds when its construction fraction moves (cachedGeometryMustRebuild)
	// and takes a new revision then; between those, the faces are the same
	// and the reveal band rides the verdict entry.
	g.Cache = body.cacheKey(hx, hy)
	// Cloak and developer modes [03 §3.12] change the final image blit, not the retained raster or its key
	// [03 R-RAST-01 §7]. Direct live packets below keep their opaque fill.
	g.Cloaked = v.Cloaked || c.developer.Mode != 0
	// The shadow is a lane of the same retained object and keeps its own slot
	// identity: it is projected from the current pose, which the body's frozen
	// cached lane is not, so a reveal or a live lane here does not disturb it
	// (§13.12 "Shadows — contract P4").
	c.configureModelGeometryFor(body, g, draw, v.Owner, modelCursorUnit, reveal, outline)
	if len(live) != 0 {
		if ss := g.Supersample; ss != nil {
			// The live lane joins the doubled raster too, at the same
			// placement as the cached lane it draws over (§17).
			ss.LiveFaces = c.borrowModelPacketDoubled(live, w, h, c.doubledPlacement(ox, oy, hx, hy, false), draw.KeyPlane).Faces
		}
		placeFaces(live, ox, oy, 1)
		g.LiveFaces = c.borrowModelPacket(live, int32(w), int32(h), ox, oy, ax, ay, 1, draw.KeyPlane, drawlist.ModelFallbackNone).Faces
	}
	if g.KeyPlane {
		// The keyed packet carries both lanes; its key still names the
		// cached one alone (above).
		return g, nil
	}
	// The one-plane branch commits its cached body, then the direct projected
	// live invocation as a later Model command [03 R-RAST-01 §2].
	g.LiveFaces = nil
	if ss := g.Supersample; ss != nil {
		ss.LiveFaces = nil
	}
	return g, c.directUnitGeometry(draw, unitTeamColor(v), id, presentationrender.PieceLaneLive)
}

// materializeUnitDraw builds the piece geometry unitGeometryPair will read
// from a deferred draw. A frame that only rebases the retained cached lane
// reads the live pieces alone: the rebase, the box, the live lane and the
// direct live packet take nothing from a cached piece. Every other frame reads
// the whole model — a rebuild or the direct projection of all pieces, a
// nanoframe (its outline rings walk every piece, and under construction every
// piece is in both lanes anyway), and a structure shadow whose retained
// projection is about to be redone from the current pose (§13.12 "Shadows —
// contract P4") — so all pieces are built. Materializing more than is read
// changes nothing, so any doubt here resolves to all.
func (c *Client) materializeUnitDraw(draw *presentationrender.UnitDraw, body *cachedModelBody, rebuildOrDirect, reveal bool) {
	lane := presentationrender.PieceLaneLive
	if rebuildOrDirect || reveal || draw.UnderConstruction || body == nil || body.geometry == nil || !c.retainedShadowReusable(body, draw) {
		lane = presentationrender.PieceLaneAll
	}
	draw.Materialize(lane)
}

// retainedShadowReusable reports whether configureModelGeometryFor's shadow for
// this draw reads no piece geometry: a silhouette, or a structure shadow whose
// retained projection is current (retainedShadowGeometry).
func (c *Client) retainedShadowReusable(body *cachedModelBody, draw *presentationrender.UnitDraw) bool {
	if draw.DiggerClip || !draw.Structure {
		return true
	}
	if body == nil || !c.modelScratch.active || !shadowPoseComplete(draw) {
		return false
	}
	return body.shadowValid && body.shadowInputs == c.cachedShadowInputs(draw) &&
		slices.Equal(body.shadowPose, drawPieceStates(draw))
}

// retainedModelExtent is the retained cached envelope unioned with the
// current live pieces' box. A packet's declared box is its commit rectangle,
// so rebasing cached corners into only the live box would crop them. store,
// when it holds retained, keeps the retained half of the union between frames
// (cachedGeometryStore.envelope): it is a pure function of faces that do not
// change until the lane is stored again.
func retainedModelExtent(store *cachedGeometryStore, retained *drawlist.ModelGeometry, width, height int, originX, originY int32) (int, int, int32, int32) {
	if retained == nil {
		return width, height, originX, originY
	}
	var env modelEnvelope
	if store != nil && retained == &store.g {
		if !store.envelopeValid {
			store.env, store.envelopeValid = retainedEnvelope(retained), true
		}
		env = store.env
	} else {
		env = retainedEnvelope(retained)
	}
	minX, minY := min(-originX, env.minX), min(-originY, env.minY)
	maxX, maxY := max(int32(width)-originX, env.maxX), max(int32(height)-originY, env.maxY)
	originX, originY = -minX, -minY
	return int(maxX - minX), int(maxY - minY), originX, originY
}

// modelEnvelope is an origin-relative box: the retained packet's own box and
// every corner of its faces, each also one pixel down and right.
type modelEnvelope struct {
	minX, minY, maxX, maxY int32
}

func retainedEnvelope(retained *drawlist.ModelGeometry) modelEnvelope {
	x0, y0 := -retained.OriginX, -retained.OriginY
	x1, y1 := retained.Width-retained.OriginX, retained.Height-retained.OriginY
	env := modelEnvelope{minX: min(x0, x1), minY: min(y0, y1), maxX: max(x0, x1), maxY: max(y0, y1)}
	for _, face := range retained.Faces {
		for _, vertex := range face.Vertices {
			x, y := vertex.X-retained.OriginX, vertex.Y-retained.OriginY
			env.minX, env.minY = min(env.minX, x), min(env.minY, y)
			env.maxX, env.maxY = max(env.maxX, x+1), max(env.maxY, y+1)
		}
	}
	return env
}

// rebaseRetained places the retained cached lane in this frame's packet, moved
// into the current union box with this frame's half-pixel offset on the
// doubled corners. For a lane retained in store the packet addresses the
// store's own rebased faces (cachedGeometryStore rebasedFaces) instead of a
// per-frame copy of every corner; with no store it copies into the frame arena. The faces are the
// same values either way, so the recorded packet is unchanged.
func (c *Client) rebaseRetained(store *cachedGeometryStore, src *drawlist.ModelGeometry, width, height, originX, originY, anchorX, anchorY, hx, hy int32) *drawlist.ModelGeometry {
	if c == nil || src == nil || !c.modelScratch.active {
		return nil
	}
	p := c.borrowPacketScratch()
	p.g = *src
	dx, dy := originX-src.OriginX, originY-src.OriginY
	// A lane filled in its store may already carry a half-pixel offset
	// (cachedGeometryStore.fill); only the difference is added.
	sdx, sdy := 2*dx+hx, 2*dy+hy
	if store != nil && src == &store.g {
		sdx, sdy = sdx-store.doubledHX, sdy-store.doubledHY
	}
	shared, sharedDoubled, ok := store.rebasedFaces(src, dx, dy, sdx, sdy, c.modelFrameSerial)
	if ok {
		p.g.Faces = shared
	} else {
		p.cachedFaces, p.cachedVertices = copyModelFaces(p.cachedFaces, p.cachedVertices, src.Faces, dx, dy)
		p.g.Faces = p.cachedFaces
	}
	p.g.LiveFaces, p.g.Outline = nil, nil
	p.g.Reveal, p.g.Shadow, p.g.Children = nil, nil, nil
	p.g.Waterline, p.g.Digger = drawlist.ModelWaterlineNone, false
	p.g.Width, p.g.Height, p.g.OriginX, p.g.OriginY, p.g.AnchorX, p.g.AnchorY = width, height, originX, originY, anchorX, anchorY
	if src.Supersample == nil {
		p.g.Supersample = nil
		return &p.g
	}
	p.supersample = *src.Supersample
	if ok {
		p.supersample.Faces = sharedDoubled
	} else {
		p.supersampleFaces, p.supersampleVerts = copyModelFaces(p.supersampleFaces, p.supersampleVerts, src.Supersample.Faces, sdx, sdy)
		p.supersample.Faces = p.supersampleFaces
	}
	p.supersample.LiveFaces, p.supersample.Outline, p.supersample.Reveal, p.supersample.Shadow, p.supersample.Children = nil, nil, nil, nil, nil
	p.supersample.Width, p.supersample.Height = 2*width, 2*height
	p.supersample.OriginX, p.supersample.OriginY = 2*originX, 2*originY
	p.supersample.AnchorX, p.supersample.AnchorY = 2*originX, 2*originY
	p.g.Supersample = &p.supersample
	return &p.g
}

// resizeModelFaces releases only records that the refill will not overwrite.
// Keeping the face allocation warm must not keep a previous corner allocation
// alive through its unused tail [DESIGN_GPU_RENDERER.md §11.5 "CPU"].
func resizeModelFaces(v []drawlist.ModelFace, n int) []drawlist.ModelFace {
	if n < len(v) {
		clear(v[n:])
	}
	return resizeScratch(v, n)
}

func copyModelFaces(dst []drawlist.ModelFace, vertices []drawlist.ModelVertex, src []drawlist.ModelFace, dx, dy int32) ([]drawlist.ModelFace, []drawlist.ModelVertex) {
	dst = resizeModelFaces(dst, len(src))
	count := 0
	for i := range src {
		count += len(src[i].Vertices)
	}
	vertices = resizeScratch(vertices, count)
	off := 0
	for i := range src {
		face := src[i]
		n := len(face.Vertices)
		dst[i] = face
		dst[i].Vertices = vertices[off : off+n : off+n]
		for j := range face.Vertices {
			vertex := face.Vertices[j]
			vertex.X, vertex.Y = vertex.X+dx, vertex.Y+dy
			dst[i].Vertices[j] = vertex
		}
		off += n
	}
	return dst, vertices
}

func (c *Client) directUnitGeometry(draw *presentationrender.UnitDraw, selector teamColor, id uint64, lane presentationrender.PieceLane) *drawlist.ModelGeometry {
	return c.directModelGeometry(draw, selector, id, modelCursorUnit, lane)
}

// directModelGeometry records the standalone direct projection. Its corners
// are already framebuffer offsets, so the packet's local space is screen space:
// the composition box is the corners' own extent with retail's margin, its
// origin the box pixel of screen (0,0), and its anchor screen (0,0). The box
// used to be the whole record extent, which reserved a framebuffer-sized slot
// per direct subject and could not be doubled at all.
func (c *Client) directModelGeometry(draw *presentationrender.UnitDraw, selector teamColor, id uint64, kind uint8, lane presentationrender.PieceLane) *drawlist.ModelGeometry {
	polys := c.collectDrawPolysLaneProjected(draw, selector, id, kind, lane, true)
	if len(polys) == 0 {
		return nil
	}
	width, height, originX, originY := directModelExtent(polys)
	// Every corner of the direct projection carries its own exact doubled
	// position, so the doubled lane takes those rather than a subject offset.
	supersample := c.modelSupersampleGeometry(polys, false, width, height, doubledPlacement{originX: originX, originY: originY, exact: true})
	placeFaces(polys, originX, originY, 1)
	g := c.borrowModelPacket(polys, int32(width), int32(height), originX, originY, 0, 0, 1, false, drawlist.ModelFallbackNone)
	g.Supersample = supersample
	c.setModelLightingHeight(g, draw)
	return g
}

// directModelExtent is modelExtent for direct-projected corners: seeded at the
// first corner rather than at the model origin, because the corners are screen
// offsets and screen (0,0) has nothing to do with the subject. The exact
// doubled corners are counted at their whole pixel too, so the box holds the
// doubled lane whatever the view scale.
func directModelExtent(polys []screenPoly) (width, height int, originX, originY int32) {
	minX, minY, maxX, maxY := polys[0].x[0], polys[0].y[0], polys[0].x[0], polys[0].y[0]
	for i := range polys {
		xs, ys, xs2, ys2 := polys[i].x, polys[i].y, polys[i].x2, polys[i].y2
		for k := range xs {
			minX, maxX = min(minX, xs[k], xs2[k]>>1), max(maxX, xs[k], xs2[k]>>1)
			minY, maxY = min(minY, ys[k], ys2[k]>>1), max(maxY, ys[k], ys2[k]>>1)
		}
	}
	originX = modelTargetMargin - minX
	originY = modelTargetMargin - minY
	return int(maxX - minX + 2*modelTargetMargin), int(maxY - minY + 2*modelTargetMargin), originX, originY
}

// directDebrisGeometry applies the detached-piece origin gate independently
// of face overlap [03 R-COMP-02 §6].
func (c *Client) directDebrisGeometry(draw *presentationrender.UnitDraw, selector teamColor, id uint64) *drawlist.ModelGeometry {
	if !c.directModelOriginVisible(draw) {
		return nil
	}
	return c.directModelGeometry(draw, selector, id, modelCursorDebris, presentationrender.PieceLaneAll)
}

// modelSupersampleGeometry is the doubled raster of one lane, placed with the
// subject's half-pixel offset (DESIGN_GPU_RENDERER §17). It shares retail's
// doubled projection, including its odd height correction [03 R-REN-03A §6],
// and targets a bounded local GPU image; final placement remains on the outer
// packet. It is filled from the UNPLACED polygons, so the caller places them
// natively afterwards.
func (c *Client) modelSupersampleGeometry(polys []screenPoly, keyPlane bool, width, height int, place doubledPlacement) *drawlist.ModelGeometry {
	if !c.supersampleGeometry() {
		return nil
	}
	return c.borrowModelPacketDoubled(polys, width, height, place, keyPlane)
}
