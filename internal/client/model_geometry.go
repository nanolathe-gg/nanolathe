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
	g := &drawlist.ModelGeometry{
		Eligible: fallback == drawlist.ModelFallbackNone,
		Fallback: fallback,
		Faces:    make([]drawlist.ModelFace, len(polys)),
		Width:    int32(target.width), Height: int32(target.heightPx),
		OriginX: target.originX, OriginY: target.originY,
		AnchorX: target.anchorX, AnchorY: target.anchorY,
		Scale: scale, KeyPlane: target.height != nil,
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

// geometryCanUseBodyPath names only composition stages P2 actually encodes.
// Shadows remain classic fallback, and a later GPU body consumer must preserve
// that command ordering. The packet does represent the all-piece transformed
// pose it receives. Construction/reveal, outlines, waterline/digger operations
// and the supersample resolve are not represented, so they are deliberately
// ineligible rather than guessed.
func (c *Client) geometryFallback(draw *presentationrender.UnitDraw, reveal *presentationrender.NanoframeReveal, outline uint8, scale int32) drawlist.ModelFallbackReason {
	if c == nil || draw == nil || reveal != nil || outline != 0 || scale != 1 || draw.DiggerClip {
		if scale != 1 {
			return drawlist.ModelFallbackSupersample
		}
		if draw != nil && draw.DiggerClip {
			return drawlist.ModelFallbackWaterlineOrDigger
		}
		return drawlist.ModelFallbackRevealOrOutline
	}
	if _, submerged := waterlineThreshold(c.seaLevel(), draw.WorldPos[1], false); submerged {
		return drawlist.ModelFallbackWaterlineOrDigger
	}
	// The packet records the actual all-piece transformed polygons, so a static
	// supplied pose is representable. A future cached/live split must receive a
	// distinct stage marker and fall back until that marker is carried.
	return drawlist.ModelFallbackNone
}

// geometryForCommit gives shadow-only, trace-only and staging commands an
// explicit CPU fallback. A body may only be GPU eligible when it commits the
// model's own image; a staging image carries child composition that P2 does not
// flatten into geometry [03 R-REN-03A §4].
func geometryForCommit(p pendingModelCommit) *drawlist.ModelGeometry {
	if p.m.geometry == nil {
		return nil
	}
	g := p.m.geometry.Clone()
	if !p.body || p.blit == nil || p.blit != p.m.image {
		g.Eligible = false
		if !p.body {
			g.Fallback = drawlist.ModelFallbackNoBodyCommit
		} else {
			g.Fallback = drawlist.ModelFallbackStaging
		}
	}
	return g
}
