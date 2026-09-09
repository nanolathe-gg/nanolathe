package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// fragmentTexture reads immutable entry data at the admission-time frame. In
// particular neither the phase-7 cursor nor the player's current LOGOS colour
// participates in this lookup [04 R-COB-04 §3].
func (c *Client) fragmentTexture(v frame.FragmentView) *formats.GAFFrame {
	if c == nil || c.modelTextures == nil || !v.MaterialValid {
		return nil
	}
	r := c.modelTextures
	m := r.unitModel("", v.UnitDefID, "")
	if m == nil || m.compiled == nil || v.PieceIndex < 0 || v.PieceIndex >= len(m.compiled.Pieces) {
		return nil
	}
	p := &m.compiled.Pieces[v.PieceIndex]
	if v.PrimitiveIndex < 0 || v.PrimitiveIndex >= len(p.Primitives) {
		return nil
	}
	pr := &p.Primitives[v.PrimitiveIndex]
	ref, ok := resolveTextureRef(r.primary, r.logos, ckey(pr.TextureName))
	if !ok {
		return nil
	}
	if ref.kind == texStatic {
		return ref.frame
	}
	if ref.entry == nil || v.FrameIndex < 0 || int(v.FrameIndex) >= len(ref.entry.Frames) {
		return nil
	}
	return ref.entry.Frames[v.FrameIndex].Frame
}

// fragmentFaces is the initialized six-quad template in stored painter order;
// admission copies the source material onto every face [04 R-COB-04 §5].
var fragmentFaces = [6][4]int{
	{0, 1, 2, 3}, {2, 1, 6, 5}, {0, 3, 4, 7},
	{1, 0, 7, 6}, {3, 2, 5, 4}, {4, 5, 6, 7},
}

// collectFragmentPolys presents the six template faces, preserving their stored
// winding and extrusion. The standalone rotation and projection have no key
// plane or shading; both executors consume these same polygons
// [04 R-COB-04 §3][03 R-COMP-02 §6].
func (c *Client) collectFragmentPolys(v frame.FragmentView, texture *formats.GAFFrame) []screenPoly {
	if texture == nil {
		return nil
	}
	pieces := [1]model.Piece{{Parent: -1}}
	m := model.Model{Pieces: pieces[:], Root: 0}
	// Fragment lanes retain the initializer's raw order: Z, Y, X. The
	// standalone helper rotates Z, then X, then Y [04 R-COB-04 §3].
	states := [1]model.PieceState{{RotX: v.Angles[2], RotY: v.Angles[1], RotZ: v.Angles[0]}}
	transform := model.Compose(&m, states[:], 0)
	var vertices [8][3]numeric.Fixed
	transform.ApplyOffsetInto(vertices[:], v.Vertices[:], v.Position)
	scratch := c.borrowPolys(len(fragmentFaces), 4*len(fragmentFaces))
	u := [4]int32{0, int32(texture.Width) - 1, int32(texture.Width) - 1, 0}
	vv := [4]int32{0, 0, int32(texture.Height) - 1, int32(texture.Height) - 1}
	for _, indices := range fragmentFaces {
		poly := scratch.next(4)
		poly.frame = texture
		poly.piece, poly.primitive = v.PieceIndex, v.PrimitiveIndex
		for corner := 0; corner < 4; corner++ {
			poly.x[corner], poly.y[corner] = c.modelDirectVertex(vertices[indices[corner]], v.Position)
			poly.attr[spanU][corner], poly.attr[spanV][corner] = u[corner], vv[corner]
		}
	}
	return scratch.polys
}

func (c *Client) drawFragment(v frame.FragmentView) bool {
	draw := render.UnitDraw{WorldPos: v.Position}
	if !c.directModelOriginVisible(&draw) {
		return false
	}
	polys := c.collectFragmentPolys(v, c.fragmentTexture(v))
	if len(polys) == 0 {
		return false
	}
	minX, minY, maxX, maxY, ok := directProjectedBounds(polys, int32(c.width), int32(c.height))
	if !ok {
		return false
	}
	placeFaces(polys, -minX, -minY, 1)
	if c.geometryOnlyModels {
		// The fragment needs only its clipped projected box, just like the
		// direct classic image. Rebasing corners and the packet origin by
		// the same offset retains every framebuffer coordinate, while avoiding
		// a viewport-sized device target for each fragment [03 R-COMP-02 §6].
		g := c.borrowModelPacket(polys, maxX-minX+1, maxY-minY+1, -minX, -minY, 0, 0, 1, false, drawlist.ModelFallbackNone)
		c.list.RecordModel(drawlist.Model{Geometry: g})
		return true
	}
	target := c.borrowModelImage(int(maxX-minX+1), int(maxY-minY+1), -minX, -minY, 0, 0, false, 1)
	target.blit = 1
	for i := range polys {
		c.blitTexturedPolyTarget(target, &polys[i], polys[i].frame, nil, uint64(v.Slot))
	}
	c.emitModel(pendingModelCommit{m: composedModel{image: target, raster: target, direct: true}, blit: target, body: true})
	return true
}
