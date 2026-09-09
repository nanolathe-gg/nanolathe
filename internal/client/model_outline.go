package client

// The single-pixel model outline and the renderer trace it feeds
// [03 R-RAST-01 §5].

import (
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
)

// attachModelTrace wires the parity trace to the image actually rasterized
// into, which is the doubled scratch while anti-aliasing.
func (c *Client) attachModelTrace(t *modelTarget, id uint64) {
	if c == nil || c.rendererTraceSink == nil || t == nil {
		return
	}
	t.trace = newRendererTrace(t.width * t.heightPx)
	t.winner = t.trace.winner
	t.trace.unit = id
	t.trace.tick = c.frameTick
	t.trace.width = t.width
	t.trace.height = t.heightPx
	t.tick = c.frameTick
}

// outlineModelInto overdraws the nanoframe wireframe into a composition image
// [R-COMP-01 §3]. It is not a polyline and not a framebuffer pass:
//
//   - pieces are walked last to first, primitives from index 1 when the piece
//     declares a selection primitive and from 0 otherwise, the same exclusion
//     the raster applies;
//   - each primitive's corners, closed with a copy of the first, go through the
//     [R-RAST-01 §1] edge walk with the composition image's own projection and
//     origin pair, and on every row where `xr − xl > 0` strictly exactly two
//     pixels are written, at `xl` and at `xr`: the polygon's per-scanline
//     extremes;
//   - with a key plane each endpoint is admitted only when the stored key is
//     at or below the edge's own interpolated key, and the key is stored — the
//     span writers' admission — so the wireframe shows through the body only
//     where the body was erased, and a carrier's geometry occludes it in the
//     staging composite like any other pixel. Without a key plane both writes
//     are unconditional.
//
// The image is the 1x composition image, which is what retail's reveal reads
// after the anti-alias resolve. raster is the image the parity trace is
// attached to, so outline writes are recorded in its coordinate space.
//
// This replaces a framebuffer overdraw of every primitive as a closed
// Bresenham polyline, drawn after the blit and never depth-tested, which
// [R-P0-19-N] once described and [R-COMP-01 §3] corrected.
func (c *Client) outlineModelInto(target, raster *modelTarget, draw *presentationrender.UnitDraw, color uint8) {
	if c == nil || target == nil || draw == nil || draw.Model == nil || target.width <= 0 || target.heightPx <= 0 {
		return
	}
	var trace *rendererTrace
	traceScale := int32(1)
	if raster != nil && raster.trace != nil {
		trace, traceScale = raster.trace, raster.scale
	}
	var xs, ys, ks []int32
	for pi := len(draw.Pieces) - 1; pi >= 0; pi-- {
		piece := draw.Pieces[pi]
		if pi >= len(draw.Model.Pieces) {
			continue
		}
		for pri, pr := range piece.Primitives {
			if draw.Model.Pieces[pi].Selection && pri == 0 {
				continue
			}
			n := len(pr.VertexIndices)
			if n < 2 {
				// One corner closed with itself has no row span and draws
				// nothing; an empty primitive has no corners at all.
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
				continue // malformed primitive suppresses the whole face [fmt 3do]
			}
			xs, ys, ks = xs[:0], ys[:0], ks[:0]
			for k := 0; k <= n; k++ {
				v := piece.WorldVertices[pr.VertexIndices[k%n]]
				lx, ly, _ := modelLocalVertex(v, draw.WorldPos)
				lx, ly = c.scaleModelLocal(lx, ly)
				xs = append(xs, lx+target.originX)
				ys = append(ys, ly+target.originY)
				ks = append(ks, modelHeightKey(v[1].Sub(draw.WorldPos[1]), draw.DiggerClip))
			}
			target.outlinePolygon(xs, ys, ks, color, trace, traceScale, pi, pri)
		}
	}
}

// outlinePolygon is the outline pass's edge walk over one closed corner list
// in image space [R-COMP-01 §3]: extrema, the left chain toward the previous
// index and the right chain toward the next, `x = x0 × 65536 + 0xFFFF` and
// `key = k0 × 65536` stepped by `(Δ × 65536) / (y1 − y0)` truncating, over rows
// `[minY, maxY)`, then two pixels per row where `xr − xl > 0`.
//
// Retail's walk has no clip: the composition box is measured from the same
// vertices with a two-pixel margin, so every corner is inside it. The image
// bounds are checked here per pixel only so a corner the box does not hold —
// a primitive the raster dispatch dropped — cannot write outside the planes.
func (t *modelTarget) outlinePolygon(xs, ys, ks []int32, color uint8, trace *rendererTrace, traceScale int32, piece, primitive int) {
	n := len(xs)
	if n < 2 {
		return
	}
	minY, maxY, topIdx, botIdx := ys[0], ys[0], 0, 0
	for i := 1; i < n; i++ {
		if ys[i] < minY {
			minY, topIdx = ys[i], i
		}
		if ys[i] > maxY {
			maxY, botIdx = ys[i], i
		}
	}
	if maxY == minY {
		return
	}
	rows := int(maxY - minY)
	if cap(t.scanLeft) < rows || cap(t.scanRight) < rows {
		t.scanLeft = make([]spanEdge, rows)
		t.scanRight = make([]spanEdge, rows)
	}
	leftTab, rightTab := t.scanLeft[:rows], t.scanRight[:rows]
	walk := func(step int, tab []spanEdge) {
		cur := topIdx
		for guard := 0; guard <= n; guard++ {
			next := cur + step
			if next < 0 {
				next = n - 1
			} else if next >= n {
				next = 0
			}
			if ys[next] > ys[cur] {
				dy := int64(ys[next] - ys[cur])
				x := int64(xs[cur])<<16 + spanEdgeBias
				xStep := (int64(xs[next]-xs[cur]) << 16) / dy
				k := int64(ks[cur]) << 16
				kStep := (int64(ks[next]-ks[cur]) << 16) / dy
				for row := ys[cur]; row < ys[next]; row++ {
					i := int(row - minY)
					tab[i].x, tab[i].a[spanKey] = int32(x>>16), k
					x += xStep
					k += kStep
				}
			}
			if next == botIdx {
				return
			}
			cur = next
		}
	}
	walk(-1, leftTab)
	walk(+1, rightTab)
	for r := minY; r < maxY; r++ {
		i := int(r - minY)
		xl, xr := leftTab[i].x, rightTab[i].x
		if xr-xl <= 0 {
			continue
		}
		t.outlinePixel(xl, r, uint8(leftTab[i].a[spanKey]>>16), color, trace, traceScale, piece, primitive)
		t.outlinePixel(xr, r, uint8(rightTab[i].a[spanKey]>>16), color, trace, traceScale, piece, primitive)
	}
}

// outlinePixel writes one outline endpoint under the span writers' admission:
// unconditionally without a key plane, else only when `storedKey <= key`, and
// then the key is stored too [R-COMP-01 §3][R-REN-03A §2].
func (t *modelTarget) outlinePixel(x, y int32, key, color uint8, trace *rendererTrace, traceScale int32, piece, primitive int) {
	if x < 0 || y < 0 || x >= int32(t.width) || y >= int32(t.heightPx) {
		return
	}
	i := int(y)*t.width + int(x)
	if t.height != nil {
		if t.height[i] > key {
			return
		}
		t.height[i] = key
	}
	t.color[i], t.covered[i] = color, color != t.transparent
	if trace != nil {
		trace.composite(x*traceScale, y*traceScale, color, piece, primitive)
	}
}
