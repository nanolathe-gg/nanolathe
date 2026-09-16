package gpurender

import "github.com/hajimehoshi/ebiten/v2"

// The Enhanced layers that read the composite they rewrite — the ground-light
// pools (§31), the blast and plume refraction (§25, §27) and the ordered lens
// (§2.5) — all have the same shape: close the scheduler's open segment so the
// composite holds everything under them, take one copy of the pixels the batch
// is going to sample, bind that copy and draw the batch once.
//
// The copy is the expensive half. Each of the three used to hand-roll the
// sequence, and two of them copied the whole framebuffer for a batch that
// covered a few hundred pixels of it — a full-frame blit per explosion frame
// and per lit frame. readRect tracks the union of the region the batch will
// actually sample as the geometry is appended, and drawOverComposite copies
// only that. Nothing the shaders see changes: a fragment outside the copied
// rectangle is never produced, and a sample outside it was never taken.

// readRect is the union, in device pixels, of the composite region a layer's
// batch samples this frame. The zero value is empty.
type readRect struct {
	x0, y0, x1, y1 int
	any            bool
}

// reset empties the region for a new frame.
func (rr *readRect) reset() { *rr = readRect{} }

// add widens the region to cover the half-open rectangle [x0,x1)×[y0,y1),
// rounding outward so a fractional edge still copies the texel under it. A
// caller that samples away from its own geometry — a refraction — passes the
// displaced bound, not the quad's.
func (rr *readRect) add(x0, y0, x1, y1 float32) {
	if !(x0 < x1) || !(y0 < y1) {
		return
	}
	ix0, iy0 := floorInt(x0), floorInt(y0)
	ix1, iy1 := ceilInt(x1), ceilInt(y1)
	if !rr.any {
		rr.x0, rr.y0, rr.x1, rr.y1, rr.any = ix0, iy0, ix1, iy1, true
		return
	}
	rr.x0, rr.y0 = min(rr.x0, ix0), min(rr.y0, iy0)
	rr.x1, rr.y1 = max(rr.x1, ix1), max(rr.y1, iy1)
}

// drawOverComposite submits the schedule, copies rr out of the composite into
// the read surface, binds it into slot 0 of opts and draws the layer's batch
// back onto the composite. opts is the layer's retained options value, so the
// image binding and blend are set here rather than rebuilt per frame.
func (r *Renderer) drawOverComposite(rr readRect, verts []ebiten.Vertex, indices []uint32, shader *ebiten.Shader, opts *ebiten.DrawTrianglesShaderOptions, blend ebiten.Blend) {
	if !rr.any || len(indices) == 0 || shader == nil || r.surfaces[0] == nil || r.surfaces[1] == nil {
		return
	}
	// The batch reads the composite it writes, so everything under it has to be
	// on the composite first and the read has to come from a copy.
	r.submitSchedule()
	r.copyComposite(r.surfaces[1], r.surfaces[0], rr.x0, rr.y0, rr.x1, rr.y1)
	opts.Images = [4]*ebiten.Image{r.surfaces[1]}
	opts.Blend = blend
	r.beginPass(r.surfaces[0])
	r.recordSubmission(len(verts), len(indices))
	r.surfaces[0].DrawTrianglesShader32(verts, indices, shader, opts)
	r.frameDraws++
}

// floorInt and ceilInt round a device coordinate outward without going through
// math.Floor's float64 conversion.
func floorInt(v float32) int {
	n := int(v)
	if float32(n) > v {
		n--
	}
	return n
}

func ceilInt(v float32) int {
	n := int(v)
	if float32(n) < v {
		n++
	}
	return n
}
