package main

import "github.com/nanolathe-gg/nanolathe/formats"

// previewPixels caches the map chooser's final indexed picture. The usable
// terrain extents determine both the source crop and the destination fit;
// stored minimap padding is never sampled [07 R-FE-01 §5][fmt tnt].
func (d *retailMapData) previewPixels(w, h int) []byte {
	if w < 2 || h < 2 || d.tnt == nil {
		return nil
	}
	if d.preview != nil && d.previewW == w && d.previewH == h {
		return d.preview
	}
	d.previewW, d.previewH = w, h
	canvas := retailMapPreviewCanvas(d.tnt, w, h)
	// The surface gadget performs a second quad mapping, from the canvas's
	// (1,1) interior to its final corner. Its exclusive span leaves the last
	// gadget row and column to the backdrop [07 R-FE-01 §5].
	d.preview = make([]byte, (w-1)*(h-1))
	du, dv := int64((w-2)<<16)/int64(w-1), int64((h-2)<<16)/int64(h-1)
	for y := 0; y < h-1; y++ {
		sy := int((65536 + int64(y)*dv) >> 16)
		for x := 0; x < w-1; x++ {
			sx := int((65536 + int64(x)*du) >> 16)
			d.preview[y*(w-1)+x] = canvas[sy*w+sx]
		}
	}
	return d.preview
}

func retailMapPreviewCanvas(t *formats.TNT, w, h int) []byte {
	canvas := make([]byte, w*h) // Retail clears the canvas to palette index zero.
	ew, eh := int64(t.Width)*16-32, int64(t.Height)*16-128
	sw, sh := int64(t.MinimapWidth), int64(t.MinimapHeight)
	if ew <= 0 || eh <= 0 || sw <= 0 || sh <= 0 || sw*sh > int64(len(t.Minimap)) {
		return canvas
	}
	srcW, srcH, dstW, dstH := sw, sh, int64(w), int64(h)
	x, y := int64(0), int64(0)
	if ew < eh {
		srcW = ew * sw / eh
		dstW = ew * int64(w) / eh
		x = (int64(w) - dstW) / 2
	} else {
		srcH = eh * sh / ew
		dstH = eh * int64(h) / ew
		y = (int64(h) - dstH) / 2
	}
	if srcW <= 0 || srcH <= 0 || dstW <= 0 || dstH <= 0 {
		return canvas
	}
	// The quad mapper divides each corner span before accumulating its fixed
	// step. Its exclusive fill meets inclusive surface clip bounds, leaving
	// the final canvas row and column clear [03 R-RAST-01 §1].
	du, dv := ((srcW-1)<<16)/dstW, ((srcH-1)<<16)/dstH
	for py := y; py < min(y+dstH, int64(h)-1); py++ {
		sy := ((py - y) * dv) >> 16
		for px := x; px < min(x+dstW, int64(w)-1); px++ {
			sx := ((px - x) * du) >> 16
			canvas[py*int64(w)+px] = t.Minimap[sy*sw+sx]
		}
	}
	return canvas
}
