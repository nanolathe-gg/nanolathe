package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The keyed (non-destination-reading) image-blit families for the modern
// executor: the keyed GAF sprite (anchored and plain), the scaled GAF blit, the
// 2D feature GAF copy, the opaque PCX background, the indexed surface and the
// software cursor (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). Each reproduces its
// classic byte writer's covered-pixel set and per-pixel value exactly: an index
// texture carries the frame's bytes in the red channel and its opacity in green,
// and a keyed blit draws it under the source-over blend so a texel the byte
// writer skips leaves the destination untouched (a discard), while an opaque
// texel overwrites the index with no blend arithmetic on red [03 R-RAST-01 §6]
// [03 §4.4][07 §4].
//
// The destination-reading kinds — BlitTinted and BlitFeatureShadow — and the
// source-through-LHT BlitLit are implemented in deststage.go (WU-2.6): the
// dest-reading kinds run over a per-command snapshot of the offscreen, exactly as
// the classic sink's counterparts read c.indexed. Only Model stays stubbed.

// buildGAFFrameImage uploads one GAF frame as an index texture: index in red,
// opacity flag in green (255 opaque, 0 transparent), alpha opaque so premultiplied
// sampling recovers both bytes (C-G4). Opacity is baked from the frame's own
// Transparent mask, the same signal every classic byte writer tests — the raw
// color key, the RLE skip runs and the composite coverage all reduce to it
// [fmt gaf]. A texel past the decoded pixel length is transparent, matching the
// byte writers' short-array skip.
func buildGAFFrameImage(f *formats.GAFFrame) *ebiten.Image {
	fw, fh := int(f.Width), int(f.Height)
	if fw <= 0 || fh <= 0 {
		return nil
	}
	np := len(f.Pixels)
	nt := len(f.Transparent)
	buf := make([]byte, fw*fh*4)
	for i := 0; i < fw*fh; i++ {
		// Opaque iff the pixel exists and is not flagged transparent — the same
		// admission blitGAFFrame and uiBlitClippedRaw make per pixel [03 §4.4].
		opaque := i < np && (i >= nt || !f.Transparent[i])
		if opaque {
			buf[i*4+0] = f.Pixels[i]
			buf[i*4+1] = 255
		}
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(fw, fh)
	img.WritePixels(buf)
	return img
}

// gafImageFor returns the cached index texture for f, building it on first use and
// caching it by pointer identity (docs/DESIGN_GPU_RENDERER.md §2.3). A nil frame
// or an empty frame has no image. The nil result is cached too, so a degenerate
// frame is not rebuilt every call.
func (r *Renderer) gafImageFor(f *formats.GAFFrame) *ebiten.Image {
	if f == nil {
		return nil
	}
	if img, ok := r.gafImages[f]; ok {
		return img
	}
	img := buildGAFFrameImage(f)
	r.gafImages[f] = img
	return img
}

// buildPCXImage uploads one PCX image as an index texture: index in red, alpha
// opaque (C-G4). PCX frontend backgrounds are copied opaquely — no key applies —
// so no opacity flag is needed and the atlas shader copies the red channel under
// BlendCopy [fmt pcx][07 "Retail palette contract"].
func buildPCXImage(p *formats.PCX) *ebiten.Image {
	pw, ph := int(p.Width), int(p.Height)
	if pw <= 0 || ph <= 0 {
		return nil
	}
	buf := make([]byte, pw*ph*4)
	n := len(p.Pixels)
	for i := 0; i < pw*ph; i++ {
		if i < n {
			buf[i*4+0] = p.Pixels[i]
		}
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(pw, ph)
	img.WritePixels(buf)
	return img
}

// pcxImageFor returns the cached index texture for p, built on first use and
// cached by pointer identity (docs/DESIGN_GPU_RENDERER.md §2.3).
func (r *Renderer) pcxImageFor(p *formats.PCX) *ebiten.Image {
	if p == nil {
		return nil
	}
	if img, ok := r.pcxImages[p]; ok {
		return img
	}
	img := buildPCXImage(p)
	r.pcxImages[p] = img
	return img
}

// Sprite replays one GAF-frame or PCX blit, routing each recorded blit to the GPU
// pass that matches its classic byte writer (docs/DESIGN_GPU_RENDERER.md §2.3).
// A non-nil PCX is an opaque background blit regardless of Kind; the keyed kinds
// (anchored/plain), the scaled kind and the feature-normal kind are copied here;
// the destination-reading kinds draw nothing and are left to a later unit.
func (r *Renderer) Sprite(sp drawlist.Sprite) {
	if r == nil || r.offscreen == nil {
		return
	}
	// A non-nil PCX carries an opaque frontend background; it cannot ride
	// Sprite.Frame, so it is routed to the PCX pass regardless of Kind, honoring the
	// recorded clip [fmt pcx][07 "Retail palette contract"].
	if sp.PCX != nil {
		clipX, clipY, clipW, clipH := r.spriteClip(sp.HasClip, sp.Clip)
		r.drawPCX(sp.PCX, int(sp.X), int(sp.Y), clipX, clipY, clipW, clipH)
		return
	}
	switch sp.Kind {
	case drawlist.BlitKeyed:
		if sp.Anchored {
			// UIBlitAnchor: subtract the frame's authored offsets, then a full-
			// framebuffer keyed copy [03 R-RAST-01 §6][fmt gaf].
			if sp.Frame == nil {
				return
			}
			r.drawKeyed(sp.Frame, int(sp.X)-int(sp.Frame.XOffset), int(sp.Y)-int(sp.Frame.YOffset),
				0, 0, r.w, r.h)
		} else {
			// UIBlit: the rectangle is the contract, no offset [07 §4].
			clipX, clipY, clipW, clipH := r.spriteClip(sp.HasClip, sp.Clip)
			r.drawKeyed(sp.Frame, int(sp.X), int(sp.Y), clipX, clipY, clipW, clipH)
		}
	case drawlist.BlitScaled:
		clipX, clipY, clipW, clipH := r.spriteClip(sp.HasClip, sp.Clip)
		r.drawScaled(sp.Frame,
			int(sp.Src.X), int(sp.Src.Y), int(sp.Src.W), int(sp.Src.H),
			int(sp.Dst.X), int(sp.Dst.Y), int(sp.Dst.W), int(sp.Dst.H),
			clipX, clipY, clipW, clipH)
	case drawlist.BlitFeatureNormal:
		// blitGAFFrame's non-shadow path: a full-framebuffer keyed copy at the
		// already-offset top-left [03 §4.4][fmt gaf].
		r.drawKeyed(sp.Frame, int(sp.X), int(sp.Y), 0, 0, r.w, r.h)
	case drawlist.BlitLit:
		// uiBlitLitRaw: every opaque source texel written as LightLookup(row, src),
		// the source folded through one LHT row. Not destination-reading, so it is
		// a keyed blit with a source remap; the row is clamped to LHT's 0..31 range
		// exactly as LightLookup does [03 §4.3.1].
		r.drawLit(sp.Frame, int(sp.X), int(sp.Y), clampLHTRow(int(sp.LightRow)))
	case drawlist.BlitTinted:
		// tintedBlitAnchor: each opaque source texel resolves the destination to
		// ALP[src*256 + dst]; anchored and destination-reading [03 R-COMP-01 §2].
		r.drawTint(sp.Frame, int(sp.X), int(sp.Y))
	case drawlist.BlitFeatureShadow:
		// blitGAFFrame isShadow: darken the destination through PALETTE.SHD where the
		// shadow frame is opaque; destination-reading. Trans selects the SHD row
		// [03 §4.4].
		r.drawShadow(sp.Frame, int(sp.X), int(sp.Y), sp.Trans)
	}
}

// Surface replays one indexed byte surface blit (the minimap/radar image),
// matching uiBlitIndexedRaw's nearest-neighbour integer scaling and framebuffer
// clip (docs/DESIGN_GPU_RENDERER.md §2.3). The surface changes every frame, so its
// texture is uploaded per call into a reused scratch image rather than cached.
func (r *Renderer) Surface(sf drawlist.Surface) {
	if r == nil || r.offscreen == nil || r.indexScaled == nil {
		return
	}
	src := sf.Pixels
	srcW, srcH := int(sf.SrcW), int(sf.SrcH)
	w, h := int(sf.Dst.W), int(sf.Dst.H)
	if len(src) == 0 || srcW <= 0 || srcH <= 0 || w <= 0 || h <= 0 {
		return
	}
	img := r.uploadSurface(src, srcW, srcH)
	if img == nil {
		return
	}
	x, y := int(sf.Dst.X), int(sf.Dst.Y)
	// uiBlitIndexedRaw clips only to the framebuffer; the source mapping is
	// sx = dx*srcW/w, sy = dy*srcH/h (size over size, not span over span).
	qx0, qy0 := maxInt(x, 0), maxInt(y, 0)
	qx1, qy1 := minInt(x+w, r.w), minInt(y+h, r.h)
	if qx0 >= qx1 || qy0 >= qy1 {
		return
	}
	r.drawIndexScaledQuad(img,
		float32(qx0), float32(qy0), float32(qx1), float32(qy1),
		x, y, srcW, srcH, w, h, 0, 0, srcW, srcH)
}

// Cursor replays the software cursor blit: a full-framebuffer plain keyed copy of
// the cursor frame at the resolved blit origin, exactly as the classic sink calls
// uiBlitClippedRaw(cu.Frame, HotX, HotY, 0, 0, width, height) [07 §8].
func (r *Renderer) Cursor(cu drawlist.Cursor) {
	if r == nil || r.offscreen == nil {
		return
	}
	r.drawKeyed(cu.Frame, int(cu.HotX), int(cu.HotY), 0, 0, r.w, r.h)
}

// spriteClip resolves a recorded Sprite clip into the (x, y, w, h) the byte
// writers took: the recorded rectangle when HasClip is set, otherwise the full
// framebuffer, exactly as the classic sink's clip helper does.
func (r *Renderer) spriteClip(has bool, rect drawlist.Rect) (x, y, w, h int) {
	if has {
		return int(rect.X), int(rect.Y), int(rect.W), int(rect.H)
	}
	return 0, 0, r.w, r.h
}

// drawKeyed reproduces uiBlitClippedRaw for a 1:1 keyed GAF copy: it computes the
// same clipped destination rectangle and intra-frame source offset the byte writer
// covers, then draws an axis-aligned quad sampling the frame's index texture under
// the source-over blend so transparent texels leave the destination untouched
// (C-G4). x and y are the destination top-left the byte writer received.
func (r *Renderer) drawKeyed(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil || r.gafKeyed == nil {
		return
	}
	img := r.gafImageFor(f)
	if img == nil {
		return
	}
	fw, fh := int(f.Width), int(f.Height)
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.w), minInt(clipY+clipH, r.h)
	col0, col1 := maxInt(0, minX-x), minInt(fw, maxX-x)
	row0, row1 := maxInt(0, minY-y), minInt(fh, maxY-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	r.drawTexQuad(img, r.gafKeyed, ebiten.BlendSourceOver,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(col0), float32(row0), float32(col1), float32(row1))
}

// drawPCX reproduces uiBlitPCXClippedRaw: an opaque 1:1 copy of the PCX pixels
// over the clip-intersected destination rectangle, sampling the PCX index texture
// under BlendCopy (no key applies) [fmt pcx].
func (r *Renderer) drawPCX(p *formats.PCX, x, y, clipX, clipY, clipW, clipH int) {
	if p == nil || r.atlas == nil {
		return
	}
	img := r.pcxImageFor(p)
	if img == nil {
		return
	}
	pw, ph := int(p.Width), int(p.Height)
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.w), minInt(clipY+clipH, r.h)
	col0, col1 := maxInt(0, minX-x), minInt(pw, maxX-x)
	row0, row1 := maxInt(0, minY-y), minInt(ph, maxY-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	r.drawTexQuad(img, r.atlas, ebiten.BlendCopy,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(col0), float32(row0), float32(col1), float32(row1))
}

// drawScaled reproduces uiBlitFrameSourceRectScaledClippedRaw: it draws the
// clip-intersected destination rectangle and lets the indexScaled shader map each
// destination pixel to its source texel with the byte writer's integer
// span-over-span division, skipping transparent or out-of-frame texels under the
// source-over blend (C-G4)[07 R-HUD-03 §11].
func (r *Renderer) drawScaled(f *formats.GAFFrame, srcX, srcY, srcW, srcH, x, y, w, h, clipX, clipY, clipW, clipH int) {
	if f == nil || r.indexScaled == nil || w <= 0 || h <= 0 || srcW <= 0 || srcH <= 0 || f.Width == 0 || f.Height == 0 {
		return
	}
	img := r.gafImageFor(f)
	if img == nil {
		return
	}
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.w), minInt(clipY+clipH, r.h)
	qx0, qy0 := maxInt(x, minX), maxInt(y, minY)
	qx1, qy1 := minInt(x+w, maxX), minInt(y+h, maxY)
	if qx0 >= qx1 || qy0 >= qy1 {
		return
	}
	// Span-over-span mapping: sx = dx*(srcW-1)/(w-1); the source origin is the
	// sub-rect corner, the frame size bounds the At coordinate.
	r.drawIndexScaledQuad(img,
		float32(qx0), float32(qy0), float32(qx1), float32(qy1),
		x, y, srcW-1, srcH-1, w-1, h-1, srcX, srcY, int(f.Width), int(f.Height))
}

// drawIndexScaledQuad draws one quad through the indexScaled shader with the
// integer-mapping uniforms. srcNum/dstDen are the numerator/denominator of the
// per-axis source mapping (span-over-span for the scaled GAF blit, size-over-size
// for the surface blit); srcOrigin is added after the mapping and frameW/frameH
// bound the source coordinate (C-G4).
func (r *Renderer) drawIndexScaledQuad(img *ebiten.Image, dx0, dy0, dx1, dy1 float32,
	dstOX, dstOY, srcNumX, srcNumY, dstDenX, dstDenY, srcOX, srcOY, frameW, frameH int) {
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(dx0, dy0, dx1, dy1, 0, 0, 0, 0)
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.indexScaled, &ebiten.DrawTrianglesShaderOptions{
		Blend: ebiten.BlendSourceOver,
		Uniforms: map[string]any{
			"DstOrigin": []float32{float32(dstOX), float32(dstOY)},
			"SrcNum":    []float32{float32(srcNumX), float32(srcNumY)},
			"DstDen":    []float32{float32(dstDenX), float32(dstDenY)},
			"SrcOrigin": []float32{float32(srcOX), float32(srcOY)},
			"FrameSize": []float32{float32(frameW), float32(frameH)},
		},
		Images: [4]*ebiten.Image{img, nil, nil, nil},
	})
}

// drawTexQuad draws one axis-aligned textured quad into the offscreen with the
// given shader and blend, sampling the source image's [sx0,sx1)×[sy0,sy1) region
// across the destination [dx0,dx1)×[dy0,dy1) rectangle (C-G4).
func (r *Renderer) drawTexQuad(img *ebiten.Image, shader *ebiten.Shader, blend ebiten.Blend,
	dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1 float32) {
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(dx0, dy0, dx1, dy1, sx0, sy0, sx1, sy1)
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, shader, &ebiten.DrawTrianglesShaderOptions{
		Blend:  blend,
		Images: [4]*ebiten.Image{img, nil, nil, nil},
	})
}

// uploadSurface uploads the indexed surface bytes into the reused scratch texture,
// growing it when the source dimensions change. Index rides red; a byte present in
// src is opaque (green 255), one past the source length is transparent (green 0),
// matching uiBlitIndexedRaw's `idx < len(src)` guard, which skips the write for a
// short source and leaves the destination untouched (C-G4).
func (r *Renderer) uploadSurface(src []byte, srcW, srcH int) *ebiten.Image {
	if r.surfaceImg == nil || r.surfaceW != srcW || r.surfaceH != srcH {
		r.surfaceImg = ebiten.NewImage(srcW, srcH)
		r.surfaceW, r.surfaceH = srcW, srcH
	}
	n := len(src)
	buf := make([]byte, srcW*srcH*4)
	for i := 0; i < srcW*srcH; i++ {
		if i < n {
			buf[i*4+0] = src[i]
			buf[i*4+1] = 255
		}
		buf[i*4+3] = 255
	}
	r.surfaceImg.WritePixels(buf)
	return r.surfaceImg
}
