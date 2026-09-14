package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// Geometry of the compiled fog pass (docs/DESIGN_GPU_RENDERER.md §11.2 "Fog as
// one pass", C-G7).
//
// The pass reads a per-cell grid texture and one fog GAF atlas and computes the
// classic byte writers' result for every pixel of the fog region in one draw.
// Three numbers fix its geometry, and they have to agree with one another:
//
//   - fogCellPixels is the hard 32-pixel fog cell [03 §3.3]. At the detail view
//     scale the cell rectangle is 32·s (§14.2), so the pass takes the scale on a
//     vertex lane and multiplies; every number below scales with it.
//   - fogAtlasNativeTile is the square the atlas reserves for one fog GAF frame
//     at the native scale, measured from the CELL ORIGIN rather than from the
//     frame's own top-left: the shipped frames carry their placement in the
//     signed XOffset/YOffset header words and are drawn at `cell origin -
//     offset`, so a frame can reach past its own 32×32 cell
//     [03 §3.3][R-RR16-A §3][fmt gaf]. Baking the authored offset into the tile
//     is what lets the shader address a frame from the cell index alone, with no
//     per-frame size or offset lookup. At scale s the tile is fogAtlasTile(s),
//     and the frame stored in it is the frame's 2× variant, whose pixels, size
//     and authored offsets are all doubled.
//   - the shader visits the 2×2 block of cells whose origins can reach a pixel.
//     A cell one column to the left contributes at offsets 32·s..64·s−1, and a
//     cell two columns to the left would start at 64·s — so a 2×2 neighbourhood
//     covers offsets 0..64·s−1 exactly, and the tile is 64·s to match it. A frame
//     that needed more would need a wider neighbourhood; the atlas builder counts
//     such a frame as oversized rather than clipping it silently.
//
// Every frame of the retail anims/fog.gaf fits: the largest reach past a cell
// origin is 33 pixels (the 33×19 gray unions and the 21×20 gray corner at
// y-offset 13), and no frame reaches a negative offset. Doubling scales both the
// reach and the tile, so the detail view's variants fit for the same reason.
// TestFogAtlasFitsRetail re-measures that against the installed corpus.
const (
	fogCellPixels      = render.FogTilePixels // 32 [03 §3.3]
	fogAtlasNativeTile = 64
	// One atlas row per family variant, one column per nibble value 1..14.
	fogAtlasCols = 14
	fogVariants  = 4
	fogAtlasRows = 2 * fogVariants // Gray1-4 then Black1-4 [03 §3.3]
	fogSlots     = fogVariants * fogAtlasCols
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
// Sources: image 0 is the pre-fog read copy of the composite (colour, sampled
// 1:1 under the fragment); image 1 is the per-cell grid; image 2 is the fog GAF
// atlas (index in red, opacity flag in green); image 3 is the table atlas, whose
// PAL row resolves the dark index and the black-family frames. The lattice origin,
// the checker parity and the view scale ride the vertex custom attributes, so no
// uniform map is built per frame.
//
// The view scale multiplies the cell edge and the atlas tile (§14.2, §14.5). The
// checker is not scaled: it is a test on the DESTINATION pixel, so it stays one
// pixel wide at any scale on its own, which is why the cell takes the frame's 2×
// variant in one blit rather than tiling the native frame across the cell.
//
// The per-pixel values are the byte writers' operations evaluated in colour
// (§13.3): the gray fills and the gray-family frames desaturate to the luminance
// floor((r+g+b)/3) the GRAY TABLE was built from [03 §4.3.3], the solid and
// checker fills write PAL of the fog dark index, and the black-family frames
// write PAL of their own keyed source index.
//
// Why the pass carries a running colour rather than sampling the copy once per
// operation: the classic writer paints cells in op order and clips a fog GAF
// frame only against the framebuffer, never against its own cell [03 §3.3]. A
// frame that reaches into the next cell therefore lands under a later cell's gray
// remap, and the remap must see it. The shader walks the same 2×2 block of cells
// in the same row-major order the op list is built in, and each operation
// transforms the colour the previous one produced, so the chain is the sequential
// byte writers' chain.
//
// GAF offsets are baked into the atlas relative to the unclipped cell origin
// [R-RR16-A §3]. Clipping limits destination writes; it must not move that
// origin to the screen edge when a camera pan takes the cell offscreen.
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
	palRow       = %[12]d.0
)

// palAt resolves one physical palette index through the table atlas' PAL row.
func palAt(idx float) vec3 {
	return imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb
}

// desaturate is the GRAY TABLE's own construction: the truncated channel average
// as a grey [03 §4.3.3](§13.2 GRAY row).
func desaturate(c vec3) vec3 {
	avg := floor((floor(c.r*255.0+0.5) + floor(c.g*255.0+0.5) + floor(c.b*255.0+0.5)) / 3.0)
	return vec3(avg/255.0, avg/255.0, avg/255.0)
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	// The fragment's screen pixel and the pre-fog colour under it.
	sp := floor(dstPos.xy - imageDstOrigin())
	col := imageSrc0At(srcPos).rgb
	// The record pixel that screen pixel shows. color.r is the free zoom's
	// screen-per-record factor (§16.3); color.gb is the framebuffer translation.
	// The cell arithmetic below is entirely in RECORD pixels, so the atlas tile
	// and the cell edge keep their recorded sizes at any factor.
	k := max(color.r, 0.0)
	p := sp
	if k > 0.0 {
		p = floor((sp - color.gb) / k)
	}
	// The view scale: the cell edge and the atlas tile are both measured in it.
	viewScale := max(custom.w, 1.0)
	cellPix := cellPixels * viewScale
	tilePix := atlasTile * viewScale
	// The byte writers' checker phase: write where (x + y + parity) & 1 == 1.
	// The checker stays a test on the DESTINATION pixel, so it is one screen
	// pixel wide at any factor, exactly as it is at any view scale.
	checker := mod(sp.x+sp.y+custom.z, 2.0)
	// The cell whose unclamped 32·s-pixel rectangle contains this pixel.
	cell := floor((p - custom.xy) / cellPix)
	for j := 0; j < 2; j++ {
		for i := 0; i < 2; i++ {
			c := cell + vec2(float(i)-1.0, float(j)-1.0)
			g := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + c + vec2(0.5, 0.5))
			code1 := floor(g.r*255.0 + 0.5)
			code0 := floor(g.g*255.0 + 0.5)
			if code1 == 0.0 && code0 == 0.0 {
				continue
			}
			raw := custom.xy + c*cellPix
			d := p - raw
			inCell := p.x >= raw.x && p.x < raw.x+cellPix && p.y >= raw.y && p.y < raw.y+cellPix
			inTile := d.x >= 0.0 && d.x < tilePix && d.y >= 0.0 && d.y < tilePix
			// Channel one renders before channel zero.
			if code1 == ch1GrayFill {
				if inCell {
					col = desaturate(col)
				}
			} else if code1 == ch1PatFill {
				if inCell && checker > 0.5 {
					col = palAt(darkIndex)
				}
			} else if code1 >= ch1GrayPlain && inTile {
				slot := code1 - ch1GrayPlain
				dithered := false
				if slot >= slots {
					slot = slot - slots
					dithered = true
				}
				row := floor(slot / atlasCols)
				tileCol := slot - row*atlasCols
				t := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(tileCol*tilePix+d.x+0.5, row*tilePix+d.y+0.5))
				if t.g >= 0.5 {
					if dithered {
						if checker > 0.5 {
							col = palAt(darkIndex)
						}
					} else {
						col = desaturate(col)
					}
				}
			}
			if code0 == ch0Solid {
				if inCell {
					col = palAt(darkIndex)
				}
			} else if code0 >= ch0Black && inTile {
				slot := code0 - ch0Black
				row := floor(slot / atlasCols)
				tileCol := slot - row*atlasCols
				t := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(tileCol*tilePix+d.x+0.5, (blackRow0+row)*tilePix+d.y+0.5))
				if t.g >= 0.5 {
					col = palAt(floor(t.r*255.0 + 0.5))
				}
			}
		}
	}
	return vec4(col, 1.0)
}
`,
	fogCellPixels, fogAtlasNativeTile, fogAtlasCols, fogVariants, fogSlots,
	int(render.FogDarkPaletteIndex),
	fogCh1GrayFill, fogCh1PatFill, fogCh1GrayPlain, fogCh0Solid, fogCh0Black,
	tableRowPAL)

// newFogPassShader compiles the fog composite pass. It is compiled lazily on the
// first Fog command rather than in NewChecked, so the fog unit owns its own
// resource lifetime.
func newFogPassShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(fogPassShaderSource))
}
