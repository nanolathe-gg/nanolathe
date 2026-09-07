package client

// The model bridge to the modern (GPU) executor. Per docs/DESIGN_GPU_RENDERER.md
// §2.1 C-G5, during the parity phases the modern executor does not rasterize
// models itself; it uploads the CLASSIC-rasterized model image. These resolvers
// hand that finished image — body and shadow — to the wiring layer as plain,
// uploadable data, keyed by the same drawlist.Model.Ref the classic sink uses.
//
// The client stays device-independent: nothing here imports Ebitengine or the
// gpurender package, and the returned data carries only builtin types [I6]. The
// wiring layer (internal/platform/ebitenapp, cmd/nanolathe), which imports both
// packages, adapts a ModelImageData to a gpurender.ModelImage.

// ModelImageData is one finished model composition image — a body or a shadow —
// in the plain form the GPU executor uploads: the colour plane (physical palette
// indices), the coverage mask that keys the blit (an uncovered pixel is the
// transparent key the commit skips), the image dimensions, the framebuffer
// top-left the image blits at, and the image's transparent index.
//
// DX,DY is anchorX-originX / anchorY-originY: the framebuffer pixel the image's
// own (0,0) lands on, exactly what modelTarget.screenX/screenY resolve an image
// pixel to during the classic commit [R-REN-03A §1]. Color and Covered alias the
// composition image's own planes; they are read-only for the life of the frame.
type ModelImageData struct {
	Color       []uint8
	Covered     []bool
	W, H        int
	DX, DY      int
	Transparent uint8
}

// modelImageData snapshots a finished composition image's planes and placement
// into the plain bridge form. The anchor-minus-origin here is the same value the
// classic commit's screenX(0)/screenY(0) produce, so the GPU blit lands the image
// at the identical framebuffer top-left [R-REN-03A §1].
func modelImageData(t *modelTarget) ModelImageData {
	if t == nil {
		return ModelImageData{}
	}
	return ModelImageData{
		Color:       t.color,
		Covered:     t.covered,
		W:           t.width,
		H:           t.heightPx,
		DX:          int(t.anchorX - t.originX),
		DY:          int(t.anchorY - t.originY),
		Transparent: t.transparent,
	}
}

// ModelBodyImage returns the finished body image for a recorded drawlist.Model
// Ref — the image classicSink.Model blits with pending.blit.commit — as plain
// data. ok is false when the Ref is out of range, records no body step, or has no
// blit image, mirroring the classicSink.Model body guard exactly so the modern
// executor blits a body in precisely the cases the classic sink does
// (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5).
func (c *Client) ModelBodyImage(ref int) (ModelImageData, bool) {
	if c == nil || ref < 0 || ref >= len(c.modelCommits) {
		return ModelImageData{}, false
	}
	pending := c.modelCommits[ref]
	if !pending.body || pending.blit == nil {
		return ModelImageData{}, false
	}
	return modelImageData(pending.blit), true
}

// ModelShadowImage builds the finished shadow image for a recorded drawlist.Model
// Ref — the punched silhouette classicSink.Model composites with drawModelShadow
// — and returns it as plain data for the GPU executor to commit through ALP. It
// runs the same buildModelShadow the classic path runs, over the same body image
// (pending.m.image, which drawModelShadow's punch-out reads, not pending.blit),
// so the shadow byte planes are identical to the classic path's; only the tinted
// commit is deferred to the GPU. ok is false when the Ref is out of range,
// records no shadow step, or the shadow has no faces (docs/DESIGN_GPU_RENDERER.md
// §2.1 C-G5) [R-REN-03D §5].
func (c *Client) ModelShadowImage(ref int) (ModelImageData, bool) {
	if c == nil || ref < 0 || ref >= len(c.modelCommits) {
		return ModelImageData{}, false
	}
	pending := c.modelCommits[ref]
	if !pending.shadow {
		return ModelImageData{}, false
	}
	img := c.buildModelShadow(pending.m.draw, pending.m.image)
	if img == nil {
		return ModelImageData{}, false
	}
	return modelImageData(img), true
}
