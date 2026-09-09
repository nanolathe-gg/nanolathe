package client

import (
	"github.com/nanolathe/nanolathe/internal/drawlist"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

// modelGeometryPacket makes the durable P3 input from the already-resolved
// composition polygons. The composition walk has selected texture frames,
// converted every corner to the subject-local image coordinates and retained
// the load-fixed face order. Copying here makes the packet independent of the
// raster scratch and of the next recording [03 R-REN-03A §1–§3].
func modelGeometryPacket(polys []screenPoly, target *modelTarget, scale int32, fallback drawlist.ModelFallbackReason) *drawlist.ModelGeometry {
	if target == nil {
		return nil
	}
	return modelGeometryPacketAt(polys, int32(target.width), int32(target.heightPx), target.originX, target.originY, target.anchorX, target.anchorY, scale, target.height != nil, fallback)
}

// modelGeometryPacketAt constructs the device packet without a modelTarget.
// Modern recording uses it so geometry preparation never creates colour,
// coverage, or height planes.
func modelGeometryPacketAt(polys []screenPoly, width, height, originX, originY, anchorX, anchorY, scale int32, keyPlane bool, fallback drawlist.ModelFallbackReason) *drawlist.ModelGeometry {
	return fillModelPacket(&drawlist.ModelGeometry{}, nil, polys, width, height, originX, originY, anchorX, anchorY, scale, keyPlane, fallback)
}
func fillModelPacket(g *drawlist.ModelGeometry, vertices []drawlist.ModelVertex, polys []screenPoly, width, height, originX, originY, anchorX, anchorY, scale int32, keyPlane bool, fallback drawlist.ModelFallbackReason) *drawlist.ModelGeometry {
	*g = drawlist.ModelGeometry{
		Eligible: fallback == drawlist.ModelFallbackNone,
		Fallback: fallback,
		Faces:    resizeScratch(g.Faces, len(polys)),
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
		key, u, v, row := p.attr[spanKey], p.attr[spanU], p.attr[spanV], p.attr[spanRow]
		for j := 0; j < n; j++ {
			face.Vertices[j] = drawlist.ModelVertex{
				X: p.x[j], Y: p.y[j], Key: key[j],
				U: u[j], V: v[j], Shade: uint8(row[j]),
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
	if reveal != nil {
		g.Reveal = &drawlist.ModelReveal{Line: reveal.Line, Floor: reveal.Floor, Below: reveal.Below, Band: reveal.Band, Above: reveal.Above}
		g.Outline = c.modelOutlineGeometry(draw, g.OriginX, g.OriginY, outline)
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
	g.Shadow = c.modelShadowGeometry(draw)
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
func (c *Client) modelOutlineGeometry(draw *presentationrender.UnitDraw, originX, originY int32, color uint8) []drawlist.ModelFace {
	s := c.borrowOutline()
	faces, verts, spans := s.faces[:0], s.verts[:0], s.spans[:0]
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
				x, y, _ := modelLocalVertex(v, draw.WorldPos)
				x, y = c.scaleModelLocal(x, y)
				verts = append(verts, drawlist.ModelVertex{X: x + originX, Y: y + originY, Key: modelHeightKey(v[1].Sub(draw.WorldPos[1]), draw.DiggerClip)})
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
// classic approximation described at buildModelShadow [03 R-REN-03D §1–§5].
func (c *Client) modelShadowGeometry(draw *presentationrender.UnitDraw) *drawlist.ModelGeometry {
	if c == nil || draw == nil || !draw.CastsShadow || c.pal == nil {
		return nil
	}
	polys := c.collectShadowPolys(draw)
	if len(polys) == 0 {
		return nil
	}
	width, height, originX, originY := modelExtent(polys)
	anchorX, anchorY := c.shadowAnchor(draw)
	placeFaces(polys, originX, originY, 1)
	return c.borrowModelPacket(polys, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, true, drawlist.ModelFallbackNone)
}

func (c *Client) prepareModelGeometry(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) *drawlist.ModelGeometry {
	polys := c.collectDrawPolys(draw, selector, id, kind)
	if len(polys) == 0 {
		return nil
	}
	anchorX, anchorY := c.modelAnchor(draw)
	width, height, originX, originY := modelExtent(polys)
	supersample := c.modelSupersampleGeometry(polys, draw, width, height, originX, originY)
	placeFaces(polys, originX, originY, 1)
	g := c.borrowModelPacket(polys, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, draw.KeyPlane, drawlist.ModelFallbackNone)
	c.configureModelGeometry(g, draw, owner, kind, reveal, outline)
	g.Supersample = supersample
	if supersample != nil {
		supersample.Reveal = g.Reveal
	}
	return g
}

// The doubled projection shares placeFaces with classic, including its odd
// height correction. It targets a bounded local GPU image; final placement
// remains on the outer packet [03 R-REN-03A §6].
func (c *Client) modelSupersampleGeometry(polys []screenPoly, draw *presentationrender.UnitDraw, width, height int, originX, originY int32) *drawlist.ModelGeometry {
	if !c.supersampleModel(draw.Structure) {
		return nil
	}
	faces := c.cloneModelPolys(polys)
	placeFaces(faces, originX, originY, 2)
	return c.borrowModelPacket(faces, int32(2*width), int32(2*height), 2*originX, 2*originY, 2*originX, 2*originY, 2, draw.KeyPlane, drawlist.ModelFallbackNone)
}
