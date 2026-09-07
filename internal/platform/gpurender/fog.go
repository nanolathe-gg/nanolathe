package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/render"
)

// The fog composite for the modern executor (docs/DESIGN_GPU_RENDERER.md C-G7).
// Fog is the first destination-reading family: the gray fills and the gray fog
// GAF remap the pixels already on the surface through the GRAY TABLE. To read the
// composed destination while writing it without a read-after-write hazard, the
// pass first builds a gray-remapped copy of the whole offscreen into grayScratch
// (a separate image); the fog fills and gray fog GAF then sample grayScratch,
// while every write goes to the offscreen (C-G4). grayScratch is
// Gray[offscreen-before-fog]; because the fog fills are pairwise-disjoint 32×32
// cells, a gray fill reads exactly the pre-fog destination its classic byte
// writer (fogFillGray) reads, so the two match pixel-for-pixel [03 §3.3].
//
// Every op reproduces its classic byte writer's rebase, clip and per-pixel value
// from internal/client (classicSink.Fog, fogFillSolid, fogFillGray,
// fogFillChecker, blitFogGAF) exactly [03 §3.3][R-RR16-A §1][R-RR16-A §2].

// Fog replays one clipped fog op list into the indexed offscreen (C-G7). It runs
// the same per-op rebase and clip classicSink.Fog runs, then dispatches each op
// to the GPU pass that matches its classic byte writer: solid fills write the
// fog dark index, gray fills copy the pre-built gray layer, patterned fills test
// (x + y + parity) & 1, and fog GAF frames sample their frame under the black,
// plain-gray or dithered-gray mode. The visible result per pixel is the byte
// writer's [03 §3.3].
func (r *Renderer) Fog(fg drawlist.Fog) {
	if r == nil || r.offscreen == nil || r.solid == nil {
		return
	}
	if len(fg.Ops) == 0 {
		return
	}
	w, h := int32(r.w), int32(r.h)

	// Build the gray-remapped destination layer once, before any fog write, so the
	// gray fills and plain gray fog GAF read the pre-fog destination (C-G7). It is
	// only needed when the gray table is present; without it the gray-reading kinds
	// are skipped, exactly as fogFillGray / blitFogGAF(fogBlitGray) return early
	// when the palette is absent [03 §3.3].
	grayReady := r.tables.gray != nil && r.grayScratch != nil && r.fogGray != nil
	if grayReady {
		r.buildGrayScratch()
	}

	for i := range fg.Ops {
		op := &fg.Ops[i]
		x0, y0, x1, y1 := op.ScreenX0, op.ScreenY0, op.ScreenX1, op.ScreenY1
		// Rebase from the retail viewport origin (128,32) to the full-window shell
		// origin (0,0). The modern executor always rebases: the fog op list is only
		// ever recorded with a live camera (drawFog gates on c.cam != nil), so this
		// matches classicSink.Fog's `if c.cam != nil` branch [03 §2.5][03 §3.3].
		x0 -= camera.OriginX
		y0 -= camera.OriginY
		x1 -= camera.OriginX
		y1 -= camera.OriginY
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > w {
			x1 = w
		}
		if y1 > h {
			y1 = h
		}
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		switch op.Kind {
		case render.FogKindSolidDark:
			// lo==15: fill the clipped cell with the fog dark index (fogFillSolid).
			r.fogFillSolidRect(x0, y0, x1, y1)
		case render.FogKindGrayRemap:
			// hi==15: remap the destination through the GRAY TABLE (fogFillGray).
			if grayReady {
				r.fogFillGrayRect(x0, y0, x1, y1)
			}
		case render.FogKindPatterned:
			// hi==15 dithered: black checker at (x+y+parity)&1==1 (fogFillChecker).
			r.fogFillCheckerRect(x0, y0, x1, y1, fogParity(op))
		case render.FogKindGAFCh1:
			// hi 1..14: gray-family fog GAF, plain (masked Gray[dst]) or dithered.
			frame := fogGAFFrame(fg.Gray, op)
			if frame == nil {
				// Missing GAF entry/frame: the cell keeps the underlying tile,
				// exactly as classicSink.Fog skips the blit [03 §3.3].
				continue
			}
			if op.Patterned {
				r.drawFogGAF(frame, int(x0), int(y0), r.fogPatMask, ebiten.BlendSourceOver, nil,
					map[string]any{"Parity": float32(fogParity(op))})
			} else if grayReady {
				r.drawFogGAF(frame, int(x0), int(y0), r.fogGrayMask, ebiten.BlendSourceOver, r.grayScratch, nil)
			}
		case render.FogKindGAFCh0:
			// lo 1..14: black-family fog GAF, always a plain keyed copy of the frame
			// (blitFogGAF fogBlitBlack). The keyed pass leaves keyed texels alone.
			frame := fogGAFFrame(fg.Black, op)
			if frame == nil {
				continue
			}
			r.drawFogGAF(frame, int(x0), int(y0), r.gafKeyed, ebiten.BlendSourceOver, nil, nil)
		default:
			// Visible cells produce no ops; nothing to draw.
			continue
		}
	}
}

// fogParity derives the fog checker parity (camX + camZ) & 1 from an op's screen
// rectangle, so the GPU sink needs no camera pointer. The producer places a cell
// at ScreenX0 = gx*32 + 16 - camX + OriginX and ScreenY0 = gy*32 + 16 - camZ +
// OriginY (render.FogScreenRect); every term but -camX / -camZ is even, so
// (ScreenX0 + ScreenY0) & 1 == (camX + camZ) & 1 for every op — the same value
// fogFillChecker and blitFogGAF compute from the camera [03 §3.3].
func fogParity(op *render.FogOp) int32 {
	return (op.ScreenX0 + op.ScreenY0) & 1
}

// fogGAFFrame resolves the GAF frame for a fog GAF op from one variant family
// carried on the record, matching the entry/frame bounds classicSink.Fog checks:
// a valid variant (0..3) and frame index, a non-nil entry, and op.Frame within
// entry.Frames; anything else yields nil and the cell keeps the underlying tile
// [03 §3.3].
func fogGAFFrame(family [4]*formats.GAFEntry, op *render.FogOp) *formats.GAFFrame {
	if op.Variant < 0 || op.Variant >= 4 || op.Frame < 0 {
		return nil
	}
	entry := family[op.Variant]
	if entry == nil || op.Frame >= len(entry.Frames) {
		return nil
	}
	return entry.Frames[op.Frame].Frame
}

// fogFillSolidRect fills [x0,x1)×[y0,y1) with the fog dark index using the
// constant-index solid pass under BlendCopy, matching fogFillSolid [03 §3.3].
func (r *Renderer) fogFillSolidRect(x0, y0, x1, y1 int32) {
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendSolidQuad(float32(x0), float32(y0), float32(x1), float32(y1), byte(render.FogDarkPaletteIndex))
	r.flushSolid()
}

// fogFillGrayRect copies the pre-built gray layer over [x0,x1)×[y0,y1) under
// BlendCopy, matching fogFillGray's `dst = Gray[dst]` [03 §3.3]. The atlas pass
// copies the red channel of the gray layer; the quad's source coordinates equal
// the destination screen coordinates, so grayScratch is sampled 1:1 (C-G4).
func (r *Renderer) fogFillGrayRect(x0, y0, x1, y1 int32) {
	if r.atlas == nil {
		return
	}
	r.drawTexQuad(r.grayScratch, r.atlas, ebiten.BlendCopy,
		float32(x0), float32(y0), float32(x1), float32(y1),
		float32(x0), float32(y0), float32(x1), float32(y1))
}

// fogFillCheckerRect writes the fog dark index on the dithered checker over
// [x0,x1)×[y0,y1), keeping every other pixel, matching fogFillChecker [03 §3.3]
// [R-RR16-A §2]. The keep case relies on the source-over blend leaving the
// offscreen byte in place under a transparent fragment (C-G4).
func (r *Renderer) fogFillCheckerRect(x0, y0, x1, y1, parity int32) {
	if r.fogCheck == nil {
		return
	}
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(float32(x0), float32(y0), float32(x1), float32(y1), 0, 0, 0, 0)
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.fogCheck, &ebiten.DrawTrianglesShaderOptions{
		Blend:    ebiten.BlendSourceOver,
		Uniforms: map[string]any{"Parity": float32(parity)},
	})
}

// buildGrayScratch fills grayScratch with GRAY TABLE[offscreen] over the whole
// surface (C-G7). It reads the offscreen (source 0) and the gray table (source 1)
// and writes grayScratch under BlendCopy — a read of the offscreen and a write of
// a different image, so there is no read-after-write hazard. The quad maps 1:1, so
// grayScratch pixel (x,y) is Gray of offscreen pixel (x,y).
func (r *Renderer) buildGrayScratch() {
	fw, fh := float32(r.w), float32(r.h)
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(0, 0, fw, fh, 0, 0, fw, fh)
	r.grayScratch.DrawTrianglesShader(r.verts, r.idx, r.fogGray, &ebiten.DrawTrianglesShaderOptions{
		Blend:  ebiten.BlendCopy,
		Images: [4]*ebiten.Image{r.offscreen, r.tables.gray, nil, nil},
	})
}

// drawFogGAF blits one fog GAF frame at cell origin (x,y) — the clamped, rebased
// cell top-left classicSink.Fog passes to blitFogGAF — reproducing blitFogGAF's
// anchor and framebuffer clip (docs/DESIGN_GPU_RENDERER.md C-G7). It subtracts the
// frame's authored XOffset/YOffset (the fog quadrant geometry lives entirely in
// those anchors), clips the frame to the framebuffer, and draws it with the given
// shader and blend, sampling the frame's index texture (source 0) plus an optional
// second image (the gray layer for the plain gray mode) [03 §3.3][R-RR16-A §3].
func (r *Renderer) drawFogGAF(f *formats.GAFFrame, x, y int, shader *ebiten.Shader, blend ebiten.Blend, src1 *ebiten.Image, uniforms map[string]any) {
	if f == nil || shader == nil {
		return
	}
	img := r.gafImageFor(f)
	if img == nil {
		return
	}
	// Retail anchor: dest = cell origin - frame offset [03 §3.3][fmt gaf].
	x -= int(f.XOffset)
	y -= int(f.YOffset)
	fw, fh := int(f.Width), int(f.Height)
	// blitFogGAF clips only against the framebuffer, not the cell rect: the fog
	// quadrant frames extend beyond their 32×32 cell [03 §3.3].
	col0, col1 := maxInt(0, -x), minInt(fw, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(fh, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(
		float32(x+col0), float32(y+row0), float32(x+col1), float32(y+row1),
		float32(col0), float32(row0), float32(col1), float32(row1))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, shader, &ebiten.DrawTrianglesShaderOptions{
		Blend:    blend,
		Uniforms: uniforms,
		Images:   [4]*ebiten.Image{img, src1, nil, nil},
	})
}
