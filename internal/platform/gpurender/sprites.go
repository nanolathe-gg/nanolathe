package gpurender

import (
	"bytes"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The keyed (non-destination-reading) image-blit families for the modern
// executor: the keyed GAF sprite (anchored and plain), the scaled GAF blit, the
// 2D feature GAF copy, the opaque PCX background, the indexed surface and the
// software cursor (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). Each reproduces its
// classic byte writer's covered-pixel set and per-pixel value exactly: the scene
// atlas carries the frame's bytes in the red channel and its opacity in green,
// and a keyed blit's fragment returns a transparent "skip" for a texel the byte
// writer skips, so the destination is left untouched, while an opaque texel
// overwrites the index with no blend arithmetic on red [03 R-RAST-01 §6]
// [03 §4.4][07 §4].
//
// ALP-tinted blits, including translucent feature bodies and shadows, and the
// source-through-LHT BlitLit are implemented in deststage.go. The
// destination-reading ALP path runs over the phase snapshot, exactly as the
// classic sink's counterpart reads c.indexed.

// buildGAFFrameImage uploads one GAF frame as a standalone index texture: index
// in red, opacity flag in green (255 opaque, 0 transparent), alpha opaque so
// premultiplied sampling recovers both bytes (C-G4). Opacity is baked from the
// frame's own Transparent mask, the same signal every classic byte writer tests —
// the raw color key, the RLE skip runs and the composite coverage all reduce to
// it [fmt gaf]. Red is retained even for a transparent-marked texel: model faces
// use the raw resolved texture index for ownership, while ordinary keyed
// blitters use green to skip it. A texel past the decoded pixel length is
// transparent and remains index zero, matching the byte writers' short-array
// skip.
//
// The 2D families read the packed scene atlas instead (atlas.go); this per-frame
// texture serves the model material passes, which sample a texture frame from its
// own image.
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
		if i < np {
			buf[i*4+0] = f.Pixels[i]
		}
		opaque := i < np && (i >= nt || !f.Transparent[i])
		if opaque {
			buf[i*4+1] = 255
		}
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(fw, fh)
	img.WritePixels(buf)
	return img
}

// gafImageFor returns the cached standalone index texture for f, building it on
// first use and caching it by pointer identity (docs/DESIGN_GPU_RENDERER.md
// §2.3). A nil frame or an empty frame has no image. The nil result is cached
// too, so a degenerate frame is not rebuilt every call.
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

// Sprite replays one GAF-frame or PCX blit, routing each recorded blit to the GPU
// pass that matches its classic byte writer (docs/DESIGN_GPU_RENDERER.md §2.3).
// A non-nil PCX is an opaque background blit regardless of Kind; the keyed kinds
// (anchored/plain), the scaled kind and opaque feature kinds are copied here;
// destination-reading variants route to the helpers in deststage.go.
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
			clipX, clipY, clipW, clipH := r.spriteClip(sp.HasClip, sp.Clip)
			r.drawKeyed(sp.Frame, int(sp.X)-int(sp.Frame.XOffset), int(sp.Y)-int(sp.Frame.YOffset),
				clipX, clipY, clipW, clipH)
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
		// Static feature bodies select the same ALP-tinted/keyed primitive pair as
		// their shadow. Live event cursors arrive with Trans clear. The tinted
		// helper accepts an anchor, while the record carries top-left placement
		// [03 R-RAST-01 §6][fmt gaf].
		if sp.Trans {
			if sp.Frame == nil {
				return
			}
			r.drawTint(sp.Frame,
				int(sp.X)+int(sp.Frame.XOffset),
				int(sp.Y)+int(sp.Frame.YOffset),
				0, 0, r.w, r.h)
			return
		}
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
		clipX, clipY, clipW, clipH := r.spriteClip(sp.HasClip, sp.Clip)
		r.drawTint(sp.Frame, int(sp.X), int(sp.Y), clipX, clipY, clipW, clipH)
	case drawlist.BlitFeatureShadow:
		// Feature shadows select the ordinary keyed or ALP-tinted frame primitive.
		// The record carries top-left placement while drawTint accepts the frame
		// anchor, so add the authored offsets before that helper subtracts them
		// [03 §5.3.1][R-REN-03D §4].
		if sp.Trans {
			if sp.Frame == nil {
				return
			}
			r.drawTint(sp.Frame,
				int(sp.X)+int(sp.Frame.XOffset),
				int(sp.Y)+int(sp.Frame.YOffset),
				0, 0, r.w, r.h)
			return
		}
		r.drawKeyed(sp.Frame, int(sp.X), int(sp.Y), 0, 0, r.w, r.h)
	}
}

// Surface replays one indexed byte surface blit (the minimap/radar image),
// matching uiBlitIndexedRaw's nearest-neighbour integer scaling and framebuffer
// clip (docs/DESIGN_GPU_RENDERER.md §2.3). The surface changes every frame, so
// its bytes are re-uploaded into a fixed scene atlas region rather than cached by
// identity, and the blit then merges into the frame's opaque batch.
func (r *Renderer) Surface(sf drawlist.Surface) {
	if r == nil || r.offscreen == nil || r.scene2D == nil {
		return
	}
	src := sf.Pixels
	srcW, srcH := int(sf.SrcW), int(sf.SrcH)
	w, h := int(sf.Dst.W), int(sf.Dst.H)
	if len(src) == 0 || srcW <= 0 || srcH <= 0 || w <= 0 || h <= 0 {
		return
	}
	e := r.uploadSurface(sf)
	if !e.ok {
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
	r.drawIndexScaledQuad(e, qx0, qy0, qx1, qy1, x, y,
		srcW, w, srcH, h, 0, 0, srcW, srcH)
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
// covers, then compiles an axis-aligned quad sampling the frame's scene atlas
// region, whose fragment skips the transparent texels (C-G4). x and y are the
// destination top-left the byte writer received.
func (r *Renderer) drawKeyed(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil || r.scene2D == nil {
		return
	}
	e := r.sceneFrameFor(f)
	if !e.ok {
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
	if !r.sched.begin(schedOpaque, x+col0, y+row0, x+col1, y+row1, r.sceneImages(e)) {
		return
	}
	r.sched.quad(schedOpaque,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{}, [4]float32{0, 0, 0, sceneOpKeyed})
}

// drawPCX reproduces uiBlitPCXClippedRaw: an opaque 1:1 copy of the PCX pixels
// over the clip-intersected destination rectangle, sampling the PCX's scene atlas
// region (no key applies) [fmt pcx].
func (r *Renderer) drawPCX(p *formats.PCX, x, y, clipX, clipY, clipW, clipH int) {
	if p == nil || r.scene2D == nil {
		return
	}
	e := r.scenePCXFor(p)
	if !e.ok {
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
	if !r.sched.begin(schedOpaque, x+col0, y+row0, x+col1, y+row1, r.sceneImages(e)) {
		return
	}
	r.sched.quad(schedOpaque,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(int(e.x)+col0), float32(int(e.y)+row0), float32(int(e.x)+col1), float32(int(e.y)+row1),
		[4]float32{}, [4]float32{0, 0, 0, sceneOpCopy})
}

// drawScaled reproduces uiBlitFrameSourceRectScaledClippedRaw: it draws the
// clip-intersected destination rectangle with the byte writer's integer
// span-over-span source mapping [07 R-HUD-03 §11]. A destination pixel whose
// mapped source lies outside the frame is one the byte writer skips, so the
// destination rectangle is narrowed to the pixels that do map inside — the same
// covered set, with the mapping kept exact inside the shader (C-G4).
func (r *Renderer) drawScaled(f *formats.GAFFrame, srcX, srcY, srcW, srcH, x, y, w, h, clipX, clipY, clipW, clipH int) {
	if f == nil || r.scene2D == nil || w <= 0 || h <= 0 || srcW <= 0 || srcH <= 0 || f.Width == 0 || f.Height == 0 {
		return
	}
	e := r.sceneFrameFor(f)
	if !e.ok {
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
	r.drawIndexScaledQuad(e, qx0, qy0, qx1, qy1, x, y,
		srcW-1, w-1, srcH-1, h-1, srcX, srcY, int(f.Width), int(f.Height))
}

// drawIndexScaledQuad compiles one integer-mapped quad. numX/denX and numY/denY
// are the per-axis source mapping (span-over-span for the scaled GAF blit,
// size-over-size for the surface blit); srcOX/srcOY is added after the mapping and
// frameW/frameH bound the source coordinate, exactly as the byte writers do
// (C-G4). The destination rectangle is first narrowed to the offsets whose mapped
// source is inside those bounds, so no fragment can sample a neighbouring atlas
// entry.
func (r *Renderer) drawIndexScaledQuad(e sceneEntry, qx0, qy0, qx1, qy1, dstOX, dstOY,
	numX, denX, numY, denY, srcOX, srcOY, frameW, frameH int) {
	loX, hiX, ok := scaledValidRange(numX, denX, srcOX, frameW, qx0-dstOX, qx1-1-dstOX)
	if !ok {
		return
	}
	loY, hiY, ok := scaledValidRange(numY, denY, srcOY, frameH, qy0-dstOY, qy1-1-dstOY)
	if !ok {
		return
	}
	x0, x1 := dstOX+loX, dstOX+hiX+1
	y0, y1 := dstOY+loY, dstOY+hiY+1
	if x0 >= x1 || y0 >= y1 {
		return
	}
	if !r.sched.begin(schedOpaque, x0, y0, x1, y1, r.sceneImages(e)) {
		return
	}
	// SrcX/SrcY carry the destination-relative offset, so the fragment recovers
	// the byte writer's dx and dy by flooring the interpolated position.
	r.sched.quad(schedOpaque,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(x0-dstOX), float32(y0-dstOY), float32(x1-dstOX), float32(y1-dstOY),
		[4]float32{float32(numX), float32(denX), float32(numY), float32(denY)},
		[4]float32{float32(int(e.x) + srcOX), float32(int(e.y) + srcOY), 0, sceneOpScaled})
}

// scaledValidRange narrows a destination offset span [dLo, dHi] to the offsets
// whose mapped source coordinate origin + (d*num)/den lies inside [0, limit) —
// the byte writers' per-pixel in-frame test, resolved once per axis because the
// mapping is monotone in d. It returns false when no offset maps inside.
func scaledValidRange(num, den, origin, limit, dLo, dHi int) (lo, hi int, ok bool) {
	if limit <= 0 || dLo > dHi {
		return 0, 0, false
	}
	if den <= 0 || num == 0 {
		// The byte writer's `if span > 0` guard leaves the source coordinate at
		// the origin for every destination pixel.
		if origin < 0 || origin >= limit {
			return 0, 0, false
		}
		return dLo, dHi, true
	}
	lo = dLo
	if origin < 0 {
		// Smallest d with floor(d*num/den) >= -origin, i.e. d*num >= -origin*den.
		need := (-origin*den + num - 1) / num
		if need > lo {
			lo = need
		}
	}
	m := limit - 1 - origin
	if m < 0 {
		return 0, 0, false
	}
	// Largest d with floor(d*num/den) <= m, i.e. d*num <= (m+1)*den - 1.
	hi = ((m+1)*den - 1) / num
	if hi > dHi {
		hi = dHi
	}
	if lo > hi {
		return 0, 0, false
	}
	return lo, hi, true
}

// sceneImages is the image binding a scene atlas entry needs: its page in source
// slot 0. Every other slot stays open, so this command can share a run with the
// fills, terrain tiles and model commits around it.
func (r *Renderer) sceneImages(e sceneEntry) [4]*ebiten.Image {
	return [4]*ebiten.Image{r.scene.pageImage(e)}
}

// uploadSurface writes an indexed surface's bytes into its scene atlas region
// and returns that region, uploading only when a durable command's revision
// changes. A zero identity is deliberately dynamic, preserving UIBlitIndexed's
// ordinary borrowed-buffer behaviour [03 §3.6]. Packing the region into the
// scene atlas is what lets the minimap blit merge into the frame's opaque batch
// (docs/DESIGN_GPU_RENDERER.md §11.2).
func (r *Renderer) uploadSurface(sf drawlist.Surface) sceneEntry {
	srcW, srcH := int(sf.SrcW), int(sf.SrcH)
	if r == nil || srcW <= 0 || srcH <= 0 {
		return sceneEntry{}
	}
	entry := &r.surfaceDynamic
	upload := true
	if sf.Identity != 0 {
		r.surfaceClock++
		var found bool
		entry, found = r.surfaceEntry(sf.Identity)
		entry.used = r.surfaceClock
		upload = !found || !entry.entry.ok || entry.w != srcW || entry.h != srcH || sf.Revision == 0 || entry.revision != sf.Revision
		if !upload {
			return entry.entry
		}
		entry.identity = sf.Identity
		entry.revision = sf.Revision
	}
	if entry.entry.ok && (entry.w != srcW || entry.h != srcH) {
		// A resized surface needs a region of its own; atlas regions are never
		// freed, and a minimap only changes size when the window does.
		entry.entry = sceneEntry{}
	}
	if !entry.entry.ok {
		entry.entry = r.scene.allocate(srcW, srcH)
		if !entry.entry.ok {
			return sceneEntry{}
		}
		entry.w, entry.h = srcW, srcH
		// A fresh region holds nothing yet, so the unchanged-bytes test below
		// must not match what the previous region held.
		entry.sent = entry.sent[:0]
	}
	n := srcW * srcH
	if entry.entry.ok && bytes.Equal(entry.sent, sf.Pixels) {
		// Same bytes as the region already holds; the upload would rewrite it
		// with itself.
		return entry.entry
	}
	entry.sent = append(entry.sent[:0], sf.Pixels...)
	if cap(entry.pixels) < n*4 {
		entry.pixels = make([]byte, n*4)
	} else {
		entry.pixels = entry.pixels[:n*4]
	}
	for i := 0; i < n; i++ {
		j := i * 4
		if i < len(sf.Pixels) {
			entry.pixels[j] = sf.Pixels[i]
			entry.pixels[j+1] = 255
		} else {
			entry.pixels[j] = 0
			entry.pixels[j+1] = 0
		}
		entry.pixels[j+2] = 0
		entry.pixels[j+3] = 255
	}
	r.scene.upload(entry.entry, entry.pixels)
	r.surfaceWrites++
	return entry.entry
}

// surfaceEntry finds the cache slot for a durable identity, or the slot to
// evict. Neither the search nor the eviction can reach output [I1].
func (r *Renderer) surfaceEntry(identity uint64) (*surfaceUpload, bool) {
	var oldest *surfaceUpload
	for i := range r.surfaceCache {
		entry := &r.surfaceCache[i]
		if entry.identity == identity {
			return entry, true
		}
		if oldest == nil || entry.identity == 0 || entry.used < oldest.used {
			oldest = entry
		}
	}
	return oldest, false
}
