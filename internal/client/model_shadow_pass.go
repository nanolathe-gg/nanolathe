package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Model shadows [R-REN-03D]. Retail does not build a stencil and does not
// darken through an SHD row. It rasterizes the model a second time with a
// 45-degree shear, fills every face solid with palette index 0, and composites
// that silhouette over the ground through the tinted blitter, which is an ALP
// blend of the shadow pixel with whatever is already there.

// shadowKeyBias is the shadow image's own height-key base. The body image uses
// 50; the shadow rasterizer uses 25 [R-REN-03D §2].
const shadowKeyBias int32 = 25

// shadowColorIndex is the index every shadow face is filled with. The tinted
// blit then resolves each ground pixel to ALP[0*256 + ground], which is that
// pixel blended halfway toward black [R-REN-03D §2][R-REN-03D §4].
const shadowColorIndex uint8 = 0

// shadowXOffset is the shadow's placement offset relative to the body: retail
// blits the body at +128 and the shadow at +133 [R-REN-03D §3].
const shadowXOffset int32 = 5

// castsModelShadow resolves the shadow gate. Retail selects one of three
// shadow branches once the master-shadows bit and the definition's
// `noshadow` have passed: a Digger's buried-clip silhouette, a mobile unit's
// waterline-clip silhouette (both additionally gated on the vehicle-shadow
// bit and on neither `canhover` nor `floater` being authored), and a
// structure's re-rasterized, punched, cached shadow, which tests only the
// master bit and `noshadow` — not the vehicle-shadow bit, not `canhover`,
// not `floater` [R-REN-03D §1, corrected]. This client implements only the
// structure branch's rasterize/punch/tint technique (see punchOut's doc
// comment); it is applied here for both structure-class draws (the
// technique the retail structure branch itself uses) and for the
// mobile/Digger branches it stands in for until their own silhouette-clip
// shadows are built, so the vehicle-shadow gate still applies to those.
//
// The tinted blitter's alpha-blend capability is enabled at window startup,
// independently of the SHADING preference [03 R-REN-03D §4]. SHADING selects
// shaded structure bodies [R-RND-02A]; it does not suppress shadows.
func (c *Client) castsModelShadow(noShadow, canHover, floater, structure bool) bool {
	if c == nil || c.pal == nil {
		return false
	}
	if !c.shadows || noShadow {
		return false
	}
	if structure {
		return true
	}
	return c.vehicleShadows && !canHover && !floater
}

// shadowLocalVertex is the shadow projection. It is the body's own narrowing
// ([R-RAST-01 §2]) — X passes through, and the screen Y lane is the high word
// of the *negated* model Z, the 3DO handedness flip — with the body's
// half-height shear replaced by a quarter-height shear applied positively in X
// and negatively in screen Y. That quarter term is a 45-degree light in screen
// space, and the absence of any half-height term is what lays the shadow flat
// on the ground [R-REN-03D §2].
//
// This previously narrowed Z with no negation, reading [R-REN-03D §2]'s code
// block literally where it wrote the screen Y lane from the plain vertex Z.
// That block has since been corrected in place — see its "Correction
// (2026-08-30)" — because it predated [R-RAST-01 §2]'s negate-then-floor
// narrowing, contradicted its own section's prose naming exactly three
// differences from the body walk (none of them a Z sign), and made a
// structure's shadow a Z-mirrored copy of its own body that swung the wrong
// way as the unit turned. Two projections of one model do not disagree on
// handedness.
func shadowLocalVertex(v, origin [3]numeric.Fixed) (sx, sy, ry int32) {
	rx := int32(v[0].Sub(origin[0]).Floor())
	ry = int32(v[1].Sub(origin[1]).Floor())
	zn := int32((-(v[2].Sub(origin[2]))).Floor()) // Zn = hi16(-vz) [R-RAST-01 §2]
	q := ry >> 2
	return rx + q, zn - q, ry
}

// collectShadowPolys walks the model exactly as the body pass does — pieces
// last to first, the selection primitive skipped — but every primitive goes to
// the flat polygon filler at any vertex count, textures are never consulted, no
// SHD row is computed, and the projection is the shadow shear [R-REN-03D §2].
//
// It emits the same screenPoly the body pass does, so the shadow is scan
// converted by the one two-chain edge walk of [R-RAST-01 §1] rather than by a
// second, fan-triangulated filler. Two rasterizers for one contract is one
// rasterizer too many: the fan walk admitted its own set of edge pixels, so a
// structure's shadow had a silhouette its body could never have.
func (c *Client) collectShadowPolys(draw *presentationrender.UnitDraw) []screenPoly {
	if c == nil || draw == nil || draw.Model == nil {
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

	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		piece := &draw.Pieces[pi]
		for pri := range piece.Primitives {
			pr := &piece.Primitives[pri]
			if draw.Model.Pieces[pi].Selection && pri == 0 {
				continue
			}
			n := len(pr.VertexIndices)
			if n < 3 {
				continue
			}
			valid := true
			for _, vi := range pr.VertexIndices {
				if int(vi) >= len(piece.WorldVertices) {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}
			if !facePaints(piece.WorldVertices, pr.VertexIndices, draw.WorldPos, shadowLocalVertex) {
				// The shadow rerasterizes every face through the same
				// two-chain filler as the body, so the same span comparison
				// removes the same back faces [R-RAST-01 §1] step 7. The test
				// runs on the shadow's own quarter-shear projection, not the
				// body's: two projections of one face can wind differently.
				continue
			}
			poly := scratch.next(n)
			// Every shadow face is the literal palette index 0 and carries no
			// SHD row, so the flat writer puts that byte down raw
			// [R-REN-03D §2].
			poly.color, poly.useSHD = shadowColorIndex, false
			poly.candidate, poly.piece, poly.primitive = uint32(len(scratch.polys)-1), pi, pri
			for corner, vi := range pr.VertexIndices {
				sx, sy, ry := shadowLocalVertex(piece.WorldVertices[vi], draw.WorldPos)
				sx, sy = c.scaleModelLocal(sx, sy)
				poly.x[corner], poly.y[corner] = sx, sy
				poly.attr[spanKey][corner] = ry + shadowKeyBias
			}
		}
	}
	return scratch.polys
}

// buildModelShadow rasterizes one model shadow into its own finished, punched
// composition image and returns it, or nil when the subject casts no shadow or
// the shadow has no faces. It is the whole of the old drawModelShadow body up to
// (but not including) the tinted commit. The classic path commits it through
// ALP; modern mode records the same projection for its own GPU shadow pass
// [DESIGN_GPU_RENDERER.md §10]. body is the subject's finished composition image
// the punch-out reads to leave a hole under the hull [R-REN-03D §5][R-RAST-01 §4].
func (c *Client) buildModelShadow(draw *presentationrender.UnitDraw, body *modelTarget) *modelTarget {
	if c == nil || draw == nil || !draw.CastsShadow || c.pal == nil {
		return nil
	}
	polys := c.collectShadowPolys(draw)
	if len(polys) == 0 {
		return nil
	}
	width, height, originX, originY := modelExtent(polys)
	anchorX, anchorY := c.shadowAnchor(draw)
	// The shadow image is never supersampled: [R-REN-03A §6]'s gate is about
	// the body composition, and §2 measures and allocates the shadow image
	// exactly as the body image is measured at 1x.
	placeFaces(polys, originX, originY, 1)
	img := c.borrowModelImage(width, height, originX, originY, anchorX, anchorY, true, 1)
	for i := range polys {
		c.fillPolyTarget(img, &polys[i], shadowColorIndex, nil)
	}
	img.punchOut(body)
	return img
}

// shadowAnchor places the shadow. It uses the same X as the body plus five
// pixels, and shears Y by the terrain height under the unit rather than by the
// unit's own height, which is what slides a shadow across a slope
// [R-REN-03D §3].
func (c *Client) shadowAnchor(draw *presentationrender.UnitDraw) (int32, int32) {
	if c == nil || c.cam == nil {
		return 0, 0
	}
	sx, sy := c.cam.WorldToScreen(draw.WorldPos[0], draw.GroundY, draw.WorldPos[2])
	return sx - camera.OriginX + shadowXOffset, sy - camera.OriginY
}

// punchOut writes the shadow image's own transparent index wherever the body
// image is opaque, so the ground directly under the body is not darkened before
// the body covers it [R-REN-03D §5].
//
// The previous note here left the punch unimplemented, recording that the two
// five-pixel offsets involved — one applied to the body column during the
// punch, one applied to the whole shadow image at the blit — composed in a way
// the trace had not settled. [R-RAST-01 §4] closed that: they are the
// difference between shifting a source and shifting a destination, they cancel
// exactly, and the hole lands at the body's own screen position on both axes.
// The advice to omit the punch is withdrawn there.
//
// Both images already carry the framebuffer anchor they will be blitted at, so
// mapping each opaque body pixel out to the framebuffer and back into the
// shadow image reproduces that cancellation directly, rather than restating two
// offsets that annihilate.
//
// [R-REN-03D §5] attaches the punch to the structure rasterization of §2, which
// is the one shadow path this client implements; when the Digger and mobile
// silhouette branches of [R-REN-03D §6] are added, the punch stays with the
// structure branch only.
func (t *modelTarget) punchOut(body *modelTarget) {
	if t == nil || body == nil {
		return
	}
	for by := 0; by < body.heightPx; by++ {
		iy := t.imageY(body.screenY(int32(by)))
		if iy < 0 || iy >= int32(t.heightPx) {
			continue
		}
		row := int(iy) * t.width
		src := by * body.width
		for bx := 0; bx < body.width; bx++ {
			if !body.covered[src+bx] {
				continue
			}
			ix := t.imageX(body.screenX(int32(bx)))
			if ix < 0 || ix >= int32(t.width) {
				continue
			}
			i := row + int(ix)
			t.color[i], t.covered[i] = t.transparent, false
		}
	}
}

// tintedCommit is retail's translucent image blit: every source pixel that is
// not the image's background resolves the destination to ALP[src*256 + dst].
// With a shadow silhouette, whose every pixel is index 0, that darkens each
// ground pixel halfway toward black [R-REN-03D §4].
func (t *modelTarget) tintedCommit(dst []uint8, width, height int, alp *[65536]byte) {
	if t == nil || alp == nil || width <= 0 || height <= 0 {
		return
	}
	for iy := 0; iy < t.heightPx; iy++ {
		sy := t.screenY(int32(iy))
		if sy < 0 || sy >= int32(height) {
			continue
		}
		row := int(sy) * width
		src := iy * t.width
		for ix := 0; ix < t.width; ix++ {
			i := src + ix
			if !t.covered[i] {
				continue
			}
			sx := t.screenX(int32(ix))
			if sx < 0 || sx >= int32(width) {
				continue
			}
			d := row + int(sx)
			dst[d] = alp[int(t.color[i])*256+int(dst[d])]
		}
	}
}

// eraseAtOrBelow writes the image background over every pixel whose height key
// is at or below the threshold. Retail runs it over the finished composition
// image for the waterline and for the digger clip [R-REN-03A §8].
func (t *modelTarget) eraseAtOrBelow(threshold uint8) {
	if t == nil || t.height == nil {
		return
	}
	for i := range t.color {
		if t.height[i] <= threshold {
			t.color[i], t.covered[i] = t.transparent, false
		}
	}
}
