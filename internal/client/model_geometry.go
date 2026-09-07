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
	g := &drawlist.ModelGeometry{
		Eligible: fallback == drawlist.ModelFallbackNone,
		Fallback: fallback,
		Faces:    make([]drawlist.ModelFace, len(polys)),
		Width:    width, Height: height,
		OriginX: originX, OriginY: originY,
		AnchorX: anchorX, AnchorY: anchorY,
		Scale: scale, KeyPlane: keyPlane,
	}
	for i := range polys {
		p := polys[i]
		face := drawlist.ModelFace{
			Vertices: make([]drawlist.ModelVertex, len(p.x)),
			Texture:  p.frame,
			Color:    p.color,
			Shaded:   p.useSHD,
		}
		for j := range p.x {
			face.Vertices[j] = drawlist.ModelVertex{
				X: p.x[j], Y: p.y[j], Key: p.attr[spanKey][j],
				U: p.attr[spanU][j], V: p.attr[spanV][j], Shade: uint8(p.attr[spanRow][j]),
			}
		}
		g.Faces[i] = face
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
func (c *Client) modelOutlineGeometry(draw *presentationrender.UnitDraw, originX, originY int32, color uint8) []drawlist.ModelFace {
	var faces []drawlist.ModelFace
	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		piece := draw.Pieces[pi]
		for pri, pr := range piece.Primitives {
			if draw.Model.Pieces[pi].Selection && pri == 0 || len(pr.VertexIndices) < 2 {
				continue
			}
			face := drawlist.ModelFace{Color: color}
			for _, vi := range pr.VertexIndices {
				if int(vi) >= len(piece.WorldVertices) {
					face.Vertices = nil
					break
				}
				v := piece.WorldVertices[vi]
				x, y, _ := modelLocalVertex(v, draw.WorldPos)
				x, y = c.scaleModelLocal(x, y)
				face.Vertices = append(face.Vertices, drawlist.ModelVertex{X: x + originX, Y: y + originY, Key: modelHeightKey(v[1].Sub(draw.WorldPos[1]), draw.DiggerClip)})
			}
			if len(face.Vertices) >= 2 {
				faces = append(faces, face)
			}
		}
	}
	return faces
}

// geometryForCommit preserves the recorded shadow/body separation. A staging
// body is represented by its carrier and ordered child packets, never a CPU
// composition image [03 R-REN-03A §4].
func geometryForCommit(p pendingModelCommit) *drawlist.ModelGeometry {
	if p.m.geometry == nil {
		return nil
	}
	g := p.m.geometry.Clone()
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
	return modelGeometryPacketAt(polys, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, true, drawlist.ModelFallbackNone)
}

func (c *Client) prepareModelGeometry(draw *presentationrender.UnitDraw, owner uint8, selector teamColor, id uint64, kind uint8, reveal *presentationrender.NanoframeReveal, outline uint8) *drawlist.ModelGeometry {
	polys := c.collectDrawPolys(draw, selector, id, kind)
	if len(polys) == 0 {
		return nil
	}
	anchorX, anchorY := c.modelAnchor(draw)
	width, height, originX, originY := modelExtent(polys)
	placeFaces(polys, originX, originY, 1)
	g := modelGeometryPacketAt(polys, int32(width), int32(height), originX, originY, anchorX, anchorY, 1, draw.KeyPlane, drawlist.ModelFallbackNone)
	c.configureModelGeometry(g, draw, owner, kind, reveal, outline)
	return g
}
