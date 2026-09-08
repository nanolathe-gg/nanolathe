package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe/nanolathe/internal/render"
)

// Geometry of the compiled fog pass (docs/DESIGN_GPU_RENDERER.md §11.2 "Fog as
// one pass", C-G7).
//
// The pass reads a per-cell grid texture and one fog GAF atlas and computes the
// classic byte writers' result for every pixel of the fog region in one draw.
// Three numbers fix its geometry, and they have to agree with one another:
//
//   - fogCellPixels is the hard 32-pixel fog cell [03 §3.3].
//   - fogAtlasTile is the square the atlas reserves for one fog GAF frame,
//     measured from the CELL ORIGIN rather than from the frame's own top-left:
//     the shipped frames carry their placement in the signed XOffset/YOffset
//     header words and are drawn at `cell origin - offset`, so a frame can
//     reach past its own 32×32 cell [03 §3.3][R-RR16-A §3][fmt gaf]. Baking the
//     authored offset into the tile is what lets the shader address a frame
//     from the cell index alone, with no per-frame size or offset lookup.
//   - the shader visits the 2×2 block of cells whose origins can reach a pixel.
//     A cell one column to the left contributes at offsets 32..63, and a cell
//     two columns to the left would start at 64 — so a 2×2 neighbourhood covers
//     offsets 0..63 exactly, and fogAtlasTile is 64 to match it. A frame that
//     needed more would need a wider neighbourhood; the atlas builder counts
//     such a frame as oversized rather than clipping it silently.
//
// Every frame of the retail anims/fog.gaf fits: the largest reach past a cell
// origin is 33 pixels (the 33×19 gray unions and the 21×20 gray corner at
// y-offset 13), and no frame reaches a negative offset. TestFogAtlasFitsRetail
// re-measures that against the installed corpus.
const (
	fogCellPixels = render.FogTilePixels // 32 [03 §3.3]
	fogAtlasTile  = 64
	// One atlas row per family variant, one column per nibble value 1..14.
	fogAtlasCols = 14
	fogVariants  = 4
	fogAtlasRows = 2 * fogVariants // Gray1-4 then Black1-4 [03 §3.3]
	fogSlots     = fogVariants * fogAtlasCols
	fogAtlasW    = fogAtlasCols * fogAtlasTile
	fogAtlasH    = fogAtlasRows * fogAtlasTile
)

// The per-cell grid texel encoding. Red carries the channel-one operation and
// green the channel-zero operation, because channel one renders BEFORE channel
// zero [03 §3.3]. A cell has at most one operation per channel, so two bytes
// describe it completely, and "no operation" is zero — the value a visible cell
// and a cell outside the grid both decode to.
//
// The GAF codes carry their atlas slot in the code itself (slot = variant*14 +
// frame), which keeps the whole encoding inside two bytes and needs no second
// lookup texture. The largest encoded value is fogCh1GrayDith+fogSlots-1 = 114,
// well inside a byte.
const (
	fogCh1None      = 0
	fogCh1GrayFill  = 1                          // hi==15: dst = Gray[dst] over the cell
	fogCh1PatFill   = 2                          // hi==15 dithered: dark index on the checker
	fogCh1GrayPlain = 3                          // + slot: gray-family GAF, masked Gray[dst]
	fogCh1GrayDith  = fogCh1GrayPlain + fogSlots // + slot: gray-family GAF, masked checker
)

const (
	fogCh0None  = 0
	fogCh0Solid = 1 // lo==15: fill the cell with the dark index
	fogCh0Black = 2 // + slot: black-family GAF, a keyed copy of the frame
)

// fogPassShaderSource is the single fog composite pass (C-G7,
// docs/DESIGN_GPU_RENDERER.md §11.2). One draw over the fog region replaces one
// draw per fog cell, and its per-pixel result is the classic byte writers'
// [03 §3.3][R-RR16-A §1][R-RR16-A §2][R-RR16-A §8].
//
// Sources: image 0 is the pre-fog snapshot of the indexed offscreen (index in
// red, sampled 1:1 under the fragment); image 1 is the per-cell grid; image 2
// is the fog GAF atlas (index in red, opacity flag in green); image 3 is the
// GRAY TABLE (256×1, index in red). The lattice origin and the checker parity
// ride the vertex custom attributes, so no uniform map is built per frame.
//
// Why the pass carries a running index rather than sampling the destination
// once per operation: the classic writer paints cells in op order and clips a
// fog GAF frame only against the framebuffer, never against its own cell
// [03 §3.3]. A frame that reaches into the next cell therefore lands under a
// later cell's gray remap, and the remap must see it. The shader walks the same
// 2×2 block of cells in the same row-major order the op list is built in, and
// each operation transforms the index the previous one produced, so the chain
// is the sequential byte writers' chain.
//
// The anchor arithmetic reproduces one further detail of the composer: it
// clamps a cell's rectangle to the framebuffer BEFORE handing the origin to the
// fog GAF blit, so a cell that hangs off the left or top edge draws its frame
// from the clamped origin. `anchor` is that clamped origin; `raw` is the
// unclamped one the 32-pixel fills are measured from.
var fogPassShaderSource = fmt.Sprintf(`//kage:unit pixels

package main

const (
	cellPixels   = %[1]d.0
	atlasTile    = %[2]d.0
	atlasCols    = %[3]d.0
	blackRow0    = %[4]d.0
	slots        = %[5]d.0
	darkIndex    = %[6]d.0
	ch1GrayFill  = %[7]d.0
	ch1PatFill   = %[8]d.0
	ch1GrayPlain = %[9]d.0
	ch0Solid     = %[10]d.0
	ch0Black     = %[11]d.0
)

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	// The fragment's screen pixel and the pre-fog index under it.
	p := floor(dstPos.xy - imageDstOrigin())
	idx := floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	// The byte writers' checker phase: write where (x + y + parity) & 1 == 1.
	checker := mod(p.x+p.y+custom.z, 2.0)
	// The cell whose unclamped 32-pixel rectangle contains this pixel.
	cell := floor((p - custom.xy) / cellPixels)
	for j := 0; j < 2; j++ {
		for i := 0; i < 2; i++ {
			c := cell + vec2(float(i)-1.0, float(j)-1.0)
			g := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + c + vec2(0.5, 0.5))
			code1 := floor(g.r*255.0 + 0.5)
			code0 := floor(g.g*255.0 + 0.5)
			if code1 == 0.0 && code0 == 0.0 {
				continue
			}
			raw := custom.xy + c*cellPixels
			anchor := max(raw, vec2(0.0, 0.0))
			d := p - anchor
			inCell := p.x >= raw.x && p.x < raw.x+cellPixels && p.y >= raw.y && p.y < raw.y+cellPixels
			inTile := d.x >= 0.0 && d.x < atlasTile && d.y >= 0.0 && d.y < atlasTile
			// Channel one renders before channel zero.
			if code1 == ch1GrayFill {
				if inCell {
					idx = floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, 0.5)).r*255.0 + 0.5)
				}
			} else if code1 == ch1PatFill {
				if inCell && checker > 0.5 {
					idx = darkIndex
				}
			} else if code1 >= ch1GrayPlain && inTile {
				slot := code1 - ch1GrayPlain
				dithered := false
				if slot >= slots {
					slot = slot - slots
					dithered = true
				}
				row := floor(slot / atlasCols)
				col := slot - row*atlasCols
				t := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(col*atlasTile+d.x+0.5, row*atlasTile+d.y+0.5))
				if t.g >= 0.5 {
					if dithered {
						if checker > 0.5 {
							idx = darkIndex
						}
					} else {
						idx = floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, 0.5)).r*255.0 + 0.5)
					}
				}
			}
			if code0 == ch0Solid {
				if inCell {
					idx = darkIndex
				}
			} else if code0 >= ch0Black && inTile {
				slot := code0 - ch0Black
				row := floor(slot / atlasCols)
				col := slot - row*atlasCols
				t := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(col*atlasTile+d.x+0.5, (blackRow0+row)*atlasTile+d.y+0.5))
				if t.g >= 0.5 {
					idx = floor(t.r*255.0 + 0.5)
				}
			}
		}
	}
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`,
	fogCellPixels, fogAtlasTile, fogAtlasCols, fogVariants, fogSlots,
	int(render.FogDarkPaletteIndex),
	fogCh1GrayFill, fogCh1PatFill, fogCh1GrayPlain, fogCh0Solid, fogCh0Black)

// newFogPassShader compiles the fog composite pass. It is compiled lazily on the
// first Fog command rather than in NewChecked, so the fog unit owns its own
// resource lifetime.
func newFogPassShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fogPassShaderSource))
}
