package client

// The two span writers the model draw uses: the flat fill and the textured
// blit, each with the nanoframe reveal gate [03 R-REN-03A §5].

import (
	"github.com/nanolathe/nanolathe/formats"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
)

// nanoframeVerdict resolves one composed nanoframe pixel. It returns the
// palette index to write and whether the pixel is drawn at all [03 §5.2].
func nanoframeVerdict(rev presentationrender.NanoframeReveal, key uint8, composed uint8) (uint8, bool) {
	switch v := rev.Verdict(key); v {
	case presentationrender.NanoframeErase:
		// Erase removes the composed scratch pixel. The target caller clears
		// coverage; it never writes palette zero into the world destination
		// [03 §5.2].
		return 0, false
	case presentationrender.NanoframeKeep:
		return composed, true
	default:
		return uint8(v), true
	}
}

// fillPolyTarget is retail's flat polygon span writer over the two-chain walk:
// one colour byte per pixel, the key interpolated across the span and tested
// per pixel [R-RAST-01 §1] steps 5-6, [R-REN-03A §5]. A non-nil reveal composes
// the nanoframe verdict on top of the same admission [03 §5.2].
//
// There are two flat writers, not one: [R-REN-03A §5]'s table pairs the
// unshaded flat polygon, which writes the colour byte raw, with the shaded flat
// polygon, which writes `SHD[row*256 + colour]`. Which one runs is the same
// per-primitive shading selection the textured pair uses, so this writer takes
// the SHD row off the span exactly as the textured one does and shades the
// authored colour with it. Emitting the raw byte for every flat face — what
// this did until now — left every untextured face at full palette intensity,
// so a model's flat panels ignored the light while its textured panels obeyed
// it, and the two halves of one hull could not agree on a tone.
func (c *Client) fillPolyTarget(target *modelTarget, p *screenPoly, color uint8, rev *presentationrender.NanoframeReveal, ids ...uint64) {
	if target == nil || p == nil {
		return
	}
	if target.trace == nil && rev == nil && !p.useSHD {
		target.fillPlainModelPoly(p, color)
		return
	}
	last := spanRow
	if p.useSHD {
		last = spanAttrs
	}
	id := rendererID(ids)
	face := p.traceFace()
	target.polyScanLanes(p, spanKey, last, func(row, xl, xr int32, a, da [spanAttrs]int64) {
		base := row * int32(target.width)
		acc := a
		for px := xl; px < xr; px++ {
			key := spanByte(acc[spanKey])
			idx := int(base + px)
			// The shaded flat writer of [R-REN-03A §5]: the authored colour
			// resolved through the interpolated SHD row, the same row lane the
			// textured writer rides [R-RAST-01 §5].
			shade := presentationrender.NoShadeRow
			b := color
			if p.useSHD {
				shade = spanShadeRow(acc[spanRow])
				if c != nil && c.pal != nil {
					b = c.pal.Shade[shade][color]
				}
			}
			event := -1
			if target.trace != nil {
				event = target.traceCandidate(rendererCandidate(face, target.tick, id, px, row, key, target.storedKey(idx), b, shade, rendererTexture(face, 0, 0, 0, RendererValueUnavailable, false)))
			}
			switch {
			case !target.admit(idx, key):
				target.traceRejected(event, RendererReasonHeightRejected, "height")
			case rev == nil:
				target.traceAdmitted(idx, event)
				target.write(idx, b, true)
			default:
				target.traceAdmitted(idx, event)
				if v, ok := nanoframeVerdict(*rev, key, b); ok {
					if event >= 0 {
						target.trace.events[event].CandidateIndex = v
					}
					target.write(idx, v, true)
				} else {
					target.traceRejected(event, RendererReasonNanoframeErase, "nanoframe-erase")
					target.write(idx, 0, false)
				}
			}
			for k := spanKey; k < last; k++ {
				acc[k] += da[k]
			}
		}
	})
}

// blitTexturedPolyTarget is retail's textured quad mapper over the same walk.
// The texel is sampled at the interpolated integer texel coordinates —
// `pixels[(v >> 16) * w + (u >> 16)]` — with no perspective divide and no
// stored UVs; the default corners keep u in [0, w-1] and v in [0, h-1] because
// the interpolation never reaches the right corner's value [R-RAST-01 §1]
// steps 5-6. The SHD row rides the same interpolation as the key
// [R-RAST-01 §5].
//
// No texel is tested for transparency. [R-REN-03A §5] establishes
// (bounded-negative) that none of the four span writers compares the sampled
// texel against a transparent or colour-key index: inside the model raster path
// a texture is fully opaque, and transparency is expressed only by the
// composition image's own background index, which is what the final blit keys
// against. This corrects the incidental "transparent holes skip SHD" remark in
// [03 §5.2]. Skipping key-coloured texels — what this did until now — punched
// holes through faces whose authored texture happens to use the key index, and
// let the terrain show through a solid hull.
//
// The bounds test that remains is not a transparency test: it guards the
// sample against an index outside the texture's own pixels. Retail's default
// corner UVs keep the interpolation inside `[0, w-1] × [0, h-1]` by
// construction, so the guard is unreachable on well-formed art
// [R-RAST-01 §1].
func (c *Client) blitTexturedPolyTarget(target *modelTarget, p *screenPoly, gafFrame *formats.GAFFrame, rev *presentationrender.NanoframeReveal, ids ...uint64) {
	if target == nil || p == nil || gafFrame == nil {
		return
	}
	if target.trace == nil && rev == nil {
		c.blitOrdinaryTexturedPoly(target, p, gafFrame)
		return
	}
	id := rendererID(ids)
	face := p.traceFace()
	w, h := int(gafFrame.Width), int(gafFrame.Height)
	target.polyScan(p, func(row, xl, xr int32, a, da [spanAttrs]int64) {
		base := row * int32(target.width)
		acc := a
		for px := xl; px < xr; px++ {
			tx, ty := int(acc[spanU]>>16), int(acc[spanV]>>16)
			key := spanByte(acc[spanKey])
			idx := int(base + px)
			var b uint8
			sourceState := RendererValueUnavailable
			inTexture := tx >= 0 && ty >= 0 && tx < w && ty < h && ty*w+tx < len(gafFrame.Pixels)
			if inTexture {
				sourceState = RendererValueAvailable
				b = gafFrame.Pixels[ty*w+tx]
			}
			shade := presentationrender.NoShadeRow
			if p.useSHD {
				shade = spanShadeRow(acc[spanRow])
			}
			event := -1
			if target.trace != nil {
				_, keyed := gafFrame.At(tx, ty)
				// The trace still records that the sampled texel carried the
				// GAF key, because a parity capture wants to see it; the writer
				// no longer acts on it [R-REN-03A §5].
				event = target.traceCandidate(rendererCandidate(face, target.tick, id, px, row, key, target.storedKey(idx), b, shade, rendererTexture(face, tx, ty, b, sourceState, !keyed)))
			}
			switch {
			case !inTexture:
				target.traceRejected(event, RendererReasonOutsideTexture, "outside-texture")
			case !target.admit(idx, key):
				target.traceRejected(event, RendererReasonHeightRejected, "height")
			default:
				target.traceAdmitted(idx, event)
				if c != nil && c.pal != nil && p.useSHD {
					b = c.pal.Shade[shade][b]
				}
				if rev != nil {
					var ok bool
					if b, ok = nanoframeVerdict(*rev, key, b); !ok {
						target.traceRejected(event, RendererReasonNanoframeErase, "nanoframe-erase")
						target.write(idx, 0, false)
						break
					}
				}
				if event >= 0 {
					target.trace.events[event].CandidateIndex = b
				}
				target.write(idx, b, true)
			}
			for k := range acc {
				acc[k] += da[k]
			}
		}
	})
}

// Ordinary model textures retain the same UV/key arithmetic and opaque source
// sampling, without diagnostic or construction-reveal work [03 R-REN-03A §5].
func (c *Client) blitOrdinaryTexturedPoly(t *modelTarget, p *screenPoly, frame *formats.GAFFrame) {
	w, h := int(frame.Width), int(frame.Height)
	shaded := c != nil && c.pal != nil && p.useSHD
	last := spanRow
	if shaded {
		last = spanAttrs
	}
	t.polyScanLanes(p, spanU, last, func(row, xl, xr int32, a, da [spanAttrs]int64) {
		base := int(row) * t.width
		for px := int(xl); px < int(xr); px++ {
			tx, ty := int(a[spanU]>>16), int(a[spanV]>>16)
			if tx >= 0 && ty >= 0 && tx < w && ty < h && ty*w+tx < len(frame.Pixels) {
				idx := base + px
				key := spanByte(a[spanKey])
				if t.height == nil || t.height[idx] <= key {
					if t.height != nil {
						t.height[idx] = key
					}
					b := frame.Pixels[ty*w+tx]
					if shaded {
						b = c.pal.Shade[spanShadeRow(a[spanRow])][b]
					}
					t.color[idx], t.covered[idx] = b, true
				}
			}
			for k := spanU; k < last; k++ {
				a[k] += da[k]
			}
		}
	})
}

// Unshaded bodies and shadows need only the height lane, and keyless painter
// targets need no lanes. Equal keys still admit, and covered/background handling
// matches the general writer [03 R-REN-03A §2][03 R-RAST-01 §1].
func (t *modelTarget) fillPlainModelPoly(p *screenPoly, color uint8) {
	last := spanRow
	if t.height == nil {
		last = spanKey
	}
	t.polyScanLanes(p, spanKey, last, func(row, xl, xr int32, a, da [spanAttrs]int64) {
		base := int(row) * t.width
		lo, hi := base+int(xl), base+int(xr)
		pixels, covered := t.color[lo:hi], t.covered[lo:hi]
		if t.height == nil {
			for i := range pixels {
				pixels[i] = color
				covered[i] = true
			}
			return
		}
		keys := t.height[lo:hi]
		acc, step := a[spanKey], da[spanKey]
		for i := range pixels {
			key := spanByte(acc)
			if keys[i] <= key {
				keys[i] = key
				pixels[i] = color
				covered[i] = true
			}
			acc += step
		}
	})
}
