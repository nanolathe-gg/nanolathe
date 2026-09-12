package formats

// Doubled returns the nearest-doubled variant of a decoded frame: the same
// picture at twice the resolution, for the detail view of
// DESIGN_GPU_RENDERER §14.3. It is a host presentation product, not a file
// format contract — nothing in a GAF file describes it [fmt gaf].
//
// Every geometric field doubles and nothing else changes. Width, Height and the
// authored anchor offsets XOffset/YOffset are multiplied by two, so a frame
// placed by the anchor blit lands on exactly the pixels the 1x frame covered,
// each covering a 2x2 block. ColorKey is carried across unchanged because the
// key is a palette index, not a size. Compressed retains the source dispatch:
// gray/dither reject RLE and nonzero glyph modes distinguish raw from RLE even
// though both pixel planes are already decoded [03 R-COMP-01 §2].
//
// Pixels and Transparent are doubled together, and PlainPixels/PlainTransparent
// keep whatever aliasing the decoder gave them: the decoder points Pixels at
// PlainPixels for every frame, and a consumer that reads the plain raster of a
// composite must find the doubled plain raster there rather than a nil slice.
//
// A composite's children are doubled leaf by leaf and the composite fields —
// SubframeCount and AlternateBlitter — are preserved on the parent and on each
// child, because the classic general-GAF walk decomposes a composite into its
// ordered leaves at emit time and selects each leaf's blitter from its own
// authored high byte. Doubling the parent's own plain raster as well keeps the
// two views of a composite consistent: the leaf walk and the precomposed
// raster describe the same doubled picture, because doubling commutes with the
// plain composite (every child offset doubles with the parent's).
//
// A nil frame doubles to nil. The source is never mutated; frames are immutable
// after load, which is what lets the client cache one variant per source frame
// for its life.
func (f *GAFFrame) Doubled() *GAFFrame {
	return f.Resampled(2, 1)
}

// Resampled returns the frame magnified by num/den with nearest sampling, the
// generalisation of Doubled to the 1.5x view (DESIGN_GPU_RENDERER §14.3): a
// 1x frame at 3/2, or a remastered 2x frame at 3/4. Width and Height become
// ceil(v·num/den), the number of screen pixels the frame's world pixels
// cover; the authored anchor offsets round half away from zero, the rule
// every scaled extent follows (camera.ViewScale.Px); and output pixel j reads
// source pixel floor(j·den/num), so a 1x source pixel covers alternately one
// and two output pixels at 3/2 and a 2x source loses every fourth column at
// 3/4. At 2/1 the result is byte-for-byte Doubled's. Everything else Doubled
// says about keys, plain rasters and composites holds here.
func (f *GAFFrame) Resampled(num, den int) *GAFFrame {
	if f == nil {
		return nil
	}
	if num <= 0 || den <= 0 {
		return f
	}
	out := &GAFFrame{
		Width:            resampleExtent(f.Width, num, den),
		Height:           resampleExtent(f.Height, num, den),
		XOffset:          resampleOffset(f.XOffset, num, den),
		YOffset:          resampleOffset(f.YOffset, num, den),
		ColorKey:         f.ColorKey,
		Compressed:       f.Compressed,
		Unknown2:         f.Unknown2,
		Unknown3:         f.Unknown3,
		SubframeCount:    f.SubframeCount,
		AlternateBlitter: f.AlternateBlitter,
	}
	w, h := int(f.Width), int(f.Height)
	dw, dh := int(out.Width), int(out.Height)
	out.Pixels, out.Transparent = resampleRaster(f.Pixels, f.Transparent, w, h, dw, dh, num, den)
	if sameByteSlice(f.PlainPixels, f.Pixels) {
		out.PlainPixels, out.PlainTransparent = out.Pixels, out.Transparent
	} else {
		out.PlainPixels, out.PlainTransparent = resampleRaster(f.PlainPixels, f.PlainTransparent, w, h, dw, dh, num, den)
	}
	if len(f.Subframes) != 0 {
		out.Subframes = make([]*GAFFrame, len(f.Subframes))
		for i, child := range f.Subframes {
			out.Subframes[i] = child.Resampled(num, den)
		}
	}
	return out
}

// resampleExtent is ceil(v·num/den) for a non-negative size.
func resampleExtent(v uint16, num, den int) uint16 {
	return uint16((int(v)*num + den - 1) / den)
}

// resampleOffset rounds v·num/den half away from zero, so a symmetric anchor
// stays symmetric; at a whole factor it is the exact multiply.
func resampleOffset(v int16, num, den int) int16 {
	p := int(v) * num * 2
	if p >= 0 {
		return int16((p + den) / (2 * den))
	}
	return int16((p - den) / (2 * den))
}

// resampleRaster maps every output pixel to source floor(j·den/num) on both
// axes. A short Pixels or Transparent slice reads as the decoder left it:
// missing pixels are zero and, absent a Transparent entry, opaque.
func resampleRaster(pixels []byte, transparent []bool, w, h, dw, dh, num, den int) ([]byte, []bool) {
	if w <= 0 || h <= 0 || dw <= 0 || dh <= 0 {
		return nil, nil
	}
	outPixels := make([]byte, dw*dh)
	outTransparent := make([]bool, dw*dh)
	for y := 0; y < dh; y++ {
		sy := y * den / num
		if sy >= h {
			sy = h - 1
		}
		row := sy * w
		outRow := y * dw
		for x := 0; x < dw; x++ {
			sx := x * den / num
			if sx >= w {
				sx = w - 1
			}
			index := row + sx
			var pixel byte
			clear := true
			if index < len(pixels) {
				pixel = pixels[index]
			}
			if index < len(transparent) {
				clear = transparent[index]
			} else if index < len(pixels) {
				clear = false
			}
			outPixels[outRow+x] = pixel
			outTransparent[outRow+x] = clear
		}
	}
	return outPixels, outTransparent
}

func sameByteSlice(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}
