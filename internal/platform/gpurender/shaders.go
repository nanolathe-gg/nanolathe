package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// The modern executor's three compiled passes (docs/DESIGN_GPU_RENDERER.md
// §11.2): one scene shader for every opaque 2D family, one destination shader
// for every family that reads the destination through a palette table, and the
// final expansion. The fog composite keeps its own pass in fog_shaders.go, and
// the model slot atlas keeps its rasterization passes in model_shaders.go.
//
// No per-frame draw carries a uniform. Every parameter — the op selector, the
// constant index, the table row, the integer source mapping — rides the vertex
// colour and custom lanes, so a whole phase's commands share one draw and a
// steady-state frame builds no uniform map (§11.2 "Allocation policy").
//
// Index arithmetic is exact (C-G4). Sampling is nearest in the pixel unit, so a
// fetched red is index/255 and floor(r*255 + 0.5) recovers the byte with no float
// landing between two entries. Every fragment returns either an opaque index or a
// fully transparent "skip", and both shaders draw under source-over: alpha 1
// stores the index unchanged, alpha 0 leaves the destination byte in place. That
// is exactly the byte writers' overwrite and key-skip pair, with no blend
// arithmetic on the index.

// The scene shader's op selector, carried in Custom3. Each op names the byte
// writer family it reproduces.
const (
	// sceneOpSolid writes one constant physical index: the solid and inclusive
	// fills, the inclusive frame, the Bresenham line and the plain point batch
	// [03 §4.3][03 §5.4][03 §5.5]. ColorR is the index.
	sceneOpSolid = 0
	// sceneOpKeyed copies an index out of the scene atlas, skipping the source's
	// transparent texels: the keyed GAF sprite (anchored and plain), the opaque
	// feature normal/shadow copy and the software cursor
	// [03 R-RAST-01 §6][03 §4.4][07 §8].
	sceneOpKeyed = 1
	// sceneOpGlyph writes ColorR where the FNT glyph strip marks a set bit and
	// leaves the destination elsewhere [03 §7.1].
	sceneOpGlyph = 2
	// sceneOpCopy copies an index out of the scene atlas unconditionally: the
	// opaque PCX frontend background [fmt pcx].
	sceneOpCopy = 3
	// sceneOpTerrain copies an index out of the per-map tile atlas bound in
	// source 3 [03 §2.2][03 §2.5].
	sceneOpTerrain = 4
	// sceneOpModelCommit copies an index out of the model slot atlas bound in
	// source 2, skipping the composition background index 1 — the keyed model
	// body commit [03 R-REN-03A §5].
	sceneOpModelCommit = 5
	// sceneOpScaled reproduces the byte writers' integer source mapping for the
	// scaled GAF blit and the indexed surface blit [07 R-HUD-03 §11]. ColorR/G
	// are the x numerator and denominator, ColorB/A the y pair, Custom0/1 the
	// atlas position the mapped source offset is added to.
	sceneOpScaled = 6
	// sceneOpLit folds each opaque source texel through one PALETTE.LHT row
	// before writing it — the source-through-light blit, which reads no
	// destination [03 §4.3.1]. ColorR is the absolute table atlas row.
	sceneOpLit = 7
)

// The destination shader's op selector, carried in Custom3.
const (
	// destOpTint resolves each opaque source texel against the destination as
	// ALP[src*256 + dst]: the translucent strip blit and the translucent feature
	// body and shadow [03 R-COMP-01 §2][03 R-FX-02 §2][03 §5.3.1].
	destOpTint = 0
	// destOpTable rewrites the destination pixel as TABLE[row][dst]: the UI light
	// rect, the UI shade rect and the lit point batch
	// [03 §4.3.1][03 R-COMP-02 §5][03 R-FX-01 §4]. ColorR is the absolute table
	// atlas row.
	destOpTable = 1
	// destOpShadowCommit blends a model silhouette through ALP over the snapshot
	// after punching the body's coverage, so overlapping silhouette faces darken
	// the ground once [03 R-REN-03D §4–§5][03 R-RAST-01 §4]. Custom0/1 is the
	// body-page offset of the shadow texel; Color carries the body slot's page
	// bounds.
	destOpShadowCommit = 2
)

// scene2DShaderSource is the one opaque pass. Source 0 is the scene atlas page
// (index in red, opacity flag in green), source 1 the table atlas, source 2 the
// model slot page, source 3 the terrain tile atlas. An op samples only the slots
// its family needs; unused slots are bound to the table atlas so no sampler is
// ever unbound.
//
// Source coordinates are the sampled image's own pixel coordinates: SrcX/SrcY
// reach the fragment as source 0's texture position, so subtracting
// imageSrc0Origin() recovers the local coordinate whichever slot the op reads,
// and imageSrcNAtFromSrc0Pos rebases it onto slot N.
func scene2DShaderSource() string {
	return `//kage:unit pixels

package main

// tableAt fetches one table atlas entry: the column is an index, the row is an
// absolute table atlas row, and the result is decoded to an integer index (C-G4).
func tableAt(col, row float) float {
	return floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(col+0.5, row+0.5)).r*255.0+0.5)
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	op := int(custom.w + 0.5)
	idx := 0.0
	if op == ` + fmt.Sprint(sceneOpSolid) + ` {
		idx = floor(color.r + 0.5)
	} else if op == ` + fmt.Sprint(sceneOpKeyed) + ` {
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		idx = floor(tex.r*255.0 + 0.5)
	} else if op == ` + fmt.Sprint(sceneOpGlyph) + ` {
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		idx = floor(color.r + 0.5)
	} else if op == ` + fmt.Sprint(sceneOpCopy) + ` {
		idx = floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	} else if op == ` + fmt.Sprint(sceneOpTerrain) + ` {
		p := imageSrc0Origin() + floor(srcPos-imageSrc0Origin()) + vec2(0.5, 0.5)
		idx = floor(imageSrc3AtFromSrc0Pos(p).r*255.0 + 0.5)
	} else if op == ` + fmt.Sprint(sceneOpModelCommit) + ` {
		p := imageSrc0Origin() + floor(srcPos-imageSrc0Origin()) + vec2(0.5, 0.5)
		idx = floor(imageSrc2AtFromSrc0Pos(p).r*255.0 + 0.5)
		if idx == 1.0 {
			return vec4(0.0)
		}
	} else if op == ` + fmt.Sprint(sceneOpScaled) + ` {
		// The destination-relative offset rides SrcX/SrcY, so flooring the
		// interpolated position recovers the byte writer's dx and dy exactly.
		d := floor(srcPos - imageSrc0Origin())
		sx := 0
		if int(color.g+0.5) > 0 {
			sx = int(d.x) * int(color.r+0.5) / int(color.g+0.5)
		}
		sy := 0
		if int(color.a+0.5) > 0 {
			sy = int(d.y) * int(color.b+0.5) / int(color.a+0.5)
		}
		tex := imageSrc0At(imageSrc0Origin() + custom.xy + vec2(float(sx)+0.5, float(sy)+0.5))
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		idx = floor(tex.r*255.0 + 0.5)
	} else {
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		idx = tableAt(floor(tex.r*255.0+0.5), floor(color.r+0.5))
	}
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`
}

// sceneDestShaderSource is the one destination-reading pass. Source 0 is the
// scene atlas page (or, for a shadow commit, the model body page), source 1 the
// table atlas, source 2 the phase snapshot, source 3 the model shadow page.
//
// The snapshot is the offscreen copied before this phase's destination batch
// runs, aligned 1:1 with it, so a destination-reading write never samples a pixel
// this batch already changed. Commands in one batch have pairwise disjoint
// rectangles by the scheduler's rule, so the byte writers' record-order chain is
// preserved (§11.2 "The scheduler")[03 R-COMP-01 §2].
func sceneDestShaderSource() string {
	return `//kage:unit pixels

package main

const alpBase = ` + fmt.Sprint(tableRowALP) + `.0

func tableAt(col, row float) float {
	return floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(col+0.5, row+0.5)).r*255.0+0.5)
}

// snapAt reads the phase snapshot under this fragment's screen pixel.
func snapAt(dstPos vec4) float {
	p := imageSrc0Origin() + floor(dstPos.xy-imageDstOrigin()) + vec2(0.5, 0.5)
	return floor(imageSrc2AtFromSrc0Pos(p).r*255.0+0.5)
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	op := int(custom.w + 0.5)
	idx := 0.0
	if op == ` + fmt.Sprint(destOpTint) + ` {
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		idx = tableAt(snapAt(dstPos), alpBase+floor(tex.r*255.0+0.5))
	} else if op == ` + fmt.Sprint(destOpTable) + ` {
		idx = tableAt(snapAt(dstPos), floor(color.r+0.5))
	} else {
		local := floor(srcPos - imageSrc0Origin())
		shadow := floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+local+vec2(0.5, 0.5)).r*255.0+0.5)
		// The body plane is punched at the body slot's own page coordinates; a
		// shadow texel outside that slot is uncovered, so it keeps the
		// composition background value 1.
		bp := local + custom.xy
		body := 1.0
		if bp.x >= floor(color.r+0.5) && bp.y >= floor(color.g+0.5) && bp.x < floor(color.b+0.5) && bp.y < floor(color.a+0.5) {
			body = floor(imageSrc0At(imageSrc0Origin()+bp+vec2(0.5, 0.5)).r*255.0+0.5)
		}
		if shadow == 1.0 || body != 1.0 {
			return vec4(0.0)
		}
		idx = tableAt(snapAt(dstPos), alpBase+shadow)
	}
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`
}

// expandShaderSource is the index→RGBA expansion pass, the final and only colour
// pass of the modern composite (C-G8). Source 0 is the indexed offscreen, whose
// red channel carries the palette index; source 1 is the table atlas, whose PAL
// row carries each index's colour. Alpha is forced opaque, exactly as the
// software convertIndexedToRGBA does.
func expandShaderSource() string {
	return `//kage:unit pixels

package main

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	idx := floor(imageSrc0At(srcPos).r*255.0 + 0.5)
	col := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + vec2(idx+0.5, palRow+0.5))
	return vec4(col.rgb, 1.0)
}
`
}

// newExpandShader compiles the expansion pass once for the renderer's lifetime.
func newExpandShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(expandShaderSource()))
}

// newScene2DShader compiles the opaque scene pass once for the renderer's life.
func newScene2DShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(scene2DShaderSource()))
}

// newSceneDestShader compiles the destination-reading pass once for the
// renderer's life.
func newSceneDestShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(sceneDestShaderSource()))
}
