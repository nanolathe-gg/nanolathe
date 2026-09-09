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
// key is a palette index, not a size, and the result is a PLAIN frame:
// Compressed is zero because the pixels are already decoded, and the caller
// reads them the same way for a raw or an RLE source.
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
	if f == nil {
		return nil
	}
	out := &GAFFrame{
		Width:            f.Width * 2,
		Height:           f.Height * 2,
		XOffset:          f.XOffset * 2,
		YOffset:          f.YOffset * 2,
		ColorKey:         f.ColorKey,
		Compressed:       0,
		Unknown2:         f.Unknown2,
		Unknown3:         f.Unknown3,
		SubframeCount:    f.SubframeCount,
		AlternateBlitter: f.AlternateBlitter,
	}
	out.Pixels, out.Transparent = doubleRaster(f.Pixels, f.Transparent, int(f.Width), int(f.Height))
	if sameByteSlice(f.PlainPixels, f.Pixels) {
		// The decoder aliases the two views for every frame it materializes;
		// preserve that so a consumer of either reads the same bytes.
		out.PlainPixels, out.PlainTransparent = out.Pixels, out.Transparent
	} else {
		out.PlainPixels, out.PlainTransparent = doubleRaster(f.PlainPixels, f.PlainTransparent, int(f.Width), int(f.Height))
	}
	if len(f.Subframes) != 0 {
		out.Subframes = make([]*GAFFrame, len(f.Subframes))
		for i, child := range f.Subframes {
			out.Subframes[i] = child.Doubled()
		}
	}
	return out
}

// doubleRaster expands one w*h index raster and its parallel transparency mask
// so each source pixel covers a 2x2 destination block. A source shorter than
// its declared size leaves the missing tail transparent, which is how
// GAFFrame.At already treats it.
func doubleRaster(pixels []byte, transparent []bool, w, h int) ([]byte, []bool) {
	if w <= 0 || h <= 0 {
		return nil, nil
	}
	dw := w * 2
	outPixels := make([]byte, dw*h*2)
	outTransparent := make([]bool, dw*h*2)
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			index := row + x
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
			base := (y*2)*dw + x*2
			for dy := 0; dy < 2; dy++ {
				offset := base + dy*dw
				outPixels[offset] = pixel
				outPixels[offset+1] = pixel
				outTransparent[offset] = clear
				outTransparent[offset+1] = clear
			}
		}
	}
	return outPixels, outTransparent
}

// sameByteSlice reports whether two slices share a backing array from the same
// start, which is how the decoder marks a frame whose plain raster is its only
// raster.
func sameByteSlice(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}
