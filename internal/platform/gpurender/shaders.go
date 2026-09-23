package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// The modern executor's two compiled scene passes (docs/DESIGN_GPU_RENDERER.md
// §11.2, §13.3): one scene shader for every opaque 2D family and one shader for
// the families that composite against the destination. The fog composite keeps
// its own pass in fog_shaders.go, and the model slot atlas keeps its
// rasterization passes in model_shaders.go.
//
// No per-frame draw carries a uniform. Every parameter — the op selector, the
// constant index, the table row, the integer source mapping, the row family's
// scale — rides the vertex colour and custom lanes, so a whole phase's commands
// share one draw and a steady-state frame builds no uniform map
// (§11.2 "Allocation policy").
//
// # Sources stay indexed, the composite is colour (§13.3)
//
// Every SOURCE-side table lookup is still an exact integer texel fetch (C-G4):
// sampling is nearest in the pixel unit, so a fetched red is index/255 and
// floor(r*255 + 0.5) recovers the byte with no float landing between two
// entries. What changed in §13.3 is the framebuffer: the composite holds RGBA
// colour, so each opaque fragment resolves its final index through the PAL row
// of the table atlas before it is written (C-G8 as amended), and the
// destination-side tables become device blends over that colour rather than a
// second index lookup.
//
// An opaque fragment therefore returns either the resolved colour with alpha 1
// or a fully transparent "skip", and draws under source-over: alpha 1 stores the
// colour, alpha 0 leaves the destination in place — the byte writers' overwrite
// and key-skip pair. The ALP families return the premultiplied half-colour
// fragment their source-over blend needs, and the row families return the scale
// their own blend applies to the destination (§13.3).

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
	// source 3 [03 §2.2][03 §2.5]. Custom0 selects filtered sampling: zero is
	// the nearest fetch every rest step takes, one is the four-tap blend the
	// world transform takes when it is not the identity
	// (docs/DESIGN_GPU_RENDERER.md §16.3 "Sampling").
	sceneOpTerrain = 4
	// sceneOpScaled reproduces the byte writers' integer source mapping for the
	// scaled GAF blit and the indexed surface blit [07 R-HUD-03 §11]. ColorR/G
	// are the x numerator and denominator, ColorB/A the y pair, Custom0/1 the
	// atlas position the mapped source offset is added to.
	sceneOpScaled = 6
	// sceneOpLit folds each opaque source texel through one PALETTE.LHT row
	// before writing it — the source-through-light blit, which reads no
	// destination [03 §4.3.1]. ColorR is the absolute table atlas row.
	sceneOpLit = 7
	// sceneOpCopyColor copies the composite's own colour: the one read copy the
	// fog run takes of the region it is about to rewrite
	// (docs/DESIGN_GPU_RENDERER.md §13.3 "Fog keeps one read copy"). It is the
	// only op whose source is already colour, so it resolves no index.
	sceneOpCopyColor = 8
	// sceneOpModelDirect is the model lane's FALLBACK face fragment
	// (model_direct.go): the flat index in ColorG, or when Custom0 is set the
	// nearest texel of the model texture page bound in source 2 at the
	// interpolated texel position, dropped when it is the composition
	// transparent index 1, resolved through PAL, scaled by the interpolated
	// shade factor in ColorR (§13.2 SHD row) and lit by Custom2; Custom1 is
	// the body's opacity, half for a cloaked subject's ALP half-colour
	// [03 R-COMP-01 §2]. Op 5 is retired.
	sceneOpModelDirect = 9
	// sceneOpModelDirectCommit commits one model lane subject from the lane's
	// 2× colour page bound in source 2: the four texels under the pixel are
	// box-resolved, colour the mean of the covered ones and alpha their share,
	// which is §17's coverage resolve done in the commit (§22).
	sceneOpModelDirectCommit = 10
	// sceneOpTint composites each opaque source texel over what is already there
	// as the ALP table's own arithmetic, floor((src + dst)/2) per channel: the
	// translucent strip blit and the translucent feature body and shadow
	// [03 §4.3.4][03 R-COMP-01 §2][03 R-FX-02 §2][03 §5.3.1]. The fragment is the
	// premultiplied half-colour (PAL[src]/2, 1/2) and source-over adds the
	// destination's other half (§13.3).
	//
	// It lives in THIS shader rather than the destination one although it
	// composites, because its blend is already source-over — the same blend the
	// opaque families draw under, since an opaque fragment is just the alpha-1
	// case of it. Ebitengine merges consecutive draws only on identical
	// destination, sources, shader and blend, so sharing the shader is what lets
	// a tinted sprite and the opaque writes around it be ONE device run, drawn in
	// record order by the device's primitive order — the same guarantee C-G3
	// already relies on for two overlapping opaque writes (§11.2 "The scheduler").
	// Custom0 set carries the smoke light modulation of §23 in the colour lanes.
	sceneOpTint = 11
	// sceneOpUnderwaterCommit is the underwater refraction's model commit
	// (underwater.go, §26.5): the page in source 2, the water mask in source 3.
	sceneOpUnderwaterCommit = 12
)

// The destination shader's op selector, carried in Custom3.
const (
	// destOpTable scales the destination by one row family's factor: the UI light
	// rect, the UI shade rect and the lit point batch
	// [03 §4.3.1][03 R-COMP-02 §5][03 R-FX-01 §4]. The factor k is computed on the
	// CPU from the LHT/SHD builder arithmetic [03 §4.3.4] and split across ColorR
	// (min(k,1)) and ColorG (max(k-1,0)) for the scale blend (§13.3). It is this
	// shader's trailing op, so an unrecognized selector resolves to it. Op 0 is
	// retired: the ALP strip and feature blit it named moved to the scene shader's
	// sceneOpTint, because its blend was never distinct from the opaque one.
	destOpTable = 1
	// destOpTrail scales the destination by the trail mark's darkening times
	// a coverage evaluated from the quad's local coordinates: an oval for a
	// footprint, a soft-sided segment for a track (§15). The colour lanes
	// carry the centre scale split like destOpTable's.
	destOpTrail = 3
	// destOpMarker draws one strategic-view unit marker: a palette index at the
	// layer's fade alpha, composited over what is already there
	// (docs/DESIGN_GPU_RENDERER.md §16.11). ColorR is the physical index and
	// ColorG the alpha; the fragment is the premultiplied (PAL[idx]·a, a) and
	// the source-over blend does the rest.
	destOpMarker = 4
	// destOpLaneAtlas brightens the destination by a per-texel scale read out of
	// source 0: each texel carries the row family's high lane as a 24-bit
	// fixed-point triple, and an uncovered texel is zero, which is the identity
	// scale. The low lane is 1 for every LHT row, so nothing else has to be
	// carried. Two families use it — a whole group of lit points as one quad over
	// the per-frame lit point plane (points.go, §13.8), and one explosion disc as
	// one magnified quad over the persistent disc atlas (flash.go, §13.11) — and
	// they share it precisely so the brightening per row cannot drift between
	// them.
	destOpLaneAtlas = 5
	// destOpHalo scales the destination by one LHT row inside a radius: the flat
	// ground halo of [03 §4.3.1] as one quad. The per-corner custom lanes carry
	// the pixel's offset from the disc centre and the third lane the squared
	// radius, so the fragment recovers the integer (dx, dy) the byte writer
	// tested and runs the same comparison; the colour lanes carry the row's scale
	// split like destOpTable's (§13.11).
	destOpHalo = 6
	// destOpModelDirectShadow commits one model lane shadow from the lane's
	// 2× colour pages, the shadow's bound in source 3 and the body's in
	// source 2: the four silhouette texels under the pixel are resolved by
	// coverage and composited as the ALP half-colour fragment, skipping a
	// pixel whose body block (Custom0/1 texels away, inside the body bounds
	// in Color) is wholly covered — the slot stage's punch on the lane's
	// planes (§22).
	destOpModelDirectShadow = 7
	// destOpModelSilhouetteShadow commits a Digger or mobile shadow from the
	// body's own pages, the colour in source 3 and, when Custom2 carries a
	// clip key, the key page in source 2: the
	// four body texels under the pixel are the silhouette, a texel at or
	// below the clip key in Custom2 is erased, and the fragment is the ALP
	// half-colour of index 0 at that coverage [03 R-REN-03D §1, §4] (§22).
	destOpModelSilhouetteShadow = 8
)

// The composite's blends (docs/DESIGN_GPU_RENDERER.md §13.3 "Blend classes").
// Every factor here comes from §13.2's generating arithmetic; none is tuned.
var (
	// blendComposite writes an opaque fragment or leaves the destination alone.
	// It serves every opaque family and the fog run, whose fragment is opaque
	// too: alpha 1 stores the colour, alpha 0 is the byte writers' key skip.
	blendComposite = ebiten.BlendSourceOver

	// blendHalfSource is the ALP families' blend. The fragment is the
	// premultiplied half-colour (PAL[src]/2, 1/2), so source-over yields
	// PAL[src]/2 + dst/2 — the ALP builder's floor((src + dst)/2) evaluated in
	// RGB [03 §4.3.4](§13.2 ALP row, §13.3).
	blendHalfSource = ebiten.BlendSourceOver

	// blendScaleDestination is the row families' blend: with the fragment
	// carrying (min(k,1)×3, max(k−1,0)) it forms
	//
	//	out.rgb = dst.rgb×src.rgb + dst.rgb×src.a = dst.rgb × k
	//
	// which is the LHT and SHD builders' per-channel product [03 §4.3.4]
	// (§13.2 LHT/SHD rows, §13.3). The alpha factors are zero on the source and
	// one on the destination, so the composite's opaque alpha survives a scale.
	blendScaleDestination = ebiten.Blend{
		BlendFactorSourceRGB:        ebiten.BlendFactorDestinationColor,
		BlendFactorSourceAlpha:      ebiten.BlendFactorZero,
		BlendFactorDestinationRGB:   ebiten.BlendFactorSourceAlpha,
		BlendFactorDestinationAlpha: ebiten.BlendFactorOne,
		BlendOperationRGB:           ebiten.BlendOperationAdd,
		BlendOperationAlpha:         ebiten.BlendOperationAdd,
	}
)

// The row families' scale k, computed on the CPU from the table builders'
// arithmetic [03 §4.3.4] and clamped to the range the blend can express
// (§13.3: "A factor above 2 clamps at 2").
const rowScaleMax = 2.0

// lightScale is the LHT row factor: the builder forms 1.0 − r×(−1/30), so row r
// multiplies each channel by 1 + r/30 [03 §4.3.4](§13.2 LHT row).
func lightScale(row int) float32 {
	return 1 + float32(row)/30
}

// shadeScale is the SHD row factor: the builder starts at zero and adds 0.06875
// per row, so row r multiplies each channel by 0.06875·r — the full signed ramp,
// near-identity at row 15 and brightening above it
// [03 §4.3.2][03 §4.3.4](§13.2 SHD row).
func shadeScale(row int) float32 {
	return 0.06875 * float32(row)
}

// rowScaleLanes splits a scale into the two vertex lanes the scale blend needs:
// the part the destination-colour factor can express and the part the
// source-alpha factor adds on top (§13.3).
func rowScaleLanes(k float32) (low, high float32) {
	if k < 0 {
		k = 0
	}
	if k > rowScaleMax {
		k = rowScaleMax
	}
	if k <= 1 {
		return k, 0
	}
	return 1, k - 1
}

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
` + modelQuadMapperSource + battleLightShaderSource + metalGlintShaderSource + modelFinishShaderSource + underwaterCommitSource + `

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0

// tableAt fetches one table atlas entry: the column is an index, the row is an
// absolute table atlas row, and the result is decoded to an integer index (C-G4).
func tableAt(col, row float) float {
	return floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(col+0.5, row+0.5)).r*255.0+0.5)
}

// palAt resolves one physical palette index to its colour through the PAL row of
// the table atlas. It is the retail composite's own final lookup, applied per
// fragment instead of once per frame (C-G8 as amended, §13.3).
func palAt(idx float) vec3 {
	return imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb
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
		if custom.x >= 0.5 {
			// The world transform is shrinking the terrain, so one screen pixel
			// covers more than one tile texel and a nearest fetch picks an
			// arbitrary one of them: the noise crawls as the factor eases and
			// sparkles between adjacent factors. Blend the four texels around the
			// sample point instead — each resolved through PAL FIRST, because an
			// index is a name and averaging two names is meaningless (C-G4). The
			// taps can reach one texel outside the tile's atlas cell, which is
			// exactly what the cell's border of duplicated edge texels is for
			// (tileAtlasPad).
			q := srcPos - imageSrc0Origin() - vec2(0.5, 0.5)
			b := floor(q)
			f := q - b
			o := imageSrc0Origin() + b + vec2(0.5, 0.5)
			c00 := palAt(floor(imageSrc3AtFromSrc0Pos(o).r*255.0 + 0.5))
			c10 := palAt(floor(imageSrc3AtFromSrc0Pos(o+vec2(1.0, 0.0)).r*255.0 + 0.5))
			c01 := palAt(floor(imageSrc3AtFromSrc0Pos(o+vec2(0.0, 1.0)).r*255.0 + 0.5))
			c11 := palAt(floor(imageSrc3AtFromSrc0Pos(o+vec2(1.0, 1.0)).r*255.0 + 0.5))
			return vec4(mix(mix(c00, c10, f.x), mix(c01, c11, f.x), f.y), 1.0)
		}
		p := imageSrc0Origin() + floor(srcPos-imageSrc0Origin()) + vec2(0.5, 0.5)
		idx = floor(imageSrc3AtFromSrc0Pos(p).r*255.0 + 0.5)
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
	} else if op == ` + fmt.Sprint(sceneOpModelDirect) + ` {
		// The model lane's fallback (model_direct.go): no key test, no
		// supersample. Custom0 is 0 flat, 1 textured; Custom1 the opacity.
		encoded := floor(color.g + 0.5)
		glint := mod(floor(encoded/256.0), 256.0)
		finish := floor(encoded/65536.0)
		idx = mod(encoded, 256.0)
		if custom.x > 0.5 {
			t := floor(srcPos-imageSrc0Origin()) + vec2(0.5, 0.5)
			idx = floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+t).r*255.0 + 0.5)
		}
		if idx == 1.0 {
			return vec4(0.0)
		}
		albedo := palAt(idx)
		return vec4(modelFinish(albedo, metalGlint(albedo, battleLit(albedo, color.r, custom.z), glint), finish), 1.0) * custom.y
	} else if op == ` + fmt.Sprint(sceneOpModelDirectCommit) + ` {
		// The direct lane's commit: srcPos interpolates the 2× atlas texel of
		// the pixel, one texel into its block; floor back to the block and
		// resolve the four texels by coverage (§22).
		rel := srcPos - imageSrc0Origin()
		b := floor(rel/2.0) * 2.0
		sum := vec3(0.0)
		cover := 0.0
		for j := 0; j < 2; j++ {
			for i := 0; i < 2; i++ {
				c := imageSrc2AtFromSrc0Pos(imageSrc0Origin() + b + vec2(float(i)+0.5, float(j)+0.5))
				if c.a > 0.5 {
					sum += c.rgb
					cover += 1.0
				}
			}
		}
		if cover == 0.0 {
			return vec4(0.0)
		}
		// Cooling rides the existing body composite: no extra samples or pass.
		// Screen blend preserves texture contrast and premultiplied coverage.
		sum += (vec3(cover)-sum)*color.rgb
		// Enhanced uses the ALP half-colour formula before source-over,
		// scaling RGB and coverage together [03 R-REN-03D §4].
		opacity := 1.0
		if custom.x > 0.5 {
			opacity = 0.5
		}
		return vec4(sum/4.0, cover/4.0)*opacity
	} else if op == ` + fmt.Sprint(sceneOpUnderwaterCommit) + ` {
		return underwaterCommit(srcPos, color, custom)
	} else if op == ` + fmt.Sprint(sceneOpCopyColor) + ` {
		// The fog run's read copy: the composite is already colour here, so it is
		// copied through unchanged (§13.3).
		return vec4(imageSrc0At(srcPos).rgb, 1.0)
	} else if op == ` + fmt.Sprint(sceneOpTint) + ` {
		// The ALP families' premultiplied half-colour fragment. Source-over adds
		// the destination's own half, which is floor((src + dst)/2) per channel up
		// to the device's rounding [03 §4.3.4](§13.2 ALP row). The key test is the
		// keyed blit's own: a transparent texel is the byte writer's skip.
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		rgb := palAt(floor(tex.r*255.0 + 0.5))
		if custom.x > 0.5 {
			// Smoke receives soft light at unchanged coverage, never additive opacity.
			rgb = min(rgb + (vec3(0.2)+rgb*0.8)*color.rgb, vec3(1.0))
		}
		return vec4(rgb*0.5, 0.5)
	} else {
		tex := imageSrc0At(srcPos)
		if tex.g < 0.5 {
			return vec4(0.0)
		}
		idx = tableAt(floor(tex.r*255.0+0.5), floor(color.r+0.5))
	}
	return vec4(palAt(idx), 1.0)
}
`
}

// sceneDestShaderSource is the one destination-compositing pass. Source 0 is the
// scene atlas page (or, for a shadow commit, the model body page), source 1 the
// table atlas and source 3 the model shadow page. Nothing here samples the
// destination: the remaining ALP families hand the device a premultiplied
// half-colour fragment and the row families hand it a scale, and the blend does
// the arithmetic the tables were generated from [03 §4.3.4](§13.2, §13.3).
//
// The ALP STRIP and feature blit is no longer one of them: its blend is
// source-over, which is the opaque families' blend, so it evaluates in the scene
// shader as sceneOpTint and shares their runs and their stream. What is left
// here either scales the destination — a different blend — or composites a
// source the scene shader has no reason to carry.
//
// Commands in one batch either have disjoint rectangles or share a blend
// stream, and a command that must observe one of another stream is a phase
// later, so the byte writers' record-order chain is preserved: a blend is a
// read-modify-write of the attachment and the device applies one pass's
// fragments in primitive order, which inside a batch is record order
// (§11.2 "The scheduler", §13.11)[03 R-COMP-01 §2].
func sceneDestShaderSource() string {
	return `//kage:unit pixels

package main

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0

func palAt(idx float) vec3 {
	return imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb
}

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	op := int(custom.w + 0.5)
	if op == ` + fmt.Sprint(destOpLaneAtlas) + ` {
		// The lit point plane and the explosion disc atlas. The stored triple is
		// an integer below 2^24, so every term and every partial sum below is
		// exact in binary32 whatever order the driver associates them in, and the
		// 2^-23 scale is exact because it is a power of two: the lane reaches the
		// blend bit-for-bit as the CPU formed it (points.go, flash.go).
		t := imageSrc0At(srcPos)
		n := floor(t.r*255.0+0.5)*65536.0 + floor(t.g*255.0+0.5)*256.0 + floor(t.b*255.0+0.5)
		return vec4(1.0, 1.0, 1.0, n/8388608.0)
	}
	if op == ` + fmt.Sprint(destOpModelDirectShadow) + ` {
		rel := srcPos - imageSrc0Origin()
		b := floor(rel/2.0) * 2.0
		sum := vec3(0.0)
		cover := 0.0
		body := 0.0
		bb := b + custom.xy
		inside := bb.x >= color.r && bb.y >= color.g && bb.x < color.b && bb.y < color.a
		for j := 0; j < 2; j++ {
			for i := 0; i < 2; i++ {
				o := vec2(float(i)+0.5, float(j)+0.5)
				c := imageSrc3AtFromSrc0Pos(imageSrc0Origin() + b + o)
				if c.a > 0.5 {
					sum += c.rgb
					cover += 1.0
				}
				if inside && imageSrc2AtFromSrc0Pos(imageSrc0Origin()+bb+o).a > 0.5 {
					body += 1.0
				}
			}
		}
		if cover == 0.0 || body >= 4.0 {
			return vec4(0.0)
		}
		return vec4(sum/4.0*0.5, cover/4.0*0.5)
	}
	if op == ` + fmt.Sprint(destOpHalo) + ` {
		// The flat ground halo. custom.xy is the fragment's own offset from the
		// disc centre, interpolated over the quad, so flooring it at the pixel
		// centre recovers the integer (dx, dy) the byte writer walked; custom.z is
		// the squared radius it compared against [03 §4.3.1]. Outside the circle
		// the fragment is the identity scale, so one quad covers the whole square.
		d := floor(custom.xy)
		if dot(d, d) > custom.z {
			return vec4(1.0, 1.0, 1.0, 0.0)
		}
		return vec4(color.r, color.r, color.r, color.g)
	}
	if op == ` + fmt.Sprint(destOpMarker) + ` {
		// The strategic marker layer: a flat index at the layer's fade alpha.
		a := clamp(color.g, 0.0, 1.0)
		return vec4(palAt(floor(color.r+0.5))*a, a)
	}
	if op == ` + fmt.Sprint(destOpTrail) + ` {
		// custom.xy is the mark's local position, −1..1 along and across the
		// travel direction; custom.z selects the oval (0) or the segment (1).
		// The coverage fades to nothing at the edge so the mark has no hard
		// outline, and the scale is 1 − (1 − k) × coverage: unchanged terrain
		// outside, the mark's own scale at its centre (§15).
		d := 0.0
		if custom.z < 0.5 {
			d = dot(custom.xy, custom.xy)
		} else {
			d = custom.y * custom.y
		}
		cov := 1.0 - smoothstep(0.6, 1.0, d)
		k := 1.0 - (1.0-color.r)*cov
		return vec4(k, k, k, 0.0)
	}
	if op == ` + fmt.Sprint(destOpModelSilhouetteShadow) + ` {
		rel := srcPos - imageSrc0Origin()
		b := floor(rel/2.0) * 2.0
		cover := 0.0
		for j := 0; j < 2; j++ {
			for i := 0; i < 2; i++ {
				o := vec2(float(i)+0.5, float(j)+0.5)
				if imageSrc3AtFromSrc0Pos(imageSrc0Origin()+b+o).a <= 0.5 {
					continue
				}
				if custom.z > 0.5 {
					key := floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+b+o).r*255.0 + 0.5)
					if key <= custom.z {
						continue
					}
				}
				cover += 1.0
			}
		}
		if cover == 0.0 {
			return vec4(0.0)
		}
		return vec4(palAt(0.0)*cover/4.0*0.5, cover/4.0*0.5)
	}
	// destOpTable, the one op left: the row families carry their scale k, already
	// split by the CPU into the part the colour factor can express and the part
	// the alpha factor adds, so the blend forms
	// dst * (min(k,1) + max(k-1,0)) = dst * k (§13.3).
	return vec4(color.r, color.r, color.r, color.g)
}
`
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
