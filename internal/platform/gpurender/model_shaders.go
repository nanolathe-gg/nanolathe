package gpurender

import (
	"fmt"

	"github.com/hajimehoshi/ebiten/v2"
)

// The model passes deliberately retain the source key as a varying until the
// fragment.  Reducing already-wrapped vertex bytes is observably different at
// a signed interpolation crossing [03 R-REN-03A §2][03 R-RAST-01 §1].
//
// Every model shader takes its per-subject parameters from vertex lanes rather
// than uniforms, so one draw can carry every subject of one slot atlas page
// [DESIGN_GPU_RENDERER.md §11.2].
// modelQuadMapperSource is shared by the key and colour passes. A textured quad
// draws as two device triangles and recovers every lane here, so both passes
// narrow the same key at the same fragment and a mapped face is never rejected
// by a key its own key pass did not write
// (docs/DESIGN_GPU_RENDERER.md §11.2 "Textured quads without strips").
//
// Source 3 is the frame's quad parameter image: twelve RGBA8 texels per quad
// holding two 16-bit values each — four corner positions, four corner texel
// coordinates, then four corner key/shade pairs with the bottom corner's
// rotated index in the first pair's spare byte.
const modelQuadMapperSource = `
func modelQuadTexel(n float) vec4 {
	w := imageSrc3Size().x
	y := floor(n/w)
	return imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(n-y*w+0.5, y+0.5))
}

func modelQuadU16(hi float, lo float) float {
	return floor(hi*255.0+0.5)*256.0 + floor(lo*255.0+0.5)
}

// modelQuadEdgeX is the span writer's fixed-point edge setup: one signed 16.16
// slope truncated toward zero and the +65535 bias, so the row's first covered
// column is the ceiling of the edge position [03 R-RAST-01 §1]. Every operand
// is a whole page pixel, and the running term is bounded by the edge's own
// horizontal extent, so the walk stays inside a signed 32-bit integer.
func modelQuadEdgeX(a vec2, b vec2, row float) float {
	dy := int(b.y) - int(a.y)
	if dy <= 0 {
		return a.x
	}
	step := ((int(b.x) - int(a.x)) * 65536) / dy
	x := int(a.x)*65536 + 65535 + step*(int(row)-int(a.y))
	return float(x / 65536)
}

// modelQuadLanes evaluates the two-chain mapping of [03 R-RAST-01 §1] at one
// fragment: the edge the decreasing-index chain and the increasing-index chain
// are on at the fragment's row, each lane interpolated along its edge by
// (y-yA)/(yB-yA), then interpolated across the row by (x-L)/(R-L). Corners
// arrive rotated so index 0 holds the minimum Y and index mm the maximum, which
// makes the two chains index ranges rather than a search. Rows are half-open,
// and a row a folded chain crosses twice keeps the later edge, exactly as the
// edge tables do. It returns (u, v, key, shade).
func modelQuadLanes(q float, d vec2) vec4 {
	base := (q-1.0)*12.0
	var pos [4]vec2
	var uv [4]vec2
	var ks [4]vec2
	mm := 0
	for i := 0; i < 4; i++ {
		a := modelQuadTexel(base+float(i))
		pos[i] = vec2(modelQuadU16(a.r, a.g), modelQuadU16(a.b, a.a))
		b := modelQuadTexel(base+4.0+float(i))
		uv[i] = vec2(modelQuadU16(b.r, b.g), modelQuadU16(b.b, b.a))
		c := modelQuadTexel(base+8.0+float(i))
		ks[i] = vec2(modelQuadU16(c.r, c.g)-32768.0, floor(c.b*255.0+0.5))
		if i == 0 {
			mm = int(floor(c.a*255.0+0.5))
		}
	}
	row := floor(d.y)
	col := floor(d.x)
	lx := pos[0].x
	rx := pos[0].x
	ll := vec4(uv[0].x, uv[0].y, ks[0].x, ks[0].y)
	rl := ll
	for k := 0; k < 3; k++ {
		// The left chain steps from the top corner towards decreasing indices
		// until it reaches the bottom corner; the right chain steps the other
		// way. Later matches overwrite earlier ones, which is the edge table's
		// "a folded chain's later edge wins" rule.
		ls := 4 - k
		if ls == 4 {
			ls = 0
		}
		le := 3 - k
		if le >= mm && pos[le].y > pos[ls].y && row >= pos[ls].y && row < pos[le].y {
			t := (row-pos[ls].y) / (pos[le].y-pos[ls].y)
			a := vec4(uv[ls].x, uv[ls].y, ks[ls].x, ks[ls].y)
			b := vec4(uv[le].x, uv[le].y, ks[le].x, ks[le].y)
			ll = a + (b-a)*t
			lx = modelQuadEdgeX(pos[ls], pos[le], row)
		}
		re := k + 1
		if k < mm && pos[re].y > pos[k].y && row >= pos[k].y && row < pos[re].y {
			t := (row-pos[k].y) / (pos[re].y-pos[k].y)
			a := vec4(uv[k].x, uv[k].y, ks[k].x, ks[k].y)
			b := vec4(uv[re].x, uv[re].y, ks[re].x, ks[re].y)
			rl = a + (b-a)*t
			rx = modelQuadEdgeX(pos[k], pos[re], row)
		}
	}
	t := 0.0
	if rx > lx {
		// The span writer's first pixel takes the left lane exactly and its
		// last is one step short of the right one, so a fragment the device
		// covers just outside the ceiling span clamps rather than extrapolating.
		t = clamp((col-lx)/(rx-lx), 0.0, 1.0)
	}
	return ll + (rl-ll)*t
}
`

const modelKeyShaderSource = `//kage:unit pixels

package main
` + modelQuadMapperSource + `
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	key := color.r
	if custom.z != 0.0 && color.b > 0.5 {
		key = modelQuadLanes(color.b, floor(dstPos.xy-imageDstOrigin())).z
	}
	key = floor(key)
	key = key - floor(key/256.0)*256.0
	return vec4(key/255.0, 0.0, 0.0, 1.0)
}
`

// Source 0 is the slot atlas key page, source 1 is SHD and source 2 is the
// model texture page.  The destination is the same page's composed plane, so
// the fragment reads the key stored at its own texel: index in red, that key in
// green, and body coverage in blue for the reveal pass that may follow.
//
// Texture transparency is intentionally ignored: keyed texels own a composition
// pixel and the composition boundary applies transparency later
// [03 R-REN-03A §5].  color.a selects the key test, so a keyless subject stays
// in painter order [03 R-REN-03A §2].
const modelBodyShaderSource = `//kage:unit pixels

package main
` + modelQuadMapperSource + `
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	d := floor(dstPos.xy - imageDstOrigin())
	key := color.r
	uvLane := custom.xy
	shadeRow := color.g
	// A textured quad carries its one-based parameter index on the dead flat
	// colour lane; every other face keeps its lanes on its own vertices.
	if custom.z != 0.0 && color.b > 0.5 {
		lanes := modelQuadLanes(color.b, d)
		uvLane = lanes.xy
		key = lanes.z
		shadeRow = lanes.w
	}
	key = floor(key)
	key = key - floor(key/256.0)*256.0
	stored := floor(imageSrc0At(imageSrc0Origin()+d+vec2(0.5, 0.5)).r*255.0+0.5)
	if color.a > 0.5 && key != stored {
		return vec4(0.0, 0.0, 0.0, 0.0)
	}
	idx := floor(color.b)
	if custom.z != 0.0 {
		// The span mapper narrows U/V with an arithmetic 16.16 shift before
		// loading a texel [03 R-RAST-01 §1]. Keep the +0.5 source-image
		// texel-centre address, but floor the interpolated lane first; feeding a
		// fraction directly to nearest sampling would round it instead.
		// The centre-sample endpoint correction can leave an intended integer
		// lane infinitesimally below that integer in backend float arithmetic.
		// A 2^-16 texel guard is GPU implementation policy: it corrects observed
		// endpoint residue and may bias values this close to a texel boundary.
		// It is one 16.16 unit, not an additional retail constant.
		uv := floor(uvLane + vec2(1.0/65536.0))
		// A positive lane packs the texture's atlas slot dimensions; a negative
		// one is a frame too large to pack, drawn from its own image instead.
		size := imageSrc2Size()
		offset := vec2(0.0, 0.0)
		if custom.z > 0.0 {
			size = vec2(floor(custom.z/4096.0), custom.z-floor(custom.z/4096.0)*4096.0)
			offset = floor(srcPos-imageSrc0Origin()+vec2(0.5, 0.5))
		}
		idx = 0.0
		if uv.x >= 0.0 && uv.y >= 0.0 && uv.x < size.x && uv.y < size.y {
			idx = floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+offset+uv+vec2(0.5, 0.5)).r*255.0+0.5)
		}
	}
	if custom.w > 0.5 {
		row := floor(shadeRow)
		idx = floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, row+0.5)).r*255.0+0.5)
	}
	return vec4(idx/255.0, stored/255.0, 1.0, 1.0)
}
`

// The nanoframe reveal runs over the finished body plane of one slot, before
// the outline pass, exactly where the per-subject colour pass used to apply it.
// The stored key is the byte that pass compared, so the verdict is the one it
// would have reached; a subject under construction always owns a key plane
// [03 R-REN-03A §2]. Blue is the body pass's coverage flag, so an untouched
// composition background keeps its transparent index. Verdict -2 erases to that
// background and -1 keeps the material index [03 §5.2][03 R-COMP-01 §3].
const modelRevealShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	src := imageSrc0At(srcPos)
	idx := floor(src.r*255.0+0.5)
	key := floor(src.g*255.0+0.5)
	if src.b > 0.5 {
		verdict := floor(custom.y+0.5)
		if key < floor(color.g+0.5) {
			verdict = floor(custom.x+0.5)
		} else if key >= floor(color.r+0.5) {
			verdict = floor(custom.z+0.5)
		}
		if verdict == -2.0 {
			idx = 1.0
		} else if verdict != -1.0 {
			idx = verdict
		}
	}
	return vec4(idx/255.0, key/255.0, 0.0, 1.0)
}
`

// modelCopy returns one processed slot region to its page. Nearest sampling at
// matched rectangles copies the stored bytes exactly (C-G4).
const modelCopyShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	return imageSrc0At(srcPos)
}
`

func newModelKeyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelKeyShaderSource))
}
func newModelBodyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelBodyShaderSource))
}
func newModelRevealShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelRevealShaderSource))
}
func newModelCopyShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelCopyShaderSource))
}

// TODO(question): The researched model blit keys composition index 1, while
// the current CPU modelTarget coverage path can retain a covered index 1.
// Resolve this mismatch by reconciling the classic coverage writer with the
// researched keyed blit before treating this comparison exception as parity.
const modelCommitShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	idx := floor(imageSrc0At(srcPos).r*255.0+0.5)
	if idx == 1.0 { return vec4(0.0, 0.0, 0.0, 0.0) }
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}`

func newModelCommitShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelCommitShaderSource))
}

// The shadow is one completed silhouette. Read a separate destination snapshot
// and punch at the body's framebuffer coordinates before the ALP lookup
// [03 R-REN-03D §4–§5][03 R-RAST-01 §4]. custom.xy is the shadow slot's
// framebuffer offset from the body slot's.
const modelShadowCommitShaderSource = `//kage:unit pixels

package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	shadow := floor(imageSrc0At(srcPos).r*255.0+0.5)
	p := srcPos-imageSrc0Origin()+custom.xy
	body := 1.0
	if p.x >= 0.0 && p.y >= 0.0 && p.x < imageSrc1Size().x && p.y < imageSrc1Size().y {
		body = floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+p).r*255.0+0.5)
	}
	if shadow == 1.0 || body != 1.0 { return vec4(0.0) }
	dest := floor(imageSrc2AtFromSrc0Pos(imageSrc0Origin()+floor(dstPos.xy-imageDstOrigin())+vec2(0.5, 0.5)).r*255.0+0.5)
	idx := floor(imageSrc3AtFromSrc0Pos(imageSrc0Origin()+vec2(dest+0.5, shadow+0.5)).r*255.0+0.5)
	return vec4(idx/255.0, 0.0, 0.0, 1.0)
}
`

func newModelShadowCommitShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelShadowCommitShaderSource))
}

// Waterline and Digger processing follow reveal/outline and preserve the key
// plane. Erasure writes the composition background, never the framebuffer
// [03 R-WATER-01 §2][03 R-REN-03A §8]. Source 0 is the slot page (index in red,
// stored key in green) and source 1 is BLUE; the thresholds ride the vertex.
const modelClipShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	src := imageSrc0At(srcPos)
	idx := floor(src.r*255.0+0.5)
	key := floor(src.g*255.0+0.5)
	mode := floor(color.r+0.5)
	if idx != 1.0 && key <= floor(color.g+0.5) {
		if mode == 1.0 { idx = 1.0 }
		if mode == 2.0 { idx = floor(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, 0.5)).r*255.0+0.5) }
	}
	if color.b > 0.5 && key <= floor(color.a+0.5) { idx = 1.0 }
	return vec4(idx/255.0, key/255.0, 0.0, 1.0)
}
`

func newModelClipShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelClipShaderSource))
}

// Source 0 is the finished child slot (index in red, stored key in green) and
// source 1 the staging image the previous merge produced, aligned with this
// draw's destination. custom.x is the signed height delta, compared at signed
// width before the byte is stored [03 R-REN-03A §4].
const modelChildShaderSource = `//kage:unit pixels
package main
func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	q := floor(dstPos.xy-imageDstOrigin())+vec2(0.5, 0.5)
	prior := imageSrc1AtFromSrc0Pos(imageSrc0Origin()+q)
	src := imageSrc0At(srcPos)
	idx := floor(src.r*255.0+0.5)
	key := floor(src.g*255.0+0.5)+floor(custom.x+0.5)
	if idx == 1.0 || floor(prior.g*255.0+0.5) > key { return prior }
	stored := key-floor(key/256.0)*256.0
	return vec4(idx/255.0, stored/255.0, 0.0, 1.0)
}
`

func newModelChildShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelChildShaderSource))
}

// The coverage resolve of DESIGN_GPU_RENDERER §17: the one stage where a
// subject's index raster becomes colour. Source 0 is the page's finished
// indexed plane and source 1 the table atlas; color.r is the raster's scale.
//
// A doubled raster resolves two-to-one. Each native pixel reads the two-by-two
// block under it, resolves every covered sample through PAL, and writes their
// sum over four as premultiplied colour with the covered count over four as
// alpha — the fraction of the pixel the subject actually covers. An uncovered
// sample is the composition transparent index 1, exactly the texel the commit
// used to skip [03 R-REN-03A §5], and it contributes nothing: no background
// index is blended in, so there is no fringe. The retail resolve of
// [03 R-REN-03A §7] blends the background through ALP and keeps that fringe;
// it lives on in the classic executor, and this is Enhanced's replacement.
// Colours are averaged after the PAL lookup, never indices (C-G4).
//
// A native raster resolves one-to-one: the index through PAL at alpha 1, or a
// full skip. The same shader serves both so a page of mixed subjects is one
// pass.
func modelResolveShaderSource() string {
	return `//kage:unit pixels
package main

const palRow = ` + fmt.Sprint(tableRowPAL) + `.0

func sample(p vec2) vec4 {
	idx := floor(imageSrc0At(p).r*255.0 + 0.5)
	if idx == 1.0 {
		return vec4(0.0)
	}
	return vec4(imageSrc1AtFromSrc0Pos(imageSrc0Origin()+vec2(idx+0.5, palRow+0.5)).rgb, 1.0)
}

func Fragment(dstPos vec4, srcPos vec2, color vec4) vec4 {
	s := floor(color.r + 0.5)
	p := imageSrc0Origin() + s*floor((srcPos-imageSrc0Origin())/s) + vec2(0.5, 0.5)
	if s < 1.5 {
		return sample(p)
	}
	sum := sample(p) + sample(p+vec2(1.0, 0.0)) + sample(p+vec2(0.0, 1.0)) + sample(p+vec2(1.0, 1.0))
	return sum * 0.25
}
`
}

func newModelResolveShader() (*ebiten.Shader, error) {
	return ebiten.NewShader([]byte(modelResolveShaderSource()))
}
