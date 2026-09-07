package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The composed-model family for the modern executor (docs/DESIGN_GPU_RENDERER.md
// §2.1 C-G5). During the parity phases the GPU does not rasterize models; it
// uploads the CLASSIC-rasterized model image and commits it exactly as the
// classic sink does — the shadow through ALP, then the body as a keyed blit. GPU
// model rasterization is Phase 3 and out of scope here.
//
// A Model command carries only a Ref into the client-side per-frame table
// (drawlist.Model.Ref). gpurender cannot import internal/client (the client must
// stay Ebitengine-free), so the client hands the finished body and shadow images
// to the wiring layer as plain data and the wiring layer adapts them to
// ModelImage and installs a ModelSource on the renderer before each Execute. The
// renderer resolves a Ref through that source; with no source installed, a Model
// command draws nothing.

// ModelImage is one finished model composition image — a body or a shadow — in
// the neutral, uploadable form the GPU executor blits: the colour plane
// (physical palette indices), the coverage mask that keys the blit (an uncovered
// pixel is the transparent key the commit skips), the image dimensions, the
// framebuffer top-left the image blits at, and the image's transparent index.
//
// DX,DY is the classic composition image's anchorX-originX / anchorY-originY: the
// framebuffer pixel the image's own (0,0) lands on, so a keyed or tinted blit at
// (DX,DY) reproduces modelTarget.commit / tintedCommit's screenX/screenY mapping
// pixel for pixel [R-REN-03A §1].
type ModelImage struct {
	Color       []uint8
	Covered     []bool
	W, H        int
	DX, DY      int
	Transparent uint8
}

// ModelSource resolves a drawlist.Model.Ref to its finished body and shadow
// images (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5). The wiring layer implements it
// over the client's per-frame model table; ok is false for a Ref that records no
// such step, matching the classic sink's own body/shadow guards so the modern
// executor draws each half in precisely the cases the classic sink does.
type ModelSource interface {
	ModelBody(ref int) (ModelImage, bool)
	ModelShadow(ref int) (ModelImage, bool)
}

// SetModelSource installs the per-frame model source. The battle app and the
// modern shot path call it after recording the frame and before Execute, because
// the client-side model table the source reads is populated during recording.
func (r *Renderer) SetModelSource(src ModelSource) {
	if r == nil {
		return
	}
	r.modelSrc = src
}

// Model replays one composed model subject: the shadow first, then the body, the
// classic sink's order for the same subject (docs/DESIGN_GPU_RENDERER.md §2.1
// C-G5)[03 §5.3]. The classic sink's third step, the parity trace, is diagnostic
// only — it reads the finished surface and writes event records, never a
// framebuffer pixel — so the modern executor skips it entirely; drawing it would
// be wrong.
func (r *Renderer) Model(cmd drawlist.Model) {
	if r == nil || r.offscreen == nil || r.modelSrc == nil {
		return
	}
	if shadow, ok := r.modelSrc.ModelShadow(cmd.Ref); ok {
		r.blitModelShadow(shadow)
	}
	if body, ok := r.modelSrc.ModelBody(cmd.Ref); ok {
		r.blitModelBody(body)
	}
}

// uploadModelImage uploads one ModelImage as an index texture: index in red,
// coverage in green (255 covered, 0 uncovered), alpha opaque so premultiplied
// sampling recovers both bytes (C-G4). Coverage is the classic commit's key — a
// covered pixel is drawn, an uncovered one is the transparent key both the keyed
// body blit and the tinted shadow commit skip — so the green flag reproduces
// modelTarget.commit's `if !covered continue` and tintedCommit's `if !covered
// continue` exactly [R-REN-03A §1]. A model image changes every frame, so it is
// uploaded per draw (a per-frame upload is acceptable for parity,
// docs/DESIGN_GPU_RENDERER.md §2.3 note under caches).
func uploadModelImage(m ModelImage) *ebiten.Image {
	if m.W <= 0 || m.H <= 0 {
		return nil
	}
	buf := make([]byte, m.W*m.H*4)
	nc := len(m.Color)
	nv := len(m.Covered)
	for i := 0; i < m.W*m.H; i++ {
		if i < nv && m.Covered[i] {
			if i < nc {
				buf[i*4+0] = m.Color[i]
			}
			buf[i*4+1] = 255
		}
		buf[i*4+3] = 255
	}
	img := ebiten.NewImage(m.W, m.H)
	img.WritePixels(buf)
	return img
}

// blitModelBody reproduces modelTarget.commit: a keyed blit of the finished body
// image at its framebuffer top-left (DX,DY), clipped to the framebuffer, skipping
// uncovered (transparent-key) pixels (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5)
// [R-REN-03A §1]. It is the keyed-blit path of C-G4: the gafKeyed shader copies a
// covered texel's index under the source-over blend and leaves the destination
// untouched under an uncovered one, exactly the covered-pixel set and per-pixel
// value the byte writer's `dst[screenY*w+screenX] = color[i]` produces. The clip
// math is commit's own: screenX = DX+ix, clamped to [0,w), and likewise in Y.
func (r *Renderer) blitModelBody(m ModelImage) {
	if r == nil || r.offscreen == nil || r.gafKeyed == nil {
		return
	}
	img := uploadModelImage(m)
	if img == nil {
		return
	}
	x, y := m.DX, m.DY
	col0, col1 := maxInt(0, -x), minInt(m.W, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(m.H, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	r.drawTexQuad(img, r.gafKeyed, ebiten.BlendSourceOver,
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(col0), float32(row0), float32(col1), float32(row1))
}

// blitModelShadow reproduces modelTarget.tintedCommit: for every covered shadow
// pixel it resolves the destination to ALP[color*256 + dst], the same ALP form
// the translucent strip blit (drawTint) already runs, sourced from the shadow
// image with NO anchor-offset subtraction because the shadow image's anchors are
// baked into DX,DY (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5)[R-REN-03D §4]. The
// shadow's colour plane is index 0 everywhere it is covered, so this darkens each
// ground pixel toward black; carrying the colour through the shader rather than
// assuming 0 keeps the commit identical to tintedCommit's `ALP[color*256+dst]`.
//
// It runs over a per-command snapshot of the covered rect (snapshotRect →
// destScratch), the dest-reading pattern WU-2.6 built, so the pass reads the
// pre-shadow destination exactly as tintedCommit reads c.indexed before the body
// overwrites it. The geometry — source rect in image-local pixels, dest in screen
// pixels — matches drawTint so the tint shader's destScratch addressing is the
// verified one.
func (r *Renderer) blitModelShadow(m ModelImage) {
	if r == nil || r.offscreen == nil || r.tint == nil || r.tables.alpha == nil || r.destScratch == nil {
		return
	}
	img := uploadModelImage(m)
	if img == nil {
		return
	}
	x, y := m.DX, m.DY
	col0, col1 := maxInt(0, -x), minInt(m.W, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(m.H, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	dx0, dy0, dx1, dy1 := x+col0, y+row0, x+col1, y+row1
	r.snapshotRect(dx0, dy0, dx1, dy1)
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(
		float32(dx0), float32(dy0), float32(dx1), float32(dy1),
		float32(col0), float32(row0), float32(col1), float32(row1))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.tint, &ebiten.DrawTrianglesShaderOptions{
		Blend:  ebiten.BlendSourceOver,
		Images: [4]*ebiten.Image{img, r.destScratch, r.tables.alpha, nil},
	})
}
