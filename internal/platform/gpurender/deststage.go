package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// The destination-reading families for the modern executor beyond fog: the
// translucent strip blit (BlitTinted), the feature shadow stencil
// (BlitFeatureShadow), the UI light/shade rects (FillLitRect/FillShadeRect) and
// the lit point batch (PointLit), plus the source-through-LHT strip blit
// (BlitLit), which reads no destination (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4,
// C-G7).
//
// Every destination-reading family runs over a per-command snapshot: before the
// shading pass writes the offscreen, snapshotRect copies the command's covered
// rect from the offscreen into destScratch, and the pass reads destScratch (the
// pre-command destination) while writing the offscreen — the same read/write
// split fog uses for its gray layer (C-G7), so a dest-reading write never samples
// a pixel it just changed. Snapshotting per command (rather than per disjoint
// run) means an overlapping later command reads the earlier command's writes,
// exactly as the byte writers do when they read c.indexed in record order
// [03 R-COMP-01 §2].

// snapshotRect copies the offscreen's [x0,x1)×[y0,y1) rect into destScratch under
// BlendCopy, so a following dest-reading pass reads the pre-command destination
// there (C-G7). The atlas shader copies the red channel (the index); destScratch
// is full-surface and aligned 1:1 with the offscreen, so a screen pixel in the
// rect reads its own pre-command index. The rect is clamped to the framebuffer.
func (r *Renderer) snapshotRect(x0, y0, x1, y1 int) {
	if r.destScratch == nil || r.atlas == nil {
		return
	}
	x0, y0 = maxInt(x0, 0), maxInt(y0, 0)
	x1, y1 = minInt(x1, r.w), minInt(y1, r.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(float32(x0), float32(y0), float32(x1), float32(y1),
		float32(x0), float32(y0), float32(x1), float32(y1))
	r.destScratch.DrawTrianglesShader(r.verts, r.idx, r.atlas, &ebiten.DrawTrianglesShaderOptions{
		Blend:  ebiten.BlendCopy,
		Images: [4]*ebiten.Image{r.offscreen, nil, nil, nil},
	})
}

// clampLHTRow clamps a light level to the LHT's 0..31 row range, matching
// palette.Tables.LightLookup's own clamp so the GPU row equals the byte writer's
// row exactly [03 §4.3.1].
func clampLHTRow(level int) int {
	if level < 0 {
		return 0
	}
	if level > 31 {
		return 31
	}
	return level
}

// drawLit reproduces uiBlitLitRaw: every opaque source texel is written as
// LightLookup(row, src) — the SOURCE index folded through one LHT row — and the
// transparent key is skipped (docs/DESIGN_GPU_RENDERER.md §2.3)[03 §4.3.1]. It
// reads no destination, so it needs no snapshot; the geometry is the plain keyed
// blit's clip to the framebuffer at (x,y) with no anchor offset, exactly as
// uiBlitLitRaw walks x+col / y+row. row is the caller's LHT row clamped to 0..31.
func (r *Renderer) drawLit(f *formats.GAFFrame, x, y, row int) {
	if f == nil || r.litBlit == nil || r.tables.light == nil {
		return
	}
	img := r.gafImageFor(f)
	if img == nil {
		return
	}
	fw, fh := int(f.Width), int(f.Height)
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
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.litBlit, &ebiten.DrawTrianglesShaderOptions{
		Blend:    ebiten.BlendSourceOver,
		Uniforms: map[string]any{"Row": float32(row)},
		Images:   [4]*ebiten.Image{img, r.tables.light, nil, nil},
	})
}

// drawTint reproduces tintedBlitAnchor: it subtracts the frame's authored anchor
// offsets, clips to the framebuffer, and for every opaque source texel writes
// ALP[src*256 + dst] — a destination read (docs/DESIGN_GPU_RENDERER.md §2.3)
// [03 R-COMP-01 §2][03 R-FX-02 §2]. The covered rect is snapshotted first so the
// pass reads the pre-blit destination. The family's gate is "ALP present": with
// no ALP table it draws nothing, exactly as tintedBlitAnchor returns on a nil
// palette [03 R-COMP-01 §2].
func (r *Renderer) drawTint(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) {
	if f == nil || r.tint == nil || r.tables.alpha == nil || r.destScratch == nil {
		return
	}
	img := r.gafImageFor(f)
	if img == nil {
		return
	}
	x -= int(f.XOffset)
	y -= int(f.YOffset)
	fw, fh := int(f.Width), int(f.Height)
	minX, minY := maxInt(clipX, 0), maxInt(clipY, 0)
	maxX, maxY := minInt(clipX+clipW, r.w), minInt(clipY+clipH, r.h)
	col0, col1 := maxInt(0, minX-x), minInt(fw, maxX-x)
	row0, row1 := maxInt(0, minY-y), minInt(fh, maxY-y)
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

// drawShadow reproduces blitGAFFrame's isShadow path: where the shadow frame is
// opaque, the destination pixel is darkened through PALETTE.SHD row 8 (translucent
// flag) or 4 (opaque flag) — a destination read (docs/DESIGN_GPU_RENDERER.md
// §2.3)[03 §4.4]. The caller has already applied the frame's anchor offset (the
// feature GAF site records the final top-left), so no offset is subtracted here;
// the clip is the framebuffer. The covered rect is snapshotted first.
//
// blitGAFFrame's palette-absent branch writes index 0 for every opaque shadow
// pixel; that branch is unreachable in a composed frame (a feature shadow is only
// recorded when the game — and its palette — is loaded), so with the SHD table
// present this reproduces the only reachable path. The table guard keeps a
// palette-less renderer from drawing rather than guessing index 0.
func (r *Renderer) drawShadow(f *formats.GAFFrame, x, y int, trans bool) {
	if f == nil || r.shadow == nil || r.tables.shade == nil || r.destScratch == nil {
		return
	}
	img := r.gafImageFor(f)
	if img == nil {
		return
	}
	fw, fh := int(f.Width), int(f.Height)
	col0, col1 := maxInt(0, -x), minInt(fw, r.w-x)
	row0, row1 := maxInt(0, -y), minInt(fh, r.h-y)
	if col0 >= col1 || row0 >= row1 {
		return
	}
	dx0, dy0, dx1, dy1 := x+col0, y+row0, x+col1, y+row1
	r.snapshotRect(dx0, dy0, dx1, dy1)
	// Row 8 for the translucent flag, 4 otherwise, matching blitGAFFrame [03 §4.4].
	shadowRow := 4
	if trans {
		shadowRow = 8
	}
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendTexQuad(
		float32(dx0), float32(dy0), float32(dx1), float32(dy1),
		float32(col0), float32(row0), float32(col1), float32(row1))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.shadow, &ebiten.DrawTrianglesShaderOptions{
		Blend:    ebiten.BlendSourceOver,
		Uniforms: map[string]any{"Row": float32(shadowRow)},
		Images:   [4]*ebiten.Image{img, r.destScratch, r.tables.shade, nil},
	})
}

// drawLitRect reproduces uiLightRectRaw: every pixel of the framebuffer-clipped
// rect is rewritten as LightLookup(level, dst) — a destination read through one
// LHT row (docs/DESIGN_GPU_RENDERER.md §2.3)[03 §4.3.1]. The level is clamped to
// the LHT's 0..31 row range as LightLookup does.
func (r *Renderer) drawLitRect(x, y, w, h, level int) {
	r.drawDestRect(x, y, w, h, clampLHTRow(level), r.tables.light)
}

// drawShadeRect reproduces uiShadeRectRaw: every pixel of the framebuffer-clipped
// rect is rewritten through the signed fade table — SHD row level+32 (clamped at
// -32) for a negative level, else LHT row level — a destination read
// (docs/DESIGN_GPU_RENDERER.md §2.3)[03 R-COMP-02 §5]. The row selection and its
// clamps match uiShadeRectRaw exactly.
func (r *Renderer) drawShadeRect(x, y, w, h, level int) {
	useShade := level < 0
	row := level
	if useShade {
		if row < -32 {
			row = -32
		}
		row += 32
	}
	if row > 31 {
		row = 31
	}
	if row < 0 {
		row = 0
	}
	table := r.tables.light
	if useShade {
		table = r.tables.shade
	}
	r.drawDestRect(x, y, w, h, row, table)
}

// drawDestRect is the shared body of the UI light/shade rects: it clips the rect
// to the framebuffer, snapshots it, and rewrites every pixel as TABLE[row][dst]
// through the destTable pass under BlendCopy (docs/DESIGN_GPU_RENDERER.md §2.3).
// The row rides the vertex colour, constant across the quad. A nil table (no
// palette) draws nothing, matching the byte writers' nil-palette return.
func (r *Renderer) drawDestRect(x, y, w, h, row int, table *ebiten.Image) {
	if r.destTable == nil || table == nil || r.destScratch == nil || w <= 0 || h <= 0 {
		return
	}
	x0, y0 := maxInt(x, 0), maxInt(y, 0)
	x1, y1 := minInt(x+w, r.w), minInt(y+h, r.h)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	r.snapshotRect(x0, y0, x1, y1)
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	r.appendDestTableQuad(float32(x0), float32(y0), float32(x1), float32(y1), uint8(row))
	r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.destTable, &ebiten.DrawTrianglesShaderOptions{
		Blend:  ebiten.BlendCopy,
		Images: [4]*ebiten.Image{r.destScratch, table, nil, nil},
	})
}

// drawLitPoints reproduces the PointLit branch of classicSink.Points: each point
// rewrites its destination pixel as LightLookup(pt.Index, dst) — a destination
// read through the point's own LHT row (docs/DESIGN_GPU_RENDERER.md §2.3)
// [03 §4.3.1][03 R-FX-01 §4]. A run of pairwise-distinct points shares one
// destination snapshot; its geometry can then split at the uint16 index bound
// while every chunk still reads that same pre-run image. A repeated point closes
// the run, so its next snapshot observes the earlier write in record order.
// Points outside the framebuffer are skipped, matching the producers' own clip.
func (r *Renderer) drawLitPoints(points []drawlist.Point) {
	if r.destTable == nil || r.tables.light == nil || r.destScratch == nil || len(points) == 0 {
		return
	}
	start := 0
	seen := make(map[uint64]struct{}, len(points))
	for i, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if x < 0 || y < 0 || x >= r.w || y >= r.h {
			continue
		}
		key := uint64(uint32(x))<<32 | uint64(uint32(y))
		if _, duplicate := seen[key]; duplicate {
			r.drawDistinctLitPoints(points[start:i])
			start = i
			clear(seen)
		}
		seen[key] = struct{}{}
	}
	r.drawDistinctLitPoints(points[start:])
}

// drawDistinctLitPoints draws a record-order run whose visible points do not
// overlap. Each chunk uses one snapshot of the entire run, so splitting the
// uint16 geometry scratch cannot change the destination bytes it reads.
func (r *Renderer) drawDistinctLitPoints(points []drawlist.Point) {
	minX, minY, maxX, maxY := 0, 0, 0, 0
	any := false
	for _, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if x < 0 || y < 0 || x >= r.w || y >= r.h {
			continue
		}
		if !any {
			minX, minY, maxX, maxY = x, y, x, y
			any = true
			continue
		}
		minX, minY = minInt(minX, x), minInt(minY, y)
		maxX, maxY = maxInt(maxX, x), maxInt(maxY, y)
	}
	if !any {
		return
	}
	r.snapshotRect(minX, minY, maxX+1, maxY+1)
	r.verts = r.verts[:0]
	r.idx = r.idx[:0]
	flush := func() {
		if len(r.verts) == 0 {
			return
		}
		r.offscreen.DrawTrianglesShader(r.verts, r.idx, r.destTable, &ebiten.DrawTrianglesShaderOptions{
			Blend:  ebiten.BlendCopy,
			Images: [4]*ebiten.Image{r.destScratch, r.tables.light, nil, nil},
		})
	}
	for _, pt := range points {
		x, y := int(pt.X), int(pt.Y)
		if x < 0 || y < 0 || x >= r.w || y >= r.h {
			continue
		}
		if !r.quadBatchHasRoom() {
			flush()
			r.resetGeometry()
		}
		row := clampLHTRow(int(pt.Index))
		r.appendDestTableQuad(float32(x), float32(y), float32(x+1), float32(y+1), uint8(row))
	}
	flush()
}

// appendDestTableQuad appends one axis-aligned quad covering [dx0,dx1)×[dy0,dy1)
// for the destTable pass: the source coordinates equal the destination screen
// coordinates so destScratch (source image 0) is sampled 1:1 at the fragment's
// pixel, and the table row rides the red vertex channel as row/255 (C-G4).
func (r *Renderer) appendDestTableQuad(dx0, dy0, dx1, dy1 float32, row uint8) {
	c := float32(row) / 255.0
	base := uint16(len(r.verts))
	r.verts = append(r.verts,
		ebiten.Vertex{DstX: dx0, DstY: dy0, SrcX: dx0, SrcY: dy0, ColorR: c, ColorA: 1},
		ebiten.Vertex{DstX: dx1, DstY: dy0, SrcX: dx1, SrcY: dy0, ColorR: c, ColorA: 1},
		ebiten.Vertex{DstX: dx0, DstY: dy1, SrcX: dx0, SrcY: dy1, ColorR: c, ColorA: 1},
		ebiten.Vertex{DstX: dx1, DstY: dy1, SrcX: dx1, SrcY: dy1, ColorR: c, ColorA: 1},
	)
	r.idx = append(r.idx, base, base+1, base+2, base+1, base+2, base+3)
}
