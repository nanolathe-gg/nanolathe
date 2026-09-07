package gpurender

import "github.com/hajimehoshi/ebiten/v2"

// expandShaderSource is the index→RGBA expansion pass, the final and only
// colour pass of the modern composite (docs/DESIGN_GPU_RENDERER.md C-G8). It
// runs in Kage pixel mode over the indexed offscreen (source image 0), whose red
// channel carries the palette index, and maps each index to its colour through
// PALETTE.PAL (source image 1, a 256×1 RGBA colour table).
//
// Index arithmetic is exact (C-G4). Nearest sampling makes the sampled red equal
// to index/255, so int(r*255 + 0.5) — floor(r*255 + 0.5) here — recovers the
// byte with no float landing between two entries. PAL is fetched by an integer
// texel coordinate: imageSrc1AtFromSrc0Pos addresses source image 1 in source
// image 0's coordinate space (it translates by the src0→src1 region origins), so
// imageSrc0Origin() + (index+0.5, 0.5) lands on the centre of column `index`,
// row 0 of PAL. There is no linear filter and no blend on the index. Alpha is
// forced opaque, exactly as the software expansion does (C-G8).
//
// Sizes differ between the two source images (the offscreen is the screen size,
// PAL is 256×1); the pixel unit permits that for DrawTrianglesShader, which is
// why the expansion is a triangle draw rather than DrawRectShader (which requires
// every source image to match the destination rectangle size).
const expandShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	// Decode the palette index from source 0's red channel (C-G4).
	idx := floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	// Fetch PALETTE.PAL[idx]. PAL is source image 1; imageSrc1AtFromSrc0Pos wants
	// the position in source 0's space, so offset by source 0's origin and pick
	// the texel centre of column idx, row 0 (C-G4, C-G8).
	col := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(idx+0.5, 0.5))
	// Force alpha opaque, matching the software convertIndexedToRGBA (C-G8).
	return vec4(col.rgb, 1.0)
}
`

// newExpandShader compiles the expansion pass once for the renderer's lifetime.
func newExpandShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(expandShaderSource))
}

// litBlitShaderSource is the shaded glyph blit (BlitLit): every opaque source
// texel is remapped through one PALETTE.LHT row before it is written, and the
// transparent key is skipped (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It
// reproduces internal/client uiBlitLitRaw's per-pixel `dst = LightLookup(level,
// src)`: the value written is the SOURCE index folded through LHT, not the
// destination, so this family reads no destination and needs no snapshot
// [03 §4.3.1].
//
// Source image 0 is the GAF frame (index in red, opacity flag in green); source
// image 1 is PALETTE.LHT (256×32, index in red). Row is the LHT row the caller
// clamped to 0..31 (LightLookup's own clamp), passed as a uniform. LHT[row*256 +
// src] is texel (col = src, row = Row); the imageSrc1AtFromSrc0Pos idiom lands on
// its centre from source 0's space (C-G4). A transparent texel (green 0) returns
// a transparent fragment, so under the source-over blend the offscreen byte is
// left in place, exactly as uiBlitLitRaw's `f.At` miss skips the write.
const litBlitShaderSource = `//kage:unit pixels

package main

var Row float

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	// Green carries the frame's opacity flag; a transparent texel writes nothing
	// under the source-over blend (C-G4).
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	src := floor(tex.r*255.0 + 0.5)
	// LHT[Row][src] from source image 1 (C-G4)[03 §4.3.1].
	l := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(src+0.5, Row+0.5))
	idx := floor(l.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// tintShaderSource is the translucent strip blit (BlitTinted): every opaque
// source texel resolves the destination to ALP[src*256 + dst], the retail
// translucent table lookup (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It
// reproduces internal/client tintedBlitAnchor exactly [03 R-COMP-01 §2]
// [03 R-FX-02 §2]. It reads the destination, so the caller snapshots the covered
// rect of the offscreen into destScratch first (the fog snapshot pattern, C-G7);
// this shader samples destScratch for the pre-blit destination byte, never the
// live offscreen, so there is no read-after-write hazard.
//
// Source image 0 is the frame (index in red, opacity flag in green), sampled at
// the frame texel; source image 1 is destScratch (full-surface pre-blit copy of
// the offscreen, index in red), sampled at the fragment's destination pixel;
// source image 2 is PALETTE.ALP (256×256, index in red). ALP[src*256 + dst] is
// texel (col = dst, row = src). A transparent frame texel returns a transparent
// fragment, so the source-over blend keeps the offscreen byte, exactly as
// tintedBlitAnchor's key skip does (C-G4).
const tintShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	src := floor(tex.r*255.0 + 0.5)
	// The pre-blit destination byte from destScratch, at this fragment's pixel.
	d := dstPos.xy - imageDstOrigin()
	ds := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(floor(d.x)+0.5, floor(d.y)+0.5))
	dst := floor(ds.r*255.0 + 0.5)
	// ALP[src*256 + dst] from source image 2 (C-G4)[03 R-COMP-01 §2].
	a := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(dst+0.5, src+0.5))
	idx := floor(a.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// shadowShaderSource is the feature GAF shadow stencil (BlitFeatureShadow): where
// the shadow frame is opaque, the destination pixel is darkened through one
// PALETTE.SHD row (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It reproduces
// internal/client blitGAFFrame's isShadow path: `dst = Shade[Row][dst]`, with Row
// 8 for the translucent flag and 4 otherwise [03 §4.4]. It reads the destination,
// so the caller snapshots the covered rect into destScratch first (C-G7).
//
// Source image 0 is the shadow frame (opacity flag in green); source image 1 is
// destScratch (pre-blit offscreen copy); source image 2 is PALETTE.SHD (256×32).
// SHD[Row][dst] is texel (col = dst, row = Row). A transparent (keyed) frame
// texel returns a transparent fragment, so the source-over blend keeps the
// offscreen byte, exactly as blitGAFFrame's key skip does (C-G4).
const shadowShaderSource = `//kage:unit pixels

package main

var Row float

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	d := dstPos.xy - imageDstOrigin()
	ds := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(floor(d.x)+0.5, floor(d.y)+0.5))
	dst := floor(ds.r*255.0 + 0.5)
	// SHD[Row][dst] from source image 2 (C-G4)[03 §4.4].
	s := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(dst+0.5, Row+0.5))
	idx := floor(s.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// destTableShaderSource is the shared destination-through-table pass for the
// frameless dest-reading families — the UI light rect, the UI shade rect and the
// lit point batch (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). Each rewrites a
// destination pixel in place as TABLE[Row][dst], where the table is PALETTE.LHT
// (FillLitRect, the non-negative FillShadeRect level, PointLit) or PALETTE.SHD
// (the negative FillShadeRect level), and Row is the resolved table row the byte
// writer used [03 §4.3.1][03 R-COMP-02 §5][03 R-FX-01 §4].
//
// Source image 0 is destScratch (the pre-pass offscreen copy, sampled at the
// screen pixel because the quad's source coordinates are the screen coordinates);
// source image 1 is the table (LHT or SHD, 256×32). Row rides the RED vertex
// channel as Row/255, so a lit point batch draws every point in one pass with its
// own per-point row while a rect draws with one constant row on all four vertices
// (C-G4). TABLE[row*256 + dst] is texel (col = dst, row = Row). The caller draws
// with BlendCopy — every covered pixel is rewritten — so no key skip applies.
const destTableShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	old := floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	row := floor(color.r*255.0 + 0.5)
	t := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(old+0.5, row+0.5))
	idx := floor(t.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// glyphShaderSource is the FNT glyph blit (Glyphs): where the glyph atlas marks a
// set bit, it writes the run's colour index; a clear bit is transparent
// (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It reproduces internal/client
// drawText's per-pixel `frame[index] = color` over the glyph's set bits, the run
// colour being one physical index for the whole run [03 §7.1]. Text is keyed, not
// destination-reading: a clear bit leaves the destination untouched.
//
// Source image 0 is the per-font glyph atlas (set bit flagged in green); the run
// colour rides the RED vertex channel as color/255 (constant across the run's
// quads). A clear bit (green 0) returns a transparent fragment, so the
// source-over blend keeps the offscreen byte; a set bit writes the colour index,
// opaque, which source-over overwrites with no blend arithmetic on the index
// (C-G4).
const glyphShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	idx := floor(color.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// newLitBlitShader compiles the shaded glyph blit pass once for the renderer's
// life.
func newLitBlitShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(litBlitShaderSource))
}

// newTintShader compiles the translucent strip blit pass once for the renderer's
// life.
func newTintShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(tintShaderSource))
}

// newShadowShader compiles the feature shadow stencil pass once for the
// renderer's life.
func newShadowShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(shadowShaderSource))
}

// newDestTableShader compiles the shared destination-through-table pass once for
// the renderer's life.
func newDestTableShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(destTableShaderSource))
}

// newGlyphShader compiles the FNT glyph blit pass once for the renderer's life.
func newGlyphShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(glyphShaderSource))
}

// solidShaderSource writes one constant palette index into the indexed offscreen
// (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It backs the destination-independent
// families this unit implements — the solid/outline fills, the Bresenham line,
// and the plain point batch — every one of which is a byte-for-byte write of a
// physical index into the destination [03 §4.3][03 §5.4][03 §5.5].
//
// The index rides the RED vertex channel as index/255. Kage sends the vertex
// color to the shader unconverted for DrawTrianglesShader (four interpolated
// floats), so color.r is that ratio exactly; every vertex of one primitive
// carries the same value, so interpolation is constant. The fragment snaps it
// back to an integer index before re-encoding — floor(r*255 + 0.5), the same
// integer decode the expansion pass uses — so no float can land between two
// entries (C-G4). Alpha is forced opaque so the stored red survives
// premultiplication and decodes back exactly; the caller draws with BlendCopy,
// so the value overwrites the destination rather than blending into it (C-G4).
const solidShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	// Decode the physical index carried in the red vertex channel and re-encode
	// it into the red output channel, opaque (C-G4).
	idx := floor(color.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// atlasShaderSource copies a palette index out of a source atlas's red channel
// into the indexed offscreen's red channel (docs/DESIGN_GPU_RENDERER.md §2.3,
// C-G4). It backs the terrain tile pass: source image 0 is the per-map tile
// atlas whose red channel stores tile indices, sampled nearest (Kage sampling is
// always nearest) so the fetched red is index/255 exactly.
//
// The fragment applies the same integer decode as the expansion pass —
// floor(r*255 + 0.5) — then re-encodes into red, opaque, matching the byte the
// classic tile blitter copies (C-G4). BlendCopy overwrites the destination.
const atlasShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	// Sample the tile atlas's red channel and decode the palette index (C-G4).
	idx := floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// gafKeyedShaderSource copies a palette index out of a GAF/PCX frame image's red
// channel into the indexed offscreen, skipping the frame's transparent texels
// (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It backs every 1:1 keyed blit this
// unit draws — the anchored and plain keyed sprites, the feature GAF copy and the
// software cursor — each of which the classic byte writer executes by copying
// only the frame's opaque pixels and leaving the destination untouched under a
// transparent one [03 R-RAST-01 §6][03 §4.4].
//
// Transparency rides the GREEN channel: the frame image stores index in red,
// 255 in green for an opaque texel and 0 for a transparent one, alpha always
// opaque (so premultiplied sampling recovers red and green exactly). A texel
// whose green is 0 returns a fully transparent fragment; drawn under the
// source-over blend (src*1 + dst*(1-srcAlpha)) that fragment leaves the
// destination byte in place, exactly as the byte writer's key skip does. An
// opaque texel returns vec4(index/255, 0, 0, 1); source-over with source alpha 1
// overwrites the destination with that index and no blend arithmetic touches the
// red channel, so the stored byte is exact (C-G4). Sampling is nearest, so the
// fetched red is index/255 and floor(r*255 + 0.5) recovers the byte.
const gafKeyedShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	// Green carries the frame's opacity flag; a transparent texel writes nothing
	// under the source-over blend (C-G4).
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	idx := floor(tex.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// indexScaledShaderSource samples a source image's red channel across a
// destination rectangle using the SAME integer source mapping the classic byte
// writers use, reproduced in integer arithmetic so no float lands between two
// texels (docs/DESIGN_GPU_RENDERER.md §2.3, C-G4). It backs both the scaled GAF
// blit (uiBlitFrameSourceRectScaledClippedRaw, source span srcW-1 over dst span
// w-1) and the indexed surface blit (uiBlitIndexedRaw, source size srcW over dst
// size w); the caller passes the numerator, denominator, source origin and frame
// size as uniforms so one shader serves both mappings [07 R-HUD-03 §11].
//
// For a fragment at destination pixel (px, py): dx = px - DstOrigin.x,
// sx = dx*SrcNum.x/DstDen.x (integer truncation toward zero, dx >= 0) + SrcOrigin.x,
// and likewise for y; DstDen.x <= 0 forces sx = SrcOrigin.x, matching the byte
// writer's `if span > 0` guard. A source coordinate outside the frame is skipped
// exactly as GAFFrame.At returns absent. Opacity again rides green: a texel past
// the source length (surface) or a transparent GAF texel writes nothing under the
// source-over blend, matching the byte writer's per-pixel skip.
const indexScaledShaderSource = `//kage:unit pixels

package main

var DstOrigin vec2
var SrcNum vec2
var DstDen vec2
var SrcOrigin vec2
var FrameSize vec2

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	dx := int(dstPos.x) - int(DstOrigin.x)
	dy := int(dstPos.y) - int(DstOrigin.y)
	sx := 0
	if int(DstDen.x) > 0 {
		sx = dx * int(SrcNum.x) / int(DstDen.x)
	}
	sy := 0
	if int(DstDen.y) > 0 {
		sy = dy * int(SrcNum.y) / int(DstDen.y)
	}
	sx += int(SrcOrigin.x)
	sy += int(SrcOrigin.y)
	if sx < 0 || sy < 0 || sx >= int(FrameSize.x) || sy >= int(FrameSize.y) {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	tex := imageSrc0At(imageSrc0Origin() + vec2(float(sx)+0.5, float(sy)+0.5))
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	idx := floor(tex.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// fogGrayShaderSource builds the gray-remapped destination layer: for each pixel
// it reads the composed offscreen index (source image 0, index in red) and writes
// GRAY TABLE[index] (source image 1, the 256×1 gray remap table, index in red)
// (docs/DESIGN_GPU_RENDERER.md C-G7, C-G4). It runs once per fog composite over
// the whole surface into a scratch image, reproducing internal/client fogFillGray's
// per-pixel remap `dst = Gray[dst]` [03 §3.3][03 §4.3.3]. Because it reads the
// offscreen and writes a different image, there is no read-after-write hazard, and
// the fog fills and gray fog GAF then sample this layer rather than the live
// offscreen — which is exactly the pre-fog destination each classic byte writer
// reads for a non-overlapping cell (the fog fills are pairwise-disjoint 32×32
// cells) [03 §3.3].
//
// The quad is drawn with SrcX/SrcY equal to the screen coordinates, so src0Pos
// addresses the offscreen texel under the fragment. The gray table is sampled
// with the same imageSrc1AtFromSrc0Pos idiom the expansion pass uses: a position
// built in source-0 space lands on the centre of column `index`, row 0 of the
// table (C-G4).
const fogGrayShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	// Decode the composed index under this pixel (C-G4).
	idx := floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	// Fetch GRAY TABLE[idx] from source image 1 (C-G4, C-G7).
	g := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(idx+0.5, 0.5))
	gi := floor(g.r*255.0 + 0.5)
	return vec4(gi/255.0, 0.0, 0.0, 1.0)
}
`

// fogCheckerShaderSource writes the fog dark index (index 0) on the dithered
// checker and leaves every other pixel untouched (docs/DESIGN_GPU_RENDERER.md
// C-G7). It reproduces internal/client fogFillChecker exactly: screen pixel
// (x, y) is written when (x + y + parity) & 1 == 1, else kept [03 §3.3]
// [R-RR16-A §2]. The parity is (camX + camZ) & 1, passed as a uniform (0 or 1).
//
// The coordinate is made image-local (dstPos.xy - imageDstOrigin()) so the parity
// test runs on screen pixels, matching the absolute screen column the byte writer
// tests. A kept pixel returns a fully transparent fragment, so under the
// source-over blend the offscreen byte is left in place (a "keep"); a written
// pixel returns index 0, opaque, which source-over overwrites with no blend
// arithmetic on the index (C-G4).
const fogCheckerShaderSource = `//kage:unit pixels

package main

var Parity float

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	d := dstPos.xy - imageDstOrigin()
	s := floor(d.x) + floor(d.y) + Parity
	if mod(s, 2.0) < 0.5 {
		// Checker off: keep the destination (C-G4, C-G7).
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	// Checker on: write the fog dark index 0, opaque.
	return vec4(0.0, 0.0, 0.0, 1.0)
}
`

// fogGrayMaskShaderSource is the plain gray-family fog GAF blit: where the fog
// GAF frame is opaque (its non-key mask), it writes the gray-remapped destination
// pixel; elsewhere it keeps the destination (docs/DESIGN_GPU_RENDERER.md C-G7).
// It reproduces internal/client blitFogGAF in fogBlitGray mode: the source art is
// a mask only, and the visible result is GRAY TABLE[dst], never the frame's own
// indices [03 §3.3][R-RR16-A §1].
//
// Source image 0 is the fog GAF frame (index in red, opacity flag in green,
// baked by buildGAFFrameImage); source image 1 is the pre-built gray layer
// (fogGrayShaderSource's output), aligned 1:1 with the offscreen. The frame is
// sampled at srcPos (the quad's SrcX/SrcY are frame texel coordinates); the gray
// layer is sampled at the fragment's image-local destination pixel. A transparent
// or keyed frame texel returns a transparent fragment, leaving the offscreen byte
// in place under the source-over blend (C-G4).
const fogGrayMaskShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	// Green carries the frame's opacity flag; a keyed/transparent texel keeps the
	// destination (C-G4).
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	d := dstPos.xy - imageDstOrigin()
	gs := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(floor(d.x)+0.5, floor(d.y)+0.5))
	idx := floor(gs.r*255.0 + 0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

// fogPatternMaskShaderSource is the dithered gray-family fog GAF blit: where the
// fog GAF frame is opaque AND the pixel is on the checker, it writes the fog dark
// index 0; elsewhere it keeps the destination (docs/DESIGN_GPU_RENDERER.md C-G7).
// It reproduces internal/client blitFogGAF in fogBlitPatterned mode: the same
// (x + y + parity) & 1 == 1 checker the fills use, masked by the frame's opacity
// [03 §3.3]. No destination read is needed — the written value is always index 0.
const fogPatternMaskShaderSource = `//kage:unit pixels

package main

var Parity float

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	tex := imageSrc0At(srcPos)
	if tex.g < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	d := dstPos.xy - imageDstOrigin()
	s := floor(d.x) + floor(d.y) + Parity
	if mod(s, 2.0) < 0.5 {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	return vec4(0.0, 0.0, 0.0, 1.0)
}
`

// newSolidShader compiles the constant-index pass once for the renderer's life.
func newSolidShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(solidShaderSource))
}

// newFogGrayShader compiles the gray-layer build pass once for the renderer's
// life.
func newFogGrayShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fogGrayShaderSource))
}

// newFogCheckerShader compiles the dithered fog fill pass once for the renderer's
// life.
func newFogCheckerShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fogCheckerShaderSource))
}

// newFogGrayMaskShader compiles the plain gray fog GAF pass once for the
// renderer's life.
func newFogGrayMaskShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fogGrayMaskShaderSource))
}

// newFogPatternMaskShader compiles the dithered gray fog GAF pass once for the
// renderer's life.
func newFogPatternMaskShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fogPatternMaskShaderSource))
}

// newAtlasShader compiles the atlas-sampling pass once for the renderer's life.
func newAtlasShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(atlasShaderSource))
}

// newGAFKeyedShader compiles the keyed GAF/PCX blit pass once for the life of the
// renderer.
func newGAFKeyedShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(gafKeyedShaderSource))
}

// newIndexScaledShader compiles the integer-mapped scaled/surface blit pass once
// for the life of the renderer.
func newIndexScaledShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(indexScaledShaderSource))
}
